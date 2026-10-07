package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	openaiwsv2 "github.com/Wei-Shaw/sub2api/internal/service/openai_ws_v2"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type hardRPMCache struct {
	GatewayCache
	limit   int
	used    int
	ids     map[string]struct{}
	err     error
	onAdmit func()
}

func (c *hardRPMCache) AdmitAccountRPM(ctx context.Context, id int64, limit int, attempt string) (AccountRPMDecision, error) {
	if c.onAdmit != nil {
		c.onAdmit()
	}
	if c.err != nil {
		return AccountRPMDecision{}, c.err
	}
	c.limit = limit
	if c.ids == nil {
		c.ids = make(map[string]struct{})
	}
	if _, ok := c.ids[attempt]; ok {
		return AccountRPMDecision{}, errors.New("dispatch ID reused")
	}
	c.ids[attempt] = struct{}{}
	if c.used >= limit {
		return AccountRPMDecision{Used: c.used, RetryAfter: 21 * time.Second}, nil
	}
	c.used++
	return AccountRPMDecision{Allowed: true, Used: c.used}, nil
}
func (c *hardRPMCache) ReadAccountRPMBatch(_ context.Context, limits map[int64]int) (map[int64]AccountRPMDecision, error) {
	if c.err != nil {
		return nil, c.err
	}
	out := make(map[int64]AccountRPMDecision)
	for id := range limits {
		out[id] = AccountRPMDecision{Used: c.used}
	}
	return out, nil
}

type hardRPMRepo struct {
	AccountRepository
	account *Account
	err     error
	reads   int
}

func (r *hardRPMRepo) GetByID(context.Context, int64) (*Account, error) {
	r.reads++
	return r.account, r.err
}
func (r *hardRPMRepo) Update(_ context.Context, a *Account) error { r.account = a; return nil }
func (r *hardRPMRepo) ListShadowsByParent(context.Context, int64) ([]*Account, error) {
	return nil, nil
}
func (r *hardRPMRepo) UpdateExtra(_ context.Context, _ int64, extra map[string]any) error {
	if r.account.Extra == nil {
		r.account.Extra = make(map[string]any)
	}
	for k, v := range extra {
		r.account.Extra[k] = v
	}
	return nil
}
func (r *hardRPMRepo) BulkUpdate(ctx context.Context, _ []int64, updates AccountBulkUpdate) (int64, error) {
	return 1, r.UpdateExtra(ctx, r.account.ID, updates.Extra)
}

type hardRPMUpstream struct {
	HTTPUpstream
	sends  int
	retry  bool
	status int
}

func (u *hardRPMUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.sends++
	if u.retry {
		if err := AdmitAccountRPMHTTPRetry(req.Context()); err != nil {
			return nil, err
		}
		u.sends++
	}
	status := u.status
	if status == 0 {
		status = 200
	}
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("{}"))}, nil
}
func (u *hardRPMUpstream) DoWithTLS(req *http.Request, proxy string, id int64, n int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, n)
}

func TestAccountHardRPMStrictConfig(t *testing.T) {
	for _, v := range []any{nil, true, "1", -1, 10001, 0.5, json.Number("1.5"), []any{1}, map[string]any{}} {
		t.Run(fmt.Sprintf("%T_%v", v, v), func(t *testing.T) { require.Error(t, ValidateAccountRPMExtra(map[string]any{"rpm_limit": v})) })
	}
	for _, v := range []any{0, 1, 10000, int64(3), float64(9), json.Number("6")} {
		require.NoError(t, ValidateAccountRPMExtra(map[string]any{"rpm_limit": v}))
	}
	require.NoError(t, ValidateAccountRPMExtra(nil))
	a := &Account{Extra: map[string]any{"base_rpm": "7", "rpm_limit": 2}}
	require.Equal(t, 7, a.GetBaseRPM())
	n, err := a.AccountRPMLimit()
	require.NoError(t, err)
	require.Equal(t, 2, n)
}

