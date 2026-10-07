package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type identityPolicyRepo struct {
	AccountRepository
	rows        map[int64]*Account
	err         error
	validations int
	onValidate  func(int)
}

type identityContinuationTestCache struct {
	*stubGatewayCache
	bindings map[string]OpenAIIdentityContinuation
	err      error
}

func (c *identityContinuationTestCache) SetOpenAIIdentityContinuation(_ context.Context, key string, binding OpenAIIdentityContinuation, _ time.Duration) error {
	if c.err != nil {
		return c.err
	}
	c.bindings[key] = binding
	return nil
}
func (c *identityContinuationTestCache) GetOpenAIIdentityContinuation(_ context.Context, key string) (*OpenAIIdentityContinuation, error) {
	if c.err != nil {
		return nil, c.err
	}
	v, ok := c.bindings[key]
	if !ok {
		return nil, nil
	}
	return &v, nil
}

func TestCodexIdentityHTTPContinuationSharedBinding(t *testing.T) {
	a := identityPolicyAccount(t)
	repo := &identityPolicyRepo{rows: map[int64]*Account{a.ID: a}}
	cache := &identityContinuationTestCache{stubGatewayCache: &stubGatewayCache{}, bindings: map[string]OpenAIIdentityContinuation{}}
	writer := &OpenAIGatewayService{accountRepo: repo, cache: cache}
	c := identityPolicyContext(77, nil)
	_, err := writer.prepareCodexAccountIdentitySource(context.Background(), c, a)
	require.NoError(t, err)
	writer.bindCodexIdentityContinuation(context.Background(), c, a, "response", "resp_test")
	writer.bindCodexIdentityContinuation(context.Background(), c, a, "ticket", "ticket_test")
	reader := &OpenAIGatewayService{accountRepo: repo, cache: cache}
	for _, mutation := range []string{"valid", "key", "user", "namespace", "revision", "mode", "missing", "cache_error", "disabled"} {
		t.Run(mutation, func(t *testing.T) {
			binding := cache.bindings[OpenAIIdentityContinuationKey(0, "response", "resp_test")]
			defer func() {
				cache.bindings[OpenAIIdentityContinuationKey(0, "response", "resp_test")] = binding
				cache.err = nil
			}()
			changed := binding
			switch mutation {
			case "key":
				changed.APIKeyID++
			case "user":
				changed.UserID++
			case "namespace":
				changed.CredentialNamespace = "other"
			case "revision":
				changed.Revision = "old"
			case "mode":
				changed.Mode = CodexIdentityIsolated
			case "missing":
				changed = OpenAIIdentityContinuation{}
			case "cache_error":
				cache.err = errors.New("offline")
			}
			cache.bindings[OpenAIIdentityContinuationKey(0, "response", "resp_test")] = changed
			rc := identityPolicyContext(77, nil)
			_, err := reader.prepareCodexAccountIdentitySource(context.Background(), rc, a)
			require.NoError(t, err)
			if mutation == "disabled" {
				rc.Set(codexIdentityPolicyContextKey, CodexIdentityPolicy{Mode: CodexIdentityIsolated})
			}
			err = reader.guardCodexIdentityRequest(rc, a, []byte(`{"previous_response_id":"resp_test"}`), "http")
			if mutation == "valid" {
				require.NoError(t, err)
				rc.Request.Header.Set(openAIWSTurnStateHeader, "ticket_test")
				require.NoError(t, reader.guardCodexIdentityRequest(rc, a, nil, "http"))
			} else {
				require.Error(t, err)
			}
		})
	}
	cache.err = errors.New("offline")
	defaultAccount := &Account{ID: 99, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	rc := identityPolicyContext(78, nil)
	_, err = reader.prepareCodexAccountIdentitySource(context.Background(), rc, defaultAccount)
	require.NoError(t, err)
	require.NoError(t, reader.guardCodexIdentityRequest(rc, defaultAccount, []byte(`{"previous_response_id":"normal"}`), "http"))
}

func TestCodexIdentitySelectedPluginOnly(t *testing.T) {
	a := identityPolicyAccount(t)
	repo := &identityPolicyRepo{rows: map[int64]*Account{a.ID: a}}
	s := &OpenAIGatewayService{accountRepo: repo, pluginManager: &PluginManager{}}
	c := identityPolicyContext(77, nil)
	_, err := s.prepareCodexAccountIdentitySource(context.Background(), c, a)
	require.NoError(t, err)
	require.NoError(t, s.guardCodexIdentityRequest(c, a, nil, "http"))
	req, err := s.buildUpstreamRequest(context.Background(), c, a, []byte(`{"model":"gpt-5.4"}`), "token", true, "", true)
	require.NoError(t, err)
	s.pluginManager.route.Store(&pluginRoute{pluginID: 1, rolloutPercent: 100})
	require.ErrorContains(t, s.guardCodexIdentityRequest(c, a, nil, "http"), "selected plugin")
	_, handled, err := s.pluginManager.RoundTripOpenAIOAuth(req.Context(), req, "", a)
	require.True(t, handled)
	require.ErrorContains(t, err, "selected plugin")
}

func TestCodexIdentityFullForwardWireAndContinuation(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, key := range []int64{77, 78} {
			a := identityPolicyAccount(t)
			a.Extra["openai_passthrough"] = passthrough
			a.Credentials["device_id"] = "must-not-inject"
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_wire\",\"model\":\"gpt-5.4\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\ndata: [DONE]\n\n"))}}
			s := newOpenAIImageGenerationControlTestService(upstream)
			s.accountRepo = &identityPolicyRepo{rows: map[int64]*Account{a.ID: a}}
			s.pluginManager = &PluginManager{}
			cache := &identityContinuationTestCache{stubGatewayCache: &stubGatewayCache{}, bindings: map[string]OpenAIIdentityContinuation{}}
			s.cache = cache
			body := []byte(`{"model":"gpt-5.4","input":"test","stream":true,"previous_response_id":"resp_prior","prompt_cache_key":"same","client_metadata":{"session_id":"same","thread_id":"distinct-thread"}}`)
			c := identityPolicyContext(key, body)
			c.Request.Header.Set("User-Agent", codexCLIUserAgent)
			if key == 77 {
				_, err := s.prepareCodexAccountIdentitySource(context.Background(), c, a)
				require.NoError(t, err)
				s.bindCodexIdentityContinuation(context.Background(), c, a, "response", "resp_prior")
			}
			_, err := s.Forward(context.Background(), c, a, body)
			require.NoError(t, err)
			require.NotNil(t, upstream.lastReq)
			if key == 77 {
				require.Equal(t, "same", upstream.lastReq.Header.Get("session_id"))
				require.Equal(t, "distinct-thread", upstream.lastReq.Header.Get("thread-id"))
				require.Equal(t, "resp_prior", gjson.GetBytes(upstream.lastBody, "previous_response_id").String())
				require.JSONEq(t, gjson.GetBytes(body, "client_metadata").Raw, gjson.GetBytes(upstream.lastBody, "client_metadata").Raw)
				require.Equal(t, "same", gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String())
			} else {
				require.NotEqual(t, "same", upstream.lastReq.Header.Get("session_id"))
				require.NotEqual(t, "same", gjson.GetBytes(upstream.lastBody, "prompt_cache_key").String())
			}
			require.Equal(t, codexOutboundAcceptLanguage, upstream.lastReq.Header.Get("Accept-Language"))
		}
	}
}

