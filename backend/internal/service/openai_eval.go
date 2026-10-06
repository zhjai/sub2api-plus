package service

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"math"
	"math/rand"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

var ErrOpenAIEvalConfigRevisionConflict = errors.New("OpenAI evaluation config revision conflict")

const (
	OpenAIEvalTypeCandy       = "candy"
	OpenAIEvalTypeFingerprint = "fingerprint"
	OpenAIEvalTypeModelTrace  = "modeltrace"
	OpenAIEvalTypeStateProbe  = "state_probe"
	// OpenAIEvalBPSAccountEffort separates account-scoped BPS probes from
	// legacy route/model State Probe schedules. It is an internal scheduler
	// dimension, never a user-facing reasoning effort.
	OpenAIEvalBPSAccountEffort          = "__bps_account__"
	OpenAIEvalDataVersion               = OpenAIEvalQualityDataVersion
	OpenAIEvalDefaultMaxRequestAttempts = 3
	OpenAIEvalBaselineVersion           = OpenAIEvalQualityBaselineVersion
	// All user-facing evaluation schedules share the same 5-minute floor.
	OpenAIEvalMinFingerprintInterval     = 5 * time.Minute
	OpenAIEvalFingerprintQuickSamples    = 60
	OpenAIEvalFingerprintStandardSamples = 200
	OpenAIEvalFingerprintStrictSamples   = 400
	openAIEvalFingerprintQuickCells      = 4
	openAIEvalFingerprintStandardCells   = 8
	openAIEvalFingerprintStrictCells     = 16
	openAIEvalFingerprintQuickRepeats    = 15
	openAIEvalFingerprintStandardRepeats = 25
	openAIEvalFingerprintStrictRepeats   = 25
	openAIEvalFingerprintPermutationN    = 1000
	OpenAIEvalModelTraceRequests         = 3
	OpenAIEvalModelTraceMinInterval      = 5 * time.Minute
)

// OpenAIEvalSchedulingPolicy controls how eligible accounts are ranked after
// capability, session, cooldown, and evaluation gates have run.  The empty
// value is kept as the legacy/default scheduler behaviour.
const (
	OpenAIEvalSchedulingPolicyLegacy           = ""
	OpenAIEvalSchedulingPolicyCostFirst        = "cost_first"
	OpenAIEvalSchedulingPolicyStabilityFirst   = "stability_first"
	OpenAIEvalSchedulingPolicyAvoidDegradation = "avoid_degradation"
	OpenAIEvalSchedulingPolicyCustomBalance    = "custom_balance"
)

// OpenAIEvalBPSMode is an explicit replacement for the historical bps_auto
// boolean.  The boolean remains on the wire for old clients and is normalized
// to auto when no explicit mode is supplied.
const (
	OpenAIEvalBPSModeAuto     = "auto"
	OpenAIEvalBPSModeForceOn  = "force_on"
	OpenAIEvalBPSModeForceOff = "force_off"
)

// The pinned shape-selectable question is distinct from historical 29 runs.
const OpenAIEvalCandyExpectedAnswer = 21

// CPA Fingerprint reference data is vendored from the pinned CPA release.
// The upstream MIT license and copyright notice are kept beside these data files.
//
//go:embed data/cpa_fingerprint_probes_97623969.json
var openAIEvalFingerprintProbesJSON []byte

//go:embed data/cpa_fingerprint_baselines_97623969.json
var openAIEvalFingerprintBaselinesJSON []byte

var OpenAIEvalFingerprintProbes = mustDecodeOpenAIEvalJSON[[]OpenAIEvalProbe](openAIEvalFingerprintProbesJSON)
var OpenAIEvalFingerprintBaselines = mustDecodeOpenAIEvalJSON[[]OpenAIEvalFingerprintBaseline](openAIEvalFingerprintBaselinesJSON)

func mustDecodeOpenAIEvalJSON[T any](data []byte) T {
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		panic(err)
	}
	return value
}

// OpenAIEvalOutcome is a route-scoped diagnostic. Fingerprint identity
// evidence is intentionally separate from capability outcomes.
type OpenAIEvalOutcome struct {
	Status        string                       `json:"status"`
	Reason        string                       `json:"reason,omitempty"`
	Score         float64                      `json:"score,omitempty"`
	SampleCount   int                          `json:"sample_count"`
	ExpectedCount int                          `json:"expected_count"`
	Confidence    string                       `json:"confidence"`
	Scheduling    string                       `json:"scheduling"`
	Attribution   *OpenAIEvalAttributionPolicy `json:"attribution,omitempty"`
	Fingerprint   *OpenAIEvalFingerprintResult `json:"fingerprint,omitempty"`
	ModelTrace    *OpenAIEvalModelTraceResult  `json:"modeltrace,omitempty"`
	StateProbe    *OpenAIStateProbeResult      `json:"state_probe,omitempty"`
}

// Raw verdict metadata survives reinterpretation of historical evidence.
type OpenAIEvalAttributionPolicy struct {
	RuleVersion         string `json:"rule_version"`
	OriginalRuleVersion string `json:"original_rule_version"`
	OriginalStatus      string `json:"original_status"`
	OriginalReason      string `json:"original_reason,omitempty"`
}

// OpenAIEvalRouteHealth is scoped to account + public requested model +
// reasoning effort. It is not an account-wide model-quality verdict.
type OpenAIEvalRouteHealth struct {
	AccountID         int64     `json:"account_id"`
	RequestedModel    string    `json:"requested_model"`
	ReasoningEffort   string    `json:"reasoning_effort"`
	HardFailureStreak int       `json:"hard_failure_streak"`
	LastFailureCode   string    `json:"last_failure_code,omitempty"`
	Penalty           float64   `json:"penalty"`
	PenaltyUntil      time.Time `json:"penalty_until,omitempty"`
	UpdatedAt         time.Time `json:"updated_at"`
}

const OpenAIEvalRouteHealthTTL = 30 * time.Minute

type openAIEvalSchedulingPolicySnapshot struct {
	AccountPriorities map[int64]openAIEvalAccountPriorityIndex
	Thresholds        OpenAIEvalSchedulingThresholds
	Enabled           bool
	Default           string
	CustomBalance     OpenAIEvalPolicyWeights
	Rules             []OpenAIEvalSchedulingPolicyRule
}

var openAIEvalSchedulingPolicy atomic.Value // *openAIEvalSchedulingPolicySnapshot

func init() {
	openAIEvalSchedulingPolicy.Store(&openAIEvalSchedulingPolicySnapshot{})
}

func OpenAIEvalEffectsEnabled() bool {
	return openAIEvalSchedulingPolicy.Load().(*openAIEvalSchedulingPolicySnapshot).Enabled
}

func SetOpenAIEvalEffectsEnabled(enabled bool) {
	cache := openAIEvalQualitySnapshots
	cache.mu.Lock()
	defer cache.mu.Unlock()
	snapshot := *openAIEvalSchedulingPolicy.Load().(*openAIEvalSchedulingPolicySnapshot)
	if snapshot.Enabled != enabled {
		snapshot.Enabled = enabled
		openAIEvalSchedulingPolicy.Store(&snapshot)
	}
}

