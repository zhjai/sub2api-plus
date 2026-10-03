package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func qualitySchedulerAccounts(t *testing.T) []*Account {
	t.Helper()
	now := time.Now().UTC().Add(-time.Second)
	low := qualityTestAccount(t, qualityTestAggregate(now, 1, 10, 0, 0))
	high := qualityTestAccount(t, qualityTestAggregate(now, 2, 10, 10, 0))
	accounts := []*Account{low, high, {ID: 3}}
	installQualityTestSnapshot(t, accounts)
	return accounts
}

func installQualityTestSnapshot(t *testing.T, accounts []*Account) {
	t.Helper()
	previous := openAIEvalQualitySnapshots
	t.Cleanup(func() { openAIEvalQualitySnapshots = previous })
	cache := &openAIEvalQualitySnapshotStore{enabled: true, entries: make(map[string]OpenAIEvalQualityAssessment)}
	for _, account := range accounts {
		evidence := make(map[string][]OpenAIEvalQualityAggregate)
		routes := make(map[string]openAIEvalQualityRoute)
		for _, raw := range account.Extra {
			payload, err := json.Marshal(raw)
			if err != nil {
				continue
			}
			var quality OpenAIEvalQualityAggregate
			if json.Unmarshal(payload, &quality) != nil || !quality.validFor(account.ID, quality.RequestedModel, quality.ReasoningEffort, time.Now()) {
				continue
			}
			route := openAIEvalQualityRoute{AccountID: account.ID, Model: quality.RequestedModel, Effort: quality.ReasoningEffort}
			key := route.key()
			if previous, ok := routes[key]; ok {
				route = previous
			}
			for i, testType := range openAIEvalQualityTestTypes {
				if testType == quality.TestType {
					route.Intervals[i] = 3600
				}
			}
			routes[key] = route
			evidence[key] = append(evidence[key], quality)
		}
		for key, route := range routes {
			assessment, ok := assessOpenAIEvalQuality(route, evidence[key], time.Now())
			require.True(t, ok)
			cache.entries[key] = assessment
		}
	}
	openAIEvalQualitySnapshots = cache
}

func TestOpenAIQualitySchedulerTopKTraceAndNeutral(t *testing.T) {
	enableQualityEffects(t)
	s := openAIResetTestScheduler(0)
	s.service.cfg.Gateway.OpenAIWS.LBTopK = 1
	accounts := qualitySchedulerAccounts(t)
	req := OpenAIAccountScheduleRequest{RequestedModel: "mapped-upstream", ClientRequestedModel: "gpt-6.1-sol", RequestedReasoningEffort: "high", SchedulingPolicy: OpenAIEvalSchedulingPolicyAvoidDegradation}
	plan := s.buildOpenAIAccountLoadPlan(context.Background(), req, accounts, nil)
	require.Len(t, plan.candidates, 3)
	require.Equal(t, int64(2), plan.selectionOrder[0].account.ID)
	scores := openAIPlanScores(plan)
	require.Equal(t, scores[2], scores[3], "quality tiers are independent of operational scores")
	require.Equal(t, scores[3], scores[1])
	require.Len(t, plan.selectionOrder, 3, "low quality must remain available as overflow")
	trace := buildOpenAIAccountScheduleCandidates(plan, nil, req.ClientRequestedModel, req.RequestedReasoningEffort)
	require.Equal(t, 1, trace[1].EvaluatedCount)
	require.Equal(t, 1, trace[1].PassCount)
	require.Equal(t, OpenAIEvalQualityAssessmentBasis, trace[1].QualityBasis)
	require.Equal(t, 1.0, *trace[1].QualityRatio)
	require.Zero(t, trace[1].QualityContribution)
	require.True(t, trace[1].InTopK)
	require.Nil(t, trace[2].QualityRatio)
	require.Zero(t, trace[2].QualityContribution)
	payload, err := json.Marshal(trace[0])
	require.NoError(t, err)
	require.Contains(t, string(payload), `"quality_ratio":0`)
	require.Contains(t, string(payload), `"quality_contribution":0`)
	require.Contains(t, string(payload), `"suspected_pass_count":0`)
	for _, dimension := range [][2]string{{"gpt-6-sol", "high"}, {"gpt-6.1-sol", "low"}} {
		req.ClientRequestedModel, req.RequestedReasoningEffort = dimension[0], dimension[1]
		other := openAIPlanScores(s.buildOpenAIAccountLoadPlan(context.Background(), req, accounts, nil))
		require.Equal(t, other[1], other[2])
	}
}

