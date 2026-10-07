//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestEvalRetryAfterHeaderFormats(t *testing.T) {
	now := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		value string
		want  time.Duration
	}{
		{"5", 5 * time.Second}, {"0.25", 250 * time.Millisecond},
		{now.Add(time.Minute).Format(http.TimeFormat), time.Minute},
		{"invalid", 0}, {"-1", 0}, {"NaN", 0}, {"Inf", 0},
		{now.Add(-time.Minute).Format(http.TimeFormat), 0},
	} {
		require.Equal(t, tc.want, openAIEvalRetryAfter(http.Header{"Retry-After": []string{tc.value}}, now), tc.value)
	}
}

func TestEvalRetryAfterDoesNotSendBeforeRunDeadline(t *testing.T) {
	svc, _, transport := evalRunHarness(t, func(_ *http.Request, _ int) (*http.Response, error) {
		resp := newJSONResponse(http.StatusTooManyRequests, `{"error":{"code":"rate_limit_exceeded","message":"wait"}}`)
		resp.Header.Set("Retry-After", "60")
		return resp, nil
	})
	target, err := svc.accountTest.ResolveOpenAIEvalTarget(t.Context(), 995, "gpt-5.4")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	_, record, err := svc.accountTest.runOpenAIEvalSampleAttempts(ctx, target, "probe", "", 3)
	var failure *OpenAIEvalRequestError
	require.True(t, errors.As(err, &failure))
	require.Equal(t, time.Minute, failure.RetryAfter)
	require.Equal(t, 1, record.Attempts)
	require.EqualValues(t, 1, transport.calls.Load())
}

func TestOAuthDiagnosticRequestOwnsSessionAndCanonicalHeaders(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "diagnostic-test-account"}}
	first := map[string]any{"model": "gpt-6-astra"}
	prepareOpenAIOAuthDiagnosticPayload(first, account, "independent-session")
	second := map[string]any{"model": "gpt-6-astra"}
	prepareOpenAIOAuthDiagnosticPayload(second, account, "independent-session")
	require.Equal(t, first, second, "linked probe chain remains coherent")
	other := map[string]any{"model": "gpt-6-astra"}
	prepareOpenAIOAuthDiagnosticPayload(other, account, "another-session")
	require.NotEqual(t, first["prompt_cache_key"], other["prompt_cache_key"])
	headers := http.Header{"Openai-Beta": []string{"responses=experimental"}, "Accept-Language": []string{"zh-CN"}}
	finalizeOpenAIOAuthDiagnosticHeaders(headers, account, "independent-session", []byte(`{"model":"gpt-6-astra"}`))
	require.NotEmpty(t, headers.Get("Originator"))
	require.NotEmpty(t, headers.Get("User-Agent"))
	require.NotEmpty(t, headers.Get("Version"))
	require.Equal(t, "en-US,en;q=0.9", headers.Get("Accept-Language"))
	require.Empty(t, headers.Get("OpenAI-Beta"))
	require.Equal(t, "model=gpt-6-astra", headers.Get(openAICodexRoutingHintHeader))
}

func TestOAuthDiagnosticConvergenceKeepsSessionProjectionConsistent(t *testing.T) {
	for _, mode := range []string{"device", "session", "full"} {
		t.Run(mode, func(t *testing.T) {
			account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth,
				Credentials: map[string]any{"chatgpt_account_id": "diagnostic-test-account"},
				Extra:       map[string]any{"codex_fingerprint_mode": mode, codexFingerprintSeedExtraKey: testCodexFingerprintSeed}}
			payload := map[string]any{"model": "gpt-6-astra"}
			ids := prepareOpenAIOAuthDiagnosticPayload(payload, account, "independent-session")
			body, err := json.Marshal(payload)
			require.NoError(t, err)
			headers := make(http.Header)
			finalizeOpenAIOAuthDiagnosticHeaders(headers, account, "independent-session", body, ids)
			require.NotEmpty(t, headers.Get("x-codex-installation-id"))
			require.Equal(t, headers.Get("x-codex-installation-id"), gjson.GetBytes(body, "client_metadata.x-codex-installation-id").String())
			if mode != "device" {
				require.Equal(t, headers.Get("session-id"), gjson.GetBytes(body, "client_metadata.session_id").String())
				require.Equal(t, headers.Get("session_id"), gjson.GetBytes(body, "prompt_cache_key").String())
				require.Equal(t, ids.turnID, gjson.GetBytes(body, "client_metadata.turn_id").String())
			}
		})
	}
}
