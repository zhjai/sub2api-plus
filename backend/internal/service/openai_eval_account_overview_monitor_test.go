//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type overviewFailingMonitorRepo struct{ ChannelMonitorRepository }

func (overviewFailingMonitorRepo) List(context.Context, ChannelMonitorListParams) ([]*ChannelMonitor, int64, error) {
	return nil, 0, errors.New("synthetic monitoring read failure")
}

type overviewFailingQualityRepo struct{ *rankingTestRepo }

func (overviewFailingQualityRepo) LatestScheduledRuns(context.Context, []OpenAIEvalEvidenceKey) ([]OpenAIEvalRun, error) {
	return nil, errors.New("synthetic quality read failure")
}

type overviewReadMonitorRepo struct {
	ChannelMonitorRepository
	before func()
}

func (r overviewReadMonitorRepo) List(context.Context, ChannelMonitorListParams) ([]*ChannelMonitor, int64, error) {
	r.before()
	return nil, 0, nil
}

func TestOpenAIAccountOverviewChecksExpiryAfterEvidenceReads(t *testing.T) {
	s, _, accounts, gateway := rankingHarness(t)
	_, err := s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	repo := overviewReadMonitorRepo{before: func() {
		s.ranking.mu.Lock()
		s.ranking.current.deadline = time.Now()
		s.ranking.mu.Unlock()
	}}
	monitor := NewChannelMonitorService(repo, &duplicateChannelMonitorEncryptor{})
	monitor.SetRuntimeReader(channelMonitorRuntimeStub{rt: ChannelMonitorRuntime{Enabled: true, Mode: ChannelMonitorModeV1}})
	s.ranking.monitor = monitor
	scheduler := gateway.persistentOpenAIAccountScheduler().(*defaultOpenAIAccountScheduler)
	_, trace, err := scheduler.explicitRanking(context.Background(), OpenAIAccountScheduleRequest{GroupID: rankingPtr(int64(7)), Platform: PlatformOpenAI, RequestedModel: "gpt-6.1-sol"}, accounts.items, nil)
	require.NoError(t, err)
	require.Equal(t, "live_fallback", trace.RankingBasis)
	require.Equal(t, "snapshot_expired", *trace.RankingFallbackReason)
}

