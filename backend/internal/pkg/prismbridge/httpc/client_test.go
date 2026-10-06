package httpc

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/config"
)

func testCfg(base string) config.UpstreamConfig {
	c := config.Default().Upstream
	c.BaseURL = base
	c.ForceHTTP2 = false
	c.DialTimeout = 2 * time.Second
	return c
}

func TestJoinPath(t *testing.T) {
	cases := []struct{ base, p, want string }{
		{"", "/api/x", "/api/x"},
		{"/", "/api/x", "/api/x"},
		{"", "api/x", "/api/x"},
		{"/v1", "/api/x", "/v1/api/x"},
		{"/v1/", "/api/x", "/v1/api/x"},
		{"/v1", "api/x", "/v1/api/x"},
		{"/v1", "", "/v1"},
		{"", "", "/"},
	}
	for _, c := range cases {
		if got := joinPath(c.base, c.p); got != c.want {
			t.Errorf("joinPath(%q,%q) = %q, want %q", c.base, c.p, got, c.want)
		}
	}
}

func TestNewRequest_PathAndQuery(t *testing.T) {
	cl, err := New(testCfg("https://example.com"), Options{})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("普通路径", func(t *testing.T) {
		req, err := cl.NewRequest(context.Background(), http.MethodGet, "/api/auth/session", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if req.URL.String() != "https://example.com/api/auth/session" {
			t.Fatalf("URL = %s", req.URL)
		}
	})

	t.Run("带 query", func(t *testing.T) {
		req, err := cl.NewRequest(context.Background(), http.MethodGet, "/api/project-access?d=uuid-1", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if req.URL.Path != "/api/project-access" || req.URL.RawQuery != "d=uuid-1" {
			t.Fatalf("URL 解析错误: %s", req.URL)
		}
	})

	t.Run("headers", func(t *testing.T) {
		req, err := cl.NewRequest(context.Background(), http.MethodPost, "/x", nil,
			map[string]string{"X-Test": "1"})
		if err != nil {
			t.Fatal(err)
		}
		if req.Header.Get("X-Test") != "1" {
			t.Fatal("请求头未设置")
		}
	})
}

// TestDo_BodyReplayOnRetry 验证重试时请求体可以被重新读取。
//
// 这是 []byte 路径的关键收益：如果 body 是一次性 reader，
// 重试会发出一个空 body，上游会返回一个看起来毫不相关的错误。
func TestDo_BodyReplayOnRetry(t *testing.T) {
	var bodies []string
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		n++
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	cfg := testCfg(srv.URL)
	cfg.MaxRetries = 2
	cfg.RetryBackoff = time.Millisecond
	cl, err := New(cfg, Options{})
	if err != nil {
		t.Fatal(err)
	}

	cl2 := &Client{HTTP: cl.HTTP, BaseURL: cl.BaseURL, transport: cl.transport, dialer: cl.dialer}
	resp, err := doWithRetry(t, cl2, srv.URL, []byte(`{"model":"gpt-5"}`), cfg)
	if err != nil {
		t.Fatalf("Do 失败: %v", err)
	}
	defer resp.Body.Close()

	if len(bodies) != 2 {
		t.Fatalf("应当请求 2 次，实际 %d 次", len(bodies))
	}
	for i, b := range bodies {
		if b != `{"model":"gpt-5"}` {
			t.Fatalf("第 %d 次请求的 body = %q（重试丢失了请求体）", i+1, b)
		}
	}
}

func doWithRetry(t *testing.T, cl *Client, base string, body []byte, cfg config.UpstreamConfig) (*http.Response, error) {
	t.Helper()
	// 这里直接驱动重试逻辑，保持与 prism.Client.Do 相同的语义。
	var lastErr error
	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		req, err := cl.NewRequest(context.Background(), http.MethodPost, "/x", body, nil)
		if err != nil {
			return nil, err
		}
		resp, err := cl.HTTP.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if RetryableStatus(resp.StatusCode) && attempt < cfg.MaxRetries {
			drain(resp)
			continue
		}
		return resp, nil
	}
	return nil, lastErr
}

func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

func TestIsNetworkError(t *testing.T) {
	if IsNetworkError(nil) {
		t.Fatal("nil 不是网络错误")
	}
	if !IsNetworkError(ErrUpstreamUnreachable) {
		t.Fatal("sentinel 应被识别")
	}
	if !IsNetworkError(&url.Error{Op: "Get", URL: "x", Err: errors.New("connection reset by peer")}) {
		t.Fatal("reset 应被识别为网络错误")
	}
	if !IsNetworkError(errors.New("dial tcp: i/o timeout")) {
		t.Fatal("超时应被识别")
	}
	if IsNetworkError(errors.New("invalid json in request")) {
		t.Fatal("业务错误不应被当成网络错误（会导致无意义重试）")
	}
}

func TestRetryableStatus(t *testing.T) {
	retryable := []int{408, 429, 500, 502, 503, 504}
	for _, c := range retryable {
		if !RetryableStatus(c) {
			t.Errorf("%d 应当可重试", c)
		}
	}
	// 4xx 是确定性错误，重试只是浪费上游配额。
	notRetryable := []int{400, 401, 403, 404, 422}
	for _, c := range notRetryable {
		if RetryableStatus(c) {
			t.Errorf("%d 不应重试", c)
		}
	}
}

func TestSleepCtx_Cancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := SleepCtx(ctx, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("已取消的 context 应当立刻返回: %v", err)
	}
	if err := SleepCtx(context.Background(), 0); err != nil {
		t.Fatalf("0 时长应当直接返回: %v", err)
	}
}

