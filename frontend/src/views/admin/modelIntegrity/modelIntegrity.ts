// Pure helpers shared by the two Model Integrity pages (降智测试 / 调度策略).
// They only describe what the backend already enforces; keep numbers here in
// sync with backend/internal/service/openai_eval*.go instead of inventing them.
import type {
  OpenAIEvalAccountRuleCondition,
  OpenAIEvalAccountRuleConditionMetric,
  OpenAIEvalAccountRuleConditionOperator,
  OpenAIEvalAccountPriorityRule,
  OpenAIEvalBackgroundControl,
  OpenAIEvalConfig,
  OpenAIEvalEffectiveStatus,
  OpenAIEvalModelCatalog,
  OpenAIEvalOverviewModel,
  OpenAIEvalPolicyWeights,
  OpenAIEvalRankingFactorKey,
  OpenAIEvalRankingFactorWeights,
  OpenAIEvalRankingFactors,
  OpenAIEvalRankingWeights,
  OpenAIEvalRouteConfig,
  OpenAIEvalRun,
  OpenAIEvalSampleRecord,
  OpenAIEvalSchedule,
  OpenAIEvalSchedulingPolicy,
  OpenAIEvalSchedulingPolicyRule,
  OpenAIEvalSchedulingThresholds,
  SchedulerDecisionCandidate
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
 * Candy, Fingerprint and ModelTrace. State Probe reads the same setting, but
 * one attempt there is a whole mint/continue chain, so its ceiling is
 * STATE_PROBE_MAX_CHAINS × STATE_PROBE_REQUESTS.
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

/** Candy uses the configured sample count; one is the server default. */
export const CANDY_REQUESTS = 1
/** State Probe mints a ticket and continues it once. */
export const STATE_PROBE_REQUESTS = 2
/**
 * One retry feeds a fresh mint/continue chain, so a failed probe starts over
 * rather than re-sending the failed request. The server caps the chain count at
 * three (openAIStateProbeMaxAttempts), i.e. six sends, even when the shared
 * attempts setting is higher.
 */
export const STATE_PROBE_MAX_CHAINS = 3
/**
 * How many mint/continue chains a State Probe run may send. Attempts above the
 * server's cap change nothing, so the count is clamped before it is used.
 */
export function stateProbeChains(maxAttempts: number): number {
  return Math.min(normalizeMaxRequestAttempts(maxAttempts), STATE_PROBE_MAX_CHAINS)
}
/** Requests one State Probe run can send at most: chains × linked requests. */
export function stateProbeMaxRequests(maxAttempts: number): number {
  return STATE_PROBE_REQUESTS * stateProbeChains(maxAttempts)
}
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

/**
 * State Probe only exists for direct OpenAI OAuth accounts. Eligibility
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
  route.bps_mode = 'force_off'
  route.bps_auto = false
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

/** Rules saved before the switch existed omit it and stay in force. */
export function isRuleEnabled(rule: { enabled?: boolean }): boolean {
  return rule.enabled !== false
}

export function accountRuleModelID(model: string): string {
  const id = model.trim()
  switch (id.toLowerCase()) {
    case 'gpt-5.6':
    case 'gpt-5.6-sol': return 'gpt-5.6-sol'
    case 'gpt-6':
    case 'gpt-6-astra': return 'gpt-6-astra'
    default: return id
  }
}

function accountRuleModels(models: string[] = []): string[] {
  return [...new Set(models.map(accountRuleModelID).filter(Boolean))]
}

export function normalizeAccountPriorityRule(rule: OpenAIEvalAccountPriorityRule): OpenAIEvalAccountPriorityRule {
  return {
    account_id: rule.account_id,
    priority: rule.priority,
    requested_models: accountRuleModels(rule.requested_models),
    enabled: isRuleEnabled(rule),
    // Kept exactly as loaded, even when out of range: validation reports it.
    ...(rule.condition ? { condition: { ...rule.condition } } : {})
  }
}

/**
 * An empty model list is sent as omitted, which the server reads as every
 * model. The condition is always sent, as null when unconditional: the server
 * keeps the stored condition when the field is missing (for clients older
 * than conditions), so null is the only way to clear one.
 */
function accountPriorityRulePayload(rule: OpenAIEvalAccountPriorityRule): OpenAIEvalAccountPriorityRule {
  const models = accountRuleModels(rule.requested_models)
  return {
    account_id: rule.account_id,
    priority: rule.priority,
    ...(models.length ? { requested_models: models } : {}),
    enabled: isRuleEnabled(rule),
    condition: rule.condition ? { metric: rule.condition.metric, operator: rule.condition.operator, threshold: rule.condition.threshold } : null
  }
}

// -- Account rule conditions --------------------------------------------------

export const CONDITION_METRICS: OpenAIEvalAccountRuleConditionMetric[] = ['quality_ratio', 'error_rate', 'ttft_ms', 'price', 'load_rate']
export const CONDITION_OPERATORS: OpenAIEvalAccountRuleConditionOperator[] = ['gte', 'gt', 'lte', 'lt', 'eq']

/** How a metric is typed in the editor: rates as percent, first-token time in seconds, price as the multiplier. */
export type ConditionUnit = 'percent' | 'seconds' | 'multiplier'
export const CONDITION_UNITS: Record<OpenAIEvalAccountRuleConditionMetric, ConditionUnit> = {
  quality_ratio: 'percent',
  error_rate: 'percent',
  ttft_ms: 'seconds',
  price: 'multiplier',
  load_rate: 'percent'
}

/** A sensible starting point when a metric is picked, e.g. 通过率 ≥ 100 % or 错误率 < 5 %. */
export const CONDITION_DEFAULTS: Record<OpenAIEvalAccountRuleConditionMetric, OpenAIEvalAccountRuleCondition> = {
  quality_ratio: { metric: 'quality_ratio', operator: 'gte', threshold: 1 },
  error_rate: { metric: 'error_rate', operator: 'lt', threshold: 0.05 },
  ttft_ms: { metric: 'ttft_ms', operator: 'lte', threshold: 8000 },
  price: { metric: 'price', operator: 'lte', threshold: 1 },
  load_rate: { metric: 'load_rate', operator: 'lt', threshold: 80 }
}

/** Stored value → the number shown in the editor (0.05 → 5, 8000 ms → 8 s). */
export function conditionDisplayValue(metric: OpenAIEvalAccountRuleConditionMetric, threshold: number): number {
  if (metric === 'quality_ratio' || metric === 'error_rate') return Number((threshold * 100).toFixed(6))
  if (metric === 'ttft_ms') return Number((threshold / 1000).toFixed(6))
  return threshold
}

/** Typed number → stored value; NaN stays NaN so an unreadable entry blocks the save. */
export function conditionStoredValue(metric: OpenAIEvalAccountRuleConditionMetric, display: number): number {
  if (metric === 'quality_ratio' || metric === 'error_rate') return Number((display / 100).toFixed(8))
  if (metric === 'ttft_ms') return Number((display * 1000).toFixed(6))
  return display
}

export function conditionDisplayText(condition: OpenAIEvalAccountRuleCondition): string {
  return Number.isFinite(condition.threshold) ? String(conditionDisplayValue(condition.metric, condition.threshold)) : ''
}

/** Mirrors the server's checks: a known metric and operator, and a finite value inside the metric's range. */
export function isValidAccountRuleCondition(condition: OpenAIEvalAccountRuleCondition | null | undefined): boolean {
  if (!condition) return true
  if (!CONDITION_METRICS.includes(condition.metric) || !CONDITION_OPERATORS.includes(condition.operator)) return false
  const value = condition.threshold
  if (typeof value !== 'number' || !Number.isFinite(value) || value < 0) return false
  if (condition.metric === 'quality_ratio' || condition.metric === 'error_rate') return value <= 1
  if (condition.metric === 'load_rate') return value <= 100
  return true
}

export type AccountPriorityRuleIssue = 'account' | 'priority' | 'models' | 'condition' | 'overlap'

/**
 * Mirrors the server's checks so a save is blocked where the server would
 * reject it: an account, a whole-number priority, at least one model when
 * models are chosen, a valid condition, and no two enabled rules for one
 * account covering the same model. Like the server, the later rule of an
 * overlapping pair is flagged.
 */
export function accountPriorityRuleIssues(rules: OpenAIEvalAccountPriorityRule[], pickingModels: (rule: OpenAIEvalAccountPriorityRule) => boolean): Map<number, AccountPriorityRuleIssue> {
  const issues = new Map<number, AccountPriorityRuleIssue>()
  const scopes = new Map<number, Set<string>>()
  rules.forEach((rule, index) => {
    const models = accountRuleModels(rule.requested_models)
    if (!(rule.account_id > 0)) issues.set(index, 'account')
    else if (typeof rule.priority !== 'number' || !Number.isSafeInteger(rule.priority)) issues.set(index, 'priority')
    else if (pickingModels(rule) && !models.length) issues.set(index, 'models')
    else if (!isValidAccountRuleCondition(rule.condition)) issues.set(index, 'condition')
    if (!isRuleEnabled(rule) || !(rule.account_id > 0)) return
    const taken = scopes.get(rule.account_id) ?? new Set<string>()
    scopes.set(rule.account_id, taken)
    for (const key of models.length ? models.map(model => model.toLowerCase()) : ['']) {
      if (taken.has(key) && !issues.has(index)) issues.set(index, 'overlap')
      taken.add(key)
    }
  })
  return issues
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
    bps_auto_enabled: false, // retired; never reactivate legacy saved switches
    scheduling_policy: config.scheduling_policy ?? '',
    custom_balance: customBalancePayload(config.custom_balance),
    // Copied as edited: the scheduling page refuses to save invalid values,
    // and the tests page never touches them.
    scheduling_thresholds: normalizeSchedulingThresholds(config.scheduling_thresholds),
    policies: (config.policies ?? []).map(rule => ({
      ...rule,
      reasoning_effort: rule.reasoning_effort || '',
      enabled: isRuleEnabled(rule),
      ...(rule.policy === 'custom_balance'
        ? { custom_balance: customBalancePayload(rule.custom_balance ?? config.custom_balance) }
        : {})
    })),
    account_priority_rules: (config.account_priority_rules ?? []).map(accountPriorityRulePayload),
    bps_accounts: [],
    max_request_attempts: normalizeMaxRequestAttempts(config.max_request_attempts),
    quality_refresh_interval_seconds: normalizeQualityRefreshInterval(config.quality_refresh_interval_seconds),
    accounts: config.accounts.map(route => {
      // eslint-disable-next-line @typescript-eslint/no-unused-vars
      const { bps_state, direct_oauth_eligible, ...rest } = route
      return { ...rest, bps_mode: 'force_off', bps_auto: false }
    }),
    // Omitted when the server never sent it (older servers), so a save cannot
    // invent an empty list; otherwise sent back with runtime stripped.
    ...(config.background_controls !== undefined
      ? { background_controls: config.background_controls.map(backgroundControlPayload) }
      : {})
  }
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
    stability: 0,
    // Saving the weights must never reorder or erase the strict comparisons.
    ...(weights.absolute_priorities?.length ? { absolute_priorities: absolutePriorities(weights) } : {})
  }
}

