package service

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIReplayIDWSTurnBudget(t *testing.T) {
	initial := []byte(`{"input":[{"type":"message","id":"msg_old"}]}`)
	repaired := []byte(`{"input":[{"type":"message"}]}`)
	client := newOpenAIRejectedFieldTestContext(initial)
	first := openAIResponsesRejectedFieldRetryStateForWSTurn(client, initial, 1)
	require.True(t, first.AllowNormalization(repaired, openAIReplayIDRejectionReason))
	secondAccount := openAIResponsesRejectedFieldRetryStateForWSTurn(client, initial, 1)
	require.False(t, secondAccount.AllowNormalization(repaired, openAIReplayIDRejectionReason))
	require.True(t, secondAccount.AllowNormalization([]byte(`{"input":[],"tools":[]}`), "tool schema compatibility"))
	nextTurn := openAIResponsesRejectedFieldRetryStateForWSTurn(client, initial, 2)
	require.True(t, nextTurn.AllowNormalization(repaired, openAIReplayIDRejectionReason))
	otherConnection := openAIResponsesRejectedFieldRetryStateForWSTurn(newOpenAIRejectedFieldTestContext(initial), initial, 1)
	require.True(t, otherConnection.AllowNormalization(repaired, openAIReplayIDRejectionReason))
	nilContext := openAIResponsesRejectedFieldRetryStateForWSTurn(nil, initial, 1)
	require.True(t, nilContext.AllowNormalization(repaired, openAIReplayIDRejectionReason))
	require.False(t, nilContext.AllowNormalization(repaired, openAIReplayIDRejectionReason))
}

func TestOpenAIReplayIDLogicalWSTurnBudgetAcrossReentry(t *testing.T) {
	initial := []byte(`{"input":[{"type":"message","id":"msg_old"}]}`)
	repaired := []byte(`{"input":[{"type":"message"}]}`)
	client := newOpenAIRejectedFieldTestContext(initial)
	firstCtx := withOpenAIReplayLogicalTurnOffset(context.Background(), client)
	firstTurn := openAIResponsesRejectedFieldRetryStateForLogicalWSTurn(firstCtx, client, initial, 1)
	require.True(t, firstTurn.AllowNormalization(repaired, openAIReplayIDRejectionReason))
	secondTurn := openAIResponsesRejectedFieldRetryStateForLogicalWSTurn(firstCtx, client, initial, 2)
	require.True(t, secondTurn.AllowNormalization(repaired, openAIReplayIDRejectionReason))
	reentryCtx := withOpenAIReplayLogicalTurnOffset(context.Background(), client)
	retriedTurn := openAIResponsesRejectedFieldRetryStateForLogicalWSTurn(reentryCtx, client, initial, 1)
	require.False(t, retriedTurn.AllowNormalization(repaired, openAIReplayIDRejectionReason))
	nextTurn := openAIResponsesRejectedFieldRetryStateForLogicalWSTurn(reentryCtx, client, initial, 2)
	require.True(t, nextTurn.AllowNormalization(repaired, openAIReplayIDRejectionReason))
}

