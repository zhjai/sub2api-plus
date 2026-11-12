package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const privacyTestUUID = "a4e59951-6f88-437b-a05d-c205232dfc92"

func privacyTestAccount(kind string) *Account {
	return &Account{ID: 300, Platform: PlatformOpenAI, Type: kind,
		Credentials: map[string]any{"api_key": "test-token", "access_token": "test-token", "chatgpt_account_id": "provider-account", "device_id": "real-local-device"},
		Extra:       map[string]any{CodexPrivacyModeKey: CodexPrivacyRandomized, codexFingerprintSeedExtraKey: testCodexFingerprintSeed}}
}

func privacyTestBody(t *testing.T, session string) []byte {
	t.Helper()
	turn := map[string]any{"installation_id": privacyTestUUID, "session_id": session, "thread_id": "client-thread", "turn_id": "client-turn", "window_id": "client-window", "cwd": "/private/cwd", "os": "local-os"}
	embedded, err := json.Marshal(turn)
	require.NoError(t, err)
	body, err := json.Marshal(map[string]any{
		"model": "gpt-6.1-sol", "stream": true, "prompt_cache_key": session, "previous_response_id": "resp_continue",
		"instructions":    "Keep /private/cwd and host-user instructions intact",
		"input":           []any{map[string]any{"type": "function_call_output", "call_id": "call_continue", "id": "fc_continue", "output": "/private/cwd"}},
		"tools":           []any{map[string]any{"type": "function", "name": "exec", "parameters": map[string]any{"type": "object"}}},
		"client_metadata": map[string]any{"installation_id": privacyTestUUID, "x-codex-installation-id": privacyTestUUID, "session_id": session, "thread_id": "client-thread", "turn_id": "client-turn", "window_id": "client-window", "x-codex-window-id": "client-window", "cwd": "/private/cwd", "environment": map[string]any{"hostname": "host-user"}, openAIWSTurnStateHeader: "opaque-ticket", openAIWSTurnMetadataHeader: string(embedded)},
	})
	require.NoError(t, err)
	return body
}

func assertPrivacyProtocol(t *testing.T, body []byte, header http.Header) {
	t.Helper()
	metadata := gjson.GetBytes(body, "client_metadata")
	for _, value := range []string{privacyTestUUID, "client-thread", "client-turn", "client-window", "/private/cwd", "host-user", "local-os"} {
		require.NotContains(t, metadata.Raw, value)
		require.NotContains(t, header.Get(openAIWSTurnMetadataHeader), value)
	}
	require.Equal(t, "opaque-ticket", metadata.Get(openAIWSTurnStateHeader).String())
	require.Equal(t, "resp_continue", gjson.GetBytes(body, "previous_response_id").String())
	require.Equal(t, "call_continue", gjson.GetBytes(body, "input.0.call_id").String())
	require.Equal(t, "fc_continue", gjson.GetBytes(body, "input.0.id").String())
	require.Equal(t, "/private/cwd", gjson.GetBytes(body, "input.0.output").String())
	require.Contains(t, gjson.GetBytes(body, "instructions").String(), "/private/cwd")
	if header.Get("session-id") != "" {
		require.Equal(t, metadata.Get("session_id").String(), header.Get("session-id"))
	}
	if header.Get("thread-id") != "" {
		require.Equal(t, metadata.Get("thread_id").String(), header.Get("thread-id"))
	}
	if header.Get(openAIWSTurnMetadataHeader) != "" {
		for _, key := range []string{"installation_id", "session_id", "thread_id", "turn_id", "window_id"} {
			require.Equal(t, metadata.Get(key).String(), gjson.Get(header.Get(openAIWSTurnMetadataHeader), key).String(), key)
		}
	}
}

