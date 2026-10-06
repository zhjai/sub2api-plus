import { flushPromises, mount, RouterLinkStub } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { OpenAIEvalAccountOverview, OpenAIEvalConfig, OpenAIEvalRankingSnapshot, OpenAIEvalRankingSummary } from '@/api/admin/accounts'
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

vi.mock('@/api/admin/accounts', () => ({ default: api, accountsAPI: api, listSchedulerDecisions: api.listSchedulerDecisions }))
vi.mock('@/api/admin/groups', () => ({ default: groups, groupsAPI: groups }))
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
  items: [{ id: 'gpt-5' }],
  baseline_version: 'v1',
  baseline_models: [],
  candy: { expected_answer: 21, confidence: 'low', scheduling: 'alert_only' },
  evaluation_notice: '',
  reasoning_efforts: ['', 'high'],
  fingerprint_modes: [{ id: 'quick', samples: 60 }],
  modeltrace: { requests: 3, bank_revision: 'x', candidate_count: 16, scheduling: 'alert_only' }
}

function serverConfig(overrides: Partial<OpenAIEvalConfig> = {}): OpenAIEvalConfig {
  return {
    revision: 4,
    effects_enabled: false,
    bps_auto_enabled: true,
    scheduling_policy: '',
    policies: [],
    bps_accounts: [],
    accounts: [],
    ...overrides
  }
}

function summary(overrides: Partial<OpenAIEvalRankingSummary> = {}): OpenAIEvalRankingSummary {
  return {
    evaluation_id: 'instance-1-7',
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
    dimension_count: 1,
    account_count: 2,
    account_row_count: 2,
    quality_route_count: 1,
    truncated: false,
    coverage: { status: 'complete', discovery_complete: true, discovered_dimension_count: 1, cached_dimension_count: 1, uncached_dimension_count: 0, wildcard_routes_present: false, reasons: [] },
    ...overrides
  }
}

function snapshot(overrides: Partial<OpenAIEvalRankingSnapshot> = {}): OpenAIEvalRankingSnapshot {
  return {
    summary: summary(),
    effective_status: 'active',
    current_config_revision: 4,
    evaluation_in_progress: false,
    ranking_error: null,
    groups: [],
    dimensions: [],
    next_cursor: null,
    ...overrides
  }
}

function mountView() {
  return mount(SchedulingView, {
    global: { stubs: { AppLayout: { template: '<div><slot /></div>' }, RouterLink: RouterLinkStub, Teleport: true } }
  })
}

function overview(overrides: Partial<OpenAIEvalAccountOverview> = {}): OpenAIEvalAccountOverview {
  return {
    summary: summary(),
    previous_summary: null,
    effective_status: 'active',
    current_config_revision: 4,
    evaluation_in_progress: false,
    ranking_error: null,
    groups: [],
    policy: 'cost_first',
    weights: { price: 0.6, error_rate: 0.2, ttft: 0.1, load: 0.1, quality: 0 },
    ordering: 'score_desc',
    accounts: [],
    next_cursor: null,
    ...overrides
  }
}

async function openRequests(wrapper: ReturnType<typeof mountView>) {
  await wrapper.get('[data-testid="records-tab-requests"]').trigger('click')
}

