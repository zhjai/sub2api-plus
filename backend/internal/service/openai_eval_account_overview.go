package service

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"sort"
	"time"
)

func overviewDefaultPolicy(cfg *OpenAIEvalConfig) (string, OpenAIEvalRankingWeights) {
	copy := *cfg
	copy.Policies = nil
	return openAIEvalRankingWeights(&copy, "", "")
}

func rankingQualityFraction(q OpenAIEvalRankingQuality) *big.Rat {
	if !q.Known || q.Ratio == nil {
		return nil
	}
	if q.macroRatio != nil {
		return new(big.Rat).Set(q.macroRatio)
	}
	if q.Selected <= 0 {
		return nil
	}
	return big.NewRat(int64(q.Pass+q.SuspectedPass), int64(q.Selected))
}

// This is called first for efforts, then for models. Counts are diagnostics.
func macroRankingFactors(cells []OpenAIEvalRankingFactors) OpenAIEvalRankingFactors {
	out := emptyOpenAIEvalRankingFactors()
	if len(cells) == 0 {
		return out
	}
	errorScore, ttftScore, errorValue := 0., 0., 0.
	errorKnown, qualityKnown := 0, 0
	quality := new(big.Rat)
	for _, f := range cells {
		errorScore += f.ErrorRate.Score
		ttftScore += f.TTFT.Score
		out.ErrorRate.SampleCount += f.ErrorRate.SampleCount
		out.TTFT.SampleCount += f.TTFT.SampleCount
		if f.ErrorRate.Known && f.ErrorRate.Value != nil {
			errorKnown++
			errorValue += *f.ErrorRate.Value
		}
		out.ErrorRate.Known = out.ErrorRate.Known || f.ErrorRate.Known
		out.TTFT.Known = out.TTFT.Known || f.TTFT.Known
		out.Quality.Pass += f.Quality.Pass
		out.Quality.SuspectedPass += f.Quality.SuspectedPass
		out.Quality.Selected += f.Quality.Selected
		out.Quality.Evaluated += f.Quality.Evaluated
		if fraction := rankingQualityFraction(f.Quality); fraction != nil {
			quality.Add(quality, fraction)
			qualityKnown++
		}
		for _, pair := range []struct {
			dst **time.Time
			src *time.Time
		}{
			{&out.ErrorRate.ObservedAt, f.ErrorRate.ObservedAt}, {&out.TTFT.ObservedAt, f.TTFT.ObservedAt},
			{&out.Quality.ObservedAt, f.Quality.ObservedAt}, {&out.Quality.ExpiresAt, f.Quality.ExpiresAt},
		} {
			if pair.src != nil && (*pair.dst == nil || pair.src.Before(**pair.dst)) {
				*pair.dst = rankingPtr(*pair.src)
			}
		}
	}
	out.ErrorRate.Score = errorScore / float64(len(cells))
	out.TTFT.Score = ttftScore / float64(len(cells))
	out.ErrorRate.DefaultApplied, out.TTFT.DefaultApplied = false, false
	for _, f := range cells {
		out.ErrorRate.DefaultApplied = out.ErrorRate.DefaultApplied || f.ErrorRate.DefaultApplied
		out.TTFT.DefaultApplied = out.TTFT.DefaultApplied || f.TTFT.DefaultApplied
	}
	if errorKnown > 0 {
		out.ErrorRate.Value = rankingPtr(errorValue / float64(errorKnown))
		out.ErrorRate.Source = rankingPtr("macro_evidence")
		out.ErrorRate.UnknownReason = nil
	}
	if out.TTFT.Known {
		out.TTFT.UnknownReason = nil
	}
	// Raw TTFT is intentionally absent across different models; scores are normalized per cell.
	if qualityKnown > 0 {
		quality.Quo(quality, big.NewRat(int64(qualityKnown), 1))
		value, _ := quality.Float64()
		out.Quality.Known, out.Quality.State, out.Quality.UnknownReason = true, "assessed", nil
		out.Quality.Score, out.Quality.Ratio, out.Quality.macroRatio = value, rankingPtr(value), quality
	}
	return out
}

func overviewSources(f OpenAIEvalRankingFactors) []string {
	sources := []string{}
	if f.ErrorRate.Known && f.ErrorRate.Source != nil {
		sources = append(sources, *f.ErrorRate.Source)
	}
	if f.TTFT.Known {
		sources = append(sources, "request_ttft_account_model_effort")
	}
	if f.Quality.Evaluated > 0 {
		sources = append(sources, "quality_evidence")
	}
	return sources
}

