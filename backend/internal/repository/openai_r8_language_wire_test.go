package repository

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/imroc/req/v3"
	"github.com/stretchr/testify/require"
)

type r8QuotaAccountRepo struct {
	service.AccountRepository
	account *service.Account
}

func (r *r8QuotaAccountRepo) GetByID(context.Context, int64) (*service.Account, error) {
	return r.account, nil
}

func TestR8QuotaFirefoxClientEnglishWireHeaders(t *testing.T) {
	headers := make(chan http.Header, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers <- r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	target, err := url.Parse(server.URL)
	require.NoError(t, err)
	factory := func(proxyURL string) (*req.Client, error) {
		client, err := CreatePrivacyReqClient(proxyURL)
		if err != nil {
			return nil, err
		}
		return client.Clone().WrapRoundTripFunc(func(rt req.RoundTripper) req.RoundTripFunc {
			return func(r *req.Request) (*req.Response, error) {
				r.URL.Scheme, r.URL.Host = target.Scheme, target.Host
				return rt.RoundTrip(r)
			}
		}), nil
	}
	account := &service.Account{ID: 17, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "synthetic-token", "chatgpt_account_id": "synthetic-account", "expires_at": time.Now().Add(time.Hour).Format(time.RFC3339)}}
	repo := &r8QuotaAccountRepo{account: account}
	tokens := service.NewOpenAITokenProvider(repo, nil, nil)
	quota := service.NewOpenAIQuotaService(repo, nil, tokens, factory, nil)
	_, err = quota.QueryUsage(t.Context(), account.ID)
	require.NoError(t, err)
	require.NotEmpty(t, headers)
	for len(headers) > 0 {
		header := <-headers
		require.Equal(t, []string{"en-US,en;q=0.9"}, header.Values("Accept-Language"))
		require.Equal(t, "en-US", header.Get("oai-language"))
	}
}

func TestR8OAuthTokenEnglishWireHeaders(t *testing.T) {
	headers := make(chan http.Header, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers <- r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"synthetic-token","refresh_token":"synthetic-refresh","token_type":"bearer","expires_in":3600}`))
	}))
	defer server.Close()
	svc := &openaiOAuthService{tokenURL: server.URL}
	_, err := svc.ExchangeCode(t.Context(), "synthetic-code", "synthetic-verifier", "", "", "")
	require.NoError(t, err)
	_, err = svc.RefreshToken(t.Context(), "synthetic-refresh", "")
	require.NoError(t, err)
	for range 2 {
		require.Equal(t, []string{"en-US,en;q=0.9"}, (<-headers).Values("Accept-Language"))
	}
}
