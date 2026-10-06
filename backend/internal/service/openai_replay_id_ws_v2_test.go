package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIReplayIDWSV2NonstreamPreservesError(t *testing.T) {
	for _, tc := range []struct {
		name         string
		param        string
		invalidValue bool
		bootstrap    bool
		semantic     bool
		staleTail    bool
		stream       bool
		conversation bool
		rpmLimit     int
		cancelRetry  bool
		retry        bool
		success      bool
	}{
		{name: "wildcard", param: "input*****.id"},
		{name: "out of range", param: "input[455].id", invalidValue: true},
		{name: "indexed repaired", param: "input[0].id", retry: true, success: true},
		{name: "invalid value repaired", param: "input[0].id", invalidValue: true, retry: true, success: true},
		{name: "repeated stops", param: "input[0].id", retry: true},
		{name: "bootstrap discarded", param: "input[0].id", bootstrap: true, retry: true, success: true},
		{name: "stale terminal discarded", param: "input[0].id", bootstrap: true, staleTail: true, retry: true, success: true},
		{name: "stream repaired without stale bootstrap", param: "input[0].id", stream: true, bootstrap: true, staleTail: true, retry: true, success: true},
		{name: "semantic output forbids replay", param: "input[0].id", semantic: true},
		{name: "conversation forbids replay", param: "input[0].id", conversation: true},
		{name: "retry consumes rpm", param: "input[0].id", retry: true, success: true, rpmLimit: 2},
		{name: "rpm blocks retry", param: "input[0].id", rpmLimit: 1},
		{name: "cancel during retry admission", param: "input[0].id", rpmLimit: 2, cancelRetry: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			errorType, code := "v_api_biz_error", "invalid_request"
			if tc.invalidValue {
				errorType, code = "invalid_request_error", "invalid_value"
			}
			body := `{"type":"error","status":400,"error":{"type":"` + errorType + `","code":"` + code + `","param":"` + tc.param + `","message":"Rejected replay item"}}`
			cfg := &config.Config{}
			cfg.Security.URLAllowlist.AllowInsecureHTTP = true
			cfg.Gateway.OpenAIWS.Enabled = true
			cfg.Gateway.OpenAIWS.APIKeyEnabled = true
			cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
			cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
			cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
			cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
			cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
			cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
			cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
			conn := &openAIWSCaptureConn{events: [][]byte{[]byte(body)}}
			if tc.bootstrap {
				conn.events = append([][]byte{[]byte(`{"type":"response.created","response":{"id":"resp_bad"}}`)}, conn.events...)
			}
			if tc.semantic {
				conn.events = append([][]byte{[]byte(`{"type":"response.output_item.added","item":{"type":"function_call","name":"exec","call_id":"call_keep"}}`)}, conn.events...)
			}
			second := &openAIWSCaptureConn{}
			if tc.staleTail {
				conn.events = append(conn.events, []byte(`{"type":"response.failed","response":{"id":"resp_bad","status":"failed","error":{"code":"invalid_value"}}}`))
			}
			if tc.retry {
				next := []byte(body)
				if tc.success {
					next = []byte(`{"type":"response.completed","response":{"id":"resp_ok","status":"completed","model":"gpt-6-astra","output":[],"usage":{"input_tokens":1,"output_tokens":1}}}`)
				}
				second.events = append(second.events, next)
			}
			pool := newOpenAIWSConnPool(cfg)
			dialer := &openAIWSQueueDialer{conns: []openAIWSClientConn{conn, second}}
			pool.setClientDialerForTest(dialer)
			t.Cleanup(pool.Close)
			upstream := &httpUpstreamRecorder{}
			service := &OpenAIGatewayService{
				cfg: cfg, httpUpstream: upstream, cache: &stubGatewayCache{},
				openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
				toolCorrector:    NewCodexToolCorrector(), openaiWSPool: pool,
			}
			account := &Account{ID: 5890, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Status: StatusActive, Schedulable: true, Concurrency: 1,
				Credentials: map[string]any{"api_key": "sk-test"},
				Extra:       map[string]any{"responses_websockets_v2_enabled": true}}
			recorder := httptest.NewRecorder()
			client, _ := gin.CreateTestContext(recorder)
			client.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var rpmCache *hardRPMCache
			if tc.rpmLimit > 0 {
				account.Extra["rpm_limit"] = tc.rpmLimit
				rpmCache = &hardRPMCache{GatewayCache: &stubGatewayCache{}}
				service.cache = rpmCache
				if tc.cancelRetry {
					rpmCache.onAdmit = func() {
						if rpmCache.used == 1 {
							cancel()
						}
					}
				}
			}
			request := `{"model":"gpt-6-astra","stream":false,"input":[{"type":"message","role":"assistant","id":"msg_old","content":"hello"}]}`
			if tc.stream {
				request = `{"model":"gpt-6-astra","stream":true,"input":[{"type":"message","role":"assistant","id":"msg_old","content":"hello"}]}`
			}
			if tc.conversation {
				request = `{"model":"gpt-6-astra","stream":false,"conversation":"conv_owner","input":[{"type":"message","role":"assistant","id":"msg_old","content":"hello"}]}`
			}
			result, err := service.Forward(ctx, client, account, []byte(request))
			if tc.success {
				require.NoError(t, err)
				require.Equal(t, "resp_ok", result.RequestID)
				if tc.stream {
					require.Contains(t, recorder.Body.String(), "response.completed")
					require.NotContains(t, recorder.Body.String(), "resp_bad")
					require.NotContains(t, recorder.Body.String(), "response.failed")
				}
			} else if tc.cancelRetry {
				require.ErrorIs(t, err, context.Canceled)
			} else if tc.rpmLimit == 1 {
				var rpmError *AccountRPMError
				require.ErrorAs(t, err, &rpmError)
			} else {
				require.Error(t, err)
				require.Equal(t, http.StatusBadRequest, recorder.Code)
				for _, field := range []string{"type", "code", "param"} {
					require.Equal(t, gjson.Get(body, "error."+field).String(), gjson.Get(recorder.Body.String(), "error."+field).String())
				}
			}
			require.Empty(t, upstream.bodies, "deterministic ID rejection must not fall back to HTTP")
			conn.mu.Lock()
			writes := append([]map[string]any(nil), conn.writes...)
			conn.mu.Unlock()
			second.mu.Lock()
			writes = append(writes, second.writes...)
			second.mu.Unlock()
			want := 1
			if tc.retry {
				require.Equal(t, 2, dialer.DialCount(), "repair must isolate rejected connection tail")
				want = 2
			}
			require.Len(t, writes, want)
			if rpmCache != nil {
				require.Len(t, rpmCache.ids, 2, "retry makes a separate admission attempt")
			}
			if tc.retry {
				items := writes[1]["input"].([]any)
				_, idPresent := items[0].(map[string]any)["id"]
				require.False(t, idPresent)
			}
		})
	}
}

