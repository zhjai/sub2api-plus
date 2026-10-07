//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCodexOutboundDiagnosticsKeepRequestAndSecretsPrivate(t *testing.T) {
	raw := `{"model":"gpt-6-astra","input":"private-prompt","prompt_cache_key":"client-session","client_metadata":{"session_id":"client-session"}}`
	req, err := http.NewRequest(http.MethodPost, chatgptCodexAPIURL, strings.NewReader(raw))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer private-token")
	req.Header.Set("session-id", "client-session")
	account := &Account{ID: 5, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "upstream-account"}}
	req = withCodexOutboundDiagnostics(req, account)
	d := req.Context().Value(codexOutboundDiagnosticKey{}).(*codexOutboundDiagnostic)
	require.Equal(t, "gpt-6-astra", d.model)
	require.Equal(t, d.ids["session-id"], d.ids["prompt_cache_key"])
	require.Equal(t, d.ids["prompt_cache_key"], d.ids["client_metadata.session_id"])
	require.NotContains(t, d.ids, "Authorization")
	for _, value := range d.ids {
		require.NotContains(t, value, "client-session")
	}
	forwarded, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	require.Equal(t, raw, string(forwarded))
	require.Equal(t, "Bearer private-token", req.Header.Get("Authorization"))
}

func TestCodexDiagnosticBodyPreservesFragmentedStreamAndIgnoresOutput(t *testing.T) {
	raw := "data: {\"delta\":\"response.completed\"}\n\n" + strings.Repeat("x", 1024) + "\nevent: response.completed\r\ndata: {}\n\n"
	terminal, emissions := "", 0
	body := &codexDiagnosticBody{ReadCloser: io.NopCloser(strings.NewReader(raw)), start: time.Now(), emit: func(event string, _ time.Duration) { terminal = event; emissions++ }}
	var result strings.Builder
	buffer := make([]byte, 3)
	for {
		n, err := body.Read(buffer)
		result.Write(buffer[:n])
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
	}
	require.NoError(t, body.Close())
	require.Equal(t, raw, result.String())
	require.Equal(t, "response.completed", terminal)
	require.Equal(t, 1, emissions)
	require.LessOrEqual(t, cap(body.line), 256)
}

func TestCodexDiagnosticObserverAllowsNilRequest(t *testing.T) {
	require.NotPanics(t, func() { ObserveCodexOutboundAttempt(nil, "plugin")(nil, nil) })
}

func TestCodexWSOutboundDiagnosticRetainsOnlySafeFields(t *testing.T) {
	account := &Account{ID: 5, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "test-account"}}
	h := make(http.Header)
	h.Set("session-id", "private-session")
	ctx := withCodexWSDiagnostics(context.Background(), nil, account, h)
	d := newCodexWSOutboundDiagnostic(ctx)
	require.NotNil(t, d)
	d.sent(map[string]any{"type": "response.create", "model": "gpt-6-astra", "input": "private-prompt", "prompt_cache_key": "private-session", "reasoning": map[string]any{"effort": "high"}}, nil)
	require.True(t, d.active)
	require.Equal(t, "high", d.effort)
	require.Equal(t, d.ids["session-id"], d.ids["prompt_cache_key"])
	for _, v := range d.ids {
		require.NotContains(t, v, "private")
	}
	d.received([]byte(`{"type":"response.output_text.delta","delta":"response.completed private-output"}`), nil)
	require.True(t, d.active)
	d.received([]byte(`{"type":"response.completed"}`), nil)
	require.False(t, d.active)
}
