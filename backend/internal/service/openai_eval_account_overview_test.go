package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func overviewRow(t *testing.T, result *OpenAIEvalAccountOverview, id int64) OpenAIEvalAccountOverviewRow {
	t.Helper()
	for _, row := range result.Accounts {
		if row.AccountID == id {
			return row
		}
	}
	t.Fatalf("account %d missing", id)
	return OpenAIEvalAccountOverviewRow{}
}

func overviewMetric(t *testing.T, stats *openAIAccountRuntimeStats, id int64, model, effort string, rate float64, samples int64, ttft int) {
	t.Helper()
	stats.reportForRequest(id, model, effort, true, rankingPtr(ttft))
	stat, ok := stats.loadRoute(id, model, effort)
	require.True(t, ok)
	stat.errorRateEWMABits.Store(math.Float64bits(rate))
	stat.sampleCount.Store(samples)
}

func TestOpenAIAccountOverviewUniqueColdAccountsAndRecords(t *testing.T) {
	s, repo, accounts, gateway := rankingHarness(t)
	base := accounts.items[0]
	accounts.items = nil
	for i := 1; i <= 13; i++ {
		a := base
		a.ID, a.Name = int64(i), fmt.Sprint("account-", i)
		a.GroupIDs = []int64{7, 8}
		a.Credentials = map[string]any{"model_mapping": map[string]any{"configured-alias": "shared-upstream", "another-alias": "shared-upstream"}}
		accounts.items = append(accounts.items, a)
	}
	repo.config.Policies = []OpenAIEvalSchedulingPolicyRule{{RequestedModel: "policy-only", Policy: OpenAIEvalSchedulingPolicyStabilityFirst}}
	repo.config.Revision++
	summary, err := s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	result, err := s.SchedulingAccountOverview(OpenAIEvalRankingFilter{})
	require.NoError(t, err)
	require.Len(t, result.Accounts, 13)
	require.Equal(t, 13, summary.AccountCount)
	require.Empty(t, gateway.RecentOpenAIAccountScheduleTraces(10))
	for _, row := range result.Accounts {
		require.Equal(t, []int64{7, 8}, row.GroupIDs)
		require.Empty(t, row.Models)
		require.Zero(t, row.ModelCount)
		require.Nil(t, row.Factors.Quality.Ratio)
		require.Nil(t, row.Factors.ErrorRate.Value)
		require.Nil(t, row.Factors.TTFT.MS)
		require.Equal(t, .9, row.Factors.ErrorRate.Score)
		require.True(t, row.Factors.ErrorRate.DefaultApplied)
	}
	first := *result.Summary
	_, err = s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	result, err = s.SchedulingAccountOverview(OpenAIEvalRankingFilter{})
	require.NoError(t, err)
	require.Equal(t, first, *result.PreviousSummary)
	require.NotEqual(t, first.EvaluationID, result.Summary.EvaluationID)
	accounts.err = errors.New("synthetic account read failure")
	_, err = s.EvaluateScheduling(context.Background(), 1)
	require.Error(t, err)
	after, err := s.SchedulingAccountOverview(OpenAIEvalRankingFilter{})
	require.NoError(t, err)
	require.Equal(t, result.Summary, after.Summary)
	require.Equal(t, result.PreviousSummary, after.PreviousSummary)
	require.NotNil(t, after.RankingError)
	require.Empty(t, gateway.RecentOpenAIAccountScheduleTraces(10))

	// Overview membership is independent of the dimension catalog.
	gen := &openAIRankingGeneration{deadline: time.Now().Add(time.Hour)}
	buildAccountOverview(gen, &repo.config, []openAIRankingScope{{group: Group{ID: 7}, accounts: []*Account{&base}}}, map[int64]*Account{base.ID: &base}, nil, nil, nil, nil, time.Now(), nil)
	require.Len(t, gen.overview, 1)
	require.Empty(t, gen.overview[0].Models)
}

