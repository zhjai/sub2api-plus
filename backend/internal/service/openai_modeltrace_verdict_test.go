package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestModelTraceCurrentVerdictHistoryQualityAndRanking(t *testing.T) {
	for _, tc := range []struct {
		name, requested, predicted, want string
		pass, suspected                  int
	}{
		{"exact", "gpt-6.1-sol", "gpt-6.1-sol", "pass", 1, 0},
		{"case_space", " GPT-6.1-SOL ", " gpt-6.1-sol ", "pass", 1, 0},
		{"dated_variant", "gpt-6.1-sol", "gpt-6.1-sol-2026-10-01", "suspected_normal", 0, 1},
		{"other", "gpt-6-astra", "gpt-6.1-sol", "suspected_normal", 0, 1},
		{"mapped_is_not_public", "public-model", "gpt-6.1-sol", "suspected_normal", 0, 1},
		{"luna", "gpt-6.1-sol", "vendor/GPT-6-LUNA", "warning", 0, 0},
		{"luna_target", "gpt-6-luna", "gpt-6-luna", "warning", 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now().UTC()
			model := openAIEvalQualityDimension(tc.requested)
			run := OpenAIEvalRun{ID: 1, AccountID: 17, RequestedModel: tc.requested, UpstreamModel: tc.predicted,
				ReasoningEffort: "high", TestType: OpenAIEvalTypeModelTrace, Status: "attributed", Error: "http_502",
				TriggerSource: "manual", DataVersion: OpenAIEvalQualityDataVersion, FinishedAt: now.Add(-time.Second),
				Outcome: OpenAIEvalOutcome{Status: "attributed", Reason: "legacy_attribution", ModelTrace: &OpenAIEvalModelTraceResult{
					Prediction: tc.predicted, BankRevision: OpenAIEvalQualityModelTraceBankRevision, UsedOutputs: 1,
					Samples: []OpenAIEvalModelTraceSample{{Valid: true}, {Error: "http_502"}, {Error: "http_502"}},
				}}}
			require.Zero(t, openAIEvalQualityCountsFromRun(&run), "retired ModelTrace remains historical only")
			require.Equal(t, "attributed", run.Status)
			require.Equal(t, "legacy_attribution", run.Outcome.Reason)
			cfg := &OpenAIEvalConfig{Accounts: []OpenAIEvalAccountConfig{{AccountID: 17, RequestedModel: model, ReasoningEffort: "high",
				ModelTraceSchedule: OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 3600}}}}
			key := OpenAIEvalEvidenceKey{17, model, "high", OpenAIEvalTypeModelTrace}
			q := qualityFromLatestRuns(cfg, 17, model, "high", map[OpenAIEvalEvidenceKey]OpenAIEvalRun{key: run}, now)
			require.False(t, q.Known)
			require.Zero(t, q.Evaluated)
			normalizeOpenAIEvalAttributionRun(&run)
			require.Equal(t, tc.want, run.Status)
			require.Equal(t, "attributed", run.Outcome.Attribution.OriginalStatus)
			require.Equal(t, "legacy_attribution", run.Outcome.Attribution.OriginalReason)
			require.Equal(t, "legacy-unversioned", run.Outcome.Attribution.OriginalRuleVersion)
			require.Equal(t, OpenAIEvalModelTraceRuleVersion, run.Outcome.Attribution.RuleVersion)
			require.Equal(t, "http_502", run.Error)
			metadata := run.Outcome.Attribution
			normalizeOpenAIEvalAttributionRun(&run)
			require.Equal(t, tc.want, run.Status, "normalization must be idempotent")
			require.Equal(t, metadata, run.Outcome.Attribution)
			require.NotSame(t, metadata, run.Outcome.Attribution, "history copies must not mutate source metadata")
		})
	}
}

