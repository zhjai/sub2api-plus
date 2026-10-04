package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenAIRankingV1EvidenceIsMatchedFreshAndNotTTFTOrQuality(t *testing.T) {
	now := time.Now()
	evidence := []openAIRankingMonitorEvidence{{accountID: 7, model: "gpt-6.1-sol", effort: "", monitorID: 2, window: 10 * time.Minute, rows: []*ChannelMonitorHistoryEntry{
		{Model: "gpt-6.1-sol", Status: MonitorStatusDegraded, LatencyMs: rankingPtr(12000), PingLatencyMs: rankingPtr(90), CheckedAt: now.Add(-time.Minute)},
		{Model: "gpt-6.1-sol", Status: MonitorStatusFailed, CheckedAt: now.Add(-2 * time.Minute)},
		{Model: "gpt-6.1-sol", Status: MonitorStatusFailed, CheckedAt: now.Add(-time.Hour)},
	}}}
	f := emptyOpenAIEvalRankingFactors()
	applyRankingMonitor(&f, evidence, 7, "gpt-6.1-sol", "", now)
	require.True(t, f.ErrorRate.Known)
	require.Equal(t, .5, *f.ErrorRate.Value)
	require.Equal(t, int64(2), f.ErrorRate.SampleCount)
	require.Equal(t, "v1_matched_probe", *f.ErrorRate.Source)
	require.False(t, f.TTFT.Known)
	require.Nil(t, f.TTFT.MS)
	require.False(t, f.Quality.Known)
	require.Len(t, f.Monitoring, 2)
	for _, effort := range []string{"high", "xhigh"} {
		f := emptyOpenAIEvalRankingFactors()
		applyRankingMonitor(&f, evidence, 7, "gpt-6.1-sol", effort, now)
		require.False(t, f.ErrorRate.Known)
	}
	f = emptyOpenAIEvalRankingFactors()
	applyRankingMonitor(&f, evidence, 8, "gpt-6.1-sol", "", now)
	require.False(t, f.ErrorRate.Known)
	f = emptyOpenAIEvalRankingFactors()
	f.ErrorRate = OpenAIEvalRankingErrorRate{OpenAIEvalFactorMeta: rankingKnown(.9, now), Value: rankingPtr(.1), SampleCount: 3, Source: rankingPtr("request_ewma_account_model_effort")}
	applyRankingMonitor(&f, evidence, 7, "gpt-6.1-sol", "", now)
	require.Equal(t, .1, *f.ErrorRate.Value)
}

func TestOpenAIRankingV1AssociationModeAndEffortControls(t *testing.T) {
	a := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://example.invalid/v1"}}
	m := &ChannelMonitor{ID: 2, Enabled: true, AccountID: rankingPtr(int64(7)), Provider: PlatformOpenAI, Endpoint: "https://example.invalid/v1", CheckMode: MonitorCheckModeProbe}
	require.True(t, rankingMonitorMatchesAccount(m, a))
	for _, mutate := range []func(*ChannelMonitor){func(m *ChannelMonitor) { m.AccountID = nil }, func(m *ChannelMonitor) { m.AccountID = rankingPtr(int64(8)) }, func(m *ChannelMonitor) { m.Endpoint = "https://unrelated.invalid/v1" }, func(m *ChannelMonitor) { m.CheckMode = MonitorCheckModeQuota }, func(m *ChannelMonitor) { m.Provider = PlatformGrok }, func(m *ChannelMonitor) { m.Enabled = false }} {
		copy := *m
		mutate(&copy)
		require.False(t, rankingMonitorMatchesAccount(&copy, a))
	}
	effort, ok := rankingMonitorEffort(m)
	require.True(t, ok)
	require.Empty(t, effort)
	m.BodyOverrideMode = "merge"
	m.BodyOverride = map[string]any{"reasoning": map[string]any{"effort": "high"}}
	effort, ok = rankingMonitorEffort(m)
	require.True(t, ok)
	require.Equal(t, "high", effort)
	m.BodyOverride["model"] = "other"
	_, ok = rankingMonitorEffort(m)
	require.False(t, ok)
	m.BodyOverrideMode = "replace"
	_, ok = rankingMonitorEffort(m)
	require.False(t, ok)
}
