package service

import (
	"context"
	"errors"
	"sort"
	"time"
)

func openAIRankedPolicyEnabled(req OpenAIAccountScheduleRequest) bool {
	return openAIEffectiveSchedulingPolicy(req) != ""
}

func (s *defaultOpenAIAccountScheduler) explicitRanking(ctx context.Context, req OpenAIAccountScheduleRequest, accounts []Account, loads map[int64]*AccountLoadInfo) ([]OpenAIEvalRankedAccount, OpenAIEvalRankingTrace, error) {
	// Mapping getters populate per-account caches; scoring owns those writes.
	accounts = append([]Account(nil), accounts...)
	model, effort := openAIClientModelForSchedule(req), normalizeOpenAIAccountRuntimeRoutePart(req.RequestedReasoningEffort, 64)
	configuredAccounts := accounts
	if len(req.rankingCandidateAccounts) > 0 {
		configuredAccounts = req.rankingCandidateAccounts
	}
	groupID := int64(0)
	if req.GroupID != nil {
		groupID = *req.GroupID
	}
	trace := OpenAIEvalRankingTrace{RecordType: "actual_dispatch", Scope: "this_instance", GroupID: req.GroupID, RankingBasis: "live_fallback", SelectionModel: model, RankingFallbackReason: rankingPtr("snapshot_missing")}
	trace.RawRequestedReasoningEffort = req.RequestedReasoningEffort
	policySnapshot := openAIEvalSchedulingPolicy.Load().(*openAIEvalSchedulingPolicySnapshot)
	cfg := &OpenAIEvalConfig{EffectsEnabled: policySnapshot.Enabled, SchedulingPolicy: policySnapshot.Default, CustomBalance: policySnapshot.CustomBalance, Policies: policySnapshot.Rules}
	if policySnapshot.Thresholds.MinErrorSamples > 0 {
		value := policySnapshot.Thresholds
		cfg.SchedulingThresholds = &value
	}
	latest := map[OpenAIEvalEvidenceKey]OpenAIEvalRun{}
	var monitoring []openAIRankingMonitorEvidence
	var monitorErr error
	var latestErr error
	var priors map[int64]*OpenAIEvalAccountQualityPrior
	now := time.Now()
	r := s.service.evalRanking
	if r != nil {
		r.mu.Lock()
		cfg = cloneRankingConfig(r.config)
		trace.ConfigRevision = cfg.Revision
		r.mu.Unlock()
		// Read only the requested cells; a request never rebuilds the overview.
		readConfig := cloneRankingConfig(cfg)
		readConfig.Accounts = nil
		ids := make(map[int64]*Account, len(accounts))
		upstreamModels := make(map[int64]string, len(accounts))
		for i := range accounts {
			ids[accounts[i].ID] = &accounts[i]
			upstreamModels[accounts[i].ID] = accounts[i].GetMappedModel(req.RequestedModel)
		}
		for _, route := range cfg.Accounts {
			if ids[route.AccountID] != nil && openAIEvalQualityDimension(route.RequestedModel) == openAIEvalQualityDimension(model) {
				readConfig.Accounts = append(readConfig.Accounts, route)
			}
		}
		latest, latestErr = r.readLatest(ctx, readConfig)
		monitoring, monitorErr = r.monitorEvidenceFor(ctx, ids, upstreamModels, &effort)
		now = time.Now()
		r.mu.Lock()
		if r.config == nil || r.config.Revision != cfg.Revision {
			r.mu.Unlock()
			if req.priorityConditionEvidence {
				return nil, trace, ErrOpenAIEvalRankingSuperseded
			}
			// Do not mix evidence from different revisions or fail a live
			// request because an administrator saved a configuration. Keep the
			// captured policy and score current runtime factors without the
			// superseded evaluation/monitoring evidence. Admission remains live.
			trace.RankingFallbackReason = rankingPtr("config_revision_changed")
			return s.scoreLiveExplicitRanking(ctx, req, accounts, loads, cfg, nil, nil, nil, now), trace, nil
		}
		gen := r.current
		readErr := errors.Join(latestErr, monitorErr)
		if readErr == nil && r.lastError == nil {
			priors = accountQualityPriors(gen, cfg, groupID, model, effort, configuredAccounts, latest, now)
		}
		if prior, ok := s.overviewPriorLocked(r, gen, cfg, req, accounts, loads, latest, monitoring, readErr, now); ok && !req.priorityConditionEvidence {
			trace.RankingBasis, trace.RankingFallbackReason = "overview_prior", nil
			trace.EvaluationID, trace.SnapshotEvaluatedAt = rankingPtr(gen.summary.EvaluationID), rankingPtr(gen.summary.EvaluatedAt)
			r.mu.Unlock()
			return prior, trace, nil
		}
		if latestErr != nil {
			trace.RankingFallbackReason = rankingPtr("quality_evidence_unavailable")
		} else if monitorErr != nil {
			trace.RankingFallbackReason = rankingPtr("monitoring_unavailable")
		} else if r.lastError != nil {
			trace.RankingFallbackReason = rankingPtr("evaluation_error")
		} else if gen != nil && cfg.Revision != gen.summary.ConfigRevision {
			trace.RankingFallbackReason = rankingPtr("config_revision_changed")
		} else if gen != nil && !req.priorityConditionEvidence {
			key := rankingDimensionKey(groupID, model, effort)
			index, found := gen.byKey[key]
			if !found {
				trace.RankingFallbackReason = rankingPtr("dimension_not_cached")
			} else {
				dim := gen.dimensions[index]
				reason := ""
				if dim.CoverageStatus == "live_fallback" {
					reason = "dimension_capacity_fallback"
					if dim.FallbackReason != nil {
						reason = *dim.FallbackReason
					}
				} else if dim.ValidUntil == nil || !now.Before(gen.deadline) || !now.Before(*dim.ValidUntil) {
					reason = "snapshot_expired"
				} else if s.stats.rankingMetricsChanged(gen, dim.Accounts, model, effort) {
					reason = "request_metrics_updated"
				} else if rankingQualityEvidenceChanged(cfg, dim.Accounts, model, effort, latest, now) {
					reason = "quality_evidence_updated"
				} else {
					variantOK := false
					for _, v := range dim.SelectionModelVariants {
						if v.SelectionModel == req.RequestedModel && v.Platform == NormalizeOpenAICompatiblePlatform(req.Platform) {
							variantOK = true
						}
					}
					if !variantOK {
						reason = "selection_model_changed"
					}
					byID := make(map[int64]OpenAIEvalRankedAccount, len(dim.Accounts))
					for _, row := range dim.Accounts {
						byID[row.AccountID] = row
					}
					for i := range accounts {
						a := &accounts[i]
						row, exists := byID[a.ID]
						if !exists {
							reason = "unranked_candidate"
							break
						}
						if !a.IsModelSupported(req.RequestedModel) {
							continue
						}
						mapped := a.GetMappedModel(req.RequestedModel)
						matched := false
						for _, upstream := range row.UpstreamModels {
							if upstream == mapped {
								matched = true
							}
						}
						if !matched {
							reason = "account_mapping_changed"
							break
						}
					}
				}
				if reason == "" {
					trace.RankingBasis = "snapshot"
					trace.RankingFallbackReason = nil
					trace.EvaluationID = rankingPtr(gen.summary.EvaluationID)
					trace.SnapshotEvaluatedAt = rankingPtr(gen.summary.EvaluatedAt)
					rows := append([]OpenAIEvalRankedAccount(nil), dim.Accounts...)
					for i := range rows {
						rows[i].AccountQualityPrior = cloneAccountQualityPrior(rows[i].AccountQualityPrior)
					}
					r.mu.Unlock()
					return rows, trace, nil
				}
				trace.RankingFallbackReason = rankingPtr(reason)
			}
		}
		r.mu.Unlock()
	}
	return s.scoreLiveExplicitRanking(ctx, req, accounts, loads, cfg, latest, monitoring, priors, now), trace, nil
}

