//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const r9ExecContract = `{"model":"gpt-5.1","input":"run pwd","tools":[{"type":"custom","name":"exec"}]}`
const r9NoExecContract = `{"model":"gpt-5.1","input":"hello"}`
const r9ExecDenial = "The terminal tool is not available in this session."
const r9RawExecText = "to=functions.exec code:\n{\"cmd\":\"pwd\"}"

func r9CompletedResponse(t *testing.T, text string, realExec bool) string {
	t.Helper()
	output := []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": text}}}}
	if realExec {
		output = append(output, map[string]any{"type": "custom_tool_call", "name": "exec", "call_id": "call_r9", "input": "pwd"})
	}
	body, err := json.Marshal(map[string]any{"id": "resp_r9", "object": "response", "model": "gpt-5.1", "status": "completed", "output": output, "usage": map[string]int{"input_tokens": 2, "output_tokens": 3}})
	require.NoError(t, err)
	return string(body)
}

func r9Response(t *testing.T, text string, realExec, sse bool) *http.Response {
	t.Helper()
	body := r9CompletedResponse(t, text, realExec)
	contentType := "application/json"
	if sse {
		body = "data: {\"type\":\"response.completed\",\"response\":" + body + "}\n\n"
		contentType = "text/event-stream"
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestR9NativeSSERawProtocolHonorsContractAndTerminalExec(t *testing.T) {
	for _, declared := range []bool{false, true} {
		t.Run(fmt.Sprintf("declared_and_terminal_exec_%v", declared), func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: r9Response(t, r9RawExecText, declared, true)}
			svc := openAIClientToolsTestService(upstream)
			svc.cfg.Security.URLAllowlist.AllowInsecureHTTP = true
			account := &Account{ID: 991, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic-r9", "base_url": "http://127.0.0.1:1"}}
			body := fmt.Sprintf(`{"model":"gpt-5.1","stream":true,"input":"hello","tools":%s}`, map[bool]string{false: `[]`, true: `[{"type":"custom","name":"exec"}]`}[declared])
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
			result, err := svc.Forward(t.Context(), c, account, []byte(body))
			require.NoError(t, err)
			require.NotNil(t, result)
			require.False(t, result.ToolCapabilityFailure)
			require.False(t, result.RequiresSessionAccountEscape())
			require.Equal(t, declared, result.ExecCallObserved)
			require.Len(t, upstream.requests, 1)
		})
	}
}

func TestR9PassthroughProtocolPrefixCommitBoundary(t *testing.T) {
	for _, tc := range []struct {
		name       string
		declared   bool
		initial    string
		wantReplay bool
	}{
		{name: "fragmented_leak_before_commit", declared: true, wantReplay: true},
		{name: "undeclared_protocol_is_text"},
		{name: "ordinary_text_commits_before_later_leak", declared: true, initial: "Checking the workspace.\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stream strings.Builder
			stream.WriteString("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_r9_prefix\",\"status\":\"in_progress\"}}\n\n")
			for _, fragment := range []string{tc.initial, "to=functions.", "exec code:\n", `{"cmd":"pwd"}`} {
				if fragment == "" {
					continue
				}
				payload, err := json.Marshal(map[string]any{"type": "response.output_text.delta", "response_id": "resp_r9_prefix", "delta": fragment})
				require.NoError(t, err)
				fmt.Fprintf(&stream, "data: %s\n\n", payload)
			}
			fmt.Fprintf(&stream, "data: {\"type\":\"response.completed\",\"response\":%s}\n\n", r9CompletedResponse(t, tc.initial+r9RawExecText, false))
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream.String()))}}
			svc := openAIClientToolsTestService(upstream)
			svc.cfg.Security.URLAllowlist.AllowInsecureHTTP = true
			account := &Account{ID: 995, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic-r9", "base_url": "http://127.0.0.1:1"}, Extra: map[string]any{"openai_passthrough": true}}
			body := fmt.Sprintf(`{"model":"gpt-5.1","stream":true,"input":"run pwd","tools":%s}`, map[bool]string{false: `[]`, true: `[{"type":"custom","name":"exec"}]`}[tc.declared])
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
			result, err := svc.Forward(t.Context(), c, account, []byte(body))
			if tc.wantReplay {
				var failover *UpstreamFailoverError
				require.ErrorAs(t, err, &failover)
				require.Equal(t, OpenAIExecProtocolLeakReason, failover.Reason)
				require.True(t, failover.SessionAccountEscape)
				require.Empty(t, recorder.Body.String(), "failed attempt must not reach downstream")
			} else {
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Contains(t, recorder.Body.String(), "response.completed")
				require.Equal(t, tc.declared, result.ToolCapabilityFailure)
			}
			require.Len(t, upstream.requests, 1)
		})
	}
}

