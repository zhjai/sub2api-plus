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
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type interruptionReadError struct{}

func (interruptionReadError) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestCodexPostcommitStreamInterruption(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, readError := range []bool{false, true} {
			for _, frameOpen := range []bool{false, true} {
				for _, kind := range []string{"text", "reasoning", "tool"} {
					t.Run(fmt.Sprintf("passthrough=%t/read_error=%t/open=%t/%s", passthrough, readError, frameOpen, kind), func(t *testing.T) {
						rec := httptest.NewRecorder()
						c, _ := gin.CreateTestContext(rec)
						c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
						c.Request.Header.Set("User-Agent", "codex_cli_rs/0.167.0")
						DeferOpenAIInterruptionTerminal(c)
						event := "response.output_text.delta"
						if kind == "reasoning" {
							event = "response.reasoning_summary_text.delta"
						} else if kind == "tool" {
							event = "response.function_call_arguments.delta"
						}
						frames := "data: {\"type\":\"response.created\",\"sequence_number\":0,\"response\":{\"id\":\"resp_interrupted\",\"created_at\":1794240000,\"status\":\"in_progress\"}}\n\n"
						frames += fmt.Sprintf("event: %s\ndata: {\"type\":%q,\"sequence_number\":1,\"delta\":\"partial\"}\n", event, event)
						if !frameOpen {
							frames += "\n"
						}
						var reader io.Reader = strings.NewReader(frames)
						if readError {
							reader = io.MultiReader(reader, interruptionReadError{})
						}
						resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(reader)}
						svc := &OpenAIGatewayService{cfg: &config.Config{}}
						account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
						outcome := &OpenAIForwardResult{}
						if passthrough {
							r, err := svc.handleStreamingResponsePassthrough(c.Request.Context(), resp, c, account, time.Now(), "gpt-6.1-sol", "gpt-6.1-sol")
							require.Error(t, err)
							r.outcome().applyForwardOutcome(outcome)
						} else {
							r, err := svc.handleStreamingResponse(c.Request.Context(), resp, c, account, time.Now(), "gpt-6.1-sol", "gpt-6.1-sol")
							require.Error(t, err)
							r.applyForwardOutcome(outcome)
						}
						require.True(t, outcome.RequiresSessionAccountEscape())
						require.False(t, OpenAIResponsesRequiresSessionEscape(c, outcome), "Codex transport retry must not create negative affinity")
						require.False(t, outcome.SucceededForScheduling())
						require.NotContains(t, rec.Body.String(), "response.failed", "client must wait for handler failure accounting")
						FlushOpenAIInterruptionTerminal(c)
						body := rec.Body.String()
						require.Equal(t, 1, strings.Count(body, "event: response.failed\n"))
						require.NotContains(t, body, "response.incomplete")
						terminals := 0
						forEachOpenAISSEFrame(body, func(eventType string, data []byte) {
							require.True(t, json.Valid(data), "every SSE frame must contain one valid JSON document: %s", data)
							if eventType == "response.failed" {
								terminals++
								require.Equal(t, "server_error", gjson.GetBytes(data, "response.error.type").String())
								require.Equal(t, "server_error", gjson.GetBytes(data, "response.error.code").String())
								require.Equal(t, "resp_interrupted", gjson.GetBytes(data, "response.id").String())
								require.Equal(t, int64(2), gjson.GetBytes(data, "sequence_number").Int())
								require.Equal(t, int64(1794240000), gjson.GetBytes(data, "response.created_at").Int())
							}
						})
						require.Equal(t, 1, terminals)
						FlushOpenAIInterruptionTerminal(c)
						require.Equal(t, body, rec.Body.String(), "terminal flush must be idempotent")
					})
				}
			}
		}
	}
}

func TestCodexPostcommitStreamInterruptionWithoutResponseID(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprintf("passthrough=%t", passthrough), func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			c.Request.Header.Set("User-Agent", "codex_cli_rs/0.167.0")
			DeferOpenAIInterruptionTerminal(c)
			frames := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"
			resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(frames))}
			svc := &OpenAIGatewayService{cfg: &config.Config{}}
			account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
			outcome := &OpenAIForwardResult{}
			if passthrough {
				r, err := svc.handleStreamingResponsePassthrough(c.Request.Context(), resp, c, account, time.Now(), "gpt-6.1-sol", "gpt-6.1-sol")
				require.Error(t, err)
				r.outcome().applyForwardOutcome(outcome)
			} else {
				r, err := svc.handleStreamingResponse(c.Request.Context(), resp, c, account, time.Now(), "gpt-6.1-sol", "gpt-6.1-sol")
				require.Error(t, err)
				r.applyForwardOutcome(outcome)
			}
			require.Equal(t, "premature_eof", outcome.ResponsesProtocolStatus)
			require.False(t, outcome.SucceededForScheduling())
			require.False(t, OpenAIResponsesRequiresSessionEscape(c, outcome))
			require.NotContains(t, rec.Body.String(), "response.failed")
			FlushOpenAIInterruptionTerminal(c)
			terminals := 0
			forEachOpenAISSEFrame(rec.Body.String(), func(event string, data []byte) {
				require.True(t, json.Valid(data))
				if event == "response.failed" {
					terminals++
					require.NotEmpty(t, gjson.GetBytes(data, "response.id").String())
					require.Equal(t, "server_error", gjson.GetBytes(data, "response.error.code").String())
				}
			})
			require.Equal(t, 1, terminals)
		})
	}
}

