// Pure helpers shared by the two Model Integrity pages (降智测试 / 调度策略).
// They only describe what the backend already enforces; keep numbers here in
// sync with backend/internal/service/openai_eval*.go instead of inventing them.
import type {
  OpenAIEvalBPSAccountConfig,
  OpenAIEvalBPSMode,
  OpenAIEvalConfig,
  OpenAIEvalModelCatalog,
  OpenAIEvalPolicyWeights,
  OpenAIEvalRouteConfig,
  OpenAIEvalRun,
  OpenAIEvalSchedule,
  OpenAIEvalSchedulingPolicy
} from '@/api/admin/accounts'

export type EvalTestType = 'candy' | 'fingerprint' | 'modeltrace' | 'state_probe'

export const TEST_TYPES: EvalTestType[] = ['candy', 'fingerprint', 'modeltrace', 'state_probe']

export const HOUR = 3600
export const DAY = 24 * HOUR
export const CANDY_MIN_SAMPLES = 1
export const CANDY_MAX_SAMPLES = 10
export const CUSTOM_INTERVAL = 'custom'
/** PostgreSQL INTEGER storage limit for openai_eval_schedule_state.interval_seconds. */
export const MAX_INTERVAL_SECONDS = 2_147_483_647
/** Custom interval inputs use whole minutes, so round the storage limit down. */
export const MAX_INTERVAL_MINUTES = Math.floor(MAX_INTERVAL_SECONDS / 60)
export const DEFAULT_CUSTOM_BALANCE: OpenAIEvalPolicyWeights = {
  cost: 0.2,
  stability: 0.3,
  error_rate: 0.25,
  ttft: 0.15,
  load: 0.1
}
export const CUSTOM_FACTORS = ['cost', 'stability', 'error_rate', 'ttft', 'load'] as const
export type CustomFactor = typeof CUSTOM_FACTORS[number]

interface TestTypeMeta {
  scheduleKey: 'candy_schedule' | 'fingerprint_schedule' | 'modeltrace_schedule' | 'state_probe_schedule'
  minInterval: number
  intervals: number[]
}

export const TEST_TYPE_META: Record<EvalTestType, TestTypeMeta> = {
  candy: { scheduleKey: 'candy_schedule', minInterval: 5 * 60, intervals: [5 * 60, 10 * 60, 30 * 60, HOUR, 6 * HOUR, 12 * HOUR, DAY] },
  fingerprint: { scheduleKey: 'fingerprint_schedule', minInterval: 5 * 60, intervals: [5 * 60, 10 * 60, 30 * 60, HOUR, 6 * HOUR, 12 * HOUR, DAY] },
  modeltrace: { scheduleKey: 'modeltrace_schedule', minInterval: 5 * 60, intervals: [5 * 60, 10 * 60, 30 * 60, HOUR, 6 * HOUR, 12 * HOUR, DAY] },
  state_probe: { scheduleKey: 'state_probe_schedule', minInterval: 5 * 60, intervals: [5 * 60, 10 * 60, 30 * 60, HOUR, 6 * HOUR, 12 * HOUR, DAY] }
}

/** Candy uses the configured sample count; one is the server default. */
export const CANDY_REQUESTS = 1
/** State Probe mints a ticket and continues it once. */
export const STATE_PROBE_REQUESTS = 2
const DEFAULT_FINGERPRINT_SAMPLES: Record<string, number> = { quick: 60, standard: 200, strict: 400 }

export function scheduleOf(route: OpenAIEvalRouteConfig, type: EvalTestType): OpenAIEvalSchedule {
  return route[TEST_TYPE_META[type].scheduleKey]
}

export function emptySchedule(type: EvalTestType): OpenAIEvalSchedule {
  return {
    enabled: false,
    interval_seconds: TEST_TYPE_META[type].minInterval,
    jitter_seconds: 0,
    ...(type === 'fingerprint' ? { sample_mode: 'quick' } : {})
  }
}