func (s *defaultOpenAIAccountScheduler) scoreLiveExplicitRanking(ctx context.Context, req OpenAIAccountScheduleRequest, accounts []Account, loads map[int64]*AccountLoadInfo, cfg *OpenAIEvalConfig, latest map[OpenAIEvalEvidenceKey]OpenAIEvalRun, monitoring []openAIRankingMonitorEvidence, priors map[int64]*OpenAIEvalAccountQualityPrior, now time.Time) []OpenAIEvalRankedAccount {
	model, effort := openAIClientModelForSchedule(req), normalizeOpenAIAccountRuntimeRoutePart(req.RequestedReasoningEffort, 64)
	policy, weights := openAIEvalRankingWeights(cfg, model, effort)
	if req.SchedulingPolicy != "" && policy == "" {
		cfg.SchedulingPolicy = req.SchedulingPolicy
		policy, weights = openAIEvalRankingWeights(cfg, model, effort)
	}
	inputs := make([]openAIEvalRankingInput, 0, len(accounts))
	for i := range accounts {
		a := &accounts[i]
		f := s.stats.rankingFactors(a.ID, model, effort, now)
		f.Quality = qualityFromLatestRuns(cfg, a.ID, model, effort, latest, now)
		applyRankingMonitor(&f, monitoring, a.ID, a.GetMappedModel(req.RequestedModel), effort, now)
		if s.service.evalRanking == nil {
			if q, ok := openAIEvalQualitySnapshots.lookup(a.ID, model, effort, now); ok {
				f.Quality = OpenAIEvalRankingQuality{OpenAIEvalFactorMeta: rankingKnown(q.Ratio(), now), State: "assessed", Pass: q.PassCount, SuspectedPass: q.SuspectedPassCount, Selected: q.EvaluatedCount, Evaluated: q.EvaluatedCount, Ratio: rankingPtr(q.Ratio()), ExpiresAt: rankingPtr(q.ExpiresAt)}
			}
		}
		if load := loads[a.ID]; load != nil {
			f.Load = OpenAIEvalRankingLoad{OpenAIEvalFactorMeta: rankingKnown(1-clamp01(float64(load.LoadRate)/100), now), LoadRate: rankingPtr(load.LoadRate), Waiting: rankingPtr(load.WaitingCount), CurrentConcurrency: rankingPtr(load.CurrentConcurrency)}
		}
		compatible := a.Platform == NormalizeOpenAICompatiblePlatform(req.Platform) && a.IsOpenAICompatible() && a.IsModelSupported(req.RequestedModel)
		inputs = append(inputs, openAIEvalRankingInput{account: a, factors: f, compatible: compatible, upstream: []string{a.GetMappedModel(req.RequestedModel)}})
	}
	return scoreOpenAIEvalRankingWithThresholds(policy, weights, inputs, now, s.service.openAIOAuthSchedulingRateMultiplier(ctx), openAIEvalSchedulingThresholds(cfg), priors)
}