func SetOpenAIEvalSchedulingPolicySnapshot(config *OpenAIEvalConfig) error {
	// The revision guard applies to effects and policy as well as the cache.
	// A rejected configuration leaves the last accepted settings intact.
	return openAIEvalQualitySnapshots.configure(config)
}

func newOpenAIEvalSchedulingPolicySnapshot(config *OpenAIEvalConfig) (*openAIEvalSchedulingPolicySnapshot, error) {
	snapshot := &openAIEvalSchedulingPolicySnapshot{}
	if config != nil {
		snapshot.Thresholds = openAIEvalSchedulingThresholds(config)
		snapshot.Enabled = config.EffectsEnabled
		snapshot.Default = config.SchedulingPolicy
		var priorityErr error
		snapshot.AccountPriorities, priorityErr = buildOpenAIEvalAccountPriorityIndex(config.AccountPriorityRules)
		if priorityErr != nil {
			return nil, fmt.Errorf("build account priority rules: %w", priorityErr)
		}
		snapshot.CustomBalance = config.CustomBalance
		snapshot.CustomBalance.AbsolutePriorities = append([]string(nil), config.CustomBalance.AbsolutePriorities...)
		snapshot.Rules = append([]OpenAIEvalSchedulingPolicyRule(nil), config.Policies...)
		for i := range snapshot.Rules {
			if snapshot.Rules[i].CustomBalance != nil {
				weights := *snapshot.Rules[i].CustomBalance
				weights.AbsolutePriorities = append([]string(nil), weights.AbsolutePriorities...)
				snapshot.Rules[i].CustomBalance = &weights
			}
		}
	}
	return snapshot, nil
}

func OpenAIEvalSchedulingPolicyForRequest(model, effort string) string {
	// Evaluation policies are an opt-in routing effect.  Keep the configured
	// policy available for the admin preview/persistence layer, but never let a
	// disabled evaluation switch alter production account ordering.
	value := openAIEvalSchedulingPolicy.Load()
	snapshot, _ := value.(*openAIEvalSchedulingPolicySnapshot)
	if snapshot == nil || !snapshot.Enabled {
		return OpenAIEvalSchedulingPolicyLegacy
	}
	return OpenAIEvalSchedulingPolicyFor(&OpenAIEvalConfig{SchedulingPolicy: snapshot.Default, CustomBalance: snapshot.CustomBalance, Policies: snapshot.Rules}, model, effort)
}

// OpenAIEvalCustomBalanceForRequest returns the normalized custom weights for
// a model/effort route. It is intentionally read-only and follows the same
// most-specific rule resolution as OpenAIEvalSchedulingPolicyFor.
func OpenAIEvalCustomBalanceForRequest(model, effort string) (OpenAIEvalPolicyWeights, bool) {
	value := openAIEvalSchedulingPolicy.Load()
	snapshot, _ := value.(*openAIEvalSchedulingPolicySnapshot)
	if snapshot == nil || !snapshot.Enabled {
		return OpenAIEvalPolicyWeights{}, false
	}
	policy := OpenAIEvalSchedulingPolicyFor(&OpenAIEvalConfig{SchedulingPolicy: snapshot.Default, CustomBalance: snapshot.CustomBalance, Policies: snapshot.Rules}, model, effort)
	if policy != OpenAIEvalSchedulingPolicyCustomBalance {
		return OpenAIEvalPolicyWeights{}, false
	}
	weights := snapshot.CustomBalance
	model = strings.ToLower(strings.TrimSpace(model))
	effort = strings.ToLower(strings.TrimSpace(effort))
	for _, rule := range snapshot.Rules {
		if rule.CustomBalance == nil || !strings.EqualFold(strings.TrimSpace(rule.RequestedModel), model) {
			continue
		}
		if strings.TrimSpace(rule.ReasoningEffort) != "" && !strings.EqualFold(strings.TrimSpace(rule.ReasoningEffort), effort) {
			continue
		}
		if strings.TrimSpace(rule.ReasoningEffort) != "" {
			weights = *rule.CustomBalance
			break
		}
		weights = *rule.CustomBalance
	}
	normalized, err := normalizeOpenAIEvalPolicyWeights(weights)
	if err != nil {
		return OpenAIEvalPolicyWeights{}, false
	}
	return normalized, true
}

func OpenAIEvalRouteHealthKey(model, effort string) string {
	return strings.ToLower(strings.TrimSpace(model)) + "\x00" + strings.ToLower(strings.TrimSpace(effort))
}

func normalizeOpenAIEvalBPSMode(mode string, legacyAuto bool) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case OpenAIEvalBPSModeForceOn:
		return OpenAIEvalBPSModeForceOn
	case OpenAIEvalBPSModeForceOff:
		return OpenAIEvalBPSModeForceOff
	case OpenAIEvalBPSModeAuto:
		return OpenAIEvalBPSModeAuto
	default:
		if legacyAuto {
			return OpenAIEvalBPSModeAuto
		}
		return OpenAIEvalBPSModeForceOff
	}
}

// OpenAIEvalBPSProbeModel returns the model used by an account-scoped BPS
// health probe. An empty admin value intentionally means the stable native
// OpenAI test model, while the selected model never changes the account-wide
// BPS state scope.
func OpenAIEvalBPSProbeModel(model string) string {
	model = strings.TrimSpace(model)
	if model != "" {
		return model
	}
	return openai.DefaultTestModel
}

func openAIEvalBPSModeEnabled(route OpenAIEvalAccountConfig) bool {
	switch normalizeOpenAIEvalBPSMode(route.BPSMode, route.BPSAuto) {
	case OpenAIEvalBPSModeForceOn, OpenAIEvalBPSModeAuto:
		return true
	default:
		return false
	}
}

func normalizeOpenAIEvalSchedulingPolicy(policy string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "", OpenAIEvalSchedulingPolicyCostFirst, OpenAIEvalSchedulingPolicyStabilityFirst, OpenAIEvalSchedulingPolicyAvoidDegradation, OpenAIEvalSchedulingPolicyCustomBalance:
		return strings.ToLower(strings.TrimSpace(policy)), nil
	default:
		return "", fmt.Errorf("unsupported scheduling policy %q", policy)
	}
}

