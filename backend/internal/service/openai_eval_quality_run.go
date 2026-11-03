package service

import "time"

func openAIEvalQualityAggregateFromRun(run OpenAIEvalRun, interval, refresh int, now time.Time) (OpenAIEvalQualityAggregate, bool) {
	return openAIEvalCurrentQualityAggregateFromRun(run, interval, refresh, now)
}

func openAIEvalQualityRunSourceSupported(source string) bool {
	return source == "manual" || source == "scheduled"
}

// Keep the compatibility entry point aligned with the current detector contract.
func openAIEvalQualityCountsFromRun(run *OpenAIEvalRun) OpenAIEvalQualityCounts {
	return openAIEvalCurrentQualityCountsFromRun(run)
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
