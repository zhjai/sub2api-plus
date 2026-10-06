//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenAIAccountPriorityActualDispatch(t *testing.T) {
	for _, policy := range []string{"", OpenAIEvalSchedulingPolicyCostFirst, OpenAIEvalSchedulingPolicyStabilityFirst, OpenAIEvalSchedulingPolicyAvoidDegradation, OpenAIEvalSchedulingPolicyCustomBalance} {
		t.Run(policy, func(t *testing.T) {
			_, repo, _, gateway := rankingHarness(t)
			repo.config.SchedulingPolicy = policy
			repo.config.AccountPriorityRules = []OpenAIEvalAccountPriorityRule{{AccountID: 2, Priority: 1}}
			SetOpenAIEvalSchedulingPolicySnapshot(&repo.config)
			gateway.cache = &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:priority-session": 1}}
			selection, decision, err := gateway.SelectAccountWithScheduler(context.Background(), rankingPtr(int64(7)), "", "priority-session", "gpt-6.1-sol", nil, OpenAIUpstreamTransportAny, false)
			require.NoError(t, err)
			require.Equal(t, int64(2), selection.Account.ID, "rule precedes cheaper account and ordinary sticky")
			selection.ReleaseFunc()
			require.Equal(t, "account_priority_rule", decision.ReasonCode)
		})
	}
}

func TestOpenAIAccountPriorityConditionalActualDispatch(t *testing.T) {
	for _, tc := range []struct {
		name      string
		condition OpenAIEvalAccountPriorityCondition
		quality   bool
		runtime   bool
		want      int64
	}{
		{"known price", OpenAIEvalAccountPriorityCondition{Metric: "price", Operator: "gte", Threshold: 3}, false, false, 2},
		{"unmatched price", OpenAIEvalAccountPriorityCondition{Metric: "price", Operator: "lt", Threshold: 3}, false, false, 1},
		{"known quality", OpenAIEvalAccountPriorityCondition{Metric: "quality_ratio", Operator: "gte", Threshold: 1}, true, false, 2},
		{"unknown quality", OpenAIEvalAccountPriorityCondition{Metric: "quality_ratio", Operator: "gte", Threshold: 0}, false, false, 1},
		{"known errors", OpenAIEvalAccountPriorityCondition{Metric: "error_rate", Operator: "lt", Threshold: .05}, false, true, 2},
		{"known latency", OpenAIEvalAccountPriorityCondition{Metric: "ttft_ms", Operator: "lte", Threshold: 8000}, false, true, 2},
		{"unknown errors", OpenAIEvalAccountPriorityCondition{Metric: "error_rate", Operator: "lt", Threshold: .05}, false, false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			eval, repo, _, gateway := rankingHarness(t)
			repo.config.AccountPriorityRules = []OpenAIEvalAccountPriorityRule{{AccountID: 2, Priority: 0, Condition: &tc.condition}}
			if tc.quality {
				// rankingHarness has already adopted revision 1. Bump the
				// repository revision so the changed evidence is visible to the
				// ranking service during this request-local dispatch test.
				repo.config.Revision++
				repo.config.Accounts = []OpenAIEvalAccountConfig{{AccountID: 2, RequestedModel: "gpt-6.1-sol", CandySchedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300, SampleCount: 1}}}
				repo.runs = []OpenAIEvalRun{{ID: 1, AccountID: 2, RequestedModel: "gpt-6.1-sol", TestType: OpenAIEvalTypeCandy, TriggerSource: "manual", DataVersion: OpenAIEvalDataVersion, Status: "pass", FinishedAt: time.Now(), Samples: []OpenAIEvalSampleRecord{{Valid: true, Answer: "21"}}}}
			}
			require.NoError(t, eval.Initialize(context.Background()))
			if tc.runtime {
				gateway.persistentOpenAIAccountScheduler().ReportResultForRequest(2, "gpt-6.1-sol", "", true, rankingPtr(7000))
			}
			selection, _, err := gateway.SelectAccountWithScheduler(context.Background(), rankingPtr(int64(7)), "", "", "gpt-6.1-sol", nil, OpenAIUpstreamTransportAny, false)
			require.NoError(t, err)
			require.Equal(t, tc.want, selection.Account.ID)
			selection.ReleaseFunc()
			// A request-local resolution never overwrites shared conditional rules.
			require.NotNil(t, openAIEvalSchedulingPolicy.Load().(*openAIEvalSchedulingPolicySnapshot).AccountPriorities[2].allModelsCondition)
		})
	}
}

