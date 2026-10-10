import { describe, expect, it } from 'vitest'
import type { OpenAIEvalConfig, OpenAIEvalModelCatalog, OpenAIEvalPolicyWeights, OpenAIEvalRouteConfig, OpenAIEvalRun } from '@/api/admin/accounts'
import { reinterpretationText, runExplanation, runStatusLabel } from '../runText'
import { zhT } from './zhT'
import en from '@/i18n/locales/en'
import zhLocale from '@/i18n/locales/zh'
import {
  absolutePriorities,
  conditionDisplayValue,
  conditionStoredValue,
  isValidAccountRuleCondition,
  normalizeAccountPriorityRule,
  policyMeters,
  activeScheduleCount,
  attributionReinterpretation,
  boardOrderingKey,
  candyExtractedAnswer,
  candyFullReply,
  customBalanceIssue,
  customBalanceIssueKey,
  customBalancePayload,
  customBalanceShares,
  hasNoPositiveWeight,
  isAttributionRun,
  isValidCustomBalance,
  policyOrderKeys,
  runStatusKey,
  dailyRequests,
  exclusionKey,
  MAX_INTERVAL_SECONDS,
  maxScheduleJitterSeconds,
  newRoute,
  normalizeCustomBalance,
  normalizeRoute,
  normalizeSchedule,
  orderCandidates,
  requestsPerRun,
  resultTone,
  toSavePayload,
  accountPriorityRuleIssues,
  totalDailyRequests,
  candyExpectedAnswer,
  CUSTOM_FACTORS,
  foldLegacyStability,
  isDirectOAuthAccount,
  isDirectOAuthRoute,
  isHistoricalDataVersion,
  DEFAULT_SCHEDULING_THRESHOLDS,
  THRESHOLD_POLICIES,
  errorRatePercentText,
  invalidThresholdFields,
  isValidRecoveryInterval,
  normalizeSchedulingThresholds,
  parseThresholdInput,
  RECOVERY_INTERVALS,
  maxRequestsPerRun,
  normalizeMaxRequestAttempts,
  stateProbeChains,
  stateProbeMaxRequests,
  totalDailyMaxRequests,
  redactSecrets,
  runSamples,
  sampleFailed,
  sampleAnswer,
  sampleState
} from '../modelIntegrity'

const catalog = {
  items: [{ id: 'gpt-5' }],
  baseline_version: 'v1',
  baseline_models: [],
  candy: { expected_answer: 29, confidence: 'low', scheduling: 'alert_only' },
  evaluation_notice: '',
  reasoning_efforts: ['', 'high'],
  fingerprint_modes: [{ id: 'quick', samples: 60 }, { id: 'standard', samples: 200 }, { id: 'strict', samples: 400 }],
  modeltrace: { requests: 3, bank_revision: 'x', candidate_count: 16, scheduling: 'alert_only' }
} satisfies OpenAIEvalModelCatalog

function oauthRoute(overrides: Partial<OpenAIEvalRouteConfig> = {}): OpenAIEvalRouteConfig {
  return { ...newRoute(1, 'gpt-5', ''), direct_oauth_eligible: true, ...overrides }
}

function enT(key: string, params: Record<string, unknown> = {}): string {
  let current: unknown = en
  for (const segment of key.split('.')) current = current && typeof current === 'object' ? (current as Record<string, unknown>)[segment] : undefined
  if (typeof current !== 'string') return key
  return current.replace(/\{(\w+)\}/g, (_, name: string) => (name in params ? String(params[name]) : `{${name}}`))
}

describe('request budget', () => {
  it('uses the fixed per-run request counts the backend enforces', () => {
    const route = oauthRoute()
    expect(requestsPerRun(route, 'candy', catalog)).toBe(1)
    route.candy_schedule.sample_count = 5
    expect(requestsPerRun(route, 'candy', catalog)).toBe(5)
    expect(requestsPerRun(route, 'state_probe', catalog)).toBe(2)
    expect(requestsPerRun(route, 'modeltrace', catalog)).toBe(3)
    expect(requestsPerRun(route, 'fingerprint', catalog)).toBe(60)
    expect(requestsPerRun(route, 'fingerprint', catalog, 'strict')).toBe(400)
  })

  it('estimates daily requests only for enabled schedules', () => {
    const route = oauthRoute()
    expect(dailyRequests(route, 'candy', catalog)).toBe(0)
    route.candy_schedule.enabled = true
    route.candy_schedule.interval_seconds = 900
    expect(dailyRequests(route, 'candy', catalog)).toBe(96)
    route.fingerprint_schedule.enabled = true
    route.fingerprint_schedule.sample_mode = 'standard'
    route.fingerprint_schedule.interval_seconds = 86400
    expect(totalDailyRequests([route], catalog)).toBe(296)
    expect(activeScheduleCount([route])).toBe(2)
  })

  it('does not count State Probe schedules the server will never run', () => {
    const route = oauthRoute({ direct_oauth_eligible: false })
    route.state_probe_schedule.enabled = true
    expect(dailyRequests(route, 'state_probe', catalog)).toBe(0)
    expect(activeScheduleCount([route])).toBe(0)
  })
})

describe('schedule normalisation', () => {
  it('clamps interval to the per-type minimum and additive jitter to half the interval or one hour', () => {
    const schedule = { enabled: true, interval_seconds: 60, jitter_seconds: 999999 }
    normalizeSchedule(schedule, 'candy')
    expect(schedule.interval_seconds).toBe(300)
    expect(schedule.jitter_seconds).toBe(150)
    const fingerprint = { enabled: true, interval_seconds: 3 * 86400, jitter_seconds: 10 * 86400 }
    normalizeSchedule(fingerprint, 'fingerprint')
    expect(fingerprint.jitter_seconds).toBe(3600)
    expect((fingerprint as { sample_mode?: string }).sample_mode).toBe('quick')
  })

  it('caps custom intervals only at the database integer storage boundary', () => {
    const schedule = { enabled: true, interval_seconds: MAX_INTERVAL_SECONDS + 1, jitter_seconds: 3600 }
    normalizeSchedule(schedule, 'candy')
    expect(schedule.interval_seconds).toBe(MAX_INTERVAL_SECONDS)
    expect(schedule.jitter_seconds).toBe(0)
    expect(maxScheduleJitterSeconds(MAX_INTERVAL_SECONDS - 60)).toBe(60)
  })
})

