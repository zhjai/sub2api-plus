//go:build unit

package admin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func bpsNativePreserveFixture() *service.Account {
	return &service.Account{ID: 7, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Credentials: map[string]any{"client_id": openai.ClientID, "access_token": "native-synthetic", "model_mapping": map[string]any{"alias": "gpt-6-astra"}, service.ExcelBPSCredentialsKey: map[string]any{"access_token": "stale-bps"}}}
}

func TestBPSNativeAdminRefreshDoesNotMarkIndependentGrantExplicit(t *testing.T) {
	for _, route := range []string{"oauth_handler", "single_and_batch"} {
		t.Run(route, func(t *testing.T) {
			svc := newStubAdminService()
			svc.getAccountResult = bpsNativePreserveFixture()
			oauth := service.NewOpenAIOAuthService(nil, nil) // no refresh token, no privacy transport
			if route == "single_and_batch" {
				h := &AccountHandler{adminService: svc, openaiOAuthService: oauth}
				_, _, err := h.refreshSingleAccount(context.Background(), svc.getAccountResult)
				require.NoError(t, err)
			} else {
				h := NewOpenAIOAuthHandler(oauth, svc, nil, nil)
				r := gin.New()
				r.POST("/openai/accounts/:id/refresh", h.RefreshAccountToken)
				rec := httptest.NewRecorder()
				r.ServeHTTP(rec, httptest.NewRequest("POST", "/openai/accounts/7/refresh", nil))
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			}
			require.NotNil(t, svc.lastUpdateAccountInput)
			require.NotContains(t, svc.lastUpdateAccountInput.Credentials, service.ExcelBPSCredentialsKey, "stale grant must remain implicit so the locked latest grant wins")
			require.Contains(t, svc.lastUpdateAccountInput.Credentials, "model_mapping")
			require.Contains(t, svc.getAccountResult.Credentials, service.ExcelBPSCredentialsKey)
		})
	}
}

func TestBPSNativeReauthPreservesIndependentGrantIntent(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		explicit   bool
	}{
		{"implicit", `{"type":"oauth","credentials":{"access_token":"new-native"}}`, false},
		{"explicit_remove", `{"type":"oauth","credentials":{"access_token":"new-native","excel_bps_credentials":null}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newStubAdminService()
			svc.getAccountResult = bpsNativePreserveFixture()
			h := &AccountHandler{adminService: svc}
			r := gin.New()
			r.POST("/accounts/:id/oauth", h.ApplyOAuthCredentials)
			req := httptest.NewRequest("POST", "/accounts/7/oauth", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			require.Equal(t, 200, rec.Code, rec.Body.String())
			value, present := svc.lastUpdateAccountInput.Credentials[service.ExcelBPSCredentialsKey]
			require.Equal(t, tc.explicit, present)
			if tc.explicit {
				require.Nil(t, value)
			}
			require.Contains(t, svc.lastUpdateAccountInput.Credentials, "model_mapping")
		})
	}
}

func TestBPSNativeBatchFieldUpdatePreservesSettingsWithoutStaleGrant(t *testing.T) {
	svc := newStubAdminService()
	svc.getAccountResult = bpsNativePreserveFixture()
	h := &AccountHandler{adminService: svc}
	r := gin.New()
	r.POST("/batch", h.BatchUpdateCredentials)
	req := httptest.NewRequest("POST", "/batch", bytes.NewBufferString(`{"account_ids":[7],"field":"account_uuid","value":"new-uuid"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.Equal(t, "new-uuid", svc.lastUpdateAccountInput.Credentials["account_uuid"])
	require.Equal(t, openai.ClientID, svc.lastUpdateAccountInput.Credentials["client_id"])
	require.Equal(t, svc.getAccountResult.Credentials["model_mapping"], svc.lastUpdateAccountInput.Credentials["model_mapping"])
	require.NotContains(t, svc.lastUpdateAccountInput.Credentials, service.ExcelBPSCredentialsKey)
	require.NotContains(t, svc.getAccountResult.Credentials, "account_uuid")
}

type bpsBatchActualAdminRepo struct {
	service.AdminAccountRepository
	account *service.Account
	writes  int
}

func (r *bpsBatchActualAdminRepo) GetByID(context.Context, int64) (*service.Account, error) {
	a := *r.account
	a.Credentials = make(map[string]any, len(r.account.Credentials))
	for k, v := range r.account.Credentials {
		a.Credentials[k] = v
	}
	return &a, nil
}

func (r *bpsBatchActualAdminRepo) UpdateWithAccountBillingSettings(_ context.Context, a *service.Account, _, _ *bool, _ *float64) error {
	r.writes++
	service.PreserveExcelBPSIndependentGrant(a, r.account.Credentials)
	r.account = a
	return nil
}

func TestBPSNativeBatchFieldUpdatePreservesCredentialsThroughActualAdmin(t *testing.T) {
	for _, kind := range []string{"dual", "bps_only", "anthropic"} {
		t.Run(kind, func(t *testing.T) {
			claims, err := json.Marshal(map[string]any{"client_id": openai.ExcelClientID, "exp": time.Now().Add(time.Hour).Unix(), "https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "workspace"}})
			require.NoError(t, err)
			excelToken := "e30." + base64.RawURLEncoding.EncodeToString(claims) + ".synthetic"
			a := bpsNativePreserveFixture()
			a.Status = service.StatusActive
			a.Credentials["refresh_token"] = "native-refresh"
			a.Credentials["chatgpt_account_id"] = "workspace"
			a.Credentials["expires_at"] = "2099-01-01T00:00:00Z"
			a.Credentials["org_uuid"] = "organization"
			a.Credentials["plan_type"] = "pro"
			a.Credentials[service.ExcelBPSCredentialsKey] = map[string]any{"client_id": openai.ExcelClientID, "access_token": excelToken, "refresh_token": "bps-refresh"}
			if kind == "bps_only" {
				delete(a.Credentials, service.ExcelBPSCredentialsKey)
				a.Credentials["client_id"] = openai.ExcelClientID
				a.Credentials["access_token"] = excelToken
			} else if kind == "anthropic" {
				a.Platform = service.PlatformAnthropic
				delete(a.Credentials, service.ExcelBPSCredentialsKey)
			}
			expected := make(map[string]any, len(a.Credentials)+1)
			for k, v := range a.Credentials {
				expected[k] = v
			}
			expected["account_uuid"] = "new-uuid"
			repo := &bpsBatchActualAdminRepo{account: a}
			svc := service.NewAdminService(nil, nil, nil, repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			h := &AccountHandler{adminService: svc}
			r := gin.New()
			r.POST("/batch", h.BatchUpdateCredentials)
			req := httptest.NewRequest("POST", "/batch", bytes.NewBufferString(`{"account_ids":[7],"field":"account_uuid","value":"new-uuid"}`))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), `"success":1`)
			require.Equal(t, 1, repo.writes)
			require.Equal(t, expected, repo.account.Credentials)
			require.NotContains(t, a.Credentials, "account_uuid", "loaded snapshot must remain unchanged")
		})
	}
}
