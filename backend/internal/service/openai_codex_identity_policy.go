package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	CodexIdentityModeKey          = "codex_identity_mode"
	CodexIdentityAPIKeyIDKey      = "codex_identity_api_key_id"
	CodexIdentityNamespaceKey     = "codex_identity_namespace"
	CodexIdentityRevisionKey      = "codex_identity_revision"
	CodexIdentityDiagnosticKey    = "codex_identity_diagnostic"
	CodexIdentityHistoryKey       = "codex_identity_experiment_history"
	CodexIdentityIsolated         = "isolated"
	CodexIdentityPreserveClient   = "preserve_client"
	codexIdentityPolicyContextKey = "codex_identity_effective_policy"
)

type CodexIdentityPolicy struct {
	Mode      string `json:"mode"`
	Reason    string `json:"reason"`
	Namespace string `json:"namespace,omitempty"`
	Revision  string `json:"revision,omitempty"`
	APIKeyID  int64  `json:"api_key_id,omitempty"`
	UserID    int64  `json:"user_id,omitempty"`
}

// CodexIdentityBindingRepository must read authoritative shared storage, never
// a scheduler snapshot. Its absence disables experimental authorization.
type CodexIdentityBindingRepository interface {
	ValidateCodexIdentityBinding(context.Context, *Account, int64, int64) error
}

func codexIdentityString(a *Account, key string) string {
	if a == nil {
		return ""
	}
	v, _ := a.Extra[key].(string)
	return v
}

func codexIdentityHasHistory(a *Account) bool {
	return a != nil && (a.Extra[CodexIdentityHistoryKey] == true || codexIdentityString(a, CodexIdentityModeKey) == CodexIdentityPreserveClient)
}

func CodexIdentityNamespace(a *Account) string {
	if a != nil && a.IsOpenAIAgentIdentity() {
		return "" // Agent task/runtime credentials have a different lifecycle.
	}
	ns := codexAccountIdentityNamespace(a)
	// A row-local fingerprint seed isolates the default path, but cannot prove
	// duplicate imports refer to the same credential. Never authorize raw IDs
	// from it. Setup tokens can instead use their stable bearer fingerprint.
	if strings.HasPrefix(ns, "seed:") {
		if a.Type != AccountTypeSetupToken || strings.TrimSpace(a.GetOpenAIAccessToken()) == "" {
			return ""
		}
		sum := sha256.Sum256([]byte("openai-setup-token:" + strings.TrimSpace(a.GetOpenAIAccessToken())))
		ns = fmt.Sprintf("setup-token:%x", sum[:16])
	}
	if ns == "" {
		return ""
	}
	sum := sha256.Sum256([]byte("codex-identity-policy:v1:" + ns))
	return fmt.Sprintf("%x", sum[:])
}

func CodexIdentityAPIKeyID(a *Account) int64 {
	if a == nil {
		return 0
	}
	switch n := a.Extra[CodexIdentityAPIKeyIDKey].(type) {
	case int:
		if n > 0 {
			return int64(n)
		}
	case int64:
		if n > 0 {
			return n
		}
	case float64:
		if n > 0 && n < math.MaxInt64 && n == math.Trunc(n) {
			return int64(n)
		}
	case json.Number:
		if id, err := n.Int64(); err == nil && id > 0 {
			return id
		}
	}
	return 0
}

func codexIdentityError(code, message string) error {
	return infraerrors.BadRequest(code, message)
}