describe('custom balance compatibility', () => {
  it('replaces an old all-zero backend object with the saveable defaults', () => {
    // The old default (stability 0.3) folded: error 0.25+0.18, ttft 0.15+0.12; quality stays 0.
    expect(normalizeCustomBalance({ cost: 0, stability: 0, error_rate: 0, ttft: 0, load: 0 })).toEqual({
      cost: 0.2,
      error_rate: 0.43,
      ttft: 0.27,
      load: 0.1,
      quality: 0,
      stability: 0
    })
  })

  it('folds a legacy stability weight into error rate and first-token latency, once', () => {
    const legacy = { cost: 0.5, stability: 0.2, error_rate: 0.1, ttft: 0.1, load: 0.1 }
    const folded = normalizeCustomBalance(legacy)
    expect(folded).toEqual({ cost: 0.5, error_rate: 0.22, ttft: 0.18, load: 0.1, quality: 0, stability: 0 })
    expect(normalizeCustomBalance(folded)).toEqual(folded)
    expect(foldLegacyStability(foldLegacyStability(legacy))).toEqual(folded)
    // Relative ranking weight is unchanged: the scheduler already split stability 60/40.
    expect(folded.error_rate + folded.ttft).toBeCloseTo(legacy.stability + legacy.error_rate + legacy.ttft)
  })

  it('keeps an explicit quality weight and accepts quality as the only factor', () => {
    expect(normalizeCustomBalance({ cost: 0, error_rate: 0, ttft: 0, load: 0, quality: 1 })).toEqual({ cost: 0, error_rate: 0, ttft: 0, load: 0, quality: 1, stability: 0 })
    expect(isValidCustomBalance({ cost: 0, error_rate: 0, ttft: 0, load: 0, quality: 1 })).toBe(true)
    expect(customBalanceShares({ cost: 0, error_rate: 0, ttft: 0, load: 0, quality: 1 })).toEqual({ cost: 0, error_rate: 0, ttft: 0, load: 0, quality: 100 })
    expect(CUSTOM_FACTORS).toEqual(['cost', 'error_rate', 'ttft', 'load', 'quality'])
  })

  it('keeps API-set absolute priorities when the weights are normalized for saving', () => {
    const weights = { cost: 1, error_rate: 1, ttft: 0, load: 0, quality: 1, absolute_priorities: ['quality', 'cost'] }
    expect(normalizeCustomBalance(weights).absolute_priorities).toEqual(['quality', 'cost'])
    expect(normalizeCustomBalance({ cost: 1, error_rate: 0, ttft: 0, load: 0 })).not.toHaveProperty('absolute_priorities')
  })

  it('canonicalizes priorities and always sends the list', () => {
    expect(absolutePriorities({ absolute_priorities: ['Price', 'errors', 'bogus', 'cost', 'latency'] })).toEqual(['cost', 'error_rate', 'ttft'])
    expect(customBalancePayload({ cost: 1, error_rate: 0, ttft: 0, load: 0 }).absolute_priorities).toEqual([])
    expect(customBalancePayload({ cost: 1, error_rate: 0, ttft: 0, load: 0, absolute_priorities: ['quality'] }).absolute_priorities).toEqual(['quality'])
    // Clearing the last priority is sent as an explicit empty list.
    expect(customBalancePayload({ cost: 1, error_rate: 0, ttft: 0, load: 0, absolute_priorities: [] }).absolute_priorities).toEqual([])
  })

  it('keeps all-zero weights with a priority order instead of replacing them with the defaults', () => {
    const zeroWithPriority = { cost: 0, error_rate: 0, ttft: 0, load: 0, quality: 0, absolute_priorities: ['load'] as OpenAIEvalPolicyWeights['absolute_priorities'] }
    expect(normalizeCustomBalance(zeroWithPriority)).toEqual({ cost: 0, error_rate: 0, ttft: 0, load: 0, quality: 0, stability: 0, absolute_priorities: ['load'] })
    expect(customBalancePayload(zeroWithPriority)).toEqual({ cost: 0, error_rate: 0, ttft: 0, load: 0, quality: 0, stability: 0, absolute_priorities: ['load'] })
    expect(customBalanceShares(zeroWithPriority)).toEqual({ cost: 0, error_rate: 0, ttft: 0, load: 0, quality: 0 })
    // Without a priority, an all-zero set is still the old unsaveable object.
    expect(normalizeCustomBalance({ cost: 0, error_rate: 0, ttft: 0, load: 0, absolute_priorities: [] })).toEqual({ cost: 0.2, error_rate: 0.43, ttft: 0.27, load: 0.1, quality: 0, stability: 0 })
    // An unknown name alone is no priority.
    expect(normalizeCustomBalance({ cost: 0, error_rate: 0, ttft: 0, load: 0, absolute_priorities: ['bogus'] }).cost).toBe(0.2)
    // A negative value is never kept, priorities or not.
    expect(normalizeCustomBalance({ cost: -1, error_rate: 0, ttft: 0, load: 0, absolute_priorities: ['cost'] })).toEqual({ cost: 0.2, error_rate: 0.43, ttft: 0.27, load: 0.1, quality: 0, stability: 0, absolute_priorities: ['cost'] })
  })

  it('explains the all-zero rule in both locales, without the old positive-weight requirement', () => {
    const key = (issue: 'invalid_value' | 'zero_total') => customBalanceIssueKey(issue)
    expect(key('zero_total')).toBe('admin.modelIntegrity.scheduling.policy.custom.zeroTotal')
    expect(key('invalid_value')).toBe('admin.modelIntegrity.scheduling.policy.custom.zeroTotal')
    expect(enT(key('zero_total'))).toBe('Weights must add up to more than 0. Set at least one weight, or add a priority.')
    expect(zhT(key('zero_total'))).toBe('权重合计须大于 0，请至少为一项设置权重，或添加优先因素。')
    for (const locale of [en, zhLocale] as unknown as Record<string, any>[]) {
      const copy = locale.admin.modelIntegrity.scheduling.policy.custom.priorities
      expect(copy).not.toHaveProperty('needsTiebreak')
      for (const name of ['then', 'thenAccountOrder', 'summary', 'summaryAccountOrder', 'rules']) expect(typeof copy[name]).toBe('string')
    }
    expect(enT('admin.modelIntegrity.scheduling.policy.custom.priorities.rules')).toContain('if every weight is 0%, accounts that tie on every priority are ordered by account ID')
    expect(zhT('admin.modelIntegrity.scheduling.policy.custom.priorities.rules')).toContain('全部为 0% 时，所有优先因素都相同的账号按账号 ID 排序')
  })

  it('accepts every weight at 0 once there is a priority order', () => {
    expect(customBalanceIssue({ cost: 0, error_rate: 1, ttft: 0, load: 0, absolute_priorities: ['cost'] })).toBeNull()
    expect(customBalanceIssue({ cost: 0, error_rate: 0, ttft: 0, load: 0, absolute_priorities: ['cost'] })).toBeNull()
    expect(isValidCustomBalance({ cost: 0, error_rate: 0, ttft: 0, load: 0, quality: 0, absolute_priorities: ['quality', 'cost'] })).toBe(true)
    expect(customBalanceIssue({ cost: 0, error_rate: 0, ttft: 0, load: 0 })).toBe('zero_total')
    expect(customBalanceIssue({ cost: 0, error_rate: 0, ttft: 0, load: 0, absolute_priorities: [] })).toBe('zero_total')
    expect(customBalanceIssue({ cost: -1, error_rate: 1, ttft: 0, load: 0, absolute_priorities: ['cost'] })).toBe('invalid_value')
    expect(hasNoPositiveWeight({ cost: 0, error_rate: 0, ttft: 0, load: 0, absolute_priorities: ['cost'] })).toBe(true)
    // A legacy stability weight is a positive weight once folded.
    expect(hasNoPositiveWeight({ cost: 0, stability: 0.5, error_rate: 0, ttft: 0, load: 0 })).toBe(false)
  })

  it('describes custom balance order from its priorities', () => {
    expect(policyOrderKeys('custom_balance', null)).toEqual(['weighted_score'])
    expect(policyOrderKeys('custom_balance', { absolute_priorities: ['cost', 'error_rate'] })).toEqual(['priority.cost', 'priority.error_rate', 'weighted_tiebreak'])
    expect(policyOrderKeys('custom_balance', { cost: 0, error_rate: 1, ttft: 0, load: 0, absolute_priorities: ['cost'] })).toEqual(['priority.cost', 'weighted_tiebreak'])
    // No weight above 0 leaves no score, so account order breaks the remaining ties.
    expect(policyOrderKeys('custom_balance', { cost: 0, error_rate: 0, ttft: 0, load: 0, absolute_priorities: ['cost'] })).toEqual(['priority.cost', 'account_order'])
    expect(enT('admin.modelIntegrity.scheduling.policy.order.account_order')).toBe('Account ID order, for any tie left')
    expect(zhT('admin.modelIntegrity.scheduling.policy.order.account_order')).toBe('仍相同时，按账号 ID 排序')
    expect(policyOrderKeys('cost_first', { absolute_priorities: ['cost'] })).toEqual(['within_thresholds', 'price'])
    expect(boardOrderingKey('score_desc', 'custom_balance', { absolute_priorities: ['quality'] })).toBe('priorities_then_score')
    expect(boardOrderingKey('score_desc', 'custom_balance', {})).toBe('score_desc')
  })
})

