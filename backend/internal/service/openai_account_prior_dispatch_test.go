package service

import (
	"context"
	"encoding/json"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A successful business request is operational evidence, not a quality test.
// It must not erase the account prior when this model/effort has no diagnostics.
func TestOpenAIAccountPriorSurvivesOrdinaryRequestMetrics(t *testing.T) {
	s, repo, accounts, gateway := rankingHarness(t)
	repo.config.SchedulingPolicy = OpenAIEvalSchedulingPolicyAvoidDegradation
	repo.config.Revision++
	schedule := OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300}
	repo.config.Accounts = []OpenAIEvalAccountConfig{
		{AccountID: 1, RequestedModel: "gpt-6.1-sol", ReasoningEffort: "medium", CandySchedule: schedule},
		{AccountID: 2, RequestedModel: "gpt-6.1-sol", ReasoningEffort: "medium", CandySchedule: schedule},
	}
	repo.runs = []OpenAIEvalRun{
		overviewQualityRun(1, 1, "gpt-6.1-sol", "medium", OpenAIEvalTypeCandy, false, time.Now()),
		overviewQualityRun(2, 2, "gpt-6.1-sol", "medium", OpenAIEvalTypeCandy, true, time.Now()),
	}
	_, err := s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	scheduler := gateway.persistentOpenAIAccountScheduler().(*defaultOpenAIAccountScheduler)
	req := OpenAIAccountScheduleRequest{GroupID: rankingPtr(int64(7)), Platform: PlatformOpenAI,
		RequestedModel: "gpt-6.1-sol", RequestedReasoningEffort: "high"}
	rows, _, err := scheduler.explicitRanking(context.Background(), req, accounts.items, nil)
	require.NoError(t, err)
	require.Equal(t, int64(2), rows[0].AccountID)
	for _, id := range []int64{1, 2} {
		gateway.openaiAccountStats.reportForRequest(id, req.RequestedModel, req.RequestedReasoningEffort, true, rankingPtr(200))
	}
	rows, _, err = scheduler.explicitRanking(context.Background(), req, accounts.items, nil)
	require.NoError(t, err)
	require.Equal(t, int64(2), rows[0].AccountID, "ordinary successful requests must not erase the quality-aware account prior")
	for _, row := range rows {
		require.False(t, row.Factors.Quality.Known)
		require.Nil(t, row.Factors.Quality.Ratio, "account prior is not an exact-model diagnostic result")
		require.Equal(t, "model_effort_fallback", row.QualityBasis)
		require.NotNil(t, row.AccountQualityPrior)
	}
	ctx := WithRequestedReasoningEffort(context.Background(), "high")
	selected, decision, err := gateway.SelectAccountWithScheduler(ctx, req.GroupID, "", "", req.RequestedModel, nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.Equal(t, int64(2), selected.Account.ID, "real admission must follow the prior quality tiers")
	selected.ReleaseFunc()
	require.Equal(t, "live_fallback", decision.RankingBasis)
	for _, candidate := range decision.Candidates {
		require.Equal(t, "model_effort_fallback", candidate.QualityBasis)
		require.Equal(t, "model_effort_fallback", candidate.DecisionReason)
		require.Nil(t, candidate.QualityRatio)
		require.Zero(t, candidate.EvaluatedCount)
	}
	// The group restriction and failure exclusion still dominate the rank.
	selected, _, err = gateway.SelectAccountWithScheduler(ctx, req.GroupID, "", "", req.RequestedModel, map[int64]struct{}{2: {}}, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.Equal(t, int64(1), selected.Account.ID)
	selected.ReleaseFunc()
}

func TestOpenAIAccountPriorModelIsolationAndUntestedFallback(t *testing.T) {
	s, repo, accounts, gateway := rankingHarness(t)
	repo.config.SchedulingPolicy = OpenAIEvalSchedulingPolicyAvoidDegradation
	repo.config.Revision++
	schedule := OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300}
	repo.config.Accounts = []OpenAIEvalAccountConfig{
		{AccountID: 1, RequestedModel: "gpt-6.1-sol", ReasoningEffort: "high", CandySchedule: schedule},
		{AccountID: 2, RequestedModel: "gpt-6.1-sol", ReasoningEffort: "high", CandySchedule: schedule},
	}
	now := time.Now()
	repo.runs = []OpenAIEvalRun{
		overviewQualityRun(1, 1, "gpt-6.1-sol", "high", OpenAIEvalTypeCandy, false, now),
		overviewQualityRun(2, 2, "gpt-6.1-sol", "high", OpenAIEvalTypeCandy, true, now),
	}
	_, err := s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	scheduler := gateway.persistentOpenAIAccountScheduler().(*defaultOpenAIAccountScheduler)
	rows, _, err := scheduler.explicitRanking(context.Background(), OpenAIAccountScheduleRequest{
		GroupID: rankingPtr(int64(7)), Platform: PlatformOpenAI, RequestedModel: "gpt-6.1-sol", RequestedReasoningEffort: "high",
	}, accounts.items, nil)
	require.NoError(t, err)
	require.Equal(t, int64(2), rows[0].AccountID)
	require.Equal(t, "exact", rows[0].QualityBasis)

	// Astra has no configured diagnostics. It must use the aggregate leaderboard
	// fallback, but the route remains explicitly labeled as a fallback.
	astraRows, trace, err := scheduler.explicitRanking(context.Background(), OpenAIAccountScheduleRequest{
		GroupID: rankingPtr(int64(7)), Platform: PlatformOpenAI, RequestedModel: "gpt-6-astra", RequestedReasoningEffort: "high",
	}, accounts.items, nil)
	require.NoError(t, err)
	require.NotEmpty(t, astraRows)
	require.Equal(t, "overview_prior", trace.RankingBasis)
	for _, row := range astraRows {
		require.Nil(t, row.Factors.Quality.Ratio)
		require.NotNil(t, row.OverviewPrior)
	}
}

func TestOpenAIAccountPriorDoesNotCrossModelLaunderWithinCandidatePool(t *testing.T) {
	s, repo, accounts, gateway := rankingHarness(t)
	repo.config.SchedulingPolicy = OpenAIEvalSchedulingPolicyAvoidDegradation
	repo.config.Revision++
	schedule := OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300}
	repo.config.Accounts = []OpenAIEvalAccountConfig{
		{AccountID: 1, RequestedModel: "gpt-6-astra", ReasoningEffort: "medium", CandySchedule: schedule},
		{AccountID: 2, RequestedModel: "gpt-6.1-sol", ReasoningEffort: "high", CandySchedule: schedule},
	}
	now := time.Now()
	repo.runs = []OpenAIEvalRun{
		overviewQualityRun(1, 1, "gpt-6-astra", "medium", OpenAIEvalTypeCandy, true, now),
		overviewQualityRun(2, 2, "gpt-6.1-sol", "high", OpenAIEvalTypeCandy, true, now),
	}
	_, err := s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	scheduler := gateway.persistentOpenAIAccountScheduler().(*defaultOpenAIAccountScheduler)
	req := OpenAIAccountScheduleRequest{GroupID: rankingPtr(int64(7)), Platform: PlatformOpenAI,
		RequestedModel: "gpt-6-astra", RequestedReasoningEffort: "high"}
	rows, _, err := scheduler.explicitRanking(context.Background(), req, accounts.items, nil)
	require.NoError(t, err)
	require.NotEmpty(t, rows)
	require.Equal(t, int64(1), rows[0].AccountID, "Astra evidence must beat an unrelated sol result")
	for _, row := range rows {
		if row.AccountID == 1 {
			require.Equal(t, "model_effort_fallback", row.QualityBasis)
			require.NotNil(t, row.AccountQualityPrior)
		} else if row.AccountID == 2 {
			require.Equal(t, "aggregate_fallback", row.QualityBasis)
			require.NotNil(t, row.AccountQualityPrior)
		}
	}
	ctx := WithRequestedReasoningEffort(context.Background(), "high")
	selected, decision, err := gateway.SelectAccountWithScheduler(ctx, req.GroupID, "", "", req.RequestedModel, nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.Equal(t, int64(1), selected.Account.ID)
	selected.ReleaseFunc()
	for _, candidate := range decision.Candidates {
		if candidate.AccountID == 2 {
			require.Equal(t, "aggregate_fallback", candidate.QualityBasis)
			require.NotNil(t, candidate.AccountQualityPrior)
		}
	}
}

func TestOpenAIAccountPriorActualDispatchUsesRequestedModel(t *testing.T) {
	for _, requestedModel := range []string{"gpt-6-astra", "gpt-6.1-sol"} {
		t.Run(requestedModel, func(t *testing.T) {
			evaluation, repository, accountRepo, gateway := rankingHarness(t)
			repository.config.SchedulingPolicy = OpenAIEvalSchedulingPolicyAvoidDegradation
			repository.config.Revision++
			accountRepo.items[0].RateMultiplier = rankingPtr(0.2)
			accountRepo.items[1].RateMultiplier = rankingPtr(0.1)
			schedule := OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300}
			repository.config.Accounts = []OpenAIEvalAccountConfig{
				{AccountID: 1, RequestedModel: "gpt-6-astra", ReasoningEffort: "high", CandySchedule: schedule},
				{AccountID: 2, RequestedModel: "gpt-6-astra", ReasoningEffort: "high", CandySchedule: schedule},
				{AccountID: 1, RequestedModel: "gpt-6.1-sol", ReasoningEffort: "high", CandySchedule: schedule},
				{AccountID: 2, RequestedModel: "gpt-6.1-sol", ReasoningEffort: "high", CandySchedule: schedule},
			}
			now := time.Now()
			repository.runs = []OpenAIEvalRun{
				overviewQualityRun(1, 1, "gpt-6-astra", "high", OpenAIEvalTypeCandy, true, now),
				overviewQualityRun(2, 2, "gpt-6-astra", "high", OpenAIEvalTypeCandy, false, now),
				overviewQualityRun(3, 1, "gpt-6.1-sol", "high", OpenAIEvalTypeCandy, false, now),
				overviewQualityRun(4, 2, "gpt-6.1-sol", "high", OpenAIEvalTypeCandy, true, now),
			}
			_, err := evaluation.EvaluateScheduling(context.Background(), 1)
			require.NoError(t, err)
			ctx := WithRequestedReasoningEffort(context.Background(), "high")
			selection, decision, err := gateway.SelectAccountWithScheduler(ctx, rankingPtr(int64(7)), "", "", requestedModel, nil, OpenAIUpstreamTransportAny, false)
			require.NoError(t, err)
			require.NotNil(t, selection)
			expectedAccountID := int64(1)
			if requestedModel == "gpt-6.1-sol" {
				expectedAccountID = 2
			}
			require.Equal(t, expectedAccountID, selection.Account.ID)
			selection.ReleaseFunc()
			require.Len(t, decision.Candidates, 2)
			for _, candidate := range decision.Candidates {
				require.Equal(t, "exact", candidate.QualityBasis)
				require.Nil(t, candidate.AccountQualityPrior)
				require.NotNil(t, candidate.QualityRatio)
				expectedRatio := 0.0
				if candidate.AccountID == expectedAccountID {
					expectedRatio = 1.0
				}
				require.Equal(t, expectedRatio, *candidate.QualityRatio)
			}
		})
	}
}