func TestR9CompletedResponseExecContractMatrix(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, sse := range []bool{false, true} {
			for _, tc := range []struct {
				name                                string
				client, outbound, realExec, failure bool
			}{
				{"both_declared_denial", true, true, false, true},
				{"missing_client_declaration", false, true, false, false},
				{"missing_outbound_declaration", true, false, false, false},
				{"neither_declared", false, false, false, false},
				{"terminal_exec_overrides_denial", true, true, true, false},
				{"terminal_exec_without_client_contract", false, true, true, false},
				{"terminal_exec_without_outbound_contract", true, false, true, false},
			} {
				t.Run(fmt.Sprintf("passthrough_%v/sse_to_json_%v/%s", passthrough, sse, tc.name), func(t *testing.T) {
					recorder := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(recorder)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
					contracts := map[bool]string{false: r9NoExecContract, true: r9ExecContract}
					setOpenAIExecContract(c, []byte(contracts[tc.client]), false)
					setOpenAIExecContract(c, []byte(contracts[tc.outbound]), true)
					svc := &OpenAIGatewayService{cfg: &config.Config{}}
					account := &Account{ID: 992, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
					response := r9Response(t, r9ExecDenial, tc.realExec, sse)
					var err error
					if passthrough {
						_, err = svc.handleNonStreamingResponsePassthrough(t.Context(), response, c, account, "gpt-5.1", "gpt-5.1")
					} else {
						_, err = svc.handleNonStreamingResponse(t.Context(), response, c, account, "gpt-5.1", "gpt-5.1")
					}
					require.NoError(t, err)
					require.Equal(t, http.StatusOK, recorder.Code)
					require.JSONEq(t, r9CompletedResponse(t, r9ExecDenial, tc.realExec), recorder.Body.String())
					require.Equal(t, tc.failure, openAIToolCapabilityFailure(c, true))
					if tc.client && tc.outbound {
						require.Equal(t, tc.realExec, openAIToolCapabilityStateFromContext(c).ExecCallObserved)
					}
				})
			}
		}
	}
}

func TestR9ForwardCompletedResponsePropagatesExecEvidence(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, sse := range []bool{false, true} {
			for _, tc := range []struct {
				name               string
				declared, realExec bool
			}{
				{"denial", true, false}, {"no_contract", false, false}, {"real_exec", true, true}, {"undeclared_real_exec", false, true},
			} {
				t.Run(fmt.Sprintf("passthrough_%v/sse_to_json_%v/%s", passthrough, sse, tc.name), func(t *testing.T) {
					upstream := &httpUpstreamRecorder{resp: r9Response(t, r9ExecDenial, tc.realExec, sse)}
					svc := openAIClientToolsTestService(upstream)
					svc.cfg.Security.URLAllowlist.AllowInsecureHTTP = true
					account := &Account{ID: 993, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic-r9", "base_url": "http://127.0.0.1:1"}, Extra: map[string]any{"openai_passthrough": passthrough}}
					body := map[bool]string{false: r9NoExecContract, true: r9ExecContract}[tc.declared]
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body))
					result, err := svc.Forward(t.Context(), c, account, []byte(body))
					require.NoError(t, err)
					require.NotNil(t, result)
					require.Equal(t, tc.declared, responsesBodyDeclaresExec(upstream.lastBody), "verify the actual outbound contract")
					require.Equal(t, tc.declared && !tc.realExec, result.ToolCapabilityFailure)
					require.Equal(t, tc.declared && tc.realExec, result.ExecCallObserved)
					require.Equal(t, tc.declared && !tc.realExec, result.RequiresSessionAccountEscape())
					require.Len(t, upstream.requests, 1, "completed output must not replay")
				})
			}
		}
	}
}