func TestDiagnosticOnlyRunCannotContributeQuality(t *testing.T) {
	now := time.Now().UTC()
	model := "gpt-6.1-sol"
	schedule := OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 3600}
	cfg := &OpenAIEvalConfig{Accounts: []OpenAIEvalAccountConfig{{AccountID: 17, RequestedModel: model, ReasoningEffort: "high", CandySchedule: schedule}}}
	key := OpenAIEvalEvidenceKey{17, model, "high", OpenAIEvalTypeCandy}
	run := OpenAIEvalRun{ID: 4, AccountID: 17, RequestedModel: model, ReasoningEffort: "high", TestType: OpenAIEvalTypeCandy,
		Status: "pass", TriggerSource: "manual", DataVersion: OpenAIEvalQualityDataVersion, FinishedAt: now,
		DiagnosticOnly: true, Samples: []OpenAIEvalSampleRecord{{Valid: true, Answer: "21"}}}
	quality := qualityFromLatestRuns(cfg, 17, model, "high", map[OpenAIEvalEvidenceKey]OpenAIEvalRun{key: run}, now)
	require.False(t, quality.Known)
	run.DiagnosticOnly = false
	quality = qualityFromLatestRuns(cfg, 17, model, "high", map[OpenAIEvalEvidenceKey]OpenAIEvalRun{key: run}, now)
	require.True(t, quality.Known)
	require.Equal(t, 1, quality.Pass)
}

// A migrated schedule selects the new detector; historic ModelTrace output
// must neither establish quality nor hide current model-level evidence.
func TestLegacyModelTraceMigrationRequiresCurrentDetectorEvidence(t *testing.T) {
	now := time.Now().UTC()
	model := "gpt-6.1-sol"
	schedule := OpenAIEvalSchedule{Enabled: true, IntervalSeconds: 3600}
	cfg := &OpenAIEvalConfig{Accounts: []OpenAIEvalAccountConfig{{AccountID: 1, RequestedModel: model, ReasoningEffort: "high", CandySchedule: schedule, ModelTraceSchedule: schedule}}}
	NormalizeOpenAIEvalDetectorConfig(cfg)
	require.False(t, cfg.Accounts[0].ModelTraceSchedule.Enabled)
	require.True(t, cfg.Accounts[0].FingerprintSchedule.Enabled)
	old := OpenAIEvalRun{ID: 2, AccountID: 1, RequestedModel: model, ReasoningEffort: "high", TestType: OpenAIEvalTypeModelTrace,
		Status: "attributed", TriggerSource: "manual", DataVersion: OpenAIEvalQualityDataVersion, FinishedAt: now,
		Outcome: OpenAIEvalOutcome{ModelTrace: &OpenAIEvalModelTraceResult{UsedOutputs: 1, Prediction: model, BankRevision: OpenAIEvalQualityModelTraceBankRevision}}}
	candy := modelDimensionCandy(1, model, "low", "29", now.Add(-time.Second))
	latest := map[OpenAIEvalEvidenceKey]OpenAIEvalRun{
		{1, model, "high", OpenAIEvalTypeModelTrace}: old,
		{1, model, "low", OpenAIEvalTypeCandy}:       candy,
	}
	q := qualityFromLatestRuns(cfg, 1, model, "max", latest, now)
	require.False(t, q.Known)
	require.Equal(t, 2, q.Selected)
	require.Equal(t, 1, q.Evaluated)
	require.Nil(t, q.Ratio)
	fp := modelDimensionFingerprint(3, model, "max", true, 3, now)
	latest[OpenAIEvalEvidenceKey{1, model, "max", OpenAIEvalTypeFingerprint}] = fp
	q = qualityFromLatestRuns(cfg, 1, model, "high", latest, now)
	require.True(t, q.Known)
	require.Equal(t, 1, q.Pass)
	require.Equal(t, .5, *q.Ratio, "one outcome per selected type, independent of request count")
	require.Equal(t, "attributed", old.Status, "raw history is immutable")
	require.Equal(t, model, old.Outcome.ModelTrace.Prediction)
}

func TestModelTraceInvalidEvidenceNeverBecomesNormal(t *testing.T) {
	for _, status := range []string{"running", "error", "insufficient", "cancelled", "attributed"} {
		run := OpenAIEvalRun{RequestedModel: "gpt-6.1-sol", TestType: OpenAIEvalTypeModelTrace, Status: status,
			Outcome: OpenAIEvalOutcome{ModelTrace: &OpenAIEvalModelTraceResult{Prediction: "gpt-6.1-sol", UsedOutputs: 2,
				Samples: []OpenAIEvalModelTraceSample{{Valid: true}, {Error: "http_502"}}}}}
		normalizeOpenAIEvalAttributionRun(&run)
		require.Equal(t, status, run.Status)
		require.Zero(t, openAIEvalQualityCountsFromRun(&run))
	}
}