export function normalizeSchedule(schedule: OpenAIEvalSchedule, type: EvalTestType): OpenAIEvalSchedule {
  const minimum = TEST_TYPE_META[type].minInterval
  const rawInterval = Number(schedule.interval_seconds)
  const interval = Number.isFinite(rawInterval) ? Math.trunc(rawInterval) : minimum
  // Presets stop at 24 hours. Custom intervals have no product ceiling, but
  // must fit the PostgreSQL INTEGER column used by schedule state.
  schedule.interval_seconds = Math.min(Math.max(interval, minimum), MAX_INTERVAL_SECONDS)
  const rawJitter = Number(schedule.jitter_seconds)
  const jitter = Number.isFinite(rawJitter) ? Math.trunc(rawJitter) : 0
  schedule.jitter_seconds = Math.min(Math.max(jitter, 0), maxScheduleJitterSeconds(schedule.interval_seconds))
  if (type === 'candy') {
    const rawSamples = Number(schedule.sample_count)
    const samples = Number.isFinite(rawSamples) ? Math.trunc(rawSamples) : CANDY_MIN_SAMPLES
    schedule.sample_count = Math.min(Math.max(samples, CANDY_MIN_SAMPLES), CANDY_MAX_SAMPLES)
  }
  if (type === 'fingerprint' && !schedule.sample_mode) schedule.sample_mode = 'quick'
  return schedule
}

export function maxScheduleJitterSeconds(intervalSeconds: number): number {
  if (!Number.isFinite(intervalSeconds) || intervalSeconds <= 0) return 0
  const interval = Math.min(Math.floor(intervalSeconds), MAX_INTERVAL_SECONDS)
  const storageHeadroom = MAX_INTERVAL_SECONDS - interval
  return Math.min(Math.floor(interval / 2), HOUR, storageHeadroom)
}

export function bpsModeOf(route: Pick<OpenAIEvalRouteConfig, 'bps_mode' | 'bps_auto'>): OpenAIEvalBPSMode {
  if (route.bps_mode === 'auto' || route.bps_mode === 'force_on' || route.bps_mode === 'force_off') return route.bps_mode
  return route.bps_auto ? 'auto' : 'force_off'
}

/** BPS and State Probe only exist for direct OpenAI OAuth accounts on the default effort. */
export function isDirectOAuthRoute(route: OpenAIEvalRouteConfig): boolean {
  return route.reasoning_effort === '' && route.direct_oauth_eligible === true
}

export function normalizeRoute(route: OpenAIEvalRouteConfig): OpenAIEvalRouteConfig {
  route.candy_schedule ||= emptySchedule('candy')
  route.fingerprint_schedule ||= emptySchedule('fingerprint')
  route.modeltrace_schedule ||= emptySchedule('modeltrace')
  route.state_probe_schedule ||= emptySchedule('state_probe')
  route.reasoning_effort = route.reasoning_effort ?? ''
  route.bps_mode = bpsModeOf(route)
  route.bps_auto = route.bps_mode !== 'force_off'
  for (const type of TEST_TYPES) normalizeSchedule(scheduleOf(route, type), type)
  return route
}

export function newRoute(accountID: number, model: string, effort: string): OpenAIEvalRouteConfig {
  return normalizeRoute({
    account_id: accountID,
    requested_model: model,
    reasoning_effort: effort,
    candy_schedule: emptySchedule('candy'),
    fingerprint_schedule: emptySchedule('fingerprint'),
    modeltrace_schedule: emptySchedule('modeltrace'),
    state_probe_schedule: emptySchedule('state_probe'),
    bps_auto: false,
    bps_mode: 'force_off'
  })
}

export function routeKey(route: Pick<OpenAIEvalRouteConfig, 'account_id' | 'requested_model' | 'reasoning_effort'>): string {
  return `${route.account_id}:${route.requested_model.toLowerCase()}:${(route.reasoning_effort || '').toLowerCase()}`
}

