//go:build unit

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	openaiwsv2 "github.com/Wei-Shaw/sub2api/internal/service/openai_ws_v2"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const r12TerminalText = `[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"partial answer"}]}]`
const r12TerminalTool = `[{"type":"custom_tool_call","name":"exec","input":"pwd"}]`

func r12Terminal(event, status, reason, output string) []byte {
	return []byte(fmt.Sprintf(`{"type":%q,"response":{"id":"resp_r12","model":"gpt-5.1","status":%q,"incomplete_details":{"reason":%q},"output":%s,"usage":{"input_tokens":7,"output_tokens":3}}}`, event, status, reason, output))
}

func TestR12NonStreamingObserverProductionPaths(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, sse := range []bool{false, true} {
			for _, tc := range []struct {
				status, reason, protocol string
				success                  bool
			}{
				{"terminated", "", "failed", false},
				{"incomplete", "max_output_tokens", "incomplete", true},
			} {
				t.Run(fmt.Sprintf("passthrough=%v/sse=%v/%s", passthrough, sse, tc.status), func(t *testing.T) {
					recorder := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(recorder)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
					c.Set(openAIToolCapabilityContextKey, openAIToolCapabilityState{ClientExecDeclared: true, OutboundExecDeclared: true})
					payload := r12Terminal("response.done", tc.status, tc.reason, r12TerminalText)
					body := gjson.GetBytes(payload, "response").Raw
					contentType := "application/json"
					if sse {
						body = "event: response.done\ndata: " + string(payload) + "\n\n"
						contentType = "text/event-stream"
					}
					resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body))}
					svc := &OpenAIGatewayService{cfg: &config.Config{}}
					account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
					var outcome *openaiStreamingResult
					if passthrough {
						result, err := svc.handleNonStreamingResponsePassthrough(c.Request.Context(), resp, c, account, "gpt-5.1", "gpt-5.1")
						require.NoError(t, err)
						require.Equal(t, 7, result.usage.InputTokens)
						outcome = result.outcome
					} else {
						result, err := svc.handleNonStreamingResponse(c.Request.Context(), resp, c, account, "gpt-5.1", "gpt-5.1")
						require.NoError(t, err)
						require.Equal(t, 7, result.usage.InputTokens)
						outcome = result.outcome
					}
					require.NotNil(t, outcome, "root terminal must reach the observer, including SSE extraction")
					forward := &OpenAIForwardResult{}
					outcome.applyForwardOutcome(forward)
					require.True(t, forward.ResponsesOutcomeObserved)
					require.Equal(t, tc.protocol, forward.ResponsesProtocolStatus)
					require.Equal(t, tc.status, forward.ResponsesStatus)
					require.Equal(t, tc.success, forward.SucceededForScheduling())
					require.False(t, forward.ToolCapabilityFailure)
					require.False(t, forward.RequiresSessionAccountEscape())
					require.Equal(t, tc.status, gjson.Get(recorder.Body.String(), "status").String())
					require.NotContains(t, recorder.Body.String(), "data:")
				})
			}
		}
	}
}

func TestR12VisibleTerminalOutput(t *testing.T) {
	for _, event := range []string{"response.completed", "response.done", "response.incomplete", "response.failed", "response.cancelled", "response.canceled", "error"} {
		for _, output := range []string{r12TerminalText, r12TerminalTool, "[]"} {
			t.Run(event+"/"+output, func(t *testing.T) {
				payload := r12Terminal(event, "incomplete", "stream_terminated", output)
				require.Equal(t, output != "[]", openAIStreamDataStartsVisibleOutput(string(payload), event))
				evidence := newOpenAIWSIntegrityEvidence(nil, nil)
				evidence.observe(event, payload)
				result := &OpenAIForwardResult{}
				evidence.apply(result, false, false)
				require.False(t, result.ResponsesMeaningfulOutput, "observation alone is not delivery")
				evidence.delivered(event, payload)
				evidence.apply(result, false, false)
				require.Equal(t, output != "[]", result.ResponsesMeaningfulOutput)
				require.Equal(t, output == r12TerminalTool, result.ResponsesToolCallForwarded)
			})
		}
	}
}

