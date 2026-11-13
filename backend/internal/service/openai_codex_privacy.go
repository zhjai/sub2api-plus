package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"regexp"
	"strings"

	openaiwsv2 "github.com/Wei-Shaw/sub2api/internal/service/openai_ws_v2"
	coderws "github.com/coder/websocket"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const CodexPrivacyModeKey = "codex_privacy_mode"
const CodexPrivacyRandomized = "randomized"

func codexPrivacyEnabled(a *Account) bool {
	return a != nil && a.Platform == PlatformOpenAI && codexIdentityString(a, CodexPrivacyModeKey) == CodexPrivacyRandomized
}

// A missing seed is an error, never an invitation to generate a new device on
// every request. The existing protected seed lifecycle persists it on save.
func validateCodexPrivacyConfig(a *Account) error {
	if a == nil {
		return nil
	}
	mode, exists := a.Extra[CodexPrivacyModeKey]
	if !exists || mode == "off" {
		return nil
	}
	if mode != CodexPrivacyRandomized {
		return codexIdentityError("CODEX_PRIVACY_INVALID_MODE", "codex_privacy_mode must be off or randomized")
	}
	if a.Platform != PlatformOpenAI || (a.Type != AccountTypeOAuth && a.Type != AccountTypeSetupToken && a.Type != AccountTypeAPIKey) || a.IsShadow() || a.IsOpenAIAgentIdentity() {
		return codexIdentityError("CODEX_PRIVACY_UNSUPPORTED_ACCOUNT", "randomized metadata requires an OpenAI API key, OAuth or token credential account")
	}
	if codexIdentityString(a, CodexIdentityModeKey) == CodexIdentityPreserveClient || codexFingerprintModeFromExtra(a.Extra) != codexFingerprintOff {
		return codexIdentityError("CODEX_PRIVACY_IDENTITY_CONFLICT", "randomized metadata conflicts with preserve_client and device/session/full convergence; use isolated identity and fingerprint mode off")
	}
	return nil
}

type codexPrivacyIDValue struct {
	kind  string
	value string
}

type codexPrivacyProjection struct {
	seed        string
	principalID int64
	// Only HTTP finalization uses these request-local sets. Stored context state
	// is immutable; every repeated finalization gets its own writable copy.
	knownIDs     map[codexPrivacyIDValue]struct{}
	projectedIDs map[codexPrivacyIDValue]struct{}
}

func newCodexPrivacyProjection(a *Account, principalIDs ...int64) (*codexPrivacyProjection, error) {
	if !codexPrivacyEnabled(a) {
		return nil, nil
	}
	if err := validateCodexPrivacyConfig(a); err != nil {
		return nil, err
	}
	seed, ok := codexFingerprintSeed(a.Extra)
	if !ok {
		return nil, codexIdentityError("CODEX_PRIVACY_SEED_REQUIRED", "Save the account again to initialize its protected random metadata seed")
	}
	p := &codexPrivacyProjection{seed: seed}
	if len(principalIDs) > 0 {
		p.principalID = principalIDs[0]
	}
	return p, nil
}

func codexPrivacyIDKind(name string) string {
	switch strings.ToLower(name) {
	case "installation_id", "installation-id", "x-codex-installation-id", "device_id", "device-id", "x-device-id", "x-oai-device-id":
		return "device"
	case "session_id", "session-id", "x-session-id", "x-codex-session-id", "x-session-affinity", "x-opencode-session", "prompt_cache_key":
		return "session"
	case "thread_id", "thread-id", "x-codex-thread-id", "parent_thread_id", "parent-thread-id":
		return "thread"
	case "conversation_id", "conversation-id", "x-conversation-id", "x-codex-conversation-id":
		return "session"
	case "turn_id", "turn-id", "x-codex-turn-id", "root_turn_id", "root-turn-id":
		return "turn"
	case "context_window_id", "context-window-id":
		return "context_window"
	case "window_id", "window-id", "x-codex-window-id":
		return "window"
	case "request_id", "request-id", "x-request-id", "x-client-request-id", "client_request_id":
		return "request"
	}
	return ""
}

func (p *codexPrivacyProjection) id(kind, raw string) string {
	if raw == "" {
		return ""
	}
	if _, known := p.knownIDs[codexPrivacyIDValue{kind, raw}]; known {
		return raw
	}
	if kind == "device" {
		raw = "account-device" // never reuse an imported real device_id
	}
	mac := hmac.New(sha256.New, []byte(p.seed))
	if kind != "device" {
		_, _ = fmt.Fprintf(mac, "principal:%d\x00", p.principalID)
	}
	_, _ = mac.Write([]byte("codex-protocol-privacy:v1\x00" + kind + "\x00" + raw))
	projected := deriveStableUUIDv4(hex.EncodeToString(mac.Sum(nil)))
	if p.projectedIDs != nil {
		p.projectedIDs[codexPrivacyIDValue{kind, projected}] = struct{}{}
	}
	return projected
}

