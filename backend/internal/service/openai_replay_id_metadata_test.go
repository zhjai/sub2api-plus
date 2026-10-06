package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIReplayIDMetadataConsistency(t *testing.T) {
	for _, testcase := range []struct {
		name string
		body string
	}{
		{name: "mixed case vapi", body: `{"error":{"type":"V_API_BIZ_ERROR","code":"INVALID_REQUEST","param":"INPUT[0].ID"}}`},
		{name: "mixed case invalid value", body: `{"error":{"type":"Invalid_Request_Error","code":"Invalid_Value","param":"Input.0.Id"}}`},
		{name: "nested code", body: `{"error":{"type":"v_api_biz_error","param":"input[0].id","message":"{\"error\":{\"code\":\"invalid_request\"}}"}}`},
		{name: "trimmed fields", body: `{"error":{"type":" v_api_biz_error ","code":" INVALID_REQUEST ","param":" INPUT[0].ID "}}`},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			source := []byte(testcase.body)
			require.True(t, isOpenAIReplayItemIDRejection(http.StatusBadRequest, source))
			require.False(t, isOpenAIReplayItemIDRejection(http.StatusBadGateway, source))
			require.False(t, shouldFailoverOpenAIPassthroughResponse(newOpenAIRejectedFieldTestAccount(), http.StatusBadRequest, source))
			request := []byte(`{"input":[{"type":"function_call","id":"fc_old","call_id":"call_keep","name":"exec","arguments":"{}"}]}`)
			repaired, reason, changed, err := normalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, request, source)
			require.NoError(t, err)
			require.True(t, changed)
			require.Equal(t, openAIReplayIDRejectionReason, reason)
			require.False(t, gjson.GetBytes(repaired, "input.0.id").Exists())
			require.Equal(t, "call_keep", gjson.GetBytes(repaired, "input.0.call_id").String())
			event := buildOpenAIWSHTTPBridgeErrorEvent(http.StatusBadRequest, "Rejected replay item", source)
			require.Equal(t, extractUpstreamErrorCode(source), gjson.GetBytes(event, "error.code").String())
			require.Equal(t, gjson.GetBytes(source, "error.type").String(), gjson.GetBytes(event, "error.type").String())
			require.Equal(t, gjson.GetBytes(source, "error.param").String(), gjson.GetBytes(event, "error.param").String())
		})
	}
}

func TestOpenAIReplayIDCustomErrorCodesKeep400(t *testing.T) {
	for _, body := range []string{
		`{"error":{"type":"v_api_biz_error","code":"invalid_request","param":"input*****.id","message":"Rejected replay item"}}`,
		`{"error":{"type":"invalid_request_error","code":"invalid_value","param":"input[455].id","message":"Rejected replay item"}}`,
	} {
		client, recorder := newOpenAIUpstreamErrorTestContext(t)
		account := newOpenAIUpstreamErrorTestAccount()
		account.Type = AccountTypeAPIKey
		account.Credentials = map[string]any{"custom_error_codes_enabled": true, "custom_error_codes": []any{float64(429)}}
		require.False(t, account.ShouldHandleErrorCode(http.StatusBadRequest))
		service := &OpenAIGatewayService{}
		_, err := service.handleErrorResponse(t.Context(), newOpenAIUpstreamErrorResponse(http.StatusBadRequest, body), client, account, nil)
		require.Error(t, err)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
		require.Equal(t, gjson.Get(body, "error.code").String(), gjson.Get(recorder.Body.String(), "error.code").String())
		require.Equal(t, gjson.Get(body, "error.param").String(), gjson.Get(recorder.Body.String(), "error.param").String())
	}
}
