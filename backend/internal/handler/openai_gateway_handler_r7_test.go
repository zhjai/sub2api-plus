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

type r7DeletedOwnerRepo struct {
	*openAIWSFailoverHandlerAccountRepoStub
	ownerID int64
	err     error
}

func (r *r7DeletedOwnerRepo) GetByID(ctx context.Context, id int64) (*service.Account, error) {
	if id == r.ownerID {
		return nil, r.err
	}
	return r.openAIWSFailoverHandlerAccountRepoStub.GetByID(ctx, id)
}

func TestOpenAIResponses_R7DeletedOwnerAdmission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, outage := range []bool{false, true} {
		t.Run(fmt.Sprintf("outage_%v", outage), func(t *testing.T) {
			upstream := &openAIHTTPR5WrongModelUpstream{}
			accounts := []service.Account{openAIWSR5Account(9979, "fallback", "http://127.0.0.1:1", service.StatusActive, true, false, 1)}
			repo := &r7DeletedOwnerRepo{openAIWSFailoverHandlerAccountRepoStub: &openAIWSFailoverHandlerAccountRepoStub{accounts: accounts}, ownerID: 9917, err: fmt.Errorf("lookup: %w", service.ErrAccountNotFound)}
			if outage {
				repo.err = fmt.Errorf("database unavailable")
			}
			cache := testutil.NewRedisGatewayCache(t)
			_, svc, apiKey, server, _ := newOpenAIR7HandlerWithRepo(t, accounts, cache, upstream, repo)
			defer server.Close()
			store := service.NewOpenAIWSStateStore(cache)
			require.NoError(t, store.BindResponseAccount(t.Context(), *apiKey.GroupID, "resp_r7_deleted", repo.ownerID, time.Hour))
			require.NoError(t, svc.BindOpenAIHTTPResponseOwner(t.Context(), *apiKey.GroupID, "resp_r7_deleted", apiKey.User.ID, apiKey.ID))
			body := `{"model":"gpt-5.1","stream":true,"previous_response_id":"resp_r7_deleted","input":"continue"}`
			resp, err := server.Client().Post(server.URL+"/openai/v1/responses", "application/json", strings.NewReader(body))
			require.NoError(t, err)
			defer resp.Body.Close()
			if outage {
				require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
			} else {
				require.Equal(t, http.StatusBadRequest, resp.StatusCode)
			}
			conn := dialOpenAIWSR5(t, server)
			defer conn.CloseNow()
			writeOpenAIWSR5(t, conn, `{"type":"response.create","model":"gpt-5.1","previous_response_id":"resp_r7_deleted","input":"continue"}`)
			err = readOpenAIWSR5(t, conn)
			if outage {
				require.Equal(t, coderws.StatusTryAgainLater, coderws.CloseStatus(err))
			} else {
				require.Equal(t, coderws.StatusPolicyViolation, coderws.CloseStatus(err))
			}
			require.Zero(t, upstream.calls.Load(), "unavailable ownership never reaches fallback")
			id, err := store.GetResponseAccount(t.Context(), *apiKey.GroupID, "resp_r7_deleted")
			require.NoError(t, err)
			require.Equal(t, repo.ownerID, id)
		})
	}
}

type r7NetworkHTTPUpstream struct {
	service.HTTPUpstream
	client *http.Client
}

func (u *r7NetworkHTTPUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.client.Do(req)
}

