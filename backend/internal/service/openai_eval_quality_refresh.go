package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

const (
	OpenAIEvalDefaultQualityRefreshIntervalSeconds = 3600
	OpenAIEvalQualityRouteLimit                    = 5000
	openAIEvalQualityAccountBatch                  = 200
)

var ErrOpenAIEvalQualityRefreshSuperseded = errors.New("quality refresh superseded by a configuration change or newer refresh")

type OpenAIEvalQualityRefreshResult struct {
	RefreshedAt   time.Time `json:"refreshed_at"`
	NextRefreshAt time.Time `json:"next_refresh_at"`
	RouteCount    int       `json:"route_count"`
}

type openAIEvalQualityRoute struct {
	AccountID int64
	Model     string
	Effort    string
	Intervals [3]int
}

func (r openAIEvalQualityRoute) key() string {
	dimension, _ := json.Marshal([]string{r.Model, r.Effort})
	return fmt.Sprintf("%d:%s", r.AccountID, dimension)
}

func assessOpenAIEvalQuality(route openAIEvalQualityRoute, evidence []OpenAIEvalQualityAggregate, now time.Time) (OpenAIEvalQualityAssessment, bool) {
	var assessment OpenAIEvalQualityAssessment
	for i, testType := range openAIEvalQualityTestTypes {
		if route.Intervals[i] == 0 {
			continue
		}
		found := false
		for _, quality := range evidence {
			if quality.TestType != testType {
				continue
			}
			if found || !quality.validFor(route.AccountID, route.Model, route.Effort, now) {
				return OpenAIEvalQualityAssessment{}, false
			}
			found = true
			status, _ := quality.diagnosticStatus()
			assessment.EvaluatedCount++
			if status == "pass" {
				assessment.PassCount++
			} else if status == "suspected_normal" {
				assessment.SuspectedPassCount++
			}
			if assessment.ExpiresAt.IsZero() || quality.ExpiresAt.Before(assessment.ExpiresAt) {
				assessment.ExpiresAt = quality.ExpiresAt
			}
		}
		if !found {
			return OpenAIEvalQualityAssessment{}, false
		}
	}
	return assessment, assessment.EvaluatedCount > 0
}

// One process-wide, route-only cache. Persisted evidence is shared by the DB;
// each process rebuilds its own snapshot. No request/session keys are retained.
type openAIEvalQualitySnapshotStore struct {
	mu             sync.Mutex
	signature      [32]byte
	generation     uint64
	configRevision int64
	ticket         uint64
	enabled        bool
	interval       time.Duration
	routes         []openAIEvalQualityRoute
	entries        map[string]OpenAIEvalQualityAssessment
	result         OpenAIEvalQualityRefreshResult
}

var openAIEvalQualitySnapshots = &openAIEvalQualitySnapshotStore{}

func normalizeOpenAIEvalQualityConfig(config *OpenAIEvalConfig) error {
	config.QualityRefreshIntervalSeconds = openAIEvalQualityRefreshSeconds(config)
	if config.QualityRefreshIntervalSeconds < 300 || int64(config.QualityRefreshIntervalSeconds) > OpenAIEvalMaxIntervalSeconds {
		return errors.New("quality_refresh_interval_seconds must be at least 300 and fit integer storage")
	}
	// Inactive custom settings are also validated; zero-valued legacy configs
	// remain valid until the custom policy is explicitly selected.
	normalize := func(weights *OpenAIEvalPolicyWeights, required bool) error {
		if *weights == (OpenAIEvalPolicyWeights{}) && !required {
			return nil
		}
		normalized, err := normalizeOpenAIEvalPolicyWeights(*weights)
		if err != nil {
			return err
		}
		*weights = normalized
		return nil
	}
	if err := normalize(&config.CustomBalance, config.SchedulingPolicy == OpenAIEvalSchedulingPolicyCustomBalance); err != nil {
		return err
	}
	for i := range config.Policies {
		if config.Policies[i].CustomBalance != nil {
			if err := normalize(config.Policies[i].CustomBalance, config.Policies[i].Policy == OpenAIEvalSchedulingPolicyCustomBalance); err != nil {
				return err
			}
		}
	}
	return nil
}

