import { flushPromises, mount, RouterLinkStub } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { OpenAIEvalConfig } from '@/api/admin/accounts'
import { zhT } from './zhT'

const api = vi.hoisted(() => ({
  getOpenAIEvalModels: vi.fn(),
  getOpenAIEvalConfig: vi.fn(),
  saveOpenAIEvalConfig: vi.fn(),
  resetOpenAIBPSState: vi.fn(),
  refreshOpenAIEvalQuality: vi.fn(),
  evaluateOpenAIEvalRanking: vi.fn(),
  getOpenAIEvalRankings: vi.fn(),
  getOpenAIEvalRankingAccounts: vi.fn(),
  listOpenAIEvalRuns: vi.fn(),
  listOpenAIEvalAudit: vi.fn(),
  list: vi.fn(),
  listSchedulerDecisions: vi.fn()
}))
const groups = vi.hoisted(() => ({ getAll: vi.fn() }))
const store = vi.hoisted(() => ({ showError: vi.fn(), showSuccess: vi.fn(), showInfo: vi.fn() }))

vi.mock('@/api/admin/accounts', () => ({
  default: api,
  accountsAPI: api,
  listSchedulerDecisions: api.listSchedulerDecisions
}))
vi.mock('@/stores/app', () => ({ useAppStore: () => store }))
// The real client would attempt a network navigation, so the group list is stubbed.
vi.mock('@/api/admin/groups', () => ({ default: groups, groupsAPI: groups }))
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
  groups.getAll.mockResolvedValue([{ id: 7, name: 'openai-team', platform: 'openai' }])
  // An empty snapshot: the new panels render but claim nothing until a real
  // evaluation or a filtered read supplies a build.
  api.getOpenAIEvalRankings.mockResolvedValue({
    summary: null,
    effective_status: 'inactive_effects_off',
    current_config_revision: 4,
    evaluation_in_progress: false,
    ranking_error: null,
    groups: [],
    dimensions: [],
    next_cursor: null
  })
  api.getOpenAIEvalRankingAccounts.mockResolvedValue({
    summary: null,
    effective_status: 'active',
    current_config_revision: 4,
    evaluation_in_progress: false,
    ranking_error: null,
    groups: [],
    dimensions: [],
    next_cursor: null
  })
  api.evaluateOpenAIEvalRanking.mockResolvedValue({
    evaluation_id: 'instance-1-1',
    record_type: 'policy_evaluation',
    scope: 'this_instance',
    algorithm_version: 'v1',
    data_version: 'data-v1',
    evaluated_at: '2026-10-03T08:00:00Z',
    published_at: '2026-10-03T08:00:01Z',
    next_evaluation_at: '2026-10-03T09:00:00Z',
    next_evaluation_reason: 'interval',
    trigger: 'manual',
    config_revision: 4,
    effects_enabled: true,
    dimension_count: 0,
    account_count: 0,
    account_row_count: 0,
    quality_route_count: 0,
    truncated: false,
    coverage: { status: 'empty', discovery_complete: true, discovered_dimension_count: 0, cached_dimension_count: 0, uncached_dimension_count: 0, wildcard_routes_present: false, reasons: [] }
  })
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
    expect(wrapper.text()).toContain('勾选两项、通过一项为 50%，勾选三项、通过一项为 33.3%')
    expect(wrapper.text()).toContain('从通过率最高的一档中选择')
    expect(wrapper.text()).toContain('该档没有可用容量时再依次尝试下一档')
    expect(wrapper.text()).toContain('账号停用、模型支持、容量和续写响应的账号绑定仍实时判断')
    expect(wrapper.text()).not.toMatch(/实际有效回答的样本数/)
    expect(wrapper.text()).not.toMatch(/仅作提醒|不参与账号排序/)
    // A strict tier is stated, never drawn as a weight price could trade against.
    expect(wrapper.get('[data-testid="mixer-quality-avoid_degradation"]').text()).toBe('优先筛选')
    expect(wrapper.get('[data-testid="mixer-quality-stability_first"]').text()).toBe('不参考')
    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()

    const payload = api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig
    expect(payload.revision).toBe(4)
    expect(payload.scheduling_policy).toBe('avoid_degradation')
    expect(payload.accounts).toHaveLength(2)
    expect(payload.accounts[1].candy_schedule.enabled).toBe(true)
    expect(payload.accounts[0]).not.toHaveProperty('bps_state')
    // Legacy config without the field gets the 1-hour default.
    expect(payload.quality_refresh_interval_seconds).toBe(3600)
    expect(payload).not.toHaveProperty('quality_refreshed_at')
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
    const defaults = { cost: 0.2, error_rate: 0.43, ttft: 0.27, load: 0.1, quality: 0, stability: 0 }
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
    // Legacy stability 10% is folded 60/40 into error rate and first-token latency.
    expect(rules()[0].get('[data-testid="rule-weights-summary"]').text()).toBe('价格 10%，错误率 16%，首包延迟 14%，并发负载 60%，降智通过率 0%')
    expect(rules()[0].find('[data-testid="weight-cost"]').exists()).toBe(false)
    expect(rules()[1].find('[data-testid="rule-weights"]').exists()).toBe(false)

    // Switching a rule to custom balance seeds a copy and opens its editor.
    await rules()[1].get('[data-testid="rule-policy"]').setValue('custom_balance')
    const seeded = rules()[1]
    expect((seeded.get('[data-testid="weight-cost"]').element as HTMLInputElement).value).toBe('50')
    await seeded.get('[data-testid="weight-ttft"]').setValue('40')
    expect(seeded.get('[data-testid="rule-weights-summary"]').text()).toContain('首包延迟 33%')

    await rules()[0].get('[data-testid="rule-weights-toggle"]').trigger('click')
    await rules()[0].get('[data-testid="weight-error_rate"]').setValue('30')

    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    const payload = api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig
    const folded = (weights: typeof global) => ({ ...weights, error_rate: Number((weights.error_rate + weights.stability * 0.6).toFixed(6)), ttft: Number((weights.ttft + weights.stability * 0.4).toFixed(6)), stability: 0, quality: 0 })
    expect(payload.scheduling_policy).toBe('cost_first')
    expect(payload.custom_balance).toEqual(folded(global))
    expect(payload.policies?.[0]).toMatchObject({ requested_model: 'gpt-5-mini', policy: 'custom_balance', custom_balance: { ...folded(other), error_rate: 0.3 } })
    expect(payload.policies?.[1]).toMatchObject({ requested_model: 'gpt-5', reasoning_effort: 'high', policy: 'custom_balance', custom_balance: { ...folded(global), ttft: 0.4 } })
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
    expect(payload.policies?.[0].custom_balance).toEqual({ cost: 0, stability: 0, error_rate: 0, ttft: 0, load: 0.1, quality: 0 })
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
    // No candidate carries integrity evidence, so the column stays hidden.
    expect(wrapper.find('[data-testid="candidate-quality"]').exists()).toBe(false)
  })

  it('shows the pass rate as passed-plus-likely test verdicts over selected tests', async () => {
    const decisions = await api.listSchedulerDecisions()
    const trace = { ...decisions.items[0], scheduling_policy: 'avoid_degradation' }
    trace.candidates = [
      // Candy + Fingerprint + ModelTrace selected: Candy passed, ModelTrace likely passed, Fingerprint did not.
      { ...trace.candidates[0], quality_state: 'assessed', evaluated_count: 3, pass_count: 1, suspected_pass_count: 1, quality_ratio: 2 / 3, quality_contribution: 2 },
      { account_id: 13, eligible: true, selected: false, in_top_k: true, score: 0.4 }
    ]
    api.listSchedulerDecisions.mockResolvedValue({ limit: 50, items: [trace] })
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="decision-row"] .ledger-toggle').trigger('click')
    expect(wrapper.text()).toContain('降智通过率')
    const cells = wrapper.findAll('[data-testid="candidate-quality"]')
    expect(cells[0].text()).toContain('66.7%')
    expect(cells[0].text()).toContain('2/3 项测试通过')
    expect(cells[0].text()).toContain('通过 1 项，疑似通过 1 项')
    expect(cells[0].text()).not.toContain('样本')
    expect(cells[0].get('[data-testid="candidate-quality-contribution"]').text()).toBe('分数贡献 +2')
    // An eligible candidate without a pass rate is explicitly unknown, never 100 %.
    expect(cells[1].text()).toBe('未知')
    expect(cells[1].text()).not.toContain('100')
  })

  it('weighs each selected test equally: 2 selected 1 passed is 50%, 3 selected 1 passed is 33.3%', async () => {
    const decisions = await api.listSchedulerDecisions()
    const trace = { ...decisions.items[0], scheduling_policy: 'avoid_degradation' }
    trace.candidates = [
      // Candy (10 samples) + ModelTrace (3 samples): one verdict each, so sample counts never matter.
      { ...trace.candidates[0], quality_state: 'assessed', evaluated_count: 2, pass_count: 1, suspected_pass_count: 0, quality_ratio: 0.5, quality_contribution: 0 },
      { account_id: 13, eligible: true, selected: false, in_top_k: true, score: 0.4, quality_state: 'assessed', evaluated_count: 3, pass_count: 0, suspected_pass_count: 1, quality_ratio: 1 / 3 },
      // A stale ratio next to an unassessed state is still unknown.
      { account_id: 14, eligible: true, selected: false, in_top_k: false, score: 0.2, quality_state: 'unassessed', evaluated_count: 2, pass_count: 2, suspected_pass_count: 0, quality_ratio: 1 }
    ]
    api.listSchedulerDecisions.mockResolvedValue({ limit: 50, items: [trace] })
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="decision-row"] .ledger-toggle').trigger('click')
    const cells = wrapper.findAll('[data-testid="candidate-quality"]')
    expect(cells[0].text()).toContain('50.0%')
    expect(cells[0].text()).toContain('1/2 项测试通过')
    expect(cells[1].text()).toContain('33.3%')
    expect(cells[1].text()).toContain('1/3 项测试通过')
    expect(cells[1].text()).toContain('通过 0 项，疑似通过 1 项')
    expect(cells[2].text()).toBe('未知')
  })

  it('marks every candidate unknown under avoid degradation when no pass rate exists yet', async () => {
    const decisions = await api.listSchedulerDecisions()
    const trace = { ...decisions.items[0], scheduling_policy: 'avoid_degradation' }
    api.listSchedulerDecisions.mockResolvedValue({ limit: 50, items: [trace] })
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="decision-row"] .ledger-toggle').trigger('click')
    const cells = wrapper.findAll('[data-testid="candidate-quality"]')
    expect(cells.map(cell => cell.text())).toEqual(['未知', '—'])
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

describe('ModelIntegritySchedulingView integrity pass rate controls', () => {
  it('edits only cost, error rate, latency, load and pass rate, and saves a quality-only balance', async () => {
    api.getOpenAIEvalConfig.mockResolvedValue(serverConfig({ scheduling_policy: 'custom_balance', custom_balance: { cost: 0.2, error_rate: 0.43, ttft: 0.27, load: 0.1, quality: 0 } }))
    const wrapper = mountView()
    await flushPromises()
    const editor = wrapper.get('[data-testid="custom-balance"]')
    expect(editor.find('[data-testid="weight-stability"]').exists()).toBe(false)
    expect(editor.findAll('input').map(input => input.attributes('data-testid'))).toEqual(['weight-cost', 'weight-error_rate', 'weight-ttft', 'weight-load', 'weight-quality'])
    expect(editor.text()).toContain('降智通过率')
    expect(editor.text()).toContain('与降智无关')
    expect(editor.text()).not.toContain('稳定性')
    expect(wrapper.find('[data-testid="legacy-stability-folded"]').exists()).toBe(false)

    for (const factor of ['cost', 'error_rate', 'ttft', 'load']) await editor.get(`[data-testid="weight-${factor}"]`).setValue('0')
    expect(editor.find('[data-testid="weights-error"]').exists()).toBe(true)
    await editor.get('[data-testid="weight-quality"]').setValue('100')
    expect(editor.find('[data-testid="weights-error"]').exists()).toBe(false)

    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    const payload = api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig
    expect(payload.custom_balance).toEqual({ cost: 0, error_rate: 0, ttft: 0, load: 0, quality: 1, stability: 0 })
  })

  it('folds a legacy stability weight on load, says so, and saves it without double folding', async () => {
    const legacy = { cost: 0.2, stability: 0.3, error_rate: 0.25, ttft: 0.15, load: 0.1 }
    const config = serverConfig()
    api.getOpenAIEvalConfig.mockResolvedValue({
      ...config,
      scheduling_policy: 'custom_balance',
      custom_balance: legacy,
      policies: [{ requested_model: 'gpt-5', reasoning_effort: 'high', policy: 'custom_balance', custom_balance: { ...legacy, quality: 0.5 } }]
    })
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.get('[data-testid="legacy-stability-folded"]').text()).toContain('60% 错误率、40% 首包延迟')
    expect((wrapper.get('[data-testid="custom-balance"] [data-testid="weight-error_rate"]').element as HTMLInputElement).value).toBe('43')
    // Folding on read alone is not an unsaved change: the scheduler already applies the same split.
    expect(wrapper.text()).not.toContain('有未保存的更改')

    await wrapper.get('[data-testid="custom-balance"] [data-testid="weight-load"]').setValue('20')
    // After saving, the server returns the folded weights.
    api.saveOpenAIEvalConfig.mockImplementation(async (payload: OpenAIEvalConfig) => ({ ...payload, revision: (payload.revision ?? 0) + 1 }))
    api.getOpenAIEvalConfig.mockImplementation(async () => ({ ...(api.saveOpenAIEvalConfig.mock.calls.at(-1)![0] as OpenAIEvalConfig), revision: 5 }))
    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    const first = api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig
    const folded = { cost: 0.2, error_rate: 0.43, ttft: 0.27, load: 0.1, quality: 0, stability: 0 }
    expect(first.custom_balance).toEqual({ ...folded, load: 0.2 })
    expect(first.policies?.[0]).toMatchObject({ requested_model: 'gpt-5', reasoning_effort: 'high', custom_balance: { ...folded, quality: 0.5 } })
    expect(wrapper.find('[data-testid="legacy-stability-folded"]').exists()).toBe(false)

    // A second save from the echoed config must send the same folded values, not fold again.
    await wrapper.get('[data-testid="custom-balance"] [data-testid="weight-load"]').setValue('10')
    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    const second = api.saveOpenAIEvalConfig.mock.calls.at(-1)![0] as OpenAIEvalConfig
    expect(second.custom_balance).toEqual(folded)
    expect(second.policies?.[0].custom_balance).toEqual({ ...folded, quality: 0.5 })
  })

  it('offers the shared interval presets plus whole-minute custom values and saves them', async () => {
    api.getOpenAIEvalConfig.mockResolvedValue(serverConfig({ quality_refresh_interval_seconds: 21600 }))
    const wrapper = mountView()
    await flushPromises()
    const select = wrapper.get('[data-testid="quality-interval"]')
    expect((select.element as HTMLSelectElement).value).toBe('21600')
    expect(select.findAll('option').map(option => option.text())).toEqual(['每 5 分钟', '每 10 分钟', '每 30 分钟', '每 1 小时', '每 6 小时', '每 12 小时', '每 24 小时', '自定义'])
    expect(wrapper.find('[data-testid="quality-interval-pending"]').exists()).toBe(false)

    await select.setValue('custom')
    const minutes = wrapper.get('[data-testid="quality-interval-minutes"]')
    await minutes.setValue('2')
    await minutes.trigger('change')
    expect((minutes.element as HTMLInputElement).value).toBe('5')
    await minutes.setValue('95')
    await minutes.trigger('change')
    expect(wrapper.get('[data-testid="quality-interval-pending"]').text()).toContain('当前仍按每 6 小时刷新')

    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    expect((api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig).quality_refresh_interval_seconds).toBe(95 * 60)
  })

  it('refreshes the ranking now, shows the result and keeps unsaved edits unsaved', async () => {
    api.getOpenAIEvalConfig.mockResolvedValue(serverConfig({ quality_refreshed_at: null }))
    let finish!: (value: unknown) => void
    api.refreshOpenAIEvalQuality.mockImplementation(() => new Promise(resolve => { finish = resolve }))
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.get('[data-testid="quality-refresh-status"]').text()).toContain('尚未刷新')

    await wrapper.get('[data-testid="quality-interval"]').setValue('600')
    expect(wrapper.text()).toContain('有未保存的更改')
    const button = wrapper.get('[data-testid="quality-refresh-now"]')
    await button.trigger('click')
    expect(button.attributes('disabled')).toBeDefined()
    expect(button.text()).toContain('正在刷新')
    await button.trigger('click')
    expect(api.refreshOpenAIEvalQuality).toHaveBeenCalledTimes(1)

    finish({ refreshed_at: '2026-10-03T08:00:00Z', next_refresh_at: '2026-10-03T09:00:00Z', route_count: 7 })
    await flushPromises()
    expect(store.showSuccess).toHaveBeenCalledWith('已按 7 条带有证据的路由重建排序。')
    expect(wrapper.get('[data-testid="quality-refresh-routes"]').text()).toBe('本次刷新 7 条带有证据的路由')
    expect(wrapper.get('[data-testid="quality-refresh-status"]').text()).toContain('上次刷新')
    expect(wrapper.get('[data-testid="quality-refresh-status"]').text()).toContain('下次')
    expect(button.attributes('disabled')).toBeUndefined()
    // The unsaved interval is untouched and still pending; nothing was saved.
    expect((wrapper.get('[data-testid="quality-interval"]').element as HTMLSelectElement).value).toBe('600')
    expect(wrapper.text()).toContain('有未保存的更改')
    expect(wrapper.find('[data-testid="quality-interval-pending"]').exists()).toBe(true)
    expect(api.saveOpenAIEvalConfig).not.toHaveBeenCalled()
  })

  it('reports a failed refresh with the server message and keeps the last refresh time', async () => {
    api.getOpenAIEvalConfig.mockResolvedValue(serverConfig({ quality_refreshed_at: '2026-10-03T07:00:00Z' }))
    api.refreshOpenAIEvalQuality.mockRejectedValue({ message: 'quality refresh is already running' })
    const wrapper = mountView()
    await flushPromises()
    const before = wrapper.get('[data-testid="quality-refresh-status"]').text()
    await wrapper.get('[data-testid="quality-refresh-now"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="quality-refresh-error"]').text()).toBe('quality refresh is already running')
    expect(store.showError).toHaveBeenCalledWith('quality refresh is already running')
    expect(wrapper.get('[data-testid="quality-refresh-status"]').text()).toBe(before)
    expect(wrapper.get('[data-testid="quality-refresh-now"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.text()).not.toContain('有未保存的更改')
  })

  it('warns that the pass rate is inactive while evaluation effects are off', async () => {
    api.getOpenAIEvalConfig.mockResolvedValue(serverConfig({ effects_enabled: false, scheduling_policy: 'stability_first' }))
    const wrapper = mountView()
    await flushPromises()
    // Stability first ignores the pass rate, so there is nothing to warn about.
    expect(wrapper.find('[data-testid="quality-effects-off"]').exists()).toBe(false)
    await wrapper.get('[data-testid="policy-avoid_degradation"]').setValue(true)
    // Selecting a real policy turns effects on visibly instead of leaving a
    // policy saved but silently unused.
    expect(wrapper.get('[data-testid="effects-auto-note"]').text()).toContain('已自动置为开启')
    expect((wrapper.get('[data-testid="effects-toggle"]').element as HTMLButtonElement).getAttribute('aria-checked')).toBe('true')
  })

  it('keeps request errors and first-token latency apart from the pass rate in the ledger', async () => {
    const decisions = await api.listSchedulerDecisions()
    const trace = { ...decisions.items[0], scheduling_policy: 'avoid_degradation' }
    trace.candidates = [{ ...trace.candidates[0], error_rate: 0.4, ttft_ms: 3000, evaluated_count: 3, pass_count: 3, suspected_pass_count: 0, quality_ratio: 1, quality_contribution: 0 }]
    api.listSchedulerDecisions.mockResolvedValue({ limit: 50, items: [trace] })
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="decision-row"] .ledger-toggle').trigger('click')
    const row = wrapper.get('[data-testid="candidate-row"]')
    expect(row.text()).toContain('40.0%')
    expect(row.get('[data-testid="candidate-quality"]').text()).toContain('100.0%')
    expect(row.get('[data-testid="candidate-quality"]').text()).toContain('3/3 项测试通过')
    expect(row.find('[data-testid="candidate-quality-contribution"]').exists()).toBe(false)
  })
})
