package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestPassthroughLifecycle_OpaqueFirstFrameRouteEpoch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, epoch := range []int64{0, 1} {
		t.Run(fmt.Sprintf("epoch_%d", epoch), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			upstream := newStagedPassthroughConn()
			account := passthroughLifecycleAccount()
			account.Extra["openai_opaque_upstream"] = true
			svc := newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream)
			if epoch > 0 {
				_, advanced, err := svc.BumpOpenAIOpaqueRouteEpoch(ctx, 0, "session", "gpt-5.1", "", account.ID, 0)
				require.NoError(t, err)
				require.True(t, advanced)
			}
			server, serverErr := startPassthroughLifecycleServerWithHooks(t, ctx, svc, account, func(c *gin.Context) *OpenAIWSIngressHooks {
				_, err := svc.PrepareOpenAIOpaqueRouteEpochAttempt(ctx, c, 0, "session", "gpt-5.1", "", account, "")
				require.NoError(t, err)
				return nil
			})
			defer server.Close()
			client := dialPassthroughLifecycleClientWithPayload(t, server, `{"type":"response.create","model":"gpt-5.1","prompt_cache_key":"original-cache","stream":false}`)
			defer client.CloseNow()
			first := requirePassthroughUpstreamWrite(t, upstream, time.Second)
			want := "original-cache"
			if epoch > 0 {
				want = openAIOpaqueRouteEpochValue(account.ID, epoch, "prompt-cache", want)
			}
			require.Equal(t, want, gjson.GetBytes(first, "prompt_cache_key").String())
			upstream.Send(`{"type":"response.completed","response":{"id":"resp_epoch","model":"gpt-5.1"}}`)
			_, err := readPassthroughLifecycleFrame(t, client, time.Second)
			require.NoError(t, err)
			require.NoError(t, client.Close(coderws.StatusNormalClosure, "done"))
			select {
			case err := <-serverErr:
				require.NoError(t, err)
			case <-time.After(3 * time.Second):
				t.Fatal("passthrough did not stop")
			}
		})
	}
}

func TestPassthroughLifecycle_OpaqueLaterTurnModelMismatchClosesWithoutReplay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	upstream := newStagedPassthroughConn()
	account := passthroughLifecycleAccount()
	account.Extra["openai_opaque_upstream"] = true
	svc := newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream)
	server, serverErr := startPassthroughLifecycleServerWithHooks(t, ctx, svc, account, func(c *gin.Context) *OpenAIWSIngressHooks {
		_, err := svc.PrepareOpenAIOpaqueRouteEpochAttempt(ctx, c, 0, "session", "gpt-5.1", "", account, "")
		require.NoError(t, err)
		return nil
	})
	defer server.Close()
	client := dialPassthroughLifecycleClient(t, server)
	defer client.CloseNow()
	requirePassthroughUpstreamWrite(t, upstream, time.Second)
	upstream.Send(`{"type":"response.completed","response":{"id":"resp_first","model":"gpt-5.1"}}`)
	_, err := readPassthroughLifecycleFrame(t, client, time.Second)
	require.NoError(t, err)
	writeCtx, cancelWrite := context.WithTimeout(ctx, time.Second)
	err = client.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1","previous_response_id":"resp_first","input":"continue"}`))
	cancelWrite()
	require.NoError(t, err)
	requirePassthroughUpstreamWrite(t, upstream, time.Second)
	upstream.Send(`{"type":"response.created","response":{"id":"resp_wrong","model":"gpt-4.1-mini"}}`)
	payload, err := readPassthroughLifecycleFrame(t, client, time.Second)
	require.Empty(t, payload, "mismatched response.created must not reach the client")
	var wsClose coderws.CloseError
	require.ErrorAs(t, err, &wsClose)
	require.Equal(t, coderws.StatusPolicyViolation, wsClose.Code)
	select {
	case err := <-serverErr:
		var closeErr *OpenAIWSClientCloseError
		require.ErrorAs(t, err, &closeErr)
		var failoverErr *UpstreamFailoverError
		require.False(t, errors.As(err, &failoverErr), "later turns must not enter account failover")
	case <-time.After(3 * time.Second):
		t.Fatal("mismatched later turn did not stop")
	}
	epoch := svc.getOpenAIOpaqueRouteEpoch(ctx, 0, "session", "gpt-5.1", "", account.ID)
	require.Zero(t, epoch.Epoch, "rejected continuation must not rotate future route identity")
	require.Zero(t, epoch.Bumps)
	select {
	case replay := <-upstream.writes:
		t.Fatalf("unexpected replay: %s", replay)
	default:
	}
}

