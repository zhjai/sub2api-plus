package service

import (
	"encoding/json"
	"net/http"

	"github.com/google/uuid"
)

// A diagnostic owns an independent session. It never receives downstream
// principal/session state or a user's prompt-cache key.
func prepareOpenAIOAuthDiagnosticPayload(payload map[string]any, credential *Account, session string) *codexFingerprintIDs {
	if session == "" {
		session = uuid.NewString()
	}
	metadata, _ := payload["client_metadata"].(map[string]any)
	if metadata == nil {
		metadata = make(map[string]any)
		payload["client_metadata"] = metadata
	}
	metadata["session_id"] = session
	payload["prompt_cache_key"] = session
	applyCodexAccountIdentityClientMetadataMap(payload, credential, 0)
	ids := resolveCodexFingerprintIDsFromRequest(credential, openAIDiagnosticSessionHeaders(session))
	if ids != nil {
		applyCodexFingerprintClientMetadata(payload, ids)
		applyCodexFingerprintPromptCacheKey(payload, ids)
	}
	if turn, ok := metadata["turn_id"].(string); ok && turn != "" {
		metadata["root_turn_id"] = turn
		raw, _ := metadata[openAIWSTurnMetadataHeader].(string)
		var embedded map[string]any
		if json.Unmarshal([]byte(raw), &embedded) == nil && embedded != nil {
			embedded["root_turn_id"] = turn
			if rebuilt, err := marshalCodexTurnMetadata(embedded); err == nil {
				metadata[openAIWSTurnMetadataHeader] = string(rebuilt)
			}
		}
		if input, ok := payload["input"].([]any); ok {
			for _, raw := range input {
				item, _ := raw.(map[string]any)
				if itemMetadata, ok := item["internal_chat_message_metadata_passthrough"].(map[string]any); ok {
					if _, exists := itemMetadata["turn_id"]; exists {
						itemMetadata["turn_id"] = turn
					}
				}
			}
		}
	}
	return ids
}

func finalizeOpenAIOAuthDiagnosticHeaders(headers http.Header, credential *Account, session string, body []byte, staged ...*codexFingerprintIDs) {
	ensureCodexIdentityHeaders(headers)
	setOpenAIChatGPTAccountHeaders(headers, credential)
	if session != "" && !applyOpenAIEvalCodexPayloadHeaders(headers, body) {
		headers.Set("session_id", isolateOpenAIUpstreamSessionID(0, credential, session))
		headers.Set("session-id", session)
		applyCodexAccountIdentityHeaders(headers, credential, 0)
		var ids *codexFingerprintIDs
		if len(staged) > 0 {
			ids = staged[0]
		} else {
			ids = resolveCodexFingerprintIDsFromRequest(credential, openAIDiagnosticSessionHeaders(session))
		}
		if ids != nil {
			applyCodexFingerprintHeaders(headers, ids)
		}
	}
	credential.ApplyHeaderOverrides(headers)
	enforceCodexIdentityHeadersWithUA(headers, credential.GetOpenAIUserAgent())
	enforceCodexAcceptLanguage(headers)
	stripOpenAILegacyResponsesBeta(headers)
	setOpenAICodexRoutingHintFromBody(headers, credential, body)
}

func openAIDiagnosticSessionHeaders(session string) http.Header {
	headers := make(http.Header)
	headers.Set("session-id", session)
	return headers
}
