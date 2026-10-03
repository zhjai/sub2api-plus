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
  OpenAIEvalSampleRecord,
  OpenAIEvalSchedule,
  OpenAIEvalSchedulingPolicy
} from '@/api/admin/accounts'
import type { Account } from '@/types'

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
/**
 * The previous default (cost 0.2, stability 0.3, error 0.25, ttft 0.15,
 * load 0.1) with stability folded in, so the ranking is unchanged. Quality
 * stays 0 until an admin opts in.
 */
export const DEFAULT_CUSTOM_BALANCE: OpenAIEvalPolicyWeights = {
  cost: 0.2,
  error_rate: 0.43,
  ttft: 0.27,
  load: 0.1,
  quality: 0
}
/** Editable Custom Balance factors. Legacy `stability` is folded, never edited. */
export const CUSTOM_FACTORS = ['cost', 'error_rate', 'ttft', 'load', 'quality'] as const
export type CustomFactor = typeof CUSTOM_FACTORS[number]

/** The scheduler reads legacy stability as 60% error rate and 40% first-token latency. */
export const LEGACY_STABILITY_ERROR_SHARE = 0.6
export const LEGACY_STABILITY_TTFT_SHARE = 0.4

// ---------------------------------------------------------------------------
// Integrity ranking refresh cadence (调度评估间隔)
// ---------------------------------------------------------------------------

export const DEFAULT_QUALITY_REFRESH_SECONDS = HOUR
export const MIN_QUALITY_REFRESH_SECONDS = 5 * 60
export const QUALITY_REFRESH_INTERVALS = [5 * 60, 10 * 60, 30 * 60, HOUR, 6 * HOUR, 12 * HOUR, DAY]

/** Older servers omit the field (3600). Whole minutes, at least 5, within INTEGER storage. */
export function normalizeQualityRefreshInterval(value: unknown): number {
  if (value === undefined || value === null || value === '') return DEFAULT_QUALITY_REFRESH_SECONDS
  const raw = Number(value)
  if (!Number.isFinite(raw)) return DEFAULT_QUALITY_REFRESH_SECONDS
  const minutes = Math.min(Math.max(Math.round(raw / 60), MIN_QUALITY_REFRESH_SECONDS / 60), MAX_INTERVAL_MINUTES)
  return minutes * 60
}

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

/**
 * Upstream attempts per logical sample, including the first request. Shared by
 * Candy, Fingerprint and ModelTrace; State Probe never retries because its two
 * requests are linked by one ticket.
 */
export const DEFAULT_MAX_REQUEST_ATTEMPTS = 3
export const MIN_MAX_REQUEST_ATTEMPTS = 1
export const MAX_MAX_REQUEST_ATTEMPTS = 10

/** Older servers omit the field; anything out of range is clamped, not rejected. */
export function normalizeMaxRequestAttempts(value: unknown): number {
  if (value === undefined || value === null || value === '') return DEFAULT_MAX_REQUEST_ATTEMPTS
  const raw = Number(value)
  if (!Number.isFinite(raw)) return DEFAULT_MAX_REQUEST_ATTEMPTS
  return Math.min(Math.max(Math.trunc(raw), MIN_MAX_REQUEST_ATTEMPTS), MAX_MAX_REQUEST_ATTEMPTS)
}

