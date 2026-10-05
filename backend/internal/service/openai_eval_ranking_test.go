package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type rankingTestRepo struct {
	OpenAIEvalRepository
	mu     sync.Mutex
	config OpenAIEvalConfig
	runs   []OpenAIEvalRun
	audits []string
}

func (r *rankingTestRepo) GetConfig(context.Context) (*OpenAIEvalConfig, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneRankingConfig(&r.config), nil
}
func (r *rankingTestRepo) SaveConfig(_ context.Context, cfg *OpenAIEvalConfig, _ int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cfg.Revision != 0 && cfg.Revision != r.config.Revision {
		return ErrOpenAIEvalConfigRevisionConflict
	}
	cfg.Revision = r.config.Revision + 1
	r.config = *cloneRankingConfig(cfg)
	return nil
}
func (r *rankingTestRepo) RecordAuditEvent(_ context.Context, _ int64, action string, _ map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.audits = append(r.audits, action)
	return nil
}
func (r *rankingTestRepo) LatestCompletedRuns(_ context.Context, keys []OpenAIEvalEvidenceKey) ([]OpenAIEvalRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var result []OpenAIEvalRun
	for _, key := range keys {
		for _, run := range r.runs {
			if run.AccountID == key.AccountID && openAIEvalQualityDimension(run.RequestedModel) == openAIEvalQualityDimension(key.RequestedModel) && openAIEvalQualityDimension(run.ReasoningEffort) == openAIEvalQualityDimension(key.ReasoningEffort) && run.TestType == key.TestType && openAIEvalQualityRunSourceSupported(run.TriggerSource) && run.Status != "running" && !run.FinishedAt.IsZero() {
				result = append(result, run)
			}
		}
	}
	return result, nil
}

type rankingTestGroups struct {
	GroupRepository
	items []Group
}

func (r *rankingTestGroups) List(_ context.Context, p pagination.PaginationParams) ([]Group, *pagination.PaginationResult, error) {
	start := min(p.Offset(), len(r.items))
	end := min(start+p.Limit(), len(r.items))
	return r.items[start:end], &pagination.PaginationResult{Pages: (len(r.items) + p.Limit() - 1) / p.Limit(), Total: int64(len(r.items))}, nil
}

type rankingTestAccounts struct {
	AccountRepository
	items  []Account
	pages  []int
	before func()
	err    error
}

func (r *rankingTestAccounts) ListWithFilters(_ context.Context, p pagination.PaginationParams, platform, typ, status, search string, groupID int64, privacy string) ([]Account, *pagination.PaginationResult, error) {
	if r.before != nil {
		r.before()
	}
	if r.err != nil {
		return nil, nil, r.err
	}
	if platform != "" || typ != "" || status != "" || search != "" || groupID != 0 || privacy != "" {
		panic("ranking discovery filtered complete membership")
	}
	r.pages = append(r.pages, p.Page)
	start := min(p.Offset(), len(r.items))
	end := min(start+p.Limit(), len(r.items))
	return append([]Account(nil), r.items[start:end]...), &pagination.PaginationResult{Pages: (len(r.items) + p.Limit() - 1) / p.Limit(), Total: int64(len(r.items))}, nil
}
func (r *rankingTestAccounts) GetByID(_ context.Context, id int64) (*Account, error) {
	for _, a := range r.items {
		if a.ID == id {
			return &a, nil
		}
	}
	return nil, ErrAccountNotFound
}
func (r *rankingTestAccounts) ListSchedulableByGroupIDAndPlatform(_ context.Context, groupID int64, platform string) ([]Account, error) {
	var result []Account
	for _, a := range r.items {
		if a.Platform == platform && a.IsSchedulable() {
			for _, id := range a.GroupIDs {
				if id == groupID {
					result = append(result, a)
					break
				}
			}
		}
	}
	return result, nil
}
func (r *rankingTestAccounts) ListSchedulableUngroupedByPlatform(_ context.Context, platform string) ([]Account, error) {
	var result []Account
	for _, a := range r.items {
		if a.Platform == platform && a.IsSchedulable() && len(a.GroupIDs) == 0 {
			result = append(result, a)
		}
	}
	return result, nil
}

