//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type hardRPMLocalWSDialer struct{ url string }

func (d *hardRPMLocalWSDialer) Dial(ctx context.Context, _ string, headers http.Header, _ string) (openAIWSClientConn, int, http.Header, error) {
	return newDefaultOpenAIWSClientDialer().Dial(ctx, d.url, headers, "")
}

type hardRPMBridgeStream struct {
	HTTPUpstream
	url   string
	sends atomic.Int64
}

func (u *hardRPMBridgeStream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.sends.Add(1)
	local, err := http.NewRequestWithContext(req.Context(), req.Method, u.url, req.Body)
	if err != nil {
		return nil, err
	}
	return http.DefaultClient.Do(local)
}

// Both client and upstream use real loopback sockets. All payloads and accounts
// are synthetic; no provider endpoint or production storage is contacted.
func TestAccountHardRPMWSSocketModesRetainOwnerOnLaterDenial(t *testing.T) {
	for _, mode := range []string{OpenAIWSIngressModeCtxPool, OpenAIWSIngressModeShared, OpenAIWSIngressModeDedicated, OpenAIWSIngressModePassthrough, OpenAIWSIngressModeHTTPBridge} {
		for _, unavailable := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "_capacity", true: "_cache"}[unavailable], func(t *testing.T) {
				cfg := passthroughLifecycleConfig()
				cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
				cfg.Gateway.OpenAIWS.IngressInterTurnIdleTimeoutSeconds = 5
				cfg.Gateway.OpenAIWS.MaxConnsPerAccount = 1
				cfg.Gateway.OpenAIWS.MaxIdlePerAccount = 1
				cfg.Gateway.OpenAIWS.QueueLimitPerConn = 4
				var upstreamFrames atomic.Int64
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if mode == OpenAIWSIngressModeHTTPBridge {
						_, _ = io.Copy(io.Discard, r.Body)
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_rpm_first\",\"model\":\"gpt-5.1\",\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
						return
					}
					conn, err := coderws.Accept(w, r, nil)
					if err != nil {
						return
					}
					defer conn.CloseNow()
					for {
						_, payload, err := conn.Read(r.Context())
						if err != nil {
							return
						}
						if gjson.GetBytes(payload, "type").String() != "response.create" {
							continue
						}
						upstreamFrames.Add(1)
						_ = conn.Write(r.Context(), coderws.MessageText, []byte(`{"type":"response.output_text.delta","delta":"hello"}`))
						_ = conn.Write(r.Context(), coderws.MessageText, []byte(`{"type":"response.completed","response":{"id":"resp_rpm_first","model":"gpt-5.1","status":"completed","usage":{"input_tokens":1,"output_tokens":1}}}`))
					}
				}))
				defer upstream.Close()
				dialer := &hardRPMLocalWSDialer{url: "ws" + strings.TrimPrefix(upstream.URL, "http")}
				s := newPassthroughLifecycleService(cfg, nil)
				s.openaiWSPassthroughDialer = dialer
				pool := newOpenAIWSConnPool(cfg)
				pool.setClientDialerForTest(dialer)
				defer pool.Close()
				s.openaiWSPool = pool
				cache := &hardRPMCache{GatewayCache: &stubGatewayCache{}}
				s.cache = cache
				store := NewOpenAIWSStateStore(nil)
				s.openaiWSStateStore = store
				a := passthroughLifecycleAccount()
				a.Extra["rpm_limit"] = 1
				a.Extra["openai_apikey_responses_websockets_v2_mode"] = mode
				bridge := &hardRPMBridgeStream{url: upstream.URL}
				s.httpUpstream = bridge
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				hooks := &OpenAIWSIngressHooks{BeforeTurn: func(turn int) error {
					if turn == 2 {
						if err := store.BindResponseRouteEpoch(ctx, 0, "resp_rpm_first", a.ID, 42, time.Minute); err != nil {
							return err
						}
						if unavailable {
							cache.err = errors.New("synthetic Redis failure")
						}
					}
					return nil
				}}
				server, serverErr := startPassthroughHookRecordingServer(t, ctx, s, a, hooks)
				defer server.Close()
				client := dialPassthroughLifecycleClientWithPayload(t, server, `{"type":"response.create","model":"gpt-5.1","stream":true,"input":"hello"}`)
				defer client.CloseNow()
				for {
					_, payload, err := client.Read(ctx)
					require.NoError(t, err)
					if gjson.GetBytes(payload, "type").String() == "response.completed" {
						break
					}
				}
				require.NoError(t, client.Write(ctx, coderws.MessageBinary, []byte(`{"type":"response.create","model":"gpt-5.1","stream":true,"previous_response_id":"resp_rpm_first","input":"next"}`)))
				go func() { _, _, _ = client.Read(ctx) }()
				select {
				case err := <-serverErr:
					var local *AccountRPMError
					require.ErrorAs(t, err, &local)
					require.True(t, local.NoMigration)
					require.Equal(t, unavailable, local.Unavailable)
				case <-ctx.Done():
					t.Fatal("WS admission did not terminate the unsent turn")
				}
				require.Equal(t, 1, cache.used)
				if mode == OpenAIWSIngressModeHTTPBridge {
					require.EqualValues(t, 1, bridge.sends.Load())
				} else {
					require.EqualValues(t, 1, upstreamFrames.Load())
				}
				owner, err := store.GetResponseAccount(context.Background(), 0, "resp_rpm_first")
				require.NoError(t, err)
				require.Equal(t, a.ID, owner)
				epoch, found, err := store.GetResponseRouteEpoch(context.Background(), 0, "resp_rpm_first", a.ID)
				require.NoError(t, err)
				require.True(t, found)
				require.EqualValues(t, 42, epoch)
			})
		}
	}
}
