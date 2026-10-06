//go:build unit

package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStateProbeRetriesFreshLinkedChains(t *testing.T) {
	completed := "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"model\":\"gpt-6-astra\"}}\n\n"
	var sessions []string
	upstream := &evalTransportStub{respond: func(req *http.Request, call int) (*http.Response, error) {
		sessions = append(sessions, req.Header.Get("session_id"))
		switch call {
		case 1:
			require.Empty(t, req.Header.Get(openAICodexTurnStateHeader))
			resp := stateProbeResponse("old-ticket", completed, 200)
			resp.Header.Set("Set-Cookie", "__oailb=old-cookie")
			return resp, nil
		case 2:
			require.Equal(t, "old-ticket", req.Header.Get(openAICodexTurnStateHeader))
			require.Contains(t, req.Header.Get("Cookie"), "old-cookie")
			return stateProbeResponse("", `{"error":{"code":"rate_limit_exceeded"}}`, http.StatusTooManyRequests), nil
		case 3:
			require.Empty(t, req.Header.Get(openAICodexTurnStateHeader))
			require.Empty(t, req.Header.Get("Cookie"))
			return stateProbeResponse("new-ticket", completed, 200), nil
		default:
			require.Equal(t, "new-ticket", req.Header.Get(openAICodexTurnStateHeader))
			return stateProbeResponse("new-ticket", completed, 200), nil
		}
	}}
	svc, target := evalOAuthHarness(upstream)
	result := svc.RunOpenAIStateProbe(t.Context(), target)
	require.Equal(t, "healthy", result.Verdict)
	require.Equal(t, 2, result.Attempts)
	require.Equal(t, 4, result.RequestCount)
	require.Len(t, result.Samples, 4)
	require.Equal(t, sessions[0], sessions[1])
	require.Equal(t, sessions[2], sessions[3])
	require.NotEqual(t, sessions[0], sessions[2])
	require.Empty(t, result.Failure)
}

func TestStateProbeMissingTicketRemainsUnknown(t *testing.T) {
	upstream := &evalTransportStub{respond: func(*http.Request, int) (*http.Response, error) {
		return stateProbeResponse("", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n", 200), nil
	}}
	svc, target := evalOAuthHarness(upstream)
	result := svc.RunOpenAIStateProbe(t.Context(), target)
	require.Equal(t, "inconclusive", result.Verdict)
	require.Equal(t, "missing_ticket", result.Failure)
	require.Equal(t, 3, result.Attempts)
	require.Equal(t, 3, result.RequestCount)
	require.False(t, result.NewTicket)
}

func TestStateProbeAuthenticationDoesNotRetry(t *testing.T) {
	upstream := &evalTransportStub{respond: func(*http.Request, int) (*http.Response, error) {
		return stateProbeResponse("", `{"error":{"code":"invalid_api_key"}}`, 401), nil
	}}
	svc, target := evalOAuthHarness(upstream)
	result := svc.RunOpenAIStateProbe(t.Context(), target)
	require.Equal(t, "inconclusive", result.Verdict)
	require.Equal(t, 1, result.RequestCount)
	require.Equal(t, 1, result.Attempts)
}

func TestStateProbeCapsConfiguredAttemptsAtThreeChains(t *testing.T) {
	upstream := &evalTransportStub{respond: func(*http.Request, int) (*http.Response, error) {
		return stateProbeResponse("", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n", 200), nil
	}}
	svc, target := evalOAuthHarness(upstream)
	result := svc.RunOpenAIStateProbeAttempts(t.Context(), target, 10)
	require.Equal(t, 3, result.Attempts)
	require.LessOrEqual(t, result.RequestCount, 6)
}

func TestStateProbeContinueFailuresUseAtMostSixFreshRequests(t *testing.T) {
	completed := "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"
	var previousSession string
	upstream := &evalTransportStub{respond: func(req *http.Request, call int) (*http.Response, error) {
		if call%2 == 1 {
			require.Empty(t, req.Header.Get(openAICodexTurnStateHeader))
			require.Empty(t, req.Header.Get("Cookie"))
			require.NotEqual(t, previousSession, req.Header.Get("session_id"))
			previousSession = req.Header.Get("session_id")
			return stateProbeResponse(previousSession, completed, 200), nil
		}
		require.Equal(t, previousSession, req.Header.Get("session_id"))
		require.Equal(t, previousSession, req.Header.Get(openAICodexTurnStateHeader))
		return stateProbeResponse("", `{"error":{"code":"rate_limit_exceeded"}}`, http.StatusTooManyRequests), nil
	}}
	svc, target := evalOAuthHarness(upstream)
	result := svc.RunOpenAIStateProbeAttempts(t.Context(), target, 10)
	require.Equal(t, "inconclusive", result.Verdict)
	require.Equal(t, 3, result.Attempts)
	require.Equal(t, 6, result.RequestCount)
	require.Len(t, result.Samples, 6)
}
