package service

import (
	"math/big"
	"time"
)

func cloneAccountQualityPrior(prior *OpenAIEvalAccountQualityPrior) *OpenAIEvalAccountQualityPrior {
	if prior == nil {
		return nil
	}
	copy := *prior
	copy.SourceModels = append([]string(nil), prior.SourceModels...)
	if prior.fraction != nil {
		copy.fraction = new(big.Rat).Set(prior.fraction)
	}
	return &copy
}

// Call with the coordinator lock held, or while building an unpublished generation.
// Runtime error/TTFT evidence does not prove model quality and cannot erase this prior.
func accountQualityPriors(gen *openAIRankingGeneration, cfg *OpenAIEvalConfig, groupID int64, model, effort string, accounts []Account, latest map[OpenAIEvalEvidenceKey]OpenAIEvalRun, now time.Time) map[int64]*OpenAIEvalAccountQualityPrior {
	if gen == nil || cfg == nil || !cfg.EffectsEnabled || cfg.Revision != gen.summary.ConfigRevision || !now.Before(gen.deadline) {
		return nil
	}
	policy, _ := openAIEvalRankingWeights(cfg, model, effort)
	// The quality evidence is independent of the overview's operational policy.
	if policy != OpenAIEvalSchedulingPolicyAvoidDegradation {
		return nil
	}
	priors := make(map[int64]*OpenAIEvalAccountQualityPrior)
	for i := range accounts {
		account := &accounts[i]
		index, exists := gen.overviewByID[account.ID]
		if !exists || index < 0 || index >= len(gen.overview) || !account.IsSchedulable() {
			continue
		}
		row := gen.overview[index]
		member := false
		for _, group := range row.GroupIDs {
			if group == groupID {
				member = true
				break
			}
		}
		var liveGroup *int64
		if groupID != 0 {
			liveGroup = rankingPtr(groupID)
		}
		if !member || !openAIStickyAccountMatchesGroup(account, liveGroup) {
			continue
		}
		quality := qualityFromLatestRuns(cfg, account.ID, model, effort, latest, now)
		if quality.Selected > 0 {
			continue
		}
		hasExact := false
		for _, testType := range openAIEvalQualityTestTypes {
			if _, found := latest[OpenAIEvalEvidenceKey{account.ID, model, effort, testType}]; found {
				hasExact = true
				break
			}
		}
		q := row.Factors.Quality
		fraction := rankingQualityFraction(q)
		if hasExact || fraction == nil || q.ExpiresAt == nil || !now.Before(*q.ExpiresAt) {
			continue
		}
		sources := make([]string, 0)
		for _, cell := range row.Models {
			if cell.Factors.Quality.Known {
				sources = append(sources, cell.RequestedModel+"/"+cell.ReasoningEffort)
			}
		}
		sources = dedupeAndSortModelIDs(sources)
		expires := *q.ExpiresAt
		if gen.deadline.Before(expires) {
			expires = gen.deadline
		}
		priors[account.ID] = &OpenAIEvalAccountQualityPrior{Ratio: *q.Ratio, EvaluationID: gen.summary.EvaluationID,
			EvaluatedAt: gen.summary.EvaluatedAt, ExpiresAt: expires, SourceModels: sources, fraction: fraction}
	}
	return priors
}