func TestCodexPrivacyFinalHTTPWire(t *testing.T) {
	for _, kind := range []string{AccountTypeOAuth, AccountTypeSetupToken, AccountTypeAPIKey} {
		for _, passthrough := range []bool{false, true} {
			t.Run(kind+"/"+map[bool]string{true: "passthrough", false: "native"}[passthrough], func(t *testing.T) {
				a := privacyTestAccount(kind)
				a.Credentials[credKeyHeaderOverrideEnabled] = true
				a.Credentials[credKeyHeaderOverrides] = map[string]any{
					"originator": "private-originator",
					"version":    "private-version",
					"user-agent": "private-agent",
				}
				body := privacyTestBody(t, "client-session")
				c := identityPolicyContext(77, body)
				c.Request.Header.Set("session-id", "client-session")
				c.Request.Header.Set("thread-id", "client-thread")
				c.Request.Header.Set(openAIWSTurnStateHeader, "opaque-ticket")
				c.Request.Header.Set(openAIWSTurnMetadataHeader, gjson.GetBytes(body, "client_metadata."+openAIWSTurnMetadataHeader).String())
				upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}}
				s := &OpenAIGatewayService{httpUpstream: upstream}
				_, err := s.prepareCodexAccountIdentitySource(context.Background(), c, a)
				require.NoError(t, err)
				projected, _, err := applyCodexAccountIdentityClientMetadataRaw(body, a, 77)
				require.NoError(t, err)
				var req *http.Request
				if passthrough {
					req, err = s.buildUpstreamRequestOpenAIPassthrough(context.Background(), c, a, projected, "test-token")
				} else {
					req, err = s.buildUpstreamRequest(context.Background(), c, a, projected, "test-token", true, "client-session", true)
				}
				require.NoError(t, err)
				// Last-minute header overrides must still be filtered at actual send.
				req.Header.Set("X-Stainless-Os", "local-os")
				req.Header.Set("X-Codex-Hostname", "host-user")
				req.Header.Set("Traceparent", privacyTestUUID)
				req.Header.Set("X-Client-Trace-Id", privacyTestUUID)
				req.Header.Set("Accept-Language", "zh-CN")
				_, err = s.doOpenAIUpstreamWithAdmission(req, "", a)
				require.NoError(t, err)
				require.NotNil(t, upstream.lastReq)
				assertPrivacyProtocol(t, upstream.lastBody, upstream.lastReq.Header)
				require.Equal(t, codexOutboundAcceptLanguage, upstream.lastReq.Header.Get("Accept-Language"))
				require.Empty(t, upstream.lastReq.Header.Get("X-Stainless-Os"))
				require.Empty(t, upstream.lastReq.Header.Get("Traceparent"))
				require.NotEqual(t, privacyTestUUID, upstream.lastReq.Header.Get("X-Client-Trace-Id"))
				require.Equal(t, "Bearer test-token", upstream.lastReq.Header.Get("Authorization"))
				replay, err := upstream.lastReq.GetBody()
				require.NoError(t, err)
				replayBytes, err := io.ReadAll(replay)
				require.NoError(t, err)
				_ = replay.Close()
				require.Equal(t, upstream.lastBody, replayBytes)
				require.Equal(t, int64(len(replayBytes)), upstream.lastReq.ContentLength)
				// Inspect serialized HTTP, since Header.Get hides noncanonical keys.
				wireRequest := upstream.lastReq.Clone(upstream.lastReq.Context())
				wireRequest.Body = io.NopCloser(bytes.NewReader(replayBytes))
				var wire bytes.Buffer
				require.NoError(t, wireRequest.Write(&wire))
				for _, raw := range []string{"private-originator", "private-version", "private-agent"} {
					require.NotContains(t, wire.String(), raw)
				}
				require.Contains(t, string(body), privacyTestUUID, "caller body snapshot remains raw")
			})
		}
	}
}

func TestCodexPrivacyIdentityAliasesMatchMetadata(t *testing.T) {
	p, err := newCodexPrivacyProjection(privacyTestAccount(AccountTypeAPIKey), 77)
	require.NoError(t, err)
	for _, raw := range []string{privacyTestUUID, "client-local-identity"} {
		for _, name := range []string{"x-codex-session-id", "x-codex-thread-id", "x-codex-conversation-id", "x-codex-turn-id"} {
			t.Run(name+"/"+raw, func(t *testing.T) {
				h := http.Header{name: []string{raw}}
				p.headers(h)
				metadata := p.metadata(map[string]any{name: raw})
				require.NotEqual(t, raw, h[name][0])
				require.Equal(t, p.id(codexPrivacyIDKind(name), raw), h[name][0])
				require.Equal(t, metadata[name], h[name][0], "header and body aliases share a mapping")
			})
		}
	}
}

