package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
)

// OpenAIIdentityContinuation extends response-owner/turn-state metadata without
// storing a ticket, response ID, bearer, or client identity in its value.
type OpenAIIdentityContinuation struct {
	CredentialNamespace string `json:"credential_namespace"`
	APIKeyID            int64  `json:"api_key_id"`
	UserID              int64  `json:"user_id"`
	Revision            string `json:"revision"`
	Mode                string `json:"mode"`
}

var ErrCodexIdentityContinuationConflict = errors.New("identity continuation already belongs to a different policy")

func codexIdentityContinuationMatches(metadata *OpenAIIdentityContinuation, p CodexIdentityPolicy) bool {
	return metadata != nil && metadata.Mode == p.Mode && metadata.APIKeyID == p.APIKeyID && metadata.UserID == p.UserID && p.UserID > 0 && metadata.CredentialNamespace == p.Namespace && metadata.Revision == p.Revision && p.Revision != ""
}

// Shared-only: a process-local owner cache is never authorization for raw IDs.
type OpenAIIdentityContinuationCache interface {
	SetOpenAIIdentityContinuation(context.Context, string, OpenAIIdentityContinuation, time.Duration) error
	GetOpenAIIdentityContinuation(context.Context, string) (*OpenAIIdentityContinuation, error)
}

func OpenAIIdentityContinuationKey(groupID int64, kind, value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("openai:identity-continuation:v1:%d:%s:%x", groupID, kind, sum[:])
}

func (s *OpenAIGatewayService) bindCodexIdentityContinuation(ctx context.Context, c *gin.Context, account *Account, kind, value string) {
	p := EffectiveCodexIdentityPolicy(c, account)
	if value == "" || (p.Mode != CodexIdentityPreserveClient && !codexIdentityHasHistory(account)) || p.APIKeyID <= 0 || p.UserID <= 0 {
		return
	}
	cache, ok := s.cache.(OpenAIIdentityContinuationCache)
	if !ok {
		return
	} // The ingress guard rejects this unsupported combination.
	bindCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), openAIWSStateStoreRedisTimeout)
	defer cancel()
	// Failure never authorizes a continuation: a later shared-cache miss rejects it.
	_ = cache.SetOpenAIIdentityContinuation(bindCtx, OpenAIIdentityContinuationKey(getOpenAIGroupIDFromContext(c), kind, value), OpenAIIdentityContinuation{
		CredentialNamespace: p.Namespace, APIKeyID: p.APIKeyID, UserID: p.UserID, Revision: p.Revision, Mode: p.Mode,
	}, s.openAIWSResponseStickyTTL())
}

func (s *OpenAIGatewayService) validateCodexIdentityContinuation(c *gin.Context, account *Account, kind, value string) error {
	if value == "" || c == nil || c.Request == nil {
		return nil
	}
	cache, ok := s.cache.(OpenAIIdentityContinuationCache)
	p := EffectiveCodexIdentityPolicy(c, account)
	// Once an account has issued raw continuations, a missing binding cannot be
	// assumed to belong to its new isolated policy. Ordinary default accounts
	// without experiment history remain independent of this cache.
	experiment := p.Mode == CodexIdentityPreserveClient || codexIdentityHasHistory(account)
	if !ok {
		if experiment {
			return rejectCodexIdentityRequest(c, "CODEX_IDENTITY_CONTINUATION_UNSUPPORTED", "Shared identity continuation metadata is unavailable; start a new session")
		}
		return nil
	}
	readCtx, cancel := context.WithTimeout(c.Request.Context(), openAIWSStateStoreRedisTimeout)
	metadata, err := cache.GetOpenAIIdentityContinuation(readCtx, OpenAIIdentityContinuationKey(getOpenAIGroupIDFromContext(c), kind, value))
	cancel()
	if err != nil {
		if experiment {
			return rejectCodexIdentityRequest(c, "CODEX_IDENTITY_CONTINUATION_UNAVAILABLE", "Cannot validate shared continuation identity; retry with a new session")
		}
		return nil // Ordinary default continuation does not depend on this cache.
	}
	if metadata == nil {
		if experiment {
			return rejectCodexIdentityRequest(c, "CODEX_IDENTITY_CONTINUATION_UNKNOWN", "Unknown or expired identity continuation; start a new session")
		}
		return nil
	}
	if experiment && s.accountRepo != nil {
		fresh, freshErr := s.accountRepo.GetByID(c.Request.Context(), account.ID)
		if freshErr != nil || fresh == nil {
			return rejectCodexIdentityRequest(c, "CODEX_IDENTITY_CONTINUATION_UNAVAILABLE", "Cannot validate current continuation policy; start a new session")
		}
		if codexIdentityString(fresh, CodexIdentityRevisionKey) != p.Revision || CodexIdentityNamespace(fresh) != p.Namespace || (p.Mode == CodexIdentityPreserveClient && codexIdentityString(fresh, CodexIdentityModeKey) != p.Mode) {
			return rejectCodexIdentityRequest(c, "CODEX_IDENTITY_CONTINUATION_MISMATCH", "Continuation policy changed; start a new session")
		}
		if p.Mode == CodexIdentityIsolated && codexIdentityString(fresh, CodexIdentityModeKey) != CodexIdentityPreserveClient {
			validator, available := s.accountRepo.(CodexIdentityBindingRepository)
			if !available || p.UserID <= 0 || validator.ValidateCodexIdentityBinding(c.Request.Context(), fresh, p.APIKeyID, p.UserID) != nil {
				return rejectCodexIdentityRequest(c, "CODEX_IDENTITY_CONTINUATION_UNAVAILABLE", "Cannot validate current continuation principal; start a new session")
			}
		}
	}
	if !codexIdentityContinuationMatches(metadata, p) {
		return rejectCodexIdentityRequest(c, "CODEX_IDENTITY_CONTINUATION_MISMATCH", "Continuation belongs to a different credential, API key or identity policy revision; start a new session")
	}
	return nil
}
