/**
 * Admin Accounts API endpoints
 * Handles AI platform account management for administrators
 */

import { apiClient } from '../client'
import type { OpenAIReferralRefreshResult, OpenAIReferralSendResult } from '@/types/openaiReferrals'
import type {
  Account,
  AccountListItem,
  CreateAccountRequest,
  UpdateAccountRequest,
  PaginatedResponse,
  AccountUsageInfo,
  WindowStats,
  ClaudeModel,
  AccountUsageStatsResponse,
  TempUnschedulableStatus,
  AdminDataPayload,
  AdminDataImportResult,
  CodexSessionImportRequest,
  CodexSessionImportResult,
  OpenAICodexPATCreateRequest,
  CheckMixedChannelRequest,
  CheckMixedChannelResponse,
  UpstreamBillingProbeResult,
  UpstreamBillingProbeSettings,
  UpstreamBillingRatesResponse,
  OllamaCloudUsageSettings,
  OllamaCloudUsageState,
  GrokMediaEligibilityMode,
  GrokMediaEligibilityState,
  OpenCodeGoUsageSettings,
  OpenCodeGoUsageState
} from '@/types'

export interface SchedulerDecisionTrace {
  at: string
  layer: string
  reason_code: string
  reason_text: string
  requested_model?: string
  requested_reasoning_effort?: string
  scheduling_policy?: OpenAIEvalSchedulingPolicy
  /**
   * 'actual_dispatch' marks a real request-selection attempt. Offline
   * evaluation never writes one of these, so this ledger only ever shows
   * dispatch traffic that actually happened.
   */
  record_type?: 'actual_dispatch' | string
  scope?: string
  group_id?: number | null
  group_name?: string
  /** The published evaluation generation this dispatch resolved against, if any. */
  evaluation_id?: string | null
  config_revision?: number
  /**
   * How the order was obtained: the published snapshot, a same-policy live
   * fallback for an uncovered route, the historical scheduler, or an owner
   * override that bypasses the order entirely.
   */
  ranking_basis?: 'snapshot' | 'live_fallback' | 'legacy' | 'owner' | string
  ranking_fallback_reason?: string | null
  selected_rank?: number | null
  selection_model?: string
  snapshot_evaluated_at?: string | null
  sticky_previous_hit: boolean
  sticky_session_hit: boolean
  candidate_count: number
  top_k: number
  latency_ms: number
  load_skew: number
  selected_account_id: number
  selected_account_name?: string
  selected_account_type: string
  /** True once the selected account's concurrency slot was actually acquired. */
  acquired?: boolean
  /** The request was only planned to wait for the selected account's slot. */
  wait_plan?: boolean
  /** Still queued for admission when the trace was written; not a confirmed slot. */
  awaiting_admission?: boolean
  selected_rate_multiplier?: number
  route_migration_active?: boolean
  migration_from_rate_multiplier?: number
  excluded_account_count: number
  excluded_account_ids?: number[]
  previous_response_given: boolean
  session_given: boolean
  error?: string
  candidates?: SchedulerDecisionCandidate[]
  candidates_truncated?: boolean
}

export interface SchedulerDecisionCandidate {
  account_id: number
  rate_multiplier?: number
  eligible: boolean
  selected: boolean
  in_top_k: boolean
  score?: number
  priority?: number
  load_rate?: number
  waiting_count?: number
  error_rate?: number
  ttft_ms?: number
  evaluation_penalty?: number
  /** Published-order position; null when the account cannot serve the model. */
  rank?: number | null
  /** 0-100 weighted total computed by the server; never derived in the browser. */
  priority_score?: number | null
  /** Per-factor normalized scores and contributions behind priority_score. */
  factors?: OpenAIEvalRankingFactors
  contributions?: OpenAIEvalRankingFactorWeights
  /**
   * Selected automatic test types (Candy / Fingerprint / ModelTrace) with a
   * fresh final verdict. Each type counts once, whatever its sample count.
   */
  evaluated_count?: number
  /** Test types whose final verdict passed. */
  pass_count?: number
  /** Test types whose final verdict was a suspected (likely) pass; counted as passed. */
  suspected_pass_count?: number
  /** (pass + suspected pass) / evaluated test types; absent while any selected type lacks fresh evidence. */
  quality_ratio?: number
  /** 'unassessed' means the pass rate is unknown, not 100 %. */
  quality_state?: 'assessed' | 'unassessed' | string
  quality_contribution?: number
  exclusion_reason?: string
  decision_reason?: string
  /** Set only when a fully cold pool was ordered by the account overview. */
  overview_prior?: { rank: number; priority_score: number; priority: OpenAIEvalAccountPriority } | null
  /**
   * Where the quality tier came from: 'exact' is this model and effort,
   * 'account_prior' is a separate account-wide reference because the requested
   * model and effort have no configured test evidence, 'none' is neither.
   */
  quality_basis?: OpenAIEvalQualityBasis | string
  /**
   * The account-wide reference behind an 'account_prior' tier. It is not a
   * measured pass rate for the requested model and effort, so it is never
   * merged into quality_ratio.
   */
  account_quality_prior?: OpenAIEvalAccountQualityPrior | null
}

/**
 * An account-wide quality reference for a dispatch or an evaluated row. The
 * ratio comes from the account's other models or efforts, so it must always be
 * shown with its sources and its expiry, never as the requested model and
 * effort's pass rate.
 */
export interface OpenAIEvalAccountQualityPrior {
  ratio: number
  evaluation_id: string
  evaluated_at: string
  /**
   * When the reference stops being usable. The server caps this at the earlier
   * of the evidence's own expiry and the evaluation generation's deadline, so
   * the value is shown exactly as supplied and never recomputed here.
   */
  expires_at: string
  /** Model/effort pairs the reference was computed from, formatted "model/effort". */
  source_models: string[]
}

/** 'exact' is a measured pass rate; the rest explain why there is none. */
export type OpenAIEvalQualityBasis = 'exact' | 'account_prior' | 'none' | 'unknown_current_model'

export interface OpenAIEvalSchedule {
  enabled: boolean
  interval_seconds: number
  jitter_seconds: number
  sample_mode?: string
  sample_count?: number
  last_run_at?: string | null
  next_run_at?: string | null
}

export interface OpenAIEvalRouteConfig {
  account_id: number
  requested_model: string
  reasoning_effort: string
  candy_schedule: OpenAIEvalSchedule
  fingerprint_schedule: OpenAIEvalSchedule
  modeltrace_schedule: OpenAIEvalSchedule
  state_probe_schedule: OpenAIEvalSchedule
  bps_auto: boolean
  /** Explicit BPS mode; older servers only return bps_auto. */
  bps_mode?: OpenAIEvalBPSMode
  bps_state?: OpenAIBPSModelState | null
  direct_oauth_eligible?: boolean
}

/** '' keeps the historical scheduler behaviour. */
export type OpenAIEvalSchedulingPolicy = '' | 'cost_first' | 'stability_first' | 'avoid_degradation' | 'custom_balance'

export interface OpenAIEvalPolicyWeights {
  cost: number
  /**
   * Legacy composite (60% error rate, 40% first-token latency). New configs
   * keep it at 0; the UI folds any saved value into error_rate/ttft.
   */
  stability?: number
  error_rate: number
  ttft: number
  load: number
  /** Weight of the evaluated integrity pass rate; older servers omit it (0). */
  quality?: number
  /**
   * Factors compared strictly, in order, before the weighted score. The
   * server canonicalizes cost/error_rate/ttft/load/quality and keeps its
   * stored list when the field is missing, so saves always send it.
   */
  absolute_priorities?: string[]
}

export type OpenAIEvalBPSMode = 'auto' | 'force_on' | 'force_off'

export interface OpenAIEvalSchedulingPolicyRule {
  requested_model: string
  /** Empty means every reasoning effort of the model. */
  reasoning_effort?: string
  policy: Exclude<OpenAIEvalSchedulingPolicy, ''>
  custom_balance?: OpenAIEvalPolicyWeights
}

/**
 * Runtime limits of one ranking policy. An account whose real requests exceed
 * either one is moved behind the accounts within both; it is never disabled.
 */
export interface OpenAIEvalPolicyThreshold {
  /** Ratio 0–1 inclusive. 0 is a real setting: any failure counts. */
  error_rate: number
  /** Finite, above 0 and at most 86400. */
  ttft_seconds: number
}

export interface OpenAIEvalSchedulingThresholds {
  cost_first: OpenAIEvalPolicyThreshold
  stability_first: OpenAIEvalPolicyThreshold
  avoid_degradation: OpenAIEvalPolicyThreshold
  custom_balance: OpenAIEvalPolicyThreshold
  /** Real requests (1–1,000,000) an account needs before its error rate is judged. */
  min_error_samples: number
  /** Real first-token measurements (1–1,000,000) needed before latency is judged. */
  min_ttft_samples: number
}

