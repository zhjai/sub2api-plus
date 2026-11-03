package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/Wei-Shaw/sub2api/internal/service/cpaeval"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func modelDimensionCandy(id int64, model, effort, answer string, at time.Time) OpenAIEvalRun {
	status := "warning"
	if answer == "21" {
		status = "pass"
	}
	return OpenAIEvalRun{ID: id, AccountID: 1, RequestedModel: model, ReasoningEffort: effort, TestType: OpenAIEvalTypeCandy, DataVersion: OpenAIEvalQualityDataVersion, Status: status, TriggerSource: "scheduled", FinishedAt: at, Samples: []OpenAIEvalSampleRecord{{Valid: true, Answer: answer}}}
}

func TestModelDimensionRuntimeAndOverview(t *testing.T) {
	stats := newOpenAIAccountRuntimeStats()
	stats.reportForRequest(1, "gpt-6.1-sol", "high", false, rankingPtr(100))
	for i := 0; i < 9; i++ {
		stats.reportForRequest(1, "gpt-6.1-sol", "", true, nil)
	}
	stats.reportForRequest(1, "gpt-6.1-sol", "low", true, rankingPtr(300))
	stats.reportForRequest(1, "gpt-6-astra", "high", true, rankingPtr(900))
	now := time.Now()
	for _, effort := range []string{"", "high", "low", "max"} {
		f := stats.rankingFactors(1, "gpt-6.1-sol", effort, now)
		require.EqualValues(t, 11, f.ErrorRate.SampleCount)
		require.InDelta(t, 1.0/11, *f.ErrorRate.Value, 1e-12)
		require.EqualValues(t, 2, f.TTFT.SampleCount)
		require.Equal(t, 200., *f.TTFT.MS)
	}
	require.Zero(t, *stats.rankingFactors(1, "gpt-6-astra", "", now).ErrorRate.Value)
	require.EqualValues(t, 2, stats.routeCount.Load())
	a := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}
	gen := &openAIRankingGeneration{deadline: now.Add(time.Hour)}
	buildAccountOverview(gen, &OpenAIEvalConfig{}, nil, map[int64]*Account{1: a}, nil, stats, nil, nil, now, nil)
	require.Len(t, gen.overview, 1)
	require.Len(t, gen.overview[0].Models, 2)
	for _, m := range gen.overview[0].Models {
		require.Empty(t, m.ReasoningEffort)
	}
	require.InDelta(t, 1.0/12, *gen.overview[0].Factors.ErrorRate.Value, 1e-12)
	require.Equal(t, rankingDimensionKey(7, "gpt-6.1-sol", ""), rankingDimensionKey(7, "gpt-6.1-sol", "high"))
	require.NotEqual(t, rankingDimensionKey(7, "gpt-6.1-sol", ""), rankingDimensionKey(7, "gpt-6-astra", ""))
}

func TestModelDimensionLatestUsablePreservesHistory(t *testing.T) {
	now := time.Now()
	pass := modelDimensionCandy(1, "gpt-6.1-sol", "high", "21", now.Add(-time.Minute))
	warning := modelDimensionCandy(2, "gpt-6.1-sol", "low", "28", now.Add(-30*time.Second))
	failed := modelDimensionCandy(3, "gpt-6.1-sol", "", "21", now.Add(-10*time.Second))
	failed.Status = "failed"
	other := modelDimensionCandy(4, "gpt-6-astra", "", "21", now.Add(-time.Second))
	latest := map[OpenAIEvalEvidenceKey]OpenAIEvalRun{}
	for _, r := range []OpenAIEvalRun{pass, warning, failed, other} {
		latest[OpenAIEvalEvidenceKey{r.AccountID, r.RequestedModel, r.ReasoningEffort, r.TestType}] = r
	}
	got, ok := latestUsableModelQualityRun(latest, 1, "gpt-6.1-sol", OpenAIEvalTypeCandy, now)
	require.True(t, ok)
	require.EqualValues(t, 2, got.ID)
	require.Equal(t, "low", got.ReasoningEffort)
	_, ok = latestUsableModelQualityRun(latest, 2, "gpt-6.1-sol", OpenAIEvalTypeCandy, now)
	require.False(t, ok)
	require.Equal(t, "high", pass.ReasoningEffort)
}