func TestOpenAIAccountPriorityEvidenceRevisionRaceFallsBackToOrdinaryScheduling(t *testing.T) {
	eval, repo, _, gateway := rankingHarness(t)
	repo.config.AccountPriorityRules = []OpenAIEvalAccountPriorityRule{{
		AccountID: 2, Priority: 0,
		Condition: &OpenAIEvalAccountPriorityCondition{Metric: "quality_ratio", Operator: "gte", Threshold: 1},
	}}
	repo.config.Revision++
	repo.config.Accounts = []OpenAIEvalAccountConfig{{AccountID: 2, RequestedModel: "gpt-6.1-sol", CandySchedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300, SampleCount: 1}}}
	repo.runs = []OpenAIEvalRun{{ID: 1, AccountID: 2, RequestedModel: "gpt-6.1-sol", TestType: OpenAIEvalTypeCandy, TriggerSource: "manual", DataVersion: OpenAIEvalDataVersion, Status: "pass", FinishedAt: time.Now(), Samples: []OpenAIEvalSampleRecord{{Valid: true, Answer: "21"}}}}
	repo.beforeLatest = func() {
		gateway.evalRanking.mu.Lock()
		gateway.evalRanking.config.Revision++
		gateway.evalRanking.mu.Unlock()
	}
	require.NoError(t, eval.Initialize(context.Background()))

	selection, decision, err := gateway.SelectAccountWithScheduler(context.Background(), rankingPtr(int64(7)), "", "", "gpt-6.1-sol", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, int64(1), selection.Account.ID, "unknown conditional evidence falls back to ordinary policy ordering")
	require.Equal(t, "account_priority_condition_fallback", decision.ReasonCode)
	require.Equal(t, "account_priority_condition_config_changed", *decision.RankingFallbackReason)
	selection.ReleaseFunc()
}

func TestOpenAIAccountPriorityRevisionRacePreservesUnconditionalFallback(t *testing.T) {
	eval, repo, _, gateway := rankingHarness(t)
	repo.config.Accounts = []OpenAIEvalAccountConfig{{AccountID: 2, RequestedModel: "gpt-6.1-sol", CandySchedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300, SampleCount: 1}}}
	repo.config.AccountPriorityRules = []OpenAIEvalAccountPriorityRule{
		{AccountID: 2, Priority: 1},
		{AccountID: 2, Priority: 0, RequestedModels: []string{"gpt-6.1-sol"}, Condition: &OpenAIEvalAccountPriorityCondition{Metric: "quality_ratio", Operator: "gte", Threshold: 1}},
	}
	repo.config.Revision++
	require.NoError(t, eval.Initialize(context.Background()))
	repo.beforeLatest = func() {
		gateway.evalRanking.mu.Lock()
		gateway.evalRanking.config.Revision++
		gateway.evalRanking.mu.Unlock()
	}
	selection, decision, err := gateway.SelectAccountWithScheduler(context.Background(), rankingPtr(int64(7)), "", "", "gpt-6.1-sol", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	t.Cleanup(selection.ReleaseFunc)
	require.Equal(t, int64(2), selection.Account.ID)
	require.Equal(t, "account_priority_rule", decision.ReasonCode)
	require.Equal(t, "account_priority_condition_config_changed", *decision.RankingFallbackReason)
	traces := gateway.persistentOpenAIAccountScheduler().(*defaultOpenAIAccountScheduler).RecentScheduleTraces(1)
	require.Len(t, traces, 1)
	require.Equal(t, decision.RankingFallbackReason, traces[0].RankingFallbackReason)
}

func TestOpenAIAccountPriorityPolicyRevisionRaceKeepsDispatchAvailable(t *testing.T) {
	eval, repo, _, gateway := rankingHarness(t)
	repo.config.Accounts = []OpenAIEvalAccountConfig{{AccountID: 2, RequestedModel: "gpt-6.1-sol", CandySchedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300, SampleCount: 1}}}
	repo.config.Revision++
	require.NoError(t, eval.Initialize(context.Background()))
	repo.beforeLatest = func() {
		gateway.evalRanking.mu.Lock()
		gateway.evalRanking.config.Revision++
		gateway.evalRanking.mu.Unlock()
	}
	selection, decision, err := gateway.SelectAccountWithScheduler(context.Background(), rankingPtr(int64(7)), "", "", "gpt-6.1-sol", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	t.Cleanup(selection.ReleaseFunc)
	require.Equal(t, int64(1), selection.Account.ID)
	require.Equal(t, "live_fallback", decision.RankingBasis)
	require.Equal(t, "config_revision_changed", *decision.RankingFallbackReason)
}

