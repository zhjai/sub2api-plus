import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent } from 'vue'

const { createAccountMock, updateAccountMock, authIsSimpleMode } = vi.hoisted(() => ({
  createAccountMock: vi.fn(),
  updateAccountMock: vi.fn(),
  authIsSimpleMode: { value: true },
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showInfo: vi.fn(),
  }),
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    get isSimpleMode() {
      return authIsSimpleMode.value
    },
  }),
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      create: createAccountMock,
      update: updateAccountMock,
      checkMixedChannelRisk: vi.fn().mockResolvedValue({ has_risk: false }),
      probeUpstreamBilling: vi.fn().mockResolvedValue({}),
      syncUpstreamModels: vi.fn().mockResolvedValue({ models: [], metadata: {} }),
    },
    settings: {
      getWebSearchEmulationConfig: vi.fn().mockResolvedValue({ enabled: false, providers: [] }),
      getSettings: vi.fn().mockResolvedValue({}),
    },
    tlsFingerprintProfiles: {
      list: vi.fn().mockResolvedValue([]),
    },
  },
}))

vi.mock('@/api/admin/accounts', () => ({
  getAntigravityDefaultModelMapping: vi.fn().mockResolvedValue([]),
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key }),
  }
})

import CreateAccountModal from '../CreateAccountModal.vue'
import EditAccountModal from '../EditAccountModal.vue'

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: { show: { type: Boolean, default: false } },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>',
})

const OAuthAuthorizationFlowStub = defineComponent({
  name: 'OAuthAuthorizationFlow',
  template: '<div />',
})

const stubs = {
  BaseDialog: BaseDialogStub,
  OAuthAuthorizationFlow: OAuthAuthorizationFlowStub,
  Icon: true,
  PlatformIcon: true,
  Select: true,
  ProxySelector: true,
  ProxyAdBanner: true,
  GroupSelector: true,
  ModelWhitelistSelector: true,
  ConfirmDialog: true,
  QuotaLimitCard: true,
}

function mountCreate() {
  return mount(CreateAccountModal, {
    props: { show: true, proxies: [], groups: [] },
    global: { stubs },
  })
}

function mountEdit(account: any) {
  return mount(EditAccountModal, {
    props: { show: true, account, proxies: [], groups: [] },
    global: { stubs },
  })
}

function buildAccount(extra = {}) {
  return {
    id: 1,
    name: 'Test Account',
    notes: '',
    platform: 'openai',
    type: 'apikey',
    credentials: { api_key: 'sk-test', base_url: 'https://api.openai.com' },
    extra,
    proxy_id: null,
    concurrency: 1,
    priority: 1,
    rate_multiplier: 1,
    status: 'active',
    group_ids: [],
    expires_at: null,
    auto_pause_on_expired: false,
  } as any
}

async function selectPlatformAndType(wrapper: ReturnType<typeof mountCreate>, platform: string) {
  const platformButton = wrapper.findAll('button').find((b) => b.text().includes(platform))
  expect(platformButton).toBeDefined()
  await platformButton?.trigger('click')
  if (platform === 'OpenAI') {
    const typeButton = wrapper.findAll('button').find((b) => b.text().includes('API Key'))
    await typeButton?.trigger('click')
  }
}