// NormalizeCodexIdentityConfig never accepts namespace/revision from a caller.
// A random revision also prevents disable/re-enable from resurrecting old grants.
func NormalizeCodexIdentityConfig(a, previous *Account) error {
	if a == nil {
		return nil
	}
	mode := codexIdentityString(a, CodexIdentityModeKey)
	if raw, exists := a.Extra[CodexIdentityModeKey]; exists && raw != CodexIdentityIsolated && raw != CodexIdentityPreserveClient {
		return codexIdentityError("CODEX_IDENTITY_INVALID_MODE", "codex_identity_mode must be isolated or preserve_client")
	}
	if mode == "" {
		mode = CodexIdentityIsolated
	}
	managed := false
	for key := range a.Extra {
		if strings.HasPrefix(key, "codex_identity_") {
			managed = true
		}
	}
	if !managed && codexIdentityString(previous, CodexIdentityRevisionKey) == "" {
		return nil
	}
	extra := make(map[string]any, len(a.Extra)+4)
	for k, v := range a.Extra {
		extra[k] = v
	}
	a.Extra = extra
	namespace := CodexIdentityNamespace(a)
	oldNamespace := codexIdentityString(previous, CodexIdentityNamespaceKey)
	authMode := func(account *Account) string {
		if account == nil {
			return "oauth"
		}
		mode := strings.ToLower(strings.TrimSpace(account.GetCredential("auth_mode")))
		if mode == "" {
			return "oauth"
		}
		return mode
	}
	credentialChanged := previous != nil && (previous.Platform != a.Platform || previous.Type != a.Type || previous.IsShadow() != a.IsShadow() || oldNamespace != namespace || authMode(previous) != authMode(a))
	delete(extra, CodexIdentityDiagnosticKey)
	if credentialChanged && codexIdentityString(previous, CodexIdentityModeKey) == CodexIdentityPreserveClient {
		mode = CodexIdentityIsolated
		extra[CodexIdentityDiagnosticKey] = "namespace_changed: experiment revoked; verify credentials and explicitly enable again"
	} else if mode == CodexIdentityIsolated && previous != nil {
		if reason := codexIdentityString(previous, CodexIdentityDiagnosticKey); reason != "" {
			extra[CodexIdentityDiagnosticKey] = reason
		}
	}
	keyID := CodexIdentityAPIKeyID(a)
	if mode == CodexIdentityPreserveClient {
		if !a.IsOpenAIOAuthLike() || a.IsShadow() || a.IsOpenAIAgentIdentity() {
			return codexIdentityError("CODEX_IDENTITY_UNSUPPORTED_ACCOUNT", "preserve_client requires an OpenAI OAuth/token credential account; Agent Identity and shadows are unsupported")
		}
		if namespace == "" {
			return codexIdentityError("CODEX_IDENTITY_NAMESPACE_REQUIRED", "preserve_client requires a stable credential namespace")
		}
		if keyID == 0 {
			return codexIdentityError("CODEX_IDENTITY_API_KEY_REQUIRED", "preserve_client requires a positive integer codex_identity_api_key_id")
		}
		if a.GetCodexFingerprintMode() != codexFingerprintOff {
			return codexIdentityError("CODEX_IDENTITY_FINGERPRINT_CONFLICT", "preserve_client conflicts with device/session/full fingerprint convergence; set codex_fingerprint_mode to off")
		}
	}
	extra[CodexIdentityModeKey] = mode
	extra[CodexIdentityNamespaceKey] = namespace
	if mode == CodexIdentityIsolated {
		delete(extra, CodexIdentityAPIKeyIDKey)
		keyID = 0
	} else {
		extra[CodexIdentityAPIKeyIDKey] = keyID
	}
	revision := codexIdentityString(previous, CodexIdentityRevisionKey)
	if revision == "" || mode != codexIdentityString(previous, CodexIdentityModeKey) || keyID != CodexIdentityAPIKeyID(previous) || credentialChanged || a.GetCodexFingerprintMode() != previous.GetCodexFingerprintMode() || codexIdentityString(a, codexFingerprintSeedExtraKey) != codexIdentityString(previous, codexFingerprintSeedExtraKey) {
		revision = uuid.NewString()
	}
	extra[CodexIdentityRevisionKey] = revision
	delete(extra, CodexIdentityHistoryKey)
	if mode == CodexIdentityPreserveClient || codexIdentityHasHistory(previous) {
		extra[CodexIdentityHistoryKey] = true
	}
	return nil
}

// Patch/bulk paths cannot atomically validate a complete identity configuration.
// Use the full account save API. Server-owned fields are never accepted here.
func ValidateCodexIdentityExtraPatch(extra map[string]any) error {
	for key := range extra {
		if strings.HasPrefix(key, "codex_identity_") {
			return codexIdentityError("CODEX_IDENTITY_FULL_SAVE_REQUIRED", "identity configuration requires an individual full account save")
		}
	}
	return nil
}

