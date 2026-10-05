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
		require.Equal(t, "account_prior", row.QualityBasis)
		require.NotNil(t, row.AccountQualityPrior)
	}
	ctx := WithRequestedReasoningEffort(context.Background(), "high")
	selected, decision, err := gateway.SelectAccountWithScheduler(ctx, req.GroupID, "", "", req.RequestedModel, nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.Equal(t, int64(2), selected.Account.ID, "real admission must follow the prior quality tiers")
	selected.ReleaseFunc()
	require.Equal(t, "live_fallback", decision.RankingBasis)
	for _, candidate := range decision.Candidates {
		require.Equal(t, "account_prior", candidate.QualityBasis)
		require.Equal(t, "account_prior_tier", candidate.DecisionReason)
		require.Nil(t, candidate.QualityRatio)
		require.Zero(t, candidate.EvaluatedCount)
	}
	// The group restriction and failure exclusion still dominate the rank.
	selected, _, err = gateway.SelectAccountWithScheduler(ctx, req.GroupID, "", "", req.RequestedModel, map[int64]struct{}{2: {}}, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.Equal(t, int64(1), selected.Account.ID)
	selected.ReleaseFunc()
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
	// Within one prior tier, current-request runtime factors break the tie.
	priors[2] = cloneAccountQualityPrior(priors[1])
	inputs[1].factors.Quality = emptyOpenAIEvalRankingFactors().Quality
	inputs[0].factors.ErrorRate = OpenAIEvalRankingErrorRate{OpenAIEvalFactorMeta: rankingKnown(0, now)}
	inputs[1].factors.ErrorRate = OpenAIEvalRankingErrorRate{OpenAIEvalFactorMeta: rankingKnown(1, now)}
	rows = scoreOpenAIEvalRanking(OpenAIEvalSchedulingPolicyAvoidDegradation, OpenAIEvalRankingWeights{ErrorRate: 1}, inputs, now, nil, priors)
	require.Equal(t, int64(2), rows[0].AccountID)
	require.Equal(t, *rows[0].QualityTier, *rows[1].QualityTier)
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
			if condition == "fresh" || condition == "ungrouped" {
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
	require.Equal(t, "account_prior", dim.Accounts[0].QualityBasis)
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