export function retriesSamples(type: EvalTestType): boolean {
  return type !== 'state_probe'
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

/**
 * BPS and State Probe only exist for direct OpenAI OAuth accounts. Eligibility
 * is an account capability; State Probe always runs on the account's default
 * effort, whatever effort the target itself tests.
 */
export function isDirectOAuthRoute(route: OpenAIEvalRouteConfig): boolean {
  return route.direct_oauth_eligible === true
}

/** OpenAIAuthModeAgentIdentity ("agentIdentity"), compared case-insensitively like the server. */
const AGENT_IDENTITY_AUTH_MODE = 'agentidentity'

export type DirectOAuthAccountFields = Pick<Account, 'platform' | 'type' | 'parent_account_id' | 'extra' | 'credentials'>

/**
 * Mirrors the server's direct OAuth eligibility (OpenAIEvalService.GetConfig:
 * OpenAI OAuth, not a shadow, not a synthetic UI test account, not an agent
 * identity) for a target added before the next reload. It reads only the
 * redacted account-list fields; an unknown account is never assumed eligible.
 */
export function isDirectOAuthAccount(account: Partial<DirectOAuthAccountFields> | null | undefined): boolean {
  if (!account || account.platform !== 'openai' || account.type !== 'oauth') return false
  if (account.parent_account_id != null) return false
  if (account.extra?.synthetic_ui_test === true) return false
  const authMode = account.credentials?.auth_mode
  return !(typeof authMode === 'string' && authMode.trim().toLowerCase() === AGENT_IDENTITY_AUTH_MODE)
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
    max_request_attempts: normalizeMaxRequestAttempts(config.max_request_attempts),
    quality_refresh_interval_seconds: normalizeQualityRefreshInterval(config.quality_refresh_interval_seconds),
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

const roundWeight = (value: number) => Math.round(value * 1e6) / 1e6

/**
 * Moves a legacy `stability` weight into error_rate (60 %) and ttft (40 %),
 * the split the scheduler already applies, so saved rankings do not change.
 * Missing quality reads as 0. Running it again is a no-op (stability is 0).
 */
export function foldLegacyStability(weights: OpenAIEvalPolicyWeights): OpenAIEvalPolicyWeights {
  const stability = Number(weights.stability) || 0
  const quality = Number(weights.quality ?? 0)
  return {
    cost: Number(weights.cost),
    error_rate: stability > 0 ? roundWeight(Number(weights.error_rate) + stability * LEGACY_STABILITY_ERROR_SHARE) : Number(weights.error_rate),
    ttft: stability > 0 ? roundWeight(Number(weights.ttft) + stability * LEGACY_STABILITY_TTFT_SHARE) : Number(weights.ttft),
    load: Number(weights.load),
    quality: Number.isFinite(quality) ? quality : 0,
    stability: 0
  }
}

/** Older backends emitted an all-zero object even though it cannot be saved. */
export function normalizeCustomBalance(weights?: OpenAIEvalPolicyWeights): OpenAIEvalPolicyWeights {
  if (!weights) return { ...DEFAULT_CUSTOM_BALANCE, stability: 0 }
  const legacy = [weights.cost, weights.stability ?? 0, weights.error_rate, weights.ttft, weights.load, weights.quality ?? 0].map(Number)
  if (legacy.some(value => !Number.isFinite(value) || value < 0) || legacy.reduce((sum, value) => sum + value, 0) <= 0) {
    return { ...DEFAULT_CUSTOM_BALANCE, stability: 0 }
  }
  return foldLegacyStability(weights)
}

/** False when the server would reject the weights: any negative/non-numeric value or an all-zero total. */
export function isValidCustomBalance(weights?: OpenAIEvalPolicyWeights): boolean {
  if (!weights) return false
  // Fold first: a quality-only set is valid, and a legacy stability weight still counts.
  const folded = foldLegacyStability(weights)
  const values = CUSTOM_FACTORS.map(factor => Number(folded[factor] ?? 0))
  return values.every(value => Number.isFinite(value) && value >= 0) && values.reduce((sum, value) => sum + value, 0) > 0
}

/** Whole-percent share of each factor after the normalization the server applies on save. */
export function customBalanceShares(weights?: OpenAIEvalPolicyWeights): Record<CustomFactor, number> {
  const normalized = normalizeCustomBalance(weights)
  const weight = (factor: CustomFactor) => Number(normalized[factor] ?? 0)
  const total = CUSTOM_FACTORS.reduce((sum, factor) => sum + weight(factor), 0)
  return Object.fromEntries(CUSTOM_FACTORS.map(factor => [factor, Math.round((weight(factor) / total) * 100)])) as Record<CustomFactor, number>
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

/** Upper bound for one run when every sample uses all of its attempts. */
export function maxRequestsPerRun(route: OpenAIEvalRouteConfig, type: EvalTestType, maxAttempts: number, catalog?: OpenAIEvalModelCatalog | null, sampleMode?: string): number {
  const samples = requestsPerRun(route, type, catalog, sampleMode)
  return retriesSamples(type) ? samples * normalizeMaxRequestAttempts(maxAttempts) : samples
}

/** Daily upper bound for all automatic schedules if every sample used all attempts. */
export function totalDailyMaxRequests(routes: OpenAIEvalRouteConfig[], maxAttempts: number, catalog?: OpenAIEvalModelCatalog | null): number {
  const attempts = normalizeMaxRequestAttempts(maxAttempts)
  return routes.reduce((sum, route) => sum + TEST_TYPES.reduce((inner, type) => inner + dailyRequests(route, type, catalog) * (retriesSamples(type) ? attempts : 1), 0), 0)
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
export type ResultTone = 'ok' | 'likely' | 'attention' | 'neutral' | 'error' | 'running'

export function resultTone(status: string | undefined): ResultTone {
  if (status === 'pass' || status === 'consistent' || status === 'healthy') return 'ok'
  if (status === 'suspected_normal') return 'likely'
  if (status === 'warning' || status === 'different' || status === 'degraded') return 'attention'
  if (status === 'error') return 'error'
  if (status === 'running') return 'running'
  return 'neutral'
}

// ---------------------------------------------------------------------------
// Per-sample diagnostics
// ---------------------------------------------------------------------------

/** Codes that describe how an answer was scored, not a failed request. */
const SCORING_CODES = new Set(['correct_answer', 'single_public_item_failed', 'invalid_probe_answer'])

export type SampleState = 'correct' | 'wrong' | 'valid' | 'invalid' | 'error'

/** ModelTrace keeps its samples inside the outcome on some servers; the others on the run. */
export function runSamples(run: Pick<OpenAIEvalRun, 'samples' | 'outcome'>): OpenAIEvalSampleRecord[] {
  if (run.samples?.length) return run.samples
  return (run.outcome?.modeltrace?.samples ?? []).map((sample, index) => ({
    probe_id: `modeltrace-${index + 1}`,
    valid: sample.accepted === true,
    error_code: sample.error,
    answer: sample.answer,
    attempts: sample.attempts,
    error_message: sample.error_message,
    http_status: sample.http_status,
    attempt_errors: sample.attempt_errors
  }))
}

/** The model's own answer: raw text first, the normalised form only as a fallback. */
export function sampleAnswer(sample: OpenAIEvalSampleRecord): string {
  return sample.answer?.trim() ? sample.answer : (sample.normalized_answer ?? '')
}

/**
 * The integer the server extracted from the model's Candy reply (a wrong one
 * included). Older records may lack it: only a sample the server scored as
 * correct_answer may then show the run's own expected answer. An unknown or
 * failed reply never becomes a number here, and the reply is never re-scanned.
 */
export function candyExtractedAnswer(sample: OpenAIEvalSampleRecord, expected?: number | null): string {
  const stored = sample.normalized_answer?.trim()
  if (stored) return stored
  return sample.error_code === 'correct_answer' && expected != null ? String(expected) : ''
}

/** The complete model reply for Candy, before any extraction. */
export function candyFullReply(sample: OpenAIEvalSampleRecord): string {
  return sample.answer?.trim() || ''
}

export function sampleFailed(sample: OpenAIEvalSampleRecord): boolean {
  // Completed Responses requests carry HTTP 200. A status field alone is
  // not a failure; only an actual error status is transport evidence.
  if (sample.error_message || (sample.http_status != null && sample.http_status >= 400)) return true
  return Boolean(sample.error_code) && !SCORING_CODES.has(sample.error_code!) && !sampleAnswer(sample)
}

export function sampleState(sample: OpenAIEvalSampleRecord, type: EvalTestType): SampleState {
  if (sampleFailed(sample)) return 'error'
  if (type === 'candy') {
    // `valid` means that a response was successfully collected, not that the
    // Candy answer was correct. The backend records the scoring reason on the
    // sample so a valid but wrong answer remains visibly wrong.
    if (sample.error_code === 'single_public_item_failed') return 'wrong'
    if (sample.error_code === 'correct_answer') return 'correct'
    return sample.valid ? 'correct' : 'wrong'
  }
  return sample.valid ? 'valid' : 'invalid'
}

/** True when the server stored nothing beyond the verdict (records from before diagnostics existed). */
export function sampleLacksDetail(sample: OpenAIEvalSampleRecord): boolean {
  return !sampleAnswer(sample) && !sample.error_message && sample.http_status == null && sample.attempts == null && !sample.attempt_errors?.length
}

/** First failed sample of a run, used to say why a run produced no verdict. */
export function firstSampleFailure(run: Pick<OpenAIEvalRun, 'samples' | 'outcome'>): OpenAIEvalSampleRecord | undefined {
  return runSamples(run).find(sampleFailed)
}

export function failedSampleCount(run: Pick<OpenAIEvalRun, 'samples' | 'outcome'>): number {
  return runSamples(run).filter(sampleFailed).length
}

/**
 * The server sanitises upstream messages already; this is a second guard so
 * a key or bearer token can never reach the page even from an older server.
 */
export function redactSecrets(text: string): string {
  return text
    .replace(/\bBearer\s+[A-Za-z0-9._~+/=-]+/gi, 'Bearer [redacted]')
    .replace(/\b(sk|rk|sess)-[A-Za-z0-9_-]{8,}/g, '$1-[redacted]')
    .replace(/\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]+/g, '[redacted-jwt]')
}

/**
 * Expected Candy answer for this run, taken from the run's own data version
 * ("…candy-21-…"), because the question changed over time. The catalog
 * number only applies when the catalog names the same data version. A run
 * without a recognisable version gets no expected answer rather than a
 * guess from today's question.
 */
export function candyExpectedAnswer(run: Pick<OpenAIEvalRun, 'data_version'>, catalog?: OpenAIEvalModelCatalog | null): number | null {
  const version = run.data_version ?? ''
  if (!version) return null
  if (version === catalog?.data_version && catalog.candy?.expected_answer != null) return catalog.candy.expected_answer
  const match = /candy-(\d+)/i.exec(version)
  return match ? Number(match[1]) : null
}

/** A run scored against an older question set than the one the server uses now. */
export function isHistoricalDataVersion(run: Pick<OpenAIEvalRun, 'data_version'>, catalog?: OpenAIEvalModelCatalog | null): boolean {
  if (!run.data_version) return false
  if (catalog?.data_version) return run.data_version !== catalog.data_version
  const expected = candyExpectedAnswer(run, catalog)
  return expected !== null && catalog?.candy?.expected_answer != null && expected !== catalog.candy.expected_answer
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

export type PolicyFactor = 'quality' | 'price' | 'errors' | 'speed'

/**
 * Relative emphasis (0–4) each policy gives the integrity pass rate, price,
 * error rate and first-token latency. Mirrors openai_account_scheduler.go:
 * default weights are error 0.8 / ttft 0.5 / upstream cost 0 (admin-tunable);
 * cost_first lifts cost to ≥2 and scales error/ttft by 0.65; stability_first
 * lifts error ≥2.5, ttft ≥1.5 and ignores the pass rate; avoid_degradation
 * first keeps only the best known pass-rate tier, then ranks it with error
 * ≥2.5, ttft ≥1.25 and cost scaled by 0.25. Custom balance weighs the pass
 * rate as one factor among the others. Load and priority are untouched.
 */
export const POLICY_EMPHASIS: Record<string, Record<PolicyFactor, number>> = {
  '': { quality: 0, price: 1, errors: 2, speed: 2 },
  cost_first: { quality: 0, price: 4, errors: 1, speed: 1 },
  stability_first: { quality: 0, price: 1, errors: 4, speed: 4 },
  avoid_degradation: { quality: 0, price: 1, errors: 3, speed: 2 },
  custom_balance: { quality: 2, price: 2, errors: 2, speed: 2 }
}

/**
 * How each policy treats the pass rate. 'tier' is a strict first filter (best
 * known tier, falling to the next only without capacity), so it is never drawn
 * as a finite weight that price could outweigh. Only custom balance weighs it.
 */
export const QUALITY_MODE: Record<string, 'tier' | 'weighted' | 'ignored'> = {
  '': 'ignored',
  cost_first: 'ignored',
  stability_first: 'ignored',
  avoid_degradation: 'tier',
  custom_balance: 'weighted'
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
  'rate_ladder_same_rate', 'rate_ladder_upgrade', 'rate_ladder_lower_rate_fallback', 'quality_tier_selection',
  'no_selection', 'selection_error'
])

export function decisionKey(code: string | undefined): string {
  return code && KNOWN_DECISIONS.has(code) ? code : 'unknown'
}

const KNOWN_CANDIDATE_REASONS = new Set([
  'score_top_k_candidate', 'ranked_below_top_k', 'same_rate_candidate', 'higher_rate_candidate', 'below_migration_rate',
  'quality_tier_top_k_candidate', 'quality_lower_tier_fallback'
])

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