func normalizeOpenAIEvalPolicyWeights(weights OpenAIEvalPolicyWeights) (OpenAIEvalPolicyWeights, error) {
	absolute, err := normalizeOpenAIEvalAbsolutePriorities(weights.AbsolutePriorities)
	if err != nil {
		return OpenAIEvalPolicyWeights{}, err
	}
	values := []float64{weights.Cost, weights.Stability, weights.ErrorRate, weights.TTFT, weights.Load, weights.Quality}
	total := 0.0
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return OpenAIEvalPolicyWeights{}, errors.New("custom balance weights must be finite and non-negative")
		}
		total += value
	}
	if math.IsInf(total, 0) {
		return OpenAIEvalPolicyWeights{}, errors.New("custom balance weight total must be finite")
	}
	if total <= 0 {
		if len(absolute) > 0 {
			weights.AbsolutePriorities = absolute
			return weights, nil
		}
		return OpenAIEvalPolicyWeights{}, errors.New("custom balance requires at least one positive weight")
	}
	if weights.Stability == 0 && math.Abs(total-1) <= 1e-12 {
		weights.AbsolutePriorities = absolute
		return weights, nil
	}
	return OpenAIEvalPolicyWeights{
		Cost:               weights.Cost / total,
		Stability:          0,
		ErrorRate:          weights.ErrorRate/total + 0.6*(weights.Stability/total),
		TTFT:               weights.TTFT/total + 0.4*(weights.Stability/total),
		Load:               weights.Load / total,
		Quality:            weights.Quality / total,
		AbsolutePriorities: absolute,
	}, nil
}

// OpenAIEvalSchedulingPolicyFor resolves the most specific configured rule.
// It is intentionally a pure helper so the scheduler and admin preview can
// share exactly the same model/effort matching semantics.
func OpenAIEvalSchedulingPolicyFor(config *OpenAIEvalConfig, model, effort string) string {
	if config == nil {
		return OpenAIEvalSchedulingPolicyLegacy
	}
	model = strings.ToLower(strings.TrimSpace(model))
	effort = strings.ToLower(strings.TrimSpace(effort))
	policy := strings.ToLower(strings.TrimSpace(config.SchedulingPolicy))
	if normalized, err := normalizeOpenAIEvalSchedulingPolicy(policy); err == nil {
		policy = normalized
	} else {
		policy = OpenAIEvalSchedulingPolicyLegacy
	}
	for _, rule := range config.Policies {
		if !strings.EqualFold(strings.TrimSpace(rule.RequestedModel), model) {
			continue
		}
		if strings.TrimSpace(rule.ReasoningEffort) != "" && !strings.EqualFold(strings.TrimSpace(rule.ReasoningEffort), effort) {
			continue
		}
		normalized, err := normalizeOpenAIEvalSchedulingPolicy(rule.Policy)
		if err != nil || normalized == "" {
			continue
		}
		if strings.TrimSpace(rule.ReasoningEffort) != "" {
			return normalized
		}
		policy = normalized
	}
	return policy
}

func OpenAIEvalRouteHealthExtraKeyFor(model, effort string) string {
	digest := sha256.Sum256([]byte(OpenAIEvalRouteHealthKey(model, effort)))
	return "openai_eval_route_health_" + hex.EncodeToString(digest[:8])
}

// readOpenAIEvalRouteHealthStateFromAccount returns the persisted state even
// while its penalty is still below the scheduler activation threshold.  The
// writer must use this raw view so a first hard failure is not lost when the
// active-only reader quite correctly returns "not active".
func readOpenAIEvalRouteHealthStateFromAccount(account *Account, model, effort string) (OpenAIEvalRouteHealth, bool) {
	if account == nil || account.Extra == nil {
		return OpenAIEvalRouteHealth{}, false
	}
	value, ok := account.Extra[OpenAIEvalRouteHealthExtraKeyFor(model, effort)]
	if !ok {
		return OpenAIEvalRouteHealth{}, false
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return OpenAIEvalRouteHealth{}, false
	}
	var health OpenAIEvalRouteHealth
	if json.Unmarshal(encoded, &health) != nil || health.UpdatedAt.IsZero() {
		return OpenAIEvalRouteHealth{}, false
	}
	return health, true
}

func ReadOpenAIEvalRouteHealthFromAccount(account *Account, model, effort string, now time.Time) (OpenAIEvalRouteHealth, bool) {
	health, ok := readOpenAIEvalRouteHealthStateFromAccount(account, model, effort)
	if !ok || health.Penalty <= 0 {
		return OpenAIEvalRouteHealth{}, false
	}
	if !health.PenaltyUntil.IsZero() && now.After(health.PenaltyUntil) {
		return OpenAIEvalRouteHealth{}, false
	}
	return health, true
}

type OpenAIEvalRun struct {
	DiagnosticOnly   bool                     `json:"diagnostic_only,omitempty"`
	Protocol         string                   `json:"protocol,omitempty"`
	ID               int64                    `json:"id"`
	AccountID        int64                    `json:"account_id"`
	TestType         string                   `json:"test_type"`
	RequestedModel   string                   `json:"requested_model"`
	UpstreamModel    string                   `json:"upstream_model,omitempty"`
	ReasoningEffort  string                   `json:"reasoning_effort"`
	DataVersion      string                   `json:"data_version"`
	BaselineVersion  string                   `json:"baseline_version"`
	Status           string                   `json:"status"`
	Outcome          OpenAIEvalOutcome        `json:"outcome"`
	RequestCount     int                      `json:"request_count"`
	InputTokens      int64                    `json:"input_tokens"`
	OutputTokens     int64                    `json:"output_tokens"`
	CostEstimateUSD  *float64                 `json:"cost_estimate_usd"`
	DurationMS       int64                    `json:"duration_ms"`
	StartedAt        time.Time                `json:"started_at"`
	FinishedAt       time.Time                `json:"finished_at"`
	TriggeredBy      int64                    `json:"triggered_by,omitempty"`
	TriggerSource    string                   `json:"trigger_source"`
	Error            string                   `json:"error,omitempty"`
	SampleCount      int                      `json:"sample_count"`
	ExpectedSamples  int                      `json:"expected_samples"`
	CompletedSamples int                      `json:"completed_samples"`
	Phase            string                   `json:"phase,omitempty"`
	Samples          []OpenAIEvalSampleRecord `json:"samples,omitempty"`
}

type OpenAIEvalSampleRecord struct {
	ProbeID          string                   `json:"probe_id"`
	NormalizedAnswer string                   `json:"normalized_answer,omitempty"`
	Valid            bool                     `json:"valid"`
	ErrorCode        string                   `json:"error_code,omitempty"`
	Answer           string                   `json:"answer,omitempty"`
	Attempts         int                      `json:"attempts"`
	ErrorMessage     string                   `json:"error_message,omitempty"`
	AttemptErrors    []OpenAIEvalAttemptError `json:"attempt_errors,omitempty"`
	HTTPStatus       int                      `json:"http_status,omitempty"`
}

type OpenAIEvalAttemptError struct {
	Attempt    int    `json:"attempt"`
	Code       string `json:"code"`
	Message    string `json:"message"`
	HTTPStatus int    `json:"http_status,omitempty"`
}

type OpenAIEvalRunFilter struct {
	AccountID       int64
	RequestedModel  string
	ReasoningEffort string
	TestType        string
	Limit           int
}