func TestOpenAIQualitySchedulerEffectsOffDefaultStabilityUnchanged(t *testing.T) {
	enableQualityEffects(t)
	s := openAIResetTestScheduler(0)
	accounts := qualitySchedulerAccounts(t)
	for _, policy := range []string{OpenAIEvalSchedulingPolicyLegacy, OpenAIEvalSchedulingPolicyStabilityFirst, OpenAIEvalSchedulingPolicyCostFirst, OpenAIEvalSchedulingPolicyCustomBalance, OpenAIEvalSchedulingPolicyAvoidDegradation} {
		t.Run(policy, func(t *testing.T) {
			if policy == OpenAIEvalSchedulingPolicyAvoidDegradation {
				SetOpenAIEvalEffectsEnabled(false)
				defer SetOpenAIEvalEffectsEnabled(true)
			}
			req := OpenAIAccountScheduleRequest{RequestedModel: "gpt-6.1-sol", RequestedReasoningEffort: "high", SchedulingPolicy: policy}
			plan := s.buildOpenAIAccountLoadPlan(context.Background(), req, accounts, nil)
			scores := openAIPlanScores(plan)
			require.Equal(t, scores[1], scores[2])
			require.Equal(t, scores[2], scores[3])
			for _, c := range plan.candidates {
				require.Zero(t, c.qualityContribution)
			}
		})
	}
}

func TestOpenAIQualitySchedulerUsesFractionIncludingSuspectedPasses(t *testing.T) {
	enableQualityEffects(t)
	now := time.Now().UTC().Add(-time.Second)
	perfect := qualityTestAccount(t, qualityTestAggregate(now, 1, 1, 1, 0))
	candy := qualityTestAccount(t, qualityTestAggregate(now, 2, 10, 8, 0))
	fingerprint := qualityTestAggregate(now, 3, 400, 0, 400)
	fingerprint.TestType = OpenAIEvalTypeFingerprint
	accounts := []*Account{perfect, candy, qualityTestAccount(t, fingerprint)}
	installQualityTestSnapshot(t, accounts)
	s := openAIResetTestScheduler(0)
	plan := s.buildOpenAIAccountLoadPlan(context.Background(), OpenAIAccountScheduleRequest{
		RequestedModel: "gpt-6.1-sol", RequestedReasoningEffort: "high", SchedulingPolicy: OpenAIEvalSchedulingPolicyAvoidDegradation,
	}, accounts, nil)
	scores := openAIPlanScores(plan)
	require.Contains(t, []int64{1, 3}, plan.selectionOrder[0].account.ID, "a passed diagnostic and a suspected-normal diagnostic share the best tier")
	require.Equal(t, scores[2], scores[3], "quality tiers are separate from operational scores")
	trace := buildOpenAIAccountScheduleCandidates(plan, nil, "gpt-6.1-sol", "high")
	require.Equal(t, 1, trace[2].SuspectedPassCount)
	require.Zero(t, trace[2].PassCount)
	require.Equal(t, 1.0, *trace[2].QualityRatio)
	require.Zero(t, *trace[1].QualityRatio, "Candy's warning verdict counts as one failed diagnostic even with eight passing samples")
}

func TestOpenAIQualitySchedulerWeightedStickyAndEscape(t *testing.T) {
	enableQualityEffects(t)
	s := openAIResetTestScheduler(0)
	s.service.cfg.Gateway.OpenAIWS.LBTopK = 2
	accounts := qualitySchedulerAccounts(t)[:2]
	req := OpenAIAccountScheduleRequest{RequestedModel: "gpt-6.1-sol", RequestedReasoningEffort: "high", SchedulingPolicy: OpenAIEvalSchedulingPolicyAvoidDegradation, StickyWeighted: true, StickyAccountID: 1}
	selected := map[int64]int{}
	for i := 0; i < 200; i++ {
		req.SessionHash = fmt.Sprintf("quality-seed-%d", i)
		plan := s.buildOpenAIAccountLoadPlan(context.Background(), req, accounts, nil)
		selected[plan.selectionOrder[0].account.ID]++
	}
	require.Equal(t, 200, selected[2], "lower ratios cannot win TopK randomness")
	require.Zero(t, selected[1])
	req.RouteMigrationActive = true
	req.RouteMigrationRateMultiplier = 1
	plan := s.buildOpenAIAccountLoadPlan(context.Background(), req, accounts, nil)
	require.Equal(t, int64(2), plan.selectionOrder[0].account.ID, "escape ranking includes quality within a rate bucket")
}