func TestCodexIdentityHTTPMintAndContinueAcrossInstances(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(map[bool]string{false: "native", true: "passthrough"}[passthrough], func(t *testing.T) {
			a := identityPolicyAccount(t)
			a.Extra["openai_passthrough"] = passthrough
			repo := &identityPolicyRepo{rows: map[int64]*Account{a.ID: a}}
			cache := &identityContinuationTestCache{stubGatewayCache: &stubGatewayCache{}, bindings: map[string]OpenAIIdentityContinuation{}}
			response := func(id, ticket string) *http.Response {
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}, http.CanonicalHeaderKey(openAIWSTurnStateHeader): {ticket}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"" + id + "\",\"model\":\"gpt-5.4\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\ndata: [DONE]\n\n"))}
			}
			firstUpstream := &httpUpstreamRecorder{resp: response("resp_minted", "minted-ticket")}
			writer := newOpenAIImageGenerationControlTestService(firstUpstream)
			writer.accountRepo, writer.cache = repo, cache
			root := []byte(`{"model":"gpt-5.4","input":"test","stream":true,"prompt_cache_key":"same"}`)
			c := identityPolicyContext(77, root)
			c.Request.Header.Set("User-Agent", codexCLIUserAgent)
			_, err := writer.Forward(context.Background(), c, a, root)
			require.NoError(t, err)
			require.Len(t, firstUpstream.requests, 1)
			policy := EffectiveCodexIdentityPolicy(c, a)
			for kind, value := range map[string]string{"response": "resp_minted", "ticket": "minted-ticket"} {
				metadata, err := cache.GetOpenAIIdentityContinuation(context.Background(), OpenAIIdentityContinuationKey(0, kind, value))
				require.NoError(t, err)
				require.True(t, codexIdentityContinuationMatches(metadata, policy), "final HTTP result must publish shared %s binding", kind)
			}
			secondUpstream := &httpUpstreamRecorder{resp: response("resp_child", "child-ticket")}
			reader := newOpenAIImageGenerationControlTestService(secondUpstream)
			reader.accountRepo, reader.cache = repo, cache
			continuation := []byte(`{"model":"gpt-5.4","input":"continue","stream":true,"prompt_cache_key":"same","previous_response_id":"resp_minted","client_metadata":{"session_id":"same","x-codex-turn-state":"minted-ticket"}}`)
			c = identityPolicyContext(77, continuation)
			c.Request.Header.Set("User-Agent", codexCLIUserAgent)
			c.Request.Header.Set(openAIWSTurnStateHeader, "minted-ticket")
			_, err = reader.Forward(context.Background(), c, a, continuation)
			require.NoError(t, err)
			require.Len(t, secondUpstream.requests, 1)
			require.Equal(t, "resp_minted", gjson.GetBytes(secondUpstream.lastBody, "previous_response_id").String())
			require.Equal(t, "minted-ticket", secondUpstream.lastReq.Header.Get(openAIWSTurnStateHeader))
			for _, mismatch := range []string{"other_key", "revision", "namespace", "duplicate", "shadow"} {
				t.Run(mismatch, func(t *testing.T) {
					selected := *a
					selected.Extra = make(map[string]any)
					for k, v := range a.Extra {
						selected.Extra[k] = v
					}
					key := int64(77)
					switch mismatch {
					case "other_key":
						key = 78
					case "revision":
						selected.Extra[CodexIdentityRevisionKey] = "new-revision"
					case "namespace":
						selected.Credentials = map[string]any{"chatgpt_account_id": "different"}
					case "duplicate":
						selected.ID = 52
						selected.Extra = nil
					case "shadow":
						selected.ID = 53
						selected.ParentAccountID = &a.ID
						selected.Extra = nil
						selected.Credentials = nil
					}
					repo.rows[selected.ID] = &selected
					defer func() { delete(repo.rows, selected.ID); repo.rows[a.ID] = a }()
					c := identityPolicyContext(key, continuation)
					_, err := reader.Forward(context.Background(), c, &selected, continuation)
					require.Error(t, err)
					require.Equal(t, http.StatusBadRequest, c.Writer.Status())
					require.Len(t, secondUpstream.requests, 1, "cross-policy continuation must not send")
				})
			}
		})
	}
}