type OpenAIEvalScheduledRun struct {
	AccountID       int64
	TestType        string
	RequestedModel  string
	ReasoningEffort string
	SampleMode      string
	SampleCount     int
}

type OpenAIEvalAuditEvent struct {
	ID        int64          `json:"id"`
	ActorID   int64          `json:"actor_id"`
	Action    string         `json:"action"`
	Payload   map[string]any `json:"payload"`
	CreatedAt time.Time      `json:"created_at"`
}

type OpenAIEvalRepository interface {
	GetConfig(context.Context) (*OpenAIEvalConfig, error)
	SaveConfig(context.Context, *OpenAIEvalConfig, int64) error
	CreateRun(context.Context, *OpenAIEvalRun) (int64, error)
	FinishRun(context.Context, int64, *OpenAIEvalRun) error
	ListRuns(context.Context, OpenAIEvalRunFilter) ([]OpenAIEvalRun, error)
	ListAuditEvents(context.Context, int) ([]OpenAIEvalAuditEvent, error)
	RecordAuditEvent(context.Context, int64, string, map[string]any) error
	ClaimDueSchedules(context.Context, time.Time, int) ([]OpenAIEvalScheduledRun, error)
	AcquireLease(context.Context, string, string, time.Duration) (bool, error)
	RenewLease(context.Context, string, string, time.Duration) (bool, error)
	ReleaseLease(context.Context, string, string) error
}

// OpenAIEvalProgressRepository is optional so in-memory test doubles and
// rolling-upgrade adapters can keep using the base repository contract.
type OpenAIEvalProgressRepository interface {
	UpdateRunProgress(context.Context, int64, *OpenAIEvalRun) error
}

type OpenAIEvalSchedule struct {
	Enabled         bool       `json:"enabled"`
	IntervalSeconds int        `json:"interval_seconds"`
	JitterSeconds   int        `json:"jitter_seconds"`
	SampleMode      string     `json:"sample_mode,omitempty"`
	SampleCount     int        `json:"sample_count,omitempty"`
	LastRunAt       *time.Time `json:"last_run_at,omitempty"`
	NextRunAt       *time.Time `json:"next_run_at,omitempty"`
}

type OpenAIEvalAccountConfig struct {
	AccountID           int64                `json:"account_id"`
	RequestedModel      string               `json:"requested_model"`
	ReasoningEffort     string               `json:"reasoning_effort"`
	CandySchedule       OpenAIEvalSchedule   `json:"candy_schedule"`
	FingerprintSchedule OpenAIEvalSchedule   `json:"fingerprint_schedule"`
	ModelTraceSchedule  OpenAIEvalSchedule   `json:"modeltrace_schedule"`
	StateProbeSchedule  OpenAIEvalSchedule   `json:"state_probe_schedule"`
	BPSAuto             bool                 `json:"bps_auto"`
	BPSMode             string               `json:"bps_mode,omitempty"`
	BPSState            *OpenAIBPSModelState `json:"bps_state,omitempty"`
	DirectOAuthEligible bool                 `json:"direct_oauth_eligible"`
}

// OpenAIEvalBPSAccountConfig is independent from the account/model/effort
// evaluation targets. The probe model identifies the health check only; a
// degraded OAuth account switches as a whole.
type OpenAIEvalBPSAccountConfig struct {
	AccountID         int64      `json:"account_id"`
	ProbeModel        string     `json:"probe_model,omitempty"`
	Mode              string     `json:"mode"`
	FailureThreshold  int        `json:"failure_threshold"`
	RecoveryThreshold int        `json:"recovery_threshold"`
	IntervalSeconds   int        `json:"interval_seconds"`
	Active            bool       `json:"active"`
	State             string     `json:"state,omitempty"`
	DisabledReason    string     `json:"disabled_reason,omitempty"`
	DegradedStreak    int        `json:"degraded_streak"`
	HealthyStreak     int        `json:"healthy_streak"`
	UpdatedAt         *time.Time `json:"updated_at,omitempty"`
	LastRunAt         *time.Time `json:"last_run_at,omitempty"`
	NextRunAt         *time.Time `json:"next_run_at,omitempty"`
}

type OpenAIEvalPolicyWeights struct {
	Cost               float64  `json:"cost"`
	Stability          float64  `json:"stability"`
	ErrorRate          float64  `json:"error_rate"`
	TTFT               float64  `json:"ttft"`
	Load               float64  `json:"load"`
	Quality            float64  `json:"quality"`
	AbsolutePriorities []string `json:"absolute_priorities,omitempty"`
}

func openAIEvalPolicyWeightsZero(weights OpenAIEvalPolicyWeights) bool {
	return weights.Cost == 0 && weights.Stability == 0 && weights.ErrorRate == 0 && weights.TTFT == 0 && weights.Load == 0 && weights.Quality == 0 && len(weights.AbsolutePriorities) == 0
}

var openAIEvalAbsolutePriorityFactors = map[string]string{
	"cost": "cost", "price": "cost", "error_rate": "error_rate", "errors": "error_rate",
	"ttft": "ttft", "latency": "ttft", "load": "load", "quality": "quality",
}

func normalizeOpenAIEvalAbsolutePriorities(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, raw := range values {
		key := strings.ToLower(strings.TrimSpace(raw))
		canonical, ok := openAIEvalAbsolutePriorityFactors[key]
		if !ok {
			return nil, fmt.Errorf("unsupported absolute priority %q", raw)
		}
		if _, duplicate := seen[canonical]; duplicate {
			return nil, fmt.Errorf("duplicate absolute priority %q", canonical)
		}
		seen[canonical] = struct{}{}
		out = append(out, canonical)
	}
	return out, nil
}

func openAIEvalHasAbsolutePriority(values []string, factor string) bool {
	for _, value := range values {
		if value == factor {
			return true
		}
	}
	return false
}

func openAIEvalRankingUsesQuality(policy string, weights OpenAIEvalRankingWeights) bool {
	return policy == OpenAIEvalSchedulingPolicyAvoidDegradation ||
		weights.Quality > 0 || openAIEvalHasAbsolutePriority(weights.AbsolutePriorities, "quality")
}

type OpenAIEvalConfig struct {
	AccountPriorityRules          []OpenAIEvalAccountPriorityRule `json:"account_priority_rules,omitempty"`
	QualityRefreshIntervalSeconds int                             `json:"quality_refresh_interval_seconds"`
	MaxRequestAttempts            int                             `json:"max_request_attempts"`
	// Revision is optimistic-concurrency metadata.  Zero is accepted for
	// legacy clients and is upgraded atomically by the repository.
	Revision             int64                            `json:"revision,omitempty"`
	EffectsEnabled       bool                             `json:"effects_enabled"`
	BPSAutoEnabled       bool                             `json:"bps_auto_enabled"`
	SchedulingPolicy     string                           `json:"scheduling_policy,omitempty"`
	Policies             []OpenAIEvalSchedulingPolicyRule `json:"policies,omitempty"`
	CustomBalance        OpenAIEvalPolicyWeights          `json:"custom_balance,omitempty"`
	SchedulingThresholds *OpenAIEvalSchedulingThresholds  `json:"scheduling_thresholds,omitempty"`
	BPSAccounts          []OpenAIEvalBPSAccountConfig     `json:"bps_accounts,omitempty"`
	Accounts             []OpenAIEvalAccountConfig        `json:"accounts"`
}

