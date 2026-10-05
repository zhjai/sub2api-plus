package service

import (
	"strings"
	"time"
)

func openAIEvalQualityAggregateFromRun(run OpenAIEvalRun, interval, refresh int, now time.Time) (OpenAIEvalQualityAggregate, bool) {
	normalizeOpenAIEvalAttributionRun(&run)
	q := OpenAIEvalQualityAggregate{
		OpenAIEvalQualityCounts: openAIEvalQualityCountsFromRun(&run), Version: OpenAIEvalQualityVersion, DataVersion: run.DataVersion,
		AccountID: run.AccountID, RequestedModel: openAIEvalQualityDimension(run.RequestedModel), ReasoningEffort: openAIEvalQualityDimension(run.ReasoningEffort),
		RunID: run.ID, TestType: run.TestType, TriggerSource: run.TriggerSource, OutcomeStatus: run.Status,
		AttributionRuleVersion: openAIEvalQualityAttributionRuleVersion(run.TestType),
		EvaluatedAt:            run.FinishedAt, ExpiresAt: run.FinishedAt.Add(openAIEvalQualityFreshness(interval, refresh)),
	}
	if run.TestType == OpenAIEvalTypeModelTrace && (run.Outcome.ModelTrace == nil || run.Outcome.ModelTrace.BankRevision != OpenAIEvalQualityModelTraceBankRevision) {
		return q, false
	}
	if run.TestType == OpenAIEvalTypeFingerprint {
		if run.Outcome.Fingerprint == nil || run.BaselineVersion != OpenAIEvalQualityBaselineVersion {
			return q, false
		}
		q.OutcomeStatus = OpenAIEvalIdentityQualityStatus(run.Outcome.Fingerprint.NearestModel)
	}
	return q, q.validFor(run.AccountID, run.RequestedModel, run.ReasoningEffort, now)
}

func openAIEvalQualityRunSourceSupported(source string) bool {
	return source == "manual" || source == "scheduled"
}

// Failed request attempts remain diagnostics. A final attribution based on
// valid outputs is still a quality verdict, even if other requests failed.
func openAIEvalQualityCountsFromRun(run *OpenAIEvalRun) OpenAIEvalQualityCounts {
	if run == nil {
		return OpenAIEvalQualityCounts{}
	}
	copy := *run
	normalizeOpenAIEvalAttributionRun(&copy)
	run = &copy
	switch run.Status {
	case "pass", "warning", "suspected_normal", "suspected_warning":
	default:
		return OpenAIEvalQualityCounts{}
	}
	var counts OpenAIEvalQualityCounts
	switch run.TestType {
	case OpenAIEvalTypeCandy:
		if run.Error != "" {
			return counts
		}
		for _, sample := range run.Samples {
			if !sample.Valid || sample.ErrorMessage != "" || strings.TrimSpace(sample.Answer) == "" {
				continue
			}
			counts.EvaluatedCount++
			answer := sample.Answer
			if sample.NormalizedAnswer != "" {
				answer = sample.NormalizedAnswer
			}
			if ScoreOpenAIEvalCandy(answer).Status == "pass" {
				counts.PassCount++
			}
		}
	case OpenAIEvalTypeFingerprint:
		result := run.Outcome.Fingerprint
		if result == nil || result.NearestModel == "" || result.ValidSamples <= 0 {
			return counts
		}
		counts.EvaluatedCount = result.ValidSamples
		if OpenAIEvalIdentityQualityStatus(result.NearestModel) == "suspected_normal" {
			counts.SuspectedPassCount = counts.EvaluatedCount
		}
	case OpenAIEvalTypeModelTrace:
		result := run.Outcome.ModelTrace
		if result == nil || result.Prediction == "" || result.UsedOutputs <= 0 {
			return counts
		}
		if !openAIEvalModelTraceUsedOutputsValid(result) {
			return counts
		}
		counts.EvaluatedCount = result.UsedOutputs
		status, _ := openAIEvalModelTraceVerdict(run.RequestedModel, result.Prediction)
		if status == "pass" {
			counts.PassCount = counts.EvaluatedCount
		} else if status == "suspected_normal" {
			counts.SuspectedPassCount = counts.EvaluatedCount
		}
	}
	return counts
}

func openAIEvalModelTraceUsedOutputsValid(result *OpenAIEvalModelTraceResult) bool {
	if result == nil || result.UsedOutputs <= 0 || result.UsedOutputs > OpenAIEvalModelTraceRequests {
		return false
	}
	if len(result.Samples) == 0 {
		return true
	}
	valid := 0
	for _, sample := range result.Samples {
		if sample.Valid && sample.Error == "" {
			valid++
		}
	}
	return valid == result.UsedOutputs
}
