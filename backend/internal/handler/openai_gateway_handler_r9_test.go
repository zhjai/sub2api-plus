package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/testutil"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func r9PostResponses(t *testing.T, server *httptest.Server, session, body string) (int, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/openai/v1/responses", strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("session_id", session)
	response, err := server.Client().Do(req)
	require.NoError(t, err)
	defer response.Body.Close()
	output, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	return response.StatusCode, output
}

func r9SeedEpoch(t *testing.T, svc *service.OpenAIGatewayService, cache *r6EpochCache, group, account int64, session, body string) string {
	t.Helper()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	c.Request.Header.Set("session_id", session)
	hash := svc.GenerateSessionHash(c, []byte(body))
	_, advanced, err := svc.BumpOpenAIOpaqueRouteEpoch(t.Context(), group, hash, "gpt-5.1", "high", account, 0)
	require.NoError(t, err)
	require.True(t, advanced)
	<-cache.bumps
	return hash
}

func r9EpochCache(t *testing.T) *r6EpochCache {
	t.Helper()
	base := testutil.NewRedisGatewayCache(t)
	return &r6EpochCache{GatewayCache: base, epochs: base.(service.OpenAIOpaqueRouteEpochCache), bumps: make(chan r6EpochBump, 16)}
}

func r9RequireResponseBinding(t *testing.T, cache service.GatewayCache, group int64, responseID string, account, epoch int64) {
	t.Helper()
	// A fresh store must reconstruct the binding from Redis, including after cancellation.
	store := service.NewOpenAIWSStateStore(cache)
	require.Eventually(t, func() bool {
		owner, err := store.GetResponseAccount(t.Context(), group, responseID)
		return err == nil && owner == account
	}, time.Second, 10*time.Millisecond, "response account binding missing for %s", responseID)
	require.Eventually(t, func() bool {
		got, found, err := store.GetResponseRouteEpoch(t.Context(), group, responseID, account)
		return err == nil && found && got == epoch
	}, time.Second, 10*time.Millisecond, "creating epoch missing for %s", responseID)
}

func TestOpenAIResponses_R9RawProtocolDoesNotBumpEpoch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, declared := range []bool{false, true} {
		t.Run(fmt.Sprintf("declared_and_terminal_exec_%v", declared), func(t *testing.T) {
			var calls atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				call := ""
				if declared {
					call = `,{"type":"custom_tool_call","name":"exec","call_id":"call_r9_raw","input":"pwd"}`
				}
				_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_r9_raw\",\"model\":\"gpt-5.1\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":%q}]}%s],\"usage\":{\"input_tokens\":2,\"output_tokens\":3}}}\n\n", "to=functions.exec code:\n{\"cmd\":\"pwd\"}", call)
			}))
			defer upstream.Close()
			account := openAIWSR5Account(9991, "r9-native", upstream.URL, service.StatusActive, true, true, 1)
			account.Extra["openai_apikey_responses_websockets_v2_enabled"] = false
			cache := r9EpochCache(t)
			_, svc, apiKey, server, concurrency := newOpenAIR5Handler(t, []service.Account{account}, cache, &r7NetworkHTTPUpstream{client: upstream.Client()})
			defer server.Close()
			tools := "[]"
			if declared {
				tools = `[{"type":"custom","name":"exec"}]`
			}
			body := fmt.Sprintf(`{"model":"gpt-5.1","reasoning":{"effort":"high"},"stream":true,"prompt_cache_key":"r9-raw","input":"hello","tools":%s}`, tools)
			hash := r9SeedEpoch(t, svc, cache, *apiKey.GroupID, account.ID, "r9-raw", body)
			status, output := r9PostResponses(t, server, "r9-raw", body)
			require.Equal(t, http.StatusOK, status)
			require.Contains(t, string(output), "resp_r9_raw")
			require.Contains(t, string(output), "to=functions.exec")
			require.Eventually(t, func() bool { return atomic.LoadInt32(&concurrency.releaseAccountCalled) == 1 }, time.Second, 10*time.Millisecond)
			require.Empty(t, cache.bumps)
			require.Equal(t, int64(1), svc.SnapshotOpenAIOpaqueRouteEpoch(t.Context(), *apiKey.GroupID, hash, "gpt-5.1", "high", account.ID).Epoch)
			require.Equal(t, int32(1), calls.Load())
		})
	}
}

