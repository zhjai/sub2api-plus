package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/testutil"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type openAIWSR5Upstream struct {
	connections atomic.Int32
	frames      chan []byte
	completed   bool
	model       string
}

func (u *openAIWSR5Upstream) handler(w http.ResponseWriter, r *http.Request) {
	u.connections.Add(1)
	conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()
	readCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	_, payload, readErr := conn.Read(readCtx)
	cancel()
	if readErr != nil {
		return
	}
	u.frames <- append([]byte(nil), payload...)
	if u.model != "" {
		writeCtx, cancelWrite := context.WithTimeout(r.Context(), 3*time.Second)
		_ = conn.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.created","response":{"id":"resp_r5_wrong","model":"`+u.model+`","status":"in_progress"}}`))
		cancelWrite()
		return
	}
	if u.completed {
		writeCtx, cancelWrite := context.WithTimeout(r.Context(), 3*time.Second)
		_ = conn.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.completed","response":{"id":"resp_r5_done","status":"completed"}}`))
		cancelWrite()
	}
}

func newOpenAIWSR5Handler(t *testing.T, accounts []service.Account, cache service.GatewayCache) (*OpenAIGatewayHandler, *service.OpenAIGatewayService, *service.APIKey, *httptest.Server, *concurrencyCacheMock) {
	return newOpenAIR5Handler(t, accounts, cache, nil)
}

func newOpenAIR5Handler(t *testing.T, accounts []service.Account, cache service.GatewayCache, upstream service.HTTPUpstream) (*OpenAIGatewayHandler, *service.OpenAIGatewayService, *service.APIKey, *httptest.Server, *concurrencyCacheMock) {
	return newOpenAIR7HandlerWithRepo(t, accounts, cache, upstream, nil)
}

func newOpenAIR7HandlerWithRepo(t *testing.T, accounts []service.Account, cache service.GatewayCache, upstream service.HTTPUpstream, accountRepo service.AccountRepository) (*OpenAIGatewayHandler, *service.OpenAIGatewayService, *service.APIKey, *httptest.Server, *concurrencyCacheMock) {
	t.Helper()
	groupID := int64(4935)
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.APIKeyEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
	cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
	cfg.Gateway.MaxAccountSwitches = 2

	if accountRepo == nil {
		accountRepo = &openAIWSFailoverHandlerAccountRepoStub{accounts: accounts}
	}
	rateLimitSvc := service.NewRateLimitService(accountRepo, nil, cfg, nil, nil)
	billingCacheSvc := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCacheSvc.Stop)
	concurrencyCache := &concurrencyCacheMock{
		acquireUserSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil },
		acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) {
			return true, nil
		},
	}
	gatewaySvc := service.NewOpenAIGatewayService(
		accountRepo, nil, nil, nil, nil, nil, cache, cfg, nil, service.NewConcurrencyService(concurrencyCache),
		service.NewBillingService(cfg, nil), rateLimitSvc, billingCacheSvc,
		upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil,
	)
	h := &OpenAIGatewayHandler{
		gatewayService:      gatewaySvc,
		billingCacheService: billingCacheSvc,
		apiKeyService:       &service.APIKeyService{},
		concurrencyHelper:   NewConcurrencyHelper(service.NewConcurrencyService(concurrencyCache), SSEPingFormatNone, time.Second),
		maxAccountSwitches:  2,
	}
	apiKey := &service.APIKey{
		ID: 1935, GroupID: &groupID,
		User:  &service.User{ID: 1835, Status: service.StatusActive},
		Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive},
	}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), apiKey)
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: apiKey.User.ID, Concurrency: 1})
		c.Next()
	})
	router.GET("/openai/v1/responses", h.ResponsesWebSocket)
	router.POST("/openai/v1/responses", h.Responses)
	return h, gatewaySvc, apiKey, httptest.NewServer(router), concurrencyCache
}

type openAIHTTPR5WrongModelUpstream struct {
	service.HTTPUpstream
	calls atomic.Int32
	body  []byte
}

func (u *openAIHTTPR5WrongModelUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls.Add(1)
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	u.body = body
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_r5_wrong\",\"model\":\"gpt-4.1-mini\",\"status\":\"in_progress\"}}\n\n")),
	}, nil
}

