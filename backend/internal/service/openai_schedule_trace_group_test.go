//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestOpenAIScheduleTraceGroupFilterBeforeLimit(t *testing.T) {
	group7, group8 := int64(7), int64(8)
	traces := []OpenAIAccountScheduleTrace{
		{OpenAIEvalRankingTrace: OpenAIEvalRankingTrace{GroupID: &group8}, SelectedAccountID: 3},
		{OpenAIEvalRankingTrace: OpenAIEvalRankingTrace{GroupID: &group7}, SelectedAccountID: 2, GroupName: "historical"},
		{OpenAIEvalRankingTrace: OpenAIEvalRankingTrace{GroupID: &group7}, SelectedAccountID: 1},
		{SelectedAccountID: 9},
	}
	filtered := filterOpenAIAccountScheduleTraces(traces, 1, group7)
	require.Len(t, filtered, 1)
	require.Equal(t, int64(2), filtered[0].SelectedAccountID)
	require.Equal(t, "historical", filtered[0].GroupName)
	all := filterOpenAIAccountScheduleTraces(traces, 0, group7)
	require.Len(t, all, 2)
	require.Empty(t, all[1].GroupName, "old records must not be hydrated from current group metadata")
	require.Empty(t, filterOpenAIAccountScheduleTraces(traces, 10, 99))
}

func TestOpenAIScheduleTraceCapturesDispatchIdentity(t *testing.T) {
	_, repo, accounts, gateway := rankingHarness(t)
	repo.config.AccountPriorityRules = []OpenAIEvalAccountPriorityRule{{AccountID: 2, Priority: 0}}
	require.NoError(t, SetOpenAIEvalSchedulingPolicySnapshot(&repo.config))
	group := &Group{ID: 7, Name: "original group"}
	accounts.items[1].Name = "original account"
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)
	selection, _, err := gateway.SelectAccountWithScheduler(ctx, &group.ID, "", "", "gpt-6.1-sol", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.Equal(t, int64(2), selection.Account.ID)
	if selection.ReleaseFunc != nil {
		t.Cleanup(selection.ReleaseFunc)
	}
	group.Name = "renamed group"
	group.ID = 8
	accounts.items[1].Name = "renamed account"
	SetOpenAIEvalEffectsEnabled(false)
	traces := gateway.RecentOpenAIAccountScheduleTraces(1, 7)
	require.Len(t, traces, 1, "history remains visible when special ordering is disabled")
	require.Equal(t, int64(7), *traces[0].GroupID)
	require.Equal(t, "original group", traces[0].GroupName)
	require.Equal(t, "original account", traces[0].SelectedAccountName)
	require.Empty(t, gateway.RecentOpenAIAccountScheduleTraces(1, 8))
}

func TestOpenAIScheduleTraceGroupNameDoesNotInferFromOtherGroup(t *testing.T) {
	scheduler := &defaultOpenAIAccountScheduler{}
	groupID := int64(7)
	ctx := context.WithValue(context.Background(), ctxkey.Group, &Group{ID: 8, Name: "different group"})
	require.Empty(t, scheduler.scheduleTraceGroupName(ctx, &groupID))
	require.Empty(t, scheduler.scheduleTraceGroupName(ctx, nil))
}

func TestOpenAIScheduleTraceLegacyEntryRecordsWithoutChangingSelection(t *testing.T) {
	for _, loadAware := range []bool{false, true} {
		t.Run(map[bool]string{false: "scheduler wrapper", true: "public load aware"}[loadAware], func(t *testing.T) {
			_, repo, _, gateway := rankingHarness(t)
			repo.config.SchedulingPolicy = ""
			repo.config.AccountPriorityRules = nil
			require.NoError(t, SetOpenAIEvalSchedulingPolicySnapshot(&repo.config))
			SetOpenAIEvalEffectsEnabled(false)
			gateway.cache = &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:trace-session": 1}}
			group := &Group{ID: 7, Name: "legacy group"}
			ctx := context.WithValue(context.Background(), ctxkey.Group, group)
			var selection *AccountSelectionResult
			var err error
			if loadAware {
				selection, err = gateway.SelectAccountWithLoadAwareness(ctx, &group.ID, "trace-session", "gpt-6.1-sol", nil)
			} else {
				selection, _, err = gateway.SelectAccountWithScheduler(ctx, &group.ID, "", "trace-session", "gpt-6.1-sol", nil, OpenAIUpstreamTransportAny, false)
			}
			require.NoError(t, err)
			require.Equal(t, int64(1), selection.Account.ID)
			if selection.ReleaseFunc != nil {
				t.Cleanup(selection.ReleaseFunc)
			}
			traces := gateway.RecentOpenAIAccountScheduleTraces(10, 7)
			require.Len(t, traces, 1, "default routing must record exactly once without enabling advanced selection")
			require.Equal(t, int64(1), traces[0].SelectedAccountID)
			require.Equal(t, "legacy group", traces[0].GroupName)
			require.Equal(t, "legacy", traces[0].RankingBasis)
			require.Equal(t, selection.Acquired, traces[0].Acquired)
			require.Equal(t, !selection.Acquired && selection.WaitPlan != nil, traces[0].WaitPlan)
			require.Equal(t, selection.stickySessionHit, traces[0].StickySessionHit)
		})
	}
}

