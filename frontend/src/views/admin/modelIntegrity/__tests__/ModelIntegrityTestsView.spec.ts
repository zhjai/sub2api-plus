import { flushPromises, mount, RouterLinkStub } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { OpenAIEvalConfig } from '@/api/admin/accounts'
import { zhT } from './zhT'
import { evalConfigServer } from './evalConfigServer'

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
  items: [{ id: 'gpt-5' }, { id: 'gpt-5.1' }],
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
      data_version: 'sub2api-candy-29-v2-cpa-fingerprint-5654020c',
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
    expect(probe.text()).toContain('只支持直连 OpenAI OAuth 账号')
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
        run(3, 'modeltrace', 'suspected_normal', 'non_luna_behavioral_attribution', { modeltrace: { bank_revision: 'x', prediction: 'gpt-4.1', probability: 0.8, used_outputs: 3, requests: 3 } }),
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
    expect(note?.textContent?.trim()).toBe('归因基于回答行为推断，不能证明实际路由。该测试开启自动运行时，最近一次已完成的归因结论会计入降智通过率，手动或自动运行均可。ModelTrace 只要有一条有效输出即可归因，失败的请求仍保留以供排查；行为指纹需全部计划采样均有效才会归因。')
    expect(document.body.querySelector('[data-testid="detail-status"]')?.textContent?.trim()).toBe('疑似 Luna')
    expect(document.body.textContent).toContain('gpt-5-luna')
    wrapper.unmount()
  })

  it('shows a target-matching ModelTrace attribution as normal and a Luna one as abnormal, with the original verdict kept apart', async () => {
    const run = (id: number, status: string, reason: string, prediction: string, attribution?: Record<string, string>) => ({
      id, account_id: 12, test_type: 'modeltrace', requested_model: 'gpt-5', reasoning_effort: 'high', status,
      outcome: { status, reason, sample_count: 2, expected_count: 3, confidence: 'low', scheduling: 'alert_only', attribution,
        modeltrace: { bank_revision: 'mt-bank-7', prediction, probability: 0.82, used_outputs: 2, requests: 3, candidates: [{ model: prediction, display_name: prediction, family: 'f', family_name: 'F', probability: 0.82, profile_similarity: 0.9, score: 1 }] } },
      request_count: 3, input_tokens: 1, output_tokens: 1, duration_ms: 1000,
      started_at: `2026-10-01T0${id}:00:00Z`, finished_at: `2026-10-01T0${id}:00:05Z`, trigger_source: 'scheduled'
    })
    api.listOpenAIEvalRuns.mockResolvedValue({
      items: [
        run(2, 'pass', 'modeltrace_target_match', 'gpt-5', { rule_version: 'public-target-match-luna-v2', original_rule_version: 'non-luna-attribution-v1', original_status: 'suspected_normal', original_reason: 'non_luna_behavioral_attribution' }),
        run(1, 'warning', 'modeltrace_luna_attribution', 'gpt-5.6-luna', { rule_version: 'public-target-match-luna-v2', original_rule_version: 'public-target-match-luna-v2', original_status: 'warning', original_reason: 'modeltrace_luna_attribution' })
      ]
    })
    const wrapper = mountView()
    await flushPromises()

    const trace = wrapper.get('[data-testid="test-modeltrace"]')
    expect(trace.get('[data-testid="latest-status"]').text()).toBe('正常')
    expect(trace.find('.tt-result-ok').exists()).toBe(true)
    expect(wrapper.findAll('[data-testid="history-status"]').map(item => item.text())).toEqual(['正常', '异常'])

    const rows = wrapper.findAll('[data-testid="history-row"]')
    await rows[0].trigger('click')
    await flushPromises()
    const text = (id: string) => document.body.querySelector(`[data-testid="${id}"]`)?.textContent?.trim()
    expect(text('detail-status')).toBe('正常')
    expect(text('detail-target')).toBe('gpt-5')
    expect(text('detail-prediction')).toBe('gpt-5')
    expect(text('detail-outputs')).toBe('2/3')
    expect(text('detail-bank')).toBe('mt-bank-7')
    expect(text('detail-rule')).toBe('public-target-match-luna-v2')
    expect(text('detail-reinterpreted')).toBe('该记录原判定为疑似正常（规则 non-luna-attribution-v1）。上方结果按当前规则 public-target-match-luna-v2 对同一批已记录输出重新解读，测试并未重新运行。')
    expect(text('attribution-note')).toContain('归因基于回答行为推断，不能证明实际路由。')
    expect(text('detail-technical')).toContain('original: suspected_normal / non_luna_behavioral_attribution / non-luna-attribution-v1')
    expect(document.body.textContent).toContain('候选模型')

    await rows[1].trigger('click')
    await flushPromises()
    expect(text('detail-status')).toBe('异常')
    expect(text('detail-prediction')).toBe('gpt-5.6-luna')
    expect(document.body.querySelector('[data-testid="detail-reinterpreted"]')).toBeNull()
    expect(text('attribution-note')).toContain('不能证明实际路由')
    wrapper.unmount()
  })

  it('keeps a valid partial ModelTrace match normal in history and names the failed request', async () => {
    api.listOpenAIEvalRuns.mockResolvedValue({
      items: [{
        id: 5, account_id: 12, test_type: 'modeltrace', requested_model: 'gpt-5', reasoning_effort: 'high', status: 'pass',
        outcome: { status: 'pass', reason: 'modeltrace_target_match', sample_count: 2, expected_count: 3, confidence: 'low', scheduling: 'alert_only',
          modeltrace: { bank_revision: 'mt-bank-7', prediction: 'gpt-5', probability: 0.82, used_outputs: 2, requests: 3,
            samples: [{ accepted: true, answer: '1 2', attempts: 1 }, { accepted: true, answer: '3 4', attempts: 1 }, { accepted: false, error: 'http_502', http_status: 502, attempts: 3, error_message: 'bad gateway' }] } },
        request_count: 5, input_tokens: 1, output_tokens: 1, duration_ms: 1000,
        started_at: '2026-10-01T05:00:00Z', finished_at: '2026-10-01T05:00:05Z', trigger_source: 'scheduled'
      }]
    })
    const wrapper = mountView()
    await flushPromises()

    const row = wrapper.get('[data-testid="history-row"]')
    expect(row.get('[data-testid="history-status"]').text()).toBe('正常')
    expect(row.text()).toContain('判定为正常。该结果为行为推断，不代表实际路由已核实。1 个样本请求失败：HTTP 502，bad gateway（尝试 3 次）。')
    const latest = wrapper.get('[data-testid="test-modeltrace"]')
    expect(latest.get('[data-testid="latest-status"]').text()).toBe('正常')
    expect(latest.get('[data-testid="latest-explanation"]').text()).toContain('1 个样本请求失败')
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

  it('names why a manual run failed, with credentials masked', async () => {
    api.runOpenAIEval.mockRejectedValue({ message: 'upstream 401: Bearer abcdef1234567890 rejected for sk-live1234567890abcd' })
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="run-candy"]').trigger('click')
    await flushPromises()

    const shown = store.showError.mock.calls.at(-1)?.[0] as string
    expect(shown).toContain('upstream 401')
    expect(shown).toContain('Bearer [redacted]')
    expect(shown).toContain('sk-[redacted]')
    expect(shown).not.toContain('abcdef1234567890')
    // The run is no longer shown as in progress, so it can be started again.
    expect(wrapper.find('[data-testid="test-progress"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="run-candy"]').attributes('disabled')).toBeUndefined()
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

    expect(api.runOpenAIEval).toHaveBeenCalledWith({ account_id: 12, requested_model: 'gpt-5', reasoning_effort: 'high', test_type: 'fingerprint', sample_mode: 'strict', max_attempts: 3 })
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
    // A legacy rule without the switch is sent back explicitly enabled, which the server reads the same way.
    expect(payload.policies).toEqual([{ requested_model: 'gpt-5', reasoning_effort: 'high', policy: 'cost_first', enabled: true }])
    expect(payload.effects_enabled).toBe(true)
    expect(payload.accounts.map(route => `${route.account_id}:${route.reasoning_effort}`)).toEqual(['12:high', '11:', '12:'])
    expect(payload.accounts.every(route => !('direct_oauth_eligible' in route))).toBe(true)
    wrapper.unmount()
  })

  it('offers State Probe on a new high-effort target only where the server would allow it', async () => {
    api.list.mockResolvedValue({
      items: [
        { id: 21, name: 'direct-oauth', platform: 'openai', type: 'oauth', parent_account_id: null, credentials: { auth_mode: 'chatgpt' } },
        { id: 22, name: 'shadow-oauth', platform: 'openai', type: 'oauth', parent_account_id: 21 },
        { id: 23, name: 'synthetic-oauth', platform: 'openai', type: 'oauth', parent_account_id: null, extra: { synthetic_ui_test: true } },
        { id: 24, name: 'agent-oauth', platform: 'openai', type: 'oauth', parent_account_id: null, credentials: { auth_mode: ' AgentIdentity ' } }
      ]
    })
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="open-add"]').trigger('click')
    await flushPromises()
    for (const [selector, value] of [['add-model', 'gpt-5'], ['add-effort', 'high']]) {
      const select = document.body.querySelector<HTMLSelectElement>(`[data-testid="${selector}"]`)!
      select.value = value
      select.dispatchEvent(new Event('change'))
    }
    for (const box of document.body.querySelectorAll<HTMLInputElement>('[data-testid="add-account"]')) {
      box.checked = true
      box.dispatchEvent(new Event('change'))
      await flushPromises()
    }
    document.body.querySelector<HTMLButtonElement>('[data-testid="add-submit"]')!.click()
    await flushPromises()

    const probeDisabled = async (name: string) => {
      await wrapper.findAll('[data-testid="target"]').find(target => target.text().includes(name))!.trigger('click')
      await flushPromises()
      return wrapper.get('[data-testid="run-state_probe"]').attributes('disabled') !== undefined
    }
    // The target tests high effort; State Probe still runs on the account default.
    expect(await probeDisabled('direct-oauth')).toBe(false)
    expect(await probeDisabled('shadow-oauth')).toBe(true)
    expect(await probeDisabled('synthetic-oauth')).toBe(true)
    expect(await probeDisabled('agent-oauth')).toBe(true)
    expect(wrapper.get('[data-testid="test-state_probe"]').text()).toContain('只支持直连 OpenAI OAuth 账号')

    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    const payload = api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig
    expect(payload.accounts.map(route => `${route.account_id}:${route.reasoning_effort}`)).toEqual(['12:high', '21:high', '22:high', '23:high', '24:high'])
    expect(payload.accounts.every(route => !('direct_oauth_eligible' in route))).toBe(true)
    wrapper.unmount()
  })
})

