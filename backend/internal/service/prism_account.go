package service

// Prism account adapter. Protocol and PKCE behavior follow oai-prism a97dbdc
// (MIT); credentials stay in the existing Sub2API account repository.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	entaccount "github.com/Wei-Shaw/sub2api/ent/account"
	entgroup "github.com/Wei-Shaw/sub2api/ent/group"
	bridgeconfig "github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/creds"
	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/httpc"
	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/prism"
	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/upstream"
	"golang.org/x/sync/singleflight"
)

type PrismAccountError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *PrismAccountError) Error() string  { return e.Message }
func prismError(code, message string) error { return &PrismAccountError{code, message} }
func safePrismError(err error) error {
	if err == nil {
		return nil
	}
	var pe *PrismAccountError
	if errors.As(err, &pe) {
		return pe
	}
	var ae *creds.APIError
	var se *upstream.SessionVerificationError
	if errors.As(err, &se) {
		err = &creds.APIError{Status: se.Status, RetryAfter: se.RetryAfter}
	}
	if errors.As(err, &ae) {
		switch ae.Status {
		case 401:
			return prismError("credentials_expired", "登录凭据已失效，请重新登录")
		case 403:
			return prismError("upstream_forbidden", "Prism 拒绝验证，请检查账号权限及代理出口")
		case 429:
			return prismError("rate_limited", "Prism 请求过于频繁，请稍后重试")
		}
		return prismError("upstream_error", fmt.Sprintf("Prism 验证失败（HTTP %d）", ae.Status))
	}
	if errors.Is(err, context.Canceled) {
		return prismError("canceled", "操作已取消")
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return prismError("timeout", "Prism 验证超时，请稍后重试")
	}
	return prismError("upstream_error", "Prism 验证失败，请检查网络、代理与登录凭据")
}

type PrismImportEntry struct {
	Cookies string `json:"cookies"`
	Name    string `json:"name"`
}
type PrismImportRequest struct {
	Cookies             string             `json:"cookies"`
	Accounts            []PrismImportEntry `json:"accounts"`
	Name                string             `json:"name"`
	GroupIDs            []int64            `json:"group_ids"`
	ProxyID             *int64             `json:"proxy_id"`
	Concurrency         *int               `json:"concurrency"`
	Priority            *int               `json:"priority"`
	UpdateExisting      *bool              `json:"update_existing"`
	Verify              *bool              `json:"verify"`
	AccountID           int64              `json:"account_id,omitempty"`
	expectedFingerprint string
}
type PrismImportItem struct {
	Index     int    `json:"index"`
	Action    string `json:"action"`
	AccountID int64  `json:"account_id,omitempty"`
	Code      string `json:"code,omitempty"`
	Message   string `json:"message,omitempty"`
}
type PrismImportResult struct {
	Total   int               `json:"total"`
	Created int               `json:"created"`
	Updated int               `json:"updated"`
	Failed  int               `json:"failed"`
	Items   []PrismImportItem `json:"items"`
}

type PrismAccountService struct {
	admin          AdminService
	mu             sync.Mutex
	sessions       map[string]*prismOAuthSession
	refreshFlights singleflight.Group
	refreshToken   func(context.Context, *Account, *creds.Credential) (*creds.Credential, error)
	verifyToken    func(context.Context, *Account, *creds.Credential) (*creds.Credential, error)
}

func NewPrismAccountService(admin AdminService) *PrismAccountService {
	return &PrismAccountService{admin: admin, sessions: map[string]*prismOAuthSession{}}
}

func prismString(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return strings.TrimSpace(v)
}
func prismCredential(a *Account) *creds.Credential {
	c := creds.FromAccountConfig(bridgeconfig.AccountConfig{Cookies: prismString(a.Credentials, "cookies"), AccessToken: prismString(a.Credentials, "access_token"), RefreshToken: prismString(a.Credentials, "refresh_token"), SessionToken: prismString(a.Credentials, "session_token")})
	c.Headers = map[string]string{"oauth_client_id": prismString(a.Credentials, "oauth_client_id")}
	return c
}
func prismCredentialMap(c *creds.Credential, identity string) map[string]any {
	return map[string]any{"cookies": c.EffectiveCookie(), "access_token": c.AccessToken, "refresh_token": c.RefreshToken, "session_token": c.SessionToken, "oauth_client_id": c.Headers["oauth_client_id"], "prism_verified_identity": identity, "prism_verified_at": time.Now().UTC().Format(time.RFC3339Nano), "prism_verification_pending": false}
}

