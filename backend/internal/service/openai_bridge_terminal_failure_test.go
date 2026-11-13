package service

import (
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBPSLocalBridgeFailureNeverFailsOver(t *testing.T) {
	payload := []byte(`{"type":"response.failed","response":{"status":"failed","error":{"code":"basispoints_protocol_error","failure_stage":"bridge_protocol_conversion","message":"tool conversion failed"}}}`)
	require.NotNil(t, parseOpenAILocalBridgeError(payload))
	require.False(t, openAIStreamFailedEventShouldFailover(payload, "tool conversion failed"))
	require.False(t, openAIStreamErrorEventShouldFailover(payload, "tool conversion failed"))
	require.Equal(t, http.StatusBadGateway, openAIStreamFailedEventSemanticStatus(payload, "tool conversion failed"))
}

func TestBPSProviderServerFailureStillFailsOver(t *testing.T) {
	payload := []byte(`{"type":"response.failed","response":{"status":"failed","error":{"code":"server_error","status_code":503,"message":"overloaded"}}}`)
	require.Nil(t, parseOpenAILocalBridgeError(payload))
	require.True(t, openAIStreamFailedEventShouldFailover(payload, "overloaded"))
}

func TestBPSBufferedTerminalFailurePreservesUsageAndStatus(t *testing.T) {
	cases := []struct {
		name, detail    string
		status          int
		local, failover bool
	}{
		{"local", `{"type":"server_error","code":"basispoints_protocol_error","failure_stage":"bridge_tool_validation","rejected_tool":"shell","repair_skipped_reason":"not_safe","message":"invalid tool"}`, 502, true, false},
		{"semantic400", `{"type":"invalid_request_error","code":"invalid_request","status":400,"message":"invalid request"}`, 400, false, false},
		{"semantic404", `{"type":"invalid_request_error","code":"model_not_found","status":404,"message":"invalid model"}`, 404, false, false},
		{"provider429", `{"type":"rate_limit_error","code":"rate_limit_exceeded","status":429,"message":"rate limited"}`, 429, false, true},
		{"provider503", `{"type":"server_error","code":"server_error","status":503,"message":"overloaded"}`, 503, false, true},
	}
	for _, tc := range cases {
		for _, passthrough := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/normal", true: "/passthrough"}[passthrough], func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
				svc := &OpenAIGatewayService{}
				wire := `data: {"type":"response.failed","response":{"id":"resp_failed","status":"failed","output":[],"usage":{"input_tokens":17,"output_tokens":3},"error":` + tc.detail + "}}\n\n"
				resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(wire))}
				account := excelBPSAccount(t)
				var err error
				var usage *OpenAIUsage
				var responseID string
				if passthrough {
					result, failure := svc.handleNonStreamingResponsePassthrough(c.Request.Context(), resp, c, account, "gpt-6-astra", "gpt-6-astra")
					require.NotNil(t, result)
					usage, responseID, err = result.usage, result.responseID, failure
				} else {
					result, failure := svc.handleNonStreamingResponse(c.Request.Context(), resp, c, account, "gpt-6-astra", "gpt-6-astra")
					require.NotNil(t, result)
					usage, responseID, err = result.usage, result.responseID, failure
				}
				require.Error(t, err)
				require.Equal(t, 17, usage.InputTokens)
				require.Equal(t, 3, usage.OutputTokens)
				require.Equal(t, "resp_failed", responseID)
				var local *OpenAILocalBridgeError
				require.Equal(t, tc.local, errors.As(err, &local))
				var failover *UpstreamFailoverError
				require.Equal(t, tc.failover, errors.As(err, &failover))
				if tc.failover {
					require.Empty(t, rec.Body.String())
					return
				}
				require.Equal(t, tc.status, rec.Code)
				require.Contains(t, rec.Header().Get("Content-Type"), "application/json")
				require.Equal(t, "failed", gjson.Get(rec.Body.String(), "status").String())
				require.Equal(t, int64(17), gjson.Get(rec.Body.String(), "usage.input_tokens").Int())
				if tc.local {
					require.Equal(t, "bridge_tool_validation", gjson.Get(rec.Body.String(), "error.failure_stage").String())
					require.Equal(t, "shell", local.Failure.RejectedTool)
				}
			})
		}
	}
}