// OpenAIEvalSchedulingPolicyRule scopes a policy to the requested public
// model and, optionally, reasoning effort.  An empty effort is the model
// default.  More specific effort rules win at read time.
type OpenAIEvalSchedulingPolicyRule struct {
	RequestedModel  string                   `json:"requested_model"`
	ReasoningEffort string                   `json:"reasoning_effort,omitempty"`
	Policy          string                   `json:"policy"`
	CustomBalance   *OpenAIEvalPolicyWeights `json:"custom_balance,omitempty"`
}

type OpenAIEvalProbe struct {
	ID           string   `json:"id"`
	Kind         string   `json:"kind"`
	Low          int      `json:"lo"`
	High         int      `json:"hi"`
	Instructions string   `json:"instructions"`
	Prompts      []string `json:"prompts"`
}

type OpenAIEvalFingerprintBaseline struct {
	Model string              `json:"model"`
	Cells map[string][]string `json:"cells"`
}

type OpenAIEvalFingerprintResult struct {
	Status          string    `json:"status"`
	NearestModel    string    `json:"nearest_model,omitempty"`
	MeanJSD         *float64  `json:"mean_jsd,omitempty"`
	SelfJSD         *float64  `json:"self_jsd,omitempty"`
	PValue          *float64  `json:"p_value,omitempty"`
	ValidSamples    int       `json:"valid_samples"`
	RequiredSamples int       `json:"required_samples"`
	CellCount       int       `json:"cell_count"`
	Reason          string    `json:"reason,omitempty"`
	EvaluatedAt     time.Time `json:"evaluated_at"`
}

type OpenAIEvalModelTraceCandidate struct {
	Model       string  `json:"model"`
	DisplayName string  `json:"display_name"`
	Family      string  `json:"family"`
	FamilyName  string  `json:"family_name"`
	Probability float64 `json:"probability"`
	Similarity  float64 `json:"profile_similarity"`
	Score       float64 `json:"score"`
}

type OpenAIEvalModelTraceFamily struct {
	Family      string  `json:"family"`
	DisplayName string  `json:"display_name"`
	Probability float64 `json:"probability"`
}

type OpenAIEvalModelTraceDiagnostic struct {
	Index   int  `json:"index"`
	Parsed  int  `json:"parsed_numbers"`
	Minimum int  `json:"minimum_numbers"`
	Valid   bool `json:"accepted"`
}

type OpenAIEvalModelTraceSample struct {
	Prompt        string                   `json:"-"`
	ExpectedCount int                      `json:"expected_count"`
	Attempts      int                      `json:"attempts"`
	Answer        string                   `json:"answer,omitempty"`
	ErrorMessage  string                   `json:"error_message,omitempty"`
	AttemptErrors []OpenAIEvalAttemptError `json:"attempt_errors,omitempty"`
	HTTPStatus    int                      `json:"http_status,omitempty"`
	Text          string                   `json:"-"`
	Error         string                   `json:"error,omitempty"`
	Parsed        int                      `json:"parsed_numbers"`
	Valid         bool                     `json:"accepted"`
}

type OpenAIEvalModelTraceResult struct {
	BankRevision      string                           `json:"bank_revision"`
	Prediction        string                           `json:"prediction,omitempty"`
	Probability       float64                          `json:"probability,omitempty"`
	FamilyPrediction  string                           `json:"family_prediction_name,omitempty"`
	FamilyProbability float64                          `json:"family_probability,omitempty"`
	UsedOutputs       int                              `json:"used_outputs"`
	Requests          int                              `json:"requests"`
	Candidates        []OpenAIEvalModelTraceCandidate  `json:"candidates,omitempty"`
	Families          []OpenAIEvalModelTraceFamily     `json:"families,omitempty"`
	Diagnostics       []OpenAIEvalModelTraceDiagnostic `json:"diagnostics,omitempty"`
	Samples           []OpenAIEvalModelTraceSample     `json:"samples,omitempty"`
}

type OpenAIEvalSample struct {
	ProbeID string
	Answer  string
	Error   string
}

// OpenAIEvalTarget is resolved once per run so a multi-sample fingerprint
// consistently uses one account and one upstream model mapping.
type OpenAIEvalTarget struct {
	Account        *Account
	Credential     *Account
	RequestedModel string
	UpstreamModel  string
}

type OpenAIEvalSampleResponse struct {
	Text         string
	InputTokens  int64
	OutputTokens int64
	CompletedAt  time.Time
	HTTPStatus   int
	Model        string
}

// Exact candyPrompt from cpa-plugin-codex-candy-eval commit
// 976239690451849223c2174ffdd08681019753d1. Never add the expected answer.
var OpenAIEvalCandyPrompt = `在一个黑色的袋子里放有三种口味的糖果，每种糖果有两种不同的形状（圆形和五角星形，不同的形状靠手感可以分辨）。现已知不同口味的糖和不同形状的数量统计如下表。参赛者需要在活动前决定摸出的糖果数目，那么，最少取出多少个糖果才能保证手中同时拥有不同形状的苹果味和桃子味的糖？（同时手中有圆形苹果味匹配五角星桃子味糖果，或者有圆形桃子味匹配五角星苹果味糖果都满足要求）

        苹果味  桃子味  西瓜味
圆形       7      9      8
五角星形   7      6      4
`

// Kept as a legacy API name for callers that identify the pinned Candy probe.
// It must never diverge from OpenAIEvalCandyPrompt.
var OpenAIEvalCandyCPAReferencePrompt = OpenAIEvalCandyPrompt

// OpenAIEvalSupportedModels lists text models from the versioned local OpenAI
// catalog. Account-specific model discovery is intersected at run time.
func OpenAIEvalSupportedModels() []openai.Model {
	models := make([]openai.Model, 0, len(openai.DefaultModels))
	for _, model := range openai.DefaultModels {
		if strings.HasPrefix(strings.ToLower(model.ID), "gpt-") && !isOpenAIImageModel(model.ID) {
			models = append(models, model)
		}
	}
	return models
}

func isOpenAIEvalSupportedModel(modelID string) bool {
	modelID = strings.TrimSpace(modelID)
	for _, model := range OpenAIEvalSupportedModels() {
		if strings.EqualFold(model.ID, modelID) {
			return true
		}
	}
	return false
}

func OpenAIEvalFingerprintSampleCount(mode string) (int, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "quick":
		return OpenAIEvalFingerprintQuickSamples, nil
	case "standard":
		return OpenAIEvalFingerprintStandardSamples, nil
	case "strict":
		return OpenAIEvalFingerprintStrictSamples, nil
	default:
		return 0, fmt.Errorf("unsupported fingerprint mode %q", mode)
	}
}