func TestOpenAIQualitySchedulerProductionSessionAndOwner(t *testing.T) {
	enableQualityEffects(t)
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	ctx := context.Background()
	accounts := qualitySchedulerAccounts(t)[:2]
	for _, a := range accounts {
		a.Platform, a.Type, a.Status = PlatformOpenAI, AccountTypeAPIKey, StatusActive
		a.Schedulable, a.Concurrency = true, 1
		a.Extra["openai_apikey_responses_websockets_v2_enabled"] = true
	}
	cfg := newSchedulerTestOpenAIWSV2Config()
	cfg.Gateway.OpenAIWS.LBTopK = 1
	cfg.Gateway.OpenAIWS.SchedulerScoreWeights = config.GatewayOpenAIWSSchedulerScoreWeights{Load: 1, SessionSticky: 1}
	cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:quality-session": 1}}
	svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{*accounts[0], *accounts[1]}}, cache: cache, cfg: cfg,
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{})}
	s := newDefaultOpenAIAccountScheduler(svc, newOpenAIAccountRuntimeStats())
	req := OpenAIAccountScheduleRequest{Platform: PlatformOpenAI, RequestedModel: "gpt-6.1-sol", RequestedReasoningEffort: "high", SchedulingPolicy: OpenAIEvalSchedulingPolicyAvoidDegradation,
		SessionHash: "quality-session", RequiredTransport: OpenAIUpstreamTransportAny}
	selection, decision, err := s.Select(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, int64(2), selection.Account.ID)
	require.Equal(t, openAIAccountScheduleLayerLoadBalance, decision.Layer)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
	require.NoError(t, svc.getOpenAIWSStateStore().BindResponseAccount(ctx, 0, "resp_quality_owner", 1, time.Hour))
	req.PreviousResponseID = "resp_quality_owner"
	selection, decision, err = s.Select(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, int64(1), selection.Account.ID)
	require.Equal(t, openAIAccountScheduleLayerPreviousResponse, decision.Layer)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
	req.PreviousResponseCanMove = true
	selection, decision, err = s.Select(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, int64(2), selection.Account.ID)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
	// Explicit task-owner protection still keeps session affinity.
	req.PreviousResponseID = ""
	req.DisableStickyEscape, req.StickyAccountID = true, 1
	selection, _, err = s.Select(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, int64(1), selection.Account.ID)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestOpenAIQualitySchedulerProductionPolicyWithAdvancedDisabled(t *testing.T) {
	enableQualityEffects(t)
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	previousPolicy := openAIEvalSchedulingPolicy.Load()
	t.Cleanup(func() {
		openAIEvalSchedulingPolicy.Store(previousPolicy)
		resetOpenAIAdvancedSchedulerSettingCacheForTest()
	})
	SetOpenAIEvalSchedulingPolicySnapshot(&OpenAIEvalConfig{EffectsEnabled: true, SchedulingPolicy: OpenAIEvalSchedulingPolicyAvoidDegradation})
	accounts := qualitySchedulerAccounts(t)[:2]
	for _, account := range accounts {
		quality, _ := ReadOpenAIEvalQualityFromAccount(account, "gpt-6.1-sol", "high", time.Now())
		quality.ReasoningEffort = ""
		account.Extra = qualityTestAccount(t, quality).Extra
		account.Platform, account.Type, account.Status = PlatformOpenAI, AccountTypeAPIKey, StatusActive
		account.Schedulable, account.Concurrency = true, 1
	}
	installQualityTestSnapshot(t, accounts)
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.LBTopK = 1
	svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{*accounts[0], *accounts[1]}},
		cache: &schedulerTestGatewayCache{}, cfg: cfg, rateLimitService: newOpenAIAdvancedSchedulerRateLimitService("false"),
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{})}
	selection, decision, err := svc.SelectAccountWithScheduler(context.Background(), nil, "", "", "gpt-6.1-sol", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, int64(2), selection.Account.ID)
	require.Equal(t, OpenAIEvalSchedulingPolicyAvoidDegradation, decision.SchedulingPolicy)
	require.Len(t, decision.Candidates, 2)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestOpenAIQualitySchedulerTierExhaustionBeforeCheaperFallback(t *testing.T) {
	enableQualityEffects(t)
	accounts := qualitySchedulerAccounts(t)
	now := time.Now().Add(-time.Second)
	secondBest := qualityTestAccount(t, qualityTestAggregate(now, 4, 10, 10, 0))
	accounts = append(accounts, secondBest)
	installQualityTestSnapshot(t, accounts)
	for _, a := range accounts {
		a.Platform, a.Type, a.Status = PlatformOpenAI, AccountTypeAPIKey, StatusActive
		a.Schedulable, a.Concurrency = true, 1
	}
	accounts[0].Priority = -10000
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.LBTopK = 1
	cfg.Gateway.OpenAIWS.SchedulerScoreWeights.Priority = 10000
	for _, available := range []map[int64]bool{{1: true, 2: false, 3: true, 4: true}, {1: true, 2: false, 3: true, 4: false}, {1: false, 2: false, 3: true, 4: false}} {
		var acquired []int64
		svc := &OpenAIGatewayService{cfg: cfg, cache: &schedulerTestGatewayCache{}, accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{*accounts[0], *accounts[1], *accounts[2], *accounts[3]}}, concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: available, acquiredIDs: &acquired})}
		s := newDefaultOpenAIAccountScheduler(svc, newOpenAIAccountRuntimeStats())
		selection, _, err := s.Select(context.Background(), OpenAIAccountScheduleRequest{Platform: PlatformOpenAI, RequestedModel: "gpt-6.1-sol", RequestedReasoningEffort: "high", SchedulingPolicy: OpenAIEvalSchedulingPolicyAvoidDegradation})
		require.NoError(t, err)
		require.NotNil(t, selection)
		want := int64(3)
		if available[4] {
			want = 4
		} else if available[1] {
			want = 1
		}
		require.Equal(t, want, selection.Account.ID)
		if selection.ReleaseFunc != nil {
			selection.ReleaseFunc()
		}
	}
}

