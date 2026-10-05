package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestModelTraceCurrentVerdictHistoryQualityAndRanking(t *testing.T) {
	for _, tc := range []struct {
		name, requested, predicted, want string
		pass, suspected                  int
	}{
		{"exact", "gpt-6.1-sol", "gpt-6.1-sol", "pass", 1, 0},
		{"case_space", " GPT-6.1-SOL ", " gpt-6.1-sol ", "pass", 1, 0},
		{"dated_variant", "gpt-6.1-sol", "gpt-6.1-sol-2026-10-01", "suspected_normal", 0, 1},
		{"other", "gpt-6-astra", "gpt-6.1-sol", "suspected_normal", 0, 1},
		{"mapped_is_not_public", "public-model", "gpt-6.1-sol", "suspected_normal", 0, 1},
		{"luna", "gpt-6.1-sol", "vendor/GPT-6-LUNA", "warning", 0, 0},
		{"luna_target", "gpt-6-luna", "gpt-6-luna", "warning", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now().UTC()
			model := openAIEvalQualityDimension(tc.requested)
			run := OpenAIEvalRun{ID: 1, AccountID: 17, RequestedModel: tc.requested, UpstreamModel: tc.predicted,
				ReasoningEffort: "high", TestType: OpenAIEvalTypeModelTrace, Status: "attributed", Error: "http_502",
				TriggerSource: "manual", DataVersion: OpenAIEvalQualityDataVersion, FinishedAt: now.Add(-time.Second),
				Outcome: OpenAIEvalOutcome{Status: "attributed", Reason: "legacy_attribution", ModelTrace: &OpenAIEvalModelTraceResult{
					Prediction: tc.predicted, BankRevision: OpenAIEvalQualityModelTraceBankRevision, UsedOutputs: 1,
					Samples: []OpenAIEvalModelTraceSample{{Valid: true}, {Error: "http_502"}, {Error: "http_502"}},
				}}}
			require.Equal(t, OpenAIEvalQualityCounts{1, tc.pass, tc.suspected}, openAIEvalQualityCountsFromRun(&run))
			require.Equal(t, "attributed", run.Status)
			require.Equal(t, "legacy_attribution", run.Outcome.Reason)
			cfg := &OpenAIEvalConfig{Accounts: []OpenAIEvalAccountConfig{{AccountID: 17, RequestedModel: model, ReasoningEffort: "high",
				ModelTraceSchedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 3600}}}}
			key := OpenAIEvalEvidenceKey{17, model, "high", OpenAIEvalTypeModelTrace}
			q := qualityFromLatestRuns(cfg, 17, model, "high", map[OpenAIEvalEvidenceKey]OpenAIEvalRun{key: run}, now)
			require.True(t, q.Known)
			require.Equal(t, tc.pass, q.Pass)
			require.Equal(t, tc.suspected, q.SuspectedPass)
			normalizeOpenAIEvalAttributionRun(&run)
			require.Equal(t, tc.want, run.Status)
			require.Equal(t, "attributed", run.Outcome.Attribution.OriginalStatus)
			require.Equal(t, "legacy_attribution", run.Outcome.Attribution.OriginalReason)
			require.Equal(t, "legacy-unversioned", run.Outcome.Attribution.OriginalRuleVersion)
			require.Equal(t, OpenAIEvalModelTraceRuleVersion, run.Outcome.Attribution.RuleVersion)
			require.Equal(t, "http_502", run.Error)
			metadata := run.Outcome.Attribution
			normalizeOpenAIEvalAttributionRun(&run)
			require.Equal(t, tc.want, run.Status, "normalization must be idempotent")
			require.Equal(t, metadata, run.Outcome.Attribution)
			require.NotSame(t, metadata, run.Outcome.Attribution, "history copies must not mutate source metadata")
		})
	}
}

