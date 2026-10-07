package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

type codexOutboundDiagnosticKey struct{}
type codexDiagnosticSourceKey struct{}

var codexDiagnosticSalt = uuid.NewString()

type codexOutboundDiagnostic struct {
	accountID           int64
	credentialNamespace string
	source              string
	workload            string
	mode                string
	policyReason        string
	fingerprint         string
	model               string
	effort              string
	tier                string
	bodyAvailable       bool
	ids                 map[string]string
	sends               atomic.Int64
}

func withCodexDiagnosticSource(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, codexDiagnosticSourceKey{}, source)
}

func codexDiagnosticDigest(value string) string {
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(codexDiagnosticSalt + "\x00" + value))
	return fmt.Sprintf("%x", sum[:8])
}

// Only format/presence/equality and process-salted identity digests leave this
// function. Reopening GetBody never consumes or mutates the outbound body.
func withCodexOutboundDiagnostics(req *http.Request, account *Account) *http.Request {
	if req == nil || account == nil || !account.IsOpenAIOAuthLike() {
		return req
	}
	d := &codexOutboundDiagnostic{accountID: account.ID, credentialNamespace: CodexIdentityNamespace(account), source: "conversation", mode: "isolated", fingerprint: string(account.GetCodexFingerprintMode()), ids: make(map[string]string)}
	if policy, ok := req.Context().Value(codexIdentityRequestKey{}).(CodexIdentityPolicy); ok {
		d.mode, d.credentialNamespace = policy.Mode, policy.Namespace
		d.policyReason = policy.Reason
	} else if account.IsShadow() {
		// A local shadow row is not proof of the effective upstream credential.
		d.credentialNamespace = ""
	}
	if source, ok := req.Context().Value(codexDiagnosticSourceKey{}).(string); ok {
		d.source = source
		d.workload = source
	}
	if automatic, _ := req.Context().Value(openAIEvalAutomaticKey{}).(bool); automatic {
		d.source = "automatic_evaluation"
	}
	for _, key := range []string{"session_id", "session-id", "thread-id", "thread_id", "x-client-request-id", "x-codex-installation-id", "x-codex-window-id"} {
		if value := req.Header.Get(key); value != "" {
			d.ids[key] = codexDiagnosticDigest(value)
		}
	}
	if req.GetBody != nil {
		if body, err := req.GetBody(); err == nil {
			raw, readErr := io.ReadAll(io.LimitReader(body, (256<<10)+1))
			_ = body.Close()
			if readErr == nil && len(raw) <= 256<<10 && gjson.ValidBytes(raw) {
				d.bodyAvailable = true
				d.model = sanitizeOpenAIEvalText(gjson.GetBytes(raw, "model").String(), 200)
				d.effort = sanitizeOpenAIEvalText(gjson.GetBytes(raw, "reasoning.effort").String(), 32)
				d.tier = normalizedOpenAIServiceTierValue(gjson.GetBytes(raw, "service_tier").String())
				for _, key := range []string{"prompt_cache_key", "client_metadata.session_id", "client_metadata.thread_id", "client_metadata.installation_id", "client_metadata.x-codex-installation-id", "client_metadata.turn_id", "client_metadata.window_id", "client_metadata.x-codex-window-id"} {
					if value := gjson.GetBytes(raw, key).String(); value != "" {
						d.ids[key] = codexDiagnosticDigest(value)
					}
				}
			}
		}
	}
	if metadata := req.Header.Get(openAIWSTurnMetadataHeader); len(metadata) <= 16<<10 {
		for _, field := range []string{"installation_id", "session_id", "thread_id", "turn_id", "window_id"} {
			if value := gjson.Get(metadata, field).String(); value != "" {
				d.ids["turn_metadata."+field] = codexDiagnosticDigest(value)
			}
		}
	}
	return req.WithContext(context.WithValue(req.Context(), codexOutboundDiagnosticKey{}, d))
}

