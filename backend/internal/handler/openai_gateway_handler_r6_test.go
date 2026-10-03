package handler

import (
	"context"
	"fmt"
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

type r6EpochBump struct {
	model, effort string
	expected      int64
}

type r6EpochCache struct {
	service.GatewayCache
	epochs service.OpenAIOpaqueRouteEpochCache
	bumps  chan r6EpochBump
}

func (c *r6EpochCache) GetOpenAIOpaqueRouteEpoch(ctx context.Context, groupID int64, session, model, effort string, accountID int64, ttl time.Duration) (service.OpenAIOpaqueRouteEpochState, error) {
	return c.epochs.GetOpenAIOpaqueRouteEpoch(ctx, groupID, session, model, effort, accountID, ttl)
}

func (c *r6EpochCache) BumpOpenAIOpaqueRouteEpoch(ctx context.Context, groupID int64, session, model, effort string, accountID, expected int64, maxBumps int, window, ttl time.Duration) (service.OpenAIOpaqueRouteEpochState, bool, error) {
	state, advanced, err := c.epochs.BumpOpenAIOpaqueRouteEpoch(ctx, groupID, session, model, effort, accountID, expected, maxBumps, window, ttl)
	c.bumps <- r6EpochBump{model: model, effort: effort, expected: expected}
	return state, advanced, err
}

func TestOpenAIResponsesWebSocket_R6FailureUsesCurrentTurnRouteSnapshot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{service.OpenAIWSIngressModePassthrough, service.OpenAIWSIngressModeCtxPool} {
		for _, tc := range []struct {
			name, model, effort string
			concurrent          bool
		}{
			{name: "effort", model: "gpt-5.1", effort: "low"},
			{name: "model", model: "gpt-5.2", effort: "high"},
			{name: "concurrent", model: "gpt-5.1", effort: "low", concurrent: true},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				var writes atomic.Int32
				finishSecond := make(chan struct{})
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					conn, err := coderws.Accept(w, r, nil)
					if err != nil {
						return
					}
					defer conn.CloseNow()
					for turn := 1; turn <= 2; turn++ {
						_, body, err := conn.Read(ctx)
						if err != nil {
							return
						}
						writes.Add(1)
						if turn == 1 {
							_ = conn.Write(ctx, coderws.MessageText, []byte(`{"type":"response.completed","response":{"id":"resp_r6_first","model":"gpt-5.1","status":"completed"}}`))
							continue
						}
						if gjson.GetBytes(body, "model").String() == "" {
							return
						}
						_ = conn.Write(ctx, coderws.MessageText, []byte(`{"type":"response.output_text.delta","response_id":"resp_r6_second","delta":"semantic output"}`))
						select {
						case <-finishSecond:
						case <-ctx.Done():
							return
						}
						_ = conn.Write(ctx, coderws.MessageText, []byte(`{"type":"response.completed","response":{"id":"resp_r6_second","model":"gpt-4.1-mini","status":"completed"}}`))
					}
					_, _, _ = conn.Read(ctx)
				}))
				defer upstream.Close()
				account := openAIWSR5Account(9986, "r6-opaque", upstream.URL, service.StatusActive, true, true, 1)
				account.Extra["openai_apikey_responses_websockets_v2_mode"] = mode
				baseCache := testutil.NewRedisGatewayCache(t)
				cache := &r6EpochCache{GatewayCache: baseCache, epochs: baseCache.(service.OpenAIOpaqueRouteEpochCache), bumps: make(chan r6EpochBump, 8)}
				_, svc, apiKey, server, _ := newOpenAIWSR5Handler(t, []service.Account{account}, cache)
				defer server.Close()
				const sessionID = "r6-route-snapshot"
				first := `{"type":"response.create","model":"gpt-5.1","reasoning":{"effort":"high"},"store":false,"input":"first"}`
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodGet, "/openai/v1/responses", nil)
				c.Request.Header.Set("session_id", sessionID)
				sessionHash := svc.GenerateSessionHash(c, []byte(first))
				require.NotEmpty(t, sessionHash)
				groupID := *apiKey.GroupID
				_, advanced, err := svc.BumpOpenAIOpaqueRouteEpoch(ctx, groupID, sessionHash, "gpt-5.1", "high", account.ID, 0)
				require.NoError(t, err)
				require.True(t, advanced)
				for epoch := int64(0); epoch < 2; epoch++ {
					_, advanced, err = svc.BumpOpenAIOpaqueRouteEpoch(ctx, groupID, sessionHash, tc.model, tc.effort, account.ID, epoch)
					require.NoError(t, err)
					require.True(t, advanced)
				}
				for len(cache.bumps) > 0 {
					<-cache.bumps
				}
				conn, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/openai/v1/responses", &coderws.DialOptions{HTTPHeader: http.Header{"session_id": {sessionID}}})
				require.NoError(t, err)
				defer conn.CloseNow()
				writeOpenAIWSR5(t, conn, first)
				require.NoError(t, readOpenAIWSR5(t, conn))
				writeOpenAIWSR5(t, conn, fmt.Sprintf(`{"type":"response.create","model":%q,"reasoning":{"effort":%q},"previous_response_id":"resp_r6_first","store":false,"input":"second"}`, tc.model, tc.effort))
				require.NoError(t, readOpenAIWSR5(t, conn), "semantic delta commits the current turn")
				if tc.concurrent {
					_, advanced, err = svc.BumpOpenAIOpaqueRouteEpoch(ctx, groupID, sessionHash, tc.model, tc.effort, account.ID, 2)
					require.NoError(t, err)
					require.True(t, advanced)
					<-cache.bumps
				}
				close(finishSecond)
				require.NoError(t, readOpenAIWSR5(t, conn))
				select {
				case bump := <-cache.bumps:
					require.Equal(t, r6EpochBump{model: tc.model, effort: tc.effort, expected: 2}, bump)
				case <-ctx.Done():
					t.Fatal("current-turn failure was not reported")
				}
				require.Eventually(t, func() bool {
					state, _ := cache.GetOpenAIOpaqueRouteEpoch(ctx, groupID, sessionHash, tc.model, tc.effort, account.ID, time.Hour)
					return state.Epoch == 3
				}, time.Second, 10*time.Millisecond)
				high := svc.SnapshotOpenAIOpaqueRouteEpoch(ctx, groupID, sessionHash, "gpt-5.1", "high", account.ID)
				require.Equal(t, int64(1), high.Epoch, "the first-frame key must not be advanced")
				store := service.NewOpenAIWSStateStore(cache)
				for _, id := range []string{"resp_r6_first", "resp_r6_second"} {
					require.Eventually(t, func() bool {
						_, found, err := store.GetResponseRouteEpoch(ctx, groupID, id, account.ID)
						return err == nil && found
					}, time.Second, 10*time.Millisecond)
					bound, found, err := store.GetResponseRouteEpoch(ctx, groupID, id, account.ID)
					require.NoError(t, err)
					require.True(t, found)
					require.Equal(t, int64(1), bound, "response identity remains the connection creating epoch")
				}
				require.Equal(t, int32(2), writes.Load(), "committed content must never be replayed")
			})
		}
	}
}