func TestOpenAIAccountPriorityTracePreservesPoolScores(t *testing.T) {
	_, repo, accounts, gateway := rankingHarness(t)
	repo.config.SchedulingPolicy = ""
	repo.config.AccountPriorityRules = []OpenAIEvalAccountPriorityRule{{AccountID: 2, Priority: 0}}
	SetOpenAIEvalSchedulingPolicySnapshot(&repo.config)
	accounts.items[0].Priority = 0
	accounts.items[1].Priority = 100
	gateway.cfg.Gateway.OpenAIWS.SchedulerScoreWeights.Priority = 2
	scheduler := gateway.persistentOpenAIAccountScheduler().(*defaultOpenAIAccountScheduler)
	req := OpenAIAccountScheduleRequest{GroupID: rankingPtr(int64(7)), RequestedModel: "gpt-6.1-sol", Platform: PlatformOpenAI, RequiredTransport: OpenAIUpstreamTransportAny}
	pool := []*Account{&accounts.items[0], &accounts.items[1]}
	baseline := buildOpenAIAccountScheduleCandidates(scheduler.buildOpenAIAccountLoadPlan(context.Background(), req, pool, nil), nil)
	require.Len(t, baseline, 2)
	require.NotEqual(t, baseline[0].Score, baseline[1].Score, "fixture must distinguish pool normalization from one-account layers")
	selection, decision, err := scheduler.Select(context.Background(), req)
	require.NoError(t, err)
	require.True(t, selection.Acquired)
	t.Cleanup(selection.ReleaseFunc)
	require.Equal(t, int64(2), selection.Account.ID, "account rule still precedes the policy score")
	scores := make(map[int64]float64)
	for _, candidate := range baseline {
		scores[candidate.AccountID] = candidate.Score
	}
	for _, candidate := range decision.Candidates {
		require.InDelta(t, scores[candidate.AccountID], candidate.Score, 1e-12)
	}
	traces := scheduler.RecentScheduleTraces(1)
	require.Len(t, traces, 1)
	for _, candidate := range traces[0].Candidates {
		require.InDelta(t, scores[candidate.AccountID], candidate.Score, 1e-12)
	}
}

func TestOpenAIAccountPriorityPublicLoadAwareEntryKeepsUnrelatedLegacyPath(t *testing.T) {
	for _, scope := range []string{"none", "other_group", "other_model", "matching"} {
		t.Run(scope, func(t *testing.T) {
			_, repo, accounts, gateway := rankingHarness(t)
			repo.config.SchedulingPolicy = ""
			gateway.cache = &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:public-entry": 1}}
			switch scope {
			case "other_group":
				third := accounts.items[1]
				third.ID, third.GroupIDs = 3, []int64{8}
				accounts.items = append(accounts.items, third)
				repo.config.AccountPriorityRules = []OpenAIEvalAccountPriorityRule{{AccountID: 3, Priority: 0}}
			case "other_model":
				repo.config.AccountPriorityRules = []OpenAIEvalAccountPriorityRule{{AccountID: 2, Priority: 0, RequestedModels: []string{"other-model"}}}
			case "matching":
				repo.config.AccountPriorityRules = []OpenAIEvalAccountPriorityRule{{AccountID: 2, Priority: 0}}
			}
			require.NoError(t, SetOpenAIEvalSchedulingPolicySnapshot(&repo.config))
			legacy, err := gateway.selectAccountWithLoadAwareness(context.Background(), rankingPtr(int64(7)), PlatformOpenAI, "public-entry", "gpt-6.1-sol", nil, false, "", true)
			require.NoError(t, err)
			require.Equal(t, int64(1), legacy.Account.ID)
			if legacy.ReleaseFunc != nil {
				legacy.ReleaseFunc()
			}
			selected, err := gateway.SelectAccountWithLoadAwareness(context.Background(), rankingPtr(int64(7)), "public-entry", "gpt-6.1-sol", nil)
			require.NoError(t, err)
			if selected.ReleaseFunc != nil {
				t.Cleanup(selected.ReleaseFunc)
			}
			if scope == "matching" {
				require.Equal(t, int64(2), selected.Account.ID)
			} else {
				require.Equal(t, legacy.Account.ID, selected.Account.ID)
				require.Equal(t, legacy.stickySessionHit, selected.stickySessionHit)
				require.Equal(t, legacy.Acquired, selected.Acquired)
				require.Equal(t, legacy.WaitPlan, selected.WaitPlan)
				scheduler := gateway.persistentOpenAIAccountScheduler().(*defaultOpenAIAccountScheduler)
				traces := scheduler.RecentScheduleTraces(1)
				require.Len(t, traces, 1, "legacy dispatch is observable without activating advanced selection")
				require.Equal(t, "legacy", traces[0].RankingBasis)
				require.Equal(t, legacy.Account.ID, traces[0].SelectedAccountID)
				require.Equal(t, legacy.stickySessionHit, traces[0].StickySessionHit)
				require.Nil(t, traces[0].Candidates[0].AccountRulePriority, "unrelated rules must not affect legacy ordering")
			}
		})
	}
}