func TestR12HTTPStreamingTerminalDelivery(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, writeFailure := range []bool{false, true} {
			for _, tc := range []struct{ event, status, reason string }{
				{"response.incomplete", "incomplete", "stream_terminated"},
				{"response.done", "incomplete", "stream_terminated"},
				{"response.incomplete", "incomplete", "max_output_tokens"},
				{"response.cancelled", "cancelled", ""},
				{"response.failed", "failed", ""},
			} {
				t.Run(fmt.Sprintf("passthrough=%v/writeFailure=%v/%s/%s", passthrough, writeFailure, tc.event, tc.reason), func(t *testing.T) {
					recorder := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(recorder)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
					failAfter := -1
					if writeFailure {
						failAfter = 0
					}
					c.Writer = &passthroughFlushTestWriter{ResponseWriter: c.Writer, recorder: recorder, failAfterWrites: failAfter}
					payload := r12Terminal(tc.event, tc.status, tc.reason, r12TerminalText)
					if tc.event == "response.failed" {
						payload = []byte(strings.Replace(string(payload), `"status":"failed"`, `"status":"failed","error":{"code":"content_policy_violation","message":"content policy"}`, 1))
					}
					body := "event: " + tc.event + "\ndata: " + string(payload) + "\n\n"
					resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
					svc := &OpenAIGatewayService{cfg: &config.Config{}}
					account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
					var outcome *openaiStreamingResult
					var usage *OpenAIUsage
					if passthrough {
						result, _ := svc.handleStreamingResponsePassthrough(c.Request.Context(), resp, c, account, time.Now(), "gpt-5.1", "gpt-5.1")
						require.NotNil(t, result)
						outcome, usage = result.outcome(), result.usage
					} else {
						result, _ := svc.handleStreamingResponse(c.Request.Context(), resp, c, account, time.Now(), "gpt-5.1", "gpt-5.1")
						require.NotNil(t, result)
						outcome, usage = result, result.usage
					}
					forward := &OpenAIForwardResult{}
					outcome.applyForwardOutcome(forward)
					require.Equal(t, 7, usage.InputTokens, "delivery failure must preserve upstream usage")
					require.Equal(t, 3, usage.OutputTokens)
					require.Equal(t, writeFailure, forward.ClientDisconnect)
					visible := !writeFailure && tc.event != "response.failed"
					require.Equal(t, visible, forward.ResponsesMeaningfulOutput)
					require.False(t, forward.ToolCapabilityFailure)
					require.Equal(t, !writeFailure && tc.reason == "stream_terminated", forward.RequiresSessionAccountEscape())
					if writeFailure {
						require.Empty(t, recorder.Body.String())
					} else if visible {
						require.Contains(t, recorder.Body.String(), "partial answer")
					} else {
						require.NotContains(t, recorder.Body.String(), "partial answer", "failed output is stripped by the existing sanitizer")
					}
					if tc.reason == "max_output_tokens" {
						require.True(t, forward.SucceededForScheduling())
					}
				})
			}
		}
	}
}

func TestR12HTTPFailedTerminalKeepsPreOutputFailover(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprint(passthrough), func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			payload := fmt.Sprintf(`{"type":"response.failed","response":{"status":"failed","output":%s,"error":{"code":"server_error","message":"upstream overloaded"}}}`, r12TerminalText)
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: " + payload + "\n\n"))}
			svc := &OpenAIGatewayService{cfg: &config.Config{}}
			account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
			var err error
			if passthrough {
				_, err = svc.handleStreamingResponsePassthrough(c.Request.Context(), resp, c, account, time.Now(), "gpt-5.1", "gpt-5.1")
			} else {
				_, err = svc.handleStreamingResponse(c.Request.Context(), resp, c, account, time.Now(), "gpt-5.1", "gpt-5.1")
			}
			var failover *UpstreamFailoverError
			require.ErrorAs(t, err, &failover)
			require.Empty(t, recorder.Body.String())
		})
	}
}

