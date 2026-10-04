//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type rankingMonitorRepo struct {
	ChannelMonitorRepository
	monitors     []*ChannelMonitor
	rows         []*ChannelMonitorHistoryEntry
	historyCalls int
}

func (r *rankingMonitorRepo) List(context.Context, ChannelMonitorListParams) ([]*ChannelMonitor, int64, error) {
	return r.monitors, int64(len(r.monitors)), nil
}

func (r *rankingMonitorRepo) ListHistory(_ context.Context, _ int64, model string, _ int) ([]*ChannelMonitorHistoryEntry, error) {
	r.historyCalls++
	var rows []*ChannelMonitorHistoryEntry
	for _, row := range r.rows {
		if model == "" || row.Model == model {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

type rankingCompositeRoutes struct {
	CompositeModelRouteRepository
	routes []CompositeModelRoute
}

func (r *rankingCompositeRoutes) ListByGroup(context.Context, int64, bool) ([]CompositeModelRoute, error) {
	return r.routes, nil
}

func TestOpenAIRankingV1CompositeEndpointModelsDoNotShareEvidence(t *testing.T) {
	s, repo, accounts, gateway := rankingHarness(t)
	repo.config.SchedulingPolicy = OpenAIEvalSchedulingPolicyCustomBalance
	repo.config.CustomBalance = OpenAIEvalPolicyWeights{ErrorRate: 1}
	repo.config.Revision++
	s.ranking.groups = &rankingTestGroups{items: []Group{{ID: 7, Name: "composite", Platform: PlatformComposite, Status: StatusActive}}}
	s.ranking.composite = &rankingCompositeRoutes{routes: []CompositeModelRoute{
		{PublicModel: "gpt-6.1-sol", MatchType: CompositeRouteMatchExact, TargetPlatform: PlatformOpenAI, UpstreamModel: "model-a", Endpoint: CompositeRouteEndpointResponses, Enabled: true},
		{PublicModel: "gpt-6.1-sol", MatchType: CompositeRouteMatchExact, TargetPlatform: PlatformOpenAI, UpstreamModel: "model-b", Endpoint: CompositeRouteEndpointChatCompletions, Enabled: true},
		{PublicModel: "gpt-6.1-sol", MatchType: CompositeRouteMatchExact, TargetPlatform: PlatformOpenAI, UpstreamModel: "model-a", Endpoint: CompositeRouteEndpointImages, Enabled: true},
	}}
	for i := range accounts.items {
		accounts.items[i].Credentials = map[string]any{"api_key": "synthetic-composite-key", "base_url": "https://ranking.invalid/v1",
			"model_mapping": map[string]any{"model-a": "model-a", "model-b": "model-b"}}
	}
	now := time.Now().UTC()
	monitorRepo := &rankingMonitorRepo{monitors: []*ChannelMonitor{{ID: 1, Enabled: true, Provider: PlatformOpenAI, Endpoint: "https://ranking.invalid/v1",
		APIKey: "OLD:synthetic-composite-key", PrimaryModel: "model-a", ExtraModels: []string{"model-b"}, IntervalSeconds: 300, UpdatedAt: now.Add(-time.Hour)}},
		rows: []*ChannelMonitorHistoryEntry{
			{Model: "model-a", Status: MonitorStatusOperational, CheckedAt: now.Add(-time.Minute)},
			{Model: "model-b", Status: MonitorStatusFailed, CheckedAt: now.Add(-time.Minute)},
		}}
	s.ranking.monitor = NewChannelMonitorService(monitorRepo, &duplicateChannelMonitorEncryptor{})
	s.ranking.monitor.SetRuntimeReader(channelMonitorRuntimeStub{rt: ChannelMonitorRuntime{Enabled: true, Mode: ChannelMonitorModeV1}})
	_, err := s.EvaluateScheduling(context.Background(), 1)
	require.NoError(t, err)
	dim := rankingDimension(t, s, 7, "gpt-6.1-sol", "")
	require.Equal(t, OpenAIEvalSchedulingPolicyCustomBalance, dim.Policy)
	require.Equal(t, OpenAIEvalRankingWeights{ErrorRate: 1}, dim.Weights)
	require.Equal(t, "live_fallback", dim.CoverageStatus)
	require.Equal(t, "selection_model_variants", *dim.FallbackReason)
	require.Nil(t, dim.ValidUntil)
	require.Empty(t, dim.Accounts, "do not publish pooled operational scores for different endpoint models")
	scheduler := gateway.persistentOpenAIAccountScheduler().(*defaultOpenAIAccountScheduler)
	for _, model := range []string{"model-a", "model-b"} {
		req := OpenAIAccountScheduleRequest{GroupID: rankingPtr(int64(7)), Platform: PlatformOpenAI, RequestedModel: model, ClientRequestedModel: "gpt-6.1-sol"}
		rows, trace, err := scheduler.explicitRanking(context.Background(), req, accounts.items, nil)
		require.NoError(t, err)
		require.Equal(t, "live_fallback", trace.RankingBasis)
		require.Equal(t, "selection_model_variants", *trace.RankingFallbackReason)
		require.Len(t, rows, 2)
		for _, row := range rows {
			want := 0.
			if model == "model-b" {
				want = 1.
			}
			require.Equal(t, want, *row.Factors.ErrorRate.Value)
			require.Equal(t, int64(1), row.Factors.ErrorRate.SampleCount)
		}
	}
}

func TestOpenAIRankingV1IndependentProbeIdentityAndConfigFreshness(t *testing.T) {
	now := time.Now().UTC()
	account := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"api_key": "synthetic-ranking-key", "base_url": "https://ranking.invalid/v1",
		"model_mapping": map[string]any{"public-model": "upstream-model"},
	}}
	for _, tc := range []struct {
		name   string
		mutate func(*ChannelMonitor, *duplicateChannelMonitorEncryptor)
		want   bool
	}{
		{name: "independent_probe", want: true},
		{name: "linked_probe", mutate: func(m *ChannelMonitor, _ *duplicateChannelMonitorEncryptor) { m.AccountID = rankingPtr(int64(7)) }, want: true},
		{name: "linked_wrong_key", mutate: func(m *ChannelMonitor, _ *duplicateChannelMonitorEncryptor) {
			m.AccountID = rankingPtr(int64(7))
			m.APIKey = "OLD:another-key"
		}},
		{name: "independent_wrong_key", mutate: func(m *ChannelMonitor, _ *duplicateChannelMonitorEncryptor) { m.APIKey = "OLD:another-key" }},
		{name: "wrong_endpoint", mutate: func(m *ChannelMonitor, _ *duplicateChannelMonitorEncryptor) { m.Endpoint = "https://other.invalid/v1" }},
		{name: "wrong_association", mutate: func(m *ChannelMonitor, _ *duplicateChannelMonitorEncryptor) { m.AccountID = rankingPtr(int64(8)) }},
		{name: "quota_only", mutate: func(m *ChannelMonitor, _ *duplicateChannelMonitorEncryptor) { m.CheckMode = MonitorCheckModeQuota }},
		{name: "decrypt_error", mutate: func(_ *ChannelMonitor, e *duplicateChannelMonitorEncryptor) {
			e.decryptErr = errors.New("synthetic decryption failure")
		}},
		{name: "changed_configuration", mutate: func(m *ChannelMonitor, _ *duplicateChannelMonitorEncryptor) { m.UpdatedAt = now }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			monitor := &ChannelMonitor{ID: 2, Enabled: true, Provider: PlatformOpenAI, Endpoint: "https://ranking.invalid/v1", APIKey: "OLD:synthetic-ranking-key",
				PrimaryModel: "upstream-model", CheckMode: MonitorCheckModeProbe, APIMode: MonitorAPIModeResponses, IntervalSeconds: 300, UpdatedAt: now.Add(-time.Hour)}
			encryptor := &duplicateChannelMonitorEncryptor{}
			if tc.mutate != nil {
				tc.mutate(monitor, encryptor)
			}
			repo := &rankingMonitorRepo{monitors: []*ChannelMonitor{monitor}, rows: []*ChannelMonitorHistoryEntry{
				{Model: "upstream-model", Status: MonitorStatusFailed, CheckedAt: now.Add(-time.Minute)},
			}}
			svc := NewChannelMonitorService(repo, encryptor)
			svc.SetRuntimeReader(channelMonitorRuntimeStub{rt: ChannelMonitorRuntime{Enabled: true, Mode: ChannelMonitorModeV1}})
			r := &OpenAIEvalRankingService{monitor: svc}
			evidence, err := r.monitorEvidence(context.Background(), map[int64]*Account{7: account})
			require.NoError(t, err)
			f := emptyOpenAIEvalRankingFactors()
			applyRankingMonitor(&f, evidence, 7, account.GetMappedModel("public-model"), "", now)
			require.Equal(t, tc.want, f.ErrorRate.Known)
			if tc.want {
				require.Equal(t, 1., *f.ErrorRate.Value)
				require.Equal(t, int64(1), f.ErrorRate.SampleCount)
				require.Equal(t, "v1_matched_probe", *f.ErrorRate.Source)
			}
			wrong := emptyOpenAIEvalRankingFactors()
			applyRankingMonitor(&wrong, evidence, 7, "public-model", "high", now)
			require.False(t, wrong.ErrorRate.Known)
			require.False(t, f.TTFT.Known)
			require.False(t, f.Quality.Known)
		})
	}
}

