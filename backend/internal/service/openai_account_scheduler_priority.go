package service

import (
	"context"
	"sort"
	"time"
)

func openAIAccountPriorityRulesActive(req OpenAIAccountScheduleRequest) bool {
	return req.accountPriorityActive
}

// Resolve once against this request's group and hard-eligible candidates.
// A rule on another group/model must leave legacy sticky/subscription paths
// untouched. Retain the group pool so selection does not repeat discovery.
func (s *defaultOpenAIAccountScheduler) resolveAccountPriorityRules(ctx context.Context, req *OpenAIAccountScheduleRequest) error {
	if req.accountPriorityResolved {
		return nil
	}
	req.accountPriorityResolved = true
	if len(req.accountPriorityIndex) == 0 {
		return nil
	}
	accounts, err := s.service.listSchedulableAccounts(ctx, req.GroupID, req.Platform)
	if err != nil {
		return err
	}
	req.accountPriorityPool = accounts
	for i := range accounts {
		a := &accounts[i]
		if _, matched := openAIEvalAccountPriorityFromIndex(req.accountPriorityIndex, a.ID, openAIClientModelForSchedule(*req)); !matched {
			continue
		}
		if _, excluded := req.ExcludedIDs[a.ID]; excluded || !a.IsSchedulable() || a.Platform != NormalizeOpenAICompatiblePlatform(req.Platform) || !a.IsOpenAICompatible() || !s.service.openAIAccountMatchesSchedulingGroup(a, req.GroupID) ||
			!s.isAccountRequestCompatible(ctx, a, *req) || !s.isAccountTransportCompatible(a, req.RequiredTransport) {
			continue
		}
		if len(s.filterGrokFreeQuotaAccounts(ctx, []Account{*a})) == 0 {
			continue
		}
		if req.RequireCompact && openAICompactSupportTier(a) == 0 {
			continue
		}
		if req.Platform == PlatformGrok {
			now := time.Now()
			model := canonicalOpenAIAccountSchedulingModel(a, req.RequestedModel)
			if isGrokTeamModelRateLimited(a, model, now) || isGrokModelQuotaBlocked(a.ID, model, now) {
				continue
			}
		}
		req.accountPriorityActive = true
		break
	}
	return nil
}

func openAIAccountPriorityLess(req OpenAIAccountScheduleRequest, left, right int64) bool {
	lp, lm := openAIEvalAccountPriorityFromIndex(req.accountPriorityIndex, left, openAIClientModelForSchedule(req))
	rp, rm := openAIEvalAccountPriorityFromIndex(req.accountPriorityIndex, right, openAIClientModelForSchedule(req))
	if lm != rm {
		return lm
	}
	return lm && lp < rp
}

func (s *defaultOpenAIAccountScheduler) selectByAccountPriorityLayers(ctx context.Context, req OpenAIAccountScheduleRequest, accounts []*Account, loads map[int64]*AccountLoadInfo, decision *OpenAIAccountScheduleDecision, filters openAISelectionFilterStats, budget *openAISelectionProbeBudget) (*AccountSelectionResult, int, int, float64, error) {
	budget.enableLimit()
	budget.limit = max(openAIAccountSelectionProbeLimit, min(10000, 2*len(accounts)))
	ordered := append([]*Account(nil), accounts...)
	sort.SliceStable(ordered, func(i, j int) bool { return openAIAccountPriorityLess(req, ordered[i].ID, ordered[j].ID) })
	if req.StickyAccountID <= 0 && req.SessionHash != "" && s.service.cache != nil {
		req.StickyAccountID, _ = s.service.getStickySessionAccountID(ctx, req.GroupID, req.SessionHash)
	}
	// Do not permit the legacy weighted-sticky fallback to reintroduce a
	// different rule layer after all live admission attempts.
	req.StickyWeighted = false
	req.rankingDecision = decision
	if decision != nil {
		// Rule layers change admission order, not the pool-relative score shown
		// in diagnostics. Scoring each layer separately makes unrelated accounts
		// appear equally ranked and hides the original policy score.
		plan := s.buildOpenAIAccountLoadPlan(ctx, req, accounts, loads)
		decision.Candidates = buildOpenAIAccountScheduleCandidatesUnbounded(plan, filters.accountReasons, openAIClientModelForSchedule(req), req.RequestedReasoningEffort)
	}
	combined := openAIAccountLoadSelectionAttempt{candidateCount: len(accounts)}
	for begin := 0; begin < len(ordered); {
		end := begin + 1
		for end < len(ordered) && !openAIAccountPriorityLess(req, ordered[begin].ID, ordered[end].ID) {
			end++
		}
		layer := ordered[begin:end]
		attempt := s.trySelectAccountPriorityLayer(ctx, req, layer, loads, budget)
		combined.selectionOrder = append(combined.selectionOrder, attempt.selectionOrder...)
		combined.compactBlocked = combined.compactBlocked || attempt.compactBlocked || attempt.noCompactCandidates
		combined.topK += attempt.topK
		if attempt.err != nil && !attempt.noCompactCandidates {
			return nil, len(accounts), combined.topK, 0, attempt.err
		}
		if attempt.result != nil {
			return attempt.result, len(accounts), combined.topK, 0, nil
		}
		begin = end
	}
	return s.finishLoadBalanceSelectionFallback(ctx, req, combined, budget, filters)
}

func (s *defaultOpenAIAccountScheduler) applyAccountPriorityLayerAffinity(req OpenAIAccountScheduleRequest, plan *openAIAccountLoadPlan) {
	for _, stickyID := range []int64{req.StickyPreviousAccountID, req.StickyAccountID, req.GuardianParentAccountID} {
		if stickyID <= 0 {
			continue
		}
		if _, _, _, escape := s.shouldEscapeStickyAccountForRequest(stickyID, openAIClientModelForSchedule(req), req.RequestedReasoningEffort, s.service.openAIStickyEscapeConfig()); escape {
			continue
		}
		for i, candidate := range plan.selectionOrder {
			if candidate.account.ID == stickyID {
				copy(plan.selectionOrder[1:i+1], plan.selectionOrder[:i])
				plan.selectionOrder[0] = candidate
				return
			}
		}
	}
}

func (s *defaultOpenAIAccountScheduler) trySelectAccountPriorityLayer(ctx context.Context, req OpenAIAccountScheduleRequest, layer []*Account, loads map[int64]*AccountLoadInfo, budget *openAISelectionProbeBudget) openAIAccountLoadSelectionAttempt {
	if !req.SubscriptionPriority {
		return s.trySelectByLoadBalancePool(ctx, req, layer, loads, budget)
	}
	subs, regular := partitionOpenAIChatGPTSubscriptionAccounts(layer)
	if len(subs) == 0 || len(regular) == 0 {
		return s.trySelectByLoadBalancePool(ctx, req, layer, loads, budget)
	}
	first := s.trySelectByLoadBalancePool(ctx, req, subs, loads, budget)
	if first.result != nil || (first.err != nil && !first.noCompactCandidates) {
		return first
	}
	next := s.trySelectByLoadBalancePool(ctx, req, regular, loads, budget)
	next.selectionOrder = append(first.selectionOrder, next.selectionOrder...)
	next.compactBlocked = next.compactBlocked || first.compactBlocked
	next.topK += first.topK
	return next
}
