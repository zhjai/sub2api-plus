package creds

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/config"
)

// makeJWT 造一个只用于测试的 JWT（不签名，因为我们只读 payload）。
func makeJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	body, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	payload := base64.RawURLEncoding.EncodeToString(body)
	return header + "." + payload + ".sig"
}

func TestParseJWT(t *testing.T) {
	exp := time.Now().Add(2 * time.Hour).Unix()
	tok := makeJWT(t, map[string]any{
		"exp":   exp,
		"sub":   "user-123",
		"email": "a@b.com",
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": "acc-1",
			"chatgpt_plan_type":  "plus",
		},
	})

	got, ok := TokenExpiry(tok)
	if !ok {
		t.Fatal("TokenExpiry 未能解析出过期时间")
	}
	if got.Unix() != exp {
		t.Fatalf("exp = %v, want %v", got.Unix(), exp)
	}

	c := &Credential{AccessToken: tok}
	c.applyJWT(tok)
	if c.Email != "a@b.com" || c.UserID != "user-123" {
		t.Fatalf("基本声明未解析: %+v", c)
	}
	if c.AccountID != "acc-1" || c.Plan != "plus" {
		t.Fatalf("自定义 claim 未解析: %+v", c)
	}
	if c.ExpiresAt.Unix() != exp {
		t.Fatalf("过期时间未设置: %v", c.ExpiresAt)
	}
}

func TestParseJWT_Errors(t *testing.T) {
	for _, bad := range []string{"", "abc", "a.b"} {
		if _, err := ParseJWT(bad); err == nil {
			t.Errorf("ParseJWT(%q) 应当报错", bad)
		}
	}
}

func TestCookieValue(t *testing.T) {
	h := "a=1; __Secure-next-auth.session-token=TOKEN123; b=2"
	if got := CookieValue(h, CookieSessionToken); got != "TOKEN123" {
		t.Fatalf("cookie 取值错误: %q", got)
	}
	if got := CookieValue(h, CookieSessionToken, CookieAuthSession); got != "TOKEN123" {
		t.Fatalf("多候选取值错误: %q", got)
	}
	if got := CookieValue(h, "missing"); got != "" {
		t.Fatalf("缺失 cookie 应返回空串: %q", got)
	}
	if got := CookieValue("", CookieSessionToken); got != "" {
		t.Fatalf("空 header 应返回空串: %q", got)
	}
	// 大小写不敏感
	if got := CookieValue("__secure-next-auth.session-token=lower", CookieSessionToken); got != "lower" {
		t.Fatalf("cookie 名应当大小写不敏感: %q", got)
	}
}

func TestMergeCookie(t *testing.T) {
	base := "a=1; __Secure-next-auth.session-token=OLD; b=2"
	got := MergeCookie(base, &http.Cookie{Name: CookieSessionToken, Value: "NEW"})
	if !strings.Contains(got, CookieSessionToken+"=NEW") {
		t.Fatalf("未替换旧值: %q", got)
	}
	if strings.Contains(got, "OLD") {
		t.Fatalf("旧值仍存在: %q", got)
	}
	if !strings.Contains(got, "a=1") || !strings.Contains(got, "b=2") {
		t.Fatalf("其他 cookie 丢失: %q", got)
	}

	// 新增不存在的 cookie
	got2 := MergeCookie("a=1", &http.Cookie{Name: "c", Value: "3"})
	if !strings.Contains(got2, "c=3") {
		t.Fatalf("新增 cookie 失败: %q", got2)
	}
}