func TestModelTraceExactVerdictStillVotesOncePerSelectedType(t *testing.T) {
	now := time.Now().UTC()
	model := "gpt-6.1-sol"
	schedule := OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 3600}
	cfg := &OpenAIEvalConfig{Accounts: []OpenAIEvalAccountConfig{{AccountID: 17, RequestedModel: model, ReasoningEffort: "high", CandySchedule: schedule, ModelTraceSchedule: schedule}}}
	latest := make(map[OpenAIEvalEvidenceKey]OpenAIEvalRun)
	latest[OpenAIEvalEvidenceKey{17, model, "high", OpenAIEvalTypeCandy}] = OpenAIEvalRun{ID: 1, AccountID: 17, RequestedModel: model, ReasoningEffort: "high", TestType: OpenAIEvalTypeCandy,
		Status: "warning", TriggerSource: "manual", DataVersion: OpenAIEvalQualityDataVersion, FinishedAt: now.Add(-time.Second), Samples: []OpenAIEvalSampleRecord{{Valid: true, Answer: "29"}}}
	trace := OpenAIEvalRun{ID: 2, AccountID: 17, RequestedModel: model, ReasoningEffort: "high", TestType: OpenAIEvalTypeModelTrace,
		Status: "attributed", TriggerSource: "manual", DataVersion: OpenAIEvalQualityDataVersion, FinishedAt: now.Add(-time.Second), Outcome: OpenAIEvalOutcome{ModelTrace: &OpenAIEvalModelTraceResult{UsedOutputs: 1, Prediction: model, BankRevision: OpenAIEvalQualityModelTraceBankRevision}}}
	latest[OpenAIEvalEvidenceKey{17, model, "high", OpenAIEvalTypeModelTrace}] = trace
	q := qualityFromLatestRuns(cfg, 17, model, "high", latest, now)
	require.True(t, q.Known)
	require.Equal(t, 1, q.Pass)
	require.InDelta(t, .5, *q.Ratio, 1e-12)
	cfg.Accounts[0].FingerprintSchedule = schedule
	jsd := .1
	latest[OpenAIEvalEvidenceKey{17, model, "high", OpenAIEvalTypeFingerprint}] = OpenAIEvalRun{ID: 3, AccountID: 17, RequestedModel: model, ReasoningEffort: "high", TestType: OpenAIEvalTypeFingerprint,
		Status: "warning", TriggerSource: "scheduled", DataVersion: OpenAIEvalQualityDataVersion, BaselineVersion: OpenAIEvalQualityBaselineVersion, FinishedAt: now.Add(-time.Second),
		Outcome: OpenAIEvalOutcome{Fingerprint: &OpenAIEvalFingerprintResult{NearestModel: "gpt-6-luna", ValidSamples: 60, RequiredSamples: 60, CellCount: 4, MeanJSD: &jsd}}}
	q = qualityFromLatestRuns(cfg, 17, model, "high", latest, now)
	require.True(t, q.Known)
	require.InDelta(t, 1./3, *q.Ratio, 1e-12)
}

type modelTraceLatestRefreshRepo struct {
	*qualityRefreshRepository
	runs []OpenAIEvalRun
}

func (r *modelTraceLatestRefreshRepo) LatestCompletedRuns(_ context.Context, _ []OpenAIEvalEvidenceKey) ([]OpenAIEvalRun, error) {
	return r.runs, nil
}

