package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCodexImportRejectsExcelGrantAcrossAllRecognizedSources(t *testing.T) {
	token := func(claims map[string]any) string {
		return buildCodexImportTestJWT(t, time.Now().Add(time.Hour), claims)
	}
	excelAT := token(map[string]any{"client_id": openai.ExcelClientID})
	for name, value := range map[string]any{
		"raw token":                            excelAT,
		"client":                               map[string]any{"client_id": openai.ExcelClientID, "access_token": "opaque"},
		"azp":                                  map[string]any{"access_token": token(map[string]any{"azp": openai.ExcelClientID}), "refresh_token": "synthetic"},
		"id audience":                          map[string]any{"access_token": "opaque", "id_token": token(map[string]any{"aud": openai.ExcelClientID})},
		"id audience array":                    map[string]any{"access_token": "opaque", "id_token": token(map[string]any{"aud": []string{"other", openai.ExcelClientID}})},
		"tokens with conflicting outer client": map[string]any{"client_id": openai.ClientID, "tokens": map[string]any{"access_token": excelAT, "refresh_token": "synthetic"}},
		"credentials":                          map[string]any{"access_token": "opaque", "credentials": map[string]any{"client_id": openai.ExcelClientID}},
		"session info":                         map[string]any{"session_info": map[string]any{"access_token": excelAT}},
	} {
		t.Run(name, func(t *testing.T) {
			item, err := normalizeCodexImportEntry(codexImportEntry{Index: 1, Value: value})
			require.ErrorContains(t, err, "BPS Excel OAuth")
			require.Nil(t, item)
		})
	}
	item, err := normalizeCodexImportEntry(codexImportEntry{Index: 1, Value: map[string]any{"access_token": token(map[string]any{"client_id": openai.ClientID}), "refresh_token": "synthetic"}})
	require.NoError(t, err)
	require.Equal(t, openai.ClientID, item.Credentials["client_id"])
}

func TestCodexImportIndexNeverMatchesExistingExcelAccount(t *testing.T) {
	native := service.Account{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Credentials: map[string]any{"client_id": openai.ClientID, "chatgpt_account_id": "shared", "chatgpt_user_id": "member", "email": "same@example.invalid"}}
	excel := native
	excel.ID = 2
	excel.Credentials = map[string]any{"client_id": openai.ExcelClientID, "chatgpt_account_id": "shared", "chatgpt_user_id": "member", "email": "same@example.invalid"}
	keys := buildCodexImportIdentityKeys("shared", "member", "same@example.invalid", "new", "refresh")
	onlyExcel := buildCodexAccountIndex([]service.Account{excel})
	matched, _ := onlyExcel.Find(keys, "member")
	require.Nil(t, matched)
	mixed := buildCodexAccountIndex([]service.Account{excel, native})
	matched, _ = mixed.Find(keys, "member")
	require.NotNil(t, matched)
	require.EqualValues(t, native.ID, matched.ID)
}

func TestBPSAvailableModelsNeverFallsBackToGlobalDefaults(t *testing.T) {
	svc := &availableModelsAdminService{stubAdminService: newStubAdminService(), account: service.Account{ID: 42, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Credentials: map[string]any{"client_id": openai.ExcelClientID}}}
	router := setupAvailableModelsRouter(svc)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/42/models", nil))
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Contains(t, recorder.Body.String(), "BPS model discovery")
	require.NotContains(t, recorder.Body.String(), "gpt-image")
}

func TestBPSAvailableModelsMappingCannotReplaceUnavailableDiscovery(t *testing.T) {
	handler := &AccountHandler{accountTestService: &service.AccountTestService{}}
	account := &service.Account{ID: 42, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Credentials: map[string]any{"client_id": openai.ExcelClientID}}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/models", nil)
	handler.respondAvailableModels(c, account)
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.Contains(t, recorder.Body.String(), "OpenAI model discovery service is unavailable")
	account.Credentials["model_mapping"] = map[string]any{"my-bps": "gpt-6-astra"}
	recorder = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/models", nil)
	handler.respondAvailableModels(c, account)
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.NotContains(t, recorder.Body.String(), "my-bps")
	require.NotContains(t, recorder.Body.String(), "gpt-image")
}