/** Server aliases for absolute priorities (openAIEvalAbsolutePriorityFactors). */
const PRIORITY_ALIASES: Record<string, CustomFactor> = {
  cost: 'cost', price: 'cost', error_rate: 'error_rate', errors: 'error_rate',
  ttft: 'ttft', latency: 'ttft', load: 'load', quality: 'quality'
}

/**
 * The strict comparisons, in order, using the server's canonical names. An
 * alias the API accepted ("price") reads as its factor; an unknown name or a
 * repeat is dropped, because the server would reject the save over it.
 */
export function absolutePriorities(weights?: Pick<OpenAIEvalPolicyWeights, 'absolute_priorities'> | null): CustomFactor[] {
  const out: CustomFactor[] = []
  for (const raw of weights?.absolute_priorities ?? []) {
    const factor = PRIORITY_ALIASES[String(raw).trim().toLowerCase()]
    if (factor && !out.includes(factor)) out.push(factor)
  }
  return out
}

/**
 * Older backends emitted an all-zero object even though it cannot be saved;
 * that, or any negative/non-numeric value, reads as the defaults. All-zero
 * weights with a priority order are a valid choice and are kept as they are.
 */
export function normalizeCustomBalance(weights?: OpenAIEvalPolicyWeights): OpenAIEvalPolicyWeights {
  if (!weights) return { ...DEFAULT_CUSTOM_BALANCE, stability: 0 }
  const legacy = [weights.cost, weights.stability ?? 0, weights.error_rate, weights.ttft, weights.load, weights.quality ?? 0].map(Number)
  const priorities = absolutePriorities(weights)
  const zeroTotal = legacy.reduce((sum, value) => sum + value, 0) <= 0
  if (legacy.some(value => !Number.isFinite(value) || value < 0) || (zeroTotal && !priorities.length)) {
    // The weights are replaced, but the strict order is the admin's own choice.
    return { ...DEFAULT_CUSTOM_BALANCE, stability: 0, ...(priorities.length ? { absolute_priorities: priorities } : {}) }
  }
  return foldLegacyStability(weights)
}

/**
 * The weights as sent on save. `absolute_priorities` is always present: the
 * server keeps its stored list when the field is missing, so an empty list is
 * the only way to clear the last priority.
 */
export function customBalancePayload(weights?: OpenAIEvalPolicyWeights): OpenAIEvalPolicyWeights {
  const normalized = normalizeCustomBalance(weights)
  return { ...normalized, absolute_priorities: absolutePriorities(normalized) }
}

/**
 * Why the server would reject the weights, or null when it would accept them.
 * Every weight may be 0 once there is a priority order: accounts that tie on
 * every priority then keep the server's account-ID order.
 */
export type CustomBalanceIssue = 'invalid_value' | 'zero_total'

export function customBalanceIssue(weights?: OpenAIEvalPolicyWeights): CustomBalanceIssue | null {
  if (!weights) return 'zero_total'
  // Fold first: a quality-only set is valid, and a legacy stability weight still counts.
  const folded = foldLegacyStability(weights)
  const values = CUSTOM_FACTORS.map(factor => Number(folded[factor] ?? 0))
  if (!values.every(value => Number.isFinite(value) && value >= 0)) return 'invalid_value'
  if (values.reduce((sum, value) => sum + value, 0) > 0 || absolutePriorities(weights).length) return null
  return 'zero_total'
}

/** False when the server would reject the weights: any negative/non-numeric value, or an all-zero total without a priority. */
export function isValidCustomBalance(weights?: OpenAIEvalPolicyWeights): boolean {
  return customBalanceIssue(weights) === null
}

/** True when no weight (a legacy stability weight included) is above 0, so a weighted score cannot break any tie. */
export function hasNoPositiveWeight(weights?: Partial<OpenAIEvalPolicyWeights> | null): boolean {
  if (!weights) return false
  return ![...CUSTOM_FACTORS, 'stability' as const].some(factor => Number(weights[factor] ?? 0) > 0)
}

/** i18n key for a rejected weight set; negative values cannot be typed, so they share the zero-total text. */
const CUSTOM_BALANCE_ISSUE_KEYS: Record<CustomBalanceIssue, string> = {
  invalid_value: 'admin.modelIntegrity.scheduling.policy.custom.zeroTotal',
  zero_total: 'admin.modelIntegrity.scheduling.policy.custom.zeroTotal'
}

export function customBalanceIssueKey(issue: CustomBalanceIssue): string {
  return CUSTOM_BALANCE_ISSUE_KEYS[issue]
}

/** Whole-percent share of each factor after the normalization the server applies on save. */
export function customBalanceShares(weights?: OpenAIEvalPolicyWeights): Record<CustomFactor, number> {
  const normalized = normalizeCustomBalance(weights)
  const weight = (factor: CustomFactor) => Number(normalized[factor] ?? 0)
  const total = CUSTOM_FACTORS.reduce((sum, factor) => sum + weight(factor), 0)
  // All-zero weights (valid with priorities) have no share to divide.
  return Object.fromEntries(CUSTOM_FACTORS.map(factor => [factor, total > 0 ? Math.round((weight(factor) / total) * 100) : 0])) as Record<CustomFactor, number>
}

// ---------------------------------------------------------------------------
// Runtime thresholds (scheduling_thresholds)
//
// Exceeding a threshold moves an account back one position (one adjacent swap,
// within its pass-rate tier under avoid_degradation); it never disables the
// account. Only real requests count, and only once the
// shared minimum sample counts are reached.
// ---------------------------------------------------------------------------

/**
 * Policies an administrator can set runtime thresholds for. Custom balance is
 * deliberately absent: it is ordered only by its own priorities and weights (openAIEvalRankingWeights
 * ignores custom thresholds), so exposing a row here would imply an effect it
 * does not have. The server keeps a custom_balance threshold object for API
 * compatibility and it is preserved through every save.
 */
export const THRESHOLD_POLICIES = ['cost_first', 'stability_first', 'avoid_degradation'] as const
export type ThresholdPolicy = typeof THRESHOLD_POLICIES[number]
/**
 * Every policy key the server's object carries. `custom_balance` is not
 * editable here, but it has to survive normalization or a save would drop it.
 */
export const THRESHOLD_ROUND_TRIP_POLICIES = [...THRESHOLD_POLICIES, 'custom_balance'] as const
export const THRESHOLD_SAMPLE_KEYS = ['min_error_samples', 'min_ttft_samples'] as const
export type ThresholdSampleKey = typeof THRESHOLD_SAMPLE_KEYS[number]
/** Edited as whole minutes, stored as seconds. */
export const RECOVERY_INTERVAL_KEY = 'recovery_interval_seconds'
/** A value that belongs to no single policy row. */
export type ThresholdScalarKey = ThresholdSampleKey | typeof RECOVERY_INTERVAL_KEY
/** Identifies one editable value, e.g. 'stability_first.error_rate' or 'min_ttft_samples'. */
export type ThresholdField = `${ThresholdPolicy}.error_rate` | `${ThresholdPolicy}.ttft_seconds` | ThresholdScalarKey

