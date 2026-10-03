package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type openAIResponseOwnerErrorCache struct {
	stubGatewayCache
	err error
}

func (c *openAIResponseOwnerErrorCache) GetSessionAccountID(context.Context, int64, string) (int64, error) {
	return 0, c.err
}

func TestOpenAIWSStateStoreResponseOwnerLookupPropagatesFailure(t *testing.T) {
	cause := errors.New("state lookup unavailable")
	store := NewOpenAIWSStateStore(&openAIResponseOwnerErrorCache{err: cause})
	owner, err := store.GetResponseAccount(t.Context(), 9, "resp_other_replica")
	require.Zero(t, owner)
	require.ErrorIs(t, err, cause)
}

func TestOpenAIWSOpaqueLaterTurnRejectsForeignAccountAndEpoch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{OpenAIWSIngressModePassthrough, OpenAIWSIngressModeCtxPool, OpenAIWSIngressModeHTTPBridge} {
		for _, foreignAccount := range []bool{false, true} {
			name := "historical_epoch"
			if foreignAccount {
				name = "foreign_account"
			}
			t.Run(mode+"/"+name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				cfg := passthroughLifecycleConfig()
				cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
				account := passthroughLifecycleAccount()
				account.Extra["openai_opaque_upstream"] = true
				account.Extra["openai_apikey_responses_websockets_v2_mode"] = mode
				staged := newStagedPassthroughConn()
				svc := newPassthroughLifecycleService(cfg, staged)
				_, advanced, err := svc.BumpOpenAIOpaqueRouteEpoch(ctx, 0, "session", "gpt-5.1", "", account.ID, 0)
				require.NoError(t, err)
				require.True(t, advanced)
				ownerID := account.ID
				if foreignAccount {
					ownerID++
				}
				store := svc.getOpenAIWSStateStore()
				require.NoError(t, store.BindResponseAccount(ctx, 0, "resp_foreign", ownerID, time.Hour))
				require.NoError(t, store.BindResponseRouteEpoch(ctx, 0, "resp_foreign", ownerID, 0, time.Hour))
				completed := `{"type":"response.completed","response":{"id":"resp_current","model":"gpt-5.1"}}`
				httpUpstream := &httpUpstreamRecorder{resp: &http.Response{
					StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}},
					Body: io.NopCloser(strings.NewReader("data: " + completed + "\n\n")),
				}}
				svc.httpUpstream = httpUpstream
				capture := &openAIWSCaptureConn{events: [][]byte{[]byte(completed)}}
				pool := newOpenAIWSConnPool(cfg)
				pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: capture})
				svc.openaiWSPool = pool
				defer pool.Close()
				server, serverErr := startPassthroughLifecycleServerWithHooks(t, ctx, svc, account, func(c *gin.Context) *OpenAIWSIngressHooks {
					_, err := svc.PrepareOpenAIOpaqueRouteEpochAttempt(ctx, c, 0, "session", "gpt-5.1", "", account, "")
					require.NoError(t, err)
					return nil
				})
				defer server.Close()
				client := dialPassthroughLifecycleClientWithPayload(t, server, `{"type":"response.create","model":"gpt-5.1","prompt_cache_key":"cache","input":"first"}`)
				defer client.CloseNow()
				if mode == OpenAIWSIngressModePassthrough {
					requirePassthroughUpstreamWrite(t, staged, time.Second)
					staged.Send(completed)
				}
				_, err = readPassthroughLifecycleFrame(t, client, 3*time.Second)
				require.NoError(t, err)
				writeCtx, cancelWrite := context.WithTimeout(ctx, time.Second)
				err = client.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1","previous_response_id":"resp_foreign","input":"continue"}`))
				cancelWrite()
				require.NoError(t, err)
				if mode == OpenAIWSIngressModePassthrough {
					_, err = readPassthroughLifecycleFrame(t, client, 3*time.Second)
					var closeErr coderws.CloseError
					require.ErrorAs(t, err, &closeErr)
					require.Equal(t, coderws.StatusPolicyViolation, closeErr.Code)
				}
				select {
				case err := <-serverErr:
					var closeErr *OpenAIWSClientCloseError
					require.ErrorAs(t, err, &closeErr)
					require.Equal(t, coderws.StatusPolicyViolation, closeErr.StatusCode())
				case <-time.After(3 * time.Second):
					t.Fatal("incompatible continuation did not stop")
				}
				if mode == OpenAIWSIngressModePassthrough {
					select {
					case payload := <-staged.writes:
						t.Fatalf("incompatible continuation reached upstream: %s", payload)
					default:
					}
				} else if mode == OpenAIWSIngressModeCtxPool {
					capture.mu.Lock()
					defer capture.mu.Unlock()
					require.Len(t, capture.writes, 1)
				} else {
					require.Len(t, httpUpstream.bodies, 1)
				}
			})
		}
	}
}
