package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestPrepareOpenAIWSHTTPBridgeBodyPreservesOpaqueContinuation(t *testing.T) {
	body, err := prepareOpenAIWSHTTPBridgeBody(opaqueModelMismatchTestAccount(), []byte(`{"type":"response.create","model":"gpt-5.5","previous_response_id":"resp_bound","input":"continue"}`))
	require.NoError(t, err)
	require.Equal(t, "resp_bound", gjson.GetBytes(body, "previous_response_id").String())
	require.False(t, gjson.GetBytes(body, "type").Exists())
	require.True(t, gjson.GetBytes(body, "stream").Bool())
}

func TestProxyOpenAIWSHTTPBridgeTurnOpaqueMismatchBeforeCommit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, turn := range []int{1, 2} {
		t.Run(strconv.Itoa(turn), func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: opaqueModelMismatchSSEResponse()}
			svc := &OpenAIGatewayService{
				cfg:          &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}},
				httpUpstream: upstream,
			}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
			payload := []byte(`{"type":"response.create","model":"gpt-5.5","input":"hello"}`)
			writes := 0
			_, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, opaqueModelMismatchTestAccount(), "test-token", payload, len(payload), "gpt-5.5", "", "", "", "", turn, func([]byte) error {
				writes++
				return nil
			})
			var failover *UpstreamFailoverError
			require.ErrorAs(t, err, &failover)
			require.True(t, failover.OpaqueRouteEpochEligible)
			require.Equal(t, OpenAIIntegritySignalModelMismatch, failover.IntegritySignal)
			require.Zero(t, writes, "wrong-model frames must not reach the client")
		})
	}
}

func TestProxyOpenAIWSHTTPBridgeTurnOpaqueMismatchAfterCommitDoesNotReplay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sse := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_committed","model":"gpt-5.5"}}`,
		``,
		`data: {"type":"response.output_text.delta","delta":"hello"}`,
		``,
		`data: {"type":"response.completed","response":{"id":"resp_committed","model":"gpt-4.1-mini","status":"completed"}}`,
		``,
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(sse)),
	}}
	svc := &OpenAIGatewayService{
		cfg:          &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}},
		httpUpstream: upstream,
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	payload := []byte(`{"type":"response.create","model":"gpt-5.5","input":"hello"}`)
	writes := 0
	result, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c, opaqueModelMismatchTestAccount(), "test-token", payload, len(payload), "gpt-5.5", "", "", "", "", 1, func([]byte) error {
		writes++
		return nil
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 3, writes)
	require.True(t, result.UpstreamResponseModelConflict)
}