func TestOpenAIAccountPriorActualDispatchKeepsUnavailableModelUnknown(t *testing.T) {
	for _, condition := range []string{"missing", "stale", "error"} {
		t.Run(condition, func(t *testing.T) {
			evaluation, repository, accountRepo, gateway := rankingHarness(t)
			repository.config.SchedulingPolicy = OpenAIEvalSchedulingPolicyAvoidDegradation
			repository.config.Revision++
			accountRepo.items[0].RateMultiplier = rankingPtr(0.1)
			accountRepo.items[1].RateMultiplier = rankingPtr(0.2)
			schedule := OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300}
			repository.config.Accounts = []OpenAIEvalAccountConfig{
				{AccountID: 1, RequestedModel: "gpt-6-astra", ReasoningEffort: "high", CandySchedule: schedule},
				{AccountID: 2, RequestedModel: "gpt-6.1-sol", ReasoningEffort: "high", CandySchedule: schedule},
			}
			now := time.Now()
			repository.runs = []OpenAIEvalRun{overviewQualityRun(1, 2, "gpt-6.1-sol", "high", OpenAIEvalTypeCandy, true, now)}
			if condition != "missing" {
				run := overviewQualityRun(2, 1, "gpt-6-astra", "high", OpenAIEvalTypeCandy, true, now.Add(-48*time.Hour))
				if condition == "error" {
					run = overviewQualityRun(2, 1, "gpt-6-astra", "high", OpenAIEvalTypeCandy, true, now)
					run.Status = "error"
				}
				repository.runs = append(repository.runs, run)
			}
			_, err := evaluation.EvaluateScheduling(context.Background(), 1)
			require.NoError(t, err)
			ctx := WithRequestedReasoningEffort(context.Background(), "high")
			selection, decision, err := gateway.SelectAccountWithScheduler(ctx, rankingPtr(int64(7)), "", "", "gpt-6-astra", nil, OpenAIUpstreamTransportAny, false)
			require.NoError(t, err)
			require.NotNil(t, selection)
			require.Equal(t, int64(2), selection.Account.ID, "an unassessed model uses the account-level leaderboard until model evidence exists")
			selection.ReleaseFunc()
			require.Len(t, decision.Candidates, 2)
			for _, candidate := range decision.Candidates {
				if candidate.AccountID == 2 {
					require.Equal(t, "aggregate_fallback", candidate.QualityBasis)
					require.NotNil(t, candidate.AccountQualityPrior)
				} else {
					require.Equal(t, "none", candidate.QualityBasis)
					require.Nil(t, candidate.QualityRatio)
					require.Nil(t, candidate.AccountQualityPrior)
				}
				require.Nil(t, candidate.OverviewPrior)
			}
		})
	}
}