export interface OpenAIEvalConfig {
  /** Optimistic-concurrency token; the server rejects stale saves with 409. */
  revision?: number
  effects_enabled: boolean
  bps_auto_enabled: boolean
  scheduling_policy?: OpenAIEvalSchedulingPolicy
  policies?: OpenAIEvalSchedulingPolicyRule[]
  custom_balance?: OpenAIEvalPolicyWeights
  /**
   * Per-policy runtime thresholds and the shared minimum sample counts.
   * Legacy servers omit it and the UI uses the defaults; a configured object
   * is sent back as loaded by every save, including one from the tests page.
   */
  scheduling_thresholds?: OpenAIEvalSchedulingThresholds
  /**
   * BPS is decided per OAuth account, independent of the account/model/effort
   * test targets in `accounts`. Older servers omit the field.
   */
  bps_accounts?: OpenAIEvalBPSAccountConfig[]
  /**
   * Upstream attempts per logical sample, including the first one (1–10).
   * Applies to Candy, Fingerprint and ModelTrace; older servers omit it and
   * the UI treats it as 3.
   */
  max_request_attempts?: number
  /**
   * How often the integrity ranking snapshot used by scheduling is rebuilt
   * (seconds). Older servers omit it; the UI treats it as 3600.
   */
  quality_refresh_interval_seconds?: number
  /** Read-only projection of the last and next snapshot rebuild. */
  quality_refreshed_at?: string | null
  quality_next_refresh_at?: string | null
  accounts: OpenAIEvalRouteConfig[]
  // -- Read-only scheduling evaluation projections (rc3) ---------------------
  // Never sent back on save; the server derives them from the published build.
  /** The ranking in force for the saved revision, if one has been built. */
  ranking?: OpenAIEvalRankingSummary | null
  /** Set when the last build for this revision failed; the page states it plainly. */
  ranking_error?: RankingError | null
  /** What the gateway is actually applying right now. */
  effective_status?: OpenAIEvalEffectiveStatus
  evaluation_in_progress?: boolean
  /** Revision the server stored on the last accepted save. */
  saved_revision?: number
}

/** Runtime route of one BPS account as reported by the server. */
export type OpenAIEvalBPSAccountState = 'native' | 'bps' | 'locked' | string

export interface OpenAIEvalBPSAccountConfig {
  account_id: number
  /** Model used for the health check only; the whole account switches. */
  probe_model?: string
  mode: OpenAIEvalBPSMode
  /** Unhealthy probes in a row before switching to BPS. */
  failure_threshold: number
  /** Healthy probes in a row before switching back. */
  recovery_threshold: number
  interval_seconds: number
  // Read-only runtime fields; never sent back on save.
  active?: boolean
  state?: OpenAIEvalBPSAccountState
  disabled_reason?: string
  degraded_streak?: number
  healthy_streak?: number
  updated_at?: string | null
  last_run_at?: string | null
  next_run_at?: string | null
}

export interface OpenAIBPSModelState {
  active: boolean
  degraded_streak: number
  healthy_streak: number
  disabled_reason?: string
  updated_at?: string
}

export interface OpenAIEvalFingerprintResult {
  status: string
  nearest_model?: string
  mean_jsd?: number
  self_jsd?: number
  p_value?: number
  valid_samples: number
  required_samples: number
  cell_count: number
  reason?: string
  evaluated_at: string
}

export interface OpenAIEvalAttemptError {
  attempt: number
  code: string
  message: string
  http_status?: number
}

/** One logical sample. Diagnostic fields are absent on records written by older servers. */
export interface OpenAIEvalSampleRecord {
  probe_id: string
  normalized_answer?: string
  valid: boolean
  error_code?: string
  answer?: string
  attempts?: number
  error_message?: string
  http_status?: number
  attempt_errors?: OpenAIEvalAttemptError[]
}

/** ModelTrace keeps its own sample shape inside the outcome. */
export interface OpenAIEvalModelTraceSample {
  expected_count?: number
  attempts?: number
  answer?: string
  error_message?: string
  attempt_errors?: OpenAIEvalAttemptError[]
  http_status?: number
  error?: string
  parsed_numbers?: number
  accepted?: boolean
}

export interface OpenAIEvalRun {
  id: number
  account_id: number
  test_type: 'candy' | 'fingerprint' | 'modeltrace' | 'state_probe'
  requested_model: string
  upstream_model?: string
  reasoning_effort: string
  /** Question/data revision the run was scored against. */
  data_version?: string
  baseline_version?: string
  status: string
  outcome: {
    status: string
    reason?: string
    sample_count: number
    expected_count: number
    confidence: string
    scheduling: string
    fingerprint?: OpenAIEvalFingerprintResult
    modeltrace?: OpenAIEvalModelTraceResult
    state_probe?: OpenAIStateProbeResult
    /** Present when the server reads an attribution under its current rule; older servers omit it. */
    attribution?: OpenAIEvalAttributionMeta
  }
  request_count: number
  sample_count?: number
  expected_samples?: number
  completed_samples?: number
  phase?: string
  input_tokens: number
  output_tokens: number
  cost_estimate_usd?: number | null
  duration_ms: number
  started_at: string
  finished_at?: string
  trigger_source: string
  error?: string
  samples?: OpenAIEvalSampleRecord[]
}

/**
 * Attribution rule metadata. `status`/`reason` on the outcome are the current
 * interpretation; the original_* fields are what the run was stored with.
 * Raw evidence (prediction, candidates, samples) is never rewritten.
 */
export interface OpenAIEvalAttributionMeta {
  /** e.g. 'public-target-match-luna-v2' */
  rule_version: string
  /**
   * Rule the stored verdict was written under, e.g. 'non-luna-attribution-v1';
   * 'legacy-unversioned' when the record carried no metadata, so its rule is unknown.
   */
  original_rule_version: string
  original_status: string
  original_reason?: string
}

export interface OpenAIStateProbeResult {
  version: string
  verdict: 'healthy' | 'degraded' | 'inconclusive' | string
  failure?: string
  request_count: number
  mint_status?: number
  continue_status?: number
  new_ticket: boolean
  reported_model?: string
  latency_ms: number
  /** Mint/continue chains sent; older servers omit it (one chain). */
  attempts?: number
  /** Chain ceiling after the server's cap of three. */
  max_attempts?: number
  retry_policy?: string
  last_failure_step?: string
}

export interface OpenAIEvalModelTraceResult {
  bank_revision: string
  prediction?: string
  probability?: number
  family_prediction_name?: string
  family_probability?: number
  used_outputs: number
  requests: number
  samples?: OpenAIEvalModelTraceSample[]
  candidates?: Array<{ model: string; display_name: string; family: string; family_name: string; probability: number; profile_similarity: number; score: number }>
}

export interface OpenAIEvalModelCatalog {
  items: Array<{ id: string; display_name?: string }>
  baseline_version: string
  baseline_models: string[]
  /** Current question/data revision; runs with another value used older questions. */
  data_version?: string
  candy: { expected_answer: number; confidence: string; scheduling: string }
  evaluation_notice: string
  reasoning_efforts: string[]
  fingerprint_modes: Array<{ id: string; samples: number }>
  modeltrace: { requests: number; bank_revision: string; candidate_count: number; scheduling: string }
}

// ---------------------------------------------------------------------------
// Scheduling evaluation rankings
//
// The server publishes an immutable, per-instance ordering snapshot built with
// the same pure scorer the gateway uses for a real request. Every number below
// is computed server-side; the page renders it and never recalculates a score
// or a rank in the browser.
// ---------------------------------------------------------------------------

/** Relative weight of each factor. The five values sum to 1. */
/** The five numeric ranking factors: published weights and per-factor contributions. */
export interface OpenAIEvalRankingFactorWeights {
  price: number
  error_rate: number
  ttft: number
  load: number
  quality: number
}

/** A policy's published weights, plus the strict order custom balance compares first. */
export interface OpenAIEvalRankingWeights extends OpenAIEvalRankingFactorWeights {
  /** Custom balance only: factors compared strictly, in order, before the score. */
  absolute_priorities?: string[]
}

/**
 * One normalized factor. `known` is false while the raw evidence is missing or
 * unusable; `score` is then the neutral 0.5 and the page shows "unknown",
 * never a real reading. A genuine tie also normalizes to 0.5 with known=true.
 */
export interface FactorMeta {
  score: number
  known: boolean
  observed_at: string | null
  unknown_reason: string | null
  /**
   * True when a labelled default score (0.9) stood in for missing evidence.
   * With known=true as well, only some of the account's cells were defaulted.
   */
  default_applied?: boolean
}

