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
const prism = vi.hoisted(() => ({ getAccountModels: vi.fn() }))
const store = vi.hoisted(() => ({ showError: vi.fn(), showSuccess: vi.fn(), showInfo: vi.fn() }))

vi.mock('@/api/admin/accounts', () => ({ default: api, accountsAPI: api }))
vi.mock('@/api/admin/prism', async importOriginal => ({
  ...(await importOriginal<typeof import('@/api/admin/prism')>()),
  prismAPI: prism
}))
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
import { normalizePrismCatalog } from '@/api/admin/prism'

const evalCatalog = {
  items: [{ id: 'gpt-5' }, { id: 'gpt-5.1' }],
  baseline_version: 'cpa-v1',
  baseline_models: ['prism-covered'],
  candy: { expected_answer: 29, confidence: 'low', scheduling: 'automatic_quality' },
  evaluation_notice: '',
  reasoning_efforts: ['', 'high'],
  fingerprint_modes: [{ id: 'quick', samples: 60 }],
  modeltrace: { requests: 3, bank_revision: 'x', candidate_count: 16, scheduling: 'automatic_quality' }
}

// Fixture catalogs (older server shape: no public_models, no modeltrace.models).
// Names are synthetic and only exist in this test.
const prismCatalogs: Record<number, unknown> = {
  31: {
    source: 'prism_account_catalog',
    models: [
      { id: 'prism-covered', label: 'Covered', reasoning_efforts: ['low', 'medium', 'deep'], default_reasoning_effort: 'medium' },
      { id: 'prism-plain', label: 'Plain' }
    ]
  },
  32: {
    source: 'prism_account_catalog',
    models: [{ id: 'prism-plain', label: 'Plain', reasoning_efforts: ['low'] }]
  }
}

const schedule = (interval: number, enabled = false) => ({ enabled, interval_seconds: interval, jitter_seconds: 0 })

function config(accounts: OpenAIEvalConfig['accounts'] = []): OpenAIEvalConfig {
  return { revision: 3, effects_enabled: true, bps_auto_enabled: false, scheduling_policy: 'stability_first', policies: [], accounts }
}

function prismRoute(model: string, effort = '', accountID = 31) {
  return {
    account_id: accountID,
    requested_model: model,
    reasoning_effort: effort,
    candy_schedule: { ...schedule(3600, true), sample_count: 5 },
    fingerprint_schedule: { ...schedule(86400, true), sample_mode: 'quick' },
    modeltrace_schedule: schedule(86400),
    state_probe_schedule: schedule(21600, true),
    bps_auto: false,
    bps_mode: 'force_off' as const,
    direct_oauth_eligible: false
  }
}

const openaiAccounts = [{ id: 11, name: 'oauth-a', platform: 'openai', type: 'oauth', parent_account_id: null }]
const prismAccounts = [
  { id: 31, name: 'prism-one', platform: 'prism', type: 'oauth', parent_account_id: null, credentials: { model_mapping: { 'alias-plain': 'prism-plain' } } },
  { id: 32, name: 'prism-two', platform: 'prism', type: 'oauth', parent_account_id: null }
]

function mountView() {
  return mount(TestsView, {
    attachTo: document.body,
    global: { stubs: { AppLayout: { template: '<div><slot /></div>' }, RouterLink: RouterLinkStub } }
  })
}

const $ = <T extends Element>(selector: string) => document.body.querySelector<T>(`.modal-overlay:not([class*="modal-leave"]) ${selector}`)
const $$ = <T extends Element>(selector: string) => Array.from(document.body.querySelectorAll<T>(`.modal-overlay:not([class*="modal-leave"]) ${selector}`))

async function choose(selector: string, value: string) {
  const select = $<HTMLSelectElement>(selector)!
  select.value = value
  select.dispatchEvent(new Event('change'))
  await flushPromises()
}

async function check(index: number) {
  const box = $$<HTMLInputElement>('[data-testid="add-account"]')[index]
  box.checked = true
  box.dispatchEvent(new Event('change'))
  await flushPromises()
}

const optionValues = (selector: string) => $$<HTMLOptionElement>(`${selector} option`).filter(option => !option.disabled).map(option => option.value)

