package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIVAPIRejectedReplayIDs(t *testing.T) {
	body := []byte(`{"model":"gpt-6-astra","input":[{"type":"message","id":"msg_old","role":"assistant","content":"hello"},{"type":"function_call","id":"fc_old","call_id":"call_1","name":"exec","arguments":"{}"},{"type":"function_call_output","id":"out_old","call_id":"call_1","output":"done"}]}`)
	for _, param := range []string{"input[1].id", "input.1.id"} {
		t.Run(param, func(t *testing.T) {
			errBody := []byte(`{"error":{"type":"v_api_biz_error","code":"invalid_request","param":"` + param + `","message":"The request could not be processed. Please check the request parameters."}}`)
			next, reason, changed, err := normalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, body, errBody)
			require.NoError(t, err)
			require.True(t, changed)
			require.Equal(t, "replayed input item ID rejection", reason)
			require.False(t, gjson.GetBytes(next, "input.1.id").Exists())
			require.Equal(t, "call_1", gjson.GetBytes(next, "input.1.call_id").String())
			require.Equal(t, "call_1", gjson.GetBytes(next, "input.2.call_id").String())
			require.Equal(t, "done", gjson.GetBytes(next, "input.2.output").String())
			_, _, changed, err = normalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, next, errBody)
			require.NoError(t, err)
			require.False(t, changed)
		})
	}
}

func TestOpenAIInvalidValueReplayIDAtLargeIndex(t *testing.T) {
	items := make([]map[string]any, 456)
	for i := range items {
		items[i] = map[string]any{"type": "message", "role": "assistant", "id": "msg_keep", "content": "history"}
	}
	items[455] = map[string]any{"type": "custom_tool_call", "id": "ctc_q9tCgEnSzhWt1Hu_", "call_id": "call_keep", "name": "exec", "input": "pwd"}
	body, err := json.Marshal(map[string]any{"model": "gpt-6-astra", "input": items})
	require.NoError(t, err)
	errorBody := []byte(`{"error":{"code":"invalid_value","message":"Invalid 'input[455].id': 'ctc_q9tCgEnSzhWt1Hu_'. Expected an ID that contains letters, numbers, underscores, or dashes, but this value contained additional characters.","param":"input[455].id","type":"invalid_request_error"}}`)
	next, reason, changed, err := normalizeOpenAIResponsesRejectedFieldRetryBody(400, body, errorBody)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, openAIReplayIDRejectionReason, reason)
	require.False(t, gjson.GetBytes(next, "input.455.id").Exists())
	require.Equal(t, "call_keep", gjson.GetBytes(next, "input.455.call_id").String())
	require.Equal(t, "pwd", gjson.GetBytes(next, "input.455.input").String())
	for i := 0; i < 455; i++ {
		require.Equal(t, "msg_keep", gjson.GetBytes(next, "input").Array()[i].Get("id").String())
	}
}

func TestOpenAIInvalidValueReplayIDFailsClosed(t *testing.T) {
	body := []byte(`{"input":[{"type":"custom_tool_call","id":"ctc_old","call_id":"call_keep","name":"exec","input":"pwd"}]}`)
	for _, response := range []string{
		`{"error":{"type":"invalid_request_error","code":"invalid_value","param":"input*****.id"}}`,
		`{"error":{"type":"invalid_request_error","code":"invalid_value","param":"input[0].call_id"}}`,
		`{"error":{"type":"invalid_request_error","code":"invalid_value","param":"input[455].id"}}`,
		`{"error":{"type":"invalid_request_error","code":"invalid_request","param":"input[0].id"}}`,
		`{"error":{"type":"server_error","code":"invalid_value","param":"input[0].id"}}`,
	} {
		_, _, changed, err := normalizeOpenAIResponsesRejectedFieldRetryBody(400, body, []byte(response))
		require.NoError(t, err)
		require.False(t, changed)
	}
	errorBody := []byte(`{"error":{"type":"invalid_request_error","code":"invalid_value","param":"input[0].id"}}`)
	for _, unsafeBody := range []string{
		`{"previous_response_id":"resp_owner","input":[{"type":"custom_tool_call","id":"ctc_old"}]}`,
		`{"input":[{"type":"custom_tool_call","id":"ctc_old"},{"type":"item_reference","id":"ctc_old"}]}`,
		`{"input":[{"type":"custom_tool_call","id":"ctc_old"},{"type":"reasoning","encrypted_content":"opaque"}]}`,
	} {
		_, _, changed, err := normalizeOpenAIResponsesRejectedFieldRetryBody(400, []byte(unsafeBody), errorBody)
		require.NoError(t, err)
		require.False(t, changed)
	}
	_, _, changed, err := normalizeOpenAIResponsesRejectedFieldRetryBody(503, body, errorBody)
	require.NoError(t, err)
	require.False(t, changed)
}