export interface OpenAIEvalRankingFactors {
  price: FactorMeta & { rate_multiplier: number | null; source: string | null }
  /** source 'v1_matched_probe' marks an identity-matched probe estimate, not real traffic. */
  error_rate: FactorMeta & { value: number | null; sample_count: number; source?: string | null }
  ttft: FactorMeta & { ms: number | null; sample_count: number }
  load: FactorMeta & { load_rate: number | null; waiting: number | null; current_concurrency: number | null }
  quality: FactorMeta & {
    /** 'unknown'/'insufficient'/'stale' all mean "not assessable", not "healthy". */
    state: 'assessed' | 'unknown' | 'insufficient' | 'stale'
    pass: number
    suspected_pass: number
    /** Test types selected for this route. */
    selected: number
    /** Selected test types that had valid evidence. */
    evaluated: number
    /** present only when selected > 0 and evaluated equals selected. */
    ratio: number | null
    expires_at: string | null
    evidence_error_code?: string
    evidence_error_message?: string
  }
  /**
   * Matched channel probes, diagnostic only. latency_ms is a full round trip,
   * never first-output latency.
   */
  monitoring?: Array<{
    monitor_id: number
    model: string
    status: string
    observed_at: string
    latency_ms: number | null
    ping_latency_ms: number | null
  }>
}

/** Where a candidate route came from while the order was built. */
export type OpenAIEvalRankingSource =
  | 'catalog' | 'account_mapping' | 'channel_mapping' | 'group_route'
  | 'policy_rule' | 'eval_route' | 'observed_route'

export interface OpenAIEvalRankingExclusion {
  code: string
  scope: 'account' | 'route' | 'live' | string
  observed_at: string
}

export interface OpenAIEvalRankedAccount {
  account_id: number
  account_name: string
  /**
   * Runtime thresholds of the policy in force that this account's own real
   * requests exceeded, as computed at evaluation time: 'error_rate_threshold',
   * 'ttft_threshold', or a code a newer server adds. It is a soft ordering
   * exception — the account moves behind the accounts within both thresholds —
   * and never an exclusion. Absent for the system default policy, which uses
   * no thresholds, and for custom balance, which is ordered by its weights.
   */
  threshold_reasons?: string[]
  /** Position in the full candidate order; null when the account cannot serve the model. */
  rank: number | null
  /** 0-100 weighted total. */
  priority_score: number | null
  quality_tier: number | null
  /** Availability at evaluation time only; an ineligible account keeps its row. */
  eligible: boolean
  exclusion_reason: string | null
  exclusion_reasons?: OpenAIEvalRankingExclusion[]
  upstream_models: string[]
  factors: OpenAIEvalRankingFactors
  contributions: OpenAIEvalRankingFactorWeights
  /** 'account_prior' marks a tier ordered by a separate account-wide reference. */
  quality_basis?: OpenAIEvalQualityBasis | string
  /** Present with quality_basis 'account_prior'; see OpenAIEvalAccountQualityPrior. */
  account_quality_prior?: OpenAIEvalAccountQualityPrior | null
}

/** One concrete endpoint/model pair a dimension can be served by. */
export interface OpenAIEvalSelectionModelVariant {
  endpoint: string
  platform: string
  selection_model: string
}

export interface OpenAIEvalRankingDimension {
  dimension_id: string
  group_id: number | null
  group_name: string
  requested_model: string
  /** The raw requested effort; '' means unspecified, not normalized. */
  reasoning_effort: string
  selection_model: string | null
  selection_model_variants?: OpenAIEvalSelectionModelVariant[]
  policy: OpenAIEvalSchedulingPolicy
  weights: OpenAIEvalRankingWeights
  ordering: 'score_desc' | 'quality_then_score' | 'legacy'
  sources: OpenAIEvalRankingSource[]
  candidate_count: number | null
  eligible_count: number | null
  preferred_account_id: number | null
  /** 'live_fallback' means this order was computed on demand, not published. */
  coverage_status: 'complete' | 'live_fallback' | 'no_candidates' | 'legacy'
  fallback_reason: string | null
  valid_until: string | null
  /** Present only for a fully filtered request; the list omits accounts otherwise. */
  accounts?: OpenAIEvalRankedAccount[]
  accounts_truncated: boolean
  accounts_next_cursor: string | null
}

export interface OpenAIEvalRankingCoverage {
  status: 'complete' | 'partial' | 'empty'
  discovery_complete: boolean
  /** null when the catalog could not be counted. */
  discovered_dimension_count: number | null
  cached_dimension_count: number
  uncached_dimension_count: number | null
  wildcard_routes_present: boolean
  reasons: string[]
}

export type OpenAIEvalRankingTrigger = 'startup' | 'policy_saved' | 'manual' | 'interval' | 'catalog_change' | 'evidence_expiry'

export interface OpenAIEvalRankingSummary {
  evaluation_id: string
  /** Policy evaluations and actual dispatches are separate record types. */
  record_type: 'policy_evaluation'
  scope: string
  algorithm_version: string
  data_version: string
  /** Closes the input sampling window; published_at records when the build finished. */
  evaluated_at: string
  published_at: string
  next_evaluation_at: string
  next_evaluation_reason: 'interval' | 'evidence_expiry'
  trigger: OpenAIEvalRankingTrigger
  config_revision: number
  effects_enabled: boolean
  dimension_count: number
  account_count: number
  account_row_count: number
  quality_route_count: number
  /** True when the server omitted coverage, not a paging indicator. */
  truncated: boolean
  coverage: OpenAIEvalRankingCoverage
}

export interface OpenAIEvalRankingGroup {
  group_id: number | null
  group_name: string
  member_count: number
  status: 'evaluated' | 'no_accounts' | 'no_models' | 'out_of_scope' | 'live_fallback'
  reason: string | null
}

/**
 * What is actually in force right now. 'inactive_effects_off' is a valid saved
 * state, not a failure: the policy is stored but the gateway keeps the legacy
 * order until evaluation effects are switched on.
 */
export type OpenAIEvalEffectiveStatus =
  | 'active'
  | 'active_partial'
  | 'inactive_effects_off'
  | 'inactive_legacy_policy'
  | 'live_fallback'
  | 'no_targets'
  | 'error'

export interface RankingError {
  code: string
  message: string
  config_revision: number
  evaluation_id: string | null
}

export interface OpenAIEvalRankingSnapshot {
  summary: OpenAIEvalRankingSummary | null
  effective_status: OpenAIEvalEffectiveStatus
  current_config_revision: number
  evaluation_in_progress: boolean
  ranking_error: RankingError | null
  groups: OpenAIEvalRankingGroup[]
  /** Dimension summaries. `accounts` is present only for a fully filtered read. */
  dimensions: OpenAIEvalRankingDimension[]
  next_cursor: string | null
}

export interface OpenAIEvalRankingQuery {
  /** 0 means the no-group scope; omit the field to skip filtering. */
  group_id?: number
  requested_model?: string
  /** An empty string explicitly means "effort not specified". */
  reasoning_effort?: string
  evaluation_id?: string
  /** Bound to an evaluation_id and filter set; a reclaimed generation returns 409. */
  cursor?: string
  /** Default 100, maximum 500. */
  limit?: number
}

// ---------------------------------------------------------------------------
// Account overview (rc4)
//
// One row per unique account across every group, ranked by the default policy
// from real evidence only. A group filter hides rows; it never changes a score
// or a rank. Every number is computed by the server.
// ---------------------------------------------------------------------------

/** Numeric factors only; absolute_priorities is ordering metadata, not a factor. */
export type OpenAIEvalRankingFactorKey = keyof OpenAIEvalRankingFactorWeights

/** How an account is placed by the default policy; the parts behind priority_score. */
export interface OpenAIEvalAccountPriority {
  quality_known: boolean
  /** Exact macro pass rate across evidenced models; null when unknown. */
  quality_ratio: number | null
  /** Weighted operational score, 0-100. */
  operational_score: number
  quality_tier: number | null
}

/** One requested model (and effort) the account has real evidence for. */
export interface OpenAIEvalOverviewModel {
  requested_model: string
  /** The raw requested effort; '' means unspecified. */
  reasoning_effort: string
  upstream_models: string[]
  factors: OpenAIEvalRankingFactors
  /** Evidence kinds behind this cell, e.g. request_ewma_account_model_effort, scheduled_quality, v1_matched_probe. */
  sources: string[]
  /** The policy requests for this model and effort use at runtime. */
  policy?: OpenAIEvalSchedulingPolicy | string | null
  weights?: OpenAIEvalRankingWeights
}

