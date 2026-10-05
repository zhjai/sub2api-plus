//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func requireR6EnglishLanguageWire(t *testing.T, h http.Header) {
	t.Helper()
	var wire bytes.Buffer
	require.NoError(t, h.Write(&wire))
	var values []string
	for _, line := range strings.Split(wire.String(), "\r\n") {
		name, value, found := strings.Cut(line, ":")
		if found && strings.EqualFold(name, "Accept-Language") {
			values = append(values, strings.TrimSpace(value))
		}
	}
	require.Equal(t, []string{codexOutboundAcceptLanguage}, values)
}

func TestR6CodexLanguageRemovesEveryWireCasing(t *testing.T) {
	h := http.Header{
		"Accept-Language": {"zh-CN"}, "accept-language": {"zh"}, "ACCEPT-LANGUAGE": {"zh-TW", "zh-HK"},
	}
	for i := 0; i < 2; i++ {
		enforceCodexAcceptLanguage(h)
		requireR6EnglishLanguageWire(t, h)
	}
}

func TestR6CodexBuildersEnforceLanguageAfterAccountOverride(t *testing.T) {
	c, _ := newTestContext()
	c.Request.Header.Set("User-Agent", codexCLIUserAgent)
	c.Request.Header.Set("Accept-Language", "zh-CN")
	account := &Account{ID: 62, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key": "test-token", "base_url": "https://example.test/v1",
			credKeyHeaderOverrideEnabled: true, credKeyHeaderOverrides: map[string]any{"accept-language": "zh-TW"},
		},
	}
	svc := &OpenAIGatewayService{}
	req, err := svc.buildUpstreamRequest(t.Context(), c, account, []byte(`{"model":"gpt-6-astra","stream":true}`), "test-token", false, "", true)
	require.NoError(t, err)
	requireR6EnglishLanguageWire(t, req.Header)
	headers, _, err := svc.buildOpenAIWSHeaders(t.Context(), c, account, "test-token",
		OpenAIWSProtocolDecision{Transport: OpenAIUpstreamTransportResponsesWebsocketV2}, true, "", "", "", "", "")
	require.NoError(t, err)
	requireR6EnglishLanguageWire(t, headers)
	seen := make(chan http.Header, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Clone()
		conn, err := coderws.Accept(w, r, nil)
		if err == nil {
			_ = conn.CloseNow()
		}
	}))
	defer server.Close()
	conn, _, err := coderws.Dial(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http"), &coderws.DialOptions{HTTPHeader: headers})
	require.NoError(t, err)
	defer conn.CloseNow()
	requireR6EnglishLanguageWire(t, <-seen)
}

func TestR6EvaluationLanguageAfterAccountOverride(t *testing.T) {
	account := &Account{ID: 63, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key": "test-token", "base_url": "https://example.test/v1",
			credKeyHeaderOverrideEnabled: true, credKeyHeaderOverrides: map[string]any{"Accept-Language": "zh-CN"},
		},
	}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(http.StatusOK,
		`{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"29"}]}]}`)}}
	svc := &AccountTestService{httpUpstream: upstream, cfg: &config.Config{}, tlsFPProfileService: &TLSFingerprintProfileService{}}
	_, err := svc.RunOpenAIEvalSample(t.Context(), &OpenAIEvalTarget{Account: account, Credential: account, UpstreamModel: "gpt-6-astra"}, "test", "high")
	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	requireR6EnglishLanguageWire(t, upstream.requests[0].Header)
}

func TestR6AccountProbeLanguageAfterAccountOverride(t *testing.T) {
	for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth} {
		for _, mode := range []string{AccountTestModeDefault, AccountTestModeCompact} {
			t.Run(accountType+"/"+mode, func(t *testing.T) {
				account := &Account{ID: 63, Platform: PlatformOpenAI, Type: accountType,
					Credentials: map[string]any{
						"api_key": "test-token", "access_token": "test-token", "base_url": "https://example.test/v1",
						credKeyHeaderOverrideEnabled: true, credKeyHeaderOverrides: map[string]any{"accept-language": "zh-CN"},
					},
				}
				upstream := &queuedHTTPUpstream{responses: []*http.Response{newJSONResponse(http.StatusOK,
					"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[]}}\n\n")}}
				svc := &AccountTestService{httpUpstream: upstream, cfg: &config.Config{}}
				c, _ := newTestContext()
				// Probe verdicts can differ; every issued request must use the same locale.
				_ = svc.testOpenAIAccountConnection(c, account, "gpt-6-astra", "", mode)
				require.Len(t, upstream.requests, 1)
				requireR6EnglishLanguageWire(t, upstream.requests[0].Header)
			})
		}
	}
}