func TestModelDimensionPolicyMigration(t *testing.T) {
	cfg := &OpenAIEvalConfig{Policies: []OpenAIEvalSchedulingPolicyRule{
		{RequestedModel: "gpt-6.1-sol", ReasoningEffort: "high", Policy: OpenAIEvalSchedulingPolicyCostFirst},
		{RequestedModel: "gpt-6-astra", ReasoningEffort: "low", Policy: OpenAIEvalSchedulingPolicyStabilityFirst},
		{RequestedModel: " GPT-6.1-SOL ", ReasoningEffort: "legacy-unknown", Policy: OpenAIEvalSchedulingPolicyAvoidDegradation},
	}}
	NormalizeOpenAIEvalModelPolicies(cfg)
	require.Len(t, cfg.Policies, 2)
	require.Equal(t, "gpt-6-astra", cfg.Policies[0].RequestedModel)
	require.Equal(t, OpenAIEvalSchedulingPolicyAvoidDegradation, cfg.Policies[1].Policy)
	for _, rule := range cfg.Policies {
		require.Empty(t, rule.ReasoningEffort)
	}
	NormalizeOpenAIEvalModelPolicies(cfg)
	require.Len(t, cfg.Policies, 2)
}

func TestModelDimensionMonitorAndUnknownAreNotRequestSamples(t *testing.T) {
	now := time.Now()
	real := emptyOpenAIEvalRankingFactors()
	real.ErrorRate = OpenAIEvalRankingErrorRate{OpenAIEvalFactorMeta: rankingKnown(.8, now), Value: rankingPtr(.2), SampleCount: 10, Source: rankingPtr("request_samples_account_model")}
	probe := emptyOpenAIEvalRankingFactors()
	probe.ErrorRate = OpenAIEvalRankingErrorRate{OpenAIEvalFactorMeta: rankingKnown(0, now), Value: rankingPtr(1.), SampleCount: 100, Source: rankingPtr("v1_matched_probe")}
	got := macroRankingFactors([]OpenAIEvalRankingFactors{real, probe, emptyOpenAIEvalRankingFactors()})
	require.Equal(t, .2, *got.ErrorRate.Value)
	require.EqualValues(t, 10, got.ErrorRate.SampleCount)
	require.Equal(t, "request_samples_account_models", *got.ErrorRate.Source)
	only := macroRankingFactors([]OpenAIEvalRankingFactors{probe, emptyOpenAIEvalRankingFactors()})
	require.Equal(t, "v1_matched_probe", *only.ErrorRate.Source)
	require.False(t, only.TTFT.Known)
	require.Nil(t, only.TTFT.MS)
}

func TestModelDimensionRecoverySharesPermitAcrossEfforts(t *testing.T) {
	stats := newOpenAIAccountRuntimeStats()
	now := time.Now()
	for i := 0; i < 10; i++ {
		stats.reportForRequest(1, "gpt-6.1-sol", "high", false, nil)
	}
	stat, _ := stats.loadRoute(1, "gpt-6.1-sol", "")
	stat.observedAt.Store(now.Add(-time.Hour).UnixNano())
	thresholds := defaultOpenAIEvalSchedulingThresholds()
	release := stats.reserveRuntimeRecovery(context.Background(), 1, "gpt-6.1-sol", "high", OpenAIEvalSchedulingPolicyCostFirst, thresholds, now)
	require.NotNil(t, release)
	defer release()
	require.Nil(t, stats.reserveRuntimeRecovery(context.Background(), 1, "gpt-6.1-sol", "low", OpenAIEvalSchedulingPolicyCostFirst, thresholds, now))
}