func TestOpenAIReplayIDWSBridgeBudgetSurvivesAccountFailover(t *testing.T) {
	body := []byte(`{"type":"response.create","model":"gpt-6-astra","input":[{"type":"message","id":"msg_old","role":"assistant","content":"hello"}]}`)
	rejection := `{"error":{"type":"v_api_biz_error","code":"invalid_request","param":"input[0].id","message":"Rejected replay item"}}`
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		newOpenAIRejectedFieldTestResponse(http.StatusBadRequest, rejection),
		newOpenAIRejectedFieldTestResponse(http.StatusServiceUnavailable, `{"error":{"message":"temporarily unavailable"}}`),
		newOpenAIRejectedFieldTestResponse(http.StatusBadRequest, rejection),
	}}
	service := newOpenAIRejectedFieldTestService(upstream)
	client := newOpenAIRejectedFieldTestContext(body)
	firstAccount := newOpenAIRejectedFieldTestAccount()
	firstAccount.ID = 101
	secondAccount := newOpenAIRejectedFieldTestAccount()
	secondAccount.ID = 102
	var events [][]byte
	writeClient := func(payload []byte) error {
		events = append(events, append([]byte(nil), payload...))
		return nil
	}
	firstResult, firstError := service.proxyOpenAIWSHTTPBridgeTurn(t.Context(), client, firstAccount, "test-token", body, len(body), "gpt-6-astra", "", "", "", "", 1, writeClient)
	require.Nil(t, firstResult)
	var failoverError *UpstreamFailoverError
	require.True(t, errors.As(firstError, &failoverError))
	require.Empty(t, events)
	require.Len(t, upstream.bodies, 2)
	require.False(t, gjson.GetBytes(upstream.bodies[1], "input.0.id").Exists())
	secondResult, secondError := service.proxyOpenAIWSHTTPBridgeTurn(t.Context(), client, secondAccount, "test-token", body, len(body), "gpt-6-astra", "", "", "", "", 1, writeClient)
	require.Nil(t, secondResult)
	require.Error(t, secondError)
	failoverError = nil
	require.False(t, errors.As(secondError, &failoverError))
	require.Len(t, upstream.bodies, 3)
	require.Len(t, events, 1)
	require.Equal(t, "invalid_request", gjson.GetBytes(events[0], "error.code").String())
	require.Equal(t, "input[0].id", gjson.GetBytes(events[0], "error.param").String())
}

func TestOpenAIReplayIDWSBridgeSecondTurnBudgetSurvivesReentry(t *testing.T) {
	body := []byte(`{"type":"response.create","model":"gpt-6-astra","input":[{"type":"message","id":"msg_old","role":"assistant","content":"hello"}]}`)
	rejection := `{"error":{"type":"v_api_biz_error","code":"invalid_request","param":"input[0].id","message":"Rejected replay item"}}`
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		newOpenAIRejectedFieldTestResponse(http.StatusBadRequest, rejection),
		newOpenAIRejectedFieldTestResponse(http.StatusTooManyRequests, `{"error":{"code":"rate_limit_exceeded","message":"temporarily unavailable"}}`),
		newOpenAIRejectedFieldTestResponse(http.StatusBadRequest, rejection),
	}}
	service := newOpenAIRejectedFieldTestService(upstream)
	client := newOpenAIRejectedFieldTestContext(body)
	firstAccount := newOpenAIRejectedFieldTestAccount()
	firstAccount.ID = 201
	secondAccount := newOpenAIRejectedFieldTestAccount()
	secondAccount.ID = 202
	var events [][]byte
	writeClient := func(payload []byte) error {
		events = append(events, append([]byte(nil), payload...))
		return nil
	}
	ctx := withOpenAIReplayLogicalTurnOffset(t.Context(), client)
	_, err := service.proxyOpenAIWSHTTPBridgeTurn(ctx, client, firstAccount, "test-token", body, len(body), "gpt-6-astra", "", "", "", "", 2, writeClient)
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.Empty(t, events)
	require.Len(t, upstream.bodies, 2)
	require.False(t, gjson.GetBytes(upstream.bodies[1], "input.0.id").Exists())
	reentryCtx := withOpenAIReplayLogicalTurnOffset(t.Context(), client)
	_, err = service.proxyOpenAIWSHTTPBridgeTurn(reentryCtx, client, secondAccount, "test-token", body, len(body), "gpt-6-astra", "", "", "", "", 1, writeClient)
	require.Error(t, err)
	failover = nil
	require.False(t, errors.As(err, &failover))
	require.Len(t, upstream.bodies, 3)
	require.Len(t, events, 1)
	require.Equal(t, "input[0].id", gjson.GetBytes(events[0], "error.param").String())
}
