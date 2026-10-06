package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	bridgeconfig "github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/creds"
)

const prismOAuthClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
const prismOAuthRedirectURI = "http://localhost:1455/auth/callback"
const prismOAuthTTL = 15 * time.Minute

// The browser redirects to its own localhost. Remote deployments paste that URL;
// no listener is opened on the server and no cross-domain HttpOnly cookies are read.
type PrismOAuthStatus struct {
	SessionID      string    `json:"session_id"`
	AuthorizeURL   string    `json:"authorize_url,omitempty"`
	RedirectURI    string    `json:"redirect_uri,omitempty"`
	ExpiresAt      time.Time `json:"expires_at"`
	Status         string    `json:"status"`
	LoginSucceeded bool      `json:"login_succeeded"`
	PrismVerified  bool      `json:"prism_verified"`
	AccountID      int64     `json:"account_id,omitempty"`
	Code           string    `json:"code,omitempty"`
	Message        string    `json:"message,omitempty"`
}
type prismOAuthSession struct {
	result   PrismOAuthStatus
	state    string
	verifier string
	req      PrismImportRequest
	cancel   context.CancelFunc
}

func prismRandom(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", prismError("random_unavailable", "无法生成安全授权会话")
	}
	return hex.EncodeToString(b), nil
}
func (s *PrismAccountService) BeginOAuth(ctx context.Context, req PrismImportRequest) (PrismOAuthStatus, error) {
	if req.Concurrency != nil && (*req.Concurrency < 1 || *req.Concurrency > 1000) || req.Priority != nil && *req.Priority < 0 {
		return PrismOAuthStatus{}, prismError("invalid_settings", "并发数需为 1 至 1000，优先级不能为负")
	}
	if req.AccountID > 0 {
		a, err := s.admin.GetAccount(ctx, req.AccountID)
		if err != nil || a.Platform != "prism" {
			return PrismOAuthStatus{}, prismError("invalid_account", "Prism 账号不存在")
		}
		req.ProxyID = a.ProxyID
		req.expectedFingerprint = prismAccountFingerprint(a)
	}
	req.Cookies = ""
	req.Accounts = nil
	id, err := prismRandom(24)
	if err != nil {
		return PrismOAuthStatus{}, err
	}
	state, err := prismRandom(32)
	if err != nil {
		return PrismOAuthStatus{}, err
	}
	verifier, err := prismRandom(64)
	if err != nil {
		return PrismOAuthStatus{}, err
	}
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{"client_id": {prismOAuthClientID}, "response_type": {"code"}, "redirect_uri": {prismOAuthRedirectURI}, "scope": {"openid profile email offline_access"}, "state": {state}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}, "id_token_add_organizations": {"true"}, "codex_cli_simplified_flow": {"true"}}
	result := PrismOAuthStatus{SessionID: id, AuthorizeURL: "https://auth.openai.com/oauth/authorize?" + q.Encode(), RedirectURI: prismOAuthRedirectURI, ExpiresAt: time.Now().Add(prismOAuthTTL), Status: "pending"}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, old := range s.sessions {
		if time.Now().After(old.result.ExpiresAt) {
			if old.cancel != nil {
				old.cancel()
			}
			delete(s.sessions, k)
		}
	}
	if len(s.sessions) >= 128 {
		return PrismOAuthStatus{}, prismError("too_many_sessions", "待处理授权会话过多，请取消旧会话后再试")
	}
	s.sessions[id] = &prismOAuthSession{result: result, state: state, verifier: verifier, req: req}
	return result, nil
}
func (s *PrismAccountService) OAuthStatus(id string) (PrismOAuthStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.sessions[id]
	if sess == nil {
		return PrismOAuthStatus{}, prismError("session_not_found", "授权会话不存在或已过期")
	}
	s.expireOAuth(sess)
	return sess.result, nil
}
func (s *PrismAccountService) expireOAuth(sess *prismOAuthSession) {
	if time.Now().After(sess.result.ExpiresAt) && (sess.result.Status == "pending" || sess.result.Status == "exchanging") {
		sess.result.Status = "expired"
		sess.result.Code = "session_expired"
		sess.result.Message = "授权会话已过期，请重新登录"
		sess.verifier = ""
		if sess.cancel != nil {
			sess.cancel()
		}
	}
}
func (s *PrismAccountService) CancelOAuth(id string) (PrismOAuthStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.sessions[id]
	if sess == nil {
		return PrismOAuthStatus{}, prismError("session_not_found", "授权会话不存在或已过期")
	}
	s.expireOAuth(sess)
	if sess.result.Status == "pending" || sess.result.Status == "exchanging" {
		sess.result.Status = "canceled"
		sess.result.Code = "canceled"
		sess.result.Message = "授权已取消"
		sess.verifier = ""
		if sess.cancel != nil {
			sess.cancel()
		}
	}
	return sess.result, nil
}