describe('Account RPM Limit', () => {
  beforeEach(() => {
    authIsSimpleMode.value = true
    createAccountMock.mockReset().mockResolvedValue({ id: 1, platform: 'openai', type: 'apikey' })
    updateAccountMock.mockReset().mockResolvedValue({})
  })

  afterEach(() => vi.clearAllMocks())

  describe('CreateAccountModal', () => {
    it('omits rpm_limit from extra when left at 0', async () => {
      const wrapper = mountCreate()
      await selectPlatformAndType(wrapper, 'OpenAI')
      await wrapper.get('form#create-account-form input[type="text"]').setValue('No RPM Account')
      await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-test')
      const rpmInput = wrapper.get<HTMLInputElement>('[data-testid="account-rpm-limit"]')
      expect(rpmInput.element.value).toBe('0')
      await wrapper.get('form#create-account-form').trigger('submit.prevent')
      await flushPromises()

      expect(createAccountMock).toHaveBeenCalledTimes(1)
      expect(createAccountMock.mock.calls[0]?.[0]?.extra).not.toHaveProperty('rpm_limit')
    })

    it('writes rpm_limit to extra when set above 0', async () => {
      const wrapper = mountCreate()
      await selectPlatformAndType(wrapper, 'OpenAI')
      await wrapper.get('form#create-account-form input[type="text"]').setValue('Capped Account')
      await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-test')
      await wrapper.get('[data-testid="account-rpm-limit"]').setValue('500')
      await wrapper.get('form#create-account-form').trigger('submit.prevent')
      await flushPromises()

      expect(createAccountMock).toHaveBeenCalledTimes(1)
      expect(createAccountMock.mock.calls[0]?.[0]?.extra?.rpm_limit).toBe(500)
    })

    it('shows validation error for negative rpm_limit', async () => {
      const wrapper = mountCreate()
      await selectPlatformAndType(wrapper, 'OpenAI')
      await wrapper.get('[data-testid="account-rpm-limit"]').setValue('-10')

      const error = wrapper.get('[data-testid="account-rpm-limit-error"]')
      expect(error.text()).toContain('admin.accounts.rpmLimitInvalid')
      await wrapper.get('form#create-account-form').trigger('submit.prevent')
      await flushPromises()
      expect(createAccountMock).not.toHaveBeenCalled()
    })

    it('shows validation error for rpm_limit above 10000', async () => {
      const wrapper = mountCreate()
      await selectPlatformAndType(wrapper, 'OpenAI')
      await wrapper.get('[data-testid="account-rpm-limit"]').setValue('10001')

      expect(wrapper.get('[data-testid="account-rpm-limit-error"]').text()).toContain('admin.accounts.rpmLimitInvalid')
    })

    it('accepts 10000 as a valid rpm_limit', async () => {
      const wrapper = mountCreate()
      await selectPlatformAndType(wrapper, 'OpenAI')
      await wrapper.get('form#create-account-form input[type="text"]').setValue('Max RPM')
      await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-test')
      await wrapper.get('[data-testid="account-rpm-limit"]').setValue('10000')
      await wrapper.get('form#create-account-form').trigger('submit.prevent')
      await flushPromises()

      expect(createAccountMock).toHaveBeenCalledTimes(1)
      expect(createAccountMock.mock.calls[0]?.[0]?.extra?.rpm_limit).toBe(10000)
    })

    it('applies rpm_limit to Anthropic accounts', async () => {
      const wrapper = mountCreate()
      const anthropic = wrapper.findAll('button').find((b) => b.text().includes('admin.accounts.claudeConsole'))
      await anthropic?.trigger('click')
      await wrapper.get('form#create-account-form input[type="text"]').setValue('Claude Account')
      await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-ant-test')
      await wrapper.get('[data-testid="account-rpm-limit"]').setValue('1200')
      await wrapper.get('form#create-account-form').trigger('submit.prevent')
      await flushPromises()

      expect(createAccountMock).toHaveBeenCalledTimes(1)
      expect(createAccountMock.mock.calls[0]?.[0]?.extra?.rpm_limit).toBe(1200)
    })
  })

  describe('EditAccountModal', () => {
    it('loads existing rpm_limit from account extra', () => {
      const account = buildAccount({ rpm_limit: 800 })
      const wrapper = mountEdit(account)
      const input = wrapper.get<HTMLInputElement>('[data-testid="account-rpm-limit"]')
      expect(input.element.value).toBe('800')
    })

    it('defaults to 0 when rpm_limit is not in extra', () => {
      const account = buildAccount()
      const wrapper = mountEdit(account)
      const input = wrapper.get<HTMLInputElement>('[data-testid="account-rpm-limit"]')
      expect(input.element.value).toBe('0')
    })

    it('keeps the stored rpm_limit when saving without touching it', async () => {
      const account = buildAccount({ rpm_limit: 300 })
      const wrapper = mountEdit(account)
      await wrapper.get('form#edit-account-form').trigger('submit.prevent')

      expect(updateAccountMock).toHaveBeenCalledTimes(1)
      const extra = updateAccountMock.mock.calls[0]?.[1]?.extra
      // Either extra is omitted (backend keeps it) or it carries the same value.
      if (extra !== undefined) expect(extra.rpm_limit).toBe(300)
    })

    it('does not send extra at all for an unchanged OAuth account', async () => {
      const account = { ...buildAccount({ rpm_limit: 300 }), platform: 'gemini', type: 'oauth', credentials: {} }
      const wrapper = mountEdit(account)
      await wrapper.get('form#edit-account-form').trigger('submit.prevent')

      expect(updateAccountMock).toHaveBeenCalledTimes(1)
      expect(updateAccountMock.mock.calls[0]?.[1]?.extra).toBeUndefined()
    })

    it('saves rpm_limit for an OAuth account that has no other extra changes', async () => {
      const account = { ...buildAccount({ keep_me: true }), platform: 'gemini', type: 'oauth', credentials: {} }
      const wrapper = mountEdit(account)
      await wrapper.get('[data-testid="account-rpm-limit"]').setValue('60')
      await wrapper.get('form#edit-account-form').trigger('submit.prevent')

      expect(updateAccountMock.mock.calls[0]?.[1]?.extra).toEqual({ keep_me: true, rpm_limit: 60 })
    })

    it('writes rpm_limit to extra when changed', async () => {
      const account = buildAccount({ rpm_limit: 100 })
      const wrapper = mountEdit(account)
      await wrapper.get('[data-testid="account-rpm-limit"]').setValue('700')
      await wrapper.get('form#edit-account-form').trigger('submit.prevent')

      expect(updateAccountMock).toHaveBeenCalledTimes(1)
      expect(updateAccountMock.mock.calls[0]?.[1]?.extra?.rpm_limit).toBe(700)
    })

    it('sends explicit rpm_limit 0 when a limited account becomes unlimited', async () => {
      // The backend keeps the stored limit when extra omits rpm_limit, so
      // dropping the key would leave the old cap in force.
      const account = buildAccount({ rpm_limit: 450, keep_me: 'x' })
      const wrapper = mountEdit(account)
      expect(wrapper.get<HTMLInputElement>('[data-testid="account-rpm-limit"]').element.value).toBe('450')
      await wrapper.get('[data-testid="account-rpm-limit"]').setValue('0')
      await wrapper.get('form#edit-account-form').trigger('submit.prevent')

      expect(updateAccountMock).toHaveBeenCalledTimes(1)
      const extra = updateAccountMock.mock.calls[0]?.[1]?.extra
      expect(extra).toHaveProperty('rpm_limit', 0)
      expect(Number.isInteger(extra.rpm_limit)).toBe(true)
      expect(extra.keep_me).toBe('x')
    })

    it('sends explicit rpm_limit 0 when the field is cleared on a limited OAuth account', async () => {
      const account = { ...buildAccount({ rpm_limit: 30, keep_me: true }), platform: 'gemini', type: 'oauth', credentials: {} }
      const wrapper = mountEdit(account)
      await wrapper.get('[data-testid="account-rpm-limit"]').setValue('')
      await wrapper.get('form#edit-account-form').trigger('submit.prevent')

      expect(updateAccountMock.mock.calls[0]?.[1]?.extra).toEqual({ keep_me: true, rpm_limit: 0 })
    })

    it('reads the top-level rpm_limit from the account DTO', async () => {
      const account = { ...buildAccount({}), platform: 'gemini', type: 'oauth', credentials: {}, rpm_limit: 75 }
      const wrapper = mountEdit(account)
      expect(wrapper.get<HTMLInputElement>('[data-testid="account-rpm-limit"]').element.value).toBe('75')

      // Unchanged: no extra is sent, so the backend keeps the stored limit.
      await wrapper.get('form#edit-account-form').trigger('submit.prevent')
      expect(updateAccountMock.mock.calls[0]?.[1]?.extra).toBeUndefined()
    })

    it('does not send rpm_limit for an unlimited account left at 0', async () => {
      const account = { ...buildAccount({}), platform: 'gemini', type: 'oauth', credentials: {} }
      const wrapper = mountEdit(account)
      await wrapper.get('form#edit-account-form').trigger('submit.prevent')

      expect(updateAccountMock.mock.calls[0]?.[1]?.extra).toBeUndefined()
    })

    it('preserves unrelated extra fields when updating rpm_limit', async () => {
      const account = buildAccount({
        rpm_limit: 200,
        openai_compact_mode: 'adaptive',
        upstream_request_id_header: 'X-Request-ID',
      })
      const wrapper = mountEdit(account)
      await wrapper.get('[data-testid="account-rpm-limit"]').setValue('999')
      await wrapper.get('form#edit-account-form').trigger('submit.prevent')

      expect(updateAccountMock).toHaveBeenCalledTimes(1)
      const extra = updateAccountMock.mock.calls[0]?.[1]?.extra
      expect(extra?.rpm_limit).toBe(999)
      expect(extra?.openai_compact_mode).toBe('adaptive')
      expect(extra?.upstream_request_id_header).toBe('X-Request-ID')
    })

    it('shows validation error for negative rpm_limit in edit', async () => {
      const account = buildAccount({ rpm_limit: 100 })
      const wrapper = mountEdit(account)
      await wrapper.get('[data-testid="account-rpm-limit"]').setValue('-5')

      expect(wrapper.find('[data-testid="account-rpm-limit-error"]').exists()).toBe(true)
      await wrapper.get('form#edit-account-form').trigger('submit.prevent')
      await flushPromises()
      expect(updateAccountMock).not.toHaveBeenCalled()
    })
  })
})