func overviewEvidenceUpstreams(account *Account, model string, scopes []openAIRankingScope) []string {
	var models []string
	for _, scope := range scopes {
		for _, variant := range rankingSelectionVariants(scope, model) {
			if variant.Platform == account.Platform && account.IsModelSupported(variant.SelectionModel) {
				models = append(models, account.GetMappedModel(variant.SelectionModel))
			}
		}
	}
	if len(models) == 0 {
		models = append(models, account.GetMappedModel(model))
	}
	return dedupeAndSortModelIDs(models)
}

func buildAccountOverview(gen *openAIRankingGeneration, cfg *OpenAIEvalConfig, scopes []openAIRankingScope, all map[int64]*Account, loads map[int64]*AccountLoadInfo, stats *openAIAccountRuntimeStats, latest map[OpenAIEvalEvidenceKey]OpenAIEvalRun, monitoring []openAIRankingMonitorEvidence, now time.Time, oauthRate *float64) {
	gen.policy, gen.weights = overviewDefaultPolicy(cfg)
	gen.ordering = "score_desc"
	if gen.policy == OpenAIEvalSchedulingPolicyAvoidDegradation {
		gen.ordering = "quality_then_score"
	} else if gen.policy == "" {
		gen.ordering = "legacy"
	}
	groups := make(map[int64]map[int64]bool)
	accountScopes := make(map[int64][]openAIRankingScope)
	for _, scope := range scopes {
		for _, a := range scope.accounts {
			accountScopes[a.ID] = append(accountScopes[a.ID], scope)
			if groups[a.ID] == nil {
				groups[a.ID] = make(map[int64]bool)
			}
			groups[a.ID][scope.group.ID] = true
		}
	}
	keys := make(map[openAIAccountRuntimeRouteKey]bool)
	upstreams := make(map[openAIAccountRuntimeRouteKey][]string)
	if stats != nil {
		stats.routes.Range(func(key, _ any) bool {
			k := key.(openAIAccountRuntimeRouteKey)
			f := stats.rankingFactors(k.AccountID, k.Model, k.Effort, now)
			if f.ErrorRate.Known || f.TTFT.Known {
				keys[k] = true
			}
			return true
		})
	}
	for k := range latest {
		q := qualityFromLatestRuns(cfg, k.AccountID, k.RequestedModel, k.ReasoningEffort, latest, now)
		if q.Evaluated > 0 {
			keys[openAIAccountRuntimeRouteKey{k.AccountID, k.RequestedModel, k.ReasoningEffort}] = true
		}
	}
	for k := range keys {
		if a := all[k.AccountID]; a != nil {
			upstreams[k] = overviewEvidenceUpstreams(a, k.Model, accountScopes[a.ID])
		}
	}
	// A probe can enrich an evidenced alias, but mappings alone cannot mint aliases.
	for _, m := range monitoring {
		a := all[m.accountID]
		if a == nil {
			continue
		}
		f := emptyOpenAIEvalRankingFactors()
		applyRankingMonitor(&f, []openAIRankingMonitorEvidence{m}, a.ID, m.model, m.effort, now)
		if !f.ErrorRate.Known {
			continue
		}
		matched := false
		for k := range keys {
			if k.AccountID == a.ID && k.Effort == m.effort && len(upstreams[k]) == 1 && upstreams[k][0] == m.model {
				matched = true
				break
			}
		}
		if !matched {
			key := openAIAccountRuntimeRouteKey{a.ID, m.model, m.effort}
			if !keys[key] {
				keys[key], upstreams[key] = true, []string{m.model}
			}
		}
	}
	type cellKey struct{ model, effort string }
	pools := make(map[cellKey][]openAIEvalRankingInput)
	for k := range keys {
		a := all[k.AccountID]
		if a == nil || !a.IsOpenAICompatible() {
			continue
		}
		f := stats.rankingFactors(a.ID, k.Model, k.Effort, now)
		f.Quality = qualityFromLatestRuns(cfg, a.ID, k.Model, k.Effort, latest, now)
		upstream := upstreams[k]
		if len(upstream) == 1 {
			applyRankingMonitor(&f, monitoring, a.ID, upstream[0], k.Effort, now)
		}
		if len(overviewSources(f)) == 0 {
			continue
		}
		key := cellKey{k.Model, k.Effort}
		pools[key] = append(pools[key], openAIEvalRankingInput{account: a, factors: f, compatible: true, upstream: upstream})
	}
	models := make(map[int64][]OpenAIEvalAccountModel)
	for key, pool := range pools {
		// The pool is the whole fleet for this exact model/effort, never a group.
		for _, row := range scoreOpenAIEvalRanking("", OpenAIEvalRankingWeights{}, pool, now, oauthRate) {
			policy, weights := openAIEvalRankingWeights(cfg, key.model, key.effort)
			models[row.AccountID] = append(models[row.AccountID], OpenAIEvalAccountModel{key.model, key.effort, row.UpstreamModels, row.Factors, overviewSources(row.Factors), policy, weights})
			if expiry := rankingFactorExpiry(row.Factors, OpenAIEvalRankingWeights{ErrorRate: 1, TTFT: 1, Quality: 1}, gen.policy); expiry != nil && expiry.Before(gen.deadline) {
				gen.deadline = *expiry
				gen.summary.NextEvaluationReason = "evidence_expiry"
			}
			// A partial quality cell still exists only while its valid tests are fresh.
			q := row.Factors.Quality
			if q.Evaluated > 0 && q.ExpiresAt != nil && q.ExpiresAt.Before(gen.deadline) {
				gen.deadline = *q.ExpiresAt
				gen.summary.NextEvaluationReason = "evidence_expiry"
			}
		}
	}
	inputs := []openAIEvalRankingInput{}
	for _, a := range all {
		if !a.IsOpenAICompatible() {
			continue
		}
		f := emptyOpenAIEvalRankingFactors()
		if load := loads[a.ID]; load != nil {
			f.Load = OpenAIEvalRankingLoad{OpenAIEvalFactorMeta: rankingKnown(1-clamp01(float64(load.LoadRate)/100), now), LoadRate: rankingPtr(load.LoadRate), Waiting: rankingPtr(load.WaitingCount), CurrentConcurrency: rankingPtr(load.CurrentConcurrency)}
		}
		var exclusions []OpenAIEvalRankingExclusion
		if !a.IsSchedulable() {
			exclusions = append(exclusions, OpenAIEvalRankingExclusion{"account_unavailable", "account", now.UTC()})
		}
		inputs = append(inputs, openAIEvalRankingInput{account: a, factors: f, compatible: true, exclusions: exclusions})
	}
	gen.overview = []OpenAIEvalAccountOverviewRow{}
	for _, base := range scoreOpenAIEvalRanking(gen.policy, gen.weights, inputs, now, oauthRate) {
		row := OpenAIEvalAccountOverviewRow{OpenAIEvalRankedAccount: base, GroupIDs: []int64{}, Models: models[base.AccountID]}
		if row.Models == nil {
			row.Models = []OpenAIEvalAccountModel{}
		}
		for id := range groups[row.AccountID] {
			row.GroupIDs = append(row.GroupIDs, id)
		}
		sort.Slice(row.GroupIDs, func(i, j int) bool { return row.GroupIDs[i] < row.GroupIDs[j] })
		sort.Slice(row.Models, func(i, j int) bool {
			a, b := row.Models[i], row.Models[j]
			if a.RequestedModel != b.RequestedModel {
				return a.RequestedModel < b.RequestedModel
			}
			return a.ReasoningEffort < b.ReasoningEffort
		})
		byModel := make(map[string][]OpenAIEvalRankingFactors)
		for i := range row.Models {
			m := &row.Models[i]
			m.Factors.Price, m.Factors.Load = base.Factors.Price, base.Factors.Load
			byModel[m.RequestedModel] = append(byModel[m.RequestedModel], m.Factors)
			if m.Factors.Quality.Known {
				row.QualityCellCount++
			} else {
				row.UnknownQualityCellCount++
			}
			row.UpstreamModels = append(row.UpstreamModels, m.UpstreamModels...)
		}
		var modelFactors []OpenAIEvalRankingFactors
		var worst *big.Rat
		for _, model := range sortedRankingKeys(byModel) {
			f := macroRankingFactors(byModel[model])
			modelFactors = append(modelFactors, f)
			if q := rankingQualityFraction(f.Quality); q != nil {
				row.QualityModelCount++
				if worst == nil || q.Cmp(worst) < 0 {
					worst = q
					row.WorstQualityModel, row.WorstQualityRatio = rankingPtr(model), f.Quality.Ratio
				}
			}
		}
		row.ModelCount = len(byModel)
		row.UnknownQualityModelCount = row.ModelCount - row.QualityModelCount
		row.Factors = macroRankingFactors(modelFactors)
		row.Factors.Price, row.Factors.Load = base.Factors.Price, base.Factors.Load
		for _, m := range row.Models {
			row.ThresholdReasons = append(row.ThresholdReasons, rankingThresholdReasons(gen.policy, openAIEvalSchedulingThresholds(cfg), m.Factors)...)
		}
		row.ThresholdReasons = dedupeAndSortModelIDs(row.ThresholdReasons)
		row.UpstreamModels = dedupeAndSortModelIDs(row.UpstreamModels)
		f, w := row.Factors, gen.weights
		row.Contributions = OpenAIEvalRankingWeights{Price: 100 * w.Price * f.Price.Score, ErrorRate: 100 * w.ErrorRate * f.ErrorRate.Score, TTFT: 100 * w.TTFT * f.TTFT.Score, Load: 100 * w.Load * f.Load.Score, Quality: 100 * w.Quality * f.Quality.Score}
		c := row.Contributions
		score := c.Price + c.ErrorRate + c.TTFT + c.Load + c.Quality
		row.Priority = OpenAIEvalAccountPriority{QualityKnown: f.Quality.Known, QualityRatio: f.Quality.Ratio, OperationalScore: score}
		if gen.policy != "" {
			row.PriorityScore = rankingPtr(score)
		}
		gen.overview = append(gen.overview, row)
	}
	sort.Slice(gen.overview, func(i, j int) bool {
		return rankingPolicyLess(gen.policy, gen.overview[i].OpenAIEvalRankedAccount, gen.overview[j].OpenAIEvalRankedAccount)
	})
	tier := 0
	gen.overviewByID = make(map[int64]int, len(gen.overview))
	for i := range gen.overview {
		row := &gen.overview[i]
		gen.overviewByID[row.AccountID] = i
		if gen.policy == "" {
			continue
		}
		row.Rank = rankingPtr(i + 1)
		if gen.policy == OpenAIEvalSchedulingPolicyAvoidDegradation {
			if i == 0 || compareRankingQuality(gen.overview[i-1].Factors.Quality, row.Factors.Quality) != 0 {
				tier++
			}
			row.QualityTier, row.Priority.QualityTier = rankingPtr(tier), rankingPtr(tier)
		}
	}
}

