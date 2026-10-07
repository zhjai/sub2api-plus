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

function serverConfig(extra: Partial<OpenAIEvalConfig> = {}): OpenAIEvalConfig {
  return {
    revision: 4,
    effects_enabled: true,
    bps_auto_enabled: false,
    scheduling_policy: '',
    policies: [],
    accounts: [{
      account_id: 11,
      requested_model: 'gpt-5',
      reasoning_effort: 'high',
      candy_schedule: { ...schedule(3600, true), sample_count: 1 },
      fingerprint_schedule: { ...schedule(86400, true), sample_mode: 'quick' },
      modeltrace_schedule: schedule(86400),
      state_probe_schedule: schedule(21600),
      bps_auto: false,
      direct_oauth_eligible: true
    }],
    ...extra
  }
}

const accounts = [{ id: 11, name: 'oauth-a', platform: 'openai', type: 'oauth', parent_account_id: null }]

function mountView() {
  return mount(TestsView, {
    attachTo: document.body,
    global: { stubs: { AppLayout: { template: '<div><slot /></div>' }, RouterLink: RouterLinkStub } }
  })
}

const $ = <T extends Element>(selector: string) => document.body.querySelector<T>(`.modal-overlay:not([class*="modal-leave"]) ${selector}`)

beforeEach(() => {
  vi.clearAllMocks()
  document.body.innerHTML = ''
  api.getOpenAIEvalModels.mockResolvedValue(catalog)
  api.list.mockResolvedValue({ items: accounts })
  api.listOpenAIEvalRuns.mockResolvedValue({ items: [] })
})

