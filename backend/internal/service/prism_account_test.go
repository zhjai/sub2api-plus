package service

import (
	"context"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/creds"
	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/prism"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestPrismAccountCookieBounds(t *testing.T) {
	for _, raw := range []string{"", "x=y", "prism_oai_access_token=abc\r\nX:secret", strings.Repeat("x", (128<<10)+1)} {
		if _, err := parsePrismCookies(raw); err == nil {
			t.Fatalf("accepted invalid cookie")
		}
	}
	c, err := parsePrismCookies("Cookie: prism_oai_access_token=abc; prism_session_token=def")
	if err != nil || c.AccessToken != "abc" {
		t.Fatalf("cookie normalization failed: %v", err)
	}
}

func TestPrismPublicModelsOnlyVerifiedEntitlements(t *testing.T) {
	a := &Account{Platform: PlatformPrism, Credentials: map[string]any{"model_mapping": map[string]any{"alias": "actual", "invented": "missing", "wild-*": "actual"}}}
	catalog := []prism.UpstreamModel{{ID: "actual", Label: "Actual", Efforts: []string{"high"}, DefaultEffort: "high"}, {ID: "not-mapped", Label: "Other"}}
	models := PrismPublicModels(a, catalog)
	if len(models) != 1 || models[0].ID != "alias" || models[0].DefaultEffort != "high" {
		t.Fatalf("unverified public models: %+v", models)
	}
	models[0].Efforts[0] = "mutated"
	if catalog[0].Efforts[0] != "high" {
		t.Fatal("public catalog modified shared entry")
	}
	a.Credentials = map[string]any{}
	if len(PrismPublicModels(a, catalog)) != 2 {
		t.Fatal("unmapped catalog missing")
	}
}
func TestPrismAccountSafeError(t *testing.T) {
	for _, err := range []error{&creds.APIError{Status: 401, Body: "secret"}, &creds.APIError{Status: 403, Body: "secret"}, &creds.APIError{Status: 429, Body: "secret"}, errors.New("secret")} {
		if strings.Contains(safePrismError(err).Error(), "secret") {
			t.Fatal("credential leak")
		}
	}
}
func TestPrismAccountOAuthCancelExpiryReplay(t *testing.T) {
	s := NewPrismAccountService(nil)
	r, err := s.BeginOAuth(context.Background(), PrismImportRequest{})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(r.AuthorizeURL)
	if u.Query().Get("code_challenge_method") != "S256" || u.Query().Get("code_challenge") == "" {
		t.Fatal("missing PKCE")
	}
	if _, err := parsePrismCallback(prismOAuthRedirectURI+"?code=abc&state=wrong", u.Query().Get("state")); err == nil {
		t.Fatal("accepted wrong state")
	}
	if _, err := parsePrismCallback(prismOAuthRedirectURI+"?code=abc&code=def&state="+u.Query().Get("state"), u.Query().Get("state")); err == nil {
		t.Fatal("accepted duplicate code")
	}
	canceled, err := s.CancelOAuth(r.SessionID)
	if err != nil || canceled.Status != "canceled" {
		t.Fatal("cancel failed")
	}
	if _, err := s.ExchangeOAuth(context.Background(), r.SessionID, prismOAuthRedirectURI+"?code=abc&state="+u.Query().Get("state")); err == nil {
		t.Fatal("canceled exchange accepted")
	}
	r, err = s.BeginOAuth(context.Background(), PrismImportRequest{})
	if err != nil {
		t.Fatal(err)
	}
	s.sessions[r.SessionID].result.ExpiresAt = time.Now().Add(-time.Second)
	expired, err := s.OAuthStatus(r.SessionID)
	if err != nil || expired.Status != "expired" || s.sessions[r.SessionID].verifier != "" {
		t.Fatal("expiry did not erase verifier")
	}
}
func TestPrismAccountCatalogIsolation(t *testing.T) {
	a := &Account{ID: 95101, Platform: "prism", Credentials: map[string]any{"access_token": "a"}}
	defer InvalidatePrismAccountCatalog(a.ID)
	key := prismAccountFingerprint(a)
	prismCatalogCache.Lock()
	prismCatalogCache.entries[key] = prismCatalogEntry{models: []prism.UpstreamModel{{ID: "real", Label: "Real", Efforts: []string{"high"}, DefaultEffort: "high"}}, expires: time.Now().Add(time.Minute)}
	prismCatalogCache.Unlock()
	models, ok := ReadPrismAccountModels(a)
	if !ok || len(models) != 1 {
		t.Fatal("cache miss")
	}
	models[0].Efforts[0] = "mutated"
	models, _ = ReadPrismAccountModels(a)
	if models[0].Efforts[0] != "high" {
		t.Fatal("cache mutable alias")
	}
	model, effort, err := ResolvePrismAccountModel(context.Background(), a, "real", "")
	if err != nil || model != "real" || effort != "high" {
		t.Fatal("live model did not resolve")
	}
	if _, _, err = ResolvePrismAccountModel(context.Background(), a, "invented", ""); err == nil {
		t.Fatal("invented model accepted")
	}
	if _, _, err = ResolvePrismAccountModel(context.Background(), a, "real", "low"); err == nil {
		t.Fatal("unsupported effort accepted")
	}
	a.Credentials["access_token"] = "b"
	if _, ok := ReadPrismAccountModels(a); ok {
		t.Fatal("old credential catalog reused")
	}
}
func TestPrismAccountIdentityNeedsUserAndWorkspace(t *testing.T) {
	if _, err := prismVerifiedIdentity(&creds.Credential{UserID: "u"}); err == nil {
		t.Fatal("workspace-less identity accepted")
	}
	first, _ := prismVerifiedIdentity(&creds.Credential{UserID: "u", AccountID: "w1"})
	second, _ := prismVerifiedIdentity(&creds.Credential{UserID: "u", AccountID: "w2"})
	if first == second {
		t.Fatal("workspace collision")
	}
}
