package service

import (
	"context"
	"errors"
	"math/big"
	"time"
)

const OpenAIEvalRankingAlgorithmVersion = "evidence-account-macro-v5-runtime-thresholds"

var (
	ErrOpenAIEvalRankingSuperseded      = errors.New("EVALUATION_SUPERSEDED")
	ErrOpenAIEvalRankingSnapshotChanged = errors.New("RANKING_SNAPSHOT_CHANGED")
	ErrOpenAIEvalRankingUnavailable     = errors.New("EVALUATION_SERVICE_UNAVAILABLE")
)

type OpenAIEvalRankingWeights struct {
	Price              float64  `json:"price"`
	ErrorRate          float64  `json:"error_rate"`
	TTFT               float64  `json:"ttft"`
	Load               float64  `json:"load"`
	Quality            float64  `json:"quality"`
	AbsolutePriorities []string `json:"absolute_priorities,omitempty"`
}

type OpenAIEvalFactorMeta struct {
	Score          float64    `json:"score"`
	Known          bool       `json:"known"`
	ObservedAt     *time.Time `json:"observed_at"`
	UnknownReason  *string    `json:"unknown_reason"`
	DefaultApplied bool       `json:"default_applied"`
}

type OpenAIEvalRankingPrice struct {
	OpenAIEvalFactorMeta
	RateMultiplier *float64 `json:"rate_multiplier"`
	Source         *string  `json:"source"`
}
type OpenAIEvalRankingErrorRate struct {
	OpenAIEvalFactorMeta
	Value         *float64 `json:"value"`
	SampleCount   int64    `json:"sample_count"`
	Source        *string  `json:"source"`
	WindowSeconds int      `json:"window_seconds"`
	MonitorIDs    []int64  `json:"monitor_ids"`
}
type OpenAIEvalRankingTTFT struct {
	OpenAIEvalFactorMeta
	MS          *float64 `json:"ms"`
	SampleCount int64    `json:"sample_count"`
}
type OpenAIEvalRankingLoad struct {
	OpenAIEvalFactorMeta
	LoadRate           *int `json:"load_rate"`
	Waiting            *int `json:"waiting"`
	CurrentConcurrency *int `json:"current_concurrency"`
}
type OpenAIEvalRankingQuality struct {
	OpenAIEvalFactorMeta
	State         string     `json:"state"`
	Pass          int        `json:"pass"`
	SuspectedPass int        `json:"suspected_pass"`
	Selected      int        `json:"selected"`
	Evaluated     int        `json:"evaluated"`
	Ratio         *float64   `json:"ratio"`
	ExpiresAt     *time.Time `json:"expires_at"`
	// EvidenceErrorCode/Message explain why the latest selected test could not
	// contribute a quality score. They are sanitized diagnostic data only; an
	// unknown result must never be treated as a pass.
	EvidenceErrorCode    string `json:"evidence_error_code,omitempty"`
	EvidenceErrorMessage string `json:"evidence_error_message,omitempty"`
	macroRatio           *big.Rat
}
type OpenAIEvalRankingMonitor struct {
	MonitorID     int64     `json:"monitor_id"`
	Model         string    `json:"model"`
	Status        string    `json:"status"`
	ObservedAt    time.Time `json:"observed_at"`
	LatencyMS     *int      `json:"latency_ms"`
	PingLatencyMS *int      `json:"ping_latency_ms"`
}
type OpenAIEvalRankingFactors struct {
	RuntimeRecovery       *OpenAIEvalRuntimeRecovery `json:"runtime_recovery,omitempty"`
	recoveryLastAttemptAt time.Time
	recoveryInFlight      bool
	recoveryMetricVersion uint64
	Price                 OpenAIEvalRankingPrice     `json:"price"`
	ErrorRate             OpenAIEvalRankingErrorRate `json:"error_rate"`
	TTFT                  OpenAIEvalRankingTTFT      `json:"ttft"`
	Load                  OpenAIEvalRankingLoad      `json:"load"`
	Quality               OpenAIEvalRankingQuality   `json:"quality"`
	Monitoring            []OpenAIEvalRankingMonitor `json:"monitoring,omitempty"`
	monitorExpiresAt      *time.Time
}
type OpenAIEvalRankingExclusion struct {
	Code       string    `json:"code"`
	Scope      string    `json:"scope"`
	ObservedAt time.Time `json:"observed_at"`
}
type OpenAIEvalRankedAccount struct {
	ThresholdReasons    []string                       `json:"threshold_reasons,omitempty"`
	QualityBasis        string                         `json:"quality_basis,omitempty"`
	AccountQualityPrior *OpenAIEvalAccountQualityPrior `json:"account_quality_prior,omitempty"`
	OverviewPrior       *OpenAIEvalOverviewPrior       `json:"overview_prior,omitempty"`
	AccountID           int64                          `json:"account_id"`
	AccountName         string                         `json:"account_name"`
	Rank                *int                           `json:"rank"`
	PriorityScore       *float64                       `json:"priority_score"`
	QualityTier         *int                           `json:"quality_tier"`
	Eligible            bool                           `json:"eligible"`
	ExclusionReason     *string                        `json:"exclusion_reason"`
	ExclusionReasons    []OpenAIEvalRankingExclusion   `json:"exclusion_reasons"`
	UpstreamModels      []string                       `json:"upstream_models"`
	Factors             OpenAIEvalRankingFactors       `json:"factors"`
	Contributions       OpenAIEvalRankingWeights       `json:"contributions"`
}
type OpenAIEvalSelectionModelVariant struct {
	Endpoint       string `json:"endpoint"`
	Platform       string `json:"platform"`
	SelectionModel string `json:"selection_model"`
}
type OpenAIEvalRankingDimension struct {
	DimensionID            string                            `json:"dimension_id"`
	GroupID                *int64                            `json:"group_id"`
	GroupName              string                            `json:"group_name"`
	RequestedModel         string                            `json:"requested_model"`
	ReasoningEffort        string                            `json:"reasoning_effort"`
	SelectionModel         *string                           `json:"selection_model"`
	SelectionModelVariants []OpenAIEvalSelectionModelVariant `json:"selection_model_variants"`
	Policy                 string                            `json:"policy"`
	Weights                OpenAIEvalRankingWeights          `json:"weights"`
	Ordering               string                            `json:"ordering"`
	Sources                []string                          `json:"sources"`
	CandidateCount         *int                              `json:"candidate_count"`
	EligibleCount          *int                              `json:"eligible_count"`
	PreferredAccountID     *int64                            `json:"preferred_account_id"`
	CoverageStatus         string                            `json:"coverage_status"`
	FallbackReason         *string                           `json:"fallback_reason"`
	ValidUntil             *time.Time                        `json:"valid_until"`
	Accounts               []OpenAIEvalRankedAccount         `json:"accounts,omitempty"`
	AccountsTruncated      bool                              `json:"accounts_truncated"`
	AccountsNextCursor     *string                           `json:"accounts_next_cursor"`
}
type OpenAIEvalRankingCoverage struct {
	Status                   string   `json:"status"`
	DiscoveryComplete        bool     `json:"discovery_complete"`
	DiscoveredDimensionCount *int     `json:"discovered_dimension_count"`
	CachedDimensionCount     int      `json:"cached_dimension_count"`
	UncachedDimensionCount   *int     `json:"uncached_dimension_count"`
	WildcardRoutesPresent    bool     `json:"wildcard_routes_present"`
	Reasons                  []string `json:"reasons"`
}
type OpenAIEvalRankingSummary struct {
	EvaluationID         string                    `json:"evaluation_id"`
	RecordType           string                    `json:"record_type"`
	Scope                string                    `json:"scope"`
	AlgorithmVersion     string                    `json:"algorithm_version"`
	DataVersion          string                    `json:"data_version"`
	EvaluatedAt          time.Time                 `json:"evaluated_at"`
	PublishedAt          time.Time                 `json:"published_at"`
	NextEvaluationAt     time.Time                 `json:"next_evaluation_at"`
	NextEvaluationReason string                    `json:"next_evaluation_reason"`
	Trigger              string                    `json:"trigger"`
	ConfigRevision       int64                     `json:"config_revision"`
	EffectsEnabled       bool                      `json:"effects_enabled"`
	DimensionCount       int                       `json:"dimension_count"`
	AccountCount         int                       `json:"account_count"`
	AccountRowCount      int                       `json:"account_row_count"`
	QualityRouteCount    int                       `json:"quality_route_count"`
	Truncated            bool                      `json:"truncated"`
	Coverage             OpenAIEvalRankingCoverage `json:"coverage"`
}
type OpenAIEvalRankingGroup struct {
	GroupID     *int64  `json:"group_id"`
	GroupName   string  `json:"group_name"`
	MemberCount int     `json:"member_count"`
	Status      string  `json:"status"`
	Reason      *string `json:"reason"`
}
type OpenAIEvalRankingError struct {
	Code           string  `json:"code"`
	Message        string  `json:"message"`
	ConfigRevision int64   `json:"config_revision"`
	EvaluationID   *string `json:"evaluation_id"`
}
type OpenAIEvalRankingSnapshot struct {
	Summary               *OpenAIEvalRankingSummary    `json:"summary"`
	PreviousSummary       *OpenAIEvalRankingSummary    `json:"previous_summary"`
	EffectiveStatus       string                       `json:"effective_status"`
	CurrentConfigRevision int64                        `json:"current_config_revision"`
	EvaluationInProgress  bool                         `json:"evaluation_in_progress"`
	RankingError          *OpenAIEvalRankingError      `json:"ranking_error"`
	Groups                []OpenAIEvalRankingGroup     `json:"groups"`
	Dimensions            []OpenAIEvalRankingDimension `json:"dimensions"`
	NextCursor            *string                      `json:"next_cursor"`
}