// Called after live admission filters; the overview never supplies current-model quality.
func (s *defaultOpenAIAccountScheduler) overviewPriorLocked(r *OpenAIEvalRankingService, gen *openAIRankingGeneration, cfg *OpenAIEvalConfig, req OpenAIAccountScheduleRequest, accounts []Account, loads map[int64]*AccountLoadInfo, latest map[OpenAIEvalEvidenceKey]OpenAIEvalRun, monitoring []openAIRankingMonitorEvidence, readErr error, now time.Time) ([]OpenAIEvalRankedAccount, bool) {
	if gen == nil || !gen.priorComplete || r.lastError != nil || readErr != nil || !cfg.EffectsEnabled || cfg.Revision != gen.summary.ConfigRevision || !now.Before(gen.deadline) || len(accounts) == 0 {
		return nil, false
	}
	model, effort := openAIClientModelForSchedule(req), normalizeOpenAIAccountRuntimeRoutePart(req.RequestedReasoningEffort, 64)
	policy, weights := openAIEvalRankingWeights(cfg, model, effort)
	if policy == "" || policy != gen.policy || !openAIEvalRankingWeightsEqual(weights, gen.weights) || (req.SchedulingPolicy != "" && policy != gen.policy) {
		return nil, false
	}
	for _, rule := range cfg.Policies {
		if rule.Enabled != nil && !*rule.Enabled {
			continue
		}
		if openAIEvalQualityDimension(rule.RequestedModel) == openAIEvalQualityDimension(model) && (rule.ReasoningEffort == "" || rule.ReasoningEffort == effort) {
			return nil, false
		}
	}
	groupID := int64(0)
	if req.GroupID != nil {
		groupID = *req.GroupID
	}
	configuredAccounts := accounts
	if len(req.rankingCandidateAccounts) > 0 {
		configuredAccounts = req.rankingCandidateAccounts
	}
	modelConfigured := openAIEvalModelConfiguredForCandidates(cfg, model, groupID, configuredAccounts)
	if s.stats != nil {
		s.stats.rankingMu.RLock()
		defer s.stats.rankingMu.RUnlock()
		if s.stats.rankingEvictions.Load() > 0 {
			return nil, false
		}
	}
	byID := make(map[int64]OpenAIEvalAccountOverviewRow, len(gen.overview))
	for _, row := range gen.overview {
		if len(row.ThresholdReasons) > 0 {
			// Cross-model thresholds are overview diagnostics, not evidence
			// for this cold request. Recompute with current-request factors.
			return nil, false
		}
		byID[row.AccountID] = row
	}
	rows := make([]OpenAIEvalRankedAccount, 0, len(accounts))
	for i := range accounts {
		a := &accounts[i]
		if s.service.concurrencyService != nil && loads[a.ID] == nil {
			return nil, false
		}
		prior, found := byID[a.ID]
		if !found || prior.Rank == nil || prior.PriorityScore == nil {
			return nil, false
		}
		member := false
		for _, id := range prior.GroupIDs {
			if id == groupID {
				member = true
			}
		}
		if !member {
			return nil, false
		}
		if s.stats != nil {
			if _, exists := s.stats.loadRoute(a.ID, model, effort); exists {
				return nil, false
			}
		}
		q := qualityFromLatestRuns(cfg, a.ID, model, effort, latest, now)
		if q.Selected > 0 {
			return nil, false
		}
		if modelConfigured {
			return nil, false
		}
		if _, active := ReadOpenAIEvalRouteHealthFromAccount(a, model, effort, now); active {
			return nil, false
		}
		for k := range latest {
			if k.AccountID == a.ID && k.RequestedModel == model && k.ReasoningEffort == effort {
				return nil, false
			}
		}
		upstream := a.GetMappedModel(req.RequestedModel)
		for _, m := range monitoring {
			if m.accountID == a.ID && m.model == upstream && m.effort == effort && len(m.rows) > 0 {
				return nil, false
			}
		}
		row := prior.OpenAIEvalRankedAccount
		row.Factors = emptyOpenAIEvalRankingFactors()
		row.QualityBasis, row.AccountQualityPrior = "aggregate_fallback", nil
		row.Factors.Price, row.Factors.Load = prior.Factors.Price, prior.Factors.Load
		row.Contributions, row.QualityTier = OpenAIEvalRankingWeights{}, nil
		row.UpstreamModels = []string{upstream}
		row.OverviewPrior = &OpenAIEvalOverviewPrior{Rank: *prior.Rank, PriorityScore: *prior.PriorityScore, Priority: prior.Priority}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool { return *rows[i].Rank < *rows[j].Rank })
	if !time.Now().Before(gen.deadline) {
		return nil, false
	}
	return rows, true
}