beforeEach(() => {
  vi.clearAllMocks()
  api.getOpenAIEvalModels.mockResolvedValue(catalog)
  api.getOpenAIEvalConfig.mockResolvedValue(serverConfig())
  api.list.mockResolvedValue({ items: [] })
  api.listOpenAIEvalRuns.mockResolvedValue({ items: [] })
  api.listOpenAIEvalAudit.mockResolvedValue({ items: [] })
  api.listSchedulerDecisions.mockResolvedValue({ limit: 50, items: [] })
  groups.getAll.mockResolvedValue([{ id: 7, name: 'openai-team', platform: 'openai' }])
  // The default config loads with effects off, so the published status agrees.
  api.getOpenAIEvalRankings.mockResolvedValue(snapshot({ effective_status: 'inactive_effects_off' }))
  api.getOpenAIEvalAccountOverview.mockResolvedValue(overview({ summary: null, effective_status: 'inactive_effects_off' }))
  api.evaluateOpenAIEvalRanking.mockResolvedValue(summary())
  api.refreshOpenAIEvalQuality.mockResolvedValue({ refreshed_at: '2026-10-03T08:00:00Z', next_refresh_at: '2026-10-03T09:00:00Z', route_count: 3, quality_route_count: 5 })
  api.saveOpenAIEvalConfig.mockImplementation(async (payload: OpenAIEvalConfig) => ({ ...payload, revision: (payload.revision ?? 0) + 1 }))
})

describe('evaluation activation', () => {
  it('states that a saved policy is not in force while effects are off', async () => {
    api.getOpenAIEvalConfig.mockResolvedValue(serverConfig({ scheduling_policy: 'stability_first', effects_enabled: false, effective_status: 'inactive_effects_off' }))
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.get('[data-testid="effects-state"]').text()).toContain('未生效：评测影响已关闭')
    expect(wrapper.get('[data-testid="effects-explicit-off"]').text()).toContain('已保存的策略会被保留，但不参与调度')
    expect(wrapper.get('[data-testid="ranking-effects-off"]').text()).toContain('该排行目前不用于调度')
  })

  it('turns effects on visibly only when a policy is selected, and only by that event', async () => {
    const wrapper = mountView()
    await flushPromises()
    // Loading a saved policy never switches effects on by itself.
    expect(wrapper.get('[data-testid="effects-toggle"]').attributes('aria-checked')).toBe('false')
    expect(wrapper.find('[data-testid="effects-auto-note"]').exists()).toBe(false)

    await wrapper.get('[data-testid="policy-cost_first"]').setValue(true)
    expect(wrapper.get('[data-testid="effects-toggle"]').attributes('aria-checked')).toBe('true')
    expect(wrapper.get('[data-testid="effects-auto-note"]').text()).toContain('已自动置为开启')
  })

  it('respects an explicit off after effects were auto-enabled', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="policy-cost_first"]').setValue(true)
    await wrapper.get('[data-testid="effects-toggle"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="effects-toggle"]').attributes('aria-checked')).toBe('false')
    // The switch itself is the source of truth; the note states what off means.
    expect(wrapper.get('[data-testid="effects-explicit-off"]').text()).toContain('评测影响为关闭，已保存的策略会被保留')
  })

  it('does not switch effects on when only loading a configuration', async () => {
    api.getOpenAIEvalConfig.mockResolvedValue(serverConfig({ scheduling_policy: 'avoid_degradation', effects_enabled: false }))
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.get('[data-testid="effects-toggle"]').attributes('aria-checked')).toBe('false')
  })
})