func TestOpenAIResponses_R9PassthroughPartialRetainsOwnerAndCreatingEpoch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, terminal := range []string{"partial_eof", "stream_terminated"} {
		t.Run(terminal, func(t *testing.T) {
			var calls atomic.Int32
			frames := make(chan []byte, 4)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					return
				}
				frames <- body
				call := calls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				if call == 1 {
					_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"response_id\":\"resp_r9_partial\",\"delta\":\"delivered output\"}\n\n")
					w.(http.Flusher).Flush()
					if terminal == "stream_terminated" {
						_, _ = fmt.Fprint(w, "data: {\"type\":\"response.incomplete\",\"response\":{\"id\":\"resp_r9_partial\",\"model\":\"gpt-5.1\",\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"stream_terminated\"},\"output\":[]}}\n\n")
					}
					return
				}
				_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_r9_future_root\",\"model\":\"gpt-5.1\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"normal output\"}]}]}}\n\n")
			}))
			defer upstream.Close()
			account := openAIWSR5Account(9992, "r9-passthrough", upstream.URL, service.StatusActive, true, true, 1)
			account.Extra["openai_apikey_responses_websockets_v2_enabled"] = false
			account.Extra["openai_passthrough"] = true
			cache := r9EpochCache(t)
			_, svc, apiKey, server, concurrency := newOpenAIR5Handler(t, []service.Account{account}, cache, &r7NetworkHTTPUpstream{client: upstream.Client()})
			defer server.Close()
			const body = `{"model":"gpt-5.1","reasoning":{"effort":"high"},"stream":true,"prompt_cache_key":"r9-partial","store":false,"input":"hello"}`
			group := *apiKey.GroupID
			hash := r9SeedEpoch(t, svc, cache, group, account.ID, "r9-partial", body)
			status, output := r9PostResponses(t, server, "r9-partial", body)
			require.Equal(t, http.StatusOK, status)
			require.Contains(t, string(output), "resp_r9_partial")
			require.Contains(t, string(output), "delivered output")
			require.Eventually(t, func() bool { return atomic.LoadInt32(&concurrency.releaseAccountCalled) == 1 }, time.Second, 10*time.Millisecond)
			require.Equal(t, int32(1), calls.Load(), "a committed partial response must never replay")
			r9RequireResponseBinding(t, cache, group, "resp_r9_partial", account.ID, 1)
			owned, err := svc.ValidateOpenAIHTTPResponseOwner(t.Context(), group, "resp_r9_partial", apiKey.User.ID, apiKey.ID)
			require.NoError(t, err)
			require.True(t, owned, "delivered response retains tenant owner")
			require.Eventually(t, func() bool { return len(cache.bumps) == 1 }, time.Second, 10*time.Millisecond)
			require.Equal(t, r6EpochBump{model: "gpt-5.1", effort: "high", expected: 1}, <-cache.bumps)
			require.Equal(t, int64(2), svc.SnapshotOpenAIOpaqueRouteEpoch(t.Context(), group, hash, "gpt-5.1", "high", account.ID).Epoch)
			original := <-frames
			status, output = r9PostResponses(t, server, "r9-partial", body)
			require.Equal(t, http.StatusOK, status)
			require.Contains(t, string(output), "resp_r9_future_root")
			future := <-frames
			require.Equal(t, int32(2), calls.Load())
			require.NotEmpty(t, gjson.GetBytes(original, "prompt_cache_key").String())
			require.NotEqual(t, gjson.GetBytes(original, "prompt_cache_key").String(), gjson.GetBytes(future, "prompt_cache_key").String())
			require.Empty(t, gjson.GetBytes(future, "previous_response_id").String())
			r9RequireResponseBinding(t, cache, group, "resp_r9_partial", account.ID, 1)
			r9RequireResponseBinding(t, cache, group, "resp_r9_future_root", account.ID, 2)
		})
	}
}

