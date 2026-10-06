package service

import (
	"context"
	"encoding/json"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenAIEvalAccountPriorityRulesMatch(t *testing.T) {
	disabled := false
	rules, err := normalizeOpenAIEvalAccountPriorityRules([]OpenAIEvalAccountPriorityRule{
		{AccountID: 1, Priority: 1},
		{AccountID: 1, Priority: 8, RequestedModels: []string{" gpt-6-astra ", "gpt-6.1-sol"}},
		{AccountID: 2, Priority: 0, Enabled: &disabled},
	})
	require.NoError(t, err)
	for _, testcase := range []struct {
		name     string
		account  int64
		model    string
		priority int
		matched  bool
	}{
		{name: "specific overrides all despite larger priority", account: 1, model: "gpt-6-astra", priority: 8, matched: true},
		{name: "second specific model", account: 1, model: "gpt-6.1-sol", priority: 8, matched: true},
		{name: "case and whitespace normalize", account: 1, model: " GPT-6-ASTRA ", priority: 8, matched: true},
		{name: "version suffix preserved", account: 1, model: "gpt-6-astra-20261005", priority: 1, matched: true},
		{name: "unknown model uses all-model rule", account: 1, model: "another-model", priority: 1, matched: true},
		{name: "disabled rule does not match", account: 2, model: "gpt-6-astra"},
		{name: "other account does not match", account: 3, model: "gpt-6-astra"},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			priority, matched := openAIEvalAccountPriorityFor(rules, testcase.account, testcase.model)
			require.Equal(t, testcase.matched, matched)
			require.Equal(t, testcase.priority, priority)
			index, err := buildOpenAIEvalAccountPriorityIndex(rules)
			require.NoError(t, err)
			indexedPriority, indexedMatched := openAIEvalAccountPriorityFromIndex(index, testcase.account, testcase.model)
			require.Equal(t, priority, indexedPriority)
			require.Equal(t, matched, indexedMatched)
		})
	}
}

func TestOpenAIEvalAccountPriorityRulesRejectAmbiguity(t *testing.T) {
	for _, testcase := range []struct {
		name  string
		rules []OpenAIEvalAccountPriorityRule
	}{
		{name: "invalid account", rules: []OpenAIEvalAccountPriorityRule{{AccountID: 0}}},
		{name: "empty model", rules: []OpenAIEvalAccountPriorityRule{{AccountID: 1, RequestedModels: []string{" "}}}},
		{name: "repeated model in row", rules: []OpenAIEvalAccountPriorityRule{{AccountID: 1, RequestedModels: []string{"gpt-6-astra", " gpt-6-astra "}}}},
		{name: "case duplicate in row", rules: []OpenAIEvalAccountPriorityRule{{AccountID: 1, RequestedModels: []string{"gpt-6-astra", " GPT-6-ASTRA "}}}},
		{name: "case overlap across rows", rules: []OpenAIEvalAccountPriorityRule{{AccountID: 1, RequestedModels: []string{"gpt-6-astra"}}, {AccountID: 1, RequestedModels: []string{"GPT-6-ASTRA"}}}},
		{name: "duplicate all-model rule", rules: []OpenAIEvalAccountPriorityRule{{AccountID: 1, Priority: 1}, {AccountID: 1, Priority: 2}}},
		{name: "overlapping model rules", rules: []OpenAIEvalAccountPriorityRule{{AccountID: 1, RequestedModels: []string{"gpt-6-astra"}}, {AccountID: 1, RequestedModels: []string{"gpt-6.1-sol", "gpt-6-astra"}}}},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			_, err := normalizeOpenAIEvalAccountPriorityRules(testcase.rules)
			require.Error(t, err)
		})
	}
}

