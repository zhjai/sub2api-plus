package handler

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type bpsFailureHandlerRepo struct {
	openAIWSFailoverHandlerAccountRepoStub
	healthWrites atomic.Int32
}

func (r *bpsFailureHandlerRepo) SetError(context.Context, int64, string) error {
	r.healthWrites.Add(1)
	return nil
}
func (r *bpsFailureHandlerRepo) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	r.healthWrites.Add(1)
	return nil
}
func (r *bpsFailureHandlerRepo) SetModelRateLimit(context.Context, int64, string, time.Time, ...string) error {
	r.healthWrites.Add(1)
	return nil
}

type bpsFailureHandlerUpstream struct {
	service.HTTPUpstream
	mu            sync.Mutex
	accounts      []int64
	failureStatus int
}

func (*bpsFailureHandlerUpstream) SupportsSingleSend() bool { return true }
func (u *bpsFailureHandlerUpstream) Do(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	u.accounts = append(u.accounts, id)
	attempt := len(u.accounts)
	u.mu.Unlock()
	if u.failureStatus == -1 {
		return &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"cyber_policy","type":"invalid_request_error","message":"blocked"}}`))}, nil
	}
	// Unknown direct tools cannot be corrected safely; the real bridge rejects
	// this completed terminal locally while preserving its consumed usage.
	wire := `data: {"type":"response.completed","response":{"id":"resp_bps_local_failure","status":"completed","model":"gpt-6-astra","output":[{"type":"function_call","name":"undeclared_tool","id":"fc_bad","call_id":"call_bad","arguments":"{}"}],"usage":{"input_tokens":19,"output_tokens":5}}}` + "\n\n"
	if u.failureStatus == 400 {
		wire = `data: {"type":"response.failed","response":{"id":"resp_cyber","status":"failed","model":"gpt-6-astra","usage":{"input_tokens":19,"output_tokens":5},"error":{"code":"cyber_policy","message":"blocked"}}}` + "\n\n"
	} else if u.failureStatus != 0 {
		if attempt == 1 {
			wire = fmt.Sprintf(`data: {"type":"response.failed","response":{"id":"resp_provider_failed","status":"failed","model":"gpt-6-astra","output":[],"usage":{"input_tokens":19,"output_tokens":5},"error":{"status":%d,"code":"basispoints_upstream_error","message":"provider failed"}}}`+"\n\n", u.failureStatus)
		} else {
			wire = `data: {"type":"response.completed","response":{"id":"resp_fallback","status":"completed","model":"gpt-6-astra","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"fallback succeeded"}]}],"usage":{"input_tokens":7,"output_tokens":3}}}` + "\n\n"
		}
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}, "X-Request-Id": {fmt.Sprintf("bps-fixture-%d", attempt)}}, Body: io.NopCloser(strings.NewReader(wire))}, nil
}

