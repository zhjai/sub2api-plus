import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { OpenAIEvalAccountOverview, OpenAIEvalOverviewAccount, OpenAIEvalRankingFactors, OpenAIEvalRankingSummary } from '@/api/admin/accounts'
import { zhT } from '@/views/admin/modelIntegrity/__tests__/zhT'

const api = vi.hoisted(() => ({ getOpenAIEvalAccountOverview: vi.fn() }))
vi.mock('@/api/admin/accounts', () => ({ default: api, accountsAPI: api }))
vi.mock('vue-i18n', async importOriginal => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: zhT })
}))

import AccountLeaderboard from '../AccountLeaderboard.vue'

function summary(id = 'gen-1', overrides: Partial<OpenAIEvalRankingSummary> = {}): OpenAIEvalRankingSummary {
  return {
    evaluation_id: id,
    record_type: 'policy_evaluation',
    scope: 'this_instance',
    algorithm_version: 'v2',
    data_version: 'd',
    evaluated_at: '2026-10-04T08:00:00Z',
    published_at: '2026-10-04T08:00:01Z',
    next_evaluation_at: '2026-10-04T09:00:00Z',
    next_evaluation_reason: 'interval',
    trigger: 'manual',
    config_revision: 4,
    effects_enabled: true,
    dimension_count: 3,
    account_count: 13,
    account_row_count: 13,
    quality_route_count: 2,
    truncated: false,
    coverage: { status: 'complete', discovery_complete: true, discovered_dimension_count: 3, cached_dimension_count: 3, uncached_dimension_count: 0, wildcard_routes_present: false, reasons: [] },
    ...overrides
  }
}

function factors(overrides: Partial<{ [K in keyof OpenAIEvalRankingFactors]: Partial<OpenAIEvalRankingFactors[K]> }> = {}): OpenAIEvalRankingFactors {
  const base: OpenAIEvalRankingFactors = {
    price: { score: 0.8, known: true, observed_at: null, unknown_reason: null, rate_multiplier: 1.2, source: 'account_rate' },
    error_rate: { score: 0.95, known: true, observed_at: null, unknown_reason: null, value: 0.008, sample_count: 40 },
    ttft: { score: 0.7, known: true, observed_at: null, unknown_reason: null, ms: 820, sample_count: 40 },
    load: { score: 0.65, known: true, observed_at: null, unknown_reason: null, load_rate: 35, waiting: 0, current_concurrency: 3 },
    quality: { score: 1, known: true, observed_at: null, unknown_reason: null, state: 'assessed', pass: 2, suspected_pass: 0, selected: 2, evaluated: 2, ratio: 2 / 3, expires_at: null }
  }
  return {
    price: { ...base.price, ...overrides.price },
    error_rate: { ...base.error_rate, ...overrides.error_rate },
    ttft: { ...base.ttft, ...overrides.ttft },
    load: { ...base.load, ...overrides.load },
    quality: { ...base.quality, ...overrides.quality }
  }
}

function account(id: number, rank: number, overrides: Partial<OpenAIEvalOverviewAccount> = {}): OpenAIEvalOverviewAccount {
  return {
    account_id: id,
    account_name: `acct-${id}`,
    rank,
    priority_score: 82.4,
    quality_tier: null,
    eligible: true,
    exclusion_reason: null,
    upstream_models: [],
    factors: factors(),
    contributions: { price: 32, error_rate: 33.2, ttft: 10.5, load: 6.5, quality: 0 },
    group_ids: [7],
    model_count: 2,
    quality_model_count: 2,
    unknown_quality_model_count: 0,
    quality_cell_count: 2,
    unknown_quality_cell_count: 0,
    worst_quality_model: 'gpt-5.6',
    worst_quality_ratio: 1 / 3,
    priority: { quality_known: true, quality_ratio: 2 / 3, operational_score: 82.4, quality_tier: 1 },
    models: [
      {
        requested_model: 'gpt-6', reasoning_effort: 'high', upstream_models: ['gpt-6'], policy: 'avoid_degradation',
        factors: factors({ error_rate: { source: 'request_ewma_account_model_effort' }, quality: { ratio: 1 } }),
        sources: ['request_ewma_account_model_effort', 'request_ttft_account_model_effort', 'scheduled_quality']
      },
      {
        requested_model: 'gpt-5.6', reasoning_effort: '', upstream_models: ['gpt-5.6'], policy: 'avoid_degradation',
        factors: factors({ error_rate: { source: 'v1_matched_probe' }, ttft: { known: false, ms: null, score: 0.9, default_applied: true }, quality: { ratio: 1 / 3 } }),
        sources: ['v1_matched_probe', 'scheduled_quality']
      }
    ],
    ...overrides
  }
}

