//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIEvalAccountPrioritySaveConfigAndClear(t *testing.T) {
	previous := openAIEvalSchedulingPolicy.Load()
	t.Cleanup(func() { openAIEvalSchedulingPolicy.Store(previous) })
	account := &Account{ID: 61, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	accounts := &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{account.ID: account}}}
	repo := &openAIEvalRepoFake{}
	service := NewOpenAIEvalService(repo, accounts, nil)
	config := &OpenAIEvalConfig{AccountPriorityRules: []OpenAIEvalAccountPriorityRule{
		{AccountID: account.ID, Priority: 1},
		{AccountID: account.ID, Priority: 8, RequestedModels: []string{" gpt-6-astra "}},
	}}
	require.NoError(t, service.SaveConfig(t.Context(), config, 9))
	require.Len(t, repo.config.AccountPriorityRules, 2)
	require.Equal(t, "gpt-6-astra", repo.config.AccountPriorityRules[1].RequestedModels[0])
	require.False(t, repo.config.EffectsEnabled)
	priority, matched := OpenAIEvalAccountPriorityForRequest(account.ID, "gpt-6-astra")
	require.True(t, matched)
	require.Equal(t, 8, priority)
	config.AccountPriorityRules = []OpenAIEvalAccountPriorityRule{}
	require.NoError(t, service.SaveConfig(t.Context(), config, 9))
	_, matched = OpenAIEvalAccountPriorityForRequest(account.ID, "gpt-6-astra")
	require.False(t, matched)
	require.Empty(t, repo.config.AccountPriorityRules)
}

func TestOpenAIEvalAccountPrioritySaveConfigRejectsMissingAccount(t *testing.T) {
	previous := openAIEvalSchedulingPolicy.Load()
	t.Cleanup(func() { openAIEvalSchedulingPolicy.Store(previous) })
	accounts := &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{}}}
	repo := &openAIEvalRepoFake{}
	service := NewOpenAIEvalService(repo, accounts, nil)
	config := &OpenAIEvalConfig{AccountPriorityRules: []OpenAIEvalAccountPriorityRule{{AccountID: 404, Priority: 1}}}
	require.ErrorContains(t, service.SaveConfig(t.Context(), config, 9), "unavailable account")
	require.Nil(t, repo.config)
}

func TestOpenAIEvalAccountPrioritySaveConfigDisabledRuleAndReenableConflict(t *testing.T) {
	previous := openAIEvalSchedulingPolicy.Load()
	t.Cleanup(func() { openAIEvalSchedulingPolicy.Store(previous) })
	account := &Account{ID: 61, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	accounts := &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{accountsByID: map[int64]*Account{account.ID: account}}}
	repo := &openAIEvalRepoFake{}
	service := NewOpenAIEvalService(repo, accounts, nil)
	disabled := false
	config := &OpenAIEvalConfig{AccountPriorityRules: []OpenAIEvalAccountPriorityRule{
		{AccountID: account.ID, Priority: 1},
		{AccountID: account.ID, Priority: 2, Enabled: &disabled},
		{AccountID: 404, Priority: 0, Enabled: &disabled},
	}}
	require.NoError(t, service.SaveConfig(t.Context(), config, 9))
	_, matched := OpenAIEvalAccountPriorityForRequest(404, "gpt-6-astra")
	require.False(t, matched)
	*config.AccountPriorityRules[1].Enabled = true
	require.ErrorContains(t, service.SaveConfig(t.Context(), config, 9), "overlaps an enabled rule")
}
