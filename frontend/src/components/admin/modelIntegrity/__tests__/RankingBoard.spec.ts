import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { OpenAIEvalRankingDimension, OpenAIEvalRankingFactors, OpenAIEvalRankingSnapshot } from '@/api/admin/accounts'
import { zhT } from '@/views/admin/modelIntegrity/__tests__/zhT'

const api = vi.hoisted(() => ({
  getOpenAIEvalRankings: vi.fn(),
  getOpenAIEvalRankingAccounts: vi.fn()
}))

vi.mock('@/api/admin/accounts', () => ({ accountsAPI: api }))
vi.mock('vue-i18n', async importOriginal => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: zhT })
}))

import RankingBoard from '../RankingBoard.vue'

function factors(overrides: Partial<OpenAIEvalRankingFactors['quality']> = {}, priceKnown = true): OpenAIEvalRankingFactors {
  return {
    price: { score: 0.9, known: priceKnown, observed_at: '2026-10-03T07:00:00Z', unknown_reason: null, rate_multiplier: 0.5, source: 'account_mapping' },
    error_rate: { score: 0.8, known: true, observed_at: null, unknown_reason: null, value: 0.02, sample_count: 42 },
    ttft: { score: 0.7, known: true, observed_at: null, unknown_reason: null, ms: 512, sample_count: 42 },
    load: { score: 0.6, known: true, observed_at: null, unknown_reason: null, load_rate: 35, waiting: 1, current_concurrency: 3 },
    quality: { score: 0.5, known: true, observed_at: null, unknown_reason: null, state: 'assessed', pass: 1, suspected_pass: 1, selected: 3, evaluated: 3, ratio: 2 / 3, expires_at: null, ...overrides }
  }
}

function dimension(overrides: Partial<OpenAIEvalRankingDimension> = {}): OpenAIEvalRankingDimension {
  return {
    dimension_id: 'd-1',
    group_id: 7,
    group_name: 'openai-team',
    requested_model: 'gpt-5',
    reasoning_effort: 'high',
    selection_model: 'gpt-5',
    selection_model_variants: [],
    policy: 'avoid_degradation',
    weights: { price: 0.4, error_rate: 0.35, ttft: 0.15, load: 0.1, quality: 0 },
    ordering: 'quality_then_score',
    sources: ['catalog', 'observed_route'],
    candidate_count: 2,
    eligible_count: 1,
    preferred_account_id: 11,
    coverage_status: 'complete',
    fallback_reason: null,
    valid_until: null,
    accounts: [
      {
        account_id: 11,
        account_name: 'oauth-a',
        rank: 1,
        priority_score: 88.5,
        quality_tier: 0,
        eligible: true,
        exclusion_reason: null,
        upstream_models: ['gpt-5'],
        factors: factors(),
        contributions: { price: 36, error_rate: 28, ttft: 10.5, load: 6, quality: 0 }
      },
      {
        account_id: 12,
        account_name: 'oauth-b',
        rank: null,
        priority_score: null,
        quality_tier: null,
        eligible: false,
        exclusion_reason: 'model_not_supported',
        exclusion_reasons: [{ code: 'model_not_supported', scope: 'route', observed_at: '2026-10-03T08:00:00Z' }],
        upstream_models: [],
        factors: factors({ state: 'insufficient', unknown_reason: 'quality_insufficient', ratio: null }, false),
        contributions: { price: 0, error_rate: 0, ttft: 0, load: 0, quality: 0 }
      }
    ],
    accounts_truncated: false,
    accounts_next_cursor: null,
    ...overrides
  }
}