// Only protocol fields are accepted here. Free-form client environment objects
// (cwd, host, OS, architecture, paths, telemetry) must not survive under a new key.
// Opaque server-issued state is deliberately preserved for continuation.
func (p *codexPrivacyProjection) metadata(value any) map[string]any {
	return p.metadataAtDepth(value, 0)
}

func (p *codexPrivacyProjection) metadataAtDepth(value any, depth int) map[string]any {
	if depth >= 4 {
		return map[string]any{}
	}
	input, _ := value.(map[string]any)
	if text, ok := value.(string); ok {
		_ = json.Unmarshal([]byte(text), &input)
	}
	output := make(map[string]any)
	for key, value := range input {
		if kind := codexPrivacyIDKind(key); kind != "" {
			if raw, ok := value.(string); ok && raw != "" {
				output[key] = p.id(kind, raw)
			}
			continue
		}
		switch strings.ToLower(key) {
		case openAIWSTurnStateHeader:
			if raw, ok := value.(string); ok {
				output[key] = raw
			}
		case openAIWSTurnMetadataHeader:
			clean := p.metadataAtDepth(value, depth+1)
			if _, ok := value.(string); ok {
				encoded, _ := json.Marshal(clean)
				output[key] = string(encoded)
			} else {
				output[key] = clean
			}
		case "turn_started_at_unix_ms":
			if _, ok := value.(float64); ok {
				output[key] = value
			}
		case "auto_review_enabled", "node_repl_auto_review_required", "node_repl_disabled", "analytics_enabled":
			if _, ok := value.(bool); ok {
				output[key] = value
			}
		case "request_kind", "thread_source", "history_initialization", "turn_trigger", "sandbox", "sandbox_mode", "model", "reasoning_effort":
			if raw, ok := value.(string); ok && len(raw) <= 128 && codexPrivacyProtocolToken.MatchString(raw) {
				output[key] = raw
			}
		}
	}
	return output
}

var codexPrivacyUUIDPattern = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)
var codexPrivacyProtocolToken = regexp.MustCompile(`^[a-zA-Z0-9_.:-]*$`)

func (p *codexPrivacyProjection) headers(h http.Header) {
	for key, values := range h {
		name := strings.ToLower(key)
		if codexPrivacyPrivateHeader(name) {
			delete(h, key)
			continue
		}
		if name == openAIWSTurnMetadataHeader {
			var metadata any
			_ = json.Unmarshal([]byte(strings.Join(values, "")), &metadata)
			encoded, _ := json.Marshal(p.metadata(metadata))
			h[key] = []string{string(encoded)}
			continue
		}
		// Credentials, provider IDs, negotiated WS keys and server-issued routing
		// tickets are not client identity. Rewriting them would break requests.
		if codexPrivacyOpaqueHeader(name) {
			continue
		}
		kind := codexPrivacyIDKind(name)
		// Unknown administrator-defined headers may contain opaque UUID API keys.
		// Only protocol client/device identity carriers may be pseudonymized.
		if kind == "" && !codexPrivacyUUIDIdentityHeader(name) {
			continue
		}
		clean := make([]string, len(values))
		for i, raw := range values {
			if kind != "" {
				clean[i] = p.id(kind, raw)
			} else {
				clean[i] = codexPrivacyUUIDPattern.ReplaceAllStringFunc(raw, func(id string) string { return p.id("header:"+name, id) })
			}
		}
		h[key] = clean
	}
	// Fixed generic public client identity, independent of the local OS, custom
	// UA, original locale and administrator header overrides.
	// Overrides may retain raw wire casing. Header.Set alone leaves those map
	// entries intact and would serialize conflicting identities on the wire.
	for _, name := range []string{"User-Agent", "Originator", "Version"} {
		deleteOpenAIHeaderEqualFold(h, name)
	}
	h.Set("User-Agent", buildCodexCLIUserAgent(CodexCanonicalClientVersion()))
	h.Set("Originator", "codex_cli_rs")
	h.Set("Version", CodexCanonicalClientVersion())
	enforceCodexAcceptLanguage(h)
}