func normalizeAndValidateCodexIdentitySave(ctx context.Context, repo AccountRepository, a, previous *Account) error {
	if err := NormalizeCodexIdentityConfig(a, previous); err != nil {
		return err
	}
	if codexIdentityString(a, CodexIdentityModeKey) != CodexIdentityPreserveClient {
		return nil
	}
	if previous != nil &&
		codexIdentityString(previous, CodexIdentityModeKey) == CodexIdentityPreserveClient &&
		CodexIdentityAPIKeyID(previous) == CodexIdentityAPIKeyID(a) &&
		codexIdentityString(previous, CodexIdentityRevisionKey) == codexIdentityString(a, CodexIdentityRevisionKey) {
		// Ordinary account edits must remain possible if the bound key later
		// expires, is disabled, or exhausts quota. Actual requests still recheck
		// the grant immediately before sending.
		return nil
	}
	validator, ok := repo.(CodexIdentityBindingRepository)
	if !ok {
		return codexIdentityError("CODEX_IDENTITY_VALIDATION_UNAVAILABLE", "repository cannot validate the identity experiment binding")
	}
	return validator.ValidateCodexIdentityBinding(ctx, a, CodexIdentityAPIKeyID(a), 0)
}

func EffectiveCodexIdentityPolicy(c *gin.Context, account *Account) CodexIdentityPolicy {
	if c != nil {
		if value, ok := c.Get(codexIdentityPolicyContextKey); ok {
			if p, ok := value.(CodexIdentityPolicy); ok {
				return p
			}
		}
	}
	return CodexIdentityPolicy{Mode: CodexIdentityIsolated, Reason: "internal_request"}
}

func preserveCodexClientIdentity(a *Account, apiKeyID int64) bool {
	return a != nil && a.codexIdentityGrant != nil && apiKeyID > 0 && a.codexIdentityGrant.APIKeyID == apiKeyID && a.codexIdentityGrant.Mode == CodexIdentityPreserveClient
}

func (s *OpenAIGatewayService) stageCodexIdentityPolicy(ctx context.Context, c *gin.Context, selected, source *Account) *Account {
	p := CodexIdentityPolicy{Mode: CodexIdentityIsolated, Reason: "default"}
	if c != nil {
		keyValue, _ := c.Get("api_key")
		if key, ok := keyValue.(*APIKey); ok && key != nil {
			p.APIKeyID, p.UserID = key.ID, key.UserID
		}
	}
	defer func() {
		if c != nil {
			c.Set(codexIdentityPolicyContextKey, p)
		}
	}()
	if source == nil {
		return source
	}
	// No private grant may survive a new attempt or repository snapshot.
	if source.codexIdentityGrant != nil {
		copySource := *source
		copySource.codexIdentityGrant = nil
		source = &copySource
	}
	p.Namespace = CodexIdentityNamespace(source)
	p.Revision = codexIdentityString(selected, CodexIdentityRevisionKey)
	if codexIdentityString(selected, CodexIdentityModeKey) != CodexIdentityPreserveClient {
		return source
	}
	p.Reason = "internal_request"
	keyID := getAPIKeyIDFromContext(c)
	if keyID <= 0 {
		return source
	}
	p.Reason = "nonbound_api_key"
	if keyID != CodexIdentityAPIKeyID(selected) {
		return source
	}
	p.Reason = "principal_unavailable"
	if p.UserID <= 0 {
		return source
	}
	p.Reason = "binding_validation_unavailable"
	validator, ok := s.accountRepo.(CodexIdentityBindingRepository)
	if !ok {
		return source
	}
	// Re-read even when a scheduler snapshot was supplied.
	fresh, err := s.accountRepo.GetByID(ctx, selected.ID)
	if err != nil || fresh == nil {
		return source
	}
	p.Reason = "policy_changed"
	if codexIdentityString(fresh, CodexIdentityRevisionKey) != p.Revision || codexIdentityString(fresh, CodexIdentityModeKey) != CodexIdentityPreserveClient {
		return source
	}
	p.Reason = "namespace_changed"
	if fresh.IsShadow() || CodexIdentityNamespace(fresh) != p.Namespace || codexIdentityString(fresh, CodexIdentityNamespaceKey) != p.Namespace || p.Namespace == "" {
		return source
	}
	p.Reason = "fingerprint_conflict"
	if selected.GetCodexFingerprintMode() != codexFingerprintOff || source.GetCodexFingerprintMode() != codexFingerprintOff || fresh.GetCodexFingerprintMode() != codexFingerprintOff {
		return source
	}
	p.Reason = "binding_conflict_or_key_unavailable"
	if p.Revision == "" || CodexIdentityAPIKeyID(fresh) != keyID || validator.ValidateCodexIdentityBinding(ctx, fresh, keyID, p.UserID) != nil {
		return source
	}
	p.Mode, p.Reason, p.APIKeyID = CodexIdentityPreserveClient, "pinned_api_key", keyID
	copySource := *source
	source = &copySource
	source.codexIdentityGrant = &p
	return source
}

