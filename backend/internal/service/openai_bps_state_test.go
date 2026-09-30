//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

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
