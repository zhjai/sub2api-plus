package service

import (
	"fmt"
	"strings"
)

type OpenAIEvalAccountPriorityRule struct {
	AccountID       int64    `json:"account_id"`
	Priority        int      `json:"priority"`
	RequestedModels []string `json:"requested_models,omitempty"`
	Enabled         *bool    `json:"enabled,omitempty"`
}

type openAIEvalAccountPriorityIndex struct {
	allModelsPriority *int
	modelPriorities   map[string]int
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
		} else {
			if entry.modelPriorities == nil {
				entry.modelPriorities = make(map[string]int)
			}
			for _, model := range rule.RequestedModels {
				entry.modelPriorities[strings.ToLower(strings.TrimSpace(model))] = rule.Priority
			}
		}
		index[rule.AccountID] = entry
	}
	return index, nil
}

func openAIEvalAccountPriorityFromIndex(index map[int64]openAIEvalAccountPriorityIndex, accountID int64, requestedModel string) (int, bool) {
	entry, exists := index[accountID]
	if !exists {
		return 0, false
	}
	if priority, matches := entry.modelPriorities[strings.ToLower(strings.TrimSpace(requestedModel))]; matches {
		return priority, true
	}
	if entry.allModelsPriority != nil {
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

func openAIEvalAccountPriorityFor(rules []OpenAIEvalAccountPriorityRule, accountID int64, requestedModel string) (int, bool) {
	requestedModel = strings.TrimSpace(requestedModel)
	var allModelsPriority int
	var allModelsMatched bool
	for _, rule := range rules {
		if rule.AccountID != accountID || (rule.Enabled != nil && !*rule.Enabled) {
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