func TestOpenAIAccountPriorityTraceDistinguishesWaitingFromAdmission(t *testing.T) {
	for _, acquired := range []bool{false, true} {
		t.Run(map[bool]string{false: "waiting", true: "acquired"}[acquired], func(t *testing.T) {
			_, repo, _, gateway := rankingHarness(t)
			repo.config.SchedulingPolicy = ""
			repo.config.AccountPriorityRules = []OpenAIEvalAccountPriorityRule{{AccountID: 2, Priority: 0}}
			SetOpenAIEvalSchedulingPolicySnapshot(&repo.config)
			gateway.concurrencyService = NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: map[int64]bool{1: acquired, 2: acquired}})
			scheduler := gateway.persistentOpenAIAccountScheduler().(*defaultOpenAIAccountScheduler)
			selection, decision, err := scheduler.Select(context.Background(), OpenAIAccountScheduleRequest{GroupID: rankingPtr(int64(7)), RequestedModel: "gpt-6.1-sol", Platform: PlatformOpenAI, RequiredTransport: OpenAIUpstreamTransportAny})
			require.NoError(t, err)
			require.NotNil(t, selection)
			require.Equal(t, int64(2), selection.Account.ID, "waiting and admission must retain the highest rule layer")
			if selection.ReleaseFunc != nil {
				t.Cleanup(selection.ReleaseFunc)
			}
			require.Equal(t, acquired, decision.Acquired)
			require.Equal(t, !acquired, decision.WaitPlan)
			if !acquired {
				require.NotNil(t, selection.WaitPlan)
				require.Equal(t, "account_admission_pending", decision.ReasonCode)
			}
			traces := scheduler.RecentScheduleTraces(1)
			require.Len(t, traces, 1)
			require.Equal(t, acquired, traces[0].Acquired)
			require.Equal(t, !acquired, traces[0].WaitPlan)
			matched := false
			for _, candidate := range traces[0].Candidates {
				if candidate.AccountID == selection.Account.ID {
					matched = true
					require.Equal(t, acquired, candidate.Selected)
					require.Equal(t, !acquired, candidate.AwaitingAdmission)
				} else {
					require.False(t, candidate.Selected)
					require.False(t, candidate.AwaitingAdmission)
				}
			}
			require.True(t, matched)
		})
	}
}

func TestOpenAIAccountPriorityActivationUsesPersistentGrokQuota(t *testing.T) {
	_, repo, accounts, gateway := rankingHarness(t)
	repo.config.SchedulingPolicy = ""
	repo.config.AccountPriorityRules = []OpenAIEvalAccountPriorityRule{{AccountID: 2, Priority: 0}}
	SetOpenAIEvalSchedulingPolicySnapshot(&repo.config)
	for i := range accounts.items {
		accounts.items[i].Platform = PlatformGrok
	}
	accounts.items[1].Type = AccountTypeOAuth
	accounts.items[1].Credentials = map[string]any{"subscription_tier": "free"}
	gateway.cfg = grokFreeQuotaTestConfig()
	gateway.usageLogRepo = &grokFreeQuotaUsageRepoStub{}
	scheduler := gateway.persistentOpenAIAccountScheduler().(*defaultOpenAIAccountScheduler)
	scheduler.grokFreeQuotaGateCache.Store(int64(2), grokFreeQuotaGateCacheEntry{tokens: 500000, known: true, checkedAt: time.Now()})
	selection, decision, err := gateway.selectAccountWithScheduler(context.Background(), rankingPtr(int64(7)), "", "", "grok-4.6", nil, OpenAIUpstreamTransportAny, "", "", false, PlatformGrok, false, false)
	require.NoError(t, err)
	require.Equal(t, int64(1), selection.Account.ID)
	require.NotEqual(t, "account_priority_fallback", decision.ReasonCode)
	selection.ReleaseFunc()
}