func TestOpenAIVAPIRejectedReplayIDsWildcardFailsClosed(t *testing.T) {
	body := []byte(`{"model":"gpt-6-astra","input":[{"type":"message","id":"msg_old","role":"assistant","content":"hello"},{"type":"function_call","id":"fc_old","call_id":"call_1","name":"exec","arguments":"{}"}]}`)
	errBody := []byte(`{"error":{"type":"v_api_biz_error","code":"invalid_request","param":"input*****.id","message":"The request could not be processed. Please check the request parameters."}}`)

	next, reason, changed, err := normalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, body, errBody)
	require.NoError(t, err)
	require.False(t, changed)
	require.Empty(t, reason)
	require.Nil(t, next)
	require.Equal(t, "msg_old", gjson.GetBytes(body, "input.0.id").String())
	require.Equal(t, "fc_old", gjson.GetBytes(body, "input.1.id").String())
}

func TestOpenAIVAPIRejectedReplayIDsWildcardPreservesUpstreamError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"input":[{"type":"message","id":"msg_old"}]}`))
	respBody := `{"error":{"type":"v_api_biz_error","code":"invalid_request","param":"input*****.id","message":"The request could not be processed. Please check the request parameters. (request id: req_test)"}}`
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(respBody)),
	}

	_, err := (&OpenAIGatewayService{}).handleErrorResponse(
		context.Background(), resp, c, &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
		[]byte(`{"input":[{"type":"message","id":"msg_old"}]}`),
		"gpt-6-astra",
	)
	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Equal(t, "v_api_biz_error", gjson.Get(recorder.Body.String(), "error.type").String())
	require.Equal(t, "invalid_request", gjson.Get(recorder.Body.String(), "error.code").String())
	require.Equal(t, "input*****.id", gjson.Get(recorder.Body.String(), "error.param").String())
	require.Contains(t, gjson.Get(recorder.Body.String(), "error.message").String(), "request id: req_test")
}

func TestOpenAIVAPIRejectedReplayIDsFailClosed(t *testing.T) {
	for _, body := range []string{
		`{"previous_response_id":"resp_owner","input":[{"type":"message","id":"msg_old"}]}`,
		`{"conversation":"conv_owner","input":[{"type":"message","id":"msg_old"}]}`,
		`{"conversation":{"id":"conv_owner"},"input":[{"type":"message","id":"msg_old"}]}`,
		`{"conversation":{},"input":[{"type":"message","id":"msg_old"}]}`,
		`{"conversation":42,"input":[{"type":"message","id":"msg_old"}]}`,
		`{"conversation":true,"input":[{"type":"message","id":"msg_old"}]}`,
		`{"conversation":[],"input":[{"type":"message","id":"msg_old"}]}`,
		`{"input":[{"type":"message","id":"msg_old"},{"type":"item_reference","id":"msg_old"}]}`,
		`{"input":[{"type":"message","id":"msg_old"},{"type":"reasoning","encrypted_content":"opaque"}]}`,
		`{"input":[{"type":"future_item","id":"item_old"}]}`,
		`{"input":[{"type":"message","id":"msg_old"},{"type":"future_item","id":"item_old"}]}`,
		`{"previous_response_id":42,"input":[{"type":"message","id":"msg_old"}]}`,
		`{"input":[{"type":"message","id":"msg_old"},"not_an_item"]}`,
		`{"input":[{"type":"message","id":"msg_old"}]`,
	} {
		_, _, changed, err := normalizeOpenAIResponsesRejectedFieldRetryBody(400, []byte(body), []byte(`{"error":{"type":"v_api_biz_error","code":"invalid_request","param":"input[0].id"}}`))
		require.NoError(t, err)
		require.False(t, changed)
	}
	for _, errBody := range []string{
		`{"error":{"type":"invalid_request_error","code":"invalid_request","param":"input[0].id"}}`,
		`{"error":{"type":"v_api_biz_error","code":"invalid_request","param":"input[0].call_id"}}`,
		`{"error":{"type":"v_api_biz_error","code":"invalid_request","param":"input[99].id"}}`,
	} {
		_, _, changed, err := normalizeOpenAIResponsesRejectedFieldRetryBody(400, []byte(`{"input":[{"type":"message","id":"msg_old"}]}`), []byte(errBody))
		require.NoError(t, err)
		require.False(t, changed)
	}
}

func TestOpenAIVAPIRejectedReplayIDsWithoutConversationLineage(t *testing.T) {
	for _, testcase := range []struct {
		name     string
		body     string
		expected string
	}{
		{name: "null", body: `{"conversation":null,"input":[{"type":"message","id":"msg_old"}]}`, expected: `{"conversation":null,"input":[{"type":"message"}]}`},
		{name: "empty", body: `{"conversation":"","input":[{"type":"message","id":"msg_old"}]}`, expected: `{"conversation":"","input":[{"type":"message"}]}`},
		{name: "blank", body: `{"conversation":"  ","input":[{"type":"message","id":"msg_old"}]}`, expected: `{"conversation":"  ","input":[{"type":"message"}]}`},
		{name: "stateless storage", body: `{"store":true,"input":[{"type":"message","id":"msg_old"}]}`, expected: `{"store":true,"input":[{"type":"message"}]}`},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			body, changed, err := repairOpenAIVAPIRejectedReplayIDs([]byte(testcase.body), []byte(`{"error":{"type":"v_api_biz_error","code":"invalid_request","param":"input[0].id"}}`), "invalid_request", "input[0].id")
			require.NoError(t, err)
			require.True(t, changed)
			require.JSONEq(t, testcase.expected, string(body))
		})
	}
}

func TestOpenAIVAPIForwardReplayIDBoundaries(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		name := "native"
		if passthrough {
			name = "passthrough"
		}
		t.Run(name, func(t *testing.T) {
			for _, tc := range []struct {
				name         string
				param        string
				nextParam    string
				success      bool
				wantCalls    int
				invalidValue bool
			}{
				{name: "wildcard is not replayed", param: "input*****.id", wantCalls: 1},
				{name: "indexed is repaired once", param: "input[1].id", success: true, wantCalls: 2},
				{name: "same rejection stops", param: "input[1].id", nextParam: "input[1].id", wantCalls: 2},
				{name: "different index still stops", param: "input[1].id", nextParam: "input[0].id", wantCalls: 2},
				{name: "invalid value repaired", param: "input[1].id", success: true, wantCalls: 2, invalidValue: true},
				{name: "invalid value repeated stops", param: "input[1].id", nextParam: "input[0].id", wantCalls: 2, invalidValue: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					body := []byte(`{"model":"gpt-6-astra","stream":false,"input":[{"type":"message","id":"msg_old","role":"assistant","content":"hello"},{"type":"function_call","id":"fc_old","call_id":"call_1","name":"exec","arguments":"{}"},{"type":"function_call_output","id":"out_old","call_id":"call_1","output":"done"}]}`)
					errorBody := func(param string) string {
						if tc.invalidValue {
							return `{"error":{"type":"invalid_request_error","code":"invalid_value","param":"` + param + `","message":"Rejected replay item (request id: req_forward)"}}`
						}
						return `{"error":{"type":"v_api_biz_error","code":"invalid_request","param":"` + param + `","message":"Rejected replay item (request id: req_forward)"}}`
					}
					upstream := &httpUpstreamRecorder{responses: []*http.Response{
						newOpenAIRejectedFieldTestResponse(http.StatusBadRequest, errorBody(tc.param)),
					}}
					if tc.success {
						upstream.responses = append(upstream.responses, newOpenAIRejectedFieldTestResponse(http.StatusOK, `{"id":"resp_ok","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`))
					} else if tc.nextParam != "" {
						upstream.responses = append(upstream.responses, newOpenAIRejectedFieldTestResponse(http.StatusBadRequest, errorBody(tc.nextParam)))
					}
					recorder := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(recorder)
					c.Request = newOpenAIRejectedFieldTestContext(body).Request
					svc := newOpenAIRejectedFieldTestService(upstream)
					account := newOpenAIRejectedFieldTestAccount()
					var result *OpenAIForwardResult
					var err error
					if passthrough {
						result, err = svc.forwardOpenAIPassthrough(t.Context(), c, account, body, body, "gpt-6-astra", false, nil, false, time.Now())
					} else {
						result, err = svc.Forward(t.Context(), c, account, body)
					}
					require.Len(t, upstream.bodies, tc.wantCalls)
					if tc.success {
						require.NoError(t, err)
						require.NotNil(t, result)
					} else {
						require.Error(t, err)
						require.Equal(t, http.StatusBadRequest, c.Writer.Status())
						var failoverErr *UpstreamFailoverError
						require.False(t, errors.As(err, &failoverErr), "deterministic ID rejection must not switch accounts")
						finalParam := tc.param
						if tc.nextParam != "" {
							finalParam = tc.nextParam
						}
						errorPayload := recorder.Body.Bytes()
						require.True(t, gjson.ValidBytes(errorPayload))
						errorType, code := "v_api_biz_error", "invalid_request"
						if tc.invalidValue {
							errorType, code = "invalid_request_error", "invalid_value"
						}
						require.Equal(t, errorType, gjson.GetBytes(errorPayload, "error.type").String())
						require.Equal(t, code, gjson.GetBytes(errorPayload, "error.code").String())
						require.Equal(t, finalParam, gjson.GetBytes(errorPayload, "error.param").String())
						require.Equal(t, "Rejected replay item (request id: req_forward)", gjson.GetBytes(errorPayload, "error.message").String())
					}
					if tc.wantCalls == 2 {
						require.False(t, gjson.GetBytes(upstream.bodies[1], "input.1.id").Exists())
						require.Equal(t, "msg_old", gjson.GetBytes(upstream.bodies[1], "input.0.id").String())
						require.Equal(t, "out_old", gjson.GetBytes(upstream.bodies[1], "input.2.id").String())
						require.Equal(t, "call_1", gjson.GetBytes(upstream.bodies[1], "input.1.call_id").String())
						require.Equal(t, "call_1", gjson.GetBytes(upstream.bodies[1], "input.2.call_id").String())
					} else {
						require.Equal(t, "fc_old", gjson.GetBytes(upstream.bodies[0], "input.1.id").String())
					}
				})
			}
		})
	}
}

