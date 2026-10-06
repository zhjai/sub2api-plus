package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenAIEvalEvidenceIndependentFromRoutingEffects(t *testing.T) {
	s, repo, accounts := setupQualityRefreshTest(t)
	repo.config.EffectsEnabled = false
	repo.config.SchedulingPolicy = OpenAIEvalSchedulingPolicyAvoidDegradation
	SetOpenAIEvalSchedulingPolicySnapshot(&repo.config)

	run := &OpenAIEvalRun{AccountID: 17, TestType: OpenAIEvalTypeCandy, RequestedModel: "gpt-6.1-sol", ReasoningEffort: "high",
		DataVersion: OpenAIEvalQualityDataVersion, TriggerSource: "manual", Status: "pass", FinishedAt: time.Now().Add(-time.Second)}
	require.NoError(t, s.recordOpenAIEvalQuality(context.Background(), 200, run, OpenAIEvalQualityCounts{1, 1, 0}))
	quality, ok := ReadOpenAIEvalQualityFromAccount(accounts.accounts[17], run.RequestedModel, run.ReasoningEffort, time.Now())
	require.True(t, ok, "system-default routing must not hide collected evidence")
	require.Equal(t, int64(200), quality.RunID)

	result, err := s.refreshOpenAIEvalQuality(context.Background(), true)
	require.NoError(t, err)
	require.Equal(t, 1, result.RouteCount)
	assessment, ok := openAIEvalQualitySnapshots.lookup(17, run.RequestedModel, run.ReasoningEffort, time.Now())
	require.True(t, ok)
	require.Equal(t, 1.0, assessment.Ratio())
	require.Empty(t, OpenAIEvalSchedulingPolicyForRequest(run.RequestedModel, run.ReasoningEffort), "evidence collection must not activate a saved draft policy")
	// A production config save that disables effects must retain the evidence
	// cache while returning production policy selection to the legacy path.
	updated := repo.config
	updated.EffectsEnabled = true
	updated.SchedulingPolicy = OpenAIEvalSchedulingPolicyAvoidDegradation
	updated.Revision++
	require.NoError(t, openAIEvalQualitySnapshots.configure(&updated))
	updated.EffectsEnabled = false
	updated.Revision++
	require.NoError(t, openAIEvalQualitySnapshots.configure(&updated))
	_, ok = openAIEvalQualitySnapshots.lookup(17, run.RequestedModel, run.ReasoningEffort, time.Now())
	require.True(t, ok, "effects-only config changes must not clear evidence cache")
	require.Equal(t, OpenAIEvalSchedulingPolicyLegacy, OpenAIEvalSchedulingPolicyForRequest(run.RequestedModel, run.ReasoningEffort))
	scheduler := openAIResetTestScheduler(0)
	plan := scheduler.buildOpenAIAccountLoadPlan(context.Background(), OpenAIAccountScheduleRequest{
		RequestedModel: run.RequestedModel, RequestedReasoningEffort: run.ReasoningEffort,
		SchedulingPolicy: OpenAIEvalSchedulingPolicyAvoidDegradation,
	}, []*Account{accounts.accounts[17]}, nil)
	require.False(t, plan.qualityFirst)
	require.Len(t, plan.candidates, 1)
	require.NotNil(t, plan.candidates[0].quality, "trace may show evidence without changing routing")
	require.Zero(t, plan.candidates[0].qualityContribution)

	SetOpenAIEvalEffectsEnabled(true)
	SetOpenAIEvalEffectsEnabled(false)
	_, ok = openAIEvalQualitySnapshots.lookup(17, run.RequestedModel, run.ReasoningEffort, time.Now())
	require.True(t, ok, "changing routing effects alone must not erase quality evidence")
}

func TestOpenAIEvalHardHealthIndependentFromRoutingEffects(t *testing.T) {
	s, repo, accounts := setupQualityRefreshTest(t)
	repo.config.EffectsEnabled = false
	SetOpenAIEvalEffectsEnabled(false)
	account := accounts.accounts[17]
	s.recordRouteHealth(context.Background(), 17, "gpt-6.1-sol", "high", true, "http_503")
	s.recordRouteHealth(context.Background(), 17, "gpt-6.1-sol", "high", true, "http_503")
	_, active := ReadOpenAIEvalRouteHealthFromAccount(account, "gpt-6.1-sol", "high", time.Now())
	require.True(t, active)
	scheduler := &defaultOpenAIAccountScheduler{}
	compatible, reason := scheduler.isAccountRequestCompatibleReason(context.Background(), account,
		OpenAIAccountScheduleRequest{RequestedModel: "gpt-6.1-sol", RequestedReasoningEffort: "high"})
	require.False(t, compatible)
	require.Equal(t, "evaluation_hard_failure", reason)
}