func TestModelDimensionLegacyQualityCacheChoosesLatestUsable(t *testing.T) {
	now := time.Now()
	a := &Account{ID: 1, Extra: map[string]any{}}
	for _, run := range []OpenAIEvalRun{
		modelDimensionCandy(1, "gpt-6.1-sol", "high", "21", now.Add(-time.Minute)),
		modelDimensionCandy(2, "gpt-6.1-sol", "low", "28", now.Add(-30*time.Second)),
		modelDimensionCandy(3, "gpt-6-astra", "", "21", now.Add(-time.Second)),
	} {
		q, ok := openAIEvalCurrentQualityAggregateFromRun(run, 300, 300, now)
		require.True(t, ok)
		a.Extra[OpenAIEvalQualityExtraKeyFor(run.RequestedModel, run.ReasoningEffort, run.TestType)] = q
	}
	got, ok := readOpenAIEvalModelQualityEvidence(a, "gpt-6.1-sol", OpenAIEvalTypeCandy, now)
	require.True(t, ok)
	require.EqualValues(t, 2, got.RunID)
	require.Zero(t, got.PassCount)
	_, ok = readOpenAIEvalModelQualityEvidence(a, "unknown-model", OpenAIEvalTypeCandy, now)
	require.False(t, ok)
}

func modelDimensionFingerprint(id int64, model, effort string, passed bool, used int, at time.Time) OpenAIEvalRun {
	prediction := "claude-sonnet-4.6"
	if passed {
		prediction = cpaeval.FingerprintModelID(model)
	}
	fp := &OpenAIEvalLMFingerprintResult{DetectorRevision: cpaeval.DetectorRevision(), ComparedAs: cpaeval.FingerprintModelID(model), Enrolled: cpaeval.ModelEnrolled(model), Attribution: cpaeval.FingerprintAttribution{Prediction: prediction, Decision: "partial", Used: used}}
	outcome := openAIEvalLMFingerprintOutcome(model, fp)
	return OpenAIEvalRun{ID: id, AccountID: 1, RequestedModel: model, ReasoningEffort: effort, TestType: OpenAIEvalTypeFingerprint, DataVersion: OpenAIEvalQualityDataVersion, BaselineVersion: OpenAIEvalQualityBaselineVersion, Status: outcome.Status, Outcome: outcome, FinishedAt: at, TriggerSource: "manual"}
}

func TestModelDimensionCurrentQualityUnionAndOneVotePerType(t *testing.T) {
	now := time.Now()
	cfg := &OpenAIEvalConfig{QualityRefreshIntervalSeconds: 300, Accounts: []OpenAIEvalAccountConfig{
		{AccountID: 1, RequestedModel: "gpt-6.1-sol", ReasoningEffort: "high", CandySchedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300}},
		{AccountID: 1, RequestedModel: "gpt-6.1-sol", ReasoningEffort: "low", CandySchedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 600}, FingerprintSchedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 900}},
	}}
	candy := modelDimensionCandy(1, "gpt-6.1-sol", "high", "21", now.Add(-time.Minute))
	for i := 0; i < 9; i++ {
		candy.Samples = append(candy.Samples, candy.Samples[0])
	}
	fp := modelDimensionFingerprint(2, "gpt-6.1-sol", "max", false, 1, now.Add(-time.Second))
	latest := map[OpenAIEvalEvidenceKey]OpenAIEvalRun{}
	for _, run := range []OpenAIEvalRun{candy, fp, modelDimensionFingerprint(3, "gpt-6-astra", "max", true, 3, now)} {
		latest[OpenAIEvalEvidenceKey{run.AccountID, run.RequestedModel, run.ReasoningEffort, run.TestType}] = run
	}
	for _, effort := range []string{"", "high", "low", "max"} {
		q := qualityFromLatestRuns(cfg, 1, "gpt-6.1-sol", effort, latest, now)
		require.True(t, q.Known)
		require.Equal(t, 2, q.Selected)
		require.Equal(t, 2, q.Evaluated)
		require.Equal(t, .5, *q.Ratio)
		interval, selected := openAIEvalQualityTestInterval(cfg, 1, "gpt-6.1-sol", effort, OpenAIEvalTypeCandy)
		require.True(t, selected)
		require.Equal(t, 600, interval)
	}
	routes, err := openAIEvalQualityRoutes(cfg)
	require.NoError(t, err)
	require.Len(t, routes, 1)
	require.Equal(t, [3]int{600, 900, 0}, routes[0].Intervals)
	counts := openAIEvalQualityCountsFromRun(&fp)
	require.Equal(t, OpenAIEvalQualityCounts{EvaluatedCount: 1}, counts)
	q, ok := openAIEvalQualityAggregateFromRun(fp, 900, 300, now)
	require.True(t, ok)
	require.Equal(t, "max", q.ReasoningEffort)
	delete(latest, OpenAIEvalEvidenceKey{1, "gpt-6.1-sol", "max", OpenAIEvalTypeFingerprint})
	missing := qualityFromLatestRuns(cfg, 1, "gpt-6.1-sol", "high", latest, now)
	require.False(t, missing.Known)
	require.Nil(t, missing.Ratio)
	require.Equal(t, 2, missing.Selected)
	require.Equal(t, 1, missing.Evaluated)
}