func TestR6BPSLegacyCountersCannotAuthorizeAutomaticActivation(t *testing.T) {
	legacy := OpenAIBPSModelState{Active: true, DegradedStreak: 99, HealthyStreak: 99}
	account := &Account{ID: 64, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Extra: map[string]any{OpenAIBPSModelStateExtraKeyFor("gpt-6-astra"): legacy}}
	repo := &openAIEvalRepoFake{config: &OpenAIEvalConfig{BPSAutoEnabled: true,
		BPSAccounts: []OpenAIEvalBPSAccountConfig{{AccountID: account.ID, Mode: OpenAIEvalBPSModeAuto, FailureThreshold: 3, RecoveryThreshold: 2}}}}
	svc := &OpenAIGatewayService{openAIEvalRepo: repo}
	require.False(t, svc.isOpenAIBPSForwardEligible(t.Context(), account, "gpt-6-astra"))
	state := readOpenAIBPSAccountState(account)
	for i := 1; i <= 3; i++ {
		state, _ = nextOpenAIBPSAccountState(state, "degraded", 3, 2)
		account.Extra[OpenAIBPSAccountStateExtraKey()] = state
		require.Equal(t, i == 3, svc.isOpenAIBPSForwardEligible(t.Context(), account, "gpt-6-astra"))
	}
	require.Equal(t, legacy, account.Extra[OpenAIBPSModelStateExtraKeyFor("gpt-6-astra")])
	for i := 1; i <= 2; i++ {
		state, _ = nextOpenAIBPSAccountState(state, "healthy", 3, 2)
		account.Extra[OpenAIBPSAccountStateExtraKey()] = state
		require.Equal(t, i < 2, svc.isOpenAIBPSForwardEligible(t.Context(), account, "gpt-6-astra"))
	}
}

func TestR6ModelTraceAttributionOnlyLunaIsSuspect(t *testing.T) {
	for _, tc := range []struct{ model, status string }{
		{"gpt-6-astra", "suspected_normal"}, {"gpt-6-sol", "suspected_normal"},
		{"claude-opus-5-5", "suspected_normal"}, {"gpt-5.6-luna", "warning"},
		{"GPT-6-LUNA", "warning"}, {"vendor/gpt-6-luna", "warning"}, {"", "insufficient"},
	} {
		t.Run(tc.model, func(t *testing.T) {
			outcome := modelTraceSchedulingOutcome("public-model", &OpenAIEvalModelTraceResult{UsedOutputs: 3, Prediction: tc.model}, nil)
			require.Equal(t, tc.status, outcome.Status)
			require.Equal(t, "alert_only", outcome.Scheduling)
			require.Zero(t, OpenAIEvalRoutePenalty(OpenAIEvalTypeModelTrace, outcome, true))
		})
	}
	for _, err := range []error{nil, errors.New("request failed")} {
		outcome := modelTraceSchedulingOutcome("public-model", &OpenAIEvalModelTraceResult{Prediction: "gpt-6-astra"}, err)
		require.Equal(t, "insufficient", outcome.Status)
	}
}

func TestR6AttributionHistoryUsesCurrentPolicyWithoutRewritingEvidence(t *testing.T) {
	for _, status := range []string{"different", "consistent", "uncertain", "warning"} {
		jsd := 0.9
		fp := &OpenAIEvalFingerprintResult{Status: status, NearestModel: "gpt-6-sol", MeanJSD: &jsd, CellCount: 4, ValidSamples: 60, RequiredSamples: 60}
		run := OpenAIEvalRun{TestType: OpenAIEvalTypeFingerprint, Status: status, Outcome: OpenAIEvalOutcome{Status: status, Fingerprint: fp}}
		normalizeOpenAIEvalAttributionRun(&run)
		require.Equal(t, "suspected_normal", run.Status)
		require.Equal(t, "suspected_normal", run.Outcome.Fingerprint.Status)
		require.Equal(t, status, fp.Status, "stored/source evidence remains unchanged")
	}
	for _, status := range []string{"running", "error", "insufficient"} {
		run := OpenAIEvalRun{TestType: OpenAIEvalTypeModelTrace, Status: status, Outcome: OpenAIEvalOutcome{Status: status, ModelTrace: &OpenAIEvalModelTraceResult{UsedOutputs: 1, Prediction: "gpt-6-sol"}}}
		normalizeOpenAIEvalAttributionRun(&run)
		require.Equal(t, status, run.Status)
	}
}

func TestR6FingerprintNonLunaRemainsSuspectedNormalWhenDifferent(t *testing.T) {
	var samples []OpenAIEvalSample
	for _, probe := range OpenAIEvalFingerprintProbes[:4] {
		for i := 0; i < 15; i++ {
			samples = append(samples, OpenAIEvalSample{ProbeID: probe.ID, Answer: OpenAIEvalFingerprintBaselines[0].Cells[probe.ID][i]})
		}
	}
	for _, tc := range []struct{ model, status string }{{"gpt-6-sol", "suspected_normal"}, {"gpt-6-luna", "warning"}} {
		t.Run(tc.model, func(t *testing.T) {
			baseline := OpenAIEvalFingerprintBaseline{Model: tc.model, Cells: OpenAIEvalFingerprintBaselines[0].Cells}
			result := ScoreOpenAIEvalFingerprint("gpt-6-astra", samples, []OpenAIEvalFingerprintBaseline{baseline}, 60)
			require.Equal(t, tc.status, result.Status)
			require.Equal(t, tc.model, result.NearestModel)
			require.NotNil(t, result.MeanJSD)
			require.Zero(t, OpenAIEvalRoutePenalty(OpenAIEvalTypeFingerprint, OpenAIEvalOutcome{Status: result.Status}, true))
		})
	}
}