func OpenAIEvalFingerprintPlan(mode string) (cells, repeats int, err error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "quick":
		return openAIEvalFingerprintQuickCells, openAIEvalFingerprintQuickRepeats, nil
	case "standard":
		return openAIEvalFingerprintStandardCells, openAIEvalFingerprintStandardRepeats, nil
	case "strict":
		return openAIEvalFingerprintStrictCells, openAIEvalFingerprintStrictRepeats, nil
	default:
		return 0, 0, fmt.Errorf("unsupported fingerprint mode %q", mode)
	}
}

func OpenAIEvalFingerprintCostEstimate(samples int, inputTokensPerRequest int, inputPricePerMillion float64) float64 {
	if samples <= 0 || inputTokensPerRequest <= 0 || inputPricePerMillion <= 0 {
		return 0
	}
	return float64(samples*inputTokensPerRequest) * inputPricePerMillion / 1_000_000
}

func ScoreOpenAIEvalCandy(answer string) OpenAIEvalOutcome {
	if value, ok := leadingOpenAIEvalCandyAnswer(answer); ok && value == OpenAIEvalCandyExpectedAnswer {
		return OpenAIEvalOutcome{Status: "pass", Reason: "correct_answer", Score: 1, SampleCount: 1, ExpectedCount: 1, Confidence: "low", Scheduling: "neutral"}
	}
	return OpenAIEvalOutcome{Status: "warning", Reason: "single_public_item_failed", Score: 0, SampleCount: 1, ExpectedCount: 1, Confidence: "low", Scheduling: "alert_only"}
}

var openAIEvalCandyFinalAnswer = regexp.MustCompile(`(?im)(?:^|[\n。.!！；;，,])\s*(?:\*\*|#+\s*)?(?:最终答案(?:是|为)?|答案(?:是|为)?|final answer(?: is)?|answer(?: is)?|(?:最终|所以|因此|综上(?:所述)?)[，,：:\s]*(?:答案(?:是|为)?|(?:最少|至少)?(?:需要|需|要)?(?:取出|取|抽取|拿出)?))\s*[:：]?\s*(?:\*\*)?([0-9０-９一二三四五六七八九十百两]+)`)

var openAIEvalCandyMinimumAnswer = regexp.MustCompile(`(?im)(?:^|[\n。.!！；;，,])\s*(?:\*\*|#+\s*)?(?:最少|至少)(?:需要|需|要)?(?:取出|取|抽取|拿出)?\s*[:：]?\s*(?:\*\*)?([0-9０-９一二三四五六七八九十百两]+)`)

var openAIEvalCandyShapeQuantity = regexp.MustCompile(`^\s*(?:\*\*)?\s*(?:颗|个|枚|粒)?\s*(?:圆形|五角星形|五角星|星形)`)

var openAIEvalCandyNegativeTail = regexp.MustCompile(`(?i)^(?:(?:仍然|依然|还是|仍|也|并|是|根本|还)\s*)*(?:不够|不对|不是|不成立|不足|不正确|无法(?:保证|确保)|不能(?:保证|确保)|可能失败|时(?:仍)?可能失败|is not|isn't)`)

var openAIEvalCandyNumericToken = regexp.MustCompile(`(?:[0-9０-９]+(?:[.．][0-9０-９]+)?|[.．][0-9０-９]+)(?:[eE][+-]?[0-9０-９]+)?`)

func leadingOpenAIEvalCandyAnswer(answer string) (int, bool) {
	// Candy uses the configured presence rule, not a proof or conclusion check.
	// Tokenize numbers so 121 and 21.5 cannot pass as the integer 21.
	for _, token := range openAIEvalCandyNumericToken.FindAllString(answer, -1) {
		if value, ok := parseEvalNumber(token); ok && value == OpenAIEvalCandyExpectedAnswer {
			return value, true
		}
	}
	// Keep extracting an explicit wrong answer when the expected number is absent.
	var explicit int
	found := false
	for _, pattern := range []*regexp.Regexp{openAIEvalCandyFinalAnswer, openAIEvalCandyMinimumAnswer} {
		for _, match := range pattern.FindAllStringSubmatchIndex(answer, -1) {
			if openAIEvalCandyShapeQuantity.MatchString(answer[match[3]:]) {
				continue
			}
			if openAIEvalCandyValueRejected(answer[match[2]:]) {
				// A counterexample explicitly called insufficient is reasoning,
				// not a competing final answer. Continue to the actual conclusion.
				continue
			}
			value, ok := leadingOpenAIEvalCandyValue(answer[match[2]:])
			if !ok || (found && value != explicit) {
				return 0, false
			}
			explicit, found = value, true
		}
	}
	if found {
		return explicit, true
	}
	return leadingOpenAIEvalCandyValue(answer)
}

func leadingOpenAIEvalCandyValue(answer string) (int, bool) {
	answer = strings.TrimLeft(strings.TrimSpace(answer), "*# ")
	for _, prefix := range []string{"最终答案是", "最终答案为", "最终答案", "答案是", "答案为", "答案", "最少需要取出", "最少需要", "最少取出", "至少需要", "至少", "最少", "需要", "Final answer is", "Final answer", "Answer is", "Answer"} {
		if strings.HasPrefix(answer, prefix) {
			answer = strings.TrimLeft(strings.TrimPrefix(answer, prefix), "：: *")
			break
		}
	}
	runes := []rune(answer)
	if len(runes) == 0 || (!isEvalDigit(runes[0]) && !isChineseEvalNumeral(runes[0])) {
		return 0, false
	}
	i := 0
	for i < len(runes) && (isEvalDigit(runes[i]) || isChineseEvalNumeral(runes[i])) {
		i++
	}
	if i < len(runes) && (runes[i] == '/' || runes[i] == '%' || (runes[i] >= 'a' && runes[i] <= 'z') || (runes[i] >= 'A' && runes[i] <= 'Z') ||
		(strings.ContainsRune(".．", runes[i]) && i+1 < len(runes) && isEvalDigit(runes[i+1]))) {
		return 0, false
	}
	if openAIEvalCandyShapeQuantity.MatchString(string(runes[i:])) || openAIEvalCandyValueRejected(answer) {
		return 0, false
	}
	return parseEvalNumber(string(runes[:i]))
}

func openAIEvalCandyValueRejected(answer string) bool {
	runes := []rune(strings.TrimLeft(strings.TrimSpace(answer), "*# "))
	i := 0
	for i < len(runes) && (isEvalDigit(runes[i]) || isChineseEvalNumeral(runes[i])) {
		i++
	}
	if i == 0 {
		return false
	}
	tail := strings.TrimLeft(string(runes[i:]), " *颗个糖果，,：:。.")
	return openAIEvalCandyNegativeTail.MatchString(tail)
}