export const MAX_TTFT_THRESHOLD_SECONDS = 86_400
export const MIN_THRESHOLD_SAMPLES = 1
export const MAX_THRESHOLD_SAMPLES = 1_000_000

/**
 * Scheduled recovery (定时恢复): every interval, one real request may undo an
 * account's threshold move-back once to try it. Nothing is sent without
 * business traffic. The range is the server's storage range; the editor offers
 * whole minutes within it.
 */
export const DEFAULT_RECOVERY_INTERVAL_SECONDS = 30 * 60
export const MIN_RECOVERY_INTERVAL_SECONDS = 5 * 60
export const MAX_RECOVERY_INTERVAL_SECONDS = MAX_INTERVAL_SECONDS
export const RECOVERY_INTERVALS = [5 * 60, 10 * 60, 30 * 60, HOUR, 6 * HOUR, 12 * HOUR, DAY]

/** The thresholds after normalization: the optional recovery fields are always filled. */
export type NormalizedSchedulingThresholds = OpenAIEvalSchedulingThresholds & Required<Pick<OpenAIEvalSchedulingThresholds, 'recovery_enabled' | 'recovery_interval_seconds'>>

export const DEFAULT_SCHEDULING_THRESHOLDS: NormalizedSchedulingThresholds = {
  cost_first: { error_rate: 0.2, ttft_seconds: 15 },
  stability_first: { error_rate: 0.05, ttft_seconds: 8 },
  avoid_degradation: { error_rate: 0.2, ttft_seconds: 15 },
  custom_balance: { error_rate: 0.2, ttft_seconds: 15 },
  min_error_samples: 10,
  min_ttft_samples: 20,
  recovery_enabled: true,
  recovery_interval_seconds: DEFAULT_RECOVERY_INTERVAL_SECONDS
}

const numberOr = (value: unknown, fallback: number) => (typeof value === 'number' ? value : fallback)

/**
 * A fresh copy with every missing value taken from the defaults. Values the
 * server sent are kept exactly, including an explicit 0 % error rate, so a
 * save sends back what was loaded; validation reports anything out of range.
 * A recovery interval of 0 is the server's legacy "unset" and reads as the default.
 */
export function normalizeSchedulingThresholds(value?: Partial<OpenAIEvalSchedulingThresholds> | null): NormalizedSchedulingThresholds {
  const source = value && typeof value === 'object' ? value : {}
  const result = { ...DEFAULT_SCHEDULING_THRESHOLDS }
  for (const policy of THRESHOLD_ROUND_TRIP_POLICIES) {
    const own = source[policy]
    const fallback = DEFAULT_SCHEDULING_THRESHOLDS[policy]
    result[policy] = {
      error_rate: numberOr(own?.error_rate, fallback.error_rate),
      ttft_seconds: numberOr(own?.ttft_seconds, fallback.ttft_seconds)
    }
  }
  for (const key of THRESHOLD_SAMPLE_KEYS) result[key] = numberOr(source[key], DEFAULT_SCHEDULING_THRESHOLDS[key])
  result.recovery_enabled = typeof source.recovery_enabled === 'boolean' ? source.recovery_enabled : DEFAULT_SCHEDULING_THRESHOLDS.recovery_enabled
  result.recovery_interval_seconds = source.recovery_interval_seconds === 0
    ? DEFAULT_RECOVERY_INTERVAL_SECONDS
    : numberOr(source.recovery_interval_seconds, DEFAULT_RECOVERY_INTERVAL_SECONDS)
  return result
}

/** Whole seconds within the server's storage range, 300 to 2,147,483,647. */
export function isValidRecoveryInterval(value: unknown): boolean {
  return typeof value === 'number' && Number.isInteger(value) && value >= MIN_RECOVERY_INTERVAL_SECONDS && value <= MAX_RECOVERY_INTERVAL_SECONDS
}

export function isValidThresholdErrorRate(value: unknown): boolean {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0 && value <= 1
}

export function isValidThresholdTTFT(value: unknown): boolean {
  return typeof value === 'number' && Number.isFinite(value) && value > 0 && value <= MAX_TTFT_THRESHOLD_SECONDS
}

export function isValidThresholdSamples(value: unknown): boolean {
  return typeof value === 'number' && Number.isInteger(value) && value >= MIN_THRESHOLD_SAMPLES && value <= MAX_THRESHOLD_SAMPLES
}

/**
 * Every editable value the server would reject, in the order they appear on
 * the page. `custom_balance` is deliberately not validated: no row edits it,
 * so flagging it would block saving on a value the page never showed.
 */
export function invalidThresholdFields(thresholds?: OpenAIEvalSchedulingThresholds | null): ThresholdField[] {
  const value = normalizeSchedulingThresholds(thresholds)
  const invalid: ThresholdField[] = []
  for (const policy of THRESHOLD_POLICIES) {
    if (!isValidThresholdErrorRate(value[policy].error_rate)) invalid.push(`${policy}.error_rate`)
    if (!isValidThresholdTTFT(value[policy].ttft_seconds)) invalid.push(`${policy}.ttft_seconds`)
  }
  for (const key of THRESHOLD_SAMPLE_KEYS) if (!isValidThresholdSamples(value[key])) invalid.push(key)
  // Checked while switched off too: the server stores the interval either way.
  if (!isValidRecoveryInterval(value.recovery_interval_seconds)) invalid.push(RECOVERY_INTERVAL_KEY)
  return invalid
}

/** The stored ratio as the percentage shown in the editor, without float noise (0.07 → "7"). */
export function errorRatePercentText(ratio: number): string {
  return Number.isFinite(ratio) ? String(Number((ratio * 100).toFixed(6))) : ''
}

