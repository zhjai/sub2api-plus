package service

import (
	"context"
	"encoding/json"
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
	"github.com/tidwall/gjson"
)

func TestOpenAIVAPINativeWSReplayIDBoundaries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name         string
		param        string
		nextParam    string
		priorOutput  bool
		staleTail    bool
		budgetSpent  bool
		wantAttempts int
		invalidValue bool
	}{
		{name: "wildcard preserved", param: "input*****.id", wantAttempts: 1},
		{name: "logical turn two budget retained after account reentry", param: "input[1].id", wantAttempts: 1, budgetSpent: true},
		{name: "indexed repaired once", param: "input[1].id", wantAttempts: 2},
		{name: "rejected connection tail discarded", param: "input[1].id", wantAttempts: 2, staleTail: true},
		{name: "different index stops", param: "input[1].id", nextParam: "input[0].id", wantAttempts: 2},
		{name: "no replay after output", param: "input[1].id", priorOutput: true, wantAttempts: 1},
		{name: "invalid value repaired", param: "input[1].id", wantAttempts: 2, invalidValue: true},
		{name: "invalid value repeated stops", param: "input[1].id", nextParam: "input[0].id", wantAttempts: 2, invalidValue: true},
		{name: "invalid value after output", param: "input[1].id", priorOutput: true, wantAttempts: 1, invalidValue: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			errorEvent := func(param string) []byte {
				if tc.invalidValue {
					return []byte(`{"type":"error","status":400,"error":{"type":"invalid_request_error","code":"invalid_value","param":"` + param + `","message":"Rejected replay item (request id: req_ws)"}}`)
				}
				return []byte(`{"type":"error","status":400,"error":{"type":"v_api_biz_error","code":"invalid_request","param":"` + param + `","message":"Rejected replay item (request id: req_ws)"}}`)
			}
			first := &openAIWSCaptureConn{events: [][]byte{errorEvent(tc.param)}}
			if tc.priorOutput {
				first.events = append([][]byte{[]byte(`{"type":"response.output_text.delta","delta":"already visible"}`)}, first.events...)
			}
			if tc.staleTail {
				first.events = append(first.events, []byte(`{"type":"response.failed","response":{"id":"resp_rejected","status":"failed"}}`))
			}
			second := &openAIWSCaptureConn{events: [][]byte{[]byte(`{"type":"response.completed","response":{"id":"resp_ws","model":"gpt-6-astra","usage":{"input_tokens":1,"output_tokens":1}}}`)}}
			if tc.nextParam != "" {
				second.events = [][]byte{errorEvent(tc.nextParam)}
			}
			cfg := &config.Config{}
			cfg.Security.URLAllowlist.AllowInsecureHTTP = true
			cfg.Gateway.OpenAIWS.Enabled = true
			cfg.Gateway.OpenAIWS.APIKeyEnabled = true
			cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
			cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 2
			cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 2
			cfg.Gateway.OpenAIWS.QueueLimitPerConn = 8
			cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
			cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
			cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
			dialer := &openAIWSQueueDialer{conns: []openAIWSClientConn{first, second}}
			pool := newOpenAIWSConnPool(cfg)
			defer pool.Close()
			pool.setClientDialerForTest(dialer)
			svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: &httpUpstreamRecorder{}, cache: &stubGatewayCache{},
				openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), toolCorrector: NewCodexToolCorrector(), openaiWSPool: pool}
			account := &Account{ID: 999, Name: "vapi-ws-test", Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Status: StatusActive, Schedulable: true, Concurrency: 1,
				Credentials: map[string]any{"api_key": "sk-test"}, Extra: map[string]any{"responses_websockets_v2_enabled": true}}
			finished := make(chan error, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := coderws.Accept(w, r, nil)
				if err != nil {
					finished <- err
					return
				}
				defer conn.CloseNow()
				_, body, err := conn.Read(ctx)
				if err != nil {
					finished <- err
					return
				}
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = r.Clone(ctx)
				if tc.budgetSpent {
					spent := openAIResponsesRejectedFieldRetryStateForWSTurn(c, body, 2)
					if !spent.AllowNormalization([]byte(`{"input":[]}`), openAIReplayIDRejectionReason) {
						finished <- errors.New("failed to seed prior logical turn budget")
						return
					}
				}
				finished <- svc.ProxyResponsesWebSocketFromClient(ctx, c, conn, account, "sk-test", body, nil)
			}))
			defer server.Close()
			conn, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
			require.NoError(t, err)
			defer conn.CloseNow()
			body := []byte(`{"type":"response.create","model":"gpt-6-astra","input":[{"type":"message","id":"msg_old","role":"assistant","content":"hello"},{"type":"function_call","id":"fc_old","call_id":"call_1","name":"exec","arguments":"{}"},{"type":"function_call_output","id":"out_old","call_id":"call_1","output":"done"}]}`)
			require.NoError(t, conn.Write(ctx, coderws.MessageText, body))
			_, event, err := conn.Read(ctx)
			require.NoError(t, err)
			if tc.priorOutput {
				require.Equal(t, "already visible", gjson.GetBytes(event, "delta").String())
				_, event, err = conn.Read(ctx)
				require.NoError(t, err)
			}
			if tc.wantAttempts == 2 && tc.nextParam == "" {
				require.Equal(t, "response.completed", gjson.GetBytes(event, "type").String())
			} else {
				param := tc.param
				if tc.nextParam != "" {
					param = tc.nextParam
				}
				errorType := "v_api_biz_error"
				if tc.invalidValue {
					errorType = "invalid_request_error"
				}
				require.Equal(t, errorType, gjson.GetBytes(event, "error.type").String())
				require.Equal(t, param, gjson.GetBytes(event, "error.param").String())
				require.Contains(t, gjson.GetBytes(event, "error.message").String(), "req_ws")
			}
			_ = conn.Close(coderws.StatusNormalClosure, "done")
			select {
			case <-finished:
			case <-ctx.Done():
				t.Fatal("websocket ingress did not finish")
			}
			require.Equal(t, tc.wantAttempts, dialer.DialCount(), "repair isolates the rejected upstream connection")
			first.mu.Lock()
			firstWrites := append([]map[string]any(nil), first.writes...)
			first.mu.Unlock()
			second.mu.Lock()
			firstWrites = append(firstWrites, second.writes...)
			second.mu.Unlock()
			require.Len(t, firstWrites, tc.wantAttempts)
			if tc.wantAttempts == 2 {
				payload, err := json.Marshal(firstWrites[1])
				require.NoError(t, err)
				require.False(t, gjson.GetBytes(payload, "input.1.id").Exists())
				require.Equal(t, "msg_old", gjson.GetBytes(payload, "input.0.id").String())
				require.Equal(t, "out_old", gjson.GetBytes(payload, "input.2.id").String())
				require.Equal(t, "call_1", gjson.GetBytes(payload, "input.1.call_id").String())
				require.Equal(t, "call_1", gjson.GetBytes(payload, "input.2.call_id").String())
			}
		})
	}
}