func TestOpenAIVAPIReplayIDNoRetryAfterOutputOrCancel(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, boundary := range []string{"output", "committed", "cancelled"} {
			path := "native"
			if passthrough {
				path = "passthrough"
			}
			t.Run(path+"/"+boundary, func(t *testing.T) {
				body := []byte(`{"model":"gpt-6-astra","stream":false,"input":[{"type":"message","id":"msg_old","role":"assistant","content":"hello"}]}`)
				upstream := &httpUpstreamRecorder{responses: []*http.Response{
					newOpenAIRejectedFieldTestResponse(http.StatusBadRequest, `{"error":{"type":"v_api_biz_error","code":"invalid_request","param":"input[0].id","message":"Rejected replay item"}}`),
				}}
				c := newOpenAIRejectedFieldTestContext(body)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				switch boundary {
				case "output":
					_, err := c.Writer.WriteString("existing semantic output")
					require.NoError(t, err)
				case "committed":
					MarkResponseCommitted(c)
				case "cancelled":
					cancel()
				}
				svc := newOpenAIRejectedFieldTestService(upstream)
				account := newOpenAIRejectedFieldTestAccount()
				var err error
				if passthrough {
					_, err = svc.forwardOpenAIPassthrough(ctx, c, account, body, body, "gpt-6-astra", false, nil, false, time.Now())
				} else {
					_, err = svc.Forward(ctx, c, account, body)
				}
				require.Error(t, err)
				require.Len(t, upstream.bodies, 1, "must not replay after output or cancellation")
				var failoverErr *UpstreamFailoverError
				require.False(t, errors.As(err, &failoverErr))
				require.Equal(t, "msg_old", gjson.GetBytes(upstream.bodies[0], "input.0.id").String())
			})
		}
	}
}