func TestOpenAIAccountPriorScorerQualityTiersAndPolicies(t *testing.T) {
	now := time.Now()
	inputs := make([]openAIEvalRankingInput, 4)
	for i := range inputs {
		inputs[i] = openAIEvalRankingInput{account: &Account{ID: int64(i + 1), Type: AccountTypeAPIKey, Platform: PlatformOpenAI, RateMultiplier: rankingPtr(float64(i + 1))}, factors: emptyOpenAIEvalRankingFactors(), compatible: true}
	}
	inputs[1].factors.Quality = OpenAIEvalRankingQuality{OpenAIEvalFactorMeta: rankingKnown(1, now), Selected: 1, Evaluated: 1, Pass: 1, Ratio: rankingPtr(1.)}
	priors := map[int64]*OpenAIEvalAccountQualityPrior{
		1: {Ratio: 1, fraction: big.NewRat(1, 1), ExpiresAt: now.Add(time.Hour), SourceModels: []string{"other/medium"}},
		3: {Ratio: 0, fraction: big.NewRat(0, 1), ExpiresAt: now.Add(time.Hour)},
	}
	_, weights := openAIEvalRankingWeights(&OpenAIEvalConfig{SchedulingPolicy: OpenAIEvalSchedulingPolicyAvoidDegradation}, "", "")
	rows := scoreOpenAIEvalRanking(OpenAIEvalSchedulingPolicyAvoidDegradation, weights, inputs, now, nil, priors)
	require.Equal(t, []int64{2, 1, 3, 4}, priorRowIDs(rows), "exact wins equal fraction; known zero beats unknown")
	require.Equal(t, "exact", rows[0].QualityBasis)
	require.Equal(t, "account_prior", rows[1].QualityBasis)
	require.Equal(t, "none", rows[3].QualityBasis)
	rows[1].AccountQualityPrior.SourceModels[0] = "changed"
	require.Equal(t, "other/medium", priors[1].SourceModels[0], "prior DTO must own its sources")
	for _, policy := range []string{"", OpenAIEvalSchedulingPolicyCostFirst, OpenAIEvalSchedulingPolicyStabilityFirst, OpenAIEvalSchedulingPolicyCustomBalance} {
		t.Run(policy, func(t *testing.T) {
			cfg := &OpenAIEvalConfig{SchedulingPolicy: policy, CustomBalance: OpenAIEvalPolicyWeights{Cost: 1}}
			p, w := openAIEvalRankingWeights(cfg, "", "")
			before := scoreOpenAIEvalRanking(p, w, inputs, now, nil)
			after := scoreOpenAIEvalRanking(p, w, inputs, now, nil, priors)
			require.Equal(t, priorRowIDs(before), priorRowIDs(after))
			for _, row := range after {
				require.Nil(t, row.AccountQualityPrior)
			}
		})
	}
	// Within one prior tier, runtime evidence changes the price order only past the sample-backed threshold.
	priors[2] = cloneAccountQualityPrior(priors[1])
	inputs[1].factors.Quality = emptyOpenAIEvalRankingFactors().Quality
	inputs[0].factors.ErrorRate = OpenAIEvalRankingErrorRate{OpenAIEvalFactorMeta: rankingKnown(0, now), Value: rankingPtr(1.), SampleCount: 10}
	inputs[1].factors.ErrorRate = OpenAIEvalRankingErrorRate{OpenAIEvalFactorMeta: rankingKnown(1, now), Value: rankingPtr(0.), SampleCount: 10}
	rows = scoreOpenAIEvalRanking(OpenAIEvalSchedulingPolicyAvoidDegradation, OpenAIEvalRankingWeights{ErrorRate: 1}, inputs, now, nil, priors)
	require.Equal(t, int64(2), rows[0].AccountID)
	var failedRow OpenAIEvalRankedAccount
	for _, row := range rows {
		if row.AccountID == 1 {
			failedRow = row
		}
	}
	require.Contains(t, failedRow.ThresholdReasons, "error_rate_threshold")
	require.Equal(t, rows[0].AccountQualityPrior.Ratio, failedRow.AccountQualityPrior.Ratio)
}

