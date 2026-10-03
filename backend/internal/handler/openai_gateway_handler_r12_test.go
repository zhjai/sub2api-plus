package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIResponsesWebSocket_R12TerminalOnlyOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range r11WSModes {
		for _, eventType := range []string{"response.incomplete", "response.done"} {
			for _, tool := range []bool{false, true} {
				for _, reason := range []string{"stream_terminated", "max_output_tokens"} {
					t.Run(fmt.Sprintf("%s/%s/tool=%t/%s", mode, eventType, tool, reason), func(t *testing.T) {
						ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
						output := `[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"partial work"}]}]`
						if tool {
							output = "[" + r11ExecItem + "]"
						}
						terminal := fmt.Sprintf(`{"type":%q,"response":{"id":"resp_r12_terminal","model":"gpt-5.1","status":"incomplete","output":%s,"incomplete_details":{"reason":%q}}}`, eventType, output, reason)
						upstream := newR11WireUpstream(t, ctx, mode, func(_ context.Context, _ int32, send func(string) error) bool {
							return r11Send(send, terminal)
						})
						t.Cleanup(cancel)
						account := openAIWSR5Account(10120, "r12-terminal", upstream.server.URL, service.StatusActive, true, true, 1)
						account.Extra["openai_apikey_responses_websockets_v2_mode"] = mode
						g := newR11WireGateway(t, []service.Account{account}, r11ExecRoot)
						conn := g.dial(t, ctx, r11ExecRoot)
						r11RequireFrames(t, ctx, conn, []string{terminal})
						r9RequireResponseBinding(t, g.cache, *g.apiKey.GroupID, "resp_r12_terminal", account.ID, 0)
						if reason == "stream_terminated" {
							require.Eventually(t, func() bool { return len(g.cache.bumps) == 1 }, time.Second, 10*time.Millisecond,
								"delivered terminal-only output must cause future-root escape")
							require.Equal(t, r6EpochBump{model: "gpt-5.1", effort: "high", expected: 0}, <-g.cache.bumps)
						}
						_ = conn.CloseNow()
						r11Await(t, ctx, g.done, "terminal-only handler completion")
						require.Empty(t, g.cache.bumps, "no duplicate escape and output limits remain neutral")
						require.Equal(t, int32(1), upstream.calls.Load(), "never replay a delivered terminal")
						expectedEpoch := int64(0)
						if reason == "stream_terminated" {
							expectedEpoch = 1
						}
						r11RequireRoute(t, ctx, g, account.ID, expectedEpoch, int(expectedEpoch))
						r9RequireResponseBinding(t, g.cache, *g.apiKey.GroupID, "resp_r12_terminal", account.ID, 0)
					})
				}
			}
		}
	}
}