export interface OpenAIEvalOverviewAccount extends OpenAIEvalRankedAccount {
  /** 0 is the ungrouped scope. */
  group_ids: number[]
  /** Models with real evidence; catalog- or mapping-only models are not counted. */
  model_count: number
  quality_model_count: number
  unknown_quality_model_count: number
  quality_cell_count?: number
  unknown_quality_cell_count?: number
  worst_quality_model: string | null
  worst_quality_ratio: number | null
  models: OpenAIEvalOverviewModel[]
  /**
   * The parts of the quality-first order. priority_score is the operational
   * score alone, so quality-first order is shown from these values.
   */
  priority?: OpenAIEvalAccountPriority
}

export interface OpenAIEvalAccountOverview {
  summary: OpenAIEvalRankingSummary | null
  /** The completed evaluation before `summary`, kept for the records tab. */
  previous_summary: OpenAIEvalRankingSummary | null
  effective_status: OpenAIEvalEffectiveStatus
  current_config_revision: number
  evaluation_in_progress: boolean
  ranking_error: RankingError | null
  groups: OpenAIEvalRankingGroup[]
  /** The default policy the overview is ranked with. */
  policy: OpenAIEvalSchedulingPolicy
  weights: OpenAIEvalRankingWeights
  ordering: 'score_desc' | 'quality_then_score' | 'legacy' | string
  accounts: OpenAIEvalOverviewAccount[]
  next_cursor: string | null
}

export interface OpenAIEvalAccountOverviewQuery {
  /** Omit for all groups. */
  group_id?: number
  /** Default 100, maximum 500. */
  limit?: number
  /** Bound to evaluation_id and group_id; an expired generation answers 409. */
  cursor?: string
  evaluation_id?: string
}

export async function getOpenAIEvalModels(): Promise<OpenAIEvalModelCatalog> {
  const { data } = await apiClient.get<OpenAIEvalModelCatalog>('/admin/accounts/evaluations/models')
  return data
}

export async function getOpenAIEvalConfig(): Promise<OpenAIEvalConfig> {
  const { data } = await apiClient.get<OpenAIEvalConfig>('/admin/accounts/evaluations/config')
  return data
}

export async function saveOpenAIEvalConfig(config: OpenAIEvalConfig): Promise<OpenAIEvalConfig> {
  const { data } = await apiClient.put<OpenAIEvalConfig>('/admin/accounts/evaluations/config', config)
  return data
}

export async function runOpenAIEval(request: {
  account_id: number
  test_type: 'candy' | 'fingerprint' | 'modeltrace' | 'state_probe'
  requested_model: string
  reasoning_effort: string
  sample_mode?: string
  sample_count?: number
  /** Attempts per sample including the first; ignored by State Probe. */
  max_attempts?: number
}): Promise<OpenAIEvalRun> {
  // Fingerprint runs can make 60-400 upstream requests; the shared 30s UI
  // timeout would cancel a healthy run and incorrectly show an error.
  const { data } = await apiClient.post<OpenAIEvalRun>('/admin/accounts/evaluations/run', request, { timeout: 30 * 60 * 1000 })
  return data
}

export async function listOpenAIEvalRuns(params?: {
  account_id?: number
  requested_model?: string
  reasoning_effort?: string
  test_type?: string
  limit?: number
}): Promise<{ items: OpenAIEvalRun[] }> {
  const { data } = await apiClient.get<{ items: OpenAIEvalRun[] }>('/admin/accounts/evaluations/runs', { params })
  return data
}

export interface OpenAIEvalQualityRefreshResult {
  refreshed_at: string | null
  next_refresh_at?: string | null
  /**
   * Entries actually published to the enabled quality cache. Stays 0 while
   * evaluation effects are off. Superseded by quality_route_count below.
   */
  route_count: number
  /** Route count this build computed, whether or not it could be published. */
  quality_route_count?: number
  /** The same evaluation summary the evaluate endpoint returns. */
  saved_revision?: number
  ranking_error?: RankingError | null
  effective_status?: OpenAIEvalEffectiveStatus
  evaluation_in_progress?: boolean
  ranking?: OpenAIEvalRankingSummary | null
}

/**
 * Rebuilds the integrity ranking snapshot from stored automatic results using
 * the saved config. It sends no upstream requests and runs no new tests.
 */
export async function refreshOpenAIEvalQuality(): Promise<OpenAIEvalQualityRefreshResult> {
  const { data } = await apiClient.post<OpenAIEvalQualityRefreshResult>('/admin/accounts/evaluations/quality/refresh', {})
  return data
}

/**
 * Runs the full scheduling evaluation now with the saved configuration and
 * returns the published summary.
 *
 * The server reads the saved config itself: this endpoint takes no body and
 * never applies unsaved page edits. It sends no upstream requests, so a slow
 * budget means work, not provider traffic. A longer timeout than the default
 * is allowed because the server budgets 20 seconds plus publication.
 */
export async function evaluateOpenAIEvalRanking(): Promise<OpenAIEvalRankingSummary> {
  const { data } = await apiClient.post<OpenAIEvalRankingSummary>(
    '/admin/accounts/evaluations/scheduling/evaluate',
    {},
    { timeout: 60 * 1000 }
  )
  return data
}

/**
 * Reads the published ranking snapshot. Without a full group + model + effort
 * filter the server returns dimension summaries only and omits every account
 * list, so a filtered read is what fetches the ranked rows.
 */
export async function getOpenAIEvalRankings(query: OpenAIEvalRankingQuery = {}): Promise<OpenAIEvalRankingSnapshot> {
  const { data } = await apiClient.get<OpenAIEvalRankingSnapshot>('/admin/accounts/evaluations/scheduling/rankings', {
    params: query
  })
  return data
}

/**
 * Reads one server-paginated page of a single dimension's ranked accounts.
 * The cursor is bound to the evaluation generation and filters; a reclaimed
 * generation answers 409 RANKING_SNAPSHOT_CHANGED and must be re-read, never
 * spliced onto the previous page.
 */
export async function getOpenAIEvalRankingAccounts(
  query: OpenAIEvalRankingQuery & { group_id: number; requested_model: string; reasoning_effort: string }
): Promise<OpenAIEvalRankingSnapshot> {
  const { data } = await apiClient.get<OpenAIEvalRankingSnapshot>('/admin/accounts/evaluations/scheduling/rankings', {
    params: query
  })
  return data
}

/**
 * Reads one page of the unique-account leaderboard. A cursor continues only
 * the generation and group filter it was issued for; never splice two.
 */
export async function getOpenAIEvalAccountOverview(query: OpenAIEvalAccountOverviewQuery = {}): Promise<OpenAIEvalAccountOverview> {
  const { data } = await apiClient.get<OpenAIEvalAccountOverview>('/admin/accounts/evaluations/scheduling/account-overview', {
    params: query
  })
  return data
}

export async function listOpenAIEvalAudit(): Promise<{ items: Array<{ id: number; actor_id: number; action: string; payload: Record<string, unknown>; created_at: string }> }> {
  const { data } = await apiClient.get<{ items: Array<{ id: number; actor_id: number; action: string; payload: Record<string, unknown>; created_at: string }> }>('/admin/accounts/evaluations/audit')
  return data
}

export async function resetOpenAIBPSState(request: { account_id: number; requested_model?: string }): Promise<{ state: OpenAIBPSModelState }> {
  const { data } = await apiClient.post<{ state: OpenAIBPSModelState }>('/admin/accounts/evaluations/bps/reset', request)
  return data
}

export async function listSchedulerDecisions(limit = 50, groupId?: number | null): Promise<{ items: SchedulerDecisionTrace[]; limit: number }> {
  const { data } = await apiClient.get<{ items: SchedulerDecisionTrace[]; limit: number }>(
    '/admin/accounts/scheduler-decisions',
    // group_id is a positive integer; anything else reads all groups.
    { params: { limit, ...(groupId != null && Number.isInteger(groupId) && groupId > 0 ? { group_id: groupId } : {}) } }
  )
  return data
}

/**
 * List all accounts with pagination
 * @param page - Page number (default: 1)
 * @param pageSize - Items per page (default: 20)
 * @param filters - Optional filters
 * @returns Paginated list of accounts
 */
export async function list(
  page: number = 1,
  pageSize: number = 20,
  filters?: {
    platform?: string
    type?: string
    status?: string
    group?: string
    search?: string
    privacy_mode?: string
    lite?: string
    include_scheduler_score?: string
    sort_by?: string
    sort_order?: 'asc' | 'desc'
  },
  options?: {
    signal?: AbortSignal
  }
): Promise<PaginatedResponse<AccountListItem>> {
  const { data } = await apiClient.get<PaginatedResponse<AccountListItem>>('/admin/accounts', {
    params: {
      page,
      page_size: pageSize,
      ...filters
    },
    signal: options?.signal
  })
  return data
}

