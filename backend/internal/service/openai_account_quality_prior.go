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

func openAIEvalQualityPriorBasis(prior *OpenAIEvalAccountQualityPrior) string {
	if prior == nil || prior.Basis == "" {
		return "account_prior"
	}
	return prior.Basis
}

func openAIEvalModelConfiguredForCandidates(cfg *OpenAIEvalConfig, model string, groupID int64, accounts []Account) bool {
	if cfg == nil {
		return false
	}
	ids := make(map[int64]struct{}, len(accounts))
	for i := range accounts {
		var liveGroup *int64
		if groupID != 0 {
			liveGroup = rankingPtr(groupID)
		}
		if accounts[i].IsSchedulable() && openAIStickyAccountMatchesGroup(&accounts[i], liveGroup) {
			ids[accounts[i].ID] = struct{}{}
		}
	}
	for _, route := range cfg.Accounts {
		if _, ok := ids[route.AccountID]; !ok || openAIEvalQualityDimension(route.RequestedModel) != openAIEvalQualityDimension(model) {
			continue
		}
		if route.CandySchedule.Enabled || route.FingerprintSchedule.Enabled || route.ModelTraceSchedule.Enabled {
			return true
		}
	}
	return false
}

// Call with the coordinator lock held, or while building an unpublished generation.
// Runtime error/TTFT evidence does not prove model quality and cannot erase this prior.
func accountQualityPriors(gen *openAIRankingGeneration, cfg *OpenAIEvalConfig, groupID int64, model, effort string, accounts []Account, latest map[OpenAIEvalEvidenceKey]OpenAIEvalRun, now time.Time) map[int64]*OpenAIEvalAccountQualityPrior {
	if gen == nil || cfg == nil || !cfg.EffectsEnabled || cfg.Revision != gen.summary.ConfigRevision || !now.Before(gen.deadline) {
		return nil
	}
	policy, weights := openAIEvalRankingWeights(cfg, model, effort)
	// The quality evidence is independent of the overview's operational policy.
	if !openAIEvalRankingUsesQuality(policy, weights) {
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
		if quality.Known && quality.Selected > 0 {
			continue
		}
		q := row.Factors.Quality
		if q.ExpiresAt != nil && !now.Before(*q.ExpiresAt) {
			continue
		}
		var modelCells []OpenAIEvalRankingFactors
		for _, cell := range row.Models {
			if openAIEvalQualityDimension(cell.RequestedModel) == openAIEvalQualityDimension(model) && cell.Factors.Quality.Known && cell.Factors.Quality.Selected > 0 {
				modelCells = append(modelCells, cell.Factors)
			}
		}
		// Prefer this public model across efforts. A configured but unavailable
		// model result falls back to the account-level aggregate. This is
		// intentional: a configured route without usable evidence is not a
		// quality signal and must not suppress the cold-start account prior.
		modelConfigured := len(modelCells) > 0
		if modelConfigured {
			q = macroRankingFactors(modelCells).Quality
		}
		fraction := rankingQualityFraction(q)
		if fraction == nil || q.ExpiresAt == nil || !now.Before(*q.ExpiresAt) {
			continue
		}
		sources := make([]string, 0)
		if modelConfigured {
			for _, route := range cfg.Accounts {
				if route.AccountID == account.ID && openAIEvalQualityDimension(route.RequestedModel) == openAIEvalQualityDimension(model) && qualityFromLatestRuns(cfg, account.ID, model, route.ReasoningEffort, latest, now).Known {
					sources = append(sources, route.RequestedModel+"/"+route.ReasoningEffort)
				}
			}
		} else {
			for _, cell := range row.Models {
				if cell.Factors.Quality.Known {
					sources = append(sources, cell.RequestedModel+"/"+cell.ReasoningEffort)
				}
			}
		}
		sources = dedupeAndSortModelIDs(sources)
		expires := *q.ExpiresAt
		if gen.deadline.Before(expires) {
			expires = gen.deadline
		}
		basis := "aggregate_fallback"
		if modelConfigured {
			basis = "model_effort_fallback"
		}
		priors[account.ID] = &OpenAIEvalAccountQualityPrior{Ratio: *q.Ratio, Basis: basis, EvaluationID: gen.summary.EvaluationID,
			EvaluatedAt: gen.summary.EvaluatedAt, ExpiresAt: expires, SourceModels: sources, fraction: fraction}
	}
	return priors
}