func rejectCodexIdentityRequest(c *gin.Context, code, message string) error {
	err := codexIdentityError(code, message)
	if c != nil && c.Writer != nil && c.Writer.Written() && GetOpenAIClientTransport(c) == OpenAIClientTransportWS {
		return NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, message, err)
	}
	if c != nil && c.Writer != nil && !c.Writer.Written() {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{"type": "invalid_request_error", "code": code, "message": message}})
	}
	return err
}

// Identity authorization failures are client policy errors, never transport
// failures that should unschedule an account or authorize a failover replay.
func respondCodexIdentityRequestError(c *gin.Context, err error) bool {
	code := infraerrors.Reason(err)
	if !strings.HasPrefix(code, "CODEX_IDENTITY_") {
		return false
	}
	rejectCodexIdentityRequest(c, code, infraerrors.Message(err))
	return true
}

func (s *OpenAIGatewayService) guardCodexIdentityRequest(c *gin.Context, a *Account, body []byte, transport string) error {
	p := EffectiveCodexIdentityPolicy(c, a)
	if err := s.validateCodexIdentityContinuation(c, a, "response", gjson.GetBytes(body, "previous_response_id").String()); err != nil {
		return err
	}
	if c != nil {
		if c.Request != nil {
			for _, ticket := range c.Request.Header.Values(openAIWSTurnStateHeader) {
				if err := s.validateCodexIdentityContinuation(c, a, "ticket", ticket); err != nil {
					return err
				}
			}
		}
	}
	if err := s.validateCodexIdentityContinuation(c, a, "ticket", gjson.GetBytes(body, "client_metadata.x-codex-turn-state").String()); err != nil {
		return err
	}
	if p.Mode != CodexIdentityPreserveClient {
		return nil
	}
	if transport != "http" || isOpenAIResponsesCompactPath(c) || isOpenAICompatMessagesBridgeContext(c) || isOpenAICompatMessagesBridgeBody(body) {
		return rejectCodexIdentityRequest(c, "CODEX_IDENTITY_TRANSPORT_UNSUPPORTED", "preserve_client requires native Responses HTTP; disable the experiment for WS, compact and bridges")
	}
	if s.pluginManager.ShouldRouteOpenAIOAuth(a) {
		return rejectCodexIdentityRequest(c, "CODEX_IDENTITY_PLUGIN_UNSUPPORTED", "preserve_client cannot verify the selected plugin's final request identity; disable the experiment or this account's plugin binding")
	}
	return nil
}

// At the end of the HTTP builder restore only client identity headers. Body
// transforms remain in place and the canonical protocol identity is untouched.
func restoreCodexClientIdentityHeaders(c *gin.Context, a *Account, h http.Header, cacheKey string) {
	if !preserveCodexClientIdentity(codexAccountIdentitySource(c, a), getAPIKeyIDFromContext(c)) || c == nil || c.Request == nil {
		return
	}
	names := []string{"session_id", "conversation_id", openAIWSTurnMetadataHeader}
	for _, field := range codexAccountIdentityFields {
		names = append(names, field.name)
	}
	for _, name := range names {
		h.Del(name)
		for _, value := range c.Request.Header.Values(name) {
			h.Add(name, value)
		}
	}
	if h.Get("session_id") == "" && cacheKey != "" {
		h.Set("session_id", cacheKey)
	}
	if h.Get("conversation_id") == "" && cacheKey != "" {
		h.Set("conversation_id", cacheKey)
	}
}

type codexIdentityRequestKey struct{}
type codexIdentityContinuationRequestKeys struct{}

func stampCodexIdentityRequest(c *gin.Context, req *http.Request, body []byte) *http.Request {
	p := EffectiveCodexIdentityPolicy(c, nil)
	if p.Mode == CodexIdentityPreserveClient {
		var keys []string
		add := func(kind, value string) {
			if value != "" {
				keys = append(keys, OpenAIIdentityContinuationKey(getOpenAIGroupIDFromContext(c), kind, value))
			}
		}
		add("response", gjson.GetBytes(body, "previous_response_id").String())
		add("ticket", gjson.GetBytes(body, "client_metadata.x-codex-turn-state").String())
		for _, ticket := range req.Header.Values(openAIWSTurnStateHeader) {
			add("ticket", ticket)
		}
		ctx := context.WithValue(req.Context(), codexIdentityRequestKey{}, p)
		ctx = context.WithValue(ctx, codexIdentityContinuationRequestKeys{}, keys)
		return req.WithContext(ctx)
	}
	return req
}

