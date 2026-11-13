import { defineComponent } from 'vue'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const { createAccountMock, showErrorMock, exchangeAuthCodeMock, generateAuthUrlMock } = vi.hoisted(() => ({
  createAccountMock: vi.fn(),
  showErrorMock: vi.fn(),
  exchangeAuthCodeMock: vi.fn(),
  generateAuthUrlMock: vi.fn()
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: showErrorMock,
    showSuccess: vi.fn(),
    showWarning: vi.fn(),
    showInfo: vi.fn()
  })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    isSimpleMode: true
  })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      create: createAccountMock,
      probeUpstreamBilling: vi.fn().mockResolvedValue({}),
      syncUpstreamModels: vi.fn().mockResolvedValue({ models: [], metadata: {} }),
      checkMixedChannelRisk: vi.fn().mockResolvedValue({ has_risk: false }),
      importCodexSession: vi.fn(),
      createOpenAICodexPAT: vi.fn()
    },
    settings: {
      getWebSearchEmulationConfig: vi.fn().mockResolvedValue({ enabled: false, providers: [] }),
      getSettings: vi.fn().mockResolvedValue({})
    },
    tlsFingerprintProfiles: {
      list: vi.fn().mockResolvedValue([])
    }
  }
}))

vi.mock('@/api/admin/accounts', () => ({
  getAntigravityDefaultModelMapping: vi.fn().mockResolvedValue([])
}))

vi.mock('@/composables/useOpenAIOAuth', async () => {
  const { ref } = await import('vue')
  const { OPENAI_EXCEL_OAUTH_CLIENT_ID } = await import('@/utils/openaiOAuthClient')
  const sessionId = ref('sess-excel-1')
  const oauthState = ref('state-excel-1')
  const loading = ref(false)
  const error = ref('')
  const authUrl = ref('https://auth.openai.com/oauth/authorize')
  return {
    useOpenAIOAuth: () => ({
      sessionId,
      oauthState,
      loading,
      error,
      authUrl,
      generateAuthUrl: generateAuthUrlMock,
      exchangeAuthCode: exchangeAuthCodeMock,
      validateRefreshToken: vi.fn(),
      buildCredentials: () => ({
        access_token: 'at-bps',
        refresh_token: 'rt-bps',
        client_id: OPENAI_EXCEL_OAUTH_CLIENT_ID,
        chatgpt_account_id: 'ws-excel'
      }),
      buildExtraInfo: () => ({ email: 'bps@example.com' }),
      resetState: () => {
        error.value = ''
      }
    })
  }
})

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

import CreateAccountModal from '../CreateAccountModal.vue'
import {
  OPENAI_EXCEL_CALLBACK_ORIGIN,
  OPENAI_EXCEL_CALLBACK_PATH,
  OPENAI_EXCEL_OAUTH_CLIENT_ID
} from '@/utils/openaiOAuthClient'

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: { show: { type: Boolean, default: false } },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>'
})

const SelectStub = defineComponent({
  name: 'SelectStub',
  props: {
    modelValue: { type: [String, Number, Boolean, null], default: '' },
    options: { type: Array, default: () => [] }
  },
  emits: ['update:modelValue'],
  template: `
    <select v-bind="$attrs" :value="modelValue" @change="$emit('update:modelValue', $event.target.value)">
      <option v-for="option in options" :key="option.value" :value="option.value">{{ option.label }}</option>
    </select>
  `
})

const OAuthAuthorizationFlowStub = defineComponent({
  name: 'OAuthAuthorizationFlow',
  data: () => ({
    authCode: `${OPENAI_EXCEL_CALLBACK_ORIGIN}${OPENAI_EXCEL_CALLBACK_PATH}?code=code-1&state=state-excel-1`,
    oauthState: 'state-excel-1',
    inputMethod: 'manual'
  }),
  methods: {
    reset() {}
  },
  template: '<div data-testid="oauth-step-two" />'
})

async function selectButtonByText(wrapper: VueWrapper<any>, label: string) {
  const btn = wrapper.findAll('button').find((b) => b.text().includes(label))
  expect(btn, `Expected button containing "${label}"`).toBeDefined()
  await btn!.trigger('click')
}

function mountCreateModal() {
  return mount(CreateAccountModal, {
    props: { show: true, proxies: [], groups: [] },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        Select: SelectStub,
        OAuthAuthorizationFlow: OAuthAuthorizationFlowStub,
        ProxySelector: true,
        GroupSelector: true,
        Icon: true,
        PlatformIcon: true,
        HelpTooltip: true
      }
    }
  })
}