func TestPassthroughLifecycle_OpaqueResponseBindingRestoresOriginalEpoch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	upstream := newStagedPassthroughConn()
	account := passthroughLifecycleAccount()
	account.Extra["openai_opaque_upstream"] = true
	svc := newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream)
	const groupID int64 = 71
	_, advanced, err := svc.BumpOpenAIOpaqueRouteEpoch(ctx, groupID, "session", "gpt-5.1", "", account.ID, 0)
	require.NoError(t, err)
	require.True(t, advanced)
	server, serverErr := startPassthroughLifecycleServerWithHooks(t, ctx, svc, account, func(c *gin.Context) *OpenAIWSIngressHooks {
		group := groupID
		c.Set("api_key", &APIKey{GroupID: &group})
		_, err := svc.PrepareOpenAIOpaqueRouteEpochAttempt(ctx, c, groupID, "session", "gpt-5.1", "", account, c.Request.Header.Get("X-Test-Previous-Response"))
		require.NoError(t, err)
		return nil
	})
	defer server.Close()
	client := dialPassthroughLifecycleClientWithPayload(t, server, `{"type":"response.create","model":"gpt-5.1","prompt_cache_key":"cache"}`)
	defer client.CloseNow()
	first := requirePassthroughUpstreamWrite(t, upstream, time.Second)
	upstream.Send(`{"type":"response.completed","response":{"id":"resp_bound","model":"gpt-5.1"}}`)
	_, err = readPassthroughLifecycleFrame(t, client, time.Second)
	require.NoError(t, err)
	require.NoError(t, client.Close(coderws.StatusNormalClosure, "done"))
	select {
	case err := <-serverErr:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("first passthrough did not stop")
	}
	boundAccount, err := svc.getOpenAIWSStateStore().GetResponseAccount(ctx, groupID, "resp_bound")
	require.NoError(t, err)
	require.Equal(t, account.ID, boundAccount)
	_, advanced, err = svc.BumpOpenAIOpaqueRouteEpoch(ctx, groupID, "session", "gpt-5.1", "", account.ID, 1)
	require.NoError(t, err)
	require.True(t, advanced)
	upstream = newStagedPassthroughConn()
	svc.openaiWSPassthroughDialer = &stagedPassthroughDialer{conn: upstream}
	dialCtx, cancelDial := context.WithTimeout(ctx, 3*time.Second)
	continuation, _, err := coderws.Dial(dialCtx, "ws"+strings.TrimPrefix(server.URL, "http"), &coderws.DialOptions{HTTPHeader: http.Header{"X-Test-Previous-Response": []string{"resp_bound"}}})
	cancelDial()
	require.NoError(t, err)
	defer continuation.CloseNow()
	writeCtx, cancelWrite := context.WithTimeout(ctx, time.Second)
	err = continuation.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1","previous_response_id":"resp_bound","prompt_cache_key":"cache"}`))
	cancelWrite()
	require.NoError(t, err)
	next := requirePassthroughUpstreamWrite(t, upstream, time.Second)
	require.Equal(t, gjson.GetBytes(first, "prompt_cache_key").String(), gjson.GetBytes(next, "prompt_cache_key").String())
	require.Equal(t, openAIOpaqueRouteEpochValue(account.ID, 1, "prompt-cache", "cache"), gjson.GetBytes(next, "prompt_cache_key").String())
	require.Equal(t, "resp_bound", gjson.GetBytes(next, "previous_response_id").String())
	upstream.Send(`{"type":"response.completed","response":{"id":"resp_continued","model":"gpt-5.1"}}`)
	_, err = readPassthroughLifecycleFrame(t, continuation, time.Second)
	require.NoError(t, err)
	require.NoError(t, continuation.Close(coderws.StatusNormalClosure, "done"))
	select {
	case err := <-serverErr:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("continuation did not stop")
	}
}

