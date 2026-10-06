import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { defineComponent } from 'vue'
import PrismAccountDialog from '../PrismAccountDialog.vue'
import type { Account } from '@/types'

const { api, showError, showSuccess } = vi.hoisted(() => ({
  api: {
    importAccounts: vi.fn(),
    beginOAuth: vi.fn(),
    exchangeOAuth: vi.fn(),
    getOAuthStatus: vi.fn(),
    cancelOAuth: vi.fn(),
    refreshAccount: vi.fn(),
    getAccountModels: vi.fn()
  },
  showError: vi.fn(),
  showSuccess: vi.fn()
}))

vi.mock('@/api/admin/prism', async () => {
  const actual = await vi.importActual<typeof import('@/api/admin/prism')>('@/api/admin/prism')
  return { ...actual, prismAPI: api }
})

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError, showSuccess })
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copyToClipboard: vi.fn() })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => (params ? `${key} ${JSON.stringify(params)}` : key),
      te: (key: string) => key.startsWith('admin.accounts.prism.errors.') && !key.endsWith('.unknown_code')
    })
  }
})

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: { show: { type: Boolean, default: false }, title: { type: String, default: '' } },
  emits: ['close'],
  template: '<div v-if="show"><h1>{{ title }}</h1><slot /><slot name="footer" /></div>'
})

const SECRET = 'prism_session_token=SYNTHETIC-SECRET-VALUE'
const CALLBACK = 'http://localhost:1455/auth/callback?code=abc&state=xyz'

function pendingSession(overrides = {}) {
  return {
    session_id: 'sess-1',
    authorize_url: 'https://auth.openai.com/oauth/authorize?state=xyz',
    redirect_uri: 'http://localhost:1455/auth/callback',
    expires_at: new Date(Date.now() + 15 * 60_000).toISOString(),
    status: 'pending',
    login_succeeded: false,
    prism_verified: false,
    ...overrides
  }
}

let wrapper: VueWrapper | null = null

function mountDialog(props: Record<string, unknown> = {}) {
  wrapper = mount(PrismAccountDialog, {
    props: { show: false, proxies: [], groups: [], ...props },
    global: {
      stubs: { BaseDialog: BaseDialogStub, GroupSelector: true, ProxySelector: true, Icon: true }
    }
  })
  return wrapper
}

async function open(w: VueWrapper) {
  await w.setProps({ show: true })
  await flushPromises()
}

beforeEach(() => {
  vi.useFakeTimers({ shouldAdvanceTime: true })
  Object.values(api).forEach((fn) => fn.mockReset())
  showError.mockReset()
  showSuccess.mockReset()
  api.cancelOAuth.mockResolvedValue(pendingSession({ status: 'canceled' }))
  vi.spyOn(window, 'open').mockImplementation(() => null)
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = null
  vi.useRealTimers()
  vi.restoreAllMocks()
})

