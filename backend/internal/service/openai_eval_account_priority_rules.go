package service

import (
	"fmt"
	"math"
	"strings"
)

type OpenAIEvalAccountPriorityCondition struct {
	Metric    string  `json:"metric"`
	Operator  string  `json:"operator"`
	Threshold float64 `json:"threshold"`
}

type OpenAIEvalAccountPriorityRule struct {
	AccountID       int64                               `json:"account_id"`
	Priority        int                                 `json:"priority"`
	RequestedModels []string                            `json:"requested_models,omitempty"`
	Enabled         *bool                               `json:"enabled,omitempty"`
	Condition       *OpenAIEvalAccountPriorityCondition `json:"condition,omitempty"`
}

type openAIEvalAccountPriorityIndex struct {
	allModelsPriority  *int
	modelPriorities    map[string]int
	allModelsCondition *OpenAIEvalAccountPriorityCondition
	modelConditions    map[string]*OpenAIEvalAccountPriorityCondition
}

func buildOpenAIEvalAccountPriorityIndex(rules []OpenAIEvalAccountPriorityRule) (map[int64]openAIEvalAccountPriorityIndex, error) {
	normalized, err := normalizeOpenAIEvalAccountPriorityRules(rules)
	if err != nil {
		return nil, err
	}
	index := make(map[int64]openAIEvalAccountPriorityIndex)
	for _, rule := range normalized {
		if rule.Enabled != nil && !*rule.Enabled {
			continue
		}
		entry := index[rule.AccountID]
		if len(rule.RequestedModels) == 0 {
			priority := rule.Priority
			entry.allModelsPriority = &priority
			entry.allModelsCondition = rule.Condition
		} else {
			if entry.modelPriorities == nil {
				entry.modelPriorities = make(map[string]int)
				entry.modelConditions = make(map[string]*OpenAIEvalAccountPriorityCondition)
			}
			for _, model := range rule.RequestedModels {
				entry.modelPriorities[strings.ToLower(strings.TrimSpace(model))] = rule.Priority
				entry.modelConditions[strings.ToLower(strings.TrimSpace(model))] = rule.Condition
			}
		}
		index[rule.AccountID] = entry
	}
	return index, nil
}

func openAIEvalAccountPriorityFromIndex(index map[int64]openAIEvalAccountPriorityIndex, accountID int64, requestedModel string, factors ...OpenAIEvalRankingFactors) (int, bool) {
	entry, exists := index[accountID]
	if !exists {
		return 0, false
	}
	model := strings.ToLower(strings.TrimSpace(requestedModel))
	var evidence OpenAIEvalRankingFactors
	if len(factors) > 0 {
		evidence = factors[0]
	}
	if priority, matches := entry.modelPriorities[model]; matches && openAIEvalAccountPriorityConditionMatches(entry.modelConditions[model], evidence) {
		return priority, true
	}
	if entry.allModelsPriority != nil && openAIEvalAccountPriorityConditionMatches(entry.allModelsCondition, evidence) {
		return *entry.allModelsPriority, true
	}
	return 0, false
}

func OpenAIEvalAccountPriorityForRequest(accountID int64, requestedModel string) (int, bool) {
	snapshot := openAIEvalSchedulingPolicy.Load().(*openAIEvalSchedulingPolicySnapshot)
	return openAIEvalAccountPriorityFromIndex(snapshot.AccountPriorities, accountID, requestedModel)
}

func normalizeOpenAIEvalAccountPriorityRules(rules []OpenAIEvalAccountPriorityRule) ([]OpenAIEvalAccountPriorityRule, error) {
	if len(rules) > 5000 {
		return nil, fmt.Errorf("account priority rules exceed the 5000 rule limit")
	}
	if rules == nil {
		return nil, nil
	}
	normalized := make([]OpenAIEvalAccountPriorityRule, 0, len(rules))
	activeScopes := make(map[int64]map[string]bool)
	for index, rule := range rules {
		if rule.AccountID <= 0 {
			return nil, fmt.Errorf("account priority rule %d requires a positive account_id", index)
		}
		if rule.Enabled != nil {
			enabled := *rule.Enabled
			rule.Enabled = &enabled
		}
		if rule.Condition != nil {
			condition := *rule.Condition
			if err := validateOpenAIEvalAccountPriorityCondition(condition); err != nil {
				return nil, fmt.Errorf("account priority rule %d: %w", index, err)
			}
			rule.Condition = &condition
		}
		models := make([]string, 0, len(rule.RequestedModels))
		seenModels := make(map[string]bool)
		for _, requestedModel := range rule.RequestedModels {
			model := strings.TrimSpace(requestedModel)
			if model == "" {
				return nil, fmt.Errorf("account priority rule %d contains an empty requested model", index)
			}
			key := strings.ToLower(model)
			if seenModels[key] {
				return nil, fmt.Errorf("account priority rule %d repeats requested model %q", index, model)
			}
			seenModels[key] = true
			models = append(models, model)
		}
		if len(models) == 0 {
			rule.RequestedModels = nil
		} else {
			rule.RequestedModels = models
		}
		if rule.Enabled == nil || *rule.Enabled {
			scopes := activeScopes[rule.AccountID]
			if scopes == nil {
				scopes = make(map[string]bool)
				activeScopes[rule.AccountID] = scopes
			}
			matchingScopes := models
			if len(matchingScopes) == 0 {
				matchingScopes = []string{""}
			}
			for _, model := range matchingScopes {
				key := strings.ToLower(model)
				if scopes[key] {
					return nil, fmt.Errorf("account priority rule %d overlaps an enabled rule for account %d and requested model %q", index, rule.AccountID, model)
				}
				scopes[key] = true
			}
		}
		normalized = append(normalized, rule)
	}
	return normalized, nil
}

