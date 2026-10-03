package service

import "strings"

// Persisted logical samples are scored once; request attempts are diagnostics,
// not extra observations of model quality.
func openAIEvalQualityCountsFromRun(run *OpenAIEvalRun) OpenAIEvalQualityCounts {
	if run == nil || run.Error != "" {
		return OpenAIEvalQualityCounts{}
	}
	switch run.Status {
	case "pass", "warning", "suspected_normal", "suspected_warning":
	default:
		return OpenAIEvalQualityCounts{}
	}
	var counts OpenAIEvalQualityCounts
	switch run.TestType {
	case OpenAIEvalTypeCandy:
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
		for _, sample := range result.Samples {
			if sample.Error != "" || !sample.Valid {
				return counts
			}
		}
		counts.EvaluatedCount = result.UsedOutputs
		if OpenAIEvalIdentityQualityStatus(result.Prediction) == "suspected_normal" {
			counts.SuspectedPassCount = counts.EvaluatedCount
		}
	}
	return counts
}