func TestOpenAIScheduleTraceGroupIdentityReadOwnership(t *testing.T) {
	groupID := int64(7)
	store := &openAIAccountScheduleTraceStore{}
	store.append(OpenAIAccountScheduleTrace{OpenAIEvalRankingTrace: OpenAIEvalRankingTrace{GroupID: &groupID}})
	groupID = 8
	first := store.recent(1)
	require.Equal(t, int64(7), *first[0].GroupID)
	*first[0].GroupID = 9
	require.Equal(t, int64(7), *store.recent(1)[0].GroupID, "callers cannot mutate historical grouping")
}

func TestOpenAIScheduleTraceFiltersEntireRetainedWindow(t *testing.T) {
	_, _, _, gateway := rankingHarness(t)
	scheduler := gateway.persistentOpenAIAccountScheduler().(*defaultOpenAIAccountScheduler)
	group7, group8 := int64(7), int64(8)
	for i := 1; i <= openAIAccountScheduleTraceCapacity+10; i++ {
		group := &group8
		if i == 15 || i == 100 {
			group = &group7
		}
		scheduler.traces.append(OpenAIAccountScheduleTrace{OpenAIEvalRankingTrace: OpenAIEvalRankingTrace{GroupID: group}, SelectedAccountID: int64(i)})
	}
	all := gateway.RecentOpenAIAccountScheduleTraces(openAIAccountScheduleTraceCapacity)
	require.Len(t, all, openAIAccountScheduleTraceCapacity)
	require.Equal(t, int64(11), all[len(all)-1].SelectedAccountID)
	filtered := gateway.RecentOpenAIAccountScheduleTraces(1, group7)
	require.Len(t, filtered, 1)
	require.Equal(t, int64(100), filtered[0].SelectedAccountID, "matching record outside the latest 50 must remain queryable")
	filtered = gateway.RecentOpenAIAccountScheduleTraces(50, group7)
	require.Len(t, filtered, 2)
	require.Equal(t, int64(15), filtered[1].SelectedAccountID)
}

func TestOpenAIScheduleTraceLegacyPendingAndFailure(t *testing.T) {
	_, repo, _, gateway := rankingHarness(t)
	repo.config.SchedulingPolicy = ""
	require.NoError(t, SetOpenAIEvalSchedulingPolicySnapshot(&repo.config))
	gateway.concurrencyService = NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: map[int64]bool{1: false, 2: false}})
	selection, err := gateway.SelectAccountWithLoadAwareness(context.Background(), rankingPtr(int64(7)), "", "gpt-6.1-sol", nil)
	require.NoError(t, err)
	require.NotNil(t, selection.WaitPlan)
	trace := gateway.RecentOpenAIAccountScheduleTraces(1)[0]
	require.False(t, trace.Acquired)
	require.True(t, trace.WaitPlan)
	require.False(t, trace.Candidates[0].Selected)
	require.True(t, trace.Candidates[0].AwaitingAdmission)
	require.Equal(t, "account_admission_pending", trace.ReasonCode)
	_, err = gateway.SelectAccountWithLoadAwareness(context.Background(), rankingPtr(int64(7)), "", "gpt-6.1-sol", map[int64]struct{}{1: {}, 2: {}})
	require.Error(t, err)
	trace = gateway.RecentOpenAIAccountScheduleTraces(1)[0]
	require.Equal(t, "selection_error", trace.ReasonCode)
	require.Zero(t, trace.SelectedAccountID)
	require.NotEmpty(t, trace.Error)
}