func TestBPSStreamingLocalFailureIsTypedBeforeAndAfterOutput(t *testing.T) {
	for _, committed := range []bool{false, true} {
		for _, passthrough := range []bool{false, true} {
			t.Run(map[bool]string{false: "before", true: "after"}[committed]+map[bool]string{false: "/normal", true: "/passthrough"}[passthrough], func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
				svc := &OpenAIGatewayService{}
				wire := ""
				if committed {
					wire = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"
				}
				wire += `data: {"type":"response.failed","response":{"id":"resp_failed","status":"failed","output":[],"usage":{"input_tokens":17,"output_tokens":3},"error":{"type":"server_error","code":"basispoints_protocol_error","failure_stage":"bridge_protocol_conversion","message":"invalid tool"}}}` + "\n\n"
				resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(wire))}
				var err error
				var usage *OpenAIUsage
				if passthrough {
					result, failure := svc.handleStreamingResponsePassthrough(c.Request.Context(), resp, c, excelBPSAccount(t), time.Now(), "gpt-6-astra", "gpt-6-astra")
					require.NotNil(t, result)
					usage, err = result.usage, failure
				} else {
					result, failure := svc.handleStreamingResponse(c.Request.Context(), resp, c, excelBPSAccount(t), time.Now(), "gpt-6-astra", "gpt-6-astra")
					require.NotNil(t, result)
					usage, err = result.usage, failure
				}
				var local *OpenAILocalBridgeError
				require.ErrorAs(t, err, &local)
				var failover *UpstreamFailoverError
				require.False(t, errors.As(err, &failover))
				require.Equal(t, 17, usage.InputTokens)
				require.Contains(t, rec.Body.String(), "bridge_protocol_conversion")
			})
		}
	}
}

func TestBPSBridgeGeneratedFailureReachesServiceWithoutToolDispatch(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "stream"}[stream], func(t *testing.T) {
			_, bridge, err := basispoints.Prepare([]byte(`{"model":"gpt-6-astra","input":"hello","tools":[{"type":"function","name":"shell"}]}`), "", nil)
			require.NoError(t, err)
			wire := `data: {"type":"response.completed","response":{"id":"resp_bad_tool","status":"completed","model":"gpt-6-astra","output":[{"type":"function_call","name":"undeclared_tool","id":"fc_bad","call_id":"call_bad","arguments":"{}"}],"usage":{"input_tokens":19,"output_tokens":5}}}` + "\n\n"
			resp := excelBPSMockResponse(200, wire)
			resp.Body = bridge.Stream(resp.Body)
			defer resp.Body.Close()
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
			svc := &OpenAIGatewayService{}
			var usage *OpenAIUsage
			if stream {
				result, failure := svc.handleStreamingResponse(c.Request.Context(), resp, c, excelBPSAccount(t), time.Now(), "gpt-6-astra", "gpt-6-astra")
				require.NotNil(t, result)
				usage, err = result.usage, failure
			} else {
				result, failure := svc.handleNonStreamingResponse(c.Request.Context(), resp, c, excelBPSAccount(t), "gpt-6-astra", "gpt-6-astra")
				require.NotNil(t, result)
				usage, err = result.usage, failure
			}
			var local *OpenAILocalBridgeError
			require.ErrorAs(t, err, &local)
			require.Equal(t, 19, usage.InputTokens)
			require.Equal(t, 5, usage.OutputTokens)
			require.NotContains(t, rec.Body.String(), `"type":"function_call"`)
			require.Contains(t, rec.Body.String(), "basispoints_protocol_error")
		})
	}
}

func TestNativeBufferedFailureDoesNotExposeRawProviderDiagnostics(t *testing.T) {
	c, rec := newNonStreamingFailoverContext(t)
	svc := newNonStreamingFailoverService()
	payload := []byte(`{"type":"response.failed","response":{"id":"resp_native","status":"failed","usage":{"input_tokens":13,"output_tokens":2},"metadata":{"authorization":"private-provider-token"},"error":{"type":"invalid_request_error","status":400,"message":"request rejected","debug":{"api_key":"private-provider-key"}}}}`)
	result, err := svc.handleSSEToJSON(newNonStreamingSSEResponse(), c, newNonStreamingFailoverAccount(), sseTerminalBody("response.failed", string(payload)), "model", "model")
	require.Error(t, err)
	require.NotNil(t, result)
	require.Equal(t, 13, result.usage.InputTokens)
	require.Equal(t, 400, rec.Code)
	require.Contains(t, rec.Body.String(), "request rejected")
	require.NotContains(t, rec.Body.String(), "private-provider")
	require.NotContains(t, rec.Body.String(), "metadata")
	require.NotContains(t, rec.Body.String(), "debug")
}
