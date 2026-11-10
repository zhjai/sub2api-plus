package handler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type postcommitCountingEscapeCache struct {
	service.GatewayCache
	service.OpenAISessionEscapeRouteCache
	calls atomic.Int32
}

func (c *postcommitCountingEscapeCache) AddOpenAISessionEscapedRoute(ctx context.Context, groupID int64, session, model, effort string, accountID int64, multiplier float64, ttl time.Duration) error {
	c.calls.Add(1)
	return c.OpenAISessionEscapeRouteCache.AddOpenAISessionEscapedRoute(ctx, groupID, session, model, effort, accountID, multiplier, ttl)
}

// Observe the actual handler write boundary, before bytes reach the client.
type postcommitTerminalObserver struct {
	http.ResponseWriter
	onTerminal func()
}

func (w *postcommitTerminalObserver) Write(p []byte) (int, error) {
	if strings.Contains(string(p), "event: response.failed") {
		w.onTerminal()
	}
	return w.ResponseWriter.Write(p)
}

func (w *postcommitTerminalObserver) Flush() {
	w.ResponseWriter.(http.Flusher).Flush()
}

func writePostcommitRecovered(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_healthy\",\"object\":\"response\",\"created_at\":1794240000,\"status\":\"in_progress\"}}\n\n")
	const item = "{\"type\":\"message\",\"id\":\"msg_recovered\",\"role\":\"assistant\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"RECOVERED\",\"annotations\":[]}]}"
	fmt.Fprintf(w, "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":%s}\n\n", item)
	fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_healthy\",\"object\":\"response\",\"created_at\":1794240000,\"status\":\"completed\",\"output\":[%s],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n", item)
}

func TestCodexPostcommitFailureRetainsAccountForClientRetry(t *testing.T) {
	require.NoError(t, service.SetOpenAIEvalSchedulingPolicySnapshot(&service.OpenAIEvalConfig{
		EffectsEnabled: true, SchedulingPolicy: service.OpenAIEvalSchedulingPolicyCostFirst,
	}))
	t.Cleanup(func() { _ = service.SetOpenAIEvalSchedulingPolicySnapshot(nil) })
	for _, passthrough := range []bool{false, true} {
		for _, opaque := range []bool{false, true} {
			for _, missingID := range []bool{false, true} {
				t.Run(fmt.Sprintf("passthrough=%t/opaque=%t/missing_id=%t", passthrough, opaque, missingID), func(t *testing.T) {
					var calls, fallbackCalls atomic.Int32
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if calls.Add(1) > 1 {
							writePostcommitRecovered(w)
							return
						}
						w.Header().Set("Content-Type", "text/event-stream")
						if !missingID {
							fmt.Fprint(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_interrupted\",\"status\":\"in_progress\"}}\n\n")
						}
						fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n")
					}))
					defer upstream.Close()
					fallback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						fallbackCalls.Add(1)
						writePostcommitRecovered(w)
					}))
					defer fallback.Close()
					accounts := []service.Account{
						openAIWSR5Account(12001, "preferred", upstream.URL, service.StatusActive, true, false, 1),
						openAIWSR5Account(12002, "fallback", fallback.URL, service.StatusActive, true, false, 2),
					}
					for i := range accounts {
						accounts[i].Extra["openai_passthrough"] = passthrough
						accounts[i].Extra["openai_apikey_responses_websockets_v2_enabled"] = false
					}
					accounts[0].Extra["openai_opaque_upstream"] = opaque
					baseCache := testutil.NewRedisGatewayCache(t)
					cache := &postcommitCountingEscapeCache{GatewayCache: baseCache, OpenAISessionEscapeRouteCache: baseCache.(service.OpenAISessionEscapeRouteCache)}
					_, gateway, apiKey, server, _ := newOpenAIR7HandlerWithRepo(t, accounts, cache, &r7NetworkHTTPUpstream{client: upstream.Client()}, nil)
					defer server.Close()
					var terminalFailureRecorded atomic.Bool
					observedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						server.Config.Handler.ServeHTTP(&postcommitTerminalObserver{ResponseWriter: w, onTerminal: func() {
							selection, decision, err := gateway.SelectAccountWithScheduler(context.Background(), apiKey.GroupID, "", "", "gpt-5.1", nil, service.OpenAIUpstreamTransportAny, false)
							if selection != nil && selection.ReleaseFunc != nil {
								defer selection.ReleaseFunc()
							}
							if err == nil {
								for _, candidate := range decision.Candidates {
									if candidate.AccountID == accounts[0].ID && candidate.Factors != nil && candidate.Factors.ErrorRate.Value != nil && *candidate.Factors.ErrorRate.Value > 0 {
										terminalFailureRecorded.Store(true)
									}
								}
							}
						}}, r)
					}))
					defer observedServer.Close()
					const session = "postcommit-client-retry"
					const body = "{\"model\":\"gpt-5.1\",\"stream\":true,\"input\":\"hello\"}"
					post := func() string {
						req, err := http.NewRequest(http.MethodPost, observedServer.URL+"/openai/v1/responses", strings.NewReader(body))
						require.NoError(t, err)
						req.Header.Set("Content-Type", "application/json")
						req.Header.Set("User-Agent", "codex_cli_rs/0.167.0")
						req.Header.Set("session_id", session)
						resp, err := observedServer.Client().Do(req)
						require.NoError(t, err)
						defer resp.Body.Close()
						data, err := io.ReadAll(resp.Body)
						require.NoError(t, err)
						return string(data)
					}
					first := post()
					require.Contains(t, first, "partial")
					require.Contains(t, first, "event: response.failed")
					require.True(t, terminalFailureRecorded.Load(), "failure accounting must be visible to scheduling before the retryable terminal is written")
					require.NotContains(t, first, "response.incomplete")
					require.Equal(t, int32(1), calls.Load(), "gateway must not replay a committed attempt")
					require.Zero(t, fallbackCalls.Load())
					require.Zero(t, cache.calls.Load(), "transient interruption must not persist negative affinity")
					c, _ := gin.CreateTestContext(httptest.NewRecorder())
					c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
					c.Request.Header.Set("session_id", session)
					hash := gateway.GenerateSessionHash(c, []byte(body))
					escape := gateway.OpenAISessionEscapeRouteStateForRequest(apiKey.GroupID, hash, "gpt-5.1", "")
					require.Empty(t, escape.AccountIDs)
					epoch, err := gateway.PrepareOpenAIOpaqueRouteEpochAttempt(context.Background(), c, *apiKey.GroupID, hash, "gpt-5.1", "", &accounts[0], "")
					require.NoError(t, err)
					require.Zero(t, epoch.Bumps, "a transient failure must not rotate opaque conversation identity")
					second := post()
					require.Contains(t, second, "RECOVERED")
					require.NotContains(t, second, "partial")
					require.Equal(t, int32(2), calls.Load(), "new client request may select the same eligible account")
					require.Zero(t, fallbackCalls.Load())
				})
			}
		}
	}
}