type rankingPeekCache struct {
	ConcurrencyCache
	loads map[int64]*AccountLoadInfo
	calls int
}

func (c *rankingPeekCache) PeekAccountsLoadBatch(_ context.Context, accounts []AccountWithConcurrency) (map[int64]*AccountLoadInfo, error) {
	c.calls++
	result := make(map[int64]*AccountLoadInfo)
	for _, a := range accounts {
		if load := c.loads[a.ID]; load != nil {
			copy := *load
			result[a.ID] = &copy
		} else {
			result[a.ID] = &AccountLoadInfo{AccountID: a.ID}
		}
	}
	return result, nil
}

func rankingHarness(t *testing.T) (*OpenAIEvalService, *rankingTestRepo, *rankingTestAccounts, *OpenAIGatewayService) {
	t.Helper()
	oldPolicy := openAIEvalSchedulingPolicy.Load()
	oldQuality := openAIEvalQualitySnapshots
	openAIEvalQualitySnapshots = &openAIEvalQualitySnapshotStore{}
	openAIEvalSchedulingPolicy.Store(&openAIEvalSchedulingPolicySnapshot{})
	t.Cleanup(func() {
		openAIEvalQualitySnapshots = oldQuality
		openAIEvalSchedulingPolicy.Store(oldPolicy)
		resetOpenAIAdvancedSchedulerSettingCacheForTest()
	})
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	repo := &rankingTestRepo{config: OpenAIEvalConfig{Revision: 1, EffectsEnabled: true, SchedulingPolicy: OpenAIEvalSchedulingPolicyCostFirst}}
	accounts := &rankingTestAccounts{items: []Account{
		{ID: 1, Name: "one", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{7}, RateMultiplier: rankingPtr(1.0)},
		{ID: 2, Name: "two", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{7}, RateMultiplier: rankingPtr(3.0)},
	}}
	gateway := &OpenAIGatewayService{accountRepo: accounts, cfg: &config.Config{}, cache: &schedulerTestGatewayCache{}, rateLimitService: newOpenAIAdvancedSchedulerRateLimitService("false")}
	eval := NewOpenAIEvalService(repo, accounts, nil)
	NewOpenAIEvalRankingService(eval, &rankingTestGroups{items: []Group{{ID: 7, Name: "Primary", Platform: PlatformOpenAI, Status: StatusActive}, {ID: 8, Name: "Empty", Platform: PlatformOpenAI, Status: StatusActive}}}, nil, nil, gateway, nil)
	require.NoError(t, eval.Initialize(context.Background()))
	return eval, repo, accounts, gateway
}

func rankingDimension(t *testing.T, s *OpenAIEvalService, group int64, model, effort string) OpenAIEvalRankingDimension {
	t.Helper()
	snapshot, err := s.SchedulingRankings(OpenAIEvalRankingFilter{GroupID: &group, RequestedModel: &model, ReasoningEffort: &effort, Limit: 500})
	require.NoError(t, err)
	require.Len(t, snapshot.Dimensions, 1)
	return snapshot.Dimensions[0]
}