func runBPSFailureFidelityHandler(t *testing.T, stream bool, failureStatus int, native ...bool) {
	isNative := len(native) > 0 && native[0]
	t.Helper()
	claims, err := json.Marshal(map[string]any{"client_id": openai.ExcelClientID, "exp": time.Now().Add(time.Hour).Unix(), "https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "synthetic-workspace", "chatgpt_plan_type": "plus"}})
	require.NoError(t, err)
	token := "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".test"
	accounts := []service.Account{}
	for i := 0; i < 2; i++ {
		accounts = append(accounts, service.Account{ID: int64(9930 + i), Name: "bps-fixture", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive, Schedulable: true, Concurrency: 2, Priority: i + 1, Credentials: map[string]any{"client_id": openai.ExcelClientID, "access_token": token, "chatgpt_account_id": "synthetic-workspace", "model_mapping": map[string]any{"gpt-6-astra": "gpt-6-astra"}}})
	}
	if isNative {
		for i := range accounts {
			accounts[i].Type = service.AccountTypeAPIKey
			accounts[i].Credentials = map[string]any{"api_key": "synthetic", "base_url": "https://upstream.example/v1", "model_mapping": map[string]any{"gpt-6-astra": "gpt-6-astra"}}
		}
	}
	repo := &bpsFailureHandlerRepo{openAIWSFailoverHandlerAccountRepoStub: openAIWSFailoverHandlerAccountRepoStub{accounts: accounts}}
	usage := &openAIWSUsageHandlerUsageLogRepoStub{created: make(chan *service.UsageLog, 4)}
	upstream := &bpsFailureHandlerUpstream{failureStatus: failureStatus}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Gateway.MaxAccountSwitches = 2
	cache := testutil.NewRedisGatewayCache(t)
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	concurrency := service.NewConcurrencyService(&concurrencyCacheMock{acquireUserSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil }, acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil }})
	gateway := service.NewOpenAIGatewayService(repo, usage, nil, nil, nil, nil, cache, cfg, nil, concurrency, service.NewBillingService(cfg, nil), service.NewRateLimitService(repo, nil, cfg, nil, nil), billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
	h := NewOpenAIGatewayHandler(gateway, concurrency, billing, &service.APIKeyService{}, nil, nil, nil, nil, cfg)
	groupID := int64(9932)
	body := `{"model":"gpt-6-astra","input":"hello","tools":[{"type":"function","name":"shell"}],"stream":` + map[bool]string{false: "false", true: "true"}[stream] + `}`
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/openai/v1/responses", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("Session-Id", t.Name())
	c.Request = c.Request.WithContext(context.WithValue(context.WithValue(c.Request.Context(), ctxkey.ClientRequestID, "shared-client-id"), ctxkey.RequestID, "shared-local-id"))
	key := &service.APIKey{ID: 9933, UserID: 9934, GroupID: &groupID, User: &service.User{ID: 9934, Status: service.StatusActive}, Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive, RateMultiplier: 1}}
	c.Set(string(middleware.ContextKeyAPIKey), key)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9934, Concurrency: 1})
	h.Responses(c)
	upstream.mu.Lock()
	called := append([]int64(nil), upstream.accounts...)
	upstream.mu.Unlock()
	wantCalls := 1
	if failureStatus > 0 && failureStatus != 400 {
		wantCalls = 2
	}
	require.Len(t, called, wantCalls)
	if failureStatus == 0 {
		require.Zero(t, repo.healthWrites.Load())
		require.Empty(t, repo.rateLimitedIDs)
		require.Contains(t, rec.Body.String(), "basispoints_protocol_error")
		require.Zero(t, gateway.SnapshotOpenAIAccountSchedulerMetrics().AccountSwitchTotal)
	} else if failureStatus == 400 || failureStatus == -1 {
		require.Contains(t, rec.Body.String(), "cyber_policy")
	} else {
		if failureStatus == http.StatusServiceUnavailable {
			require.NotEqual(t, called[0], called[1], "provider 503 should reach the second account")
		}
		// Semantic 429 retains the existing bounded same-account retry policy.
		require.Equal(t, http.StatusOK, rec.Code)
		require.Contains(t, rec.Body.String(), "fallback succeeded")
	}
	require.NotContains(t, rec.Body.String(), `"type":"function_call"`)
	firstRecorded := 0
	if isNative {
		firstRecorded = 1
	} // Existing native failover attempts must not enter BPS accounting.
	billingIDs := map[string]bool{}
	for attempt := firstRecorded; attempt < wantCalls; attempt++ {
		select {
		case logged := <-usage.created:
			require.Equal(t, called[attempt], logged.AccountID)
			if isNative {
				require.Equal(t, "client:shared-client-id", logged.RequestID)
			} else if failureStatus != -1 {
				require.True(t, strings.HasPrefix(logged.RequestID, "bps:"))
				require.False(t, billingIDs[logged.RequestID], "distinct consumed attempts must not deduplicate under shared client/local ID")
				billingIDs[logged.RequestID] = true
			}
			if failureStatus == -1 {
				require.Zero(t, logged.InputTokens)
				require.Zero(t, logged.OutputTokens)
			} else if attempt == 0 {
				require.Equal(t, 19, logged.InputTokens)
				require.Equal(t, 5, logged.OutputTokens)
			} else {
				require.Equal(t, 7, logged.InputTokens)
				require.Equal(t, 3, logged.OutputTokens)
			}
		case <-time.After(time.Second):
			t.Fatal("known attempt usage was not recorded")
		}
	}
	require.Empty(t, usage.created, "one usage record per inference attempt")
	if failureStatus == 400 || failureStatus == -1 {
		select {
		case extra := <-usage.created:
			t.Fatalf("cyber usage double-recorded: %s", extra.RequestID)
		case <-time.After(150 * time.Millisecond):
		}
	}
}

func TestOpenAIResponses_BPSLocalConversionFailureDoesNotReplayAndRecordsUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "stream"}[stream], func(t *testing.T) { runBPSFailureFidelityHandler(t, stream, 0) })
	}
}

func TestOpenAIResponses_BPSProviderFailureStillFailsOverAndRecordsEveryAttempt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, status := range []int{429, 503} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/%v", status, stream), func(t *testing.T) { runBPSFailureFidelityHandler(t, stream, status) })
		}
	}
}

func TestOpenAIResponses_NativeFailoverDoesNotAdoptBPSUsageAccounting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) { runBPSFailureFidelityHandler(t, stream, http.StatusServiceUnavailable, true) })
	}
}

func TestOpenAIResponses_BPSCyberFailureHasOneUsageOwner(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) { runBPSFailureFidelityHandler(t, stream, 400) })
	}
}

func TestOpenAIResponses_BPSCyberWithoutResultRetainsUsageFallback(t *testing.T) {
	runBPSFailureFidelityHandler(t, false, -1)
}
