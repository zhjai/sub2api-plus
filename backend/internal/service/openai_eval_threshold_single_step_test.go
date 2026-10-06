package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRankingThresholdDemotesOnlyOnePosition(t *testing.T) {
	now := time.Now()
	var inputs []openAIEvalRankingInput
	for i := 1; i <= 4; i++ {
		f := emptyOpenAIEvalRankingFactors()
		if i == 1 {
			f.ErrorRate = OpenAIEvalRankingErrorRate{OpenAIEvalFactorMeta: rankingKnown(0, now), Value: rankingPtr(1.), SampleCount: 20}
		}
		inputs = append(inputs, openAIEvalRankingInput{account: &Account{ID: int64(i), Platform: PlatformOpenAI, Type: AccountTypeAPIKey, RateMultiplier: rankingPtr(float64(i) / 10)}, factors: f, compatible: true})
	}
	for _, policy := range []string{OpenAIEvalSchedulingPolicyCostFirst, OpenAIEvalSchedulingPolicyStabilityFirst} {
		rows := scoreOpenAIEvalRanking(policy, OpenAIEvalRankingWeights{Price: 1}, inputs, now, nil)
		require.Equal(t, []int64{2, 1, 3, 4}, []int64{rows[0].AccountID, rows[1].AccountID, rows[2].AccountID, rows[3].AccountID})
	}
	for i := range inputs {
		passes := 1
		if i < 2 {
			passes = 2
		}
		ratio := float64(passes) / 2
		inputs[i].factors.Quality = OpenAIEvalRankingQuality{OpenAIEvalFactorMeta: rankingKnown(ratio, now), Selected: 2, Evaluated: 2, Pass: passes, Ratio: rankingPtr(ratio)}
	}
	rows := scoreOpenAIEvalRanking(OpenAIEvalSchedulingPolicyAvoidDegradation, OpenAIEvalRankingWeights{Price: 1}, inputs, now, nil)
	require.Equal(t, []int64{2, 1, 3, 4}, []int64{rows[0].AccountID, rows[1].AccountID, rows[2].AccountID, rows[3].AccountID})
	inputs[1].factors.Quality = inputs[2].factors.Quality
	rows = scoreOpenAIEvalRanking(OpenAIEvalSchedulingPolicyAvoidDegradation, OpenAIEvalRankingWeights{Price: 1}, inputs, now, nil)
	require.EqualValues(t, 1, rows[0].AccountID, "no demotion across lower quality tier")
}

func TestRankingThresholdSingleStepOverviewAndActualDispatch(t *testing.T) {
	eval, repo, accounts, gateway := rankingHarness(t)
	base := accounts.items[0]
	accounts.items = nil
	for i := 1; i <= 4; i++ {
		a := base
		a.ID = int64(i)
		a.RateMultiplier = rankingPtr(float64(i) / 10)
		accounts.items = append(accounts.items, a)
	}
	for _, policy := range []string{OpenAIEvalSchedulingPolicyCostFirst, OpenAIEvalSchedulingPolicyStabilityFirst, OpenAIEvalSchedulingPolicyAvoidDegradation} {
		repo.config.SchedulingPolicy = policy
		repo.config.Revision++
		require.NoError(t, eval.Initialize(context.Background()))
		overviewMetric(t, gateway.openaiAccountStats, 1, "gpt-6.1-sol", "", .9, 30, 1000)
		_, err := eval.EvaluateScheduling(context.Background(), 1)
		require.NoError(t, err)
		overview, err := eval.SchedulingAccountOverview(OpenAIEvalRankingFilter{})
		require.NoError(t, err)
		require.Equal(t, []int64{2, 1, 3, 4}, []int64{overview.Accounts[0].AccountID, overview.Accounts[1].AccountID, overview.Accounts[2].AccountID, overview.Accounts[3].AccountID})
		selection, decision, err := gateway.SelectAccountWithScheduler(context.Background(), rankingPtr(int64(7)), "", "", "gpt-6.1-sol", nil, OpenAIUpstreamTransportAny, false)
		require.NoError(t, err)
		require.EqualValues(t, 2, selection.Account.ID)
		selection.ReleaseFunc()
		var order []int64
		for _, candidate := range decision.Candidates {
			if candidate.Rank != nil {
				order = append(order, candidate.AccountID)
			}
		}
		require.Equal(t, []int64{2, 1, 3, 4}, order)
	}
}

func TestAvoidDegradationQualityPrecedesExplicitAccountRule(t *testing.T) {
	eval, repo, _, gateway := rankingHarness(t)
	repo.config.Revision++
	repo.config.SchedulingPolicy = OpenAIEvalSchedulingPolicyAvoidDegradation
	repo.config.AccountPriorityRules = []OpenAIEvalAccountPriorityRule{{AccountID: 2, Priority: 0}}
	for _, id := range []int64{1, 2} {
		repo.config.Accounts = append(repo.config.Accounts, OpenAIEvalAccountConfig{AccountID: id, RequestedModel: "gpt-6.1-sol", CandySchedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300}})
		repo.runs = append(repo.runs, overviewQualityRun(id, id, "gpt-6.1-sol", "", OpenAIEvalTypeCandy, id == 1, time.Now()))
	}
	require.NoError(t, eval.Initialize(context.Background()))
	selection, _, err := gateway.SelectAccountWithScheduler(context.Background(), rankingPtr(int64(7)), "", "", "gpt-6.1-sol", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.EqualValues(t, 1, selection.Account.ID, "explicit account rule cannot cross pass-rate tier")
	selection.ReleaseFunc()
}