/**
 * Serialises the editable part of the config. Read-only runtime fields
 * (bps_state, direct_oauth_eligible) never go back to the server, while every
 * field the page does not edit (effects_enabled, revision, policies) is kept
 * so saving one page cannot erase the other page's settings.
 */
export function toSavePayload(config: OpenAIEvalConfig): OpenAIEvalConfig {
  return {
    revision: config.revision,
    effects_enabled: config.effects_enabled,
    bps_auto_enabled: config.bps_auto_enabled,
    scheduling_policy: config.scheduling_policy ?? '',
    custom_balance: normalizeCustomBalance(config.custom_balance),
    policies: (config.policies ?? []).map(rule => ({
      ...rule,
      reasoning_effort: rule.reasoning_effort || '',
      ...(rule.policy === 'custom_balance'
        ? { custom_balance: normalizeCustomBalance(rule.custom_balance ?? config.custom_balance) }
        : {})
    })),
    bps_accounts: (config.bps_accounts ?? []).map(bpsAccountPayload),
    accounts: config.accounts.map(route => {
      const mode = bpsModeOf(route)
      // eslint-disable-next-line @typescript-eslint/no-unused-vars
      const { bps_state, direct_oauth_eligible, ...rest } = route
      return { ...rest, bps_mode: mode, bps_auto: mode !== 'force_off' }
    })
  }
}

// ---------------------------------------------------------------------------
// BPS accounts (account-scoped, independent of test targets)
// ---------------------------------------------------------------------------

export const BPS_MODES: OpenAIEvalBPSMode[] = ['auto', 'force_on', 'force_off']
export const BPS_THRESHOLD_MIN = 1
export const BPS_THRESHOLD_MAX = 10
export const BPS_DEFAULT_FAILURE_THRESHOLD = 3
export const BPS_DEFAULT_RECOVERY_THRESHOLD = 2
/** Same lower bound as the State Probe schedule (OpenAIEvalMinStateProbeInterval). */
export const BPS_INTERVALS = [5 * 60, 10 * 60, 30 * 60, HOUR, 6 * HOUR, 12 * HOUR, DAY]

function clampInt(value: unknown, min: number, max: number, fallback: number): number {
  const raw = Number(value)
  if (!Number.isFinite(raw)) return fallback
  return Math.min(Math.max(Math.trunc(raw), min), max)
}

export function newBPSAccount(accountID: number, probeModel = ''): OpenAIEvalBPSAccountConfig {
  return {
    account_id: accountID,
    probe_model: probeModel,
    mode: 'auto',
    failure_threshold: BPS_DEFAULT_FAILURE_THRESHOLD,
    recovery_threshold: BPS_DEFAULT_RECOVERY_THRESHOLD,
    interval_seconds: BPS_INTERVALS[0]
  }
}

export function normalizeBPSAccount(item: OpenAIEvalBPSAccountConfig): OpenAIEvalBPSAccountConfig {
  item.mode = BPS_MODES.includes(item.mode) ? item.mode : 'force_off'
  item.probe_model = (item.probe_model ?? '').trim()
  item.failure_threshold = clampInt(item.failure_threshold, BPS_THRESHOLD_MIN, BPS_THRESHOLD_MAX, BPS_DEFAULT_FAILURE_THRESHOLD)
  item.recovery_threshold = clampInt(item.recovery_threshold, BPS_THRESHOLD_MIN, BPS_THRESHOLD_MAX, BPS_DEFAULT_RECOVERY_THRESHOLD)
  item.interval_seconds = clampInt(item.interval_seconds, BPS_INTERVALS[0], MAX_INTERVAL_SECONDS, BPS_INTERVALS[0])
  item.degraded_streak = Number(item.degraded_streak) || 0
  item.healthy_streak = Number(item.healthy_streak) || 0
  return item
}