func TestCodexPrivacySessionUpdateFinalWrites(t *testing.T) {
	for _, kind := range []string{AccountTypeOAuth, AccountTypeSetupToken, AccountTypeAPIKey} {
		t.Run(kind, func(t *testing.T) {
			a := privacyTestAccount(kind)
			p, err := newCodexPrivacyProjection(a, 77)
			require.NoError(t, err)
			body := json.RawMessage(`{"type":"session.update","session":{"client_metadata":{"installation_id":"raw-device","cwd":"/private/cwd","session_id":"raw-session"},"session_id":"raw-session","x-codex-turn-metadata":"{\"thread_id\":\"raw-thread\",\"hostname\":\"private-host\"}","previous_response_id":"resp_opaque","input":[{"text":"/private/cwd raw-device"}],"tools":[{"name":"exec"}]}}`)
			ctx := withCodexPrivacyWS(context.Background(), a, 77)
			pooled, err := codexPrivacyWSValue(ctx, body)
			require.NoError(t, err)
			wire, err := json.Marshal(pooled)
			require.NoError(t, err)
			session := gjson.GetBytes(wire, "session")
			require.Equal(t, p.id("device", "raw-device"), session.Get("client_metadata.installation_id").String())
			require.Equal(t, p.id("session", "raw-session"), session.Get("session_id").String())
			require.Equal(t, session.Get("session_id").String(), session.Get("client_metadata.session_id").String())
			require.False(t, session.Get("client_metadata.cwd").Exists())
			require.NotContains(t, session.Get(openAIWSTurnMetadataHeader).String(), "private-host")
			require.NotContains(t, session.Get(openAIWSTurnMetadataHeader).String(), "raw-thread")
			for _, field := range []string{"input", "tools", "previous_response_id"} {
				require.Equal(t, gjson.GetBytes(body, "session."+field).Raw, session.Get(field).Raw, field)
			}
			capture := &privacyCaptureWS{}
			conn := &codexPrivacyFrameConn{FrameConn: capture, projection: p}
			require.NoError(t, conn.WriteFrame(ctx, coderws.MessageText, body))
			require.JSONEq(t, string(wire), string(capture.wire[0]), "pooled and passthrough writers agree")
		})
	}
}

func TestCodexPrivacyContinuationStableAndIsolated(t *testing.T) {
	a := privacyTestAccount(AccountTypeAPIKey)
	p, err := newCodexPrivacyProjection(a)
	require.NoError(t, err)
	body := privacyTestBody(t, "session-one")
	first, err := p.body(body)
	require.NoError(t, err)
	// A new projector simulates another request/process using the persisted seed.
	p2, err := newCodexPrivacyProjection(a)
	require.NoError(t, err)
	continued, err := sjson.SetBytes(body, "previous_response_id", "resp_next")
	require.NoError(t, err)
	second, err := p2.body(continued)
	require.NoError(t, err)
	require.Equal(t, "resp_next", gjson.GetBytes(second, "previous_response_id").String())
	for _, path := range []string{"client_metadata", "prompt_cache_key", "input", "tools"} {
		require.Equal(t, gjson.GetBytes(first, path).Raw, gjson.GetBytes(second, path).Raw, path)
	}
	other, err := p.body(privacyTestBody(t, "session-two"))
	require.NoError(t, err)
	require.NotEqual(t, gjson.GetBytes(first, "prompt_cache_key").String(), gjson.GetBytes(other, "prompt_cache_key").String())
	b := privacyTestAccount(AccountTypeAPIKey)
	b.Extra[codexFingerprintSeedExtraKey] = userSuppliedCodexFingerprintSeed
	p3, err := newCodexPrivacyProjection(b)
	require.NoError(t, err)
	otherAccount, err := p3.body(body)
	require.NoError(t, err)
	require.NotEqual(t, gjson.GetBytes(first, "client_metadata.session_id").String(), gjson.GetBytes(otherAccount, "client_metadata.session_id").String())
	req, err := http.NewRequest("POST", "https://example.test/v1/responses", bytes.NewReader(body))
	require.NoError(t, err)
	once, err := applyCodexPrivacyBeforeSend(req, a)
	require.NoError(t, err)
	twice, err := applyCodexPrivacyBeforeSend(once, a)
	require.NoError(t, err)
	require.Same(t, once, twice, "transport wrappers must not hash the same body twice")
}

type privacyCaptureWS struct {
	openAIWSFakeConn
	wire [][]byte
}

func (w *privacyCaptureWS) WriteJSON(_ context.Context, value any) error {
	raw, err := json.Marshal(value)
	w.wire = append(w.wire, raw)
	return err
}

