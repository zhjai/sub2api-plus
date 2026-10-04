package service

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

const openAIRankingDiscoveryLimit = 50000

type openAIRankingScope struct {
	group    Group
	accounts []*Account
	models   map[string]map[string]bool
	routes   []CompositeModelRoute
	channel  *Channel
}

func (r *OpenAIEvalRankingService) discover(ctx context.Context, cfg *OpenAIEvalConfig, observed []openAIRankingObservedRoute) ([]openAIRankingScope, OpenAIEvalRankingCoverage, error) {
	coverage := OpenAIEvalRankingCoverage{Status: "complete", DiscoveryComplete: true, Reasons: []string{}}
	groups := []Group{{Name: "Ungrouped", Platform: PlatformOpenAI, Status: StatusActive}}
	for page := 1; ; page++ {
		batch, meta, err := r.groups.List(ctx, pagination.PaginationParams{Page: page, PageSize: 200, SortBy: "id", SortOrder: "asc"})
		if err != nil {
			return nil, coverage, err
		}
		groups = append(groups, batch...)
		if len(groups) > 5000 {
			coverage.DiscoveryComplete = false
			coverage.Reasons = append(coverage.Reasons, "group_discovery_limit")
			groups = groups[:5000]
			break
		}
		if len(batch) == 0 || (meta != nil && page >= meta.Pages) || (meta == nil && len(batch) < 200) {
			break
		}
		if err := ctx.Err(); err != nil {
			return nil, coverage, err
		}
	}
	channels := make(map[int64]*Channel)
	if r.channels != nil {
		for page := 1; page <= 25; page++ {
			batch, meta, err := r.channels.List(ctx, pagination.PaginationParams{Page: page, PageSize: 200, SortBy: "id", SortOrder: "asc"}, "", "")
			if err != nil {
				return nil, coverage, err
			}
			for i := range batch {
				for _, id := range batch[i].GroupIDs {
					channels[id] = &batch[i]
				}
			}
			if len(batch) == 0 || (meta != nil && page >= meta.Pages) || (meta == nil && len(batch) < 200) {
				break
			}
			if page == 25 {
				coverage.DiscoveryComplete = false
				coverage.Reasons = append(coverage.Reasons, "channel_discovery_limit")
			}
		}
	}
	var accounts []*Account
	accountsComplete := true
	for page := 1; ; page++ {
		batch, meta, err := r.eval.accounts.ListWithFilters(ctx, pagination.PaginationParams{Page: page, PageSize: 500, SortBy: "id", SortOrder: "asc"}, "", "", "", "", 0, "")
		if err != nil {
			return nil, coverage, err
		}
		for i := range batch {
			accounts = append(accounts, &batch[i])
		}
		if len(batch) == 0 || (meta != nil && page >= meta.Pages) || (meta == nil && len(batch) < 500) {
			break
		}
		if len(accounts) >= openAIRankingRowLimit {
			accountsComplete = false
			coverage.DiscoveryComplete = false
			coverage.Reasons = append(coverage.Reasons, "member_discovery_limit")
			break
		}
		if err := ctx.Err(); err != nil {
			return nil, coverage, err
		}
	}
	simple := r.gateway != nil && r.gateway.cfg != nil && r.gateway.cfg.RunMode == config.RunModeSimple
	scopes := make([]openAIRankingScope, 0, len(groups))
	for _, group := range groups {
		scope := openAIRankingScope{group: group, models: make(map[string]map[string]bool), channel: channels[group.ID]}
		if group.Platform != PlatformOpenAI && group.Platform != PlatformGrok && group.Platform != PlatformComposite {
			scopes = append(scopes, scope)
			continue
		}
		for _, account := range accounts {
			member := false
			if simple {
				member = account.Platform == group.Platform || (group.ID == 0 && account.IsOpenAICompatible())
			} else if group.ID == 0 {
				member = len(account.GroupIDs) == 0 && len(account.AccountGroups) == 0
			} else {
				for _, id := range account.GroupIDs {
					if id == group.ID {
						member = true
						break
					}
				}
				if !member {
					for _, binding := range account.AccountGroups {
						if binding.GroupID == group.ID {
							member = true
							break
						}
					}
				}
			}
			if member {
				scope.accounts = append(scope.accounts, account)
			}
		}
		add := func(model, source string) {
			model = strings.TrimSpace(model)
			if model == "" || len(model) > openAIModelTransientMaxModelBytes {
				return
			}
			if strings.ContainsAny(model, "*?") {
				coverage.WildcardRoutesPresent = true
				return
			}
			if len(scope.models) >= openAIRankingDiscoveryLimit {
				coverage.DiscoveryComplete = false
				return
			}
			if scope.models[model] == nil {
				scope.models[model] = make(map[string]bool)
			}
			scope.models[model][source] = true
		}
		for _, model := range OpenAIEvalSupportedModels() {
			add(model.ID, "catalog")
		}
		for _, account := range scope.accounts {
			for model := range account.GetModelMapping() {
				add(model, "account_mapping")
			}
		}
		for model := range group.ModelRouting {
			add(model, "group_route")
		}
		for _, model := range group.ModelAllowlist.Models {
			add(model, "group_route")
		}
		if scope.channel != nil {
			for _, model := range scope.channel.SupportedModels() {
				add(model.Name, "channel_mapping")
			}
			for _, mapping := range scope.channel.ModelMapping {
				for model := range mapping {
					add(model, "channel_mapping")
				}
			}
		}
		if group.Platform == PlatformComposite && r.composite != nil {
			routes, err := r.composite.ListByGroup(ctx, group.ID, false)
			if err != nil {
				return nil, coverage, err
			}
			if len(routes) > openAIRankingDiscoveryLimit {
				coverage.DiscoveryComplete = false
				coverage.Reasons = append(coverage.Reasons, "composite_discovery_limit")
				routes = routes[:openAIRankingDiscoveryLimit]
			}
			scope.routes = routes
			for _, route := range routes {
				if route.MatchType == CompositeRouteMatchExact {
					add(route.PublicModel, "group_route")
				} else {
					coverage.WildcardRoutesPresent = true
				}
			}
		}
		for _, rule := range cfg.Policies {
			add(rule.RequestedModel, "policy_rule")
		}
		for _, route := range cfg.Accounts {
			add(route.RequestedModel, "eval_route")
		}
		for _, route := range observed {
			if route.groupID == group.ID {
				add(route.model, "observed_route")
			}
		}
		if !accountsComplete {
			scope.models = nil
		}
		scopes = append(scopes, scope)
	}
	if coverage.WildcardRoutesPresent {
		coverage.Reasons = append(coverage.Reasons, "wildcard_routes_use_live_fallback")
	}
	return scopes, coverage, nil
}