func (r *identityPolicyRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	return r.rows[id], nil
}
func (r *identityPolicyRepo) ValidateCodexIdentityBinding(_ context.Context, a *Account, key, user int64) error {
	r.validations++
	if r.onValidate != nil {
		r.onValidate(r.validations)
	}
	if r.err != nil {
		return r.err
	}
	for id, row := range r.rows {
		if id != a.ID && codexIdentityString(row, CodexIdentityModeKey) == CodexIdentityPreserveClient && CodexIdentityNamespace(row) == CodexIdentityNamespace(a) {
			return errors.New("duplicate binding")
		}
	}
	return nil
}
func identityPolicyAccount(t *testing.T) *Account {
	a := &Account{ID: 41, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "test-credential", "access_token": "test-token"}, Extra: map[string]any{CodexIdentityModeKey: CodexIdentityPreserveClient, CodexIdentityAPIKeyIDKey: 77}}
	require.NoError(t, NormalizeCodexIdentityConfig(a, nil))
	return a
}
func identityPolicyContext(key int64, body []byte) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(body))
	c.Set("api_key", &APIKey{ID: key, UserID: 9})
	for name, value := range map[string]string{"session_id": "same", "session-id": "same", "thread-id": "distinct-thread", "x-client-request-id": "same", "conversation_id": "distinct-conversation", "x-codex-window-id": "window", "x-codex-turn-metadata": `{"session_id":"same","thread_id":"distinct-thread","turn_id":"same"}`} {
		c.Request.Header.Set(name, value)
	}
	return c
}

