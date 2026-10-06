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
  getOpenAIEvalAccountOverview: vi.fn(),
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

/** Request records live on their own tab, next to the evaluation records. */
async function mountOnRequests() {
  const wrapper = mountView()
  await flushPromises()
  await wrapper.get('[data-testid="records-tab-requests"]').trigger('click')
  return wrapper
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
      group_id: 7,
      group_name: 'openai-team',
      selected_account_id: 11,
      selected_account_name: 'oauth-a',
      selected_account_type: 'oauth',
      acquired: true,
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
    expect(wrapper.text()).toContain('该档没有未超阈值的账号或没有可用容量时，再依次尝试下一档')
    expect(wrapper.text()).toContain('同一档内按价格排序')
    expect(wrapper.text()).toContain('账号停用、模型支持、容量和续写响应的账号绑定仍实时判断')
    expect(wrapper.text()).not.toMatch(/实际有效回答的样本数/)
    expect(wrapper.text()).not.toMatch(/仅作提醒|不参与账号排序/)
    // The pass rate is its own comparison step, stated rather than drawn as a
    // weight price could trade against.
    expect(wrapper.get('[data-testid="mixer-quality-avoid_degradation"]').text()).toBe('降智通过率更高')
    // Stability first ignores the pass rate entirely, so its order has no
    // pass-rate step to show and cannot read as a weight of 0 %.
    expect(wrapper.find('[data-testid="mixer-quality-stability_first"]').exists()).toBe(false)
    // The policy cards state the order, never a stale preset percentage.
    const orderText = wrapper.get('[data-testid="mixer-order"]').text()
    expect(orderText).toContain('价格更低')
    expect(orderText).not.toMatch(/%/)
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

  it('describes custom balance as weighted factors, without a threshold step', async () => {
    const wrapper = mountView()
    await flushPromises()

    // rankingThresholdReasons returns nil for custom balance, so nothing is
    // screened out by a threshold: the card must show the weighted order alone.
    const card = wrapper.findAll('.mixer-option').find(option => (option.find('input').element as HTMLInputElement).value === 'custom_balance')!
    const steps = card.findAll('.mixer-order-step')
    expect(steps.map(step => step.find('.mixer-order-text').text())).toEqual(['自定义加权分更高'])
    expect(card.find('[data-testid="mixer-order"]').text()).not.toContain('未超运行阈值')
    // One step is not a sequence, so it carries no step number.
    expect(card.find('.mixer-order-num').exists()).toBe(false)
    // The factors that decide that score are the ones the page lets admins set.
    expect(card.text()).toContain('按自定义的价格、错误率、首包延迟、负载与降智通过率权重排序')
    expect(card.text()).toContain('不应用运行阈值')
  })

  it('edits an ordered list of absolute priorities and describes the real order on the card', async () => {
    api.getOpenAIEvalConfig.mockResolvedValue(serverConfig({
      scheduling_policy: 'custom_balance',
      // 'price' is a server alias; the editor reads it as the price factor.
      custom_balance: { cost: 0, error_rate: 0.5, ttft: 0.5, load: 0, quality: 0, absolute_priorities: ['price'] }
    }))
    const wrapper = mountView()
    await flushPromises()
    const editor = wrapper.get('[data-testid="custom-balance"]')
    const card = () => wrapper.findAll('.mixer-option').find(option => (option.find('input').element as HTMLInputElement).value === 'custom_balance')!
    const rows = () => editor.findAll('[data-testid="priority-row"]').map(row => row.find('.priority-name').text())

    expect(rows()).toEqual([expect.stringContaining('价格更低')])
    // A prioritized factor may weigh 0 %; that is not the all-zero error.
    expect(editor.find('[data-testid="weights-error"]').exists()).toBe(false)
    expect(editor.get('[data-testid="weight-rank-cost"]').text()).toBe('第 1 优先')
    expect(card().findAll('.mixer-order-text').map(step => step.text())).toEqual(['价格更低', '仍相同时，自定义加权分更高'])
    expect(card().text()).toContain('先按你设定的优先因素逐项严格比较')
    expect(card().text()).not.toContain('按自定义的价格、错误率、首包延迟、负载与降智通过率权重排序')

    await editor.get('[data-testid="priority-add-select"]').setValue('error_rate')
    await editor.get('[data-testid="priority-add"]').trigger('click')
    expect(rows()).toEqual([expect.stringContaining('价格更低'), expect.stringContaining('错误率更低')])
    expect(editor.get('[data-testid="priority-announce"]').text()).toBe('已将错误率添加为第 2 优先。')
    // The first row cannot move up and the last cannot move down.
    expect(editor.get('[data-testid="priority-up-cost"]').attributes('disabled')).toBeDefined()
    expect(editor.get('[data-testid="priority-down-error_rate"]').attributes('disabled')).toBeDefined()
    expect(editor.get('[data-testid="priority-up-error_rate"]').attributes('aria-label')).toBe('将错误率上移')

    await editor.get('[data-testid="priority-up-error_rate"]').trigger('click')
    expect(rows()).toEqual([expect.stringContaining('错误率更低'), expect.stringContaining('价格更低')])
    expect(card().findAll('.mixer-order-text').map(step => step.text())).toEqual(['错误率更低', '价格更低', '仍相同时，自定义加权分更高'])

    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    const payload = api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig
    expect(payload.custom_balance?.absolute_priorities).toEqual(['error_rate', 'cost'])
    expect(payload.custom_balance?.cost).toBe(0)
  })

  it('sends an empty priority list after the last priority is removed, so the server clears it', async () => {
    api.getOpenAIEvalConfig.mockResolvedValue(serverConfig({
      scheduling_policy: 'custom_balance',
      custom_balance: { cost: 0.5, error_rate: 0.5, ttft: 0, load: 0, quality: 0, absolute_priorities: ['quality'] }
    }))
    const wrapper = mountView()
    await flushPromises()
    const editor = wrapper.get('[data-testid="custom-balance"]')
    await editor.get('[data-testid="priority-remove-quality"]').trigger('click')
    expect(editor.find('[data-testid="priority-row"]').exists()).toBe(false)
    expect(editor.get('[data-testid="priority-empty"]').text()).toBe('未设置优先因素，账号只按加权分排序。')

    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    const payload = api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig
    expect(payload.custom_balance).toHaveProperty('absolute_priorities', [])
  })

  it('saves a prioritized set whose weights are all zero as it is, and says account order breaks the ties', async () => {
    api.getOpenAIEvalConfig.mockResolvedValue(serverConfig({
      scheduling_policy: 'custom_balance',
      custom_balance: { cost: 1, error_rate: 0, ttft: 0, load: 0, quality: 0, absolute_priorities: ['cost'] }
    }))
    const wrapper = mountView()
    await flushPromises()
    const editor = wrapper.get('[data-testid="custom-balance"]')
    const card = () => wrapper.findAll('.mixer-option').find(option => (option.find('input').element as HTMLInputElement).value === 'custom_balance')!
    await editor.get('[data-testid="weight-cost"]').setValue('0')
    expect(editor.find('[data-testid="weights-error"]').exists()).toBe(false)
    expect(editor.get('[data-testid="weight-cost"]').attributes('aria-invalid')).toBeUndefined()
    expect(editor.get('[data-testid="priority-then"]').text()).toBe('所有权重均为 0%，之后仍相同时按账号 ID 排序。')
    expect(card().findAll('.mixer-order-text').map(step => step.text())).toEqual(['价格更低', '仍相同时，按账号 ID 排序'])

    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    expect(api.saveOpenAIEvalConfig).toHaveBeenCalledTimes(1)
    const payload = api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig
    // Never replaced by the defaults.
    expect(payload.custom_balance).toEqual({ cost: 0, error_rate: 0, ttft: 0, load: 0, quality: 0, stability: 0, absolute_priorities: ['cost'] })
  })

  it('loads all-zero weights with a priority order without resetting them, and blocks once the last priority is removed', async () => {
    api.getOpenAIEvalConfig.mockResolvedValue(serverConfig({
      scheduling_policy: 'cost_first',
      custom_balance: { cost: 0, error_rate: 0, ttft: 0, load: 0, quality: 0, absolute_priorities: ['quality', 'cost'] },
      policies: [{ requested_model: 'gpt-5', reasoning_effort: '', policy: 'custom_balance', custom_balance: { cost: 0, error_rate: 0, ttft: 0, load: 0, quality: 0, absolute_priorities: ['error_rate'] } }]
    }))
    const wrapper = mountView()
    await flushPromises()
    const rule = () => wrapper.get('[data-testid="policy-rule"]')
    expect(rule().get('[data-testid="rule-weights-summary"]').text()).toBe('优先：错误率；权重均为 0%，其后按账号 ID')
    expect(rule().get('[data-testid="rule-weights-summary"]').classes()).not.toContain('rule-weights-invalid')

    await rule().get('[data-testid="rule-weights-toggle"]').trigger('click')
    expect((rule().get('[data-testid="weight-error_rate"]').element as HTMLInputElement).value).toBe('0')
    await rule().get('[data-testid="priority-remove-error_rate"]').trigger('click')
    expect(rule().get('[data-testid="weights-error"]').text()).toBe('权重合计须大于 0，请至少为一项设置权重，或添加优先因素。')
    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    expect(api.saveOpenAIEvalConfig).not.toHaveBeenCalled()

    // Adding a priority back makes the same zero weights saveable.
    await rule().get('[data-testid="priority-add-select"]').setValue('ttft')
    await rule().get('[data-testid="priority-add"]').trigger('click')
    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    const payload = api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig
    expect(payload.policies?.[0].custom_balance).toEqual({ cost: 0, error_rate: 0, ttft: 0, load: 0, quality: 0, stability: 0, absolute_priorities: ['ttft'] })
    expect(payload.custom_balance).toEqual({ cost: 0, error_rate: 0, ttft: 0, load: 0, quality: 0, stability: 0, absolute_priorities: ['quality', 'cost'] })
  })

  it('keeps model-rule priorities separate from the default and saves them in order', async () => {
    api.getOpenAIEvalConfig.mockResolvedValue(serverConfig({
      scheduling_policy: 'cost_first',
      custom_balance: { cost: 0.5, error_rate: 0.5, ttft: 0, load: 0, quality: 0, absolute_priorities: ['quality'] },
      policies: [{ requested_model: 'gpt-5', reasoning_effort: '', policy: 'custom_balance', custom_balance: { cost: 0, error_rate: 1, ttft: 0, load: 0, quality: 0, absolute_priorities: ['cost', 'error_rate'] } }]
    }))
    const wrapper = mountView()
    await flushPromises()
    const rule = () => wrapper.get('[data-testid="policy-rule"]')
    expect(rule().get('[data-testid="rule-weights-summary"]').text()).toBe('优先：价格，其次错误率；其后按权重：价格 0%，错误率 100%，首包延迟 0%，并发负载 0%，降智通过率 0%')

    await rule().get('[data-testid="rule-weights-toggle"]').trigger('click')
    await rule().get('[data-testid="priority-down-cost"]').trigger('click')
    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    const payload = api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig
    expect(payload.policies?.[0].custom_balance?.absolute_priorities).toEqual(['error_rate', 'cost'])
    expect(payload.custom_balance?.absolute_priorities).toEqual(['quality'])
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
    // An empty priority list is always sent: the server keeps its stored list when the field is missing.
    const defaults = { cost: 0.2, error_rate: 0.43, ttft: 0.27, load: 0.1, quality: 0, stability: 0, absolute_priorities: [] }
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
    expect(payload.custom_balance).toEqual({ ...folded(global), absolute_priorities: [] })
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
    expect(error.text()).toBe('权重合计须大于 0，请至少为一项设置权重，或添加优先因素。')
    expect(cost.attributes('aria-invalid')).toBe('true')
    expect(cost.attributes('aria-describedby')).toBe(error.attributes('id'))
    expect(rule().get('[data-testid="rule-weights-summary"]').text()).toContain('权重合计须大于 0')

    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    expect(api.saveOpenAIEvalConfig).not.toHaveBeenCalled()
    expect(store.showError).toHaveBeenCalledWith('权重合计须大于 0，请至少为一项设置权重，或添加优先因素。')

    await rule().get('[data-testid="weight-load"]').setValue('10')
    expect(rule().find('[data-testid="weights-error"]').exists()).toBe(false)
    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    const payload = api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig
    expect(payload.policies?.[0].custom_balance).toEqual({ cost: 0, stability: 0, error_rate: 0, ttft: 0, load: 0.1, quality: 0, absolute_priorities: [] })
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
    const wrapper = await mountOnRequests()

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
    const wrapper = await mountOnRequests()
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
    const wrapper = await mountOnRequests()
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
    const wrapper = await mountOnRequests()
    await wrapper.get('[data-testid="decision-row"] .ledger-toggle').trigger('click')
    const cells = wrapper.findAll('[data-testid="candidate-quality"]')
    expect(cells.map(cell => cell.text())).toEqual(['未知', '—'])
  })

  it('shows the latest evaluation error in the unknown quality hint', async () => {
    const decisions = await api.listSchedulerDecisions()
    const trace = { ...decisions.items[0], scheduling_policy: 'avoid_degradation' }
    trace.candidates = [{
      ...trace.candidates[0],
      eligible: true,
      factors: {
        quality: {
          score: 0.5, known: false, observed_at: null, unknown_reason: 'selected_test_evidence_unavailable',
          state: 'insufficient', pass: 0, suspected_pass: 0, selected: 1, evaluated: 0, ratio: null, expires_at: null,
          evidence_error_code: 'insufficient_valid_samples', evidence_error_message: 'valid answers 0/1'
        }
      }
    } as typeof trace.candidates[0]]
    api.listSchedulerDecisions.mockResolvedValue({ limit: 50, items: [trace] })
    const wrapper = await mountOnRequests()
    await wrapper.get('[data-testid="decision-row"] .ledger-toggle').trigger('click')
    const cell = wrapper.get('[data-testid="candidate-quality"]')
    const title = cell.get('[data-testid="candidate-quality-unknown"]').attributes('title')
    expect(title).toContain('最新评测未计入')
    expect(title).toContain('insufficient_valid_samples')
    expect(title).toContain('valid answers 0/1')
  })

  it('keeps the model pass rate unknown and shows the account reference beside it', async () => {
    const decisions = await api.listSchedulerDecisions()
    const trace = { ...decisions.items[0], scheduling_policy: 'avoid_degradation', ranking_basis: 'snapshot' }
    trace.candidates = [
      {
        ...trace.candidates[0],
        quality_basis: 'account_prior',
        decision_reason: 'account_prior_tier',
        // The account-wide figure never fills the candidate's own pass rate.
        account_quality_prior: {
          ratio: 0.625,
          evaluation_id: 'eval-1',
          evaluated_at: '2026-10-01T07:00:00Z',
          expires_at: '2026-10-01T09:00:00Z',
          source_models: ['gpt-5-mini/high', 'gpt-5-nano/']
        }
      },
      { account_id: 13, eligible: true, selected: false, in_top_k: true, score: 0.4, quality_state: 'assessed', evaluated_count: 3, pass_count: 3, suspected_pass_count: 0, quality_ratio: 1, quality_basis: 'exact' }
    ]
    api.listSchedulerDecisions.mockResolvedValue({ limit: 50, items: [trace] })
    const wrapper = await mountOnRequests()
    const row = wrapper.get('[data-testid="decision-row"]')
    expect(row.get('[data-testid="dispatch-basis"]').text()).toContain('顺序：已发布的评估')
    expect(row.get('[data-testid="dispatch-account-reference"]').text()).toContain('账号参考')
    await row.get('.ledger-toggle').trigger('click')
    const cells = wrapper.findAll('[data-testid="candidate-quality"]')
    // The measured cell stays unknown; the reference is a separate badge.
    expect(cells[0].get('[data-testid="candidate-quality-unknown"]').text()).toBe('未知')
    const badge = cells[0].get('[data-testid="candidate-account-reference"]')
    expect(badge.text()).toContain('账号参考 62.5%')
    expect(badge.attributes('title')).toContain('gpt-5-mini/high')
    expect(badge.attributes('title')).toContain('不是本次模型与推理强度的实测通过率')
    // The unknown tooltip names the account reference, not the generic neutral-value hint.
    expect(cells[0].get('[data-testid="candidate-quality-unknown"]').attributes('title')).toContain('账号参考')
    expect(cells[0].text()).not.toContain('3/3')
    // An assessed candidate keeps its measured rate and gets no reference badge.
    expect(cells[1].text()).toContain('100.0%')
    expect(cells[1].find('[data-testid="candidate-account-reference"]').exists()).toBe(false)
    expect(cells[1].find('[data-testid="candidate-quality-unknown"]').exists()).toBe(false)
  })

  it('says the reference may come from another effort, not only another model', async () => {
    const decisions = await api.listSchedulerDecisions()
    // The same model at a different effort is the live case: only the effort differs.
    const trace = { ...decisions.items[0], scheduling_policy: 'avoid_degradation' }
    trace.candidates = [
      {
        ...trace.candidates[0],
        quality_basis: 'account_prior',
        decision_reason: 'account_prior_tier',
        account_quality_prior: {
          ratio: 0.5,
          evaluation_id: 'eval-1',
          evaluated_at: '2026-10-01T07:00:00Z',
          expires_at: '2026-10-01T09:00:00Z',
          source_models: ['gpt-5/low']
        }
      }
    ]
    api.listSchedulerDecisions.mockResolvedValue({ limit: 50, items: [trace] })
    const wrapper = await mountOnRequests()
    const row = wrapper.get('[data-testid="decision-row"]')
    await row.get('.ledger-toggle').trigger('click')
    const cell = wrapper.get('[data-testid="candidate-quality"]')
    const hint = cell.get('[data-testid="candidate-account-reference"]').attributes('title')!
    // The wording covers models or efforts, and names the exact source pair.
    expect(hint).toContain('其他模型或推理强度')
    expect(hint).toContain('gpt-5/low')
    // The expiry is described as the earlier of the two limits, not as a diagnostic expiry alone.
    expect(hint).toContain('两者中较早者')
    expect(row.get('[data-testid="dispatch-account-reference"]').text()).toContain('其他模型或推理强度')
    expect(row.get('[data-testid="candidate-row"]').text()).toContain('其他模型或推理强度')
  })

  it('exposes the reference sources and expiry to assistive technology, not only as a title', async () => {
    const decisions = await api.listSchedulerDecisions()
    const trace = { ...decisions.items[0], scheduling_policy: 'avoid_degradation' }
    trace.candidates = [
      {
        ...trace.candidates[0],
        quality_basis: 'account_prior',
        account_quality_prior: { ratio: 0.5, evaluation_id: 'eval-1', evaluated_at: '2026-10-01T07:00:00Z', expires_at: '2026-10-01T09:00:00Z', source_models: ['gpt-5/low'] }
      }
    ]
    api.listSchedulerDecisions.mockResolvedValue({ limit: 50, items: [trace] })
    const wrapper = await mountOnRequests()
    await wrapper.get('[data-testid="decision-row"] .ledger-toggle').trigger('click')
    const detail = wrapper.get('[data-testid="candidate-account-reference-detail"]')
    expect(detail.text()).toContain('gpt-5/low')
    expect(detail.text()).toContain('评估于')
    expect(detail.text()).toContain('可用至')
  })

  it('keeps the hidden reference detail inside a positioned table scroll container', async () => {
    const decisions = await api.listSchedulerDecisions()
    const trace = { ...decisions.items[0], scheduling_policy: 'avoid_degradation' }
    trace.candidates = [
      {
        ...trace.candidates[0],
        quality_basis: 'account_prior',
        account_quality_prior: { ratio: 1, evaluation_id: 'eval-1', evaluated_at: '2026-10-01T07:00:00Z', expires_at: '2026-10-01T09:00:00Z', source_models: ['gpt-5/low'] }
      }
    ]
    api.listSchedulerDecisions.mockResolvedValue({ limit: 50, items: [trace] })
    const wrapper = await mountOnRequests()
    await wrapper.get('[data-testid="decision-row"] .ledger-toggle').trigger('click')

    // The detail is visually hidden, but Tailwind's sr-only is `position:
    // absolute`, so it needs the table's scroll container to be a positioned
    // containing block. Without one it resolves against the page wrapper at its
    // offset inside the wide table, which grows document.scrollWidth and scrolls
    // the whole page sideways on a narrow screen.
    const scroller = wrapper.get('[data-testid="candidate-account-reference-detail"]').element.closest('.overflow-x-auto')
    expect(scroller).not.toBeNull()
    expect(scroller!.classList.contains('relative')).toBe(true)
    // The table shares that container, so the hidden text scrolls with the table.
    expect(scroller!.querySelector('table.cand-table')).not.toBeNull()
  })

  it('does not narrate an account reference on an owner-bound request', async () => {
    const decisions = await api.listSchedulerDecisions()
    const trace = {
      ...decisions.items[0],
      ranking_basis: 'owner',
      layer: 'previous_response_id',
      reason_code: 'required_owner_override',
      scheduling_policy: 'avoid_degradation'
    }
    // The account is pinned by a binding rule; the evaluated row is attached
    // only to describe it, so the request was never ordered by the reference.
    trace.candidates = [
      {
        ...trace.candidates[0],
        selected: true,
        decision_reason: 'required_owner_override',
        quality_basis: 'account_prior',
        account_quality_prior: { ratio: 0.25, evaluation_id: 'eval-1', evaluated_at: '2026-10-01T07:00:00Z', expires_at: '2026-10-01T09:00:00Z', source_models: ['gpt-5/low'] }
      }
    ]
    api.listSchedulerDecisions.mockResolvedValue({ limit: 50, items: [trace] })
    const wrapper = await mountOnRequests()
    const row = wrapper.get('[data-testid="decision-row"]')
    // The owner override stays authoritative and no order claim is made.
    expect(row.get('[data-testid="dispatch-owner"]').text()).toContain('绑定了指定账号')
    expect(row.find('[data-testid="dispatch-account-reference"]').exists()).toBe(false)
    // The badge itself still reports what the pinned account's tier rested on.
    await row.get('.ledger-toggle').trigger('click')
    expect(wrapper.get('[data-testid="candidate-account-reference"]').text()).toContain('账号参考 25.0%')
  })

  it('states the tier-then-score order for avoid degradation instead of a flat score order', async () => {
    const decisions = await api.listSchedulerDecisions()
    const tiered = { ...decisions.items[0], scheduling_policy: 'avoid_degradation' }
    const plain = { ...decisions.items[0], scheduling_policy: 'cost_first' }
    const custom = { ...decisions.items[0], scheduling_policy: 'custom_balance' }
    api.listSchedulerDecisions.mockResolvedValue({ limit: 50, items: [tiered, plain, custom] })
    const wrapper = await mountOnRequests()

    // The note lives in the expanded detail, so each row is opened in turn.
    const rows = wrapper.findAll('[data-testid="decision-row"]')
    const hints: string[] = []
    for (const row of rows) {
      await row.get('.ledger-toggle').trigger('click')
      hints.push(row.findAll('.ledger-note').map(note => note.text()).join(' | '))
    }
    const flat = '分数仅在可用账号之间比较，分数越高越优先。'
    // Avoid degradation describes tiers then score, and never the flat score-only order.
    expect(hints[0]).toContain('先用降智通过率分档，同一档内再比较分数')
    expect(hints[0]).not.toContain(flat)
    // A policy without a quality term keeps the original line.
    expect(hints[1]).toContain(flat)
    // Custom balance has an explicit quality weight the flat line does not describe.
    expect(hints[2]).toContain('自定义权重')
    expect(hints[2]).not.toContain(flat)
  })

  it('never turns an account reference into a rate on its own, even at 0%', async () => {
    const decisions = await api.listSchedulerDecisions()
    const trace = { ...decisions.items[0], scheduling_policy: 'avoid_degradation' }
    trace.candidates = [{
      ...trace.candidates[0],
      quality_basis: 'account_prior',
      // Only the separate object drives the badge; a 0 % reference is a real value.
      account_quality_prior: { ratio: 0, evaluation_id: 'eval-1', evaluated_at: '2026-10-01T07:00:00Z', expires_at: '2026-10-01T09:00:00Z', source_models: [] },
      quality_ratio: 1,
      quality_state: 'assessed',
      evaluated_count: 3
    }]
    api.listSchedulerDecisions.mockResolvedValue({ limit: 50, items: [trace] })
    const wrapper = await mountOnRequests()
    await wrapper.get('[data-testid="decision-row"] .ledger-toggle').trigger('click')
    const cell = wrapper.get('[data-testid="candidate-quality"]')
    expect(cell.get('[data-testid="candidate-account-reference"]').text()).toBe('账号参考 0.0%')
    expect(cell.get('[data-testid="candidate-quality-unknown"]').text()).toBe('未知')
    expect(cell.text()).not.toContain('100')
  })

  it('shows a full 100 % account reference as a reference, never as this model\'s rate', async () => {
    const decisions = await api.listSchedulerDecisions()
    const trace = { ...decisions.items[0], scheduling_policy: 'avoid_degradation' }
    trace.candidates = [{
      ...trace.candidates[0],
      quality_basis: 'account_prior',
      account_quality_prior: { ratio: 1, evaluation_id: 'eval-1', evaluated_at: '2026-10-01T07:00:00Z', expires_at: '2026-10-01T09:00:00Z', source_models: ['gpt-5/low'] }
    }]
    api.listSchedulerDecisions.mockResolvedValue({ limit: 50, items: [trace] })
    const wrapper = await mountOnRequests()
    await wrapper.get('[data-testid="decision-row"] .ledger-toggle').trigger('click')
    const cell = wrapper.get('[data-testid="candidate-quality"]')
    // The reference reads 100 %, and the measured cell still reads unknown.
    expect(cell.get('[data-testid="candidate-account-reference"]').text()).toBe('账号参考 100.0%')
    expect(cell.get('[data-testid="candidate-quality-unknown"]').text()).toBe('未知')
    // 100 % is never presented as a measured pass rate with its test counts.
    expect(cell.find('[data-testid="candidate-quality-contribution"]').exists()).toBe(false)
    expect(cell.text()).not.toContain('项测试通过')
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
    expect(payload.custom_balance).toEqual({ cost: 0, error_rate: 0, ttft: 0, load: 0, quality: 1, stability: 0, absolute_priorities: [] })
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
    const folded = { cost: 0.2, error_rate: 0.43, ttft: 0.27, load: 0.1, quality: 0, stability: 0, absolute_priorities: [] }
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
    expect(wrapper.get('[data-testid="quality-interval-pending"]').text()).toContain('当前仍按每 6 小时评估')

    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    expect((api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig).quality_refresh_interval_seconds).toBe(95 * 60)
  })

  it('evaluates from the interval row, shows the last and next time, and keeps unsaved edits unsaved', async () => {
    api.getOpenAIEvalConfig.mockResolvedValue(serverConfig({ quality_refreshed_at: null }))
    // The summary the default mock resolves with, reused as the finished run.
    const base = await api.evaluateOpenAIEvalRanking()
    api.evaluateOpenAIEvalRanking.mockClear()
    let finish!: (value: unknown) => void
    api.evaluateOpenAIEvalRanking.mockImplementation(() => new Promise(resolve => { finish = resolve }))
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.get('[data-testid="quality-refresh-status"]').text()).toContain('尚未评估')

    await wrapper.get('[data-testid="quality-interval"]').setValue('600')
    expect(wrapper.text()).toContain('有未保存的更改')
    const button = wrapper.get('[data-testid="evaluate-now"]')
    await button.trigger('click')
    expect(button.attributes('disabled')).toBeDefined()
    expect(button.text()).toContain('正在评估')
    await button.trigger('click')
    expect(api.evaluateOpenAIEvalRanking).toHaveBeenCalledTimes(1)

    finish({ ...base, trigger: 'manual' })
    await flushPromises()
    expect(wrapper.get('[data-testid="last-evaluated"]').text()).toContain('上次评估')
    expect(wrapper.get('[data-testid="last-evaluated"]').text()).toContain('手动评估')
    expect(wrapper.get('[data-testid="next-evaluation"]').text()).toContain('下次')
    expect(button.attributes('disabled')).toBeUndefined()
    // The unsaved interval is untouched and still pending; nothing was saved.
    expect((wrapper.get('[data-testid="quality-interval"]').element as HTMLSelectElement).value).toBe('600')
    expect(wrapper.text()).toContain('有未保存的更改')
    expect(wrapper.find('[data-testid="quality-interval-pending"]').exists()).toBe(true)
    expect(api.saveOpenAIEvalConfig).not.toHaveBeenCalled()
  })

  it('reports a failed evaluation with the server message and keeps the last evaluation time', async () => {
    api.getOpenAIEvalConfig.mockResolvedValue(serverConfig({ quality_refreshed_at: '2026-10-03T07:00:00Z' }))
    api.evaluateOpenAIEvalRanking.mockRejectedValue({ error: 'evaluation is already running' })
    const wrapper = mountView()
    await flushPromises()
    const before = wrapper.get('[data-testid="quality-refresh-status"]').text()
    await wrapper.get('[data-testid="evaluate-now"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="evaluate-message"]').text()).toContain('evaluation is already running')
    expect(wrapper.get('[data-testid="evaluate-kept"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="quality-refresh-status"]').text()).toBe(before)
    expect(wrapper.get('[data-testid="evaluate-now"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.text()).not.toContain('有未保存的更改')
  })

  it('warns that the pass rate is inactive while an older saved policy is not applied', async () => {
    api.getOpenAIEvalConfig.mockResolvedValue(serverConfig({ effects_enabled: false, scheduling_policy: 'stability_first' }))
    const wrapper = mountView()
    await flushPromises()
    // Stability first ignores the pass rate, so there is nothing to warn about.
    expect(wrapper.find('[data-testid="quality-effects-off"]').exists()).toBe(false)
    await wrapper.get('[data-testid="policy-avoid_degradation"]').setValue(true)
    expect(wrapper.get('[data-testid="quality-effects-off"]').exists()).toBe(true)
    // Applying clears the warning; the selection stays pending until saved.
    await wrapper.get('[data-testid="effects-apply"]').trigger('click')
    expect(wrapper.find('[data-testid="quality-effects-off"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="effects-pending"]').text()).toContain(zhT('admin.modelIntegrity.scheduling.policy.options.avoid_degradation.name'))
    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    expect((api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig).effects_enabled).toBe(true)
  })

  it('keeps request errors and first-token latency apart from the pass rate in the ledger', async () => {
    const decisions = await api.listSchedulerDecisions()
    const trace = { ...decisions.items[0], scheduling_policy: 'avoid_degradation' }
    trace.candidates = [{ ...trace.candidates[0], error_rate: 0.4, ttft_ms: 3000, evaluated_count: 3, pass_count: 3, suspected_pass_count: 0, quality_ratio: 1, quality_contribution: 0 }]
    api.listSchedulerDecisions.mockResolvedValue({ limit: 50, items: [trace] })
    const wrapper = await mountOnRequests()
    await wrapper.get('[data-testid="decision-row"] .ledger-toggle').trigger('click')
    const row = wrapper.get('[data-testid="candidate-row"]')
    expect(row.text()).toContain('40.0%')
    expect(row.get('[data-testid="candidate-quality"]').text()).toContain('100.0%')
    expect(row.get('[data-testid="candidate-quality"]').text()).toContain('3/3 项测试通过')
    expect(row.find('[data-testid="candidate-quality-contribution"]').exists()).toBe(false)
  })

  it('shows a dispatch error with credentials masked', async () => {
    const decisions = await api.listSchedulerDecisions()
    const trace = { ...decisions.items[0], error: 'select failed: Authorization: Bearer abcdef1234567890 (sk-live1234567890abcd)' }
    api.listSchedulerDecisions.mockResolvedValue({ limit: 50, items: [trace] })
    const wrapper = await mountOnRequests()
    const text = wrapper.get('[data-testid="dispatch-error"]').text()
    expect(text).toContain('select failed')
    expect(text).toContain('Bearer [redacted]')
    expect(text).not.toContain('abcdef1234567890')
    expect(text).not.toContain('sk-live1234567890abcd')
  })
})

