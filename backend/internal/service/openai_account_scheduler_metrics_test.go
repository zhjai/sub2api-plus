package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestOpenAIAccountScheduleTraceStoreIsBoundedAndNewestFirst(t *testing.T) {
	store := &openAIAccountScheduleTraceStore{}
	for i := 0; i < openAIAccountScheduleTraceCapacity+3; i++ {
		store.append(OpenAIAccountScheduleTrace{SelectedAccountID: int64(i)})
	}

	traces := store.recent(2)
	require.Len(t, traces, 2)
	require.Equal(t, int64(openAIAccountScheduleTraceCapacity+2), traces[0].SelectedAccountID)
	require.Equal(t, int64(openAIAccountScheduleTraceCapacity+1), traces[1].SelectedAccountID)

	all := store.recent(0)
	require.Len(t, all, openAIAccountScheduleTraceCapacity)
	require.Equal(t, int64(openAIAccountScheduleTraceCapacity+2), all[0].SelectedAccountID)
	require.Equal(t, int64(3), all[len(all)-1].SelectedAccountID)
}

func TestOpenAIAccountRuntimeStatsAreIsolatedByModelAndReasoningEffort(t *testing.T) {
	stats := newOpenAIAccountRuntimeStats()
	stats.reportForRequest(7, "gpt-6-astra", "high", false, nil)
	stats.reportForRequest(7, "gpt-6-astra", "low", true, nil)
	stats.reportForRequest(7, "gpt-5.5", "high", true, nil)

	highError, _, _ := stats.snapshotForRequest(7, "gpt-6-astra", "high")
	lowError, _, _ := stats.snapshotForRequest(7, "gpt-6-astra", "low")
	otherModelError, _, _ := stats.snapshotForRequest(7, "gpt-5.5", "high")
	coldError, _, _ := stats.snapshotForRequest(7, "gpt-6-astra", "xhigh")

	require.Greater(t, highError, 0.0)
	require.Equal(t, 0.0, lowError)
	require.Equal(t, 0.0, otherModelError)
	require.Equal(t, 0.0, coldError)
}

func TestOpenAIClientRequestedModelContextIsIndependentFromMappedModel(t *testing.T) {
	ctx := WithOpenAIClientRequestedModel(context.Background(), " gpt-6-astra ")
	req := OpenAIAccountScheduleRequest{RequestedModel: "upstream-gpt-6", RequestedReasoningEffort: "high"}
	req.ClientRequestedModel = OpenAIClientRequestedModelFromContext(ctx)

	require.Equal(t, "gpt-6-astra", openAIClientModelForSchedule(req))
	require.Equal(t, "upstream-gpt-6", req.RequestedModel, "account compatibility must retain the mapped model")
	require.Empty(t, OpenAIClientRequestedModelFromContext(context.Background()))
}

func TestOpenAISchedulerUsesRouteMigrationContextBeforeAffinity(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
	rate := func(value float64) *float64 { return &value }
	groupID := int64(802)
	accounts := []Account{
		{ID: 801, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 5, GroupIDs: []int64{groupID}, RateMultiplier: rate(0.06)},
		{ID: 802, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 5, GroupIDs: []int64{groupID}, RateMultiplier: rate(0.06)},
		{ID: 803, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 5, GroupIDs: []int64{groupID}, RateMultiplier: rate(0.08)},
	}
	cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"session": 801}}
	settings := newOpenAIAdvancedSchedulerRateLimitService("true", "true")
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: accounts},
		cache:              cache,
		cfg:                &config.Config{},
		rateLimitService:   settings,
		openaiAccountStats: newOpenAIAccountRuntimeStats(),
	}
	ctx := WithOpenAIClientRequestedModel(context.Background(), "gpt-6-astra")
	ctx = WithRequestedReasoningEffort(ctx, "high")
	ctx = WithOpenAIRouteMigration(ctx, 0.06, "gpt-6-astra", "high")

	selection, decision, err := svc.SelectAccountWithSchedulerForCapability(
		ctx, &groupID, "resp_previous", "session", "gpt-6-astra", map[int64]struct{}{801: {}},
		OpenAIUpstreamTransportAny, "", false, true, true, PlatformOpenAI,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, int64(802), selection.Account.ID, "same-rate healthy account must precede a higher-rate account")
	require.True(t, decision.RouteMigrationActive)
	require.Equal(t, 0.06, decision.MigrationFromRateMultiplier)
	require.Equal(t, 0.06, decision.SelectedRateMultiplier)
	require.Equal(t, "rate_ladder_same_rate", decision.ReasonCode)
	require.Equal(t, "gpt-6-astra", OpenAIClientRequestedModelFromContext(ctx))
	require.Equal(t, "gpt-6-astra", openAIClientModelForSchedule(OpenAIAccountScheduleRequest{RequestedModel: "mapped-model", ClientRequestedModel: "gpt-6-astra"}))
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}

	selection, decision, err = svc.SelectAccountWithSchedulerForCapability(
		ctx, &groupID, "resp_previous", "session", "gpt-6-astra", map[int64]struct{}{801: {}, 802: {}},
		OpenAIUpstreamTransportAny, "", false, true, true, PlatformOpenAI,
	)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, int64(803), selection.Account.ID)
	require.Equal(t, 0.08, decision.SelectedRateMultiplier)
	require.Equal(t, "rate_ladder_upgrade", decision.ReasonCode)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestOpenAIStickyEscapeUsesRequestedModelAndEffortRoute(t *testing.T) {
	stats := newOpenAIAccountRuntimeStats()
	for i := 0; i < 4; i++ {
		stats.reportForRequest(7, "gpt-5.5", "high", false, nil)
	}
	scheduler := &defaultOpenAIAccountScheduler{stats: stats}
	cfg := openAIStickyEscapeConfig{enabled: true, errorRate: 0.5, ttftMs: 15_000}

	_, _, _, shouldEscape := scheduler.shouldEscapeStickyAccountForRequest(7, "gpt-6-astra", "high", cfg)
	require.False(t, shouldEscape, "a different model route must remain neutral")

	_, _, _, shouldEscape = scheduler.shouldEscapeStickyAccountForRequest(7, "gpt-5.5", "high", cfg)
	require.True(t, shouldEscape, "the unhealthy model/effort route should escape")
}

