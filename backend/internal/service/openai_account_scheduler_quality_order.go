package service

import "sort"

// Ratios are compared as small integer fractions, so 1/2 and 2/4 share a
// tier. Unknown evidence is a separate last tier, never an implicit 100%.
func compareOpenAIQuality(left, right *OpenAIEvalQualityAssessment) int {
	if left == nil && right == nil {
		return 0
	}
	if left == nil {
		return -1
	}
	if right == nil {
		return 1
	}
	a := int64(left.PassCount+left.SuspectedPassCount) * int64(right.EvaluatedCount)
	b := int64(right.PassCount+right.SuspectedPassCount) * int64(left.EvaluatedCount)
	if a > b {
		return 1
	}
	if a < b {
		return -1
	}
	return 0
}

func openAIQualityTiers(pool []openAIAccountCandidateScore) [][]openAIAccountCandidateScore {
	ordered := append([]openAIAccountCandidateScore(nil), pool...)
	sort.SliceStable(ordered, func(i, j int) bool { return compareOpenAIQuality(ordered[i].quality, ordered[j].quality) > 0 })
	var tiers [][]openAIAccountCandidateScore
	for _, candidate := range ordered {
		if len(tiers) == 0 || compareOpenAIQuality(tiers[len(tiers)-1][0].quality, candidate.quality) != 0 {
			tiers = append(tiers, nil)
		}
		tiers[len(tiers)-1] = append(tiers[len(tiers)-1], candidate)
	}
	return tiers
}