func TestOpenAIRankingEvaluateThenActualDispatchAdvancedOff(t *testing.T) {
	for _, policy := range []string{OpenAIEvalSchedulingPolicyCostFirst, OpenAIEvalSchedulingPolicyStabilityFirst, OpenAIEvalSchedulingPolicyAvoidDegradation, OpenAIEvalSchedulingPolicyCustomBalance} {
		t.Run(policy, func(t *testing.T) {
			s, repo, accounts, gateway := rankingHarness(t)
			repo.config.SchedulingPolicy = policy
			repo.config.CustomBalance = OpenAIEvalPolicyWeights{Cost: 1}
			repo.config.Revision++
			peek := &rankingPeekCache{loads: map[int64]*AccountLoadInfo{1: {AccountID: 1, CurrentConcurrency: 1, LoadRate: 100}}}
			gateway.concurrencyService = NewConcurrencyService(peek)
			summary, err := s.EvaluateScheduling(context.Background(), 42)
			require.NoError(t, err)
			require.Greater(t, summary.DimensionCount, 0)
			require.Equal(t, []string{"scheduling_evaluation_requested"}, repo.audits)
			require.Positive(t, peek.calls)
			dim := rankingDimension(t, s, 7, "gpt-6.1-sol", "")
			require.Len(t, dim.Accounts, 2)
			require.NotNil(t, dim.Accounts[0].Rank)
			// Frozen full-load diagnostics must not skip a recovered rank one.
			gateway.concurrencyService = nil
			accounts.items[0].Priority = 999
			accounts.items[1].Priority = -999
			group := int64(7)
			selection, decision, err := gateway.SelectAccountWithScheduler(context.Background(), &group, "", "ranked-session", "gpt-6.1-sol", nil, OpenAIUpstreamTransportAny, false)
			require.NoError(t, err)
			require.NotNil(t, selection)
			require.Equal(t, dim.Accounts[0].AccountID, selection.Account.ID)
			selection.ReleaseFunc()
			require.Equal(t, "overview_prior", decision.RankingBasis)
			require.Equal(t, summary.EvaluationID, *decision.EvaluationID)
			require.Equal(t, 1, *decision.SelectedRank)
			require.NotEmpty(t, gateway.RecentOpenAIAccountScheduleTraces(10))
			before := s.ranking.current
			_, err = s.GetConfig(context.Background())
			require.NoError(t, err)
			require.Same(t, before, s.ranking.current)
		})
	}
}

func TestOpenAIRankingScorerFiveFactorsQualityAndTies(t *testing.T) {
	now := time.Now()
	account := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, RateMultiplier: rankingPtr(1.0)}
	f := emptyOpenAIEvalRankingFactors()
	f.ErrorRate = OpenAIEvalRankingErrorRate{OpenAIEvalFactorMeta: rankingKnown(.8, now), Value: rankingPtr(.2), SampleCount: 5}
	f.Load = OpenAIEvalRankingLoad{OpenAIEvalFactorMeta: rankingKnown(.25, now)}
	rows := scoreOpenAIEvalRanking(OpenAIEvalSchedulingPolicyCostFirst, OpenAIEvalRankingWeights{.6, .2, .1, .1, 0}, []openAIEvalRankingInput{{account: account, factors: f, compatible: true}}, now, nil)
	require.InDelta(t, 30+16+9+2.5, *rows[0].PriorityScore, 1e-9)
	require.True(t, rows[0].Factors.Price.Known)
	require.Equal(t, .5, rows[0].Factors.Price.Score)
	var inputs []openAIEvalRankingInput
	for i, ratio := range []float64{0, 1.0 / 3, .5, 1} {
		a := *account
		a.ID = int64(i + 1)
		f := emptyOpenAIEvalRankingFactors()
		if i > 0 {
			selected := 4 - i
			f.Quality = OpenAIEvalRankingQuality{OpenAIEvalFactorMeta: rankingKnown(ratio, now), Selected: selected, Evaluated: selected, Pass: 1, Ratio: rankingPtr(ratio), State: "assessed"}
		}
		inputs = append(inputs, openAIEvalRankingInput{account: &a, factors: f, compatible: true})
	}
	rows = scoreOpenAIEvalRanking(OpenAIEvalSchedulingPolicyAvoidDegradation, OpenAIEvalRankingWeights{.4, .35, .15, .1, 0}, inputs, now, nil)
	require.Equal(t, []int64{4, 3, 2, 1}, []int64{rows[0].AccountID, rows[1].AccountID, rows[2].AccountID, rows[3].AccountID})
	rows = scoreOpenAIEvalRanking(OpenAIEvalSchedulingPolicyStabilityFirst, OpenAIEvalRankingWeights{.1, .5, .3, .1, 0}, inputs, now, nil)
	for i, row := range rows {
		require.Equal(t, int64(i+1), row.AccountID)
		require.Equal(t, 86.0, *row.PriorityScore)
	}
	_, weights := openAIEvalRankingWeights(&OpenAIEvalConfig{SchedulingPolicy: OpenAIEvalSchedulingPolicyCustomBalance, CustomBalance: OpenAIEvalPolicyWeights{Stability: 1}}, "gpt-6.1-sol", "")
	require.Equal(t, OpenAIEvalRankingWeights{ErrorRate: .6, TTFT: .4}, weights)
}