func TestNewRequest_ReaderBody(t *testing.T) {
	cl, _ := New(testCfg("https://example.com"), Options{})

	// net/http 会识别 *bytes.Reader / *strings.Reader 从而补上 ContentLength。
	// 对不透明 reader，Go 刻意把 ContentLength 留成 0 而不是 -1
	// （历史兼容原因，见 golang/go#18117）；transport 会把
	// "ContentLength==0 且 Body!=nil" 理解为未知长度并改用 chunked。
	// 这条断言把这个反直觉的行为固化下来，免得后人误以为 0 就是"空 body"。
	req, err := cl.NewRequest(context.Background(), http.MethodPost, "/x", opaqueReader{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if req.ContentLength != 0 || req.Body == nil {
		t.Fatalf("未知长度 body 应为 ContentLength=0 且 Body!=nil（chunked），得到 CL=%d Body=%v",
			req.ContentLength, req.Body)
	}

	// 已知长度的 reader 走快路径，应当带上 ContentLength。
	req2, err := cl.NewRequest(context.Background(), http.MethodPost, "/x", strings.NewReader("hi"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if req2.ContentLength != 2 {
		t.Fatalf("已知长度 body 的 ContentLength 应为 2，得到 %d", req2.ContentLength)
	}
}

type opaqueReader struct{}

func (opaqueReader) Read([]byte) (int, error) { return 0, io.EOF }

func TestNew_BadBaseURL(t *testing.T) {
	cfg := testCfg("://bad")
	if _, err := New(cfg, Options{}); err == nil {
		t.Fatal("非法 BaseURL 应当报错")
	}
}

func TestWarmup(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cl, _ := New(testCfg(srv.URL), Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 预热只是"建立连接"，不关心握手是否完全成功，
	// 因此不断言具体数量，只断言它不会 panic 且不阻塞。
	got := cl.Warmup(ctx, 4)
	if got < 0 || got > 4 {
		t.Fatalf("预热数量异常: %d", got)
	}
	cl.CloseIdle()
}

func TestPick(t *testing.T) {
	if pick("a", "b") != "b" {
		t.Fatal("b 非空时应优先 b")
	}
	if pick("a", "") != "a" {
		t.Fatal("b 为空时应回退到 a")
	}
}

func TestProxyFunc(t *testing.T) {
	if f, err := proxyFunc(""); f != nil || err != nil {
		t.Fatal("空代理应返回 nil（直连）")
	}
	if f, err := proxyFunc("http://127.0.0.1:7890"); f == nil || err != nil {
		t.Fatal("http 代理应被识别")
	} else {
		req, _ := http.NewRequest(http.MethodGet, "https://x.com", nil)
		u, err := f(req)
		if err != nil || u.Host != "127.0.0.1:7890" {
			t.Fatalf("代理 URL 错误: %v %v", u, err)
		}
	}
	for _, raw := range []string{"not-a-url", "http://", "ftp://localhost:21", "://bad"} {
		if _, err := proxyFunc(raw); err == nil {
			t.Errorf("invalid proxy accepted: %q", raw)
		}
	}
	for _, scheme := range []string{"socks5", "socks5h"} {
		if f, err := proxyFunc(scheme + "://127.0.0.1:1080"); f == nil || err != nil {
			t.Errorf("SOCKS proxy rejected: %v", err)
		}
	}
}

func TestIsDefaultPort(t *testing.T) {
	cases := map[string]bool{
		"https://a.com":      true,
		"https://a.com:443":  true,
		"http://a.com:80":    true,
		"https://a.com:8443": false,
	}
	for raw, want := range cases {
		u, _ := url.Parse(raw)
		if got := isDefaultPort(u); got != want {
			t.Errorf("isDefaultPort(%s) = %v, want %v", raw, got, want)
		}
	}
}