func openAIEvalAccountPriorityFor(rules []OpenAIEvalAccountPriorityRule, accountID int64, requestedModel string, factors ...OpenAIEvalRankingFactors) (int, bool) {
	requestedModel = strings.TrimSpace(requestedModel)
	var evidence OpenAIEvalRankingFactors
	if len(factors) > 0 {
		evidence = factors[0]
	}
	var allModelsPriority int
	var allModelsMatched bool
	for _, rule := range rules {
		if rule.AccountID != accountID || (rule.Enabled != nil && !*rule.Enabled) || !openAIEvalAccountPriorityConditionMatches(rule.Condition, evidence) {
			continue
		}
		if len(rule.RequestedModels) == 0 {
			allModelsPriority = rule.Priority
			allModelsMatched = true
			continue
		}
		for _, model := range rule.RequestedModels {
			if strings.EqualFold(strings.TrimSpace(model), requestedModel) {
				return rule.Priority, true
			}
		}
	}
	return allModelsPriority, allModelsMatched
}

func validateOpenAIEvalAccountPriorityCondition(c OpenAIEvalAccountPriorityCondition) error {
	switch c.Metric {
	case "quality_ratio", "error_rate", "ttft_ms", "price", "load_rate":
	default:
		return fmt.Errorf("unknown priority condition metric %q", c.Metric)
	}
	switch c.Operator {
	case "gte", "gt", "lte", "lt", "eq":
	default:
		return fmt.Errorf("unknown priority condition operator %q", c.Operator)
	}
	if math.IsNaN(c.Threshold) || math.IsInf(c.Threshold, 0) || c.Threshold < 0 {
		return fmt.Errorf("priority condition threshold must be finite and nonnegative")
	}
	if (c.Metric == "quality_ratio" || c.Metric == "error_rate") && c.Threshold > 1 {
		return fmt.Errorf("priority condition %s threshold must be between 0 and 1", c.Metric)
	}
	if c.Metric == "load_rate" && c.Threshold > 100 {
		return fmt.Errorf("priority condition load_rate threshold must be between 0 and 100")
	}
	return nil
}

// Raw known evidence only. Neutral scores and missing measurements are not
// observations and must never satisfy a conditional priority rule.
func openAIEvalAccountPriorityConditionMatches(c *OpenAIEvalAccountPriorityCondition, f OpenAIEvalRankingFactors) bool {
	if c == nil {
		return true
	}
	if validateOpenAIEvalAccountPriorityCondition(*c) != nil {
		return false
	}
	var value *float64
	switch c.Metric {
	case "quality_ratio":
		if f.Quality.Known {
			value = f.Quality.Ratio
		}
	case "error_rate":
		if f.ErrorRate.Known {
			value = f.ErrorRate.Value
		}
	case "ttft_ms":
		if f.TTFT.Known {
			value = f.TTFT.MS
		}
	case "price":
		if f.Price.Known {
			value = f.Price.RateMultiplier
		}
	case "load_rate":
		if f.Load.Known && f.Load.LoadRate != nil {
			v := float64(*f.Load.LoadRate)
			value = &v
		}
	}
	if value == nil || math.IsNaN(*value) || math.IsInf(*value, 0) {
		return false
	}
	switch c.Operator {
	case "gte":
		return *value >= c.Threshold
	case "gt":
		return *value > c.Threshold
	case "lte":
		return *value <= c.Threshold
	case "lt":
		return *value < c.Threshold
	case "eq":
		return *value == c.Threshold
	}
	return false
}
