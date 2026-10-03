package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIEvalQualityCountsFromRunIgnorePhysicalRetries(t *testing.T) {
	run := &OpenAIEvalRun{TestType: OpenAIEvalTypeCandy, Status: "warning", RequestCount: 9,
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
	require.Equal(t, OpenAIEvalQualityCounts{60, 0, 60}, openAIEvalQualityCountsFromRun(run))
	run.Status, run.Outcome.Fingerprint.NearestModel = "warning", "gpt-6-luna"
	require.Equal(t, OpenAIEvalQualityCounts{60, 0, 0}, openAIEvalQualityCountsFromRun(run))
	run.TestType, run.Status = OpenAIEvalTypeModelTrace, "suspected_normal"
	run.Outcome.ModelTrace = &OpenAIEvalModelTraceResult{Prediction: "gpt-6.1-sol", UsedOutputs: 3,
		Samples: []OpenAIEvalModelTraceSample{{Valid: true}, {Valid: true}, {Valid: true}}}
	require.Equal(t, OpenAIEvalQualityCounts{3, 0, 3}, openAIEvalQualityCountsFromRun(run))
	run.Outcome.ModelTrace.Samples[2].Error = "http_503"
	require.Zero(t, openAIEvalQualityCountsFromRun(run))
	run.TestType = OpenAIEvalTypeStateProbe
	require.Zero(t, openAIEvalQualityCountsFromRun(run))
	require.Zero(t, openAIEvalQualityCountsFromRun(nil))
}