func rankingSelectionVariants(scope openAIRankingScope, model string) []OpenAIEvalSelectionModelVariant {
	variants := make([]OpenAIEvalSelectionModelVariant, 0, 2)
	for _, endpoint := range []string{CompositeRouteEndpointResponses, CompositeRouteEndpointChatCompletions, CompositeRouteEndpointImages} {
		platform, selection := scope.group.Platform, model
		if platform == PlatformComposite {
			if route, ok := matchCompositeRoute(scope.routes, model, endpoint); ok {
				platform = route.TargetPlatform
				if route.UpstreamModel != "" {
					selection = route.UpstreamModel
				}
			} else if detected, ok := DetectModelPlatform(model); ok {
				platform = detected
			} else {
				platform = ""
				for _, a := range scope.accounts {
					if a.IsModelSupported(model) {
						if platform != "" && platform != a.Platform {
							platform = "ambiguous"
							break
						}
						platform = a.Platform
					}
				}
			}
		}
		if scope.channel != nil {
			mapping := scope.channel.ModelMapping[platform]
			if target, ok := mapping[model]; ok {
				selection = target
			} else {
				longest := ""
				for pattern, target := range mapping {
					if strings.HasSuffix(pattern, "*") && strings.HasPrefix(model, strings.TrimSuffix(pattern, "*")) && len(pattern) > len(longest) {
						longest = pattern
						selection = target
					}
				}
			}
		}
		if selection == "" || selection == "*" {
			selection = model
		}
		variants = append(variants, OpenAIEvalSelectionModelVariant{endpoint, platform, selection})
	}
	return variants
}

