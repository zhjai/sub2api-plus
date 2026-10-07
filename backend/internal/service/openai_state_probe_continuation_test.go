//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStateProbeContinuationTicketObservation(t *testing.T) {
	completed := "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"
	for _, tc := range []struct {
		name, ticket, verdict string
		changed               bool
	}{
		{"no renewal", "", "healthy", false},
		{"same ticket", "mint-ticket", "healthy", false},
		{"new ticket", "replacement-ticket", "degraded", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var session string
			upstream := &evalTransportStub{respond: func(req *http.Request, call int) (*http.Response, error) {
				if call == 1 {
					session = req.Header.Get("session_id")
					require.Empty(t, req.Header.Get(openAICodexTurnStateHeader))
					response := stateProbeResponse("mint-ticket", completed, 200)
					response.Header.Set("Set-Cookie", "__oailb=route-cookie; Path=/; Secure")
					return response, nil
				}
				require.Equal(t, 2, call, "completed continuations must not restart the chain")
				require.Equal(t, session, req.Header.Get("session_id"))
				require.Equal(t, "mint-ticket", req.Header.Get(openAICodexTurnStateHeader))
				require.Contains(t, req.Header.Get("Cookie"), "__oailb=route-cookie")
				return stateProbeResponse(tc.ticket, completed, 200), nil
			}}
			svc, target := evalOAuthHarness(upstream)
			result := svc.RunOpenAIStateProbe(t.Context(), target)
			require.Equal(t, "codex-turn-state-v2", result.Version)
			require.Equal(t, tc.verdict, result.Verdict)
			require.Equal(t, tc.changed, result.NewTicket)
			require.Equal(t, len(tc.ticket), result.ContinueTicketLength)
			require.Equal(t, 1, result.Attempts)
			require.Equal(t, 2, result.RequestCount)
			require.Empty(t, result.Failure)
			require.Empty(t, result.LastFailureStep)
			require.Len(t, result.Samples, 2)
			for _, sample := range result.Samples {
				require.True(t, sample.Valid)
				require.Empty(t, sample.ErrorCode)
				require.Empty(t, sample.AttemptErrors)
			}
		})
	}
}

func TestStateProbeTicketlessContinuationRequiresCompletedStream(t *testing.T) {
	completed := "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"
	for _, tc := range []struct {
		name, body string
		status     int
		cancel     bool
	}{
		{"failed terminal", "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\"}}\n\n", 200, false},
		{"incomplete terminal", "data: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\"}}\n\n", 200, false},
		{"premature EOF", "data: {\"type\":\"response.created\"}\n\n", 200, false},
		{"DONE only", "data: [DONE]\n\n", 200, false},
		{"rate limited", `{"error":{"code":"rate_limit_exceeded"}}`, 429, false},
		{"unauthorized", `{"error":{"code":"invalid_api_key"}}`, 401, false},
		{"cancelled", "", 200, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			upstream := &evalTransportStub{respond: func(_ *http.Request, call int) (*http.Response, error) {
				if call == 1 {
					return stateProbeResponse("mint-ticket", completed, 200), nil
				}
				if tc.cancel {
					cancel()
					return nil, context.Canceled
				}
				return stateProbeResponse("", tc.body, tc.status), nil
			}}
			svc, target := evalOAuthHarness(upstream)
			result := svc.RunOpenAIStateProbeAttempts(ctx, target, 1)
			require.Equal(t, "inconclusive", result.Verdict)
			require.NotEmpty(t, result.Failure)
			require.NotEqual(t, "missing_ticket", result.Failure)
			require.False(t, result.NewTicket)
			require.Len(t, result.Samples, 2)
			require.False(t, result.Samples[1].Valid)
		})
	}
}
