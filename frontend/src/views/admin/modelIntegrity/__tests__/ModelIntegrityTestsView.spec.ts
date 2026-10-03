import { flushPromises, mount, RouterLinkStub } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { OpenAIEvalConfig } from '@/api/admin/accounts'
import { zhT } from './zhT'

const api = vi.hoisted(() => ({
  getOpenAIEvalModels: vi.fn(),
  getOpenAIEvalConfig: vi.fn(),
  saveOpenAIEvalConfig: vi.fn(),
  runOpenAIEval: vi.fn(),
  listOpenAIEvalRuns: vi.fn(),
  list: vi.fn()
}))
const store = vi.hoisted(() => ({ showError: vi.fn(), showSuccess: vi.fn(), showInfo: vi.fn() }))

vi.mock('@/api/admin/accounts', () => ({ default: api, accountsAPI: api }))
vi.mock('@/stores/app', () => ({ useAppStore: () => store }))
vi.mock('vue-i18n', async importOriginal => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: zhT })
}))
vi.mock('vue-router', async importOriginal => ({
  ...(await importOriginal<typeof import('vue-router')>()),
  useRoute: () => ({ path: '/admin/model-integrity/tests' }),
  onBeforeRouteLeave: vi.fn()
}))

import TestsView from '../ModelIntegrityTestsView.vue'

const catalog = {
  items: [{ id: 'gpt-5' }],
  baseline_version: 'cpa-v1',
  baseline_models: [],
  candy: { expected_answer: 29, confidence: 'low', scheduling: 'alert_only' },
  evaluation_notice: '',
  reasoning_efforts: ['', 'high'],
  fingerprint_modes: [{ id: 'quick', samples: 60 }, { id: 'standard', samples: 200 }, { id: 'strict', samples: 400 }],
  modeltrace: { requests: 3, bank_revision: 'x', candidate_count: 16, scheduling: 'alert_only' }
}

const schedule = (interval: number, enabled = false) => ({ enabled, interval_seconds: interval, jitter_seconds: 0 })

function serverConfig(): OpenAIEvalConfig {
  return {
    revision: 9,
    effects_enabled: true,
    bps_auto_enabled: false,
    scheduling_policy: 'stability_first',
    policies: [{ requested_model: 'gpt-5', reasoning_effort: 'high', policy: 'cost_first' }],
    accounts: [
      {
        account_id: 12,
        requested_model: 'gpt-5',
        reasoning_effort: 'high',
        candy_schedule: { ...schedule(3600, true), sample_count: 5 },
        fingerprint_schedule: { ...schedule(86400), sample_mode: 'standard' },
        modeltrace_schedule: schedule(86400),
        state_probe_schedule: schedule(21600),
        bps_auto: false,
        direct_oauth_eligible: false
      }
    ]
  }
}

const accounts = [
  { id: 11, name: 'oauth-a', platform: 'openai', type: 'oauth', parent_account_id: null },
  { id: 12, name: 'apikey-b', platform: 'openai', type: 'apikey' }
]

function mountView() {
  return mount(TestsView, {
    attachTo: document.body,
    global: {
      stubs: { AppLayout: { template: '<div><slot /></div>' }, RouterLink: RouterLinkStub }
    }
  })
}

beforeEach(() => {
  vi.clearAllMocks()
  document.body.innerHTML = ''
  api.getOpenAIEvalModels.mockResolvedValue(catalog)
  api.getOpenAIEvalConfig.mockResolvedValue(serverConfig())
  api.list.mockResolvedValue({ items: accounts })
  api.listOpenAIEvalRuns.mockResolvedValue({
    items: [{
      id: 1,
      account_id: 12,
      test_type: 'candy',
      requested_model: 'gpt-5',
      reasoning_effort: 'high',
      status: 'warning',
      outcome: { status: 'warning', reason: 'one_or_more_public_candy_variants_failed', sample_count: 5, expected_count: 5, confidence: 'low', scheduling: 'alert_only' },
      request_count: 5,
      input_tokens: 100,
      output_tokens: 20,
      cost_estimate_usd: 0.0012,
      duration_ms: 3000,
      started_at: '2026-10-01T07:00:00Z',
      finished_at: '2026-10-01T07:00:03Z',
      trigger_source: 'scheduled'
    }]
  })
  api.saveOpenAIEvalConfig.mockImplementation(async (payload: OpenAIEvalConfig) => ({ ...payload, revision: (payload.revision ?? 0) + 1 }))
})

