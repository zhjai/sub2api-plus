package handler

import (
	"context"
	"crypto/sha256"
	"encoding/json"
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

var r11WSModes = []string{service.OpenAIWSIngressModeCtxPool, service.OpenAIWSIngressModeHTTPBridge, service.OpenAIWSIngressModePassthrough}

const r11ExecRoot = `{"type":"response.create","model":"gpt-5.1","reasoning":{"effort":"high"},"store":false,"prompt_cache_key":"r11-cache","tools":[{"type":"custom","name":"exec"}],"input":"run pwd"}`
const r11ExecItem = `{"type":"custom_tool_call","id":"ct_r11","name":"exec","call_id":"call_r11","input":"pwd"}`
const r11Keepalive = `{"type":"keepalive"}`

type r11WireUpstream struct {
	calls    atomic.Int32
	requests chan []byte
	server   *httptest.Server
}

// The script returns false to close the upstream, or true to accept another WS turn.
func newR11WireUpstream(t *testing.T, ctx context.Context, mode string, script func(context.Context, int32, func(string) error) bool) *r11WireUpstream {
	t.Helper()
	u := &r11WireUpstream{requests: make(chan []byte, 8)}
	u.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		run := func(body []byte, send func(string) error) bool {
			call := u.calls.Add(1)
			select {
			case u.requests <- append([]byte(nil), body...):
			case <-ctx.Done():
				return false
			}
			return script(ctx, call, send)
		}
		if mode == service.OpenAIWSIngressModeHTTPBridge {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			run(body, func(event string) error {
				_, err := fmt.Fprintf(w, "data: %s\n\n", event)
				w.(http.Flusher).Flush()
				return err
			})
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
			if !run(body, func(event string) error { return conn.Write(ctx, coderws.MessageText, []byte(event)) }) {
				return
			}
		}
	}))
	t.Cleanup(u.server.Close)
	return u
}

type r11WireGateway struct {
	svc     *service.OpenAIGatewayService
	cache   *r6EpochCache
	apiKey  *service.APIKey
	server  *httptest.Server
	done    chan struct{}
	hash    string
	session string
}

func newR11WireGateway(t *testing.T, accounts []service.Account, body string) *r11WireGateway {
	t.Helper()
	g := &r11WireGateway{cache: r9EpochCache(t), done: make(chan struct{}), session: "r11-wire"}
	_, svc, apiKey, fixture, _ := newOpenAIR5Handler(t, accounts, g.cache, &r7NetworkHTTPUpstream{client: &http.Client{}})
	g.svc, g.apiKey = svc, apiKey
	realHandler := fixture.Config.Handler
	fixture.Close()
	// Observe completion outside the router, retaining all production turn hooks.
	g.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(g.done)
		realHandler.ServeHTTP(w, r)
	}))
	t.Cleanup(g.server.Close)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/openai/v1/responses", nil)
	c.Request.Header.Set("session_id", g.session)
	g.hash = svc.GenerateSessionHash(c, []byte(body))
	return g
}

func (g *r11WireGateway) dial(t *testing.T, ctx context.Context, body string) *coderws.Conn {
	t.Helper()
	conn, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(g.server.URL, "http")+"/openai/v1/responses", &coderws.DialOptions{HTTPHeader: http.Header{"session_id": {g.session}}})
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.CloseNow() })
	require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(body)))
	return conn
}

func r11Await(t *testing.T, ctx context.Context, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-ctx.Done():
		t.Fatalf("waiting for %s: %v", description, ctx.Err())
	}
}

func r11Gate(ctx context.Context, signal <-chan struct{}) bool {
	select {
	case <-signal:
		return true
	case <-ctx.Done():
		return false
	}
}

func r11Send(send func(string) error, events ...string) bool {
	for _, event := range events {
		if err := send(event); err != nil {
			return false
		}
	}
	return true
}

func r11Created(id string) string {
	return fmt.Sprintf(`{"type":"response.created","response":{"id":%q,"model":"gpt-5.1","status":"in_progress"}}`, id)
}

func r11Delta(id, text string) string {
	return fmt.Sprintf(`{"type":"response.output_text.delta","response_id":%q,"delta":%q}`, id, text)
}

func r11Completed(id, output string) string {
	return fmt.Sprintf(`{"type":"response.completed","response":{"id":%q,"model":"gpt-5.1","status":"completed","output":%s,"usage":{"input_tokens":1,"output_tokens":1}}}`, id, output)
}

func r11Prefix(id string) []string {
	return []string{r11Created(id), r11Delta(id, "to=functions."), r11Delta(id, "exec code:\n")}
}