// No caller supplied upstream URL or headers: authentication can only go to the pinned Prism service.
func PrismAccountPrincipal(ctx context.Context, a *Account) (*prism.Client, prism.Principal, func(), error) {
	if a == nil || a.Platform != "prism" {
		return nil, prism.Principal{}, func() {}, prismError("invalid_account", "不是 Prism 账号")
	}
	if err := ctx.Err(); err != nil {
		return nil, prism.Principal{}, func() {}, err
	}
	if prismVerificationPending(a) {
		return nil, prism.Principal{}, func() {}, prismError("verification_pending", "Prism 登录凭据等待身份验证")
	}
	cfg := bridgeconfig.Default()
	proxy := ""
	if a.Proxy != nil {
		proxy = a.Proxy.URL()
	} else if a.ProxyID != nil {
		return nil, prism.Principal{}, func() {}, prismError("proxy_unavailable", "账号代理未加载")
	}
	hc, err := httpc.New(cfg.Upstream, httpc.Options{Proxy: proxy})
	if err != nil {
		return nil, prism.Principal{}, func() {}, safePrismError(err)
	}
	return prism.NewDefault(hc), prism.Principal{Client: hc, Cred: prismCredential(a)}, hc.CloseIdle, nil
}

type prismCatalogEntry struct {
	models  []prism.UpstreamModel
	expires time.Time
}

var prismCatalogCache = struct {
	sync.Mutex
	entries    map[string]prismCatalogEntry
	flights    singleflight.Group
	generation uint64
}{entries: map[string]prismCatalogEntry{}}

func prismAccountFingerprint(a *Account) string {
	raw, _ := json.Marshal(a.Credentials)
	sum := sha256.Sum256(raw)
	proxy := ""
	if a.Proxy != nil {
		proxy = a.Proxy.URL()
	}
	ps := sha256.Sum256([]byte(proxy))
	return fmt.Sprintf("%d:%x:%x", a.ID, sum, ps)
}
func PrismAccountCatalog(ctx context.Context, a *Account) ([]prism.UpstreamModel, error) {
	_, models, err := PrismAccountCatalogSnapshot(ctx, a)
	return models, err
}

// PrismAccountCatalogSnapshot keeps credentials, aliases and capabilities in
// the same refreshed generation for callers that project or resolve models.
func PrismAccountCatalogSnapshot(ctx context.Context, a *Account) (*Account, []prism.UpstreamModel, error) {
	var refresh func(context.Context, *Account) (*Account, error)
	if lifecycle, ok := ctx.Value(prismLifecycleContextKey{}).(*PrismAccountService); ok && lifecycle != nil {
		refresh = lifecycle.EnsureFresh
	}
	return prismAccountCatalogSnapshot(ctx, a, refresh, prismAccountCatalogForSnapshot)
}

func prismAccountCatalogSnapshot(ctx context.Context, a *Account, refresh func(context.Context, *Account) (*Account, error), catalog func(context.Context, *Account) ([]prism.UpstreamModel, error)) (*Account, []prism.UpstreamModel, error) {
	if a == nil {
		return nil, nil, prismError("invalid_account", "账号不存在")
	}
	if refresh != nil {
		fresh, err := refresh(ctx, a)
		if err != nil {
			return nil, nil, err
		}
		if fresh == nil || fresh.ID != a.ID || fresh.Platform != PlatformPrism {
			return nil, nil, prismError("invalid_account", "Prism refreshed account identity changed")
		}
		a = fresh
	}
	ctx = context.WithValue(ctx, prismLifecycleContextKey{}, false)
	models, err := catalog(ctx, a)
	return a, models, err
}

