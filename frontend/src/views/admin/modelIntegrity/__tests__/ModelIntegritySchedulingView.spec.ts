import { flushPromises, mount, RouterLinkStub } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { OpenAIEvalConfig } from '@/api/admin/accounts'
import { zhT } from './zhT'

const api = vi.hoisted(() => ({
  getOpenAIEvalModels: vi.fn(),
  getOpenAIEvalConfig: vi.fn(),
  saveOpenAIEvalConfig: vi.fn(),
  resetOpenAIBPSState: vi.fn(),
  list: vi.fn(),
  listSchedulerDecisions: vi.fn()
}))
const store = vi.hoisted(() => ({ showError: vi.fn(), showSuccess: vi.fn(), showInfo: vi.fn() }))

vi.mock('@/api/admin/accounts', () => ({
  default: api,
  accountsAPI: api,
  listSchedulerDecisions: api.listSchedulerDecisions
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => store }))
vi.mock('vue-i18n', async importOriginal => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: zhT })
}))
vi.mock('vue-router', async importOriginal => ({
  ...(await importOriginal<typeof import('vue-router')>()),
  useRoute: () => ({ path: '/admin/model-integrity/scheduling' }),
  onBeforeRouteLeave: vi.fn()
}))

import SchedulingView from '../ModelIntegritySchedulingView.vue'

const catalog = {
  items: [{ id: 'gpt-5' }, { id: 'gpt-5-mini' }],
  baseline_version: 'v1',
  baseline_models: [],
  candy: { expected_answer: 21, confidence: 'low', scheduling: 'alert_only' },
  evaluation_notice: '',
  reasoning_efforts: ['', 'high'],
  fingerprint_modes: [{ id: 'quick', samples: 60 }],
  modeltrace: { requests: 3, bank_revision: 'x', candidate_count: 16, scheduling: 'alert_only' }
}

const schedule = (interval: number, enabled = false) => ({ enabled, interval_seconds: interval, jitter_seconds: 0 })

function serverConfig(overrides: Partial<OpenAIEvalConfig> = {}): OpenAIEvalConfig {
  return {
    revision: 4,
    effects_enabled: false,
    bps_auto_enabled: true,
    scheduling_policy: '',
    policies: [],
    accounts: [
      {
        account_id: 11,
        requested_model: 'gpt-5',
        reasoning_effort: '',
        candy_schedule: schedule(900),
        fingerprint_schedule: { ...schedule(86400), sample_mode: 'quick' },
        modeltrace_schedule: schedule(86400),
        state_probe_schedule: schedule(21600, true),
        bps_auto: true,
        bps_mode: 'auto',
        bps_state: { active: false, degraded_streak: 0, healthy_streak: 0, disabled_reason: 'upstream_403' },
        direct_oauth_eligible: true
      },
      {
        account_id: 12,
        requested_model: 'gpt-5',
        reasoning_effort: 'high',
        candy_schedule: schedule(900, true),
        fingerprint_schedule: { ...schedule(86400), sample_mode: 'quick' },
        modeltrace_schedule: schedule(86400),
        state_probe_schedule: schedule(21600),
        bps_auto: false,
        direct_oauth_eligible: false
      }
    ],
    ...overrides
  }
}

function mountView() {
  return mount(SchedulingView, {
    global: {
      stubs: { AppLayout: { template: '<div><slot /></div>' }, RouterLink: RouterLinkStub, Teleport: true }
    }
  })
}

beforeEach(() => {
  vi.clearAllMocks()
  api.getOpenAIEvalModels.mockResolvedValue(catalog)
  api.getOpenAIEvalConfig.mockResolvedValue(serverConfig())
  api.list.mockResolvedValue({ items: [{ id: 11, name: 'oauth-a', platform: 'openai', type: 'oauth' }, { id: 12, name: 'apikey-b', platform: 'openai', type: 'apikey' }] })
  api.listSchedulerDecisions.mockResolvedValue({
    limit: 50,
    items: [{
      at: '2026-10-01T08:00:00Z',
      layer: 'load_balance',
      reason_code: 'load_balance_selection',
      reason_text: '',
      requested_model: 'gpt-5',
      scheduling_policy: 'cost_first',
      sticky_previous_hit: false,
      sticky_session_hit: false,
      candidate_count: 2,
      top_k: 1,
      latency_ms: 1,
      load_skew: 0,
      selected_account_id: 11,
      selected_account_type: 'oauth',
      excluded_account_count: 1,
      previous_response_given: false,
      session_given: false,
      candidates: [
        { account_id: 11, eligible: true, selected: true, in_top_k: true, score: 0.8, rate_multiplier: 0.5, error_rate: 0.01, ttft_ms: 420, decision_reason: 'load_balance_selection' },
        { account_id: 12, eligible: false, selected: false, in_top_k: false, exclusion_reason: 'model_not_supported' }
      ]
    }]
  })
  api.saveOpenAIEvalConfig.mockImplementation(async (payload: OpenAIEvalConfig) => ({ ...payload, revision: (payload.revision ?? 0) + 1 }))
})

