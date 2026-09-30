//go:build unit

package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func stateProbeResponse(ticket, body string, status int) *http.Response {
	response := newJSONResponse(status, body)
	if ticket != "" {
		response.Header.Set(openAICodexTurnStateHeader, ticket)
	}
	return response
}

func TestOpenAIStateProbeShotClassifiesContinuationState(t *testing.T) {
	account := &Account{
		ID:          901,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{"access_token": "probe-token"},
	}
	credential := account
	completed := "data: {\"type\":\"response.completed\",\"response\":{\"model\":\"gpt-6-astra\"}}\n\n"

	t.Run("unchanged ticket is healthy", func(t *testing.T) {
		upstream := &queuedHTTPUpstream{responses: []*http.Response{
			stateProbeResponse("ticket-a", completed, http.StatusOK),
			stateProbeResponse("ticket-a", completed, http.StatusOK),
		}}
		svc := &AccountTestService{httpUpstream: upstream, tlsFPProfileService: &TLSFingerprintProfileService{}}
		first := svc.openAIStateProbeShot(t.Context(), account, credential, "probe-token", "gpt-6-astra", "", "")
		second := svc.openAIStateProbeShot(t.Context(), account, credential, "probe-token", "gpt-6-astra", first.ticket, first.cookies)
		require.Empty(t, first.failure)
		require.Empty(t, second.failure)
		require.Equal(t, "ticket-a", second.ticket)
		require.True(t, second.terminal)
	})

	t.Run("changed ticket is still a valid shot", func(t *testing.T) {
		upstream := &queuedHTTPUpstream{responses: []*http.Response{
			stateProbeResponse("ticket-a", completed, http.StatusOK),
			stateProbeResponse("ticket-b", completed, http.StatusOK),
		}}
		svc := &AccountTestService{httpUpstream: upstream, tlsFPProfileService: &TLSFingerprintProfileService{}}
		first := svc.openAIStateProbeShot(t.Context(), account, credential, "probe-token", "gpt-6-astra", "", "")
		second := svc.openAIStateProbeShot(t.Context(), account, credential, "probe-token", "gpt-6-astra", first.ticket, first.cookies)
		require.Empty(t, first.failure)
		require.Empty(t, second.failure)
		require.NotEqual(t, first.ticket, second.ticket)
	})

	t.Run("missing second ticket is inconclusive evidence", func(t *testing.T) {
		upstream := &queuedHTTPUpstream{responses: []*http.Response{
			stateProbeResponse("ticket-a", completed, http.StatusOK),
			stateProbeResponse("", completed, http.StatusOK),
		}}
		svc := &AccountTestService{httpUpstream: upstream, tlsFPProfileService: &TLSFingerprintProfileService{}}
		first := svc.openAIStateProbeShot(t.Context(), account, credential, "probe-token", "gpt-6-astra", "", "")
		second := svc.openAIStateProbeShot(t.Context(), account, credential, "probe-token", "gpt-6-astra", first.ticket, first.cookies)
		require.Empty(t, first.failure)
		require.Equal(t, "missing_ticket", second.failure)
	})

	t.Run("429 is rate limited", func(t *testing.T) {
		upstream := &queuedHTTPUpstream{responses: []*http.Response{
			stateProbeResponse("", `{"error":{"code":"rate_limit_exceeded"}}`, http.StatusTooManyRequests),
		}}
		svc := &AccountTestService{httpUpstream: upstream, tlsFPProfileService: &TLSFingerprintProfileService{}}
		shot := svc.openAIStateProbeShot(t.Context(), account, credential, "probe-token", "gpt-6-astra", "", "")
		require.Equal(t, "rate_limited", shot.failure)
	})

	t.Run("failed terminal is stream error", func(t *testing.T) {
		upstream := &queuedHTTPUpstream{responses: []*http.Response{
			stateProbeResponse("ticket-a", "data: {\"type\":\"response.failed\",\"response\":{}}\n\n", http.StatusOK),
		}}
		svc := &AccountTestService{httpUpstream: upstream, tlsFPProfileService: &TLSFingerprintProfileService{}}
		shot := svc.openAIStateProbeShot(t.Context(), account, credential, "probe-token", "gpt-6-astra", "", "")
		require.Equal(t, "stream_error", shot.failure)
	})
}
