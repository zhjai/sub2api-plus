package prism

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/httpc"
)

func newTestClient(t *testing.T, baseURL string) *Client {
	t.Helper()
	up := config.Default().Upstream
	up.BaseURL = baseURL
	up.ForceHTTP2 = false
	hc, err := httpc.New(up, httpc.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return New(hc, UpstreamOptions{MaxRetries: 3, RetryBackoff: time.Millisecond, RetryMaxDelay: time.Millisecond}, testSchema())
}

// start 不幂等：502/504 时上游可能已经开始生成，重放会在同一会话上并发出第二次生成。
func TestStartResponse_NoReplayOn5xx(t *testing.T) {
	var starts, polls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == testSchema().StartPath {
			starts.Add(1)
		} else {
			polls.Add(1)
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	c := newTestClient(t, srv.URL)

	if _, err := c.StartResponse(context.Background(), Principal{}, &StartRequest{}); err == nil {
		t.Fatal("502 应返回错误")
	}
	if n := starts.Load(); n != 1 {
		t.Fatalf("start 被重放了 %d 次", n)
	}

	// 幂等的轮询仍按原策略重试。
	_, _ = c.PollResponse(context.Background(), Principal{}, &StatusRequest{RequestID: "r", TurnState: []byte(`{}`)}, "")
	if n := polls.Load(); n < 2 {
		t.Fatalf("status 轮询应重试，实际 %d 次", n)
	}
}

// A 503 may come from an intermediary after dispatch; it cannot prove rejection.
func TestStartResponse_NoReplayOn503(t *testing.T) {
	var starts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if starts.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"started","request_id":"req-1","turn_state":{"a":1}}`))
	}))
	defer srv.Close()
	c := newTestClient(t, srv.URL)

	resp, err := c.StartResponse(context.Background(), Principal{}, &StartRequest{})
	if err == nil || resp != nil {
		t.Fatalf("503 should fail without replay: %+v err=%v", resp, err)
	}
	if starts.Load() != 1 {
		t.Fatalf("start replayed: %d", starts.Load())
	}
}