func TestOpenAIAccountOverviewProbePrecedenceAliasesAndColdGuards(t *testing.T) {
	s, _, accounts, gateway := rankingHarness(t)
	now := time.Now()
	for i := range accounts.items {
		accounts.items[i].Credentials = map[string]any{"api_key": "synthetic-key", "base_url": "https://synthetic.invalid/v1",
			"model_mapping": map[string]any{"alias-one": "upstream", "alias-two": "upstream", "only-config": "upstream"}}
	}
	mrepo := &rankingMonitorRepo{
		monitors: []*ChannelMonitor{{ID: 1, Enabled: true, Provider: PlatformOpenAI, Endpoint: "https://synthetic.invalid/v1", APIKey: "OLD:synthetic-key", PrimaryModel: "upstream", IntervalSeconds: 300, UpdatedAt: now.Add(-time.Hour)}},
		rows:     []*ChannelMonitorHistoryEntry{{Model: "upstream", Status: MonitorStatusFailed, CheckedAt: now.Add(-time.Minute), LatencyMs: rankingPtr(5432), PingLatencyMs: rankingPtr(99)}},
	}
	monitor := NewChannelMonitorService(mrepo, &duplicateChannelMonitorEncryptor{})
	monitor.SetRuntimeReader(channelMonitorRuntimeStub{rt: ChannelMonitorRuntime{Enabled: true, Mode: ChannelMonitorModeV1}})
	s.ranking.monitor = monitor
	overviewMetric(t, gateway.openaiAccountStats, 1, "alias-one", "", .02, 100, 120)
	_, err := s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	result, err := s.SchedulingAccountOverview(OpenAIEvalRankingFilter{})
	require.NoError(t, err)
	one, two := overviewRow(t, result, 1), overviewRow(t, result, 2)
	require.Len(t, one.Models, 1)
	require.Equal(t, "alias-one", one.Models[0].RequestedModel)
	require.InDelta(t, .02, *one.Factors.ErrorRate.Value, 1e-12)
	require.Equal(t, "request_ewma_account_model_effort", *one.Models[0].Factors.ErrorRate.Source)
	require.Len(t, two.Models, 1, "probe does not manufacture votes for configured aliases")
	require.Equal(t, "upstream", two.Models[0].RequestedModel)
	require.Equal(t, 1., *two.Factors.ErrorRate.Value)
	require.Equal(t, "v1_matched_probe", *two.Models[0].Factors.ErrorRate.Source)
	require.Nil(t, two.Models[0].Factors.TTFT.MS)
	require.Equal(t, .9, two.Models[0].Factors.TTFT.Score)
	require.Equal(t, 5432, *two.Models[0].Factors.Monitoring[0].LatencyMS)
	require.Nil(t, two.Factors.Quality.Ratio)
	scheduler := gateway.persistentOpenAIAccountScheduler().(*defaultOpenAIAccountScheduler)
	req := OpenAIAccountScheduleRequest{GroupID: rankingPtr(int64(7)), Platform: PlatformOpenAI, RequestedModel: "alias-two"}
	_, trace, err := scheduler.explicitRanking(context.Background(), req, accounts.items, nil)
	require.NoError(t, err)
	require.NotEqual(t, "overview_prior", trace.RankingBasis, "exact mapped probe is warm evidence")
	mrepo.rows[0].CheckedAt = now.Add(-time.Hour)
	_, trace, err = scheduler.explicitRanking(context.Background(), req, accounts.items, nil)
	require.NoError(t, err)
	require.NotEqual(t, "overview_prior", trace.RankingBasis, "expired exact probe is not a cold route")
	mrepo.rows = nil
	_, trace, err = scheduler.explicitRanking(context.Background(), req, accounts.items, nil)
	require.NoError(t, err)
	require.Equal(t, "overview_prior", trace.RankingBasis)
	monitor.repo = overviewFailingMonitorRepo{}
	_, trace, err = scheduler.explicitRanking(context.Background(), req, accounts.items, nil)
	require.NoError(t, err)
	require.Equal(t, "live_fallback", trace.RankingBasis)
	require.Equal(t, "monitoring_unavailable", *trace.RankingFallbackReason)
}

func TestOpenAIAccountOverviewQualityReadFailureAndNewestErrorInvalidateSnapshot(t *testing.T) {
	s, repo, accounts, gateway := rankingHarness(t)
	repo.config.Revision++
	repo.config.SchedulingPolicy = OpenAIEvalSchedulingPolicyAvoidDegradation
	repo.config.Accounts = []OpenAIEvalAccountConfig{{AccountID: 2, RequestedModel: "gpt-6.1-sol", CandySchedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300}}}
	repo.runs = []OpenAIEvalRun{overviewQualityRun(1, 2, "gpt-6.1-sol", "", OpenAIEvalTypeCandy, true, time.Now())}
	_, err := s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	scheduler := gateway.persistentOpenAIAccountScheduler().(*defaultOpenAIAccountScheduler)
	req := OpenAIAccountScheduleRequest{GroupID: rankingPtr(int64(7)), Platform: PlatformOpenAI, RequestedModel: "gpt-6.1-sol"}
	rows, trace, err := scheduler.explicitRanking(context.Background(), req, accounts.items, nil)
	require.NoError(t, err)
	require.Equal(t, "snapshot", trace.RankingBasis)
	require.Equal(t, int64(2), rows[0].AccountID)
	newest := repo.runs[0]
	newest.ID, newest.Status, newest.FinishedAt = 2, "error", time.Now().Add(-time.Second)
	repo.runs = append(repo.runs, newest)
	rows, trace, err = scheduler.explicitRanking(context.Background(), req, accounts.items, nil)
	require.NoError(t, err)
	require.Equal(t, "live_fallback", trace.RankingBasis)
	require.Equal(t, "quality_evidence_updated", *trace.RankingFallbackReason)
	for _, row := range rows {
		require.Nil(t, row.Factors.Quality.Ratio)
	}
	s.repo = overviewFailingQualityRepo{repo}
	rows, trace, err = scheduler.explicitRanking(context.Background(), req, accounts.items, nil)
	require.NoError(t, err)
	require.Equal(t, "live_fallback", trace.RankingBasis)
	require.Equal(t, "quality_evidence_unavailable", *trace.RankingFallbackReason)
	for _, row := range rows {
		require.Nil(t, row.Factors.Quality.Ratio)
		require.Nil(t, row.OverviewPrior)
	}
}