func TestOpenAIResponsesEscapePolicyPreservesOtherFailures(t *testing.T) {
	for _, tc := range []struct {
		name         string
		codex        bool
		deferred     bool
		status       string
		toolFailure  bool
		protocolLeak bool
		want         bool
	}{
		{name: "codex synthesized interruption", codex: true, deferred: true, status: "premature_eof"},
		{name: "explicit upstream incomplete", codex: true, deferred: true, status: "incomplete", want: true},
		{name: "non Codex interruption", deferred: true, status: "premature_eof", want: true},
		{name: "no synthesized terminal", codex: true, status: "premature_eof", want: true},
		{name: "tool failure", codex: true, deferred: true, status: "premature_eof", toolFailure: true, want: true},
		{name: "exec leak", codex: true, deferred: true, status: "premature_eof", protocolLeak: true, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			if tc.codex {
				c.Request.Header.Set("User-Agent", "codex_cli_rs/0.167.0")
			}
			if tc.deferred {
				DeferOpenAIInterruptionTerminal(c)
				deferOpenAIInterruptionTerminal(c, "event: response.failed\ndata: {}\n\n")
			}
			outcome := &OpenAIForwardResult{ResponsesOutcomeObserved: true, ResponsesMeaningfulOutput: true, ResponsesProtocolStatus: tc.status, ResponsesIncompleteReason: "stream_terminated", ToolCapabilityFailure: tc.toolFailure, PrecommitExecProtocolLeak: tc.protocolLeak}
			require.Equal(t, tc.want, OpenAIResponsesRequiresSessionEscape(c, outcome))
		})
	}
	require.False(t, OpenAIResponsesRequiresSessionEscape(nil, nil))
}

func TestCodexInterruptionTerminalRespectsCancellation(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	ctx, cancel := context.WithCancel(context.Background())
	c.Request = httptest.NewRequest(http.MethodPost, "/responses", nil).WithContext(ctx)
	c.Request.Header.Set("Originator", "codex-tui")
	DeferOpenAIInterruptionTerminal(c)
	require.True(t, deferOpenAIInterruptionTerminal(c, buildOpenAIStreamInterruptionSSE(c, "resp_cancelled", "model", 1, 2)))
	cancel()
	FlushOpenAIInterruptionTerminal(c)
	require.Empty(t, rec.Body.String())
}

func TestCodexPostcommitNativePartialBilling(t *testing.T) {
	body := []byte(`{"model":"gpt-5.1","stream":true,"service_tier":"priority","input":"hello"}`)
	c := newOpenAIRejectedFieldTestContext(body)
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.167.0")
	frames := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_billing\",\"model\":\"gpt-5.1\",\"service_tier\":\"priority\",\"status\":\"in_progress\"}}\n\n"
	frames += "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(frames))}
	upstream := &httpUpstreamRecorder{resp: resp}
	account := newOpenAIRejectedFieldTestAccount()
	account.Extra["openai_passthrough"] = false
	result, err := newOpenAIRejectedFieldTestService(upstream).Forward(c.Request.Context(), c, account, body)
	require.Error(t, err)
	require.NotNil(t, result)
	require.True(t, result.RequiresSessionAccountEscape())
	require.NotNil(t, result.ServiceTier)
	require.Equal(t, "priority", *result.ServiceTier)
	require.Empty(t, result.UpstreamResponseServiceTier, "created event only echoes the request tier; no terminal confirmed the processing tier")
	require.Equal(t, "gpt-5.1", result.UpstreamResponseModel)
	require.False(t, result.UpstreamResponseModelConflict)
	require.Equal(t, "gpt-5.1", result.BillingModel)
}

func TestOpenAINonCodexPassthroughInterruptionUnchanged(t *testing.T) {
	for _, readError := range []bool{false, true} {
		t.Run(fmt.Sprintf("read_error=%t", readError), func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			c.Request.Header.Set("User-Agent", "curl/8.0")
			frames := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_generic\",\"status\":\"in_progress\"}}\n\n"
			frames += "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n"
			var reader io.Reader = strings.NewReader(frames)
			if readError {
				reader = io.MultiReader(reader, interruptionReadError{})
			}
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(reader)}
			svc := &OpenAIGatewayService{cfg: &config.Config{}}
			_, err := svc.handleStreamingResponsePassthrough(c.Request.Context(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, time.Now(), "gpt-5.1", "gpt-5.1")
			require.Error(t, err)
			require.NotContains(t, rec.Body.String(), "response.failed")
			if readError {
				require.NotContains(t, rec.Body.String(), "response.incomplete")
			} else {
				require.Contains(t, rec.Body.String(), "response.incomplete")
			}
		})
	}
}