func TestAccountHardRPMHTTPBoundaries(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformAnthropic, PlatformGemini, PlatformAntigravity} {
		t.Run(platform, func(t *testing.T) {
			cache := &hardRPMCache{}
			a := &Account{ID: 3, Platform: platform, Extra: map[string]any{"rpm_limit": 2}}
			repo := &hardRPMRepo{account: a}
			up := &hardRPMUpstream{}
			req, _ := http.NewRequestWithContext(context.Background(), "POST", "https://example.invalid/v1/responses", nil)
			for i := 0; i < 3; i++ {
				var resp *http.Response
				var err error
				switch platform {
				case PlatformOpenAI:
					resp, err = (&OpenAIGatewayService{cache: cache, accountRepo: repo, httpUpstream: up}).doOpenAIUpstream(req, "", a)
				case PlatformAnthropic:
					resp, err = (&GatewayService{cache: cache, accountRepo: repo, httpUpstream: up}).doAccountRPMUpstreamTLS(req, "", a, &tlsfingerprint.Profile{})
				case PlatformGemini:
					resp, err = (&GeminiMessagesCompatService{cache: cache, accountRepo: repo, httpUpstream: up}).doAccountRPMUpstream(req, "", a)
				case PlatformAntigravity:
					resp, err = (&AntigravityGatewayService{cache: cache, accountRepo: repo, httpUpstream: up}).doAccountRPMUpstream(req, "", a)
				}
				if i < 2 {
					require.NoError(t, err)
					resp.Body.Close()
				} else {
					require.True(t, IsAccountRPMError(err))
					require.Nil(t, resp)
				}
			}
			require.Equal(t, 2, up.sends)
			require.Equal(t, 2, cache.used)
			require.Equal(t, 3, repo.reads)
			// Disable in the repository, while the selected account still says 2.
			repo.account = &Account{ID: 3, Extra: map[string]any{"rpm_limit": 0}}
			cache.err = errors.New("cache offline")
			resp, err := accountRPMDo(up, cache, repo, a, req, "", nil)
			require.NoError(t, err)
			resp.Body.Close()
			require.Equal(t, 3, up.sends)
		})
	}
}

func TestAccountHardRPMRetryAndCancellation(t *testing.T) {
	a := &Account{ID: 1, Extra: map[string]any{"rpm_limit": 1}}
	cache := &hardRPMCache{}
	up := &hardRPMUpstream{retry: true}
	req, _ := http.NewRequestWithContext(context.Background(), "POST", "https://example.invalid", nil)
	_, err := accountRPMDo(up, cache, nil, a, req, "", nil)
	require.True(t, IsAccountRPMError(err))
	require.Equal(t, 1, up.sends)
	require.Equal(t, 1, cache.used)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = accountRPMDo(up, cache, nil, a, req.WithContext(ctx), "", nil)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, up.sends)
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	cache = &hardRPMCache{onAdmit: cancel}
	_, err = accountRPMDo(up, cache, nil, a, req.WithContext(ctx), "", nil)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, up.sends)
	require.Equal(t, 1, cache.used)
}

func TestAccountHardRPMUnavailableAndHealthIsolation(t *testing.T) {
	a := &Account{ID: 1, Extra: map[string]any{"rpm_limit": 1}}
	for _, cache := range []GatewayCache{nil, &hardRPMCache{err: errors.New("redis offline")}} {
		err := admitAccountRPM(context.Background(), cache, nil, a)
		var local *AccountRPMError
		require.ErrorAs(t, err, &local)
		require.True(t, local.Unavailable)
		require.Equal(t, 503, local.StatusCode())
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
		s := &OpenAIGatewayService{}
		require.Same(t, local, s.handleOpenAIUpstreamTransportError(c.Request.Context(), c, a, local, false))
		require.Same(t, local, (&GatewayService{}).handleUpstreamTransportError(c.Request.Context(), c, a, local, OpsUpstreamErrorEvent{}))
		require.Same(t, local, (&GeminiMessagesCompatService{}).handleUpstreamTransportError(c.Request.Context(), c, a, local))
		s.ReportOpenAIAccountScheduleResult(a, "test", false, nil, local)
		_, recorded := c.Get(OpsUpstreamErrorsKey)
		require.False(t, recorded)
	}
	cache := &hardRPMCache{err: errors.New("offline")}
	capacity := AccountRPMCapacities(context.Background(), cache, []Account{*a, {ID: 2}})
	require.Equal(t, "unavailable", capacity[1].Status)
	require.Nil(t, capacity[1].Used)
	require.Nil(t, capacity[1].Remaining)
	require.Equal(t, "unlimited", capacity[2].Status)
}

