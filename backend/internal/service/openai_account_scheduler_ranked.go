package service

import (
	"context"
	"errors"
	"time"
)

func openAIRankedPolicyEnabled(req OpenAIAccountScheduleRequest) bool {
	return openAIEffectiveSchedulingPolicy(req) != ""
}

func (s *defaultOpenAIAccountScheduler) explicitRanking(ctx context.Context, req OpenAIAccountScheduleRequest, accounts []Account, loads map[int64]*AccountLoadInfo) ([]OpenAIEvalRankedAccount, OpenAIEvalRankingTrace, error) {
	model, effort := openAIClientModelForSchedule(req), NormalizeMaxReasoningEffort(req.RequestedReasoningEffort)
	groupID := int64(0)
	if req.GroupID != nil {
		groupID = *req.GroupID
	}
	trace := OpenAIEvalRankingTrace{RecordType: "actual_dispatch", Scope: "this_instance", GroupID: req.GroupID, RankingBasis: "live_fallback", SelectionModel: req.RequestedModel, RankingFallbackReason: rankingPtr("snapshot_missing")}
	trace.RawRequestedReasoningEffort = req.RequestedReasoningEffort
	policySnapshot := openAIEvalSchedulingPolicy.Load().(*openAIEvalSchedulingPolicySnapshot)
	cfg := &OpenAIEvalConfig{EffectsEnabled: policySnapshot.Enabled, SchedulingPolicy: policySnapshot.Default, CustomBalance: policySnapshot.CustomBalance, Policies: policySnapshot.Rules}
	latest := map[OpenAIEvalEvidenceKey]OpenAIEvalRun{}
	var monitoring []openAIRankingMonitorEvidence
	now := time.Now()
	r := s.service.evalRanking
	if r != nil {
		r.mu.Lock()
		cfg = cloneRankingConfig(r.config)
		trace.ConfigRevision = cfg.Revision
		gen := r.current
		if gen != nil && cfg.Revision != gen.summary.ConfigRevision {
			trace.RankingFallbackReason = rankingPtr("config_revision_changed")
		} else if gen != nil {
			key := rankingDimensionKey(groupID, model, effort)
			index, found := gen.byKey[key]
			if !found {
				trace.RankingFallbackReason = rankingPtr("dimension_not_cached")
			} else {
				dim := gen.dimensions[index]
				reason := ""
				if dim.CoverageStatus == "live_fallback" {
					reason = "dimension_capacity_fallback"
				} else if dim.ValidUntil == nil || !now.Before(gen.deadline) || !now.Before(*dim.ValidUntil) {
					reason = "snapshot_expired"
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
					r.mu.Unlock()
					return rows, trace, nil
				}
				trace.RankingFallbackReason = rankingPtr(reason)
			}
		}
		r.mu.Unlock()
		// Fallback uses one complete current normalization pool and fresh latest
		// evidence. It never mixes newly scored accounts into a frozen order.
		var err error
		latest, err = r.readLatest(ctx, cfg)
		if err != nil {
			return nil, trace, err
		}
		pool := make(map[int64]*Account, len(accounts))
		for i := range accounts {
			pool[accounts[i].ID] = &accounts[i]
		}
		monitoring, _ = r.monitorEvidence(ctx, pool)
	}
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
		applyRankingMonitor(&f, monitoring, a.ID, model, effort, now)
		if r == nil {
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
	return scoreOpenAIEvalRanking(policy, weights, inputs, now, s.service.openAIOAuthSchedulingRateMultiplier(ctx)), trace, nil
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
	index, ok := gen.byKey[rankingDimensionKey(groupID, openAIClientModelForSchedule(req), req.RequestedReasoningEffort)]
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
	candidate := OpenAIAccountScheduleCandidate{AccountID: row.AccountID, Rank: row.Rank, PriorityScore: row.PriorityScore, Factors: rankingPtr(row.Factors), Contributions: rankingPtr(row.Contributions), Eligible: row.Eligible,
		EvaluatedCount: q.Evaluated, PassCount: q.Pass, SuspectedPassCount: q.SuspectedPass, QualityRatio: q.Ratio, QualityState: q.State, QualityBasis: OpenAIEvalQualityAssessmentBasis, DecisionReason: "explicit_policy_rank"}
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
	}
	for _, row := range rows {
		a, available := byID[row.AccountID]
		if available && row.Rank != nil {
			order = append(order, openAIAccountCandidateScore{account: a, score: *row.PriorityScore, loadInfo: loads[a.ID], loadKnown: loads[a.ID] != nil})
		}
		if decision != nil {
			candidate := rankedScheduleCandidate(row)
			candidate.Eligible = available && row.Rank != nil
			if reason := filters.accountReasons[row.AccountID]; reason != "" {
				candidate.ExclusionReason = reason
			}
			decision.Candidates = append(decision.Candidates, candidate)
		}
	}
	budget.enableLimit()
	req.rankingDecision = decision
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
	if budget.acquireExhausted() || (budget.limited && budget.rechecks >= openAIAccountSelectionProbeLimit) {
		if decision != nil {
			decision.ReasonCode = "selection_budget_exhausted"
			decision.ReasonText = "ranked admission probe budget exhausted"
		}
		return nil, len(eligible), len(order), 0, errors.New("selection_budget_exhausted")
	}
	// Waiting follows the frozen order too. No affinity or score recalculation.
	cfg := s.service.schedulingConfig()
	for _, candidate := range order {
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