describe('ModelIntegritySchedulingView runtime thresholds', () => {
  const configured = {
    cost_first: { error_rate: 0, ttft_seconds: 12.5 },
    stability_first: { error_rate: 0.025, ttft_seconds: 6 },
    avoid_degradation: { error_rate: 0.3, ttft_seconds: 20 },
    custom_balance: { error_rate: 0.07, ttft_seconds: 30 },
    min_error_samples: 50,
    min_ttft_samples: 100
  }
  const field = (wrapper: ReturnType<typeof mountView>, id: string) => wrapper.get(`[data-testid="threshold-${id}"]`)
  const valueOf = (wrapper: ReturnType<typeof mountView>, id: string) => (field(wrapper, id).element as HTMLInputElement).value
  const save = async (wrapper: ReturnType<typeof mountView>) => {
    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
  }

  it('shows the defaults for a legacy config and saves them without marking the page changed', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(valueOf(wrapper, 'cost_first-error_rate')).toBe('20')
    expect(valueOf(wrapper, 'cost_first-ttft_seconds')).toBe('15')
    expect(valueOf(wrapper, 'stability_first-error_rate')).toBe('5')
    expect(valueOf(wrapper, 'stability_first-ttft_seconds')).toBe('8')
    expect(valueOf(wrapper, 'min_error_samples')).toBe('10')
    expect(valueOf(wrapper, 'min_ttft_samples')).toBe('20')
    expect(wrapper.text()).not.toContain('有未保存的更改')
    expect(wrapper.get('[data-testid="scheduling-thresholds"]').text()).toContain('不会被停用')
    expect(wrapper.get('[data-testid="scheduling-thresholds"]').text()).toContain('监测探测和估算值不会充当缺少的样本')

    await wrapper.get('[data-testid="policy-cost_first"]').setValue(true)
    await save(wrapper)
    const payload = api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig
    expect(payload.scheduling_thresholds).toEqual({
      cost_first: { error_rate: 0.2, ttft_seconds: 15 },
      stability_first: { error_rate: 0.05, ttft_seconds: 8 },
      avoid_degradation: { error_rate: 0.2, ttft_seconds: 15 },
      custom_balance: { error_rate: 0.2, ttft_seconds: 15 },
      min_error_samples: 10,
      min_ttft_samples: 20
    })
  })

  it('keeps a configured object, including an explicit 0% error rate, through an unrelated save and reload', async () => {
    api.getOpenAIEvalConfig.mockResolvedValue(serverConfig({ scheduling_thresholds: configured }))
    const wrapper = mountView()
    await flushPromises()

    expect(valueOf(wrapper, 'cost_first-error_rate')).toBe('0')
    expect(valueOf(wrapper, 'stability_first-error_rate')).toBe('2.5')
    // custom_balance is retained for API compatibility but never edited here.
    expect(wrapper.find('[data-testid="threshold-custom_balance-error_rate"]').exists()).toBe(false)
    expect(valueOf(wrapper, 'cost_first-ttft_seconds')).toBe('12.5')
    expect(wrapper.text()).not.toContain('有未保存的更改')

    await wrapper.get('[data-testid="policy-stability_first"]').setValue(true)
    await save(wrapper)
    expect((api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig).scheduling_thresholds).toEqual(configured)
    // The re-read after saving shows the same object, unchanged.
    expect(valueOf(wrapper, 'cost_first-error_rate')).toBe('0')
    expect(valueOf(wrapper, 'min_ttft_samples')).toBe('100')
  })

  it('edits percentages as ratios and saves every threshold and sample count', async () => {
    const wrapper = mountView()
    await flushPromises()

    await field(wrapper, 'stability_first-error_rate').setValue('2.5')
    await field(wrapper, 'stability_first-ttft_seconds').setValue('6')
    await field(wrapper, 'avoid_degradation-error_rate').setValue('0')
    await field(wrapper, 'min_error_samples').setValue('1000000')
    await field(wrapper, 'min_ttft_samples').setValue('1')
    expect(wrapper.text()).toContain('有未保存的更改')
    await save(wrapper)

    const thresholds = (api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig).scheduling_thresholds
    expect(thresholds?.stability_first).toEqual({ error_rate: 0.025, ttft_seconds: 6 })
    expect(thresholds?.avoid_degradation).toEqual({ error_rate: 0, ttft_seconds: 15 })
    expect(thresholds?.min_error_samples).toBe(1_000_000)
    expect(thresholds?.min_ttft_samples).toBe(1)
  })

  it.each([
    ['cost_first-error_rate', '100.5'],
    ['cost_first-error_rate', '-1'],
    ['stability_first-error_rate', ''],
    ['stability_first-ttft_seconds', '0'],
    ['stability_first-ttft_seconds', '86401'],
    ['avoid_degradation-ttft_seconds', ''],
    ['min_error_samples', '0'],
    ['min_ttft_samples', '10.5'],
    ['min_ttft_samples', '1000001']
  ])('blocks saving %s = "%s" and keeps every other edit', async (id, value) => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="policy-avoid_degradation"]').setValue(true)
    await field(wrapper, 'cost_first-ttft_seconds').setValue('9')
    await field(wrapper, id).setValue(value)
    expect(field(wrapper, id).attributes('aria-invalid')).toBe('true')
    const describedBy = field(wrapper, id).attributes('aria-describedby')!
    expect(wrapper.get(`#${describedBy}`).text()).not.toBe('')

    await save(wrapper)
    expect(api.saveOpenAIEvalConfig).not.toHaveBeenCalled()
    expect(store.showError).toHaveBeenCalledWith('部分运行阈值超出范围，请修正标出的数值后再保存。')
    // Nothing was reset: the typed value, the other edits and the unsaved state all remain.
    expect(valueOf(wrapper, id)).toBe(value)
    expect(valueOf(wrapper, 'cost_first-ttft_seconds')).toBe('9')
    expect((wrapper.get('[data-testid="policy-avoid_degradation"]').element as HTMLInputElement).checked).toBe(true)
    expect(wrapper.text()).toContain('有未保存的更改')
  })

  it('accepts the inclusive bounds 0%, 100%, 86,400 seconds and saves once corrected', async () => {
    const wrapper = mountView()
    await flushPromises()

    await field(wrapper, 'cost_first-error_rate').setValue('101')
    await save(wrapper)
    expect(api.saveOpenAIEvalConfig).not.toHaveBeenCalled()

    await field(wrapper, 'cost_first-error_rate').setValue('100')
    await field(wrapper, 'stability_first-error_rate').setValue('0')
    await field(wrapper, 'stability_first-ttft_seconds').setValue('86400')
    expect(wrapper.findAll('[data-testid="threshold-error"]')).toHaveLength(0)
    await save(wrapper)
    const thresholds = (api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig).scheduling_thresholds
    expect(thresholds?.cost_first.error_rate).toBe(1)
    expect(thresholds?.stability_first).toEqual({ error_rate: 0, ttft_seconds: 86400 })
  })

  it('marks the rows the default policy and model rules use', async () => {
    api.getOpenAIEvalConfig.mockResolvedValue(serverConfig({
      scheduling_policy: 'cost_first',
      policies: [{ requested_model: 'gpt-5', reasoning_effort: '', policy: 'avoid_degradation' }]
    }))
    const wrapper = mountView()
    await flushPromises()

    const marked = (policy: string) => wrapper.get(`[data-testid="threshold-row-${policy}"]`).find('[data-testid="threshold-in-use"]').exists()
    expect(marked('cost_first')).toBe(true)
    expect(marked('avoid_degradation')).toBe(true)
    expect(marked('stability_first')).toBe(false)

    await wrapper.get('[data-testid="policy-custom_balance"]').setValue(true)
    // Custom balance has no threshold row at all: its order comes from its
    // weights, so no runtime threshold would ever be applied to it. Avoid
    // degradation stays marked because a model rule still uses it.
    expect(marked('cost_first')).toBe(false)
    expect(marked('stability_first')).toBe(false)
    expect(marked('avoid_degradation')).toBe(true)
    expect(wrapper.find('[data-testid="threshold-row-custom_balance"]').exists()).toBe(false)
  })

  it('labels every threshold input with its policy for assistive technology', async () => {
    const wrapper = mountView()
    await flushPromises()

    expect(field(wrapper, 'stability_first-error_rate').attributes('aria-label')).toBe('优先稳定：错误率超过')
    expect(field(wrapper, 'stability_first-ttft_seconds').attributes('aria-label')).toBe('优先稳定：首包延迟超过')
    expect(wrapper.get('[data-testid="threshold-min_error_samples"]').element.closest('label')?.textContent).toContain('计入错误率前所需的请求数')
  })

  it('keeps an edited threshold through a conflict and shows the server values after reload', async () => {
    api.saveOpenAIEvalConfig.mockRejectedValue({ status: 409, message: 'conflict' })
    const wrapper = mountView()
    await flushPromises()

    await field(wrapper, 'cost_first-error_rate').setValue('12')
    await save(wrapper)
    expect(wrapper.text()).toContain('该配置已在其他页面或由其他管理员修改')
    expect(valueOf(wrapper, 'cost_first-error_rate')).toBe('12')

    api.getOpenAIEvalConfig.mockResolvedValue(serverConfig({ revision: 5, scheduling_thresholds: configured }))
    const reload = wrapper.findAll('button').find(button => button.text() === zhT('admin.modelIntegrity.common.reload'))!
    await reload.trigger('click')
    await flushPromises()
    expect(valueOf(wrapper, 'cost_first-error_rate')).toBe('0')
    expect(wrapper.text()).not.toContain('有未保存的更改')
  })

  it('moves keyboard focus to the first value that blocks saving', async () => {
    // Focus only sticks to a node that is in the document, so this one mounts attached.
    const host = document.createElement('div')
    document.body.appendChild(host)
    const wrapper = mount(SchedulingView, {
      attachTo: host,
      global: { stubs: { AppLayout: { template: '<div><slot /></div>' }, RouterLink: RouterLinkStub, Teleport: true } }
    })
    await flushPromises()

    await field(wrapper, 'stability_first-ttft_seconds').setValue('0')
    await field(wrapper, 'avoid_degradation-error_rate').setValue('101')
    await save(wrapper)

    expect(api.saveOpenAIEvalConfig).not.toHaveBeenCalled()
    // Focus lands on the first invalid field in page order, so the administrator
    // can correct it without hunting through the matrix.
    expect(document.activeElement).toBe(field(wrapper, 'stability_first-ttft_seconds').element)
    wrapper.unmount()
    host.remove()
  })
})

