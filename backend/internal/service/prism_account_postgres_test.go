//go:build unit

package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	entaccount "github.com/Wei-Shaw/sub2api/ent/account"
	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/creds"
	_ "github.com/lib/pq"
)

type prismPGRepo struct {
	AccountRepository
	client *dbent.Client
}

func (r prismPGRepo) GetByID(ctx context.Context, id int64) (*Account, error) {
	a, err := r.client.Account.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	errText := ""
	if a.ErrorMessage != nil {
		errText = *a.ErrorMessage
	}
	return &Account{ID: a.ID, Platform: a.Platform, Credentials: a.Credentials, ProxyID: a.ProxyID, Status: a.Status, Schedulable: a.Schedulable, ErrorMessage: errText, Extra: a.Extra}, nil
}
func (r prismPGRepo) ListAllWithFilters(ctx context.Context, platform, kind, status, search string, gid int64, privacy string) ([]Account, error) {
	rows, err := r.client.Account.Query().Where(entaccount.PlatformEQ(platform), entaccount.DeletedAtIsNil()).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Account, len(rows))
	for i, a := range rows {
		out[i] = Account{ID: a.ID, Platform: a.Platform, Credentials: a.Credentials}
	}
	return out, nil
}

func TestPrismAccountPostgresAtomicAndCAS(t *testing.T) {
	dsn := os.Getenv("PRISM_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("isolated Prism PostgreSQL fixture not configured")
	}
	if !strings.Contains(dsn, "host=/tmp/prism-lifecycle-pg.") {
		t.Fatal("fixture requires a dedicated ephemeral Prism socket directory")
	}
	admin, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	name := fmt.Sprintf("prism_atomic_%d", time.Now().UnixNano())
	if _, err := admin.Exec("CREATE DATABASE " + name); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("postgres", strings.Replace(dsn, "dbname=postgres", "dbname="+name, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	defer client.Close()
	ctx := context.Background()
	if err := client.Schema.Create(ctx); err != nil {
		t.Fatal(err)
	}
	impl := &adminServiceImpl{entClient: client, accountRepo: prismPGRepo{client: client}}
	s := NewPrismAccountService(impl)
	credential := &creds.Credential{AccessToken: "fixture", UserID: "user-fixture", AccountID: "workspace-fixture"}
	identity, _ := prismVerifiedIdentity(credential)
	// Missing outbox table forces failure after insertion; save must roll back.
	if _, _, err := s.save(ctx, PrismImportRequest{}, credential, identity); err == nil {
		t.Fatal("missing outbox should fail")
	}
	n, err := client.Account.Query().Count(ctx)
	if err != nil || n != 0 {
		t.Fatalf("partial account persisted: %d %v", n, err)
	}
	if _, err := db.Exec("CREATE TABLE scheduler_outbox(event_type TEXT,account_id BIGINT,payload JSONB)"); err != nil {
		t.Fatal(err)
	}
	id, action, err := s.save(ctx, PrismImportRequest{}, credential, identity)
	if err != nil || id == 0 || action != "created" {
		t.Fatalf("atomic create: %d %s %v", id, action, err)
	}
	disabled := false
	duplicate, action, err := s.save(ctx, PrismImportRequest{UpdateExisting: &disabled}, credential, identity)
	if err != nil || duplicate != id || action != "skipped" {
		t.Fatalf("identity dedupe failed: %d %s %v", duplicate, action, err)
	}
	before, err := impl.GetAccount(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := prismAccountFingerprint(before)
	if _, err := client.Account.UpdateOneID(id).SetCredentials(map[string]any{"access_token": "newer", "prism_verified_identity": identity}).Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.save(ctx, PrismImportRequest{AccountID: id, expectedFingerprint: fingerprint}, credential, identity); err == nil {
		t.Fatal("stale refresh overwrote new credential")
	}
	after, err := impl.GetAccount(ctx, id)
	if err != nil || prismString(after.Credentials, "access_token") != "newer" {
		t.Fatal("stale refresh changed credentials")
	}
	if err := client.Account.DeleteOneID(id).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.save(ctx, PrismImportRequest{AccountID: id, expectedFingerprint: fingerprint}, credential, identity); err == nil {
		t.Fatal("deleted account revived")
	}
	n, err = client.Account.Query().Count(ctx)
	if err != nil || n != 0 {
		t.Fatal("deleted account was recreated")
	}
	t.Run("rotation_pending_recovery", func(t *testing.T) { testPrismRefreshDurability(t, s, client) })
}

func testPrismRefreshDurability(t *testing.T, s *PrismAccountService, client *dbent.Client) {
	ctx := context.Background()
	owner := &creds.Credential{AccessToken: "old", RefreshToken: "old-refresh", UserID: "owner", AccountID: "workspace"}
	identity, _ := prismVerifiedIdentity(owner)
	rotations, verifications := 0, 0
	failVerify := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			rotations++
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "fresh", "refresh_token": "rotated"})
			return
		}
		verifications++
		if failVerify {
			w.Header().Set("Retry-After", "90")
			w.WriteHeader(429)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"session": "verified"})
	}))
	defer server.Close()
	s.refreshToken = func(ctx context.Context, a *Account, c *creds.Credential) (*creds.Credential, error) {
		req, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/token", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		var tok map[string]string
		if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
			return nil, err
		}
		return &creds.Credential{AccessToken: tok["access_token"], RefreshToken: tok["refresh_token"], UserID: "owner", AccountID: "workspace"}, nil
	}
	s.verifyToken = func(ctx context.Context, a *Account, c *creds.Credential) (*creds.Credential, error) {
		req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/auth/session", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return nil, &creds.APIError{Status: resp.StatusCode, RetryAfter: 90 * time.Second}
		}
		copy := *c
		copy.UserID = "owner"
		copy.AccountID = "workspace"
		copy.SessionToken = "verified"
		return &copy, nil
	}
	makeAccount := func() *Account {
		row, err := client.Account.Create().SetName("refresh fixture").SetPlatform("prism").SetType(AccountTypeOAuth).SetCredentials(map[string]any{"access_token": "old", "refresh_token": "old-refresh", "prism_verified_identity": identity, "prism_verified_at": "prior", "model_mapping": map[string]any{"alias": "model"}}).SetStatus("error").SetSchedulable(false).SetErrorMessage("hard failure").SetExtra(map[string]any{"hard_health": "keep"}).Save(ctx)
		if err != nil {
			t.Fatal(err)
		}
		a, err := s.admin.GetAccount(ctx, row.ID)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	a := makeAccount()
	if _, err := s.refreshAccount(ctx, a); err == nil {
		t.Fatal("429 missing")
	}
	pending, _ := s.admin.GetAccount(ctx, a.ID)
	if prismString(pending.Credentials, "refresh_token") != "rotated" || !prismVerificationPending(pending) || prismString(pending.Credentials, "prism_verified_identity") != identity || prismString(pending.Credentials, "prism_verified_at") != "prior" {
		t.Fatal("rotated credential not durably pending")
	}
	if pending.Status != "error" || pending.Schedulable || pending.ErrorMessage != "hard failure" || pending.Extra["hard_health"] != "keep" {
		t.Fatal("hard health overwritten")
	}
	if _, _, _, err := PrismAccountPrincipal(ctx, pending); err == nil {
		t.Fatal("pending inference accepted")
	}
	if _, err := s.EnsureFresh(ctx, pending); err == nil {
		t.Fatal("backoff not enforced")
	}
	if rotations != 1 || verifications != 1 {
		t.Fatal("backoff retried upstream")
	}
	prismVerificationBackoff.Lock()
	delete(prismVerificationBackoff.entries, prismAccountFingerprint(pending))
	prismVerificationBackoff.Unlock()
	failVerify = false
	recovered, err := s.EnsureFresh(ctx, pending)
	if err != nil || prismVerificationPending(recovered) {
		t.Fatalf("verify-only recovery: %v", err)
	}
	if rotations != 1 || verifications != 2 {
		t.Fatal("fresh pending token rotated again")
	}
	t.Run("cancel_after_rotation", func(t *testing.T) {
		a := makeAccount()
		work, cancel := context.WithCancel(ctx)
		original := s.refreshToken
		defer func() { s.refreshToken = original }()
		s.refreshToken = func(ctx context.Context, a *Account, c *creds.Credential) (*creds.Credential, error) {
			rotated, err := original(ctx, a, c)
			cancel()
			return rotated, err
		}
		if _, err := s.refreshAccount(work, a); err == nil {
			t.Fatal("cancellation ignored")
		}
		saved, _ := s.admin.GetAccount(ctx, a.ID)
		if prismString(saved.Credentials, "refresh_token") != "rotated" || !prismVerificationPending(saved) {
			t.Fatal("cancellation lost rotation")
		}
	})
	t.Run("identity_mismatch", func(t *testing.T) {
		a := makeAccount()
		original := s.verifyToken
		defer func() { s.verifyToken = original }()
		s.verifyToken = func(context.Context, *Account, *creds.Credential) (*creds.Credential, error) {
			return &creds.Credential{AccessToken: "fresh", RefreshToken: "rotated", UserID: "other", AccountID: "workspace"}, nil
		}
		if _, err := s.refreshAccount(ctx, a); err == nil {
			t.Fatal("identity mismatch accepted")
		}
		saved, _ := s.admin.GetAccount(ctx, a.ID)
		if !prismVerificationPending(saved) || prismString(saved.Credentials, "prism_verified_identity") != identity {
			t.Fatal("new identity promoted")
		}
	})
	t.Run("mapping_CAS", func(t *testing.T) {
		a := makeAccount()
		changed := map[string]any{}
		for k, v := range a.Credentials {
			changed[k] = v
		}
		changed["model_mapping"] = map[string]any{"alias": "new-model"}
		if _, err := client.Account.UpdateOneID(a.ID).SetCredentials(changed).Save(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := s.persistRefreshCredentials(ctx, a, owner, true); err == nil {
			t.Fatal("mapping edit overwritten")
		}
	})
	t.Run("proxy_CAS", func(t *testing.T) {
		a := makeAccount()
		row, err := client.Proxy.Create().SetName("fixture").SetProtocol("http").SetHost("127.0.0.1").SetPort(1).Save(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.Account.UpdateOneID(a.ID).SetProxyID(row.ID).Save(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := s.persistRefreshCredentials(ctx, a, owner, true); err == nil {
			t.Fatal("proxy edit overwritten")
		}
		a.ProxyID = &row.ID
		a.Proxy = &Proxy{ID: row.ID, Protocol: "http", Host: "127.0.0.1", Port: 1}
		if _, err := client.Proxy.UpdateOneID(row.ID).SetHost("127.0.0.2").Save(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := s.persistRefreshCredentials(ctx, a, owner, true); err == nil {
			t.Fatal("same proxy generation edit overwritten")
		}
	})
	t.Run("concurrent_health_preserved", func(t *testing.T) {
		a := makeAccount()
		a.Status = "active"
		a.Schedulable = true
		a.ErrorMessage = "stale clean snapshot"
		a.Extra = map[string]any{"stale": "snapshot"}
		if _, err := s.persistRefreshCredentials(ctx, a, owner, true); err != nil {
			t.Fatal(err)
		}
		saved, _ := s.admin.GetAccount(ctx, a.ID)
		if saved.Status != "error" || saved.Schedulable || saved.ErrorMessage != "hard failure" || saved.Extra["hard_health"] != "keep" {
			t.Fatal("credential write overwrote current health")
		}
	})
}
