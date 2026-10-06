import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { defineComponent } from 'vue'
import PrismCatalogDialog from '../PrismCatalogDialog.vue'
import AccountActionMenu from '@/components/admin/account/AccountActionMenu.vue'
import type { Account } from '@/types'

const { prism, accounts, showError, showSuccess } = vi.hoisted(() => ({
  prism: { getAccountModels: vi.fn(), refreshAccount: vi.fn() },
  accounts: { getById: vi.fn(), update: vi.fn() },
  showError: vi.fn(),
  showSuccess: vi.fn()
}))

vi.mock('@/api/admin/prism', async () => {
  const actual = await vi.importActual<typeof import('@/api/admin/prism')>('@/api/admin/prism')
  return { ...actual, prismAPI: prism }
})
vi.mock('@/api/admin', () => ({ adminAPI: { accounts } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError, showSuccess }) }))
vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => (params ? `${key} ${JSON.stringify(params)}` : key),
      te: (key: string) => key.startsWith('admin.accounts.prism.errors.')
    })
  }
})

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: { show: { type: Boolean, default: false } },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>'
})

const CATALOG = {
  source: 'prism_account_catalog',
  models: [
    { id: 'model-a', label: 'Model A', reasoning_efforts: ['low', 'high'], default_reasoning_effort: 'high' },
    { id: 'model-b', label: '', reasoning_efforts: [], default_reasoning_effort: '' }
  ]
}

function prismAccount(overrides: Partial<Account> = {}): Account {
  return {
    id: 5,
    name: 'prism-a',
    platform: 'prism',
    type: 'oauth',
    credentials: { model_mapping: { fast: 'model-a' } },
    ...overrides
  } as Account
}

let wrapper: VueWrapper | null = null

beforeEach(() => {
  Object.values(prism).forEach((fn) => fn.mockReset())
  Object.values(accounts).forEach((fn) => fn.mockReset())
  showError.mockReset()
  showSuccess.mockReset()
})

afterEach(() => {
  wrapper?.unmount()
  wrapper = null
})

function mountCatalog(account = prismAccount()) {
  wrapper = mount(PrismCatalogDialog, {
    props: { show: true, account },
    global: { stubs: { BaseDialog: BaseDialogStub, Icon: true } }
  })
  return wrapper
}

describe('PrismCatalogDialog', () => {
  it('lists catalog models with efforts and marks the default', async () => {
    prism.getAccountModels.mockResolvedValue(CATALOG)
    const w = mountCatalog()
    await flushPromises()

    expect(prism.getAccountModels).toHaveBeenCalledWith(5, expect.objectContaining({ refresh: false }))
    const rows = w.findAll('[data-testid="prism-catalog-row"]')
    expect(rows).toHaveLength(2)
    expect(rows[0].text()).toContain('model-a')
    expect(rows[0].find('[title="admin.accounts.prism.catalog.defaultEffort"]').text()).toBe('high')
    expect(rows[1].text()).toContain('admin.accounts.prism.catalog.noEfforts')
  })

  it('reloads from Prism on request', async () => {
    prism.getAccountModels.mockResolvedValue(CATALOG)
    const w = mountCatalog()
    await flushPromises()
    await w.find('[data-testid="prism-catalog-refresh"]').trigger('click')
    await flushPromises()
    expect(prism.getAccountModels).toHaveBeenLastCalledWith(5, expect.objectContaining({ refresh: true }))
  })

  it('shows an actionable error and offers sign-in when the catalog fails', async () => {
    prism.getAccountModels.mockRejectedValue({ code: 'credentials_expired', message: 'raw' })
    const w = mountCatalog()
    await flushPromises()
    expect(w.find('[data-testid="prism-catalog-error"]').text()).toContain('admin.accounts.prism.errors.credentials_expired')
    await w.find('[data-testid="prism-catalog-error"] button').trigger('click')
    expect(w.emitted('relogin')?.[0]?.[0]).toMatchObject({ id: 5 })
  })

  it('flags aliases that point outside the catalog instead of substituting', async () => {
    prism.getAccountModels.mockResolvedValue(CATALOG)
    const w = mountCatalog(prismAccount({ credentials: { model_mapping: { old: 'model-retired' } } }))
    await flushPromises()
    expect(w.text()).toContain('admin.accounts.prism.aliases.issues.unknown_target')
    expect(w.find('[data-testid="prism-alias-save"]').attributes('disabled')).toBeDefined()
  })

  it('saves aliases onto the latest credentials without touching other keys', async () => {
    prism.getAccountModels.mockResolvedValue(CATALOG)
    accounts.getById.mockResolvedValue(prismAccount({ credentials: { model_mapping: { fast: 'model-a' }, prism_verified_at: 'x' } }))
    accounts.update.mockImplementation(async (id: number, payload: { credentials: Record<string, unknown> }) => prismAccount({ id, credentials: payload.credentials }))
    const w = mountCatalog()
    await flushPromises()

    await w.find('[data-testid="prism-alias-keep-direct"]').trigger('click')
    await w.find('[data-testid="prism-alias-save"]').trigger('click')
    await flushPromises()

    expect(accounts.update).toHaveBeenCalledWith(5, {
      credentials: {
        prism_verified_at: 'x',
        model_mapping: { fast: 'model-a', 'model-a': 'model-a', 'model-b': 'model-b' }
      }
    })
    expect(w.emitted('updated')).toHaveLength(1)
  })

  it('removes the mapping when every alias is deleted', async () => {
    prism.getAccountModels.mockResolvedValue(CATALOG)
    accounts.getById.mockResolvedValue(prismAccount())
    accounts.update.mockResolvedValue(prismAccount({ credentials: {} }))
    const w = mountCatalog()
    await flushPromises()
    await w.find('[data-testid="prism-alias-row"] button').trigger('click')
    await w.find('[data-testid="prism-alias-save"]').trigger('click')
    await flushPromises()
    expect(accounts.update).toHaveBeenCalledWith(5, { credentials: {} })
  })
})

describe('AccountActionMenu — Prism', () => {
  const anchorRect = new DOMRect(100, 100, 24, 24)
  const bodyText = () => document.body.textContent ?? ''

  it('offers Prism actions and hides generic OAuth re-auth and token refresh', () => {
    const menu = mount(AccountActionMenu, {
      props: { show: true, account: prismAccount(), anchorRect },
      attachTo: document.body
    })
    expect(document.body.querySelector('[data-testid="prism-menu-models"]')).not.toBeNull()
    expect(document.body.querySelector('[data-testid="prism-menu-relogin"]')).not.toBeNull()
    expect(document.body.querySelector('[data-testid="prism-menu-refresh"]')).not.toBeNull()
    expect(bodyText()).not.toContain('admin.accounts.reAuthorize')
    expect(bodyText()).not.toContain('admin.accounts.refreshToken')
    expect(bodyText()).not.toContain('admin.accounts.createSparkShadow')
    expect(bodyText()).not.toContain('admin.accounts.setPrivacy')
    menu.unmount()
  })

  it('does not show Prism actions on OpenAI OAuth accounts', () => {
    const menu = mount(AccountActionMenu, {
      props: { show: true, account: prismAccount({ platform: 'openai' }), anchorRect },
      attachTo: document.body
    })
    expect(document.body.querySelector('[data-testid="prism-menu-models"]')).toBeNull()
    expect(bodyText()).toContain('admin.accounts.reAuthorize')
    menu.unmount()
  })
})
