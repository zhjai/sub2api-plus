//go:build prism_live

package service

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	bridgeconfig "github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/creds"
)

// This opt-in test reads only a user-designated credential file. It never
// changes the file, persists credentials or prints upstream bodies/identity.
func TestPrismLiveAcceptance(t *testing.T) {
	path := os.Getenv("PRISM_LIVE_ACCOUNTS_FILE")
	if path == "" {
		t.Skip("user-designated Prism credentials required")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("cannot read designated credentials")
	}
	var file struct {
		Accounts []bridgeconfig.AccountConfig `json:"accounts"`
	}
	if json.Unmarshal(raw, &file) != nil {
		t.Fatal("invalid account file")
	}
	var credential *creds.Credential
	for _, candidate := range file.Accounts {
		c := creds.FromAccountConfig(candidate)
		if candidate.IsEnabled() && candidate.Proxy == "" && c.AccessToken != "" && !c.NeedsRefresh(time.Now(), 2*time.Minute) {
			credential = c
			break
		}
	}
	if credential == nil {
		t.Fatal("no enabled, unexpired direct account; live test does not rotate file credentials")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	verified, identity, models, err := NewPrismAccountService(nil).verify(ctx, credential, nil)
	if err != nil {
		t.Fatal("identity/catalog verification: ", safePrismError(err))
	}
	if identity == "" || len(models) == 0 {
		t.Fatal("missing verified identity or entitlements")
	}
	t.Logf("verified identity and catalog; available models=%d", len(models))
	model := models[0].ID
	for _, m := range models {
		if strings.Contains(m.ID, "gpt-6.1-sol") || strings.Contains(m.ID, "gpt-6-astra") {
			model = m.ID
			break
		}
	}
	account := &Account{ID: 987654, Platform: PlatformPrism, Type: AccountTypeOAuth, Credentials: prismCredentialMap(verified, identity)}
	defer InvalidatePrismAccountCatalog(account.ID)
	store := &prismTestStore{states: map[string][]byte{}}
	gateway := &OpenAIGatewayService{cache: store, openaiWSStateStore: NewOpenAIWSStateStore(nil)}
	t.Run("responses_text", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"model": model, "input": "Reply with exactly PRISM_TEXT_OK and no other text."})
		c, w := prismTestContext(string(body))
		result, err := gateway.forwardPrismResponses(ctx, c, account, body)
		if err != nil || result == nil || !strings.Contains(w.Body.String(), "PRISM_TEXT_OK") || !strings.Contains(w.Body.String(), `"status":"completed"`) {
			t.Fatal("Responses text did not complete: ", safePrismError(err))
		}
	})
	t.Run("codex_tool_continuation", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"model": model, "input": "Use the available exec tool to run printf prism-live-ok. Do not invent its output; call the tool now, then wait for the tool result.", "tools": []any{map[string]any{"type": "custom", "name": "exec", "description": "Execute JavaScript that can call tools.exec_command({cmd: string}); emit results using text(...)."}}})
		c, w := prismTestContext(string(body))
		result, err := gateway.forwardPrismResponses(ctx, c, account, body)
		if err != nil || result == nil {
			t.Fatal("tool request did not complete: ", safePrismError(err))
		}
		var response struct {
			ID     string `json:"id"`
			Output []struct {
				Type   string `json:"type"`
				Name   string `json:"name"`
				CallID string `json:"call_id"`
			} `json:"output"`
		}
		if json.Unmarshal(w.Body.Bytes(), &response) != nil || len(response.Output) != 1 || response.Output[0].Type != "custom_tool_call" || response.Output[0].Name != "exec" || response.Output[0].CallID == "" {
			t.Fatal("expected structured exec call, not raw assistant protocol text")
		}
		// Feed the known harmless command result, never execute model-generated JS.
		body, _ = json.Marshal(map[string]any{"model": model, "previous_response_id": response.ID, "input": []any{map[string]any{"type": "custom_tool_call_output", "call_id": response.Output[0].CallID, "output": "prism-live-ok"}}})
		c, w = prismTestContext(string(body))
		result, err = gateway.forwardPrismResponses(ctx, c, account, body)
		if err != nil || result == nil || !strings.Contains(w.Body.String(), "prism-live-ok") || strings.Contains(w.Body.String(), `"type":"custom_tool_call"`) {
			t.Fatal("tool continuation did not consume its output: ", safePrismError(err))
		}
	})
	t.Run("responses_stream", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"model": model, "input": "Reply with exactly PRISM_STREAM_OK and no other text.", "stream": true})
		c, w := prismTestContext(string(body))
		result, err := gateway.forwardPrismResponses(ctx, c, account, body)
		if err != nil || result == nil || !strings.Contains(w.Body.String(), "PRISM_STREAM_OK") || !strings.Contains(w.Body.String(), "event: response.completed") || strings.Contains(w.Body.String(), "event: response.failed") {
			t.Fatal("Responses stream did not complete: ", safePrismError(err))
		}
	})
	t.Run("chat_text", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"model": model, "messages": []any{map[string]any{"role": "user", "content": "Reply with exactly PRISM_CHAT_OK and no other text."}}})
		c, w := prismTestContext(string(body))
		result, err := gateway.forwardPrismChat(ctx, c, account, body)
		if err != nil || result == nil || !strings.Contains(w.Body.String(), "PRISM_CHAT_OK") {
			t.Fatal("Chat text did not complete: ", safePrismError(err))
		}
	})
}
