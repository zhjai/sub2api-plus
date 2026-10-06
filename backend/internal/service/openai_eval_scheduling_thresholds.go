package service

import (
	"fmt"
	"math"
)

type OpenAIEvalRuntimeThreshold struct {
	ErrorRate   float64 `json:"error_rate"`
	TTFTSeconds float64 `json:"ttft_seconds"`
}

type OpenAIEvalSchedulingThresholds struct {
	CostFirst        OpenAIEvalRuntimeThreshold `json:"cost_first"`
	StabilityFirst   OpenAIEvalRuntimeThreshold `json:"stability_first"`
	AvoidDegradation OpenAIEvalRuntimeThreshold `json:"avoid_degradation"`
	CustomBalance    OpenAIEvalRuntimeThreshold `json:"custom_balance"`
	MinErrorSamples  int64                      `json:"min_error_samples"`
	MinTTFTSamples   int64                      `json:"min_ttft_samples"`
}

func defaultOpenAIEvalSchedulingThresholds() OpenAIEvalSchedulingThresholds {
	return OpenAIEvalSchedulingThresholds{
		CostFirst:        OpenAIEvalRuntimeThreshold{.2, 15},
		StabilityFirst:   OpenAIEvalRuntimeThreshold{.05, 8},
		AvoidDegradation: OpenAIEvalRuntimeThreshold{.2, 15},
		CustomBalance:    OpenAIEvalRuntimeThreshold{.2, 15},
		MinErrorSamples:  10, MinTTFTSamples: 20,
	}
}

func openAIEvalSchedulingThresholds(config *OpenAIEvalConfig) OpenAIEvalSchedulingThresholds {
	if config != nil && config.SchedulingThresholds != nil {
		return *config.SchedulingThresholds
	}
	return defaultOpenAIEvalSchedulingThresholds()
}

func normalizeOpenAIEvalSchedulingThresholds(config *OpenAIEvalConfig) error {
	if config.SchedulingThresholds == nil {
		value := defaultOpenAIEvalSchedulingThresholds()
		config.SchedulingThresholds = &value
	}
	t := config.SchedulingThresholds
	if t.MinErrorSamples < 1 || t.MinErrorSamples > 1000000 || t.MinTTFTSamples < 1 || t.MinTTFTSamples > 1000000 {
		return fmt.Errorf("scheduling_thresholds sample counts must be between 1 and 1000000")
	}
	for _, pair := range []struct {
		name      string
		threshold OpenAIEvalRuntimeThreshold
	}{
		{"cost_first", t.CostFirst}, {"stability_first", t.StabilityFirst},
		{"avoid_degradation", t.AvoidDegradation}, {"custom_balance", t.CustomBalance},
	} {
		v := pair.threshold
		if math.IsNaN(v.ErrorRate) || math.IsInf(v.ErrorRate, 0) || v.ErrorRate < 0 || v.ErrorRate > 1 {
			return fmt.Errorf("scheduling_thresholds.%s.error_rate must be between 0 and 1", pair.name)
		}
		if math.IsNaN(v.TTFTSeconds) || math.IsInf(v.TTFTSeconds, 0) || v.TTFTSeconds <= 0 || v.TTFTSeconds > 86400 {
			return fmt.Errorf("scheduling_thresholds.%s.ttft_seconds must be greater than 0 and at most 86400", pair.name)
		}
	}
	return nil
}

func (t OpenAIEvalSchedulingThresholds) forPolicy(policy string) OpenAIEvalRuntimeThreshold {
	switch policy {
	case OpenAIEvalSchedulingPolicyStabilityFirst:
		return t.StabilityFirst
	case OpenAIEvalSchedulingPolicyAvoidDegradation:
		return t.AvoidDegradation
	case OpenAIEvalSchedulingPolicyCustomBalance:
		return t.CustomBalance
	default:
		return t.CostFirst
	}
}

// Monitor estimates carry no business sample count and cannot trigger this gate.
func rankingThresholdReasons(policy string, t OpenAIEvalSchedulingThresholds, f OpenAIEvalRankingFactors) []string {
	if policy == "" {
		return nil
	}
	v := t.forPolicy(policy)
	if policy == OpenAIEvalSchedulingPolicyCustomBalance {
		return nil
	}
	var reasons []string
	if f.ErrorRate.Known && f.ErrorRate.Value != nil && (f.ErrorRate.Source == nil || *f.ErrorRate.Source != "v1_matched_probe") && f.ErrorRate.SampleCount >= t.MinErrorSamples && *f.ErrorRate.Value > v.ErrorRate {
		reasons = append(reasons, "error_rate_threshold")
	}
	if f.TTFT.Known && f.TTFT.MS != nil && f.TTFT.SampleCount >= t.MinTTFTSamples && *f.TTFT.MS > v.TTFTSeconds*1000 {
		reasons = append(reasons, "ttft_threshold")
	}
	return reasons
}

// The threshold is a soft ordering exception, never a hard admission bypass.
func rankingPolicyLess(policy string, a, b OpenAIEvalRankedAccount) bool {
	if (a.PriorityScore == nil) != (b.PriorityScore == nil) {
		return a.PriorityScore != nil
	}
	if a.PriorityScore != nil && policy == OpenAIEvalSchedulingPolicyAvoidDegradation {
		if cmp := compareRankingEffectiveQuality(a, b); cmp != 0 {
			return cmp > 0
		}
	}
	if policy == OpenAIEvalSchedulingPolicyCostFirst || policy == OpenAIEvalSchedulingPolicyStabilityFirst || policy == OpenAIEvalSchedulingPolicyAvoidDegradation {
		left, right := a.Factors.Price.RateMultiplier, b.Factors.Price.RateMultiplier
		if (left != nil) != (right != nil) {
			return left != nil
		}
		if left != nil && *left != *right {
			return *left < *right
		}
	} else if a.PriorityScore != nil && b.PriorityScore != nil && *a.PriorityScore != *b.PriorityScore {
		return *a.PriorityScore > *b.PriorityScore
	}
	return a.AccountID < b.AccountID
}

// Demote each threshold-breaching account by at most one position from the
// base policy order. This is a post-sort operation, not a nontransitive sort
// comparator. Quality tiers remain absolute under avoid-degradation.
func demoteRankingThresholdsOnePosition(policy string, length int, row func(int) OpenAIEvalRankedAccount, swap func(int, int)) {
	if policy != OpenAIEvalSchedulingPolicyCostFirst && policy != OpenAIEvalSchedulingPolicyStabilityFirst && policy != OpenAIEvalSchedulingPolicyAvoidDegradation {
		return
	}
	for i := 0; i+1 < length; i++ {
		a, b := row(i), row(i+1)
		if a.PriorityScore == nil || b.PriorityScore == nil || len(a.ThresholdReasons) == 0 || !a.Eligible || !b.Eligible {
			continue
		}
		if policy == OpenAIEvalSchedulingPolicyAvoidDegradation && compareRankingEffectiveQuality(a, b) != 0 {
			continue
		}
		swap(i, i+1)
		i++
	}
}