func openAIEvalQualityRoutes(config *OpenAIEvalConfig) ([]openAIEvalQualityRoute, error) {
	if config == nil {
		return nil, nil
	}
	if len(config.Accounts) > OpenAIEvalQualityRouteLimit {
		return nil, errors.New("quality refresh exceeds 5000 configured routes")
	}
	seen := make(map[string]int)
	var routes []openAIEvalQualityRoute
	for _, item := range config.Accounts {
		route := openAIEvalQualityRoute{AccountID: item.AccountID, Model: openAIEvalQualityDimension(item.RequestedModel), Effort: openAIEvalQualityDimension(item.ReasoningEffort)}
		if route.AccountID <= 0 || route.Model == "" {
			continue
		}
		automatic := false
		for i, schedule := range []OpenAIEvalSchedule{item.CandySchedule, item.FingerprintSchedule, item.ModelTraceSchedule} {
			if schedule.Enabled {
				if schedule.IntervalSeconds < 300 || int64(schedule.IntervalSeconds) > OpenAIEvalMaxIntervalSeconds {
					return nil, errors.New("invalid automatic quality test interval")
				}
				route.Intervals[i] = schedule.IntervalSeconds
				automatic = true
			}
		}
		if automatic {
			if index, exists := seen[route.key()]; exists {
				for i := range route.Intervals {
					if route.Intervals[i] > routes[index].Intervals[i] {
						routes[index].Intervals[i] = route.Intervals[i]
					}
				}
			} else {
				seen[route.key()] = len(routes)
				routes = append(routes, route)
			}
		}
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].key() < routes[j].key() })
	return routes, nil
}

func (cache *openAIEvalQualitySnapshotStore) configure(config *OpenAIEvalConfig) error {
	routes, err := openAIEvalQualityRoutes(config)
	if err != nil {
		return err
	}
	interval := openAIEvalQualityRefreshSeconds(config)
	if interval < 300 || int64(interval) > OpenAIEvalMaxIntervalSeconds {
		return errors.New("invalid quality refresh interval")
	}
	enabled := config != nil && config.EffectsEnabled
	revision := int64(0)
	if config != nil {
		revision = config.Revision
	}
	policy := newOpenAIEvalSchedulingPolicySnapshot(config)
	payload, _ := json.Marshal(struct {
		Revision int64
		Enabled  bool
		Interval int
		Routes   []openAIEvalQualityRoute
		Policy   *openAIEvalSchedulingPolicySnapshot
	}{revision, enabled, interval, routes, policy})
	signature := sha256.Sum256(payload)
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if revision < cache.configRevision {
		return ErrOpenAIEvalQualityRefreshSuperseded
	}
	if signature == cache.signature {
		openAIEvalSchedulingPolicy.Store(policy)
		return nil
	}
	cache.signature, cache.enabled = signature, enabled
	cache.configRevision = revision
	cache.generation++
	cache.interval = time.Duration(interval) * time.Second
	cache.routes = routes
	cache.entries = nil
	cache.result = OpenAIEvalQualityRefreshResult{}
	openAIEvalSchedulingPolicy.Store(policy)
	return nil
}

func (cache *openAIEvalQualitySnapshotStore) clear() {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.clearLocked()
}

func (cache *openAIEvalQualitySnapshotStore) clearLocked() {
	cache.generation++
	cache.signature = [32]byte{}
	cache.entries = nil
	cache.result = OpenAIEvalQualityRefreshResult{}
}

func (cache *openAIEvalQualitySnapshotStore) lookup(accountID int64, model, effort string, now time.Time) (OpenAIEvalQualityAssessment, bool) {
	if !OpenAIEvalEffectsEnabled() {
		return OpenAIEvalQualityAssessment{}, false
	}
	key := (openAIEvalQualityRoute{AccountID: accountID, Model: openAIEvalQualityDimension(model), Effort: openAIEvalQualityDimension(effort)}).key()
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if !cache.enabled {
		return OpenAIEvalQualityAssessment{}, false
	}
	assessment, ok := cache.entries[key]
	if !ok || !now.Before(assessment.ExpiresAt) {
		delete(cache.entries, key)
		return OpenAIEvalQualityAssessment{}, false
	}
	return assessment, true
}

// Status describes only a successfully published snapshot on this instance.
// It is runtime response metadata, never persisted or accepted from clients.
func OpenAIEvalQualityRefreshStatus() OpenAIEvalQualityRefreshResult {
	cache := openAIEvalQualitySnapshots
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.result
}