func TestOpenAIRankingLatestOffPeriodUnknownAndExactEffort(t *testing.T) {
	s, repo, _, _ := rankingHarness(t)
	schedule := OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300}
	repo.config.EffectsEnabled = false
	repo.config.Revision++
	repo.config.Accounts = []OpenAIEvalAccountConfig{{AccountID: 1, RequestedModel: "gpt-6.1-sol", CandySchedule: schedule}}
	now := time.Now().Add(-time.Minute)
	repo.runs = []OpenAIEvalRun{{ID: 1, AccountID: 1, RequestedModel: "gpt-6.1-sol", TestType: OpenAIEvalTypeCandy, TriggerSource: "scheduled", DataVersion: OpenAIEvalDataVersion, Status: "pass", FinishedAt: now, Samples: []OpenAIEvalSampleRecord{{Valid: true, Answer: "21"}}}}
	_, err := s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, 1.0, *rankingDimension(t, s, 7, "gpt-6.1-sol", "").Accounts[0].Factors.Quality.Ratio)
	for _, status := range []string{"error", "insufficient", "cancelled"} {
		run := repo.runs[0]
		run.ID = 2
		run.Status = status
		run.FinishedAt = now.Add(time.Second)
		repo.runs = append(repo.runs[:1], run)
		_, err = s.EvaluateScheduling(context.Background(), 1)
		require.NoError(t, err)
		q := rankingDimension(t, s, 7, "gpt-6.1-sol", "").Accounts[0].Factors.Quality
		require.False(t, q.Known)
		require.Nil(t, q.Ratio)
		require.Equal(t, 1, q.Selected)
	}
	repo.runs[1].Status = "running"
	_, err = s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	require.True(t, rankingDimension(t, s, 7, "gpt-6.1-sol", "").Accounts[0].Factors.Quality.Known)
	repo.runs[1].Status = "pass"
	repo.runs[1].ReasoningEffort = "high"
	_, err = s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	require.Nil(t, rankingDimension(t, s, 7, "gpt-6.1-sol", "high").Accounts[0].Factors.Quality.Ratio)
	require.Zero(t, OpenAIEvalQualityRefreshStatus().RouteCount)
}

func TestOpenAIRankingCompleteMembersPaginationAndGeneration(t *testing.T) {
	s, _, accounts, _ := rankingHarness(t)
	base := accounts.items[0]
	accounts.items = nil
	for i := 1; i <= 601; i++ {
		a := base
		a.ID = int64(i)
		a.Name = fmt.Sprint(i)
		if i == 600 {
			a.Status = "disabled"
		}
		if i == 601 {
			a.Schedulable = false
		}
		accounts.items = append(accounts.items, a)
	}
	summary, err := s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	require.Contains(t, accounts.pages, 2)
	var covered OpenAIEvalRankingDimension
	for _, dim := range s.ranking.current.dimensions {
		if len(dim.Accounts) == 601 {
			covered = dim
			break
		}
	}
	require.NotEmpty(t, covered.DimensionID)
	filter := OpenAIEvalRankingFilter{GroupID: rankingPtr(int64(7)), RequestedModel: rankingPtr(covered.RequestedModel), ReasoningEffort: rankingPtr(covered.ReasoningEffort), Limit: 500}
	first, err := s.SchedulingRankings(filter)
	require.NoError(t, err)
	require.Len(t, first.Dimensions[0].Accounts, 500)
	require.Equal(t, 601, *first.Dimensions[0].CandidateCount)
	require.NotNil(t, first.NextCursor)
	filter.Cursor = *first.NextCursor
	second, err := s.SchedulingRankings(filter)
	require.NoError(t, err)
	require.Len(t, second.Dimensions[0].Accounts, 101)
	for _, row := range second.Dimensions[0].Accounts {
		if row.AccountID >= 600 {
			require.NotNil(t, row.Rank)
			require.False(t, row.Eligible)
		}
	}
	_, err = s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	_, err = s.SchedulingRankings(filter)
	require.NoError(t, err)
	_, err = s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	_, err = s.SchedulingRankings(filter)
	require.ErrorIs(t, err, ErrOpenAIEvalRankingSnapshotChanged)
	require.NotEqual(t, summary.EvaluationID, s.ranking.current.summary.EvaluationID)
}