func TestModelDimensionPolicyReadSaveAndDisabledLastRule(t *testing.T) {
	previous := openAIEvalSchedulingPolicy.Load()
	cache := openAIEvalQualitySnapshots
	openAIEvalQualitySnapshots = &openAIEvalQualitySnapshotStore{}
	t.Cleanup(func() { openAIEvalSchedulingPolicy.Store(previous); openAIEvalQualitySnapshots = cache })
	cfg := &OpenAIEvalConfig{EffectsEnabled: true, SchedulingPolicy: OpenAIEvalSchedulingPolicyCostFirst,
		Policies: []OpenAIEvalSchedulingPolicyRule{
			{RequestedModel: "gpt-6.1-sol", ReasoningEffort: "high", Policy: OpenAIEvalSchedulingPolicyAvoidDegradation},
			{RequestedModel: " GPT-6.1-SOL ", ReasoningEffort: "obsolete", Policy: OpenAIEvalSchedulingPolicyCustomBalance, CustomBalance: &OpenAIEvalPolicyWeights{Quality: 1}},
		}, Accounts: []OpenAIEvalAccountConfig{{AccountID: 1, RequestedModel: "gpt-6.1-sol", ReasoningEffort: "high"}}}
	repo := &rankingTestRepo{config: *cloneRankingConfig(cfg)}
	s := &OpenAIEvalService{repo: repo}
	read, err := s.GetConfig(context.Background())
	require.NoError(t, err)
	require.Len(t, read.Policies, 1)
	require.Empty(t, read.Policies[0].ReasoningEffort)
	require.Len(t, repo.config.Policies, 2, "read projection must not mutate persisted config")
	require.NoError(t, s.SaveConfig(context.Background(), cfg, 1))
	require.Len(t, repo.config.Policies, 1)
	require.Equal(t, "high", repo.config.Accounts[0].ReasoningEffort)
	for _, effort := range []string{"", "high", "low"} {
		require.Equal(t, OpenAIEvalSchedulingPolicyCustomBalance, OpenAIEvalSchedulingPolicyForRequest("gpt-6.1-sol", effort))
		weights, ok := OpenAIEvalCustomBalanceForRequest("gpt-6.1-sol", effort)
		require.True(t, ok)
		require.Equal(t, 1., weights.Quality)
		_, ranking := openAIEvalRankingWeights(cfg, "gpt-6.1-sol", effort)
		require.Equal(t, 1., ranking.Quality)
	}
	cfg.Policies = append(cfg.Policies, OpenAIEvalSchedulingPolicyRule{RequestedModel: "gpt-6.1-sol", ReasoningEffort: "low", Enabled: rankingPtr(false), Policy: OpenAIEvalSchedulingPolicyAvoidDegradation})
	require.Equal(t, OpenAIEvalSchedulingPolicyCostFirst, OpenAIEvalSchedulingPolicyFor(cfg, "gpt-6.1-sol", "high"))
	NormalizeOpenAIEvalModelPolicies(cfg)
	require.Len(t, cfg.Policies, 1)
	require.Equal(t, OpenAIEvalSchedulingPolicyCostFirst, OpenAIEvalSchedulingPolicyFor(cfg, "gpt-6.1-sol", "low"))
}