func TestOpenAIEvalAccountPriorityRulesCopyAndDisabledOverlap(t *testing.T) {
	disabled := false
	input := []OpenAIEvalAccountPriorityRule{
		{AccountID: 1, Priority: 1, RequestedModels: []string{" gpt-6-astra "}},
		{AccountID: 1, Priority: 2, RequestedModels: []string{"gpt-6-astra"}, Enabled: &disabled},
	}
	normalized, err := normalizeOpenAIEvalAccountPriorityRules(input)
	require.NoError(t, err)
	require.Equal(t, " gpt-6-astra ", input[0].RequestedModels[0])
	normalized[0].RequestedModels[0] = "changed"
	*normalized[1].Enabled = true
	require.Equal(t, " gpt-6-astra ", input[0].RequestedModels[0])
	require.False(t, *input[1].Enabled)
	_, err = normalizeOpenAIEvalAccountPriorityRules(normalized)
	require.NoError(t, err)
}

func TestOpenAIEvalAccountPriorityRulesSnapshotAndConfigRoundTrip(t *testing.T) {
	enabled := true
	config := &OpenAIEvalConfig{AccountPriorityRules: []OpenAIEvalAccountPriorityRule{
		{AccountID: 1, Priority: 1},
		{AccountID: 1, Priority: 8, RequestedModels: []string{"gpt-6-astra"}, Enabled: &enabled},
	}}
	payload, err := json.Marshal(config)
	require.NoError(t, err)
	var restored OpenAIEvalConfig
	require.NoError(t, json.Unmarshal(payload, &restored))
	require.Equal(t, config.AccountPriorityRules, restored.AccountPriorityRules)
	snapshot, err := newOpenAIEvalSchedulingPolicySnapshot(&restored)
	require.NoError(t, err)
	*restored.AccountPriorityRules[1].Enabled = false
	restored.AccountPriorityRules[1].RequestedModels[0] = "changed-model"
	restored.AccountPriorityRules[0].Priority = 99
	priority, matched := openAIEvalAccountPriorityFromIndex(snapshot.AccountPriorities, 1, "gpt-6-astra")
	require.True(t, matched)
	require.Equal(t, 8, priority)
	priority, matched = openAIEvalAccountPriorityFromIndex(snapshot.AccountPriorities, 1, "another-model")
	require.True(t, matched)
	require.Equal(t, 1, priority)
	cleared, err := newOpenAIEvalSchedulingPolicySnapshot(&OpenAIEvalConfig{AccountPriorityRules: []OpenAIEvalAccountPriorityRule{}})
	require.NoError(t, err)
	_, matched = openAIEvalAccountPriorityFromIndex(cleared.AccountPriorities, 1, "gpt-6-astra")
	require.False(t, matched)
	_, err = normalizeOpenAIEvalAccountPriorityRules(restored.AccountPriorityRules)
	require.NoError(t, err)
}

func TestOpenAIEvalAccountPriorityConditionMatchesKnownEvidence(t *testing.T) {
	known := OpenAIEvalRankingFactors{
		Quality:   OpenAIEvalRankingQuality{OpenAIEvalFactorMeta: rankingKnown(1, time.Now()), Ratio: rankingPtr(1.0)},
		ErrorRate: OpenAIEvalRankingErrorRate{OpenAIEvalFactorMeta: rankingKnown(0.95, time.Now()), Value: rankingPtr(0.05)},
		TTFT:      OpenAIEvalRankingTTFT{OpenAIEvalFactorMeta: rankingKnown(.5, time.Now()), MS: rankingPtr(7000.)},
		Price:     OpenAIEvalRankingPrice{OpenAIEvalFactorMeta: rankingKnown(.5, time.Now()), RateMultiplier: rankingPtr(0.06)},
		Load:      OpenAIEvalRankingLoad{OpenAIEvalFactorMeta: rankingKnown(.5, time.Now()), LoadRate: rankingPtr(20)},
	}
	for _, tc := range []struct {
		name      string
		condition OpenAIEvalAccountPriorityCondition
		want      bool
	}{
		{"quality", OpenAIEvalAccountPriorityCondition{Metric: "quality_ratio", Operator: "gte", Threshold: 1}, true},
		{"error strict", OpenAIEvalAccountPriorityCondition{Metric: "error_rate", Operator: "lt", Threshold: .05}, false},
		{"ttft", OpenAIEvalAccountPriorityCondition{Metric: "ttft_ms", Operator: "lte", Threshold: 8000}, true},
		{"price", OpenAIEvalAccountPriorityCondition{Metric: "price", Operator: "eq", Threshold: .06}, true},
		{"load", OpenAIEvalAccountPriorityCondition{Metric: "load_rate", Operator: "lt", Threshold: 25}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, openAIEvalAccountPriorityConditionMatches(&tc.condition, known))
		})
	}
	unknown := known
	unknown.Quality.Known = false
	unknown.Quality.Ratio = nil
	require.False(t, openAIEvalAccountPriorityConditionMatches(&OpenAIEvalAccountPriorityCondition{Metric: "quality_ratio", Operator: "gte", Threshold: 0}, unknown))
}