func TestOpenAIVAPIReplayIDBudgetIsRequestWide(t *testing.T) {
	budget := &openAIResponsesRejectedFieldRetryBudget{}
	first := newOpenAIResponsesRejectedFieldRetryStateWithBudget([]byte(`{"input":[]}`), budget)
	require.True(t, first.AllowNormalization([]byte(`{"input":[{}]}`), openAIReplayIDRejectionReason))
	second := newOpenAIResponsesRejectedFieldRetryStateWithBudget([]byte(`{"input":[{"id":"another"}]}`), budget)
	require.False(t, second.AllowNormalization([]byte(`{"input":[{"type":"message"}]}`), openAIReplayIDRejectionReason))
	require.True(t, second.AllowNormalization([]byte(`{"input":[{"content":[]}]}`), "status rejection"))
}

func TestOpenAIVAPIWSHTTPBridgeReplayIDBoundaries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name         string
		param        string
		nextParam    string
		success      bool
		cancelled    bool
		wantCalls    int
		invalidValue bool
	}{
		{name: "wildcard", param: "input*****.id", wantCalls: 1},
		{name: "indexed success", param: "input[1].id", success: true, wantCalls: 2},
		{name: "different index stops", param: "input[1].id", nextParam: "input[0].id", wantCalls: 2},
		{name: "cancelled", param: "input[1].id", cancelled: true, wantCalls: 1},
		{name: "invalid value repaired", param: "input[1].id", success: true, wantCalls: 2, invalidValue: true},
		{name: "invalid value repeated stops", param: "input[1].id", nextParam: "input[0].id", wantCalls: 2, invalidValue: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errorResponse := func(param string) *http.Response {
				if tc.invalidValue {
					return newOpenAIRejectedFieldTestResponse(http.StatusBadRequest, `{"error":{"type":"invalid_request_error","code":"invalid_value","param":"`+param+`","message":"Rejected replay item (request id: req_bridge)"}}`)
				}
				return newOpenAIRejectedFieldTestResponse(http.StatusBadRequest, `{"error":{"type":"v_api_biz_error","code":"invalid_request","param":"`+param+`","message":"Rejected replay item (request id: req_bridge)"}}`)
			}
			upstream := &httpUpstreamRecorder{responses: []*http.Response{errorResponse(tc.param)}}
			if tc.success {
				resp := newOpenAIRejectedFieldTestResponse(http.StatusOK, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_bridge\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
				resp.Header.Set("Content-Type", "text/event-stream")
				upstream.responses = append(upstream.responses, resp)
			} else if tc.nextParam != "" {
				upstream.responses = append(upstream.responses, errorResponse(tc.nextParam))
			}
			body := []byte(`{"type":"response.create","model":"gpt-6-astra","input":[{"type":"message","id":"msg_old","role":"assistant","content":"hello"},{"type":"function_call","id":"fc_old","call_id":"call_1","name":"exec","arguments":"{}"}]}`)
			c := newOpenAIRejectedFieldTestContext(body)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tc.cancelled {
				cancel()
			}
			var writes [][]byte
			result, err := newOpenAIRejectedFieldTestService(upstream).proxyOpenAIWSHTTPBridgeTurn(
				ctx, c, newOpenAIRejectedFieldTestAccount(), "sk-test", body, len(body),
				"gpt-6-astra", "", "", "", "", 1,
				func(message []byte) error {
					writes = append(writes, append([]byte(nil), message...))
					return nil
				},
			)
			if tc.cancelled {
				require.Error(t, err)
				require.LessOrEqual(t, len(upstream.bodies), 1)
				require.LessOrEqual(t, len(writes), 1)
				var failoverErr *UpstreamFailoverError
				require.False(t, errors.As(err, &failoverErr))
				if len(writes) == 1 {
					require.Equal(t, "error", gjson.GetBytes(writes[0], "type").String())
				}
				return
			}
			require.Len(t, upstream.bodies, tc.wantCalls)
			require.Len(t, writes, 1)
			if tc.success {
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, "response.completed", gjson.GetBytes(writes[0], "type").String())
			} else {
				require.Error(t, err)
				var failoverErr *UpstreamFailoverError
				require.False(t, errors.As(err, &failoverErr))
				finalParam := tc.param
				if tc.nextParam != "" {
					finalParam = tc.nextParam
				}
				errorType, code := "v_api_biz_error", "invalid_request"
				if tc.invalidValue {
					errorType, code = "invalid_request_error", "invalid_value"
				}
				require.Equal(t, errorType, gjson.GetBytes(writes[0], "error.type").String())
				require.Equal(t, code, gjson.GetBytes(writes[0], "error.code").String())
				require.Equal(t, finalParam, gjson.GetBytes(writes[0], "error.param").String())
				require.Equal(t, "Rejected replay item (request id: req_bridge)", gjson.GetBytes(writes[0], "error.message").String())
			}
			if tc.wantCalls == 2 {
				require.False(t, gjson.GetBytes(upstream.bodies[1], "input.1.id").Exists())
				require.Equal(t, "msg_old", gjson.GetBytes(upstream.bodies[1], "input.0.id").String())
				require.Equal(t, "call_1", gjson.GetBytes(upstream.bodies[1], "input.1.call_id").String())
			}
		})
	}
}