func (s *OpenAIGatewayService) validateCodexIdentityBeforeSend(req *http.Request, account *Account) error {
	p, ok := req.Context().Value(codexIdentityRequestKey{}).(CodexIdentityPolicy)
	if !ok {
		return nil
	}
	if s.pluginManager.ShouldRouteOpenAIOAuth(account) {
		return codexIdentityError("CODEX_IDENTITY_PLUGIN_UNSUPPORTED", "preserve_client does not support plugin transport")
	}
	validator, ok := s.accountRepo.(CodexIdentityBindingRepository)
	if !ok {
		return codexIdentityError("CODEX_IDENTITY_POLICY_CHANGED", "Identity validation unavailable; start a new session")
	}
	fresh, err := s.accountRepo.GetByID(req.Context(), account.ID)
	if err != nil {
		return codexIdentityError("CODEX_IDENTITY_VALIDATION_UNAVAILABLE", "Cannot validate current identity policy; start a new session")
	}
	if fresh == nil || codexIdentityString(fresh, CodexIdentityModeKey) != CodexIdentityPreserveClient || codexIdentityString(fresh, CodexIdentityRevisionKey) != p.Revision || CodexIdentityNamespace(fresh) != p.Namespace || codexIdentityString(fresh, CodexIdentityNamespaceKey) != p.Namespace || CodexIdentityAPIKeyID(fresh) != p.APIKeyID || fresh.GetCodexFingerprintMode() != codexFingerprintOff {
		return codexIdentityError("CODEX_IDENTITY_POLICY_CHANGED", "Identity policy changed before send; start a new session")
	}
	if err := validator.ValidateCodexIdentityBinding(req.Context(), fresh, p.APIKeyID, p.UserID); err != nil {
		if strings.HasPrefix(infraerrors.Reason(err), "CODEX_IDENTITY_") {
			return err
		}
		return codexIdentityError("CODEX_IDENTITY_VALIDATION_UNAVAILABLE", "Cannot validate current identity binding; start a new session")
	}
	keys, _ := req.Context().Value(codexIdentityContinuationRequestKeys{}).([]string)
	cache, available := s.cache.(OpenAIIdentityContinuationCache)
	for _, key := range keys {
		if !available {
			return codexIdentityError("CODEX_IDENTITY_CONTINUATION_UNSUPPORTED", "Shared identity continuation metadata is unavailable; start a new session")
		}
		readCtx, cancel := context.WithTimeout(req.Context(), openAIWSStateStoreRedisTimeout)
		metadata, err := cache.GetOpenAIIdentityContinuation(readCtx, key)
		cancel()
		if err != nil {
			return codexIdentityError("CODEX_IDENTITY_CONTINUATION_UNAVAILABLE", "Cannot validate shared continuation identity before send; start a new session")
		}
		if !codexIdentityContinuationMatches(metadata, p) {
			return codexIdentityError("CODEX_IDENTITY_CONTINUATION_MISMATCH", "Continuation identity changed before send; start a new session")
		}
	}
	return nil
}

func stageCodexClientIdentityBody(c *gin.Context, body []byte) {
	if c == nil {
		return
	}
	c.Set("codex_identity_client_body", map[string]string{
		"client_metadata":  gjson.GetBytes(body, "client_metadata").Raw,
		"prompt_cache_key": gjson.GetBytes(body, "prompt_cache_key").Raw,
	})
}

func restoreCodexClientIdentityBody(c *gin.Context, a *Account, body []byte) ([]byte, error) {
	if !preserveCodexClientIdentity(codexAccountIdentitySource(c, a), getAPIKeyIDFromContext(c)) {
		return body, nil
	}
	v, ok := c.Get("codex_identity_client_body")
	if !ok {
		return body, nil
	}
	fields, _ := v.(map[string]string)
	var err error
	for _, name := range []string{"client_metadata", "prompt_cache_key"} {
		if raw := fields[name]; raw != "" {
			body, err = sjson.SetRawBytes(body, name, []byte(raw))
		} else if name == "client_metadata" {
			body, err = sjson.DeleteBytes(body, name)
		}
		if err != nil {
			return nil, err
		}
	}
	return body, nil
}
