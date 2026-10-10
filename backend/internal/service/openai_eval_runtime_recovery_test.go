package service

import (
	"context"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func agedRecoveryMetric(t *testing.T, stats *openAIAccountRuntimeStats, model, effort string, at time.Time, errorRate, ttft float64) {
	t.Helper()
	stats.rankingMu.Lock()
	defer stats.rankingMu.Unlock()
	stat, ok := stats.loadOrCreateRoute(1, model, effort)
	require.True(t, ok)
	stat.errorRateEWMABits.Store(math.Float64bits(errorRate))
	stat.ttftEWMABits.Store(math.Float64bits(ttft))
	stat.sampleCount.Store(30)
	stat.ttftSampleCount.Store(30)
	stat.observedAt.Store(at.UnixNano())
	stat.ttftObservedAt.Store(at.UnixNano())
	stat.metricVersion.Store(stats.metricSeq.Add(1))
}

func TestRuntimeRecoveryClockAndConfig(t *testing.T) {
	now := time.Now()
	stats := newOpenAIAccountRuntimeStats()
	thresholds := defaultOpenAIEvalSchedulingThresholds()
	agedRecoveryMetric(t, stats, "gpt-6.1-sol", "high", now.Add(-time.Minute), .9, 30000)
	f := stats.rankingFactors(1, "gpt-6.1-sol", "high", now)
	r := rankingRuntimeRecovery(OpenAIEvalSchedulingPolicyCostFirst, thresholds, f, now)
	require.Equal(t, "waiting", r.State)
	require.Equal(t, "ready", rankingRuntimeRecovery(OpenAIEvalSchedulingPolicyCostFirst, thresholds, f, r.NextTrialAt).State)
	require.Equal(t, .9, *f.ErrorRate.Value, "time does not improve the old error rate")
	thresholds.RecoveryEnabled = rankingPtr(false)
	require.Nil(t, rankingRuntimeRecovery(OpenAIEvalSchedulingPolicyCostFirst, thresholds, f, r.NextTrialAt))
	thresholds.RecoveryEnabled = rankingPtr(true)
	require.Nil(t, rankingRuntimeRecovery(OpenAIEvalSchedulingPolicyCustomBalance, thresholds, f, r.NextTrialAt))
	for _, invalid := range []int{-1, 299, int(OpenAIEvalMaxIntervalSeconds) + 1} {
		copy := thresholds
		copy.RecoveryIntervalSeconds = invalid
		require.Error(t, normalizeOpenAIEvalSchedulingThresholds(&OpenAIEvalConfig{SchedulingThresholds: &copy}))
	}
}

func TestRuntimeRecoveryPermitConcurrencyCancellationAndResult(t *testing.T) {
	now := time.Now()
	stats := newOpenAIAccountRuntimeStats()
	thresholds := defaultOpenAIEvalSchedulingThresholds()
	agedRecoveryMetric(t, stats, "gpt-6.1-sol", "high", now.Add(-time.Hour), .9, 30000)
	var won atomic.Int64
	var wg sync.WaitGroup
	var release func()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if finish := stats.reserveRuntimeRecovery(ctx, 1, "gpt-6.1-sol", "high", OpenAIEvalSchedulingPolicyCostFirst, thresholds, now); finish != nil {
				won.Add(1)
				release = finish
			}
		}()
	}
	wg.Wait()
	require.EqualValues(t, 1, won.Load())
	require.Equal(t, "in_flight", rankingRuntimeRecovery(OpenAIEvalSchedulingPolicyCostFirst, thresholds, stats.rankingFactors(1, "gpt-6.1-sol", "high", now), now).State)
	cancel()
	release()
	release() // Idempotent; explicit cancellation with no sample refunds the trial.
	finish := stats.reserveRuntimeRecovery(context.Background(), 1, "gpt-6.1-sol", "high", OpenAIEvalSchedulingPolicyCostFirst, thresholds, now)
	require.NotNil(t, finish)
	stats.reportForRequest(1, "gpt-6.1-sol", "high", false, nil)
	finish()
	require.Nil(t, stats.reserveRuntimeRecovery(context.Background(), 1, "gpt-6.1-sol", "high", OpenAIEvalSchedulingPolicyCostFirst, thresholds, time.Now()))
	last := stats.reserveRuntimeRecovery(context.Background(), 1, "gpt-6.1-sol", "high", OpenAIEvalSchedulingPolicyCostFirst, thresholds, now.Add(time.Hour))
	require.NotNil(t, last)
	defer last()
	require.Nil(t, stats.reserveRuntimeRecovery(context.Background(), 1, "gpt-6.1-sol", "medium", OpenAIEvalSchedulingPolicyCostFirst, thresholds, now.Add(time.Hour)))
	require.Nil(t, stats.reserveRuntimeRecovery(context.Background(), 1, "gpt-6-astra", "high", OpenAIEvalSchedulingPolicyCostFirst, thresholds, now.Add(time.Hour)))
}