func (w *privacyCaptureWS) ReadFrame(ctx context.Context) (coderws.MessageType, []byte, error) {
	raw, err := w.ReadMessage(ctx)
	return coderws.MessageText, raw, err
}

func (w *privacyCaptureWS) WriteFrame(ctx context.Context, _ coderws.MessageType, raw []byte) error {
	w.wire = append(w.wire, bytes.Clone(raw))
	return nil
}

func TestCodexPrivacyFinalWSWritesAndContinuation(t *testing.T) {
	for _, kind := range []string{AccountTypeOAuth, AccountTypeAPIKey} {
		t.Run(kind, func(t *testing.T) {
			a := privacyTestAccount(kind)
			body := privacyTestBody(t, "client-session")
			c := identityPolicyContext(77, body)
			c.Request.Header.Set("session-id", "client-session")
			c.Request.Header.Set("thread-id", "client-thread")
			c.Request.Header.Set(openAIWSTurnMetadataHeader, gjson.GetBytes(body, "client_metadata."+openAIWSTurnMetadataHeader).String())
			s := &OpenAIGatewayService{}
			_, err := s.prepareCodexAccountIdentitySource(context.Background(), c, a)
			require.NoError(t, err)
			headers, _, err := s.buildOpenAIWSHeaders(context.Background(), c, a, "test-token", OpenAIWSProtocolDecision{Transport: OpenAIUpstreamTransportResponsesWebsocketV2}, true, "opaque-ticket", c.GetHeader(openAIWSTurnMetadataHeader), "client-session", "gpt-6.1-sol", "")
			require.NoError(t, err)
			capture := &privacyCaptureWS{}
			lease := &openAIWSConnLease{conn: newOpenAIWSConn("privacy", a.ID, capture, nil)}
			ctx := withCodexPrivacyWS(context.Background(), a, 77)
			for i := 0; i < 2; i++ {
				require.NoError(t, lease.WriteJSONWithContextTimeout(ctx, json.RawMessage(body), time.Second))
			}
			require.Len(t, capture.wire, 2)
			require.Equal(t, capture.wire[0], capture.wire[1], "retry uses same pseudonyms")
			assertPrivacyProtocol(t, capture.wire[0], headers)
			require.Equal(t, "opaque-ticket", headers.Get(openAIWSTurnStateHeader))
			projection, err := newCodexPrivacyProjection(a, 77)
			require.NoError(t, err)
			frame := &codexPrivacyFrameConn{FrameConn: capture, projection: projection}
			require.NoError(t, frame.WriteFrame(context.Background(), coderws.MessageText, body))
			require.Equal(t, capture.wire[0], capture.wire[2], "dedicated and pooled sockets share projection")
			require.Contains(t, string(body), privacyTestUUID)
		})
	}
}

func TestCodexPrivacyAccountTestsUseFinalProjection(t *testing.T) {
	for _, kind := range []string{AccountTypeOAuth, AccountTypeAPIKey} {
		t.Run(kind, func(t *testing.T) {
			a := privacyTestAccount(kind)
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Body: http.NoBody}}
			s := &AccountTestService{httpUpstream: upstream}
			req, err := http.NewRequest("POST", "https://example.test/v1/responses", bytes.NewReader(privacyTestBody(t, "probe-session")))
			require.NoError(t, err)
			req.Header.Set("X-Stainless-OS", "local-os")
			_, err = s.doOpenAIAccountTestUpstream(req, "", a, false)
			require.NoError(t, err)
			assertPrivacyProtocol(t, upstream.lastBody, upstream.lastReq.Header)
			require.Empty(t, upstream.lastReq.Header.Get("X-Stainless-OS"))
		})
	}
}