func TestOpenAIAccountPriorModelPolicyOverride(t *testing.T) {
	for _, policy := range []string{"", OpenAIEvalSchedulingPolicyCostFirst, OpenAIEvalSchedulingPolicyStabilityFirst} {
		t.Run(policy, func(t *testing.T) {
			s, repo, accounts, gateway := rankingHarness(t)
			repo.config.SchedulingPolicy = policy
			repo.config.Policies = []OpenAIEvalSchedulingPolicyRule{{RequestedModel: "gpt-6.1-sol", ReasoningEffort: "high", Policy: OpenAIEvalSchedulingPolicyAvoidDegradation}}
			repo.config.Revision++
			schedule := OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300}
			repo.config.Accounts = []OpenAIEvalAccountConfig{{AccountID: 2, RequestedModel: "gpt-6.1-sol", ReasoningEffort: "medium", CandySchedule: schedule}}
			repo.runs = []OpenAIEvalRun{overviewQualityRun(1, 2, "gpt-6.1-sol", "medium", OpenAIEvalTypeCandy, true, time.Now())}
			gateway.persistentOpenAIAccountScheduler()
			gateway.openaiAccountStats.reportForRequest(1, "gpt-6.1-sol", "high", true, rankingPtr(200))
			_, err := s.EvaluateScheduling(context.Background(), 1)
			require.NoError(t, err)
			quality := s.ranking.current.overview[s.ranking.current.overviewByID[2]].Factors.Quality
			require.True(t, quality.Known, "published account evidence exists independently of the overview policy")
			require.Equal(t, 1., *quality.Ratio)
			priors := accountQualityPriors(s.ranking.current, &repo.config, 7, "gpt-6.1-sol", "high", accounts.items, nil, time.Now())
			require.NotNil(t, priors[2], "effective model rule must not require same overview policy")
			dim := rankingDimension(t, s, 7, "gpt-6.1-sol", "high")
			require.Equal(t, int64(2), dim.Accounts[0].AccountID)
			req := OpenAIAccountScheduleRequest{GroupID: rankingPtr(int64(7)), Platform: PlatformOpenAI, RequestedModel: "gpt-6.1-sol", RequestedReasoningEffort: "high"}
			scheduler := gateway.persistentOpenAIAccountScheduler().(*defaultOpenAIAccountScheduler)
			gateway.openaiAccountStats.reportForRequest(1, "gpt-6.1-sol", "high", true, rankingPtr(200))
			rows, trace, err := scheduler.explicitRanking(context.Background(), req, accounts.items, nil)
			require.NoError(t, err)
			require.Equal(t, "live_fallback", trace.RankingBasis)
			require.Equal(t, int64(2), rows[0].AccountID)
			ctx := WithRequestedReasoningEffort(context.Background(), "high")
			selected, _, err := gateway.SelectAccountWithScheduler(ctx, req.GroupID, "", "", req.RequestedModel, nil, OpenAIUpstreamTransportAny, false)
			require.NoError(t, err)
			require.Equal(t, int64(2), selected.Account.ID, "real admission must honor the model-specific policy")
			selected.ReleaseFunc()
		})
	}
}

