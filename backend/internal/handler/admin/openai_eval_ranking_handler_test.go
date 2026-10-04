package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type rankingHandlerRepo struct {
	service.OpenAIEvalRepository
	config service.OpenAIEvalConfig
	audits int
}

func (r *rankingHandlerRepo) GetConfig(context.Context) (*service.OpenAIEvalConfig, error) {
	copy := r.config
	return &copy, nil
}
func (r *rankingHandlerRepo) SaveConfig(_ context.Context, c *service.OpenAIEvalConfig, _ int64) error {
	c.Revision++
	r.config = *c
	return nil
}
func (r *rankingHandlerRepo) RecordAuditEvent(context.Context, int64, string, map[string]any) error {
	r.audits++
	return nil
}

type rankingHandlerGroups struct {
	service.GroupRepository
	err error
}

func (r *rankingHandlerGroups) List(context.Context, pagination.PaginationParams) ([]service.Group, *pagination.PaginationResult, error) {
	return nil, nil, r.err
}

type rankingHandlerAccounts struct{ service.AccountRepository }

func (rankingHandlerAccounts) ListWithFilters(context.Context, pagination.PaginationParams, string, string, string, string, int64, string) ([]service.Account, *pagination.PaginationResult, error) {
	return nil, nil, nil
}

func TestOpenAIRankingHandlersEvaluateReadPaginationAndSavedError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &rankingHandlerRepo{}
	groups := &rankingHandlerGroups{}
	eval := service.NewOpenAIEvalService(repo, rankingHandlerAccounts{}, nil)
	service.NewOpenAIEvalRankingService(eval, groups, nil, nil, nil, nil)
	h := &AccountHandler{openAIEvalService: eval}
	router := gin.New()
	router.POST("/evaluate", h.EvaluateOpenAIScheduling)
	router.GET("/rankings", h.GetOpenAISchedulingRankings)
	router.GET("/account-overview", h.GetOpenAISchedulingAccountOverview)
	router.PUT("/config", h.UpdateOpenAIEvalConfig)
	router.POST("/refresh", h.RefreshOpenAIEvalQuality)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		return w
	}
	response := request("POST", "/evaluate", "{}")
	require.Equal(t, 200, response.Code)
	var summary service.OpenAIEvalRankingSummary
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &summary))
	require.NotEmpty(t, summary.EvaluationID)
	require.Equal(t, "policy_evaluation", summary.RecordType)
	response = request("GET", "/rankings?limit=1", "")
	require.Equal(t, 200, response.Code)
	var snapshot service.OpenAIEvalRankingSnapshot
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &snapshot))
	require.Equal(t, summary.EvaluationID, snapshot.Summary.EvaluationID)
	require.Equal(t, "inactive_effects_off", snapshot.EffectiveStatus)
	response = request("GET", "/account-overview", "")
	require.Equal(t, 200, response.Code)
	var overview service.OpenAIEvalAccountOverview
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &overview))
	require.Equal(t, summary.EvaluationID, overview.Summary.EvaluationID)
	require.NotNil(t, overview.Accounts)
	require.Nil(t, overview.PreviousSummary)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &fields))
	for _, name := range []string{"summary", "previous_summary", "effective_status", "current_config_revision", "evaluation_in_progress", "ranking_error", "groups", "next_cursor", "policy", "weights", "ordering", "accounts"} {
		require.Contains(t, fields, name)
	}
	for _, query := range []string{"limit=501", "limit=0", "group_id=-1", "cursor=invalid", "limit=no"} {
		require.Equal(t, 400, request("GET", "/rankings?"+query, "").Code)
		require.Equal(t, 400, request("GET", "/account-overview?"+query, "").Code)
	}
	require.Equal(t, 409, request("GET", "/rankings?evaluation_id=old-instance", "").Code)
	require.Equal(t, 409, request("GET", "/account-overview?evaluation_id=old-instance", "").Code)
	response = request("POST", "/refresh", "{}")
	require.Equal(t, 200, response.Code)
	var alias map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &alias))
	require.Contains(t, alias, "evaluation_id")
	require.Contains(t, alias, "refreshed_at")
	require.Equal(t, float64(0), alias["route_count"])
	response = request("GET", "/account-overview?limit=500&group_id=0", "")
	require.Equal(t, 200, response.Code)
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &overview))
	require.Equal(t, summary.EvaluationID, overview.PreviousSummary.EvaluationID)
	groups.err = errors.New("private diagnostic must not escape")
	response = request("PUT", "/config", `{"effects_enabled":false,"scheduling_policy":"cost_first","accounts":[]}`)
	require.Equal(t, 200, response.Code)
	var saved map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &saved))
	require.Equal(t, float64(1), saved["saved_revision"])
	require.NotNil(t, saved["ranking_error"])
	require.NotContains(t, response.Body.String(), "private diagnostic")
	require.Equal(t, "cost_first", repo.config.SchedulingPolicy)
	response = request("GET", "/account-overview", "")
	require.Equal(t, 200, response.Code)
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &overview))
	require.NotNil(t, overview.RankingError)
	require.NotNil(t, overview.Summary)
	require.NotNil(t, overview.PreviousSummary)
	require.NotContains(t, response.Body.String(), "private diagnostic")
}

func TestOpenAIRankingHandlerUnavailableAndErrorMapping(t *testing.T) {
	for _, test := range []struct {
		err    error
		status int
		code   string
	}{{service.ErrOpenAIEvalRankingSuperseded, 409, "EVALUATION_SUPERSEDED"}, {service.ErrOpenAIEvalRankingSnapshotChanged, 409, "RANKING_SNAPSHOT_CHANGED"}, {service.ErrOpenAIEvalRankingUnavailable, 503, "EVALUATION_SERVICE_UNAVAILABLE"}, {context.DeadlineExceeded, 504, "EVALUATION_TIMEOUT"}} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		openAIEvalRankingHTTPError(c, test.err)
		require.Equal(t, test.status, w.Code)
		require.Contains(t, w.Body.String(), test.code)
	}
	router := gin.New()
	router.POST("/evaluate", (&AccountHandler{}).EvaluateOpenAIScheduling)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/evaluate", nil))
	require.Equal(t, 503, w.Code)
}