describe('retired BPS compatibility', () => {
  it('fills missing schedules on routes saved by older versions', () => {
    const legacy = { account_id: 3, requested_model: 'gpt-5', reasoning_effort: '', candy_schedule: { enabled: true, interval_seconds: 3600, jitter_seconds: 0 }, fingerprint_schedule: { enabled: false, interval_seconds: 86400, jitter_seconds: 0 }, bps_auto: true } as unknown as OpenAIEvalRouteConfig
    const route = normalizeRoute(legacy)
    expect(route.modeltrace_schedule.interval_seconds).toBe(300)
    expect(route.state_probe_schedule.interval_seconds).toBe(300)
    expect(route.bps_mode).toBe('force_off')
  })
})

describe('save payload', () => {
  it('unifies the GPT-6 alias as Astra without changing distinct Sol or custom models', () => {
    const rule = { account_id: 3, priority: 1, requested_models: [' GPT-6 ', 'gpt-6-astra', 'gpt-6-sol', 'custom-astra'] }
    expect(normalizeAccountPriorityRule(rule).requested_models).toEqual(['gpt-6-astra', 'gpt-6-sol', 'custom-astra'])
    const payload = toSavePayload({ effects_enabled: false, bps_auto_enabled: false, accounts: [], account_priority_rules: [rule] })
    expect(payload.account_priority_rules![0].requested_models).toEqual(['gpt-6-astra', 'gpt-6-sol', 'custom-astra'])
    expect([...accountPriorityRuleIssues([
      { account_id: 3, priority: 1, requested_models: ['gpt-6'] },
      { account_id: 3, priority: 2, requested_models: ['gpt-6-astra'] }
    ], () => false).entries()]).toEqual([[1, 'overlap']])
  })

  it('unifies the GPT-5.6 alias in account rules on load and save', () => {
    const rule = { account_id: 3, priority: 1, requested_models: [' GPT-5.6 ', 'gpt-5.6-sol', 'custom-sol'] }
    expect(normalizeAccountPriorityRule(rule).requested_models).toEqual(['gpt-5.6-sol', 'custom-sol'])
    const payload = toSavePayload({ effects_enabled: false, bps_auto_enabled: false, accounts: [], account_priority_rules: [rule] })
    expect(payload.account_priority_rules![0].requested_models).toEqual(['gpt-5.6-sol', 'custom-sol'])
    expect([...accountPriorityRuleIssues([
      { account_id: 3, priority: 1, requested_models: ['gpt-5.6'] },
      { account_id: 3, priority: 2, requested_models: ['gpt-5.6-sol'] }
    ], () => false).entries()]).toEqual([[1, 'overlap']])
  })

  it('sends account priority rules with every model omitted as all models, keeping disabled ones', () => {
    const payload = toSavePayload({
      effects_enabled: false,
      bps_auto_enabled: false,
      accounts: [],
      account_priority_rules: [
        { account_id: 3, priority: 0, requested_models: [] },
        { account_id: 4, priority: -2, requested_models: [' gpt-5 ', ''], enabled: false }
      ]
    })
    expect(payload.account_priority_rules).toEqual([
      { account_id: 3, priority: 0, enabled: true, condition: null },
      { account_id: 4, priority: -2, requested_models: ['gpt-5'], enabled: false, condition: null }
    ])
    // A config from an older server has no rules and saves none.
    expect(toSavePayload({ effects_enabled: false, bps_auto_enabled: false, accounts: [] }).account_priority_rules).toEqual([])
  })

  it('flags account rules the server would reject, ignoring disabled rules for overlaps', () => {
    const issues = accountPriorityRuleIssues([
      { account_id: 0, priority: 1 },
      { account_id: 5, priority: 1.5 },
      { account_id: 6, priority: 1, requested_models: ['gpt-5'] },
      { account_id: 6, priority: 2, requested_models: ['GPT-5'], enabled: false },
      { account_id: 6, priority: 3, requested_models: ['gpt-5', 'gpt-5-mini'] },
      { account_id: 7, priority: 1, requested_models: [] }
    ], rule => rule.account_id === 7)
    expect([...issues.entries()]).toEqual([[0, 'account'], [1, 'priority'], [4, 'overlap'], [5, 'models']])
  })

  it('keeps rule conditions through load and save, and sends unconditional rules as an explicit null', () => {
    const loaded = normalizeAccountPriorityRule({ account_id: 3, priority: 1, condition: { metric: 'error_rate', operator: 'lt', threshold: 0.05 } })
    expect(loaded.condition).toEqual({ metric: 'error_rate', operator: 'lt', threshold: 0.05 })
    expect(normalizeAccountPriorityRule({ account_id: 3, priority: 1, condition: null })).not.toHaveProperty('condition')
    const payload = toSavePayload({
      effects_enabled: false,
      bps_auto_enabled: false,
      accounts: [],
      account_priority_rules: [
        loaded,
        { account_id: 4, priority: 2, requested_models: ['gpt-5'], enabled: false, condition: { metric: 'quality_ratio', operator: 'gte', threshold: 1 } },
        { account_id: 5, priority: 3, condition: null }
      ]
    })
    expect(payload.account_priority_rules).toEqual([
      { account_id: 3, priority: 1, enabled: true, condition: { metric: 'error_rate', operator: 'lt', threshold: 0.05 } },
      { account_id: 4, priority: 2, requested_models: ['gpt-5'], enabled: false, condition: { metric: 'quality_ratio', operator: 'gte', threshold: 1 } },
      // Omitted would make the server keep a stored condition; null clears it.
      { account_id: 5, priority: 3, enabled: true, condition: null }
    ])
    expect(payload.account_priority_rules![2]).toHaveProperty('condition', null)
  })

  it('converts condition values between storage and the editor units', () => {
    expect(conditionDisplayValue('error_rate', 0.05)).toBe(5)
    expect(conditionDisplayValue('quality_ratio', 1)).toBe(100)
    expect(conditionDisplayValue('ttft_ms', 8000)).toBe(8)
    expect(conditionDisplayValue('price', 0.5)).toBe(0.5)
    expect(conditionDisplayValue('load_rate', 80)).toBe(80)
    expect(conditionStoredValue('error_rate', 7)).toBe(0.07)
    expect(conditionStoredValue('ttft_ms', 1.5)).toBe(1500)
    expect(conditionStoredValue('load_rate', 80)).toBe(80)
    expect(conditionStoredValue('error_rate', NaN)).toBeNaN()
  })

  it('validates a condition the way the server does', () => {
    expect(isValidAccountRuleCondition(null)).toBe(true)
    expect(isValidAccountRuleCondition(undefined)).toBe(true)
    expect(isValidAccountRuleCondition({ metric: 'quality_ratio', operator: 'gte', threshold: 1 })).toBe(true)
    expect(isValidAccountRuleCondition({ metric: 'quality_ratio', operator: 'gte', threshold: 1.01 })).toBe(false)
    expect(isValidAccountRuleCondition({ metric: 'error_rate', operator: 'lt', threshold: -0.01 })).toBe(false)
    expect(isValidAccountRuleCondition({ metric: 'load_rate', operator: 'lt', threshold: 100 })).toBe(true)
    expect(isValidAccountRuleCondition({ metric: 'load_rate', operator: 'lt', threshold: 101 })).toBe(false)
    expect(isValidAccountRuleCondition({ metric: 'ttft_ms', operator: 'lte', threshold: 120000 })).toBe(true)
    expect(isValidAccountRuleCondition({ metric: 'price', operator: 'eq', threshold: 0 })).toBe(true)
    // A blank entry is NaN, never coerced to 0.
    expect(isValidAccountRuleCondition({ metric: 'price', operator: 'lte', threshold: NaN })).toBe(false)
    expect(isValidAccountRuleCondition({ metric: 'ttft_ms', operator: 'lte', threshold: Infinity })).toBe(false)
    expect(isValidAccountRuleCondition({ metric: 'speed' as never, operator: 'lte', threshold: 1 })).toBe(false)
    expect(isValidAccountRuleCondition({ metric: 'price', operator: 'ne' as never, threshold: 1 })).toBe(false)
    const issues = accountPriorityRuleIssues([
      { account_id: 1, priority: 1, condition: { metric: 'error_rate', operator: 'lt', threshold: NaN } },
      { account_id: 2, priority: 1, condition: { metric: 'error_rate', operator: 'lt', threshold: 0.05 } }
    ], () => false)
    expect([...issues.entries()]).toEqual([[0, 'condition']])
  })
})

