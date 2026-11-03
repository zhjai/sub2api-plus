package service

import "strings"

const openAIEvalModelTraceBankRevision = "sha256:a4e256c00444179b76f3855578660e66f30659df8c0122f1768dd05a8d705630"
const OpenAIEvalModelTraceRuleVersion = "public-target-match-luna-v2"

// Compare public model IDs only. Version suffixes and provider mappings are
// distinct identities; a behavioral match does not prove upstream identity.
func openAIEvalModelTraceVerdict(requested, prediction string) (string, string) {
	status, reason := openAIEvalAttributionVerdict(prediction)
	if status == "insufficient" {
		return status, reason
	}
	if OpenAIEvalIdentityQualityStatus(prediction) == "suspected_warning" {
		return "warning", "modeltrace_luna_attribution"
	}
	if openAIEvalQualityDimension(requested) != "" && openAIEvalQualityDimension(requested) == openAIEvalQualityDimension(prediction) {
		return "pass", "modeltrace_target_match"
	}
	return "suspected_normal", "non_luna_behavioral_attribution"
}

func modelTraceSchedulingOutcome(requested string, result *OpenAIEvalModelTraceResult, err error) OpenAIEvalOutcome {
	if err != nil || !openAIEvalModelTraceUsedOutputsValid(result) {
		return OpenAIEvalOutcome{Status: "insufficient", Reason: "modeltrace_insufficient_outputs", SampleCount: 0, ExpectedCount: OpenAIEvalModelTraceRequests, Confidence: "none", Scheduling: "alert_only", ModelTrace: result}
	}
	status, reason := openAIEvalModelTraceVerdict(requested, result.Prediction)
	return OpenAIEvalOutcome{Status: status, Reason: reason, SampleCount: result.UsedOutputs, ExpectedCount: OpenAIEvalModelTraceRequests, Confidence: "low", Scheduling: "alert_only", ModelTrace: result,
		Attribution: &OpenAIEvalAttributionPolicy{RuleVersion: OpenAIEvalModelTraceRuleVersion, OriginalRuleVersion: OpenAIEvalModelTraceRuleVersion, OriginalStatus: status, OriginalReason: reason}}
}

func isModelTraceType(testType string) bool {
	return strings.EqualFold(strings.TrimSpace(testType), OpenAIEvalTypeModelTrace)
}
