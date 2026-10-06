package service

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIReplayIDPoolModeDoesNotFailover(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		mode := "native"
		if passthrough {
			mode = "passthrough"
		}
		t.Run(mode, func(t *testing.T) {
			for _, testcase := range []struct {
				name      string
				param     string
				errorType string
				code      string
				wantCalls int
			}{
				{name: "indexed vapi", param: "input[0].id", errorType: "v_api_biz_error", code: "invalid_request", wantCalls: 2},
				{name: "indexed invalid value", param: "input[0].id", errorType: "invalid_request_error", code: "invalid_value", wantCalls: 2},
				{name: "ambiguous vapi", param: "input*****.id", errorType: "v_api_biz_error", code: "invalid_request", wantCalls: 1},
				{name: "ambiguous invalid value", param: "input*****.id", errorType: "invalid_request_error", code: "invalid_value", wantCalls: 1},
			} {
				t.Run(testcase.name, func(t *testing.T) {
					body := []byte(`{"model":"gpt-6-astra","stream":false,"input":[{"type":"message","id":"msg_old","role":"assistant","content":"hello"}]}`)
					errorBody := `{"error":{"type":"` + testcase.errorType + `","code":"` + testcase.code + `","param":"` + testcase.param + `","message":"Rejected replay item"}}`
					upstream := &httpUpstreamRecorder{}
					for attempt := 0; attempt < testcase.wantCalls; attempt++ {
						upstream.responses = append(upstream.responses, newOpenAIRejectedFieldTestResponse(http.StatusBadRequest, errorBody))
					}
					account := newOpenAIRejectedFieldTestAccount()
					if account.Credentials == nil {
						account.Credentials = map[string]any{}
					}
					account.Credentials["pool_mode"] = true
					account.Credentials["pool_mode_retry_status_codes"] = []any{float64(http.StatusBadRequest)}
					require.True(t, account.IsPoolMode())
					require.True(t, account.IsPoolModeRetryableStatus(http.StatusBadRequest))
					require.False(t, shouldFailoverOpenAIPassthroughResponse(account, http.StatusBadRequest, []byte(errorBody)))
					require.True(t, shouldFailoverOpenAIPassthroughResponse(account, http.StatusBadRequest, []byte(`{"error":{"type":"invalid_request_error","code":"invalid_value","param":"model"}}`)))
					recorder := httptest.NewRecorder()
					client, _ := gin.CreateTestContext(recorder)
					client.Request = newOpenAIRejectedFieldTestContext(body).Request
					service := newOpenAIRejectedFieldTestService(upstream)
					var err error
					if passthrough {
						_, err = service.forwardOpenAIPassthrough(t.Context(), client, account, body, body, "gpt-6-astra", false, nil, false, time.Now())
					} else {
						_, err = service.Forward(t.Context(), client, account, body)
					}
					require.Error(t, err)
					require.Len(t, upstream.bodies, testcase.wantCalls)
					var failoverError *UpstreamFailoverError
					require.False(t, errors.As(err, &failoverError))
					require.Equal(t, http.StatusBadRequest, client.Writer.Status())
					require.Equal(t, testcase.param, gjson.Get(recorder.Body.String(), "error.param").String())
					require.Equal(t, testcase.code, gjson.Get(recorder.Body.String(), "error.code").String())
				})
			}
		})
	}
}