describe('policy meters', () => {
  const roles = (meters: ReturnType<typeof policyMeters>) => Object.fromEntries(meters.map(meter => [meter.factor, `${meter.role}:${meter.level}`]))

  it('draws presets by their real order: price sorts, thresholds gate, pass rate tiers', () => {
    expect(roles(policyMeters('cost_first'))).toEqual({ quality: 'ignored:0', price: 'sort:4', errors: 'gate_standard:2', speed: 'gate_standard:2' })
    // Stability is the same price sort with stricter thresholds, never a weighted error score.
    expect(roles(policyMeters('stability_first'))).toEqual({ quality: 'ignored:0', price: 'sort:4', errors: 'gate_strict:3', speed: 'gate_strict:3' })
    expect(roles(policyMeters('avoid_degradation'))).toEqual({ quality: 'tier:0', price: 'sort_in_tier:3', errors: 'gate_standard:2', speed: 'gate_standard:2' })
    expect(roles(policyMeters(''))).toEqual({ quality: 'ignored:0', price: 'system:1', errors: 'system:2', speed: 'system:2' })
  })

  it('follows the edited thresholds', () => {
    const meters = policyMeters('cost_first', null, {
      cost_first: { error_rate: 0.5, ttft_seconds: 5 },
      stability_first: { error_rate: 0.05, ttft_seconds: 8 },
      avoid_degradation: { error_rate: 0.2, ttft_seconds: 15 },
      custom_balance: { error_rate: 0.2, ttft_seconds: 15 },
      min_error_samples: 10,
      min_ttft_samples: 20
    })
    expect(roles(meters)).toMatchObject({ errors: 'gate_loose:1', speed: 'gate_strict:3' })
    expect(meters.find(meter => meter.factor === 'errors')?.value).toBe(0.5)
  })

  it('shows custom priorities as named steps and weights as shares, listing load only when it counts', () => {
    const plain = policyMeters('custom_balance', { cost: 0.5, error_rate: 0.5, ttft: 0, load: 0, quality: 0 })
    expect(roles(plain)).toEqual({ quality: 'ignored:0', price: 'weight:2', errors: 'weight:2', speed: 'ignored:0' })
    const ranked = policyMeters('custom_balance', { cost: 0, error_rate: 0.6, ttft: 0, load: 0.4, quality: 0, absolute_priorities: ['quality'] })
    expect(roles(ranked)).toEqual({ quality: 'priority:0', price: 'ignored:0', errors: 'weight:3', speed: 'ignored:0', load: 'weight:2' })
    expect(ranked[0].value).toBe(1)
  })

  it('keeps fields the page does not edit and strips read-only runtime state', () => {
    const route = oauthRoute({ bps_mode: 'auto', bps_state: { active: true, degraded_streak: 3, healthy_streak: 0 } })
    const config: OpenAIEvalConfig = {
      revision: 7,
      effects_enabled: true,
      bps_auto_enabled: true,
      scheduling_policy: 'cost_first',
      policies: [{ requested_model: 'gpt-5', policy: 'avoid_degradation' }],
      accounts: [route]
    }
    const payload = toSavePayload(config)
    expect(payload.revision).toBe(7)
    expect(payload.effects_enabled).toBe(true)
    expect(payload.scheduling_policy).toBe('cost_first')
    expect(payload.policies).toEqual([{ requested_model: 'gpt-5', reasoning_effort: '', policy: 'avoid_degradation', enabled: true }])
    expect(payload.accounts[0]).not.toHaveProperty('bps_state')
    expect(payload.accounts[0]).not.toHaveProperty('direct_oauth_eligible')
    expect(payload.accounts[0].bps_mode).toBe('force_off')
    expect(payload.accounts[0].bps_auto).toBe(false)
  })
})

describe('scheduling thresholds', () => {
  it('fills a missing field or value from the defaults and keeps every sent value, including 0', () => {
    expect(normalizeSchedulingThresholds(undefined)).toEqual(DEFAULT_SCHEDULING_THRESHOLDS)
    expect(normalizeSchedulingThresholds(null)).toEqual(DEFAULT_SCHEDULING_THRESHOLDS)
    const partial = normalizeSchedulingThresholds({ cost_first: { error_rate: 0 } as never, min_ttft_samples: 3 })
    expect(partial.cost_first).toEqual({ error_rate: 0, ttft_seconds: 15 })
    expect(partial.stability_first).toEqual({ error_rate: 0.05, ttft_seconds: 8 })
    expect(partial.min_error_samples).toBe(10)
    expect(partial.min_ttft_samples).toBe(3)
    // A copy: editing the result never changes the defaults.
    partial.custom_balance.error_rate = 0.5
    expect(DEFAULT_SCHEDULING_THRESHOLDS.custom_balance.error_rate).toBe(0.2)
  })

  it('reports every value the server would reject', () => {
    expect(invalidThresholdFields(DEFAULT_SCHEDULING_THRESHOLDS)).toEqual([])
    const value = normalizeSchedulingThresholds({
      cost_first: { error_rate: 1, ttft_seconds: 86400 },
      stability_first: { error_rate: 0, ttft_seconds: 0.001 },
      avoid_degradation: { error_rate: 1.01, ttft_seconds: 0 },
      custom_balance: { error_rate: NaN, ttft_seconds: Infinity },
      min_error_samples: 1_000_000,
      min_ttft_samples: 2.5
    })
    expect(invalidThresholdFields(value)).toEqual([
      'avoid_degradation.error_rate',
      'avoid_degradation.ttft_seconds',
      'min_ttft_samples'
    ])
    // An invalid custom_balance value is out of the edited scope, so it neither
    // blocks saving nor is silently corrected.
    expect(value.custom_balance).toEqual({ error_rate: NaN, ttft_seconds: Infinity })
    expect(invalidThresholdFields({ ...DEFAULT_SCHEDULING_THRESHOLDS, min_error_samples: 0 })).toEqual(['min_error_samples'])
    expect(invalidThresholdFields({ ...DEFAULT_SCHEDULING_THRESHOLDS, cost_first: { error_rate: -0.01, ttft_seconds: 86400.5 } })).toEqual(['cost_first.error_rate', 'cost_first.ttft_seconds'])
  })

  it('shows ratios as clean percentages and never reads empty input as 0', () => {
    expect(errorRatePercentText(0.07)).toBe('7')
    expect(errorRatePercentText(0.025)).toBe('2.5')
    expect(errorRatePercentText(0)).toBe('0')
    expect(errorRatePercentText(NaN)).toBe('')
    expect(parseThresholdInput('')).toBeNaN()
    expect(parseThresholdInput('  ')).toBeNaN()
    expect(parseThresholdInput('0')).toBe(0)
    expect(parseThresholdInput(' 12.5 ')).toBe(12.5)
  })

  it('is part of every save payload, defaulting when the config never had it', () => {
    expect(toSavePayload({ effects_enabled: false, bps_auto_enabled: false, accounts: [] }).scheduling_thresholds).toEqual(DEFAULT_SCHEDULING_THRESHOLDS)
    const configured = normalizeSchedulingThresholds({ ...DEFAULT_SCHEDULING_THRESHOLDS, stability_first: { error_rate: 0, ttft_seconds: 3 } })
    expect(toSavePayload({ effects_enabled: false, bps_auto_enabled: false, accounts: [], scheduling_thresholds: configured }).scheduling_thresholds).toEqual(configured)
  })

  it('has the threshold copy in both locales', () => {
    for (const locale of [en, zhLocale] as unknown as Record<string, any>[]) {
      const copy = locale.admin.modelIntegrity.scheduling.thresholds
      for (const key of ['title', 'hint', 'inUse', 'fieldAria', 'zeroNote', 'invalid']) expect(typeof copy[key]).toBe('string')
      for (const key of ['errorRate', 'ttft']) expect(typeof copy.columns[key]).toBe('string')
      for (const key of ['title', 'min_error_samples', 'min_ttft_samples', 'note']) expect(typeof copy.samples[key]).toBe('string')
      for (const key of ['error_rate', 'ttft_seconds', 'samples']) expect(typeof copy.errors[key]).toBe('string')
      expect(typeof copy.units.seconds).toBe('string')
    }
  })

  it('edits only the policies that read a threshold, and keeps custom balance intact', () => {
    // Custom balance is ordered by its weights, so it has no editable row; the
    // schema still round-trips the object for API compatibility.
    expect([...THRESHOLD_POLICIES]).toEqual(['cost_first', 'stability_first', 'avoid_degradation'])
    expect(invalidThresholdFields(DEFAULT_SCHEDULING_THRESHOLDS)).toEqual([])
    const kept = normalizeSchedulingThresholds({ custom_balance: { error_rate: 0.07, ttft_seconds: 30 } })
    expect(kept.custom_balance).toEqual({ error_rate: 0.07, ttft_seconds: 30 })
  })
})