func TestOpenAIResponses_R5OpaqueContinuationMismatchPreservesCreatingEpoch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, passthrough := range []bool{false, true} {
		name := "native"
		if passthrough {
			name = "passthrough"
		}
		t.Run(name, func(t *testing.T) {
			upstream := &openAIHTTPR5WrongModelUpstream{}
			account := openAIWSR5Account(9972, "opaque-http-owner", "http://127.0.0.1:1", service.StatusActive, true, true, 1)
			account.Extra["openai_apikey_responses_websockets_v2_enabled"] = false
			account.Extra["openai_passthrough"] = passthrough
			cache := testutil.NewRedisGatewayCache(t)
			_, gatewaySvc, apiKey, server, concurrencyCache := newOpenAIR5Handler(t, []service.Account{account}, cache, upstream)
			defer server.Close()
			const responseID = "resp_r5_http_bound"
			store := service.NewOpenAIWSStateStore(cache)
			require.NoError(t, store.BindResponseAccount(context.Background(), *apiKey.GroupID, responseID, account.ID, time.Hour))
			require.NoError(t, store.BindResponseRouteEpoch(context.Background(), *apiKey.GroupID, responseID, account.ID, 0, time.Hour))
			require.NoError(t, gatewaySvc.BindOpenAIHTTPResponseOwner(context.Background(), *apiKey.GroupID, responseID, apiKey.User.ID, apiKey.ID))
			body := `{"model":"gpt-5.1","stream":true,"previous_response_id":"resp_r5_http_bound","input":"continue"}`
			req, err := http.NewRequest(http.MethodPost, server.URL+"/openai/v1/responses", strings.NewReader(body))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("session_id", "r5_http_opaque")
			resp, err := server.Client().Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()
			output, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, http.StatusBadGateway, resp.StatusCode)
			require.Equal(t, int32(1), upstream.calls.Load(), "bound continuation must never replay")
			require.Equal(t, responseID, gjson.GetBytes(upstream.body, "previous_response_id").String())
			require.NotContains(t, string(output), "resp_r5_wrong")
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = req
			sessionHash := gatewaySvc.GenerateSessionHash(c, []byte(body))
			epoch, err := cache.(service.OpenAIOpaqueRouteEpochCache).GetOpenAIOpaqueRouteEpoch(context.Background(), *apiKey.GroupID, sessionHash, "gpt-5.1", "", account.ID, time.Hour)
			require.NoError(t, err)
			require.Equal(t, int64(1), epoch.Epoch, "failure rotates only future-root identity")
			require.Equal(t, 1, epoch.Bumps)
			boundEpoch, found, err := store.GetResponseRouteEpoch(context.Background(), *apiKey.GroupID, responseID, account.ID)
			require.NoError(t, err)
			require.True(t, found)
			require.Zero(t, boundEpoch)
			require.Eventually(t, func() bool { return atomic.LoadInt32(&concurrencyCache.releaseAccountCalled) == 1 }, time.Second, 10*time.Millisecond)
		})
	}
}

func TestOpenAIResponsesWebSocket_R5OpaqueContinuationMismatchPreservesCreatingEpoch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	owner := openAIWSR5Upstream{frames: make(chan []byte, 1), model: "gpt-4.1-mini"}
	ownerServer := httptest.NewServer(http.HandlerFunc(owner.handler))
	defer ownerServer.Close()
	account := openAIWSR5Account(9973, "opaque-ws-owner", ownerServer.URL, service.StatusActive, true, true, 1)
	cache := testutil.NewRedisGatewayCache(t)
	_, gatewaySvc, apiKey, server, concurrencyCache := newOpenAIWSR5Handler(t, []service.Account{account}, cache)
	defer server.Close()
	const responseID = "resp_r5_ws_bound"
	store := service.NewOpenAIWSStateStore(cache)
	require.NoError(t, store.BindResponseAccount(context.Background(), *apiKey.GroupID, responseID, account.ID, time.Hour))
	require.NoError(t, store.BindResponseRouteEpoch(context.Background(), *apiKey.GroupID, responseID, account.ID, 0, time.Hour))
	conn := dialOpenAIWSR5(t, server)
	defer conn.CloseNow()
	body := `{"type":"response.create","model":"gpt-5.1","previous_response_id":"resp_r5_ws_bound","session_id":"r5_ws_opaque","input":"continue"}`
	writeOpenAIWSR5(t, conn, body)
	err := readOpenAIWSR5(t, conn)
	var closeErr coderws.CloseError
	require.ErrorAs(t, err, &closeErr)
	require.Equal(t, coderws.StatusTryAgainLater, closeErr.Code)
	require.Equal(t, int32(1), owner.connections.Load())
	frame := <-owner.frames
	require.Equal(t, responseID, gjson.GetBytes(frame, "previous_response_id").String())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/openai/v1/responses", nil)
	sessionHash := gatewaySvc.GenerateSessionHash(c, []byte(body))
	epoch, err := cache.(service.OpenAIOpaqueRouteEpochCache).GetOpenAIOpaqueRouteEpoch(context.Background(), *apiKey.GroupID, sessionHash, "gpt-5.1", "", account.ID, time.Hour)
	require.NoError(t, err)
	require.Equal(t, int64(1), epoch.Epoch, "failure rotates only future-root identity")
	require.Equal(t, 1, epoch.Bumps)
	boundEpoch, found, err := store.GetResponseRouteEpoch(context.Background(), *apiKey.GroupID, responseID, account.ID)
	require.NoError(t, err)
	require.True(t, found)
	require.Zero(t, boundEpoch, "historical continuation retains its creating epoch")
	require.Eventually(t, func() bool { return atomic.LoadInt32(&concurrencyCache.releaseAccountCalled) == 1 }, time.Second, 10*time.Millisecond)
}