// ObserveCodexOutboundAttempt is called at RoundTrip, including redirects.
// Native Go transport's hidden retries cannot be counted individually unless
// the existing single-send transport is used; the log exposes this distinction.
func ObserveCodexOutboundAttempt(req *http.Request, transport string) func(*http.Response, error) {
	if req == nil {
		return func(*http.Response, error) {}
	}
	d, _ := req.Context().Value(codexOutboundDiagnosticKey{}).(*codexOutboundDiagnostic)
	if d == nil {
		return func(*http.Response, error) {}
	}
	attempt, start := d.sends.Add(1), time.Now()
	return func(resp *http.Response, err error) {
		status, protocol := 0, "unknown"
		if resp != nil {
			status, protocol = resp.StatusCode, resp.Proto
		}
		emit := func(terminal string, firstRead time.Duration) {
			tlsMode := "native_go"
			if transport == "plugin" {
				tlsMode = "unknown"
			} else if req.URL != nil && req.URL.Scheme == "http" {
				tlsMode = "none"
			}
			logger.FromContext(req.Context()).Debug("codex outbound diagnostic",
				zap.Int64("account_id", d.accountID), zap.String("credential_namespace", d.credentialNamespace), zap.String("source", d.source), zap.String("workload", d.workload),
				zap.String("identity_mode", d.mode), zap.String("identity_reason", d.policyReason), zap.String("fingerprint_mode", d.fingerprint), zap.String("transport", transport), zap.String("http_protocol", protocol),
				zap.String("tls_mode", tlsMode),
				zap.Bool("plugin_effective_identity_known", transport != "plugin"), zap.Bool("physical_send_count_known", HTTPUpstreamSingleSendRequired(req.Context()) && transport != "plugin"),
				zap.Int64("dispatch_attempt", attempt), zap.String("model", d.model), zap.String("effort", d.effort), zap.String("service_tier", d.tier),
				zap.Bool("body_identity_available", d.bodyAvailable), zap.Any("identity_digests", d.ids), zap.Int("http_status", status), zap.Bool("transport_error", err != nil),
				zap.String("terminal", terminal), zap.Duration("first_body_byte", firstRead), zap.Duration("duration", time.Since(start)))
		}
		if err != nil || resp == nil || resp.Body == nil {
			emit("unavailable", 0)
			return
		}
		if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
			emit("not_observed", 0)
			return
		}
		resp.Body = &codexDiagnosticBody{ReadCloser: resp.Body, start: start, emit: emit}
	}
}

// A WS connection carries only this bounded diagnostic state, never a prompt.
// Capture after final handshake header construction; raw identity experiments
// are rejected upstream of this point until WS supports policy-bound ownership.
func withCodexWSDiagnostics(ctx context.Context, c *gin.Context, account *Account, headers http.Header) context.Context {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", nil)
	req.Header = headers
	if c != nil {
		req = req.WithContext(context.WithValue(req.Context(), codexIdentityRequestKey{}, EffectiveCodexIdentityPolicy(c, account)))
	}
	return withCodexOutboundDiagnostics(req, account).Context()
}

type codexWSOutboundDiagnostic struct {
	mu                  sync.Mutex
	ctx                 context.Context
	base                *codexOutboundDiagnostic
	start               time.Time
	model, effort, tier string
	active              bool
	ids                 map[string]string
}

func newCodexWSOutboundDiagnostic(ctx context.Context) *codexWSOutboundDiagnostic {
	d, _ := ctx.Value(codexOutboundDiagnosticKey{}).(*codexOutboundDiagnostic)
	if d == nil {
		return nil
	}
	return &codexWSOutboundDiagnostic{ctx: ctx, base: d}
}