func TestCodexPrivacySeedLifecycleAndValidation(t *testing.T) {
	for _, kind := range []string{AccountTypeOAuth, AccountTypeSetupToken, AccountTypeAPIKey} {
		t.Run(kind, func(t *testing.T) {
			extra := prepareCodexFingerprintExtraForCreate(PlatformOpenAI, kind, map[string]any{CodexPrivacyModeKey: CodexPrivacyRandomized, codexFingerprintSeedExtraKey: userSuppliedCodexFingerprintSeed})
			seed := requireValidCodexFingerprintSeed(t, extra)
			require.NotEqual(t, userSuppliedCodexFingerprintSeed, seed)
			a := &Account{Platform: PlatformOpenAI, Type: kind, Extra: extra}
			require.NoError(t, NormalizeCodexIdentityConfig(a, nil))
			updated := prepareCodexFingerprintExtraForUpdate(a, map[string]any{CodexPrivacyModeKey: CodexPrivacyRandomized})
			require.Equal(t, seed, requireValidCodexFingerprintSeed(t, updated))
			duplicate := prepareCodexFingerprintExtraForCreate(PlatformOpenAI, kind, updated)
			require.NotEqual(t, seed, requireValidCodexFingerprintSeed(t, duplicate))
			a.Extra[codexFingerprintModeExtraKey] = "full"
			require.ErrorContains(t, NormalizeCodexIdentityConfig(a, nil), "conflicts")
			delete(a.Extra, codexFingerprintModeExtraKey)
			a.Extra[CodexIdentityModeKey] = CodexIdentityPreserveClient
			require.ErrorContains(t, NormalizeCodexIdentityConfig(a, nil), "conflicts")
		})
	}
	a := privacyTestAccount(AccountTypeAPIKey)
	delete(a.Extra, codexFingerprintSeedExtraKey)
	_, err := newCodexPrivacyProjection(a)
	require.ErrorContains(t, err, "Save the account")
	require.Error(t, ValidateCodexIdentityExtraPatch(map[string]any{CodexPrivacyModeKey: CodexPrivacyRandomized}))
	req, err := http.NewRequest("POST", "https://example.test/v1/responses", strings.NewReader(`{"input":"unchanged"}`))
	require.NoError(t, err)
	off, err := applyCodexPrivacyBeforeSend(req, &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey})
	require.NoError(t, err)
	require.Same(t, req, off)
}

func TestCodexPrivacyPreservesOpaqueCustomCredentialsAndProtocolFlags(t *testing.T) {
	p, err := newCodexPrivacyProjection(privacyTestAccount(AccountTypeAPIKey))
	require.NoError(t, err)
	h := http.Header{}
	for _, name := range []string{"X-Custom-Api-Token", "X-Vendor-Credential", "X-Vendor-Opaque", "X-Client-Auth-Key"} {
		h.Set(name, privacyTestUUID)
	}
	h.Set("X-Client-Trace-Id", privacyTestUUID)
	p.headers(h)
	for _, name := range []string{"X-Custom-Api-Token", "X-Vendor-Credential", "X-Vendor-Opaque", "X-Client-Auth-Key"} {
		require.Equal(t, privacyTestUUID, h.Get(name), name)
	}
	require.NotEqual(t, privacyTestUUID, h.Get("X-Client-Trace-Id"))
	fields := map[string]any{"request_kind": "memory", "thread_source": "memory_consolidation", "sandbox": "seccomp", "sandbox_mode": "workspace-write", "auto_review_enabled": true, "root_turn_id": "turn-original", "turn_id": "turn-original", "context_window_id": privacyTestUUID, "cwd": "/private/cwd", "hostname": "private-host", "unknown_environment": "private-host"}
	clean := p.metadata(fields)
	for _, name := range []string{"request_kind", "thread_source", "sandbox", "sandbox_mode", "auto_review_enabled"} {
		require.Equal(t, fields[name], clean[name], name)
	}
	require.Equal(t, clean["turn_id"], clean["root_turn_id"])
	require.NotEqual(t, privacyTestUUID, clean["context_window_id"])
	for _, name := range []string{"cwd", "hostname", "unknown_environment"} {
		require.NotContains(t, clean, name)
	}
}

func TestCodexPrivacyEditPreservesModeUntilExplicitlyDisabled(t *testing.T) {
	a := privacyTestAccount(AccountTypeAPIKey)
	updated := prepareCodexFingerprintExtraForUpdate(a, map[string]any{"rpm_limit": 1})
	require.Equal(t, CodexPrivacyRandomized, updated[CodexPrivacyModeKey])
	require.Equal(t, a.Extra[codexFingerprintSeedExtraKey], updated[codexFingerprintSeedExtraKey])
	disabled := prepareCodexFingerprintExtraForUpdate(a, map[string]any{CodexPrivacyModeKey: "off"})
	require.Equal(t, "off", disabled[CodexPrivacyModeKey])
}

