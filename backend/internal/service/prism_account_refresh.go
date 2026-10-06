package service

import (
	"context"
	"errors"
	entaccount "github.com/Wei-Shaw/sub2api/ent/account"
	entproxy "github.com/Wei-Shaw/sub2api/ent/proxy"
	bridgeconfig "github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/creds"
	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/upstream"
	"maps"
	"sync"
	"time"
)

func prismVerificationPending(a *Account) bool {
	if a == nil {
		return false
	}
	pending, _ := a.Credentials["prism_verification_pending"].(bool)
	return pending
}

type prismVerificationDelay struct {
	until time.Time
	err   error
}

var prismVerificationBackoff = struct {
	sync.Mutex
	entries map[string]prismVerificationDelay
}{entries: map[string]prismVerificationDelay{}}

func prismPendingBackoff(a *Account, err error) error {
	key := prismAccountFingerprint(a)
	prismVerificationBackoff.Lock()
	defer prismVerificationBackoff.Unlock()
	if err == nil {
		if entry, ok := prismVerificationBackoff.entries[key]; ok && time.Now().Before(entry.until) {
			return entry.err
		}
		delete(prismVerificationBackoff.entries, key)
		return nil
	}
	delay := 15 * time.Second
	var ae *creds.APIError
	var se *upstream.SessionVerificationError
	if errors.As(err, &ae) && ae.RetryAfter > delay {
		delay = ae.RetryAfter
	}
	if errors.As(err, &se) && se.RetryAfter > delay {
		delay = se.RetryAfter
	}
	if delay > 5*time.Minute {
		delay = 5 * time.Minute
	}
	if len(prismVerificationBackoff.entries) >= 512 {
		for k := range prismVerificationBackoff.entries {
			delete(prismVerificationBackoff.entries, k)
			break
		}
	}
	safe := safePrismError(err)
	prismVerificationBackoff.entries[key] = prismVerificationDelay{time.Now().Add(delay), safe}
	return safe
}

// Only the credential column is changed. A full account UpdateAccount would
// overwrite a concurrent disable or hard-health change from its old snapshot.
func (s *PrismAccountService) persistRefreshCredentials(parent context.Context, a *Account, c *creds.Credential, pending bool) (*Account, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 10*time.Second)
	defer cancel()
	impl, ok := s.admin.(*adminServiceImpl)
	if !ok || impl.entClient == nil {
		return nil, prismError("storage_unavailable", "Prism 刷新存储不可用")
	}
	tx, err := impl.entClient.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	row, err := tx.Account.Query().Where(entaccount.IDEQ(a.ID), entaccount.DeletedAtIsNil()).ForUpdate().Only(ctx)
	if err != nil {
		return nil, prismError("stale_credentials", "账号已删除")
	}
	expectedProxy, actualProxy := int64(0), int64(0)
	if a.ProxyID != nil {
		expectedProxy = *a.ProxyID
	}
	if row.ProxyID != nil {
		actualProxy = *row.ProxyID
	}
	current := *a
	current.Credentials = row.Credentials
	if expectedProxy != actualProxy {
		return nil, prismError("stale_credentials", "账号代理已修改")
	}
	if row.ProxyID != nil {
		p, proxyErr := tx.Proxy.Query().Where(entproxy.IDEQ(*row.ProxyID), entproxy.DeletedAtIsNil()).ForShare().Only(ctx)
		if proxyErr != nil {
			return nil, prismError("stale_credentials", "账号代理已修改")
		}
		current.Proxy = &Proxy{ID: p.ID, Protocol: p.Protocol, Host: p.Host, Port: p.Port}
		if p.Username != nil {
			current.Proxy.Username = *p.Username
		}
		if p.Password != nil {
			current.Proxy.Password = *p.Password
		}
	}
	if expectedProxy != actualProxy || prismAccountFingerprint(&current) != prismAccountFingerprint(a) {
		return nil, prismError("stale_credentials", "账号凭据、映射或代理已修改")
	}
	next := maps.Clone(row.Credentials)
	next["access_token"] = c.AccessToken
	next["refresh_token"] = c.RefreshToken
	next["session_token"] = c.SessionToken
	next["cookies"] = c.EffectiveCookie()
	next["prism_verification_pending"] = pending
	if !pending {
		next["prism_verified_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if _, err = tx.Account.UpdateOneID(a.ID).SetCredentials(next).Save(ctx); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO scheduler_outbox (event_type,account_id,payload) VALUES ($1,$2,$3)", SchedulerOutboxEventAccountChanged, a.ID, []byte(`{}`)); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	InvalidatePrismAccountCatalog(a.ID)
	out := *a
	out.Credentials = next
	return &out, nil
}