func TestOpenAIWSOpaqueLaterTurnContinuationRejectsReplay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{OpenAIWSIngressModeHTTPBridge, OpenAIWSIngressModeCtxPool} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cfg := passthroughLifecycleConfig()
			cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
			account := passthroughLifecycleAccount()
			account.Extra["openai_opaque_upstream"] = true
			account.Extra["openai_apikey_responses_websockets_v2_mode"] = mode
			svc := newPassthroughLifecycleService(cfg, newStagedPassthroughConn())
			completed := `{"type":"response.completed","response":{"id":"resp_first","model":"gpt-5.1","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"first-ok"}]}]}}`
			limited := `{"type":"error","error":{"type":"usage_limit_reached","code":"rate_limit_exceeded","message":"The usage limit has been reached"}}`
			httpUpstream := &httpUpstreamRecorder{responses: []*http.Response{
				{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: " + completed + "\n\n"))},
				{StatusCode: http.StatusTooManyRequests, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(limited))},
			}}
			svc.httpUpstream = httpUpstream
			capture := &openAIWSCaptureConn{events: [][]byte{[]byte(completed), []byte(limited)}}
			dialer := &openAIWSCaptureDialer{conn: capture}
			pool := newOpenAIWSConnPool(cfg)
			pool.setClientDialerForTest(dialer)
			svc.openaiWSPool = pool
			defer pool.Close()
			server, serverErr := startPassthroughLifecycleServer(t, ctx, svc, account)
			defer server.Close()
			client := dialPassthroughLifecycleClientWithPayload(t, server, `{"type":"response.create","model":"gpt-5.1","input":[{"role":"user","content":"first"}]}`)
			defer client.CloseNow()
			first, err := readPassthroughLifecycleFrame(t, client, 3*time.Second)
			require.NoError(t, err)
			require.Equal(t, "resp_first", gjson.GetBytes(first, "response.id").String())
			writeCtx, cancelWrite := context.WithTimeout(ctx, time.Second)
			err = client.Write(writeCtx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-5.1","previous_response_id":"resp_first","input":[{"role":"user","content":"continue"}]}`))
			cancelWrite()
			require.NoError(t, err)
			select {
			case err := <-serverErr:
				var failoverErr *UpstreamFailoverError
				require.ErrorAs(t, err, &failoverErr)
				retryPayload, currentTurn := OpenAIWSCurrentTurnRetryPayload(err)
				require.True(t, currentTurn)
				require.Empty(t, retryPayload, "opaque continuation must not offer a detached replay payload")
			case <-time.After(3 * time.Second):
				t.Fatal("later-turn failover did not stop")
			}
			if mode == OpenAIWSIngressModeCtxPool {
				require.Equal(t, 1, dialer.DialCount())
				capture.mu.Lock()
				defer capture.mu.Unlock()
				require.Len(t, capture.writes, 2)
				require.Equal(t, "resp_first", capture.writes[1]["previous_response_id"])
			} else {
				require.Len(t, httpUpstream.bodies, 2)
				require.Equal(t, "resp_first", gjson.GetBytes(httpUpstream.bodies[1], "previous_response_id").String())
				require.EqualValues(t, 1, gjson.GetBytes(httpUpstream.bodies[1], "input.#").Int(), "opaque continuation must not duplicate server-side history")
				require.Equal(t, "continue", gjson.GetBytes(httpUpstream.bodies[1], "input.0.content").String())
			}
		})
	}
}

func TestOpenAIWSOpaqueLaterTurnPreservesContinuationWhenRequestChanges(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name         string
		instructions string
		window       string
	}{
		{name: "changed_instructions", instructions: "different", window: "window-a"},
		{name: "changed_context_window", instructions: "original", window: "window-b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cfg := passthroughLifecycleConfig()
			cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
			account := passthroughLifecycleAccount()
			account.Extra["openai_opaque_upstream"] = true
			account.Extra["openai_apikey_responses_websockets_v2_mode"] = OpenAIWSIngressModeCtxPool
			svc := newPassthroughLifecycleService(cfg, newStagedPassthroughConn())
			capture := &openAIWSCaptureConn{events: [][]byte{
				[]byte(`{"type":"response.completed","response":{"id":"resp_first","model":"gpt-5.1"}}`),
				[]byte(`{"type":"error","error":{"type":"usage_limit_reached","code":"rate_limit_exceeded","message":"The usage limit has been reached"}}`),
			}}
			pool := newOpenAIWSConnPool(cfg)
			pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: capture})
			svc.openaiWSPool = pool
			defer pool.Close()
			server, serverErr := startPassthroughLifecycleServer(t, ctx, svc, account)
			defer server.Close()
			client := dialPassthroughLifecycleClientWithPayload(t, server, `{"type":"response.create","model":"gpt-5.1","store":false,"instructions":"original","input":[{"role":"user","content":"first"}],"client_metadata":{"x-codex-window-id":"window-a"}}`)
			defer client.CloseNow()
			_, err := readPassthroughLifecycleFrame(t, client, 3*time.Second)
			require.NoError(t, err)
			payload := fmt.Sprintf(`{"type":"response.create","model":"gpt-5.1","store":false,"instructions":%q,"previous_response_id":"resp_first","input":[{"role":"user","content":"continue"}],"client_metadata":{"x-codex-window-id":%q}}`, tc.instructions, tc.window)
			writeCtx, cancelWrite := context.WithTimeout(ctx, time.Second)
			err = client.Write(writeCtx, coderws.MessageText, []byte(payload))
			cancelWrite()
			require.NoError(t, err)
			select {
			case err := <-serverErr:
				_, currentTurn := OpenAIWSCurrentTurnRetryPayload(err)
				require.True(t, currentTurn)
			case <-time.After(3 * time.Second):
				t.Fatal("later turn did not stop")
			}
			capture.mu.Lock()
			defer capture.mu.Unlock()
			require.Len(t, capture.writes, 2)
			require.Equal(t, "resp_first", capture.writes[1]["previous_response_id"])
			input, ok := capture.writes[1]["input"].([]any)
			require.True(t, ok)
			require.Len(t, input, 1, "opaque continuation must preserve only the current incremental input")
		})
	}
}
