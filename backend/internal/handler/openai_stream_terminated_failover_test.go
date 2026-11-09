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

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIResponsesStreamTerminatedRetriesBeforeSwitching(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, passthrough := range []bool{false, true} {
		for _, retryCount := range []int{0, 1, 3} {
			for _, eof := range []bool{false, true} {
				for _, recoverOnSecond := range []bool{false, true} {
					if recoverOnSecond && retryCount == 0 {
						continue
					}
					t.Run(fmt.Sprintf("passthrough=%t/retries=%d/eof=%t/recover=%t", passthrough, retryCount, eof, recoverOnSecond), func(t *testing.T) {
						var brokenCalls, healthyCalls atomic.Int32
						heartbeat := passthrough && retryCount == 1 && eof && !recoverOnSecond
						broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							call := brokenCalls.Add(1)
							w.Header().Set("Content-Type", "text/event-stream")
							if recoverOnSecond && call > 1 {
								fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_recovered\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"recovered answer\"}]}],\"usage\":{\"input_tokens\":7,\"output_tokens\":2}}}\n\n")
								return
							}
							if eof {
								fmt.Fprint(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_broken\"}}\n\n")
								if heartbeat {
									w.(http.Flusher).Flush()
									time.Sleep(1200 * time.Millisecond)
								}
								return
							}
							fmt.Fprint(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_broken\"}}\n\ndata: {\"type\":\"response.incomplete\",\"response\":{\"id\":\"resp_broken\",\"status\":\"incomplete\",\"output\":[],\"incomplete_details\":{\"reason\":\"stream_terminated\"},\"usage\":{\"input_tokens\":7,\"output_tokens\":0}}}\n\n")
						}))
						t.Cleanup(broken.Close)
						healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							healthyCalls.Add(1)
							w.Header().Set("Content-Type", "text/event-stream")
							fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_healthy\",\"model\":\"gpt-5.1\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"healthy answer\"}]}],\"usage\":{\"input_tokens\":7,\"output_tokens\":2}}}\n\n")
						}))
						t.Cleanup(healthy.Close)
						accounts := []service.Account{
							openAIWSR5Account(11001, "broken", broken.URL, service.StatusActive, true, false, 1),
							openAIWSR5Account(11002, "healthy", healthy.URL, service.StatusActive, true, false, 2),
						}
						for i := range accounts {
							accounts[i].Credentials["pool_mode"] = true
							accounts[i].Credentials["pool_mode_retry_count"] = retryCount
							accounts[i].Extra["openai_passthrough"] = passthrough
							accounts[i].Extra["openai_apikey_responses_websockets_v2_enabled"] = false
						}
						cache := &streamRetryRedirectCache{GatewayCache: testutil.NewRedisGatewayCache(t), calls: &brokenCalls, alternateID: accounts[1].ID}
						_, gateway, apiKey, server, _ := newOpenAIR7HandlerWithRepo(t, accounts, cache, &r7NetworkHTTPUpstream{client: broken.Client()}, nil, func(cfg *config.Config) {
							if heartbeat {
								cfg.Gateway.StreamKeepaliveInterval = 1
							}
						})
						t.Cleanup(server.Close)
						status, output := r9PostResponses(t, server, "stream-terminated-first-failure", `{"model":"gpt-5.1","stream":true,"input":"hello"}`)
						require.Equal(t, http.StatusOK, status)
						if recoverOnSecond {
							require.Contains(t, string(output), "recovered answer")
							require.Equal(t, int32(2), brokenCalls.Load())
							require.Zero(t, healthyCalls.Load(), "a recovered account must not be abandoned")
						} else {
							require.Contains(t, string(output), "healthy answer")
						}
						if heartbeat {
							require.GreaterOrEqual(t, strings.Count(string(output), ": keepalive\n\n"), 2, "both failed attempts must deliver a nonsemantic heartbeat before switching")
						}
						require.NotContains(t, string(output), "resp_broken")
						require.NotContains(t, string(output), "stream_terminated")
						if !recoverOnSecond {
							require.Equal(t, int32(1+retryCount), brokenCalls.Load(), "configured same-account retries must finish before switching")
							require.Equal(t, int32(1), healthyCalls.Load())
						}
						c, _ := gin.CreateTestContext(httptest.NewRecorder())
						c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
						c.Request.Header.Set("session_id", "stream-terminated-first-failure")
						sessionHash := gateway.GenerateSessionHash(c, []byte(`{"model":"gpt-5.1","stream":true,"input":"hello"}`))
						escape := gateway.OpenAISessionEscapeRouteStateForRequest(apiKey.GroupID, sessionHash, "gpt-5.1", "")
						if recoverOnSecond {
							require.Empty(t, escape.AccountIDs, "successful retry must not leave an escape marker")
						} else {
							require.Contains(t, escape.AccountIDs, accounts[0].ID)
						}
					})
				}
			}
		}
	}
}

