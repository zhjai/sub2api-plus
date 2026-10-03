package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type qualityHandlerRepo struct {
	service.OpenAIEvalRepository
	action string
	actor  int64
	err    error
}

func (r *qualityHandlerRepo) GetConfig(context.Context) (*service.OpenAIEvalConfig, error) {
	return &service.OpenAIEvalConfig{}, r.err
}
func (r *qualityHandlerRepo) RecordAuditEvent(_ context.Context, actor int64, action string, payload map[string]any) error {
	r.actor, r.action = actor, action
	return r.err
}

type qualityHandlerAccounts struct{ service.AccountRepository }

func (qualityHandlerAccounts) GetByIDs(context.Context, []int64) ([]*service.Account, error) {
	return nil, errors.New("disabled effects must not read account evidence")
}

func TestOpenAIEvalQualityRefreshHandlerAuditedNoInference(t *testing.T) {
	gin.SetMode(gin.TestMode)
	previous := service.OpenAIEvalEffectsEnabled()
	service.SetOpenAIEvalEffectsEnabled(false)
	t.Cleanup(func() { service.SetOpenAIEvalEffectsEnabled(previous) })
	repo := &qualityHandlerRepo{}
	h := &AccountHandler{openAIEvalService: service.NewOpenAIEvalService(repo, qualityHandlerAccounts{}, nil)}
	router := gin.New()
	router.POST("/quality/refresh", h.RefreshOpenAIEvalQuality)
	request := httptest.NewRequest(http.MethodPost, "/quality/refresh", nil)
	request = request.WithContext(context.WithValue(request.Context(), ctxkey.UserID, int64(42)))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	var result service.OpenAIEvalQualityRefreshResult
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.False(t, result.RefreshedAt.IsZero())
	require.Equal(t, time.Hour, result.NextRefreshAt.Sub(result.RefreshedAt))
	require.Zero(t, result.RouteCount)
	require.Equal(t, int64(42), repo.actor)
	require.Equal(t, "quality_refresh_requested", repo.action)
	configPayload, err := json.Marshal(openAIEvalConfigQualityStatus(&service.OpenAIEvalConfig{}))
	require.NoError(t, err)
	var configStatus map[string]any
	require.NoError(t, json.Unmarshal(configPayload, &configStatus))
	require.Contains(t, configStatus, "quality_refreshed_at")
	require.Contains(t, configStatus, "quality_next_refresh_at")
	repo.err = errors.New("private database diagnostic")
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusInternalServerError, response.Code)
	require.NotContains(t, response.Body.String(), "private database")
}

func TestOpenAIEvalQualityRefreshHandlerUnavailable(t *testing.T) {
	router := gin.New()
	router.POST("/quality/refresh", (&AccountHandler{}).RefreshOpenAIEvalQuality)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/quality/refresh", nil))
	require.Equal(t, http.StatusServiceUnavailable, response.Code)
}

func TestOpenAIEvalModelTraceCatalogUsesActualBank(t *testing.T) {
	router := gin.New()
	router.GET("/models", (&AccountHandler{}).ListOpenAIEvalModels)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/models", nil))
	require.Equal(t, http.StatusOK, response.Code)
	var result struct {
		ModelTrace struct {
			BankRevision   string `json:"bank_revision"`
			CandidateCount int    `json:"candidate_count"`
		} `json:"modeltrace"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
	require.Equal(t, service.OpenAIEvalQualityModelTraceBankRevision, result.ModelTrace.BankRevision)
	require.Equal(t, 17, result.ModelTrace.CandidateCount)
}

func TestOpenAIEvalQualityConfigMergePreservesOmittedRefreshAndQuality(t *testing.T) {
	current := &service.OpenAIEvalConfig{QualityRefreshIntervalSeconds: 21600, CustomBalance: service.OpenAIEvalPolicyWeights{Quality: .4}}
	incoming := &service.OpenAIEvalConfig{CustomBalance: service.OpenAIEvalPolicyWeights{Cost: 1}}
	mergeOpenAIEvalConfigOmittedFields(incoming, current, map[string]json.RawMessage{"custom_balance": json.RawMessage(`{"cost":1}`)})
	require.Equal(t, 21600, incoming.QualityRefreshIntervalSeconds)
	require.Equal(t, .4, incoming.CustomBalance.Quality)
	incoming = &service.OpenAIEvalConfig{QualityRefreshIntervalSeconds: 300, CustomBalance: service.OpenAIEvalPolicyWeights{Cost: 1}}
	mergeOpenAIEvalConfigOmittedFields(incoming, current, map[string]json.RawMessage{"quality_refresh_interval_seconds": json.RawMessage(`300`), "custom_balance": json.RawMessage(`{"cost":1,"quality":0}`)})
	require.Equal(t, 300, incoming.QualityRefreshIntervalSeconds)
	require.Zero(t, incoming.CustomBalance.Quality)
}
