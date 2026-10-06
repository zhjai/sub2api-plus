package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenAIEvalAbsoluteOnlyWeightsValidation(t *testing.T) {
	w, err := normalizeOpenAIEvalPolicyWeights(OpenAIEvalPolicyWeights{AbsolutePriorities: []string{" Price ", "ERRORS", "quality"}})
	require.NoError(t, err)
	require.Equal(t, []string{"cost", "error_rate", "quality"}, w.AbsolutePriorities)
	require.Zero(t, w.Cost+w.ErrorRate+w.TTFT+w.Load+w.Quality)
	require.False(t, openAIEvalPolicyWeightsZero(w))
	again, err := normalizeOpenAIEvalPolicyWeights(w)
	require.NoError(t, err)
	require.Equal(t, w, again)
	for _, priorities := range [][]string{nil, {"cost", "price"}, {"quality", "quality"}, {"unknown"}} {
		_, err := normalizeOpenAIEvalPolicyWeights(OpenAIEvalPolicyWeights{AbsolutePriorities: priorities})
		require.Error(t, err)
	}
}

func TestOpenAIEvalAbsoluteSnapshotIsImmutable(t *testing.T) {
	cfg := &OpenAIEvalConfig{
		EffectsEnabled: true, SchedulingPolicy: OpenAIEvalSchedulingPolicyCustomBalance,
		CustomBalance: OpenAIEvalPolicyWeights{AbsolutePriorities: []string{"cost", "error_rate"}},
		Policies:      []OpenAIEvalSchedulingPolicyRule{{RequestedModel: "gpt-6.1-sol", CustomBalance: &OpenAIEvalPolicyWeights{AbsolutePriorities: []string{"quality"}}}},
	}
	snapshot, err := newOpenAIEvalSchedulingPolicySnapshot(cfg)
	require.NoError(t, err)
	cfg.CustomBalance.AbsolutePriorities[0] = "load"
	cfg.Policies[0].CustomBalance.AbsolutePriorities[0] = "ttft"
	require.Equal(t, []string{"cost", "error_rate"}, snapshot.CustomBalance.AbsolutePriorities)
	require.Equal(t, []string{"quality"}, snapshot.Rules[0].CustomBalance.AbsolutePriorities)
	snapshot.CustomBalance.AbsolutePriorities[1] = "load"
	require.Equal(t, "error_rate", cfg.CustomBalance.AbsolutePriorities[1])
}

func TestOpenAIEvalAbsoluteQualityEnabledWithoutWeight(t *testing.T) {
	previous := openAIEvalSchedulingPolicy.Load()
	t.Cleanup(func() { openAIEvalSchedulingPolicy.Store(previous) })
	cfg := &OpenAIEvalConfig{EffectsEnabled: true, SchedulingPolicy: OpenAIEvalSchedulingPolicyCustomBalance,
		CustomBalance: OpenAIEvalPolicyWeights{AbsolutePriorities: []string{"quality"}}}
	snapshot, err := newOpenAIEvalSchedulingPolicySnapshot(cfg)
	require.NoError(t, err)
	openAIEvalSchedulingPolicy.Store(snapshot)
	req := OpenAIAccountScheduleRequest{RequestedModel: "gpt-6.1-sol", RequestedReasoningEffort: "high"}
	require.True(t, openAIQualityRankingEnabled(req))
	require.True(t, openAIEvalRankingUsesQuality(OpenAIEvalSchedulingPolicyCustomBalance, OpenAIEvalRankingWeights{AbsolutePriorities: []string{"quality"}}))
	cfg.CustomBalance.AbsolutePriorities = []string{"cost"}
	snapshot, err = newOpenAIEvalSchedulingPolicySnapshot(cfg)
	require.NoError(t, err)
	openAIEvalSchedulingPolicy.Store(snapshot)
	require.False(t, openAIQualityRankingEnabled(req))
}

func TestOpenAIEvalAbsoluteEvidenceDeadlineWithoutWeight(t *testing.T) {
	now := time.Now()
	expires := now.Add(time.Minute)
	f := emptyOpenAIEvalRankingFactors()
	f.Quality = OpenAIEvalRankingQuality{OpenAIEvalFactorMeta: rankingKnown(1, now), ExpiresAt: &expires}
	until := rankingFactorExpiry(f, OpenAIEvalRankingWeights{AbsolutePriorities: []string{"quality"}}, OpenAIEvalSchedulingPolicyCustomBalance)
	require.Equal(t, &expires, until)
	f = emptyOpenAIEvalRankingFactors()
	f.ErrorRate.Source = rankingPtr("v1_matched_probe")
	f.monitorExpiresAt = &expires
	until = rankingFactorExpiry(f, OpenAIEvalRankingWeights{AbsolutePriorities: []string{"error_rate"}}, OpenAIEvalSchedulingPolicyCustomBalance)
	require.Equal(t, &expires, until)
}

func TestOpenAIRankingAbsoluteQualityUsesFreshPriorWithoutWeight(t *testing.T) {
	now := time.Now()
	cheap := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, RateMultiplier: rankingPtr(1.)}
	quality := &Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, RateMultiplier: rankingPtr(2.)}
	inputs := []openAIEvalRankingInput{
		{account: cheap, compatible: true, factors: emptyOpenAIEvalRankingFactors()},
		{account: quality, compatible: true, factors: emptyOpenAIEvalRankingFactors()},
	}
	prior := &OpenAIEvalAccountQualityPrior{Ratio: 1, ExpiresAt: now.Add(time.Hour)}
	// Reuse the constructor path so the exact rational used by sorting is present.
	prior.fraction = rankingQualityFraction(OpenAIEvalRankingQuality{OpenAIEvalFactorMeta: rankingKnown(1, now), Pass: 1, Evaluated: 1, Selected: 1, Ratio: rankingPtr(1.)})
	weights := OpenAIEvalRankingWeights{Price: 1, AbsolutePriorities: []string{"quality", "cost"}}
	rows := scoreOpenAIEvalRanking(OpenAIEvalSchedulingPolicyCustomBalance, weights, inputs, now, nil, map[int64]*OpenAIEvalAccountQualityPrior{2: prior})
	require.Equal(t, int64(2), rows[0].AccountID)
	require.NotNil(t, rows[0].AccountQualityPrior)
	require.Zero(t, rows[0].Contributions.Quality)
	prior.ExpiresAt = now.Add(-time.Second)
	rows = scoreOpenAIEvalRanking(OpenAIEvalSchedulingPolicyCustomBalance, weights, inputs, now, nil, map[int64]*OpenAIEvalAccountQualityPrior{2: prior})
	require.Equal(t, int64(1), rows[0].AccountID)
	require.Nil(t, rows[1].AccountQualityPrior)
}