func TestOpenAIAccountPriorityActivationIgnoresGrokModelBlocks(t *testing.T) {
	for _, gate := range []string{"team_rate_limit", "model_quota"} {
		t.Run(gate, func(t *testing.T) {
			_, repo, accounts, gateway := rankingHarness(t)
			repo.config.SchedulingPolicy = ""
			for i := range accounts.items {
				accounts.items[i].ID += 810000
				accounts.items[i].Platform = PlatformGrok
				accounts.items[i].Type = AccountTypeOAuth
				accounts.items[i].Credentials = map[string]any{"subscription_tier": "pro", "team_id": t.Name()}
			}
			accounts.items[0].Credentials["team_id"] = "healthy-" + t.Name()
			repo.config.AccountPriorityRules = []OpenAIEvalAccountPriorityRule{{AccountID: accounts.items[1].ID, Priority: 0}}
			require.NoError(t, SetOpenAIEvalSchedulingPolicySnapshot(&repo.config))
			scheduler := gateway.persistentOpenAIAccountScheduler().(*defaultOpenAIAccountScheduler)
			until := time.Now().Add(time.Minute)
			if gate == "team_rate_limit" {
				markGrokTeamModelRateLimit(&accounts.items[1], "grok-4.6", until)
			} else {
				markGrokModelQuotaBlock(accounts.items[1].ID, "grok-4.6", until)
			}
			req := OpenAIAccountScheduleRequest{GroupID: rankingPtr(int64(7)), Platform: PlatformGrok, RequestedModel: "grok-4.6", accountPriorityIndex: openAIEvalSchedulingPolicy.Load().(*openAIEvalSchedulingPolicySnapshot).AccountPriorities}
			require.NoError(t, scheduler.resolveAccountPriorityRules(context.Background(), &req))
			require.False(t, openAIAccountPriorityRulesActive(req), "a hard-blocked rule must not change the legacy routing path")
		})
	}
}

func TestOpenAIAccountPriorityPublicEntryHonorsPrivacyBeforeActivation(t *testing.T) {
	_, repo, accounts, gateway := rankingHarness(t)
	repo.config.SchedulingPolicy = ""
	repo.config.AccountPriorityRules = []OpenAIEvalAccountPriorityRule{{AccountID: 2, Priority: 0}}
	require.NoError(t, SetOpenAIEvalSchedulingPolicySnapshot(&repo.config))
	for i := range accounts.items {
		accounts.items[i].Type = AccountTypeOAuth
	}
	accounts.items[0].Extra = map[string]any{"privacy_mode": PrivacyModeTrainingOff}
	gateway.cache = &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:privacy-entry": 1}}
	gateway.schedulerSnapshot = &SchedulerSnapshotService{accountRepo: accounts, groupRepo: guardianAffinityGroupRepo{group: &Group{ID: 7, RequirePrivacySet: true}}}
	selection, err := gateway.SelectAccountWithLoadAwareness(context.Background(), rankingPtr(int64(7)), "privacy-entry", "gpt-6.1-sol", nil)
	require.NoError(t, err)
	require.Equal(t, int64(1), selection.Account.ID)
	if selection.ReleaseFunc != nil {
		t.Cleanup(selection.ReleaseFunc)
	}
	scheduler := gateway.persistentOpenAIAccountScheduler().(*defaultOpenAIAccountScheduler)
	traces := scheduler.RecentScheduleTraces(1)
	require.Len(t, traces, 1)
	require.Equal(t, "legacy", traces[0].RankingBasis, "privacy-ineligible rules must not activate advanced selection")
	require.Equal(t, int64(1), traces[0].SelectedAccountID)
	require.Nil(t, traces[0].Candidates[0].AccountRulePriority)
}

func TestOpenAIAccountPriorityCapacityAndHardGate(t *testing.T) {
	for _, gate := range []string{"capacity", "excluded", "disabled", "other_group"} {
		t.Run(gate, func(t *testing.T) {
			_, repo, accounts, gateway := rankingHarness(t)
			repo.config.AccountPriorityRules = []OpenAIEvalAccountPriorityRule{{AccountID: 2, Priority: -5}}
			SetOpenAIEvalSchedulingPolicySnapshot(&repo.config)
			var excluded map[int64]struct{}
			switch gate {
			case "capacity":
				gateway.concurrencyService = NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: map[int64]bool{2: false}})
			case "excluded":
				excluded = map[int64]struct{}{2: {}}
			case "disabled":
				accounts.items[1].Schedulable = false
			case "other_group":
				accounts.items[1].GroupIDs = []int64{8}
			}
			selection, _, err := gateway.SelectAccountWithScheduler(context.Background(), rankingPtr(int64(7)), "", "", "gpt-6.1-sol", excluded, OpenAIUpstreamTransportAny, false)
			require.NoError(t, err)
			require.Equal(t, int64(1), selection.Account.ID)
			selection.ReleaseFunc()
		})
	}
}