func TestOpenAIResponses_R5OpaqueUnavailableOwnerFailsClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name   string
		status string
		want   int
	}{
		{name: "temporarily_unschedulable", status: service.StatusActive, want: http.StatusServiceUnavailable},
		{name: "disabled", status: service.StatusDisabled, want: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &openAIHTTPR5WrongModelUpstream{}
			accounts := []service.Account{
				openAIWSR5Account(9974, "unavailable-http-owner", "http://127.0.0.1:1", tc.status, false, true, 1),
				openAIWSR5Account(9975, "http-fallback", "http://127.0.0.1:1", service.StatusActive, true, false, 2),
			}
			for i := range accounts {
				accounts[i].Extra["openai_apikey_responses_websockets_v2_enabled"] = false
			}
			cache := testutil.NewRedisGatewayCache(t)
			_, gatewaySvc, apiKey, server, concurrencyCache := newOpenAIR5Handler(t, accounts, cache, upstream)
			defer server.Close()
			const responseID = "resp_r5_http_unavailable"
			store := service.NewOpenAIWSStateStore(cache)
			require.NoError(t, store.BindResponseAccount(context.Background(), *apiKey.GroupID, responseID, accounts[0].ID, time.Hour))
			require.NoError(t, gatewaySvc.BindOpenAIHTTPResponseOwner(context.Background(), *apiKey.GroupID, responseID, apiKey.User.ID, apiKey.ID))
			for attempt := 1; attempt <= 2; attempt++ {
				req, err := http.NewRequest(http.MethodPost, server.URL+"/openai/v1/responses", strings.NewReader(`{"model":"gpt-5.1","stream":true,"previous_response_id":"resp_r5_http_unavailable","input":"continue"}`))
				require.NoError(t, err)
				req.Header.Set("Content-Type", "application/json")
				resp, err := server.Client().Do(req)
				require.NoError(t, err)
				_, err = io.Copy(io.Discard, resp.Body)
				require.NoError(t, err)
				require.NoError(t, resp.Body.Close())
				require.Equal(t, tc.want, resp.StatusCode, "retry %d", attempt)
				require.Zero(t, upstream.calls.Load(), "fallback must not receive the opaque continuation")
				owner, err := store.GetResponseAccount(context.Background(), *apiKey.GroupID, responseID)
				require.NoError(t, err)
				require.Equal(t, accounts[0].ID, owner)
				require.Eventually(t, func() bool { return atomic.LoadInt32(&concurrencyCache.releaseAccountCalled) == int32(attempt) }, time.Second, 10*time.Millisecond)
			}
		})
	}
}

func openAIWSR5Account(id int64, name, baseURL string, status string, schedulable, opaque bool, priority int) service.Account {
	extra := map[string]any{
		"openai_apikey_responses_websockets_v2_enabled": true,
		"openai_apikey_responses_websockets_v2_mode":    service.OpenAIWSIngressModePassthrough,
	}
	if opaque {
		extra["openai_opaque_upstream"] = true
	}
	return service.Account{
		ID: id, Name: name, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Status: status, Schedulable: schedulable, Concurrency: 1, Priority: priority,
		Credentials: map[string]any{"api_key": "sk-r5", "base_url": baseURL}, Extra: extra,
	}
}