function page(overrides: Partial<OpenAIEvalAccountOverview> = {}): OpenAIEvalAccountOverview {
  return {
    summary: summary(),
    previous_summary: null,
    effective_status: 'active',
    current_config_revision: 4,
    evaluation_in_progress: false,
    ranking_error: null,
    groups: [{ group_id: 7, group_name: 'team-a', member_count: 9, status: 'evaluated', reason: null }, { group_id: 8, group_name: 'team-b', member_count: 4, status: 'evaluated', reason: null }],
    policy: 'avoid_degradation',
    weights: { price: 0.4, error_rate: 0.35, ttft: 0.15, load: 0.1, quality: 0 },
    ordering: 'quality_then_score',
    accounts: [account(11, 1), account(12, 2, {
      factors: factors({ quality: { state: 'unknown', ratio: null, known: false } }),
      priority_score: 79.1,
      priority: { quality_known: false, quality_ratio: null, operational_score: 79.1, quality_tier: null },
      quality_model_count: 0, unknown_quality_model_count: 1, worst_quality_model: null, worst_quality_ratio: null
    })],
    next_cursor: null,
    ...overrides
  }
}

function mountBoard(props: Partial<InstanceType<typeof AccountLeaderboard>['$props']> = {}) {
  return mount(AccountLeaderboard, {
    props: { groups: [], accountLabel: (id: number) => `#${id}`, rules: [], currentRevision: 4, ...props }
  })
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (error: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

beforeEach(() => {
  vi.clearAllMocks()
  api.getOpenAIEvalAccountOverview.mockResolvedValue(page())
})

describe('AccountLeaderboard', () => {
  it('lists unique accounts across all groups with no model or effort selector', async () => {
    const wrapper = mountBoard()
    await flushPromises()
    expect(api.getOpenAIEvalAccountOverview).toHaveBeenCalledWith({ limit: 100 })
    expect(wrapper.findAll('[data-testid="board-row"]').map(row => row.attributes('data-account-id'))).toEqual(['11', '12'])
    expect(wrapper.findAll('select')).toHaveLength(1)
    expect(wrapper.get('[data-testid="board-group"]').text()).toContain('全部分组')
    expect(wrapper.get('[data-testid="board-count"]').text()).toBe('13 个账号')
  })

  it('shows the pass rate above the operational score under quality-first ordering', async () => {
    const wrapper = mountBoard()
    await flushPromises()
    const [first, second] = wrapper.findAll('[data-testid="board-score"]')
    expect(first.get('[data-testid="board-score-quality"]').text()).toBe('66.7%')
    expect(first.get('[data-testid="board-score-operational"]').text()).toBe('运行分 82.4')
    // Unknown quality is never shown as a rate, and the operational score stays visible.
    expect(second.get('[data-testid="board-score-quality"]').text()).toBe('未知')
    expect(second.get('[data-testid="board-score-operational"]').text()).toBe('运行分 79.1')
    // The operational score is never the main value under quality-first order.
    expect(first.get('[data-testid="board-score-quality"]').text()).not.toContain('82.4')
    expect(wrapper.text()).toContain('降智优先')
    expect(wrapper.get('[data-testid="board-ordering"]').text()).toContain('未知排在所有已知之后')
  })

  it('shows the composite score under other policies and marks an ignored pass rate', async () => {
    api.getOpenAIEvalAccountOverview.mockResolvedValue(page({ policy: 'stability_first', ordering: 'score_desc', weights: { price: 0.1, error_rate: 0.5, ttft: 0.3, load: 0.1, quality: 0 } }))
    const wrapper = mountBoard()
    await flushPromises()
    const row = wrapper.get('[data-testid="board-row"]')
    expect(row.get('[data-testid="board-score-composite"]').text()).toBe('82.4')
    expect(row.find('[data-testid="board-score-quality"]').exists()).toBe(false)
    expect(row.get('[data-testid="board-factor-quality"]').text()).toContain('不计分')
    expect(row.get('[data-testid="board-factor-price"]').text()).toContain('80 分')
  })

  it('shows every factor with its raw value, score and source, and missing values as labels', async () => {
    const cold = account(13, 3, {
      factors: factors({
        error_rate: { known: false, value: null, score: 0.9, default_applied: true, source: 'optimistic_default' },
        ttft: { known: false, ms: null, score: 0.9, default_applied: true },
        quality: { state: 'unknown', ratio: null, known: false }
      }),
      model_count: 0,
      quality_model_count: 0,
      unknown_quality_model_count: 0,
      worst_quality_model: null,
      worst_quality_ratio: null,
      models: []
    })
    api.getOpenAIEvalAccountOverview.mockResolvedValue(page({ accounts: [account(11, 1), cold] }))
    const wrapper = mountBoard()
    await flushPromises()
    const [warm, coldRow] = wrapper.findAll('[data-testid="board-row"]')
    expect(warm.get('[data-testid="board-factor-price"]').text()).toContain('1.2x')
    expect(warm.get('[data-testid="board-factor-error_rate"]').text()).toContain('0.8%')
    expect(warm.get('[data-testid="board-factor-ttft"]').text()).toContain('820 ms')
    expect(warm.get('[data-testid="board-factor-load"]').text()).toContain('35%')
    // The account error rate mixes a real-request cell and a probe cell.
    expect(warm.get('[data-testid="board-factor-error_rate"]').attributes('data-source')).toBe('mixed')
    expect(warm.get('[data-testid="board-factor-price"]').attributes('data-source')).toBe('measured')

    const error = coldRow.get('[data-testid="board-factor-error_rate"]')
    // A default is labelled, scored and never shown as 0% or 0 ms.
    expect(error.attributes('data-source')).toBe('default')
    expect(error.text()).toContain('无数据')
    expect(error.text()).toContain('90 分')
    expect(error.text()).toContain('默认')
    expect(error.text()).not.toContain('0%')
    expect(coldRow.get('[data-testid="board-factor-ttft"]').text()).not.toContain('ms')
    expect(coldRow.get('[data-testid="board-factor-quality"]').attributes('data-source')).toBe('unknown')
    expect(coldRow.get('[data-testid="board-coverage"]').text()).toBe('暂无模型证据')
  })

  it('says first-output latency is per model when the account value is a normalised average', async () => {
    const row = account(11, 1, { factors: factors({ ttft: { ms: null, known: true, default_applied: true } }) })
    api.getOpenAIEvalAccountOverview.mockResolvedValue(page({ accounts: [row] }))
    const wrapper = mountBoard()
    await flushPromises()
    const ttft = wrapper.get('[data-testid="board-factor-ttft"]')
    expect(ttft.text()).toContain('见各模型')
    expect(ttft.text()).not.toContain('ms')
    expect(ttft.attributes('data-source')).toBe('mixed')
  })

  it('filters the ungrouped scope with group_id 0', async () => {
    const wrapper = mountBoard()
    await flushPromises()
    await wrapper.get('[data-testid="board-group"]').setValue('0')
    await flushPromises()
    expect(api.getOpenAIEvalAccountOverview).toHaveBeenLastCalledWith({ group_id: 0, limit: 100 })
  })

  it('expands evidence, the worst model, rule exceptions and probe diagnostics', async () => {
    const probed = account(11, 1)
    probed.factors.monitoring = [{ monitor_id: 5, model: 'gpt-5.6', status: 'operational', observed_at: '2026-10-04T07:59:00Z', latency_ms: 2400, ping_latency_ms: 80 }]
    api.getOpenAIEvalAccountOverview.mockResolvedValue(page({ accounts: [probed] }))
    probed.models[1].policy = 'stability_first'
    const wrapper = mountBoard()
    await flushPromises()
    const toggle = wrapper.get('[data-testid="board-toggle"]')
    expect(toggle.attributes('aria-expanded')).toBe('false')
    await toggle.trigger('click')
    expect(toggle.attributes('aria-expanded')).toBe('true')
    const detail = wrapper.get('[data-testid="board-detail"]')
    expect(detail.get('[data-testid="board-worst"]').text()).toContain('gpt-5.6，33.3%')
    expect(detail.get('[data-testid="board-exceptions"]').text()).toContain('「优先稳定」')
    expect(detail.get('[data-testid="board-probes"]').text()).toContain('总耗时 2400 ms，不是首包延迟')
    const models = detail.findAll('[data-testid="board-model-row"]')
    expect(models).toHaveLength(2)
    expect(models[1].get('[data-testid="board-model-error_rate"]').text()).toContain('探测')
    expect(models[1].get('[data-testid="board-model-ttft"]').text()).toContain('默认')
    expect(models[1].get('[data-testid="board-model-ttft"]').text()).toContain('无数据')
    expect(models[1].get('[data-testid="board-model-policy"]').text()).toBe('优先稳定')
    expect(detail.get('[data-testid="board-cells"]').text()).toContain('2 个通过率已知')
    expect(detail.get('[data-testid="board-contributions"]').text()).toContain('价格 +32')
  })

  it('filters by group on the server and keeps the global rank', async () => {
    const wrapper = mountBoard({ groups: [{ id: 8, name: 'team-b' } as never] })
    await flushPromises()
    api.getOpenAIEvalAccountOverview.mockResolvedValue(page({ accounts: [account(12, 2, { group_ids: [8] })] }))
    await wrapper.get('[data-testid="board-group"]').setValue('8')
    await flushPromises()
    expect(api.getOpenAIEvalAccountOverview).toHaveBeenLastCalledWith({ group_id: 8, limit: 100 })
    expect(wrapper.get('[data-testid="board-rank"]').text()).toBe('2')
    expect(wrapper.get('[data-testid="board-filter-note"]').text()).toContain('不改变名次')
    expect(wrapper.get('[data-testid="board-count"]').text()).toBe('该分组 1 个账号')
  })

  it('drops an answer for a group the administrator already left', async () => {
    const wrapper = mountBoard()
    await flushPromises()
    const slow = deferred<OpenAIEvalAccountOverview>()
    api.getOpenAIEvalAccountOverview.mockReturnValueOnce(slow.promise)
    await wrapper.get('[data-testid="board-group"]').setValue('7')
    api.getOpenAIEvalAccountOverview.mockResolvedValueOnce(page({ accounts: [account(21, 5)] }))
    await wrapper.get('[data-testid="board-group"]').setValue('8')
    await flushPromises()
    slow.resolve(page({ accounts: [account(99, 1)] }))
    await flushPromises()
    expect(wrapper.findAll('[data-testid="board-row"]').map(row => row.attributes('data-account-id'))).toEqual(['21'])
  })

  it('pages with the cursor bound to the same generation and group', async () => {
    api.getOpenAIEvalAccountOverview.mockResolvedValueOnce(page({ next_cursor: 'c1' }))
    const wrapper = mountBoard()
    await flushPromises()
    expect(wrapper.get('[data-testid="board-count"]').text()).toBe('13 个账号')
    api.getOpenAIEvalAccountOverview.mockResolvedValueOnce(page({ accounts: [account(12, 2), account(13, 3)], next_cursor: null }))
    await wrapper.get('[data-testid="board-more"]').trigger('click')
    await flushPromises()
    expect(api.getOpenAIEvalAccountOverview).toHaveBeenLastCalledWith({ evaluation_id: 'gen-1', cursor: 'c1', limit: 100 })
    // The boundary row is not repeated.
    expect(wrapper.findAll('[data-testid="board-row"]').map(row => row.attributes('data-account-id'))).toEqual(['11', '12', '13'])
    expect(wrapper.find('[data-testid="board-more"]').exists()).toBe(false)
  })

  it('restarts from the first page when the next page belongs to another generation', async () => {
    api.getOpenAIEvalAccountOverview.mockResolvedValueOnce(page({ next_cursor: 'c1' }))
    const wrapper = mountBoard()
    await flushPromises()
    api.getOpenAIEvalAccountOverview
      .mockResolvedValueOnce(page({ summary: summary('gen-2'), accounts: [account(50, 9)] }))
      .mockResolvedValueOnce(page({ summary: summary('gen-2'), accounts: [account(31, 1)] }))
    await wrapper.get('[data-testid="board-more"]').trigger('click')
    await flushPromises()
    // Never spliced: the list is the new generation's first page.
    expect(wrapper.findAll('[data-testid="board-row"]').map(row => row.attributes('data-account-id'))).toEqual(['31'])
    expect(wrapper.get('[data-testid="board-notice"]').text()).toContain('已发布新的评估')
  })

  it('restarts once on an expired cursor and stops instead of looping', async () => {
    api.getOpenAIEvalAccountOverview.mockResolvedValueOnce(page({ next_cursor: 'c1' }))
    const wrapper = mountBoard()
    await flushPromises()
    api.getOpenAIEvalAccountOverview
      .mockRejectedValueOnce({ status: 409, code: 'RANKING_SNAPSHOT_CHANGED' })
      .mockResolvedValueOnce(page({ next_cursor: 'c1' }))
      .mockRejectedValueOnce({ status: 409, code: 'RANKING_SNAPSHOT_CHANGED' })
    await wrapper.get('[data-testid="board-more"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="board-more"]').trigger('click')
    await flushPromises()
    expect(api.getOpenAIEvalAccountOverview).toHaveBeenCalledTimes(4)
    expect(wrapper.get('[data-testid="board-page-error"]').text()).toContain('不再提供')
  })

  it('keeps the previous result, labelled, when a read after an evaluation fails', async () => {
    const wrapper = mountBoard()
    await flushPromises()
    api.getOpenAIEvalAccountOverview.mockRejectedValueOnce({ error: 'overview timeout' })
    const outcome = await (wrapper.vm as unknown as { reload: (o: { invalidate: boolean }) => Promise<{ ok: boolean }> }).reload({ invalidate: true })
    await flushPromises()
    expect(outcome.ok).toBe(false)
    expect(wrapper.get('[data-testid="board-error"]').text()).toContain('overview timeout')
    expect(wrapper.get('[data-testid="board-rows-kept"]').text()).toContain('上一次评估的结果')
    expect(wrapper.findAll('[data-testid="board-row"]')).toHaveLength(2)
  })

  it('never shows another group’s rows after a failed filtered read', async () => {
    const wrapper = mountBoard()
    await flushPromises()
    api.getOpenAIEvalAccountOverview.mockRejectedValueOnce({ error: 'boom' })
    await wrapper.get('[data-testid="board-group"]').setValue('8')
    await flushPromises()
    expect(wrapper.findAll('[data-testid="board-row"]')).toHaveLength(0)
    expect(wrapper.find('[data-testid="board-empty"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="board-error"]').text()).toContain('boom')
  })

  it('invites an evaluation when none exists and flags a stale revision', async () => {
    api.getOpenAIEvalAccountOverview.mockResolvedValueOnce(page({ summary: null, accounts: [] }))
    const empty = mountBoard()
    await flushPromises()
    expect(empty.get('[data-testid="board-empty"]').text()).toContain('立即评估')

    const stale = mountBoard({ currentRevision: 5 })
    await flushPromises()
    expect(stale.get('[data-testid="board-stale"]').text()).toContain('版本 4')
  })
})
