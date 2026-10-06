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
	}
	_ = json.Unmarshal(response.Body.Bytes(), &payload)
	require.NotEmpty(t, payload.Items)
	for _, item := range payload.Items {
		require.NotContains(t, item.ID, "gpt-image")
		require.NotContains(t, item.ID, "embedding")
	}
	require.Len(t, payload.Items, len(service.OpenAIEvalSupportedModels()))
}

func TestMergeOpenAIEvalRouteBPSFieldsPreservesOmittedLegacyFields(t *testing.T) {
	previous := service.OpenAIEvalAccountConfig{BPSMode: service.OpenAIEvalBPSModeAuto, BPSAuto: true}
	incoming := service.OpenAIEvalAccountConfig{}
	mergeOpenAIEvalRouteBPSFields(&incoming, previous, map[string]json.RawMessage{})
	require.Equal(t, service.OpenAIEvalBPSModeAuto, incoming.BPSMode)
	require.True(t, incoming.BPSAuto)
}

func TestMergeOpenAIEvalRouteBPSFieldsHonorsLegacyExplicitBoolean(t *testing.T) {
	previous := service.OpenAIEvalAccountConfig{BPSMode: service.OpenAIEvalBPSModeAuto, BPSAuto: true}
	for _, test := range []struct {
		name string
		raw  string
		mode string
		auto bool
	}{
		{name: "enable", raw: "true", mode: service.OpenAIEvalBPSModeAuto, auto: true},
		{name: "disable", raw: "false", mode: service.OpenAIEvalBPSModeForceOff, auto: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			incoming := service.OpenAIEvalAccountConfig{}
			mergeOpenAIEvalRouteBPSFields(&incoming, previous, map[string]json.RawMessage{
				"bps_auto": json.RawMessage(test.raw),
			})
			require.Equal(t, test.mode, incoming.BPSMode)
			require.Equal(t, test.auto, incoming.BPSAuto)
		})
	}
}

func TestMergeOpenAIEvalRouteBPSFieldsKeepsExplicitMode(t *testing.T) {
	previous := service.OpenAIEvalAccountConfig{BPSMode: service.OpenAIEvalBPSModeAuto, BPSAuto: true}
	incoming := service.OpenAIEvalAccountConfig{BPSMode: "", BPSAuto: true}
	mergeOpenAIEvalRouteBPSFields(&incoming, previous, map[string]json.RawMessage{
		"bps_mode": json.RawMessage(`""`),
		"bps_auto": json.RawMessage("true"),
	})
	require.Empty(t, incoming.BPSMode, "an explicitly empty mode is a deliberate clear operation")
	require.True(t, incoming.BPSAuto)
}

func TestOpenAIEvalHandlersFailClosedWithoutService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewAccountHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.GET("/config", h.GetOpenAIEvalConfig)
	router.PUT("/config", h.UpdateOpenAIEvalConfig)
	router.POST("/run", h.RunOpenAIEval)
	router.POST("/bps/reset", h.ResetOpenAIEvalBPSState)
	router.GET("/runs", h.ListOpenAIEvalRuns)
	router.GET("/audit", h.ListOpenAIEvalAudit)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/config"},
		{http.MethodPut, "/config"},
		{http.MethodPost, "/run"},
		{http.MethodPost, "/bps/reset"},
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