func TestCodexIdentityPolicyFinalHTTPRequests(t *testing.T) {
	body := []byte(`{"model":"gpt-5.4","stream":true,"prompt_cache_key":"same","client_metadata":{"session_id":"same","thread_id":"distinct-thread","turn_id":"same","x-codex-turn-metadata":"{\"session_id\":\"same\",\"turn_id\":\"same\"}"}}`)
	for _, passthrough := range []bool{false, true} {
		for _, key := range []int64{77, 78} {
			a := identityPolicyAccount(t)
			repo := &identityPolicyRepo{rows: map[int64]*Account{a.ID: a}}
			upstream := &openCodeSessionHTTPUpstream{}
			s := &OpenAIGatewayService{accountRepo: repo, httpUpstream: upstream}
			c := identityPolicyContext(key, body)
			_, err := s.prepareCodexAccountIdentitySource(context.Background(), c, a)
			require.NoError(t, err)
			stageCodexClientIdentityBody(c, body)
			projected, _, err := applyCodexAccountIdentityClientMetadataRaw(body, codexAccountIdentitySource(c, a), key)
			require.NoError(t, err)
			var req *http.Request
			if passthrough {
				req, err = s.buildUpstreamRequestOpenAIPassthrough(context.Background(), c, a, projected, "token")
			} else {
				req, err = s.buildUpstreamRequest(context.Background(), c, a, projected, "token", true, gjson.GetBytes(projected, "prompt_cache_key").String(), true)
			}
			require.NoError(t, err)
			_, err = s.doOpenAIUpstreamWithAdmission(req, "", a)
			require.NoError(t, err)
			require.Same(t, req, upstream.request)
			wire, err := io.ReadAll(upstream.request.Body)
			require.NoError(t, err)
			require.Equal(t, codexOutboundAcceptLanguage, req.Header.Get("Accept-Language"))
			require.NotEmpty(t, req.Header.Get("originator"))
			require.NotEmpty(t, req.Header.Get("version"))
			if key == 77 {
				for _, name := range []string{"session_id", "session-id", "thread-id", "x-client-request-id", "conversation_id", "x-codex-window-id", "x-codex-turn-metadata"} {
					require.Equal(t, c.Request.Header.Get(name), req.Header.Get(name), name)
				}
				require.JSONEq(t, string(body), string(wire))
			} else {
				require.NotEqual(t, "same", req.Header.Get("session_id"))
				require.NotEqual(t, "same", gjson.GetBytes(wire, "prompt_cache_key").String())
				require.NotEqual(t, "same", gjson.GetBytes(wire, "client_metadata.session_id").String())
			}
			require.Nil(t, a.codexIdentityGrant, "shared source must never receive request authorization")
		}
	}
	c1, c2 := identityPolicyContext(77, body), identityPolicyContext(78, body)
	scope1, _ := resolveOpenAIWSExecutionScope(c1, body, 77)
	scope2, _ := resolveOpenAIWSExecutionScope(c2, body, 78)
	require.NotEqual(t, scope1, scope2, "internal cache/execution scopes remain tenant-isolated")
}