describe('ModelIntegrityTestsView target editing', () => {
  const bpsAccounts = [{ account_id: 11, probe_model: 'gpt-5', mode: 'auto' as const, failure_threshold: 4, recovery_threshold: 3, interval_seconds: 1800 }]

  function editableConfig(): OpenAIEvalConfig {
    const config = serverConfig()
    config.bps_auto_enabled = true
    config.bps_accounts = bpsAccounts.map(item => ({ ...item }))
    config.accounts.push({
      account_id: 11,
      requested_model: 'gpt-5',
      reasoning_effort: '',
      candy_schedule: { ...schedule(1800, true), sample_count: 3 },
      fingerprint_schedule: { ...schedule(43200, true), sample_mode: 'strict' },
      modeltrace_schedule: { ...schedule(21600, true), jitter_seconds: 600 },
      state_probe_schedule: schedule(3600, true),
      bps_auto: false,
      bps_mode: 'force_off',
      direct_oauth_eligible: true
    })
    return config
  }

  // A closed dialog stays in the DOM while its leave transition runs, so only
  // look inside the dialog that is actually open.
  const $ = <T extends Element>(selector: string) => document.body.querySelector<T>(`.modal-overlay:not([class*="modal-leave"]) ${selector}`)
  const editOpen = () => Boolean($('[data-testid="edit-model"]'))
  async function choose(selector: string, value: string) {
    const select = $<HTMLSelectElement>(selector)!
    select.value = value
    select.dispatchEvent(new Event('change'))
    await flushPromises()
  }
  async function selectTarget(wrapper: ReturnType<typeof mountView>, index: number) {
    await wrapper.findAll('[data-testid="target"]')[index].trigger('click')
    await flushPromises()
  }
  async function openEdit(wrapper: ReturnType<typeof mountView>) {
    await wrapper.get('[data-testid="edit-target"]').trigger('click')
    await flushPromises()
  }
  async function apply() {
    $<HTMLButtonElement>('[data-testid="edit-submit"]')!.click()
    await flushPromises()
  }

  beforeEach(() => {
    api.getOpenAIEvalConfig.mockResolvedValue(editableConfig())
  })

  it('changes model and explicit effort in place and saves them with schedules, BPS and order intact', async () => {
    const wrapper = mountView()
    await flushPromises()
    const before = editableConfig()

    await openEdit(wrapper)
    expect($('[data-testid="edit-account"]')?.textContent).toContain('apikey-b #12')
    expect($<HTMLSelectElement>('[data-testid="edit-model"]')!.value).toBe('gpt-5')
    expect($<HTMLSelectElement>('[data-testid="edit-effort"]')!.value).toBe('high')
    // Nothing changed yet, so there is nothing to apply.
    expect($<HTMLButtonElement>('[data-testid="edit-submit"]')!.disabled).toBe(true)

    await choose('[data-testid="edit-model"]', 'gpt-5.1')
    await choose('[data-testid="edit-effort"]', 'high')
    await apply()

    expect(editOpen()).toBe(false)
    expect(wrapper.get('[data-testid="target"][aria-current="true"]').text()).toContain('gpt-5.1 · high')
    expect(wrapper.text()).toContain('有未保存的更改')
    expect(api.saveOpenAIEvalConfig).not.toHaveBeenCalled()

    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    const payload = api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig
    expect(payload.revision).toBe(9)
    expect(payload.accounts.map(route => `${route.account_id}:${route.requested_model}:${route.reasoning_effort}`)).toEqual(['12:gpt-5.1:high', '11:gpt-5:'])
    const edited = payload.accounts[0]
    expect(edited.candy_schedule).toEqual(before.accounts[0].candy_schedule)
    expect(edited.fingerprint_schedule).toEqual(before.accounts[0].fingerprint_schedule)
    expect(edited.modeltrace_schedule).toEqual(before.accounts[0].modeltrace_schedule)
    expect(edited.state_probe_schedule).toEqual(before.accounts[0].state_probe_schedule)
    expect(edited.bps_mode).toBe('force_off')
    expect(payload.bps_auto_enabled).toBe(false)
    expect(payload.bps_accounts).toEqual([])
    // A legacy rule without the switch is sent back explicitly enabled, which the server reads the same way.
    expect(payload.policies).toEqual([{ requested_model: 'gpt-5', reasoning_effort: 'high', policy: 'cost_first', enabled: true }])
    wrapper.unmount()
  })

  it('switches an explicit effort back to the default effort and back again', async () => {
    const wrapper = mountView()
    await flushPromises()

    await openEdit(wrapper)
    await choose('[data-testid="edit-effort"]', '')
    await apply()
    expect(wrapper.get('[data-testid="target"][aria-current="true"]').text()).toContain('gpt-5 · 默认强度')

    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    expect((api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig).accounts[0].reasoning_effort).toBe('')
    wrapper.unmount()
  })

  it('sends the configured scheduling thresholds back unchanged with a test-only edit', async () => {
    const thresholds = {
      cost_first: { error_rate: 0, ttft_seconds: 12.5 },
      stability_first: { error_rate: 0.025, ttft_seconds: 6 },
      avoid_degradation: { error_rate: 0.3, ttft_seconds: 20 },
      custom_balance: { error_rate: 0.07, ttft_seconds: 30 },
      min_error_samples: 50,
      min_ttft_samples: 100
    }
    // Account rule conditions are edited on the scheduling page and must survive this page's save.
    const accountRules = [{ account_id: 1, priority: 1, enabled: true, condition: { metric: 'error_rate' as const, operator: 'lt' as const, threshold: 0.05 } }]
    api.getOpenAIEvalConfig.mockResolvedValue({ ...editableConfig(), scheduling_thresholds: thresholds, account_priority_rules: accountRules })
    const wrapper = mountView()
    await flushPromises()

    await openEdit(wrapper)
    await choose('[data-testid="edit-effort"]', '')
    await apply()
    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    expect((api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig).scheduling_thresholds).toEqual(thresholds)
    expect((api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig).account_priority_rules).toEqual(accountRules)
    wrapper.unmount()
  })

  it('keeps configured rule conditions through a tests-page save, server merge and reload', async () => {
    const server = evalConfigServer({
      ...editableConfig(),
      account_priority_rules: [
        { account_id: 11, priority: 1, enabled: true, condition: { metric: 'ttft_ms', operator: 'lte', threshold: 2500 } },
        { account_id: 12, priority: 2, requested_models: ['gpt-5'], enabled: false, condition: { metric: 'price', operator: 'lt', threshold: 0.5 } },
        { account_id: 13, priority: 3, enabled: true }
      ]
    })
    api.getOpenAIEvalConfig.mockImplementation(server.read)
    api.saveOpenAIEvalConfig.mockImplementation(server.save)
    const wrapper = mountView()
    await flushPromises()

    await openEdit(wrapper)
    await choose('[data-testid="edit-effort"]', '')
    await apply()
    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()

    // Every rule states its condition on the wire, so the server never has to guess.
    const sent = (api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig).account_priority_rules!
    expect(sent.map(rule => rule.condition)).toEqual([
      { metric: 'ttft_ms', operator: 'lte', threshold: 2500 },
      { metric: 'price', operator: 'lt', threshold: 0.5 },
      null
    ])
    const expected = [
      { account_id: 11, priority: 1, enabled: true, condition: { metric: 'ttft_ms', operator: 'lte', threshold: 2500 } },
      { account_id: 12, priority: 2, requested_models: ['gpt-5'], enabled: false, condition: { metric: 'price', operator: 'lt', threshold: 0.5 } },
      { account_id: 13, priority: 3, enabled: true }
    ]
    expect(server.stored().account_priority_rules).toEqual(expected)
    wrapper.unmount()

    // A second, different tests-page edit after reopening sends the re-read rules back unchanged.
    const reopened = mountView()
    await flushPromises()
    await reopened.findAll('[data-testid="target"]')[1].trigger('click')
    await reopened.get('[data-testid="edit-target"]').trigger('click')
    await flushPromises()
    expect($<HTMLSelectElement>('[data-testid="edit-effort"]')!.value).toBe('')
    await choose('[data-testid="edit-effort"]', 'high')
    expect($<HTMLButtonElement>('[data-testid="edit-submit"]')!.disabled).toBe(false)
    const formIDs = [...document.querySelectorAll('form[id^="edit-target-form-"]')].map(form => form.id)
    expect(new Set(formIDs).size).toBe(formIDs.length)
    expect($<HTMLButtonElement>('[data-testid="edit-submit"]')!.form).toBe($<HTMLFormElement>('form[id^="edit-target-form-"]'))
    await apply()
    expect(reopened.get('[data-testid="target"][aria-current="true"]').text()).toContain('gpt-5 · high')
    expect(reopened.get('[data-testid="model-integrity-save"]').attributes('disabled')).toBeUndefined()
    await reopened.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    expect(api.saveOpenAIEvalConfig).toHaveBeenCalledTimes(2)
    expect(server.stored().account_priority_rules).toEqual(expected)
    reopened.unmount()
  })

  it('keeps the State Probe schedule and eligibility when a direct OAuth target moves to an explicit effort', async () => {
    const wrapper = mountView()
    await flushPromises()
    await selectTarget(wrapper, 1)
    expect(wrapper.get('[data-testid="run-state_probe"]').attributes('disabled')).toBeUndefined()

    await openEdit(wrapper)
    expect($('[data-testid="edit-state-probe"]')).toBeNull()
    await choose('[data-testid="edit-effort"]', 'high')
    // Explains the default-effort behaviour instead of claiming OAuth is unsupported.
    expect($('[data-testid="edit-state-probe"]')?.textContent).toContain('状态探针会继续按账号的默认推理强度运行')
    expect($('[data-testid="edit-state-probe"]')?.textContent).not.toContain('只支持')
    await apply()
    expect(wrapper.get('[data-testid="run-state_probe"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.get('[data-testid="state-probe-effort"]').text()).toContain('按账号的默认推理强度运行')
    expect(wrapper.get('[data-testid="test-state_probe"]').text()).not.toContain('只支持直连')

    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    const payload = api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig
    const before = editableConfig().accounts[1]
    const edited = payload.accounts[1]
    expect(edited).toMatchObject({ account_id: 11, requested_model: 'gpt-5', reasoning_effort: 'high', bps_mode: 'force_off' })
    expect(edited.state_probe_schedule).toEqual(before.state_probe_schedule)
    expect(edited.candy_schedule).toEqual(before.candy_schedule)
    expect(edited.fingerprint_schedule).toEqual(before.fingerprint_schedule)
    expect(edited.modeltrace_schedule).toEqual(before.modeltrace_schedule)
    expect(payload.bps_accounts).toEqual([])
    wrapper.unmount()
  })

  it('edits a high-effort OAuth target with legacy route BPS without losing schedules or BPS', async () => {
    const config = editableConfig()
    config.accounts[1].reasoning_effort = 'high'
    config.accounts[1].bps_mode = 'auto'
    config.accounts[1].bps_auto = true
    api.getOpenAIEvalConfig.mockResolvedValue(config)
    const wrapper = mountView()
    await flushPromises()
    await selectTarget(wrapper, 1)
    expect(wrapper.get('[data-testid="run-state_probe"]').attributes('disabled')).toBeUndefined()

    await openEdit(wrapper)
    expect($('[data-testid="edit-bps-locked"]')).toBeNull()
    expect($<HTMLOptionElement>('[data-testid="edit-effort"] option[value="high"]')!.disabled).toBe(false)
    expect($<HTMLOptionElement>('[data-testid="edit-effort"] option[value=""]')!.disabled).toBe(false)
    await choose('[data-testid="edit-model"]', 'gpt-5.1')
    expect($<HTMLButtonElement>('[data-testid="edit-submit"]')!.disabled).toBe(false)
    await apply()

    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    const payload = api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig
    const before = config.accounts[1]
    const edited = payload.accounts[1]
    expect(edited).toMatchObject({ account_id: 11, requested_model: 'gpt-5.1', reasoning_effort: 'high', bps_mode: 'force_off', bps_auto: false })
    expect(edited.state_probe_schedule).toEqual(before.state_probe_schedule)
    expect(edited.candy_schedule).toEqual(before.candy_schedule)
    expect(edited.fingerprint_schedule).toEqual(before.fingerprint_schedule)
    expect(edited.modeltrace_schedule).toEqual(before.modeltrace_schedule)
    expect(payload.bps_accounts).toEqual([])
    wrapper.unmount()
  })

  it('keeps a target with route-level BPS editable on the default effort', async () => {
    const config = editableConfig()
    config.accounts[1].bps_mode = 'auto'
    api.getOpenAIEvalConfig.mockResolvedValue(config)
    const wrapper = mountView()
    await flushPromises()
    await selectTarget(wrapper, 1)

    await openEdit(wrapper)
    await choose('[data-testid="edit-model"]', 'gpt-5.1')
    await apply()

    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    const edited = (api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig).accounts[1]
    expect(edited).toMatchObject({ requested_model: 'gpt-5.1', reasoning_effort: '', bps_mode: 'force_off' })
    expect(edited.state_probe_schedule.enabled).toBe(true)
    wrapper.unmount()
  })

  it('rejects a combination the account already tests', async () => {
    const config = editableConfig()
    config.accounts.push({ ...config.accounts[0], requested_model: 'GPT-5.1', reasoning_effort: 'high' })
    api.getOpenAIEvalConfig.mockResolvedValue(config)
    const wrapper = mountView()
    await flushPromises()

    await openEdit(wrapper)
    await choose('[data-testid="edit-model"]', 'gpt-5.1')
    expect($('[data-testid="edit-duplicate"]')?.textContent).toContain('该账号已有 gpt-5.1 · high 的测试对象')
    expect($<HTMLButtonElement>('[data-testid="edit-submit"]')!.disabled).toBe(true)
    await apply()
    expect(editOpen()).toBe(true)

    // Another account may use the same model and effort.
    await choose('[data-testid="edit-model"]', 'gpt-5')
    await choose('[data-testid="edit-effort"]', '')
    expect($('[data-testid="edit-duplicate"]')).toBeNull()
    wrapper.unmount()
  })

  it('leaves the target untouched when the edit is cancelled', async () => {
    const wrapper = mountView()
    await flushPromises()

    await openEdit(wrapper)
    await choose('[data-testid="edit-model"]', 'gpt-5.1')
    await choose('[data-testid="edit-effort"]', '')
    $<HTMLButtonElement>('[data-testid="edit-cancel"]')!.click()
    await flushPromises()

    expect(editOpen()).toBe(false)
    expect(wrapper.get('[data-testid="target"][aria-current="true"]').text()).toContain('gpt-5 · high')
    expect(wrapper.text()).not.toContain('有未保存的更改')

    // Reopening starts from the target's current values, not the abandoned draft.
    await openEdit(wrapper)
    expect($<HTMLSelectElement>('[data-testid="edit-model"]')!.value).toBe('gpt-5')
    expect($<HTMLSelectElement>('[data-testid="edit-effort"]')!.value).toBe('high')
    wrapper.unmount()
  })

  it('clears the unsaved state when an edit is reverted to the saved values', async () => {
    const wrapper = mountView()
    await flushPromises()

    await openEdit(wrapper)
    await choose('[data-testid="edit-model"]', 'gpt-5.1')
    await apply()
    expect(wrapper.text()).toContain('有未保存的更改')
    await openEdit(wrapper)
    await choose('[data-testid="edit-model"]', 'gpt-5')
    await apply()
    expect(wrapper.text()).not.toContain('有未保存的更改')
    wrapper.unmount()
  })

  it('shows history for the new identity only and ignores a late reply for the old one', async () => {
    const oldRun = {
      id: 1, account_id: 12, test_type: 'candy', requested_model: 'gpt-5', reasoning_effort: 'high', status: 'warning',
      outcome: { status: 'warning', reason: 'one_or_more_public_candy_variants_failed', sample_count: 5, expected_count: 5, confidence: 'low', scheduling: 'alert_only' },
      request_count: 5, input_tokens: 1, output_tokens: 1, duration_ms: 1, started_at: '2026-10-01T07:00:00Z', finished_at: '2026-10-01T07:00:03Z', trigger_source: 'scheduled'
    }
    const lateOld: Array<(value: unknown) => void> = []
    api.listOpenAIEvalRuns.mockImplementation(async (params: { requested_model?: string }) => {
      if (params.requested_model === 'gpt-5') return new Promise(resolve => { lateOld.push(resolve) })
      return { items: params.requested_model ? [] : [oldRun] }
    })
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.get('[data-testid="test-candy"]').get('[data-testid="latest-status"]').text()).toBe('异常')

    await openEdit(wrapper)
    await choose('[data-testid="edit-model"]', 'gpt-5.1')
    await apply()

    expect(api.listOpenAIEvalRuns).toHaveBeenLastCalledWith({ account_id: 12, requested_model: 'gpt-5.1', limit: 50 })
    // The old target's pending narrow query resolves after the edit.
    lateOld.forEach(resolve => resolve({ items: [oldRun] }))
    await flushPromises()
    expect(wrapper.get('[data-testid="test-candy"]').find('[data-testid="latest-status"]').exists()).toBe(false)
    const scopeTarget = wrapper.findAll('.scope-btn')[1]
    await scopeTarget.trigger('click')
    expect(wrapper.findAll('[data-testid="history-row"]')).toHaveLength(0)
    // The run itself still names the model it was made with.
    await wrapper.findAll('.scope-btn')[0].trigger('click')
    expect(wrapper.get('[data-testid="history-row"]').text()).toContain('gpt-5 · high')
    wrapper.unmount()
  })

  it('lets a run that started before the edit finish on its original model', async () => {
    let finish!: (value: unknown) => void
    api.runOpenAIEval.mockImplementation(() => new Promise(resolve => { finish = resolve }))
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    try {
      const wrapper = mountView()
      await flushPromises()

      await wrapper.get('[data-testid="run-candy"]').trigger('click')
      await flushPromises()
      expect(api.runOpenAIEval).toHaveBeenCalledWith({ account_id: 12, requested_model: 'gpt-5', reasoning_effort: 'high', test_type: 'candy', sample_count: 5, max_attempts: 3 })
      expect(wrapper.get('[data-testid="run-candy"]').attributes('disabled')).toBeDefined()

      await openEdit(wrapper)
      await choose('[data-testid="edit-model"]', 'gpt-5.1')
      await apply()
      // The edited target is a new identity with no run in flight.
      expect(wrapper.get('[data-testid="run-candy"]').attributes('disabled')).toBeUndefined()

      api.listOpenAIEvalRuns.mockClear()
      vi.advanceTimersByTime(1000)
      await flushPromises()
      const polls = api.listOpenAIEvalRuns.mock.calls.map(call => call[0]).filter(params => params.test_type === 'candy')
      expect(polls).toEqual([{ account_id: 12, requested_model: 'gpt-5', reasoning_effort: 'high', test_type: 'candy', limit: 5 }])

      finish({
        id: 3, account_id: 12, test_type: 'candy', requested_model: 'gpt-5', reasoning_effort: 'high', status: 'pass',
        outcome: { status: 'pass', reason: 'all_public_candy_variants_passed', sample_count: 5, expected_count: 5, confidence: 'low', scheduling: 'alert_only' },
        request_count: 5, input_tokens: 1, output_tokens: 1, duration_ms: 900, started_at: '2026-10-01T08:00:00Z', trigger_source: 'manual'
      })
      await flushPromises()
      expect(wrapper.get('[data-testid="target"][aria-current="true"]').text()).toContain('gpt-5.1 · high')

      await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
      await flushPromises()
      const payload = api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig
      expect(payload.accounts[0]).toMatchObject({ account_id: 12, requested_model: 'gpt-5.1', reasoning_effort: 'high' })
      wrapper.unmount()
    } finally {
      vi.useRealTimers()
    }
  })
})

describe('ModelIntegrityTestsView retries and per-sample diagnostics', () => {
  const versionedCatalog = { ...catalog, data_version: 'sub2api-candy-21-v3-cpa', candy: { ...catalog.candy, expected_answer: 21 } }
  const candyRun = (id: number, extra: Record<string, unknown>) => ({
    id, account_id: 12, test_type: 'candy', requested_model: 'gpt-5', reasoning_effort: 'high', status: 'pass',
    outcome: { status: 'pass', reason: 'all_public_candy_variants_passed', sample_count: 1, expected_count: 1, confidence: 'low', scheduling: 'alert_only' },
    request_count: 1, input_tokens: 1, output_tokens: 1, duration_ms: 900,
    started_at: `2026-10-02T0${id}:00:00Z`, finished_at: `2026-10-02T0${id}:00:05Z`, trigger_source: 'manual', ...extra
  })
  const $ = <T extends Element>(selector: string) => document.body.querySelector<T>(`.modal-overlay:not([class*="modal-leave"]) ${selector}`)
  const $$ = (selector: string) => [...document.body.querySelectorAll(`.modal-overlay:not([class*="modal-leave"]) ${selector}`)]
  async function openLatest(wrapper: ReturnType<typeof mountView>, type = 'candy') {
    await wrapper.get(`[data-testid="test-${type}"] .tt-result-btn`).trigger('click')
    await flushPromises()
  }

  it('defaults a legacy config to 3 attempts, saves the edited value and clamps invalid input', async () => {
    const wrapper = mountView()
    await flushPromises()
    const input = wrapper.get<HTMLInputElement>('[data-testid="max-attempts"]')
    expect(input.element.value).toBe('3')
    // Candy 5 samples × 3 attempts; State Probe 3 chains of 2 linked requests.
    expect(wrapper.get('[data-testid="test-candy"] [data-testid="per-run"]').text()).toBe('每次 5 次请求，失败重试时最多 15 次')
    expect(wrapper.get('[data-testid="budget"]').text()).toContain('失败重试时最多约 360 次')
    expect(wrapper.text()).not.toContain('有未保存的更改')

    for (const [raw, expected] of [['0', '1'], ['25', '10'], ['abc', '3'], ['4.7', '4']] as const) {
      await input.setValue(raw)
      await input.trigger('change')
      expect(input.element.value).toBe(expected)
    }
    expect(wrapper.get('[data-testid="test-modeltrace"] [data-testid="per-run"]').text()).toBe('每次 3 次请求，失败重试时最多 12 次')
    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    const payload = api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig
    expect(payload.max_request_attempts).toBe(4)
    wrapper.unmount()
  })

  it('reads a saved attempts value and sends it with manual runs, State Probe included', async () => {
    const config = serverConfig()
    config.max_request_attempts = 5
    config.accounts[0].direct_oauth_eligible = true
    api.getOpenAIEvalConfig.mockResolvedValue(config)
    api.runOpenAIEval.mockResolvedValue(candyRun(9, {}))
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.get<HTMLInputElement>('[data-testid="max-attempts"]').element.value).toBe('5')
    // Five attempts would be five chains, but the server caps a probe at three.
    expect(wrapper.get('[data-testid="test-state_probe"] [data-testid="per-run"]').text()).toBe('每次 2 次请求为一条链，失败重试时最多 3 条链、共 6 次请求')

    await wrapper.get('[data-testid="run-modeltrace"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-testid="run-state_probe"]').trigger('click')
    await flushPromises()
    const calls = api.runOpenAIEval.mock.calls.map(call => call[0])
    expect(calls[0]).toEqual({ account_id: 12, requested_model: 'gpt-5', reasoning_effort: 'high', test_type: 'modeltrace', max_attempts: 5 })
    // The probe reads the same setting; the server clamps it to three chains.
    expect(calls[1]).toEqual({ account_id: 12, requested_model: 'gpt-5', reasoning_effort: 'high', test_type: 'state_probe', max_attempts: 5 })
    wrapper.unmount()
  })

  it('omits the retry budget for a probe set to a single chain', async () => {
    const config = serverConfig()
    config.max_request_attempts = 1
    config.accounts[0].direct_oauth_eligible = true
    api.getOpenAIEvalConfig.mockResolvedValue(config)
    const wrapper = mountView()
    await flushPromises()

    // One attempt is one chain, so there is no retry to announce — but the run
    // still sends its two linked requests, and the page must not claim more.
    const perRun = wrapper.get('[data-testid="test-state_probe"] [data-testid="per-run"]').text()
    expect(perRun).toBe('每次 2 次请求')
    expect(perRun).not.toContain('条链')
    expect(perRun).not.toContain('6')
    wrapper.unmount()
  })

  it('shows logical sample progress and physical upstream requests separately', async () => {
    let finish!: (value: unknown) => void
    api.runOpenAIEval.mockImplementation(() => new Promise(resolve => { finish = resolve }))
    const wrapper = mountView()
    await flushPromises()
    api.listOpenAIEvalRuns.mockResolvedValue({
      items: [{
        id: 4, account_id: 12, test_type: 'modeltrace', requested_model: 'gpt-5', reasoning_effort: 'high', status: 'running',
        outcome: { status: 'running', sample_count: 2, expected_count: 3, confidence: 'none', scheduling: 'disabled' },
        request_count: 7, completed_samples: 2, expected_samples: 3, input_tokens: 1, output_tokens: 1,
        duration_ms: 500, started_at: '2026-10-02T08:00:00Z', trigger_source: 'manual'
      }]
    })
    await wrapper.get('[data-testid="run-modeltrace"]').trigger('click')
    await flushPromises()
    const progress = wrapper.get('[data-testid="test-modeltrace"] [data-testid="test-progress"]')
    expect(progress.text()).toContain('正在采样 2/3')
    expect(progress.find('.tt-progress-value').attributes('style')).toContain('67%')
    expect(progress.get('[data-testid="test-progress-requests"]').text()).toBe('已发出 7 次上游请求')
    finish(candyRun(4, { test_type: 'modeltrace' }))
    await flushPromises()
    wrapper.unmount()
  })

  it('shows the current Candy expected answer 21 and each extracted answer below the result', async () => {
    api.getOpenAIEvalModels.mockResolvedValue(versionedCatalog)
    api.listOpenAIEvalRuns.mockResolvedValue({
      items: [candyRun(5, {
        data_version: 'sub2api-candy-21-v3-cpa', request_count: 4,
        outcome: { status: 'pass', reason: 'all_public_candy_variants_passed', sample_count: 2, expected_count: 2, confidence: 'low', scheduling: 'alert_only' },
        samples: [
          { probe_id: 'candy-21-v3-1', valid: true, answer: '最终答案：21 颗', normalized_answer: '21', attempts: 1 },
          { probe_id: 'candy-21-v3-2', valid: true, normalized_answer: '21', attempts: 3, attempt_errors: [{ attempt: 1, code: 'http_5xx', message: 'bad gateway', http_status: 502 }, { attempt: 2, code: 'timeout', message: 'upstream timeout' }] }
        ]
      })]
    })
    const wrapper = mountView()
    await flushPromises()
    const answers = wrapper.get('[data-testid="test-candy"] [data-testid="latest-answers"]').text()
    expect(answers).toContain('预期答案 21')
    const rows = wrapper.findAll('[data-testid="test-candy"] [data-testid="latest-answer-value"]').map(row => row.text())
    expect(rows).toEqual(['样本 1：提取答案 21', '样本 2：提取答案 21'])
    // Only the first sample stored a reply; it stays available but collapsed.
    const replies = wrapper.findAll('[data-testid="test-candy"] .tt-answer-reply')
    expect(replies).toHaveLength(1)
    expect((replies[0].element as HTMLDetailsElement).open).toBe(false)
    expect(replies[0].get('[data-testid="latest-answer-reply"]').text()).toBe('最终答案：21 颗')

    await openLatest(wrapper)
    expect($('[data-testid="detail-expected"]')?.textContent).toBe('21')
    expect($('[data-testid="detail-logical"]')?.textContent).toBe('2/2')
    expect($('[data-testid="detail-physical"]')?.textContent).toBe('4')
    expect($('[data-testid="detail-historical"]')).toBeNull()
    const states = $$('[data-testid="sample-state"]').map(node => node.textContent)
    expect(states).toEqual(['正确', '正确'])
    expect($$('[data-testid="sample-extracted"]').map(node => node.textContent?.trim())).toEqual(['提取答案 21', '提取答案 21'])
    expect($$('[data-testid="sample-answer"]').map(node => node.textContent)).toEqual(['最终答案：21 颗'])
    expect($$('[data-testid="sample-attempt"]').map(node => [...node.children].map(child => child.textContent).join(' '))).toEqual(['第 1 次 HTTP 502 bad gateway', '第 2 次 upstream timeout'])
    wrapper.unmount()
  })

  it('keeps 29 for historical Candy runs and never invents an answer the server did not store', async () => {
    api.getOpenAIEvalModels.mockResolvedValue(versionedCatalog)
    api.listOpenAIEvalRuns.mockResolvedValue({
      items: [
        candyRun(6, {
          data_version: 'sub2api-candy-29-v2-cpa-fingerprint-5654020c', status: 'warning',
          outcome: { status: 'warning', reason: 'one_or_more_public_candy_variants_failed', sample_count: 1, expected_count: 1, confidence: 'low', scheduling: 'alert_only' },
          samples: [{ probe_id: 'candy-29-v2-1', valid: false, error_code: 'single_public_item_failed' }]
        }),
        candyRun(5, {
          status: 'warning',
          outcome: { status: 'warning', reason: 'one_or_more_public_candy_variants_failed', sample_count: 1, expected_count: 1, confidence: 'low', scheduling: 'alert_only' }
        })
      ]
    })
    const wrapper = mountView()
    await flushPromises()
    const panel = wrapper.get('[data-testid="test-candy"]')
    expect(panel.get('[data-testid="latest-explanation"]').text()).toContain('未答出 29')
    expect(panel.text()).not.toContain('21')
    expect(panel.get('[data-testid="latest-answers"]').text()).toBe('预期答案 29')

    await openLatest(wrapper)
    expect($('[data-testid="detail-expected"]')?.textContent).toBe('29')
    expect($('[data-testid="detail-historical"]')?.textContent).toContain('旧版题目')
    expect($('[data-testid="sample-state"]')?.textContent).toBe('错误')
    expect($('[data-testid="sample-legacy"]')?.textContent).toContain('未保存回答与错误详情')
    expect($('[data-testid="sample-extracted"]')).toBeNull()
    expect($('[data-testid="sample-answer"]')).toBeNull()
    expect($('[data-testid="sample-error"]')).toBeNull()
    document.body.querySelector<HTMLButtonElement>('.modal-overlay .btn-secondary')!.click()
    await flushPromises()

    // An unversioned run on a versioned server has no knowable expected answer.
    const history = wrapper.findAll('[data-testid="history-row"]')
    expect(history[1].text()).toContain('至少 1 次回答错误')
    expect(history[1].text()).not.toMatch(/21|29/)
    await history[1].trigger('click')
    await flushPromises()
    expect($('[data-testid="detail-expected"]')).toBeNull()
    expect($('[data-testid="detail-historical"]')?.textContent).toContain('无法确定当时的预期答案')
    expect($('[data-testid="samples-none"]')?.textContent).toContain('未保存逐个样本的结果')
    wrapper.unmount()
  })

  it('names the upstream failure behind insufficient evidence and keeps it apart from errors', async () => {
    api.listOpenAIEvalRuns.mockResolvedValue({
      items: [
        candyRun(7, {
          status: 'insufficient', request_count: 5,
          outcome: { status: 'insufficient', reason: 'insufficient_valid_samples', sample_count: 1, expected_count: 2, confidence: 'none', scheduling: 'alert_only' },
          samples: [
            { probe_id: 'candy-21-v3-1', valid: true, answer: '21', attempts: 1 },
            {
              probe_id: 'candy-21-v3-2', valid: false, error_code: 'http_5xx', http_status: 503, attempts: 3,
              error_message: 'Service Unavailable: upstream overloaded, Authorization: Bearer abc.def-ghi and key sk-proj1234567890abcdef',
              attempt_errors: [
                { attempt: 1, code: 'http_5xx', message: 'overloaded', http_status: 503 },
                { attempt: 2, code: 'http_5xx', message: 'overloaded', http_status: 503 },
                { attempt: 3, code: 'http_5xx', message: 'overloaded', http_status: 503 }
              ]
            }
          ]
        }),
        candyRun(6, { test_type: 'fingerprint', status: 'error', outcome: { status: 'error', reason: 'http_401', sample_count: 0, expected_count: 0, confidence: 'none', scheduling: 'alert_only' } })
      ]
    })
    const wrapper = mountView()
    await flushPromises()
    const candy = wrapper.get('[data-testid="test-candy"]')
    expect(candy.get('[data-testid="latest-status"]').text()).toBe('证据不足')
    expect(candy.get('footer').classes()).toContain('tt-result-neutral')
    const explanation = candy.get('[data-testid="latest-explanation"]').text()
    expect(explanation).toContain('有效回答 1/2')
    expect(explanation).toContain('1 个样本请求失败：HTTP 503，Service Unavailable: upstream overloaded')
    expect(explanation).toContain('尝试 3 次')
    expect(explanation).not.toContain('abc.def-ghi')
    expect(explanation).not.toContain('sk-proj1234567890abcdef')
    const fingerprint = wrapper.get('[data-testid="test-fingerprint"]')
    expect(fingerprint.get('[data-testid="latest-status"]').text()).toBe('失败')
    expect(fingerprint.get('footer').classes()).toContain('tt-result-error')
    const statuses = wrapper.findAll('[data-testid="history-status"]')
    expect(statuses[0].classes()).toContain('tone-neutral')
    expect(statuses[1].classes()).toContain('tone-error')

    await openLatest(wrapper)
    const failed = $$('[data-testid="sample"]')[1]
    expect(failed.querySelector('details')!.open).toBe(true)
    expect(failed.querySelector('[data-testid="sample-state"]')!.textContent).toBe('请求失败')
    expect(failed.querySelector('.sample-attempts')!.textContent).toBe('尝试 3 次')
    expect(failed.textContent).toContain('HTTP 503')
    expect(failed.querySelector('[data-testid="sample-error"]')!.textContent).toContain('Bearer [redacted]')
    expect(failed.querySelector('[data-testid="sample-error"]')!.textContent).toContain('sk-[redacted]')
    expect(failed.querySelectorAll('[data-testid="sample-attempt"]')).toHaveLength(3)
    expect($$('[data-testid="sample"]')[0].querySelector('details')!.open).toBe(false)
    wrapper.unmount()
  })

  it('works with the current server catalog that has no data version: 21 for new runs, 29 for old ones', async () => {
    api.getOpenAIEvalModels.mockResolvedValue({ ...catalog, candy: { ...catalog.candy, expected_answer: 21 } })
    api.listOpenAIEvalRuns.mockResolvedValue({
      items: [
        candyRun(6, {
          data_version: 'sub2api-candy-21-v3-97623969-cpa-fingerprint-5654020c',
          samples: [{ probe_id: 'candy-21-v3-1', valid: true, answer: '21', normalized_answer: '21', attempts: 2 }]
        }),
        candyRun(5, {
          data_version: 'sub2api-candy-29-v2-cpa-fingerprint-5654020c', status: 'warning',
          outcome: { status: 'warning', reason: 'one_or_more_public_candy_variants_failed', sample_count: 1, expected_count: 1, confidence: 'low', scheduling: 'alert_only' }
        })
      ]
    })
    const wrapper = mountView()
    await flushPromises()
    const answers = wrapper.get('[data-testid="test-candy"] [data-testid="latest-answers"]')
    expect(answers.get('.tt-answers-expected').text()).toBe('预期答案 21')
    expect(answers.findAll('[data-testid="latest-answer-value"]').map(row => row.text())).toEqual(['提取答案 21'])
    expect((answers.get('.tt-answer-reply').element as HTMLDetailsElement).open).toBe(false)
    const rows = wrapper.findAll('[data-testid="history-row"]')
    expect(rows[1].text()).toContain('未答出 29')
    await rows[1].trigger('click')
    await flushPromises()
    expect($('[data-testid="detail-expected"]')?.textContent).toBe('29')
    expect($('[data-testid="detail-historical"]')?.textContent).toContain('旧版题目')
    wrapper.unmount()
  })

  it('renders model answers and upstream messages as text, never as HTML', async () => {
    const hostile = '<img src=x onerror="window.__pwned=1"><b>21</b>'
    api.listOpenAIEvalRuns.mockResolvedValue({
      items: [candyRun(8, {
        status: 'insufficient',
        outcome: { status: 'insufficient', reason: 'insufficient_valid_samples', sample_count: 1, expected_count: 2, confidence: 'none', scheduling: 'alert_only' },
        samples: [
          { probe_id: 'a', valid: true, answer: hostile, attempts: 1 },
          { probe_id: 'b', valid: false, error_code: 'upstream_error', http_status: 400, attempts: 1, error_message: '<script>alert(1)</script>' + 'x'.repeat(4000) }
        ]
      })]
    })
    const wrapper = mountView()
    await flushPromises()
    // The server extracted nothing, so the reply is the collapsed annotation, shown as text.
    expect(wrapper.findAll('[data-testid="latest-answer-value"]').map(row => row.text())).toEqual(['样本 1：未提取到答案', '样本 2：请求失败'])
    expect(wrapper.get('[data-testid="latest-answer-reply"]').text()).toBe(hostile)
    expect(wrapper.find('[data-testid="latest-answers"] img').exists()).toBe(false)
    await openLatest(wrapper)
    expect($('[data-testid="sample-extracted"]')!.textContent?.trim()).toBe('未提取到答案')
    const answer = $('[data-testid="sample-answer"]')!
    expect(answer.textContent).toBe(hostile)
    expect(answer.querySelector('img, b')).toBeNull()
    const error = $$('[data-testid="sample-error"]')[0]
    expect(error.querySelector('script')).toBeNull()
    expect(error.textContent).toContain('<script>alert(1)</script>')
    expect(error.classList.contains('sample-text')).toBe(true)
    expect((window as unknown as { __pwned?: number }).__pwned).toBeUndefined()
    wrapper.unmount()
  })

  describe('Candy answer annotations', () => {
    const longReply = `先考虑最坏情况：${'每种颜色各取若干颗，'.repeat(300)}没有给出结论。`
    const run = (samples: Record<string, unknown>[], extra: Record<string, unknown> = {}) => candyRun(9, {
      data_version: 'sub2api-candy-21-v3-cpa', status: 'warning', request_count: samples.length,
      outcome: { status: 'warning', reason: 'one_or_more_public_candy_variants_failed', sample_count: samples.length, expected_count: samples.length, confidence: 'low', scheduling: 'alert_only' },
      samples, ...extra
    })
    const values = (wrapper: ReturnType<typeof mountView>) => wrapper.findAll('[data-testid="test-candy"] [data-testid="latest-answer-value"]').map(row => row.text())

    beforeEach(() => { api.getOpenAIEvalModels.mockResolvedValue(versionedCatalog) })

    it('shows only the extracted integers, each sample on its own, with every full reply collapsed', async () => {
      api.listOpenAIEvalRuns.mockResolvedValue({
        items: [run([
          { probe_id: 'candy-21-v3-1', valid: true, error_code: 'correct_answer', http_status: 200, attempts: 1, normalized_answer: '21', answer: '红色取 9 颗、蓝色取 12 颗还不够。\n最终答案：21' },
          { probe_id: 'candy-21-v3-2', valid: true, error_code: 'single_public_item_failed', http_status: 200, attempts: 1, normalized_answer: '29', answer: '答案是 29 颗' },
          { probe_id: 'candy-21-v3-3', valid: true, error_code: 'single_public_item_failed', http_status: 200, attempts: 1, answer: longReply }
        ])]
      })
      const wrapper = mountView()
      await flushPromises()
      const panel = wrapper.get('[data-testid="test-candy"]')
      expect(panel.get('.tt-answers-expected').text()).toBe('预期答案 21')
      // 9 and 12 in the working are never offered as the answer; the stored final integer is.
      expect(values(wrapper)).toEqual(['样本 1：提取答案 21', '样本 2：提取答案 29', '样本 3：未提取到答案'])
      const replies = panel.findAll('.tt-answer-reply')
      expect(replies.map(node => (node.element as HTMLDetailsElement).open)).toEqual([false, false, false])
      // The annotation sits outside the button that opens the run details.
      expect(panel.find('.tt-result-btn details, .tt-result-btn [data-testid="latest-answers"]').exists()).toBe(false)

      const third = replies[2]
      ;(third.element as HTMLDetailsElement).open = true
      await third.get('summary').trigger('click')
      expect(third.get('summary').text()).toBe('模型完整回答')
      // Expanding shows the whole stored reply, untruncated; the box scrolls instead.
      expect(third.get('[data-testid="latest-answer-reply"]').text()).toBe(longReply)
      expect(third.get('[data-testid="latest-answer-reply"]').classes()).toContain('tt-answer-text')

      await openLatest(wrapper)
      expect($$('[data-testid="sample-state"]').map(node => node.textContent)).toEqual(['正确', '错误', '错误'])
      expect($$('[data-testid="sample-extracted"]').map(node => node.textContent?.trim())).toEqual(['提取答案 21', '提取答案 29', '未提取到答案'])
      expect($$('[data-testid="sample"]').every(row => !row.querySelector('details')!.open)).toBe(true)
      const detailReplies = $$('[data-testid="sample-reply"]') as HTMLDetailsElement[]
      expect(detailReplies.map(node => node.open)).toEqual([false, false, false])
      expect(detailReplies[2].querySelector('summary')!.textContent).toBe('模型完整回答')
      expect(detailReplies[2].querySelector('[data-testid="sample-answer"]')!.textContent).toBe(longReply)
      wrapper.unmount()
    })

    it('keeps the first failure open but the unextracted reply collapsed', async () => {
      api.listOpenAIEvalRuns.mockResolvedValue({
        items: [run([
          { probe_id: 'candy-21-v3-1', valid: false, error_code: 'http_5xx', http_status: 503, attempts: 3, error_message: 'upstream overloaded', answer: '部分回答：我认为', attempt_errors: [{ attempt: 1, code: 'http_5xx', message: 'overloaded', http_status: 503 }] },
          { probe_id: 'candy-21-v3-2', valid: true, error_code: 'single_public_item_failed', http_status: 200, attempts: 1, answer: '无法确定' }
        ])]
      })
      const wrapper = mountView()
      await flushPromises()
      expect(values(wrapper)).toEqual(['样本 1：请求失败', '样本 2：未提取到答案'])

      await openLatest(wrapper)
      const [failed, unextracted] = $$('[data-testid="sample"]')
      expect(failed.querySelector('details')!.open).toBe(true)
      expect(failed.querySelector('[data-testid="sample-error"]')!.textContent).toBe('upstream overloaded')
      expect(failed.querySelectorAll('[data-testid="sample-attempt"]')).toHaveLength(1)
      expect((failed.querySelector('[data-testid="sample-reply"]') as HTMLDetailsElement).open).toBe(false)
      expect(unextracted.querySelector('details')!.open).toBe(false)
      expect((unextracted.querySelector('[data-testid="sample-reply"]') as HTMLDetailsElement).open).toBe(false)
      wrapper.unmount()
    })

    it('uses the run\'s expected answer only for a legacy correct sample, never for an unknown reply', async () => {
      api.listOpenAIEvalRuns.mockResolvedValue({
        items: [run([
          { probe_id: 'candy-21-v3-1', valid: true, error_code: 'correct_answer', http_status: 200, attempts: 1, answer: '共需 21 颗' },
          { probe_id: 'candy-21-v3-2', valid: true, error_code: 'single_public_item_failed', http_status: 200, attempts: 1, answer: '也许是 21 颗，也许不是' }
        ])]
      })
      const wrapper = mountView()
      await flushPromises()
      expect(values(wrapper)).toEqual(['样本 1：提取答案 21', '样本 2：未提取到答案'])
      wrapper.unmount()
    })

    it('redacts secrets in the full reply and renders it as text', async () => {
      const reply = '<b>21</b> Authorization: Bearer abc.def-ghi key sk-proj1234567890abcdef'
      api.listOpenAIEvalRuns.mockResolvedValue({
        items: [run([{ probe_id: 'candy-21-v3-1', valid: true, error_code: 'single_public_item_failed', http_status: 200, attempts: 1, answer: reply }])]
      })
      const wrapper = mountView()
      await flushPromises()
      const footer = wrapper.get('[data-testid="latest-answer-reply"]')
      expect(footer.text()).toContain('<b>21</b>')
      expect(footer.text()).toContain('Bearer [redacted]')
      expect(footer.text()).toContain('sk-[redacted]')
      expect(footer.find('b').exists()).toBe(false)
      await openLatest(wrapper)
      const detail = $('[data-testid="sample-answer"]')!
      expect(detail.textContent).toContain('sk-[redacted]')
      expect(detail.querySelector('b')).toBeNull()
      expect(document.body.textContent).not.toContain('abc.def-ghi')
      expect(document.body.textContent).not.toContain('sk-proj1234567890abcdef')
      wrapper.unmount()
    })
  })
})