describe('manual evaluation', () => {
  it('has exactly one 立即评估 control, on the interval row, and no standalone evaluation panel', async () => {
    const wrapper = mountView()
    await flushPromises()
    const buttons = wrapper.findAll('button').filter(button => button.text().includes('立即评估'))
    expect(buttons).toHaveLength(1)
    expect(wrapper.get('[data-testid="quality-refresh"]').find('[data-testid="evaluate-now"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="evaluation-panel"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="quality-evaluate-now"]').exists()).toBe(false)
  })

  it('labels the action exactly 立即评估 and reports a published result', async () => {
    const wrapper = mountView()
    await flushPromises()
    const button = wrapper.get('[data-testid="evaluate-now"]')
    expect(button.text()).toBe('立即评估')

    let finish!: (value: unknown) => void
    api.evaluateOpenAIEvalRanking.mockImplementation(() => new Promise(resolve => { finish = resolve }))
    await button.trigger('click')
    // A second click while running must not start another evaluation.
    expect(button.attributes('disabled')).toBeDefined()
    expect(button.text()).toContain('正在评估')
    await button.trigger('click')
    expect(api.evaluateOpenAIEvalRanking).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[data-testid="evaluate-progress"]').text()).toContain('服务端正在重建账号顺序')

    finish(summary())
    await flushPromises()
    expect(wrapper.get('[data-testid="evaluate-message"]').text()).toContain('已发布')
    expect(button.attributes('disabled')).toBeUndefined()
  })

  it('preserves unsaved edits and keeps them out of the request', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="quality-interval"]').setValue('600')
    expect(wrapper.text()).toContain('有未保存的更改')

    await wrapper.get('[data-testid="evaluate-now"]').trigger('click')
    await flushPromises()
    // The endpoint takes no body, so no unsaved value can reach the server.
    expect(api.evaluateOpenAIEvalRanking).toHaveBeenCalledWith()
    expect((wrapper.get('[data-testid="quality-interval"]').element as HTMLSelectElement).value).toBe('600')
    expect(wrapper.text()).toContain('有未保存的更改')
    expect(wrapper.get('[data-testid="evaluate-dirty"]').text()).toContain('评估始终使用已保存的策略')
    expect(api.saveOpenAIEvalConfig).not.toHaveBeenCalled()
  })

  it('distinguishes a superseded run from an unavailable service and a timeout', async () => {
    const wrapper = mountView()
    await flushPromises()
    for (const [status, expected] of [[409, '已被新的配置或更新的评估取代'], [503, '评估服务暂不可用'], [504, '评估未在预算内完成']] as const) {
      api.evaluateOpenAIEvalRanking.mockRejectedValueOnce({ response: { status } })
      await wrapper.get('[data-testid="evaluate-now"]').trigger('click')
      await flushPromises()
      expect(wrapper.get('[data-testid="evaluate-message"]').text()).toContain(expected)
      // A failed run never claims a success time, and the prior order stands.
      expect(wrapper.get('[data-testid="evaluate-kept"]').text()).toContain('仍沿用上一次评估结果')
    }
  })

  it('reports config saved but evaluation failed instead of a plain success', async () => {
    api.saveOpenAIEvalConfig.mockResolvedValue(serverConfig({ revision: 5, effective_status: 'error', ranking_error: { code: 'build_failed', message: 'budget exceeded', config_revision: 5, evaluation_id: null } }))
    api.getOpenAIEvalConfig.mockResolvedValue(serverConfig({ revision: 5, effective_status: 'error', ranking_error: { code: 'build_failed', message: 'budget exceeded', config_revision: 5, evaluation_id: null } }))
    api.getOpenAIEvalConfig.mockResolvedValueOnce(serverConfig())
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="policy-cost_first"]').setValue(true)
    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()

    expect(store.showError).toHaveBeenCalledWith(expect.stringContaining('配置已保存，但对应评估失败'))
    expect(store.showSuccess).not.toHaveBeenCalled()
    const banner = wrapper.get('[data-testid="save-evaluation-error"]')
    expect(banner.text()).toContain('budget exceeded')
    expect(banner.text()).toContain('已保存的策略尚未生效')
  })

  it('reports a plain success when the save and its evaluation both succeed', async () => {
    api.saveOpenAIEvalConfig.mockResolvedValue(serverConfig({ revision: 5, effective_status: 'active' }))
    api.getOpenAIEvalConfig.mockResolvedValue(serverConfig({ revision: 5, effective_status: 'active' }))
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="policy-cost_first"]').setValue(true)
    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    // Saving already evaluated, so no second manual run is asked for.
    expect(store.showSuccess).toHaveBeenCalledWith('已保存，并已按新配置完成评估。')
    expect(store.showError).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="save-evaluation-error"]').exists()).toBe(false)
    expect(api.evaluateOpenAIEvalRanking).not.toHaveBeenCalled()
    // The leaderboard is read again for the new generation.
    expect(api.getOpenAIEvalAccountOverview.mock.calls.length).toBeGreaterThanOrEqual(2)
  })

  it('blocks evaluation while a configuration conflict is unresolved', async () => {
    api.saveOpenAIEvalConfig.mockRejectedValue({ status: 409 })
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="policy-cost_first"]').setValue(true)
    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="evaluate-now"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="evaluate-blocked"]').text()).toContain('存在未解决的配置冲突')
  })
})