func TestCodexIdentityPolicyRevocationAndFallback(t *testing.T) {
	for _, reason := range []string{"duplicate", "namespace", "key", "fingerprint", "internal", "nonbound"} {
		t.Run(reason, func(t *testing.T) {
			a := identityPolicyAccount(t)
			repo := &identityPolicyRepo{rows: map[int64]*Account{a.ID: a}}
			s := &OpenAIGatewayService{accountRepo: repo}
			key := int64(77)
			switch reason {
			case "duplicate":
				other := *a
				other.ID = 42
				repo.rows[42] = &other
			case "namespace":
				a.Credentials["chatgpt_user_id"] = "new-user"
			case "key":
				repo.err = errors.New("key unavailable")
			case "fingerprint":
				a.Extra["codex_fingerprint_mode"] = "device"
			case "internal":
				key = 0
			case "nonbound":
				key = 78
			}
			c := identityPolicyContext(key, nil)
			_, err := s.prepareCodexAccountIdentitySource(context.Background(), c, a)
			require.NoError(t, err)
			require.Equal(t, CodexIdentityIsolated, EffectiveCodexIdentityPolicy(c, a).Mode)
			require.NotEqual(t, "same", isolateOpenAIUpstreamSessionID(key, codexAccountIdentitySource(c, a), "same"))
		})
	}
}

func TestCodexIdentityConfigServerOwnedAndSemanticRevision(t *testing.T) {
	a := identityPolicyAccount(t)
	old := *a
	a.Extra = map[string]any{CodexIdentityModeKey: CodexIdentityPreserveClient, CodexIdentityAPIKeyIDKey: 77, CodexIdentityNamespaceKey: "forged", CodexIdentityRevisionKey: "forged"}
	require.NoError(t, NormalizeCodexIdentityConfig(a, &old))
	require.Equal(t, old.Extra[CodexIdentityRevisionKey], a.Extra[CodexIdentityRevisionKey])
	require.Equal(t, CodexIdentityNamespace(a), a.Extra[CodexIdentityNamespaceKey])
	a.Credentials = map[string]any{"chatgpt_account_id": "changed"}
	require.NoError(t, NormalizeCodexIdentityConfig(a, &old))
	require.Equal(t, CodexIdentityIsolated, a.Extra[CodexIdentityModeKey])
	require.Contains(t, a.Extra[CodexIdentityDiagnosticKey], "namespace_changed")
	require.NotEqual(t, old.Extra[CodexIdentityRevisionKey], a.Extra[CodexIdentityRevisionKey])
	for _, mode := range []string{"device", "session", "full"} {
		a = identityPolicyAccount(t)
		a.Extra["codex_fingerprint_mode"] = mode
		require.ErrorContains(t, NormalizeCodexIdentityConfig(a, nil), "conflicts")
	}
	a = identityPolicyAccount(t)
	a.Credentials = nil
	require.ErrorContains(t, NormalizeCodexIdentityConfig(a, nil), "stable credential namespace")
	for _, key := range []any{nil, 0, -1, 1.5, "77"} {
		a = identityPolicyAccount(t)
		a.Extra[CodexIdentityAPIKeyIDKey] = key
		require.Error(t, NormalizeCodexIdentityConfig(a, nil))
	}
	a = identityPolicyAccount(t)
	a.Credentials = nil
	a.Extra[codexFingerprintSeedExtraKey] = "11111111-1111-4111-8111-111111111111"
	require.Empty(t, CodexIdentityNamespace(a), "row-local seed is not an actual credential namespace")
	require.ErrorContains(t, NormalizeCodexIdentityConfig(a, nil), "stable credential namespace")
	setup := &Account{Platform: PlatformOpenAI, Type: AccountTypeSetupToken, Credentials: map[string]any{"access_token": "fake-setup"}}
	ns := CodexIdentityNamespace(setup)
	setup.Extra = map[string]any{codexFingerprintSeedExtraKey: "11111111-1111-4111-8111-111111111111"}
	require.NotEmpty(t, ns)
	require.Equal(t, ns, CodexIdentityNamespace(setup), "seed cannot split duplicate setup credentials")
	for _, authMode := range []string{OpenAIAuthModeAgentIdentity, OpenAIAuthModePersonalAccessToken} {
		a = identityPolicyAccount(t)
		old := *a
		a.Credentials = map[string]any{"chatgpt_account_id": "test-credential", "access_token": "test-token", "auth_mode": authMode}
		require.NoError(t, NormalizeCodexIdentityConfig(a, &old))
		require.Equal(t, CodexIdentityIsolated, a.Extra[CodexIdentityModeKey], "credential auth mode change revokes authorization")
	}
	a = identityPolicyAccount(t)
	a.Credentials["auth_mode"] = OpenAIAuthModeAgentIdentity
	require.ErrorContains(t, NormalizeCodexIdentityConfig(a, nil), "Agent Identity")
}