func TestCodexPrivacyTogglePreservesSeedAndContinuation(t *testing.T) {
	for _, kind := range []string{AccountTypeOAuth, AccountTypeSetupToken, AccountTypeAPIKey} {
		t.Run(kind, func(t *testing.T) {
			a := privacyTestAccount(kind)
			body := privacyTestBody(t, "switch-session")
			project := func() (*http.Request, []byte) {
				req, err := http.NewRequest(http.MethodPost, "https://example.test/responses", bytes.NewReader(body))
				require.NoError(t, err)
				req.Header.Set("Session-Id", "switch-session")
				req.Header.Set(openAIWSTurnStateHeader, "server-ticket")
				req = stampCodexPrivacySource(req, a, a, 77)
				clean, err := applyCodexPrivacyBeforeSend(req, a)
				require.NoError(t, err)
				raw, err := io.ReadAll(clean.Body)
				require.NoError(t, err)
				_ = clean.Body.Close()
				require.Equal(t, "server-ticket", clean.Header.Get(openAIWSTurnStateHeader))
				require.Equal(t, "resp_continue", gjson.GetBytes(raw, "previous_response_id").String())
				for _, field := range []string{"input", "instructions", "tools"} {
					require.Equal(t, gjson.GetBytes(body, field).Raw, gjson.GetBytes(raw, field).Raw, field)
				}
				return clean, raw
			}
			first, firstBody := project()
			a.Extra = prepareCodexFingerprintExtraForUpdate(a, map[string]any{CodexPrivacyModeKey: "off"})
			disabled, disabledBody := project()

			require.Equal(t, body, disabledBody)
			require.Equal(t, "switch-session", disabled.Header.Get("Session-Id"))
			a.Extra = prepareCodexFingerprintExtraForUpdate(a, map[string]any{CodexPrivacyModeKey: CodexPrivacyRandomized})
			require.NoError(t, validateCodexPrivacyConfig(a))
			reenabled, reenabledBody := project()
			require.Equal(t, first.Header.Get("Session-Id"), reenabled.Header.Get("Session-Id"))
			require.Equal(t, firstBody, reenabledBody)
		})
	}
}

func TestCodexPrivacyHTTPShadowUsesCredentialPolicyAndResetsBetweenAttempts(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(map[bool]string{true: "passthrough", false: "native"}[passthrough], func(t *testing.T) {
			parent := privacyTestAccount(AccountTypeOAuth)
			parent.Status = StatusActive
			shadow := &Account{ID: 400, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parent.ID, Extra: map[string]any{}}
			capture := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Body: http.NoBody}}
			svc := &OpenAIGatewayService{accountRepo: newStubCredRepo(parent), httpUpstream: capture}
			body := privacyTestBody(t, "shadow-session")
			c := identityPolicyContext(77, body)
			c.Request.Header.Set(openAIWSTurnMetadataHeader, gjson.GetBytes(body, "client_metadata."+openAIWSTurnMetadataHeader).String())
			_, err := svc.prepareCodexAccountIdentitySource(t.Context(), c, shadow)
			require.NoError(t, err)
			var req *http.Request
			if passthrough {
				req, err = svc.buildUpstreamRequestOpenAIPassthrough(t.Context(), c, shadow, body, "test-token")
			} else {
				req, err = svc.buildUpstreamRequest(t.Context(), c, shadow, body, "test-token", true, "shadow-session", true)
			}
			require.NoError(t, err)
			_, err = svc.doOpenAIUpstreamWithAdmission(req, "", shadow)
			require.NoError(t, err)
			assertPrivacyProtocol(t, capture.lastBody, capture.lastReq.Header)
			other := &Account{ID: 401, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
			_, err = svc.prepareCodexAccountIdentitySource(t.Context(), c, other)
			require.NoError(t, err)
			req, err = svc.buildUpstreamRequest(t.Context(), c, other, body, "test-token", true, "shadow-session", true)
			require.NoError(t, err)
			_, err = svc.doOpenAIUpstreamWithAdmission(req, "", other)
			require.NoError(t, err)
			require.Equal(t, privacyTestUUID, gjson.GetBytes(capture.lastBody, "client_metadata.installation_id").String(), "previous shadow policy must not affect next account")
		})
	}
}