describe('evaluation records', () => {
  it('shows the server record at once with zero requests, even when the leaderboard read fails', async () => {
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.get('[data-testid="record-empty"]').text()).toContain('还没有评估记录')

    api.getOpenAIEvalAccountOverview.mockRejectedValue({ error: 'overview offline' })
    api.evaluateOpenAIEvalRanking.mockResolvedValue(summary({ evaluation_id: 'instance-1-9', account_count: 13, trigger: 'manual' }))
    await wrapper.get('[data-testid="evaluate-now"]').trigger('click')
    await flushPromises()

    const record = wrapper.get('[data-testid="evaluation-record"]')
    expect(record.attributes('data-evaluation-id')).toBe('instance-1-9')
    expect(record.get('[data-testid="record-accounts"]').text()).toBe('13')
    expect(record.text()).toContain('手动评估')
    expect(record.text()).toContain('当前')
    // Published is not the same as read: both are stated.
    expect(wrapper.get('[data-testid="evaluate-message"]').text()).toContain('新的账号排行读取失败：overview offline')
    expect(wrapper.get('[data-testid="board-error"]').text()).toContain('overview offline')
    expect(wrapper.get('[data-testid="last-evaluated"]').text()).toContain('手动评估')
  })

  it('lists the current and previous records the server keeps', async () => {
    api.getOpenAIEvalAccountOverview.mockResolvedValue(overview({
      summary: summary({ evaluation_id: 'instance-1-8', trigger: 'interval' }),
      previous_summary: summary({ evaluation_id: 'instance-1-7', trigger: 'policy_saved', effects_enabled: false, published_at: '2026-10-03T07:00:01Z' })
    }))
    const wrapper = mountView()
    await flushPromises()
    const records = wrapper.findAll('[data-testid="evaluation-record"]')
    expect(records.map(record => record.attributes('data-evaluation-id'))).toEqual(['instance-1-8', 'instance-1-7'])
    expect(records[0].text()).toContain('定时评估')
    expect(records[1].text()).toContain('上一次')
    expect(records[1].get('[data-testid="record-applied"]').text()).toContain('否，评测影响已关闭')
  })

  it('keeps the previous record and states the failure when an evaluation fails', async () => {
    api.getOpenAIEvalAccountOverview.mockResolvedValue(overview({ summary: summary({ evaluation_id: 'instance-1-8' }) }))
    api.evaluateOpenAIEvalRanking.mockRejectedValue({ response: { status: 500 }, error: 'scorer crashed' })
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="evaluate-now"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="evaluate-message"]').text()).toContain('评估失败：scorer crashed')
    expect(wrapper.get('[data-testid="evaluate-kept"]').text()).toContain('仍沿用上一次评估结果')
    expect(wrapper.get('[data-testid="evaluation-record"]').attributes('data-evaluation-id')).toBe('instance-1-8')
  })

  it('never moves the current record back when a read lags behind the evaluation', async () => {
    api.getOpenAIEvalAccountOverview.mockResolvedValue(overview({ summary: summary({ evaluation_id: 'instance-1-8' }) }))
    api.evaluateOpenAIEvalRanking.mockResolvedValue(summary({ evaluation_id: 'instance-1-9', published_at: '2026-10-03T08:30:01Z' }))
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="evaluate-now"]').trigger('click')
    await flushPromises()
    const records = wrapper.findAll('[data-testid="evaluation-record"]')
    expect(records.map(record => record.attributes('data-evaluation-id'))).toEqual(['instance-1-9', 'instance-1-8'])
  })

  it('states a failed evaluation without inventing a record', async () => {
    api.evaluateOpenAIEvalRanking.mockRejectedValue({ response: { status: 500 }, error: 'scorer crashed' })
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="evaluate-now"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="evaluate-message"]').text()).toContain('评估失败：scorer crashed')
    expect(wrapper.find('[data-testid="evaluation-record"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="record-empty"]').exists()).toBe(true)
  })
})

