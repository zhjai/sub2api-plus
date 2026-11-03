package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenAIEvalQualityCountsFromRunIgnorePhysicalRetries(t *testing.T) {
	run := &OpenAIEvalRun{DataVersion: OpenAIEvalQualityDataVersion, TestType: OpenAIEvalTypeCandy, Status: "warning", RequestCount: 9,
		Samples: []OpenAIEvalSampleRecord{
			{Valid: true, Answer: "21", Attempts: 3, AttemptErrors: []OpenAIEvalAttemptError{{Code: "server_error"}}},
			{Valid: true, Answer: "29", Attempts: 3},
			{Valid: true, Answer: "Wrong answer", Attempts: 3},
		}}
	counts := openAIEvalQualityCountsFromRun(run)
	require.Equal(t, OpenAIEvalQualityCounts{3, 1, 0}, counts)
	require.InDelta(t, 1.0/3, counts.Ratio(), 1e-12)
	for _, status := range []string{"insufficient", "error", "running", "cancelled"} {
		run.Status = status
		require.Zero(t, openAIEvalQualityCountsFromRun(run))
	}
	run.Status, run.Error = "warning", "http_503"
	require.Zero(t, openAIEvalQualityCountsFromRun(run))
}

func TestOpenAIEvalQualityCountsFromRunIdentityAndStateProbe(t *testing.T) {
	run := &OpenAIEvalRun{TestType: OpenAIEvalTypeFingerprint, Status: "suspected_normal",
		Outcome: OpenAIEvalOutcome{Fingerprint: &OpenAIEvalFingerprintResult{NearestModel: "gpt-6.1-sol", ValidSamples: 60}}}
	require.Zero(t, openAIEvalQualityCountsFromRun(run), "legacy fingerprints remain history only")
	run.Status, run.Outcome.Fingerprint.NearestModel = "warning", "gpt-6-luna"
	require.Zero(t, openAIEvalQualityCountsFromRun(run))
	run.TestType, run.Status = OpenAIEvalTypeModelTrace, "suspected_normal"
	run.Outcome.ModelTrace = &OpenAIEvalModelTraceResult{Prediction: "gpt-6.1-sol", UsedOutputs: 3,
		Samples: []OpenAIEvalModelTraceSample{{Valid: true}, {Valid: true}, {Valid: true}}}
	require.Zero(t, openAIEvalQualityCountsFromRun(run), "retired ModelTrace cannot affect scheduling")
	run.Outcome.ModelTrace.Samples[2].Error = "http_503"
	require.Zero(t, openAIEvalQualityCountsFromRun(run))
	run.TestType = OpenAIEvalTypeStateProbe
	require.Zero(t, openAIEvalQualityCountsFromRun(run))
	require.Zero(t, openAIEvalQualityCountsFromRun(nil))
}

func TestOpenAIEvalQualityCountsPreservePartialAttributionVerdict(t *testing.T) {
	for _, prediction := range []string{"gpt-6.1-sol", "gpt-6-luna"} {
		t.Run(prediction, func(t *testing.T) {
			run := &OpenAIEvalRun{TestType: OpenAIEvalTypeModelTrace, Status: "attributed", Error: "upstream_error",
				Outcome: OpenAIEvalOutcome{ModelTrace: &OpenAIEvalModelTraceResult{
					Prediction: prediction, UsedOutputs: 1,
					Samples: []OpenAIEvalModelTraceSample{{Error: "http_502"}, {Valid: true}, {Error: "http_502"}},
				}}}
			require.Zero(t, openAIEvalQualityCountsFromRun(run))
			require.Equal(t, "attributed", run.Status, "scoring must not rewrite persisted diagnostics")
			require.Equal(t, "upstream_error", run.Error)
			for _, status := range []string{"error", "insufficient", "running", "cancelled"} {
				run.Status = status
				require.Zero(t, openAIEvalQualityCountsFromRun(run))
			}
			run.Status = "suspected_normal"
			run.Outcome.ModelTrace.UsedOutputs = 0
			require.Zero(t, openAIEvalQualityCountsFromRun(run))
		})
	}
}

func TestOpenAIEvalQualityManualPartialVerdictPersistsWithoutHidingErrors(t *testing.T) {
	enableQualityEffects(t)
	accounts := &qualityAccountRepository{account: &Account{ID: 17, Extra: map[string]any{}}}
	s := &OpenAIEvalService{accounts: accounts, repo: &qualityLeaseRepository{}}
	now := time.Now().UTC().Add(-time.Second)
	runValue := modelDimensionFingerprint(635, "gpt-6.1-sol", "high", true, 1, now)
	runValue.AccountID, runValue.Error = 17, "upstream_error"
	run := &runValue
	require.NoError(t, s.recordOpenAIEvalQualityResult(context.Background(), 635, run))
	quality, known := ReadOpenAIEvalQualityFromAccount(accounts.account, run.RequestedModel, run.ReasoningEffort, now, OpenAIEvalTypeFingerprint)
	require.True(t, known)
	require.Equal(t, "manual", quality.TriggerSource)
	require.Equal(t, "pass", quality.OutcomeStatus)
	require.Equal(t, 1, quality.PassCount)
	require.Zero(t, quality.SuspectedPassCount)
	require.Equal(t, 1., quality.Ratio())
	require.Equal(t, "upstream_error", run.Error)
	// A newer no-output result invalidates the old verdict across both sources.
	run.TriggerSource, run.Status = "scheduled", "insufficient"
	run.FinishedAt = now.Add(time.Millisecond)
	run.Outcome.LMFingerprint.Attribution.Used = 0
	require.NoError(t, s.recordOpenAIEvalQualityResult(context.Background(), 636, run))
	_, known = ReadOpenAIEvalQualityFromAccount(accounts.account, run.RequestedModel, run.ReasoningEffort, now.Add(time.Second), OpenAIEvalTypeFingerprint)
	require.False(t, known)
}