func codexPrivacyUUIDIdentityHeader(name string) bool {
	for _, word := range []string{"auth", "token", "secret", "key", "signature", "credential", "ticket", "cookie"} {
		if strings.Contains(name, word) {
			return false
		}
	}
	for _, prefix := range []string{"x-client-", "x-codex-", "x-device-", "x-opencode-"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func codexPrivacyPrivateHeader(name string) bool {
	for _, prefix := range []string{"x-stainless-", "sec-ch-", "x-forwarded-", "x-client-os", "x-client-arch", "x-client-host", "x-codex-os", "x-codex-cwd", "x-codex-host", "x-local-"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	switch name {
	case "x-client-platform", "x-client-architecture", "x-client-cwd", "x-client-working-directory",
		"x-codex-workdir", "x-codex-working-directory", "x-opencode-cwd", "x-opencode-hostname", "x-opencode-os", "x-opencode-arch":
		return true
	case "forwarded", "via", "x-real-ip", "traceparent", "tracestate", "baggage", "sentry-trace", "referer", "x-codex-environment", "x-codex-home", "x-codex-platform", "x-codex-architecture":
		return true
	}
	return false
}

func codexPrivacyOpaqueHeader(name string) bool {
	switch name {
	case "authorization", "proxy-authorization", "cookie", "x-api-key", "api-key", "openai-organization", "openai-project", "chatgpt-account-id", "chatgpt-user-id", openAIWSTurnStateHeader, openAICodexRoutingHintHeader, "sec-websocket-key", "sec-websocket-protocol":
		return true
	}
	return false
}

func (p *codexPrivacyProjection) body(body []byte) ([]byte, error) {
	if len(body) == 0 {
		return body, nil
	}
	if !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		return nil, fmt.Errorf("Codex metadata privacy requires a JSON object")
	}
	// Rewrite only small protocol subobjects, never decode or walk input/tools.
	for _, prefix := range []string{"", "response.", "session."} {
		if prefix != "" && !gjson.GetBytes(body, strings.TrimSuffix(prefix, ".")).IsObject() {
			continue
		}
		for _, name := range []string{"client_metadata", openAIWSTurnMetadataHeader} {
			path := prefix + name
			if field := gjson.GetBytes(body, path); field.Exists() {
				clean := p.metadata(field.Value())
				var value any = clean
				if field.Type == gjson.String {
					encoded, _ := json.Marshal(clean)
					value = string(encoded)
				}
				var err error
				body, err = sjson.SetBytes(body, path, value)
				if err != nil {
					return nil, err
				}
			}
		}
		root := gjson.GetBytes(body, strings.TrimSuffix(prefix, "."))
		if prefix == "" {
			root = gjson.ParseBytes(body)
		}
		var rewriteErr error
		root.ForEach(func(key, value gjson.Result) bool {
			if kind := codexPrivacyIDKind(key.String()); kind != "" && value.Type == gjson.String {
				body, rewriteErr = sjson.SetBytes(body, prefix+key.String(), p.id(kind, value.String()))
			}
			return rewriteErr == nil
		})
		if rewriteErr != nil {
			return nil, rewriteErr
		}
	}
	return body, nil
}

type codexPrivacyPrincipalKey struct{}

func withCodexPrivacyPrincipal(ctx context.Context, ids ...int64) context.Context {
	if len(ids) > 0 {
		return context.WithValue(ctx, codexPrivacyPrincipalKey{}, ids[0])
	}
	return ctx
}
func codexPrivacyPrincipal(ctx context.Context) int64 {
	id, _ := ctx.Value(codexPrivacyPrincipalKey{}).(int64)
	return id
}

type codexPrivacyAppliedKey struct{}
type codexPrivacyAppliedState struct {
	marker       string
	projectedIDs map[codexPrivacyIDValue]struct{}
}
type codexPrivacyWSKey struct{}
type codexPrivacyHTTPSourceKey struct{}
type codexPrivacyHTTPSource struct {
	selectedID int64
	credential *Account
}

func stampCodexPrivacySource(req *http.Request, selected, credential *Account, principalIDs ...int64) *http.Request {
	return req.WithContext(withCodexPrivacyHTTPSource(req.Context(), selected, credential, principalIDs...))
}

func withCodexPrivacyHTTPSource(ctx context.Context, selected, credential *Account, principalIDs ...int64) context.Context {
	ctx = withCodexPrivacyPrincipal(ctx, principalIDs...)
	if selected == nil || credential == nil {
		return ctx
	}
	return context.WithValue(ctx, codexPrivacyHTTPSourceKey{}, codexPrivacyHTTPSource{selected.ID, credential})
}

// Final HTTP boundary: run after header overrides and all protocol transforms.
// The returned request owns a new replayable body; caller snapshots stay intact.
func applyCodexPrivacyBeforeSend(req *http.Request, a *Account) (*http.Request, error) {
	if source, ok := req.Context().Value(codexPrivacyHTTPSourceKey{}).(codexPrivacyHTTPSource); ok && a != nil && source.selectedID == a.ID {
		a = source.credential
	}
	p, err := newCodexPrivacyProjection(a, codexPrivacyPrincipal(req.Context()))
	if err != nil || p == nil {
		return req, err
	}
	// This trusted attachment body is produced from validated image bytes by
	// our BPS uploader. It carries no client protocol metadata or filenames;
	// preserve pixels and framing, while applying the same header projection.
	if trusted, _ := req.Context().Value(excelBPSAttachmentRequestKey{}).(bool); trusted && a.IsExcelOAuth() &&
		req.URL != nil && req.URL.Scheme == "https" && req.URL.Host == "bps.openai.com" && req.URL.Path == "/basispoints/api/attachments" &&
		strings.HasPrefix(req.Header.Get("Content-Type"), "multipart/form-data;") {
		next := req.Clone(req.Context())
		p.headers(next.Header)
		return next, nil
	}
	marker := fmt.Sprintf("%s:%d", p.seed, p.principalID)
	applied, _ := req.Context().Value(codexPrivacyAppliedKey{}).(*codexPrivacyAppliedState)
	p.projectedIDs = make(map[codexPrivacyIDValue]struct{})
	if applied != nil && applied.marker == marker {
		p.knownIDs = applied.projectedIDs
		for value := range applied.projectedIDs {
			p.projectedIDs[value] = struct{}{}
		}
	}
	// Recheck even a previously projected request: a later component may have
	// appended protocol metadata. Known outputs are accepted only for this request
	// and identity kind, never trusted from arbitrary client UUIDs or global state.
	var raw []byte
	if req.Body != nil && req.Body != http.NoBody {
		if req.GetBody == nil {
			return nil, fmt.Errorf("Codex metadata privacy requires a replayable request body")
		}
		copyBody, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		raw, err = io.ReadAll(copyBody)
		_ = copyBody.Close()
		if err != nil {
			return nil, err
		}
	}
	clean, err := p.body(raw)
	if err != nil {
		return nil, err
	}
	next := req.Clone(context.WithValue(req.Context(), codexPrivacyAppliedKey{}, &codexPrivacyAppliedState{
		marker: marker, projectedIDs: p.projectedIDs,
	}))
	p.headers(next.Header)
	if applied != nil && applied.marker == marker && bytes.Equal(raw, clean) && reflect.DeepEqual(req.Header, next.Header) {
		return req, nil
	}
	if req.Body != nil {
		_ = req.Body.Close()
		next.Body = io.NopCloser(bytes.NewReader(clean))
		next.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(clean)), nil }
		next.ContentLength = int64(len(clean))
		next.Header.Del("Content-Length")
	}
	return next, nil
}