func priorRowIDs(rows []OpenAIEvalRankedAccount) []int64 {
	ids := make([]int64, len(rows))
	for i := range rows {
		ids[i] = rows[i].AccountID
	}
	return ids
}

func TestOpenAIAccountPriorEligibilityAndNoLaundering(t *testing.T) {
	for _, condition := range []string{"fresh", "revision", "generation_expired", "quality_expired", "disabled", "wrong_group", "missing_overview", "configured_missing", "configured_stale", "exact_diagnostic", "policy_override", "effects_off", "ungrouped"} {
		t.Run(condition, func(t *testing.T) {
			s, repo, accounts, _ := rankingHarness(t)
			repo.config.SchedulingPolicy = OpenAIEvalSchedulingPolicyAvoidDegradation
			repo.config.Revision++
			schedule := OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300}
			repo.config.Accounts = []OpenAIEvalAccountConfig{{AccountID: 2, RequestedModel: "gpt-6.1-sol", ReasoningEffort: "medium", CandySchedule: schedule}}
			repo.runs = []OpenAIEvalRun{overviewQualityRun(1, 2, "gpt-6.1-sol", "medium", OpenAIEvalTypeCandy, true, time.Now())}
			_, err := s.EvaluateScheduling(context.Background(), 1)
			require.NoError(t, err)
			gen, cfg := s.ranking.current, cloneRankingConfig(s.ranking.config)
			now, group := time.Now(), int64(7)
			latest := map[OpenAIEvalEvidenceKey]OpenAIEvalRun{}
			switch condition {
			case "revision":
				cfg.Revision++
			case "generation_expired":
				gen.deadline = now.Add(-time.Second)
			case "quality_expired":
				gen.overview[gen.overviewByID[2]].Factors.Quality.ExpiresAt = rankingPtr(now.Add(-time.Second))
			case "disabled":
				accounts.items[1].Schedulable = false
			case "wrong_group":
				accounts.items[1].GroupIDs = []int64{9}
			case "missing_overview":
				delete(gen.overviewByID, 2)
			case "configured_missing", "configured_stale":
				cfg.Accounts = append(cfg.Accounts, OpenAIEvalAccountConfig{AccountID: 2, RequestedModel: "gpt-6.1-sol", ReasoningEffort: "high", CandySchedule: schedule})
				if condition == "configured_stale" {
					latest[OpenAIEvalEvidenceKey{2, "gpt-6.1-sol", "high", OpenAIEvalTypeCandy}] = overviewQualityRun(2, 2, "gpt-6.1-sol", "high", OpenAIEvalTypeCandy, true, now.Add(-48*time.Hour))
				}
			case "exact_diagnostic":
				latest[OpenAIEvalEvidenceKey{2, "gpt-6.1-sol", "high", OpenAIEvalTypeCandy}] = OpenAIEvalRun{Status: "error"}
			case "policy_override":
				cfg.Policies = []OpenAIEvalSchedulingPolicyRule{{RequestedModel: "gpt-6.1-sol", ReasoningEffort: "high", Policy: OpenAIEvalSchedulingPolicyStabilityFirst}}
			case "effects_off":
				cfg.EffectsEnabled = false
			case "ungrouped":
				group = 0
				accounts.items[1].GroupIDs = nil
				gen.overview[gen.overviewByID[2]].GroupIDs = []int64{0}
			}
			priors := accountQualityPriors(gen, cfg, group, "gpt-6.1-sol", "high", accounts.items, latest, now)
			if condition == "fresh" || condition == "ungrouped" || condition == "configured_missing" || condition == "configured_stale" || condition == "exact_diagnostic" {
				require.NotNil(t, priors[2])
				require.Equal(t, 1., priors[2].Ratio)
			} else {
				require.Nil(t, priors[2])
			}
		})
	}
}