func TestOpenAIReplayIDNativeWSSecondTurnFailoverBudget(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	rejection := []byte(`{"type":"error","status":400,"error":{"type":"v_api_biz_error","code":"invalid_request","param":"input[0].id","message":"Rejected replay item"}}`)
	first := &openAIWSCaptureConn{events: [][]byte{
		[]byte(`{"type":"response.completed","response":{"id":"resp_first","status":"completed","model":"gpt-6-astra","output":[]}}`), rejection,
	}}
	repaired := &openAIWSCaptureConn{events: [][]byte{[]byte(`{"type":"error","status":429,"error":{"type":"rate_limit_error","code":"rate_limit_exceeded","message":"rate limit reached"}}`)}}
	nextAccount := &openAIWSCaptureConn{events: [][]byte{rejection}}
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
	dialer := &openAIWSQueueDialer{conns: []openAIWSClientConn{first, repaired, nextAccount}}
	pool := newOpenAIWSConnPool(cfg)
	t.Cleanup(pool.Close)
	pool.setClientDialerForTest(dialer)
	svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: &httpUpstreamRecorder{}, cache: &stubGatewayCache{},
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), toolCorrector: NewCodexToolCorrector(), openaiWSPool: pool}
	account := &Account{ID: 799, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_key": "sk-test"}, Extra: map[string]any{"responses_websockets_v2_enabled": true}}
	finished := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			finished <- err
			return
		}
		defer conn.CloseNow()
		_, body, err := conn.Read(ctx)
		if err != nil {
			finished <- err
			return
		}
		client, _ := gin.CreateTestContext(httptest.NewRecorder())
		client.Request = r.Clone(ctx)
		err = svc.ProxyResponsesWebSocketFromClient(ctx, client, conn, account, "sk-test", body, nil)
		var failover *UpstreamFailoverError
		if !errors.As(err, &failover) {
			finished <- fmt.Errorf("expected second-turn failover, got %w", err)
			return
		}
		retryBody, ok := OpenAIWSCurrentTurnRetryPayload(err)
		if !ok || len(retryBody) == 0 {
			finished <- errors.New("missing current-turn retry payload")
			return
		}
		other := *account
		other.ID++
		finished <- svc.ProxyResponsesWebSocketFromClient(ctx, client, conn, &other, "sk-test", retryBody, nil)
	}))
	t.Cleanup(server.Close)
	conn, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	defer conn.CloseNow()
	initial := []byte(`{"type":"response.create","model":"gpt-6-astra","input":[{"type":"message","role":"user","content":"first"}]}`)
	require.NoError(t, conn.Write(ctx, coderws.MessageText, initial))
	_, event, err := conn.Read(ctx)
	require.NoError(t, err)
	require.Equal(t, "response.completed", gjson.GetBytes(event, "type").String())
	second := []byte(`{"type":"response.create","model":"gpt-6-astra","input":[{"type":"message","role":"assistant","id":"msg_second","content":"second"}]}`)
	require.NoError(t, conn.Write(ctx, coderws.MessageText, second))
	_, event, err = conn.Read(ctx)
	require.NoError(t, err)
	require.Equal(t, "invalid_request", gjson.GetBytes(event, "error.code").String())
	require.Equal(t, "input[0].id", gjson.GetBytes(event, "error.param").String())
	_ = conn.Close(coderws.StatusNormalClosure, "done")
	select {
	case err := <-finished:
		require.Error(t, err)
	case <-ctx.Done():
		t.Fatal("second-turn failover did not finish")
	}
	require.Equal(t, 3, dialer.DialCount())
	repaired.mu.Lock()
	retryWrites := append([]map[string]any(nil), repaired.writes...)
	repaired.mu.Unlock()
	require.Len(t, retryWrites, 1)
	items := retryWrites[0]["input"].([]any)
	_, idPresent := items[0].(map[string]any)["id"]
	require.False(t, idPresent)
	nextAccount.mu.Lock()
	nextWrites := append([]map[string]any(nil), nextAccount.writes...)
	nextAccount.mu.Unlock()
	require.Len(t, nextWrites, 1, "new account must not receive another repaired attempt")
}