type openAIReplayIDReacquireDialer struct {
	first, second *openAIWSCaptureConn
	mu            sync.Mutex
	calls         int
	retryStatus   int
	retryOnce     bool
	omitTurnState bool
}

func (d *openAIReplayIDReacquireDialer) Dial(ctx context.Context, wsURL string, headers http.Header, proxyURL string) (openAIWSClientConn, int, http.Header, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls++
	if d.calls == 1 {
		return d.first, http.StatusSwitchingProtocols, http.Header{http.CanonicalHeaderKey(openAIWSTurnStateHeader): {"turn_old"}}, nil
	}
	if d.retryStatus != 0 && (!d.retryOnce || d.calls == 2) {
		return nil, d.retryStatus, http.Header{"Retry-After": {"60"}}, errors.New("retry handshake rejected")
	}
	retryHeaders := http.Header{}
	if !d.omitTurnState {
		retryHeaders.Set(openAIWSTurnStateHeader, "turn_new")
	}
	return d.second, http.StatusSwitchingProtocols, retryHeaders, nil
}

func TestOpenAIReplayIDWSV2ReacquireWithoutTurnState(t *testing.T) {
	cfg := newOpenAIWSV2TestConfig()
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	dialer := &openAIReplayIDReacquireDialer{
		first:         &openAIWSCaptureConn{events: [][]byte{[]byte(`{"type":"error","status":400,"error":{"type":"invalid_request_error","code":"invalid_value","param":"input[0].id"}}`)}},
		second:        &openAIWSCaptureConn{events: [][]byte{[]byte(`{"type":"response.completed","response":{"id":"resp_no_state","status":"completed","model":"gpt-6-astra","output":[]}}`)}},
		omitTurnState: true,
	}
	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(dialer)
	t.Cleanup(pool.Close)
	service := &OpenAIGatewayService{cfg: cfg, openaiWSPool: pool, cache: &stubGatewayCache{}, toolCorrector: NewCodexToolCorrector()}
	account := &Account{ID: 5993, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test"}}
	recorder := httptest.NewRecorder()
	client, _ := gin.CreateTestContext(recorder)
	client.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	body := map[string]any{"model": "gpt-6-astra", "input": []any{map[string]any{"type": "message", "id": "msg_old", "role": "assistant", "content": "history"}}}
	recovery := false
	result, err := service.forwardOpenAIWSV2(t.Context(), client, account, body, "cache_test", "scope_test", "sk-test",
		OpenAIWSProtocolDecision{Transport: OpenAIUpstreamTransportResponsesWebsocketV2}, false, false, "gpt-6-astra", "gpt-6-astra", time.Now(), 1, "", &recovery)
	require.NoError(t, err)
	require.Equal(t, "resp_no_state", result.RequestID)
	require.Empty(t, recorder.Header().Get(openAIWSTurnStateHeader))
	require.Empty(t, result.ResponseHeaders.Get(openAIWSTurnStateHeader))
	_, exists := service.getOpenAIWSStateStore().GetSessionTurnState(0, "scope_test")
	require.False(t, exists, "discarded connection turn-state must not remain in the session")
}

func TestOpenAIReplayIDWSV2RepairSurvivesReconnect(t *testing.T) {
	cfg := newOpenAIWSV2TestConfig()
	cfg.Gateway.OpenAIWS.PrewarmGenerateEnabled = true
	cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
	cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
	dialer := &openAIReplayIDReacquireDialer{
		first:       &openAIWSCaptureConn{events: [][]byte{[]byte(`{"type":"error","status":400,"error":{"type":"invalid_request_error","code":"invalid_value","param":"input[0].id"}}`)}},
		second:      &openAIWSCaptureConn{events: [][]byte{[]byte(`{"type":"response.completed","response":{"id":"resp_reconnect","status":"completed","model":"gpt-6-astra","output":[]}}`)}},
		retryStatus: http.StatusServiceUnavailable,
		retryOnce:   true,
	}
	pool := newOpenAIWSConnPool(cfg)
	pool.setClientDialerForTest(dialer)
	t.Cleanup(pool.Close)
	upstream := &httpUpstreamRecorder{}
	service := &OpenAIGatewayService{cfg: cfg, openaiWSPool: pool, cache: &stubGatewayCache{}, httpUpstream: upstream,
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), toolCorrector: NewCodexToolCorrector()}
	account := &Account{ID: 5992, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test"}, Extra: map[string]any{"responses_websockets_v2_enabled": true}}
	recorder := httptest.NewRecorder()
	client, _ := gin.CreateTestContext(recorder)
	client.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	result, err := service.Forward(t.Context(), client, account, []byte(`{"model":"gpt-6-astra","stream":false,"input":[{"type":"custom_tool_call","id":"ctc_old","call_id":"call_keep","name":"exec","input":"pwd"}]}`))
	require.NoError(t, err)
	require.Equal(t, "resp_reconnect", result.RequestID)
	require.Empty(t, upstream.bodies)
	dialer.mu.Lock()
	require.Equal(t, 3, dialer.calls)
	dialer.mu.Unlock()
	dialer.second.mu.Lock()
	defer dialer.second.mu.Unlock()
	require.Len(t, dialer.second.writes, 1)
	written := payloadAsJSONBytes(dialer.second.writes[0])
	require.False(t, gjson.GetBytes(written, "input.0.id").Exists())
	require.Equal(t, "call_keep", gjson.GetBytes(written, "input.0.call_id").String())
	require.False(t, gjson.GetBytes(written, "generate").Exists(), "repaired tool history must still suppress prewarm")
}

func TestOpenAIReplayIDWSV2ReacquireMetadataAndFailure(t *testing.T) {
	for _, status := range []int{0, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			cfg := newOpenAIWSV2TestConfig()
			cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
			cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
			dialer := &openAIReplayIDReacquireDialer{
				first:       &openAIWSCaptureConn{events: [][]byte{[]byte(`{"type":"error","status":400,"error":{"type":"invalid_request_error","code":"invalid_value","param":"input[0].id"}}`)}},
				second:      &openAIWSCaptureConn{events: [][]byte{[]byte(`{"type":"response.completed","response":{"id":"resp_retry","status":"completed","model":"gpt-6-astra","output":[]}}`)}},
				retryStatus: status,
			}
			pool := newOpenAIWSConnPool(cfg)
			pool.setClientDialerForTest(dialer)
			t.Cleanup(pool.Close)
			repo := &openAIWSRateLimitSignalRepo{}
			rateLimitService := NewRateLimitService(repo, nil, cfg, nil, nil)
			service := &OpenAIGatewayService{cfg: cfg, openaiWSPool: pool, accountRepo: repo, rateLimitService: rateLimitService, cache: &stubGatewayCache{}, toolCorrector: NewCodexToolCorrector()}
			account := &Account{ID: 5991, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
				Credentials: map[string]any{"api_key": "sk-test"}}
			recorder := httptest.NewRecorder()
			client, _ := gin.CreateTestContext(recorder)
			client.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			body := map[string]any{"model": "gpt-6-astra", "input": []any{map[string]any{"type": "message", "id": "msg_old", "role": "assistant", "content": "history"}}}
			recovery := false
			result, err := service.forwardOpenAIWSV2(t.Context(), client, account, body, "cache_test", "", "sk-test",
				OpenAIWSProtocolDecision{Transport: OpenAIUpstreamTransportResponsesWebsocketV2}, false, false, "gpt-6-astra", "gpt-6-astra", time.Now(), 1, "", &recovery)
			require.Equal(t, 2, dialer.calls)
			if status == 0 {
				require.NoError(t, err)
				require.Equal(t, "resp_retry", result.RequestID)
				require.Equal(t, "turn_new", recorder.Header().Get(openAIWSTurnStateHeader))
				require.Equal(t, "turn_new", result.ResponseHeaders.Get(openAIWSTurnStateHeader))
				connID, exists := client.Get(OpsOpenAIWSConnIDKey)
				require.True(t, exists)
				storedID, exists := service.getOpenAIWSStateStore().GetResponseConn("resp_retry")
				require.True(t, exists)
				require.Equal(t, storedID, connID)
				reused, exists := client.Get(OpsOpenAIWSConnReusedKey)
				require.True(t, exists)
				require.Equal(t, false, reused)
			} else {
				require.Nil(t, result)
				var fallback *openAIWSFallbackError
				require.ErrorAs(t, err, &fallback)
				var dialErr *openAIWSDialError
				require.ErrorAs(t, err, &dialErr)
				require.Equal(t, status, dialErr.StatusCode)
				require.False(t, recorder.Flushed)
				if status == http.StatusTooManyRequests {
					require.NotEmpty(t, repo.rateLimitCalls)
				}
			}
		})
	}
}