func TestCodexIdentityPolicyDriftIsRequestErrorWithoutSendOrFailover(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(map[bool]string{false: "native", true: "passthrough"}[passthrough], func(t *testing.T) {
			a := identityPolicyAccount(t)
			a.Extra["openai_passthrough"] = passthrough
			repo := &identityPolicyRepo{rows: map[int64]*Account{a.ID: a}}
			repo.onValidate = func(n int) {
				if n == 2 {
					repo.err = errors.New("storage unavailable")
				}
			}
			upstream := &httpUpstreamRecorder{}
			s := newOpenAIImageGenerationControlTestService(upstream)
			s.accountRepo = repo
			body := []byte(`{"model":"gpt-5.4","input":"test","stream":true}`)
			c := identityPolicyContext(77, body)
			c.Request.Header.Set("User-Agent", codexCLIUserAgent)
			_, err := s.Forward(context.Background(), c, a, body)
			require.ErrorContains(t, err, "CODEX_IDENTITY_VALIDATION_UNAVAILABLE")
			require.Empty(t, upstream.requests)
			require.Equal(t, http.StatusBadRequest, c.Writer.Status())
			require.Nil(t, a.TempUnschedulableUntil)
		})
	}
}

func TestCodexIdentityHTTPContinuationAfterDisableAndDefaultCacheIndependence(t *testing.T) {
	a := identityPolicyAccount(t)
	repo := &identityPolicyRepo{rows: map[int64]*Account{a.ID: a}}
	cache := &identityContinuationTestCache{stubGatewayCache: &stubGatewayCache{}, bindings: map[string]OpenAIIdentityContinuation{}}
	s := &OpenAIGatewayService{accountRepo: repo, cache: cache}
	c := identityPolicyContext(77, nil)
	_, err := s.prepareCodexAccountIdentitySource(context.Background(), c, a)
	require.NoError(t, err)
	s.bindCodexIdentityContinuation(context.Background(), c, a, "response", "raw_response")
	old := *a
	a.Extra = map[string]any{CodexIdentityModeKey: CodexIdentityIsolated}
	require.NoError(t, NormalizeCodexIdentityConfig(a, &old))
	require.True(t, codexIdentityHasHistory(a))
	c = identityPolicyContext(77, nil)
	_, err = s.prepareCodexAccountIdentitySource(context.Background(), c, a)
	require.NoError(t, err)
	require.ErrorContains(t, s.guardCodexIdentityRequest(c, a, []byte(`{"previous_response_id":"raw_response"}`), "http"), "CONTINUATION_MISMATCH")
	c = identityPolicyContext(77, nil)
	_, err = s.prepareCodexAccountIdentitySource(context.Background(), c, a)
	require.NoError(t, err)
	s.bindCodexIdentityContinuation(context.Background(), c, a, "response", "new_isolated_response")
	require.NoError(t, s.guardCodexIdentityRequest(c, a, []byte(`{"previous_response_id":"new_isolated_response"}`), "http"))
	cache.err = errors.New("offline")
	c = identityPolicyContext(77, nil)
	_, err = s.prepareCodexAccountIdentitySource(context.Background(), c, a)
	require.NoError(t, err)
	require.ErrorContains(t, s.guardCodexIdentityRequest(c, a, []byte(`{"previous_response_id":"raw_response"}`), "http"), "CONTINUATION_UNAVAILABLE")
	for _, store := range []GatewayCache{cache, &stubGatewayCache{}} {
		// An explicitly configured ordinary default account has no raw history.
		ordinary := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{CodexIdentityModeKey: CodexIdentityIsolated, CodexIdentityHistoryKey: true}}
		require.NoError(t, NormalizeCodexIdentityConfig(ordinary, nil))
		require.False(t, codexIdentityHasHistory(ordinary), "caller cannot forge server history")
		s.cache = store
		c = identityPolicyContext(77, nil)
		_, err = s.prepareCodexAccountIdentitySource(context.Background(), c, ordinary)
		require.NoError(t, err)
		require.NoError(t, s.guardCodexIdentityRequest(c, ordinary, []byte(`{"previous_response_id":"ordinary_response"}`), "http"))
	}
}

