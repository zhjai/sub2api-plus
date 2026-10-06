//go:build unit

package service

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSavedSchedulingThresholdsChangeActualDispatch(t *testing.T) {
	s, repo, _, gateway := rankingHarness(t)
	scheduler := gateway.persistentOpenAIAccountScheduler().(*defaultOpenAIAccountScheduler)
	for i := 0; i < 20; i++ {
		scheduler.stats.reportForRequest(1, "gpt-6.1-sol", "", i < 18, nil)
	}
	group := int64(7)
	selectID := func() int64 {
		selection, _, err := gateway.SelectAccountWithScheduler(context.Background(), &group, "", "threshold-hot-save", "gpt-6.1-sol", nil, OpenAIUpstreamTransportAny, false)
		require.NoError(t, err)
		defer selection.ReleaseFunc()
		return selection.Account.ID
	}
	thresholds := defaultOpenAIEvalSchedulingThresholds()
	thresholds.CostFirst.ErrorRate = .4
	cfg := cloneRankingConfig(&repo.config)
	cfg.SchedulingThresholds = &thresholds
	require.NoError(t, s.SaveConfig(context.Background(), cfg, 1))
	require.EqualValues(t, 1, selectID(), "under the saved threshold, cheaper account must lead")
	cfg = cloneRankingConfig(&repo.config)
	cfg.SchedulingThresholds.CostFirst.ErrorRate = .2
	require.NoError(t, s.SaveConfig(context.Background(), cfg, 1))
	require.EqualValues(t, 2, selectID(), "web configuration hot update must change actual dispatch")
}

func TestSchedulingThresholdConfigDefaultsAndValidation(t *testing.T) {
	cfg := &OpenAIEvalConfig{}
	require.NoError(t, normalizeOpenAIEvalSchedulingThresholds(cfg))
	require.Equal(t, .2, cfg.SchedulingThresholds.CostFirst.ErrorRate)
	require.Equal(t, .05, cfg.SchedulingThresholds.StabilityFirst.ErrorRate)
	require.EqualValues(t, 10, cfg.SchedulingThresholds.MinErrorSamples)
	cfg.SchedulingThresholds.CostFirst.ErrorRate = 0
	require.NoError(t, normalizeOpenAIEvalSchedulingThresholds(cfg))
	require.Zero(t, cfg.SchedulingThresholds.CostFirst.ErrorRate)
	for _, edit := range []func(*OpenAIEvalSchedulingThresholds){
		func(v *OpenAIEvalSchedulingThresholds) { v.MinTTFTSamples = 0 },
		func(v *OpenAIEvalSchedulingThresholds) { v.CostFirst.ErrorRate = math.NaN() },
		func(v *OpenAIEvalSchedulingThresholds) { v.StabilityFirst.TTFTSeconds = -1 },
		func(v *OpenAIEvalSchedulingThresholds) { v.AvoidDegradation.ErrorRate = 1.1 },
	} {
		v := defaultOpenAIEvalSchedulingThresholds()
		edit(&v)
		require.Error(t, normalizeOpenAIEvalSchedulingThresholds(&OpenAIEvalConfig{SchedulingThresholds: &v}))
	}
}