beforeEach(() => {
  vi.clearAllMocks()
  document.body.innerHTML = ''
  api.getOpenAIEvalModels.mockResolvedValue(evalCatalog)
  api.getOpenAIEvalConfig.mockResolvedValue(config())
  api.list.mockImplementation(async (_page: number, _size: number, filters: { platform: string }) => ({
    items: filters.platform === 'prism' ? prismAccounts : openaiAccounts
  }))
  api.listOpenAIEvalRuns.mockResolvedValue({ items: [] })
  api.saveOpenAIEvalConfig.mockImplementation(async (payload: OpenAIEvalConfig) => ({ ...payload, revision: (payload.revision ?? 0) + 1 }))
  prism.getAccountModels.mockImplementation(async (id: number) => {
    if (!(id in prismCatalogs)) throw { message: 'catalog unavailable' }
    return normalizePrismCatalog(prismCatalogs[id])
  })
})

describe('Prism quality-test targets', () => {
  it('lists Prism OAuth accounts next to OpenAI and loads their models from the account catalog', async () => {
    const wrapper = mountView()
    await flushPromises()
    expect(api.list).toHaveBeenCalledWith(1, 500, { platform: 'prism', lite: '1' })

    await wrapper.get('[data-testid="open-add"]').trigger('click')
    await flushPromises()
    // OpenAI stays the default source; its model list is the evaluation catalog.
    expect(optionValues('[data-testid="add-model"]')).toEqual(['gpt-5', 'gpt-5.1'])

    $<HTMLButtonElement>('[data-testid="add-source-prism"]')!.click()
    await flushPromises()
    expect($$('[data-testid="add-account"]').length).toBe(2)
    // No account selected: no models at all, never the OpenAI fallback.
    expect(optionValues('[data-testid="add-model"]')).toEqual([])
    expect($<HTMLSelectElement>('[data-testid="add-model"]')!.disabled).toBe(true)

    await check(0)
    expect(prism.getAccountModels).toHaveBeenCalledWith(31, { refresh: false })
    expect(optionValues('[data-testid="add-model"]')).toEqual(['prism-covered', 'prism-plain'])

    await choose('[data-testid="add-model"]', 'prism-covered')
    expect(optionValues('[data-testid="add-effort"]')).toEqual(['', 'low', 'medium', 'deep'])
    // The account default is named, not guessed.
    expect($<HTMLOptionElement>('[data-testid="add-effort"] option[value=""]')!.textContent).toContain('medium')
    wrapper.unmount()
  })

  it('offers only models every selected account has, and clears a choice that no longer fits', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="open-add"]').trigger('click')
    await flushPromises()
    $<HTMLButtonElement>('[data-testid="add-source-prism"]')!.click()
    await flushPromises()
    await check(0)
    await choose('[data-testid="add-model"]', 'prism-covered')
    await check(1)

    expect(optionValues('[data-testid="add-model"]')).toEqual(['prism-plain'])
    expect($<HTMLSelectElement>('[data-testid="add-model"]')!.value).toBe('')
    expect($<HTMLButtonElement>('[data-testid="add-submit"]')!.disabled).toBe(true)
    wrapper.unmount()
  })

  it('shows a catalog failure with retry and no fallback models', async () => {
    prism.getAccountModels.mockRejectedValueOnce({ message: 'down' })
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="open-add"]').trigger('click')
    await flushPromises()
    $<HTMLButtonElement>('[data-testid="add-source-prism"]')!.click()
    await flushPromises()
    await check(0)

    expect($('[data-testid="add-catalog-error"]')?.textContent).toContain('prism-one')
    expect(optionValues('[data-testid="add-model"]')).toEqual([])
    $<HTMLButtonElement>('[data-testid="add-catalog-retry"]')!.click()
    await flushPromises()
    expect(prism.getAccountModels).toHaveBeenLastCalledWith(31, { refresh: true })
    expect(optionValues('[data-testid="add-model"]')).toEqual(['prism-covered', 'prism-plain'])
    wrapper.unmount()
  })

  it('adds a Prism Candy target with probe and BPS off and saves the exact model and effort', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="open-add"]').trigger('click')
    await flushPromises()
    $<HTMLButtonElement>('[data-testid="add-source-prism"]')!.click()
    await flushPromises()
    await check(0)
    await choose('[data-testid="add-model"]', 'prism-plain')
    $<HTMLButtonElement>('[data-testid="add-submit"]')!.click()
    await flushPromises()

    expect(wrapper.get('[data-testid="prism-target-note"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="run-candy"]').attributes('disabled')).toBeUndefined()
    const probe = wrapper.get('[data-testid="test-state_probe"]')
    expect(probe.get('[data-testid="run-state_probe"]').attributes('disabled')).toBeDefined()
    expect(probe.text()).toContain(zhT('admin.modelIntegrity.tests.prism.stateProbeUnsupported'))
    // prism-plain has no Fingerprint baseline.
    expect(wrapper.get('[data-testid="run-fingerprint"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="test-fingerprint"]').text()).toContain('prism-plain')

    // Candy runs with the exact requested model, nothing substituted.
    api.runOpenAIEval.mockResolvedValue({ id: 5, account_id: 31, test_type: 'candy', requested_model: 'prism-plain', reasoning_effort: '', status: 'pass', outcome: { status: 'pass', sample_count: 1, expected_count: 1, confidence: 'low', scheduling: 'automatic_quality' }, request_count: 1, input_tokens: 10, output_tokens: 2, duration_ms: 900, started_at: '2026-10-07T00:00:00Z', finished_at: '2026-10-07T00:00:01Z', trigger_source: 'manual' })
    await wrapper.get('[data-testid="run-candy"]').trigger('click')
    await flushPromises()
    expect(api.runOpenAIEval).toHaveBeenCalledWith(expect.objectContaining({ account_id: 31, requested_model: 'prism-plain', reasoning_effort: '', test_type: 'candy' }))

    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    const saved = (api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig).accounts[0]
    expect(saved).toMatchObject({ account_id: 31, requested_model: 'prism-plain', reasoning_effort: '', bps_mode: 'force_off', bps_auto: false })
    expect(saved.state_probe_schedule.enabled).toBe(false)
    expect(saved.fingerprint_schedule.enabled).toBe(false)
    wrapper.unmount()
  })

  it('reports baseline coverage per test for saved Prism targets and keeps their settings', async () => {
    api.getOpenAIEvalConfig.mockResolvedValue(config([prismRoute('prism-covered', 'deep'), prismRoute('alias-plain')]))
    const wrapper = mountView()
    await flushPromises()

    // Covered model: Fingerprint available, ModelTrace explains the server-side bank check.
    expect(wrapper.get('[data-testid="run-fingerprint"]').attributes('disabled')).toBeUndefined()
    // This fixture is an older server without modeltrace.models: uncertainty is stated, not coverage.
    expect(wrapper.get('[data-testid="test-modeltrace"]').text()).toContain(zhT('admin.modelIntegrity.tests.prism.modelTraceCoverageUnknown'))
    expect(wrapper.get('[data-testid="run-state_probe"]').attributes('disabled')).toBeDefined()

    // The alias resolves to the uncovered model, so Fingerprint is unavailable and explained.
    await wrapper.findAll('[data-testid="target"]')[1].trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="prism-target-note"]').text()).toContain('prism-plain')
    expect(wrapper.get('[data-testid="unavailable-note"]').text()).toContain('prism-plain')
    // Candy + covered Fingerprint on the first target, Candy on the second; the
    // saved but unavailable plans (probe, uncovered Fingerprint) are not counted.
    expect(wrapper.get('[data-testid="budget"]').text()).toContain('3 个自动计划')

    // Saving without edits returns the routes untouched.
    await wrapper.get('[data-testid="max-attempts"]').setValue(4)
    await wrapper.get('[data-testid="max-attempts"]').trigger('change')
    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    const saved = (api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig).accounts
    expect(saved.map(route => `${route.requested_model}:${route.reasoning_effort}`)).toEqual(['prism-covered:deep', 'alias-plain:'])
    expect(saved[0].candy_schedule).toMatchObject({ enabled: true, interval_seconds: 3600, sample_count: 5 })
    wrapper.unmount()
  })

  it('edits a Prism target from its own catalog and keeps a missing saved model selectable', async () => {
    api.getOpenAIEvalConfig.mockResolvedValue(config([prismRoute('prism-retired', '', 32)]))
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.get('[data-testid="test-candy"]').text()).toContain(zhT('admin.modelIntegrity.tests.prism.notInCatalog', { model: 'prism-retired' }))

    await wrapper.get('[data-testid="edit-target"]').trigger('click')
    await flushPromises()
    expect($<HTMLSelectElement>('[data-testid="edit-model"]')!.value).toBe('prism-retired')
    expect(optionValues('[data-testid="edit-model"]')).toEqual(['prism-retired', 'prism-plain'])
    expect(optionValues('[data-testid="edit-model"]')).not.toContain('gpt-5')

    await choose('[data-testid="edit-model"]', 'prism-plain')
    expect(optionValues('[data-testid="edit-effort"]')).toEqual(['', 'low'])
    await choose('[data-testid="edit-effort"]', 'low')
    $<HTMLButtonElement>('[data-testid="edit-submit"]')!.click()
    await flushPromises()
    expect(wrapper.get('[data-testid="target"][aria-current="true"]').text()).toContain('prism-plain · low')
    wrapper.unmount()
  })
})

