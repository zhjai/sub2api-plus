package handler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIResponses_R10ContinuationFailureRotatesFutureRootOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{"http", "http_passthrough", service.OpenAIWSIngressModeCtxPool, service.OpenAIWSIngressModeHTTPBridge, service.OpenAIWSIngressModePassthrough} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			var calls atomic.Int32
			frames := make(chan []byte, 4)
			makeEvent := func(body []byte) []byte {
				frames <- append([]byte(nil), body...)
				if calls.Add(1) == 2 {
					return []byte(`{"type":"response.created","response":{"id":"resp_r10_bad","model":"gpt-4.1-mini","status":"in_progress"}}`)
				}
				return []byte(fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_r10_%d","model":"gpt-5.1","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"normal"}]}],"usage":{"input_tokens":1,"output_tokens":1}}}`, calls.Load()))
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasPrefix(mode, "ctx_") && mode != service.OpenAIWSIngressModePassthrough {
					body, err := io.ReadAll(r.Body)
					if err != nil {
						return
					}
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprintf(w, "data: %s\n\n", makeEvent(body))
					return
				}
				conn, err := coderws.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer conn.CloseNow()
				for {
					_, body, err := conn.Read(ctx)
					if err != nil {
						return
					}
					if err := conn.Write(ctx, coderws.MessageText, makeEvent(body)); err != nil {
						return
					}
				}
			}))
			defer func() { cancel(); upstream.Close() }()
			account := openAIWSR5Account(10010, "r10-continuation", upstream.URL, service.StatusActive, true, true, 1)
			httpMode := strings.HasPrefix(mode, "http") && mode != service.OpenAIWSIngressModeHTTPBridge
			if httpMode {
				account.Extra["openai_apikey_responses_websockets_v2_enabled"] = false
				account.Extra["openai_passthrough"] = mode == "http_passthrough"
			} else {
				account.Extra["openai_apikey_responses_websockets_v2_mode"] = mode
			}
			cache := r9EpochCache(t)
			_, svc, apiKey, server, _ := newOpenAIR5Handler(t, []service.Account{account}, cache, &r7NetworkHTTPUpstream{client: upstream.Client()})
			defer server.Close()
			const session = "r10-continuation"
			const first = `{"model":"gpt-5.1","reasoning":{"effort":"high"},"stream":true,"store":false,"prompt_cache_key":"r10-continuation","input":"first"}`
			const next = `{"model":"gpt-5.1","reasoning":{"effort":"high"},"stream":true,"store":false,"prompt_cache_key":"r10-continuation","previous_response_id":"resp_r10_1","input":"continuation"}`
			group := *apiKey.GroupID
			hash := r9SeedEpoch(t, svc, cache, group, account.ID, session, first)
			request := func(body string, expectFailure bool) {
				if httpMode {
					status, output := r9PostResponses(t, server, session, body)
					if expectFailure {
						require.Equal(t, http.StatusBadGateway, status, "failure response: %s", output)
						require.NotContains(t, string(output), "resp_r10_bad")
					} else {
						require.Equal(t, 200, status)
					}
					return
				}
				conn, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/openai/v1/responses", &coderws.DialOptions{HTTPHeader: http.Header{"session_id": {session}}})
				require.NoError(t, err)
				defer conn.CloseNow()
				payload := `{"type":"response.create",` + strings.TrimPrefix(body, "{")
				writeOpenAIWSR5(t, conn, payload)
				_, output, err := conn.Read(ctx)
				if expectFailure {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
					require.Equal(t, "response.completed", gjson.GetBytes(output, "type").String())
					r9RequireResponseBinding(t, cache, group, gjson.GetBytes(output, "response.id").String(), account.ID, svc.SnapshotOpenAIOpaqueRouteEpoch(ctx, group, hash, "gpt-5.1", "high", account.ID).Epoch)
				}
			}
			request(first, false)
			r9RequireResponseBinding(t, cache, group, "resp_r10_1", account.ID, 1)
			for epoch := int64(1); epoch < 2; epoch++ {
				_, advanced, err := svc.BumpOpenAIOpaqueRouteEpoch(ctx, group, hash, "gpt-5.1", "high", account.ID, epoch)
				require.NoError(t, err)
				require.True(t, advanced)
				<-cache.bumps
			}
			request(next, true)
			select {
			case bump := <-cache.bumps:
				require.Equal(t, r6EpochBump{model: "gpt-5.1", effort: "high", expected: 2}, bump)
			case <-ctx.Done():
				t.Fatal("continuation failure did not update future-root route")
			}
			require.Equal(t, int64(3), svc.SnapshotOpenAIOpaqueRouteEpoch(ctx, group, hash, "gpt-5.1", "high", account.ID).Epoch)
			r9RequireResponseBinding(t, cache, group, "resp_r10_1", account.ID, 1)
			require.Equal(t, int32(2), calls.Load(), "pinned continuation must not be retried")
			firstBody, failedBody := <-frames, <-frames
			require.Equal(t, gjson.GetBytes(firstBody, "prompt_cache_key").String(), gjson.GetBytes(failedBody, "prompt_cache_key").String(), "continuation retains creating identity")
			request(first, false)
			rootBody := <-frames
			require.NotEqual(t, gjson.GetBytes(firstBody, "prompt_cache_key").String(), gjson.GetBytes(rootBody, "prompt_cache_key").String())
			require.Equal(t, int32(3), calls.Load())
		})
	}
}
