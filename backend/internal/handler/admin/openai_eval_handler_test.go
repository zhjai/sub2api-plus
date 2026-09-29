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

func TestListOpenAIEvalModelsOnlyAdvertisesTextCatalogAndSampleCosts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewAccountHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.GET("/models", h.ListOpenAIEvalModels)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/models", nil))
	require.Equal(t, http.StatusOK, response.Code)

	var payload struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &payload)
	require.NotEmpty(t, payload.Items)
	for _, item := range payload.Items {
		require.NotContains(t, item.ID, "gpt-image")
		require.NotContains(t, item.ID, "embedding")
	}
	require.Len(t, payload.Items, len(service.OpenAIEvalSupportedModels()))
}

func TestOpenAIEvalHandlersFailClosedWithoutService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewAccountHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.GET("/config", h.GetOpenAIEvalConfig)
	router.PUT("/config", h.UpdateOpenAIEvalConfig)
	router.POST("/run", h.RunOpenAIEval)
	router.GET("/runs", h.ListOpenAIEvalRuns)
	router.GET("/audit", h.ListOpenAIEvalAudit)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/config"},
		{http.MethodPut, "/config"},
		{http.MethodPost, "/run"},
		{http.MethodGet, "/runs"},
		{http.MethodGet, "/audit"},
	} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(route.method, route.path, nil))
		require.Equal(t, http.StatusServiceUnavailable, recorder.Code, route.method+" "+route.path)
	}
}