func firstEvalNumber(value string) (int, bool) {
	runes := []rune(value)
	for i := 0; i < len(runes); i++ {
		if isEvalDigit(runes[i]) {
			start := i
			for i < len(runes) && isEvalDigit(runes[i]) {
				i++
			}
			decimal := start > 0 && runes[start-1] == '.' || i < len(runes) && runes[i] == '.'
			if parsed, ok := parseEvalNumber(string(runes[start:i])); ok && !decimal {
				return parsed, true
			}
			i--
		} else if isChineseEvalNumeral(runes[i]) {
			start := i
			for i < len(runes) && isChineseEvalNumeral(runes[i]) {
				i++
			}
			if parsed, ok := parseEvalNumber(string(runes[start:i])); ok {
				return parsed, true
			}
			i--
		}
	}
	return 0, false
}

func isEvalDigit(r rune) bool {
	return r >= '0' && r <= '9' || r >= '０' && r <= '９'
}

func isChineseEvalNumeral(r rune) bool {
	return strings.ContainsRune("零〇一二两三四五六七八九十百千", r)
}

func NormalizeOpenAIEvalFingerprintAnswer(raw string, probe OpenAIEvalProbe) (string, bool) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" || containsEvalRefusal(raw) {
		return "", false
	}
	word := firstEvalWord(raw)
	if word == "" {
		return "", false
	}
	switch probe.Kind {
	case "int":
		n, ok := parseEvalNumber(word)
		if !ok || n < probe.Low || n > probe.High {
			return "", false
		}
		return fmt.Sprint(n), true
	case "coin":
		switch {
		case word == "heads" || word == "head" || word == "字" || strings.HasPrefix(word, "正"):
			return "heads", true
		case word == "tails" || word == "tail" || word == "花" || strings.HasPrefix(word, "反"):
			return "tails", true
		default:
			return "", false
		}
	case "letter":
		if len(word) == 1 && word[0] >= 'a' && word[0] <= 'z' {
			return word, true
		}
		return "", false
	default:
		if len([]rune(word)) > 40 || strings.ContainsAny(word, "0123456789") {
			return "", false
		}
		return word, true
	}
}

func OpenAIEvalFingerprintJSD(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return math.NaN()
	}
	counts := map[string][2]float64{}
	for _, value := range a {
		count := counts[value]
		count[0]++
		counts[value] = count
	}
	for _, value := range b {
		count := counts[value]
		count[1]++
		counts[value] = count
	}
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	distance := 0.0
	for _, key := range keys {
		count := counts[key]
		p, q := count[0]/float64(len(a)), count[1]/float64(len(b))
		m := (p + q) / 2
		if p > 0 {
			distance += p * math.Log2(p/m) / 2
		}
		if q > 0 {
			distance += q * math.Log2(q/m) / 2
		}
	}
	if distance < 0 {
		return 0
	}
	if distance > 1 {
		return 1
	}
	return distance
}

func firstEvalWord(value string) string {
	runes := []rune(strings.TrimSpace(value))
	start := 0
	for start < len(runes) && !unicode.IsLetter(runes[start]) && !unicode.IsDigit(runes[start]) {
		start++
	}
	if start == len(runes) {
		return ""
	}
	isNumeric := isEvalDigit(runes[start]) || isChineseEvalNumeral(runes[start])
	end := start
	for end < len(runes) {
		if isNumeric {
			if !isEvalDigit(runes[end]) && !isChineseEvalNumeral(runes[end]) {
				break
			}
		} else if !unicode.IsLetter(runes[end]) && !unicode.IsDigit(runes[end]) {
			if runes[end] != '-' || end+1 >= len(runes) || !unicode.IsLetter(runes[end+1]) {
				break
			}
		}
		end++
	}
	return string(runes[start:end])
}

func parseEvalNumber(value string) (int, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}

	var ascii strings.Builder
	allDigits := true
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9':
			ascii.WriteRune(r)
		case r >= '０' && r <= '９':
			ascii.WriteRune('0' + (r - '０'))
		default:
			allDigits = false
		}
	}
	if allDigits {
		n, err := strconv.Atoi(ascii.String())
		return n, err == nil
	}

	if n, ok := parseEnglishEvalNumber(value); ok {
		return n, true
	}

	return parseChineseEvalNumber(value)
}

// parseEnglishEvalNumber accepts the compact number words commonly returned
// by English fingerprint probes (for example "seventy" or "forty-two").
// It deliberately handles only cardinal numbers through 999 so explanatory
// prose cannot be mistaken for a probe answer.
func parseEnglishEvalNumber(value string) (int, bool) {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", " ")
	parts := strings.Fields(value)
	if len(parts) == 0 || len(parts) > 3 {
		return 0, false
	}
	ones := map[string]int{"zero": 0, "one": 1, "two": 2, "three": 3, "four": 4, "five": 5, "six": 6, "seven": 7, "eight": 8, "nine": 9, "ten": 10, "eleven": 11, "twelve": 12, "thirteen": 13, "fourteen": 14, "fifteen": 15, "sixteen": 16, "seventeen": 17, "eighteen": 18, "nineteen": 19}
	tens := map[string]int{"twenty": 20, "thirty": 30, "forty": 40, "fifty": 50, "sixty": 60, "seventy": 70, "eighty": 80, "ninety": 90}
	if len(parts) == 1 {
		if n, ok := ones[parts[0]]; ok {
			return n, true
		}
		if n, ok := tens[parts[0]]; ok {
			return n, true
		}
		return 0, false
	}
	if len(parts) == 2 {
		if tensValue, ok := tens[parts[0]]; ok {
			if onesValue, ok := ones[parts[1]]; ok && onesValue > 0 && onesValue < 10 {
				return tensValue + onesValue, true
			}
		}
		return 0, false
	}
	if parts[1] != "hundred" {
		return 0, false
	}
	hundreds, ok := ones[parts[0]]
	if !ok || hundreds == 0 {
		return 0, false
	}
	if parts[2] == "" {
		return hundreds * 100, true
	}
	if n, ok := ones[parts[2]]; ok {
		return hundreds*100 + n, true
	}
	if n, ok := tens[parts[2]]; ok {
		return hundreds*100 + n, true
	}
	return 0, false
}

func parseChineseEvalNumber(value string) (int, bool) {
	digits := map[rune]int{'零': 0, '〇': 0, '一': 1, '二': 2, '两': 2, '三': 3, '四': 4, '五': 5, '六': 6, '七': 7, '八': 8, '九': 9}
	units := map[rune]int{'十': 10, '百': 100, '千': 1000}
	total, section, digit := 0, 0, 0
	usedUnit := false
	for _, r := range value {
		if n, ok := digits[r]; ok {
			digit = n
			continue
		}
		unit, ok := units[r]
		if !ok {
			return 0, false
		}
		usedUnit = true
		if digit == 0 && unit == 10 {
			digit = 1
		} else if digit == 0 {
			return 0, false
		}
		section += digit * unit
		digit = 0
	}
	if !usedUnit {
		return 0, false
	}
	return total + section + digit, true
}