func TestOpenAIEvalAccountPriorityConditionValidation(t *testing.T) {
	for _, condition := range []OpenAIEvalAccountPriorityCondition{
		{Metric: "nope", Operator: "gte", Threshold: 1},
		{Metric: "quality_ratio", Operator: "gte", Threshold: 2},
		{Metric: "error_rate", Operator: "lt", Threshold: -0.1},
		{Metric: "load_rate", Operator: "lt", Threshold: 101},
		{Metric: "price", Operator: "unknown", Threshold: 1},
		{Metric: "price", Operator: "lt", Threshold: math.Inf(1)},
		{Metric: "ttft_ms", Operator: "gte", Threshold: math.NaN()},
	} {
		require.Error(t, validateOpenAIEvalAccountPriorityCondition(condition))
	}
}

func TestOpenAIEvalAccountPriorityDisabledConditionDoesNotMatch(t *testing.T) {
	disabled := false
	index, err := buildOpenAIEvalAccountPriorityIndex([]OpenAIEvalAccountPriorityRule{{
		AccountID: 1, Priority: 0, Enabled: &disabled,
		Condition: &OpenAIEvalAccountPriorityCondition{Metric: "load_rate", Operator: "lte", Threshold: 100},
	}})
	require.NoError(t, err)
	f := emptyOpenAIEvalRankingFactors()
	f.Load = OpenAIEvalRankingLoad{OpenAIEvalFactorMeta: rankingKnown(1, time.Now()), LoadRate: rankingPtr(0)}
	_, matched := openAIEvalAccountPriorityFromIndex(index, 1, "gpt-6-astra", f)
	require.False(t, matched)
	require.False(t, openAIEvalAccountPriorityConditionMatches(&OpenAIEvalAccountPriorityCondition{Metric: "load_rate", Operator: "lte", Threshold: 100}, emptyOpenAIEvalRankingFactors()))
}

func TestOpenAIEvalAccountPriorityConditionalModelFallsBack(t *testing.T) {
	condition := &OpenAIEvalAccountPriorityCondition{Metric: "quality_ratio", Operator: "gte", Threshold: 1}
	rules := []OpenAIEvalAccountPriorityRule{{AccountID: 1, Priority: 5}, {AccountID: 1, Priority: 0, RequestedModels: []string{"gpt-6-astra"}, Condition: condition}}
	index, err := buildOpenAIEvalAccountPriorityIndex(rules)
	require.NoError(t, err)
	condition.Threshold = 0 // published index owns a deep copy
	f := emptyOpenAIEvalRankingFactors()
	for _, model := range []string{"gpt-6-astra", "gpt-6.1-sol"} {
		priority, matched := openAIEvalAccountPriorityFromIndex(index, 1, model, f)
		require.True(t, matched)
		require.Equal(t, 5, priority)
	}
	f.Quality = OpenAIEvalRankingQuality{OpenAIEvalFactorMeta: rankingKnown(1, time.Now()), Ratio: rankingPtr(1.)}
	priority, matched := openAIEvalAccountPriorityFromIndex(index, 1, "gpt-6-astra", f)
	require.True(t, matched)
	require.Zero(t, priority)
	priority, matched = openAIEvalAccountPriorityFromIndex(index, 1, "gpt-6.1-sol", f)
	require.True(t, matched)
	require.Equal(t, 5, priority)
	data, err := json.Marshal(rules)
	require.NoError(t, err)
	var restored []OpenAIEvalAccountPriorityRule
	require.NoError(t, json.Unmarshal(data, &restored))
	require.Equal(t, rules, restored)
}