func dialOpenAIWSR5(t *testing.T, server *httptest.Server) *coderws.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/openai/v1/responses", &coderws.DialOptions{CompressionMode: coderws.CompressionContextTakeover})
	require.NoError(t, err)
	return conn
}

func writeOpenAIWSR5(t *testing.T, conn *coderws.Conn, payload string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(payload)))
}

func readOpenAIWSR5(t *testing.T, conn *coderws.Conn) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _, err := conn.Read(ctx)
	return err
}

func TestOpenAIResponsesWebSocket_R5OpaqueDisabledOwnerFailsClosedBeforeFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var fallback openAIWSR5Upstream
	fallback.frames = make(chan []byte, 1)
	fallbackServer := httptest.NewServer(http.HandlerFunc(fallback.handler))
	defer fallbackServer.Close()
	accounts := []service.Account{
		openAIWSR5Account(9956, "disabled-opaque-owner", "http://127.0.0.1:1", service.StatusDisabled, false, true, 1),
		openAIWSR5Account(9957, "ordinary-fallback", fallbackServer.URL, service.StatusActive, true, false, 2),
	}
	cache := testutil.NewRedisGatewayCache(t)
	h, gatewaySvc, apiKey, handlerServer, _ := newOpenAIWSR5Handler(t, accounts, cache)
	_ = h
	defer handlerServer.Close()
	responseID := "resp_r5_disabled_owner"
	store := service.NewOpenAIWSStateStore(cache)
	require.NoError(t, store.BindResponseAccount(context.Background(), *apiKey.GroupID, responseID, accounts[0].ID, time.Hour))

	for attempt := 1; attempt <= 2; attempt++ {
		conn := dialOpenAIWSR5(t, handlerServer)
		writeOpenAIWSR5(t, conn, `{"type":"response.create","model":"gpt-5.1","previous_response_id":"resp_r5_disabled_owner","input":"hello"}`)
		err := readOpenAIWSR5(t, conn)
		var closeErr coderws.CloseError
		require.ErrorAs(t, err, &closeErr)
		require.Equal(t, coderws.StatusPolicyViolation, closeErr.Code, "retry %d", attempt)
		require.Contains(t, closeErr.Reason, "previous_response_id owner is unavailable")
		_ = conn.CloseNow()
		owner, err := store.GetResponseAccount(context.Background(), *apiKey.GroupID, responseID)
		require.NoError(t, err)
		require.Equal(t, accounts[0].ID, owner)
	}
	require.Eventually(t, func() bool { return fallback.connections.Load() == 0 }, 300*time.Millisecond, 10*time.Millisecond)
	select {
	case frame := <-fallback.frames:
		t.Fatalf("fallback received frame despite fail-closed owner: %s", frame)
	default:
	}
	_ = gatewaySvc
}

func TestOpenAIResponsesWebSocket_R5OpaqueTemporarilyUnschedulableOwnerIsRetryable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var fallback openAIWSR5Upstream
	fallback.frames = make(chan []byte, 1)
	fallbackServer := httptest.NewServer(http.HandlerFunc(fallback.handler))
	defer fallbackServer.Close()
	accounts := []service.Account{
		openAIWSR5Account(9970, "unavailable-opaque-owner", "http://127.0.0.1:1", service.StatusActive, false, true, 1),
		openAIWSR5Account(9971, "ordinary-fallback", fallbackServer.URL, service.StatusActive, true, false, 2),
	}
	cache := testutil.NewRedisGatewayCache(t)
	_, _, apiKey, handlerServer, concurrencyCache := newOpenAIWSR5Handler(t, accounts, cache)
	defer handlerServer.Close()
	store := service.NewOpenAIWSStateStore(cache)
	require.NoError(t, store.BindResponseAccount(context.Background(), *apiKey.GroupID, "resp_r5_temp_owner", accounts[0].ID, time.Hour))
	for attempt := 1; attempt <= 2; attempt++ {
		conn := dialOpenAIWSR5(t, handlerServer)
		writeOpenAIWSR5(t, conn, `{"type":"response.create","model":"gpt-5.1","previous_response_id":"resp_r5_temp_owner","input":"continue"}`)
		err := readOpenAIWSR5(t, conn)
		var closeErr coderws.CloseError
		require.ErrorAs(t, err, &closeErr)
		require.Equal(t, coderws.StatusTryAgainLater, closeErr.Code, "retry %d", attempt)
		_ = conn.CloseNow()
		require.Equal(t, int32(0), fallback.connections.Load())
		owner, err := store.GetResponseAccount(context.Background(), *apiKey.GroupID, "resp_r5_temp_owner")
		require.NoError(t, err)
		require.Equal(t, accounts[0].ID, owner)
		require.Eventually(t, func() bool { return atomic.LoadInt32(&concurrencyCache.releaseAccountCalled) == int32(attempt) }, time.Second, 10*time.Millisecond)
	}
	status, errorType := openAIPreviousResponseOwnerUnavailableStatus(&accounts[0])
	require.Equal(t, http.StatusServiceUnavailable, status)
	require.Equal(t, "service_unavailable", errorType)
}