func TestCodexIdentityBeforeSendContinuationRecheck(t *testing.T) {
	a := identityPolicyAccount(t)
	repo := &identityPolicyRepo{rows: map[int64]*Account{a.ID: a}}
	cache := &identityContinuationTestCache{stubGatewayCache: &stubGatewayCache{}, bindings: map[string]OpenAIIdentityContinuation{}}
	s := &OpenAIGatewayService{accountRepo: repo, cache: cache}
	c := identityPolicyContext(77, nil)
	_, err := s.prepareCodexAccountIdentitySource(context.Background(), c, a)
	require.NoError(t, err)
	s.bindCodexIdentityContinuation(context.Background(), c, a, "response", "previous")
	s.bindCodexIdentityContinuation(context.Background(), c, a, "ticket", "ticket")
	c.Request.Header.Set(openAIWSTurnStateHeader, "ticket")
	req, err := s.buildUpstreamRequest(context.Background(), c, a, []byte(`{"model":"gpt-5.4","previous_response_id":"previous"}`), "fake", true, "", true)
	require.NoError(t, err)
	require.NoError(t, s.validateCodexIdentityBeforeSend(req, a))
	cache.bindings[OpenAIIdentityContinuationKey(0, "ticket", "ticket")] = OpenAIIdentityContinuation{}
	require.ErrorContains(t, s.validateCodexIdentityBeforeSend(req, a), "CONTINUATION_MISMATCH")
	c.Request.Header.Add(openAIWSTurnStateHeader, "unknown-second-value")
	require.Error(t, s.guardCodexIdentityRequest(c, a, nil, "http"), "every ticket header value must be checked")
}

func TestCodexIdentityWSRejectsBeforeUpstreamAndReportsPolicyClose(t *testing.T) {
	a := identityPolicyAccount(t)
	repo := &identityPolicyRepo{rows: map[int64]*Account{a.ID: a}}
	upstream := &httpUpstreamRecorder{}
	s := newOpenAIImageGenerationControlTestService(upstream)
	s.accountRepo = repo
	body := []byte(`{"model":"gpt-5.4","input":"test","stream":true}`)
	c := identityPolicyContext(77, body)
	SetOpenAIClientTransport(c, OpenAIClientTransportWS)
	_, err := s.Forward(context.Background(), c, a, body)
	require.ErrorContains(t, err, "CODEX_IDENTITY_TRANSPORT_UNSUPPORTED")
	require.Empty(t, upstream.requests)
	require.Equal(t, http.StatusBadRequest, c.Writer.Status())
	c = identityPolicyContext(77, body)
	SetOpenAIClientTransport(c, OpenAIClientTransportWS)
	_, err = s.prepareCodexAccountIdentitySource(context.Background(), c, a)
	require.NoError(t, err)
	c.Writer.WriteHeader(http.StatusSwitchingProtocols)
	c.Writer.WriteHeaderNow()
	err = s.guardCodexIdentityRequest(c, a, body, "ws")
	var closeErr *OpenAIWSClientCloseError
	require.ErrorAs(t, err, &closeErr)
	require.Equal(t, coderws.StatusPolicyViolation, closeErr.StatusCode())
}