func TestBuildOpenAIAccountScheduleCandidatesExplainsSelectionAndExclusions(t *testing.T) {
	first := &Account{ID: 2}
	second := &Account{ID: 1}
	plan := openAIAccountLoadPlan{
		candidates: []openAIAccountCandidateScore{
			{account: first, loadInfo: &AccountLoadInfo{AccountID: 2, LoadRate: 20}, score: 4.5, priority: 2},
			{account: second, loadInfo: &AccountLoadInfo{AccountID: 1, LoadRate: 80}, score: 3.5, priority: 4},
		},
		topK: 1,
	}
	got := buildOpenAIAccountScheduleCandidates(plan, map[int64]string{9: "session_excluded"})
	require.Len(t, got, 3)
	require.Equal(t, int64(1), got[0].AccountID)
	require.False(t, got[0].InTopK)
	require.Equal(t, int64(2), got[1].AccountID)
	require.True(t, got[1].InTopK)
	require.Equal(t, int64(9), got[2].AccountID)
	require.False(t, got[2].Eligible)
	require.Equal(t, "session_excluded", got[2].ExclusionReason)
	require.Equal(t, "ranked_below_top_k", got[0].DecisionReason)
	require.Equal(t, "score_top_k_candidate", got[1].DecisionReason)
}

func TestBuildOpenAIRouteMigrationSelectionOrderUsesRateLadder(t *testing.T) {
	rate := func(value float64) *float64 { return &value }
	candidates := []openAIAccountCandidateScore{
		{account: &Account{ID: 1, RateMultiplier: rate(0.06)}, rateMultiplier: 0.06, score: 8},
		{account: &Account{ID: 2, RateMultiplier: rate(0.06)}, rateMultiplier: 0.06, score: 2},
		{account: &Account{ID: 3, RateMultiplier: rate(0.08)}, rateMultiplier: 0.08, score: 20},
		{account: &Account{ID: 4, RateMultiplier: rate(0.10)}, rateMultiplier: 0.10, score: 30},
	}

	ordered := buildOpenAIRouteMigrationSelectionOrder(candidates, OpenAIAccountScheduleRequest{
		RouteMigrationActive:         true,
		RouteMigrationRateMultiplier: 0.06,
	})
	require.Len(t, ordered, len(candidates))
	require.Equal(t, 0.06, ordered[0].rateMultiplier)
	require.Equal(t, 0.06, ordered[1].rateMultiplier)
	require.Equal(t, int64(3), ordered[2].account.ID)
	require.Equal(t, int64(4), ordered[3].account.ID)

	ordered = buildOpenAIRouteMigrationSelectionOrder(candidates, OpenAIAccountScheduleRequest{
		RouteMigrationActive:         true,
		RouteMigrationRateMultiplier: 0.08,
	})
	require.Equal(t, int64(3), ordered[0].account.ID)
	require.Equal(t, int64(4), ordered[1].account.ID)
	require.Equal(t, 0.08, ordered[0].rateMultiplier)

	// If the failed rate is above the available pool, retain a lower-rate
	// fallback rather than returning no candidates at all.
	ordered = buildOpenAIRouteMigrationSelectionOrder(candidates, OpenAIAccountScheduleRequest{
		RouteMigrationActive:         true,
		RouteMigrationRateMultiplier: 0.12,
	})
	require.Equal(t, int64(4), ordered[0].account.ID)
	require.Equal(t, 0.10, ordered[0].rateMultiplier)
	require.Equal(t, 0.08, ordered[1].rateMultiplier)
	require.Equal(t, 0.06, ordered[2].rateMultiplier)
}