func TestOpenAIResponsesWebSocket_R5OpaqueSchedulableOwnerUsesOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var owner, fallback openAIWSR5Upstream
	owner.frames, fallback.frames = make(chan []byte, 1), make(chan []byte, 1)
	owner.completed = true
	ownerServer := httptest.NewServer(http.HandlerFunc(owner.handler))
	defer ownerServer.Close()
	fallbackServer := httptest.NewServer(http.HandlerFunc(fallback.handler))
	defer fallbackServer.Close()
	accounts := []service.Account{
		openAIWSR5Account(9958, "active-opaque-owner", ownerServer.URL, service.StatusActive, true, true, 1),
		openAIWSR5Account(9959, "ordinary-fallback", fallbackServer.URL, service.StatusActive, true, false, 2),
	}
	cache := testutil.NewRedisGatewayCache(t)
	_, _, apiKey, handlerServer, _ := newOpenAIWSR5Handler(t, accounts, cache)
	defer handlerServer.Close()
	responseID := "resp_r5_active_owner"
	store := service.NewOpenAIWSStateStore(cache)
	require.NoError(t, store.BindResponseAccount(context.Background(), *apiKey.GroupID, responseID, accounts[0].ID, time.Hour))
	require.NoError(t, store.BindResponseRouteEpoch(context.Background(), *apiKey.GroupID, responseID, accounts[0].ID, 0, time.Hour))

	conn := dialOpenAIWSR5(t, handlerServer)
	defer func() { _ = conn.CloseNow() }()
	writeOpenAIWSR5(t, conn, `{"type":"response.create","model":"gpt-5.1","previous_response_id":"resp_r5_active_owner","input":"hello"}`)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	_, event, err := conn.Read(ctx)
	cancel()
	require.NoError(t, err)
	require.Equal(t, "response.completed", gjson.GetBytes(event, "type").String())
	select {
	case frame := <-owner.frames:
		require.Equal(t, responseID, gjson.GetBytes(frame, "previous_response_id").String())
	case <-time.After(3 * time.Second):
		t.Fatal("opaque owner did not receive the continuation frame")
	}
	require.Equal(t, int32(1), owner.connections.Load())
	require.Equal(t, int32(0), fallback.connections.Load())
}

func TestOpenAIResponsesWebSocket_R5OpaqueRouteEpochFailureReleasesSelection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var owner openAIWSR5Upstream
	owner.frames = make(chan []byte, 1)
	ownerServer := httptest.NewServer(http.HandlerFunc(owner.handler))
	defer ownerServer.Close()
	accounts := []service.Account{openAIWSR5Account(9960, "active-opaque-owner", ownerServer.URL, service.StatusActive, true, true, 1)}
	cache := testutil.NewRedisGatewayCache(t)
	_, _, apiKey, handlerServer, concurrencyCache := newOpenAIWSR5Handler(t, accounts, cache)
	defer handlerServer.Close()
	responseID := "resp_r5_missing_epoch"
	store := service.NewOpenAIWSStateStore(cache)
	require.NoError(t, store.BindResponseAccount(context.Background(), *apiKey.GroupID, responseID, accounts[0].ID, time.Hour))

	conn := dialOpenAIWSR5(t, handlerServer)
	defer func() { _ = conn.CloseNow() }()
	writeOpenAIWSR5(t, conn, `{"type":"response.create","model":"gpt-5.1","previous_response_id":"resp_r5_missing_epoch","input":"hello"}`)
	err := readOpenAIWSR5(t, conn)
	var closeErr coderws.CloseError
	require.ErrorAs(t, err, &closeErr)
	require.Equal(t, coderws.StatusPolicyViolation, closeErr.Code, "a missing route epoch is not a cache outage")
	require.Eventually(t, func() bool { return owner.connections.Load() == 0 }, 300*time.Millisecond, 10*time.Millisecond)
	require.Eventually(t, func() bool { return atomic.LoadInt32(&concurrencyCache.releaseAccountCalled) == 1 }, 300*time.Millisecond, 10*time.Millisecond)
	select {
	case frame := <-owner.frames:
		t.Fatalf("owner received frame despite missing route epoch: %s", frame)
	default:
	}
}