// RefreshOpenAIEvalQuality rebuilds from persisted automatic evidence only.
// The audit records the admin command even if the rebuild subsequently fails.
func (s *OpenAIEvalService) RefreshOpenAIEvalQuality(ctx context.Context, actorID int64) (*OpenAIEvalQualityRefreshResult, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("evaluation service is unavailable")
	}
	if err := s.repo.RecordAuditEvent(ctx, actorID, "quality_refresh_requested", map[string]any{"scope": "configured_auto_routes"}); err != nil {
		return nil, err
	}
	return s.refreshOpenAIEvalQuality(ctx, true)
}

func (s *OpenAIEvalService) refreshOpenAIEvalQuality(ctx context.Context, force bool) (*OpenAIEvalQualityRefreshResult, error) {
	if s == nil || s.repo == nil || s.accounts == nil {
		return nil, errors.New("evaluation quality service is unavailable")
	}
	config, err := s.repo.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	if config != nil {
		if err := normalizeOpenAIEvalQualityConfig(config); err != nil {
			return nil, err
		}
	}
	cache := openAIEvalQualitySnapshots
	if err = cache.configure(config); err != nil {
		return nil, err
	}
	cache.mu.Lock()
	now := time.Now().UTC()
	for key, assessment := range cache.entries {
		if !now.Before(assessment.ExpiresAt) {
			delete(cache.entries, key)
		}
	}
	if !force && !cache.result.RefreshedAt.IsZero() && now.Before(cache.result.NextRefreshAt) {
		result := cache.result
		cache.mu.Unlock()
		return &result, nil
	}
	cache.ticket++
	ticket, generation := cache.ticket, cache.generation
	routes := append([]openAIEvalQualityRoute(nil), cache.routes...)
	enabled := cache.enabled && OpenAIEvalEffectsEnabled()
	interval := cache.interval
	cache.mu.Unlock()
	entries := make(map[string]OpenAIEvalQualityAssessment)
	if enabled {
		ids := make([]int64, 0, len(routes))
		seen := make(map[int64]bool)
		for _, route := range routes {
			if !seen[route.AccountID] {
				ids = append(ids, route.AccountID)
				seen[route.AccountID] = true
			}
		}
		accounts := make(map[int64]*Account, len(ids))
		for start := 0; start < len(ids); start += openAIEvalQualityAccountBatch {
			if err = ctx.Err(); err != nil {
				return nil, err
			}
			batch, lookupErr := s.accounts.GetByIDs(ctx, ids[start:min(start+openAIEvalQualityAccountBatch, len(ids))])
			if lookupErr != nil {
				return nil, lookupErr
			}
			for _, account := range batch {
				if account != nil && seen[account.ID] {
					accounts[account.ID] = account
				}
			}
		}
		for _, route := range routes {
			var evidence []OpenAIEvalQualityAggregate
			for i, testType := range openAIEvalQualityTestTypes {
				if route.Intervals[i] == 0 {
					continue
				}
				quality, ok := readOpenAIEvalQualityEvidence(accounts[route.AccountID], route.Model, route.Effort, testType, now)
				if !ok {
					continue
				}
				// A shorter configured cadence cannot keep an old long-lived
				// record fresh beyond the current evidence freshness contract.
				currentExpiry := quality.EvaluatedAt.Add(openAIEvalQualityFreshness(route.Intervals[i], int(interval/time.Second)))
				if currentExpiry.Before(quality.ExpiresAt) {
					quality.ExpiresAt = currentExpiry
				}
				if now.Before(quality.ExpiresAt) {
					evidence = append(evidence, quality)
				}
			}
			if assessment, ok := assessOpenAIEvalQuality(route, evidence, now); ok {
				entries[route.key()] = assessment
			}
		}
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.generation != generation || cache.ticket != ticket || (enabled && !OpenAIEvalEffectsEnabled()) {
		return nil, ErrOpenAIEvalQualityRefreshSuperseded
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	now = time.Now().UTC()
	for key, assessment := range entries {
		if !now.Before(assessment.ExpiresAt) {
			delete(entries, key)
		}
	}
	cache.entries = entries
	cache.result = OpenAIEvalQualityRefreshResult{RefreshedAt: now, NextRefreshAt: now.Add(interval), RouteCount: len(entries)}
	result := cache.result
	return &result, nil
}