func TestOpenAIRankingSupersededAndSavedEvaluationFailure(t *testing.T) {
	s, repo, accounts, _ := rankingHarness(t)
	_, err := s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	before := s.ranking.current.summary.EvaluationID
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	accounts.before = func() { once.Do(func() { close(entered); <-release }) }
	done := make(chan error, 1)
	go func() { _, err := s.EvaluateScheduling(context.Background(), 1); done <- err }()
	<-entered
	s.ranking.mu.Lock()
	repo.mu.Lock()
	repo.config.Revision++
	repo.config.EffectsEnabled = false
	next := cloneRankingConfig(&repo.config)
	repo.mu.Unlock()
	s.ranking.adoptLocked(next)
	s.ranking.mu.Unlock()
	close(release)
	require.ErrorIs(t, <-done, ErrOpenAIEvalRankingSuperseded)
	require.Equal(t, before, s.ranking.current.summary.EvaluationID)
	require.False(t, OpenAIEvalEffectsEnabled())
	accounts.before = nil
	accounts.err = errors.New("test database failure")
	require.NoError(t, s.SaveConfig(context.Background(), next, 1))
	status, err := s.SchedulingRankings(OpenAIEvalRankingFilter{})
	require.NoError(t, err)
	require.NotNil(t, status.RankingError)
	require.Equal(t, next.Revision, status.CurrentConfigRevision)
	require.Equal(t, before, status.Summary.EvaluationID)
	accounts.err = nil
	_, err = s.ranking.evaluate(context.Background(), "interval", false)
	require.NoError(t, err)
	require.Nil(t, s.ranking.lastError)
}

func TestOpenAIRankingFallbackEntirePoolAndMetricInvalidation(t *testing.T) {
	s, _, accounts, gateway := rankingHarness(t)
	_, err := s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	scheduler := gateway.persistentOpenAIAccountScheduler().(*defaultOpenAIAccountScheduler)
	req := OpenAIAccountScheduleRequest{Platform: PlatformOpenAI, GroupID: rankingPtr(int64(7)), RequestedModel: "gpt-6.1-sol", ClientRequestedModel: "gpt-6.1-sol"}
	rows, trace, err := scheduler.explicitRanking(context.Background(), req, accounts.items, nil)
	require.NoError(t, err)
	require.Equal(t, "overview_prior", trace.RankingBasis)
	before, _ := json.Marshal(rows)
	ttft := 9999
	gateway.openaiAccountStats.reportForRequest(1, req.RequestedModel, "", false, &ttft)
	rows, trace, err = scheduler.explicitRanking(context.Background(), req, accounts.items, map[int64]*AccountLoadInfo{1: {LoadRate: 100}})
	require.NoError(t, err)
	after, _ := json.Marshal(rows)
	require.NotEqual(t, string(before), string(after))
	require.Equal(t, "live_fallback", trace.RankingBasis)
	require.Equal(t, "request_metrics_updated", *trace.RankingFallbackReason)
	newAccount := accounts.items[0]
	newAccount.ID = 3
	newAccount.RateMultiplier = rankingPtr(.01)
	accounts.items = append(accounts.items, newAccount)
	rows, trace, err = scheduler.explicitRanking(context.Background(), req, accounts.items, nil)
	require.NoError(t, err)
	require.Equal(t, "live_fallback", trace.RankingBasis)
	require.Nil(t, trace.EvaluationID)
	require.Equal(t, "request_metrics_updated", *trace.RankingFallbackReason)
	require.Equal(t, int64(3), rows[0].AccountID)
	req.RequestedModel = "uncatalogued-alias"
	req.ClientRequestedModel = req.RequestedModel
	rows, trace, err = scheduler.explicitRanking(context.Background(), req, accounts.items, nil)
	require.NoError(t, err)
	require.Len(t, rows, 3)
	require.Equal(t, "live_fallback", trace.RankingBasis)
}