function snapshot(overrides: Partial<OpenAIEvalRankingSnapshot> = {}): OpenAIEvalRankingSnapshot {
  return {
    summary: {
      evaluation_id: 'instance-1-7',
      record_type: 'policy_evaluation',
      scope: 'this_instance',
      algorithm_version: 'v1',
      data_version: 'data-v1',
      evaluated_at: '2026-10-03T08:00:00Z',
      published_at: '2026-10-03T08:00:01Z',
      next_evaluation_at: '2026-10-03T09:00:00Z',
      next_evaluation_reason: 'interval',
      trigger: 'policy_saved',
      config_revision: 4,
      effects_enabled: true,
      dimension_count: 1,
      account_count: 2,
      account_row_count: 2,
      quality_route_count: 1,
      truncated: false,
      coverage: { status: 'complete', discovery_complete: true, discovered_dimension_count: 1, cached_dimension_count: 1, uncached_dimension_count: 0, wildcard_routes_present: false, reasons: [] }
    },
    effective_status: 'active',
    current_config_revision: 4,
    evaluation_in_progress: false,
    ranking_error: null,
    groups: [{ group_id: 7, group_name: 'openai-team', member_count: 2, status: 'evaluated', reason: null }],
    dimensions: [dimension()],
    next_cursor: null,
    ...overrides
  }
}

function mountBoard(props: Record<string, unknown> = {}) {
  return mount(RankingBoard, {
    props: {
      snapshot: snapshot(),
      effectiveStatus: 'active',
      summaryRevision: 4,
      currentRevision: 4,
      groups: [{ id: 7, name: 'openai-team' }],
      catalogModels: ['gpt-5'],
      efforts: ['', 'high'],
      accountLabel: (id: number) => `#${id}`,
      ...props
    }
  })
}

beforeEach(() => {
  vi.clearAllMocks()
  api.getOpenAIEvalRankings.mockResolvedValue(snapshot())
  api.getOpenAIEvalRankingAccounts.mockResolvedValue(snapshot())
})