func parsePrismCallback(raw, state string) (string, error) {
	if len(raw) > 16<<10 {
		return "", prismError("invalid_callback", "授权回调地址过长")
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "http" || u.Host != "localhost:1455" || u.Path != "/auth/callback" || u.User != nil || u.Fragment != "" {
		return "", prismError("invalid_callback", "请粘贴完整的 localhost:1455/auth/callback 回调地址")
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(q["state"]) != 1 || q.Get("state") != state {
		return "", prismError("state_mismatch", "授权 state 不匹配，请使用本次授权的回调地址")
	}
	if q.Get("error") != "" {
		return "", prismError("authorization_denied", "浏览器授权被拒绝，请重新登录")
	}
	if len(q["code"]) != 1 || q.Get("code") == "" {
		return "", prismError("invalid_callback", "回调地址缺少唯一授权 code")
	}
	return q.Get("code"), nil
}
func (s *PrismAccountService) ExchangeOAuth(ctx context.Context, id, callback string) (PrismOAuthStatus, error) {
	s.mu.Lock()
	sess := s.sessions[id]
	if sess == nil {
		s.mu.Unlock()
		return PrismOAuthStatus{}, prismError("session_not_found", "授权会话不存在或已过期")
	}
	s.expireOAuth(sess)
	if sess.result.Status != "pending" {
		out := sess.result
		s.mu.Unlock()
		return out, prismError("session_already_used", "授权会话已提交、取消或过期，请查看状态")
	}
	code, err := parsePrismCallback(callback, sess.state)
	if err != nil {
		s.mu.Unlock()
		return PrismOAuthStatus{}, err
	}
	workCtx, cancel := context.WithDeadline(ctx, sess.result.ExpiresAt)
	sess.cancel = cancel
	sess.result.Status = "exchanging"
	verifier := sess.verifier
	sess.verifier = ""
	req := sess.req
	s.mu.Unlock()
	defer cancel()
	c, err := s.exchangeOAuthToken(workCtx, code, verifier, req.ProxyID)
	loginSucceeded := err == nil
	identity := ""
	if err == nil {
		verifyCtx, stop := context.WithTimeout(workCtx, 45*time.Second)
		c, identity, _, err = s.verify(verifyCtx, c, req.ProxyID)
		stop()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireOAuth(sess)
	if sess.result.Status != "exchanging" {
		return sess.result, nil
	}
	sess.result.LoginSucceeded = loginSucceeded
	if err == nil {
		var saved int64
		saved, _, err = s.save(workCtx, req, c, identity)
		if err == nil {
			sess.result.AccountID = saved
			sess.result.PrismVerified = true
			sess.result.Status = "completed"
		}
	}
	if err != nil {
		safe := safePrismError(err).(*PrismAccountError)
		sess.result.Status = "failed"
		sess.result.Code = safe.Code
		sess.result.Message = safe.Message
	}
	sess.cancel = nil
	return sess.result, nil
}
func (s *PrismAccountService) exchangeOAuthToken(ctx context.Context, code, verifier string, proxyID *int64) (*creds.Credential, error) {
	a, err := s.temporaryAccount(ctx, &creds.Credential{}, proxyID)
	if err != nil {
		return nil, err
	}
	_, p, close, err := PrismAccountPrincipal(ctx, a)
	if err != nil {
		return nil, err
	}
	defer close()
	payload, _ := json.Marshal(map[string]string{"grant_type": "authorization_code", "client_id": prismOAuthClientID, "code": code, "redirect_uri": prismOAuthRedirectURI, "code_verifier": verifier})
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://auth.openai.com/oauth/token", strings.NewReader(string(payload)))
	if err != nil {
		return nil, safePrismError(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := p.Client.HTTP.Do(req)
	if err != nil {
		return nil, safePrismError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, safePrismError(&creds.APIError{Status: resp.StatusCode})
	}
	var tok creds.OAuthResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tok); err != nil || tok.AccessToken == "" || tok.Error != "" {
		return nil, prismError("token_exchange_failed", "授权服务器未返回有效登录凭据")
	}
	c := creds.FromAccountConfig(bridgeconfig.AccountConfig{AccessToken: tok.AccessToken, RefreshToken: tok.RefreshToken})
	c.Headers = map[string]string{"oauth_client_id": prismOAuthClientID}
	if tok.ExpiresIn > 0 {
		c.ExpiresAt = time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	}
	return c, nil
}