describe('PrismAccountDialog — browser sign-in', () => {
  it('keeps token login and Prism verification as separate checkpoints', async () => {
    api.beginOAuth.mockResolvedValue(pendingSession())
    api.exchangeOAuth.mockResolvedValue(pendingSession({
      status: 'failed',
      login_succeeded: true,
      prism_verified: false,
      code: 'no_entitlement',
      message: 'raw server text'
    }))
    const w = mountDialog()
    await open(w)

    await w.find('[data-testid="prism-begin"]').trigger('click')
    await flushPromises()
    expect(api.beginOAuth).toHaveBeenCalledWith(expect.objectContaining({ concurrency: 2, priority: 50, group_ids: [] }))
    expect(window.open).toHaveBeenCalledWith(expect.stringContaining('auth.openai.com'), '_blank', 'noopener,noreferrer')

    await w.find('[data-testid="prism-callback"]').setValue(CALLBACK)
    await w.find('[data-testid="prism-exchange"]').trigger('click')
    await flushPromises()

    expect(api.exchangeOAuth).toHaveBeenCalledWith('sess-1', CALLBACK, expect.anything())
    expect(w.find('[data-testid="prism-gate-login"]').attributes('data-state')).toBe('done')
    expect(w.find('[data-testid="prism-gate-access"]').attributes('data-state')).toBe('failed')
    expect(w.find('[data-testid="prism-session-message"]').text()).toBe('admin.accounts.prism.errors.no_entitlement')
    expect(w.emitted('changed')).toBeUndefined()
  })

  it('reports success only after Prism verification and saving', async () => {
    api.beginOAuth.mockResolvedValue(pendingSession())
    api.exchangeOAuth.mockResolvedValue(pendingSession({ status: 'completed', login_succeeded: true, prism_verified: true, account_id: 42 }))
    const w = mountDialog()
    await open(w)
    await w.find('[data-testid="prism-begin"]').trigger('click')
    await flushPromises()
    await w.find('[data-testid="prism-callback"]').setValue(CALLBACK)
    await w.find('[data-testid="prism-exchange"]').trigger('click')
    await flushPromises()

    expect(w.find('[data-testid="prism-gate-access"]').attributes('data-state')).toBe('done')
    expect(w.find('[data-testid="prism-gate-access"]').text()).toContain('"id":42')
    expect(w.emitted('changed')).toHaveLength(1)
  })

  it('blocks a callback from the wrong address before sending it', async () => {
    api.beginOAuth.mockResolvedValue(pendingSession())
    const w = mountDialog()
    await open(w)
    await w.find('[data-testid="prism-begin"]').trigger('click')
    await flushPromises()
    await w.find('[data-testid="prism-callback"]').setValue('https://evil.example/cb?code=1&state=2')

    expect(w.text()).toContain('admin.accounts.prism.callbackIssues.wrong_address')
    expect(w.find('[data-testid="prism-exchange"]').attributes('disabled')).toBeDefined()
    await w.find('[data-testid="prism-exchange"]').trigger('click')
    expect(api.exchangeOAuth).not.toHaveBeenCalled()
  })

  it('cancels a pending session explicitly and when the dialog closes', async () => {
    api.beginOAuth.mockResolvedValue(pendingSession())
    const w = mountDialog()
    await open(w)
    await w.find('[data-testid="prism-begin"]').trigger('click')
    await flushPromises()

    await w.find('[data-testid="prism-cancel-session"]').trigger('click')
    await flushPromises()
    expect(api.cancelOAuth).toHaveBeenCalledWith('sess-1')
    expect(w.find('[data-testid="prism-gate-login"]').text()).toContain('gates.ended.canceled')

    api.cancelOAuth.mockClear()
    api.beginOAuth.mockResolvedValue(pendingSession({ session_id: 'sess-2' }))
    await w.find('[data-testid="prism-begin"]').trigger('click')
    await flushPromises()
    await w.setProps({ show: false })
    await flushPromises()
    expect(api.cancelOAuth).toHaveBeenCalledWith('sess-2')
  })

  it('shows expiry from the server status poll', async () => {
    api.beginOAuth.mockResolvedValue(pendingSession())
    api.getOAuthStatus.mockResolvedValue(pendingSession({ status: 'expired', code: 'session_expired' }))
    const w = mountDialog()
    await open(w)
    await w.find('[data-testid="prism-begin"]').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid="prism-expiry"]').exists()).toBe(true)

    await vi.advanceTimersByTimeAsync(5_100)
    await flushPromises()
    expect(api.getOAuthStatus).toHaveBeenCalledWith('sess-1')
    expect(w.find('[data-testid="prism-session-message"]').text()).toBe('admin.accounts.prism.errors.session_expired')
    expect(w.find('[data-testid="prism-callback"]').attributes('disabled')).toBeDefined()
    expect(w.find('[data-testid="prism-begin"]').exists()).toBe(true)
  })

  it('signs in again for an existing account by id only', async () => {
    api.beginOAuth.mockResolvedValue(pendingSession())
    const account = { id: 7, name: 'prism-a', platform: 'prism', type: 'oauth' } as Account
    const w = mountDialog({ account })
    await open(w)

    expect(w.find('[data-testid="prism-method-cookies"]').exists()).toBe(false)
    expect(w.find('[data-testid="prism-name"]').exists()).toBe(false)
    await w.find('[data-testid="prism-begin"]').trigger('click')
    await flushPromises()
    expect(api.beginOAuth).toHaveBeenCalledWith({ account_id: 7 })
  })
})