type r9TenantOwnerCache struct {
	service.GatewayCache
	lookupErr error
	lookups   atomic.Int32
}

func (c *r9TenantOwnerCache) GetSessionAccountID(ctx context.Context, group int64, key string) (int64, error) {
	if strings.HasPrefix(key, "openai:http-response-owner:") {
		c.lookups.Add(1)
		return 0, c.lookupErr
	}
	return c.GatewayCache.GetSessionAccountID(ctx, group, key)
}

func TestOpenAIResponses_R9ContinuationRequiresVerifiedAccountOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name            string
		tenant, account bool
		lookupErr       error
		status          int
	}{
		{name: "tenant_bound_account_absent", tenant: true, status: http.StatusBadRequest},
		{name: "cold_tenant_cache_outage", lookupErr: errors.New("synthetic tenant cache outage"), status: http.StatusServiceUnavailable},
		{name: "cold_tenant_cache_not_found", lookupErr: service.ErrStickySessionNotFound, status: http.StatusBadRequest},
		{name: "verified_ordinary_owner", tenant: true, account: true, status: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var writes atomic.Int32
			frames := make(chan []byte, 4)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				frames <- body
				writes.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, `{"id":"resp_r9_ordinary_next","model":"gpt-5.1","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"continued"}]}],"usage":{"input_tokens":2,"output_tokens":3}}`)
			}))
			defer upstream.Close()
			account := openAIWSR5Account(9993, "r9-ordinary", upstream.URL, service.StatusActive, true, false, 1)
			account.Extra["openai_apikey_responses_websockets_v2_enabled"] = false
			base := testutil.NewRedisGatewayCache(t)
			var cache service.GatewayCache = base
			var failing *r9TenantOwnerCache
			if tc.lookupErr != nil {
				failing = &r9TenantOwnerCache{GatewayCache: base, lookupErr: tc.lookupErr}
				cache = failing
			}
			_, svc, apiKey, server, _ := newOpenAIR5Handler(t, []service.Account{account}, cache, &r7NetworkHTTPUpstream{client: upstream.Client()})
			defer server.Close()
			const responseID = "resp_r9_ordinary_owner"
			if tc.tenant {
				require.NoError(t, svc.BindOpenAIHTTPResponseOwner(t.Context(), *apiKey.GroupID, responseID, apiKey.User.ID, apiKey.ID))
			}
			if tc.account {
				store := service.NewOpenAIWSStateStore(cache)
				require.NoError(t, store.BindResponseAccount(t.Context(), *apiKey.GroupID, responseID, account.ID, time.Hour))
			}
			status, _ := r9PostResponses(t, server, "r9-owner", `{"model":"gpt-5.1","stream":false,"previous_response_id":"resp_r9_ordinary_owner","input":"continue"}`)
			if failing != nil {
				require.Positive(t, failing.lookups.Load(), "fixture must exercise a genuinely cold tenant lookup")
			}
			if !tc.account {
				require.Zero(t, writes.Load(), "unknown ownership must fail before upstream writes")
			} else {
				require.Equal(t, int32(1), writes.Load())
				require.Equal(t, responseID, gjson.GetBytes(<-frames, "previous_response_id").String())
			}
			require.Equal(t, tc.status, status)
		})
	}
}