describe('Prism quality-test targets with public aliases and ModelTrace coverage', () => {
  // Current server shape: raw models plus verified public aliases, and a
  // published ModelTrace bank. Account 41 maps fast → prism-covered and
  // plain → prism-plain; prism-covered has both baselines, prism-plain none.
  const aliasCatalog = {
    source: 'prism_account_catalog',
    models: [
      { id: 'prism-covered', label: 'Covered', reasoning_efforts: ['low', 'deep'], default_reasoning_effort: 'deep' },
      { id: 'prism-plain', label: 'Plain' }
    ],
    public_models: [
      { id: 'fast', object: 'model', type: 'model', display_name: 'Covered', reasoning_efforts: ['low', 'deep'], default_reasoning_effort: 'deep' },
      { id: 'plain', object: 'model', type: 'model', display_name: 'Plain' }
    ]
  }
  const aliasAccount = { id: 41, name: 'prism-alias', platform: 'prism', type: 'oauth', parent_account_id: null, credentials: { model_mapping: { fast: 'prism-covered', plain: 'prism-plain' } } }

  beforeEach(() => {
    api.getOpenAIEvalModels.mockResolvedValue({ ...evalCatalog, modeltrace: { ...evalCatalog.modeltrace, models: ['prism-covered', 'gpt-5'] } })
    api.list.mockImplementation(async (_page: number, _size: number, filters: { platform: string }) => ({
      items: filters.platform === 'prism' ? [aliasAccount] : openaiAccounts
    }))
    prism.getAccountModels.mockImplementation(async () => normalizePrismCatalog(aliasCatalog))
  })

  it('offers only the public aliases, never the raw upstream IDs, with their efforts', async () => {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="open-add"]').trigger('click')
    await flushPromises()
    $<HTMLButtonElement>('[data-testid="add-source-prism"]')!.click()
    await flushPromises()
    await check(0)

    expect(optionValues('[data-testid="add-model"]')).toEqual(['fast', 'plain'])
    await choose('[data-testid="add-model"]', 'fast')
    expect(optionValues('[data-testid="add-effort"]')).toEqual(['', 'low', 'deep'])
    expect($<HTMLOptionElement>('[data-testid="add-effort"] option[value=""]')!.textContent).toContain('deep')
    $<HTMLButtonElement>('[data-testid="add-submit"]')!.click()
    await flushPromises()

    // The alias resolves to a covered model: Fingerprint and ModelTrace both run.
    expect(wrapper.get('[data-testid="prism-target-note"]').text()).toContain('prism-covered')
    expect(wrapper.get('[data-testid="run-fingerprint"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.get('[data-testid="run-modeltrace"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.find('[data-testid="test-modeltrace"] [data-testid="test-notice"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="run-state_probe"]').attributes('disabled')).toBeDefined()

    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    expect((api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig).accounts[0]).toMatchObject({ account_id: 41, requested_model: 'fast' })
    wrapper.unmount()
  })

  it('disables ModelTrace for an alias whose actual model the bank does not cover and never runs or schedules it', async () => {
    const route = prismRoute('plain', '', 41)
    route.modeltrace_schedule.enabled = true
    api.getOpenAIEvalConfig.mockResolvedValue(config([route]))
    const wrapper = mountView()
    await flushPromises()

    const trace = wrapper.get('[data-testid="test-modeltrace"]')
    expect(trace.get('[data-testid="run-modeltrace"]').attributes('disabled')).toBeDefined()
    expect(trace.get('[data-testid="unavailable-note"]').text()).toBe(zhT('admin.modelIntegrity.tests.prism.noModelTraceBaseline', { model: 'prism-plain' }))
    expect(wrapper.get('[data-testid="run-fingerprint"]').attributes('disabled')).toBeDefined()
    // Only Candy counts as an automatic plan; saved but unsupported schedules are not run.
    expect(wrapper.get('[data-testid="budget"]').text()).toContain('1 个自动计划')
    await trace.get('[data-testid="run-modeltrace"]').trigger('click')
    await flushPromises()
    expect(api.runOpenAIEval).not.toHaveBeenCalled()

    // Editing the target switches the unsupported plans off.
    await wrapper.get('[data-testid="edit-target"]').trigger('click')
    await flushPromises()
    await choose('[data-testid="edit-effort"]', '')
    await choose('[data-testid="edit-model"]', 'fast')
    $<HTMLButtonElement>('[data-testid="edit-submit"]')!.click()
    await flushPromises()
    await wrapper.get('[data-testid="edit-target"]').trigger('click')
    await flushPromises()
    await choose('[data-testid="edit-model"]', 'plain')
    $<HTMLButtonElement>('[data-testid="edit-submit"]')!.click()
    await flushPromises()
    await wrapper.get('[data-testid="model-integrity-save"]').trigger('click')
    await flushPromises()
    const saved = (api.saveOpenAIEvalConfig.mock.calls[0][0] as OpenAIEvalConfig).accounts[0]
    expect(saved.requested_model).toBe('plain')
    expect(saved.modeltrace_schedule.enabled).toBe(false)
    expect(saved.fingerprint_schedule.enabled).toBe(false)
    expect(saved.candy_schedule.enabled).toBe(true)
    wrapper.unmount()
  })

  it('keeps a saved raw name selectable without rewriting it when the account now uses aliases', async () => {
    api.getOpenAIEvalConfig.mockResolvedValue(config([prismRoute('prism-covered', '', 41)]))
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.get('[data-testid="test-candy"]').text()).toContain(zhT('admin.modelIntegrity.tests.prism.notInCatalog', { model: 'prism-covered' }))

    await wrapper.get('[data-testid="edit-target"]').trigger('click')
    await flushPromises()
    expect($<HTMLSelectElement>('[data-testid="edit-model"]')!.value).toBe('prism-covered')
    expect(optionValues('[data-testid="edit-model"]')).toEqual(['prism-covered', 'fast', 'plain'])
    // Opening and closing changes nothing.
    expect($<HTMLButtonElement>('[data-testid="edit-submit"]')!.disabled).toBe(true)
    wrapper.unmount()
  })

  it('treats an empty public list as no selectable models, not as the raw list', async () => {
    prism.getAccountModels.mockImplementation(async () => normalizePrismCatalog({ ...aliasCatalog, public_models: [] }))
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-testid="open-add"]').trigger('click')
    await flushPromises()
    $<HTMLButtonElement>('[data-testid="add-source-prism"]')!.click()
    await flushPromises()
    await check(0)
    expect(optionValues('[data-testid="add-model"]')).toEqual([])
    expect($('[data-testid="add-no-shared"]')?.textContent).toContain(zhT('admin.modelIntegrity.tests.add.prismNoModels'))
    wrapper.unmount()
  })
})
