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

func TestNextOpenAIBPSModelStateRequiresThreeDegradedProbes(t *testing.T) {
	state := OpenAIBPSModelState{}
	for i := 0; i < 2; i++ {
		var changed bool
		state, changed = nextOpenAIBPSModelState(state, "degraded")
		require.True(t, changed)
		require.False(t, state.Active)
	}
	state, changed := nextOpenAIBPSModelState(state, "degraded")
	require.True(t, changed)
	require.True(t, state.Active)
	require.Equal(t, 3, state.DegradedStreak)
}

func TestNextOpenAIBPSModelStateNeedsTwoHealthyProbesToRecover(t *testing.T) {
	state := OpenAIBPSModelState{Active: true, DegradedStreak: 3}
	state, changed := nextOpenAIBPSModelState(state, "healthy")
	require.True(t, changed)
	require.True(t, state.Active)
	require.Equal(t, 1, state.HealthyStreak)
	state, changed = nextOpenAIBPSModelState(state, "healthy")
	require.True(t, changed)
	require.False(t, state.Active)
	require.Zero(t, state.HealthyStreak)
}

func TestNextOpenAIBPSModelStateIgnoresInconclusiveAnd403Disabled(t *testing.T) {
	state := OpenAIBPSModelState{Active: true, DegradedStreak: 3}
	unchanged, changed := nextOpenAIBPSModelState(state, "inconclusive")
	require.False(t, changed)
	require.Equal(t, state, unchanged)

	state.DisabledReason = "upstream_403"
	unchanged, changed = nextOpenAIBPSModelState(state, "degraded")
	require.False(t, changed)
	require.Equal(t, state, unchanged)
	unchanged, changed = nextOpenAIBPSModelState(state, "healthy")
	require.False(t, changed)
	require.Equal(t, state, unchanged)
}

func TestNextOpenAIBPSModelStateHealthyProbeClearsOtherDisableReasons(t *testing.T) {
	state := OpenAIBPSModelState{
		Active:         true,
		DegradedStreak: 3,
		DisabledReason: "temporary_probe_lock",
	}
	next, changed := nextOpenAIBPSModelState(state, "healthy")
	require.True(t, changed)
	require.False(t, next.Active)
	require.Empty(t, next.DisabledReason)
	require.Zero(t, next.DegradedStreak)
	require.Zero(t, next.HealthyStreak)
}