func TestR12WSTurnSettlementDeliveryAndAccounting(t *testing.T) {
	for _, output := range []string{r12TerminalText, r12TerminalTool} {
		for _, mode := range []string{"delivered", "write_error", "dropped", "cancelled", "cancelled_terminal", "bare_error", "bare_error_dropped"} {
			t.Run(mode+"/"+output, func(t *testing.T) {
				event, status := "response.incomplete", "incomplete"
				if strings.HasPrefix(mode, "bare_error") {
					event, status = "error", "failed"
				} else if mode == "cancelled_terminal" {
					event, status = "response.canceled", "cancelled"
				}
				payload := r12Terminal(event, status, "stream_terminated", output)
				evidence := newOpenAIWSIntegrityEvidence(nil, nil)
				evidence.observe(event, payload)
				completion := &openAIWSPassthroughTurnCompletion{turn: 1, terminalEvent: event, evidence: evidence, result: &OpenAIForwardResult{
					Model: "gpt-5.1", UpstreamTerminalEvent: normalizeOpenAIWSTerminalEvent(event), OpenAIWSMode: true, Usage: OpenAIUsage{InputTokens: 7, OutputTokens: 3},
				}}
				if mode == "delivered" || mode == "cancelled" || mode == "bare_error" || mode == "cancelled_terminal" {
					evidence.delivered(event, payload)
				}
				calls := 0
				after := func(turn int, result *OpenAIForwardResult, err error) {
					calls++
					require.Equal(t, 1, turn)
					require.NoError(t, err, "nil callback error preserves billing despite delivery failure")
					require.Equal(t, 7, result.Usage.InputTokens)
					require.Equal(t, 3, result.Usage.OutputTokens)
					require.Equal(t, mode == "delivered", result.RequiresSessionAccountEscape())
					require.Equal(t, mode != "delivered" && mode != "bare_error" && mode != "cancelled_terminal", result.ClientDisconnect)
				}
				completion.settle(mode == "cancelled", after)
				completion.settle(true, after)
				require.Equal(t, 1, calls)
			})
		}
	}
}

func TestR12WSTerminalSettlementPrecedesNextTurnAdmission(t *testing.T) {
	lifecycle := newOpenAIWSPassthroughTurnLifecycle(true)
	first := []byte(`{"type":"response.create","model":"gpt-5.1","reasoning":{"effort":"high"}}`)
	meta := newOpenAIWSPassthroughUsageMeta("gpt-5.1", first)
	meta.initFromFirstFrame(first, "gpt-5.1")
	meta.captureRequestedReasoningEffort(first)
	evidence := newOpenAIWSIntegrityEvidence(nil, nil)
	payload := r12Terminal("response.incomplete", "incomplete", "stream_terminated", r12TerminalText)
	evidence.observe("response.incomplete", payload)
	model, _ := meta.turnModels("")
	completion := &openAIWSPassthroughTurnCompletion{turn: 1, terminalEvent: "response.incomplete", evidence: evidence, result: &OpenAIForwardResult{
		Model: model, RequestedReasoningEffort: meta.requestedReasoningEffort.Load(), UpstreamTerminalEvent: "response.incomplete",
	}}
	lifecycle.beginTerminalWrite()
	admitted := make(chan bool, 1)
	go func() {
		admitted <- lifecycle.beginResponseCreate(func() {
			next := []byte(`{"type":"response.create","model":"gpt-5.2","reasoning":{"effort":"low"}}`)
			meta.updateFromResponseCreate(next, "gpt-5.2", "gpt-5.2")
			meta.captureRequestedReasoningEffort(next)
		})
	}()
	evidence.delivered("response.incomplete", payload)
	completion.settle(false, func(_ int, result *OpenAIForwardResult, _ error) {
		select {
		case <-admitted:
			t.Fatal("next turn was admitted before settlement")
		default:
		}
		require.Equal(t, "gpt-5.1", result.Model)
		require.Equal(t, "high", *result.RequestedReasoningEffort)
		require.True(t, result.RequiresSessionAccountEscape())
	})
	lifecycle.finishTerminalWrite(true, nil)
	select {
	case ok := <-admitted:
		require.True(t, ok)
	case <-time.After(time.Second):
		t.Fatal("next turn blocked after settlement")
	}
	require.Equal(t, "gpt-5.1", completion.result.Model)
	require.Equal(t, "high", *completion.result.RequestedReasoningEffort)
	require.Equal(t, "low", *meta.requestedReasoningEffort.Load())
}

