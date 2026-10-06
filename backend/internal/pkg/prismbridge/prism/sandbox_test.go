package prism

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSandboxPath_IsRelative 是回归测试。
//
// 背景：httpc.NewRequest 是把 path **拼接**到 BaseURL.Path 上的，
// 所以如果直接把沙箱的绝对 URL（https://host/s/sandboxes/proxy）传下去，
// 最终会拼出 "/https://prism.openai.com/s/sandboxes/proxy/..." 这种畸形路径。
// 服务端只会回 404，而且症状是"端点不存在"——极难联想到是 URL 拼接问题。
//
// 沙箱代理与上游同域，所以正确做法是只取 path，复用同一个 HTTP 客户端
// （连接池、HTTP/2、Cookie 注入全在里面）。
func TestSandboxPath_IsRelative(t *testing.T) {
	sb := &Sandbox{URL: "https://prism.openai.com/s/sandboxes/proxy", Token: "t"}

	got := sandboxPath(sb, "resources-token")
	want := "/s/sandboxes/proxy/resources-token"
	if got != want {
		t.Fatalf("sandboxPath = %q, want %q", got, want)
	}
	if strings.Contains(got, "://") {
		t.Fatalf("绝不能把绝对 URL 拼进 path：%q", got)
	}
}

func TestSandboxPath_TrailingSlash(t *testing.T) {
	// 上游返回的 url 末尾带斜杠（实测就是 /s/sandboxes/proxy/），
	// 不能因此拼出双斜杠。
	sb := &Sandbox{URL: "https://prism.openai.com/s/sandboxes/proxy/"}
	if got := sandboxPath(sb, "/wait-for-sync"); got != "/s/sandboxes/proxy/wait-for-sync" {
		t.Fatalf("sandboxPath = %q", got)
	}
}

func TestSandboxPath_QueryPreserved(t *testing.T) {
	// wait-for-sync 需要 query，不能被 TrimRight 吃掉。
	sb := &Sandbox{URL: "https://prism.openai.com/s/sandboxes/proxy/"}
	got := sandboxPath(sb, "wait-for-sync?wait_ms=10000")
	if !strings.HasSuffix(got, "/wait-for-sync?wait_ms=10000") {
		t.Fatalf("query 丢失: %q", got)
	}
}

