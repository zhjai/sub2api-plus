//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type deletedEvalAccounts struct {
	AccountRepository
	items map[int64]*Account
	err   error
}

func (r *deletedEvalAccounts) GetByID(_ context.Context, int64ID int64) (*Account, error) {
	if r.err != nil {
		return nil, r.err
	}
	if account := r.items[int64ID]; account != nil {
		return account, nil
	}
	return nil, ErrAccountNotFound
}

func legacyDeletedEvalConfig() *OpenAIEvalConfig {
	return &OpenAIEvalConfig{Revision: 7,
		Accounts: []OpenAIEvalAccountConfig{
			{AccountID: 1, RequestedModel: "gpt-6-astra"},
			{AccountID: 2, RequestedModel: "gpt-6-astra"},
		},
		AccountPriorityRules: []OpenAIEvalAccountPriorityRule{{AccountID: 1, Priority: 1}, {AccountID: 2, Priority: 2}},
		BPSAccounts:          []OpenAIEvalBPSAccountConfig{{AccountID: 1, Mode: OpenAIEvalBPSModeForceOff}},
	}
}

func TestOpenAIEvalDeletedReferencesReadAndSave(t *testing.T) {
	previous, previousQuality := openAIEvalSchedulingPolicy.Load(), openAIEvalQualitySnapshots
	openAIEvalQualitySnapshots = &openAIEvalQualitySnapshotStore{}
	t.Cleanup(func() {
		openAIEvalSchedulingPolicy.Store(previous)
		openAIEvalQualitySnapshots = previousQuality
	})
	accounts := &deletedEvalAccounts{items: map[int64]*Account{2: {ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}}}
	repo := &openAIEvalRepoFake{config: legacyDeletedEvalConfig(), runs: []*OpenAIEvalRun{{ID: 5, AccountID: 1, Status: "pass"}}}
	svc := NewOpenAIEvalService(repo, accounts, nil)
	require.NoError(t, svc.Initialize(t.Context()))
	view, err := svc.GetConfig(t.Context())
	require.NoError(t, err)
	require.Len(t, view.Accounts, 1)
	require.Equal(t, int64(2), view.Accounts[0].AccountID)
	require.Len(t, view.AccountPriorityRules, 1)
	require.Empty(t, view.BPSAccounts)
	require.Len(t, repo.config.Accounts, 2, "reads do not persist cleanup")
	// An already-open tests tab can still submit the obsolete references.
	draft := legacyDeletedEvalConfig()
	draft.MaxRequestAttempts = 4
	require.NoError(t, svc.SaveConfig(t.Context(), draft, 9))
	require.Len(t, repo.config.Accounts, 1)
	require.Len(t, repo.config.AccountPriorityRules, 1)
	require.Empty(t, repo.config.BPSAccounts)
	require.Equal(t, 4, repo.config.MaxRequestAttempts)
	require.Len(t, repo.runs, 1, "historical test records are retained")
}

func TestOpenAIEvalDeletedCleanupPreservesConfigOnLookupFailure(t *testing.T) {
	repo := &openAIEvalRepoFake{config: legacyDeletedEvalConfig()}
	accounts := &deletedEvalAccounts{err: errors.New("database unavailable")}
	svc := NewOpenAIEvalService(repo, accounts, nil)
	_, err := svc.GetConfig(t.Context())
	require.ErrorContains(t, err, "database unavailable")
	require.ErrorContains(t, svc.SaveConfig(t.Context(), legacyDeletedEvalConfig(), 9), "database unavailable")
	require.Len(t, repo.config.Accounts, 2)
	require.Len(t, repo.config.AccountPriorityRules, 2)
	require.Len(t, repo.config.BPSAccounts, 1)
}

func TestOpenAIEvalDeletedCleanupRejectsStaleRevisionBeforeValidation(t *testing.T) {
	repo := &openAIEvalRepoFake{config: &OpenAIEvalConfig{Revision: 8}}
	svc := NewOpenAIEvalService(repo, &deletedEvalAccounts{}, nil)
	require.ErrorIs(t, svc.SaveConfig(t.Context(), legacyDeletedEvalConfig(), 9), ErrOpenAIEvalConfigRevisionConflict)
}

func TestOpenAIEvalDeletedCleanupDoesNotAcceptNewInvalidAccount(t *testing.T) {
	repo := &openAIEvalRepoFake{config: &OpenAIEvalConfig{Revision: 8}}
	svc := NewOpenAIEvalService(repo, &deletedEvalAccounts{}, nil)
	draft := &OpenAIEvalConfig{Revision: 8, AccountPriorityRules: []OpenAIEvalAccountPriorityRule{{AccountID: 99, Priority: 1}}}
	require.ErrorContains(t, svc.SaveConfig(t.Context(), draft, 9), "unavailable account 99")
	require.Empty(t, repo.config.AccountPriorityRules)
}