describe('ModelIntegritySchedulingView', () => {
  it('saves the chosen policy with the loaded revision and keeps test-page settings', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="policy-avoid_degradation"]').setValue(true)
    expect(wrapper.text()).toContain('只在于更少考虑价格')
    await wrapper.get('.btn-primary').trigger('click')
    await flushPromises()

    const payload = api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig
    expect(payload.revision).toBe(4)
    expect(payload.scheduling_policy).toBe('avoid_degradation')
    expect(payload.accounts).toHaveLength(2)
    expect(payload.accounts[1].candy_schedule.enabled).toBe(true)
    expect(payload.accounts[0]).not.toHaveProperty('bps_state')
  })

  it('adds per-model overrides and blocks duplicates before saving', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="add-rule"]').trigger('click')
    await wrapper.get('[data-testid="add-rule"]').trigger('click')
    const rules = wrapper.findAll('[data-testid="policy-rule"]')
    for (const rule of rules) await rule.find('select').setValue('gpt-5')
    expect(wrapper.text()).toContain('这个模型和推理强度已经有规则了')
    await wrapper.get('.btn-primary').trigger('click')
    expect(api.saveOpenAIEvalConfig).not.toHaveBeenCalled()
  })

  it('lists BPS-capable targets and offers explicit force-on mode', async () => {
    const wrapper = mountView()
    await flushPromises()

    const rows = wrapper.findAll('[data-testid="bps-row"]')
    expect(rows).toHaveLength(1)
    const options = rows[0].findAll('option').map(option => option.attributes('value'))
    expect(options).toEqual(['auto', 'force_off', 'force_on'])
    expect(rows[0].text()).toContain('BPS 已停用')
    expect(rows[0].text()).toContain('返回 403')
    expect(wrapper.text()).toContain('另有 1 个测试对象不支持 BPS')
  })

  it('restores a locked BPS route through the reset API without touching unsaved config', async () => {
    api.resetOpenAIBPSState.mockResolvedValue({ state: { active: false, degraded_streak: 0, healthy_streak: 0 } })
    const wrapper = mountView()
    await flushPromises()

    const row = wrapper.get('[data-testid="bps-row"]')
    await row.get('button').trigger('click')
    wrapper.findComponent({ name: 'ConfirmDialog' }).vm.$emit('confirm')
    await flushPromises()

    expect(api.resetOpenAIBPSState).toHaveBeenCalledWith({ account_id: 11, requested_model: 'gpt-5' })
    expect(wrapper.get('[data-testid="bps-row"]').text()).toContain('原线路')
    expect(api.saveOpenAIEvalConfig).not.toHaveBeenCalled()
  })

  it('explains the latest decision and each candidate in plain words', async () => {
    const wrapper = mountView()
    await flushPromises()

    const row = wrapper.get('[data-testid="decision-row"]')
    expect(row.text()).toContain('选中 oauth-a #11')
    expect(row.text()).toContain('选法：优先低价')
    expect(row.text()).toContain('1 个可用，1 个被排除')
    await row.get('.ledger-toggle').trigger('click')
    const candidates = wrapper.findAll('[data-testid="candidate-row"]')
    expect(candidates[0].text()).toContain('选中')
    expect(candidates[1].text()).toContain('账号不支持这个模型')
    expect(wrapper.text()).not.toMatch(/路线资格|硬失败/)
  })

  it('shows a reload prompt instead of overwriting when the server reports a conflict', async () => {
    api.saveOpenAIEvalConfig.mockRejectedValue({ status: 409, message: 'conflict' })
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="policy-cost_first"]').setValue(true)
    await wrapper.get('.btn-primary').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('这份设置刚在别处被改过')
    expect(wrapper.get('.btn-primary').attributes('disabled')).toBeDefined()
    expect(store.showError).not.toHaveBeenCalled()
  })
})