export interface AccountListWithEtagResult {
  notModified: boolean
  etag: string | null
  data: PaginatedResponse<AccountListItem> | null
}

export interface AccountUpstreamBillingRatesWithEtagResult {
  notModified: boolean
  etag: string | null
  data: UpstreamBillingRatesResponse | null
}

export async function getUpstreamBillingRatesWithEtag(
  page: number = 1,
  pageSize: number = 20,
  filters?: {
    platform?: string
    type?: string
    status?: string
    group?: string
    search?: string
    privacy_mode?: string
    sort_by?: string
    sort_order?: 'asc' | 'desc'
  },
  options?: {
    signal?: AbortSignal
    etag?: string | null
  }
): Promise<AccountUpstreamBillingRatesWithEtagResult> {
  const headers: Record<string, string> = {}
  if (options?.etag) headers['If-None-Match'] = options.etag

  const response = await apiClient.get<UpstreamBillingRatesResponse>('/admin/accounts/upstream-billing-rates', {
    params: { page, page_size: pageSize, ...filters },
    headers,
    signal: options?.signal,
    validateStatus: (status) => (status >= 200 && status < 300) || status === 304
  })

  const etagHeader = typeof response.headers?.etag === 'string' ? response.headers.etag : null
  if (response.status === 304) return { notModified: true, etag: etagHeader, data: null }
  return { notModified: false, etag: etagHeader, data: response.data }
}

export async function listWithEtag(
  page: number = 1,
  pageSize: number = 20,
  filters?: {
    platform?: string
    type?: string
    status?: string
    group?: string
    search?: string
    privacy_mode?: string
    lite?: string
    include_scheduler_score?: string
    sort_by?: string
    sort_order?: 'asc' | 'desc'
  },
  options?: {
    signal?: AbortSignal
    etag?: string | null
  }
): Promise<AccountListWithEtagResult> {
  const headers: Record<string, string> = {}
  if (options?.etag) {
    headers['If-None-Match'] = options.etag
  }

  const response = await apiClient.get<PaginatedResponse<AccountListItem>>('/admin/accounts', {
    params: {
      page,
      page_size: pageSize,
      ...filters
    },
    headers,
    signal: options?.signal,
    validateStatus: (status) => (status >= 200 && status < 300) || status === 304
  })

  const etagHeader = typeof response.headers?.etag === 'string' ? response.headers.etag : null
  if (response.status === 304) {
    return {
      notModified: true,
      etag: etagHeader,
      data: null
    }
  }

  return {
    notModified: false,
    etag: etagHeader,
    data: response.data
  }
}

/**
 * Get account by ID
 * @param id - Account ID
 * @returns Account details
 */
export async function getById(id: number): Promise<Account> {
  const { data } = await apiClient.get<Account>(`/admin/accounts/${id}`)
  return data
}

/**
 * Create new account
 * @param accountData - Account data
 * @returns Created account
 */
export async function create(accountData: CreateAccountRequest): Promise<Account> {
  const { data } = await apiClient.post<Account>('/admin/accounts', accountData)
  return data
}

/**
 * Duplicate an account while keeping credentials on the server.
 * @param id - Source account ID
 * @returns Newly created account
 */
const duplicateOperationKeys = new Map<number, string>()

function duplicateOperationStorageKey(id: number): string {
  return `sub2api:admin:account-duplicate:${id}`
}

function getStoredDuplicateOperationKey(id: number): string | null {
  try {
    return globalThis.sessionStorage?.getItem(duplicateOperationStorageKey(id)) ?? null
  } catch {
    return null
  }
}

function storeDuplicateOperationKey(id: number, key: string | null): void {
  try {
    if (key) globalThis.sessionStorage?.setItem(duplicateOperationStorageKey(id), key)
    else globalThis.sessionStorage?.removeItem(duplicateOperationStorageKey(id))
  } catch {
    // In-memory retry protection still works when browser storage is unavailable.
  }
}