func TestOpenAIResponsesWebSocket_R7LaterRootRetryUsesOriginalRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{service.OpenAIWSIngressModeCtxPool, service.OpenAIWSIngressModeHTTPBridge} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			var calls atomic.Int32
			frames := make(chan []byte, 4)
			makeEvents := func(body []byte) [][]byte {
				call := calls.Add(1)
				frames <- append([]byte(nil), body...)
				if call == 2 {
					return [][]byte{[]byte(`{"type":"response.created","response":{"id":"resp_r7_bad","model":"gpt-4.1-mini","status":"in_progress"}}`)}
				}
				id, model := "resp_r7_first", "gpt-5.1"
				if call >= 3 {
					id, model = "resp_r7_retry", "gpt-4.1-mini"
				}
				return [][]byte{
					[]byte(fmt.Sprintf(`{"type":"response.output_text.delta","response_id":%q,"delta":"valid output"}`, id)),
					[]byte(fmt.Sprintf(`{"type":"response.completed","response":{"id":%q,"model":%q,"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"valid output"}]}]}}`, id, model)),
				}
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
				}
			}))
			defer upstream.Close()
			account := openAIWSR5Account(9987, "r7-opaque", upstream.URL, service.StatusActive, true, true, 1)
			account.Extra["openai_apikey_responses_websockets_v2_mode"] = mode
			baseCache := testutil.NewRedisGatewayCache(t)
			cache := &r6EpochCache{GatewayCache: baseCache, epochs: baseCache.(service.OpenAIOpaqueRouteEpochCache), bumps: make(chan r6EpochBump, 8)}
			_, svc, apiKey, server, _ := newOpenAIR5Handler(t, []service.Account{account}, cache, &r7NetworkHTTPUpstream{client: upstream.Client()})
			defer server.Close()
			apiKey.Group.ReasoningEffortMappings = []service.ReasoningEffortMapping{{From: "low", To: "medium"}, {From: "medium", To: "high"}}
			const session = "r7-later-root"
			first := `{"type":"response.create","model":"gpt-5.1","reasoning":{"effort":"high"},"store":false,"input":"first"}`
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/openai/v1/responses", nil)
			c.Request.Header.Set("session_id", session)
			hash := svc.GenerateSessionHash(c, []byte(first))
			groupID := *apiKey.GroupID
			for _, route := range []struct {
				model, effort string
				count         int
			}{{"gpt-5.1", "high", 1}, {"gpt-5.2", "low", 2}} {
				for i := 0; i < route.count; i++ {
					_, advanced, err := svc.BumpOpenAIOpaqueRouteEpoch(ctx, groupID, hash, route.model, route.effort, account.ID, int64(i))
					require.NoError(t, err)
					require.True(t, advanced)
				}
			}
			for len(cache.bumps) > 0 {
				<-cache.bumps
			}
			conn, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/openai/v1/responses", &coderws.DialOptions{HTTPHeader: http.Header{"session_id": {session}}})
			require.NoError(t, err)
			defer conn.CloseNow()
			readCompleted := func() {
				for {
					_, body, err := conn.Read(ctx)
					require.NoError(t, err)
					require.NotEqual(t, "resp_r7_bad", gjson.GetBytes(body, "response.id").String(), "precommit failure must not reach client")
					if gjson.GetBytes(body, "type").String() == "response.completed" {
						return
					}
				}
			}
			writeOpenAIWSR5(t, conn, first)
			readCompleted()
			writeOpenAIWSR5(t, conn, `{"type":"response.create","model":"gpt-5.2","reasoning":{"effort":"low"},"store":false,"input":"second"}`)
			readCompleted()
			for _, expected := range []int64{2, 3} {
				select {
				case bump := <-cache.bumps:
					require.Equal(t, r6EpochBump{model: "gpt-5.2", effort: "low", expected: expected}, bump)
				case <-ctx.Done():
					t.Fatal("failed-turn route was not advanced")
				}
			}
			require.Equal(t, int64(1), svc.SnapshotOpenAIOpaqueRouteEpoch(ctx, groupID, hash, "gpt-5.1", "high", account.ID).Epoch)
			for call := 1; call <= 3; call++ {
				body := <-frames
				model, effort := "gpt-5.2", "medium"
				if call == 1 {
					model, effort = "gpt-5.1", "high"
				}
				require.Equal(t, model, gjson.GetBytes(body, "model").String())
				require.Equal(t, effort, gjson.GetBytes(body, "reasoning.effort").String())
			}
			store := service.NewOpenAIWSStateStore(cache)
			for id, expected := range map[string]int64{"resp_r7_first": 1, "resp_r7_retry": 3} {
				require.Eventually(t, func() bool {
					epoch, found, err := store.GetResponseRouteEpoch(ctx, groupID, id, account.ID)
					return err == nil && found && epoch == expected
				}, time.Second, 10*time.Millisecond)
			}
			require.Equal(t, int32(3), calls.Load(), "successful first turn must not replay")
		})
	}
}