func TestOpenAIAccountOverviewPaginationFilterAndImmutableGenerations(t *testing.T) {
	s, _, accounts, _ := rankingHarness(t)
	base := accounts.items[0]
	accounts.items = nil
	for i := 1; i <= 613; i++ {
		a := base
		a.ID = int64(i)
		if i%2 == 0 {
			a.GroupIDs = []int64{7, 8}
		}
		accounts.items = append(accounts.items, a)
	}
	_, err := s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	first, err := s.SchedulingAccountOverview(OpenAIEvalRankingFilter{})
	require.NoError(t, err)
	require.Len(t, first.Accounts, 100)
	require.Equal(t, 613, first.Summary.AccountCount)
	page, err := s.SchedulingAccountOverview(OpenAIEvalRankingFilter{Limit: 500})
	require.NoError(t, err)
	require.Len(t, page.Accounts, 500)
	require.NotNil(t, page.NextCursor)
	filter := OpenAIEvalRankingFilter{Cursor: *page.NextCursor, Limit: 500}
	last, err := s.SchedulingAccountOverview(filter)
	require.NoError(t, err)
	require.Len(t, last.Accounts, 113)
	require.Nil(t, last.NextCursor)
	require.Equal(t, 501, *last.Accounts[0].Rank)
	group, err := s.SchedulingAccountOverview(OpenAIEvalRankingFilter{GroupID: rankingPtr(int64(8)), Limit: 500})
	require.NoError(t, err)
	require.Len(t, group.Accounts, 306)
	require.Equal(t, 2, *group.Accounts[0].Rank)
	require.Equal(t, page.Accounts[1].PriorityScore, group.Accounts[0].PriorityScore)
	_, err = s.SchedulingAccountOverview(OpenAIEvalRankingFilter{GroupID: rankingPtr(int64(8)), Cursor: *page.NextCursor})
	require.Error(t, err)
	_, err = s.SchedulingAccountOverview(OpenAIEvalRankingFilter{EvaluationID: "wrong", Cursor: *page.NextCursor})
	require.ErrorIs(t, err, ErrOpenAIEvalRankingSnapshotChanged)
	first.Accounts[0].AccountName = "mutated consumer"
	first.Accounts[0].GroupIDs[0] = 999
	first.Summary.AccountCount = 0
	unchanged, err := s.SchedulingAccountOverview(OpenAIEvalRankingFilter{})
	require.NoError(t, err)
	require.NotEqual(t, first.Accounts[0], unchanged.Accounts[0])
	require.Equal(t, 613, unchanged.Summary.AccountCount)
	_, err = s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	retained, err := s.SchedulingAccountOverview(filter)
	require.NoError(t, err)
	require.Equal(t, last.Accounts, retained.Accounts)
	_, err = s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	_, err = s.SchedulingAccountOverview(filter)
	require.ErrorIs(t, err, ErrOpenAIEvalRankingSnapshotChanged)
}

func TestOpenAIAccountOverviewMacroMetricsAndFixedFleetTTFT(t *testing.T) {
	s, _, accounts, gateway := rankingHarness(t)
	accounts.items[0].GroupIDs = []int64{7, 8}
	accounts.items[0].Credentials = map[string]any{"model_mapping": map[string]any{"Model-A": "shared", "model-b": "shared", "config-only": "shared"}}
	stats := gateway.openaiAccountStats
	overviewMetric(t, stats, 1, "Model-A", "high", .02, 100000, 100)
	overviewMetric(t, stats, 1, "model-b", "", .30, 1, 10000)
	overviewMetric(t, stats, 2, "Model-A", "high", .1, 2, 200)
	overviewMetric(t, stats, 2, "model-b", "", .1, 2, 20000)
	_, err := s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	result, err := s.SchedulingAccountOverview(OpenAIEvalRankingFilter{})
	require.NoError(t, err)
	row := overviewRow(t, result, 1)
	require.Equal(t, 2, row.ModelCount)
	require.Len(t, row.Models, 2)
	require.Equal(t, "Model-A", row.Models[0].RequestedModel)
	require.Equal(t, "high", row.Models[0].ReasoningEffort)
	require.InDelta(t, .16, *row.Factors.ErrorRate.Value, 1e-12)
	require.InDelta(t, .84, row.Factors.ErrorRate.Score, 1e-12)
	require.Equal(t, 1., row.Factors.TTFT.Score)
	require.Nil(t, row.Factors.TTFT.MS)
	require.Equal(t, 0., overviewRow(t, result, 2).Factors.TTFT.Score)
	filtered, err := s.SchedulingAccountOverview(OpenAIEvalRankingFilter{GroupID: rankingPtr(int64(8))})
	require.NoError(t, err)
	require.Len(t, filtered.Accounts, 1)
	require.Equal(t, row, filtered.Accounts[0])
	overviewMetric(t, stats, 1, "Model-A", "low", .06, 2, 50)
	overviewMetric(t, stats, 1, "model-a", "vendor-effort", .4, 3, 50)
	_, err = s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	result, err = s.SchedulingAccountOverview(OpenAIEvalRankingFilter{})
	require.NoError(t, err)
	row = overviewRow(t, result, 1)
	require.Equal(t, 3, row.ModelCount, "distinct runtime identities must not merge through mappings or case folding")
	require.Len(t, row.Models, 4)
	require.InDelta(t, (.04+.30+.4)/3, *row.Factors.ErrorRate.Value, 1e-12)
}

