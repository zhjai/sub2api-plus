//go:build unit

package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestResolveOpenAIBPSAccountStateMigratesAllLegacyModelKeysConservatively(t *testing.T) {
	older := time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	newer := older.Add(time.Hour)
	extra := map[string]any{
		OpenAIBPSModelStateExtraKeyFor("gpt-5.4"):     OpenAIBPSModelState{DegradedStreak: 2, HealthyStreak: 2, UpdatedAt: older},
		OpenAIBPSModelStateExtraKeyFor("gpt-6-astra"): OpenAIBPSModelState{Active: true, DegradedStreak: 3, HealthyStreak: 1, DisabledReason: "upstream_403", UpdatedAt: newer},
		"openai_eval_bps_not-a-state":                 map[string]any{"active": true},
	}

	state, legacy := ResolveOpenAIBPSAccountState(extra)
	require.True(t, legacy)
	require.False(t, state.Active)
	require.Zero(t, state.DegradedStreak)
	require.Zero(t, state.HealthyStreak)
	require.Equal(t, "upstream_403", state.DisabledReason)
	require.Equal(t, newer, state.UpdatedAt)
	require.True(t, extra[OpenAIBPSModelStateExtraKeyFor("gpt-6-astra")].(OpenAIBPSModelState).Active, "legacy history remains unchanged")
}

func TestResolveOpenAIBPSAccountStatePrefersAccountScopedState(t *testing.T) {
	want := OpenAIBPSAccountState{HealthyStreak: 1}
	extra := map[string]any{
		OpenAIBPSAccountStateExtraKey():           want,
		OpenAIBPSModelStateExtraKeyFor("gpt-5.4"): OpenAIBPSModelState{Active: true, DisabledReason: "upstream_403"},
	}

	state, legacy := ResolveOpenAIBPSAccountState(extra)
	require.False(t, legacy)
	require.Equal(t, want, state)
}
