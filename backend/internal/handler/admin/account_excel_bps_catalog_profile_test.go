package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type bpsCatalogProfileUpstream struct {
	syncUpstreamHTTPUpstream
	seen *http.Request
}

func (u *bpsCatalogProfileUpstream) Do(req *http.Request, proxy string, id int64, conc int) (*http.Response, error) {
	u.seen = req
	return u.syncUpstreamHTTPUpstream.Do(req, proxy, id, conc)
}
func TestBPSCatalogProfileUsesIndependentGrantWithoutEnabling(t *testing.T) {
	token := buildCodexImportTestJWT(t, time.Now().Add(time.Hour), map[string]any{"client_id": openai.ExcelClientID, "https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "workspace-test", "chatgpt_plan_type": "plus"}})
	a := service.Account{ID: 42, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Concurrency: 1, Credentials: map[string]any{"client_id": openai.ClientID, "access_token": "native-test-token", service.ExcelBPSCredentialsKey: map[string]any{"client_id": openai.ExcelClientID, "access_token": token, "plan_type": "plus", "chatgpt_account_id": "workspace-test"}}}
	admin := &availableModelsAdminService{stubAdminService: newStubAdminService(), account: a}
	upstream := &bpsCatalogProfileUpstream{syncUpstreamHTTPUpstream: syncUpstreamHTTPUpstream{resp: &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"allowed":true,"model_catalog":{"models":[{"id":"gpt-6-astra"}]}}`))}}}
	cfg := &config.Config{}
	gateway := service.NewOpenAIGatewayService(nil, nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil, upstream, nil, nil, nil, nil, nil, nil, nil, nil)
	tests := service.NewAccountTestService(nil, nil, nil, nil, nil, upstream, cfg, nil)
	tests.SetOpenAIGatewayService(gateway)
	handler := &AccountHandler{adminService: admin, accountTestService: tests}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/accounts/:id/models/sync-upstream", handler.SyncUpstreamModels)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("POST", "/accounts/42/models/sync-upstream?profile=excel", nil))
	require.Equal(t, 200, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"discovery_source":"bps_access"`)
	require.NotNil(t, upstream.seen)
	require.Equal(t, "/basispoints/api/responses/access", upstream.seen.URL.Path)
	require.Equal(t, "Bearer "+token, upstream.seen.Header.Get("Authorization"))
	require.Equal(t, "native-test-token", a.GetOpenAIAccessToken())
	require.False(t, a.IsExcelBPSEnabled())
	// A missing grant cannot silently query native credentials under an Excel label.
	delete(admin.account.Credentials, service.ExcelBPSCredentialsKey)
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest("POST", "/accounts/42/models/sync-upstream?profile=excel", nil))
	require.Equal(t, 400, rec.Code)
	require.Contains(t, rec.Body.String(), "no BPS authorization")
}