func containsEvalRefusal(value string) bool {
	for _, phrase := range []string{
		"i cannot", "i can't", "i am unable", "i'm unable", "unable to", "sorry,", "as an ai",
		"抱歉", "无法", "不能回答", "不能提供", "作为一个人工智能",
	} {
		if strings.Contains(value, phrase) {
			return true
		}
	}
	return false
}

func ScoreOpenAIEvalFingerprint(model string, samples []OpenAIEvalSample, baselines []OpenAIEvalFingerprintBaseline, required int) OpenAIEvalFingerprintResult {
	result := OpenAIEvalFingerprintResult{Status: "insufficient", ValidSamples: 0, RequiredSamples: required, EvaluatedAt: time.Now().UTC()}
	valid := map[string][]string{}
	for _, sample := range samples {
		if sample.Error != "" {
			continue
		}
		probe, ok := findOpenAIEvalProbe(sample.ProbeID)
		if !ok {
			continue
		}
		answer, ok := NormalizeOpenAIEvalFingerprintAnswer(sample.Answer, probe)
		if !ok {
			continue
		}
		valid[probe.ID] = append(valid[probe.ID], answer)
		result.ValidSamples++
	}
	if result.ValidSamples < required || len(baselines) == 0 {
		result.Reason = "insufficient_valid_samples"
		if len(baselines) == 0 {
			result.Reason = "no_versioned_baseline"
		}
		return result
	}
	for _, baseline := range baselines {
		cellScores := make([]float64, 0, len(valid))
		for _, probe := range OpenAIEvalFingerprintProbes {
			cell := probe.ID
			candidate := valid[cell]
			reference := baseline.Cells[cell]
			if len(candidate) < 10 || len(reference) < 10 {
				continue
			}
			cellScores = append(cellScores, OpenAIEvalFingerprintJSD(candidate, reference))
		}
		if len(cellScores) < 4 {
			continue
		}
		mean := 0.0
		for _, score := range cellScores {
			mean += score
		}
		mean /= float64(len(cellScores))
		if result.MeanJSD == nil || mean < *result.MeanJSD {
			result.MeanJSD = &mean
			result.NearestModel = baseline.Model
			result.CellCount = len(cellScores)
			p := openAIEvalFingerprintPermutationPValue(valid, baseline.Cells, model+"|"+baseline.Model, openAIEvalFingerprintPermutationN)
			result.PValue = p
		}
	}
	if result.MeanJSD == nil {
		result.Status, result.Reason = "insufficient", "insufficient_cells"
		return result
	}
	result.Status, result.Reason = openAIEvalAttributionVerdict(result.NearestModel)
	return result
}

// Attribution remains a low-confidence diagnostic, not proof of the actual
// provider route. Only the Luna family is considered a suspected downgrade.
func openAIEvalAttributionVerdict(model string) (status, reason string) {
	if strings.TrimSpace(model) == "" {
		return "insufficient", "unresolved_behavioral_attribution"
	}
	parts := strings.FieldsFunc(strings.ToLower(strings.TrimSpace(model)), func(r rune) bool {
		return r == '-' || r == '_' || r == '/' || r == ':' || r == '.'
	})
	for _, part := range parts {
		if part == "luna" {
			return "warning", "suspected_luna_attribution"
		}
	}
	return "suspected_normal", "non_luna_behavioral_attribution"
}

func openAIEvalFingerprintPermutationPValue(a, b map[string][]string, seed string, permutations int) *float64 {
	entries := make([]string, 0)
	for _, probe := range OpenAIEvalFingerprintProbes {
		if len(a[probe.ID]) >= 10 && len(b[probe.ID]) >= 10 {
			entries = append(entries, probe.ID)
		}
	}
	if len(entries) < 4 || permutations <= 0 {
		return nil
	}
	observed := 0.0
	pools := make([][]string, len(entries))
	sizes := make([]int, len(entries))
	for i, cell := range entries {
		observed += OpenAIEvalFingerprintJSD(a[cell], b[cell])
		pools[i] = append(append([]string(nil), a[cell]...), b[cell]...)
		sizes[i] = len(a[cell])
	}
	observed /= float64(len(entries))
	rng := rand.New(rand.NewSource(int64(crc32.ChecksumIEEE([]byte(seed)))))
	hits := 0
	for range permutations {
		total := 0.0
		for i, pool := range pools {
			shuffled := append([]string(nil), pool...)
			rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
			total += OpenAIEvalFingerprintJSD(shuffled[:sizes[i]], shuffled[sizes[i]:])
		}
		if total/float64(len(entries)) >= observed-1e-12 {
			hits++
		}
	}
	p := float64(hits+1) / float64(permutations+1)
	return &p
}

func findOpenAIEvalProbe(id string) (OpenAIEvalProbe, bool) {
	for _, probe := range OpenAIEvalFingerprintProbes {
		if probe.ID == id {
			return probe, true
		}
	}
	return OpenAIEvalProbe{}, false
}

func OpenAIEvalBaselineVersionFor(models []string) string {
	copyModels := append([]string(nil), models...)
	sort.Strings(copyModels)
	hash := sha256.Sum256([]byte(strings.Join(copyModels, "\n")))
	return OpenAIEvalBaselineVersion + "-" + hex.EncodeToString(hash[:4])
}

func OpenAIEvalSchedulingDisposition(testType string, outcome OpenAIEvalOutcome, globalEnabled bool) string {
	if !globalEnabled {
		return "disabled"
	}
	if strings.EqualFold(testType, OpenAIEvalTypeFingerprint) || strings.EqualFold(testType, OpenAIEvalTypeCandy) || strings.EqualFold(testType, OpenAIEvalTypeModelTrace) || strings.EqualFold(testType, OpenAIEvalTypeStateProbe) {
		return "alert_only"
	}
	if outcome.Status == "fail" && outcome.Confidence == "high" {
		return "downrank_route"
	}
	return "alert_only"
}

// OpenAIEvalRoutePenalty returns the only evaluation-derived scheduler signal
// currently allowed: an explicit, high-confidence operational failure. Candy
// and Fingerprint outcomes are intentionally excluded because they are low
// confidence and/or identity evidence, not a capability verdict.
func OpenAIEvalRoutePenalty(testType string, outcome OpenAIEvalOutcome, globalEnabled bool) float64 {
	if !globalEnabled || strings.EqualFold(testType, OpenAIEvalTypeCandy) || strings.EqualFold(testType, OpenAIEvalTypeFingerprint) || strings.EqualFold(testType, OpenAIEvalTypeModelTrace) || strings.EqualFold(testType, OpenAIEvalTypeStateProbe) {
		return 0
	}
	if outcome.Status == "fail" && strings.EqualFold(outcome.Confidence, "high") && outcome.SampleCount >= outcome.ExpectedCount && outcome.ExpectedCount > 0 {
		return 1
	}
	return 0
}