func TestOpenAIRankingObservedBoundAndDisabledDefault(t *testing.T) {
	s, repo, _, _ := rankingHarness(t)
	for i := 0; i < 1000; i++ {
		s.ranking.observe(7, fmt.Sprint("model-", i), "high")
	}
	require.Len(t, s.ranking.observed, 512)
	require.Positive(t, s.ranking.observedEvictions)
	repo.config = OpenAIEvalConfig{Revision: 2}
	_, err := s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	status, err := s.SchedulingRankings(OpenAIEvalRankingFilter{})
	require.NoError(t, err)
	require.Equal(t, "inactive_effects_off", status.EffectiveStatus)
	require.False(t, OpenAIEvalEffectsEnabled())
	newStore := NewOpenAIEvalRankingService(nil, nil, nil, nil, nil, nil)
	require.NotEqual(t, s.ranking.instance, newStore.instance)
}

func TestOpenAIRankingOwnerOverrideAndMovablePreferences(t *testing.T) {
	s, _, _, gateway := rankingHarness(t)
	_, err := s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	group := int64(7)
	ctx := context.Background()
	require.NoError(t, gateway.getOpenAIWSStateStore().BindResponseAccount(ctx, group, "resp_ranked_owner", 2, time.Hour))
	scheduler := gateway.persistentOpenAIAccountScheduler().(*defaultOpenAIAccountScheduler)
	req := OpenAIAccountScheduleRequest{GroupID: &group, Platform: PlatformOpenAI, RequestedModel: "gpt-6.1-sol", PreviousResponseID: "resp_ranked_owner", RequiredTransport: OpenAIUpstreamTransportAny}
	selected, decision, err := scheduler.Select(ctx, req)
	require.NoError(t, err)
	require.Equal(t, int64(2), selected.Account.ID)
	selected.ReleaseFunc()
	require.Equal(t, "owner", decision.RankingBasis)
	require.Equal(t, 2, *decision.SelectedRank)
	req.PreviousResponseCanMove = true
	req.StickyAccountID = 2
	req.GuardianParentAccountID = 2
	req.SubscriptionPriority = true
	req.RouteMigrationActive = true
	req.RouteMigrationRateMultiplier = 3
	selected, decision, err = scheduler.Select(ctx, req)
	require.NoError(t, err)
	require.Equal(t, int64(1), selected.Account.ID)
	selected.ReleaseFunc()
	require.Equal(t, "overview_prior", decision.RankingBasis)
	req.PreviousResponseCanMove = false
	req.ExcludedIDs = map[int64]struct{}{2: {}}
	selected, decision, err = scheduler.Select(ctx, req)
	require.Error(t, err)
	require.Nil(t, selected)
}