func TestAccountHardRPMStorageFallbackDoesNotMaskAuthoritativeErrors(t *testing.T) {
	for _, tc := range []struct {
		name        string
		selected    int
		latest      *Account
		err         error
		unavailable bool
	}{
		{"unlimited_storage_outage", 0, nil, errors.New("storage unavailable"), false},
		{"limited_storage_outage", 2, nil, errors.New("storage unavailable"), true},
		{"invalid_fresh_config", 0, &Account{Extra: map[string]any{"rpm_limit": "2"}}, nil, true},
		{"deleted", 0, nil, ErrAccountNotFound, true},
		{"enabled_since_selection", 0, &Account{Extra: map[string]any{"rpm_limit": 2}}, nil, true},
		{"disabled_since_selection", 2, &Account{Extra: map[string]any{"rpm_limit": 0}}, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selected := &Account{ID: 1, Extra: map[string]any{"rpm_limit": tc.selected}}
			repo := &hardRPMRepo{account: tc.latest, err: tc.err}
			err := admitAccountRPM(context.Background(), nil, repo, selected)
			if tc.unavailable {
				var local *AccountRPMError
				require.ErrorAs(t, err, &local)
				require.True(t, local.Unavailable)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestAccountHardRPMPluginWithoutSendAdmissionFailsClosed(t *testing.T) {
	manager := &PluginManager{}
	manager.route.Store(&pluginRoute{pluginID: 1, rolloutPercent: 100})
	upstream := &hardRPMUpstream{}
	cache := &hardRPMCache{}
	s := &OpenAIGatewayService{pluginManager: manager, httpUpstream: upstream, cache: cache}
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"rpm_limit": 1}}
	req := httptest.NewRequest(http.MethodPost, "https://example.invalid/v1/responses", nil)
	resp, err := s.doOpenAIUpstream(req, "", account)
	var local *AccountRPMError
	require.ErrorAs(t, err, &local)
	require.True(t, local.Unavailable)
	require.Nil(t, resp)
	require.Zero(t, upstream.sends)
	require.Zero(t, cache.used)
}

func TestAccountHardRPMUserImageBackfillIsMetered(t *testing.T) {
	upstream := &hardRPMUpstream{}
	cache := &hardRPMCache{used: 1}
	s := &OpenAIGatewayService{httpUpstream: upstream, cache: cache, cfg: &config.Config{}}
	account := &Account{ID: 1, Extra: map[string]any{"rpm_limit": 1}}
	_, err := s.fetchOpenAIImageURLBase64(context.Background(), account, "https://cdn.example.com/image.png")
	require.True(t, IsAccountRPMError(err))
	require.Zero(t, upstream.sends)
}

func TestAccountHardRPMLiveReroutesOnlyUnsentCapacityDenial(t *testing.T) {
	s := &OpenAIGatewayService{}
	account := &Account{ID: 1}
	require.True(t, s.shouldFailoverLiveCreateError(account, &AccountRPMError{AccountID: 1}))
	require.False(t, s.shouldFailoverLiveCreateError(account, &AccountRPMError{AccountID: 1, Unavailable: true}))
	require.False(t, s.shouldFailoverLiveCreateError(account, &AccountRPMError{AccountID: 1, NoMigration: true}))
}

type hardRPMFrames struct {
	openaiwsv2.FrameConn
	writes int
}

func (c *hardRPMFrames) WriteFrame(context.Context, coderws.MessageType, []byte) error {
	c.writes++
	return nil
}

func TestAccountHardRPMWSTurnsAndFreshConfig(t *testing.T) {
	a := &Account{ID: 1, Extra: map[string]any{"rpm_limit": 1}}
	repo := &hardRPMRepo{account: a}
	cache := &hardRPMCache{}
	base := &hardRPMFrames{}
	conn := &accountRPMFrameConn{FrameConn: base, admit: func(ctx context.Context) error { return admitAccountRPM(ctx, cache, repo, a) }}
	ctx := context.Background()
	require.NoError(t, conn.WriteFrame(ctx, coderws.MessageText, []byte(`{"type":"session.update"}`)))
	require.Equal(t, 0, cache.used)
	require.NoError(t, conn.WriteFrame(ctx, coderws.MessageText, []byte(`{"type":"response.create","generate":false}`)))
	err := conn.WriteFrame(ctx, coderws.MessageBinary, []byte(`{"type":" response.create "}`))
	var local *AccountRPMError
	require.ErrorAs(t, err, &local)
	require.True(t, local.NoMigration)
	require.Equal(t, 2, base.writes)
	repo.account = &Account{ID: 1, Extra: map[string]any{"rpm_limit": 2}}
	require.NoError(t, conn.WriteFrame(ctx, coderws.MessageBinary, []byte(`{"type":"response.create"}`)))
	require.Equal(t, 2, cache.used)
	require.Equal(t, 3, base.writes)
}

func TestAccountHardRPMAdminSaveDisableBulk(t *testing.T) {
	repo := &hardRPMRepo{account: &Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Extra: map[string]any{"rpm_limit": 3}}}
	s := &adminServiceImpl{accountRepo: repo}
	for _, extra := range []map[string]any{{"rpm_limit": 6}, {"unrelated": true}, {"rpm_limit": 0}} {
		_, err := s.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Extra: extra})
		require.NoError(t, err)
		want := 6
		if v, ok := extra["rpm_limit"].(int); ok {
			want = v
		}
		got, err := repo.account.AccountRPMLimit()
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
	for _, extra := range []map[string]any{{"rpm_limit": 8}, {"unrelated": true}, {"rpm_limit": 0}} {
		_, err := s.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{1}, Extra: extra})
		require.NoError(t, err)
		want := 8
		if v, ok := extra["rpm_limit"].(int); ok {
			want = v
		}
		got, _ := repo.account.AccountRPMLimit()
		require.Equal(t, want, got)
	}
	bad := map[string]any{"rpm_limit": nil}
	_, err := s.CreateAccount(context.Background(), &CreateAccountInput{Extra: bad})
	require.Error(t, err)
	_, err = s.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Extra: bad})
	require.Error(t, err)
	_, err = s.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{AccountIDs: []int64{1}, Extra: bad})
	require.Error(t, err)
	require.Error(t, s.UpdateAccountExtra(context.Background(), 1, bad))
}