func TestOpenAIAccountPriorityPublicModelAndHotToggle(t *testing.T) {
	_, repo, _, gateway := rankingHarness(t)
	disabled := false
	repo.config.AccountPriorityRules = []OpenAIEvalAccountPriorityRule{
		{AccountID: 1, Priority: 0},
		{AccountID: 1, Priority: 10, RequestedModels: []string{"public-model"}},
		{AccountID: 2, Priority: 5, RequestedModels: []string{"public-model"}},
	}
	SetOpenAIEvalSchedulingPolicySnapshot(&repo.config)
	ctx := WithOpenAIClientRequestedModel(context.Background(), "public-model")
	selection, decision, err := gateway.SelectAccountWithScheduler(ctx, rankingPtr(int64(7)), "", "", "gpt-6.1-sol", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.Equal(t, int64(2), selection.Account.ID, "specific public-model rule overrides all-model rule")
	selection.ReleaseFunc()
	for _, candidate := range decision.Candidates {
		require.NotNil(t, candidate.AccountRulePriority)
	}
	repo.config.AccountPriorityRules[2].Enabled = &disabled
	repo.config.Revision++
	SetOpenAIEvalSchedulingPolicySnapshot(&repo.config)
	selection, _, err = gateway.SelectAccountWithScheduler(ctx, rankingPtr(int64(7)), "", "", "gpt-6.1-sol", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.Equal(t, int64(1), selection.Account.ID)
	selection.ReleaseFunc()
}

func TestOpenAIAccountPriorityRequiredOwnerUnchanged(t *testing.T) {
	_, repo, _, gateway := rankingHarness(t)
	repo.config.AccountPriorityRules = []OpenAIEvalAccountPriorityRule{{AccountID: 2, Priority: 0}}
	SetOpenAIEvalSchedulingPolicySnapshot(&repo.config)
	store := gateway.getOpenAIWSStateStore()
	require.NoError(t, store.BindResponseAccount(context.Background(), int64(7), "resp_rule_owner", 1, time.Hour))
	selection, decision, err := gateway.SelectAccountWithScheduler(context.Background(), rankingPtr(int64(7)), "resp_rule_owner", "", "gpt-6.1-sol", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.Equal(t, int64(1), selection.Account.ID)
	require.Equal(t, openAIAccountScheduleLayerPreviousResponse, decision.Layer)
	selection.ReleaseFunc()
}

func TestOpenAIAccountPrioritySameLayerAffinityAndOverflow(t *testing.T) {
	_, repo, accounts, gateway := rankingHarness(t)
	repo.config.SchedulingPolicy = ""
	third := accounts.items[0]
	third.ID, third.Name = 3, "three"
	accounts.items = append(accounts.items, third)
	repo.config.AccountPriorityRules = []OpenAIEvalAccountPriorityRule{
		{AccountID: 1, Priority: 1}, {AccountID: 2, Priority: 1}, {AccountID: 3, Priority: 2},
	}
	SetOpenAIEvalSchedulingPolicySnapshot(&repo.config)
	gateway.cache = &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:same-layer": 2}}
	selection, _, err := gateway.SelectAccountWithScheduler(context.Background(), rankingPtr(int64(7)), "", "same-layer", "gpt-6.1-sol", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.Equal(t, int64(2), selection.Account.ID, "ordinary affinity is retained inside the best layer")
	selection.ReleaseFunc()
	gateway.concurrencyService = NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: map[int64]bool{2: false}})
	selection, _, err = gateway.SelectAccountWithScheduler(context.Background(), rankingPtr(int64(7)), "", "same-layer", "gpt-6.1-sol", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.Equal(t, int64(1), selection.Account.ID, "exhaust same-layer candidates before moving lower")
	selection.ReleaseFunc()
}

func TestOpenAIAccountPriorityMovableResponseDoesNotPinOldOwner(t *testing.T) {
	_, repo, _, gateway := rankingHarness(t)
	repo.config.SchedulingPolicy = ""
	repo.config.AccountPriorityRules = []OpenAIEvalAccountPriorityRule{{AccountID: 2, Priority: 0}}
	SetOpenAIEvalSchedulingPolicySnapshot(&repo.config)
	require.NoError(t, gateway.getOpenAIWSStateStore().BindResponseAccount(context.Background(), int64(7), "resp_movable_priority", 1, time.Hour))
	selection, _, err := gateway.selectAccountWithScheduler(context.Background(), rankingPtr(int64(7)), "resp_movable_priority", "", "gpt-6.1-sol", nil, OpenAIUpstreamTransportAny, "", "", false, PlatformOpenAI, true, false)
	require.NoError(t, err)
	require.Equal(t, int64(2), selection.Account.ID)
	selection.ReleaseFunc()
}

func TestOpenAIAccountPriorityExplicitPolicyStillOrdersWithinLayer(t *testing.T) {
	_, repo, _, gateway := rankingHarness(t)
	repo.config.AccountPriorityRules = []OpenAIEvalAccountPriorityRule{{AccountID: 1, Priority: 0}, {AccountID: 2, Priority: 0}}
	SetOpenAIEvalSchedulingPolicySnapshot(&repo.config)
	selection, decision, err := gateway.SelectAccountWithScheduler(context.Background(), rankingPtr(int64(7)), "", "", "gpt-6.1-sol", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.Equal(t, int64(1), selection.Account.ID, "cost-first policy remains intact within a rule layer")
	selection.ReleaseFunc()
	require.Len(t, decision.Candidates, 2)
	for _, candidate := range decision.Candidates {
		require.NotNil(t, candidate.AccountRulePriority)
		require.NotNil(t, candidate.PriorityScore, "rule ordering must not replace policy score")
	}
}

func TestOpenAIAccountPriorityOtherGroupKeepsLegacySticky(t *testing.T) {
	_, repo, accounts, gateway := rankingHarness(t)
	repo.config.SchedulingPolicy = ""
	other := accounts.items[0]
	other.ID, other.GroupIDs = 3, []int64{8}
	accounts.items = append(accounts.items, other)
	repo.config.AccountPriorityRules = []OpenAIEvalAccountPriorityRule{{AccountID: 3, Priority: 0}}
	SetOpenAIEvalSchedulingPolicySnapshot(&repo.config)
	gateway.cache = &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:untouched": 2}}
	selection, decision, err := gateway.SelectAccountWithScheduler(context.Background(), rankingPtr(int64(7)), "", "untouched", "gpt-6.1-sol", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.Equal(t, int64(2), selection.Account.ID)
	require.Equal(t, openAIAccountScheduleLayerSessionSticky, decision.Layer)
	selection.ReleaseFunc()
}

func TestOpenAIAccountPriorityLargeBusyLayerReachesFreeLowerLayer(t *testing.T) {
	for _, policy := range []string{"", OpenAIEvalSchedulingPolicyCostFirst} {
		t.Run(policy, func(t *testing.T) {
			_, repo, accounts, gateway := rankingHarness(t)
			repo.config.SchedulingPolicy = policy
			base := accounts.items[0]
			accounts.items = nil
			busy := make(map[int64]bool)
			for id := int64(1); id <= 70; id++ {
				account := base
				account.ID = id
				accounts.items = append(accounts.items, account)
				repo.config.AccountPriorityRules = append(repo.config.AccountPriorityRules, OpenAIEvalAccountPriorityRule{AccountID: id, Priority: 0})
				busy[id] = false
			}
			base.ID = 71
			accounts.items = append(accounts.items, base)
			SetOpenAIEvalSchedulingPolicySnapshot(&repo.config)
			gateway.concurrencyService = NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: busy})
			selection, decision, err := gateway.SelectAccountWithScheduler(context.Background(), rankingPtr(int64(7)), "", "", "gpt-6.1-sol", nil, OpenAIUpstreamTransportAny, false)
			require.NoError(t, err)
			require.Equal(t, int64(71), selection.Account.ID)
			require.Equal(t, "account_priority_fallback", decision.ReasonCode)
			require.True(t, decision.CandidatesTruncated)
			scheduler := gateway.persistentOpenAIAccountScheduler().(*defaultOpenAIAccountScheduler)
			traces := scheduler.RecentScheduleTraces(1)
			require.Len(t, traces, 1)
			require.True(t, traces[0].CandidatesTruncated)
			require.LessOrEqual(t, len(traces[0].Candidates), openAIAccountScheduleCandidateLimit)
			found := false
			for _, candidate := range traces[0].Candidates {
				if candidate.AccountID == 71 {
					found = true
					require.True(t, candidate.Selected)
				}
			}
			require.True(t, found, "trace truncation must preserve the admitted lower-layer account")
			selection.ReleaseFunc()
		})
	}
}

func TestOpenAIAccountPriorityMigrationUsesRuleLayerBeforeRateLadder(t *testing.T) {
	for _, policy := range []string{"", OpenAIEvalSchedulingPolicyCostFirst} {
		t.Run(policy, func(t *testing.T) {
			_, repo, accounts, gateway := rankingHarness(t)
			repo.config.SchedulingPolicy = policy
			repo.config.AccountPriorityRules = []OpenAIEvalAccountPriorityRule{{AccountID: 2, Priority: 0}}
			accounts.items[0].RateMultiplier = rankingPtr(0.08)
			accounts.items[1].RateMultiplier = rankingPtr(0.12)
			require.NoError(t, SetOpenAIEvalSchedulingPolicySnapshot(&repo.config))
			ctx := WithOpenAIRouteMigration(context.Background(), 0.06, "gpt-6.1-sol", "")
			selection, _, err := gateway.SelectAccountWithScheduler(ctx, rankingPtr(int64(7)), "", "", "gpt-6.1-sol", nil, OpenAIUpstreamTransportAny, false)
			require.NoError(t, err)
			require.Equal(t, int64(2), selection.Account.ID)
			selection.ReleaseFunc()
			gateway.concurrencyService = NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: map[int64]bool{2: false}})
			selection, _, err = gateway.SelectAccountWithScheduler(ctx, rankingPtr(int64(7)), "", "", "gpt-6.1-sol", nil, OpenAIUpstreamTransportAny, false)
			require.NoError(t, err)
			require.Equal(t, int64(1), selection.Account.ID, "busy rule layer must not prevent healthy lower-layer migration")
			selection.ReleaseFunc()
		})
	}
}

func TestOpenAIAccountPrioritySameLayerSubscriptionAndHealth(t *testing.T) {
	t.Run("subscription", func(t *testing.T) {
		_, repo, accounts, gateway := rankingHarness(t)
		repo.config.SchedulingPolicy = ""
		repo.config.AccountPriorityRules = []OpenAIEvalAccountPriorityRule{{AccountID: 1, Priority: 0}, {AccountID: 2, Priority: 0}}
		SetOpenAIEvalSchedulingPolicySnapshot(&repo.config)
		accounts.items[1].Type = AccountTypeOAuth
		accounts.items[1].Credentials = map[string]any{"plan_type": "plus"}
		scheduler := gateway.persistentOpenAIAccountScheduler()
		selection, _, err := scheduler.Select(context.Background(), OpenAIAccountScheduleRequest{GroupID: rankingPtr(int64(7)), Platform: PlatformOpenAI, RequestedModel: "gpt-6.1-sol", SubscriptionPriority: true})
		require.NoError(t, err)
		require.Equal(t, int64(2), selection.Account.ID)
		selection.ReleaseFunc()
	})
	t.Run("sticky health escape", func(t *testing.T) {
		_, repo, _, gateway := rankingHarness(t)
		repo.config.SchedulingPolicy = ""
		repo.config.AccountPriorityRules = []OpenAIEvalAccountPriorityRule{{AccountID: 1, Priority: 0}, {AccountID: 2, Priority: 0}}
		SetOpenAIEvalSchedulingPolicySnapshot(&repo.config)
		gateway.cache = &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:bad-sticky": 2}}
		// The harness intentionally has zero scheduler weights. Give this
		// health test a deterministic healthy candidate instead of asserting
		// that escaping affinity bans a still-eligible account from TopK.
		gateway.cfg.Gateway.OpenAIWS.SchedulerScoreWeights.ErrorRate = 1
		gateway.cfg.Gateway.OpenAIWS.LBTopK = 1
		scheduler := gateway.persistentOpenAIAccountScheduler().(*defaultOpenAIAccountScheduler)
		for i := 0; i < 30; i++ {
			scheduler.ReportResultForRequest(2, "gpt-6.1-sol", "", false, rankingPtr(60000))
		}
		_, _, _, escape := scheduler.shouldEscapeStickyAccountForRequest(2, "gpt-6.1-sol", "", gateway.openAIStickyEscapeConfig())
		require.True(t, escape)
		selection, _, err := gateway.SelectAccountWithScheduler(context.Background(), rankingPtr(int64(7)), "", "bad-sticky", "gpt-6.1-sol", nil, OpenAIUpstreamTransportAny, false)
		require.NoError(t, err)
		require.Equal(t, int64(1), selection.Account.ID)
		selection.ReleaseFunc()
	})
}
