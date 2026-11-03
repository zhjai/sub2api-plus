package service

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// NormalizeOpenAIEvalModelPolicies migrates only scheduling rules. Evaluation
// schedules and immutable runs retain their effort. Last occurrence wins,
// including a disabled rule, with stable ordering of the surviving rules.
func NormalizeOpenAIEvalModelPolicies(config *OpenAIEvalConfig) {
	if config == nil {
		return
	}
	last := make(map[string]int)
	for i, rule := range config.Policies {
		last[openAIEvalQualityDimension(rule.RequestedModel)] = i
	}
	rules := make([]OpenAIEvalSchedulingPolicyRule, 0, len(last))
	for i, rule := range config.Policies {
		model := openAIEvalQualityDimension(rule.RequestedModel)
		if last[model] != i {
			continue
		}
		rule.RequestedModel, rule.ReasoningEffort = model, ""
		rules = append(rules, rule)
	}
	config.Policies = rules
}

// Resolve the last rule even before a legacy configuration has been migrated.
func openAIEvalModelPolicyRule(config *OpenAIEvalConfig, model string) *OpenAIEvalSchedulingPolicyRule {
	if config != nil {
		for i := len(config.Policies) - 1; i >= 0; i-- {
			rule := &config.Policies[i]
			if openAIEvalQualityDimension(rule.RequestedModel) == openAIEvalQualityDimension(model) {
				if rule.Enabled != nil && !*rule.Enabled {
					return nil
				}
				return rule
			}
		}
	}
	return nil
}

func openAIEvalAccountModelConfigured(config *OpenAIEvalConfig, accountID int64, model string) bool {
	if config == nil {
		return false
	}
	for _, route := range config.Accounts {
		if route.AccountID == accountID && openAIEvalQualityDimension(route.RequestedModel) == openAIEvalQualityDimension(model) {
			return true
		}
	}
	return false
}

func openAIEvalModelEvidenceKey(accountID int64, model, testType string) OpenAIEvalEvidenceKey {
	return OpenAIEvalEvidenceKey{accountID, openAIEvalQualityDimension(model), "", testType}
}

func openAIEvalRunNewer(a, b OpenAIEvalRun) bool {
	return a.FinishedAt.After(b.FinishedAt) || (a.FinishedAt.Equal(b.FinishedAt) && a.ID > b.ID)
}

// OpenAIEvalUsableModelQualityRun checks the current detector contract without
// changing history. Cadence freshness is checked by the consumer separately.
func OpenAIEvalUsableModelQualityRun(run OpenAIEvalRun, now time.Time) bool {
	if run.FinishedAt.IsZero() || run.FinishedAt.After(now) {
		return false
	}
	_, ok := openAIEvalCurrentQualityAggregateFromRun(run, 300, 300, run.FinishedAt)
	return ok
}

func latestUsableModelQualityRun(latest map[OpenAIEvalEvidenceKey]OpenAIEvalRun, accountID int64, model, testType string, now time.Time) (OpenAIEvalRun, bool) {
	var selected OpenAIEvalRun
	found := false
	for _, run := range latest {
		if run.AccountID != accountID || openAIEvalQualityDimension(run.RequestedModel) != openAIEvalQualityDimension(model) || run.TestType != testType || !OpenAIEvalUsableModelQualityRun(run, now) {
			continue
		}
		if !found || openAIEvalRunNewer(run, selected) {
			selected, found = run, true
		}
	}
	return selected, found
}

// Optional repository capability preserves the effort-specific history API.
// Production implements this to look past unusable attempts at every effort.
type OpenAIEvalModelEvidenceRepository interface {
	LatestModelQualityRuns(context.Context, []OpenAIEvalEvidenceKey, time.Time) ([]OpenAIEvalRun, error)
}

func loadOpenAIEvalModelQualityRuns(ctx context.Context, repo OpenAIEvalRepository, config *OpenAIEvalConfig, now time.Time) (map[OpenAIEvalEvidenceKey]OpenAIEvalRun, error) {
	result := make(map[OpenAIEvalEvidenceKey]OpenAIEvalRun)
	routes, err := openAIEvalQualityRoutes(config)
	if err != nil {
		return nil, err
	}
	var keys []OpenAIEvalEvidenceKey
	for _, route := range routes {
		for i, testType := range openAIEvalQualityTestTypes {
			if route.Intervals[i] > 0 {
				keys = append(keys, openAIEvalModelEvidenceKey(route.AccountID, route.Model, testType))
			}
		}
	}
	if len(keys) == 0 {
		return result, nil
	}
	modelRepo, modelOK := repo.(OpenAIEvalModelEvidenceRepository)
	legacyRepo, legacyOK := repo.(OpenAIEvalLatestEvidenceRepository)
	if !modelOK && !legacyOK {
		return result, nil
	}
	// The compatibility path queries every configured effort; production uses
	// model queries so manual evidence at other efforts is included as well.
	queryKeys := keys
	if !modelOK {
		queryKeys = nil
		seen := make(map[OpenAIEvalEvidenceKey]bool)
		for _, route := range config.Accounts {
			for _, testType := range openAIEvalQualityTestTypes {
				if _, ok := openAIEvalQualityTestInterval(config, route.AccountID, route.RequestedModel, "", testType); !ok {
					continue
				}
				k := OpenAIEvalEvidenceKey{route.AccountID, openAIEvalQualityDimension(route.RequestedModel), openAIEvalQualityDimension(route.ReasoningEffort), testType}
				if !seen[k] {
					queryKeys = append(queryKeys, k)
					seen[k] = true
				}
			}
		}
	}
	selected := make(map[OpenAIEvalEvidenceKey]bool)
	for _, k := range keys {
		selected[k] = true
	}
	for start := 0; start < len(queryKeys); start += 600 {
		batch := queryKeys[start:min(start+600, len(queryKeys))]
		var runs []OpenAIEvalRun
		if modelOK {
			runs, err = modelRepo.LatestModelQualityRuns(ctx, batch, now)
		} else {
			runs, err = legacyRepo.LatestCompletedRuns(ctx, batch)
		}
		if err != nil {
			return nil, err
		}
		for _, run := range runs {
			k := openAIEvalModelEvidenceKey(run.AccountID, run.RequestedModel, run.TestType)
			if !selected[k] || !OpenAIEvalUsableModelQualityRun(run, now) {
				continue
			}
			old, found := result[k]
			if !found || openAIEvalRunNewer(run, old) {
				result[k] = run
			}
		}
	}
	return result, nil
}

// Read legacy effort-scoped caches by their payload identity, never by map
// iteration order. Runs remain the authoritative source when available.
func readOpenAIEvalModelQualityEvidence(account *Account, model, testType string, now time.Time) (OpenAIEvalQualityAggregate, bool) {
	var selected OpenAIEvalQualityAggregate
	found := false
	if account == nil {
		return selected, false
	}
	for key, value := range account.Extra {
		if !strings.HasPrefix(key, "openai_eval_quality_") {
			continue
		}
		payload, err := json.Marshal(value)
		if err != nil {
			continue
		}
		var q OpenAIEvalQualityAggregate
		if json.Unmarshal(payload, &q) != nil || q.TestType != testType || !q.validFor(account.ID, model, q.ReasoningEffort, now) {
			continue
		}
		if !found || q.EvaluatedAt.After(selected.EvaluatedAt) || (q.EvaluatedAt.Equal(selected.EvaluatedAt) && q.RunID > selected.RunID) {
			selected, found = q, true
		}
	}
	return selected, found
}