func (s *PrismAccountService) rotatePrismToken(ctx context.Context, a *Account, c *creds.Credential) (*creds.Credential, error) {
	if s.refreshToken != nil {
		return s.refreshToken(ctx, a, c)
	}
	// Internal auth transport may be used for verification of pending material;
	// the exported inference principal must continue to fail closed.
	copy := *a
	copy.Credentials = maps.Clone(a.Credentials)
	delete(copy.Credentials, "prism_verification_pending")
	_, p, close, err := PrismAccountPrincipal(ctx, &copy)
	if err != nil {
		return nil, err
	}
	defer close()
	cfg := bridgeconfig.Default()
	r, _ := creds.NewRefresher(cfg.Creds, cfg.Upstream, p.Client)
	return r.RefreshOAuth(ctx, c)
}
func (s *PrismAccountService) verifyPrismToken(ctx context.Context, a *Account, c *creds.Credential) (*creds.Credential, error) {
	if s.verifyToken != nil {
		return s.verifyToken(ctx, a, c)
	}
	proxy := ""
	if a.Proxy != nil {
		proxy = a.Proxy.URL()
	} else if a.ProxyID != nil {
		return nil, prismError("proxy_unavailable", "账号代理未加载")
	}
	session, err := upstream.VerifyAccessToken(ctx, upstream.Options{Proxy: proxy}, c.AccessToken)
	if err != nil {
		return nil, err
	}
	verified := creds.FromAccountConfig(bridgeconfig.AccountConfig{AccessToken: c.AccessToken, RefreshToken: c.RefreshToken, SessionToken: session})
	verified.Headers = c.Headers
	return verified, nil
}
func (s *PrismAccountService) refreshAndVerify(ctx context.Context, a *Account) (PrismImportItem, error) {
	if err := prismPendingBackoff(a, nil); err != nil {
		return PrismImportItem{}, err
	}
	c := prismCredential(a)
	if !prismVerificationPending(a) || c.NeedsRefresh(time.Now(), time.Minute) {
		if c.RefreshToken != "" {
			rotated, err := s.rotatePrismToken(ctx, a, c)
			if err != nil {
				return PrismImportItem{}, prismPendingBackoff(a, err)
			}
			c = rotated
		}
		// Rotation has already happened upstream. Even cancellation must not discard
		// the only usable refresh token. Save before session or catalog requests.
		saved, err := s.persistRefreshCredentials(ctx, a, c, true)
		if err != nil {
			return PrismImportItem{}, safePrismError(err)
		}
		a = saved
	}
	verified, err := s.verifyPrismToken(ctx, a, c)
	if err != nil {
		return PrismImportItem{}, prismPendingBackoff(a, err)
	}
	identity, err := prismVerifiedIdentity(verified)
	if err != nil {
		return PrismImportItem{}, prismPendingBackoff(a, err)
	}
	if identity != prismString(a.Credentials, "prism_verified_identity") {
		return PrismImportItem{}, prismPendingBackoff(a, prismError("identity_mismatch", "刷新后的身份与原账号不一致，请重新登录"))
	}
	if _, err = s.persistRefreshCredentials(ctx, a, verified, false); err != nil {
		return PrismImportItem{}, safePrismError(err)
	}
	// Catalog failures remain separate capability failures; session verification
	// does not require another token rotation or lose durable credentials.
	return PrismImportItem{Index: 1, Action: "updated", AccountID: a.ID}, nil
}