func (r *OpenAIEvalRankingService) annotateOwner(req OpenAIAccountScheduleRequest, decision *OpenAIAccountScheduleDecision) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.config != nil {
		decision.ConfigRevision = r.config.Revision
	}
	gen := r.current
	if gen == nil || gen.summary.ConfigRevision != decision.ConfigRevision {
		return
	}
	groupID := int64(0)
	if req.GroupID != nil {
		groupID = *req.GroupID
	}
	effort := normalizeOpenAIAccountRuntimeRoutePart(req.RequestedReasoningEffort, 64)
	index, ok := gen.byKey[rankingDimensionKey(groupID, openAIClientModelForSchedule(req), effort)]
	if !ok {
		return
	}
	for _, row := range gen.dimensions[index].Accounts {
		if row.AccountID == decision.SelectedAccountID {
			decision.SelectedRank = row.Rank
			decision.EvaluationID = rankingPtr(gen.summary.EvaluationID)
			decision.SnapshotEvaluatedAt = rankingPtr(gen.summary.EvaluatedAt)
			candidate := rankedScheduleCandidate(row)
			candidate.Selected = true
			candidate.DecisionReason = "required_owner_override"
			decision.Candidates = []OpenAIAccountScheduleCandidate{candidate}
			return
		}
	}
}

