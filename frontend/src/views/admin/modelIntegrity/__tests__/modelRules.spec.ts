import { describe, expect, it } from 'vitest'
import type { OpenAIEvalOverviewModel, OpenAIEvalRankingFactors, OpenAIEvalRouteConfig, OpenAIEvalSchedulingPolicyRule } from '@/api/admin/accounts'
import {
  isCatalogModel,
  mergeLegacyModelTraceSchedule,
  modelPolicyRule,
  normalizeModelPolicyRules,
  overviewModelRows,
  parseFingerprintDisabledWarning,
  pelicanVariantName,
  toSavePayload
} from '../modelIntegrity'

const rule = (requested_model: string, policy: OpenAIEvalSchedulingPolicyRule['policy'], reasoning_effort = '', enabled?: boolean): OpenAIEvalSchedulingPolicyRule =>
  ({ requested_model, policy, reasoning_effort, ...(enabled === undefined ? {} : { enabled }) })

describe('model rules without effort', () => {
  it('keeps the last rule per model, a disabled one included, and clears every effort', () => {
    const { rules, merges } = normalizeModelPolicyRules([
      rule('gpt-5', 'cost_first', 'high'),
      rule('gpt-6.1-sol', 'stability_first'),
      rule(' GPT-5 ', 'avoid_degradation', 'low', false),
      rule('gpt-6-astra', 'cost_first', 'xhigh')
    ])
    expect(rules).toEqual([
      { requested_model: 'gpt-6.1-sol', policy: 'stability_first', reasoning_effort: '' },
      { requested_model: ' GPT-5 ', policy: 'avoid_degradation', reasoning_effort: '', enabled: false },
      { requested_model: 'gpt-6-astra', policy: 'cost_first', reasoning_effort: '' }
    ])
    expect(merges).toEqual([
      { model: 'GPT-5', kept: { effort: 'low', policy: 'avoid_degradation', enabled: false }, dropped: [{ effort: 'high', policy: 'cost_first', enabled: true }] },
      { model: 'gpt-6-astra', kept: { effort: 'xhigh', policy: 'cost_first', enabled: true }, dropped: [] }
    ])
  })

  it('leaves an already migrated config unchanged and reports nothing', () => {
    const input = [rule('gpt-5', 'cost_first'), rule('gpt-5.1', 'stability_first', '', false)]
    expect(normalizeModelPolicyRules(input)).toEqual({ rules: input, merges: [] })
  })

  it('resolves a model to its last rule and to none when that rule is disabled', () => {
    const rules = [rule('gpt-5', 'cost_first', 'high'), rule('gpt-5', 'stability_first', 'low'), rule('gpt-6', 'cost_first'), rule('GPT-6', 'avoid_degradation', '', false)]
    expect(modelPolicyRule(rules, 'GPT-5')?.policy).toBe('stability_first')
    expect(modelPolicyRule(rules, 'gpt-6')).toBeNull()
    expect(modelPolicyRule(rules, 'gpt-7')).toBeNull()
  })

  it('saves rules without an effort while evaluation targets keep theirs', () => {
    const route = { account_id: 1, requested_model: 'gpt-5', reasoning_effort: 'high', candy_schedule: { enabled: false, interval_seconds: 300, jitter_seconds: 0 }, fingerprint_schedule: { enabled: false, interval_seconds: 300, jitter_seconds: 0 }, state_probe_schedule: { enabled: false, interval_seconds: 300, jitter_seconds: 0 }, bps_auto: false }
    const payload = toSavePayload({ effects_enabled: true, bps_auto_enabled: false, policies: [rule('gpt-5', 'cost_first', 'high')], accounts: [route] })
    expect(payload.policies).toEqual([{ requested_model: 'gpt-5', policy: 'cost_first', reasoning_effort: '', enabled: true }])
    expect(payload.accounts[0].reasoning_effort).toBe('high')
  })
})

describe('legacy ModelTrace schedule', () => {
  const route = (fingerprintOn: boolean): OpenAIEvalRouteConfig => ({
    account_id: 1, requested_model: 'gpt-5', reasoning_effort: '',
    candy_schedule: { enabled: false, interval_seconds: 3600, jitter_seconds: 0 },
    fingerprint_schedule: { enabled: fingerprintOn, interval_seconds: 43200, jitter_seconds: 0, concurrency: 2 },
    modeltrace_schedule: { enabled: true, interval_seconds: 21600, jitter_seconds: 600 },
    state_probe_schedule: { enabled: false, interval_seconds: 3600, jitter_seconds: 0 },
    bps_auto: false
  })

  it('keeps an enabled fingerprint schedule as it is, like the server', () => {
    const merged = mergeLegacyModelTraceSchedule(route(true))
    expect(merged.fingerprint_schedule).toEqual({ enabled: true, interval_seconds: 43200, jitter_seconds: 0, concurrency: 2 })
    expect(merged).not.toHaveProperty('modeltrace_schedule')
  })

  it('adopts an enabled ModelTrace schedule when fingerprint was off', () => {
    const merged = mergeLegacyModelTraceSchedule(route(false))
    expect(merged.fingerprint_schedule).toEqual({ enabled: true, interval_seconds: 21600, jitter_seconds: 600, concurrency: 2 })
  })
})