describe('ModelIntegrityTestsView background controls', () => {
  it('says "not counted yet" instead of zero when the server reports no runtime', async () => {
    api.getOpenAIEvalConfig.mockResolvedValue(serverConfig())
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.get('[data-testid="metric-sent"]').text()).toBe('暂未统计')
    expect(wrapper.get('[data-testid="metric-reserved"]').text()).toBe('暂未统计')
    expect(wrapper.get('[data-testid="metric-active"]').text()).toBe('暂未统计')
    expect(wrapper.get('[data-testid="runtime-missing"]').text()).toContain('不代表 0 次')
    // Candy hourly (1) + fingerprint quick daily (60/24 = 2.5) = 3.5 per hour.
    expect(wrapper.get('[data-testid="metric-planned"]').text()).toContain('约 3.5 次/小时')
    expect(wrapper.get('[data-testid="auto-state"]').text()).toContain('自动测试按计划运行')
    wrapper.unmount()
  })

  it('names why live counters are unavailable and still shows them as unknown', async () => {
    api.getOpenAIEvalConfig.mockResolvedValue(serverConfig({
      background_controls: [{
        account_id: 11, paused_until: null, pause_reason: '', budget_enabled: true,
        max_requests_per_hour: 60, min_send_interval_seconds: 60, max_background_concurrency: 1, sampling_window_seconds: 900,
        runtime_unavailable_reason: 'evaluation_budget_unavailable'
      }]
    }))
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.get('[data-testid="metric-sent"]').text()).toBe('暂未统计')
    expect(wrapper.get('[data-testid="runtime-missing"]').text()).toContain('共享预算计数不可用')
    expect(wrapper.get('[data-testid="runtime-missing"]').text()).toContain('不代表 0 次')
    expect(wrapper.get('[data-testid="limits-summary"]').text()).not.toContain('剩余')
    wrapper.unmount()
  })

  it('rejects a zero send interval instead of reading it as no interval', async () => {
    api.getOpenAIEvalConfig.mockResolvedValue(serverConfig())
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="open-limits"]').trigger('click')
    await flushPromises()
    $<HTMLButtonElement>('[data-testid="limits-enabled"]')!.click()
    await flushPromises()
    const interval = $<HTMLInputElement>('[data-testid="limit-min_send_interval_seconds"]')!
    expect(interval.min).toBe('1')
    interval.value = '0'
    interval.dispatchEvent(new Event('input'))
    await flushPromises()
    expect($<HTMLButtonElement>('[data-testid="confirm-limits"]')!.disabled).toBe(true)
    wrapper.unmount()
  })

  it('shows live runtime and the deferral reason from the server', async () => {
    api.getOpenAIEvalConfig.mockResolvedValue(serverConfig({
      background_controls: [{
        account_id: 11, paused_until: null, pause_reason: '', budget_enabled: false,
        max_requests_per_hour: 60, min_send_interval_seconds: 60, max_background_concurrency: 1, sampling_window_seconds: 900,
        runtime: { sent_last_hour: 0, reserved: 3, active_runs: 1, deferred_reason: 'foreground_waiting', next_send_at: null }
      }]
    }))
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.get('[data-testid="metric-sent"]').text()).toBe('0')
    expect(wrapper.get('[data-testid="metric-reserved"]').text()).toBe('3')
    expect(wrapper.get('[data-testid="metric-active"]').text()).toBe('1')
    expect(wrapper.get('[data-testid="deferred-reason"]').text()).toContain('正常请求正在等待')
    expect(wrapper.find('[data-testid="runtime-missing"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('pauses account-wide without disabling schedules and saves the pause without runtime', async () => {
    api.getOpenAIEvalConfig.mockResolvedValue(serverConfig())
    api.saveOpenAIEvalConfig.mockImplementation(async (payload: OpenAIEvalConfig) => ({ ...payload, revision: 5 }))
    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-testid="pause-auto"]').trigger('click')
    await flushPromises()
    const reason = $<HTMLInputElement>('[data-testid="pause-reason-input"]')!
    reason.value = '对比实验'
    reason.dispatchEvent(new Event('input'))
    $<HTMLButtonElement>('[data-testid="confirm-pause"]')!.click()
    await flushPromises()

    expect(wrapper.get('[data-testid="auto-state"]').text()).toContain('自动测试已暂停至')
    expect(wrapper.get('[data-testid="auto-pending"]').text()).toContain('保存后生效')
    expect(wrapper.get('[data-testid="pause-reason"]').text()).toContain('对比实验')
    expect(wrapper.get('[data-testid="target-paused"]').text()).toBe('已暂停')
    expect(wrapper.get('[data-testid="paused-summary"]').text()).toContain('oauth-a')
    expect(wrapper.get('[data-testid="test-candy"] [data-testid="auto-note"]').text()).toContain('已暂停')

    // Re-reads after save return what was saved.
    api.getOpenAIEvalConfig.mockImplementation(async () => JSON.parse(JSON.stringify(api.saveOpenAIEvalConfig.mock.calls.at(-1)?.[0] ?? serverConfig())))
    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()

    const payload = api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig
    expect(payload.revision).toBe(4)
    expect(payload.accounts[0].candy_schedule.enabled).toBe(true)
    expect(payload.accounts[0].fingerprint_schedule.enabled).toBe(true)
    expect(payload.background_controls).toHaveLength(1)
    const control = payload.background_controls![0]
    expect(control).not.toHaveProperty('runtime')
    expect(control.account_id).toBe(11)
    expect(control.pause_reason).toBe('对比实验')
    expect(new Date(control.paused_until!).getTime()).toBeGreaterThan(Date.now() + 50 * 60 * 1000)
    expect(control.budget_enabled).toBe(false)
    expect(store.showError).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="auto-pending"]').exists()).toBe(false)

    // Resume clears the pause but keeps the control.
    await wrapper.get('[data-testid="resume-auto"]').trigger('click')
    expect(wrapper.get('[data-testid="auto-state"]').text()).toContain('自动测试按计划运行')
    wrapper.unmount()
  })

  it('omits background_controls on save when an older server never sent them', async () => {
    api.getOpenAIEvalConfig.mockResolvedValue(serverConfig())
    api.saveOpenAIEvalConfig.mockImplementation(async (payload: OpenAIEvalConfig) => ({ ...payload, revision: 5 }))
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="max-attempts"]').setValue(4)
    await wrapper.get('[data-testid="max-attempts"]').trigger('change')
    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    expect(api.saveOpenAIEvalConfig.mock.calls[0][0]).not.toHaveProperty('background_controls')
    wrapper.unmount()
  })

  it('warns that a 60-sample fingerprint cannot fit 1 per minute in 15 minutes and blocks the manual run', async () => {
    api.getOpenAIEvalConfig.mockResolvedValue(serverConfig({
      background_controls: [{
        account_id: 11, paused_until: null, pause_reason: '', budget_enabled: true,
        max_requests_per_hour: 60, min_send_interval_seconds: 60, max_background_concurrency: 1, sampling_window_seconds: 900
      }]
    }))
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.get('[data-testid="limits-summary"]').text()).toContain('每小时最多 60 次，发送间隔 60 秒，后台并发 1，采样窗 15 分钟')
    const warnings = wrapper.get('[data-testid="budget-warnings"]').text()
    expect(warnings).toContain('行为指纹')
    expect(warnings).toContain('一轮需要 60 次请求，采样窗内最多 15 次')
    expect(warnings).not.toContain('糖果题')
    expect(wrapper.get('[data-testid="test-fingerprint"] [data-testid="auto-note"]').text()).toContain('不会自动启动')

    await wrapper.get('[data-testid="run-fingerprint"]').trigger('click')
    await flushPromises()
    for (const mode of ['quick', 'standard', 'strict']) {
      expect($<HTMLInputElement>(`input[name="manual-sample"][value="${mode}"]`)!.disabled).toBe(true)
    }
    expect($<HTMLButtonElement>('[data-testid="confirm-fingerprint"]')!.disabled).toBe(true)
    expect($('[data-testid="manual-budget-note"]')).not.toBeNull()
    wrapper.unmount()
  })

  it('opts into limits with editable defaults and previews whole-run admission', async () => {
    api.getOpenAIEvalConfig.mockResolvedValue(serverConfig())
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="open-limits"]').trigger('click')
    await flushPromises()
    const field = (key: string) => $<HTMLInputElement>(`[data-testid="limit-${key}"]`)!
    expect(field('max_requests_per_hour').value).toBe('60')
    expect(field('min_send_interval_seconds').value).toBe('60')
    expect(field('max_background_concurrency').value).toBe('1')
    expect(field('sampling_window_seconds').value).toBe('15')
    expect(field('max_requests_per_hour').disabled).toBe(true)

    $<HTMLButtonElement>('[data-testid="limits-enabled"]')!.click()
    await flushPromises()
    expect($('[data-testid="fingerprint-infeasible"]')!.textContent).toContain('60 / 200 / 400')

    // An out-of-range entry is reported, not silently clamped.
    field('max_background_concurrency').value = '0'
    field('max_background_concurrency').dispatchEvent(new Event('input'))
    await flushPromises()
    expect($<HTMLButtonElement>('[data-testid="confirm-limits"]')!.disabled).toBe(true)
    field('max_background_concurrency').value = '1'
    field('max_background_concurrency').dispatchEvent(new Event('input'))
    field('min_send_interval_seconds').value = '5'
    field('min_send_interval_seconds').dispatchEvent(new Event('input'))
    await flushPromises()
    // 15 min / 5 s + 1 = 181 by interval, but 60 per hour caps it.
    expect($('[data-testid="limits-estimate"]')!.textContent).toContain('最多可发送 60 次请求')
    $<HTMLButtonElement>('[data-testid="confirm-limits"]')!.click()
    await flushPromises()
    expect(wrapper.get('[data-testid="limits-summary"]').text()).toContain('发送间隔 5 秒')
    wrapper.unmount()
  })

  it('labels the schedule interval as counted from the end of each run and keeps custom minutes', async () => {
    const config = serverConfig()
    config.accounts[0].candy_schedule.interval_seconds = 47 * 60
    api.getOpenAIEvalConfig.mockResolvedValue(config)
    const wrapper = mountView()
    await flushPromises()
    const candy = wrapper.get('[data-testid="test-candy"]')
    expect(candy.text()).toContain('本轮结束后间隔')
    expect((candy.get('[data-testid="custom-interval"]').element as HTMLInputElement).value).toBe('47')
    wrapper.unmount()
  })
})
