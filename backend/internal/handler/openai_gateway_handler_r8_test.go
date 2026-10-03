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
	"github.com/Wei-Shaw/sub2api/internal/testutil"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIResponsesWebSocket_R8IntegrityModeMatrix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{service.OpenAIWSIngressModeCtxPool, service.OpenAIWSIngressModeHTTPBridge, service.OpenAIWSIngressModePassthrough} {
		for _, kind := range []string{"tool_denial", "partial_eof", "stream_terminated", "model_mismatch", "precommit_mismatch", "max_output_tokens", "real_exec", "ordinary_refusal", "client_cancel",
			"late_leak", "late_leak_real_exec", "early_exec_leak", "done_completed_denial", "done_incomplete", "done_failed", "done_cancelled", "done_canceled", "done_terminated", "done_literal_terminated"} {
			t.Run(mode+"/"+kind, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
				defer cancel()
				var calls atomic.Int32
				frames := make(chan []byte, 8)
				cancelData := make(chan struct{})
				makeEvents := func(body []byte) [][]byte {
					call := calls.Add(1)
					frames <- append([]byte(nil), body...)
					if call != 2 {
						return [][]byte{[]byte(fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_r8_%d","model":%q,"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"normal"}]}]}}`, call, gjson.GetBytes(body, "model").String()))}
					}
					text := "semantic output"
					if kind == "precommit_mismatch" {
						return [][]byte{[]byte(`{"type":"response.created","response":{"id":"resp_r8_2","model":"gpt-4.1-mini","status":"in_progress"}}`)}
					}
					if kind == "early_exec_leak" {
						events := [][]byte{[]byte(`{"type":"response.created","response":{"id":"resp_r8_2","model":"gpt-5.2","status":"in_progress"}}`)}
						for _, fragment := range []string{"to=functions.", "exec code:\n", `{"cmd":"pwd"}`} {
							events = append(events, []byte(fmt.Sprintf(`{"type":"response.output_text.delta","response_id":"resp_r8_2","delta":%q}`, fragment)))
						}
						return events
					}
					if kind == "tool_denial" || kind == "real_exec" || strings.HasPrefix(kind, "done_") {
						text = "当前会话没有可用的终端工具"
					}
					if kind == "ordinary_refusal" {
						text = "I cannot assist with that request."
					}
					events := [][]byte{[]byte(fmt.Sprintf(`{"type":"response.output_text.delta","response_id":"resp_r8_2","delta":%q}`, text))}
					if strings.HasPrefix(kind, "late_leak") {
						for _, fragment := range []string{"\nto=functions.", "exec code:\n", `{"cmd":"pwd"}`} {
							events = append(events, []byte(fmt.Sprintf(`{"type":"response.output_text.delta","response_id":"resp_r8_2","delta":%q}`, fragment)))
						}
					}
					if kind == "partial_eof" || kind == "client_cancel" {
						return events
					}
					terminal, status, reason := "response.completed", "completed", ""
					if kind == "stream_terminated" || kind == "max_output_tokens" {
						terminal, status, reason = "response.incomplete", "incomplete", kind
					}
					if strings.HasPrefix(kind, "done_") {
						terminal = "response.done"
						switch kind {
						case "done_incomplete":
							status, reason = "incomplete", "max_output_tokens"
						case "done_terminated":
							status, reason = "incomplete", "stream_terminated"
						case "done_failed":
							status = "failed"
						case "done_literal_terminated":
							status = "terminated"
						case "done_cancelled":
							status = "cancelled"
						case "done_canceled":
							status = "canceled"
						}
					}
					model := "gpt-5.2"
					if kind == "model_mismatch" {
						model = "gpt-4.1-mini"
					}
					output := `[]`
					if kind == "real_exec" || kind == "late_leak_real_exec" {
						output = `[{"type":"custom_tool_call","name":"exec","call_id":"call_r8","input":"pwd"}]`
					}
					return append(events, []byte(fmt.Sprintf(`{"type":%q,"response":{"id":"resp_r8_2","model":%q,"status":%q,"output":%s,"incomplete_details":{"reason":%q}}}`, terminal, model, status, output, reason)))
				}
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if mode == service.OpenAIWSIngressModeHTTPBridge {
						body, err := io.ReadAll(r.Body)
						if err != nil {
							return
						}
						w.Header().Set("Content-Type", "text/event-stream")
						for _, event := range makeEvents(body) {
							_, _ = fmt.Fprintf(w, "data: %s\n\n", event)
							w.(http.Flusher).Flush()
						}
						if kind == "client_cancel" && calls.Load() == 2 {
							select {
							case <-cancelData:
							case <-ctx.Done():
								return
							}
							_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"response_id\":\"resp_r8_2\",\"delta\":\"after cancellation\"}\n\n")
							w.(http.Flusher).Flush()
						}
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
						for _, event := range makeEvents(body) {
							if err := conn.Write(ctx, coderws.MessageText, event); err != nil {
								return
							}
						}
						if kind == "partial_eof" && calls.Load() == 2 {
							return
						}
						if kind == "client_cancel" && calls.Load() == 2 {
							select {
							case <-cancelData:
							case <-ctx.Done():
								return
							}
							_ = conn.Write(ctx, coderws.MessageText, []byte(`{"type":"response.output_text.delta","response_id":"resp_r8_2","delta":"after cancellation"}`))
						}
					}
				}))
				defer func() { cancel(); upstream.Close() }()
				account := openAIWSR5Account(9988, "r8-opaque", upstream.URL, service.StatusActive, true, true, 1)
				account.Extra["openai_apikey_responses_websockets_v2_mode"] = mode
				base := testutil.NewRedisGatewayCache(t)
				cache := &r6EpochCache{GatewayCache: base, epochs: base.(service.OpenAIOpaqueRouteEpochCache), bumps: make(chan r6EpochBump, 8)}
				_, svc, apiKey, server, _ := newOpenAIR5Handler(t, []service.Account{account}, cache, &r7NetworkHTTPUpstream{client: upstream.Client()})
				defer server.Close()
				const session = "r8-integrity"
				first := `{"type":"response.create","model":"gpt-5.1","reasoning":{"effort":"high"},"prompt_cache_key":"r8-cache","store":false,"input":"first"}`
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodGet, "/openai/v1/responses", nil)
				c.Request.Header.Set("session_id", session)
				hash := svc.GenerateSessionHash(c, []byte(first))
				group := *apiKey.GroupID
				for _, route := range []struct {
					model, effort string
					count         int
				}{{"gpt-5.1", "high", 1}, {"gpt-5.2", "low", 2}} {
					for i := 0; i < route.count; i++ {
						_, advanced, err := svc.BumpOpenAIOpaqueRouteEpoch(ctx, group, hash, route.model, route.effort, account.ID, int64(i))
						require.NoError(t, err)
						require.True(t, advanced)
					}
				}
				for len(cache.bumps) > 0 {
					<-cache.bumps
				}
				dial := func() *coderws.Conn {
					conn, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/openai/v1/responses", &coderws.DialOptions{HTTPHeader: http.Header{"session_id": {session}}})
					require.NoError(t, err)
					return conn
				}
				conn := dial()
				defer conn.CloseNow()
				readTerminal := func(conn *coderws.Conn) error {
					for {
						_, body, err := conn.Read(ctx)
						if err != nil {
							return err
						}
						switch gjson.GetBytes(body, "type").String() {
						case "response.completed", "response.incomplete", "response.failed", "response.done":
							return nil
						}
					}
				}
				writeOpenAIWSR5(t, conn, first)
				require.NoError(t, readTerminal(conn))
				writeOpenAIWSR5(t, conn, `{"type":"response.create","model":"gpt-5.2","reasoning":{"effort":"low"},"previous_response_id":"resp_r8_1","prompt_cache_key":"r8-cache","store":false,"tools":[{"type":"custom","name":"exec"}],"input":"second"}`)
				if kind == "precommit_mismatch" || kind == "early_exec_leak" {
					require.Error(t, readTerminal(conn), "mismatched response is not delivered")
				} else {
					_, delta, err := conn.Read(ctx)
					require.NoError(t, err)
					require.Equal(t, "response.output_text.delta", gjson.GetBytes(delta, "type").String())
					if kind == "client_cancel" {
						_ = conn.CloseNow()
						close(cancelData)
					} else if kind == "partial_eof" {
						require.Error(t, readTerminal(conn))
					} else {
						require.NoError(t, readTerminal(conn))
					}
				}
				escape := kind == "tool_denial" || kind == "partial_eof" || kind == "stream_terminated" || kind == "model_mismatch" || kind == "precommit_mismatch" || kind == "early_exec_leak" || kind == "late_leak" || kind == "done_completed_denial" || kind == "done_terminated"
				if escape {
					select {
					case bump := <-cache.bumps:
						require.Equal(t, r6EpochBump{model: "gpt-5.2", effort: "low", expected: 2}, bump)
					case <-ctx.Done():
						t.Fatal("missing current-turn route escape")
					}
				} else {
					require.Never(t, func() bool { return len(cache.bumps) != 0 }, 150*time.Millisecond, 10*time.Millisecond)
				}
				store := service.NewOpenAIWSStateStore(cache)
				if kind != "client_cancel" {
					for _, id := range []string{"resp_r8_1", "resp_r8_2"} {
						if (kind == "precommit_mismatch" || kind == "early_exec_leak") && id == "resp_r8_2" {
							_, found, err := store.GetResponseRouteEpoch(ctx, group, id, account.ID)
							require.NoError(t, err)
							require.False(t, found)
							continue
						}
						require.Eventually(t, func() bool {
							_, found, err := store.GetResponseRouteEpoch(ctx, group, id, account.ID)
							return err == nil && found
						}, time.Second, 10*time.Millisecond)
						epoch, found, err := store.GetResponseRouteEpoch(ctx, group, id, account.ID)
						require.NoError(t, err)
						require.True(t, found)
						require.Equal(t, int64(1), epoch, "historical response keeps creating identity")
					}
				}
				require.Equal(t, int64(1), svc.SnapshotOpenAIOpaqueRouteEpoch(ctx, group, hash, "gpt-5.1", "high", account.ID).Epoch)
				require.Equal(t, int32(2), calls.Load(), "no replay after committed output")
				<-frames
				failedBody := <-frames
				_ = conn.CloseNow()
				if escape {
					root := dial()
					defer root.CloseNow()
					writeOpenAIWSR5(t, root, `{"type":"response.create","model":"gpt-5.2","reasoning":{"effort":"low"},"prompt_cache_key":"r8-cache","store":false,"input":"new root"}`)
					require.NoError(t, readTerminal(root), "reconnected root; upstream attempts=%d", calls.Load())
					rootBody := <-frames
					require.NotEqual(t, gjson.GetBytes(failedBody, "prompt_cache_key").String(), gjson.GetBytes(rootBody, "prompt_cache_key").String(), "reconnected root uses new upstream identity")
					require.Empty(t, gjson.GetBytes(rootBody, "previous_response_id").String())
					require.Never(t, func() bool { return len(cache.bumps) != 0 }, 100*time.Millisecond, 10*time.Millisecond, "finished turn must not be reported twice on idle disconnect")
				}
			})
		}
	}
}