func (d *codexWSOutboundDiagnostic) sent(value any, err error) {
	if d == nil {
		return
	}
	// Select only diagnostic fields before encoding; never serialize input/tools.
	var raw []byte
	switch v := value.(type) {
	case []byte:
		raw = v
	case json.RawMessage:
		raw = v
	case map[string]any:
		safe := map[string]any{}
		for _, key := range []string{"type", "model", "reasoning", "service_tier", "client_metadata", "prompt_cache_key"} {
			safe[key] = v[key]
		}
		raw, _ = json.Marshal(safe)
	default:
		return
	}
	if kind := gjson.GetBytes(raw, "type").String(); kind != "" && kind != "response.create" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.start, d.active = time.Now(), true
	d.model = sanitizeOpenAIEvalText(gjson.GetBytes(raw, "model").String(), 200)
	d.effort = sanitizeOpenAIEvalText(gjson.GetBytes(raw, "reasoning.effort").String(), 32)
	d.tier = normalizedOpenAIServiceTierValue(gjson.GetBytes(raw, "service_tier").String())
	d.ids = make(map[string]string, len(d.base.ids)+3)
	for k, v := range d.base.ids {
		d.ids[k] = v
	}
	for _, key := range []string{"prompt_cache_key", "client_metadata.session_id", "client_metadata.thread_id", "client_metadata.turn_id"} {
		if v := gjson.GetBytes(raw, key).String(); v != "" {
			d.ids[key] = codexDiagnosticDigest(v)
		}
	}
	if err != nil {
		d.finishLocked("write_unknown", true)
	}
}

func (d *codexWSOutboundDiagnostic) received(raw []byte, err error) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.active {
		return
	}
	kind := gjson.GetBytes(raw, "type").String()
	if isOpenAIWSTerminalEvent(kind) {
		d.finishLocked(kind, false)
	} else if err != nil {
		d.finishLocked("not_observed", true)
	}
}

func (d *codexWSOutboundDiagnostic) finishLocked(terminal string, failed bool) {
	logger.FromContext(d.ctx).Debug("codex outbound diagnostic", zap.Int64("account_id", d.base.accountID), zap.String("credential_namespace", d.base.credentialNamespace), zap.String("source", d.base.source), zap.String("identity_mode", d.base.mode), zap.String("identity_reason", d.base.policyReason), zap.String("fingerprint_mode", d.base.fingerprint), zap.String("transport", "websocket"), zap.String("tls_mode", "native_go"), zap.String("model", d.model), zap.String("effort", d.effort), zap.String("service_tier", d.tier), zap.Any("identity_digests", d.ids), zap.String("terminal", terminal), zap.Bool("transport_error", failed), zap.Duration("duration", time.Since(d.start)))
	d.active = false
}

type codexDiagnosticBody struct {
	io.ReadCloser
	start    time.Time
	first    time.Duration
	terminal string
	line     []byte
	discard  bool
	once     sync.Once
	emit     func(string, time.Duration)
}

func (b *codexDiagnosticBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 && b.first == 0 {
		b.first = time.Since(b.start)
	}
	for _, c := range p[:n] {
		if c == '\n' {
			if !b.discard {
				line := strings.TrimSpace(string(b.line))
				if strings.HasPrefix(line, "event:") {
					event := strings.TrimSpace(strings.TrimPrefix(line, "event:"))
					if event == "response.completed" || event == "response.failed" || event == "response.incomplete" {
						b.terminal = event
					}
				}
			}
			b.line = b.line[:0]
			b.discard = false
		} else if !b.discard {
			if len(b.line) >= 128 {
				b.line = b.line[:0]
				b.discard = true
			} else {
				b.line = append(b.line, c)
			}
		}
	}
	if err != nil {
		b.finish()
	}
	return n, err
}
func (b *codexDiagnosticBody) finish() {
	b.once.Do(func() {
		terminal := b.terminal
		if terminal == "" {
			terminal = "not_observed"
		}
		b.emit(terminal, b.first)
	})
}
func (b *codexDiagnosticBody) Close() error { err := b.ReadCloser.Close(); b.finish(); return err }
