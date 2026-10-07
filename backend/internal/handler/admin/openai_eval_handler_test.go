package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
		ModelTrace struct {
			Models         []string `json:"models"`
			CandidateCount int      `json:"candidate_count"`
		} `json:"modeltrace"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &payload)
	require.NotEmpty(t, payload.Items)
	for _, item := range payload.Items {
		require.NotContains(t, item.ID, "gpt-image")
		require.NotContains(t, item.ID, "embedding")
	}
	require.Len(t, payload.Items, len(service.OpenAIEvalSupportedModels()))
	require.Equal(t, service.OpenAIEvalModelTraceModels(), payload.ModelTrace.Models)
	require.Len(t, payload.ModelTrace.Models, payload.ModelTrace.CandidateCount)
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

func TestMergeOpenAIEvalConfigPreservesThresholdsForOlderClients(t *testing.T) {
	thresholds := &service.OpenAIEvalSchedulingThresholds{MinErrorSamples: 17, MinTTFTSamples: 23}
	current := &service.OpenAIEvalConfig{SchedulingThresholds: thresholds}
	incoming := &service.OpenAIEvalConfig{}
	mergeOpenAIEvalConfigOmittedFields(incoming, current, map[string]json.RawMessage{})
	require.Equal(t, thresholds, incoming.SchedulingThresholds)
	incoming.SchedulingThresholds.MinErrorSamples = 31
	require.EqualValues(t, 17, thresholds.MinErrorSamples)
	explicit := &service.OpenAIEvalConfig{SchedulingThresholds: &service.OpenAIEvalSchedulingThresholds{MinErrorSamples: 1}}
	mergeOpenAIEvalConfigOmittedFields(explicit, current, map[string]json.RawMessage{"scheduling_thresholds": json.RawMessage(`{}`)})
	require.EqualValues(t, 1, explicit.SchedulingThresholds.MinErrorSamples)
}

func TestMergeOpenAIEvalConfigPreservesAttemptsForOlderClients(t *testing.T) {
	current := &service.OpenAIEvalConfig{MaxRequestAttempts: 8}
	incoming := &service.OpenAIEvalConfig{}
	mergeOpenAIEvalConfigOmittedFields(incoming, current, map[string]json.RawMessage{})
	require.Equal(t, 8, incoming.MaxRequestAttempts)
	incoming.MaxRequestAttempts = 1
	mergeOpenAIEvalConfigOmittedFields(incoming, current, map[string]json.RawMessage{"max_request_attempts": json.RawMessage("1")})
	require.Equal(t, 1, incoming.MaxRequestAttempts)
}

func TestMergeOpenAIEvalConfigPreservesAccountPriorityRulesAndDisabledState(t *testing.T) {
	disabled := false
	current := &service.OpenAIEvalConfig{AccountPriorityRules: []service.OpenAIEvalAccountPriorityRule{
		{AccountID: 7, Priority: 1, RequestedModels: []string{"gpt-6.1-sol"}, Enabled: &disabled},
	}}
	incoming := &service.OpenAIEvalConfig{}
	mergeOpenAIEvalConfigOmittedFields(incoming, current, map[string]json.RawMessage{})
	require.Len(t, incoming.AccountPriorityRules, 1)
	require.NotSame(t, current.AccountPriorityRules[0].Enabled, incoming.AccountPriorityRules[0].Enabled)
	require.False(t, *incoming.AccountPriorityRules[0].Enabled)
	incoming.AccountPriorityRules[0].RequestedModels[0] = "changed"
	require.Equal(t, "gpt-6.1-sol", current.AccountPriorityRules[0].RequestedModels[0])

	// A client that sends the list but predates the enabled field must not turn
	// an explicitly disabled saved rule back on.
	incoming = &service.OpenAIEvalConfig{AccountPriorityRules: []service.OpenAIEvalAccountPriorityRule{{AccountID: 7, Priority: 1, RequestedModels: []string{"gpt-6.1-sol"}}}}
	mergeOpenAIEvalConfigOmittedFields(incoming, current, map[string]json.RawMessage{
		"account_priority_rules": json.RawMessage(`[{"account_id":7,"priority":1,"requested_models":["gpt-6.1-sol"]}]`),
	})
	require.NotNil(t, incoming.AccountPriorityRules[0].Enabled)
	require.False(t, *incoming.AccountPriorityRules[0].Enabled)

	// Legacy clients may reorder rules while omitting the switch field. State
	// follows the rule identity, not its array position.
	otherDisabled := false
	current = &service.OpenAIEvalConfig{AccountPriorityRules: []service.OpenAIEvalAccountPriorityRule{
		{AccountID: 7, Priority: 1, RequestedModels: []string{"gpt-6.1-sol"}, Enabled: &disabled},
		{AccountID: 8, Priority: 2, RequestedModels: []string{"gpt-6-astra"}, Enabled: &otherDisabled},
	}}
	incoming = &service.OpenAIEvalConfig{AccountPriorityRules: []service.OpenAIEvalAccountPriorityRule{
		{AccountID: 8, Priority: 9, RequestedModels: []string{" GPT-6-ASTRA "}},
		{AccountID: 7, Priority: 3, RequestedModels: []string{"gpt-6.1-sol"}},
	}}
	mergeOpenAIEvalConfigOmittedFields(incoming, current, map[string]json.RawMessage{
		"account_priority_rules": json.RawMessage(`[{"account_id":8,"priority":9,"requested_models":[" GPT-6-ASTRA "]},{"account_id":7,"priority":3,"requested_models":["gpt-6.1-sol"]}]`),
	})
	require.NotNil(t, incoming.AccountPriorityRules[0].Enabled)
	require.False(t, *incoming.AccountPriorityRules[0].Enabled)
	require.NotNil(t, incoming.AccountPriorityRules[1].Enabled)
	require.False(t, *incoming.AccountPriorityRules[1].Enabled)
}

func TestOpenAIEvalAccountPriorityConditionCloneAndMergeKey(t *testing.T) {
	rule := service.OpenAIEvalAccountPriorityRule{AccountID: 7, Priority: 1, Condition: &service.OpenAIEvalAccountPriorityCondition{Metric: "quality_ratio", Operator: "gte", Threshold: 1}}
	copy := cloneOpenAIEvalAccountPriorityRules([]service.OpenAIEvalAccountPriorityRule{rule})
	require.NotSame(t, rule.Condition, copy[0].Condition)
	require.Equal(t, openAIEvalAccountPriorityRuleMergeKey(rule), openAIEvalAccountPriorityRuleMergeKey(copy[0]))
	copy[0].Condition.Threshold = .5
	require.Equal(t, 1., rule.Condition.Threshold)
	require.NotEqual(t, openAIEvalAccountPriorityRuleMergeKey(rule), openAIEvalAccountPriorityRuleMergeKey(copy[0]))
}

func TestMergeOpenAIEvalConfigPreservesOmittedLegacyCondition(t *testing.T) {
	disabled := false
	current := &service.OpenAIEvalConfig{AccountPriorityRules: []service.OpenAIEvalAccountPriorityRule{
		{AccountID: 7, Priority: 1, RequestedModels: []string{"gpt-6.1-sol"}, Enabled: &disabled,
			Condition: &service.OpenAIEvalAccountPriorityCondition{Metric: "quality_ratio", Operator: "gte", Threshold: 1}},
	}}
	incoming := &service.OpenAIEvalConfig{AccountPriorityRules: []service.OpenAIEvalAccountPriorityRule{{
		AccountID: 7, Priority: 4, RequestedModels: []string{"gpt-6.1-sol"},
	}}}
	mergeOpenAIEvalConfigOmittedFields(incoming, current, map[string]json.RawMessage{
		"account_priority_rules": json.RawMessage(`[{"account_id":7,"priority":4,"requested_models":["gpt-6.1-sol"]}]`),
	})
	require.NotNil(t, incoming.AccountPriorityRules[0].Condition)
	require.Equal(t, "quality_ratio", incoming.AccountPriorityRules[0].Condition.Metric)
	require.NotNil(t, incoming.AccountPriorityRules[0].Enabled)
	require.False(t, *incoming.AccountPriorityRules[0].Enabled)

	// An explicit null remains a deliberate request to clear the condition.
	incoming = &service.OpenAIEvalConfig{AccountPriorityRules: []service.OpenAIEvalAccountPriorityRule{{
		AccountID: 7, Priority: 4, RequestedModels: []string{"gpt-6.1-sol"},
	}}}
	mergeOpenAIEvalConfigOmittedFields(incoming, current, map[string]json.RawMessage{
		"account_priority_rules": json.RawMessage(`[{"account_id":7,"priority":4,"requested_models":["gpt-6.1-sol"],"condition":null}]`),
	})
	require.Nil(t, incoming.AccountPriorityRules[0].Condition)
}

func TestMergeOpenAIEvalConfigPreservesDisabledModelRuleForOlderClients(t *testing.T) {
	disabled := false
	current := &service.OpenAIEvalConfig{Policies: []service.OpenAIEvalSchedulingPolicyRule{
		{RequestedModel: "gpt-6.1-sol", Policy: service.OpenAIEvalSchedulingPolicyAvoidDegradation, Enabled: &disabled},
	}}
	incoming := &service.OpenAIEvalConfig{Policies: []service.OpenAIEvalSchedulingPolicyRule{
		{RequestedModel: "gpt-6.1-sol", Policy: service.OpenAIEvalSchedulingPolicyAvoidDegradation},
	}}
	mergeOpenAIEvalConfigOmittedFields(incoming, current, map[string]json.RawMessage{
		"policies": json.RawMessage(`[{"requested_model":"gpt-6.1-sol","policy":"avoid_degradation"}]`),
	})
	require.NotNil(t, incoming.Policies[0].Enabled)
	require.False(t, *incoming.Policies[0].Enabled)
}

func TestOpenAIEvalConfigRejectsInvalidExplicitAttempts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &AccountHandler{openAIEvalService: service.NewOpenAIEvalService(nil, nil, nil)}
	router := gin.New()
	router.PUT("/config", h.UpdateOpenAIEvalConfig)
	for _, value := range []string{"0", "-1", "11", "1.5", "null", `"3"`} {
		r := httptest.NewRecorder()
		router.ServeHTTP(r, httptest.NewRequest(http.MethodPut, "/config", strings.NewReader(`{"max_request_attempts":`+value+`}`)))
		require.Equal(t, http.StatusBadRequest, r.Code, value)
	}
}

func TestOpenAIEvalConfigTypeErrorReportsFieldWithoutValue(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &AccountHandler{openAIEvalService: service.NewOpenAIEvalService(nil, nil, nil)}
	router := gin.New()
	router.PUT("/config", h.UpdateOpenAIEvalConfig)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPut, "/config", strings.NewReader(`{"account_priority_rules":[{"account_id":"private-value"}]}`)))
	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Contains(t, response.Body.String(), "account_priority_rules")
	require.Contains(t, response.Body.String(), "account_id")
	require.Contains(t, response.Body.String(), "int64")
	require.NotContains(t, response.Body.String(), "private-value")
}

func TestMergeOpenAIEvalAccountRuleAliasesPreservesDisabledCondition(t *testing.T) {
	for _, pair := range [][2]string{{"gpt-6", "gpt-6-astra"}, {"gpt-5.6", "gpt-5.6-sol"}} {
		disabled := false
		current := &service.OpenAIEvalConfig{AccountPriorityRules: []service.OpenAIEvalAccountPriorityRule{{
			AccountID: 7, Priority: 1, RequestedModels: []string{pair[1]}, Enabled: &disabled,
			Condition: &service.OpenAIEvalAccountPriorityCondition{Metric: "quality_ratio", Operator: "gte", Threshold: 1},
		}}}
		incoming := &service.OpenAIEvalConfig{AccountPriorityRules: []service.OpenAIEvalAccountPriorityRule{{
			AccountID: 7, Priority: 3, RequestedModels: []string{pair[0]},
		}}}
		mergeOpenAIEvalConfigOmittedFields(incoming, current, map[string]json.RawMessage{
			"account_priority_rules": json.RawMessage(`[{"account_id":7,"priority":3}]`),
		})
		require.NotNil(t, incoming.AccountPriorityRules[0].Enabled)
		require.False(t, *incoming.AccountPriorityRules[0].Enabled)
		require.Equal(t, current.AccountPriorityRules[0].Condition, incoming.AccountPriorityRules[0].Condition)
	}
}