func TestModelDimensionHealthLegacyMigrationAndRecovery(t *testing.T) {
	now := time.Now()
	a := &Account{ID: 1, Extra: map[string]any{}}
	legacyKey := func(model, effort string) string {
		digest := sha256.Sum256([]byte(model + "\x00" + effort))
		return "openai_eval_route_health_" + hex.EncodeToString(digest[:8])
	}
	old := OpenAIEvalRouteHealth{AccountID: 1, RequestedModel: "gpt-6.1-sol", ReasoningEffort: "high", Penalty: 1, PenaltyUntil: now.Add(time.Minute), UpdatedAt: now.Add(-time.Minute)}
	a.Extra[legacyKey(old.RequestedModel, "high")] = old
	newer := old
	newer.ReasoningEffort, newer.UpdatedAt, newer.Penalty = "low", now, .5
	a.Extra[legacyKey(old.RequestedModel, "low")] = newer
	for _, effort := range []string{"", "high", "low"} {
		health, ok := ReadOpenAIEvalRouteHealthFromAccount(a, old.RequestedModel, effort, now)
		require.True(t, ok)
		require.Equal(t, .5, health.Penalty)
	}
	_, ok := ReadOpenAIEvalRouteHealthFromAccount(a, "gpt-6-astra", "high", now)
	require.False(t, ok)
	newer.AccountID = 999
	a.Extra[legacyKey(old.RequestedModel, "low")] = newer
	health, ok := ReadOpenAIEvalRouteHealthFromAccount(a, old.RequestedModel, "low", now)
	require.True(t, ok)
	require.Equal(t, 1., health.Penalty)
	recovered := updateOpenAIEvalRouteHealth(old, 1, old.RequestedModel, "max", now, false, "")
	require.Empty(t, recovered.ReasoningEffort)
	a.Extra[OpenAIEvalRouteHealthExtraKeyFor(old.RequestedModel, "")] = recovered
	_, ok = ReadOpenAIEvalRouteHealthFromAccount(a, old.RequestedModel, "high", now)
	require.False(t, ok, "canonical recovery wins over legacy penalties")
}

type modelDimensionRefreshRepo struct {
	*qualityRefreshRepository
	runs    []OpenAIEvalRun
	queries [][]OpenAIEvalEvidenceKey
}

func (r *modelDimensionRefreshRepo) LatestModelQualityRuns(_ context.Context, keys []OpenAIEvalEvidenceKey, _ time.Time) ([]OpenAIEvalRun, error) {
	r.queries = append(r.queries, append([]OpenAIEvalEvidenceKey(nil), keys...))
	return r.runs, nil
}

func TestModelDimensionManualAndPeriodicRefreshUseSameLatestModelEvidence(t *testing.T) {
	s, base, accounts := setupQualityRefreshTest(t)
	now := time.Now().Add(-time.Minute)
	base.config.Accounts = []OpenAIEvalAccountConfig{
		{AccountID: 1, RequestedModel: "gpt-6.1-sol", ReasoningEffort: "high", CandySchedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300}},
		{AccountID: 1, RequestedModel: "gpt-6.1-sol", ReasoningEffort: "low", FingerprintSchedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300}},
	}
	repo := &modelDimensionRefreshRepo{qualityRefreshRepository: base, runs: []OpenAIEvalRun{
		modelDimensionCandy(1, "gpt-6.1-sol", "max", "21", now),
		modelDimensionFingerprint(2, "gpt-6.1-sol", "max", false, 2, now),
		modelDimensionFingerprint(3, "gpt-6-astra", "high", true, 3, now.Add(time.Second)),
	}}
	failed := repo.runs[0]
	failed.ID, failed.Status, failed.FinishedAt = 4, "failed", now.Add(time.Second)
	repo.runs = append(repo.runs, failed)
	s.repo = repo
	accounts.accounts[1] = &Account{ID: 1}
	for _, manual := range []bool{false, true} {
		var result *OpenAIEvalQualityRefreshResult
		var err error
		if manual {
			result, err = s.RefreshOpenAIEvalQuality(context.Background(), 42)
		} else {
			result, err = s.refreshOpenAIEvalQuality(context.Background(), false)
		}
		require.NoError(t, err)
		require.Equal(t, 1, result.RouteCount)
		for _, effort := range []string{"", "high", "low"} {
			q, ok := openAIEvalQualitySnapshots.lookup(1, "gpt-6.1-sol", effort, time.Now())
			require.True(t, ok)
			require.Equal(t, .5, q.Ratio())
		}
	}
	require.Len(t, repo.queries, 2)
	for _, keys := range repo.queries {
		require.Len(t, keys, 2)
		for _, key := range keys {
			require.Empty(t, key.ReasoningEffort)
		}
	}
}