describe('ModelIntegritySchedulingView request records by group', () => {
  type Page = { limit: number; items: Record<string, unknown>[] }

  /** A read the test resolves itself, so responses can arrive out of order. */
  function deferred() {
    let resolve!: (page: Page) => void
    let reject!: (error: unknown) => void
    const promise = new Promise<Page>((ok, fail) => { resolve = ok; reject = fail })
    return { promise, resolve, reject }
  }

  async function baseTrace(overrides: Record<string, unknown> = {}) {
    const decisions = await api.listSchedulerDecisions()
    api.listSchedulerDecisions.mockClear()
    return { ...decisions.items[0], ...overrides }
  }

  async function pickGroup(wrapper: Awaited<ReturnType<typeof mountOnRequests>>, id: number | null) {
    const select = wrapper.get('[data-testid="requests-group"]')
    const options = select.findAll('option')
    const index = id === null ? 0 : options.findIndex(option => option.text() === (id === 7 ? 'openai-team' : 'openai-pro'))
    ;(select.element as HTMLSelectElement).selectedIndex = index
    await select.trigger('change')
  }

  beforeEach(() => {
    groups.getAll.mockResolvedValue([{ id: 7, name: 'openai-team', platform: 'openai' }, { id: 8, name: 'openai-pro', platform: 'openai' }])
  })

  it('reads all groups by default, the chosen group on change, and the same group on refresh', async () => {
    const wrapper = await mountOnRequests()
    expect(api.listSchedulerDecisions).toHaveBeenCalledTimes(1)
    expect(api.listSchedulerDecisions).toHaveBeenLastCalledWith(50, null)
    const select = wrapper.get('[data-testid="requests-group"]')
    expect(select.attributes('aria-label')).toBe('按请求分组筛选')
    expect(select.findAll('option').map(option => option.text())).toEqual(['全部分组', 'openai-team', 'openai-pro'])

    await pickGroup(wrapper, 8)
    await flushPromises()
    expect(api.listSchedulerDecisions).toHaveBeenLastCalledWith(50, 8)

    await wrapper.get('[data-testid="requests-refresh"]').trigger('click')
    await flushPromises()
    expect(api.listSchedulerDecisions).toHaveBeenCalledTimes(3)
    expect(api.listSchedulerDecisions).toHaveBeenLastCalledWith(50, 8)

    await pickGroup(wrapper, null)
    await flushPromises()
    expect(api.listSchedulerDecisions).toHaveBeenLastCalledWith(50, null)
  })

  it('shows only the newest selection when an older read answers last', async () => {
    const team = await baseTrace({ group_id: 7, group_name: 'openai-team' })
    const pro = await baseTrace({ group_id: 8, group_name: 'openai-pro' })
    const wrapper = await mountOnRequests()
    const slow = deferred()
    const fast = deferred()
    api.listSchedulerDecisions.mockReturnValueOnce(slow.promise).mockReturnValueOnce(fast.promise)

    await pickGroup(wrapper, 7)
    await pickGroup(wrapper, 8)
    await flushPromises()
    // Records read for all groups never stand in for the group being loaded.
    expect(wrapper.find('[data-testid="decision-row"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="requests-loading"]').text()).toContain('正在读取')

    fast.resolve({ limit: 50, items: [pro] })
    await flushPromises()
    slow.resolve({ limit: 50, items: [team] })
    await flushPromises()

    const rows = wrapper.findAll('[data-testid="decision-row"]')
    expect(rows).toHaveLength(1)
    expect(rows[0].get('[data-testid="dispatch-group"]').text()).toBe('openai-pro')
    expect(wrapper.get('[data-testid="requests-scope"]').text()).toContain('「openai-pro」')
    expect(wrapper.find('[data-testid="requests-loading"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="requests-refresh"]').attributes('disabled')).toBeUndefined()
  })

  it('ignores an older failure that answers after the newest read', async () => {
    const pro = await baseTrace({ group_id: 8, group_name: 'openai-pro' })
    const wrapper = await mountOnRequests()
    const slow = deferred()
    api.listSchedulerDecisions.mockReturnValueOnce(slow.promise).mockResolvedValueOnce({ limit: 50, items: [pro] })

    await pickGroup(wrapper, 7)
    await pickGroup(wrapper, 8)
    await flushPromises()
    slow.reject({ message: 'stale failure' })
    await flushPromises()

    expect(wrapper.find('[data-testid="requests-error"]').exists()).toBe(false)
    expect(wrapper.findAll('[data-testid="decision-row"]')).toHaveLength(1)
  })

  it('never presents all-group records as the group when its read fails', async () => {
    const wrapper = await mountOnRequests()
    expect(wrapper.findAll('[data-testid="decision-row"]')).toHaveLength(1)
    api.listSchedulerDecisions.mockRejectedValueOnce({ message: 'ledger down' })

    await pickGroup(wrapper, 7)
    await flushPromises()

    expect(api.listSchedulerDecisions).toHaveBeenLastCalledWith(50, 7)
    expect(wrapper.find('[data-testid="decision-row"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="requests-error"]').text()).toContain('ledger down')
    expect(wrapper.find('[data-testid="requests-kept"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="requests-unavailable"]').exists()).toBe(true)
    // A failed read is not an empty result.
    expect(wrapper.find('[data-testid="requests-empty-group"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="requests-empty"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="requests-scope"]').exists()).toBe(false)
  })

  it('keeps records from the same group when its refresh fails, and says so', async () => {
    const team = await baseTrace({ group_id: 7, group_name: 'openai-team' })
    const wrapper = await mountOnRequests()
    api.listSchedulerDecisions.mockResolvedValueOnce({ limit: 50, items: [team] })
    await pickGroup(wrapper, 7)
    await flushPromises()

    api.listSchedulerDecisions.mockRejectedValueOnce({ message: 'ledger down' })
    await wrapper.get('[data-testid="requests-refresh"]').trigger('click')
    await flushPromises()

    expect(api.listSchedulerDecisions).toHaveBeenLastCalledWith(50, 7)
    expect(wrapper.findAll('[data-testid="decision-row"]')).toHaveLength(1)
    expect(wrapper.get('[data-testid="requests-error"]').text()).toContain('ledger down')
    expect(wrapper.get('[data-testid="requests-kept"]').text()).toContain('上次成功读取')
  })

  it('names the group and account as recorded at request time, not as renamed since', async () => {
    // Today account 11 is "oauth-a" and group 7 is "openai-team".
    const renamed = await baseTrace({ group_id: 7, group_name: 'legacy-team', selected_account_name: 'old-name' })
    api.listSchedulerDecisions.mockResolvedValue({ limit: 50, items: [renamed] })
    const wrapper = await mountOnRequests()

    const row = wrapper.get('[data-testid="decision-row"]')
    expect(row.get('[data-testid="dispatch-identity"]').text()).toBe('legacy-team · 选中 old-name #11')
    expect(row.get('[data-testid="dispatch-identity"]').text()).not.toMatch(/oauth-a|openai-team/)

    await row.get('.ledger-toggle').trigger('click')
    expect(wrapper.get('.cand-table thead').text()).toContain('账号（当前名称）')
    const selected = wrapper.findAll('[data-testid="candidate-row"]')[0]
    expect(selected.text()).toContain('oauth-a #11')
    expect(selected.get('[data-testid="candidate-historical-name"]').text()).toBe('请求时名称：old-name')
  })

  it('falls back to the recorded ID, never to the current name, for traces without snapshots', async () => {
    const withId = await baseTrace({ group_id: 7, group_name: undefined, selected_account_name: undefined })
    const noGroup = await baseTrace({ group_id: null, group_name: '', selected_account_name: '  ' })
    api.listSchedulerDecisions.mockResolvedValue({ limit: 50, items: [withId, noGroup] })
    const wrapper = await mountOnRequests()

    const identities = wrapper.findAll('[data-testid="dispatch-identity"]')
    expect(identities[0].text()).toBe('分组 #7 · 选中 账号 #11')
    expect(identities[1].text()).toBe('分组未记录 · 选中 账号 #11')
    for (const identity of identities) expect(identity.text()).not.toMatch(/oauth-a|openai-team/)
    expect(identities[0].get('[data-testid="dispatch-group"]').attributes('title')).toContain('未保存分组名称')
    expect(identities[0].get('[data-testid="dispatch-selected"]').attributes('title')).toContain('未保存账号名称')

    await wrapper.findAll('[data-testid="decision-row"]')[0].get('.ledger-toggle').trigger('click')
    expect(wrapper.find('[data-testid="candidate-historical-name"]').exists()).toBe(false)
  })

  it('states the retained window apart from the per-read limit', async () => {
    const wrapper = await mountOnRequests()
    const panel = wrapper.get('[data-testid="records-requests"]')
    expect(panel.text()).toContain('本实例内存只保留最近 256 条，服务重启后清空')
    expect(wrapper.get('[data-testid="requests-scope"]').text()).toBe('本实例保留最近 256 条记录，这里显示其中全部分组最新的 1 条。')
    expect(panel.text()).not.toContain('最近 50 条')

    const trace = await baseTrace()
    api.listSchedulerDecisions.mockResolvedValueOnce({ limit: 50, items: Array.from({ length: 50 }, (_, index) => ({ ...trace, at: `2026-10-01T08:00:${String(index).padStart(2, '0')}Z` })) })
    await wrapper.get('[data-testid="requests-refresh"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="requests-scope"]').text()).toContain('全部分组最新的 50 条。每次最多读取 50 条，更早的记录未列出。')
  })

  it('explains an empty group within the retained window and offers all groups', async () => {
    const wrapper = await mountOnRequests()
    api.listSchedulerDecisions.mockResolvedValueOnce({ limit: 50, items: [] })
    await pickGroup(wrapper, 8)
    await flushPromises()

    const empty = wrapper.get('[data-testid="requests-empty-group"]')
    expect(empty.text()).toContain('本实例保留的最近 256 条记录中，没有「openai-pro」分组的请求')
    expect(empty.text()).toContain('重启时清空')
    expect(wrapper.find('[data-testid="requests-empty"]').exists()).toBe(false)

    await empty.get('[data-testid="requests-show-all"]').trigger('click')
    await flushPromises()
    expect(api.listSchedulerDecisions).toHaveBeenLastCalledWith(50, null)
    expect((wrapper.get('[data-testid="requests-group"]').element as HTMLSelectElement).selectedIndex).toBe(0)
    expect(wrapper.findAll('[data-testid="decision-row"]')).toHaveLength(1)
  })

  it('keeps the model filter working within a group', async () => {
    const team = await baseTrace({ group_id: 7, group_name: 'openai-team' })
    const mini = { ...team, requested_model: 'gpt-5-mini' }
    const wrapper = await mountOnRequests()
    api.listSchedulerDecisions.mockResolvedValueOnce({ limit: 50, items: [team, mini] })
    await pickGroup(wrapper, 7)
    await flushPromises()
    await wrapper.get('[data-testid="requests-model"]').setValue('gpt-5-mini')
    const rows = wrapper.findAll('[data-testid="decision-row"]')
    expect(rows).toHaveLength(1)
    expect(rows[0].text()).toContain('gpt-5-mini')
  })

  it('tells a planned wait apart from an acquired slot', async () => {
    const acquired = await baseTrace({ acquired: true })
    const planned = await baseTrace({ acquired: false, wait_plan: true })
    const queued = await baseTrace({ acquired: undefined, awaiting_admission: true })
    const legacy = await baseTrace({ acquired: undefined })
    api.listSchedulerDecisions.mockResolvedValue({ limit: 50, items: [acquired, planned, queued, legacy] })
    const wrapper = await mountOnRequests()
    const rows = wrapper.findAll('[data-testid="decision-row"]')

    expect(rows[0].get('[data-testid="dispatch-identity"]').text()).toBe('openai-team · 选中 oauth-a #11')
    expect(rows[0].get('[data-testid="dispatch-acquired"]').text()).toBe('已获得并发名额')
    expect(rows[0].find('[data-testid="dispatch-waiting"]').exists()).toBe(false)

    for (const row of [rows[1], rows[2]]) {
      expect(row.get('[data-testid="dispatch-identity"]').text()).toBe('openai-team · 等待 oauth-a #11 的并发名额')
      expect(row.get('[data-testid="dispatch-identity"]').text()).not.toContain('选中')
      expect(row.get('[data-testid="dispatch-waiting"]').text()).toBe('未确认获得名额')
      expect(row.get('[data-testid="dispatch-waiting-note"]').text()).toContain('尚未确认获得名额')
      expect(row.find('[data-testid="dispatch-acquired"]').exists()).toBe(false)
    }

    // Older traces predate slot tracking: plain wording, no claim either way.
    expect(rows[3].get('[data-testid="dispatch-identity"]').text()).toBe('openai-team · 选中 oauth-a #11')
    expect(rows[3].find('[data-testid="dispatch-acquired"]').exists()).toBe(false)
    expect(rows[3].find('[data-testid="dispatch-waiting"]').exists()).toBe(false)
  })

  it('does not show an unacquired selection without a wait plan as served', async () => {
    const unacquired = await baseTrace({ acquired: false, wait_plan: false, awaiting_admission: false })
    api.listSchedulerDecisions.mockResolvedValue({ limit: 50, items: [unacquired] })
    const wrapper = await mountOnRequests()
    const row = wrapper.get('[data-testid="decision-row"]')

    expect(row.get('[data-testid="dispatch-identity"]').text()).toBe('openai-team · 选中 oauth-a #11')
    expect(row.get('[data-testid="dispatch-not-acquired"]').text()).toBe('未确认获得名额')
    expect(row.find('[data-testid="dispatch-acquired"]').exists()).toBe(false)
  })

  it('reads records without waiting for the group list, and says when that list fails', async () => {
    let failGroups!: (error: unknown) => void
    groups.getAll.mockReturnValueOnce(new Promise((_, fail) => { failGroups = fail }))
    const wrapper = mountView()
    await flushPromises()
    expect(api.listSchedulerDecisions).toHaveBeenCalledTimes(1)
    expect(api.listSchedulerDecisions).toHaveBeenLastCalledWith(50, null)

    failGroups({ message: 'groups down' })
    await flushPromises()
    await wrapper.get('[data-testid="records-tab-requests"]').trigger('click')
    expect(wrapper.get('[data-testid="requests-groups-failed"]').text()).toContain('分组列表读取失败')
    expect(wrapper.get('[data-testid="requests-group"]').findAll('option').map(option => option.text())).toEqual(['全部分组'])
    expect(wrapper.findAll('[data-testid="decision-row"]')).toHaveLength(1)
    expect(wrapper.find('[data-testid="requests-error"]').exists()).toBe(false)
  })

  it('does not add request records when an evaluation runs', async () => {
    const wrapper = await mountOnRequests()
    expect(api.listSchedulerDecisions).toHaveBeenCalledTimes(1)
    await wrapper.get('[data-testid="evaluate-now"]').trigger('click')
    await flushPromises()
    expect(api.evaluateOpenAIEvalRanking).toHaveBeenCalledTimes(1)
    expect(api.listSchedulerDecisions).toHaveBeenCalledTimes(1)
    expect(wrapper.findAll('[data-testid="decision-row"]')).toHaveLength(1)
  })
})