func r11Healthy(id string, exec bool) []string {
	if exec {
		return []string{r11Created(id), fmt.Sprintf(`{"type":"response.output_item.added","response_id":%q,"output_index":0,"item":%s}`, id, r11ExecItem), r11Completed(id, "["+r11ExecItem+"]")}
	}
	return []string{r11Created(id), r11Delta(id, "healthy ordinary output"), r11Completed(id, `[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"healthy ordinary output"}]}]`)}
}

func r11RequireFrames(t *testing.T, ctx context.Context, conn *coderws.Conn, events []string) {
	t.Helper()
	for i, expected := range events {
		kind, payload, err := conn.Read(ctx)
		require.NoError(t, err, "client frame %d, expected %s", i, expected)
		require.Equal(t, coderws.MessageText, kind)
		var actualFields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(payload, &actualFields))
		// The HTTP custom-tool bridge fills missing sequence numbers.
		if sequence, exists := actualFields["sequence_number"]; exists && !gjson.Get(expected, "sequence_number").Exists() {
			var number int64
			require.NoError(t, json.Unmarshal(sequence, &number))
			require.GreaterOrEqual(t, number, int64(0))
			delete(actualFields, "sequence_number")
		}
		actual, err := json.Marshal(actualFields)
		require.NoError(t, err)
		require.JSONEq(t, expected, string(actual), "client frame %d; no rejected-attempt frame may be delivered", i)
	}
}

func r11RequireRequest(t *testing.T, ctx context.Context, u *r11WireUpstream, exec bool, cacheKey string) {
	t.Helper()
	var body []byte
	select {
	case body = <-u.requests:
	case <-ctx.Done():
		t.Fatalf("missing upstream request: %v", ctx.Err())
	}
	require.True(t, gjson.ValidBytes(body))
	require.Equal(t, "gpt-5.1", gjson.GetBytes(body, "model").String())
	require.Equal(t, "high", gjson.GetBytes(body, "reasoning.effort").String())
	require.Equal(t, "run pwd", gjson.GetBytes(body, "input").String())
	require.False(t, gjson.GetBytes(body, "previous_response_id").Exists(), "initial root must stay a root on every attempt")
	require.Equal(t, exec, gjson.GetBytes(body, `tools.#(name=="exec").name`).String() == "exec", "verify the final outbound exec contract")
	require.Equal(t, cacheKey, gjson.GetBytes(body, "prompt_cache_key").String(), "exact outbound route identity")
}

func r11RequireRoute(t *testing.T, ctx context.Context, g *r11WireGateway, accountID, epoch int64, bumps int) {
	t.Helper()
	state := g.svc.SnapshotOpenAIOpaqueRouteEpoch(ctx, *g.apiKey.GroupID, g.hash, "gpt-5.1", "high", accountID)
	require.Equal(t, epoch, state.Epoch)
	require.Equal(t, bumps, state.Bumps)
}

func TestOpenAIResponsesWebSocket_R11InitialRootOpaqueExecLeakRetriesBeforeCommit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range r11WSModes {
		for _, healthyExec := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/healthy_exec=%t", mode, healthyExec), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
				retried := make(chan struct{})
				healthy := r11Healthy("resp_r11_healthy", healthyExec)
				upstream := newR11WireUpstream(t, ctx, mode, func(ctx context.Context, call int32, send func(string) error) bool {
					if call == 1 {
						if r11Send(send, append(r11Prefix("resp_r11_bad"), r11Delta("resp_r11_bad", `{"cmd":"pwd"}`))...) {
							// Failover must happen while this attempt has neither terminated nor closed.
							r11Gate(ctx, retried)
						}
						return false
					}
					if call == 2 {
						close(retried)
					}
					return r11Send(send, healthy...)
				})
				t.Cleanup(cancel)
				account := openAIWSR5Account(10011, "r11-opaque", upstream.server.URL, service.StatusActive, true, true, 1)
				account.Extra["openai_apikey_responses_websockets_v2_mode"] = mode
				g := newR11WireGateway(t, []service.Account{account}, r11ExecRoot)
				conn := g.dial(t, ctx, r11ExecRoot)
				r11RequireFrames(t, ctx, conn, healthy)
				r9RequireResponseBinding(t, g.cache, *g.apiKey.GroupID, "resp_r11_healthy", account.ID, 1)
				_ = conn.CloseNow()
				r11Await(t, ctx, g.done, "production handler completion")
				require.Equal(t, int32(2), upstream.calls.Load(), "one rejected attempt and one healthy self-retry")
				r11RequireRequest(t, ctx, upstream, true, "r11-cache")
				sum := sha256.Sum256([]byte("sub2api:route-epoch:v1|10011|1|prompt-cache|r11-cache"))
				r11RequireRequest(t, ctx, upstream, true, fmt.Sprintf("r%x", sum[:12]))
				require.Len(t, g.cache.bumps, 1, "one route bump for the rejected attempt; none for healthy completion or disconnect")
				require.Equal(t, r6EpochBump{model: "gpt-5.1", effort: "high", expected: 0}, <-g.cache.bumps)
				r11RequireRoute(t, ctx, g, account.ID, 1, 1)
				store := service.NewOpenAIWSStateStore(g.cache)
				owner, err := store.GetResponseAccount(ctx, *g.apiKey.GroupID, "resp_r11_bad")
				require.NoError(t, err)
				require.Zero(t, owner, "rejected lifecycle metadata must not acquire a response owner")
				_, found, err := store.GetResponseRouteEpoch(ctx, *g.apiKey.GroupID, "resp_r11_bad", account.ID)
				require.NoError(t, err)
				require.False(t, found)
			})
		}
	}
}