func rankedScheduleCandidate(row OpenAIEvalRankedAccount) OpenAIAccountScheduleCandidate {
	q := row.Factors.Quality
	qualityBasis := row.QualityBasis
	if qualityBasis == "" {
		qualityBasis = "none"
	}
	candidate := OpenAIAccountScheduleCandidate{AccountID: row.AccountID, Rank: row.Rank, PriorityScore: row.PriorityScore, Factors: rankingPtr(row.Factors), Contributions: rankingPtr(row.Contributions), Eligible: row.Eligible,
		EvaluatedCount: q.Evaluated, PassCount: q.Pass, SuspectedPassCount: q.SuspectedPass, QualityRatio: q.Ratio, QualityState: q.State, QualityBasis: qualityBasis, DecisionReason: "explicit_policy_rank"}
	if row.OverviewPrior != nil {
		candidate.OverviewPrior = row.OverviewPrior
		candidate.DecisionReason = "overview_prior"
		candidate.QualityBasis = "aggregate_fallback"
	}
	if row.AccountQualityPrior != nil {
		candidate.AccountQualityPrior = cloneAccountQualityPrior(row.AccountQualityPrior)
		basis := openAIEvalQualityPriorBasis(row.AccountQualityPrior)
		candidate.DecisionReason = basis
		candidate.QualityBasis = basis
	}
	if row.PriorityScore != nil {
		candidate.Score = *row.PriorityScore
	}
	if row.ExclusionReason != nil {
		candidate.ExclusionReason = *row.ExclusionReason
	}
	if row.Factors.Price.RateMultiplier != nil {
		candidate.RateMultiplier = *row.Factors.Price.RateMultiplier
	}
	return candidate
}