func TestOpenAIRankingUnavailableOwnerCannotFallThroughToSessionAffinity(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("ranking_enabled_%v", enabled), func(t *testing.T) {
			s, repo, _, gateway := rankingHarness(t)
			repo.config.EffectsEnabled = enabled
			repo.config.Revision++
			_, err := s.EvaluateScheduling(context.Background(), 1)
			require.NoError(t, err)
			require.Equal(t, enabled, OpenAIEvalEffectsEnabled())
			group := int64(7)
			ctx := context.Background()
			require.NoError(t, gateway.getOpenAIWSStateStore().BindResponseAccount(ctx, group, "resp_unavailable_owner", 2, time.Hour))
			require.NoError(t, gateway.cache.SetSessionAccountID(ctx, group, "conflicting-session", 1, time.Hour))
			scheduler := gateway.persistentOpenAIAccountScheduler().(*defaultOpenAIAccountScheduler)
			for _, guardian := range []int64{0, 1} {
				req := OpenAIAccountScheduleRequest{
					GroupID: &group, Platform: PlatformOpenAI, RequestedModel: "gpt-6.1-sol",
					PreviousResponseID: "resp_unavailable_owner", PreviousResponseCanMove: false,
					SessionHash: "conflicting-session", StickyAccountID: 1, GuardianParentAccountID: guardian,
					ExcludedIDs: map[int64]struct{}{2: {}}, RequiredTransport: OpenAIUpstreamTransportAny,
				}
				selected, decision, err := scheduler.Select(ctx, req)
				if selected != nil && selected.ReleaseFunc != nil {
					selected.ReleaseFunc()
				}
				require.Error(t, err, "an excluded response owner is not a soft session preference")
				require.Nil(t, selected)
				require.Equal(t, "owner", decision.RankingBasis)
			}
			selected, _, err := gateway.SelectAccountWithSchedulerForCapability(ctx, &group, "resp_unavailable_owner", "conflicting-session", "gpt-6.1-sol",
				map[int64]struct{}{2: {}}, OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityResponses, false, false, true)
			if selected != nil && selected.ReleaseFunc != nil {
				selected.ReleaseFunc()
			}
			require.Error(t, err, "the public selector preserves the hard owner with advanced scheduling disabled")
			require.Nil(t, selected)
		})
	}
}

func TestOpenAIRankingEmbeddingsDoesNotRequireResponsesOwner(t *testing.T) {
	s, _, accounts, gateway := rankingHarness(t)
	accounts.items[0].Credentials = map[string]any{"openai_capabilities": []any{"chat_completions"}}
	accounts.items[1].Credentials = map[string]any{"openai_capabilities": []any{"embeddings"}}
	_, err := s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	require.True(t, OpenAIEvalEffectsEnabled())
	ctx := context.Background()
	group := int64(7)
	require.NoError(t, gateway.getOpenAIWSStateStore().BindResponseAccount(ctx, group, "resp_embedding_incidental", 1, time.Hour))
	scheduler := gateway.persistentOpenAIAccountScheduler().(*defaultOpenAIAccountScheduler)
	req := OpenAIAccountScheduleRequest{
		GroupID: &group, Platform: PlatformOpenAI, RequestedModel: "text-embedding-3-small",
		PreviousResponseID: "resp_embedding_incidental", PreviousResponseCanMove: false,
		RequiredTransport: OpenAIUpstreamTransportAny, RequiredCapability: OpenAIEndpointCapabilityEmbeddings,
	}
	selected, decision, err := scheduler.Select(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, selected)
	require.Equal(t, int64(2), selected.Account.ID)
	require.False(t, decision.StickyPreviousHit)
	selected.ReleaseFunc()

	req.DisableStickyEscape = true
	selected, _, err = scheduler.Select(ctx, req)
	if selected != nil && selected.ReleaseFunc != nil {
		selected.ReleaseFunc()
	}
	require.Error(t, err, "the independent no-escape constraint remains enforced")
	require.Nil(t, selected)
}