// Expose the same model-wide capability as the production repository while
// retaining the harness's configuration/audit behavior.
type modelDimensionRankingRepo struct{ *rankingTestRepo }

func (r *modelDimensionRankingRepo) LatestModelQualityRuns(_ context.Context, _ []OpenAIEvalEvidenceKey, _ time.Time) ([]OpenAIEvalRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]OpenAIEvalRun(nil), r.runs...), nil
}

func TestModelDimensionDispatchUsesUnconfiguredEffortEvidence(t *testing.T) {
	s, repo, _, gateway := rankingHarness(t)
	s.repo = &modelDimensionRankingRepo{repo}
	repo.config.Revision++
	repo.config.SchedulingPolicy = OpenAIEvalSchedulingPolicyAvoidDegradation
	schedule := OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300}
	for _, id := range []int64{1, 2} {
		repo.config.Accounts = append(repo.config.Accounts, OpenAIEvalAccountConfig{AccountID: id, RequestedModel: "gpt-6.1-sol", ReasoningEffort: "high", CandySchedule: schedule})
	}
	now := time.Now().Add(-time.Minute)
	repo.runs = []OpenAIEvalRun{
		overviewQualityRun(1, 1, "gpt-6.1-sol", "max", OpenAIEvalTypeCandy, false, now),
		overviewQualityRun(2, 2, "gpt-6.1-sol", "low", OpenAIEvalTypeCandy, true, now),
		overviewQualityRun(3, 1, "gpt-6-astra", "max", OpenAIEvalTypeCandy, true, now.Add(time.Second)),
	}
	failed := repo.runs[0]
	failed.ID, failed.Status, failed.FinishedAt = 4, "failed", now.Add(2*time.Second)
	repo.runs = append(repo.runs, failed)
	_, err := s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	for _, effort := range []string{"", "high", "low", "max"} {
		ctx := WithRequestedReasoningEffort(context.Background(), effort)
		selected, decision, err := gateway.SelectAccountWithScheduler(ctx, rankingPtr(int64(7)), "", "", "gpt-6.1-sol", nil, OpenAIUpstreamTransportAny, false)
		require.NoError(t, err)
		require.EqualValues(t, 2, selected.Account.ID)
		selected.ReleaseFunc()
		for _, candidate := range decision.Candidates {
			require.Equal(t, "exact", candidate.QualityBasis)
			require.Nil(t, candidate.AccountQualityPrior)
			require.Equal(t, 1, candidate.EvaluatedCount)
			want := 0.
			if candidate.AccountID == 2 {
				want = 1
			}
			require.Equal(t, want, *candidate.QualityRatio)
		}
	}
	overview, err := s.SchedulingAccountOverview(OpenAIEvalRankingFilter{})
	require.NoError(t, err)
	for _, account := range overview.Accounts {
		require.Len(t, account.Models, 1)
		require.Equal(t, "gpt-6.1-sol", account.Models[0].RequestedModel)
		require.Empty(t, account.Models[0].ReasoningEffort)
	}
	require.Equal(t, "max", repo.runs[0].ReasoningEffort)
	require.Equal(t, "low", repo.runs[1].ReasoningEffort)
}