func TestOpenAIResponsesWebSocket_R11InitialRootOrdinaryAccountExecLeakFailsOver(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range r11WSModes {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
			retried := make(chan struct{})
			bad := newR11WireUpstream(t, ctx, mode, func(ctx context.Context, _ int32, send func(string) error) bool {
				if r11Send(send, append(r11Prefix("resp_r11_bad"), r11Delta("resp_r11_bad", `{"cmd":"pwd"}`))...) {
					r11Gate(ctx, retried)
				}
				return false
			})
			healthy := r11Healthy("resp_r11_fallback", true)
			good := newR11WireUpstream(t, ctx, mode, func(_ context.Context, call int32, send func(string) error) bool {
				if call == 1 {
					close(retried)
				}
				return r11Send(send, healthy...)
			})
			t.Cleanup(cancel)
			accounts := []service.Account{
				openAIWSR5Account(10012, "r11-bad", bad.server.URL, service.StatusActive, true, false, 1),
				openAIWSR5Account(10013, "r11-good", good.server.URL, service.StatusActive, true, false, 2),
			}
			for i := range accounts {
				accounts[i].Extra["openai_apikey_responses_websockets_v2_mode"] = mode
			}
			g := newR11WireGateway(t, accounts, r11ExecRoot)
			conn := g.dial(t, ctx, r11ExecRoot)
			r11RequireFrames(t, ctx, conn, healthy)
			_ = conn.CloseNow()
			r11Await(t, ctx, g.done, "production handler completion")
			require.Equal(t, int32(1), bad.calls.Load())
			require.Equal(t, int32(1), good.calls.Load())
			r11RequireRequest(t, ctx, bad, true, "r11-cache")
			r11RequireRequest(t, ctx, good, true, "r11-cache")
			require.Empty(t, g.cache.bumps, "ordinary-account failover must not rotate opaque routes")
			store := service.NewOpenAIWSStateStore(g.cache)
			owner, err := store.GetResponseAccount(ctx, *g.apiKey.GroupID, "resp_r11_fallback")
			require.NoError(t, err)
			require.Equal(t, accounts[1].ID, owner)
			owner, err = store.GetResponseAccount(ctx, *g.apiKey.GroupID, "resp_r11_bad")
			require.NoError(t, err)
			require.Zero(t, owner)
		})
	}
}

func TestOpenAIResponsesWebSocket_R11OrdinaryOutputArrivesBeforeTerminal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range r11WSModes {
		for _, declared := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/exec_declared=%t", mode, declared), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
				body := r11ExecRoot
				events := []string{r11Created("resp_r11_prose"), r11Delta("resp_r11_prose", "ordinary prose is available now")}
				if !declared {
					var request map[string]any
					require.NoError(t, json.Unmarshal([]byte(body), &request))
					delete(request, "tools")
					payload, err := json.Marshal(request)
					require.NoError(t, err)
					body = string(payload)
					// Without an exec contract even this syntax is ordinary output text.
					events = append(r11Prefix("resp_r11_prose"), r11Delta("resp_r11_prose", `{"cmd":"pwd"}`))
				}
				paused, release := make(chan struct{}), make(chan struct{})
				terminal := r11Completed("resp_r11_prose", `[]`)
				upstream := newR11WireUpstream(t, ctx, mode, func(ctx context.Context, call int32, send func(string) error) bool {
					if !r11Send(send, events...) {
						return false
					}
					if call == 1 {
						close(paused)
					}
					return r11Gate(ctx, release) && r11Send(send, terminal)
				})
				t.Cleanup(cancel)
				account := openAIWSR5Account(10014, "r11-prose", upstream.server.URL, service.StatusActive, true, true, 1)
				account.Extra["openai_apikey_responses_websockets_v2_mode"] = mode
				g := newR11WireGateway(t, []service.Account{account}, body)
				conn := g.dial(t, ctx, body)
				r11Await(t, ctx, paused, "upstream waiting before terminal")
				r11RequireFrames(t, ctx, conn, events)
				close(release)
				r11RequireFrames(t, ctx, conn, []string{terminal})
				_ = conn.CloseNow()
				r11Await(t, ctx, g.done, "production handler completion")
				require.Equal(t, int32(1), upstream.calls.Load())
				r11RequireRequest(t, ctx, upstream, declared, "r11-cache")
				require.Empty(t, g.cache.bumps, "ordinary output must not be classified as a capability failure")
				r11RequireRoute(t, ctx, g, account.ID, 0, 0)
			})
		}
	}
}

