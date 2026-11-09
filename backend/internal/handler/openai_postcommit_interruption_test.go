package handler

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/testutil"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type postcommitBlockingEscapeCache struct {
	service.GatewayCache
	service.OpenAISessionEscapeRouteCache
	entered chan struct{}
	release chan struct{}
}

func (c *postcommitBlockingEscapeCache) AddOpenAISessionEscapedRoute(ctx context.Context, groupID int64, session, model, effort string, accountID int64, multiplier float64, ttl time.Duration) error {
	close(c.entered)
	<-c.release
	return c.OpenAISessionEscapeRouteCache.AddOpenAISessionEscapedRoute(ctx, groupID, session, model, effort, accountID, multiplier, ttl)
}

func TestCodexPostcommitFailureEscapesBeforeClientRetry(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(fmt.Sprintf("passthrough=%t", passthrough), func(t *testing.T) {
			var brokenCalls, healthyCalls atomic.Int32
			broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				brokenCalls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_interrupted\",\"status\":\"in_progress\"}}\n\n")
				fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n")
			}))
			defer broken.Close()
			healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				healthyCalls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_healthy\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"recovered\"}]}]}}\n\n")
			}))
			defer healthy.Close()
			accounts := []service.Account{
				openAIWSR5Account(12001, "broken", broken.URL, service.StatusActive, true, false, 1),
				openAIWSR5Account(12002, "healthy", healthy.URL, service.StatusActive, true, false, 2),
			}
			for i := range accounts {
				accounts[i].Extra["openai_passthrough"] = passthrough
				accounts[i].Extra["openai_apikey_responses_websockets_v2_enabled"] = false
			}
			baseCache := testutil.NewRedisGatewayCache(t)
			cache := &postcommitBlockingEscapeCache{GatewayCache: baseCache, OpenAISessionEscapeRouteCache: baseCache.(service.OpenAISessionEscapeRouteCache), entered: make(chan struct{}), release: make(chan struct{})}
			_, gateway, apiKey, server, _ := newOpenAIR7HandlerWithRepo(t, accounts, cache, &r7NetworkHTTPUpstream{client: broken.Client()}, nil)
			defer server.Close()
			var releaseOnce sync.Once
			releaseEscape := func() { releaseOnce.Do(func() { close(cache.release) }) }
			defer releaseEscape()
			const session = "postcommit-client-retry"
			const body = `{"model":"gpt-5.1","stream":true,"input":"hello"}`
			post := func() *http.Response {
				req, err := http.NewRequest(http.MethodPost, server.URL+"/openai/v1/responses", strings.NewReader(body))
				require.NoError(t, err)
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("User-Agent", "codex_cli_rs/0.167.0")
				req.Header.Set("session_id", session)
				resp, err := server.Client().Do(req)
				require.NoError(t, err)
				return resp
			}
			response := post()
			defer response.Body.Close()
			scanner := bufio.NewScanner(response.Body)
			terminal := make(chan string, 1)
			go func() {
				for scanner.Scan() && scanner.Text() != "event: response.failed" {
				}
				terminal <- scanner.Text()
			}()
			select {
			case <-cache.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("escape persistence was not attempted")
			}
			select {
			case line := <-terminal:
				t.Fatalf("terminal released while escape persistence is blocked: %s", line)
			case <-time.After(100 * time.Millisecond):
			}
			releaseEscape()
			select {
			case line := <-terminal:
				require.Equal(t, "event: response.failed", line)
			case <-time.After(5 * time.Second):
				t.Fatal("terminal not released after escape persistence")
			}
			require.NoError(t, scanner.Err())
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
			c.Request.Header.Set("session_id", session)
			hash := gateway.GenerateSessionHash(c, []byte(body))
			escape := gateway.OpenAISessionEscapeRouteStateForRequest(apiKey.GroupID, hash, "gpt-5.1", "")
			require.Contains(t, escape.AccountIDs, accounts[0].ID, "escape must exist before the client sees retry advice")
			require.Equal(t, int32(1), brokenCalls.Load(), "gateway must not replay a committed attempt")
			require.Zero(t, healthyCalls.Load())
			retry := post()
			defer retry.Body.Close()
			output, err := io.ReadAll(retry.Body)
			require.NoError(t, err)
			require.Contains(t, string(output), "recovered")
			require.NotContains(t, string(output), "partial")
			require.Equal(t, int32(1), brokenCalls.Load())
			require.Equal(t, int32(1), healthyCalls.Load())
		})
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
				brokenCalls.Add(1)
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
			require.Equal(t, int32(1), brokenCalls.Load(), "gateway must not retry the committed account")
			require.Equal(t, int32(1), healthyCalls.Load(), "Codex retry must reach the healthy account")
		})
	}
}