func TestOpenAIRankingDispatchBeyondTraceLimitAndLiveRecovery(t *testing.T) {
	s, _, accounts, gateway := rankingHarness(t)
	base := accounts.items[0]
	accounts.items = nil
	for i := 1; i <= 70; i++ {
		a := base
		a.ID = int64(i)
		accounts.items = append(accounts.items, a)
	}
	_, err := s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	loads := make(map[int64]*AccountLoadInfo)
	for i := int64(1); i < 70; i++ {
		loads[i] = &AccountLoadInfo{AccountID: i, CurrentConcurrency: 1, LoadRate: 100}
	}
	acquired := []int64{}
	gateway.concurrencyService = NewConcurrencyService(schedulerTestConcurrencyCache{loadMap: loads, acquiredIDs: &acquired})
	group := int64(7)
	selection, decision, err := gateway.SelectAccountWithScheduler(context.Background(), &group, "", "", "gpt-6.1-sol", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.Equal(t, int64(70), selection.Account.ID)
	selection.ReleaseFunc()
	require.Equal(t, 70, *decision.SelectedRank)
	require.Equal(t, []int64{70}, acquired)
	traces := gateway.RecentOpenAIAccountScheduleTraces(1)
	require.Len(t, traces, 1)
	require.True(t, traces[0].CandidatesTruncated)
	require.LessOrEqual(t, len(traces[0].Candidates), 64)
	found := false
	for _, candidate := range traces[0].Candidates {
		if candidate.AccountID == 70 {
			found = true
			require.True(t, candidate.Selected)
		}
	}
	require.True(t, found)
	gateway.concurrencyService = NewConcurrencyService(schedulerTestConcurrencyCache{})
	selection, _, err = gateway.SelectAccountWithScheduler(context.Background(), &group, "", "", "gpt-6.1-sol", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.Equal(t, int64(1), selection.Account.ID)
	selection.ReleaseFunc()
}

func TestOpenAIRankingQualityExpiryAndClockPublication(t *testing.T) {
	s, repo, accounts, _ := rankingHarness(t)
	repo.config.Revision++
	repo.config.SchedulingPolicy = OpenAIEvalSchedulingPolicyAvoidDegradation
	repo.config.QualityRefreshIntervalSeconds = 300
	repo.config.Accounts = []OpenAIEvalAccountConfig{{AccountID: 1, RequestedModel: "gpt-6.1-sol", CandySchedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300}}}
	now := time.Now()
	s.ranking.now = func() time.Time { return now }
	repo.runs = []OpenAIEvalRun{{ID: 1, AccountID: 1, RequestedModel: "gpt-6.1-sol", TestType: OpenAIEvalTypeCandy, TriggerSource: "scheduled", DataVersion: OpenAIEvalDataVersion, Status: "pass", FinishedAt: now.Add(-590 * time.Second), Samples: []OpenAIEvalSampleRecord{{Valid: true, Answer: "21"}}}}
	summary, err := s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, "evidence_expiry", summary.NextEvaluationReason)
	require.Equal(t, 10*time.Second, summary.NextEvaluationAt.Sub(summary.EvaluatedAt))
	now = now.Add(11 * time.Second)
	_, err = s.ranking.evaluate(context.Background(), "evidence_expiry", false)
	require.NoError(t, err)
	require.False(t, rankingDimension(t, s, 7, "gpt-6.1-sol", "").Accounts[0].Factors.Quality.Known)
	before := s.ranking.current.summary.EvaluationID
	accounts.err = context.DeadlineExceeded
	_, err = s.EvaluateScheduling(context.Background(), 1)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Equal(t, before, s.ranking.current.summary.EvaluationID)
}

func TestOpenAIRankingMetricsCollectedWithEffectsAndAdvancedOff(t *testing.T) {
	s, repo, accounts, gateway := rankingHarness(t)
	repo.config = OpenAIEvalConfig{Revision: 2}
	_, err := s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	ttft := 250
	gateway.ReportOpenAIAccountScheduleResultForRequest(&accounts.items[0], "mapped", &OpenAIForwardResult{Model: "gpt-6.1-sol", RequestedReasoningEffort: rankingPtr("high")}, true, &ttft)
	f := gateway.openaiAccountStats.rankingFactors(1, "gpt-6.1-sol", "high", time.Now())
	require.True(t, f.ErrorRate.Known)
	require.Equal(t, int64(1), f.ErrorRate.SampleCount)
	require.Equal(t, 250.0, *f.TTFT.MS)
	require.False(t, gateway.openaiAccountStats.rankingFactors(1, "gpt-6.1-sol", "", time.Now()).ErrorRate.Known)
}