/** Only the editable fields; runtime state is owned by the server. */
export function bpsAccountPayload(item: OpenAIEvalBPSAccountConfig): OpenAIEvalBPSAccountConfig {
  return {
    account_id: item.account_id,
    probe_model: item.probe_model ?? '',
    mode: item.mode,
    failure_threshold: item.failure_threshold,
    recovery_threshold: item.recovery_threshold,
    interval_seconds: item.interval_seconds
  }
}

export type BPSLane = 'bps' | 'native' | 'locked' | 'inactive'

/** Which route the account uses right now, from the admin's point of view. */
export function bpsAccountLane(item: OpenAIEvalBPSAccountConfig, autoEnabled: boolean): BPSLane {
  if (item.disabled_reason?.trim() || item.state === 'locked') return 'locked'
  if (item.mode === 'force_off') return 'inactive'
  if (item.mode === 'force_on') return 'bps'
  if (!autoEnabled) return 'inactive'
  return item.active || item.state === 'bps' ? 'bps' : 'native'
}

export function canResetBPSAccount(item: OpenAIEvalBPSAccountConfig): boolean {
  return Boolean(item.active || item.disabled_reason || item.state === 'bps' || item.state === 'locked' || item.degraded_streak || item.healthy_streak)
}

/** Older backends emitted an all-zero object even though it cannot be saved. */
export function normalizeCustomBalance(weights?: OpenAIEvalPolicyWeights): OpenAIEvalPolicyWeights {
  if (!weights) return { ...DEFAULT_CUSTOM_BALANCE }
  const values = [weights.cost, weights.stability, weights.error_rate, weights.ttft, weights.load]
  if (values.some(value => !Number.isFinite(value) || value < 0) || values.reduce((sum, value) => sum + value, 0) <= 0) {
    return { ...DEFAULT_CUSTOM_BALANCE }
  }
  return { ...weights }
}

/** False when the server would reject the weights: any negative/non-numeric value or an all-zero total. */
export function isValidCustomBalance(weights?: OpenAIEvalPolicyWeights): boolean {
  if (!weights) return false
  const values = CUSTOM_FACTORS.map(factor => Number(weights[factor]))
  return values.every(value => Number.isFinite(value) && value >= 0) && values.reduce((sum, value) => sum + value, 0) > 0
}

/** Whole-percent share of each factor after the normalization the server applies on save. */
export function customBalanceShares(weights?: OpenAIEvalPolicyWeights): Record<CustomFactor, number> {
  const normalized = normalizeCustomBalance(weights)
  const total = CUSTOM_FACTORS.reduce((sum, factor) => sum + normalized[factor], 0)
  return Object.fromEntries(CUSTOM_FACTORS.map(factor => [factor, Math.round((normalized[factor] / total) * 100)])) as Record<CustomFactor, number>
}

export function requestsPerRun(route: OpenAIEvalRouteConfig, type: EvalTestType, catalog?: OpenAIEvalModelCatalog | null, sampleMode?: string): number {
  switch (type) {
    case 'candy':
      return Math.max(1, Math.trunc(Number(route.candy_schedule.sample_count) || CANDY_REQUESTS))
    case 'state_probe':
      return STATE_PROBE_REQUESTS
    case 'modeltrace':
      return catalog?.modeltrace?.requests || 3
    case 'fingerprint': {
      const mode = sampleMode || route.fingerprint_schedule.sample_mode || 'quick'
      return catalog?.fingerprint_modes?.find(item => item.id === mode)?.samples ?? DEFAULT_FINGERPRINT_SAMPLES[mode] ?? 60
    }
  }
}

