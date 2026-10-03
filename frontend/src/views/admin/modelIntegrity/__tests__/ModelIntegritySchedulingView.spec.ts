import { flushPromises, mount, RouterLinkStub } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { OpenAIEvalConfig } from '@/api/admin/accounts'
import { zhT } from './zhT'

const api = vi.hoisted(() => ({
  getOpenAIEvalModels: vi.fn(),
  getOpenAIEvalConfig: vi.fn(),
  saveOpenAIEvalConfig: vi.fn(),
  resetOpenAIBPSState: vi.fn(),
  listOpenAIEvalRuns: vi.fn(),
  listOpenAIEvalAudit: vi.fn(),
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
  candy: { expected_answer: 29, confidence: 'low', scheduling: 'alert_only' },
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
    bps_accounts: [{
      account_id: 11,
      probe_model: 'gpt-5',
      mode: 'auto',
      failure_threshold: 3,
      recovery_threshold: 2,
      interval_seconds: 21600,
      active: false,
      state: 'locked',
      disabled_reason: 'upstream_403',
      degraded_streak: 0,
      healthy_streak: 0
    }],
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
  api.listOpenAIEvalRuns.mockResolvedValue({ items: [] })
  api.listOpenAIEvalAudit.mockResolvedValue({ items: [] })
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
    expect(wrapper.text()).toContain('区别主要在于价格权重更低')
    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
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
    expect(wrapper.text()).toContain('该模型与推理强度的规则已存在')
    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    expect(api.saveOpenAIEvalConfig).not.toHaveBeenCalled()
  })

  it('lists BPS-capable targets and offers explicit force-on mode', async () => {
    const wrapper = mountView()
    await flushPromises()

    const rows = wrapper.findAll('[data-testid="bps-row"]')
    expect(rows).toHaveLength(1)
    expect(rows[0].text()).toContain('BPS 已停用')
    expect(rows[0].text()).toContain('返回 403')
    expect(wrapper.text()).toContain('1 个测试对象仍保留升级前的模型级 BPS 配置')
    await rows[0].get('[data-testid="bps-edit"]').trigger('click')
    expect(wrapper.get('[data-testid="bps-mode-auto"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="bps-mode-force_off"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="bps-mode-force_on"]').exists()).toBe(true)
  })

  it('uses the shared interval presets and preserves a custom BPS interval', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="bps-edit"]').trigger('click')
    const select = wrapper.get('[data-testid="bps-interval"]')
    expect(select.text()).toContain('每 5 分钟')
    expect(select.text()).toContain('每 24 小时')
    await select.setValue('custom')
    const custom = wrapper.get('[data-testid="bps-custom-interval"]')
    expect(custom.attributes('max')).toBe('35791394')
    expect(wrapper.text()).toContain('存储上限 35,791,394')
    await custom.setValue(17)
    await wrapper.get('[data-testid="bps-dialog"]').trigger('submit')
    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()

    const payload = api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig
    expect(payload.bps_accounts?.[0].interval_seconds).toBe(17 * 60)
  })

  it('normalizes an old all-zero custom balance response before saving a new custom rule', async () => {
    api.getOpenAIEvalConfig.mockResolvedValue(serverConfig({
      scheduling_policy: 'custom_balance',
      custom_balance: { cost: 0, stability: 0, error_rate: 0, ttft: 0, load: 0 }
    }))
    const wrapper = mountView()
    await flushPromises()

    expect(wrapper.get('[data-testid="custom-balance"]').find('input').element.value).toBe('20')
    await wrapper.get('[data-testid="add-rule"]').trigger('click')
    const rule = wrapper.get('[data-testid="policy-rule"]')
    const selects = rule.findAll('select')
    await selects[0].setValue('gpt-5')
    await selects[2].setValue('custom_balance')
    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()

    const payload = api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig
    const defaults = { cost: 0.2, stability: 0.3, error_rate: 0.25, ttft: 0.15, load: 0.1 }
    expect(payload.custom_balance).toEqual(defaults)
    expect(payload.policies?.[0].custom_balance).toEqual(defaults)
  })

  it('edits custom-balance rule weights while the site default is cost first, without touching other weights', async () => {
    const global = { cost: 0.5, stability: 0.2, error_rate: 0.1, ttft: 0.1, load: 0.1 }
    const other = { cost: 0.1, stability: 0.1, error_rate: 0.1, ttft: 0.1, load: 0.6 }
    api.getOpenAIEvalConfig.mockResolvedValue(serverConfig({
      scheduling_policy: 'cost_first',
      custom_balance: global,
      policies: [
        { requested_model: 'gpt-5-mini', reasoning_effort: '', policy: 'custom_balance', custom_balance: other },
        { requested_model: 'gpt-5', reasoning_effort: 'high', policy: 'stability_first' }
      ]
    }))
    const wrapper = mountView()
    await flushPromises()

    // The site default is not custom, so only the rule-level editor exists.
    expect(wrapper.find('[data-testid="custom-balance"]').exists()).toBe(false)
    const rules = () => wrapper.findAll('[data-testid="policy-rule"]')
    expect(rules()[0].get('[data-testid="rule-weights-summary"]').text()).toBe('价格 10%，稳定性 10%，错误率 10%，首包延迟 10%，并发负载 60%')
    expect(rules()[0].find('[data-testid="weight-cost"]').exists()).toBe(false)
    expect(rules()[1].find('[data-testid="rule-weights"]').exists()).toBe(false)

    // Switching a rule to custom balance seeds a copy and opens its editor.
    await rules()[1].get('[data-testid="rule-policy"]').setValue('custom_balance')
    const seeded = rules()[1]
    expect((seeded.get('[data-testid="weight-cost"]').element as HTMLInputElement).value).toBe('50')
    await seeded.get('[data-testid="weight-ttft"]').setValue('40')
    expect(seeded.get('[data-testid="rule-weights-summary"]').text()).toContain('首包延迟 31%')

    await rules()[0].get('[data-testid="rule-weights-toggle"]').trigger('click')
    await rules()[0].get('[data-testid="weight-error_rate"]').setValue('30')

    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    const payload = api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig
    expect(payload.scheduling_policy).toBe('cost_first')
    expect(payload.custom_balance).toEqual(global)
    expect(payload.policies?.[0]).toMatchObject({ requested_model: 'gpt-5-mini', policy: 'custom_balance', custom_balance: { ...other, error_rate: 0.3 } })
    expect(payload.policies?.[1]).toMatchObject({ requested_model: 'gpt-5', reasoning_effort: 'high', policy: 'custom_balance', custom_balance: { ...global, ttft: 0.4 } })
  })

  it('keeps each rule weight object separate from the site default after a rule is seeded', async () => {
    api.getOpenAIEvalConfig.mockResolvedValue(serverConfig({ scheduling_policy: 'custom_balance' }))
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="add-rule"]').trigger('click')
    const rule = wrapper.get('[data-testid="policy-rule"]')
    await rule.find('select').setValue('gpt-5')
    await rule.get('[data-testid="rule-policy"]').setValue('custom_balance')
    await wrapper.get('[data-testid="custom-balance"]').get('[data-testid="weight-cost"]').setValue('90')

    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    const payload = api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig
    expect(payload.custom_balance?.cost).toBe(0.9)
    expect(payload.policies?.[0].custom_balance?.cost).toBe(0.2)
  })

  it('flags an all-zero rule weight set inline and blocks saving instead of silently resetting it', async () => {
    api.getOpenAIEvalConfig.mockResolvedValue(serverConfig({
      scheduling_policy: 'cost_first',
      policies: [{ requested_model: 'gpt-5', reasoning_effort: '', policy: 'custom_balance', custom_balance: { cost: 0.5, stability: 0, error_rate: 0, ttft: 0, load: 0 } }]
    }))
    const wrapper = mountView()
    await flushPromises()

    const rule = () => wrapper.get('[data-testid="policy-rule"]')
    const toggle = rule().get('[data-testid="rule-weights-toggle"]')
    expect(toggle.attributes('aria-expanded')).toBe('false')
    await toggle.trigger('click')
    expect(toggle.attributes('aria-expanded')).toBe('true')
    expect(rule().find(`#${toggle.attributes('aria-controls')}`).exists()).toBe(true)

    const cost = rule().get('[data-testid="weight-cost"]')
    await cost.setValue('250')
    expect((cost.element as HTMLInputElement).value).toBe('100')
    await cost.setValue('0')
    const error = rule().get('[data-testid="weights-error"]')
    expect(error.text()).toBe('权重合计须大于 0，请至少为一项设置权重。')
    expect(cost.attributes('aria-invalid')).toBe('true')
    expect(cost.attributes('aria-describedby')).toBe(error.attributes('id'))
    expect(rule().get('[data-testid="rule-weights-summary"]').text()).toContain('权重合计须大于 0')

    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    expect(api.saveOpenAIEvalConfig).not.toHaveBeenCalled()
    expect(store.showError).toHaveBeenCalledWith('权重合计须大于 0，请至少为一项设置权重。')

    await rule().get('[data-testid="weight-load"]').setValue('10')
    expect(rule().find('[data-testid="weights-error"]').exists()).toBe(false)
    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    const payload = api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig
    expect(payload.policies?.[0].custom_balance).toEqual({ cost: 0, stability: 0, error_rate: 0, ttft: 0, load: 0.1 })
  })

  it('restores a locked BPS route through the reset API without touching unsaved config', async () => {
    api.resetOpenAIBPSState.mockResolvedValue({ state: { active: false, degraded_streak: 0, healthy_streak: 0 } })
    const wrapper = mountView()
    await flushPromises()

    const row = wrapper.get('[data-testid="bps-row"]')
    await row.get('[data-testid="bps-reset"]').trigger('click')
    await flushPromises()
    const dialogs = wrapper.findAllComponents({ name: 'ConfirmDialog' })
    const resetDialog = dialogs.find(dialog => dialog.props('show') === true)
    expect(resetDialog).toBeDefined()
    resetDialog!.vm.$emit('confirm')
    await flushPromises()

    expect(api.resetOpenAIBPSState).toHaveBeenCalledWith({ account_id: 11 })
    expect(wrapper.get('[data-testid="bps-row"]').text()).toContain('原线路')
    expect(api.saveOpenAIEvalConfig).not.toHaveBeenCalled()
  })

  it('explains the latest decision and each candidate in plain words', async () => {
    const wrapper = mountView()
    await flushPromises()

    const row = wrapper.get('[data-testid="decision-row"]')
    expect(row.text()).toContain('选中 oauth-a #11')
    expect(row.text()).toContain('策略：优先低价')
    expect(row.text()).toContain('1 个可用，1 个已排除')
    await row.get('.ledger-toggle').trigger('click')
    const candidates = wrapper.findAll('[data-testid="candidate-row"]')
    expect(candidates[0].text()).toContain('选中')
    expect(candidates[1].text()).toContain('账号不支持该模型')
    expect(wrapper.text()).not.toMatch(/路线资格|硬失败/)
  })

  it('shows a reload prompt instead of overwriting when the server reports a conflict', async () => {
    api.saveOpenAIEvalConfig.mockRejectedValue({ status: 409, message: 'conflict' })
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="policy-cost_first"]').setValue(true)
    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()

    expect(wrapper.text()).toContain('该配置已在其他页面或由其他管理员修改')
    expect(wrapper.get('[data-testid="model-integrity-save"]').attributes('disabled')).toBeDefined()
    expect(store.showError).not.toHaveBeenCalled()
  })
})