func TestOpenAIResponsesStreamIntervalRetriesBeforeSwitching(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, recoverOnSecond := range []bool{false, true} {
		t.Run(fmt.Sprintf("recover=%t", recoverOnSecond), func(t *testing.T) {
			var brokenCalls, healthyCalls atomic.Int32
			broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				call := brokenCalls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				if recoverOnSecond && call > 1 {
					fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_recovered\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"recovered answer\"}]}]}}\n\n")
					return
				}
				fmt.Fprint(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_broken\"}}\n\n")
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			t.Cleanup(broken.Close)
			healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				healthyCalls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_healthy\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"healthy answer\"}]}]}}\n\n")
			}))
			t.Cleanup(healthy.Close)
			accounts := []service.Account{
				openAIWSR5Account(11101, "broken", broken.URL, service.StatusActive, true, false, 1),
				openAIWSR5Account(11102, "healthy", healthy.URL, service.StatusActive, true, false, 2),
			}
			for i := range accounts {
				accounts[i].Credentials["pool_mode"] = true
				accounts[i].Credentials["pool_mode_retry_count"] = 3
				accounts[i].Extra["openai_apikey_responses_websockets_v2_enabled"] = false
			}
			cache := testutil.NewRedisGatewayCache(t)
			_, _, _, server, _ := newOpenAIR7HandlerWithRepo(t, accounts, cache, &r7NetworkHTTPUpstream{client: broken.Client()}, nil, func(cfg *config.Config) {
				cfg.Gateway.StreamDataIntervalTimeout = 1
			})
			t.Cleanup(server.Close)
			client := &http.Client{Timeout: 15 * time.Second}
			req, err := http.NewRequest(http.MethodPost, server.URL+"/openai/v1/responses", strings.NewReader("{\"model\":\"gpt-5.1\",\"stream\":true,\"input\":\"hello\"}"))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("session_id", "stream-interval-retry")
			resp, err := client.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()
			output, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			status := resp.StatusCode
			require.Equal(t, http.StatusOK, status)
			require.NotContains(t, string(output), "resp_broken")
			if recoverOnSecond {
				require.Contains(t, string(output), "recovered answer")
				require.Equal(t, int32(2), brokenCalls.Load())
				require.Zero(t, healthyCalls.Load())
			} else {
				require.Contains(t, string(output), "healthy answer")
				require.Equal(t, int32(4), brokenCalls.Load())
				require.Equal(t, int32(1), healthyCalls.Load())
			}
		})
	}
}

// A changed sticky preference must not steal a retry already assigned to A.
type streamRetryRedirectCache struct {
	service.GatewayCache
	calls       *atomic.Int32
	alternateID int64
}

func (c *streamRetryRedirectCache) GetSessionAccountID(ctx context.Context, groupID int64, hash string) (int64, error) {
	if c.calls.Load() > 0 {
		return c.alternateID, nil
	}
	return c.GatewayCache.GetSessionAccountID(ctx, groupID, hash)
}