func overviewQualityRun(id, account int64, model, effort, typ string, pass bool, now time.Time) OpenAIEvalRun {
	run := OpenAIEvalRun{ID: id, AccountID: account, RequestedModel: model, ReasoningEffort: effort, TestType: typ,
		TriggerSource: "scheduled", DataVersion: OpenAIEvalQualityDataVersion, FinishedAt: now.Add(-time.Minute), Status: "warning"}
	answer, identity := "wrong", "gpt-6-luna"
	if pass {
		run.Status, answer, identity = "pass", "21", "gpt-6.1-sol"
	}
	switch typ {
	case OpenAIEvalTypeCandy:
		run.Samples = []OpenAIEvalSampleRecord{{Valid: true, Answer: answer}}
	case OpenAIEvalTypeFingerprint:
		run.BaselineVersion = OpenAIEvalQualityBaselineVersion
		run.Outcome.Fingerprint = &OpenAIEvalFingerprintResult{NearestModel: identity, ValidSamples: 60}
	case OpenAIEvalTypeModelTrace:
		run.Outcome.ModelTrace = &OpenAIEvalModelTraceResult{Prediction: identity, BankRevision: OpenAIEvalQualityModelTraceBankRevision, UsedOutputs: 2}
	}
	return run
}

func TestOpenAIAccountOverviewQualityTestEffortModelWeighting(t *testing.T) {
	s, repo, accounts, gateway := rankingHarness(t)
	accounts.items[0].GroupIDs = []int64{7, 8}
	repo.config.Revision++
	repo.config.SchedulingPolicy = OpenAIEvalSchedulingPolicyAvoidDegradation
	schedule := OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300}
	repo.config.Accounts = []OpenAIEvalAccountConfig{
		{AccountID: 1, RequestedModel: "gpt-6.1-sol", ReasoningEffort: "high", CandySchedule: schedule, FingerprintSchedule: schedule},
		{AccountID: 1, RequestedModel: "gpt-6.1-sol", ReasoningEffort: "low", CandySchedule: schedule, FingerprintSchedule: schedule, ModelTraceSchedule: schedule},
		{AccountID: 1, RequestedModel: "gpt-6-astra", CandySchedule: schedule},
		{AccountID: 1, RequestedModel: "gpt-6-astra", ReasoningEffort: "high", CandySchedule: schedule, FingerprintSchedule: schedule},
	}
	now := time.Now()
	for _, route := range repo.config.Accounts {
		for _, typ := range openAIEvalQualityTestTypes {
			if _, selected := openAIEvalQualityTestInterval(&repo.config, 1, route.RequestedModel, route.ReasoningEffort, typ); !selected {
				continue
			}
			if route.RequestedModel == "gpt-6-astra" && route.ReasoningEffort == "high" && typ == OpenAIEvalTypeFingerprint {
				continue
			}
			pass := typ == OpenAIEvalTypeFingerprint || route.RequestedModel == "gpt-6-astra"
			repo.runs = append(repo.runs, overviewQualityRun(int64(len(repo.runs)+1), 1, route.RequestedModel, route.ReasoningEffort, typ, pass, now))
		}
	}
	overviewMetric(t, gateway.openaiAccountStats, 1, "unknown-model", "", .1, 5, 20)
	_, err := s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	result, err := s.SchedulingAccountOverview(OpenAIEvalRankingFilter{})
	require.NoError(t, err)
	row := overviewRow(t, result, 1)
	require.Equal(t, 3, row.ModelCount)
	require.Equal(t, 2, row.QualityModelCount)
	require.Equal(t, 1, row.UnknownQualityModelCount)
	require.Equal(t, 3, row.QualityCellCount)
	require.Equal(t, 2, row.UnknownQualityCellCount)
	require.InDelta(t, ((.5+1./3)/2+1)/2, *row.Factors.Quality.Ratio, 1e-12)
	require.Equal(t, "gpt-6.1-sol", *row.WorstQualityModel)
	require.InDelta(t, 5./12, *row.WorstQualityRatio, 1e-12)
	for _, m := range row.Models {
		if m.RequestedModel == "gpt-6.1-sol" {
			want := .5
			if m.ReasoningEffort == "low" {
				want = 1. / 3
			}
			require.InDelta(t, want, *m.Factors.Quality.Ratio, 1e-12)
		}
	}
	require.Less(t, *row.Rank, *overviewRow(t, result, 2).Rank)
	require.Equal(t, *row.PriorityScore, row.Priority.OperationalScore)
	for _, invalid := range []string{"error", "expired", "future", "wrong_version"} {
		t.Run(invalid, func(t *testing.T) {
			run := repo.runs[0]
			switch invalid {
			case "error":
				run.Status = "error"
				run.Error = "insufficient_valid_samples"
			case "expired":
				run.FinishedAt = now.Add(-24 * time.Hour)
			case "future":
				run.FinishedAt = now.Add(time.Hour)
			case "wrong_version":
				run.DataVersion = "old"
			}
			latest := map[OpenAIEvalEvidenceKey]OpenAIEvalRun{}
			for _, r := range repo.runs {
				latest[OpenAIEvalEvidenceKey{r.AccountID, r.RequestedModel, r.ReasoningEffort, r.TestType}] = r
			}
			latest[OpenAIEvalEvidenceKey{run.AccountID, run.RequestedModel, run.ReasoningEffort, run.TestType}] = run
			q := qualityFromLatestRuns(&repo.config, 1, "gpt-6.1-sol", "high", latest, now)
			require.False(t, q.Known)
			require.Nil(t, q.Ratio)
			if invalid == "error" {
				require.Equal(t, "insufficient_valid_samples", q.EvidenceErrorCode)
			}
		})
	}
}