func (s *defaultOpenAIAccountScheduler) selectByExplicitRanking(ctx context.Context, req OpenAIAccountScheduleRequest, all []Account, eligible []*Account, loads map[int64]*AccountLoadInfo, decision *OpenAIAccountScheduleDecision, filters openAISelectionFilterStats, budget *openAISelectionProbeBudget) (*AccountSelectionResult, int, int, float64, error) {
	req.rankingCandidateAccounts = make([]Account, 0, len(eligible))
	for _, account := range eligible {
		if account != nil {
			req.rankingCandidateAccounts = append(req.rankingCandidateAccounts, *account)
		}
	}
	rows, trace, err := s.explicitRanking(ctx, req, all, loads)
	if decision != nil {
		decision.OpenAIEvalRankingTrace = trace
	}
	if err != nil {
		return nil, len(eligible), 0, 0, err
	}
	byID := make(map[int64]*Account, len(eligible))
	for _, a := range eligible {
		byID[a.ID] = a
	}
	order := make([]openAIAccountCandidateScore, 0, len(eligible))
	if decision != nil {
		decision.Candidates = nil
		decision.ReasonCode = "explicit_policy_rank"
		decision.ReasonText = "highest available policy rank"
		if trace.RankingBasis == "overview_prior" {
			decision.ReasonCode, decision.ReasonText = "overview_prior", "fully cold pool ordered by account overview"
		}
	}
	for _, row := range rows {
		a, available := byID[row.AccountID]
		if available && row.Rank != nil {
			order = append(order, openAIAccountCandidateScore{account: a, score: *row.PriorityScore, loadInfo: loads[a.ID], loadKnown: loads[a.ID] != nil})
		}
		if decision != nil {
			candidate := rankedScheduleCandidate(row)
			candidate.Eligible = available && row.Rank != nil
			// A route with no exact quality evidence may still be ordered by an
			// aggregate/account prior, but the trace must retain that evidence
			// basis instead of labeling ordinary policy dispatch as the source.
			if candidate.AccountQualityPrior != nil {
				candidate.DecisionReason = candidate.QualityBasis
			}
			if reason := filters.accountReasons[row.AccountID]; reason != "" {
				candidate.ExclusionReason = reason
			}
			decision.Candidates = append(decision.Candidates, candidate)
		}
	}
	budget.enableLimit()
	if openAIAccountPriorityRulesActive(req) && !req.DisableStickyEscape {
		budget.limit = max(openAIAccountSelectionProbeLimit, min(10000, 2*len(order)))
		// Avoid-degradation quality tiers are absolute, including when an
		// administrator configures account priorities. Rules break ties within
		// a tier; they must not promote an assessed lower-quality account.
		qualityRows := make(map[int64]OpenAIEvalRankedAccount, len(rows))
		for _, row := range rows {
			qualityRows[row.AccountID] = row
		}
		sort.SliceStable(order, func(i, j int) bool {
			if openAIEffectiveSchedulingPolicy(req) == OpenAIEvalSchedulingPolicyAvoidDegradation {
				if cmp := compareRankingEffectiveQuality(qualityRows[order[i].account.ID], qualityRows[order[j].account.ID]); cmp != 0 {
					return cmp > 0
				}
			}
			return openAIAccountPriorityLess(req, order[i].account.ID, order[j].account.ID)
		})
	}
	req.rankingDecision = decision
	order = s.runtimeRecoveryOrder(req, rows, order, time.Now())
	result, compactBlocked, err := s.tryAcquireOpenAISelectionOrderWithBudget(ctx, req, order, budget)
	if err != nil || result != nil {
		return result, len(eligible), len(order), 0, err
	}
	if s.service.concurrencyService != nil && !budget.acquireExhausted() {
		if fresh, loadErr := s.service.concurrencyService.GetAccountsLoadBatchFresh(ctx, buildOpenAIAccountLoadRequest(eligible)); loadErr == nil {
			for i := range order {
				order[i].loadInfo = fresh[order[i].account.ID]
				order[i].loadKnown = order[i].loadInfo != nil
			}
			var blocked bool
			result, blocked, err = s.tryAcquireOpenAISelectionOrderWithBudget(ctx, req, order, budget)
			compactBlocked = compactBlocked || blocked
			if err != nil || result != nil {
				return result, len(eligible), len(order), 0, err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, len(eligible), len(order), 0, err
	}
	if budget.acquireExhausted() || (budget.limited && budget.rechecks >= budget.probeLimit()) {
		if decision != nil {
			decision.ReasonCode = "selection_budget_exhausted"
			decision.ReasonText = "ranked admission probe budget exhausted"
		}
		return nil, len(eligible), len(order), 0, errors.New("selection_budget_exhausted")
	}
	// Waiting follows the frozen order too. No affinity or score recalculation.
	cfg := s.service.schedulingConfig()
	for _, candidate := range order {
		// A WaitPlan is not an acquired slot and cannot own a retry permit.
		// Keep waiting in the ordinary demoted order, not the trial insertion.
		if candidate.thresholdRecoveryTrial {
			continue
		}
		fresh := s.service.resolveFreshSchedulableOpenAIAccount(ctx, candidate.account, req.Platform, req.RequestedModel, false, req.RequiredCapability)
		if fresh == nil || !s.isAccountRequestCompatible(ctx, fresh, req) || !s.isAccountTransportCompatible(fresh, req.RequiredTransport) {
			continue
		}
		if !s.consumeOpenAISelectionDBRecheck(budget) {
			return nil, len(eligible), len(order), 0, errors.New("selection_budget_exhausted")
		}
		fresh = s.service.recheckSelectedOpenAIAccountFromDB(ctx, fresh, req.GroupID, req.Platform, req.RequestedModel, false, req.RequiredCapability)
		if fresh == nil || !s.isAccountRequestCompatible(ctx, fresh, req) || !s.isAccountTransportCompatible(fresh, req.RequiredTransport) {
			continue
		}
		if req.RequireCompact && openAICompactSupportTier(fresh) == 0 {
			compactBlocked = true
			continue
		}
		return attachSelectionProfitGate(ctx, &AccountSelectionResult{Account: fresh, WaitPlan: &AccountWaitPlan{AccountID: fresh.ID, MaxConcurrency: fresh.Concurrency, Timeout: cfg.FallbackWaitTimeout, MaxWaiting: cfg.FallbackMaxWaiting}}), len(eligible), len(order), 0, nil
	}
	return nil, len(eligible), len(order), 0, noAvailableOpenAISelectionError(req.RequestedModel, compactBlocked, filters.summary("ranked_order_exhausted"))
}

func recordOpenAIRankedSkip(req OpenAIAccountScheduleRequest, accountID int64, reason string) {
	if req.rankingDecision == nil {
		return
	}
	for i := range req.rankingDecision.Candidates {
		candidate := &req.rankingDecision.Candidates[i]
		if candidate.AccountID == accountID {
			candidate.Eligible = false
			candidate.ExclusionReason = reason
			candidate.DecisionReason = "live_admission_skipped"
			return
		}
	}
}