describe('scheduled recovery settings', () => {
  it('defaults an older object to on every 30 minutes, and reads 0 as the default', () => {
    expect(DEFAULT_SCHEDULING_THRESHOLDS.recovery_enabled).toBe(true)
    expect(DEFAULT_SCHEDULING_THRESHOLDS.recovery_interval_seconds).toBe(1800)
    const legacy = normalizeSchedulingThresholds({ min_error_samples: 3 })
    expect(legacy.recovery_enabled).toBe(true)
    expect(legacy.recovery_interval_seconds).toBe(1800)
    expect(normalizeSchedulingThresholds({ recovery_interval_seconds: 0 }).recovery_interval_seconds).toBe(1800)
  })

  it('keeps a configured switch and interval exactly, including off and a non-minute value', () => {
    const configured = normalizeSchedulingThresholds({ recovery_enabled: false, recovery_interval_seconds: 301 })
    expect(configured.recovery_enabled).toBe(false)
    expect(configured.recovery_interval_seconds).toBe(301)
    expect(invalidThresholdFields(configured)).toEqual([])
    expect(toSavePayload({ effects_enabled: false, bps_auto_enabled: false, accounts: [], scheduling_thresholds: configured }).scheduling_thresholds).toEqual(configured)
    // A save from either page carries the defaults when the config never had them.
    expect(toSavePayload({ effects_enabled: false, bps_auto_enabled: false, accounts: [] }).scheduling_thresholds).toMatchObject({ recovery_enabled: true, recovery_interval_seconds: 1800 })
  })

  it('accepts the whole storage range, 300 to 2,147,483,647 seconds, and nothing else', () => {
    expect([...RECOVERY_INTERVALS]).toEqual([300, 600, 1800, 3600, 21600, 43200, 86400])
    for (const value of [300, 301, 1800, 2_147_483_647]) expect(isValidRecoveryInterval(value)).toBe(true)
    for (const value of [299, 0, -300, 300.5, 2_147_483_648, NaN, Infinity, '1800', null]) expect(isValidRecoveryInterval(value)).toBe(false)
    // Out of range is reported, not corrected, so a save cannot change it silently.
    const bad = normalizeSchedulingThresholds({ recovery_interval_seconds: 240 })
    expect(bad.recovery_interval_seconds).toBe(240)
    expect(invalidThresholdFields(bad)).toEqual(['recovery_interval_seconds'])
    // Still checked while switched off: the server stores the interval either way.
    expect(invalidThresholdFields({ ...bad, recovery_enabled: false })).toEqual(['recovery_interval_seconds'])
  })

  it('has the recovery copy in both locales', () => {
    for (const locale of [en, zhLocale] as unknown as Record<string, any>[]) {
      const copy = locale.admin.modelIntegrity.scheduling.thresholds.recovery
      for (const key of ['title', 'toggle', 'interval', 'hint', 'rules', 'off', 'error']) expect(typeof copy[key]).toBe('string')
      expect(copy.error).toContain('{maxSeconds}')
      const board = locale.admin.modelIntegrity.scheduling.board.recovery
      for (const key of ['waiting', 'ready', 'in_flight', 'other', 'next', 'nextRow', 'rowScope', 'note']) expect(typeof board[key]).toBe('string')
      for (const key of ['waiting', 'ready', 'in_flight', 'other']) expect(typeof board.detail[key]).toBe('string')
      expect(typeof locale.admin.modelIntegrity.scheduling.decision.runtime_recovery_trial).toBe('string')
      expect(typeof locale.admin.modelIntegrity.scheduling.candidateReason.runtime_recovery_trial).toBe('string')
    }
  })
})

describe('result tone', () => {
  it('only marks warning/different as needing attention and keeps errors apart from missing evidence', () => {
    expect(resultTone('pass')).toBe('ok')
    expect(resultTone('warning')).toBe('attention')
    expect(resultTone('different')).toBe('attention')
    expect(resultTone('insufficient')).toBe('neutral')
    expect(resultTone('error')).toBe('error')
  })

  it('gives inferred non-Luna attributions their own likely-normal tone and keeps legacy statuses readable', () => {
    expect(resultTone('suspected_normal')).toBe('likely')
    expect(resultTone('attributed')).toBe('neutral')
    expect(resultTone('uncertain')).toBe('neutral')
    expect(resultTone('consistent')).toBe('ok')
  })
})

