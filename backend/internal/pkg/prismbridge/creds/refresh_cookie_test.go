package creds

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/prismbridge/config"
)

func newCookieRefreshTestRefresher(t *testing.T, baseURL string) *Refresher {
	t.Helper()
	settings := config.Default()
	settings.Upstream.BaseURL = baseURL
	settings.Upstream.ForceHTTP2 = false
	settings.Creds.OAuthTokenURL = baseURL + "/oauth/token"
	refresher, err := NewRefresher(settings.Creds, settings.Upstream, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(refresher.client.CloseIdle)
	return refresher
}

func TestRefresh_ReplacesAccessTokenExpiry(t *testing.T) {
	expires := time.Now().Add(time.Hour).Truncate(time.Second)
	token := makeJWT(t, map[string]any{"exp": expires.Unix()})
	for _, oauth := range []bool{false, true} {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if oauth {
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": token, "refresh_token": "rotated"})
			} else {
				_ = json.NewEncoder(w).Encode(map[string]any{"accessToken": token, "expires": expires.Add(time.Hour).Format(time.RFC3339)})
			}
		}))
		refresher := newCookieRefreshTestRefresher(t, upstream.URL)
		old := &Credential{AccessToken: "old", RefreshToken: "refresh", SessionToken: "session", ExpiresAt: time.Now().Add(-time.Hour)}
		var next *Credential
		var err error
		if oauth {
			next, err = refresher.RefreshOAuth(context.Background(), old)
		} else {
			next, err = refresher.FetchSession(context.Background(), old)
		}
		upstream.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !next.ExpiresAt.Equal(expires) || !old.Expired(time.Now()) {
			t.Fatalf("oauth=%v expiry=%v, want=%v; input must remain expired", oauth, next.ExpiresAt, expires)
		}
	}
}

func TestFetchSessionOpaqueTokenUsesBoundedRecheck(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"accessToken": "opaque-new-token", "expires": time.Now().Add(24 * time.Hour).Format(time.RFC3339)})
	}))
	defer upstream.Close()
	refresher := newCookieRefreshTestRefresher(t, upstream.URL)
	now := time.Now()
	next, err := refresher.FetchSession(context.Background(), &Credential{AccessToken: "old", SessionToken: "session", ExpiresAt: now.Add(-time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if !next.ExpiresAt.After(now.Add(29*time.Minute)) || next.ExpiresAt.After(time.Now().Add(30*time.Minute)) {
		t.Fatalf("opaque token used session lifetime as access expiry: %s", next.ExpiresAt)
	}
}

func TestRefresh_FallbackAttemptsSessionOnce(t *testing.T) {
	var sessions atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/oauth/token" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		sessions.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"token_invalidated"}`))
	}))
	defer upstream.Close()
	refresher := newCookieRefreshTestRefresher(t, upstream.URL)
	_, err := refresher.Refresh(context.Background(), &Credential{AccessToken: "old", RefreshToken: "refresh", SessionToken: "session"})
	if err == nil || sessions.Load() != 1 {
		t.Fatalf("session attempts=%d, error=%v", sessions.Load(), err)
	}
}

func TestFetchSession_SynchronizesRotatedCookieTokens(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.SetCookie(writer, &http.Cookie{Name: CookiePrismSessionToken, Value: "session-new"})
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/auth/session" {
			_, _ = writer.Write([]byte(`{}`))
			return
		}
		http.SetCookie(writer, &http.Cookie{Name: CookiePrismRefreshToken, Value: "refresh-new"})
		_ = json.NewEncoder(writer).Encode(map[string]string{"accessToken": "access-new"})
	}))
	defer upstream.Close()
	refresher := newCookieRefreshTestRefresher(t, upstream.URL)
	original := &Credential{
		AccessToken: "access-old", RefreshToken: "refresh-old", SessionToken: "session-old",
		SessionCookieName: CookiePrismSessionToken,
		CookieHeader:      "cf_clearance=synthetic; " + CookiePrismAccessToken + "=access-old; " + CookiePrismRefreshToken + "=refresh-old; " + CookiePrismSessionToken + "=session-old",
	}
	updated, err := refresher.FetchSession(context.Background(), original)
	if err != nil {
		t.Fatal(err)
	}
	for cookieName, expected := range map[string]string{
		CookiePrismAccessToken: "access-new", CookiePrismRefreshToken: "refresh-new",
		CookiePrismSessionToken: "session-new", "cf_clearance": "synthetic",
	} {
		if actual := CookieValue(updated.EffectiveCookie(), cookieName); actual != expected {
			t.Fatalf("cookie %s = %q, want %q", cookieName, actual, expected)
		}
	}
	if updated.SessionToken != "session-new" || updated.RefreshToken != "refresh-new" {
		t.Fatal("rotated cookie fields were not updated")
	}
	if original.AccessToken != "access-old" || CookieValue(original.CookieHeader, CookiePrismAccessToken) != "access-old" {
		t.Fatal("refresh mutated the original credential")
	}
}

func TestRefreshOAuth_SynchronizesCookiesWithoutSynthesizingHeader(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"access_token": "access-new", "refresh_token": "refresh-new", "expires_in": 3600,
		})
	}))
	defer upstream.Close()
	refresher := newCookieRefreshTestRefresher(t, upstream.URL)
	for _, withHeader := range []bool{false, true} {
		original := &Credential{AccessToken: "access-old", RefreshToken: "refresh-old"}
		if withHeader {
			original.CookieHeader = "cf_clearance=synthetic; " + CookiePrismAccessToken + "=access-old; " + CookiePrismRefreshToken + "=refresh-old"
		}
		updated, err := refresher.RefreshOAuth(context.Background(), original)
		if err != nil {
			t.Fatal(err)
		}
		if !withHeader {
			if updated.CookieHeader != "" {
				t.Fatal("OAuth refresh synthesized a previously absent cookie header")
			}
			continue
		}
		if CookieValue(updated.CookieHeader, CookiePrismAccessToken) != updated.AccessToken || CookieValue(updated.CookieHeader, CookiePrismRefreshToken) != updated.RefreshToken {
			t.Fatal("cookie tokens disagree with refreshed OAuth fields")
		}
		if CookieValue(updated.CookieHeader, "cf_clearance") != "synthetic" || original.RefreshToken != "refresh-old" {
			t.Fatal("refresh discarded unrelated cookies or mutated its input")
		}
	}
}
