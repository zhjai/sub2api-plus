package service

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service/cpaeval"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

const openAIEvalCodexRequestProfile = "codex-turn-cpa-179d6dae-v1"
const openAIEvalAPIKeyCodexRequestProfile = "codex-turn-cpa-179d6dae-apikey-v1"

//go:embed data/codex_eval/codex_context.json
var openAIEvalCodexContextJSON []byte

//go:embed data/codex_eval/LICENSE
var openAIEvalCodexContextLicense string

// The compatibility entry point leaves developer instructions unspecified.
func newOpenAIEvalCodexPayload(credential *Account, model, effort, prompt string, now time.Time) (map[string]any, string, error) {
	return newOpenAIEvalCodexPayloadWithInstructions(credential, model, effort, "", prompt, now)
}

// Developer instructions lead the first captured context message, as in the
// upstream plugin. Canonical OAuth identity projection remains the caller's job.
func newOpenAIEvalCodexPayloadWithInstructions(credential *Account, model, effort, instructions, prompt string, now time.Time) (map[string]any, string, error) {
	namespace := codexAccountIdentityNamespace(credential)
	if namespace == "" && credential != nil && credential.Type == AccountTypeAPIKey {
		namespace = openAIEvalCredentialNamespace(credential)
	}
	installation := uuid.NewString()
	if namespace != "" {
		installation = deriveStableUUIDv4("sub2api:codex-evaluation-installation:" + namespace)
	}
	payload, session, err := cpaeval.NewCodexPayload(installation, model, strings.ToLower(strings.TrimSpace(effort)), instructions, prompt, now)
	if err != nil {
		return nil, "", err
	}
	// Preserve the service's request-ID projection contract for the caller.
	payload["client_metadata"].(map[string]any)["x-client-request-id"] = session
	return payload, session, nil
}

// The payload has already been projected once. Copy its final IDs rather than
// scoping the same IDs again in headers; no downstream principal is borrowed.
func applyOpenAIEvalCodexPayloadHeaders(headers http.Header, body []byte) bool {
	cm := gjson.GetBytes(body, "client_metadata")
	if cm.Get("thread_id").String() == "" || cm.Get(openAIWSTurnMetadataHeader).String() == "" {
		return false
	}
	for header, field := range map[string]string{
		"session-id": "session_id", "thread-id": "thread_id", "x-client-request-id": "x-client-request-id",
		"x-codex-installation-id": "x-codex-installation-id", "x-codex-window-id": "x-codex-window-id",
		openAIWSTurnMetadataHeader: openAIWSTurnMetadataHeader,
	} {
		headers.Set(header, cm.Get(field).String())
	}
	headers.Set("session_id", gjson.GetBytes(body, "prompt_cache_key").String())
	headers.Set(responsesLiteHeader, "true")
	headers.Set("x-codex-beta-features", "remote_compaction_v2")
	return true
}

func marshalOpenAIEvalPayload(payload map[string]any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(payload); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}