type r6ResponseLookupCache struct {
	stubGatewayCache
	fail bool
}

func (c *r6ResponseLookupCache) GetSessionAccountID(ctx context.Context, groupID int64, key string) (int64, error) {
	if c.fail && key == openAIWSResponseAccountCacheKey("resp_opaque_foreign") {
		return 0, errors.New("response owner cache unavailable")
	}
	return c.stubGatewayCache.GetSessionAccountID(ctx, groupID, key)
}

func TestR6OrdinaryPooledWSValidatesOriginalOpaqueOwnerBeforeReconstruction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name                                             string
		bound, opaque, unresolved, cacheFailure, deleted bool
		wantClose                                        coderws.StatusCode
	}{
		{name: "opaque_owner", bound: true, opaque: true, wantClose: coderws.StatusPolicyViolation},
		{name: "unresolved_owner", bound: true, unresolved: true, wantClose: coderws.StatusPolicyViolation},
		{name: "deleted_owner", bound: true, deleted: true, wantClose: coderws.StatusPolicyViolation},
		{name: "operational_lookup_error", cacheFailure: true, wantClose: coderws.StatusTryAgainLater},
		{name: "ordinary_owner", bound: true},
		{name: "ordinary_cache_miss", wantClose: coderws.StatusPolicyViolation},
	} {
		for _, windowChange := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/window_change_%v", tc.name, windowChange), func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				cfg := passthroughLifecycleConfig()
				cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
				account := passthroughLifecycleAccount()
				account.Extra["openai_apikey_responses_websockets_v2_mode"] = OpenAIWSIngressModeCtxPool
				svc := newPassthroughLifecycleService(cfg, newStagedPassthroughConn())
				svc.cache = &r6ResponseLookupCache{fail: tc.cacheFailure}
				owner := &Account{ID: account.ID + 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{"openai_opaque_upstream": tc.opaque}}
				svc.accountRepo = &mockAccountRepoForGemini{accountsByID: map[int64]*Account{owner.ID: owner}}
				if tc.unresolved {
					svc.accountRepo = &mockAccountRepoForGemini{accountsByID: map[int64]*Account{owner.ID: nil}}
				}
				if tc.deleted {
					svc.accountRepo = &r7OwnerAccountRepo{err: fmt.Errorf("lookup: %w", ErrAccountNotFound)}
				}
				if tc.bound {
					require.NoError(t, svc.getOpenAIWSStateStore().BindResponseAccount(ctx, 0, "resp_opaque_foreign", owner.ID, time.Hour))
				}
				capture := &openAIWSCaptureConn{events: [][]byte{
					[]byte(`{"type":"response.completed","response":{"id":"resp_first","model":"gpt-5.1"}}`),
					[]byte(`{"type":"response.completed","response":{"id":"resp_second","model":"gpt-5.1"}}`),
				}}
				pool := newOpenAIWSConnPool(cfg)
				pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: capture})
				svc.openaiWSPool = pool
				defer pool.Close()
				server, serverErr := startPassthroughLifecycleServerWithHooks(t, ctx, svc, account, nil)
				defer server.Close()
				client := dialPassthroughLifecycleClientWithPayload(t, server,
					`{"type":"response.create","model":"gpt-5.1","store":false,"client_metadata":{"x-codex-window-id":"window-a"},"input":"first"}`)
				defer client.CloseNow()
				_, err := readPassthroughLifecycleFrame(t, client, 3*time.Second)
				require.NoError(t, err)
				window := "window-a"
				if windowChange {
					window = "window-b"
				}
				err = client.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1","store":false,"previous_response_id":"resp_opaque_foreign","client_metadata":{"x-codex-window-id":"`+window+`"},"input":"continue"}`))
				require.NoError(t, err)
				if tc.wantClose == 0 {
					_, err = readPassthroughLifecycleFrame(t, client, 3*time.Second)
					require.NoError(t, err)
					require.NoError(t, client.Close(coderws.StatusNormalClosure, "done"))
				}
				select {
				case err := <-serverErr:
					if tc.wantClose != 0 {
						var closeErr *OpenAIWSClientCloseError
						require.ErrorAs(t, err, &closeErr)
						require.Equal(t, tc.wantClose, closeErr.StatusCode())
					} else {
						require.NoError(t, err)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("unsafe original continuation was not rejected")
				}
				capture.mu.Lock()
				defer capture.mu.Unlock()
				if tc.wantClose != 0 {
					require.Len(t, capture.writes, 1, "unsafe response must never reach the ordinary upstream")
				} else {
					require.Len(t, capture.writes, 2, "safe ordinary reconstruction remains supported")
				}
			})
		}
	}
}