func withCodexPrivacyWS(ctx context.Context, a *Account, principalIDs ...int64) context.Context {
	ctx = withCodexPrivacyPrincipal(ctx, principalIDs...)
	return context.WithValue(ctx, codexPrivacyWSKey{}, a)
}

func codexPrivacyWSValue(ctx context.Context, value any) (any, error) {
	a, _ := ctx.Value(codexPrivacyWSKey{}).(*Account)
	p, err := newCodexPrivacyProjection(a, codexPrivacyPrincipal(ctx))
	if err != nil || p == nil {
		return value, err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if !gjson.ParseBytes(raw).IsObject() {
		return value, nil
	}
	clean, err := p.body(raw)
	return json.RawMessage(clean), err
}

// Dedicated passthrough sockets do not use the pooled lease writer. Wrap their
// final upstream writer too, including the first frame and interleaved frames.
type codexPrivacyFrameConn struct {
	openaiwsv2.FrameConn
	projection *codexPrivacyProjection
}

func (c *codexPrivacyFrameConn) WriteFrame(ctx context.Context, kind coderws.MessageType, raw []byte) error {
	if kind != coderws.MessageText && (kind != coderws.MessageBinary || !gjson.ValidBytes(raw) || !gjson.ParseBytes(raw).IsObject()) {
		return c.FrameConn.WriteFrame(ctx, kind, raw)
	}
	if gjson.ValidBytes(raw) && !gjson.ParseBytes(raw).IsObject() {
		return c.FrameConn.WriteFrame(ctx, kind, raw)
	}
	clean, err := c.projection.body(raw)
	if err != nil {
		return err
	}
	return c.FrameConn.WriteFrame(ctx, kind, clean)
}