func TestOpenAIWSOpaqueIngressRouteIdentityMatchesHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{OpenAIWSIngressModeHTTPBridge, OpenAIWSIngressModeCtxPool} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cfg := passthroughLifecycleConfig()
			cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
			account := passthroughLifecycleAccount()
			account.Extra["openai_opaque_upstream"] = true
			account.Extra["openai_apikey_responses_websockets_v2_mode"] = mode
			svc := newPassthroughLifecycleService(cfg, newStagedPassthroughConn())
			_, advanced, err := svc.BumpOpenAIOpaqueRouteEpoch(ctx, 0, "session", "gpt-5.1", "", account.ID, 0)
			require.NoError(t, err)
			require.True(t, advanced)
			store := svc.getOpenAIWSStateStore()
			require.NoError(t, store.BindResponseAccount(ctx, 0, "resp_http_owner", account.ID, time.Hour))
			require.NoError(t, store.BindResponseRouteEpoch(ctx, 0, "resp_http_owner", account.ID, 1, time.Hour))
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			_, err = svc.PrepareOpenAIOpaqueRouteEpochAttempt(ctx, c, 0, "session", "gpt-5.1", "", account, "resp_http_owner")
			require.NoError(t, err)
			req, err := svc.buildUpstreamRequestOpenAIPassthrough(ctx, c, account, []byte(`{"model":"gpt-5.1","prompt_cache_key":"original-cache"}`), "test-token")
			require.NoError(t, err)
			httpBody, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			require.NoError(t, req.Body.Close())
			want := gjson.GetBytes(httpBody, "prompt_cache_key").String()
			require.Equal(t, openAIOpaqueRouteEpochValue(account.ID, 1, "prompt-cache", "original-cache"), want)
			completed := `{"type":"response.completed","response":{"id":"resp_ws_continued","model":"gpt-5.1"}}`
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}},
				Body: io.NopCloser(strings.NewReader("data: " + completed + "\n\n")),
			}}
			svc.httpUpstream = upstream
			capture := &openAIWSCaptureConn{events: [][]byte{[]byte(completed)}}
			pool := newOpenAIWSConnPool(cfg)
			pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: capture})
			svc.openaiWSPool = pool
			defer pool.Close()
			server, serverErr := startPassthroughLifecycleServerWithHooks(t, ctx, svc, account, func(c *gin.Context) *OpenAIWSIngressHooks {
				_, err := svc.PrepareOpenAIOpaqueRouteEpochAttempt(ctx, c, 0, "session", "gpt-5.1", "", account, "resp_http_owner")
				require.NoError(t, err)
				return nil
			})
			defer server.Close()
			client := dialPassthroughLifecycleClientWithPayload(t, server, `{"type":"response.create","model":"gpt-5.1","store":true,"previous_response_id":"resp_http_owner","prompt_cache_key":"original-cache","input":"continue"}`)
			defer client.CloseNow()
			_, err = readPassthroughLifecycleFrame(t, client, 3*time.Second)
			require.NoError(t, err)
			require.NoError(t, client.Close(coderws.StatusNormalClosure, "done"))
			select {
			case err := <-serverErr:
				require.NoError(t, err)
			case <-time.After(3 * time.Second):
				t.Fatal("websocket ingress did not stop")
			}
			if mode == OpenAIWSIngressModeHTTPBridge {
				require.Len(t, upstream.bodies, 1)
				require.Equal(t, want, gjson.GetBytes(upstream.bodies[0], "prompt_cache_key").String())
				require.Equal(t, "resp_http_owner", gjson.GetBytes(upstream.bodies[0], "previous_response_id").String())
			} else {
				capture.mu.Lock()
				defer capture.mu.Unlock()
				require.Len(t, capture.writes, 1)
				require.Equal(t, want, capture.writes[0]["prompt_cache_key"])
				require.Equal(t, "resp_http_owner", capture.writes[0]["previous_response_id"])
			}
		})
	}
}

func TestOpenAIWSHTTPBridgePreservesRequestedEffortBeforePolicyRewrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	account := passthroughLifecycleAccount()
	account.Extra["openai_apikey_responses_websockets_v2_mode"] = OpenAIWSIngressModeHTTPBridge
	svc := newPassthroughLifecycleService(passthroughLifecycleConfig(), newStagedPassthroughConn())
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_effort\",\"model\":\"gpt-5.1\"}}\n\n")),
	}}
	svc.httpUpstream = upstream
	results := make(chan *OpenAIForwardResult, 1)
	server, serverErr := startPassthroughLifecycleServerWithHooks(t, ctx, svc, account, func(*gin.Context) *OpenAIWSIngressHooks {
		return &OpenAIWSIngressHooks{
			ReasoningEffortMappings: []ReasoningEffortMapping{{From: "high", To: "low"}},
			AfterTurn: func(_ int, result *OpenAIForwardResult, err error) {
				require.NoError(t, err)
				results <- result
			},
		}
	})
	defer server.Close()
	client := dialPassthroughLifecycleClientWithPayload(t, server, `{"type":"response.create","model":"gpt-5.1","reasoning":{"effort":"high"},"input":"hello"}`)
	defer client.CloseNow()
	_, err := readPassthroughLifecycleFrame(t, client, 3*time.Second)
	require.NoError(t, err)
	select {
	case result := <-results:
		require.NotNil(t, result)
		require.NotNil(t, result.RequestedReasoningEffort)
		require.Equal(t, "high", *result.RequestedReasoningEffort)
	case <-time.After(3 * time.Second):
		t.Fatal("bridge result was not reported")
	}
	require.NoError(t, client.Close(coderws.StatusNormalClosure, "done"))
	select {
	case err := <-serverErr:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("bridge did not stop")
	}
	require.Len(t, upstream.bodies, 1)
	require.Equal(t, "low", gjson.GetBytes(upstream.bodies[0], "reasoning.effort").String())
}