func TestOpenAIResponsesWebSocket_R12BinaryDeliveryNeverReplays(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, prefixHeld := range []bool{false, true} {
		t.Run(fmt.Sprintf("held_prefix=%t", prefixHeld), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
			allowBinary := make(chan struct{})
			binaryPayload := []byte{1, 2, 3, 4}
			prefix := r11Prefix("resp_r12_binary")
			leakText := "to=functions.exec code:\n{\"cmd\":\"pwd\"}"
			if prefixHeld {
				leakText = `{"cmd":"pwd"}`
			}
			leak := r11Delta("resp_r12_binary", leakText)
			terminal := r11Completed("resp_r12_binary", `[]`)
			upstream := &r11WireUpstream{requests: make(chan []byte, 8)}
			upstream.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := coderws.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer conn.CloseNow()
				_, body, err := conn.Read(ctx)
				if err != nil {
					return
				}
				call := upstream.calls.Add(1)
				upstream.requests <- append([]byte(nil), body...)
				send := func(event string) error { return conn.Write(ctx, coderws.MessageText, []byte(event)) }
				if call > 1 {
					r11Send(send, r11Healthy("resp_r12_unexpected_retry", true)...)
					return
				}
				if prefixHeld && !r11Send(send, append(append([]string(nil), prefix...), r11Keepalive)...) {
					return
				}
				if !r11Gate(ctx, allowBinary) || conn.Write(ctx, coderws.MessageBinary, binaryPayload) != nil {
					return
				}
				r11Send(send, leak, terminal)
				_, _, _ = conn.Read(ctx)
			}))
			t.Cleanup(upstream.server.Close)
			t.Cleanup(cancel)
			account := openAIWSR5Account(10121, "r12-binary", upstream.server.URL, service.StatusActive, true, true, 1)
			account.Extra["openai_apikey_responses_websockets_v2_mode"] = service.OpenAIWSIngressModePassthrough
			g := newR11WireGateway(t, []service.Account{account}, r11ExecRoot)
			conn := g.dial(t, ctx, r11ExecRoot)
			if prefixHeld {
				r11RequireFrames(t, ctx, conn, []string{r11Keepalive})
			}
			close(allowBinary)
			if prefixHeld {
				r11RequireFrames(t, ctx, conn, prefix)
			}
			kind, payload, err := conn.Read(ctx)
			require.NoError(t, err)
			require.Equal(t, coderws.MessageBinary, kind)
			require.Equal(t, binaryPayload, payload)
			r11RequireFrames(t, ctx, conn, []string{leak, terminal})
			r9RequireResponseBinding(t, g.cache, *g.apiKey.GroupID, "resp_r12_binary", account.ID, 0)
			require.Eventually(t, func() bool { return len(g.cache.bumps) == 1 }, time.Second, 10*time.Millisecond)
			require.Equal(t, r6EpochBump{model: "gpt-5.1", effort: "high", expected: 0}, <-g.cache.bumps)
			_ = conn.CloseNow()
			r11Await(t, ctx, g.done, "binary-output handler completion")
			require.Equal(t, int32(1), upstream.calls.Load(), "binary delivery forbids cross-account replay")
			require.Empty(t, g.cache.bumps)
			r11RequireRoute(t, ctx, g, account.ID, 1, 1)
		})
	}
}

func TestOpenAIResponsesWebSocket_R12ImmediateNextTurnKeepsFailureDimensions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range r11WSModes {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
			nextRoot := `{"type":"response.create","model":"gpt-5.2","reasoning":{"effort":"low"},"store":false,"prompt_cache_key":"r11-cache","tools":[{"type":"custom","name":"exec"}],"input":"next task"}`
			terminal := func(id, model string) string {
				return fmt.Sprintf(`{"type":"response.incomplete","response":{"id":%q,"model":%q,"status":"incomplete","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"partial work"}]}],"incomplete_details":{"reason":"stream_terminated"}}}`, id, model)
			}
			first, second := terminal("resp_r12_first", "gpt-5.1"), terminal("resp_r12_second", "gpt-5.2")
			upstream := newR11WireUpstream(t, ctx, mode, func(_ context.Context, call int32, send func(string) error) bool {
				if call == 1 {
					return r11Send(send, first)
				}
				return r11Send(send, second)
			})
			t.Cleanup(cancel)
			account := openAIWSR5Account(10122, "r12-next-turn", upstream.server.URL, service.StatusActive, true, true, 1)
			account.Extra["openai_apikey_responses_websockets_v2_mode"] = mode
			g := newR11WireGateway(t, []service.Account{account}, r11ExecRoot)
			conn := g.dial(t, ctx, r11ExecRoot)
			r11RequireFrames(t, ctx, conn, []string{first})
			// No pause or polling between terminal reception and next-turn admission.
			require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte(nextRoot)))
			r11RequireFrames(t, ctx, conn, []string{second})
			r9RequireResponseBinding(t, g.cache, *g.apiKey.GroupID, "resp_r12_second", account.ID, 0)
			require.Eventually(t, func() bool { return len(g.cache.bumps) == 2 }, time.Second, 10*time.Millisecond)
			require.Equal(t, r6EpochBump{model: "gpt-5.1", effort: "high", expected: 0}, <-g.cache.bumps)
			require.Equal(t, r6EpochBump{model: "gpt-5.2", effort: "low", expected: 0}, <-g.cache.bumps)
			_ = conn.CloseNow()
			r11Await(t, ctx, g.done, "immediate-next-turn handler completion")
			require.Equal(t, int32(2), upstream.calls.Load(), "two client turns and no replay")
			require.Empty(t, g.cache.bumps)
			r9RequireResponseBinding(t, g.cache, *g.apiKey.GroupID, "resp_r12_first", account.ID, 0)
			r9RequireResponseBinding(t, g.cache, *g.apiKey.GroupID, "resp_r12_second", account.ID, 0)
		})
	}
}