func TestOpenAIRankingV1ExpiryUsesEveryContributingWindow(t *testing.T) {
	now := time.Now().UTC()
	evidence := []openAIRankingMonitorEvidence{
		{accountID: 7, model: "upstream-model", window: 5 * time.Minute, monitorID: 1, rows: []*ChannelMonitorHistoryEntry{
			{Model: "upstream-model", Status: MonitorStatusFailed, CheckedAt: now.Add(-4 * time.Minute)},
		}},
		{accountID: 7, model: "upstream-model", window: 30 * time.Minute, monitorID: 2, rows: []*ChannelMonitorHistoryEntry{
			{Model: "upstream-model", Status: MonitorStatusOperational, CheckedAt: now.Add(-time.Minute)},
		}},
	}
	f := emptyOpenAIEvalRankingFactors()
	applyRankingMonitor(&f, evidence, 7, "upstream-model", "", now)
	require.Equal(t, .5, *f.ErrorRate.Value)
	require.Equal(t, now.Add(time.Minute), *rankingFactorExpiry(f, OpenAIEvalRankingWeights{ErrorRate: 1}, OpenAIEvalSchedulingPolicyStabilityFirst))
	f = emptyOpenAIEvalRankingFactors()
	applyRankingMonitor(&f, evidence, 7, "upstream-model", "", now.Add(time.Minute))
	require.Equal(t, 0., *f.ErrorRate.Value)
	require.Equal(t, int64(1), f.ErrorRate.SampleCount)
}