func TestOpenAIQualitySchedulerCustomIndependentWeightAndSticky(t *testing.T) {
	enableQualityEffects(t)
	previousPolicy := openAIEvalSchedulingPolicy.Load()
	t.Cleanup(func() { openAIEvalSchedulingPolicy.Store(previousPolicy) })
	SetOpenAIEvalSchedulingPolicySnapshot(&OpenAIEvalConfig{EffectsEnabled: true, SchedulingPolicy: OpenAIEvalSchedulingPolicyCustomBalance, CustomBalance: OpenAIEvalPolicyWeights{Quality: 1}})
	accounts := qualitySchedulerAccounts(t)
	s := openAIResetTestScheduler(0)
	s.service.cfg.Gateway.OpenAIWS.LBTopK = 1
	req := OpenAIAccountScheduleRequest{RequestedModel: "gpt-6.1-sol", RequestedReasoningEffort: "high", SchedulingPolicy: OpenAIEvalSchedulingPolicyCustomBalance, StickyAccountID: 1, StickyWeighted: true}
	plan := s.buildOpenAIAccountLoadPlan(context.Background(), req, accounts, nil)
	require.False(t, plan.qualityFirst)
	require.Equal(t, int64(2), plan.selectionOrder[0].account.ID)
	require.Equal(t, -5.0, plan.candidates[0].qualityContribution)
	require.Equal(t, 5.0, plan.candidates[1].qualityContribution)
	require.Zero(t, plan.candidates[2].qualityContribution)
	require.True(t, openAIQualityRankingEnabled(req))
	SetOpenAIEvalSchedulingPolicySnapshot(&OpenAIEvalConfig{EffectsEnabled: true, SchedulingPolicy: OpenAIEvalSchedulingPolicyCustomBalance, CustomBalance: OpenAIEvalPolicyWeights{Load: 1}})
	require.False(t, openAIQualityRankingEnabled(req))
}

func TestOpenAIQualitySchedulerEqualRatioUsesPrice(t *testing.T) {
	enableQualityEffects(t)
	now := time.Now().Add(-time.Minute)
	a := qualityTestAccount(t, qualityTestAggregate(now, 1, 2, 1, 0))
	b := qualityTestAccount(t, qualityTestAggregate(now, 2, 10, 5, 0))
	cheap, costly := .5, 2.0
	a.RateMultiplier, b.RateMultiplier = &costly, &cheap
	a.Platform, b.Platform = PlatformOpenAI, PlatformOpenAI
	a.Type, b.Type = AccountTypeAPIKey, AccountTypeAPIKey
	accounts := []*Account{a, b}
	installQualityTestSnapshot(t, accounts)
	s := openAIResetTestScheduler(0)
	s.service.cfg.Gateway.OpenAIWS.LBTopK = 1
	s.service.cfg.Gateway.OpenAIWS.SchedulerScoreWeights.UpstreamCost = 10
	plan := s.buildOpenAIAccountLoadPlan(context.Background(), OpenAIAccountScheduleRequest{RequestedModel: "gpt-6.1-sol", RequestedReasoningEffort: "high", SchedulingPolicy: OpenAIEvalSchedulingPolicyAvoidDegradation, UseUpstreamTokenCost: true}, accounts, nil)
	require.Len(t, openAIQualityTiers(plan.candidates), 1)
	require.Greater(t, plan.candidates[1].score, plan.candidates[0].score)
	require.Equal(t, int64(2), plan.selectionOrder[0].account.ID)
}