type hardRPMNativeWS struct{ writes int }

func (c *hardRPMNativeWS) WriteJSON(context.Context, any) error { c.writes++; return nil }
func (*hardRPMNativeWS) ReadMessage(context.Context) ([]byte, error) {
	return []byte(`{"type":"response.completed","response":{"id":"prewarm"}}`), nil
}
func (*hardRPMNativeWS) Ping(context.Context) error { return nil }
func (*hardRPMNativeWS) Close() error               { return nil }

func TestAccountHardRPMNativePrewarm(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.PrewarmGenerateEnabled = true
	cache := &hardRPMCache{}
	s := &OpenAIGatewayService{cfg: cfg, cache: cache}
	a := &Account{ID: 1, Extra: map[string]any{"rpm_limit": 1}}
	base := &hardRPMNativeWS{}
	lease := &openAIWSConnLease{conn: newOpenAIWSConn("test", 1, base, nil)}
	payload := map[string]any{"type": "response.create", "model": "test"}
	decision := OpenAIWSProtocolDecision{Transport: OpenAIUpstreamTransportResponsesWebsocketV2}
	require.NoError(t, s.performOpenAIWSGeneratePrewarm(context.Background(), lease, decision, payload, "", payload, a, nil, 0))
	require.Equal(t, 1, base.writes)
	require.Equal(t, 1, cache.used)
	// A prewarmed lease is not sent again, and a second lease must acquire another slot.
	require.NoError(t, s.performOpenAIWSGeneratePrewarm(context.Background(), lease, decision, payload, "", payload, a, nil, 0))
	require.Equal(t, 1, cache.used)
	second := &openAIWSConnLease{conn: newOpenAIWSConn("second", 1, base, nil)}
	err := s.performOpenAIWSGeneratePrewarm(context.Background(), second, decision, payload, "", payload, a, nil, 0)
	require.True(t, IsAccountRPMError(err))
	require.Equal(t, 1, base.writes)
}