export async function duplicate(id: number): Promise<Account> {
  let idempotencyKey = duplicateOperationKeys.get(id) ?? getStoredDuplicateOperationKey(id)
  if (!idempotencyKey) {
    const requestID = globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(36).slice(2)}`
    idempotencyKey = `account-duplicate-${id}-${requestID}`
  }
  duplicateOperationKeys.set(id, idempotencyKey)
  storeDuplicateOperationKey(id, idempotencyKey)
  const { data } = await apiClient.post<Account>(`/admin/accounts/${id}/duplicate`, undefined, {
    headers: { 'Idempotency-Key': idempotencyKey }
  })
  duplicateOperationKeys.delete(id)
  storeDuplicateOperationKey(id, null)
  return data
}

/**
 * Update account
 * @param id - Account ID
 * @param updates - Fields to update
 * @returns Updated account
 */
export async function update(id: number, updates: UpdateAccountRequest): Promise<Account> {
  const { data } = await apiClient.put<Account>(`/admin/accounts/${id}`, updates)
  return data
}

export async function getGrokMediaEligibility(id: number): Promise<GrokMediaEligibilityState> {
  const { data } = await apiClient.get<GrokMediaEligibilityState>(
    `/admin/accounts/${id}/grok-media-eligibility`
  )
  return data
}

export async function updateGrokMediaEligibility(
  id: number,
  mode: GrokMediaEligibilityMode
): Promise<GrokMediaEligibilityState> {
  const { data } = await apiClient.put<GrokMediaEligibilityState>(
    `/admin/accounts/${id}/grok-media-eligibility`,
    { mode }
  )
  return data
}

/**
 * Check mixed-channel risk for account-group binding.
 */
export async function checkMixedChannelRisk(
  payload: CheckMixedChannelRequest
): Promise<CheckMixedChannelResponse> {
  const { data } = await apiClient.post<CheckMixedChannelResponse>('/admin/accounts/check-mixed-channel', payload)
  return data
}

/**
 * Delete account
 * @param id - Account ID
 * @returns Success confirmation
 */
export async function deleteAccount(id: number): Promise<{ message: string }> {
  const { data } = await apiClient.delete<{ message: string }>(`/admin/accounts/${id}`)
  return data
}

/**
 * Toggle account status
 * @param id - Account ID
 * @param status - New status
 * @returns Updated account
 */
export async function toggleStatus(id: number, status: 'active' | 'inactive'): Promise<Account> {
  return update(id, { status })
}

/**
 * Test account connectivity
 * @param id - Account ID
 * @returns Test result
 */
export async function testAccount(id: number): Promise<{
  success: boolean
  message: string
  latency_ms?: number
}> {
  const { data } = await apiClient.post<{
    success: boolean
    message: string
    latency_ms?: number
  }>(`/admin/accounts/${id}/test`)
  return data
}

/**
 * Refresh account credentials
 * @param id - Account ID
 * @returns Updated account
 */
export type RefreshCredentialsResult =
  | { account: Account; message: string; warning: 'missing_project_id_temporary' }
  | { account: Account; message?: never; warning?: never }

export async function refreshCredentials(id: number): Promise<RefreshCredentialsResult> {
  const { data } = await apiClient.post<Account | RefreshCredentialsResult>(`/admin/accounts/${id}/refresh`)
  return 'account' in data ? data : { account: data }
}

/**
 * Apply OAuth credentials after re-authorization.
 *
 * Unlike `update()`, this endpoint:
 * - never overwrites the whole `extra` JSONB (merges incrementally instead),
 *   so persistent settings like `base_rpm`, `window_cost_limit`, `max_sessions`,
 *   `quota_*` and `privacy_mode` are preserved
 * - clears the account error and invalidates the token cache server-side
 */
export async function applyOAuthCredentials(
  id: number,
  payload: {
    type: 'oauth' | 'setup-token'
    credentials: Record<string, unknown>
    extra?: Record<string, unknown>
  }
): Promise<Account> {
  const { data } = await apiClient.post<Account>(
    `/admin/accounts/${id}/apply-oauth-credentials`,
    payload
  )
  return data
}

/**
 * Get account usage statistics
 * @param id - Account ID
 * @param days - Number of days (default: 30)
 * @returns Account usage statistics with history, summary, and models
 */
export async function getStats(id: number, days: number = 30): Promise<AccountUsageStatsResponse> {
  const { data } = await apiClient.get<AccountUsageStatsResponse>(`/admin/accounts/${id}/stats`, {
    params: { days }
  })
  return data
}

/**
 * Clear account error
 * @param id - Account ID
 * @returns Updated account
 */
export async function clearError(id: number): Promise<Account> {
  const { data } = await apiClient.post<Account>(`/admin/accounts/${id}/clear-error`)
  return data
}

/**
 * Get account usage information (5h/7d window)
 * @param id - Account ID
 * @returns Account usage info
 */
export async function getUsage(id: number, source?: 'passive' | 'active', force?: boolean): Promise<AccountUsageInfo> {
  const params: Record<string, string> = {}
  if (source) params.source = source
  if (force) params.force = 'true'
  const { data } = await apiClient.get<AccountUsageInfo>(`/admin/accounts/${id}/usage`, {
    params: Object.keys(params).length > 0 ? params : undefined
  })
  return data
}

export interface BatchAccountUsageResponse {
  usage: Record<string, AccountUsageInfo>
  errors: Record<string, string>
}

export async function getBatchUsage(accountIds: number[], force?: boolean): Promise<BatchAccountUsageResponse> {
  const { data } = await apiClient.post<BatchAccountUsageResponse>('/admin/accounts/usage/batch', {
    account_ids: accountIds,
    force: force === true
  })
  return data
}

/**
 * Clear account rate limit status
 * @param id - Account ID
 * @returns Updated account
 */
export async function clearRateLimit(id: number): Promise<Account> {
  const { data } = await apiClient.post<Account>(
    `/admin/accounts/${id}/clear-rate-limit`
  )
  return data
}

/**
 * Recover account runtime state in one call
 * @param id - Account ID
 * @returns Updated account
 */
export async function recoverState(id: number): Promise<Account> {
  const { data } = await apiClient.post<Account>(`/admin/accounts/${id}/recover-state`)
  return data
}

/**
 * Reset account quota usage
 * @param id - Account ID
 * @returns Updated account
 */
export async function resetAccountQuota(id: number): Promise<Account> {
  const { data } = await apiClient.post<Account>(
    `/admin/accounts/${id}/reset-quota`
  )
  return data
}

/**
 * Get temporary unschedulable status
 * @param id - Account ID
 * @returns Status with detail state if active
 */
export async function getTempUnschedulableStatus(id: number): Promise<TempUnschedulableStatus> {
  const { data } = await apiClient.get<TempUnschedulableStatus>(
    `/admin/accounts/${id}/temp-unschedulable`
  )
  return data
}

/**
 * Reset temporary unschedulable status
 * @param id - Account ID
 * @returns Success confirmation
 */
export async function resetTempUnschedulable(id: number): Promise<{ message: string }> {
  const { data } = await apiClient.delete<{ message: string }>(
    `/admin/accounts/${id}/temp-unschedulable`
  )
  return data
}

/**
 * Generate OAuth authorization URL
 * @param endpoint - API endpoint path
 * @param config - Proxy configuration
 * @returns Auth URL and session ID
 */
export async function generateAuthUrl(
  endpoint: string,
  config: { proxy_id?: number }
): Promise<{ auth_url: string; session_id: string }> {
  const { data } = await apiClient.post<{ auth_url: string; session_id: string }>(endpoint, config)
  return data
}

/**
 * Exchange authorization code for tokens
 * @param endpoint - API endpoint path
 * @param exchangeData - Session ID, code, and optional proxy config
 * @returns Token information
 */
export async function exchangeCode(
  endpoint: string,
  exchangeData: { session_id: string; code: string; state?: string; proxy_id?: number }
): Promise<Record<string, unknown>> {
  const { data } = await apiClient.post<Record<string, unknown>>(endpoint, exchangeData)
  return data
}

/**
 * Batch create accounts
 * @param accounts - Array of account data
 * @returns Results of batch creation
 */
export async function batchCreate(accounts: CreateAccountRequest[]): Promise<{
  success: number
  failed: number
  results: Array<{ success: boolean; account?: Account; error?: string }>
}> {
  const { data } = await apiClient.post<{
    success: number
    failed: number
    results: Array<{ success: boolean; account?: Account; error?: string }>
  }>('/admin/accounts/batch', { accounts })
  return data
}

/**
 * Batch update credentials fields for multiple accounts
 * @param request - Batch update request containing account IDs, field name, and value
 * @returns Results of batch update
 */
export async function batchUpdateCredentials(request: {
  account_ids: number[]
  field: string
  value: any
}): Promise<{
  success: number
  failed: number
  results: Array<{ account_id: number; success: boolean; error?: string }>
}> {
  const { data } = await apiClient.post<{
    success: number
    failed: number
    results: Array<{ account_id: number; success: boolean; error?: string }>
  }>('/admin/accounts/batch-update-credentials', request)
  return data
}

/**
 * Bulk update multiple accounts
 * @param accountIds - Array of account IDs
 * @param updates - Fields to update
 * @returns Success confirmation
 */
export async function bulkUpdate(
  accountIdsOrPayload: number[] | Record<string, unknown>,
  updates?: Record<string, unknown>
): Promise<{
  success: number
  failed: number
  success_ids?: number[]
  failed_ids?: number[]
  long_context_inherited_count?: number
  results: Array<{ account_id: number; success: boolean; error?: string }>
  }> {
  const payload = Array.isArray(accountIdsOrPayload)
    ? {
        account_ids: accountIdsOrPayload,
        ...(updates ?? {})
      }
    : accountIdsOrPayload
  const { data } = await apiClient.post<{
    success: number
    failed: number
    success_ids?: number[]
    failed_ids?: number[]
    long_context_inherited_count?: number
    results: Array<{ account_id: number; success: boolean; error?: string }>
  }>('/admin/accounts/bulk-update', payload)
  return data
}

/**
 * Get account today statistics
 * @param id - Account ID
 * @returns Today's stats (requests, tokens, cost)
 */
export async function getTodayStats(id: number): Promise<WindowStats> {
  const { data } = await apiClient.get<WindowStats>(`/admin/accounts/${id}/today-stats`)
  return data
}

export interface BatchTodayStatsResponse {
  stats: Record<string, WindowStats>
}

/**
 * 批量获取多个账号的今日统计
 * @param accountIds - 账号 ID 列表
 * @returns 以账号 ID（字符串）为键的统计映射
 */
export async function getBatchTodayStats(accountIds: number[]): Promise<BatchTodayStatsResponse> {
  const { data } = await apiClient.post<BatchTodayStatsResponse>('/admin/accounts/today-stats/batch', {
    account_ids: accountIds
  })
  return data
}

/**
 * Set account schedulable status
 * @param id - Account ID
 * @param schedulable - Whether the account should participate in scheduling
 * @returns Updated account
 */
export async function setSchedulable(id: number, schedulable: boolean): Promise<Account> {
  const { data } = await apiClient.post<Account>(`/admin/accounts/${id}/schedulable`, {
    schedulable
  })
  return data
}

/**
 * Get available models for an account
 * @param id - Account ID
 * @returns List of available models for this account
 */
export async function getAvailableModels(id: number): Promise<ClaudeModel[]> {
  const { data } = await apiClient.get<ClaudeModel[]>(`/admin/accounts/${id}/models`)
  return data
}

export interface SyncUpstreamModelsResult {
  models: string[]
  metadata?: Record<string, UpstreamModelMetadata>
  warnings?: UpstreamModelSyncWarning[]
}

export interface UpstreamModelSyncWarning {
  code: string
  message: string
}

export interface UpstreamModelMetadata {
  id: string
  display_name?: string
  description?: string
  reasoning?: boolean
  default_reasoning_level?: string
  supported_reasoning_levels?: string[]
  input_modalities?: string[]
  context_window?: number
  max_context_window?: number
  max_output_tokens?: number
}

/**
 * Sync live supported models from the account's upstream model-list endpoint
 * @param id - Account ID
 * @returns List of model IDs returned by the upstream
 */
export async function syncUpstreamModels(id: number): Promise<SyncUpstreamModelsResult> {
  const { data } = await apiClient.post<SyncUpstreamModelsResult>(`/admin/accounts/${id}/models/sync-upstream`)
  return data
}

export interface SyncUpstreamPreviewParams {
  platform: string
  type: string
  base_url?: string
  api_key: string
  model_mapping?: Record<string, string>
}

/**
 * Preview upstream models without a saved account (create-flow)
 * @param params - Connection credentials
 * @returns List of model IDs returned by the upstream
 */
export async function syncUpstreamModelsPreview(params: SyncUpstreamPreviewParams): Promise<SyncUpstreamModelsResult> {
  const { data } = await apiClient.post<SyncUpstreamModelsResult>('/admin/accounts/models/sync-upstream-preview', params)
  return data
}

export interface CRSPreviewAccount {
  crs_account_id: string
  kind: string
  name: string
  platform: string
  type: string
}

export interface PreviewFromCRSResult {
  new_accounts: CRSPreviewAccount[]
  existing_accounts: CRSPreviewAccount[]
}

export async function previewFromCrs(params: {
  base_url: string
  username: string
  password: string
}): Promise<PreviewFromCRSResult> {
  const { data } = await apiClient.post<PreviewFromCRSResult>('/admin/accounts/sync/crs/preview', params)
  return data
}

export async function syncFromCrs(params: {
  base_url: string
  username: string
  password: string
  sync_proxies?: boolean
  selected_account_ids?: string[]
}): Promise<{
  created: number
  updated: number
  skipped: number
  failed: number
  items: Array<{
    crs_account_id: string
    kind: string
    name: string
    action: string
    error?: string
  }>
}> {
  const { data } = await apiClient.post<{
    created: number
    updated: number
    skipped: number
    failed: number
    items: Array<{
      crs_account_id: string
      kind: string
      name: string
      action: string
      error?: string
    }>
  }>('/admin/accounts/sync/crs', params, {
    timeout: 180000 // 180s timeout: sync refreshes each existing account's OAuth token serially
  })
  return data
}

export async function exportData(options?: {
  ids?: number[]
  filters?: {
    platform?: string
    type?: string
    status?: string
    group?: string
    privacy_mode?: string
    search?: string
    sort_by?: string
    sort_order?: 'asc' | 'desc'
  }
  includeProxies?: boolean
}): Promise<AdminDataPayload> {
  const params: Record<string, string> = {}
  if (options?.ids && options.ids.length > 0) {
    params.ids = options.ids.join(',')
  } else if (options?.filters) {
    const { platform, type, status, group, privacy_mode, search, sort_by, sort_order } = options.filters
    if (platform) params.platform = platform
    if (type) params.type = type
    if (status) params.status = status
    if (group) params.group = group
    if (privacy_mode) params.privacy_mode = privacy_mode
    if (search) params.search = search
    if (sort_by) params.sort_by = sort_by
    if (sort_order) params.sort_order = sort_order
  }
  if (options?.includeProxies === false) {
    params.include_proxies = 'false'
  }
  const { data } = await apiClient.get<AdminDataPayload>('/admin/accounts/data', { params })
  return data
}

export async function importData(payload: {
  data: AdminDataPayload
  skip_default_group_bind?: boolean
}): Promise<AdminDataImportResult> {
  const { data } = await apiClient.post<AdminDataImportResult>('/admin/accounts/data', {
    data: payload.data,
    skip_default_group_bind: payload.skip_default_group_bind
  })
  return data
}

export async function importCodexSession(payload: CodexSessionImportRequest): Promise<CodexSessionImportResult> {
  const { data } = await apiClient.post<CodexSessionImportResult>('/admin/accounts/import/codex-session', payload, {
    timeout: 120000 // 120s timeout for large session imports
  })
  return data
}

export async function createOpenAICodexPAT(payload: OpenAICodexPATCreateRequest): Promise<Account> {
  const { data } = await apiClient.post<Account>('/admin/openai/create-from-codex-pat', payload)
  return data
}

/**
 * Get Antigravity default model mapping from backend
 * @returns Default model mapping (from -> to)
 */
export async function getAntigravityDefaultModelMapping(): Promise<Record<string, string>> {
  const { data } = await apiClient.get<Record<string, string>>(
    '/admin/accounts/antigravity/default-model-mapping'
  )
  return data
}

/**
 * Refresh OpenAI token using refresh token
 * @param refreshToken - The refresh token
 * @param proxyId - Optional proxy ID
 * @returns Token information including access_token, email, etc.
 */
export async function refreshOpenAIToken(
  refreshToken: string,
  proxyId?: number | null,
  endpoint: string = '/admin/openai/refresh-token',
  clientId?: string
): Promise<Record<string, unknown>> {
  const payload: { refresh_token: string; proxy_id?: number; client_id?: string } = {
    refresh_token: refreshToken
  }
  if (proxyId) {
    payload.proxy_id = proxyId
  }
  if (clientId) {
    payload.client_id = clientId
  }
  const { data } = await apiClient.post<Record<string, unknown>>(endpoint, payload)
  return data
}

/**
 * Batch operation result type
 */
export interface BatchOperationResult {
  total: number
  success: number
  failed: number
  success_ids?: number[]
  failed_ids?: number[]
  errors?: Array<{ account_id: number; error: string }>
  warnings?: Array<{ account_id: number; warning: string }>
}

/**
 * Revert account proxy to original before fallback
 * @param id - Account ID
 * @returns Success confirmation
 */
export async function revertProxyFallback(id: number): Promise<{ message: string }> {
  const { data } = await apiClient.post<{ message: string }>(`/admin/accounts/${id}/revert-proxy-fallback`)
  return data
}

/**
 * Delete multiple accounts with bounded server-side concurrency.
 */
export async function batchDelete(accountIds: number[]): Promise<BatchOperationResult> {
  const { data } = await apiClient.post<BatchOperationResult>('/admin/accounts/batch-delete', {
    account_ids: accountIds
  })
  return data
}

/**
 * Batch clear account errors
 * @param accountIds - Array of account IDs
 * @returns Batch operation result
 */
export async function batchClearError(accountIds: number[]): Promise<BatchOperationResult> {
  const { data } = await apiClient.post<BatchOperationResult>('/admin/accounts/batch-clear-error', {
    account_ids: accountIds
  })
  return data
}

/**
 * Batch refresh account credentials
 * @param accountIds - Array of account IDs
 * @returns Batch operation result
 */
export async function batchRefresh(accountIds: number[]): Promise<BatchOperationResult> {
  const { data } = await apiClient.post<BatchOperationResult>('/admin/accounts/batch-refresh', {
    account_ids: accountIds,
  }, {
    timeout: 120000  // 120s timeout for large batch refreshes
  })
  return data
}

/**
 * Set privacy for an Antigravity OAuth account
 * @param id - Account ID
 * @returns Updated account
 */
export async function setPrivacy(id: number): Promise<Account> {
  const { data } = await apiClient.post<Account>(`/admin/accounts/${id}/set-privacy`)
  return data
}

/**
 * OpenAI / Codex rate-limit reset feature: query and reset upstream usage.
 */
export interface OpenAIRateLimitWindow {
  used_percent: number
  limit_window_seconds: number
  reset_after_seconds: number
  reset_at: number
}

export interface OpenAIRateLimit {
  allowed: boolean
  limit_reached: boolean
  primary_window?: OpenAIRateLimitWindow | null
  secondary_window?: OpenAIRateLimitWindow | null
}

export interface OpenAIAdditionalRateLimit {
  limit_name: string
  metered_feature: string
  rate_limit?: OpenAIRateLimit | null
}

export interface OpenAIRateLimitResetCreditDetail {
  expires_at?: string
}

export interface OpenAIRateLimitResetCredits {
  available_count: number
  credits?: OpenAIRateLimitResetCreditDetail[]
}

export interface OpenAIQuotaUsage {
  user_id?: string
  account_id?: string
  email?: string
  plan_type?: string
  rate_limit?: OpenAIRateLimit | null
  additional_rate_limits?: OpenAIAdditionalRateLimit[]
  rate_limit_reset_credits?: OpenAIRateLimitResetCredits | null
  credits?: OpenAICredits | null
  fetched_at: number
}

export interface OpenAICredits {
  has_credits: boolean
  unlimited: boolean
  balance: string | null
}

export interface OpenAIQuotaResetCredit {
  id?: string
  reset_type?: string
  status?: string
  granted_at?: string
  expires_at?: string
  redeem_started_at?: string
  redeemed_at?: string
}

export interface OpenAIQuotaResetResult {
  code: string
  credit?: OpenAIQuotaResetCredit | null
  windows_reset: number
  quota?: OpenAIQuotaUsage | null
  account?: Account | null
  cache_refreshed: boolean
  account_state_recovered: boolean
  warning_code?:
    | 'reset_credit_cache_refresh_failed'
    | 'account_state_recovery_failed'
    | 'account_state_refresh_failed'
}

/** Usage payload plus whether the reset-credit snapshot was persisted. */
export interface OpenAIQuotaRefreshResult extends OpenAIQuotaUsage {
  cache_persisted: boolean
  credits_cache_persisted?: boolean
}

/**
 * Query the upstream quota AND persist the reset-credit snapshot on the account
 * so the card can be rehydrated without an upstream round-trip. It is a POST
 * because it writes account state (and must therefore be audited).
 *
 * The read-only `GET /admin/openai/accounts/:id/quota` endpoint still exists for
 * API consumers; the panel always wants the snapshot persisted, so it has no
 * client binding here.
 */
export async function refreshOpenAIQuota(id: number): Promise<OpenAIQuotaRefreshResult> {
  const { data } = await apiClient.post<OpenAIQuotaRefreshResult>(
    `/admin/openai/accounts/${id}/quota/refresh`
  )
  return data
}

export async function refreshOpenAIReferrals(id: number): Promise<OpenAIReferralRefreshResult> {
  const { data } = await apiClient.post<OpenAIReferralRefreshResult>(
    `/admin/openai/accounts/${id}/referrals/refresh`
  )
  return data
}

export async function sendOpenAIReferralInvite(
  id: number,
  input: { email: string; program_id: string; confirmed: boolean }
): Promise<OpenAIReferralSendResult> {
  const { data } = await apiClient.post<OpenAIReferralSendResult>(
    `/admin/openai/accounts/${id}/referrals/invite`, input, { timeout: 90_000 }
  )
  return data
}

/**
 * Consume one rate-limit-reset credit for an OpenAI/Codex OAuth account.
 *
 * The credit is non-refundable and the endpoint chains an upstream reset with an
 * upstream re-query, so it needs a larger budget than the default client
 * timeout: aborting locally would report a successful consumption as a failure
 * and invite a retry that spends a second credit.
 */
export async function resetOpenAIQuota(id: number): Promise<OpenAIQuotaResetResult> {
  const { data } = await apiClient.post<OpenAIQuotaResetResult>(
    `/admin/openai/accounts/${id}/reset-quota`,
    undefined,
    { timeout: 90_000 }
  )
  return data
}

export interface SparkShadowCreatePayload {
  name?: string
  priority?: number
  concurrency?: number
  group_ids?: number[]
}

export async function createSparkShadow(parentId: number, payload: SparkShadowCreatePayload): Promise<Account> {
  const { data } = await apiClient.post<Account>(`/admin/accounts/${parentId}/shadow`, payload)
  return data
}

export async function getUpstreamBillingProbeSettings(): Promise<UpstreamBillingProbeSettings> {
  const { data } = await apiClient.get<UpstreamBillingProbeSettings>('/admin/accounts/upstream-billing-probe/settings')
  return data
}

export async function updateUpstreamBillingProbeSettings(
  settings: UpstreamBillingProbeSettings
): Promise<UpstreamBillingProbeSettings> {
  const { data } = await apiClient.put<UpstreamBillingProbeSettings>(
    '/admin/accounts/upstream-billing-probe/settings',
    settings
  )
  return data
}

export async function setUpstreamBillingProbeEnabled(id: number, enabled: boolean): Promise<void> {
  await apiClient.put(`/admin/accounts/${id}/upstream-billing-probe`, { enabled })
}

export async function probeUpstreamBilling(id: number): Promise<UpstreamBillingProbeResult> {
  const { data } = await apiClient.post<UpstreamBillingProbeResult>(`/admin/accounts/${id}/upstream-billing-probe`)
  return data
}

export async function probeUpstreamBillingBatch(accountIds: number[]): Promise<UpstreamBillingProbeResult[]> {
  const { data } = await apiClient.post<{ results: UpstreamBillingProbeResult[] }>(
    '/admin/accounts/upstream-billing-probe/batch',
    { account_ids: accountIds }
  )
  return data.results
}

export async function getOllamaCloudUsageSettings(): Promise<OllamaCloudUsageSettings> {
  const { data } = await apiClient.get<OllamaCloudUsageSettings>('/admin/accounts/ollama-cloud-usage/settings')
  return data
}

export async function updateOllamaCloudUsageSettings(
  settings: OllamaCloudUsageSettings
): Promise<OllamaCloudUsageSettings> {
  const { data } = await apiClient.put<OllamaCloudUsageSettings>(
    '/admin/accounts/ollama-cloud-usage/settings',
    settings
  )
  return data
}

export async function getOllamaCloudUsage(id: number): Promise<OllamaCloudUsageState> {
  const { data } = await apiClient.get<OllamaCloudUsageState>(`/admin/accounts/${id}/ollama-cloud-usage`)
  return data
}

export async function saveOllamaCloudUsageSession(id: number, session: string): Promise<OllamaCloudUsageState> {
  const { data } = await apiClient.put<OllamaCloudUsageState>(`/admin/accounts/${id}/ollama-cloud-usage/session`, {
    session
  })
  return data
}

export async function deleteOllamaCloudUsageSession(id: number): Promise<OllamaCloudUsageState> {
  const { data } = await apiClient.delete<OllamaCloudUsageState>(`/admin/accounts/${id}/ollama-cloud-usage/session`)
  return data
}

export async function setOllamaCloudUsageAutoRefresh(id: number, enabled: boolean): Promise<OllamaCloudUsageState> {
  const { data } = await apiClient.put<OllamaCloudUsageState>(`/admin/accounts/${id}/ollama-cloud-usage/auto-refresh`, {
    enabled
  })
  return data
}

export async function refreshOllamaCloudUsage(id: number): Promise<OllamaCloudUsageState> {
  const { data } = await apiClient.post<OllamaCloudUsageState>(`/admin/accounts/${id}/ollama-cloud-usage/refresh`)
  return data
}

export async function getOpenCodeGoUsageSettings(): Promise<OpenCodeGoUsageSettings> {
  const { data } = await apiClient.get<OpenCodeGoUsageSettings>('/admin/accounts/opencode-go-usage/settings')
  return data
}

export async function updateOpenCodeGoUsageSettings(
  settings: OpenCodeGoUsageSettings
): Promise<OpenCodeGoUsageSettings> {
  const { data } = await apiClient.put<OpenCodeGoUsageSettings>(
    '/admin/accounts/opencode-go-usage/settings',
    settings
  )
  return data
}

export async function getOpenCodeGoUsage(id: number): Promise<OpenCodeGoUsageState> {
  const { data } = await apiClient.get<OpenCodeGoUsageState>(`/admin/accounts/${id}/opencode-go-usage`)
  return data
}

export async function setOpenCodeGoUsageAutoRefresh(id: number, enabled: boolean): Promise<OpenCodeGoUsageState> {
  const { data } = await apiClient.put<OpenCodeGoUsageState>(`/admin/accounts/${id}/opencode-go-usage/auto-refresh`, {
    enabled
  })
  return data
}

export async function refreshOpenCodeGoUsage(id: number): Promise<OpenCodeGoUsageState> {
  const { data } = await apiClient.post<OpenCodeGoUsageState>(`/admin/accounts/${id}/opencode-go-usage/refresh`)
  return data
}

export const accountsAPI = {
  list,
  listWithEtag,
  getUpstreamBillingRatesWithEtag,
  getById,
  create,
  duplicate,
  update,
  getGrokMediaEligibility,
  updateGrokMediaEligibility,
  checkMixedChannelRisk,
  delete: deleteAccount,
  toggleStatus,
  testAccount,
  refreshCredentials,
  applyOAuthCredentials,
  getStats,
  clearError,
  getUsage,
  getBatchUsage,
  getTodayStats,
  getBatchTodayStats,
  clearRateLimit,
  recoverState,
  resetAccountQuota,
  getTempUnschedulableStatus,
  resetTempUnschedulable,
  setSchedulable,
  getAvailableModels,
  syncUpstreamModels,
  syncUpstreamModelsPreview,
  generateAuthUrl,
  exchangeCode,
  refreshOpenAIToken,
  batchCreate,
  batchUpdateCredentials,
  bulkUpdate,
  previewFromCrs,
  syncFromCrs,
  exportData,
  importData,
  importCodexSession,
  createOpenAICodexPAT,
  getAntigravityDefaultModelMapping,
  batchDelete,
  batchClearError,
  batchRefresh,
  setPrivacy,
  revertProxyFallback,
  refreshOpenAIQuota,
  resetOpenAIQuota,
  createSparkShadow,
  getUpstreamBillingProbeSettings,
  updateUpstreamBillingProbeSettings,
  setUpstreamBillingProbeEnabled,
  probeUpstreamBilling,
  probeUpstreamBillingBatch,
  getOllamaCloudUsageSettings,
  updateOllamaCloudUsageSettings,
  getOllamaCloudUsage,
  saveOllamaCloudUsageSession,
  deleteOllamaCloudUsageSession,
  setOllamaCloudUsageAutoRefresh,
  refreshOllamaCloudUsage,
  getOpenCodeGoUsageSettings,
  updateOpenCodeGoUsageSettings,
  getOpenCodeGoUsage,
  setOpenCodeGoUsageAutoRefresh,
  refreshOpenCodeGoUsage
  ,getOpenAIEvalModels
  ,getOpenAIEvalConfig
  ,saveOpenAIEvalConfig
  ,runOpenAIEval
  ,listOpenAIEvalRuns
  ,refreshOpenAIEvalQuality
  ,evaluateOpenAIEvalRanking
  ,getOpenAIEvalRankings
  ,getOpenAIEvalRankingAccounts
  ,getOpenAIEvalAccountOverview
  ,listOpenAIEvalAudit
  ,resetOpenAIBPSState
}

export default accountsAPI