func TestCodexPrivacyWSCompatibilitySeparatesSessionsWithConvergenceOff(t *testing.T) {
	a := privacyTestAccount(AccountTypeAPIKey)
	first := openAIWSAcquireRequest{Account: a, WSURL: "wss://example.test/responses", Headers: http.Header{}, CodexPrivacy: true}
	first.Headers.Set("Session-Id", "private-session-one")
	first.Headers.Set(openAIWSTurnMetadataHeader, `{"thread_id":"private-thread-one"}`)
	same := cloneOpenAIWSAcquireRequest(first)
	require.True(t, sameOpenAIWSPrewarmTarget(first, same))
	second := cloneOpenAIWSAcquireRequest(first)
	second.Headers.Set("Session-Id", "private-session-two")
	require.False(t, sameOpenAIWSPrewarmTarget(first, second))
	second = cloneOpenAIWSAcquireRequest(first)
	second.Headers.Set(openAIWSTurnMetadataHeader, `{"thread_id":"private-thread-two"}`)
	require.False(t, sameOpenAIWSPrewarmTarget(first, second))
	// A shadow has no own privacy mode; the resolved policy is still captured.
	shadow := &Account{ID: 400, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &a.ID}
	first.Account = shadow
	second.Account = shadow
	require.False(t, sameOpenAIWSPrewarmTarget(first, second))
	require.True(t, normalizeOpenAIWSAcquireCompatibility(first).codexPrivacy)
	off := cloneOpenAIWSAcquireRequest(first)
	off.CodexPrivacy = false
	require.NotEqual(t, normalizeOpenAIWSAcquireCompatibility(first), normalizeOpenAIWSAcquireCompatibility(off))
}

func TestCodexPrivacyPrincipalIsolationAndOpaqueFrames(t *testing.T) {
	a := privacyTestAccount(AccountTypeAPIKey)
	p1, err := newCodexPrivacyProjection(a, 77)
	require.NoError(t, err)
	p2, err := newCodexPrivacyProjection(a, 78)
	require.NoError(t, err)
	require.Equal(t, p1.id("device", privacyTestUUID), p2.id("device", privacyTestUUID))
	for _, kind := range []string{"session", "thread", "turn", "window", "request"} {
		require.NotEqual(t, p1.id(kind, "same-client"), p2.id(kind, "same-client"), kind)
		require.Equal(t, p1.id(kind, "same-client"), p1.id(kind, "same-client"), kind)
	}
	capture := &privacyCaptureWS{}
	frame := &codexPrivacyFrameConn{FrameConn: capture, projection: p1}
	binary := []byte{0x00, 0xff, 0x21}
	require.NoError(t, frame.WriteFrame(context.Background(), coderws.MessageBinary, binary))
	require.Equal(t, binary, capture.wire[0])
	for _, raw := range []string{"null", "[]", "42", "true"} {
		require.NoError(t, frame.WriteFrame(context.Background(), coderws.MessageText, []byte(raw)))
		require.Equal(t, []byte(raw), capture.wire[len(capture.wire)-1])
		value, err := codexPrivacyWSValue(withCodexPrivacyWS(context.Background(), a, 77), json.RawMessage(raw))
		require.NoError(t, err)
		require.Equal(t, json.RawMessage(raw), value)
	}
}

func TestCodexPrivacyFinalHTTPSeparatesPrincipals(t *testing.T) {
	a := privacyTestAccount(AccountTypeAPIKey)
	body := privacyTestBody(t, "same-client-session")
	var sessions []string
	var devices []string
	for _, keyID := range []int64{77, 78, 77} {
		req, err := http.NewRequest(http.MethodPost, "https://example.test/v1/responses", bytes.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("session-id", "same-client-session")
		req = stampCodexPrivacySource(req, a, a, keyID)
		clean, err := applyCodexPrivacyBeforeSend(req, a)
		require.NoError(t, err)
		wire, err := io.ReadAll(clean.Body)
		require.NoError(t, err)
		_ = clean.Body.Close()
		session := gjson.GetBytes(wire, "client_metadata.session_id").String()
		require.Equal(t, session, clean.Header.Get("session-id"))
		require.Equal(t, "resp_continue", gjson.GetBytes(wire, "previous_response_id").String())
		sessions = append(sessions, session)
		devices = append(devices, gjson.GetBytes(wire, "client_metadata.installation_id").String())
	}
	require.NotEqual(t, sessions[0], sessions[1])
	require.Equal(t, sessions[0], sessions[2])
	require.Equal(t, devices[0], devices[1])
}