describe('ModelIntegrityTestsView', () => {
  it('states the automatic request budget in plain words', async () => {
    const wrapper = mountView()
    await flushPromises()
    // Candy every hour: 5 requests × 24 = 120 requests per day.
    expect(wrapper.get('[data-testid="budget"]').text()).toContain('预计每天发出约 120 次上游请求（1 个自动计划）')
    expect(wrapper.get('[data-testid="test-candy"]').text()).toContain('每次 5 次请求')
    expect(wrapper.get('[data-testid="test-candy"]').text()).toContain('每天约 120 次请求')
    wrapper.unmount()
  })

  it('explains the latest result without internal jargon and marks State Probe as unavailable', async () => {
    const wrapper = mountView()
    await flushPromises()
    const candy = wrapper.get('[data-testid="test-candy"]')
    expect(candy.text()).toContain('异常')
    expect(candy.text()).toContain('5 次中至少 1 次未答出 29')
    const probe = wrapper.get('[data-testid="test-state_probe"]')
    expect(probe.text()).toContain('只支持直连 OpenAI OAuth 账号的默认推理强度')
    expect(probe.get('[data-testid="run-state_probe"]').attributes('disabled')).toBeDefined()
    expect(wrapper.text()).not.toMatch(/路线资格|硬失败/)
    wrapper.unmount()
  })

  it('shows non-Luna attributions as likely normal and Luna as a possible match, both marked as inferred', async () => {
    const run = (id: number, test_type: 'modeltrace' | 'fingerprint', status: string, reason: string, extra: Record<string, unknown>) => ({
      id, account_id: 12, test_type, requested_model: 'gpt-5', reasoning_effort: 'high', status,
      outcome: { status, reason, sample_count: 3, expected_count: 3, confidence: 'low', scheduling: 'alert_only', ...extra },
      request_count: 3, input_tokens: 1, output_tokens: 1, duration_ms: 1000,
      started_at: `2026-10-01T0${id}:00:00Z`, finished_at: `2026-10-01T0${id}:00:05Z`, trigger_source: 'scheduled'
    })
    api.listOpenAIEvalRuns.mockResolvedValue({
      items: [
        run(3, 'modeltrace', 'suspected_normal', 'non_luna_behavioral_attribution', { modeltrace: { bank_revision: 'x', prediction: 'gpt-5', probability: 0.8, used_outputs: 3, requests: 3 } }),
        run(2, 'fingerprint', 'warning', 'suspected_luna_attribution', { fingerprint: { status: 'warning', nearest_model: 'gpt-5-luna', mean_jsd: 0.1, p_value: 0.4, valid_samples: 60, required_samples: 60, cell_count: 6, evaluated_at: '' } }),
        run(1, 'fingerprint', 'insufficient', 'unresolved_behavioral_attribution', {})
      ]
    })
    const wrapper = mountView()
    await flushPromises()

    const trace = wrapper.get('[data-testid="test-modeltrace"]')
    expect(trace.get('[data-testid="latest-status"]').text()).toBe('疑似正常')
    expect(trace.find('.tt-result-likely').exists()).toBe(true)
    expect(trace.text()).toContain('不代表实际路由已核实')
    // The inference qualifier is stated once, not repeated in a second note.
    expect(trace.text().split('不代表实际路由已核实')).toHaveLength(2)
    expect(trace.text()).not.toContain('不参与账号调度')
    const fingerprint = wrapper.get('[data-testid="test-fingerprint"]')
    expect(fingerprint.get('[data-testid="latest-status"]').text()).toBe('疑似 Luna')
    expect(fingerprint.text()).toContain('不代表已确认降智')
    expect(fingerprint.text()).not.toContain('异常')

    const statuses = () => wrapper.findAll('[data-testid="history-status"]').map(item => item.text())
    expect(statuses()).toEqual(['疑似正常', '疑似 Luna', '证据不足'])
    const toneFilter = wrapper.findAll('select').find(select => select.find('option[value="likely"]').exists())!
    await toneFilter.setValue('likely')
    expect(statuses()).toEqual(['疑似正常'])
    await toneFilter.setValue('neutral')
    expect(statuses()).toEqual(['证据不足'])
    await toneFilter.setValue('attention')
    expect(statuses()).toEqual(['疑似 Luna'])

    await wrapper.get('[data-testid="history-row"]').trigger('click')
    await flushPromises()
    const note = document.body.querySelector('[data-testid="attribution-note"]')
    expect(note?.textContent?.trim()).toBe('归因结果仅作提醒，不参与账号调度。')
    expect(document.body.querySelector('[data-testid="detail-status"]')?.textContent?.trim()).toBe('疑似 Luna')
    expect(document.body.textContent).toContain('gpt-5-luna')
    wrapper.unmount()
  })

  it('keeps manual Candy and fingerprint parameters editable when automatic tests are off', async () => {
    const config = serverConfig()
    config.accounts[0].candy_schedule.enabled = false
    config.accounts[0].fingerprint_schedule.enabled = false
    api.getOpenAIEvalConfig.mockResolvedValue(config)

    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[data-testid="candy-sample-count"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.get('[data-testid="fingerprint-sample-mode"]').attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })

  it('limits a custom schedule interval only to the database storage boundary', async () => {
    const config = serverConfig()
    config.accounts[0].candy_schedule.interval_seconds = 17 * 60
    api.getOpenAIEvalConfig.mockResolvedValue(config)

    const wrapper = mountView()
    await flushPromises()

    const input = wrapper.get('[data-testid="custom-interval"]')
    expect(input.attributes('max')).toBe('35791394')
    expect(wrapper.text()).toContain('存储上限 35,791,394')
    wrapper.unmount()
  })

  it('polls and displays persisted sample progress while a manual run is active', async () => {
    let finish!: (value: unknown) => void
    api.runOpenAIEval.mockImplementation(() => new Promise(resolve => { finish = resolve }))
    const wrapper = mountView()
    await flushPromises()
    api.listOpenAIEvalRuns.mockResolvedValue({
      items: [{
        id: 3, account_id: 12, test_type: 'candy', requested_model: 'gpt-5', reasoning_effort: 'high', status: 'running',
        outcome: { status: 'running', reason: 'sampling', sample_count: 2, expected_count: 5, confidence: 'none', scheduling: 'disabled' },
        request_count: 2, completed_samples: 2, expected_samples: 5, input_tokens: 1, output_tokens: 1,
        duration_ms: 500, started_at: '2026-10-01T08:00:00Z', trigger_source: 'manual'
      }]
    })

    await wrapper.get('[data-testid="run-candy"]').trigger('click')
    await flushPromises()
    const progress = wrapper.get('[data-testid="test-progress"]')
    expect(progress.text()).toContain('正在采样 2/5')
    expect(progress.find('.tt-progress-value').attributes('style')).toContain('40%')

    finish({
      id: 3, account_id: 12, test_type: 'candy', requested_model: 'gpt-5', reasoning_effort: 'high', status: 'pass',
      outcome: { status: 'pass', reason: 'all_public_candy_variants_passed', sample_count: 5, expected_count: 5, confidence: 'low', scheduling: 'alert_only' },
      request_count: 5, input_tokens: 1, output_tokens: 1, duration_ms: 900, started_at: '2026-10-01T08:00:00Z', trigger_source: 'manual'
    })
    await flushPromises()
    wrapper.unmount()
  })

  it('asks for the fingerprint sample size before a manual run and does not change the schedule', async () => {
    api.runOpenAIEval.mockResolvedValue({
      id: 2, account_id: 12, test_type: 'fingerprint', requested_model: 'gpt-5', reasoning_effort: 'high', status: 'consistent',
      outcome: { status: 'consistent', reason: 'behavior_distribution_consistent_with_versioned_reference', sample_count: 400, expected_count: 400, confidence: 'low', scheduling: 'alert_only' },
      request_count: 400, input_tokens: 1, output_tokens: 1, duration_ms: 1000, started_at: '2026-10-01T08:00:00Z', trigger_source: 'manual'
    })
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="run-fingerprint"]').trigger('click')
    const strict = document.body.querySelector<HTMLInputElement>('input[name="manual-sample"][value="strict"]')
    expect(strict).not.toBeNull()
    strict!.checked = true
    strict!.dispatchEvent(new Event('change'))
    document.body.querySelector<HTMLButtonElement>('[data-testid="confirm-fingerprint"]')!.click()
    await flushPromises()

    expect(api.runOpenAIEval).toHaveBeenCalledWith({ account_id: 12, requested_model: 'gpt-5', reasoning_effort: 'high', test_type: 'fingerprint', sample_mode: 'strict' })
    expect(wrapper.text()).not.toContain('有未保存的更改')
    wrapper.unmount()
  })

  it('adds targets for several accounts and saves without dropping scheduling settings', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="open-add"]').trigger('click')
    await flushPromises()
    const model = document.body.querySelector<HTMLSelectElement>('[data-testid="add-model"]')!
    model.value = 'gpt-5'
    model.dispatchEvent(new Event('change'))
    for (const box of document.body.querySelectorAll<HTMLInputElement>('[data-testid="add-account"]')) {
      box.checked = true
      box.dispatchEvent(new Event('change'))
      // The checkbox-array v-model reads the rendered array, so let it re-render.
      await flushPromises()
    }
    await flushPromises()
    document.body.querySelector<HTMLButtonElement>('[data-testid="add-submit"]')!.click()
    await flushPromises()

    expect(wrapper.findAll('[data-testid="target"]')).toHaveLength(3)
    expect(wrapper.text()).toContain('有未保存的更改')
    // The new direct-OAuth default-effort target can run State Probe right away.
    expect(wrapper.get('[data-testid="run-state_probe"]').attributes('disabled')).toBeUndefined()

    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    const payload = api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig
    expect(payload.revision).toBe(9)
    expect(payload.scheduling_policy).toBe('stability_first')
    expect(payload.policies).toEqual([{ requested_model: 'gpt-5', reasoning_effort: 'high', policy: 'cost_first' }])
    expect(payload.effects_enabled).toBe(true)
    expect(payload.accounts.map(route => `${route.account_id}:${route.reasoning_effort}`)).toEqual(['12:high', '11:', '12:'])
    expect(payload.accounts.every(route => !('direct_oauth_eligible' in route))).toBe(true)
    wrapper.unmount()
  })
})