func TestPrioritizeOpenAIAccountScheduleCandidatesKeepsDecisionEvidence(t *testing.T) {
	candidates := make([]OpenAIAccountScheduleCandidate, openAIAccountScheduleCandidateLimit+3)
	for i := range candidates {
		candidates[i] = OpenAIAccountScheduleCandidate{AccountID: int64(i + 1), Eligible: true}
	}
	candidates[openAIAccountScheduleCandidateLimit+1].InTopK = true
	candidates[openAIAccountScheduleCandidateLimit+2].Selected = true

	got := prioritizeOpenAIAccountScheduleCandidates(candidates, openAIAccountScheduleCandidateLimit)
	require.Len(t, got, openAIAccountScheduleCandidateLimit)
	require.Contains(t, got, candidates[openAIAccountScheduleCandidateLimit+1])
	require.Contains(t, got, candidates[openAIAccountScheduleCandidateLimit+2])
}

type schedulerLatencyAccountRepo struct{ schedulerTestOpenAIAccountRepo }

func (r schedulerLatencyAccountRepo) ListSchedulableByPlatform(ctx context.Context, platform string) ([]Account, error) {
	time.Sleep(20 * time.Millisecond)
	return r.schedulerTestOpenAIAccountRepo.ListSchedulableByPlatform(ctx, platform)
}

func (r schedulerLatencyAccountRepo) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]Account, error) {
	return r.ListSchedulableByPlatform(ctx, platform)
}

func TestOpenAISchedulerSelectReturnsRealLatency(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
	for _, withAccount := range []bool{false, true} {
		name := "no_available_account"
		var accounts []Account
		if withAccount {
			name = "selected_account"
			accounts = []Account{{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 5}}
		}
		t.Run(name, func(t *testing.T) {
			scheduler := &defaultOpenAIAccountScheduler{
				service: &OpenAIGatewayService{accountRepo: schedulerLatencyAccountRepo{schedulerTestOpenAIAccountRepo{accounts: accounts}}},
				stats:   newOpenAIAccountRuntimeStats(),
			}
			selection, decision, err := scheduler.Select(context.Background(), OpenAIAccountScheduleRequest{Platform: PlatformOpenAI})
			if withAccount {
				require.NoError(t, err)
				require.NotNil(t, selection)
				require.Equal(t, int64(1), decision.SelectedAccountID)
				if selection.ReleaseFunc != nil {
					t.Cleanup(selection.ReleaseFunc)
				}
			} else {
				require.Error(t, err)
			}
			require.GreaterOrEqual(t, decision.LatencyMs, int64(20))
			metrics := scheduler.SnapshotMetrics()
			require.Equal(t, decision.LatencyMs, metrics.SchedulerLatencyMsTotal)
			require.Equal(t, float64(decision.LatencyMs), metrics.SchedulerLatencyMsAvg)
		})
	}
}

func TestOpenAISchedulerStickyHitRatioCountsSelectionOnce(t *testing.T) {
	scheduler := &defaultOpenAIAccountScheduler{stats: newOpenAIAccountRuntimeStats()}
	scheduler.metrics.recordSelect(OpenAIAccountScheduleDecision{StickyPreviousHit: true, StickySessionHit: true})
	snapshot := scheduler.SnapshotMetrics()
	require.Equal(t, int64(1), snapshot.SelectTotal)
	require.Equal(t, int64(1), snapshot.StickyPreviousHitTotal)
	require.Equal(t, int64(1), snapshot.StickySessionHitTotal)
	require.Equal(t, 1.0, snapshot.StickyHitRatio)

	scheduler.metrics.recordSelect(OpenAIAccountScheduleDecision{StickyPreviousHit: true})
	scheduler.metrics.recordSelect(OpenAIAccountScheduleDecision{StickySessionHit: true})
	scheduler.metrics.recordSelect(OpenAIAccountScheduleDecision{})
	snapshot = scheduler.SnapshotMetrics()
	require.Equal(t, int64(4), snapshot.SelectTotal)
	require.Equal(t, int64(2), snapshot.StickyPreviousHitTotal)
	require.Equal(t, int64(2), snapshot.StickySessionHitTotal)
	require.Equal(t, 0.75, snapshot.StickyHitRatio)
}