describe('ModelIntegritySchedulingView rule switches', () => {
  it('reads a legacy model rule without the field as enabled and saves it as enabled', async () => {
    api.getOpenAIEvalConfig.mockResolvedValue(serverConfig({ policies: [{ requested_model: 'gpt-5', policy: 'cost_first' }] }))
    const wrapper = mountView()
    await flushPromises()

    const toggle = wrapper.get('[data-testid="rule-enabled"]')
    expect(toggle.attributes('aria-checked')).toBe('true')
    expect(toggle.attributes('aria-label')).toBe('启用 gpt-5 的规则')
    expect(wrapper.find('[data-testid="rule-off"]').exists()).toBe(false)
    // Reading the default does not count as an edit.
    expect(wrapper.get('[data-testid="model-integrity-save"]').attributes('disabled')).toBeDefined()

    await wrapper.get('[data-testid="rule-policy"]').setValue('stability_first')
    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    const payload = api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig
    expect(payload.policies).toEqual([{ requested_model: 'gpt-5', reasoning_effort: '', policy: 'stability_first', enabled: true }])
  })

  it('keeps a disabled model rule configured, saves enabled false, and lets an enabled copy of it coexist', async () => {
    api.getOpenAIEvalConfig.mockResolvedValue(serverConfig({ policies: [{ requested_model: 'gpt-5', policy: 'avoid_degradation' }] }))
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="rule-enabled"]').trigger('click')
    expect(wrapper.get('[data-testid="rule-enabled"]').attributes('aria-checked')).toBe('false')
    expect(wrapper.get('[data-testid="rule-off"]').text()).toBe('已停用：配置保留，不参与调度。')
    // A disabled avoid-degradation rule no longer marks its threshold row as used.
    expect(wrapper.text()).not.toContain(zhT('admin.modelIntegrity.scheduling.policy.avoidNote'))

    // An enabled rule for the same model is not a duplicate of a disabled one.
    await wrapper.get('[data-testid="add-rule"]').trigger('click')
    const second = wrapper.findAll('[data-testid="policy-rule"]')[1]
    await second.find('select').setValue('gpt-5')
    expect(wrapper.text()).not.toContain('该模型与推理强度的规则已存在')

    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    const payload = api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig
    expect(payload.policies).toEqual([
      { requested_model: 'gpt-5', reasoning_effort: '', policy: 'avoid_degradation', enabled: false },
      { requested_model: 'gpt-5', reasoning_effort: '', policy: 'stability_first', enabled: true }
    ])
  })
})