/** Average upstream requests per day for one automatic schedule (0 when off). */
export function dailyRequests(route: OpenAIEvalRouteConfig, type: EvalTestType, catalog?: OpenAIEvalModelCatalog | null): number {
  const schedule = scheduleOf(route, type)
  if (!schedule?.enabled || !(schedule.interval_seconds > 0)) return 0
  if (type === 'state_probe' && !isDirectOAuthRoute(route)) return 0
  return (requestsPerRun(route, type, catalog) * DAY) / schedule.interval_seconds
}

export function totalDailyRequests(routes: OpenAIEvalRouteConfig[], catalog?: OpenAIEvalModelCatalog | null): number {
  return routes.reduce((sum, route) => sum + TEST_TYPES.reduce((inner, type) => inner + dailyRequests(route, type, catalog), 0), 0)
}

export function activeScheduleCount(routes: OpenAIEvalRouteConfig[]): number {
  return routes.reduce((sum, route) => sum + TEST_TYPES.filter(type => scheduleOf(route, type)?.enabled && (type !== 'state_probe' || isDirectOAuthRoute(route))).length, 0)
}

/**
 * Result tone: 'pass'/'consistent' are verified good; 'suspected_normal' is an
 * inferred non-Luna attribution and gets its own 'likely' tone so it is never
 * mistaken for a verified pass or for degradation. Only 'warning'/'different'
 * deserve attention; insufficient/error/unresolved stay neutral.
 */
export type ResultTone = 'ok' | 'likely' | 'attention' | 'neutral' | 'running'

export function resultTone(status: string | undefined): ResultTone {
  if (status === 'pass' || status === 'consistent' || status === 'healthy') return 'ok'
  if (status === 'suspected_normal') return 'likely'
  if (status === 'warning' || status === 'different' || status === 'degraded') return 'attention'
  if (status === 'running') return 'running'
  return 'neutral'
}

/** Backend reasons for ModelTrace / fingerprint attributions (openAIEvalAttributionVerdict). */
export const LUNA_ATTRIBUTION_REASON = 'suspected_luna_attribution'
export const NON_LUNA_ATTRIBUTION_REASON = 'non_luna_behavioral_attribution'

/**
 * Status key used for the visible label. A Luna attribution is stored as a
 * generic 'warning'; label it "possible Luna" rather than the Candy-style
 * "abnormal" so it never reads as confirmed degradation.
 */
export function runStatusKey(run: Pick<OpenAIEvalRun, 'status' | 'outcome'>): string {
  if (run.status === 'warning' && run.outcome?.reason === LUNA_ATTRIBUTION_REASON) return 'suspected_luna'
  return run.status
}

/** True when the run's verdict is a behavioural attribution, so the UI must say it is inferred. */
export function isAttributionRun(run: Pick<OpenAIEvalRun, 'status' | 'outcome' | 'test_type'>): boolean {
  if (run.test_type !== 'modeltrace' && run.test_type !== 'fingerprint') return false
  return run.status === 'suspected_normal' || run.outcome?.reason === LUNA_ATTRIBUTION_REASON || run.outcome?.reason === NON_LUNA_ATTRIBUTION_REASON
}

export function latestRunFor(runs: OpenAIEvalRun[], route: OpenAIEvalRouteConfig, type: EvalTestType): OpenAIEvalRun | undefined {
  const model = route.requested_model.toLowerCase()
  const effort = type === 'state_probe' ? '' : (route.reasoning_effort || '').toLowerCase()
  return runs.find(run => run.account_id === route.account_id && run.test_type === type && run.requested_model.toLowerCase() === model && (run.reasoning_effort || '').toLowerCase() === effort)
}

// ---------------------------------------------------------------------------
// Scheduling policy
// ---------------------------------------------------------------------------

export const POLICIES: OpenAIEvalSchedulingPolicy[] = ['', 'cost_first', 'stability_first', 'avoid_degradation', 'custom_balance']

export type PolicyFactor = 'price' | 'errors' | 'speed'