func TestFromAccountConfig_AllForms(t *testing.T) {
	exp := time.Now().Add(time.Hour)

	t.Run("整串 cookie", func(t *testing.T) {
		c := FromAccountConfig(config.AccountConfig{
			ID:      "x",
			Cookies: "__Secure-next-auth.session-token=sess; oai-did=dev1",
		})
		if c.SessionToken != "sess" {
			t.Fatalf("未从 cookie 提取 session token: %+v", c)
		}
		if c.AccountID != "dev1" {
			t.Fatalf("未从 cookie 提取 device id: %+v", c)
		}
		if !c.CanRefresh() {
			t.Fatal("有 session cookie 就应当具备自愈能力")
		}
	})

	t.Run("cookie_map", func(t *testing.T) {
		c := FromAccountConfig(config.AccountConfig{
			ID:        "x",
			CookieMap: map[string]string{CookieSessionToken: "frommap"},
		})
		if c.SessionToken != "frommap" {
			t.Fatalf("cookie_map 未生效: %+v", c)
		}
	})

	t.Run("只有 access token", func(t *testing.T) {
		tok := makeJWT(t, map[string]any{"exp": exp.Unix(), "email": "e@x.com"})
		c := FromAccountConfig(config.AccountConfig{ID: "x", AccessToken: tok})
		if c.Email != "e@x.com" {
			t.Fatalf("JWT 未解析: %+v", c)
		}
		if c.CanRefresh() {
			t.Fatal("只有 access token 不应具备自愈能力")
		}
		if !c.Usable() {
			t.Fatal("有 access token 就应当可用")
		}
	})

	t.Run("refresh token", func(t *testing.T) {
		c := FromAccountConfig(config.AccountConfig{ID: "x", RefreshToken: "rt"})
		if c.Source != "oauth" {
			t.Fatalf("来源标记应为 oauth: %q", c.Source)
		}
		if !c.CanRefresh() {
			t.Fatal("有 refresh token 必须可以自愈")
		}
		// 只有 refresh token 时还不能直接发请求，需要先换一次。
		if c.Usable() {
			t.Fatal("仅 refresh token 时不应被认为可直接使用")
		}
	})

	t.Run("显式过期时间优先", func(t *testing.T) {
		tok := makeJWT(t, map[string]any{"exp": exp.Unix()})
		c := FromAccountConfig(config.AccountConfig{
			ID:          "x",
			AccessToken: tok,
			ExpiresAt:   &exp,
		})
		if !c.ExpiresAt.Equal(exp) {
			t.Fatalf("显式过期时间未生效: %v", c.ExpiresAt)
		}
	})
}

func TestCredential_NeedsRefresh(t *testing.T) {
	now := time.Now()
	c := &Credential{ExpiresAt: now.Add(3 * time.Minute)}
	if !c.NeedsRefresh(now, 5*time.Minute) {
		t.Fatal("距过期 3 分钟、提前 5 分钟刷新 -> 应当需要刷新")
	}
	if c.NeedsRefresh(now, time.Minute) {
		t.Fatal("距过期 3 分钟、提前 1 分钟刷新 -> 不应刷新")
	}
	// 无过期信息时保守地不刷新，交给 401 兜底。
	unknown := &Credential{}
	if unknown.NeedsRefresh(now, time.Hour) {
		t.Fatal("未知过期时间不应触发刷新")
	}
	if unknown.Expired(now) {
		t.Fatal("未知过期时间不应判为已过期")
	}
}

func TestCredential_EffectiveCookie(t *testing.T) {
	// 未记录来源时，默认按 Prism 自己的 cookie 名拼 ——
	// 用错名字（比如 next-auth 的）上游会认为"没有会话"，
	// 表现为一次莫名其妙的 401。
	c := &Credential{SessionToken: "s1"}
	got := c.EffectiveCookie()
	if !strings.Contains(got, CookiePrismSessionToken+"=s1") {
		t.Fatalf("未按 Prism 的 cookie 名拼出会话: %q", got)
	}

	// 记录来源时以记录为准（兼容别的站点）。
	c2 := &Credential{SessionToken: "s2", SessionCookieName: CookieSessionToken}
	if !strings.Contains(c2.EffectiveCookie(), CookieSessionToken+"=s2") {
		t.Fatalf("未按记录的 cookie 名拼出会话: %q", c2.EffectiveCookie())
	}

	// 已有完整 CookieHeader 时原样返回。
	c3 := &Credential{CookieHeader: "x=1", SessionToken: "s3"}
	if c3.EffectiveCookie() != "x=1" {
		t.Fatalf("应原样返回 CookieHeader: %q", c3.EffectiveCookie())
	}
}