describe('ModelIntegritySchedulingView account priority rules', () => {
  it('explains the order and starts empty for a config without rules', async () => {
    const wrapper = mountView()
    await flushPromises()

    const section = wrapper.get('[data-testid="account-rules"]')
    expect(section.text()).toContain('数字越小越优先')
    expect(section.text()).toContain('未配置规则，账号按调度策略排序。')
    await wrapper.get('[data-testid="policy-cost_first"]').setValue(true)
    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    expect((api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig).account_priority_rules).toEqual([])
  })

  it('adds an all-models rule and a specific-models rule and saves them', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="add-account-rule"]').trigger('click')
    await wrapper.get('[data-testid="add-account-rule"]').trigger('click')
    const rows = () => wrapper.findAll('[data-testid="account-rule"]')
    await rows()[0].get('[data-testid="account-rule-account"]').setValue('11')
    await rows()[0].get('[data-testid="account-rule-priority"]').setValue('2')
    await rows()[1].get('[data-testid="account-rule-account"]').setValue('12')
    await rows()[1].get('[data-testid="account-rule-priority"]').setValue('1')
    await rows()[1].get('[data-testid="account-rule-scope"]').setValue('some')
    // No model picked yet: the row says so and the save is blocked.
    expect(rows()[1].get('[data-testid="account-rule-error"]').text()).toBe('请至少选择一个模型。')
    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    expect(api.saveOpenAIEvalConfig).not.toHaveBeenCalled()

    const models = rows()[1].get('[data-testid="account-rule-models"]')
    expect(models.get('legend').text()).toBe('适用的公开模型')
    await models.findAll('input[type="checkbox"]')[1].setValue(true)
    expect(rows()[1].find('[data-testid="account-rule-error"]').exists()).toBe(false)
    expect(rows()[1].get('[data-testid="account-rule-enabled"]').attributes('aria-label')).toBe('启用 apikey-b #12 的优先规则')

    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    const payload = api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig
    expect(payload.account_priority_rules).toEqual([
      { account_id: 11, priority: 2, enabled: true },
      { account_id: 12, priority: 1, requested_models: ['gpt-5-mini'], enabled: true }
    ])
  })

  it('reads legacy rules as enabled, keeps a disabled rule, and only flags overlaps between enabled rules', async () => {
    api.getOpenAIEvalConfig.mockResolvedValue(serverConfig({
      account_priority_rules: [
        { account_id: 11, priority: 1 },
        { account_id: 11, priority: 5, enabled: false },
        { account_id: 11, priority: 3, requested_models: ['gpt-5'] }
      ]
    }))
    const wrapper = mountView()
    await flushPromises()

    const rows = () => wrapper.findAll('[data-testid="account-rule"]')
    expect(rows()).toHaveLength(3)
    expect(rows()[0].get('[data-testid="account-rule-enabled"]').attributes('aria-checked')).toBe('true')
    expect(rows()[1].get('[data-testid="account-rule-enabled"]').attributes('aria-checked')).toBe('false')
    expect(rows()[1].get('[data-testid="account-rule-off"]').text()).toContain('不参与调度')
    expect(rows()[2].get('[data-testid="account-rule-scope"]').element).toHaveProperty('value', 'some')
    expect(wrapper.find('[data-testid="account-rule-error"]').exists()).toBe(false)

    // Enabling the second all-models rule would overlap the first one.
    await rows()[1].get('[data-testid="account-rule-enabled"]').trigger('click')
    expect(rows()[1].get('[data-testid="account-rule-error"]').text()).toBe('该账号已有覆盖相同模型的启用规则。')
    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    expect(api.saveOpenAIEvalConfig).not.toHaveBeenCalled()

    // Switching the first one off resolves it; both stay in the config.
    await rows()[0].get('[data-testid="account-rule-enabled"]').trigger('click')
    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    const payload = api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig
    expect(payload.account_priority_rules).toEqual([
      { account_id: 11, priority: 1, enabled: false },
      { account_id: 11, priority: 5, enabled: true },
      { account_id: 11, priority: 3, requested_models: ['gpt-5'], enabled: true }
    ])
  })

  it('blocks a rule without an account or with a fractional priority, and deletes a rule', async () => {
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="add-account-rule"]').trigger('click')
    const row = () => wrapper.get('[data-testid="account-rule"]')
    expect(row().get('[data-testid="account-rule-error"]').text()).toBe('请选择账号。')
    await row().get('[data-testid="account-rule-account"]').setValue('11')
    await row().get('[data-testid="account-rule-priority"]').setValue('1.5')
    expect(row().get('[data-testid="account-rule-error"]').text()).toBe('优先级须为整数。')
    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    expect(api.saveOpenAIEvalConfig).not.toHaveBeenCalled()
    expect(store.showError).toHaveBeenCalledWith('优先级须为整数。')

    await row().get('button[aria-label="删除账号规则"]').trigger('click')
    expect(wrapper.find('[data-testid="account-rule"]').exists()).toBe(false)
  })
})
