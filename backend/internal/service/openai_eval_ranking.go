package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

const (
	openAIRankingDimensionLimit = 5000
	openAIRankingRowLimit       = 100000
	openAIRankingByteLimit      = 32 << 20
	openAIRankingObservedLimit  = 512
)

type openAIRankingGeneration struct {
	summary         OpenAIEvalRankingSummary
	previousSummary *OpenAIEvalRankingSummary
	groups          []OpenAIEvalRankingGroup
	dimensions      []OpenAIEvalRankingDimension
	byKey           map[string]int
	deadline        time.Time
	overview        []OpenAIEvalAccountOverviewRow
	overviewByID    map[int64]int
	policy          string
	weights         OpenAIEvalRankingWeights
	ordering        string
	priorComplete   bool
	metricVersions  map[openAIAccountRuntimeRouteKey]uint64
}
type openAIRankingBuild struct {
	revision   int64
	generation uint64
	done       chan struct{}
	result     *OpenAIEvalRankingSummary
	err        error
}
type openAIRankingObservedRoute struct {
	groupID       int64
	model, effort string
	at            time.Time
}

// The coordinator lock covers local config commits and immutable publication.
// No account, credential, request ID, or session body is retained in a snapshot.
type OpenAIEvalRankingService struct {
	mu                sync.Mutex
	eval              *OpenAIEvalService
	groups            GroupRepository
	channels          ChannelRepository
	composite         CompositeModelRouteRepository
	gateway           *OpenAIGatewayService
	monitor           *ChannelMonitorService
	now               func() time.Time
	instance          string
	seq               uint64
	generation        uint64
	config            *OpenAIEvalConfig
	current, previous *openAIRankingGeneration
	building          *openAIRankingBuild
	lastError         *OpenAIEvalRankingError
	observed          map[string]openAIRankingObservedRoute
	observedEvictions uint64
}

func NewOpenAIEvalRankingService(eval *OpenAIEvalService, groups GroupRepository, channels ChannelRepository, composite CompositeModelRouteRepository, gateway *OpenAIGatewayService, monitor *ChannelMonitorService) *OpenAIEvalRankingService {
	instance, err := newOpenAIEvalLeaseOwner()
	if err != nil {
		panic("cannot create scheduling evaluation instance ID")
	}
	r := &OpenAIEvalRankingService{eval: eval, groups: groups, channels: channels, composite: composite, gateway: gateway, monitor: monitor,
		now: time.Now, instance: instance, observed: make(map[string]openAIRankingObservedRoute)}
	if eval != nil {
		eval.ranking = r
	}
	if gateway != nil {
		gateway.evalRanking = r
		gateway.persistentOpenAIAccountScheduler()
		gateway.getOpenAIAccountModelTransientState()
		gateway.getOpenAIProxyStreamCircuit()
	}
	return r
}

func cloneRankingConfig(config *OpenAIEvalConfig) *OpenAIEvalConfig {
	if config == nil {
		return &OpenAIEvalConfig{}
	}
	payload, _ := json.Marshal(config)
	var copy OpenAIEvalConfig
	_ = json.Unmarshal(payload, &copy)
	return &copy
}

func (r *OpenAIEvalRankingService) adoptLocked(config *OpenAIEvalConfig) bool {
	if config == nil {
		config = &OpenAIEvalConfig{}
	}
	if r.config != nil && config.Revision < r.config.Revision {
		return false
	}
	if r.config == nil || config.Revision != r.config.Revision {
		if err := SetOpenAIEvalSchedulingPolicySnapshot(config); err != nil {
			r.lastError = &OpenAIEvalRankingError{Code: "EVALUATION_CONFIG_INVALID", Message: err.Error(), ConfigRevision: config.Revision}
			return false
		}
		r.generation++
		r.config = cloneRankingConfig(config)
		r.lastError = nil
	}
	return true
}