describe('unknown models and server warnings', () => {
  const catalog = { items: [{ id: 'gpt-5' }, { id: 'GPT-6.1-Sol' }], baseline_version: '', baseline_models: [], candy: { expected_answer: 21, confidence: 'low', scheduling: '' }, evaluation_notice: '', reasoning_efforts: [''], pelican: { requests: 1, variants: [{ id: 'original', label: '原版', prompt: 'x' }] } }

  it('runs only models the catalog lists, and blocks nothing without a catalog', () => {
    expect(isCatalogModel(' gpt-6.1-sol ', catalog)).toBe(true)
    expect(isCatalogModel('gpt-9-preview', catalog)).toBe(false)
    expect(isCatalogModel('gpt-9-preview', null)).toBe(true)
  })

  it('reads the server’s model_not_enrolled warning and nothing else', () => {
    expect(parseFingerprintDisabledWarning('account 12 model gpt-9-preview: automatic fingerprint disabled (model_not_enrolled)')).toEqual({ accountID: 12, model: 'gpt-9-preview' })
    expect(parseFingerprintDisabledWarning('something else happened')).toBeNull()
  })

  it('names a Pelican variant by the plugin label and falls back to its id', () => {
    expect(pelicanVariantName('original', catalog)).toBe('原版')
    expect(pelicanVariantName('retired-variant', catalog)).toBe('retired-variant')
  })
})

describe('overview model rows', () => {
  const factors = (error: { value: number | null; samples: number }, ttft: { ms: number | null; samples: number }, ratio: number | null): OpenAIEvalRankingFactors => ({
    price: { score: 1, known: true, observed_at: null, unknown_reason: null, rate_multiplier: 1, source: 'account_rate' },
    error_rate: { score: 1 - (error.value ?? 0), known: error.value !== null, observed_at: null, unknown_reason: null, value: error.value, sample_count: error.samples, source: 'request_ewma_account_model_effort' },
    ttft: { score: 0.5, known: ttft.ms !== null, observed_at: null, unknown_reason: null, ms: ttft.ms, sample_count: ttft.samples },
    load: { score: 0.5, known: true, observed_at: null, unknown_reason: null, load_rate: 10, waiting: 0, current_concurrency: 0 },
    quality: { score: ratio ?? 0.5, known: ratio !== null, observed_at: null, unknown_reason: null, state: ratio === null ? 'unknown' : 'assessed', pass: ratio === null ? 0 : 1, suspected_pass: 0, selected: 1, evaluated: ratio === null ? 0 : 1, ratio, expires_at: null }
  })
  const model = (requested_model: string, reasoning_effort: string, f: OpenAIEvalRankingFactors): OpenAIEvalOverviewModel => ({ requested_model, reasoning_effort, upstream_models: [requested_model], factors: f, sources: ['scheduled_quality'] })

  it('uses a server row as sent', () => {
    const only = model('gpt-5', '', factors({ value: 0.5, samples: 2 }, { ms: 700, samples: 2 }, 0.5))
    const [row] = overviewModelRows([only])
    expect(row.basis).toBe('server')
    expect(row.factors).toBe(only.factors)
  })

  it('weights by samples, averages only known pass rates and keeps sol and astra apart', () => {
    const rows = overviewModelRows([
      model('gpt-6.1-sol', '', factors({ value: 0, samples: 10 }, { ms: 1000, samples: 10 }, 0.5)),
      model('gpt-6-astra', 'high', factors({ value: 0.1, samples: 4 }, { ms: null, samples: 0 }, null)),
      model('GPT-6.1-SOL', 'high', factors({ value: 1, samples: 1 }, { ms: 2100, samples: 1 }, 1)),
      model('gpt-6.1-sol', 'low', factors({ value: null, samples: 0 }, { ms: null, samples: 0 }, null))
    ])
    expect(rows.map(row => [row.requested_model, row.basis])).toEqual([['gpt-6.1-sol', 'projected'], ['gpt-6-astra', 'server']])
    const sol = rows[0].factors
    expect(sol.error_rate.value).toBeCloseTo(1 / 11)
    expect(sol.error_rate.sample_count).toBe(11)
    expect(sol.ttft.ms).toBeCloseTo(1100)
    expect(sol.quality.ratio).toBeCloseTo(0.75)
    expect(sol.quality.state).toBe('assessed')
    expect(rows[0].efforts.map(item => item.effort)).toEqual(['', 'high', 'low'])
  })

  it('leaves a folded factor unknown when no effort has a reading', () => {
    const [row] = overviewModelRows([
      model('gpt-5', 'high', factors({ value: null, samples: 0 }, { ms: null, samples: 0 }, null)),
      model('gpt-5', 'low', factors({ value: null, samples: 0 }, { ms: null, samples: 0 }, null))
    ])
    expect(row.factors.error_rate.known).toBe(false)
    expect(row.factors.error_rate.value).toBeNull()
    expect(row.factors.quality.ratio).toBeNull()
    expect(row.factors.quality.state).toBe('unknown')
  })
})