func rankingOfflineExclusions(account *Account, scope openAIRankingScope, model string, variants []OpenAIEvalSelectionModelVariant, load *AccountLoadInfo, now time.Time) (bool, []string, []OpenAIEvalRankingExclusion) {
	exclusions := []OpenAIEvalRankingExclusion{}
	add := func(code, kind string) {
		exclusions = append(exclusions, OpenAIEvalRankingExclusion{code, kind, now.UTC()})
	}
	compatible := false
	upstream := []string{}
	for _, variant := range variants {
		if account.Platform == variant.Platform && account.IsOpenAICompatible() && account.IsModelSupported(variant.SelectionModel) {
			compatible = true
			upstream = append(upstream, account.GetMappedModel(variant.SelectionModel))
		}
	}
	if !compatible {
		add("model_or_platform_incompatible", "route")
	}
	if scope.group.ModelAllowlistEnabled() && !scope.group.ModelAllowlist.Allows(model) {
		compatible = false
		add("group_model_restricted", "route")
	}
	if scope.group.RequireOAuthOnly && account.Type == AccountTypeAPIKey {
		compatible = false
		add("oauth_only", "route")
	}
	if scope.group.RequirePrivacySet && !account.IsPrivacySet() {
		add("privacy_not_set", "live")
	}
	if scope.group.Status != StatusActive {
		add("group_inactive", "live")
	}
	if account.Status != StatusActive {
		add("account_inactive", "account")
	}
	if !account.Schedulable {
		add("account_disabled", "account")
	}
	if account.AutoPauseOnExpired && account.ExpiresAt != nil && !now.Before(*account.ExpiresAt) {
		add("account_expired", "live")
	}
	if account.OverloadUntil != nil && now.Before(*account.OverloadUntil) {
		add("overloaded", "live")
	}
	if account.RateLimitResetAt != nil && now.Before(*account.RateLimitResetAt) {
		add("rate_limited", "live")
	}
	if account.TempUnschedulableUntil != nil && now.Before(*account.TempUnschedulableUntil) {
		add("temporary_cooldown", "live")
	}
	if account.IsAPIKeyOrBedrock() && account.IsQuotaExceeded() {
		add("quota_exceeded", "live")
	}
	if load != nil && account.Concurrency > 0 && load.CurrentConcurrency >= account.Concurrency {
		add("concurrency_full_at_evaluation", "live")
	}
	return compatible, dedupeAndSortModelIDs(upstream), exclusions
}