describe('ModelIntegrityTestsView State Probe diagnostics', () => {
  const probeRun = (probe: Record<string, unknown>, extra: Record<string, unknown> = {}) => {
    const verdict = (probe.verdict as string) ?? 'inconclusive'
    return {
      id: 20, account_id: 12, test_type: 'state_probe', requested_model: 'gpt-5', reasoning_effort: '', status: verdict,
      outcome: {
        status: verdict, reason: probe.failure, sample_count: probe.request_count ?? 0, expected_count: 2, confidence: 'low', scheduling: 'alert_only',
        state_probe: { version: 'v1', verdict, request_count: 0, new_ticket: false, latency_ms: 800, retry_policy: 'unsupported_linked_ticket_chain', ...probe }
      },
      request_count: probe.request_count ?? 0, input_tokens: 0, output_tokens: 0, duration_ms: 800,
      started_at: '2026-10-02T09:00:00Z', finished_at: '2026-10-02T09:00:01Z', trigger_source: 'manual', ...extra
    }
  }
  const mint = (extra: Record<string, unknown> = {}) => ({ probe_id: 'state-probe-mint', valid: true, attempts: 1, http_status: 200, ...extra })
  const linked = (extra: Record<string, unknown> = {}) => ({ probe_id: 'state-probe-continue', valid: true, attempts: 1, http_status: 200, ...extra })
  const $ = <T extends Element>(selector: string) => document.body.querySelector<T>(`.modal-overlay:not([class*="modal-leave"]) ${selector}`)
  const $$ = (selector: string) => [...document.body.querySelectorAll(`.modal-overlay:not([class*="modal-leave"]) ${selector}`)]
  async function openProbe(wrapper: ReturnType<typeof mountView>) {
    await wrapper.get('[data-testid="test-state_probe"] .tt-result-btn').trigger('click')
    await flushPromises()
  }

  it('names the HTTP 503 capacity failure from the outcome records instead of a bare verdict', async () => {
    // Older records keep the request records only inside outcome.state_probe.
    api.listOpenAIEvalRuns.mockResolvedValue({
      items: [probeRun({
        failure: 'upstream_error', request_count: 1, mint_status: 503,
        samples: [mint({
          valid: false, http_status: 503, error_code: 'server_is_overloaded',
          error_message: 'Service Unavailable: capacity busy, Authorization: Bearer abc.def-ghi',
          attempt_errors: [{ attempt: 1, code: 'server_is_overloaded', message: 'Service Unavailable: capacity busy', http_status: 503 }]
        })]
      })]
    })
    const wrapper = mountView()
    await flushPromises()
    const panel = wrapper.get('[data-testid="test-state_probe"]')
    expect(panel.get('[data-testid="latest-status"]').text()).toBe('证据不足')
    expect(panel.get('footer').classes()).not.toContain('tt-result-ok')
    const explanation = panel.get('[data-testid="latest-explanation"]').text()
    expect(explanation).toContain('上游返回未知错误')
    expect(explanation).toContain('首次请求失败：HTTP 503，Service Unavailable: capacity busy')
    expect(explanation).toContain('Bearer [redacted]')
    expect(explanation).not.toContain('abc.def-ghi')
    // Each record is one send of a chain that restarts on failure, so no attempt count is reported.
    expect(explanation).not.toContain('尝试')

    await openProbe(wrapper)
    expect($('[data-testid="detail-samples"]')).not.toBeNull()
    const rows = $$('[data-testid="sample"]')
    expect(rows).toHaveLength(1)
    expect(rows[0].querySelector('.sample-index')!.textContent).toBe('首次请求')
    expect(rows[0].querySelector('[data-testid="sample-state"]')!.textContent).toBe('请求失败')
    expect(rows[0].querySelector('details')!.open).toBe(true)
    expect(rows[0].querySelector('.sample-brief')!.textContent).toBe('HTTP 503')
    expect(rows[0].querySelector('[data-testid="sample-error"]')!.textContent).toContain('Bearer [redacted]')
    expect(rows[0].textContent).not.toContain('abc.def-ghi')
    expect(rows[0].querySelector('.sample-attempts')).toBeNull()
    expect(rows[0].querySelector('[data-testid="sample-attempt"]')).toBeNull()
    const metric = $$('p').find(node => node.textContent?.includes('两次请求状态码'))!.textContent!
    expect(metric).toContain('503 / —')
    expect(metric).toContain('未能根据票据判断线路是否切换')
    expect(metric).not.toContain('票据未显示切换')
    wrapper.unmount()
  })

  it('shows a stream that ended without a terminal event as a failed linked request, not as HTTP 200', async () => {
    api.listOpenAIEvalRuns.mockResolvedValue({
      items: [probeRun({ failure: 'missing_terminal', request_count: 2, mint_status: 200, continue_status: 200 }, {
        samples: [
          mint(),
          linked({ valid: false, error_code: 'missing_terminal', error_message: 'premature EOF before response.completed', attempt_errors: [{ attempt: 1, code: 'missing_terminal', message: 'premature EOF before response.completed', http_status: 200 }] })
        ]
      })]
    })
    const wrapper = mountView()
    await flushPromises()
    const explanation = wrapper.get('[data-testid="test-state_probe"] [data-testid="latest-explanation"]').text()
    expect(explanation).toContain('上游响应流在完成前中断')
    expect(explanation).toContain('关联请求失败：premature EOF before response.completed')
    expect(explanation).not.toContain('HTTP 200')

    await openProbe(wrapper)
    const rows = $$('[data-testid="sample"]')
    expect(rows.map(row => row.querySelector('.sample-index')!.textContent)).toEqual(['首次请求', '关联请求'])
    expect(rows.map(row => row.querySelector('[data-testid="sample-state"]')!.textContent)).toEqual(['已完成', '请求失败'])
    expect(rows[0].querySelector('.sample-brief')!.textContent).toBe('HTTP 200')
    expect(rows[0].querySelector('[data-testid="sample-completed"]')).not.toBeNull()
    expect(rows[1].querySelector('details')!.open).toBe(true)
    expect(rows[1].querySelector('.sample-brief')!.textContent).not.toBe('HTTP 200')
    expect(rows[1].querySelector('[data-testid="sample-error"]')!.textContent).toBe('premature EOF before response.completed')
    expect($('[data-testid="detail-status"]')!.classList.contains('tone-ok')).toBe(false)
    wrapper.unmount()
  })

  it('names each chain of a retried probe and reports the last chain’s failure', async () => {
    // Retrying servers number every chain; the verdict belongs to the last one sent.
    api.listOpenAIEvalRuns.mockResolvedValue({
      items: [probeRun({ failure: 'missing_terminal', request_count: 3, mint_status: 200, continue_status: 200, attempts: 2, max_attempts: 3, retry_policy: 'fresh_linked_ticket_chain' }, {
        samples: [
          mint({ probe_id: 'state-probe-1-mint', valid: false, http_status: 503, error_code: 'server_is_overloaded', error_message: 'capacity busy' }),
          mint({ probe_id: 'state-probe-2-mint' }),
          linked({ probe_id: 'state-probe-2-continue', valid: false, error_code: 'missing_terminal', error_message: 'premature EOF before response.completed' })
        ]
      })]
    })
    const wrapper = mountView()
    await flushPromises()
    const explanation = wrapper.get('[data-testid="test-state_probe"] [data-testid="latest-explanation"]').text()
    expect(explanation).toContain('第 2 条链的关联请求失败：premature EOF before response.completed')
    expect(explanation).not.toContain('capacity busy')

    await openProbe(wrapper)
    const rows = $$('[data-testid="sample"]')
    expect(rows.map(row => row.querySelector('.sample-index')!.textContent)).toEqual(['第 1 条链的首次请求', '第 2 条链的首次请求', '第 2 条链的关联请求'])
    const metric = $$('p').find(node => node.textContent?.includes('共发送'))!.textContent!
    expect(metric).toContain('共发送 2 条链，最后一条链的状态码 200 / 200')
    wrapper.unmount()
  })

  it('keeps plain request names for a single numbered chain', async () => {
    api.listOpenAIEvalRuns.mockResolvedValue({
      items: [probeRun({ verdict: 'healthy', request_count: 2, mint_status: 200, continue_status: 200, attempts: 1, max_attempts: 3 }, {
        samples: [mint({ probe_id: 'state-probe-1-mint' }), linked({ probe_id: 'state-probe-1-continue' })]
      })]
    })
    const wrapper = mountView()
    await flushPromises()
    await openProbe(wrapper)
    expect($$('[data-testid="sample"]').map(row => row.querySelector('.sample-index')!.textContent)).toEqual(['首次请求', '关联请求'])
    expect($$('p').some(node => node.textContent?.includes('两次请求状态码 200 / 200'))).toBe(true)
    wrapper.unmount()
  })

  it('explains an unusable OAuth credential as zero requests sent and redacts the message', async () => {
    api.listOpenAIEvalRuns.mockResolvedValue({
      items: [probeRun({ failure: 'credential_unavailable', request_count: 0 }, {
        samples: [mint({ valid: false, attempts: 0, http_status: undefined, error_code: 'credential_unavailable', error_message: 'OAuth access token is unavailable: refresh failed for sess-abcdefgh12345678' })]
      })]
    })
    const wrapper = mountView()
    await flushPromises()
    const explanation = wrapper.get('[data-testid="test-state_probe"] [data-testid="latest-explanation"]').text()
    expect(explanation).toContain('OAuth 凭据不可用')
    expect(explanation).toContain('未发送请求：OAuth access token is unavailable: refresh failed for sess-[redacted]')
    expect(explanation).not.toContain('sess-abcdefgh12345678')

    await openProbe(wrapper)
    expect($('[data-testid="detail-physical"]')!.textContent).toBe('0')
    const row = $$('[data-testid="sample"]')[0]
    expect(row.querySelector('[data-testid="sample-state"]')!.textContent).toBe('请求失败')
    expect(row.querySelector('[data-testid="sample-not-sent"]')!.textContent).toBe('未发送')
    expect(row.querySelector('[data-testid="sample-error"]')!.textContent).toContain('sess-[redacted]')
    expect(document.body.textContent).not.toContain('sess-abcdefgh12345678')
    wrapper.unmount()
  })

  it('shows a successful pre-v2 probe without inventing an error or a ticket observation', async () => {
    api.listOpenAIEvalRuns.mockResolvedValue({
      items: [probeRun({ verdict: 'healthy', request_count: 2, mint_status: 200, continue_status: 200 }, { samples: [mint(), linked()] })]
    })
    const wrapper = mountView()
    await flushPromises()
    const panel = wrapper.get('[data-testid="test-state_probe"]')
    expect(panel.get('[data-testid="latest-status"]').text()).toBe('正常')
    // No ticket lengths were recorded, so neither absence nor sameness is claimed.
    expect(panel.get('[data-testid="latest-explanation"]').text()).toBe('关联请求已完成，票据未显示线路切换。')

    await openProbe(wrapper)
    const rows = $$('[data-testid="sample"]')
    expect(rows.map(row => row.querySelector('[data-testid="sample-state"]')!.textContent)).toEqual(['已完成', '已完成'])
    expect(rows.every(row => !row.querySelector('details')!.open)).toBe(true)
    expect($('[data-testid="sample-error"]')).toBeNull()
    expect($('[data-testid="sample-answer"]')).toBeNull()
    expect($$('[data-testid="sample-completed"]')).toHaveLength(2)
    const metric = $$('p').find(node => node.textContent?.includes('两次请求状态码'))!.textContent!
    expect(metric).toContain('200 / 200，票据未显示切换')
    expect(metric).not.toContain('未签发新票据')
    expect(metric).not.toContain('相同票据')
    expect(document.body.textContent).not.toContain('同一线路')
    wrapper.unmount()
  })

  describe('codex-turn-state-v2 ticket observations', () => {
    const v2 = (probe: Record<string, unknown>, extra: Record<string, unknown> = {}) =>
      probeRun({ version: 'codex-turn-state-v2', retry_policy: 'fresh_linked_ticket_chain', attempts: 1, max_attempts: 3, ...probe }, extra)
    const metricText = () => $$('p').find(node => node.textContent?.includes('两次请求状态码'))!.textContent!

    it.each([
      ['none', { verdict: 'healthy', ticket_length: 36 }, '正常', '关联请求已完成，未签发新票据。', '未签发新票据'],
      ['same', { verdict: 'healthy', ticket_length: 36, continue_ticket_length: 36 }, '正常', '关联请求已完成，返回相同票据。', '返回相同票据'],
      ['different', { verdict: 'degraded', ticket_length: 36, continue_ticket_length: 40, new_ticket: true }, '异常', '关联请求返回不同票据', '返回不同票据']
    ])('reports a %s ticket from the linked request', async (_, probe, status, explanation, metric) => {
      api.listOpenAIEvalRuns.mockResolvedValue({
        items: [v2({ request_count: 2, mint_status: 200, continue_status: 200, ...probe }, { samples: [mint({ probe_id: 'state-probe-1-mint' }), linked({ probe_id: 'state-probe-1-continue' })] })]
      })
      const wrapper = mountView()
      await flushPromises()
      const panel = wrapper.get('[data-testid="test-state_probe"]')
      expect(panel.get('[data-testid="latest-status"]').text()).toBe(status)
      expect(panel.get('[data-testid="latest-explanation"]').text()).toContain(explanation)
      expect(panel.get('[data-testid="latest-explanation"]').text()).not.toContain('同一线路')

      await openProbe(wrapper)
      expect(metricText()).toContain(`200 / 200，${metric}`)
      expect($$('[data-testid="sample-state"]').map(node => node.textContent)).toEqual(['已完成', '已完成'])
      expect($('[data-testid="sample-error"]')).toBeNull()
      wrapper.unmount()
    })

    it('keeps a completed first request without a ticket as inconclusive, not as a failed request', async () => {
      api.listOpenAIEvalRuns.mockResolvedValue({
        items: [v2({ failure: 'missing_ticket', last_failure_step: 'mint', request_count: 1, mint_status: 200 }, {
          samples: [mint({ probe_id: 'state-probe-1-mint', valid: false, error_code: 'missing_ticket', error_message: 'missing ticket', attempt_errors: [{ attempt: 1, code: 'missing_ticket', message: 'missing ticket', http_status: 200 }] })]
        })]
      })
      const wrapper = mountView()
      await flushPromises()
      const panel = wrapper.get('[data-testid="test-state_probe"]')
      expect(panel.get('[data-testid="latest-status"]').text()).toBe('证据不足')
      const explanation = panel.get('[data-testid="latest-explanation"]').text()
      expect(explanation).toBe('请求已完成，但未取得首次票据，无法继续判定。')
      expect(explanation).not.toContain('失败')

      await openProbe(wrapper)
      const row = $$('[data-testid="sample"]')[0]
      expect(row.querySelector('.sample-index')!.textContent).toBe('首次请求')
      expect(row.querySelector('[data-testid="sample-state"]')!.textContent).toBe('已完成')
      expect(row.querySelector('.sample-brief')!.textContent).toBe('HTTP 200')
      expect(row.querySelector('details')!.open).toBe(false)
      expect(row.querySelector('[data-testid="sample-ticket-missing"]')).not.toBeNull()
      expect(row.querySelector('[data-testid="sample-error"]')).toBeNull()
      expect($('[data-testid="detail-status"]')!.textContent).toBe('证据不足')
      expect(metricText()).toContain('未能根据票据判断线路是否切换')
      expect(document.body.textContent).not.toContain('旧版探针结果')
      wrapper.unmount()
    })

    it('still shows an HTTP 200 stream that broke as a failed linked request', async () => {
      api.listOpenAIEvalRuns.mockResolvedValue({
        items: [v2({ failure: 'missing_terminal', last_failure_step: 'continue', request_count: 2, mint_status: 200, continue_status: 200, ticket_length: 36 }, {
          samples: [
            mint({ probe_id: 'state-probe-1-mint' }),
            linked({ probe_id: 'state-probe-1-continue', valid: false, error_code: 'missing_terminal', error_message: 'premature EOF before response.completed' })
          ]
        })]
      })
      const wrapper = mountView()
      await flushPromises()
      const explanation = wrapper.get('[data-testid="test-state_probe"] [data-testid="latest-explanation"]').text()
      expect(explanation).toContain('关联请求失败：premature EOF before response.completed')
      expect(explanation).not.toContain('旧版探针结果')

      await openProbe(wrapper)
      expect($$('[data-testid="sample-state"]').map(node => node.textContent)).toEqual(['已完成', '请求失败'])
      expect($('[data-testid="sample-ticket-missing"]')).toBeNull()
      wrapper.unmount()
    })
  })

  describe('pre-v2 missing_ticket records', () => {
    const linkedMissing = (probeId: string) => linked({ probe_id: probeId, valid: false, error_code: 'missing_ticket', error_message: 'missing ticket' })

    it.each([
      ['an absent version and last_failure_step', { version: undefined, last_failure_step: 'continue' }, 'state-probe-1'],
      ['codex-turn-state-v1 with numbered records only', { version: 'codex-turn-state-v1' }, 'state-probe-1'],
      ['an unnumbered legacy chain', { version: 'v1' }, 'state-probe']
    ])('keeps a linked missing_ticket from %s and asks for a new test', async (_, probe, prefix) => {
      const run = probeRun({ failure: 'missing_ticket', request_count: 2, mint_status: 200, continue_status: 200, ...probe }, {
        samples: [mint({ probe_id: `${prefix}-mint` }), linkedMissing(`${prefix}-continue`)]
      })
      const before = JSON.parse(JSON.stringify(run))
      api.listOpenAIEvalRuns.mockResolvedValue({ items: [run] })
      const wrapper = mountView()
      await flushPromises()
      const panel = wrapper.get('[data-testid="test-state_probe"]')
      expect(panel.get('[data-testid="latest-status"]').text()).toBe('证据不足')
      const explanation = panel.get('[data-testid="latest-explanation"]').text()
      expect(explanation).toContain('上游未返回线路票据')
      expect(explanation).toContain('旧版探针结果，请重新测试。')

      await openProbe(wrapper)
      // The stored failure is shown as recorded, not reread under v2.
      expect($$('[data-testid="sample-state"]').map(node => node.textContent)).toEqual(['已完成', '请求失败'])
      expect($('[data-testid="detail-status"]')!.textContent).toBe('证据不足')
      expect(document.body.textContent).not.toContain('未签发新票据')
      expect(run).toEqual(before)
      wrapper.unmount()
    })

    it('does not call an old first-request missing_ticket a linked-request result', async () => {
      const run = probeRun({ version: 'codex-turn-state-v1', failure: 'missing_ticket', last_failure_step: 'mint', request_count: 1, mint_status: 200 }, {
        samples: [mint({ probe_id: 'state-probe-1-mint', valid: false, error_code: 'missing_ticket', error_message: 'missing ticket' })]
      })
      const before = JSON.parse(JSON.stringify(run))
      api.listOpenAIEvalRuns.mockResolvedValue({ items: [run] })
      const wrapper = mountView()
      await flushPromises()
      const explanation = wrapper.get('[data-testid="test-state_probe"] [data-testid="latest-explanation"]').text()
      expect(explanation).toBe('请求已完成，但未取得首次票据，无法继续判定。')
      expect(explanation).not.toContain('旧版探针结果')
      expect(explanation).not.toContain('关联请求')
      expect(run).toEqual(before)
      wrapper.unmount()
    })
  })
})
