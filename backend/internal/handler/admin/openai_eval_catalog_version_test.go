package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIEvalCatalogReportsCandyPresenceVersion(t *testing.T) {
	router := gin.New()
	router.GET("/models", (&AccountHandler{}).ListOpenAIEvalModels)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/models", nil))
	require.Equal(t, http.StatusOK, response.Code)
	var result struct {
		DataVersion     string `json:"data_version"`
		BaselineVersion string `json:"baseline_version"`
		Candy           struct {
			ExpectedAnswer int `json:"expected_answer"`
		} `json:"candy"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.Equal(t, service.OpenAIEvalDataVersion, result.DataVersion)
	require.Contains(t, result.DataVersion, "candy-21-v4-number-presence")
	require.Equal(t, service.OpenAIEvalBaselineVersion, result.BaselineVersion)
	require.Equal(t, 21, result.Candy.ExpectedAnswer)
}