func (s *OpenAIEvalService) SchedulingAccountOverview(filter OpenAIEvalRankingFilter) (*OpenAIEvalAccountOverview, error) {
	if s == nil || s.ranking == nil {
		return nil, ErrOpenAIEvalRankingUnavailable
	}
	r := s.ranking
	r.mu.Lock()
	defer r.mu.Unlock()
	if filter.Limit == 0 {
		filter.Limit = 100
	}
	if filter.Limit < 1 || filter.Limit > 500 {
		return nil, errors.New("overview limit must be between 1 and 500")
	}
	filterKey, _ := json.Marshal([]any{"account_overview", filter.GroupID})
	var cursor rankingCursor
	if filter.Cursor != "" {
		if len(filter.Cursor) > 8192 {
			return nil, errors.New("invalid overview cursor")
		}
		payload, err := base64.RawURLEncoding.DecodeString(filter.Cursor)
		if err != nil || len(payload) > 4096 || json.Unmarshal(payload, &cursor) != nil || cursor.Offset < 0 || cursor.Filter != string(filterKey) || cursor.EvaluationID == "" {
			return nil, errors.New("invalid overview cursor")
		}
		if filter.EvaluationID != "" && filter.EvaluationID != cursor.EvaluationID {
			return nil, ErrOpenAIEvalRankingSnapshotChanged
		}
		filter.EvaluationID = cursor.EvaluationID
	}
	gen := r.current
	if filter.EvaluationID != "" && (gen == nil || gen.summary.EvaluationID != filter.EvaluationID) {
		gen = r.previous
		if gen == nil || gen.summary.EvaluationID != filter.EvaluationID {
			return nil, ErrOpenAIEvalRankingSnapshotChanged
		}
	}
	status := r.statusLocked(gen)
	out := OpenAIEvalAccountOverview{Summary: status.Summary, PreviousSummary: status.PreviousSummary, EffectiveStatus: status.EffectiveStatus,
		CurrentConfigRevision: status.CurrentConfigRevision, EvaluationInProgress: status.EvaluationInProgress, RankingError: status.RankingError,
		Groups: status.Groups, Accounts: []OpenAIEvalAccountOverviewRow{}, Ordering: "legacy"}
	if gen != nil {
		out.Policy, out.Weights, out.Ordering = gen.policy, gen.weights, gen.ordering
		rows := []OpenAIEvalAccountOverviewRow{}
		for _, row := range gen.overview {
			include := filter.GroupID == nil
			for _, id := range row.GroupIDs {
				if filter.GroupID != nil && id == *filter.GroupID {
					include = true
				}
			}
			if include {
				rows = append(rows, row)
			}
		}
		if cursor.Offset > len(rows) {
			return nil, errors.New("invalid overview cursor offset")
		}
		end := min(cursor.Offset+filter.Limit, len(rows))
		out.Accounts = append(out.Accounts, rows[cursor.Offset:end]...)
		if end < len(rows) {
			payload, _ := json.Marshal(rankingCursor{gen.summary.EvaluationID, string(filterKey), end})
			out.NextCursor = rankingPtr(base64.RawURLEncoding.EncodeToString(payload))
		}
	}
	// API consumers receive owned DTOs, including nested factors and model cells.
	payload, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	var owned OpenAIEvalAccountOverview
	err = json.Unmarshal(payload, &owned)
	return &owned, err
}