func TestOpenAIResponsesWebSocket_R11PartialPrefixRealExecReleasesPendingFrames(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range r11WSModes {
		for _, terminalOnly := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/terminal_only=%t", mode, terminalOnly), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
				prefix := r11Prefix("resp_r11_exec")
				allowExec, allowTerminal := make(chan struct{}), make(chan struct{})
				item := fmt.Sprintf(`{"type":"response.output_item.added","response_id":"resp_r11_exec","output_index":0,"item":%s}`, r11ExecItem)
				terminal := r11Completed("resp_r11_exec", "["+r11ExecItem+"]")
				upstream := newR11WireUpstream(t, ctx, mode, func(ctx context.Context, _ int32, send func(string) error) bool {
					if !r11Send(send, append(append([]string(nil), prefix...), r11Keepalive)...) || !r11Gate(ctx, allowExec) {
						return false
					}
					if !terminalOnly && (!r11Send(send, item) || !r11Gate(ctx, allowTerminal)) {
						return false
					}
					return r11Send(send, terminal)
				})
				t.Cleanup(cancel)
				account := openAIWSR5Account(10015, "r11-real-exec", upstream.server.URL, service.StatusActive, true, true, 1)
				account.Extra["openai_apikey_responses_websockets_v2_mode"] = mode
				g := newR11WireGateway(t, []service.Account{account}, r11ExecRoot)
				conn := g.dial(t, ctx, r11ExecRoot)
				// The keepalive passes through only after the earlier prefix frames were processed.
				r11RequireFrames(t, ctx, conn, []string{r11Keepalive})
				close(allowExec)
				r11RequireFrames(t, ctx, conn, prefix)
				if !terminalOnly {
					r11RequireFrames(t, ctx, conn, []string{item})
					close(allowTerminal)
				}
				r11RequireFrames(t, ctx, conn, []string{terminal})
				_ = conn.CloseNow()
				r11Await(t, ctx, g.done, "production handler completion")
				require.Equal(t, int32(1), upstream.calls.Load())
				r11RequireRequest(t, ctx, upstream, true, "r11-cache")
				require.Empty(t, g.cache.bumps, "genuine exec must not count as a capability failure")
				r11RequireRoute(t, ctx, g, account.ID, 0, 0)
				r9RequireResponseBinding(t, g.cache, *g.apiKey.GroupID, "resp_r11_exec", account.ID, 0)
			})
		}
	}
}

func TestOpenAIResponsesWebSocket_R11CancellationWithStagedPrefixDoesNotBumpRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range r11WSModes {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
			release := make(chan struct{})
			upstream := newR11WireUpstream(t, ctx, mode, func(ctx context.Context, _ int32, send func(string) error) bool {
				if r11Send(send, append(r11Prefix("resp_r11_cancel"), r11Keepalive)...) {
					r11Gate(ctx, release)
				}
				return false
			})
			t.Cleanup(cancel)
			account := openAIWSR5Account(10016, "r11-cancel", upstream.server.URL, service.StatusActive, true, true, 1)
			account.Extra["openai_apikey_responses_websockets_v2_mode"] = mode
			g := newR11WireGateway(t, []service.Account{account}, r11ExecRoot)
			conn := g.dial(t, ctx, r11ExecRoot)
			r11RequireFrames(t, ctx, conn, []string{r11Keepalive})
			_ = conn.CloseNow()
			close(release)
			r11Await(t, ctx, g.done, "cancelled production handler completion")
			require.Equal(t, int32(1), upstream.calls.Load(), "client cancellation must not retry a staged turn")
			r11RequireRequest(t, ctx, upstream, true, "r11-cache")
			require.Empty(t, g.cache.bumps, "cancellation must not count as a capability failure")
			r11RequireRoute(t, ctx, g, account.ID, 0, 0)
			store := service.NewOpenAIWSStateStore(g.cache)
			owner, err := store.GetResponseAccount(ctx, *g.apiKey.GroupID, "resp_r11_cancel")
			require.NoError(t, err)
			require.Zero(t, owner)
		})
	}
}