func (r *OpenAIEvalRankingService) build(ctx context.Context, cfg *OpenAIEvalConfig, id, trigger string, observed []openAIRankingObservedRoute) (*openAIRankingGeneration, error) {
	scopes, coverage, err := r.discover(ctx, cfg, observed)
	if err != nil {
		return nil, err
	}
	latest, err := r.readLatest(ctx, cfg)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	if r.observedEvictions > 0 {
		coverage.Reasons = append(coverage.Reasons, "observed_route_catalog_evictions")
	}
	if len(r.observed) >= openAIRankingObservedLimit {
		coverage.Reasons = append(coverage.Reasons, "observed_route_catalog_saturated")
	}
	r.mu.Unlock()
	all := make(map[int64]*Account)
	for _, scope := range scopes {
		for _, a := range scope.accounts {
			all[a.ID] = a
		}
	}
	loads := make(map[int64]*AccountLoadInfo)
	if r.gateway != nil && r.gateway.concurrencyService != nil {
		request := make([]AccountWithConcurrency, 0, len(all))
		for _, a := range all {
			request = append(request, AccountWithConcurrency{ID: a.ID, MaxConcurrency: a.EffectiveLoadFactor()})
		}
		sort.Slice(request, func(i, j int) bool { return request[i].ID < request[j].ID })
		for start := 0; start < len(request); start += 200 {
			batch, loadErr := r.gateway.concurrencyService.PeekAccountsLoadBatch(ctx, request[start:min(start+200, len(request))])
			if loadErr != nil {
				coverage.Reasons = append(coverage.Reasons, "load_unavailable")
				break
			}
			for key, value := range batch {
				loads[key] = value
			}
		}
	}
	monitoring, err := r.monitorEvidence(ctx, all)
	if err != nil {
		coverage.Reasons = append(coverage.Reasons, "monitoring_unavailable")
	}
	now := r.now()
	interval := time.Duration(openAIEvalQualityRefreshSeconds(cfg)) * time.Second
	gen := &openAIRankingGeneration{groups: []OpenAIEvalRankingGroup{}, dimensions: []OpenAIEvalRankingDimension{}, byKey: make(map[string]int), deadline: now.Add(interval)}
	gen.summary = OpenAIEvalRankingSummary{EvaluationID: id, RecordType: "policy_evaluation", Scope: "this_instance", AlgorithmVersion: OpenAIEvalRankingAlgorithmVersion,
		DataVersion: OpenAIEvalDataVersion, EvaluatedAt: now.UTC(), Trigger: trigger, ConfigRevision: cfg.Revision, EffectsEnabled: cfg.EffectsEnabled, NextEvaluationReason: "interval"}
	var oauthRate *float64
	var stats *openAIAccountRuntimeStats
	if r.gateway != nil {
		oauthRate = r.gateway.openAIOAuthSchedulingRateMultiplier(ctx)
		r.gateway.persistentOpenAIAccountScheduler()
		stats = r.gateway.openaiAccountStats
	}
	// Reserve room for group/status metadata; account rows dominate the budget.
	bytes, discovered := 1<<20, 0
	unique := make(map[int64]bool)
	qualityRoutes := make(map[string]bool)
	for _, scope := range scopes {
		group := OpenAIEvalRankingGroup{GroupName: scope.group.Name, MemberCount: len(scope.accounts), Status: "evaluated"}
		if scope.group.ID != 0 {
			group.GroupID = rankingPtr(scope.group.ID)
		}
		if scope.group.Platform != PlatformOpenAI && scope.group.Platform != PlatformGrok && scope.group.Platform != PlatformComposite {
			group.Status = "out_of_scope"
			group.Reason = rankingPtr("platform_not_supported_by_openai_scheduler")
		} else if scope.models == nil {
			group.Status = "live_fallback"
			group.Reason = rankingPtr("member_discovery_limit")
		} else if len(scope.accounts) == 0 {
			group.Status = "no_accounts"
		} else if len(scope.models) == 0 {
			group.Status = "no_models"
		}
		gen.groups = append(gen.groups, group)
		if group.Status == "out_of_scope" || group.Status == "live_fallback" {
			continue
		}
		for _, model := range sortedRankingKeys(scope.models) {
			for _, effort := range []string{"", "minimal", "low", "medium", "high", "xhigh", "max"} {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				discovered++
				if discovered > openAIRankingDiscoveryLimit {
					coverage.DiscoveryComplete = false
					continue
				}
				policy, weights := openAIEvalRankingWeights(cfg, model, effort)
				variants := rankingSelectionVariants(scope, model)
				dim := OpenAIEvalRankingDimension{DimensionID: rankingDimensionKey(scope.group.ID, model, effort), GroupID: group.GroupID, GroupName: group.GroupName,
					RequestedModel: model, ReasoningEffort: effort, SelectionModelVariants: variants, Policy: policy, Weights: weights, Ordering: "score_desc", Sources: sortedRankingKeys(scope.models[model]),
					CandidateCount: rankingPtr(len(scope.accounts)), CoverageStatus: "complete", ValidUntil: rankingPtr(now.Add(interval).UTC())}
				if len(variants) > 0 {
					dim.SelectionModel = rankingPtr(variants[0].SelectionModel)
					for _, v := range variants {
						if v.SelectionModel != *dim.SelectionModel {
							dim.SelectionModel = nil
							break
						}
					}
				}
				if policy == OpenAIEvalSchedulingPolicyAvoidDegradation {
					dim.Ordering = "quality_then_score"
				}
				if policy == "" {
					dim.Ordering = "legacy"
					dim.CoverageStatus = "legacy"
				}
				if len(scope.accounts) == 0 {
					dim.CoverageStatus = "no_candidates"
				}
				if gen.summary.DimensionCount >= openAIRankingDimensionLimit || gen.summary.AccountRowCount+len(scope.accounts) > openAIRankingRowLimit || bytes >= openAIRankingByteLimit {
					dim.CoverageStatus = "live_fallback"
					dim.FallbackReason = rankingPtr("snapshot_capacity")
					dim.ValidUntil = nil
				} else {
					inputs := make([]openAIEvalRankingInput, 0, len(scope.accounts))
					for _, account := range scope.accounts {
						f := stats.rankingFactors(account.ID, model, effort, now)
						f.Quality = qualityFromLatestRuns(cfg, account.ID, model, effort, latest, now)
						applyRankingMonitor(&f, monitoring, account.ID, model, effort, now)
						load := loads[account.ID]
						if load != nil {
							f.Load = OpenAIEvalRankingLoad{OpenAIEvalFactorMeta: rankingKnown(1-clamp01(float64(load.LoadRate)/100), now), LoadRate: rankingPtr(load.LoadRate), Waiting: rankingPtr(load.WaitingCount), CurrentConcurrency: rankingPtr(load.CurrentConcurrency)}
						}
						compatible, upstream, exclusions := rankingOfflineExclusions(account, scope, model, variants, load, now)
						exclusions = append(exclusions, r.offlineRuntimeExclusions(account, variants, now)...)
						inputs = append(inputs, openAIEvalRankingInput{account, f, exclusions, compatible, upstream})
					}
					dim.Accounts = scoreOpenAIEvalRanking(policy, weights, inputs, now, oauthRate)
					eligible := 0
					for _, a := range dim.Accounts {
						if a.Eligible {
							eligible++
							if dim.PreferredAccountID == nil && a.Rank != nil {
								dim.PreferredAccountID = rankingPtr(a.AccountID)
							}
						}
					}
					dim.EligibleCount = rankingPtr(eligible)
					payload, marshalErr := json.Marshal(dim)
					if marshalErr != nil {
						return nil, marshalErr
					}
					if bytes+len(payload) > openAIRankingByteLimit {
						dim.Accounts = nil
						dim.CoverageStatus = "live_fallback"
						dim.FallbackReason = rankingPtr("snapshot_bytes_limit")
						dim.ValidUntil = nil
						dim.PreferredAccountID = nil
					} else {
						bytes += len(payload)
						gen.summary.DimensionCount++
						gen.summary.AccountRowCount += len(dim.Accounts)
						for _, a := range dim.Accounts {
							unique[a.AccountID] = true
							q := a.Factors.Quality
							if q.Known {
								qualityRoutes[(openAIEvalQualityRoute{AccountID: a.AccountID, Model: model, Effort: effort}).key()] = true
							}
							if q.Known && (policy == OpenAIEvalSchedulingPolicyAvoidDegradation || weights.Quality > 0) && q.ExpiresAt != nil && q.ExpiresAt.Before(*dim.ValidUntil) {
								dim.ValidUntil = q.ExpiresAt
							}
							if expiry := rankingFactorExpiry(a.Factors, weights, policy); expiry != nil && expiry.Before(*dim.ValidUntil) {
								dim.ValidUntil = expiry
							}
						}
						if dim.ValidUntil.Before(gen.deadline) {
							gen.deadline = now.Add(dim.ValidUntil.Sub(now))
							gen.summary.NextEvaluationReason = "evidence_expiry"
						}
					}
				}
				if dim.Accounts == nil {
					payload, marshalErr := json.Marshal(dim)
					if marshalErr != nil {
						return nil, marshalErr
					}
					if bytes+len(payload) > openAIRankingByteLimit {
						coverage.DiscoveryComplete = false
						continue
					}
					bytes += len(payload)
				}
				gen.byKey[dim.DimensionID] = len(gen.dimensions)
				gen.dimensions = append(gen.dimensions, dim)
			}
		}
	}
	if coverage.DiscoveryComplete {
		coverage.DiscoveredDimensionCount = rankingPtr(discovered)
		coverage.UncachedDimensionCount = rankingPtr(discovered - gen.summary.DimensionCount)
	}
	coverage.CachedDimensionCount = gen.summary.DimensionCount
	if !coverage.DiscoveryComplete || discovered > gen.summary.DimensionCount || coverage.WildcardRoutesPresent {
		coverage.Status = "partial"
	}
	if discovered == 0 && coverage.DiscoveryComplete {
		coverage.Status = "empty"
	}
	if discovered > gen.summary.DimensionCount {
		coverage.Reasons = append(coverage.Reasons, "uncached_dimensions_use_live_fallback")
	}
	if !coverage.DiscoveryComplete {
		coverage.Reasons = append(coverage.Reasons, "discovery_incomplete")
	}
	gen.summary.AccountCount = len(unique)
	gen.summary.QualityRouteCount = len(qualityRoutes)
	gen.summary.Coverage = coverage
	gen.summary.Truncated = discovered > gen.summary.DimensionCount || !coverage.DiscoveryComplete
	gen.summary.NextEvaluationAt = gen.deadline.UTC()
	return gen, nil
}
