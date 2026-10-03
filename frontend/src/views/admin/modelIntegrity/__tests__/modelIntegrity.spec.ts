import { describe, expect, it } from 'vitest'
import type { OpenAIEvalConfig, OpenAIEvalModelCatalog, OpenAIEvalRouteConfig, OpenAIEvalRun } from '@/api/admin/accounts'
import { runExplanation, runStatusLabel } from '../runText'
import { zhT } from './zhT'
import zhLocale from '@/i18n/locales/zh'
import {
  activeScheduleCount,
  candyExtractedAnswer,
  candyFullReply,
  customBalanceShares,
  isAttributionRun,
  isValidCustomBalance,
  runStatusKey,
  bpsModeOf,
  dailyRequests,
  exclusionKey,
  MAX_INTERVAL_SECONDS,
  maxScheduleJitterSeconds,
  newRoute,
  normalizeBPSAccount,
  normalizeCustomBalance,
  normalizeRoute,
  normalizeSchedule,
  orderCandidates,
  requestsPerRun,
  resultTone,
  toSavePayload,
  totalDailyRequests,
  candyExpectedAnswer,
  CUSTOM_FACTORS,
  foldLegacyStability,
  isDirectOAuthAccount,
  isDirectOAuthRoute,
  isHistoricalDataVersion,
  maxRequestsPerRun,
  normalizeMaxRequestAttempts,
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
    const bps = normalizeBPSAccount({
      account_id: 1,
      mode: 'auto',
      failure_threshold: 3,
      recovery_threshold: 2,
      interval_seconds: Number.MAX_SAFE_INTEGER
    })
    expect(bps.interval_seconds).toBe(MAX_INTERVAL_SECONDS)
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
})

describe('BPS mode compatibility', () => {
  it('maps the legacy bps_auto boolean when no explicit mode exists', () => {
    expect(bpsModeOf({ bps_auto: true })).toBe('auto')
    expect(bpsModeOf({ bps_auto: false })).toBe('force_off')
    expect(bpsModeOf({ bps_auto: false, bps_mode: 'force_on' })).toBe('force_on')
  })

  it('fills missing schedules on routes saved by older versions', () => {
    const legacy = { account_id: 3, requested_model: 'gpt-5', reasoning_effort: '', candy_schedule: { enabled: true, interval_seconds: 3600, jitter_seconds: 0 }, fingerprint_schedule: { enabled: false, interval_seconds: 86400, jitter_seconds: 0 }, bps_auto: true } as unknown as OpenAIEvalRouteConfig
    const route = normalizeRoute(legacy)
    expect(route.modeltrace_schedule.interval_seconds).toBe(300)
    expect(route.state_probe_schedule.interval_seconds).toBe(300)
    expect(route.bps_mode).toBe('auto')
  })
})

describe('save payload', () => {
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
    expect(payload.policies).toEqual([{ requested_model: 'gpt-5', reasoning_effort: '', policy: 'avoid_degradation' }])
    expect(payload.accounts[0]).not.toHaveProperty('bps_state')
    expect(payload.accounts[0]).not.toHaveProperty('direct_oauth_eligible')
    expect(payload.accounts[0].bps_mode).toBe('auto')
    expect(payload.accounts[0].bps_auto).toBe(true)
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
    const item = run('modeltrace', 'suspected_normal', 'non_luna_behavioral_attribution', { modeltrace: { bank_revision: 'x', prediction: 'gpt-5', probability: 0.71, used_outputs: 3, requests: 3 } })
    expect(runStatusKey(item)).toBe('suspected_normal')
    expect(runStatusLabel(zhT, item)).toBe('疑似正常')
    expect(runExplanation(zhT, item)).toBe('归因模型 gpt-5，概率 0.71。行为归因结果为非 Luna 模型，判定为疑似正常。该结果为行为推断，不代表实际路由已核实。')
    expect(isAttributionRun(item)).toBe(true)
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
    expect(zhT('admin.modelIntegrity.scheduling.gates.title')).toBe('调度条件')
    expect(zhT('admin.modelIntegrity.scheduling.policy.options.stability_first.effect')).toContain('不参考降智通过率')
  })

  it('does not claim that test-page probes advance automatic BPS switching', () => {
    // Backend: only the internal scheduled account probe under Scheduling policy
    // advances BPS state (openai_bps_state.go applyOpenAIStateProbeBPS).
    const history = zhT('admin.modelIntegrity.reason.stateProbe.degraded')
    const probe = zhT('admin.modelIntegrity.tests.types.state_probe.what')
    const testDescription = zhT('admin.modelIntegrity.tests.description')
    for (const text of [history, probe, testDescription]) {
      expect(text).toContain('不改变 BPS 状态')
      expect(text).toContain('调度策略')
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

  it('counts the request ceiling per run with attempts but never multiplies State Probe', () => {
    const route = oauthRoute()
    expect(maxRequestsPerRun(route, 'modeltrace', 3, catalog)).toBe(9)
    expect(maxRequestsPerRun(route, 'state_probe', 3, catalog)).toBe(2)
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