/**
 * Relative emphasis (0–4) each policy gives price, error rate and first-token
 * latency. Mirrors the weight overrides in openai_account_scheduler.go:
 * default weights are error 0.8 / ttft 0.5 / upstream cost 0 (admin-tunable);
 * cost_first lifts cost to ≥2 and scales error/ttft by 0.65; stability_first
 * lifts error ≥2.5, ttft ≥1.5; avoid_degradation lifts error ≥2.5, ttft ≥1.25
 * and scales cost by 0.25. Load and priority weights are untouched.
 */
export const POLICY_EMPHASIS: Record<string, Record<PolicyFactor, number>> = {
  '': { price: 1, errors: 2, speed: 2 },
  cost_first: { price: 4, errors: 1, speed: 1 },
  stability_first: { price: 1, errors: 4, speed: 4 },
  avoid_degradation: { price: 0, errors: 4, speed: 3 },
  custom_balance: { price: 2, errors: 2, speed: 2 }
}

export function policyKey(policy: string | undefined): string {
  return policy ? policy : 'legacy'
}

// ---------------------------------------------------------------------------
// Scheduler decision explanations
// ---------------------------------------------------------------------------

const KNOWN_EXCLUSIONS = new Set([
  'excluded', 'not_schedulable', 'platform_mismatch', 'runtime_blocked', 'privacy_not_set', 'transport_incompatible',
  'model_not_supported', 'account_model_not_owned', 'capability_mismatch', 'channel_upstream_restricted',
  'exec_capability_cooldown', 'shadow_parent_unhealthy', 'evaluation_hard_failure', 'proxy_stream_quarantined',
  'compact_support_unknown', 'concurrency_full', 'conn_queue_full', 'model_rate_limited', 'upstream_rate_limited',
  'account_nil'
])

/** Maps a backend exclusion code to an i18n key suffix under modelIntegrity.exclusion. */
export function exclusionKey(code: string | undefined): string {
  if (!code) return 'unknown'
  if (KNOWN_EXCLUSIONS.has(code)) return code
  if (code.startsWith('quota_auto_pause')) return 'quota_auto_pause'
  if (code.startsWith('grok_')) return 'platform_quota'
  return 'unknown'
}

const KNOWN_DECISIONS = new Set([
  'load_balance_selection', 'session_sticky', 'previous_response_sticky', 'guardian_parent_sticky', 'sticky_escape',
  'rate_ladder_same_rate', 'rate_ladder_upgrade', 'rate_ladder_lower_rate_fallback', 'no_selection', 'selection_error'
])

export function decisionKey(code: string | undefined): string {
  return code && KNOWN_DECISIONS.has(code) ? code : 'unknown'
}

const KNOWN_CANDIDATE_REASONS = new Set(['score_top_k_candidate', 'ranked_below_top_k', 'same_rate_candidate', 'higher_rate_candidate', 'below_migration_rate'])

export function candidateReasonKey(code: string | undefined): string | null {
  if (!code) return null
  return KNOWN_CANDIDATE_REASONS.has(code) ? code : KNOWN_DECISIONS.has(code) ? `decision.${code}` : null
}

const BPS_DISABLED_REASONS = new Set(['upstream_403'])

export function bpsDisabledKey(reason: string | undefined): string {
  if (!reason) return ''
  return BPS_DISABLED_REASONS.has(reason.trim().toLowerCase()) ? reason.trim().toLowerCase() : 'other'
}

/** Sort candidates: chosen first, then everyone who could serve by score, then excluded ones. */
export function orderCandidates<T extends { selected: boolean; eligible: boolean; score?: number; account_id: number }>(candidates: T[]): T[] {
  return [...candidates].sort((a, b) => {
    if (a.selected !== b.selected) return a.selected ? -1 : 1
    if (a.eligible !== b.eligible) return a.eligible ? -1 : 1
    const score = (b.score ?? -Infinity) - (a.score ?? -Infinity)
    if (score !== 0 && Number.isFinite(score)) return score
    return a.account_id - b.account_id
  })
}
