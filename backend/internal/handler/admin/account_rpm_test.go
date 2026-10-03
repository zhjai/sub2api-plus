package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountHardRPMImportValidatesEachAccount(t *testing.T) {
	router, admin := setupAccountDataRouter()
	accounts := make([]map[string]any, 0)
	for _, limit := range []any{nil, "2", -1, 1.5, 10001, 0, 10000} {
		accounts = append(accounts, map[string]any{
			"name": "synthetic", "platform": service.PlatformOpenAI, "type": service.AccountTypeAPIKey,
			"credentials": map[string]any{"api_key": "synthetic"},
			"extra":       map[string]any{"rpm_limit": limit}, "concurrency": 1,
		})
	}
	body, err := json.Marshal(map[string]any{"data": map[string]any{"type": dataType, "version": dataVersion, "proxies": []any{}, "accounts": accounts}})
	require.NoError(t, err)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/data", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var response struct {
		Data DataImportResult `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Equal(t, 5, response.Data.AccountFailed)
	require.Equal(t, 2, response.Data.AccountCreated)
	require.Len(t, admin.createdAccounts, 2)
	require.Equal(t, float64(0), admin.createdAccounts[0].Extra["rpm_limit"])
	require.Equal(t, float64(10000), admin.createdAccounts[1].Extra["rpm_limit"])
}