func TestSandboxResourcesBaseURL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://prism.openai.com/s/sandboxes/proxy/", "https://prism.openai.com/s/sandbox-resources"},
		{"https://prism.openai.com/s/sandboxes/proxy", "https://prism.openai.com/s/sandbox-resources"},
		{"https://x/sandboxes/proxy/", "https://x/sandbox-resources"},
		{"https://x/unknown", ""},
	}
	for _, c := range cases {
		if got := sandboxResourcesBaseURL(c.in); got != c.want {
			t.Errorf("sandboxResourcesBaseURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestSandboxSyncStatus_Ready 固化"什么算就绪"。
//
// 判定规则照抄真实前端的 lr()：它把"旧协议 / 新协议"和
// "缺哪个令牌"分得很细，自己发明一套只会得到假就绪或假超时。
func TestSandboxSyncStatus_Ready(t *testing.T) {
	mk := func(status string, caps []string, yToken, syncedProvider bool) *SandboxSyncStatus {
		st := &SandboxSyncStatus{Status: status, ReadinessCapabilities: caps}
		st.Tokens.HasCurrentYSweetToken = yToken
		st.Tokens.HasSyncedYSweetProvider = syncedProvider
		return st
	}

	cases := []struct {
		name string
		st   *SandboxSyncStatus
		want bool
	}{
		{
			name: "旧协议：synced + 有 y-sweet 令牌即就绪",
			st:   mk("synced", nil, true, false),
			want: true,
		},
		{
			name: "旧协议：还在 syncing 不算就绪",
			st:   mk("syncing", nil, true, false),
			want: false,
		},
		{
			name: "旧协议：没有 y-sweet 令牌不算就绪",
			st:   mk("synced", nil, false, false),
			want: false,
		},
		{
			name: "新协议：两项都满足才就绪",
			st:   mk("synced", []string{"current_y_sweet_provider"}, true, true),
			want: true,
		},
		{
			name: "新协议：provider 未同步完不算就绪",
			st:   mk("synced", []string{"current_y_sweet_provider"}, true, false),
			want: false,
		},
		{
			name: "新协议：只有 token 但还在 syncing",
			st:   mk("syncing", []string{"current_y_sweet_provider"}, true, true),
			want: false,
		},
		{
			name: "failed 永远是失败",
			st:   mk("failed", []string{"current_y_sweet_provider"}, true, true),
			want: false,
		},
		{
			name: "能力列表里没有 y-sweet provider 时退化为旧协议判定",
			st:   mk("synced", []string{"something_else"}, true, false),
			want: true,
		},
	}
	for _, c := range cases {
		if got := c.st.Ready(); got != c.want {
			t.Errorf("%s: Ready() = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestSandboxSyncStatus_Parse 覆盖实测到的真实响应（缺凭证时的样子）。
func TestSandboxSyncStatus_Parse(t *testing.T) {
	raw := `{"status":"syncing","readinessCapabilities":["current_y_sweet_provider"],
		"tokens":{"hasResourceToken":false,"hasResourceBaseUrl":false,
		          "hasResourceProjectId":true,"hasCurrentYSweetToken":false,
		          "hasSyncedYSweetProvider":false,"fileCredentialSource":"resources-token"}}`
	var st SandboxSyncStatus
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		t.Fatal(err)
	}
	if st.Ready() {
		t.Error("缺 y-sweet 令牌时不该判为就绪")
	}
	if !st.NeedsYSweetProvider() {
		t.Error("应识别出 current_y_sweet_provider 能力")
	}
	if !st.Tokens.HasResourceProjectID {
		t.Error("hasResourceProjectId 解析失败")
	}
	if st.Tokens.FileCredentialSource != "resources-token" {
		t.Errorf("fileCredentialSource = %q", st.Tokens.FileCredentialSource)
	}
}

// TestYSweetTokenRawPreserved 确保转发用的是原始 JSON。
//
// 用 Raw 而不是重新序列化结构体：上游新增字段时不会被我方结构体吃掉。
func TestYSweetTokenRawPreserved(t *testing.T) {
	raw := `{"docId":"d1","url":"wss://x/y","baseUrl":"https://x/y",
	         "authorization":"full","token":"tk","futureField":{"a":1}}`
	var tk YSweetToken
	if err := json.Unmarshal([]byte(raw), &tk); err != nil {
		t.Fatal(err)
	}
	tk.Raw = json.RawMessage(raw)

	// 结构体不认识 futureField，但 Raw 必须完整保留它。
	if !strings.Contains(string(tk.Raw), "futureField") {
		t.Fatal("Raw 未保留未知字段，转发时会丢失上游数据")
	}
	if tk.DocID != "d1" || tk.Token != "tk" || tk.Authorization != "full" {
		t.Errorf("常用字段解析错误: %+v", tk)
	}
}

func TestResourceTokenExpiry(t *testing.T) {
	// 两个字段都给时取更早的，保守估计不会让令牌在途中失效。
	rt := &ResourceToken{ExpiresAt: 1000000, MaxAgeSeconds: 3600}
	if got := rt.Expiry(); got.Unix() != 1000000 {
		t.Errorf("应取 exp（更早的那个），得到 %v", got)
	}
	// 只给 max_age_seconds 时按当前时间推算。
	rt2 := &ResourceToken{MaxAgeSeconds: 60}
	if got := rt2.Expiry(); got.IsZero() {
		t.Error("只给 max_age_seconds 时也应算出过期时间")
	}
	// 都没有则应返回零值，由调用方决定不解缓存。
	if got := (&ResourceToken{}).Expiry(); !got.IsZero() {
		t.Errorf("无过期信息时应返回零值，得到 %v", got)
	}
	if got := (*ResourceToken)(nil).Expiry(); !got.IsZero() {
		t.Error("nil 接收者不应 panic")
	}
}