func prismAccountCatalogForSnapshot(ctx context.Context, a *Account) ([]prism.UpstreamModel, error) {
	if prismVerificationPending(a) {
		return nil, prismError("verification_pending", "Prism 登录凭据等待身份验证")
	}
	key := prismAccountFingerprint(a)
	prismCatalogCache.Lock()
	ent, ok := prismCatalogCache.entries[key]
	generation := prismCatalogCache.generation
	prismCatalogCache.Unlock()
	if ok && time.Now().Before(ent.expires) {
		return clonePrismModels(ent.models), nil
	}
	ch := prismCatalogCache.flights.DoChan(key, func() (any, error) {
		// Shared fetch has its own bounded context; one canceled waiter cannot cancel other requests.
		fetchCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		pc, p, close, err := PrismAccountPrincipal(fetchCtx, a)
		if err != nil {
			return nil, err
		}
		defer close()
		models, err := pc.InferenceModels(fetchCtx, p)
		if err != nil {
			return nil, safePrismError(err)
		}
		prismCatalogCache.Lock()
		defer prismCatalogCache.Unlock()
		if prismCatalogCache.generation != generation {
			return nil, prismError("stale_catalog", "账号目录已失效，请重新查询")
		}
		if len(prismCatalogCache.entries) >= 512 {
			for k := range prismCatalogCache.entries {
				delete(prismCatalogCache.entries, k)
				break
			}
		}
		prismCatalogCache.entries[key] = prismCatalogEntry{clonePrismModels(models), time.Now().Add(2 * time.Minute)}
		return models, nil
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-ch:
		if result.Err != nil {
			return nil, result.Err
		}
		return clonePrismModels(result.Val.([]prism.UpstreamModel)), nil
	}
}
func clonePrismModels(models []prism.UpstreamModel) []prism.UpstreamModel {
	out := append([]prism.UpstreamModel{}, models...)
	for i := range out {
		out[i].Efforts = append([]string(nil), out[i].Efforts...)
	}
	return out
}
func ReadPrismAccountModels(a *Account) ([]prism.UpstreamModel, bool) {
	if a == nil || a.Platform != "prism" || prismVerificationPending(a) {
		return nil, false
	}
	key := prismAccountFingerprint(a)
	prismCatalogCache.Lock()
	defer prismCatalogCache.Unlock()
	entry, ok := prismCatalogCache.entries[key]
	if !ok || time.Now().After(entry.expires) {
		return nil, false
	}
	return clonePrismModels(entry.models), true
}
func PrismAccountModelEligibility(ctx context.Context, a *Account, model, effort string) error {
	_, _, err := ResolvePrismAccountModel(ctx, a, model, effort)
	return err
}
func InvalidatePrismAccountCatalog(id int64) {
	prefix := fmt.Sprintf("%d:", id)
	prismCatalogCache.Lock()
	defer prismCatalogCache.Unlock()
	prismCatalogCache.generation++
	for k := range prismCatalogCache.entries {
		if strings.HasPrefix(k, prefix) {
			delete(prismCatalogCache.entries, k)
		}
	}
}
func ResolvePrismAccountModel(ctx context.Context, a *Account, requested, effort string) (string, string, error) {
	if a == nil {
		return "", "", prismError("invalid_account", "账号不存在")
	}
	a, models, err := PrismAccountCatalogSnapshot(ctx, a)
	if err != nil {
		return "", "", err
	}
	return resolvePrismAccountSnapshotModel(a, models, requested, effort)
}

func resolvePrismAccountSnapshotModel(a *Account, models []prism.UpstreamModel, requested, effort string) (string, string, error) {
	if prismVerificationPending(a) {
		return "", "", prismError("verification_pending", "Prism 登录凭据等待身份验证")
	}
	if mapping := a.GetModelMapping(); len(mapping) > 0 && !mappingSupportsRequestedModel(mapping, requested) {
		return "", "", prismError("model_not_supported", "账号未配置该模型或别名")
	}
	model := a.GetMappedModel(requested)
	for _, m := range models {
		if m.ID != model {
			continue
		}
		if effort == "" {
			effort = m.DefaultEffort
		}
		if effort != "" {
			valid := false
			for _, e := range m.Efforts {
				if e == effort {
					valid = true
				}
			}
			if !valid {
				return "", "", prismError("unsupported_effort", "此账号目录不支持指定推理强度")
			}
		}
		return model, effort, nil
	}
	return "", "", prismError("unsupported_model", "此账号的 Prism 目录不包含请求模型")
}

func parsePrismCookies(raw string) (*creds.Credential, error) {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(strings.ToLower(raw), "cookie:") {
		raw = strings.TrimSpace(raw[len("cookie:"):])
	}
	if len(raw) == 0 || len(raw) > 128<<10 || strings.ContainsAny(raw, "\r\n\x00") {
		return nil, prismError("invalid_cookie", "请输入有效的单行 Cookie（最大 128 KiB）")
	}
	c := creds.FromAccountConfig(bridgeconfig.AccountConfig{Cookies: raw})
	if c.AccessToken == "" && c.RefreshToken == "" {
		return nil, prismError("invalid_cookie", "Cookie 不包含 Prism 登录凭据")
	}
	return c, nil
}

// EnsureFresh serializes rotating refresh tokens by account and credential
// version. Each waiter observes its own cancellation without canceling peers.
func (s *PrismAccountService) EnsureFresh(ctx context.Context, a *Account) (*Account, error) {
	if a == nil || a.Platform != "prism" {
		return nil, prismError("invalid_account", "Prism 账号不存在")
	}
	c := prismCredential(a)
	if !prismVerificationPending(a) && !c.NeedsRefresh(time.Now(), time.Minute) {
		return a, nil
	}
	if !prismVerificationPending(a) && c.RefreshToken == "" {
		return nil, prismError("credentials_expired", "登录凭据即将或已经失效，请重新登录")
	}
	fresh, err := s.admin.GetAccount(ctx, a.ID)
	if err != nil {
		return nil, prismError("invalid_account", "账号已删除")
	}
	if prismAccountFingerprint(fresh) != prismAccountFingerprint(a) {
		if prismVerificationPending(fresh) {
			return s.EnsureFresh(ctx, fresh)
		}
		return fresh, nil
	}
	if _, err := s.Refresh(ctx, a.ID); err != nil {
		return nil, err
	}
	fresh, err = s.admin.GetAccount(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	if prismVerificationPending(fresh) {
		return nil, prismError("verification_pending", "Prism 登录凭据等待身份验证")
	}
	return fresh, nil
}
func (s *PrismAccountService) temporaryAccount(ctx context.Context, c *creds.Credential, proxyID *int64) (*Account, error) {
	a := &Account{Platform: "prism", Type: AccountTypeOAuth, Credentials: prismCredentialMap(c, ""), ProxyID: proxyID}
	if proxyID != nil {
		p, err := s.admin.GetProxy(ctx, *proxyID)
		if err != nil {
			return nil, prismError("invalid_proxy", "代理不存在或不可用")
		}
		a.Proxy = p
	}
	return a, nil
}

// Called only after the exact access token has been accepted by a fresh Prism
// /auth/session exchange. Merely decoding JWT claims is never sufficient.
func prismVerifiedIdentity(c *creds.Credential) (string, error) {
	if c.UserID == "" || c.AccountID == "" {
		return "", prismError("identity_unverified", "已验证的登录凭据缺少用户或工作区身份，不能保存或合并账号")
	}
	raw, _ := json.Marshal([]string{c.UserID, c.AccountID})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func (s *PrismAccountService) verify(ctx context.Context, c *creds.Credential, proxyID *int64) (*creds.Credential, string, []prism.UpstreamModel, error) {
	a, err := s.temporaryAccount(ctx, c, proxyID)
	if err != nil {
		return nil, "", nil, err
	}
	pc, p, close, err := PrismAccountPrincipal(ctx, a)
	if err != nil {
		return nil, "", nil, err
	}
	defer close()
	if p.Cred.AccessToken == "" || p.Cred.NeedsRefresh(time.Now(), time.Minute) {
		if p.Cred.RefreshToken == "" {
			return nil, "", nil, prismError("credentials_expired", "登录凭据已过期，请重新登录")
		}
		cfg := bridgeconfig.Default()
		r, _ := creds.NewRefresher(cfg.Creds, cfg.Upstream, p.Client)
		p.Cred, err = r.RefreshOAuth(ctx, p.Cred)
		if err != nil {
			return nil, "", nil, safePrismError(err)
		}
	}
	proxy := ""
	if a.Proxy != nil {
		proxy = a.Proxy.URL()
	}
	sessionToken, err := upstream.VerifyAccessToken(ctx, upstream.Options{Proxy: proxy}, p.Cred.AccessToken)
	if err != nil {
		return nil, "", nil, safePrismError(err)
	}
	// Rebuild exclusively from the accepted token so caller-supplied metadata or
	// unrelated cookie identity cannot influence deduplication.
	verified := creds.FromAccountConfig(bridgeconfig.AccountConfig{AccessToken: p.Cred.AccessToken, RefreshToken: p.Cred.RefreshToken, SessionToken: sessionToken})
	verified.Headers = p.Cred.Headers
	p.Cred = verified
	identity, err := prismVerifiedIdentity(p.Cred)
	if err != nil {
		return nil, "", nil, err
	}
	models, err := pc.InferenceModels(ctx, p)
	if err != nil {
		return nil, "", nil, safePrismError(err)
	}
	if len(models) == 0 {
		return nil, "", nil, prismError("no_entitlement", "登录成功，但 Prism 模型目录为空，尚无可用模型权限")
	}
	return p.Cred, identity, models, nil
}

var prismImportWriteMu sync.Mutex

// Import is individually atomic: a failed item cannot leave a partially created account/group binding.
func (s *PrismAccountService) Import(ctx context.Context, req PrismImportRequest) (PrismImportResult, error) {
	entries := append([]PrismImportEntry(nil), req.Accounts...)
	if strings.TrimSpace(req.Cookies) != "" {
		entries = append(entries, PrismImportEntry{Cookies: req.Cookies, Name: req.Name})
	}
	result := PrismImportResult{Total: len(entries), Items: []PrismImportItem{}}
	if len(entries) < 1 || len(entries) > 100 {
		return result, prismError("invalid_batch", "每次需导入 1 至 100 个账号")
	}
	if req.Verify != nil && !*req.Verify {
		return result, prismError("verification_required", "Prism 导入必须验证身份与模型权限")
	}
	if req.Concurrency != nil && (*req.Concurrency < 1 || *req.Concurrency > 1000) || req.Priority != nil && *req.Priority < 0 {
		return result, prismError("invalid_settings", "并发数需为 1 至 1000，优先级不能为负")
	}
	for i, e := range entries {
		if err := ctx.Err(); err != nil {
			safe := safePrismError(err).(*PrismAccountError)
			for j := i; j < len(entries); j++ {
				result.Failed++
				result.Items = append(result.Items, PrismImportItem{Index: j + 1, Action: "failed", Code: safe.Code, Message: safe.Message})
			}
			return result, nil
		}
		item := PrismImportItem{Index: i + 1}
		c, err := parsePrismCookies(e.Cookies)
		var identity string
		if err == nil {
			verifyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			c, identity, _, err = s.verify(verifyCtx, c, req.ProxyID)
			cancel()
		}
		if err == nil {
			itemReq := req
			if e.Name != "" {
				itemReq.Name = e.Name
			}
			item.AccountID, item.Action, err = s.save(ctx, itemReq, c, identity)
		}
		if err != nil {
			safe := safePrismError(err).(*PrismAccountError)
			item.Action = "failed"
			item.Code = safe.Code
			item.Message = safe.Message
			result.Failed++
		} else if item.Action == "created" {
			result.Created++
		} else if item.Action == "updated" {
			result.Updated++
		}
		result.Items = append(result.Items, item)
	}
	return result, nil
}

func (s *PrismAccountService) save(ctx context.Context, req PrismImportRequest, c *creds.Credential, identity string) (int64, string, error) {
	prismImportWriteMu.Lock()
	defer prismImportWriteMu.Unlock()
	impl, ok := s.admin.(*adminServiceImpl)
	if !ok || impl.entClient == nil {
		return 0, "", prismError("storage_unavailable", "Prism 原子账号存储不可用")
	}
	tx, err := impl.entClient.Tx(ctx)
	if err != nil {
		return 0, "", prismError("storage_error", "无法开始账号保存事务")
	}
	defer tx.Rollback()
	ctx = dbent.NewTxContext(ctx, tx)
	// Serialize identity dedup across replicas using a PostgreSQL transaction advisory lock.
	release, err := lockAuthPendingIdentityKeys(ctx, tx.Client(), "prism:"+identity)
	if err != nil {
		return 0, "", prismError("storage_error", "无法锁定账号身份")
	}
	defer release()
	if req.AccountID > 0 {
		if _, err := tx.Account.Query().Where(entaccount.IDEQ(req.AccountID)).ForUpdate().Only(ctx); err != nil {
			return 0, "", prismError("stale_credentials", "账号已删除，请重新操作")
		}
		fresh, err := s.admin.GetAccount(ctx, req.AccountID)
		if err != nil || req.expectedFingerprint != "" && prismAccountFingerprint(fresh) != req.expectedFingerprint {
			return 0, "", prismError("stale_credentials", "账号已修改或删除，请重新操作")
		}
	}
	accounts, err := s.admin.ListAccountsForSchedulerScoreFilter(ctx, "prism", "", "", "", 0, "")
	if err != nil {
		return 0, "", prismError("storage_error", "读取账号失败")
	}
	var existing *Account
	for i := range accounts {
		if prismString(accounts[i].Credentials, "prism_verified_identity") == identity {
			if existing != nil {
				return 0, "", prismError("duplicate_identity", "已存在多个相同身份账号，请先处理重复账号")
			}
			existing = &accounts[i]
		}
	}
	if req.AccountID > 0 && (existing == nil || existing.ID != req.AccountID) {
		return 0, "", prismError("identity_mismatch", "重新登录身份与原账号不一致")
	}
	credentials := prismCredentialMap(c, identity)
	action := "created"
	var saved *Account
	if existing != nil {
		if req.UpdateExisting != nil && !*req.UpdateExisting {
			return existing.ID, "skipped", nil
		}
		action = "updated"
		if _, lockErr := tx.Account.Query().Where(entaccount.IDEQ(existing.ID), entaccount.DeletedAtIsNil()).ForUpdate().Only(ctx); lockErr != nil {
			return 0, "", prismError("stale_credentials", "账号已删除，请重新导入")
		}
		fresh, readErr := s.admin.GetAccount(ctx, existing.ID)
		if readErr != nil || prismString(fresh.Credentials, "prism_verified_identity") != identity {
			return 0, "", prismError("stale_credentials", "账号身份已修改，请重新导入")
		}
		existing = fresh
		for k, v := range existing.Credentials {
			if _, ok := credentials[k]; !ok {
				credentials[k] = v
			}
		}
		if c.RefreshToken == "" {
			credentials["refresh_token"] = existing.Credentials["refresh_token"]
			credentials["oauth_client_id"] = existing.Credentials["oauth_client_id"]
		}
		saved, err = s.admin.UpdateAccount(withVerifiedPrismCredentialWrite(ctx), existing.ID, &UpdateAccountInput{Credentials: credentials, ProxyID: req.ProxyID})
	} else {
		concurrency, priority := 2, 50
		if req.Concurrency != nil {
			concurrency = *req.Concurrency
		}
		if req.Priority != nil {
			priority = *req.Priority
		}
		name := strings.TrimSpace(req.Name)
		if name == "" {
			name = "Prism " + identity[:8]
		}
		saved, err = s.createAtomic(ctx, tx, &CreateAccountInput{Name: name, Platform: "prism", Type: AccountTypeOAuth, Credentials: credentials, ProxyID: req.ProxyID, Concurrency: concurrency, Priority: priority, GroupIDs: req.GroupIDs})
	}
	if err != nil {
		return 0, "", prismError("storage_error", "账号保存失败，请检查分组与账号设置")
	}
	if err = ctx.Err(); err != nil {
		return 0, "", safePrismError(err)
	}
	if err = tx.Commit(); err != nil {
		return 0, "", prismError("storage_error", "账号事务提交失败")
	}
	InvalidatePrismAccountCatalog(saved.ID)
	return saved.ID, action, nil
}

// The legacy generic Create/BindGroups repository methods do not consume a
// transaction from context. Keep this small dedicated write on the same Ent
// transaction, including group bindings and the normal scheduler outbox event.
func (s *PrismAccountService) createAtomic(ctx context.Context, tx *dbent.Tx, input *CreateAccountInput) (*Account, error) {
	groupIDs := append([]int64(nil), input.GroupIDs...)
	if len(groupIDs) == 0 {
		defaults, err := tx.Group.Query().Where(entgroup.PlatformEQ("prism"), entgroup.NameEQ("prism-default"), entgroup.StatusEQ(StatusActive), entgroup.DeletedAtIsNil()).All(ctx)
		if err != nil {
			return nil, err
		}
		for _, g := range defaults {
			groupIDs = append(groupIDs, g.ID)
		}
	}
	if err := s.admin.ValidateAccountGroupBindings(ctx, groupIDs); err != nil {
		return nil, err
	}
	if err := s.admin.CheckMixedChannelRisk(ctx, 0, "prism", groupIDs); err != nil {
		return nil, err
	}
	if len(groupIDs) > 0 {
		locked, err := tx.Group.Query().Where(entgroup.IDIn(groupIDs...), entgroup.DeletedAtIsNil()).ForShare().All(ctx)
		if err != nil {
			return nil, err
		}
		unique := map[int64]bool{}
		for _, id := range groupIDs {
			unique[id] = true
		}
		if len(locked) != len(unique) {
			return nil, prismError("invalid_group", "分组不存在或已删除")
		}
		groupIDs = groupIDs[:0]
		for _, g := range locked {
			groupIDs = append(groupIDs, g.ID)
		}
	}
	a, err := buildAccountForCreate(input, map[string]any{})
	if err != nil {
		return nil, err
	}
	row, err := tx.Account.Create().SetName(a.Name).SetPlatform("prism").SetType(AccountTypeOAuth).SetCredentials(a.Credentials).SetExtra(a.Extra).SetNillableProxyID(a.ProxyID).SetConcurrency(a.Concurrency).SetPriority(a.Priority).SetStatus(a.Status).SetSchedulable(a.Schedulable).SetAutoPauseOnExpired(a.AutoPauseOnExpired).Save(ctx)
	if err != nil {
		return nil, err
	}
	a.ID = row.ID
	for _, id := range groupIDs {
		if _, err := tx.AccountGroup.Create().SetAccountID(a.ID).SetGroupID(id).SetPriority(0).Save(ctx); err != nil {
			return nil, err
		}
	}
	payload, _ := json.Marshal(map[string]any{"group_ids": groupIDs})
	if _, err := tx.ExecContext(ctx, "INSERT INTO scheduler_outbox (event_type, account_id, payload) VALUES ($1,$2,$3)", SchedulerOutboxEventAccountChanged, a.ID, payload); err != nil {
		return nil, err
	}
	a.GroupIDs = groupIDs
	return a, nil
}

func (s *PrismAccountService) Catalog(ctx context.Context, id int64, refresh bool) ([]prism.UpstreamModel, error) {
	_, models, err := s.CatalogSnapshot(ctx, id, refresh)
	return models, err
}

func (s *PrismAccountService) CatalogSnapshot(ctx context.Context, id int64, refresh bool) (*Account, []prism.UpstreamModel, error) {
	a, err := s.admin.GetAccount(ctx, id)
	if err != nil {
		return nil, nil, prismError("invalid_account", "账号不存在")
	}
	if refresh {
		InvalidatePrismAccountCatalog(id)
	}
	return PrismAccountCatalogSnapshot(withPrismLifecycle(ctx, s), a)
}
func (s *PrismAccountService) Refresh(ctx context.Context, id int64) (PrismImportItem, error) {
	a, err := s.admin.GetAccount(ctx, id)
	if err != nil || a.Platform != "prism" {
		return PrismImportItem{}, prismError("invalid_account", "Prism 账号不存在")
	}
	ch := s.refreshFlights.DoChan(prismAccountFingerprint(a), func() (any, error) {
		work, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		return s.refreshAccount(work, a)
	})
	select {
	case <-ctx.Done():
		return PrismImportItem{}, safePrismError(ctx.Err())
	case result := <-ch:
		if result.Err != nil {
			return PrismImportItem{}, result.Err
		}
		return result.Val.(PrismImportItem), nil
	}
}
func (s *PrismAccountService) refreshAccount(ctx context.Context, a *Account) (PrismImportItem, error) {
	id := a.ID
	version := prismAccountFingerprint(a)
	// A distinct transaction advisory lock serializes rotating refresh tokens
	// across application replicas; it never relies on a process-local mutex.
	impl, ok := s.admin.(*adminServiceImpl)
	if !ok || impl.entClient == nil {
		return PrismImportItem{}, prismError("storage_unavailable", "Prism 刷新存储不可用")
	}
	leaseCtx, leaseCancel := context.WithTimeout(context.WithoutCancel(ctx), 90*time.Second)
	defer leaseCancel()
	lease, err := impl.entClient.Tx(leaseCtx)
	if err != nil {
		return PrismImportItem{}, prismError("storage_error", "无法锁定账号刷新")
	}
	defer lease.Rollback()
	release, err := lockAuthPendingIdentityKeys(leaseCtx, lease.Client(), fmt.Sprintf("prism-refresh:%d", id))
	if err != nil {
		return PrismImportItem{}, prismError("storage_error", "无法锁定账号刷新")
	}
	defer release()
	fresh, err := s.admin.GetAccount(ctx, id)
	if err != nil {
		return PrismImportItem{}, prismError("stale_credentials", "账号已删除")
	}
	if prismAccountFingerprint(fresh) != version {
		return PrismImportItem{Index: 1, Action: "updated", AccountID: id}, nil
	}
	return s.refreshAndVerify(ctx, a)
}