func TestOpenAIAccountOverviewStrictColdPriorAndMetricInvalidation(t *testing.T) {
	s, repo, accounts, gateway := rankingHarness(t)
	repo.config.SchedulingPolicy = OpenAIEvalSchedulingPolicyAvoidDegradation
	repo.config.Revision++
	schedule := OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300}
	repo.config.Accounts = []OpenAIEvalAccountConfig{{AccountID: 2, RequestedModel: "gpt-6.1-sol", CandySchedule: schedule}}
	repo.runs = []OpenAIEvalRun{overviewQualityRun(1, 2, "gpt-6.1-sol", "", OpenAIEvalTypeCandy, true, time.Now())}
	_, err := s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	frozen := s.ranking.current
	before, _ := json.Marshal(frozen.overview)
	scheduler := gateway.persistentOpenAIAccountScheduler().(*defaultOpenAIAccountScheduler)
	req := OpenAIAccountScheduleRequest{GroupID: rankingPtr(int64(7)), Platform: PlatformOpenAI, RequestedModel: "cold-model"}
	rows, trace, err := scheduler.explicitRanking(context.Background(), req, accounts.items, nil)
	require.NoError(t, err)
	require.Equal(t, "overview_prior", trace.RankingBasis)
	require.Equal(t, int64(2), rows[0].AccountID)
	for _, row := range rows {
		require.False(t, row.Factors.Quality.Known)
		require.Nil(t, row.Factors.Quality.Ratio)
		require.NotNil(t, row.OverviewPrior)
		candidate := rankedScheduleCandidate(row)
		require.Equal(t, "overview_prior", candidate.DecisionReason)
		require.Zero(t, candidate.EvaluatedCount)
	}
	selected, decision, err := gateway.SelectAccountWithScheduler(context.Background(), req.GroupID, "", "", req.RequestedModel, nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.Equal(t, int64(2), selected.Account.ID)
	selected.ReleaseFunc()
	require.Equal(t, "overview_prior", decision.RankingBasis)
	traces := gateway.RecentOpenAIAccountScheduleTraces(1)
	require.Len(t, traces, 1)
	require.Equal(t, "actual_dispatch", traces[0].RecordType)
	require.Equal(t, "overview_prior", traces[0].ReasonCode)
	for _, candidate := range traces[0].Candidates {
		require.Nil(t, candidate.QualityRatio)
		require.NotNil(t, candidate.OverviewPrior)
	}
	for _, test := range []struct {
		name    string
		mutate  func()
		restore func()
	}{
		{"expired", func() { frozen.deadline = time.Now().Add(-time.Second) }, func() { frozen.deadline = time.Now().Add(time.Minute) }},
		{"incomplete", func() { frozen.priorComplete = false }, func() { frozen.priorComplete = true }},
		{"read_error", func() { s.ranking.lastError = &OpenAIEvalRankingError{Code: "test"} }, func() { s.ranking.lastError = nil }},
		{"config_changed", func() { s.ranking.config.Revision++ }, func() { s.ranking.config.Revision-- }},
		{"rule", func() {
			s.ranking.config.Policies = []OpenAIEvalSchedulingPolicyRule{{RequestedModel: "cold-model", Policy: OpenAIEvalSchedulingPolicyCostFirst}}
		}, func() { s.ranking.config.Policies = nil }},
		{"weights", func() { frozen.weights.Price += .01 }, func() { frozen.weights.Price -= .01 }},
		{"unrepresented", func() { a := accounts.items[0]; a.ID = 3; accounts.items = append(accounts.items, a) }, func() { accounts.items = accounts.items[:2] }},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.mutate()
			defer test.restore()
			_, trace, err := scheduler.explicitRanking(context.Background(), req, accounts.items, nil)
			require.NoError(t, err)
			require.NotEqual(t, "overview_prior", trace.RankingBasis)
		})
	}
	// One exact failure makes the pool mixed; other-model quality cannot leak in.
	gateway.openaiAccountStats.reportForRequest(1, "cold-model", "", false, nil)
	rows, trace, err = scheduler.explicitRanking(context.Background(), req, accounts.items, nil)
	require.NoError(t, err)
	require.Equal(t, "live_fallback", trace.RankingBasis)
	for _, row := range rows {
		require.Nil(t, row.Factors.Quality.Ratio)
		require.Nil(t, row.OverviewPrior)
	}
	stat, _ := gateway.openaiAccountStats.loadRoute(1, "cold-model", "")
	stat.observedAt.Store(time.Now().Add(-48 * time.Hour).UnixNano())
	_, trace, err = scheduler.explicitRanking(context.Background(), req, accounts.items, nil)
	require.NoError(t, err)
	require.NotEqual(t, "overview_prior", trace.RankingBasis, "expired evidence is not fully cold")
	after, _ := json.Marshal(frozen.overview)
	require.Equal(t, string(before), string(after))
	require.Same(t, frozen, s.ranking.current)

	req.RequestedModel = "gpt-6.1-sol"
	req.RequestedReasoningEffort = "high"
	gateway.openaiAccountStats.reportForRequest(1, req.RequestedModel, "high", true, rankingPtr(200))
	_, err = s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	_, trace, err = scheduler.explicitRanking(context.Background(), req, accounts.items, nil)
	require.NoError(t, err)
	require.Equal(t, "snapshot", trace.RankingBasis)
	gateway.openaiAccountStats.reportForRequest(1, req.RequestedModel, "low", false, nil)
	_, trace, err = scheduler.explicitRanking(context.Background(), req, accounts.items, nil)
	require.NoError(t, err)
	require.Equal(t, "snapshot", trace.RankingBasis, "other effort cannot invalidate this dimension")
	gateway.openaiAccountStats.reportForRequest(1, req.RequestedModel, "high", false, nil)
	_, trace, err = scheduler.explicitRanking(context.Background(), req, accounts.items, nil)
	require.NoError(t, err)
	require.Equal(t, "live_fallback", trace.RankingBasis)
	require.Equal(t, "request_metrics_updated", *trace.RankingFallbackReason)
}