func TestOpenAIAccountOverviewCompositeEvidenceDoesNotPoolEndpointProbes(t *testing.T) {
	s, repo, accounts, _ := rankingHarness(t)
	now := time.Now()
	s.ranking.groups = &rankingTestGroups{items: []Group{{ID: 7, Platform: PlatformComposite, Status: StatusActive}}}
	s.ranking.composite = &rankingCompositeRoutes{routes: []CompositeModelRoute{
		{PublicModel: "gpt-6.1-sol", MatchType: CompositeRouteMatchExact, TargetPlatform: PlatformOpenAI, UpstreamModel: "model-a", Endpoint: CompositeRouteEndpointResponses, Enabled: true},
		{PublicModel: "gpt-6.1-sol", MatchType: CompositeRouteMatchExact, TargetPlatform: PlatformOpenAI, UpstreamModel: "model-b", Endpoint: CompositeRouteEndpointChatCompletions, Enabled: true},
	}}
	for i := range accounts.items {
		accounts.items[i].Credentials = map[string]any{"api_key": "synthetic-key", "base_url": "https://synthetic.invalid/v1",
			"model_mapping": map[string]any{"model-a": "model-a", "model-b": "model-b"}}
	}
	mrepo := &rankingMonitorRepo{monitors: []*ChannelMonitor{{ID: 1, Enabled: true, Provider: PlatformOpenAI, Endpoint: "https://synthetic.invalid/v1", APIKey: "OLD:synthetic-key", PrimaryModel: "model-a", ExtraModels: []string{"model-b"}, IntervalSeconds: 300, UpdatedAt: now.Add(-time.Hour)}},
		rows: []*ChannelMonitorHistoryEntry{{Model: "model-a", Status: MonitorStatusOperational, CheckedAt: now.Add(-time.Minute)}, {Model: "model-b", Status: MonitorStatusFailed, CheckedAt: now.Add(-time.Minute)}}}
	monitor := NewChannelMonitorService(mrepo, &duplicateChannelMonitorEncryptor{})
	monitor.SetRuntimeReader(channelMonitorRuntimeStub{rt: ChannelMonitorRuntime{Enabled: true, Mode: ChannelMonitorModeV1}})
	s.ranking.monitor = monitor
	repo.config.Revision++
	repo.config.Accounts = []OpenAIEvalAccountConfig{{AccountID: 1, RequestedModel: "gpt-6.1-sol", CandySchedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 300}}}
	repo.runs = []OpenAIEvalRun{overviewQualityRun(1, 1, "gpt-6.1-sol", "", OpenAIEvalTypeCandy, true, now)}
	_, err := s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	result, err := s.SchedulingAccountOverview(OpenAIEvalRankingFilter{})
	require.NoError(t, err)
	row := overviewRow(t, result, 1)
	require.Len(t, row.Models, 3)
	public := row.Models[0]
	require.Equal(t, "gpt-6.1-sol", public.RequestedModel)
	require.Equal(t, []string{"model-a", "model-b"}, public.UpstreamModels)
	require.False(t, public.Factors.ErrorRate.Known)
	require.Nil(t, public.Factors.ErrorRate.Value)
	require.Equal(t, []string{"scheduled_quality"}, public.Sources)
	require.Equal(t, 0., *row.Models[1].Factors.ErrorRate.Value)
	require.Equal(t, 1., *row.Models[2].Factors.ErrorRate.Value)
}