type OpenAIEvalAccountModel struct {
	RequestedModel  string                   `json:"requested_model"`
	ReasoningEffort string                   `json:"reasoning_effort"`
	UpstreamModels  []string                 `json:"upstream_models"`
	Factors         OpenAIEvalRankingFactors `json:"factors"`
	Sources         []string                 `json:"sources"`
	Policy          string                   `json:"policy"`
	Weights         OpenAIEvalRankingWeights `json:"weights"`
}

// Quality tier bands preserve the lexicographic order without rounding ratios.
type OpenAIEvalAccountPriority struct {
	QualityKnown     bool     `json:"quality_known"`
	QualityRatio     *float64 `json:"quality_ratio"`
	OperationalScore float64  `json:"operational_score"`
	QualityTier      *int     `json:"quality_tier"`
}

type OpenAIEvalOverviewPrior struct {
	Rank          int                       `json:"rank"`
	PriorityScore float64                   `json:"priority_score"`
	Priority      OpenAIEvalAccountPriority `json:"priority"`
}

// An account-wide preference is not evidence about the requested model/effort.
type OpenAIEvalAccountQualityPrior struct {
	Ratio        float64   `json:"ratio"`
	Basis        string    `json:"basis,omitempty"`
	EvaluationID string    `json:"evaluation_id"`
	EvaluatedAt  time.Time `json:"evaluated_at"`
	ExpiresAt    time.Time `json:"expires_at"`
	SourceModels []string  `json:"source_models"`
	fraction     *big.Rat
}