describe('RankingBoard', () => {
  it('renders the server-computed rank, score and every factor without recalculating', async () => {
    const wrapper = mountBoard()
    await wrapper.get('[data-testid="ranking-scope"] button').trigger('click')
    await flushPromises()
    const rows = wrapper.findAll('[data-testid="ranking-row"]')
    expect(rows).toHaveLength(2)
    // Rank and score come straight from the payload.
    expect(rows[0].text()).toContain('88.5')
    expect(rows[0].text()).toContain('1')
    expect(rows[0].find('[data-testid="factor-unknown-price"]').exists()).toBe(false)
    expect(rows[0].text()).toContain('0.5x')
    expect(rows[0].text()).toContain('2.0%')
    expect(rows[0].text()).toContain('512 ms')
    expect(rows[0].text()).toContain('35%')
    expect(rows[0].text()).toContain('66.7%')
    expect(rows[0].text()).toContain('2/3 项测试通过')
  })

  it('shows a missing factor as unknown and never as a healthy reading', async () => {
    // The scope read returns only the ineligible account, with no usable price.
    const filtered = snapshot({ dimensions: [{ ...dimension(), accounts: [dimension().accounts![1]] }] })
    api.getOpenAIEvalRankings.mockResolvedValue(filtered)
    const missing = mountBoard({ snapshot: filtered })
    await missing.get('[data-testid="ranking-scope"] button').trigger('click')
    await flushPromises()
    const row = missing.get('[data-testid="ranking-row"]')
    expect(row.get('[data-testid="factor-unknown-price"]').text()).toBe('未知')
    expect(row.get('[data-testid="ranking-quality-unknown"]').text()).toBe('未知')
    // An unknown pass rate must never be rendered as a percentage.
    expect(row.text()).not.toContain('100%')
  })

  it('explains why an account is excluded, with the scope of the restriction', async () => {
    const wrapper = mountBoard({ groups: [] })
    await wrapper.get('[data-testid="ranking-scope"] button').trigger('click')
    await flushPromises()
    const excluded = wrapper.findAll('[data-testid="ranking-row"]')[1]
    expect(excluded.text()).toContain('已排除')
    expect(excluded.text()).toContain('路由')
    expect(excluded.text()).toContain('账号不支持该模型')
  })

  it('labels a request-time fallback scope so it is not read as a published order', async () => {
    const wrapper = mountBoard({
      snapshot: snapshot({
        effective_status: 'live_fallback',
        dimensions: [dimension({ coverage_status: 'live_fallback', fallback_reason: 'dimension_not_covered' })]
      })
    })
    expect(wrapper.text()).toContain('请求时回退')
    expect(wrapper.text()).toContain('该分组、模型与推理强度组合不在已发布的评估范围内')
  })

  it('warns that the shown order belongs to an older revision', () => {
    const wrapper = mountBoard({ summaryRevision: 4, currentRevision: 5 })
    expect(wrapper.get('[data-testid="ranking-stale"]').text()).toContain('配置已是版本 5')
  })

  it('reports a server ranking error instead of implying a working order', () => {
    const wrapper = mountBoard({
      snapshot: snapshot({ summary: null, effective_status: 'error', ranking_error: { code: 'build_failed', message: 'evaluation budget exceeded', config_revision: 4, evaluation_id: null } }),
      effectiveStatus: 'error'
    })
    expect(wrapper.get('[data-testid="ranking-status"]').text()).toContain('evaluation budget exceeded')
  })

  it('restarts the list when a cursor belongs to a different evaluation generation', async () => {
    const first = snapshot({ dimensions: [dimension({ accounts_next_cursor: 'c1' })] })
    // A page from a newer generation repeats account 11; it must replace, not append.
    const second = snapshot({
      summary: { ...first.summary!, evaluation_id: 'instance-1-9' },
      dimensions: [dimension({ accounts_next_cursor: null, accounts: [dimension().accounts![0]] })]
    })
    api.getOpenAIEvalRankings.mockResolvedValue(first)
    api.getOpenAIEvalRankingAccounts.mockResolvedValue(second)
    const wrapper = mountBoard({ snapshot: first })
    await wrapper.get('[data-testid="ranking-scope"] button').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="ranking-load-more"]').trigger('click')
    await flushPromises()
    expect(api.getOpenAIEvalRankingAccounts).toHaveBeenCalledWith(expect.objectContaining({ cursor: 'c1', group_id: 7, requested_model: 'gpt-5', reasoning_effort: 'high' }))
    // The second page repeats account 11; it is not duplicated in the list.
    expect(wrapper.findAll('[data-testid="ranking-row"]')).toHaveLength(1)
  })

  it('states the weights and that load was measured at evaluation time', async () => {
    const wrapper = mountBoard()
    await wrapper.get('[data-testid="ranking-scope"] button').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('评估时的并发负载')
    expect(wrapper.text()).toContain('实际并发与速率限制仍按请求实时判断')
    expect(wrapper.text()).toContain('先按降智通过率分档排序，再按总分排序')
  })

  it('sends no filter at all until a group, model and effort are all chosen', async () => {
    const wrapper = mountBoard()
    // The explicit reload is unfiltered, so the server returns scope summaries only.
    await wrapper.get('[data-testid="ranking-reload"]').trigger('click')
    await flushPromises()
    expect(api.getOpenAIEvalRankings).toHaveBeenLastCalledWith({ limit: 200 })

    await wrapper.get('[data-testid="ranking-model"]').setValue('gpt-5')
    await wrapper.get('[data-testid="ranking-effort"]').setValue('high')
    await wrapper.get('[data-testid="ranking-scope"] button').trigger('click')
    await flushPromises()
    expect(api.getOpenAIEvalRankings).toHaveBeenLastCalledWith({
      group_id: 7,
      requested_model: 'gpt-5',
      reasoning_effort: 'high',
      limit: 200
    })
  })

  it('keeps "no group", "no effort specified" and "any effort" as distinct filters', async () => {
    const wrapper = mountBoard({
      snapshot: snapshot({
        groups: [{ group_id: null, group_name: '', member_count: 1, status: 'evaluated', reason: null }],
        dimensions: [dimension({ group_id: null, group_name: '', reasoning_effort: '' })]
      })
    })
    // group_id=0 is the no-group scope, and '' is a real effort filter.
    await wrapper.get('[data-testid="ranking-group"]').setValue('0')
    await wrapper.get('[data-testid="ranking-effort"]').setValue('')
    await wrapper.get('[data-testid="ranking-model"]').setValue('gpt-5')
    await wrapper.get('[data-testid="ranking-scope"] button').trigger('click')
    await flushPromises()
    expect(api.getOpenAIEvalRankings).toHaveBeenLastCalledWith({
      group_id: 0,
      requested_model: 'gpt-5',
      reasoning_effort: '',
      limit: 200
    })
  })
})