func TestSchedulingThresholdPriceOrderSampleFloorAndQuality(t *testing.T) {
	now := time.Now()
	f := emptyOpenAIEvalRankingFactors()
	f.ErrorRate = OpenAIEvalRankingErrorRate{OpenAIEvalFactorMeta: rankingKnown(.7, now), Value: rankingPtr(.3), SampleCount: 9}
	cheap := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, RateMultiplier: rankingPtr(.06)}
	expensive := &Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, RateMultiplier: rankingPtr(.12)}
	inputs := []openAIEvalRankingInput{{account: cheap, factors: f, compatible: true}, {account: expensive, factors: emptyOpenAIEvalRankingFactors(), compatible: true}}
	for _, policy := range []string{OpenAIEvalSchedulingPolicyCostFirst, OpenAIEvalSchedulingPolicyStabilityFirst} {
		p, w := openAIEvalRankingWeights(&OpenAIEvalConfig{SchedulingPolicy: policy}, "", "")
		rows := scoreOpenAIEvalRanking(p, w, inputs, now, nil)
		require.EqualValues(t, 1, rows[0].AccountID)
		inputs[0].factors.ErrorRate.SampleCount = 10
		rows = scoreOpenAIEvalRanking(p, w, inputs, now, nil)
		require.EqualValues(t, 2, rows[0].AccountID)
		require.Contains(t, rows[1].ThresholdReasons, "error_rate_threshold")
		inputs[0].factors.ErrorRate.SampleCount = 9
	}
	inputs[0].factors.ErrorRate.Value = rankingPtr(.1)
	inputs[0].factors.ErrorRate.SampleCount = 10
	for _, tc := range []struct {
		policy string
		want   int64
	}{
		{OpenAIEvalSchedulingPolicyCostFirst, 1}, {OpenAIEvalSchedulingPolicyStabilityFirst, 2},
	} {
		p, w := openAIEvalRankingWeights(&OpenAIEvalConfig{SchedulingPolicy: tc.policy}, "", "")
		require.Equal(t, tc.want, scoreOpenAIEvalRanking(p, w, inputs, now, nil)[0].AccountID)
	}
	inputs[0].factors.Quality = OpenAIEvalRankingQuality{OpenAIEvalFactorMeta: rankingKnown(.5, now), Selected: 2, Evaluated: 2, Pass: 1, Ratio: rankingPtr(.5)}
	inputs[1].factors.Quality = OpenAIEvalRankingQuality{OpenAIEvalFactorMeta: rankingKnown(1, now), Selected: 2, Evaluated: 2, Pass: 2, Ratio: rankingPtr(1.)}
	p, w := openAIEvalRankingWeights(&OpenAIEvalConfig{SchedulingPolicy: OpenAIEvalSchedulingPolicyAvoidDegradation}, "", "")
	require.EqualValues(t, 2, scoreOpenAIEvalRanking(p, w, inputs, now, nil)[0].AccountID)
	inputs[1].factors.TTFT = OpenAIEvalRankingTTFT{OpenAIEvalFactorMeta: rankingKnown(.1, now), MS: rankingPtr(16000.), SampleCount: 20}
	require.EqualValues(t, 1, scoreOpenAIEvalRanking(p, w, inputs, now, nil)[0].AccountID)
	thresholds := defaultOpenAIEvalSchedulingThresholds()
	thresholds.AvoidDegradation.TTFTSeconds = 20
	require.EqualValues(t, 2, scoreOpenAIEvalRankingWithThresholds(p, w, inputs, now, nil, thresholds)[0].AccountID)
}

func TestSchedulingThresholdsIgnoreMonitorEstimatesAndCustomWeights(t *testing.T) {
	thresholds := defaultOpenAIEvalSchedulingThresholds()
	f := emptyOpenAIEvalRankingFactors()
	f.ErrorRate = OpenAIEvalRankingErrorRate{OpenAIEvalFactorMeta: rankingKnown(0, time.Now()), Value: rankingPtr(1.), SampleCount: 100}
	f.ErrorRate.Source = rankingPtr("v1_matched_probe")
	f.TTFT = OpenAIEvalRankingTTFT{OpenAIEvalFactorMeta: rankingKnown(0, time.Now()), MS: rankingPtr(30000.), SampleCount: 0}
	require.Empty(t, rankingThresholdReasons(OpenAIEvalSchedulingPolicyCostFirst, thresholds, f), "monitor estimates must not stand in for business sample counts")
	f.ErrorRate.Source = rankingPtr("runtime")
	f.TTFT.SampleCount = 20
	require.ElementsMatch(t, []string{"error_rate_threshold", "ttft_threshold"}, rankingThresholdReasons(OpenAIEvalSchedulingPolicyCostFirst, thresholds, f))
	require.Empty(t, rankingThresholdReasons(OpenAIEvalSchedulingPolicyCustomBalance, thresholds, f), "custom balance uses its weights rather than preset thresholds")
}