func TestCodexIdentityTicketBindingRequiresCommittedPassthroughHeader(t *testing.T) {
	a := identityPolicyAccount(t)
	cache := &identityContinuationTestCache{stubGatewayCache: &stubGatewayCache{}, bindings: map[string]OpenAIIdentityContinuation{}}
	s := &OpenAIGatewayService{accountRepo: &identityPolicyRepo{rows: map[int64]*Account{a.ID: a}}, cache: cache}
	c := identityPolicyContext(77, nil)
	_, err := s.prepareCodexAccountIdentitySource(context.Background(), c, a)
	require.NoError(t, err)
	h := http.Header{}
	h.Set(openAIWSTurnStateHeader, "ticket")
	c.Writer.Header().Set(openAIWSTurnStateHeader, "ticket")
	s.bindCommittedCodexTurnState(c, a, h)
	require.Empty(t, cache.bindings, "discarded response headers are not authorization")
	c.Writer.WriteHeaderNow()
	s.bindCommittedCodexTurnState(c, a, h)
	require.Len(t, cache.bindings, 1)
}

func TestCodexIdentityUnsupportedAndBeforeSendDrift(t *testing.T) {
	a := identityPolicyAccount(t)
	repo := &identityPolicyRepo{rows: map[int64]*Account{a.ID: a}}
	s := &OpenAIGatewayService{accountRepo: repo}
	for _, transport := range []string{"ws", "chat_bridge", "messages_bridge", "alpha_search"} {
		c := identityPolicyContext(77, nil)
		_, err := s.prepareCodexAccountIdentitySource(context.Background(), c, a)
		require.NoError(t, err)
		require.ErrorContains(t, s.guardCodexIdentityRequest(c, a, nil, transport), "native Responses HTTP")
	}
	for _, body := range []string{`{"previous_response_id":"unknown"}`, `{"client_metadata":{"x-codex-turn-state":"ticket"}}`} {
		c := identityPolicyContext(77, nil)
		_, _ = s.prepareCodexAccountIdentitySource(context.Background(), c, a)
		require.ErrorContains(t, s.guardCodexIdentityRequest(c, a, []byte(body), "http"), "new session")
	}
	c := identityPolicyContext(77, nil)
	_, _ = s.prepareCodexAccountIdentitySource(context.Background(), c, a)
	req, err := s.buildUpstreamRequest(context.Background(), c, a, []byte(`{"model":"gpt-5.4"}`), "token", true, "", true)
	require.NoError(t, err)
	a.Extra[CodexIdentityRevisionKey] = "changed"
	require.ErrorContains(t, s.validateCodexIdentityBeforeSend(req, a), "changed before send")
	// A reused gin context must not retain the grant during failover/shadow routing.
	other := &Account{ID: 52, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "other"}}
	_, err = s.prepareCodexAccountIdentitySource(context.Background(), c, other)
	require.NoError(t, err)
	require.Equal(t, CodexIdentityIsolated, EffectiveCodexIdentityPolicy(c, other).Mode)
	parentID := a.ID
	shadow := &Account{ID: 53, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parentID}
	_, err = s.prepareCodexAccountIdentitySource(context.Background(), c, shadow)
	require.NoError(t, err)
	require.Equal(t, CodexIdentityIsolated, EffectiveCodexIdentityPolicy(c, shadow).Mode)
}