describe('attribution status labels', () => {
  const base = { id: 1, account_id: 1, requested_model: 'gpt-5', reasoning_effort: '', request_count: 3, input_tokens: 0, output_tokens: 0, duration_ms: 0, started_at: '2026-10-01T00:00:00Z', trigger_source: 'manual' }
  const run = (test_type: OpenAIEvalRun['test_type'], status: string, reason?: string, extra: Partial<OpenAIEvalRun['outcome']> = {}): OpenAIEvalRun => ({
    ...base, test_type, status, outcome: { status, reason, sample_count: 3, expected_count: 3, confidence: 'low', scheduling: 'alert_only', ...extra }
  })

  it('labels a non-Luna ModelTrace prediction as likely normal and says it is inferred', () => {
    const item = run('modeltrace', 'suspected_normal', 'non_luna_behavioral_attribution', { modeltrace: { bank_revision: 'x', prediction: 'gpt-4.1', probability: 0.71, used_outputs: 3, requests: 3 } })
    expect(runStatusKey(item)).toBe('suspected_normal')
    expect(runStatusLabel(zhT, item)).toBe('疑似正常')
    expect(runExplanation(zhT, item)).toBe('归因模型 gpt-4.1，概率 0.71。行为归因结果为非 Luna 模型，判定为疑似正常。该结果为行为推断，不代表实际路由已核实。')
    expect(isAttributionRun(item)).toBe(true)
  })

  it('labels a ModelTrace attribution matching the tested model as normal, still marked as inferred', () => {
    const item = run('modeltrace', 'pass', 'modeltrace_target_match', { modeltrace: { bank_revision: 'x', prediction: 'gpt-5', probability: 0.82, used_outputs: 2, requests: 3 } })
    expect(runStatusKey(item)).toBe('pass')
    expect(runStatusLabel(zhT, item)).toBe('正常')
    expect(runStatusLabel(zhT, item)).not.toBe('疑似正常')
    expect(resultTone(item.status)).toBe('ok')
    expect(runExplanation(zhT, item)).toBe('归因模型 gpt-5，概率 0.82。行为归因结果与被测模型 gpt-5 一致，判定为正常。该结果为行为推断，不代表实际路由已核实。')
    expect(isAttributionRun(item)).toBe(true)
  })

  it('labels a ModelTrace Luna-family attribution as abnormal, not "possible Luna"', () => {
    const item = run('modeltrace', 'warning', 'modeltrace_luna_attribution', { modeltrace: { bank_revision: 'x', prediction: 'gpt-5.6-luna', probability: 0.64, used_outputs: 3, requests: 3 } })
    expect(runStatusKey(item)).toBe('warning')
    expect(runStatusLabel(zhT, item)).toBe('异常')
    expect(resultTone(item.status)).toBe('attention')
    const text = runExplanation(zhT, item)
    expect(text).toContain('归因模型 gpt-5.6-luna，概率 0.64。行为归因结果为 Luna 系列模型，判定为异常。')
    expect(text).toContain('不能证明实际路由')
    expect(isAttributionRun(item)).toBe(true)
  })

  it('keeps the fingerprint Luna label unchanged by the ModelTrace rule', () => {
    const item = run('fingerprint', 'warning', 'suspected_luna_attribution', { fingerprint: { status: 'warning', nearest_model: 'gpt-5-luna', mean_jsd: 0.12, p_value: 0.3, valid_samples: 60, required_samples: 60, cell_count: 6, evaluated_at: '' } })
    expect(runStatusKey(item)).toBe('suspected_luna')
    expect(runStatusLabel(zhT, item)).toBe('疑似 Luna')
  })

  it('keeps a valid partial ModelTrace attribution normal and names its failed requests as failures, not degradation', () => {
    const samples = [
      { accepted: true, answer: '1 2 3', attempts: 1 },
      { accepted: true, answer: '4 5 6', attempts: 1 },
      { accepted: false, error: 'http_502', http_status: 502, attempts: 3, error_message: 'bad gateway' }
    ]
    const item = run('modeltrace', 'pass', 'modeltrace_target_match', { sample_count: 2, modeltrace: { bank_revision: 'x', prediction: 'gpt-5', probability: 0.82, used_outputs: 2, requests: 3, samples } })
    expect(runStatusLabel(zhT, item)).toBe('正常')
    expect(resultTone(item.status)).toBe('ok')
    expect(runExplanation(zhT, item)).toBe('归因模型 gpt-5，概率 0.82。行为归因结果与被测模型 gpt-5 一致，判定为正常。该结果为行为推断，不代表实际路由已核实。1 个样本请求失败：HTTP 502，bad gateway（尝试 3 次）。')
    expect(runExplanation(enT, item)).toBe('Attributed model gpt-5, probability 0.82. Behavioral attribution matches the tested model gpt-5, so the result is normal. This is an inference, not a verified route. 1 samples failed: HTTP 502, bad gateway (3 attempts).')
    expect(runExplanation(zhT, item)).not.toMatch(/降智|异常/)
  })

  it('appends a recorded ModelTrace run error when no sample failed', () => {
    const item = { ...run('modeltrace', 'suspected_normal', 'non_luna_behavioral_attribution', { modeltrace: { bank_revision: 'x', prediction: 'gpt-4.1', probability: 0.6, used_outputs: 3, requests: 3 } }), error: 'stream closed Bearer abc.def-123' }
    expect(runStatusLabel(zhT, item)).toBe('疑似正常')
    expect(runExplanation(zhT, item)).toBe('归因模型 gpt-4.1，概率 0.60。行为归因结果为非 Luna 模型，判定为疑似正常。该结果为行为推断，不代表实际路由已核实。记录的错误：stream closed Bearer [redacted]。')
  })

  it('adds nothing to a ModelTrace summary without failures', () => {
    const item = run('modeltrace', 'pass', 'modeltrace_target_match', { modeltrace: { bank_revision: 'x', prediction: 'gpt-5', probability: 0.82, used_outputs: 3, requests: 3, samples: [{ accepted: true, answer: '1', attempts: 1 }] } })
    expect(runExplanation(zhT, item)).toBe('归因模型 gpt-5，概率 0.82。行为归因结果与被测模型 gpt-5 一致，判定为正常。该结果为行为推断，不代表实际路由已核实。')
  })

  it('trusts the server verdict and never re-derives it from the prediction', () => {
    // Prediction equals the target, but the server said suspected_normal: the label follows the server.
    const item = run('modeltrace', 'suspected_normal', 'non_luna_behavioral_attribution', { modeltrace: { bank_revision: 'x', prediction: 'GPT-5', probability: 0.5, used_outputs: 1, requests: 3 } })
    expect(runStatusLabel(zhT, item)).toBe('疑似正常')
  })

  it('labels a Luna attribution as possible Luna without claiming confirmed degradation', () => {
    const item = run('fingerprint', 'warning', 'suspected_luna_attribution', { fingerprint: { status: 'warning', nearest_model: 'gpt-5-luna', mean_jsd: 0.12, p_value: 0.3, valid_samples: 60, required_samples: 60, cell_count: 6, evaluated_at: '' } })
    expect(runStatusLabel(zhT, item)).toBe('疑似 Luna')
    expect(resultTone(item.status)).toBe('attention')
    const text = runExplanation(zhT, item)
    expect(text).toContain('最接近的参考模型为 gpt-5-luna。行为归因结果最接近 Luna')
    expect(text).toContain('不代表已确认降智')
  })

  it('keeps unresolved attribution and Candy warnings out of the attribution labels', () => {
    const unresolved = run('fingerprint', 'insufficient', 'unresolved_behavioral_attribution')
    expect(runStatusLabel(zhT, unresolved)).toBe('证据不足')
    expect(resultTone(unresolved.status)).toBe('neutral')
    expect(isAttributionRun(unresolved)).toBe(false)
    expect(runExplanation(zhT, unresolved)).toBe('未能确定最接近的参考模型，本次不作判定。')
    expect(runStatusLabel(zhT, run('candy', 'warning', 'single_public_item_failed'))).toBe('异常')
  })

  it('keeps insufficient samples, unresolved attribution and upstream errors worded differently', () => {
    const insufficient = runExplanation(zhT, run('modeltrace', 'insufficient', 'modeltrace_insufficient_outputs'))
    const unresolved = runExplanation(zhT, run('modeltrace', 'insufficient', 'unresolved_behavioral_attribution'))
    const failed = runExplanation(zhT, run('modeltrace', 'error', 'timeout'))
    expect(new Set([insufficient, unresolved, failed]).size).toBe(3)
    expect(insufficient).toContain('有效输出不足')
    expect(runStatusLabel(zhT, run('modeltrace', 'error', 'timeout'))).toBe('失败')
    for (const text of [insufficient, unresolved, failed]) expect(text).not.toContain('疑似正常')
  })

  it('still reads legacy ModelTrace and fingerprint history', () => {
    const legacy = run('modeltrace', 'attributed', 'modeltrace_behavioral_attribution', { modeltrace: { bank_revision: 'x', prediction: 'gpt-5', probability: 0.5, used_outputs: 3, requests: 3 } })
    expect(runStatusLabel(zhT, legacy)).toBe('已归因')
    expect(runExplanation(zhT, legacy)).toContain('不能作为实际模型的证明')
    expect(runStatusLabel(zhT, run('fingerprint', 'different', 'behavior_distribution_differs_from_reference'))).toBe('与参考不同')
  })
})