/** Reads typed text as a number; empty or non-numeric text is NaN, never 0. */
export function parseThresholdInput(text: string): number {
  const trimmed = text.trim()
  return trimmed === '' ? NaN : Number(trimmed)
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
/** Whether a test can run for a target; the default only knows the State Probe rule. */
export type TestApplicability = (route: OpenAIEvalRouteConfig, type: EvalTestType) => boolean
const defaultApplicability: TestApplicability = (route, type) => type !== 'state_probe' || isDirectOAuthRoute(route)

export function dailyRequests(route: OpenAIEvalRouteConfig, type: EvalTestType, catalog?: OpenAIEvalModelCatalog | null, applies: TestApplicability = defaultApplicability): number {
  const schedule = scheduleOf(route, type)
  if (!schedule?.enabled || !(schedule.interval_seconds > 0)) return 0
  if (!applies(route, type)) return 0
  return (requestsPerRun(route, type, catalog) * DAY) / schedule.interval_seconds
}

export function totalDailyRequests(routes: OpenAIEvalRouteConfig[], catalog?: OpenAIEvalModelCatalog | null, applies: TestApplicability = defaultApplicability): number {
  return routes.reduce((sum, route) => sum + TEST_TYPES.reduce((inner, type) => inner + dailyRequests(route, type, catalog, applies), 0), 0)
}

/**
 * Upper bound for one run when every sample uses all of its attempts. State
 * Probe is not exempt: its attempts are chains of two linked requests, capped
 * at STATE_PROBE_MAX_CHAINS, so the ceiling is 2 × min(attempts, 3).
 */
export function maxRequestsPerRun(route: OpenAIEvalRouteConfig, type: EvalTestType, maxAttempts: number, catalog?: OpenAIEvalModelCatalog | null, sampleMode?: string): number {
  if (type === 'state_probe') return stateProbeMaxRequests(maxAttempts)
  const samples = requestsPerRun(route, type, catalog, sampleMode)
  return samples * normalizeMaxRequestAttempts(maxAttempts)
}

/** Daily upper bound for all automatic schedules if every sample used all attempts. */
export function totalDailyMaxRequests(routes: OpenAIEvalRouteConfig[], maxAttempts: number, catalog?: OpenAIEvalModelCatalog | null, applies: TestApplicability = defaultApplicability): number {
  const attempts = normalizeMaxRequestAttempts(maxAttempts)
  return routes.reduce((sum, route) => sum + TEST_TYPES.reduce((inner, type) => inner + dailyRequests(route, type, catalog, applies) * (type === 'state_probe' ? stateProbeChains(attempts) : attempts), 0), 0)
}

export function activeScheduleCount(routes: OpenAIEvalRouteConfig[], applies: TestApplicability = defaultApplicability): number {
  return routes.reduce((sum, route) => sum + TEST_TYPES.filter(type => scheduleOf(route, type)?.enabled && applies(route, type)).length, 0)
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
/** ModelTrace only: a non-Luna attribution equal to the public tested model (trim/case-insensitive; Luna is checked first). */
export const MODELTRACE_TARGET_MATCH_REASON = 'modeltrace_target_match'
/** ModelTrace only: a Luna-family attribution, stored as 'warning' and shown as abnormal. */
export const MODELTRACE_LUNA_REASON = 'modeltrace_luna_attribution'

const ATTRIBUTION_REASONS = new Set([LUNA_ATTRIBUTION_REASON, NON_LUNA_ATTRIBUTION_REASON, MODELTRACE_TARGET_MATCH_REASON, MODELTRACE_LUNA_REASON])

/**
 * Status key used for the visible label. A Fingerprint (or legacy ModelTrace)
 * Luna attribution is stored as a generic 'warning'; label it "possible Luna"
 * rather than "abnormal". A ModelTrace 'modeltrace_luna_attribution' keeps the
 * plain status, so it reads "abnormal".
 */
export function runStatusKey(run: Pick<OpenAIEvalRun, 'status' | 'outcome'>): string {
  if (run.status === 'warning' && run.outcome?.reason === LUNA_ATTRIBUTION_REASON) return 'suspected_luna'
  return run.status
}

/** True when the run's verdict is a behavioural attribution, so the UI must say it is inferred. */
export function isAttributionRun(run: Pick<OpenAIEvalRun, 'status' | 'outcome' | 'test_type'>): boolean {
  if (run.test_type !== 'modeltrace' && run.test_type !== 'fingerprint') return false
  return run.status === 'suspected_normal' || ATTRIBUTION_REASONS.has(run.outcome?.reason ?? '')
}

/**
 * The verdict a run was stored with, when the server now reads it under a
 * different attribution rule. Null when nothing changed or the record carries
 * no metadata, so an unchanged run never shows a "reinterpreted" note.
 */
export function attributionReinterpretation(run: Pick<OpenAIEvalRun, 'status' | 'outcome'>): { status: string; reason?: string; rule: string; currentRule: string } | null {
  const meta = run.outcome?.attribution
  if (!meta?.original_status) return null
  const sameVerdict = meta.original_status === run.status && (meta.original_reason ?? '') === (run.outcome.reason ?? '')
  if (sameVerdict && meta.original_rule_version === meta.rule_version) return null
  return { status: meta.original_status, reason: meta.original_reason, rule: meta.original_rule_version, currentRule: meta.rule_version }
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
 * Relative emphasis (0–4) of the system default weights in
 * openai_account_scheduler.go (error 0.8 / ttft 0.5 / upstream cost 0,
 * admin-tunable). Display levels only, and only for “System default”: every
 * other policy is shown by its sort order (POLICY_ORDER).
 */
export const POLICY_EMPHASIS: Record<string, Record<PolicyFactor, number>> = {
  '': { quality: 0, price: 1, errors: 2, speed: 2 }
}

/** The ranking policies, as opposed to the system default. */
export type RankingPolicy = Exclude<OpenAIEvalSchedulingPolicy, ''>
/** Policies with a fixed order rather than administrator weights. */
export type PresetPolicy = Exclude<RankingPolicy, 'custom_balance'>

export function isPresetPolicy(policy: string | null | undefined): policy is PresetPolicy {
  return policy === 'cost_first' || policy === 'stability_first' || policy === 'avoid_degradation'
}

export type OrderStep = 'within_thresholds' | 'quality' | 'price' | 'weighted_score'

/**
 * How each policy compares two accounts, step by step; a later step only
 * breaks ties of the earlier ones. Mirrors rankingPolicyLess in
 * openai_eval_scheduling_thresholds.go. Accounts that fail a live check
 * (disabled, ownership, group, capacity) never reach this comparison.
 * Custom balance has no threshold step: rankingThresholdReasons returns nil
 * for it, so its absolute priorities and custom weights decide alone (see
 * policyOrderKeys). Its threshold row exists for API
 * compatibility only and is never read.
 */
export const POLICY_ORDER: Record<RankingPolicy, OrderStep[]> = {
  cost_first: ['within_thresholds', 'price'],
  stability_first: ['within_thresholds', 'price'],
  avoid_degradation: ['within_thresholds', 'quality', 'price'],
  custom_balance: ['weighted_score']
}

/**
 * i18n key suffixes under scheduling.policy.order for one policy. Custom
 * balance with absolute priorities compares each priority strictly first, so
 * the weighted score only breaks the ties they leave. With every weight at 0
 * there is no score, so the server's account-ID order breaks them instead.
 */
export function policyOrderKeys(policy: RankingPolicy, custom?: Partial<OpenAIEvalPolicyWeights> | null): string[] {
  if (policy !== 'custom_balance') return POLICY_ORDER[policy]
  const priorities = absolutePriorities(custom)
  if (!priorities.length) return POLICY_ORDER.custom_balance
  // Priorities alone (no weights given) say nothing about the score.
  const weightless = custom?.cost !== undefined && hasNoPositiveWeight(custom)
  return [...priorities.map(factor => `priority.${factor}`), weightless ? 'account_order' : 'weighted_tiebreak']
}

/**
 * How each policy treats the pass rate. 'tier' is a strict comparison step
 * ahead of price, never a finite weight that price could outweigh. Only
 * custom balance weighs it.
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

/**
 * The role a factor plays on one policy card, drawn with the same
 * four-segment bar as “System default”. A bar is a reading aid for how
 * strongly the factor decides the order, never a weight the server
 * multiplies by:
 * - sort: presets order by price (sort_in_tier: within a pass-rate tier);
 * - gate_*: a runtime threshold; an account over it moves back one position, and
 *   the bar follows how strict the configured threshold is;
 * - weight: a custom balance share; system: the system scheduling weights.
 * Roles without a bar are stated as text: tier (pass rate compared before
 * anything else), priority (custom balance's strict order) and ignored.
 */
export type MeterRole = 'sort' | 'sort_in_tier' | 'gate_strict' | 'gate_standard' | 'gate_loose' | 'weight' | 'system' | 'tier' | 'priority' | 'ignored'
export type MeterFactor = PolicyFactor | 'load'

export interface PolicyMeter {
  factor: MeterFactor
  /** Filled segments, 0–4; unused for the text roles. */
  level: number
  role: MeterRole
  /** Priority rank (1-based) for 'priority', whole-percent share for 'weight', the threshold for gates. */
  value?: number
}

/** Roles stated in words rather than drawn: a strict step or no effect is not an amount. */
export const TEXT_METER_ROLES: MeterRole[] = ['tier', 'priority', 'ignored']

const METER_FACTORS: PolicyFactor[] = ['quality', 'price', 'errors', 'speed']
const CUSTOM_METER_FACTOR: Record<MeterFactor, CustomFactor> = { quality: 'quality', price: 'cost', errors: 'error_rate', speed: 'ttft', load: 'load' }
const GATE_LEVEL: Partial<Record<MeterRole, number>> = { gate_strict: 3, gate_standard: 2, gate_loose: 1 }

/** At or under the stability defaults reads strict, at or under the shared defaults standard. */
function gateRole(value: number, strict: number, standard: number): MeterRole {
  if (value <= strict) return 'gate_strict'
  return value <= standard ? 'gate_standard' : 'gate_loose'
}

/**
 * The bars for one policy card. Presets sort by price and use error rate and
 * first-token time only as thresholds, so those rows show how strict the
 * configured threshold is, not a share of a score; that is how “Stability
 * first” differs from “Cost first”. Custom balance shows its strict
 * priorities and its real weight shares; load is listed only when it counts.
 */
export function policyMeters(
  policy: OpenAIEvalSchedulingPolicy,
  custom?: OpenAIEvalPolicyWeights | null,
  thresholds?: OpenAIEvalSchedulingThresholds | null
): PolicyMeter[] {
  if (!policy) {
    return METER_FACTORS.map(factor => {
      const level = POLICY_EMPHASIS[''][factor]
      return { factor, level, role: level ? 'system' : 'ignored' }
    })
  }
  if (policy === 'custom_balance') {
    const priorities = absolutePriorities(custom)
    const shares = customBalanceShares(custom ?? undefined)
    const meters = ([...METER_FACTORS, 'load'] as MeterFactor[]).map((factor): PolicyMeter => {
      const key = CUSTOM_METER_FACTOR[factor]
      const rank = priorities.indexOf(key)
      if (rank >= 0) return { factor, level: 0, role: 'priority', value: rank + 1 }
      const share = shares[key]
      return share > 0 ? { factor, level: Math.min(4, Math.ceil(share / 25)), role: 'weight', value: share } : { factor, level: 0, role: 'ignored' }
    })
    return meters.filter(meter => meter.factor !== 'load' || meter.role !== 'ignored')
  }
  const limits = normalizeSchedulingThresholds(thresholds)[policy]
  const strict = DEFAULT_SCHEDULING_THRESHOLDS.stability_first
  const standard = DEFAULT_SCHEDULING_THRESHOLDS.cost_first
  const errors = gateRole(limits.error_rate, strict.error_rate, standard.error_rate)
  const speed = gateRole(limits.ttft_seconds, strict.ttft_seconds, standard.ttft_seconds)
  const tiered = policy === 'avoid_degradation'
  return [
    tiered ? { factor: 'quality', level: 0, role: 'tier' } : { factor: 'quality', level: 0, role: 'ignored' },
    tiered ? { factor: 'price', level: 3, role: 'sort_in_tier' } : { factor: 'price', level: 4, role: 'sort' },
    { factor: 'errors', level: GATE_LEVEL[errors] ?? 0, role: errors, value: limits.error_rate },
    { factor: 'speed', level: GATE_LEVEL[speed] ?? 0, role: speed, value: limits.ttft_seconds }
  ]
}

// ---------------------------------------------------------------------------
// Scheduler decision explanations
// ---------------------------------------------------------------------------

const KNOWN_EXCLUSIONS = new Set([
  'excluded', 'not_schedulable', 'platform_mismatch', 'runtime_blocked', 'privacy_not_set', 'transport_incompatible',
  'model_not_supported', 'account_model_not_owned', 'capability_mismatch', 'channel_upstream_restricted',
  'exec_capability_cooldown', 'shadow_parent_unhealthy', 'evaluation_hard_failure', 'proxy_stream_quarantined',
  'compact_support_unknown', 'concurrency_full', 'conn_queue_full', 'model_rate_limited', 'upstream_rate_limited',
  'account_nil',
  // Ranking evaluation (openai_eval_ranking_discovery.go / _availability.go).
  'model_or_platform_incompatible', 'group_model_restricted', 'oauth_only', 'group_inactive', 'account_inactive',
  'account_disabled', 'account_expired', 'overloaded', 'rate_limited', 'temporary_cooldown', 'quota_exceeded',
  'concurrency_full_at_evaluation', 'model_runtime_cooldown',
  // Ranked dispatch live admission (recordOpenAIRankedSkip).
  'live_gate_changed', 'database_admission_changed', 'compact_unsupported'
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
  'no_selection', 'selection_error',
  // Ranked dispatch and quality-aware selection (openai_account_scheduler*.go).
  'explicit_policy_rank', 'required_owner_override', 'selection_budget_exhausted', 'quality_unassessed_fallback',
  'quality_weighted_selection',
  // A scheduled recovery trial undid one threshold move-back for this request.
  'runtime_recovery_trial'
])

export function decisionKey(code: string | undefined): string {
  return code && KNOWN_DECISIONS.has(code) ? code : 'unknown'
}

const KNOWN_CANDIDATE_REASONS = new Set([
  'score_top_k_candidate', 'ranked_below_top_k', 'same_rate_candidate', 'higher_rate_candidate', 'below_migration_rate',
  'quality_tier_top_k_candidate', 'quality_lower_tier_fallback',
  'explicit_policy_rank', 'live_admission_skipped', 'required_owner_override', 'quality_unassessed_fallback',
  // A fully cold pool ordered by the account overview (rc4).
  'overview_prior',
  // The requested model and effort have no configured test evidence, so the
  // tier was ordered by the separate account-wide quality reference (rc6).
  'account_prior_tier',
  'runtime_recovery_trial'
])

export function candidateReasonKey(code: string | undefined): string | null {
  if (!code) return null
  return KNOWN_CANDIDATE_REASONS.has(code) ? code : KNOWN_DECISIONS.has(code) ? `decision.${code}` : null
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

// ---------------------------------------------------------------------------
// Scheduling evaluation rankings (rc3)
//
// Everything here is presentation of server-computed numbers: the page never
// derives a score, a rank or a tier. The preset weight sets below mirror
// openAIEvalRankingWeights so the copy on screen matches the scorer the
// backend actually runs — and, since the presets publish a price-only set,
// so that the page never presents it as the whole comparison.
// ---------------------------------------------------------------------------

/**
 * The weights the backend publishes for the presets
 * (openAIEvalRankingWeights). They only produce the price score shown on the
 * board; the order itself compares the threshold tier, then (avoid
 * degradation) the pass rate, then price — see POLICY_ORDER. Error rate and
 * first-token latency act through the runtime thresholds, never as weights.
 */
export const PRESET_WEIGHTS: Record<PresetPolicy, OpenAIEvalRankingWeights> = {
  cost_first: { price: 1, error_rate: 0, ttft: 0, load: 0, quality: 0 },
  stability_first: { price: 1, error_rate: 0, ttft: 0, load: 0, quality: 0 },
  avoid_degradation: { price: 1, error_rate: 0, ttft: 0, load: 0, quality: 0 }
}

/**
 * Every weight set the backend publishes for a non-custom policy: the
 * price-only set above for the presets, plus the all-zero set it returns for
 * the system default, which ranks with the system scheduling weights instead
 * of a published score. Used to keep the page from drawing a stale percentage.
 */
export const PUBLISHED_PRESET_WEIGHTS: OpenAIEvalRankingWeights[] = [
  ...Object.values(PRESET_WEIGHTS),
  { price: 0, error_rate: 0, ttft: 0, load: 0, quality: 0 }
]

/** Factors in the order the ranking contract lists them. */
export const RANKING_FACTORS: OpenAIEvalRankingFactorKey[] = ['price', 'error_rate', 'ttft', 'load', 'quality']

/** The weights a policy ranks with. Custom Balance reads the saved config. */
export function weightsForPolicy(policy: OpenAIEvalSchedulingPolicy, custom?: OpenAIEvalPolicyWeights | null): OpenAIEvalRankingWeights | null {
  if (!policy) return null
  if (policy !== 'custom_balance') return PRESET_WEIGHTS[policy]
  if (!custom) return null
  const normalized = normalizeCustomBalance(custom)
  const total = CUSTOM_FACTORS.reduce((sum, factor) => sum + Number(normalized[factor] ?? 0), 0)
  if (total <= 0) return null
  const share = (factor: CustomFactor) => Number(normalized[factor] ?? 0) / total
  return { price: share('cost'), error_rate: share('error_rate'), ttft: share('ttft'), load: share('load'), quality: share('quality') }
}

/**
 * Avoid degradation compares the pass rate as its own step before price, so
 * its quality weight is 0 and the copy must say the tier decides, not a weight.
 */
export function policyQualityEmphasis(policy: OpenAIEvalSchedulingPolicy): 'tier' | 'weighted' | 'ignored' {
  if (policy === 'avoid_degradation') return 'tier'
  if (policy === 'custom_balance') return 'weighted'
  return 'ignored'
}

/** Number of factors a policy actually weighs, used to label the preset row. */
export function weightedFactorCount(weights: OpenAIEvalRankingWeights): number {
  return RANKING_FACTORS.filter(factor => weights[factor] > 0).length
}

// ---------------------------------------------------------------------------
// Effective status
// ---------------------------------------------------------------------------

export type EffectiveTone = 'active' | 'partial' | 'off' | 'error'

/** Every status the server can report, so an unknown value still gets a label. */
export const EFFECTIVE_KEYS = [
  'active', 'active_partial', 'inactive_effects_off', 'inactive_legacy_policy',
  'live_fallback', 'no_targets', 'error'
] as const

export type EffectiveKey = typeof EFFECTIVE_KEYS[number]

/** Tone drives the status pill colour, so a saved-but-inactive policy never reads as live. */
export function effectiveTone(status: OpenAIEvalEffectiveStatus | null | undefined): EffectiveTone {
  switch (status) {
    case 'active':
      return 'active'
    case 'active_partial':
    case 'live_fallback':
      return 'partial'
    case 'error':
      return 'error'
    default:
      return 'off'
  }
}

/**
 * True when the saved policy is stored but the gateway is not using it. This
 * is a valid configuration, so the page explains it rather than rejecting it.
 */
export function isInactiveStatus(status: OpenAIEvalEffectiveStatus | null | undefined): boolean {
  return status === 'inactive_effects_off' || status === 'inactive_legacy_policy' || status === 'no_targets'
}

// ---------------------------------------------------------------------------
// Unknown reasons and evidence labels
// ---------------------------------------------------------------------------

const KNOWN_UNKNOWN_REASONS = new Set([
  'no_price_evidence', 'no_error_samples', 'no_ttft_samples', 'no_load_reading',
  'quality_unknown', 'quality_insufficient', 'quality_stale', 'quality_expired',
  'no_samples', 'no_evidence', 'not_selected', 'selection_mismatch', 'no_candidates',
  // Codes the scorer writes today (openai_eval_ranking_score.go).
  'price_unavailable', 'no_request_samples', 'no_first_output_samples', 'load_unavailable', 'no_selected_tests',
  'selected_test_evidence_unavailable'
])

/** Maps a backend unknown_reason to an i18n key under modelIntegrity.scheduling.rank.unknown. */
export function unknownReasonKey(reason: string | null | undefined): string {
  if (!reason) return 'generic'
  return KNOWN_UNKNOWN_REASONS.has(reason) ? reason : 'generic'
}

const KNOWN_RANKING_SOURCES = new Set([
  'catalog', 'account_mapping', 'channel_mapping', 'group_route',
  'policy_rule', 'eval_route', 'observed_route'
])

export function rankingSourceKey(source: string | undefined): string {
  return source && KNOWN_RANKING_SOURCES.has(source) ? source : 'unknown'
}

const KNOWN_EXCLUSION_SCOPES = new Set(['account', 'route', 'live'])

/** Why an account could not serve this dimension. */
export function exclusionScopeKey(scope: string | undefined): string {
  return scope && KNOWN_EXCLUSION_SCOPES.has(scope) ? scope : 'account'
}

const KNOWN_COVERAGE_STATUSES = new Set(['complete', 'live_fallback', 'no_candidates', 'legacy'])

export function coverageStatusKey(status: string | undefined): string {
  return status && KNOWN_COVERAGE_STATUSES.has(status) ? status : 'complete'
}

const KNOWN_FALLBACK_REASONS = new Set([
  'no_snapshot', 'dimension_not_covered', 'quality_expired', 'route_mapping_changed',
  'unranked_candidate', 'evidence_expired', 'snapshot_superseded',
  // Dispatch-time fallbacks (explicitRanking) and publication limits.
  'snapshot_missing', 'config_revision_changed', 'dimension_not_cached', 'dimension_capacity_fallback',
  'snapshot_expired', 'selection_model_changed', 'account_mapping_changed', 'snapshot_bytes_limit', 'snapshot_capacity',
  // Exact scoring after fresh evidence or an uncertain read (rc4).
  'request_metrics_updated', 'quality_evidence_updated', 'quality_evidence_unavailable', 'monitoring_unavailable'
])

export function fallbackReasonKey(reason: string | null | undefined): string {
  if (!reason) return 'generic'
  return KNOWN_FALLBACK_REASONS.has(reason) ? reason : 'generic'
}

const KNOWN_TRIGGERS = new Set(['startup', 'policy_saved', 'manual', 'interval', 'catalog_change', 'evidence_expiry'])

export function rankingTriggerKey(trigger: string | undefined): string {
  return trigger && KNOWN_TRIGGERS.has(trigger) ? trigger : 'manual'
}

const KNOWN_ORDERINGS = new Set(['score_desc', 'quality_then_score', 'legacy'])

export function orderingKey(ordering: string | undefined): string {
  return ordering && KNOWN_ORDERINGS.has(ordering) ? ordering : 'score_desc'
}

/**
 * The board's order explanation. The server still labels cost first and
 * stability first 'score_desc', but they sort by price after the threshold
 * tier, so the policy decides the text before the ordering field does.
 * Custom balance with absolute priorities is also labelled 'score_desc',
 * although the priorities decide before the score does.
 */
export function boardOrderingKey(ordering: string | undefined, policy: string | null | undefined, weights?: Pick<OpenAIEvalPolicyWeights, 'absolute_priorities'> | null): string {
  if (policy === 'cost_first' || policy === 'stability_first') return 'price_asc'
  if (policy === 'custom_balance' && absolutePriorities(weights).length) return 'priorities_then_score'
  return orderingKey(ordering)
}

export const THRESHOLD_REASONS = ['error_rate_threshold', 'ttft_threshold'] as const
export type ThresholdReason = typeof THRESHOLD_REASONS[number]

/** Known reason codes for an i18n key; anything else is shown with its raw code. */
export function thresholdReasonKey(code: string): ThresholdReason | 'other' {
  return (THRESHOLD_REASONS as readonly string[]).includes(code) ? code as ThresholdReason : 'other'
}

export const RUNTIME_RECOVERY_STATES = ['waiting', 'ready', 'in_flight'] as const
export type RuntimeRecoveryState = typeof RUNTIME_RECOVERY_STATES[number]

export interface RuntimeRecoveryView {
  /** 'other' is a state this page does not know; it is shown with its raw code. */
  state: RuntimeRecoveryState | 'other'
  code: string
  nextTrialAt: string | null
}

/**
 * The scheduled-recovery state of a threshold move-back, or null when the
 * server sent none. Every state, 'ready' included, still means the account is
 * over its threshold: none of them is a recovery or a healthy reading.
 */
export function runtimeRecoveryView(factors: Pick<OpenAIEvalRankingFactors, 'runtime_recovery'> | null | undefined): RuntimeRecoveryView | null {
  const value = factors?.runtime_recovery
  if (!value || typeof value !== 'object') return null
  const code = typeof value.state === 'string' ? value.state : ''
  const state = (RUNTIME_RECOVERY_STATES as readonly string[]).includes(code) ? code as RuntimeRecoveryState : 'other'
  const nextTrialAt = typeof value.next_trial_at === 'string' && value.next_trial_at ? value.next_trial_at : null
  return { state, code, nextTrialAt }
}

/**
 * Quality states that mean "not assessable". They are never rendered as a
 * pass rate: missing integrity evidence is unknown, never healthy.
 */
export function isQualityAssessed(state: string | undefined): boolean {
  return state === 'assessed'
}

/** The published pass rate, only when the server marked the route assessed. */
export function assessedQualityRatio(factors: OpenAIEvalRankingFactors | undefined): number | null {
  if (!factors || !isQualityAssessed(factors.quality.state)) return null
  return factors.quality.ratio
}

/** (pass + suspected pass) / selected test types, for the "1 of 2 tests" hint. */
export function qualityTestCounts(factors: OpenAIEvalRankingFactors | undefined): { passed: number; selected: number; evaluated: number } {
  return {
    passed: (factors?.quality.pass ?? 0) + (factors?.quality.suspected_pass ?? 0),
    selected: factors?.quality.selected ?? 0,
    evaluated: factors?.quality.evaluated ?? 0
  }
}

// ---------------------------------------------------------------------------
// Evaluation generations and paging
// ---------------------------------------------------------------------------

/** A published summary that no longer describes the loaded configuration. */
export function isStaleEvaluation(summaryRevision: number | null | undefined, currentRevision: number | null | undefined): boolean {
  if (summaryRevision == null || currentRevision == null) return false
  return summaryRevision !== currentRevision
}

/** Deduplicates ranked rows by account so a boundary row never appears twice. */
export function mergeRankedAccounts<T extends { account_id: number }>(existing: T[], incoming: T[]): T[] {
  const seen = new Set(existing.map(item => item.account_id))
  return [...existing, ...incoming.filter(item => !seen.has(item.account_id))]
}

// ---------------------------------------------------------------------------
// Account overview (rc4)
//
// One row per unique account. The only filter is the group, and it hides rows
// without changing a score or a rank. Pages continue one generation and one
// group filter; anything else restarts the list.
// ---------------------------------------------------------------------------

/** Accounts per page; the server default is 100 and its maximum 500. */
export const OVERVIEW_PAGE_SIZE = 100

/** The generation and group filter a list on screen belongs to. */
export interface OverviewBinding {
  evaluationId: string
  /** null for all groups. */
  groupId: number | null
}

export function sameOverviewBinding(a: OverviewBinding | null, b: OverviewBinding | null): boolean {
  return Boolean(a && b) && a!.evaluationId === b!.evaluationId && a!.groupId === b!.groupId
}

/** How a leaderboard read ended, so a caller never reports a failed read as updated. */
export type RankingReadOutcome = { ok: true; evaluationId: string } | { ok: false; message: string }

/**
 * True when the order puts the pass rate first. The main score cell then shows
 * the pass rate above the operational score, never the score alone.
 */
export function isQualityFirst(ordering: string | null | undefined, policy: OpenAIEvalSchedulingPolicy | null | undefined): boolean {
  return ordering === 'quality_then_score' || (!ordering && policy === 'avoid_degradation')
}

export type FactorSourceKind = 'measured' | 'probe' | 'default' | 'mixed' | 'neutral' | 'unknown' | 'other'

const MEASURED_SOURCES = new Set([
  'measured', 'actual', 'real', 'request', 'requests', 'request_metrics', 'real_requests', 'runtime',
  'scheduled_tests', 'scheduled_quality', 'evaluation', 'quality_evidence', 'load_snapshot', 'account',
  'account_rate', 'oauth_scheduling_rate', 'upstream_reported_rate',
  'request_ewma_account_model_effort', 'request_ttft_account_model_effort', 'macro_evidence'
])
const PROBE_SOURCES = new Set(['probe', 'v1_matched_probe', 'matched_probe', 'probe_estimate'])
const DEFAULT_SOURCES = new Set(['default', 'labeled_default', 'optimistic_default'])
const NEUTRAL_SOURCES = new Set(['neutral', 'legacy_neutral', 'neutral_default'])
const UNKNOWN_SOURCES = new Set(['unknown', 'missing', 'none'])

/** Groups a server source code; an unrecognised code is never promoted to "measured". */
export function sourceKind(source: string | null | undefined): FactorSourceKind | null {
  if (!source) return null
  const code = source.trim().toLowerCase()
  if (MEASURED_SOURCES.has(code)) return 'measured'
  if (PROBE_SOURCES.has(code)) return 'probe'
  if (DEFAULT_SOURCES.has(code)) return 'default'
  if (NEUTRAL_SOURCES.has(code)) return 'neutral'
  if (UNKNOWN_SOURCES.has(code)) return 'unknown'
  return 'other'
}

/**
 * Where a factor value came from, from what the server reports: the labelled
 * default flag, then the factor's own source code, then whether it is known.
 * An unassessed pass rate that still contributed points is the neutral rule.
 * A default is only ever labelled when the server says one was applied, and
 * an unrecognised source is never promoted to "measured".
 */
export function factorSourceKind(
  factor: OpenAIEvalRankingFactorKey,
  factors: OpenAIEvalRankingFactors,
  contributions?: Pick<OpenAIEvalRankingFactorWeights, 'quality'> | null
): FactorSourceKind {
  if (factor === 'quality') {
    if (isQualityAssessed(factors.quality.state) && factors.quality.ratio != null) return 'measured'
    return (contributions?.quality ?? 0) > 0 ? 'neutral' : 'unknown'
  }
  const meta = factors[factor]
  if (meta.default_applied) return meta.known ? 'mixed' : 'default'
  const own = factor === 'price' ? factors.price.source : factor === 'error_rate' ? factors.error_rate.source : null
  const kind = sourceKind(own)
  if (kind === 'default' || kind === 'probe' || kind === 'unknown') return kind
  if (!meta.known) return 'unknown'
  return kind === 'other' ? 'other' : 'measured'
}

/**
 * The account-level kind, refined by the per-model cells it averages. The
 * account error rate is a macro over model cells, so it is "probe" only when
 * every cell was a probe and "mixed" when probes and real requests were mixed.
 */
export function accountFactorSourceKind(
  factor: OpenAIEvalRankingFactorKey,
  factors: OpenAIEvalRankingFactors,
  models: Pick<OpenAIEvalOverviewModel, 'factors'>[],
  contributions?: Pick<OpenAIEvalRankingFactorWeights, 'quality'> | null
): FactorSourceKind {
  const own = factorSourceKind(factor, factors, contributions)
  if (factor !== 'error_rate' && factor !== 'ttft') return own
  if (own !== 'measured' || !models.length) return own
  const kinds = new Set(models.map(model => factorSourceKind(factor, model.factors)))
  if (kinds.size === 1) return [...kinds][0]
  return 'mixed'
}

/** The raw reading behind a factor, or null. A missing reading is never 0. */
export function factorRawValue(factor: OpenAIEvalRankingFactorKey, factors: OpenAIEvalRankingFactors): number | null {
  switch (factor) {
    case 'price':
      return factors.price.rate_multiplier ?? null
    case 'error_rate':
      return factors.error_rate.value ?? null
    case 'ttft':
      return factors.ttft.ms ?? null
    case 'load':
      return factors.load.load_rate ?? null
    case 'quality':
      return assessedQualityRatio(factors)
  }
}

/**
 * Model rules that override the default policy for a model this account has
 * evidence for. They stay authoritative at request time, so each one is shown
 * as an exception to the overview order.
 */
export function ruleExceptionsFor(
  rules: OpenAIEvalSchedulingPolicyRule[],
  models: Pick<OpenAIEvalOverviewModel, 'requested_model' | 'reasoning_effort'>[],
  defaultPolicy: OpenAIEvalSchedulingPolicy
): OpenAIEvalSchedulingPolicyRule[] {
  return rules.filter(rule => {
    if (!rule.requested_model || rule.policy === defaultPolicy && rule.policy !== 'custom_balance') return false
    const model = rule.requested_model.toLowerCase()
    const effort = (rule.reasoning_effort || '').toLowerCase()
    return models.some(item => item.requested_model.toLowerCase() === model && (!effort || (item.reasoning_effort || '').toLowerCase() === effort))
  })
}

/** A reclaimed generation answers 409 RANKING_SNAPSHOT_CHANGED. */
export function isRankingSnapshotChanged(error: unknown): boolean {
  const e = error as { status?: number; code?: unknown; response?: { status?: number } } | null
  if (!e || typeof e !== 'object') return false
  return e.code === 'RANKING_SNAPSHOT_CHANGED' || e.status === 409 || e.response?.status === 409
}

/**
 * The ranking endpoints answer `{ error, code }` without a message, so the
 * readable text is in `error`; the client's generic message is the fallback.
 */
export function rankingErrorText(error: unknown, fallback: string): string {
  const e = error as { error?: unknown; message?: unknown; response?: { data?: { error?: unknown; message?: unknown } } } | null
  const candidates = [e?.error, e?.response?.data?.error, e?.message, e?.response?.data?.message]
  const text = candidates.find((value): value is string => typeof value === 'string' && value.trim() !== '')
  return text ?? fallback
}

// ---------------------------------------------------------------------------
// Actual dispatch readings
//
// Ranked dispatch records the published factors as nested objects. Older
// traces carry flat scalars, which the server omitted when they were zero or
// unmeasured. The nested factor wins whenever it is present; the scalar is
// read only for a trace that has no nested factor. Missing evidence is
// "unknown", never a zero.
// ---------------------------------------------------------------------------

export type DispatchFactor = 'price' | 'error_rate' | 'ttft' | 'load'

export type DispatchReading =
  | { kind: 'value'; value: number }
  | { kind: 'unknown'; reason: string | null }
  /** A historical excluded candidate that was never measured. */
  | { kind: 'none' }

export function dispatchFactorReading(candidate: SchedulerDecisionCandidate, factor: DispatchFactor): DispatchReading {
  const factors = candidate.factors
  const nested = factors?.[factor]
  if (nested) {
    const raw = factor === 'price'
      ? factors!.price.rate_multiplier
      : factor === 'error_rate'
        ? factors!.error_rate.value
        : factor === 'ttft'
          ? factors!.ttft.ms
          : factors!.load.load_rate
    if (nested.known && raw != null) return { kind: 'value', value: raw }
    return { kind: 'unknown', reason: nested.unknown_reason ?? null }
  }
  const scalar = factor === 'price'
    ? candidate.rate_multiplier
    : factor === 'error_rate'
      ? candidate.error_rate
      : factor === 'ttft'
        ? candidate.ttft_ms
        : candidate.load_rate
  if (factor === 'price') return scalar != null ? { kind: 'value', value: scalar } : { kind: 'none' }
  // Historical traces only measured candidates that could serve the request.
  if (!candidate.eligible) return { kind: 'none' }
  return scalar != null ? { kind: 'value', value: scalar } : { kind: 'unknown', reason: null }
}

export type DispatchQuality =
  | { kind: 'assessed'; ratio: number; pass: number; suspected: number; evaluated: number }
  | { kind: 'unknown'; reason: string | null }
  | { kind: 'none' }

/**
 * The pass rate a dispatch candidate was ranked with. Only an assessed state
 * with a published ratio is a pass rate; anything else is unknown. A neutral
 * scoring contribution is never turned into a ratio.
 *
 * `quality_basis` decides what the counters mean, so an 'account_prior' tier
 * is unknown here even if a payload also carried counters: that tier came from
 * another model's or effort's evidence, and showing it as this model and
 * effort's pass rate would report a reference as a measurement.
 */
export function dispatchQuality(candidate: SchedulerDecisionCandidate): DispatchQuality {
  const quality = candidate.factors?.quality
  if (quality) {
    // The reference is the reason there is no measurement, so it is reported as
    // unknown pass-rate evidence rather than as this model's assessed state.
    if (candidate.quality_basis === 'account_prior') return { kind: 'unknown', reason: 'quality_unknown' }
    if (isQualityAssessed(quality.state) && quality.ratio != null) {
      return { kind: 'assessed', ratio: quality.ratio, pass: quality.pass ?? 0, suspected: quality.suspected_pass ?? 0, evaluated: quality.selected || quality.evaluated || 0 }
    }
    return { kind: 'unknown', reason: quality.unknown_reason ?? (quality.state ? `quality_${quality.state}` : null) }
  }
  if (candidate.quality_basis === 'account_prior') return candidate.eligible ? { kind: 'unknown', reason: 'quality_unknown' } : { kind: 'none' }
  if (candidate.quality_state !== 'unassessed' && candidate.quality_ratio != null && Number(candidate.evaluated_count) > 0) {
    return { kind: 'assessed', ratio: candidate.quality_ratio, pass: candidate.pass_count ?? 0, suspected: candidate.suspected_pass_count ?? 0, evaluated: Number(candidate.evaluated_count) }
  }
  return candidate.eligible ? { kind: 'unknown', reason: null } : { kind: 'none' }
}

/**
 * Points of 100 an unassessed pass rate still contributed. Custom balance
 * scores unknown evidence at the neutral midpoint, which is a scoring rule,
 * not a measured 50 % pass rate.
 */
export function neutralQualityContribution(contributions: Pick<OpenAIEvalRankingFactorWeights, 'quality'> | null | undefined, assessed: boolean): number | null {
  if (assessed) return null
  const value = contributions?.quality
  return value != null && value > 0 ? value : null
}

export interface AccountQualityReference {
  ratio: number
  evaluationId: string
  evaluatedAt: string
  /**
   * Supplied by the server, already capped at the earlier of the evidence
   * expiry and the evaluation deadline. Displayed as given, never recomputed.
   */
  expiresAt: string
  /** Model/effort pairs the reference was averaged from, formatted "model/effort". */
  sourceModels: string[]
}

/**
 * The separate account-wide quality reference a tier was ordered by, or null.
 *
 * Only the server's own `account_prior` basis produces this. It is deliberately
 * not read from `quality_ratio` or `quality_state`: under an account-wide
 * reference those stay unknown for the requested model and effort, and a
 * browser that filled them in would turn an account-wide figure into a measured
 * pass rate. The reference itself can come from the same model at a different
 * effort, or from a different model, so its sources are always shown with it.
 * Zero is a real reference and is kept; a missing ratio is not.
 */
export function accountQualityReference(candidate: Pick<SchedulerDecisionCandidate, 'quality_basis' | 'account_quality_prior'>): AccountQualityReference | null {
  if (candidate.quality_basis !== 'account_prior') return null
  const prior = candidate.account_quality_prior
  if (!prior || typeof prior.ratio !== 'number' || !Number.isFinite(prior.ratio)) return null
  return {
    ratio: prior.ratio,
    evaluationId: prior.evaluation_id,
    evaluatedAt: prior.evaluated_at,
    expiresAt: prior.expires_at,
    sourceModels: Array.isArray(prior.source_models) ? prior.source_models : []
  }
}

// -- Automatic-test background controls ---------------------------------------

/**
 * Starting values when an administrator opts into the experimental limits:
 * 60 sends per hour, one send per minute, one background run at a time and a
 * 15-minute sampling window. They are a pressure limit, not a known upstream
 * safety threshold, and are only applied while budget_enabled is true.
 */
export const BACKGROUND_DEFAULTS = {
  max_requests_per_hour: 60,
  min_send_interval_seconds: 60,
  max_background_concurrency: 1,
  sampling_window_seconds: 15 * 60
} as const

export type BackgroundLimitKey = keyof typeof BACKGROUND_DEFAULTS

/** Accepted ranges; the server stays authoritative and reports its own limits. */
export const BACKGROUND_LIMITS: Record<BackgroundLimitKey, { min: number; max: number }> = {
  max_requests_per_hour: { min: 1, max: 1_000_000 },
  // At least 1 s: the server turns 0 into the 60 s default while limits are on,
  // so 0 would read as "no interval" but behave as one minute.
  min_send_interval_seconds: { min: 1, max: DAY },
  max_background_concurrency: { min: 1, max: 1000 },
  sampling_window_seconds: { min: 60, max: DAY }
}

export const BACKGROUND_LIMIT_KEYS = Object.keys(BACKGROUND_DEFAULTS) as BackgroundLimitKey[]

/** Pause lengths offered in the pause dialog, in seconds. */
export const PAUSE_DURATIONS = [HOUR, 6 * HOUR, DAY, 3 * DAY, 7 * DAY]

export function defaultBackgroundControl(accountID: number): OpenAIEvalBackgroundControl {
  return { account_id: accountID, paused_until: null, pause_reason: '', budget_enabled: false, ...BACKGROUND_DEFAULTS }
}

/** An integer inside the field's range, or null when the entry is not a number. */
export function parseBackgroundLimit(key: BackgroundLimitKey, value: unknown): number | null {
  if (value === '' || value === null || value === undefined) return null
  const raw = Number(value)
  if (!Number.isFinite(raw) || !Number.isInteger(raw)) return null
  const { min, max } = BACKGROUND_LIMITS[key]
  return raw < min || raw > max ? null : raw
}

function limitOrDefault(key: BackgroundLimitKey, value: unknown): number {
  return parseBackgroundLimit(key, value) ?? BACKGROUND_DEFAULTS[key]
}

/** The saved part of a control; runtime counters are read-only and never sent. */
export function backgroundControlPayload(control: OpenAIEvalBackgroundControl): OpenAIEvalBackgroundControl {
  const pausedUntil = control.paused_until || null
  return {
    account_id: control.account_id,
    paused_until: pausedUntil,
    pause_reason: pausedUntil ? (control.pause_reason ?? '').trim() : '',
    budget_enabled: control.budget_enabled === true,
    max_requests_per_hour: limitOrDefault('max_requests_per_hour', control.max_requests_per_hour),
    min_send_interval_seconds: limitOrDefault('min_send_interval_seconds', control.min_send_interval_seconds),
    max_background_concurrency: limitOrDefault('max_background_concurrency', control.max_background_concurrency),
    sampling_window_seconds: limitOrDefault('sampling_window_seconds', control.sampling_window_seconds)
  }
}

/** Copies a control as loaded, keeping its runtime for display only. */
export function cloneBackgroundControl(control: OpenAIEvalBackgroundControl): OpenAIEvalBackgroundControl {
  return { ...control, runtime: control.runtime ? { ...control.runtime } : control.runtime }
}

export function findBackgroundControl(controls: OpenAIEvalBackgroundControl[] | undefined, accountID: number): OpenAIEvalBackgroundControl | undefined {
  return controls?.find(control => control.account_id === accountID)
}

/** A pause whose end time has passed (or cannot be read) no longer applies. */
export function pausedUntil(control: Pick<OpenAIEvalBackgroundControl, 'paused_until'> | null | undefined, now = Date.now()): Date | null {
  if (!control?.paused_until) return null
  const until = new Date(control.paused_until)
  return Number.isNaN(until.getTime()) || until.getTime() <= now ? null : until
}

/** True when the saved and edited controls differ in what would be sent. */
export function sameBackgroundControl(a: OpenAIEvalBackgroundControl | undefined, b: OpenAIEvalBackgroundControl | undefined): boolean {
  if (!a || !b) return !a && !b
  return JSON.stringify(backgroundControlPayload(a)) === JSON.stringify(backgroundControlPayload(b))
}

export type FeasibilityLimit = 'interval' | 'hourly' | 'rpm'

export interface RunFeasibility {
  /** Planned sends for one run, before retries. */
  nominal: number
  /** Sends if every sample used all of its attempts. */
  maxWithRetries: number
  /** Most sends the limits allow inside one sampling window. */
  capacity: number
  /** Which limit sets the capacity. */
  limitedBy: FeasibilityLimit
  /** Shortest time the nominal sends need under the send interval. */
  minDurationSeconds: number
  /** Whole run fits the window; otherwise the server will not start it. */
  fits: boolean
}

/**
 * Whole-run admission estimate, mirroring openAIEvalBudgetFeasible on the
 * server: the nominal sends must fit the hourly budget (n ≤ hourly) and the
 * time they need, max((n − 1) × interval, floor((n − 1) / rpm) × 60), must be
 * shorter than the sampling window. Retries need spare budget but are not part
 * of admission. Sends already made this hour are not known here; the server's
 * decision, which also counts them, wins.
 */
export function runFeasibility(
  nominal: number,
  maxWithRetries: number,
  control: Pick<OpenAIEvalBackgroundControl, BackgroundLimitKey>,
  rpmLimit?: number | null
): RunFeasibility {
  const window = limitOrDefault('sampling_window_seconds', control.sampling_window_seconds)
  const interval = limitOrDefault('min_send_interval_seconds', control.min_send_interval_seconds)
  const hourly = limitOrDefault('max_requests_per_hour', control.max_requests_per_hour)
  const candidates: Array<[FeasibilityLimit, number]> = [
    ['interval', interval > 0 ? Math.ceil(window / interval) : Number.POSITIVE_INFINITY],
    ['hourly', hourly]
  ]
  // floor((n − 1) / rpm) × 60 < window  ⇔  n ≤ rpm × ceil(window / 60)
  const rpm = rpmLimit && rpmLimit > 0 ? Math.trunc(rpmLimit) : 0
  if (rpm) candidates.push(['rpm', rpm * Math.ceil(window / 60)])
  const [limitedBy, capacity] = candidates.reduce((low, item) => (item[1] < low[1] ? item : low))
  const sends = Math.max(0, Math.trunc(nominal))
  const gaps = Math.max(0, sends - 1)
  return {
    nominal: sends,
    maxWithRetries: Math.max(sends, Math.trunc(maxWithRetries)),
    capacity,
    limitedBy,
    minDurationSeconds: Math.max(gaps * interval, rpm ? Math.floor(gaps / rpm) * 60 : 0),
    fits: sends <= capacity
  }
}

/** Planned automatic sends per hour for one account, nominal and with every retry used. */
export function accountHourlyPlan(
  routes: OpenAIEvalRouteConfig[],
  accountID: number,
  maxAttempts: number,
  catalog?: OpenAIEvalModelCatalog | null,
  applies: TestApplicability = defaultApplicability
): { nominal: number; max: number } {
  const own = routes.filter(route => route.account_id === accountID)
  return {
    nominal: totalDailyRequests(own, catalog, applies) / 24,
    max: totalDailyMaxRequests(own, maxAttempts, catalog, applies) / 24
  }
}