describe('CreateAccountModal BPS options, explicit models, and upstream client timezone', () => {
  beforeEach(() => {
    createAccountMock.mockReset().mockResolvedValue({ id: 901, platform: 'openai', type: 'oauth' })
    exchangeAuthCodeMock.mockReset().mockResolvedValue({ access_token: 'at-bps' })
    generateAuthUrlMock.mockReset().mockResolvedValue('https://auth.openai.com/oauth/authorize')
    showErrorMock.mockReset()
  })

  it('clears auto-filled Codex models when selecting Excel (BPS), hides flattenNamespaces, and creates BPS account with dedicated options + timezone', async () => {
    const wrapper = mountCreateModal()
    await selectButtonByText(wrapper, 'OpenAI')

    // Native Codex OAuth initially shows flattenNamespaces
    expect(wrapper.find('[data-testid="create-openai-flatten-namespaces-toggle"]').exists()).toBe(true)
    await wrapper.get('[data-testid="create-openai-flatten-namespaces-toggle"]').trigger('click')

    // Switch login client to official Excel (BPS)
    await wrapper.get('[data-testid="openai-oauth-client-excel"]').trigger('click')

    expect(wrapper.find('[data-testid="create-openai-flatten-namespaces-toggle"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="openai-bps-options-section"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="create-bps-sync-unavailable"]').exists()).toBe(true)
    // Untouched Codex default models were cleared -> explicit model hint is visible, never supportsAllModels
    expect(wrapper.find('[data-testid="create-bps-explicit-models-hint"]').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('admin.accounts.supportsAllModels')

    // Prefill recommended BPS models
    await wrapper.get('[data-testid="create-bps-prefill-models"]').trigger('click')
    expect(wrapper.find('[data-testid="create-bps-explicit-models-hint"]').exists()).toBe(false)

    // Configure BPS options + upstream client timezone
    await wrapper.get('[data-testid="openai-bps-skip-historical-encrypted-toggle"]').trigger('click')
    await wrapper.get('[data-testid="openai-bps-hosted-tools-policy-select"]').setValue('reject')
    await wrapper.get('[data-testid="create-upstream-client-timezone-mode"]').setValue('manual')
    await wrapper.get('form#create-account-form input[type="text"]').setValue('bps-new-01')

    // Empty manual timezone blocks advancing to OAuth step 2
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()
    expect(wrapper.find('[data-testid="oauth-step-two"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="upstream-client-timezone-error"]').text()).toContain(
      'admin.accounts.upstreamClientTimezone.errorRequired'
    )

    // Enter valid IANA timezone, then toggle to auto (manual value stays in memory, auto is emitted)
    await wrapper.get('[data-testid="create-upstream-client-timezone-input"]').setValue('Asia/Singapore')
    await wrapper.get('[data-testid="create-upstream-client-timezone-mode"]').setValue('auto')
    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(wrapper.find('[data-testid="oauth-step-two"]').exists()).toBe(true)
    await selectButtonByText(wrapper, 'admin.accounts.oauth.completeAuth')
    await flushPromises()

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    const payload = createAccountMock.mock.calls[0]?.[0]
    expect(payload?.credentials?.client_id).toBe(OPENAI_EXCEL_OAUTH_CLIENT_ID)
    expect(payload?.credentials?.model_mapping).toEqual({
      'gpt-6-astra': 'gpt-6-astra',
      'gpt-6.1-sol': 'gpt-6.1-sol'
    })
    expect(payload?.extra?.openai_bps_skip_historical_encrypted).toBe(true)
    expect(payload?.extra?.openai_bps_hosted_tools_policy).toBe('reject')
    expect(payload?.extra?.upstream_client_timezone_mode).toBe('auto')
    expect(payload?.extra).not.toHaveProperty('upstream_client_timezone')
    expect(payload?.extra).not.toHaveProperty('openai_responses_flatten_namespaces')
  })

  it('supports manual upstream client timezone on OpenAI API Key accounts', async () => {
    const wrapper = mountCreateModal()
    await selectButtonByText(wrapper, 'OpenAI')
    await selectButtonByText(wrapper, 'API Key')

    await wrapper.get('form#create-account-form input[type="text"]').setValue('openai-key-tz')
    await wrapper.get('form#create-account-form input[type="password"]').setValue('sk-proj-test')
    await wrapper.get('[data-testid="create-upstream-client-timezone-mode"]').setValue('manual')
    await wrapper.get('[data-testid="create-upstream-client-timezone-input"]').setValue('America/New_York')

    await wrapper.get('form#create-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    const payload = createAccountMock.mock.calls[0]?.[0]
    expect(payload?.type).toBe('apikey')
    expect(payload?.extra?.upstream_client_timezone_mode).toBe('manual')
    expect(payload?.extra?.upstream_client_timezone).toBe('America/New_York')
  })
})