type OpenAIEvalAccountOverviewRow struct {
	OpenAIEvalRankedAccount
	GroupIDs                 []int64                   `json:"group_ids"`
	ModelCount               int                       `json:"model_count"`
	QualityModelCount        int                       `json:"quality_model_count"`
	UnknownQualityModelCount int                       `json:"unknown_quality_model_count"`
	QualityCellCount         int                       `json:"quality_cell_count"`
	UnknownQualityCellCount  int                       `json:"unknown_quality_cell_count"`
	WorstQualityModel        *string                   `json:"worst_quality_model"`
	WorstQualityRatio        *float64                  `json:"worst_quality_ratio"`
	Models                   []OpenAIEvalAccountModel  `json:"models"`
	Priority                 OpenAIEvalAccountPriority `json:"priority"`
}

type OpenAIEvalAccountOverview struct {
	Summary               *OpenAIEvalRankingSummary      `json:"summary"`
	PreviousSummary       *OpenAIEvalRankingSummary      `json:"previous_summary"`
	EffectiveStatus       string                         `json:"effective_status"`
	CurrentConfigRevision int64                          `json:"current_config_revision"`
	EvaluationInProgress  bool                           `json:"evaluation_in_progress"`
	RankingError          *OpenAIEvalRankingError        `json:"ranking_error"`
	Groups                []OpenAIEvalRankingGroup       `json:"groups"`
	NextCursor            *string                        `json:"next_cursor"`
	Policy                string                         `json:"policy"`
	Weights               OpenAIEvalRankingWeights       `json:"weights"`
	Ordering              string                         `json:"ordering"`
	Accounts              []OpenAIEvalAccountOverviewRow `json:"accounts"`
}
type OpenAIEvalRankingFilter struct {
	GroupID         *int64
	RequestedModel  *string
	ReasoningEffort *string
	EvaluationID    string
	Cursor          string
	Limit           int
}

type OpenAIEvalEvidenceKey struct {
	AccountID       int64  `json:"account_id"`
	RequestedModel  string `json:"requested_model"`
	ReasoningEffort string `json:"reasoning_effort"`
	TestType        string `json:"test_type"`
}

// Latest terminal rows must be selected before validating status or version.
type OpenAIEvalLatestEvidenceRepository interface {
	LatestCompletedRuns(context.Context, []OpenAIEvalEvidenceKey) ([]OpenAIEvalRun, error)
}

type OpenAIEvalRankingTrace struct {
	RawRequestedReasoningEffort string     `json:"raw_requested_reasoning_effort,omitempty"`
	RecordType                  string     `json:"record_type"`
	Scope                       string     `json:"scope"`
	GroupID                     *int64     `json:"group_id"`
	EvaluationID                *string    `json:"evaluation_id"`
	ConfigRevision              int64      `json:"config_revision"`
	RankingBasis                string     `json:"ranking_basis"`
	RankingFallbackReason       *string    `json:"ranking_fallback_reason"`
	SelectedRank                *int       `json:"selected_rank"`
	SelectionModel              string     `json:"selection_model"`
	SnapshotEvaluatedAt         *time.Time `json:"snapshot_evaluated_at"`
}