func TestOpenAIResponsesWebSocket_R9PassthroughCancelPreservesContinuation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	var calls atomic.Int32
	frames := make(chan []byte, 4)
	upstreamClosed := make(chan struct{}, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		for {
			_, body, err := conn.Read(ctx)
			if err != nil {
				upstreamClosed <- struct{}{}
				return
			}
			frames <- body
			if calls.Add(1) == 1 {
				_ = conn.Write(ctx, coderws.MessageText, []byte(`{"type":"response.output_text.delta","response_id":"resp_r9_cancelled","delta":"delivered before cancellation"}`))
				continue
			}
			_ = conn.Write(ctx, coderws.MessageText, []byte(`{"type":"response.completed","response":{"id":"resp_r9_resumed","model":"gpt-5.1","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"resumed"}]}]}}`))
		}
	}))
	defer func() { cancel(); upstream.Close() }()
	account := openAIWSR5Account(9994, "r9-ws-owner", upstream.URL, service.StatusActive, true, true, 1)
	account.Extra["openai_apikey_responses_websockets_v2_mode"] = service.OpenAIWSIngressModePassthrough
	cache := r9EpochCache(t)
	_, svc, apiKey, server, concurrency := newOpenAIWSR5Handler(t, []service.Account{account}, cache)
	defer server.Close()
	const session = "r9-ws-cancel"
	const first = `{"type":"response.create","model":"gpt-5.1","reasoning":{"effort":"high"},"prompt_cache_key":"r9-cancel","store":false,"input":"first"}`
	group := *apiKey.GroupID
	hash := r9SeedEpoch(t, svc, cache, group, account.ID, session, first)
	dial := func() *coderws.Conn {
		conn, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/openai/v1/responses", &coderws.DialOptions{HTTPHeader: http.Header{"session_id": {session}}})
		require.NoError(t, err)
		return conn
	}
	conn := dial()
	defer conn.CloseNow()
	writeOpenAIWSR5(t, conn, first)
	_, partial, err := conn.Read(ctx)
	require.NoError(t, err)
	require.Equal(t, "resp_r9_cancelled", gjson.GetBytes(partial, "response_id").String())
	_ = conn.CloseNow()
	select {
	case <-upstreamClosed:
	case <-ctx.Done():
		t.Fatal("client cancellation did not close the active upstream")
	}
	require.Eventually(t, func() bool { return atomic.LoadInt32(&concurrency.releaseAccountCalled) == 1 }, time.Second, 10*time.Millisecond)
	require.Equal(t, int32(1), calls.Load(), "cancelled committed turn must not replay")
	require.Empty(t, cache.bumps, "cancellation is not degradation")
	require.Equal(t, int64(1), svc.SnapshotOpenAIOpaqueRouteEpoch(t.Context(), group, hash, "gpt-5.1", "high", account.ID).Epoch)
	r9RequireResponseBinding(t, cache, group, "resp_r9_cancelled", account.ID, 1)
	original := <-frames
	// Move only the future-root route to prove a continuation uses its creating epoch.
	_, advanced, err := svc.BumpOpenAIOpaqueRouteEpoch(t.Context(), group, hash, "gpt-5.1", "high", account.ID, 1)
	require.NoError(t, err)
	require.True(t, advanced)
	<-cache.bumps
	reconnected := dial()
	defer reconnected.CloseNow()
	writeOpenAIWSR5(t, reconnected, `{"type":"response.create","model":"gpt-5.1","reasoning":{"effort":"high"},"previous_response_id":"resp_r9_cancelled","prompt_cache_key":"r9-cancel","store":false,"input":"continue"}`)
	_, terminal, err := reconnected.Read(ctx)
	require.NoError(t, err)
	require.Equal(t, "response.completed", gjson.GetBytes(terminal, "type").String())
	continued := <-frames
	require.Equal(t, int32(2), calls.Load())
	require.Equal(t, "resp_r9_cancelled", gjson.GetBytes(continued, "previous_response_id").String())
	require.NotEmpty(t, gjson.GetBytes(original, "prompt_cache_key").String())
	require.Equal(t, gjson.GetBytes(original, "prompt_cache_key").String(), gjson.GetBytes(continued, "prompt_cache_key").String())
	r9RequireResponseBinding(t, cache, group, "resp_r9_resumed", account.ID, 1)
	require.Empty(t, cache.bumps)
}