describe('actual dispatch separation', () => {
  it('says an empty request list is limited to this instance, not proof of no traffic', async () => {
    const wrapper = mountView()
    await flushPromises()
    await openRequests(wrapper)
    const empty = wrapper.get('[data-testid="requests-empty"]')
    expect(empty.text()).toContain('本实例暂无请求调度记录')
    expect(empty.text()).toContain('空列表不代表没有流量')
    // The instance keeps 256 records; 50 is only how many one read returns.
    expect(empty.text()).toContain('最多 256 条')
    expect(empty.text()).not.toContain('50')
  })

  it('keeps request records apart from evaluation records and states a failed read', async () => {
    api.listSchedulerDecisions.mockRejectedValue({ message: 'ledger down' })
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.find('[data-testid="records-requests"]').exists()).toBe(false)
    await openRequests(wrapper)
    expect(wrapper.find('[data-testid="records-evaluations"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="requests-error"]').text()).toContain('ledger down')
    // A failed read is never presented as "no records on this instance".
    expect(wrapper.find('[data-testid="requests-empty"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="requests-unavailable"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="records-tab-requests"]').attributes('aria-selected')).toBe('true')
  })

  it('labels the ledger as real dispatch and never as an offline recommendation', async () => {
    api.listSchedulerDecisions.mockResolvedValue({
      limit: 50,
      items: [{
        at: '2026-10-03T08:00:00Z',
        layer: 'load_balance',
        reason_code: 'load_balance_selection',
        reason_text: '',
        requested_model: 'gpt-5',
        scheduling_policy: 'avoid_degradation',
        record_type: 'actual_dispatch',
        ranking_basis: 'snapshot',
        selected_rank: 1,
        evaluation_id: 'instance-1-7',
        snapshot_evaluated_at: '2026-10-03T07:00:00Z',
        sticky_previous_hit: false,
        sticky_session_hit: false,
        candidate_count: 1,
        top_k: 1,
        latency_ms: 1,
        load_skew: 0,
        selected_account_id: 11,
        selected_account_type: 'oauth',
        excluded_account_count: 0,
        previous_response_given: false,
        session_given: false
      }]
    })
    const wrapper = mountView()
    await flushPromises()
    await openRequests(wrapper)
    expect(wrapper.get('[data-testid="dispatch-banner"]').text()).toContain('每一行都是一次真实请求')
    expect(wrapper.get('[data-testid="dispatch-banner"]').text()).toContain('评估不会在这里新增记录')
    const row = wrapper.get('[data-testid="decision-row"]')
    expect(row.get('[data-testid="dispatch-basis"]').text()).toContain('顺序：已发布的评估')
    expect(row.get('[data-testid="dispatch-rank"]').text()).toContain('第 1 位')
  })

  it('marks a request-time fallback dispatch and never fabricates a rank', async () => {
    api.listSchedulerDecisions.mockResolvedValue({
      limit: 50,
      items: [{
        at: '2026-10-03T08:00:00Z',
        layer: 'load_balance',
        reason_code: 'load_balance_selection',
        reason_text: '',
        requested_model: 'gpt-5-mini',
        ranking_basis: 'live_fallback',
        ranking_fallback_reason: 'dimension_not_covered',
        selected_rank: null,
        sticky_previous_hit: false,
        sticky_session_hit: false,
        candidate_count: 1,
        top_k: 1,
        latency_ms: 1,
        load_skew: 0,
        selected_account_id: 11,
        selected_account_type: 'oauth',
        excluded_account_count: 0,
        previous_response_given: false,
        session_given: false
      }]
    })
    const wrapper = mountView()
    await flushPromises()
    await openRequests(wrapper)
    const row = wrapper.get('[data-testid="decision-row"]')
    expect(row.get('[data-testid="dispatch-basis"]').text()).toContain('请求时按同一策略计算')
    expect(row.find('[data-testid="dispatch-rank"]').exists()).toBe(false)
    expect(row.get('[data-testid="dispatch-fallback"]').text()).toContain('该分组、模型与推理强度组合不在已发布的评估范围内')
  })

  it('labels a cold-pool overview prior and keeps the model pass rate unknown', async () => {
    api.listSchedulerDecisions.mockResolvedValue({
      limit: 50,
      items: [{
        at: '2026-10-03T08:00:00Z',
        layer: 'load_balance',
        reason_code: 'explicit_policy_rank',
        reason_text: '',
        requested_model: 'gpt-7-new',
        scheduling_policy: 'avoid_degradation',
        ranking_basis: 'overview_prior',
        selected_rank: 1,
        sticky_previous_hit: false,
        sticky_session_hit: false,
        candidate_count: 1,
        top_k: 1,
        latency_ms: 1,
        load_skew: 0,
        selected_account_id: 11,
        selected_account_type: 'oauth',
        excluded_account_count: 0,
        previous_response_given: false,
        session_given: false,
        candidates: [{
          account_id: 11, eligible: true, selected: true, in_top_k: true, rank: 1, priority_score: 82.4,
          decision_reason: 'overview_prior', quality_basis: 'unknown_current_model',
          factors: undefined,
          overview_prior: { rank: 3, priority_score: 82.4, priority: { quality_known: true, quality_ratio: 1, operational_score: 82.4, quality_tier: 1 } }
        }]
      }]
    })
    const wrapper = mountView()
    await flushPromises()
    await openRequests(wrapper)
    const row = wrapper.get('[data-testid="decision-row"]')
    expect(row.get('[data-testid="dispatch-basis"]').text()).toContain('账号总排行名次')
    expect(row.get('[data-testid="dispatch-overview-prior"]').text()).toContain('通过率仍为未知')
    await row.get('.ledger-toggle').trigger('click')
    expect(wrapper.get('[data-testid="candidate-overview-rank"]').text()).toBe('总榜第 3')
    expect(wrapper.get('[data-testid="candidate-row"]').text()).toContain('按账号总排行排位')
    // The overview's pass rate is never presented as this model's pass rate.
    expect(wrapper.get('[data-testid="candidate-quality"]').text()).toContain('未知')
  })

  it('explains an owner override without flagging a routing failure', async () => {
    api.listSchedulerDecisions.mockResolvedValue({
      limit: 50,
      items: [{
        at: '2026-10-03T08:00:00Z',
        layer: 'previous_response_id',
        reason_code: 'previous_response_sticky',
        reason_text: '',
        requested_model: 'gpt-5',
        ranking_basis: 'owner',
        selected_rank: null,
        sticky_previous_hit: true,
        sticky_session_hit: false,
        candidate_count: 1,
        top_k: 1,
        latency_ms: 1,
        load_skew: 0,
        selected_account_id: 11,
        selected_account_type: 'oauth',
        excluded_account_count: 0,
        previous_response_given: true,
        session_given: false
      }]
    })
    const wrapper = mountView()
    await flushPromises()
    await openRequests(wrapper)
    const row = wrapper.get('[data-testid="decision-row"]')
    expect(row.get('[data-testid="dispatch-basis"]').text()).toContain('账号绑定，非评估顺序')
    expect(row.get('[data-testid="dispatch-owner"]').text()).toContain('未按评估顺序选择')
    expect(row.classes()).not.toContain('ledger-row-problem')
  })
})
