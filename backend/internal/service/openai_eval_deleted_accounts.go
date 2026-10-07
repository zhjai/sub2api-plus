package service

import (
	"context"
	"errors"
	"fmt"
)

// Legacy BPS fields remain readable for storage compatibility only. They may
// never activate forwarding or autonomous probes after feature retirement.
// RetireOpenAIEvalBPS clears retired route settings without deleting probe history.
func RetireOpenAIEvalBPS(config *OpenAIEvalConfig) {
	if config == nil {
		return
	}
	config.BPSAutoEnabled = false
	config.BPSAccounts = nil
	for i := range config.Accounts {
		config.Accounts[i].BPSAuto = false
		config.Accounts[i].BPSMode = OpenAIEvalBPSModeForceOff
		config.Accounts[i].BPSState = nil
	}
}

func openAIEvalReferencedAccounts(config *OpenAIEvalConfig) map[int64]bool {
	ids := make(map[int64]bool)
	if config == nil {
		return ids
	}
	for _, rule := range config.AccountPriorityRules {
		ids[rule.AccountID] = true
	}
	for _, route := range config.Accounts {
		ids[route.AccountID] = true
	}
	for _, item := range config.BPSAccounts {
		ids[item.AccountID] = true
	}
	for _, control := range config.BackgroundControls {
		ids[control.AccountID] = true
	}
	return ids
}

// Old configs may retain deleted accounts. Only confirmed missing references
// are removed; a database/transport failure must not erase configuration.
// On writes, allowed restricts cleanup to references already stored, so a new
// invalid account ID still produces a useful validation error.
func (s *OpenAIEvalService) pruneDeletedAccountReferences(ctx context.Context, config *OpenAIEvalConfig, allowed map[int64]bool) error {
	if s.accounts == nil || config == nil {
		return nil
	}
	deleted := make(map[int64]bool)
	for id := range openAIEvalReferencedAccounts(config) {
		if id <= 0 || (allowed != nil && !allowed[id]) {
			continue
		}
		account, err := s.accounts.GetByID(ctx, id)
		if errors.Is(err, ErrAccountNotFound) || (err == nil && account == nil) {
			deleted[id] = true
		} else if err != nil {
			return fmt.Errorf("validate evaluation account %d: %w", id, err)
		}
	}
	if len(deleted) == 0 {
		return nil
	}
	rules := make([]OpenAIEvalAccountPriorityRule, 0, len(config.AccountPriorityRules))
	for _, rule := range config.AccountPriorityRules {
		if !deleted[rule.AccountID] {
			rules = append(rules, rule)
		}
	}
	routes := make([]OpenAIEvalAccountConfig, 0, len(config.Accounts))
	for _, route := range config.Accounts {
		if !deleted[route.AccountID] {
			routes = append(routes, route)
		}
	}
	bps := make([]OpenAIEvalBPSAccountConfig, 0, len(config.BPSAccounts))
	for _, item := range config.BPSAccounts {
		if !deleted[item.AccountID] {
			bps = append(bps, item)
		}
	}
	background := make([]OpenAIEvalBackgroundControl, 0, len(config.BackgroundControls))
	for _, control := range config.BackgroundControls {
		if !deleted[control.AccountID] {
			background = append(background, control)
		}
	}
	config.AccountPriorityRules, config.Accounts, config.BPSAccounts, config.BackgroundControls = rules, routes, bps, background
	return nil
}