type openAIResponseOwnerLookupErrorCache struct{ service.GatewayCache }

func (c *openAIResponseOwnerLookupErrorCache) GetSessionAccountID(context.Context, int64, string) (int64, error) {
	return 0, errors.New("owner lookup unavailable")
}

func TestOpenAIResponses_R6OwnerLookupFailureIsRetryableWithoutForwarding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &openAIHTTPR5WrongModelUpstream{}
	accounts := []service.Account{openAIWSR5Account(9978, "fallback", "http://127.0.0.1:1", service.StatusActive, true, false, 1)}
	cache := &openAIResponseOwnerLookupErrorCache{GatewayCache: testutil.NewRedisGatewayCache(t)}
	_, gateway, apiKey, server, _ := newOpenAIR5Handler(t, accounts, cache, upstream)
	defer server.Close()
	require.NoError(t, gateway.BindOpenAIHTTPResponseOwner(t.Context(), *apiKey.GroupID, "resp_lookup_error", apiKey.User.ID, apiKey.ID))
	req, err := http.NewRequest(http.MethodPost, server.URL+"/openai/v1/responses", strings.NewReader(`{"model":"gpt-5.1","stream":true,"previous_response_id":"resp_lookup_error","input":"continue"}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := server.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	_, err = io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	require.Zero(t, upstream.calls.Load())
	conn := dialOpenAIWSR5(t, server)
	defer conn.CloseNow()
	writeOpenAIWSR5(t, conn, `{"type":"response.create","model":"gpt-5.1","previous_response_id":"resp_lookup_error","input":"continue"}`)
	var closeErr coderws.CloseError
	require.ErrorAs(t, readOpenAIWSR5(t, conn), &closeErr)
	require.Equal(t, coderws.StatusTryAgainLater, closeErr.Code)
	require.Zero(t, upstream.calls.Load())
}

func TestOpenAIResponsesWebSocket_R6OpaqueFallbackPreservesOwnerRetryability(t *testing.T) {
	gin.SetMode(gin.TestMode)
	accounts := []service.Account{
		openAIWSR5Account(9976, "unavailable-opaque-owner", "http://127.0.0.1:1", service.StatusActive, false, true, 1),
		openAIWSR5Account(9977, "opaque-fallback", "http://127.0.0.1:1", service.StatusActive, true, true, 2),
	}
	cache := testutil.NewRedisGatewayCache(t)
	_, _, apiKey, server, slots := newOpenAIWSR5Handler(t, accounts, cache)
	defer server.Close()
	store := service.NewOpenAIWSStateStore(cache)
	require.NoError(t, store.BindResponseAccount(t.Context(), *apiKey.GroupID, "resp_opaque_fallback", accounts[0].ID, time.Hour))
	conn := dialOpenAIWSR5(t, server)
	defer conn.CloseNow()
	writeOpenAIWSR5(t, conn, `{"type":"response.create","model":"gpt-5.1","previous_response_id":"resp_opaque_fallback","input":"continue"}`)
	var closeErr coderws.CloseError
	require.ErrorAs(t, readOpenAIWSR5(t, conn), &closeErr)
	require.Equal(t, coderws.StatusTryAgainLater, closeErr.Code)
	require.Eventually(t, func() bool { return atomic.LoadInt32(&slots.releaseAccountCalled) == 1 }, time.Second, 10*time.Millisecond)
}