func TestOpenAIAccountPriorSnapshotLiveParityAndImmutableDTO(t *testing.T) {
	s, repo, accounts, gateway := rankingHarness(t)
	repo.config.SchedulingPolicy = OpenAIEvalSchedulingPolicyAvoidDegradation
	repo.config.Revision++
	schedule := OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300}
	repo.config.Accounts = []OpenAIEvalAccountConfig{{AccountID: 2, RequestedModel: "gpt-6.1-sol", ReasoningEffort: "medium", CandySchedule: schedule}}
	repo.runs = []OpenAIEvalRun{overviewQualityRun(1, 2, "gpt-6.1-sol", "medium", OpenAIEvalTypeCandy, true, time.Now())}
	gateway.persistentOpenAIAccountScheduler()
	gateway.openaiAccountStats.reportForRequest(1, "gpt-6.1-sol", "high", true, rankingPtr(200))
	_, err := s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	dim := rankingDimension(t, s, 7, "gpt-6.1-sol", "high")
	require.Equal(t, int64(2), dim.Accounts[0].AccountID)
	require.Equal(t, "model_effort_fallback", dim.Accounts[0].QualityBasis)
	require.False(t, dim.Accounts[0].AccountQualityPrior.ExpiresAt.After(s.ranking.current.deadline))
	require.False(t, dim.Accounts[0].Factors.Quality.Known)
	require.False(t, dim.ValidUntil.After(dim.Accounts[0].AccountQualityPrior.ExpiresAt))
	scheduler := gateway.persistentOpenAIAccountScheduler().(*defaultOpenAIAccountScheduler)
	req := OpenAIAccountScheduleRequest{GroupID: rankingPtr(int64(7)), Platform: PlatformOpenAI, RequestedModel: "gpt-6.1-sol", RequestedReasoningEffort: "high"}
	before, err := json.Marshal(s.ranking.current.dimensions)
	require.NoError(t, err)
	rows, trace, err := scheduler.explicitRanking(context.Background(), req, accounts.items, nil)
	require.NoError(t, err)
	require.Equal(t, "snapshot", trace.RankingBasis)
	require.Equal(t, priorRowIDs(dim.Accounts), priorRowIDs(rows))
	rows[0].AccountQualityPrior.SourceModels[0] = "mutated"
	after, _ := json.Marshal(s.ranking.current.dimensions)
	require.Equal(t, string(before), string(after))
	// Force fallback without changing factors; identical input keeps the order.
	gateway.openaiAccountStats.reportForRequest(1, "gpt-6.1-sol", "high", true, rankingPtr(200))
	rows, trace, err = scheduler.explicitRanking(context.Background(), req, accounts.items, nil)
	require.NoError(t, err)
	require.Equal(t, "live_fallback", trace.RankingBasis)
	require.Equal(t, priorRowIDs(dim.Accounts), priorRowIDs(rows))
	require.Equal(t, "gpt-6.1-sol/medium", rows[0].AccountQualityPrior.SourceModels[0])
}
