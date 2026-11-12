package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/imroc/req/v3"
	"github.com/stretchr/testify/require"
)

func excelTestJWT(t *testing.T, patch map[string]any) string {
	t.Helper()
	claims := map[string]any{"client_id": openai.ExcelClientID, "exp": time.Now().Add(time.Hour).Unix(),
		"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "workspace-test", "chatgpt_plan_type": "plus"}}
	for k, v := range patch {
		claims[k] = v
	}
	raw, err := json.Marshal(claims)
	require.NoError(t, err)
	return "e30." + base64.RawURLEncoding.EncodeToString(raw) + ".test"
}

type excelOAuthClientStub struct {
	openaiOAuthClientAuthURLStub
	response           *openai.TokenResponse
	err                error
	calls              atomic.Int32
	clientID, redirect string
}

func (s *excelOAuthClientStub) ExchangeCode(ctx context.Context, code, verifier, redirect, proxy, client string) (*openai.TokenResponse, error) {
	s.calls.Add(1)
	s.clientID, s.redirect = client, redirect
	return s.response, s.err
}

func (s *excelOAuthClientStub) RefreshTokenWithClientID(ctx context.Context, token, proxy, client string) (*openai.TokenResponse, error) {
	s.clientID = client
	return s.response, s.err
}

func TestOpenAIExcelOAuthAuthorizationProfile(t *testing.T) {
	s := NewOpenAIOAuthService(nil, &openaiOAuthClientAuthURLStub{})
	defer s.Stop()
	r, err := s.GenerateAuthURLWithClient(context.Background(), nil, "", PlatformOpenAI, "excel")
	require.NoError(t, err)
	u, err := url.Parse(r.AuthURL)
	require.NoError(t, err)
	require.Equal(t, openai.ExcelAuthorizeURL, u.Scheme+"://"+u.Host+u.Path)
	q := u.Query()
	require.Equal(t, openai.ExcelClientID, q.Get("client_id"))
	require.Equal(t, openai.ExcelRedirectURI, q.Get("redirect_uri"))
	require.Equal(t, "PC", q.Get("platform"))
	require.Empty(t, q.Get("codex_cli_simplified_flow"))
	state, ok := s.sessionStore.Get(r.SessionID)
	require.True(t, ok)
	require.Equal(t, state.State, q.Get("state"))
	require.Equal(t, openai.GenerateCodeChallenge(state.CodeVerifier), q.Get("code_challenge"))
	_, err = s.GenerateAuthURLWithClient(context.Background(), nil, "https://other.invalid/callback", PlatformOpenAI, "excel")
	require.ErrorContains(t, err, "official Excel callback")
	_, err = s.GenerateAuthURLWithClient(context.Background(), nil, "", PlatformOpenAI, "arbitrary-client")
	require.ErrorContains(t, err, "unsupported OAuth client")
}

func TestOpenAIExcelOAuthStateRedirectAndOneShot(t *testing.T) {
	client := &excelOAuthClientStub{response: &openai.TokenResponse{AccessToken: excelTestJWT(t, nil), RefreshToken: "excel-rt", ExpiresIn: 3600}}
	s := NewOpenAIOAuthService(nil, client)
	defer s.Stop()
	r, err := s.GenerateAuthURLWithClient(context.Background(), nil, "", PlatformOpenAI, "excel")
	require.NoError(t, err)
	state, _ := s.sessionStore.Get(r.SessionID)
	input := &OpenAIExchangeCodeInput{SessionID: r.SessionID, Code: "code", State: "wrong"}
	_, err = s.ExchangeCode(context.Background(), input)
	require.Error(t, err)
	require.Zero(t, client.calls.Load())
	input.State = state.State
	input.RedirectURI = "https://other.invalid/callback"
	_, err = s.ExchangeCode(context.Background(), input)
	require.ErrorContains(t, err, "callback must match")
	input.RedirectURI = ""
	var wg sync.WaitGroup
	var success atomic.Int32
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			info, err := s.ExchangeCode(context.Background(), input)
			if err == nil {
				success.Add(1)
				if info.ClientID != openai.ExcelClientID || info.ChatGPTAccountID != "workspace-test" {
					t.Error("Excel grant identity lost")
				}
			}
		}()
	}
	wg.Wait()
	require.EqualValues(t, 1, success.Load())
	require.EqualValues(t, 1, client.calls.Load())
	require.Equal(t, openai.ExcelRedirectURI, client.redirect)
}

func TestOpenAIExcelOAuthFailedExchangeConsumesSession(t *testing.T) {
	client := &excelOAuthClientStub{err: errors.New("exchange failed")}
	s := NewOpenAIOAuthService(nil, client)
	defer s.Stop()
	r, err := s.GenerateAuthURLWithClient(context.Background(), nil, "", PlatformOpenAI, "excel")
	require.NoError(t, err)
	state, _ := s.sessionStore.Get(r.SessionID)
	_, err = s.ExchangeCode(context.Background(), &OpenAIExchangeCodeInput{SessionID: r.SessionID, Code: "code", State: state.State})
	require.ErrorContains(t, err, "exchange failed")
	_, ok := s.sessionStore.Get(r.SessionID)
	require.False(t, ok)
}

func TestOpenAIExcelOAuthTokenPairingAndRefresh(t *testing.T) {
	valid := excelTestJWT(t, nil)
	require.NoError(t, validateExcelOAuthTokenResponse(&openai.TokenResponse{AccessToken: valid}))
	for name, claims := range map[string]map[string]any{
		"wrong-client": {"client_id": openai.ClientID},
		"expired":      {"exp": time.Now().Add(-time.Hour).Unix()},
		"no-workspace": {"https://api.openai.com/auth": map[string]any{}},
	} {
		t.Run(name, func(t *testing.T) {
			require.Error(t, validateExcelOAuthTokenResponse(&openai.TokenResponse{AccessToken: excelTestJWT(t, claims)}))
		})
	}
	require.Error(t, validateExcelOAuthTokenResponse(&openai.TokenResponse{AccessToken: valid, IDToken: excelTestJWT(t, map[string]any{"aud": []string{openai.ClientID}})}))
	client := &excelOAuthClientStub{response: &openai.TokenResponse{AccessToken: valid, ExpiresIn: 3600}}
	s := NewOpenAIOAuthService(nil, client)
	defer s.Stop()
	info, err := s.RefreshTokenWithClientID(context.Background(), "old-excel-rt", "", openai.ExcelClientID)
	require.NoError(t, err)
	require.Equal(t, "old-excel-rt", info.RefreshToken)
	require.Equal(t, "workspace-test", info.ChatGPTAccountID)
	require.Equal(t, openai.ExcelClientID, s.BuildAccountCredentials(info)["client_id"])
	a := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"client_id": openai.ExcelClientID, "chatgpt_account_id": "other-workspace"}}
	require.Error(t, validateExcelOAuthAccount(a, valid))
}

func TestOpenAIExcelOAuthSkipsNativePrivacySideEffects(t *testing.T) {
	a := excelBPSAccount(t)
	calls := 0
	factory := func(string) (*req.Client, error) {
		calls++
		return nil, errors.New("native privacy transport must not be called")
	}
	admin := &adminServiceImpl{privacyClientFactory: factory}
	require.Empty(t, admin.EnsureOpenAIPrivacy(context.Background(), a))
	require.Empty(t, admin.ForceOpenAIPrivacy(context.Background(), a))
	refresher := &TokenRefreshService{privacyClientFactory: factory}
	refresher.ensureOpenAIPrivacy(context.Background(), a)
	require.Zero(t, calls)
}