func TestRuntimeRecoveryActualDispatchAndEvidenceRecovery(t *testing.T) {
	eval, repo, _, gateway := rankingHarness(t)
	repo.config.SchedulingPolicy = OpenAIEvalSchedulingPolicyCostFirst
	repo.config.Revision++
	require.NoError(t, eval.Initialize(context.Background()))
	stats := gateway.openaiAccountStats
	agedRecoveryMetric(t, stats, "gpt-6.1-sol", "", time.Now().Add(-time.Hour), .9, 1000)
	_, err := eval.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	overview, err := eval.SchedulingAccountOverview(OpenAIEvalRankingFilter{})
	require.NoError(t, err)
	require.EqualValues(t, 2, overview.Accounts[0].AccountID, "published normal ranking stays demoted")
	require.NotNil(t, overview.Accounts[1].Factors.RuntimeRecovery)
	require.Equal(t, "ready", overview.Accounts[1].Factors.RuntimeRecovery.State)
	selectAccount := func() (*AccountSelectionResult, OpenAIAccountScheduleDecision) {
		selection, decision, selectErr := gateway.SelectAccountWithScheduler(context.Background(), rankingPtr(int64(7)), "", "", "gpt-6.1-sol", nil, OpenAIUpstreamTransportAny, false)
		require.NoError(t, selectErr)
		return selection, decision
	}
	first, decision := selectAccount()
	require.EqualValues(t, 1, first.Account.ID, "due trial works even with a cached leaderboard")
	require.Equal(t, "runtime_recovery_trial", decision.ReasonCode)
	for _, candidate := range decision.Candidates {
		if candidate.AccountID == 1 {
			require.Equal(t, "runtime_recovery_trial", candidate.DecisionReason)
			require.Equal(t, "in_flight", candidate.Factors.RuntimeRecovery.State)
		}
	}
	second, _ := selectAccount()
	require.EqualValues(t, 2, second.Account.ID, "another request uses the ordinary ranking")
	second.ReleaseFunc()
	first.ReleaseFunc()
	// HTTP returns from Forward and releases its slot before ReportResult.
	gap, _ := selectAccount()
	require.EqualValues(t, 2, gap.Account.ID, "release-before-report must not refund a completed trial")
	gap.ReleaseFunc()
	stats.reportForRequest(1, "gpt-6.1-sol", "", true, nil)
	third, _ := selectAccount()
	require.EqualValues(t, 2, third.Account.ID, "one successful trial cannot erase old failure evidence")
	third.ReleaseFunc()
	for i := 0; i < 15; i++ {
		stats.reportForRequest(1, "gpt-6.1-sol", "", true, nil)
	}
	fourth, decision := selectAccount()
	require.EqualValues(t, 1, fourth.Account.ID, "fresh successful samples restore ordinary ordering")
	require.NotEqual(t, "runtime_recovery_trial", decision.ReasonCode)
	fourth.ReleaseFunc()
}

func TestRuntimeRecoveryRespectsQualityAndAccountPriority(t *testing.T) {
	for _, qualityFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "account_rule", true: "quality_tier"}[qualityFirst], func(t *testing.T) {
			eval, repo, _, gateway := rankingHarness(t)
			repo.config.SchedulingPolicy = OpenAIEvalSchedulingPolicyCostFirst
			if qualityFirst {
				repo.config.SchedulingPolicy = OpenAIEvalSchedulingPolicyAvoidDegradation
				for _, id := range []int64{1, 2} {
					repo.config.Accounts = append(repo.config.Accounts, OpenAIEvalAccountConfig{AccountID: id, RequestedModel: "gpt-6.1-sol", CandySchedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300}})
					repo.runs = append(repo.runs, overviewQualityRun(id, id, "gpt-6.1-sol", "", OpenAIEvalTypeCandy, id == 2, time.Now()))
				}
			} else {
				repo.config.AccountPriorityRules = []OpenAIEvalAccountPriorityRule{{AccountID: 2, Priority: 0}}
			}
			repo.config.Revision++
			require.NoError(t, eval.Initialize(context.Background()))
			agedRecoveryMetric(t, gateway.openaiAccountStats, "gpt-6.1-sol", "", time.Now().Add(-time.Hour), .9, 1000)
			selection, _, err := gateway.SelectAccountWithScheduler(context.Background(), rankingPtr(int64(7)), "", "", "gpt-6.1-sol", nil, OpenAIUpstreamTransportAny, false)
			require.NoError(t, err)
			require.EqualValues(t, 2, selection.Account.ID)
			selection.ReleaseFunc()
		})
	}
}

func TestRuntimeRecoveryLatencyOnlyAndDisabledDispatch(t *testing.T) {
	now := time.Now()
	stats := newOpenAIAccountRuntimeStats()
	agedRecoveryMetric(t, stats, "gpt-6.1-sol", "", now.Add(-time.Hour), 0, 30000)
	stat, _ := stats.loadRoute(1, "gpt-6.1-sol", "")
	stat.observedAt.Store(now.Add(-25 * time.Hour).UnixNano())
	f := stats.rankingFactors(1, "gpt-6.1-sol", "", now)
	require.False(t, f.ErrorRate.Known)
	require.True(t, f.TTFT.Known)
	finish := stats.reserveRuntimeRecovery(context.Background(), 1, "gpt-6.1-sol", "", OpenAIEvalSchedulingPolicyCostFirst, defaultOpenAIEvalSchedulingThresholds(), now)
	require.NotNil(t, finish, "a latency-only failure still has a retry opportunity")
	finish()

	eval, repo, _, gateway := rankingHarness(t)
	thresholds := defaultOpenAIEvalSchedulingThresholds()
	thresholds.RecoveryEnabled = rankingPtr(false)
	repo.config.SchedulingThresholds = &thresholds
	repo.config.Revision++
	require.NoError(t, eval.Initialize(context.Background()))
	agedRecoveryMetric(t, gateway.openaiAccountStats, "gpt-6.1-sol", "", now.Add(-time.Hour), .9, 1000)
	selection, decision, err := gateway.SelectAccountWithScheduler(context.Background(), rankingPtr(int64(7)), "", "", "gpt-6.1-sol", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.EqualValues(t, 2, selection.Account.ID)
	require.NotEqual(t, "runtime_recovery_trial", decision.ReasonCode)
	selection.ReleaseFunc()
}