func TestOpenAIEvalDisabledModelRuleDoesNotAffectPolicyOrWeights(t *testing.T) {
	disabled := false
	config := &OpenAIEvalConfig{
		SchedulingPolicy: OpenAIEvalSchedulingPolicyCostFirst,
		CustomBalance:    OpenAIEvalPolicyWeights{Cost: 1},
		Policies: []OpenAIEvalSchedulingPolicyRule{
			{RequestedModel: "gpt-6.1-sol", Policy: OpenAIEvalSchedulingPolicyAvoidDegradation, Enabled: &disabled},
			{RequestedModel: "gpt-6.1-sol", ReasoningEffort: "high", Policy: OpenAIEvalSchedulingPolicyCustomBalance,
				CustomBalance: &OpenAIEvalPolicyWeights{Quality: 1}, Enabled: &disabled},
		},
	}
	require.Equal(t, OpenAIEvalSchedulingPolicyCostFirst, OpenAIEvalSchedulingPolicyFor(config, "gpt-6.1-sol", "high"))
	policy, weights := openAIEvalRankingWeights(config, "gpt-6.1-sol", "high")
	require.Equal(t, OpenAIEvalSchedulingPolicyCostFirst, policy)
	require.Equal(t, 1.0, weights.Price)
	require.Zero(t, weights.Quality)
}

func TestOpenAIEvalLegacyModelRuleWithoutEnabledRemainsActive(t *testing.T) {
	config := &OpenAIEvalConfig{SchedulingPolicy: OpenAIEvalSchedulingPolicyCostFirst, Policies: []OpenAIEvalSchedulingPolicyRule{
		{RequestedModel: "gpt-6.1-sol", Policy: OpenAIEvalSchedulingPolicyAvoidDegradation},
	}}
	require.Equal(t, OpenAIEvalSchedulingPolicyAvoidDegradation, OpenAIEvalSchedulingPolicyFor(config, "gpt-6.1-sol", "high"))
}

func TestOpenAIEvalInvalidAccountPrioritySnapshotPreservesAcceptedState(t *testing.T) {
	previous := openAIEvalSchedulingPolicy.Load()
	cache := &openAIEvalQualitySnapshotStore{}
	t.Cleanup(func() { openAIEvalSchedulingPolicy.Store(previous) })
	valid := &OpenAIEvalConfig{Revision: 10, AccountPriorityRules: []OpenAIEvalAccountPriorityRule{{AccountID: 2, Priority: 1}}}
	require.NoError(t, cache.configure(valid))
	accepted := openAIEvalSchedulingPolicy.Load()
	generation := cache.generation
	invalid := &OpenAIEvalConfig{Revision: 11, AccountPriorityRules: []OpenAIEvalAccountPriorityRule{{AccountID: 2, Priority: 1}, {AccountID: 2, Priority: 2}}}
	snapshot, err := newOpenAIEvalSchedulingPolicySnapshot(invalid)
	require.ErrorContains(t, err, "build account priority rules")
	require.Nil(t, snapshot)
	require.ErrorContains(t, cache.configure(invalid), "build account priority rules")
	require.Same(t, accepted, openAIEvalSchedulingPolicy.Load())
	require.Equal(t, int64(10), cache.configRevision)
	require.Equal(t, generation, cache.generation)
	priority, matched := OpenAIEvalAccountPriorityForRequest(2, "gpt-6.1-sol")
	require.True(t, matched)
	require.Equal(t, 1, priority)
}

func TestOpenAIEvalInvalidStoredAccountPriorityRejectsInitializationAndRead(t *testing.T) {
	service, repo, _ := setupQualityRefreshTest(t)
	repo.config.AccountPriorityRules = []OpenAIEvalAccountPriorityRule{{AccountID: 2, Priority: 1}, {AccountID: 2, Priority: 2}}
	require.ErrorContains(t, service.Initialize(context.Background()), "build account priority rules")
	_, err := service.GetConfig(context.Background())
	require.ErrorContains(t, err, "build account priority rules")
}