func TestModelTraceQualityRefreshReinterpretsRawRunAndNeverRevivesOldPass(t *testing.T) {
	s, repo, accounts := setupQualityRefreshTest(t)
	repo.config.Accounts[0].CandySchedule.Enabled = false
	repo.config.Accounts[0].ModelTraceSchedule = OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 3600}
	now := time.Now().UTC().Add(-time.Minute)
	old := qualityTestAggregate(now, 17, 1, 0, 1)
	old.TestType, old.OutcomeStatus = OpenAIEvalTypeModelTrace, "suspected_normal"
	addQualityEvidence(t, accounts.accounts[17], old)
	_, known := ReadOpenAIEvalQualityFromAccount(accounts.accounts[17], "gpt-6.1-sol", "high", now, OpenAIEvalTypeModelTrace)
	require.False(t, known, "unversioned counts cannot establish whether the public target matched")
	latest := &modelTraceLatestRefreshRepo{qualityRefreshRepository: repo, runs: []OpenAIEvalRun{{ID: 1, AccountID: 17, RequestedModel: "gpt-6.1-sol", ReasoningEffort: "high",
		TestType: OpenAIEvalTypeModelTrace, Status: "attributed", TriggerSource: "manual", DataVersion: OpenAIEvalQualityDataVersion, FinishedAt: now,
		Outcome: OpenAIEvalOutcome{ModelTrace: &OpenAIEvalModelTraceResult{UsedOutputs: 1, Prediction: "gpt-6.1-sol", BankRevision: OpenAIEvalQualityModelTraceBankRevision}}}}}
	s.repo = latest
	result, err := s.refreshOpenAIEvalQuality(t.Context(), true)
	require.NoError(t, err)
	require.Equal(t, 1, result.RouteCount)
	q, known := openAIEvalQualitySnapshots.lookup(17, "gpt-6.1-sol", "high", time.Now())
	require.True(t, known)
	require.Equal(t, 1, q.PassCount)
	require.Equal(t, "attributed", latest.runs[0].Status, "raw record is immutable")
	newer := latest.runs[0]
	newer.ID, newer.Status, newer.FinishedAt = 2, "insufficient", now.Add(time.Second)
	newer.Outcome = OpenAIEvalOutcome{}
	latest.runs = append(latest.runs, newer)
	result, err = s.refreshOpenAIEvalQuality(t.Context(), true)
	require.NoError(t, err)
	require.Zero(t, result.RouteCount)
	_, known = openAIEvalQualitySnapshots.lookup(17, "gpt-6.1-sol", "high", time.Now())
	require.False(t, known)
}

func TestModelTraceMixedCaseLatestRankingMatchesRefresh(t *testing.T) {
	now := time.Now().UTC().Add(-time.Minute)
	run := OpenAIEvalRun{ID: 1, AccountID: 17, RequestedModel: " GPT-6.1-SOL ", ReasoningEffort: " HIGH ", TestType: OpenAIEvalTypeModelTrace,
		Status: "suspected_normal", TriggerSource: "manual", DataVersion: OpenAIEvalQualityDataVersion, FinishedAt: now,
		Outcome: OpenAIEvalOutcome{ModelTrace: &OpenAIEvalModelTraceResult{UsedOutputs: 1, Prediction: "gpt-6.1-sol", BankRevision: OpenAIEvalQualityModelTraceBankRevision}}}
	cfg := OpenAIEvalConfig{Accounts: []OpenAIEvalAccountConfig{{AccountID: 17, RequestedModel: "GPT-6.1-SOL", ReasoningEffort: "high", ModelTraceSchedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 3600}}}}
	s := &OpenAIEvalService{repo: &rankingTestRepo{config: cfg, runs: []OpenAIEvalRun{run}}}
	ranking := &OpenAIEvalRankingService{eval: s}
	latest, err := ranking.readLatest(t.Context(), &cfg)
	require.NoError(t, err)
	require.Len(t, latest, 1)
	q := qualityFromLatestRuns(&cfg, 17, "gpt-6.1-sol", "HIGH", latest, time.Now())
	require.True(t, q.Known)
	require.Equal(t, 1, q.Pass)
	routes, err := openAIEvalQualityRoutes(&cfg)
	require.NoError(t, err)
	refreshLatest, err := s.latestQualityRefreshRuns(t.Context(), routes)
	require.NoError(t, err)
	require.Equal(t, latest, refreshLatest)
}

func TestModelTraceInvalidEvidenceNeverBecomesNormal(t *testing.T) {
	for _, status := range []string{"running", "error", "insufficient", "cancelled", "attributed"} {
		run := OpenAIEvalRun{RequestedModel: "gpt-6.1-sol", TestType: OpenAIEvalTypeModelTrace, Status: status,
			Outcome: OpenAIEvalOutcome{ModelTrace: &OpenAIEvalModelTraceResult{Prediction: "gpt-6.1-sol", UsedOutputs: 2,
				Samples: []OpenAIEvalModelTraceSample{{Valid: true}, {Error: "http_502"}}}}}
		normalizeOpenAIEvalAttributionRun(&run)
		require.Equal(t, status, run.Status)
		require.Zero(t, openAIEvalQualityCountsFromRun(&run))
	}
}