describe('PrismAccountDialog — cookie import', () => {
  it('imports in bounded chunks with verification and never echoes cookies', async () => {
    api.importAccounts.mockImplementation(async (request: { accounts: unknown[] }) => ({
      total: request.accounts.length,
      created: request.accounts.length,
      updated: 0,
      failed: 0,
      items: request.accounts.map((_, i) => ({ index: i + 1, action: 'created', account_id: 100 + i }))
    }))
    const w = mountDialog()
    await open(w)
    await w.find('[data-testid="prism-method-cookies"]').trigger('click')
    const lines = Array.from({ length: 6 }, (_, i) => `${SECRET}-${i}`)
    await w.find('[data-testid="prism-cookie-input"]').setValue(lines.join('\n'))
    await w.find('[data-testid="prism-import"]').trigger('click')
    await flushPromises()

    expect(api.importAccounts).toHaveBeenCalledTimes(2)
    const [first] = api.importAccounts.mock.calls[0]
    expect(first.accounts).toHaveLength(4)
    expect(first.verify).toBeUndefined() // the API layer forces verify=true
    expect(first.update_existing).toBe(true)
    expect(w.find('[data-testid="prism-import-result"]').text()).toContain('"created":6')
    expect(w.html()).not.toContain('SYNTHETIC-SECRET-VALUE')
    expect((w.find('[data-testid="prism-cookie-input"]').element as HTMLTextAreaElement).value).toBe('')
    expect(w.emitted('changed')).toBeTruthy()
  })

  it('keeps only failed lines for retry and shows actionable reasons', async () => {
    api.importAccounts.mockResolvedValue({
      total: 2,
      created: 1,
      updated: 0,
      failed: 1,
      items: [
        { index: 1, action: 'created', account_id: 9 },
        { index: 2, action: 'failed', code: 'credentials_expired', message: 'raw' }
      ]
    })
    const w = mountDialog()
    await open(w)
    await w.find('[data-testid="prism-method-cookies"]').trigger('click')
    await w.find('[data-testid="prism-cookie-input"]').setValue('first=1\nsecond=2')
    await w.find('[data-testid="prism-import"]').trigger('click')
    await flushPromises()

    expect(w.find('[data-testid="prism-import-result"]').text()).toContain('admin.accounts.prism.errors.credentials_expired')
    expect((w.find('[data-testid="prism-cookie-input"]').element as HTMLTextAreaElement).value).toBe('second=2')
  })

  it('refuses more than the server batch limit', async () => {
    const w = mountDialog()
    await open(w)
    await w.find('[data-testid="prism-method-cookies"]').trigger('click')
    await w.find('[data-testid="prism-cookie-input"]').setValue(Array.from({ length: 101 }, (_, i) => `c=${i}`).join('\n'))
    expect(w.find('[data-testid="prism-import"]').attributes('disabled')).toBeDefined()
  })

  it('stops between chunks and leaves unsent lines in place', async () => {
    let release: (value: unknown) => void = () => {}
    api.importAccounts.mockImplementationOnce((request: { accounts: unknown[] }, options: { signal: AbortSignal }) =>
      new Promise((resolve, reject) => {
        release = resolve
        options.signal.addEventListener('abort', () => reject({ code: 'ERR_CANCELED' }))
        void request
      })
    )
    const w = mountDialog()
    await open(w)
    await w.find('[data-testid="prism-method-cookies"]').trigger('click')
    await w.find('[data-testid="prism-cookie-input"]').setValue(Array.from({ length: 6 }, (_, i) => `c=${i}`).join('\n'))
    await w.find('[data-testid="prism-import"]').trigger('click')
    await flushPromises()
    expect(w.find('[data-testid="prism-import-progress"]').exists()).toBe(true)

    await w.find('[data-testid="prism-stop-import"]').trigger('click')
    await flushPromises()
    void release

    expect(api.importAccounts).toHaveBeenCalledTimes(1)
    expect(w.find('[data-testid="prism-import-result"]').text()).toContain('"count":6')
    expect((w.find('[data-testid="prism-cookie-input"]').element as HTMLTextAreaElement).value.split('\n')).toHaveLength(6)
  })
})