func TestCredential_CloneIsDeep(t *testing.T) {
	c := &Credential{Headers: map[string]string{"a": "1"}}
	n := c.Clone()
	n.Headers["a"] = "2"
	n.Email = "new"
	if c.Headers["a"] != "1" {
		t.Fatal("Clone 应当深拷贝 Headers")
	}
	if c.Email == "new" {
		t.Fatal("Clone 应当拷贝值字段")
	}
}

func TestIsAuthError(t *testing.T) {
	if !IsAuthError(&APIError{Status: 401}) {
		t.Fatal("401 应判为认证错误")
	}
	if !IsAuthError(&APIError{Status: 403}) {
		t.Fatal("403 应判为认证错误")
	}
	if IsAuthError(&APIError{Status: 500}) {
		t.Fatal("500 不是认证错误")
	}
	if IsAuthError(nil) {
		t.Fatal("nil 不是认证错误")
	}
	if !IsRateLimited(&APIError{Status: 429}) {
		t.Fatal("429 应判为限流")
	}
}

func TestAPIError_RetryAfterRendered(t *testing.T) {
	e := &APIError{Op: "op", Status: 429, Body: "slow down", RetryAfter: 5 * time.Second}
	if !strings.Contains(e.Error(), "retry-after") {
		t.Fatalf("Retry-After 应出现在错误信息里: %s", e.Error())
	}
}

// TestFromAccountConfig_PrismCookies 是回归测试。
//
// 背景：我最初假设 Prism 用 next-auth 的 __Secure-next-auth.session-token，
// 结果实测（DevTools 的 Cookie 表）发现根本不是 —— Prism 用的是
// 自己的一套 prism_* cookie：
//
//	prism_oai_access_token   ← 真正的 Bearer JWT
//	prism_session_token      ← Prism 自己的会话 JWT
//	prism_oai_refresh_token  ← OAuth refresh token（可自动续期）
//	prism-did                ← 设备标识
//
// 少了这条识别，用户把完整 Cookie 串贴进来也会被判成"不可用"。
func TestFromAccountConfig_PrismCookies(t *testing.T) {
	header := "prism-did=dev-1; " +
		"prism_oai_access_token=eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ1IiwiZXhwIjoxOTAwMDAwMDAwfQ.s; " +
		"prism_session_token=sess-jwt; " +
		"prism_oai_refresh_token=rt.1.AAAai9xd_xyz; " +
		"oai-did=dev-2"

	c := FromAccountConfig(config.AccountConfig{ID: "p", Cookies: header})

	if c.AccessToken == "" {
		t.Fatal("未从 Cookie 串里取出 prism_oai_access_token")
	}
	if c.SessionToken != "sess-jwt" {
		t.Errorf("SessionToken = %q（应取 prism_session_token）", c.SessionToken)
	}
	if c.RefreshToken != "rt.1.AAAai9xd_xyz" {
		t.Errorf("RefreshToken = %q", c.RefreshToken)
	}
	if c.AccountID != "dev-1" {
		t.Errorf("AccountID = %q（prism-did 应优先于 oai-did）", c.AccountID)
	}
	if !c.Usable() {
		t.Fatal("含 access token 的凭据应当可用")
	}
	if !c.CanRefresh() {
		t.Error("有 prism_session_token 与 refresh_token，应当具备自愈能力")
	}
	// 应该就地解析出 JWT 的过期时间，省掉一次 /api/auth/session 往返。
	if c.ExpiresAt.IsZero() {
		t.Error("未从 prism_oai_access_token 解析出过期时间")
	}
}

// TestEffectiveCookie_Prism 验证只有零散字段时也能拼出可用的 Cookie 头。
func TestEffectiveCookie_Prism(t *testing.T) {
	c := &Credential{
		AccessToken:  "tok",
		SessionToken: "sess",
		RefreshToken: "rt",
		AccountID:    "dev",
	}
	got := c.EffectiveCookie()
	for _, want := range []string{
		CookiePrismSessionToken + "=sess",
		CookiePrismRefreshToken + "=rt",
		CookieDeviceID + "=dev",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Cookie 头缺少 %s\n实际: %s", want, got)
		}
	}
}