func TestOpenAIAccountOverviewPolicyDefaultsAndExceptions(t *testing.T) {
	s, repo, _, _ := rankingHarness(t)
	repo.config.Revision++
	repo.config.SchedulingPolicy = OpenAIEvalSchedulingPolicyCustomBalance
	repo.config.CustomBalance = OpenAIEvalPolicyWeights{Quality: 1}
	repo.config.Policies = []OpenAIEvalSchedulingPolicyRule{{RequestedModel: "gpt-6.1-sol", Policy: OpenAIEvalSchedulingPolicyStabilityFirst}}
	repo.config.Accounts = []OpenAIEvalAccountConfig{{AccountID: 1, RequestedModel: "gpt-6.1-sol", CandySchedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300}}}
	repo.runs = []OpenAIEvalRun{overviewQualityRun(1, 1, "gpt-6.1-sol", "", OpenAIEvalTypeCandy, true, time.Now())}
	_, err := s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	result, err := s.SchedulingAccountOverview(OpenAIEvalRankingFilter{})
	require.NoError(t, err)
	require.Equal(t, OpenAIEvalSchedulingPolicyCustomBalance, result.Policy)
	require.Equal(t, OpenAIEvalRankingWeights{Quality: 1}, result.Weights)
	one, two := overviewRow(t, result, 1), overviewRow(t, result, 2)
	require.Equal(t, 100., *one.PriorityScore)
	require.Equal(t, OpenAIEvalSchedulingPolicyStabilityFirst, one.Models[0].Policy)
	require.Zero(t, one.Models[0].Weights.Quality)
	require.Equal(t, 50., *two.PriorityScore)
	require.False(t, two.Factors.Quality.Known)
	require.Nil(t, two.Factors.Quality.Ratio)

	repo.config.Revision++
	repo.config.SchedulingPolicy = OpenAIEvalSchedulingPolicyStabilityFirst
	_, err = s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	before, err := s.SchedulingAccountOverview(OpenAIEvalRankingFilter{})
	require.NoError(t, err)
	repo.runs = []OpenAIEvalRun{overviewQualityRun(2, 1, "gpt-6.1-sol", "", OpenAIEvalTypeCandy, false, time.Now())}
	_, err = s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	after, err := s.SchedulingAccountOverview(OpenAIEvalRankingFilter{})
	require.NoError(t, err)
	require.Equal(t, overviewRow(t, before, 1).PriorityScore, overviewRow(t, after, 1).PriorityScore)
	require.Equal(t, overviewRow(t, before, 1).Rank, overviewRow(t, after, 1).Rank)
	require.Zero(t, overviewRow(t, after, 1).Contributions.Quality)
}