describe('attribution reinterpretation', () => {
  const base = { id: 1, account_id: 1, requested_model: 'gpt-5', reasoning_effort: '', request_count: 3, input_tokens: 0, output_tokens: 0, duration_ms: 0, started_at: '2026-10-01T00:00:00Z', trigger_source: 'manual' }
  const trace = { bank_revision: 'x', prediction: 'gpt-5', probability: 0.8, used_outputs: 3, requests: 3 }
  const run = (status: string, reason: string, attribution?: OpenAIEvalRun['outcome']['attribution']): OpenAIEvalRun => ({
    ...base, test_type: 'modeltrace', status, outcome: { status, reason, sample_count: 3, expected_count: 3, confidence: 'low', scheduling: 'alert_only', modeltrace: trace, attribution }
  })

  it('reports the stored verdict and both rule versions when the current rule reads it differently', () => {
    const item = run('pass', 'modeltrace_target_match', { rule_version: 'public-target-match-luna-v2', original_rule_version: 'non-luna-attribution-v1', original_status: 'suspected_normal', original_reason: 'non_luna_behavioral_attribution' })
    expect(attributionReinterpretation(item)).toEqual({ status: 'suspected_normal', reason: 'non_luna_behavioral_attribution', rule: 'non-luna-attribution-v1', currentRule: 'public-target-match-luna-v2' })
    expect(reinterpretationText(zhT, item)).toBe('该记录原判定为疑似正常（规则 non-luna-attribution-v1）。上方结果按当前规则 public-target-match-luna-v2 对同一批已记录输出重新解读，测试并未重新运行。')
  })

  it('names a legacy Luna verdict as it was originally shown', () => {
    const item = run('warning', 'modeltrace_luna_attribution', { rule_version: 'public-target-match-luna-v2', original_rule_version: 'non-luna-attribution-v1', original_status: 'warning', original_reason: 'suspected_luna_attribution' })
    expect(reinterpretationText(zhT, item)).toContain('原判定为疑似 Luna')
  })

  it('stays silent for runs recorded under the current rule or without metadata', () => {
    expect(attributionReinterpretation(run('pass', 'modeltrace_target_match', { rule_version: 'public-target-match-luna-v2', original_rule_version: 'public-target-match-luna-v2', original_status: 'pass', original_reason: 'modeltrace_target_match' }))).toBeNull()
    expect(attributionReinterpretation(run('suspected_normal', 'non_luna_behavioral_attribution'))).toBeNull()
    expect(reinterpretationText(zhT, run('suspected_normal', 'non_luna_behavioral_attribution'))).toBe('')
  })

  it('still notes an older rule version even when the verdict did not change', () => {
    const item = run('suspected_normal', 'non_luna_behavioral_attribution', { rule_version: 'public-target-match-luna-v2', original_rule_version: 'non-luna-attribution-v1', original_status: 'suspected_normal', original_reason: 'non_luna_behavioral_attribution' })
    expect(reinterpretationText(zhT, item)).toContain('原判定为疑似正常（规则 non-luna-attribution-v1）')
  })
})

describe('custom balance validity', () => {
  it('rejects missing, negative and all-zero weights', () => {
    expect(isValidCustomBalance(undefined)).toBe(false)
    expect(isValidCustomBalance({ cost: 0, stability: 0, error_rate: 0, ttft: 0, load: 0 })).toBe(false)
    expect(isValidCustomBalance({ cost: -0.1, stability: 1, error_rate: 0, ttft: 0, load: 0 })).toBe(false)
    expect(isValidCustomBalance({ cost: 0, stability: 0, error_rate: 0, ttft: 0, load: 0.01 })).toBe(true)
    // A legacy stability-only set is still valid once folded.
    expect(isValidCustomBalance({ cost: 0, stability: 1, error_rate: 0, ttft: 0, load: 0 })).toBe(true)
  })
})

describe('custom balance shares', () => {
  it('reports the normalized share of each factor', () => {
    expect(customBalanceShares({ cost: 1, stability: 1, error_rate: 1, ttft: 1, load: 0 })).toEqual({ cost: 25, error_rate: 40, ttft: 35, load: 0, quality: 0 })
    expect(customBalanceShares(undefined)).toEqual({ cost: 20, error_rate: 43, ttft: 27, load: 10, quality: 0 })
  })
})

describe('scheduler explanations', () => {
  it('groups prefixed exclusion codes and falls back for unknown ones', () => {
    expect(exclusionKey('model_not_supported')).toBe('model_not_supported')
    expect(exclusionKey('quota_auto_pause_7d')).toBe('quota_auto_pause')
    expect(exclusionKey('grok_free_quota_soft_gate')).toBe('platform_quota')
    expect(exclusionKey('something_new')).toBe('unknown')
    expect(exclusionKey(undefined)).toBe('unknown')
  })

  it('lists the chosen account first, then servable ones by score, then excluded', () => {
    const ordered = orderCandidates([
      { account_id: 1, eligible: false, selected: false },
      { account_id: 2, eligible: true, selected: false, score: 0.2 },
      { account_id: 3, eligible: true, selected: false, score: 0.9 },
      { account_id: 4, eligible: true, selected: true, score: 0.5 }
    ])
    expect(ordered.map(item => item.account_id)).toEqual([4, 3, 2, 1])
  })
})

describe('official admin wording', () => {
  function strings(node: unknown): string[] {
    if (typeof node === 'string') return [node]
    if (node && typeof node === 'object') return Object.values(node).flatMap(strings)
    return []
  }
  const all = strings(zhLocale.admin.modelIntegrity).join('\n')

  it('does not bring back colloquial phrasing the user rejected', () => {
    for (const phrase of ['选账号的方式', '选法', '填了推理强度', '请求来了', '怎么切换', '好几个账号都能用', '这次就不会被选', '先检查，再排序', '不像 Luna', '没法', '最像']) {
      expect(all).not.toContain(phrase)
    }
  })

  it('names policies and states rule precedence directly', () => {
    expect(zhT('admin.modelIntegrity.scheduling.policy.title')).toBe('排序策略')
    expect(zhT('admin.modelIntegrity.scheduling.rules.policy')).toBe('策略')
    expect(zhT('admin.modelIntegrity.scheduling.rules.hint')).toContain('规则优先于默认策略')
    // Account rules never lift a lower pass-rate tier under avoid-degradation.
    const accountRules = zhT('admin.modelIntegrity.scheduling.accountRules.hint')
    expect(accountRules).toContain('「避免降智」下通过率分档始终在前，规则只在同一通过率档内调整顺序')
    expect(accountRules).toContain('数字越小越优先')
    expect(accountRules).toContain('同一账号的指定模型规则优先于全部模型规则')
    expect(zhT('admin.modelIntegrity.scheduling.gates.title')).toBe('调度条件')
    expect(zhT('admin.modelIntegrity.scheduling.policy.options.stability_first.effect')).toContain('不参考降智通过率')
  })

  it('does not claim that test-page probes advance automatic BPS switching', () => {
    // State Probe remains diagnostic-only after BPS retirement.
    const history = zhT('admin.modelIntegrity.reason.stateProbe.degraded')
    const probe = zhT('admin.modelIntegrity.tests.types.state_probe.what')
    const testDescription = zhT('admin.modelIntegrity.tests.description')
    for (const text of [history, probe, testDescription]) {
      expect(text).toContain('不自动切换线路')
      expect(text).not.toContain('BPS')
    }
    expect(probe).not.toContain('依据此结果')
    expect(history).not.toContain('将切换至 BPS')
    for (const phrase of ['BPS 自动切换依据', '用于 BPS 自动切换', '结果用于 BPS 自动切换']) {
      expect(all).not.toContain(phrase)
    }
  })
})