func TestR12WSBinaryFlushesTextInOrderAndPreventsReplay(t *testing.T) {
	contract := []byte(`{"tools":[{"type":"custom","name":"exec"}]}`)
	evidence := newOpenAIWSIntegrityEvidence(contract, contract)
	pending := []string{`{"type":"response.created","response":{"id":"resp_binary"}}`, `{"type":"response.output_text.delta","delta":"to=functions."}`}
	for _, payload := range pending {
		event, _, _ := parseOpenAIWSEventEnvelope([]byte(payload))
		evidence.observe(event, []byte(payload))
		frames, err := prepareOpenAIWSPassthroughIntegrityFrames(evidence, coderws.MessageText, []byte(payload))
		require.NoError(t, err)
		require.Empty(t, frames)
	}
	for _, typ := range []coderws.MessageType{coderws.MessageText, coderws.MessageBinary} {
		frames, err := prepareOpenAIWSPassthroughIntegrityFrames(evidence, typ, []byte(`{"type":"keepalive"}`))
		require.NoError(t, err)
		require.Len(t, frames, 1)
		evidence.delivered("keepalive", frames[0].Payload)
		require.False(t, evidence.committed)
		require.Len(t, evidence.pendingFrames, 2)
	}
	frames, err := prepareOpenAIWSPassthroughIntegrityFrames(evidence, coderws.MessageBinary, []byte{0, 1, 2})
	require.NoError(t, err)
	require.Len(t, frames, 3)
	for i, payload := range pending {
		require.Equal(t, coderws.MessageText, frames[i].MessageType)
		require.Equal(t, payload, string(frames[i].Payload))
	}
	require.Equal(t, coderws.MessageBinary, frames[2].MessageType)
	require.Equal(t, []byte{0, 1, 2}, frames[2].Payload)
	require.Empty(t, evidence.pendingFrames)
	require.Zero(t, evidence.pendingBytes)
	// Binary alone must commit, independently of the preceding text writes.
	evidence.delivered("", frames[2].Payload)
	require.True(t, evidence.committed)
	leak := []byte(`{"type":"response.output_text.delta","delta":"exec code:\n{\"cmd\":\"pwd\"}"}`)
	evidence.observe("response.output_text.delta", leak)
	frames, err = prepareOpenAIWSPassthroughIntegrityFrames(evidence, coderws.MessageText, leak)
	require.NoError(t, err, "delivered output cannot expose a replayable failure")
	require.Len(t, frames, 1)
	result := &OpenAIForwardResult{}
	evidence.apply(result, false, false)
	require.False(t, result.PrecommitExecProtocolLeak)
}

func TestR12ObserverIgnoresUnrecognizedRootStatus(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(context.Background())
	require.Nil(t, observeOpenAINonStreamingOutcome(c, []byte(`{"status":"in_progress"}`)))
}

func TestR12WSBinaryRelayCommitBoundary(t *testing.T) {
	for _, keepalive := range []bool{false, true} {
		t.Run(fmt.Sprint(keepalive), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			client, upstream := newStagedPassthroughConn(), newStagedPassthroughConn()
			defer client.Close()
			payload := []byte(`{"type":"response.output_text.delta","delta":"binary output"}`)
			if keepalive {
				payload = []byte(`{"type":"keepalive"}`)
			}
			upstream.frames <- stagedPassthroughFrame{messageType: coderws.MessageBinary, payload: payload}
			upstream.Fail(io.EOF)
			_, exit := openaiwsv2.Relay(ctx, client, upstream, []byte(`{"type":"response.create","model":"gpt-5.1"}`), openaiwsv2.RelayOptions{
				StartClientAfterFirstDownstream: true,
			})
			require.NotNil(t, exit)
			require.Equal(t, !keepalive, exit.WroteDownstream)
			require.Equal(t, payload, <-client.writes)
		})
	}
}