func (r *OpenAIEvalRankingService) evaluate(ctx context.Context, trigger string, force bool) (*OpenAIEvalRankingSummary, error) {
	if r == nil || r.eval == nil || r.eval.repo == nil || r.groups == nil || r.eval.accounts == nil {
		return nil, ErrOpenAIEvalRankingUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	config, err := r.eval.repo.GetConfig(ctx)
	if err != nil {
		r.recordError("EVALUATION_CONFIG_UNAVAILABLE", nil)
		return nil, err
	}
	if config == nil {
		config = &OpenAIEvalConfig{}
	}
	if err := normalizeOpenAIEvalQualityConfig(config); err != nil {
		return nil, err
	}
	r.mu.Lock()
	if !r.adoptLocked(config) {
		r.mu.Unlock()
		return nil, ErrOpenAIEvalRankingSuperseded
	}
	if !force && r.current != nil && r.current.summary.ConfigRevision == config.Revision && r.lastError == nil && r.now().Before(r.current.deadline) {
		result := r.current.summary
		r.mu.Unlock()
		return &result, nil
	}
	if build := r.building; build != nil {
		r.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-build.done:
		}
		if build.revision == config.Revision && !force {
			return build.result, build.err
		}
		return r.evaluate(ctx, trigger, force)
	}
	config = cloneRankingConfig(r.config)
	if r.current != nil && r.current.summary.ConfigRevision != config.Revision && trigger != "policy_saved" && trigger != "manual" {
		trigger = "catalog_change"
	}
	r.seq++
	id := fmt.Sprintf("%s-%d", r.instance, r.seq)
	build := &openAIRankingBuild{revision: config.Revision, generation: r.generation, done: make(chan struct{})}
	r.building = build
	observed := r.observedLocked(r.now())
	r.mu.Unlock()
	var snapshot *openAIRankingGeneration
	// Expiring evidence at publication is retried with a fresh input set.
	for attempt := 0; attempt < 2; attempt++ {
		snapshot, err = r.build(ctx, config, id, trigger, observed)
		if err != nil || snapshot == nil || r.now().Before(snapshot.deadline) {
			break
		}
		err = errors.New("evaluation evidence expired while building")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	defer close(build.done)
	r.building = nil
	if build.generation != r.generation || r.config.Revision != build.revision {
		build.err = ErrOpenAIEvalRankingSuperseded
		return nil, build.err
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		build.err = err
		r.lastError = &OpenAIEvalRankingError{Code: "EVALUATION_FAILED", Message: "Scheduling evaluation failed; the saved configuration remains in effect.", ConfigRevision: config.Revision, EvaluationID: rankingPtr(id)}
		return nil, err
	}
	if !r.now().Before(snapshot.deadline) {
		build.err = errors.New("evaluation evidence expired before publication")
		r.lastError = &OpenAIEvalRankingError{Code: "EVIDENCE_EXPIRED", Message: "Evidence expired during evaluation; a refresh will be retried.", ConfigRevision: config.Revision, EvaluationID: rankingPtr(id)}
		return nil, build.err
	}
	snapshot.summary.PublishedAt = r.now().UTC()
	if r.current != nil {
		summary := r.current.summary
		snapshot.previousSummary = &summary
	}
	r.previous, r.current, r.lastError = r.current, snapshot, nil
	// Compatibility quality lookup and ranking publication share this commit.
	cache := openAIEvalQualitySnapshots
	cache.mu.Lock()
	if cache.configRevision > config.Revision {
		cache.mu.Unlock()
		build.err = ErrOpenAIEvalRankingSuperseded
		return nil, build.err
	}
	cache.entries = make(map[string]OpenAIEvalQualityAssessment)
	for _, dim := range snapshot.dimensions {
		for _, account := range dim.Accounts {
			q := account.Factors.Quality
			if q.Known && q.ExpiresAt != nil {
				key := (openAIEvalQualityRoute{AccountID: account.AccountID, Model: openAIEvalQualityDimension(dim.RequestedModel), Effort: dim.ReasoningEffort}).key()
				cache.entries[key] = OpenAIEvalQualityAssessment{EvaluatedCount: q.Selected, PassCount: q.Pass, SuspectedPassCount: q.SuspectedPass, ExpiresAt: *q.ExpiresAt}
			}
		}
	}
	cache.result = OpenAIEvalQualityRefreshResult{RefreshedAt: snapshot.summary.EvaluatedAt, NextRefreshAt: snapshot.summary.NextEvaluationAt, RouteCount: len(cache.entries)}
	cache.mu.Unlock()
	result := snapshot.summary
	build.result = &result
	return &result, nil
}

func (r *OpenAIEvalRankingService) recordError(code string, evaluationID *string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	revision := int64(0)
	if r.config != nil {
		revision = r.config.Revision
	}
	r.lastError = &OpenAIEvalRankingError{Code: code, Message: "Scheduling evaluation is unavailable; refresh will be retried.", ConfigRevision: revision, EvaluationID: evaluationID}
}

func (s *OpenAIEvalService) EvaluateScheduling(ctx context.Context, actorID int64) (*OpenAIEvalRankingSummary, error) {
	if s == nil || s.ranking == nil {
		return nil, ErrOpenAIEvalRankingUnavailable
	}
	if err := s.repo.RecordAuditEvent(ctx, actorID, "scheduling_evaluation_requested", map[string]any{"scope": "this_instance"}); err != nil {
		return nil, err
	}
	return s.ranking.evaluate(ctx, "manual", true)
}

func rankingDimensionKey(groupID int64, model, effort string) string {
	payload, _ := json.Marshal([]any{groupID, model, normalizeOpenAIAccountRuntimeRoutePart(effort, 64)})
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}

func (r *OpenAIEvalRankingService) observedLocked(now time.Time) []openAIRankingObservedRoute {
	out := make([]openAIRankingObservedRoute, 0, len(r.observed))
	for key, route := range r.observed {
		if now.Sub(route.at) >= 24*time.Hour {
			delete(r.observed, key)
			r.observedEvictions++
			continue
		}
		out = append(out, route)
	}
	return out
}

func (r *OpenAIEvalRankingService) observe(groupID int64, model, effort string) {
	if r == nil || model == "" || len(model) > openAIModelTransientMaxModelBytes {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	r.observedLocked(now)
	key := rankingDimensionKey(groupID, model, effort)
	if _, exists := r.observed[key]; !exists && len(r.observed) >= openAIRankingObservedLimit {
		oldestKey := ""
		var oldest time.Time
		for k, v := range r.observed {
			if oldestKey == "" || v.at.Before(oldest) || (v.at.Equal(oldest) && k < oldestKey) {
				oldestKey, oldest = k, v.at
			}
		}
		delete(r.observed, oldestKey)
		r.observedEvictions++
	}
	r.observed[key] = openAIRankingObservedRoute{groupID, model, normalizeOpenAIAccountRuntimeRoutePart(effort, 64), now}
}

func (r *OpenAIEvalRankingService) statusLocked(snapshot *openAIRankingGeneration) OpenAIEvalRankingSnapshot {
	result := OpenAIEvalRankingSnapshot{EffectiveStatus: "live_fallback", EvaluationInProgress: r.building != nil,
		RankingError: r.lastError, Groups: []OpenAIEvalRankingGroup{}, Dimensions: []OpenAIEvalRankingDimension{}}
	if r.config != nil {
		result.CurrentConfigRevision = r.config.Revision
		if !r.config.EffectsEnabled {
			result.EffectiveStatus = "inactive_effects_off"
		} else if !rankingConfigHasPolicy(r.config) {
			result.EffectiveStatus = "inactive_legacy_policy"
		}
	}
	if snapshot != nil {
		summary := snapshot.summary
		result.Summary = &summary
		result.Groups = append([]OpenAIEvalRankingGroup{}, snapshot.groups...)
		if result.EffectiveStatus == "live_fallback" && summary.ConfigRevision == result.CurrentConfigRevision && r.now().Before(snapshot.deadline) {
			result.EffectiveStatus = "active"
			if summary.Coverage.Status == "partial" {
				result.EffectiveStatus = "active_partial"
			}
			if summary.DimensionCount == 0 && len(snapshot.overview) == 0 {
				result.EffectiveStatus = "no_targets"
			}
		}
	}
	if snapshot != nil && snapshot.previousSummary != nil {
		summary := *snapshot.previousSummary
		result.PreviousSummary = &summary
	}
	if r.lastError != nil && result.EffectiveStatus != "inactive_effects_off" && result.EffectiveStatus != "inactive_legacy_policy" {
		result.EffectiveStatus = "error"
	}
	return result
}

func rankingConfigHasPolicy(config *OpenAIEvalConfig) bool {
	if config == nil {
		return false
	}
	if config.SchedulingPolicy != "" {
		return true
	}
	for _, rule := range config.Policies {
		if rule.Policy != "" {
			return true
		}
	}
	return false
}

type rankingCursor struct {
	EvaluationID string `json:"e"`
	Filter       string `json:"f"`
	Offset       int    `json:"o"`
}

func (s *OpenAIEvalService) SchedulingRankings(filter OpenAIEvalRankingFilter) (*OpenAIEvalRankingSnapshot, error) {
	if s == nil || s.ranking == nil {
		return nil, ErrOpenAIEvalRankingUnavailable
	}
	r := s.ranking
	r.mu.Lock()
	defer r.mu.Unlock()
	if filter.Limit <= 0 {
		filter.Limit = 100
	}
	if filter.Limit > 500 {
		return nil, errors.New("ranking limit must be between 1 and 500")
	}
	filterKey, _ := json.Marshal([]any{filter.GroupID, filter.RequestedModel, filter.ReasoningEffort})
	var cursor rankingCursor
	if filter.Cursor != "" {
		payload, err := base64.RawURLEncoding.DecodeString(filter.Cursor)
		if err != nil || len(payload) > 4096 || json.Unmarshal(payload, &cursor) != nil || cursor.Offset < 0 || cursor.Filter != string(filterKey) {
			return nil, errors.New("invalid ranking cursor")
		}
		if filter.EvaluationID != "" && filter.EvaluationID != cursor.EvaluationID {
			return nil, ErrOpenAIEvalRankingSnapshotChanged
		}
		filter.EvaluationID = cursor.EvaluationID
	}
	snapshot := r.current
	if filter.EvaluationID != "" && (snapshot == nil || snapshot.summary.EvaluationID != filter.EvaluationID) {
		snapshot = r.previous
		if snapshot == nil || snapshot.summary.EvaluationID != filter.EvaluationID {
			return nil, ErrOpenAIEvalRankingSnapshotChanged
		}
	}
	result := r.statusLocked(snapshot)
	if snapshot == nil {
		return &result, nil
	}
	complete := filter.GroupID != nil && filter.RequestedModel != nil && filter.ReasoningEffort != nil
	var dimensions []OpenAIEvalRankingDimension
	for _, dim := range snapshot.dimensions {
		groupID := int64(0)
		if dim.GroupID != nil {
			groupID = *dim.GroupID
		}
		if filter.GroupID != nil && groupID != *filter.GroupID {
			continue
		}
		if filter.RequestedModel != nil && dim.RequestedModel != *filter.RequestedModel {
			continue
		}
		if filter.ReasoningEffort != nil && dim.ReasoningEffort != *filter.ReasoningEffort {
			continue
		}
		dimensions = append(dimensions, dim)
	}
	next := func(offset int) *string {
		payload, _ := json.Marshal(rankingCursor{snapshot.summary.EvaluationID, string(filterKey), offset})
		return rankingPtr(base64.RawURLEncoding.EncodeToString(payload))
	}
	if complete {
		if len(dimensions) > 0 {
			dim := dimensions[0]
			if cursor.Offset > len(dim.Accounts) {
				return nil, errors.New("invalid ranking cursor offset")
			}
			end := min(cursor.Offset+filter.Limit, len(dim.Accounts))
			dim.Accounts = append([]OpenAIEvalRankedAccount{}, dim.Accounts[cursor.Offset:end]...)
			if end < len(dimensions[0].Accounts) {
				dim.AccountsNextCursor = next(end)
				result.NextCursor = dim.AccountsNextCursor
			}
			result.Dimensions = []OpenAIEvalRankingDimension{dim}
		}
	} else {
		if cursor.Offset > len(dimensions) {
			return nil, errors.New("invalid ranking cursor offset")
		}
		end := min(cursor.Offset+filter.Limit, len(dimensions))
		for _, dim := range dimensions[cursor.Offset:end] {
			dim.Accounts = nil
			result.Dimensions = append(result.Dimensions, dim)
		}
		if end < len(dimensions) {
			result.NextCursor = next(end)
		}
	}
	// Return owned DTOs so callers cannot mutate a published generation.
	payload, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	var copy OpenAIEvalRankingSnapshot
	err = json.Unmarshal(payload, &copy)
	return &copy, err
}

func (r *OpenAIEvalRankingService) readLatest(ctx context.Context, config *OpenAIEvalConfig) (map[OpenAIEvalEvidenceKey]OpenAIEvalRun, error) {
	result := make(map[OpenAIEvalEvidenceKey]OpenAIEvalRun)
	var keys []OpenAIEvalEvidenceKey
	seen := make(map[OpenAIEvalEvidenceKey]bool)
	for _, route := range config.Accounts {
		for _, testType := range openAIEvalQualityTestTypes {
			if _, selected := openAIEvalQualityTestInterval(config, route.AccountID, route.RequestedModel, route.ReasoningEffort, testType); !selected {
				continue
			}
			key := OpenAIEvalEvidenceKey{route.AccountID, openAIEvalQualityDimension(route.RequestedModel), openAIEvalQualityDimension(route.ReasoningEffort), testType}
			if !seen[key] {
				seen[key] = true
				keys = append(keys, key)
			}
		}
	}
	if len(keys) == 0 {
		return result, nil
	}
	repo, ok := r.eval.repo.(OpenAIEvalLatestEvidenceRepository)
	if !ok {
		return nil, ErrOpenAIEvalRankingUnavailable
	}
	for start := 0; start < len(keys); start += 600 {
		runs, err := repo.LatestCompletedRuns(ctx, keys[start:min(start+600, len(keys))])
		if err != nil {
			return nil, err
		}
		for _, run := range runs {
			key := OpenAIEvalEvidenceKey{run.AccountID, openAIEvalQualityDimension(run.RequestedModel), openAIEvalQualityDimension(run.ReasoningEffort), run.TestType}
			if !seen[key] || run.Status == "running" || run.DiagnosticOnly {
				continue
			}
			previous, exists := result[key]
			if !exists || run.FinishedAt.After(previous.FinishedAt) || (run.FinishedAt.Equal(previous.FinishedAt) && run.ID > previous.ID) {
				result[key] = run
			}
		}
	}
	return result, nil
}

func sortedRankingKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