func TestOpenAIAccountOverviewPartialQualityEvidenceExpiry(t *testing.T) {
	s, repo, _, _ := rankingHarness(t)
	now := time.Now()
	s.ranking.now = func() time.Time { return now }
	repo.config.Revision++
	repo.config.QualityRefreshIntervalSeconds = 300
	schedule := OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300}
	repo.config.Accounts = []OpenAIEvalAccountConfig{{AccountID: 1, RequestedModel: "gpt-6.1-sol", CandySchedule: schedule, FingerprintSchedule: schedule}}
	run := overviewQualityRun(1, 1, "gpt-6.1-sol", "", OpenAIEvalTypeCandy, true, now)
	run.FinishedAt = now.Add(-590 * time.Second)
	repo.runs = []OpenAIEvalRun{run}
	summary, err := s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, 10*time.Second, summary.NextEvaluationAt.Sub(now))
	result, err := s.SchedulingAccountOverview(OpenAIEvalRankingFilter{})
	require.NoError(t, err)
	require.Equal(t, 1, overviewRow(t, result, 1).ModelCount)
	require.False(t, overviewRow(t, result, 1).Factors.Quality.Known)
	now = now.Add(11 * time.Second)
	_, err = s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	result, err = s.SchedulingAccountOverview(OpenAIEvalRankingFilter{})
	require.NoError(t, err)
	require.Zero(t, overviewRow(t, result, 1).ModelCount)
	require.Len(t, result.Accounts, 2)
}

func TestOpenAIAccountOverviewConcurrentMetricsEvaluationAndReads(t *testing.T) {
	s, _, accounts, gateway := rankingHarness(t)
	gateway.openaiAccountStats.reportForRequest(1, "gpt-6.1-sol", "high", true, rankingPtr(100))
	_, err := s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	scheduler := gateway.persistentOpenAIAccountScheduler().(*defaultOpenAIAccountScheduler)
	req := OpenAIAccountScheduleRequest{GroupID: rankingPtr(int64(7)), Platform: PlatformOpenAI, RequestedModel: "gpt-6.1-sol", RequestedReasoningEffort: "high"}
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			gateway.openaiAccountStats.reportForRequest(1, req.RequestedModel, "high", i%2 == 0, rankingPtr(100+i))
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 5; i++ {
			if _, err := s.EvaluateScheduling(context.Background(), 1); err != nil {
				errors <- err
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			if _, err := s.SchedulingAccountOverview(OpenAIEvalRankingFilter{}); err != nil {
				errors <- err
				return
			}
			if _, _, err := scheduler.explicitRanking(context.Background(), req, accounts.items, nil); err != nil {
				errors <- err
				return
			}
		}
	}()
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
}