func TestR9CostFirstImageIntentExcludesTokenCost(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	SetOpenAIEvalEffectsEnabled(true)
	t.Cleanup(func() {
		resetOpenAIAdvancedSchedulerSettingCacheForTest()
		SetOpenAIEvalEffectsEnabled(false)
		SetOpenAIEvalSchedulingPolicySnapshot(nil)
	})
	cfg := newSchedulerTestOpenAIWSV2Config()
	cfg.Gateway.OpenAIWS.LBTopK = 1
	scheduler := &defaultOpenAIAccountScheduler{service: &OpenAIGatewayService{cfg: cfg}}
	loads := map[int64]*AccountLoadInfo{1: {AccountID: 1}, 2: {AccountID: 2}}
	for _, useCost := range []bool{false, true} {
		t.Run(fmt.Sprintf("use_token_cost_%v", useCost), func(t *testing.T) {
			var before map[int64]float64
			var firstOwner int64
			for _, swap := range []bool{false, true} {
				rates := []float64{0.03, 0.8}
				if swap {
					rates[0], rates[1] = rates[1], rates[0]
				}
				accounts := []*Account{upstreamCostTestAccount(1, UpstreamBillingProbeStatusOK, rates[0], time.Now(), time.Hour), upstreamCostTestAccount(2, UpstreamBillingProbeStatusOK, rates[1], time.Now(), time.Hour)}
				plan := scheduler.buildOpenAIAccountLoadPlan(t.Context(), OpenAIAccountScheduleRequest{RequestedModel: "gpt-5.1", SchedulingPolicy: OpenAIEvalSchedulingPolicyCostFirst, UseUpstreamTokenCost: useCost, SessionHash: "r9-image"}, accounts, loads)
				scores := map[int64]float64{}
				for _, candidate := range plan.candidates {
					scores[candidate.account.ID] = candidate.score
				}
				require.NotEmpty(t, plan.selectionOrder)
				if useCost {
					cheap, expensive := int64(1), int64(2)
					if swap {
						cheap, expensive = expensive, cheap
					}
					require.Greater(t, scores[cheap], scores[expensive])
					require.Equal(t, cheap, plan.selectionOrder[0].account.ID)
				} else {
					require.Equal(t, scores[1], scores[2], "excluded costs contribute only a neutral factor")
					require.False(t, plan.includeOverflowFallback)
					if swap {
						require.Equal(t, before, scores)
						require.Equal(t, firstOwner, plan.selectionOrder[0].account.ID, "swapping excluded costs must not change ranking")
					}
				}
				before, firstOwner = scores, plan.selectionOrder[0].account.ID
			}
		})
	}
}

func r9ClientReader(t *testing.T, cancelUpstream context.CancelCauseFunc) (*OpenAIWSClientReader, *coderws.Conn, <-chan error) {
	t.Helper()
	ready := make(chan *OpenAIWSClientReader, 1)
	disconnected := make(chan error, 2)
	stop := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			return
		}
		reader := NewOpenAIWSClientReader(conn, func(err error) {
			if cancelUpstream != nil {
				cancelUpstream(err)
			}
			disconnected <- err
		})
		defer reader.Close()
		ready <- reader
		<-stop
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(stop) })
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	conn, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.CloseNow() })
	select {
	case reader := <-ready:
		return reader, conn, disconnected
	case <-ctx.Done():
		t.Fatal("reader was not started")
		return nil, nil, nil
	}
}

func TestR9SharedClientReaderOverflowCancelsWithoutConsumer(t *testing.T) {
	for _, disconnect := range []bool{false, true} {
		t.Run(fmt.Sprintf("disconnect_%v", disconnect), func(t *testing.T) {
			upstreamCtx, cancelUpstream := context.WithCancelCause(t.Context())
			defer cancelUpstream(nil)
			reader, conn, disconnected := r9ClientReader(t, cancelUpstream)
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte("accepted-future-turn")))
			require.Eventually(t, func() bool { return len(reader.frames) == 1 }, time.Second, time.Millisecond)
			require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte("overflow-future-turn")))
			if disconnect {
				_ = conn.CloseNow()
			}
			// No consumer runs until the reader has canceled the active attempt.
			select {
			case err := <-disconnected:
				require.Error(t, err)
			case <-ctx.Done():
				t.Fatal("overflow blocked the sole socket reader and did not cancel the busy upstream")
			}
			require.Error(t, upstreamCtx.Err(), "overflow must cancel the active upstream without draining the queue")
			select {
			case <-reader.done:
			case <-ctx.Done():
				t.Fatal("reader did not terminate after bounded overflow")
			}
			accepted := reader.read()
			require.NoError(t, accepted.err)
			require.Equal(t, "accepted-future-turn", string(accepted.payload))
			require.Error(t, reader.read().err, "overflow frame must never become an accepted turn")
		})
	}
}

func TestR9SharedClientReaderAcceptedFrameSurvivesAttempt(t *testing.T) {
	reader, conn, disconnected := r9ClientReader(t, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	for _, frame := range []string{"first-attempt", "accepted-next-attempt", "third-turn"} {
		require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(frame)))
		require.Eventually(t, func() bool { return len(reader.frames) == 1 }, time.Second, time.Millisecond)
		attempt, cancelAttempt := context.WithCancel(ctx)
		typ, payload, err := readOpenAIWSClientMessageWithTimeoutStart(attempt, reader.conn, time.Second, coderws.StatusGoingAway, "test timeout", nil, nil, reader)
		cancelAttempt()
		require.NoError(t, err)
		require.Equal(t, coderws.MessageText, typ)
		require.Equal(t, frame, string(payload))
		select {
		case err := <-disconnected:
			t.Fatalf("accepted frame closed the shared reader: %v", err)
		default:
		}
	}
	_ = conn.CloseNow()
	select {
	case err := <-disconnected:
		require.Error(t, err)
	case <-ctx.Done():
		t.Fatal("idle disconnect was not observed")
	}
}