// This opt-in check uses only synthetic localhost upstreams and an isolated
// Codex configuration. Ordinary CI does not require the Codex executable.
func TestCodexPostcommitClientRecovery(t *testing.T) {
	if os.Getenv("SUB2API_CODEX_CLIENT_E2E") != "1" {
		t.Skip("set SUB2API_CODEX_CLIENT_E2E=1 to exercise the installed Codex client")
	}
	cli, err := exec.LookPath("codex")
	require.NoError(t, err)
	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprintf("passthrough=%t", passthrough), func(t *testing.T) {
			var brokenCalls, healthyCalls atomic.Int32
			broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if brokenCalls.Add(1) > 1 {
					writePostcommitRecovered(w)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_broken\",\"object\":\"response\",\"created_at\":1794240000,\"status\":\"in_progress\"}}\n\n")
				fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"item_id\":\"msg_partial\",\"output_index\":0,\"content_index\":0,\"delta\":\"Partial progress.\"}\n")
			}))
			defer broken.Close()
			healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				healthyCalls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_healthy\",\"object\":\"response\",\"created_at\":1794240000,\"status\":\"in_progress\"}}\n\n")
				const item = `{"type":"message","id":"msg_recovered","role":"assistant","status":"completed","content":[{"type":"output_text","text":"RECOVERED","annotations":[]}]}`
				fmt.Fprintf(w, "data: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":%s}\n\n", item)
				fmt.Fprintf(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_healthy\",\"object\":\"response\",\"created_at\":1794240000,\"status\":\"completed\",\"output\":[%s],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n", item)
			}))
			defer healthy.Close()
			accounts := []service.Account{
				openAIWSR5Account(12101, "broken", broken.URL, service.StatusActive, true, false, 1),
				openAIWSR5Account(12102, "healthy", healthy.URL, service.StatusActive, true, false, 2),
			}
			for i := range accounts {
				accounts[i].Extra["openai_passthrough"] = passthrough
				accounts[i].Extra["openai_apikey_responses_websockets_v2_enabled"] = false
			}
			_, _, _, server, _ := newOpenAIR7HandlerWithRepo(t, accounts, testutil.NewRedisGatewayCache(t), &r7NetworkHTTPUpstream{client: broken.Client()}, nil)
			defer server.Close()
			root := t.TempDir()
			configHome := filepath.Join(root, "codex-home")
			require.NoError(t, os.Mkdir(configHome, 0700))
			config := fmt.Sprintf("model_provider = \"fixture\"\nmodel = \"gpt-5.1\"\n[model_providers.fixture]\nname = \"localhost-fixture\"\nbase_url = %q\nwire_api = \"responses\"\nrequires_openai_auth = false\nstream_max_retries = 1\n", server.URL+"/openai/v1")
			require.NoError(t, os.WriteFile(filepath.Join(configHome, "config.toml"), []byte(config), 0600))
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, cli, "exec", "--ephemeral", "--ignore-rules", "--skip-git-repo-check", "-C", root, "-s", "read-only", "--json", "Reply with RECOVERED. Use no tools.")
			for _, entry := range os.Environ() {
				key := strings.ToUpper(strings.SplitN(entry, "=", 2)[0])
				if key == "CODEX_HOME" || strings.Contains(key, "TOKEN") || strings.Contains(key, "API_KEY") || strings.Contains(key, "AUTH") {
					continue
				}
				cmd.Env = append(cmd.Env, entry)
			}
			cmd.Env = append(cmd.Env, "CODEX_HOME="+configHome)
			output, err := cmd.CombinedOutput()
			require.NoError(t, err, "%s", output)
			require.Contains(t, string(output), "RECOVERED")
			require.Equal(t, int32(2), brokenCalls.Load(), "Codex retries the original eligible account in a new request")
			require.Zero(t, healthyCalls.Load(), "one transient failure must not exclude the original account")
		})
	}
}