describe('retries and per-sample helpers', () => {
  it('does not treat a successful HTTP 200 status as a failed sample', () => {
    const sample = { probe_id: 'ok', valid: true, answer: '21', http_status: 200, attempts: 1 }
    expect(sampleFailed(sample)).toBe(false)
    expect(sampleState(sample, 'candy')).toBe('correct')
    expect(sampleFailed({ ...sample, valid: false, answer: '', error_code: 'upstream_error', http_status: 503 })).toBe(true)
    expect(sampleState({ ...sample, error_code: 'single_public_item_failed' }, 'candy')).toBe('wrong')
  })
  it('normalises attempts per sample to an integer between 1 and 10, defaulting to 3', () => {
    expect(normalizeMaxRequestAttempts(undefined)).toBe(3)
    expect(normalizeMaxRequestAttempts(null)).toBe(3)
    expect(normalizeMaxRequestAttempts('')).toBe(3)
    expect(normalizeMaxRequestAttempts('x')).toBe(3)
    expect(normalizeMaxRequestAttempts(0)).toBe(1)
    expect(normalizeMaxRequestAttempts(-4)).toBe(1)
    expect(normalizeMaxRequestAttempts(11)).toBe(10)
    expect(normalizeMaxRequestAttempts(2.9)).toBe(2)
    expect(toSavePayload({ effects_enabled: false, bps_auto_enabled: false, accounts: [] }).max_request_attempts).toBe(3)
  })

  it('counts the request ceiling per run with attempts, capping State Probe at three chains', () => {
    const route = oauthRoute()
    expect(maxRequestsPerRun(route, 'modeltrace', 3, catalog)).toBe(9)
    // One chain is a mint plus a continue, and the server allows three chains
    // even when the shared attempts setting is higher — six sends at most.
    expect(maxRequestsPerRun(route, 'state_probe', 3, catalog)).toBe(6)
    expect(maxRequestsPerRun(route, 'state_probe', 1, catalog)).toBe(2)
    expect(maxRequestsPerRun(route, 'state_probe', 10, catalog)).toBe(6)
  })

  it('scales the State Probe daily ceiling by chains, not by the raw attempts value', () => {
    const route = oauthRoute()
    route.state_probe_schedule.enabled = true
    route.state_probe_schedule.interval_seconds = 86_400
    const probeRequests = dailyRequests(route, 'state_probe', catalog)
    expect(probeRequests).toBe(2)
    // Two sends per chain feeds the average; the ceiling adds one more chain.
    expect(totalDailyMaxRequests([route], 3, catalog)).toBe(2 * 3)
    expect(totalDailyMaxRequests([route], 8, catalog)).toBe(2 * 3)
  })

  it('clamps State Probe to the server’s three-chain budget before multiplying', () => {
    // The server clamps the value it is handed (openAIStateProbeMaxAttempts),
    // so the page must show the capped ceiling, never 2 × the raw setting.
    expect(stateProbeChains(0)).toBe(1)
    expect(stateProbeChains(1)).toBe(1)
    expect(stateProbeChains(2)).toBe(2)
    expect(stateProbeChains(3)).toBe(3)
    expect(stateProbeChains(10)).toBe(3)
    expect(stateProbeMaxRequests(1)).toBe(2)
    expect(stateProbeMaxRequests(3)).toBe(6)
    expect(stateProbeMaxRequests(10)).toBe(6)
  })

  it('treats State Probe eligibility as an account capability regardless of target effort', () => {
    expect(isDirectOAuthRoute(oauthRoute({ reasoning_effort: 'high' }))).toBe(true)
    expect(isDirectOAuthRoute(oauthRoute({ direct_oauth_eligible: false }))).toBe(false)
  })

  it('mirrors the server direct OAuth predicate for an account added before reload', () => {
    const direct = { platform: 'openai', type: 'oauth', parent_account_id: null } as const
    expect(isDirectOAuthAccount(direct)).toBe(true)
    expect(isDirectOAuthAccount({ ...direct, credentials: { auth_mode: 'chatgpt' }, extra: { synthetic_ui_test: false } })).toBe(true)
    expect(isDirectOAuthAccount({ ...direct, parent_account_id: 7 })).toBe(false)
    expect(isDirectOAuthAccount({ ...direct, extra: { synthetic_ui_test: true } })).toBe(false)
    // The server only honours a boolean flag.
    expect(isDirectOAuthAccount({ ...direct, extra: { synthetic_ui_test: 'true' } })).toBe(true)
    expect(isDirectOAuthAccount({ ...direct, credentials: { auth_mode: 'agentIdentity' } })).toBe(false)
    expect(isDirectOAuthAccount({ ...direct, credentials: { auth_mode: '  AGENTIDENTITY ' } })).toBe(false)
    expect(isDirectOAuthAccount({ ...direct, type: 'apikey' })).toBe(false)
    expect(isDirectOAuthAccount({ ...direct, type: 'setup-token' })).toBe(false)
    expect(isDirectOAuthAccount({ ...direct, platform: 'anthropic' })).toBe(false)
    expect(isDirectOAuthAccount(undefined)).toBe(false)
  })

  it('reads the expected Candy answer from the run data version, never from today\'s catalog alone', () => {
    const current = { ...catalog, data_version: 'sub2api-candy-21-v3', candy: { ...catalog.candy, expected_answer: 21 } }
    expect(candyExpectedAnswer({ data_version: 'sub2api-candy-21-v3' }, current)).toBe(21)
    expect(candyExpectedAnswer({ data_version: 'sub2api-candy-29-v2-cpa' }, current)).toBe(29)
    expect(candyExpectedAnswer({ data_version: 'mystery' }, current)).toBeNull()
    expect(candyExpectedAnswer({}, current)).toBeNull()
    // The current server's catalog has no data_version; runs still carry theirs.
    const unversioned = { ...catalog, candy: { ...catalog.candy, expected_answer: 21 } }
    expect(candyExpectedAnswer({ data_version: 'sub2api-candy-21-v3-97623969-cpa' }, unversioned)).toBe(21)
    expect(candyExpectedAnswer({ data_version: 'sub2api-candy-29-v2-cpa' }, unversioned)).toBe(29)
    expect(isHistoricalDataVersion({ data_version: 'sub2api-candy-29-v2-cpa' }, unversioned)).toBe(true)
    expect(isHistoricalDataVersion({ data_version: 'sub2api-candy-21-v3-97623969-cpa' }, unversioned)).toBe(false)
    expect(candyExpectedAnswer({}, unversioned)).toBeNull()
  })

  it('reads ModelTrace diagnostics from the outcome when the run has no sample records', () => {
    const samples = runSamples({
      outcome: {
        status: 'insufficient', sample_count: 2, expected_count: 3, confidence: 'none', scheduling: 'alert_only',
        modeltrace: { bank_revision: 'x', used_outputs: 2, requests: 5, samples: [{ accepted: true, answer: '1 2 3', attempts: 1 }, { accepted: false, error: 'http_429', http_status: 429, attempts: 3, error_message: 'slow down' }] }
      }
    })
    expect(samples.map(sample => sampleState(sample, 'modeltrace'))).toEqual(['valid', 'error'])
    expect(samples[1]).toMatchObject({ http_status: 429, attempts: 3, error_message: 'slow down', error_code: 'http_429' })
  })

  it('prefers the raw answer, separates scoring from request failures, and redacts secrets', () => {
    expect(sampleAnswer({ probe_id: 'a', valid: true, answer: '21 颗', normalized_answer: '21' })).toBe('21 颗')
    expect(sampleAnswer({ probe_id: 'a', valid: true, normalized_answer: '21' })).toBe('21')
    expect(sampleState({ probe_id: 'a', valid: false, error_code: 'single_public_item_failed' }, 'candy')).toBe('wrong')
    expect(sampleState({ probe_id: 'a', valid: false, error_code: 'invalid_probe_answer', normalized_answer: '' }, 'fingerprint')).toBe('invalid')
    expect(sampleState({ probe_id: 'a', valid: false, error_code: 'timeout' }, 'fingerprint')).toBe('error')
    expect(redactSecrets('Bearer eyJabc.def sk-abcdefghijkl ok')).toBe('Bearer [redacted] sk-[redacted] ok')
  })

  it('reads the Candy answer the server extracted and never derives one from the reply', () => {
    const base = { probe_id: 'a', valid: true, http_status: 200, attempts: 1 }
    expect(candyExtractedAnswer({ ...base, error_code: 'correct_answer', normalized_answer: '21', answer: '9 + 12，最终答案：21' }, 21)).toBe('21')
    expect(candyExtractedAnswer({ ...base, error_code: 'single_public_item_failed', normalized_answer: '29', answer: '答案是 29' }, 21)).toBe('29')
    expect(candyExtractedAnswer({ ...base, error_code: 'single_public_item_failed', answer: '可能是 21' }, 21)).toBe('')
    expect(candyExtractedAnswer({ ...base, answer: '21' }, 21)).toBe('')
    // A legacy correct sample without a stored integer may show the run's expected answer.
    expect(candyExtractedAnswer({ ...base, error_code: 'correct_answer' }, 21)).toBe('21')
    expect(candyExtractedAnswer({ ...base, error_code: 'correct_answer' }, 29)).toBe('29')
    expect(candyExtractedAnswer({ ...base, error_code: 'correct_answer' }, null)).toBe('')
    expect(candyExtractedAnswer({ probe_id: 'a', valid: false, error_code: 'http_5xx', http_status: 503 }, 21)).toBe('')
    expect(candyFullReply({ ...base, answer: '  最终答案：21\n' })).toBe('最终答案：21')
    expect(candyFullReply({ ...base, normalized_answer: '21' })).toBe('')
  })
})