func TestAccountHardRPMActualForwardDenials(t *testing.T) {
	for _, path := range []string{"openai", "anthropic", "gemini", "antigravity", "ws_http_bridge"} {
		t.Run(path, func(t *testing.T) {
			cfg := &config.Config{}
			cache := &hardRPMCache{used: 1}
			up := &hardRPMUpstream{}
			a := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "synthetic"}, Extra: map[string]any{"rpm_limit": 1}}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
			ctx := c.Request.Context()
			var err error
			switch path {
			case "openai":
				s := &OpenAIGatewayService{cfg: cfg, cache: cache, httpUpstream: up}
				body := []byte(`{"model":"gpt-5.5","input":"hello"}`)
				_, err = s.forwardOpenAIPassthrough(ctx, c, a, body, body, "gpt-5.5", false, nil, false, time.Now())
			case "anthropic":
				a.Platform = PlatformAnthropic
				s := &GatewayService{cfg: cfg, cache: cache, httpUpstream: up}
				_, err = s.forwardAnthropicAPIKeyPassthrough(ctx, c, a, []byte(`{"model":"claude-sonnet-4-5","max_tokens":10,"messages":[{"role":"user","content":"hello"}]}`), "claude-sonnet-4-5", "claude-sonnet-4-5", false, time.Now())
			case "gemini":
				a.Platform = PlatformGemini
				s := &GeminiMessagesCompatService{cfg: cfg, cache: cache, httpUpstream: up}
				_, err = s.ForwardNative(ctx, c, a, "gemini-2.5-flash", "generateContent", false, []byte(`{"contents":[{"parts":[{"text":"hello"}]}]}`))
			case "antigravity":
				a.Platform = PlatformAntigravity
				a.Credentials["base_url"] = "https://example.invalid"
				s := &AntigravityGatewayService{cache: cache, httpUpstream: up}
				_, err = s.ForwardUpstream(ctx, c, a, []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hello"}]}`))
			case "ws_http_bridge":
				s := &OpenAIGatewayService{cfg: cfg, cache: cache, httpUpstream: up}
				body := []byte(`{"type":"response.create","model":"gpt-5.5","input":"hello"}`)
				_, err = s.proxyOpenAIWSHTTPBridgeTurn(ctx, c, a, "synthetic", body, len(body), "gpt-5.5", "", "", "", "", 1, func([]byte) error { t.Fatal("local denial wrote a semantic event"); return nil })
			}
			require.True(t, IsAccountRPMError(err), "error: %v", err)
			require.Zero(t, up.sends)
			require.Equal(t, 1, cache.used)
			require.Empty(t, w.Body.String())
			_, recorded := c.Get(OpsUpstreamErrorsKey)
			require.False(t, recorded)
		})
	}
}
