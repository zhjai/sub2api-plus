package service

import (
	"net/http"

	"github.com/google/uuid"
)

// A diagnostic owns an independent session. It never receives downstream
// principal/session state or a user's prompt-cache key.
func prepareOpenAIOAuthDiagnosticPayload(payload map[string]any, credential *Account, session string) *codexFingerprintIDs {
	if session == "" {
		session = uuid.NewString()
	}
	payload["client_metadata"] = map[string]any{"session_id": session}
	payload["prompt_cache_key"] = session
	applyCodexAccountIdentityClientMetadataMap(payload, credential, 0)
	ids := resolveCodexFingerprintIDsFromRequest(credential, openAIDiagnosticSessionHeaders(session))
	if ids != nil {
		applyCodexFingerprintClientMetadata(payload, ids)
		applyCodexFingerprintPromptCacheKey(payload, ids)
	}
	return ids
}

func finalizeOpenAIOAuthDiagnosticHeaders(headers http.Header, credential *Account, session string, body []byte, staged ...*codexFingerprintIDs) {
	ensureCodexIdentityHeaders(headers)
	setOpenAIChatGPTAccountHeaders(headers, credential)
	if session != "" {
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
