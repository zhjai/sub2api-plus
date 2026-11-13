import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'

const { updateAccountMock, checkMixedChannelRiskMock, showErrorMock } = vi.hoisted(() => ({
  updateAccountMock: vi.fn(),
  checkMixedChannelRiskMock: vi.fn(),
  showErrorMock: vi.fn()
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: showErrorMock,
    showSuccess: vi.fn(),
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
      update: updateAccountMock,
      checkMixedChannelRisk: checkMixedChannelRiskMock
    },
    settings: {
      getWebSearchEmulationConfig: vi.fn().mockResolvedValue({ enabled: false, providers: [] }),
      getSettings: vi.fn().mockResolvedValue({})
    },
    tlsFingerprintProfiles: {
      list: vi.fn().mockResolvedValue([])
    },
    usage: {
      searchApiKeys: vi.fn()
    }
  }
}))

vi.mock('@/api/admin/accounts', () => ({
  getAntigravityDefaultModelMapping: vi.fn()
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

import EditAccountModal from '../EditAccountModal.vue'
import { OPENAI_EXCEL_OAUTH_CLIENT_ID } from '@/utils/openaiOAuthClient'

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

// Stale Codex-only / native-transport keys that a BPS edit must not re-save.
const staleCodexExtra = {
  openai_oauth_responses_websockets_v2_mode: 'ctx_pool',
  openai_oauth_responses_websockets_v2_enabled: true,
  openai_passthrough: true,
  openai_responses_flatten_namespaces: true,
  codex_cli_only: true,
  codex_cli_only_allow_app_server: true,
  codex_fingerprint_mode: 'full',
  codex_identity_mode: 'preserve_client',
  codex_privacy_mode: 'randomized',
  codex_image_generation_bridge: true,
  auto_pause_5h_threshold: 0.8,
  auto_pause_7d_disabled: true,
  auto_reset_credit_enabled: true,
  auto_reset_credit_5h_threshold: 0.9,
  auto_reset_credit_7d_threshold: 0.95
}

function buildOAuthAccount(
  clientId: string | undefined,
  extra: Record<string, unknown> = {},
  overrides: Record<string, unknown> = {}
) {
  const credentials: Record<string, unknown> = {
    access_token: 'at',
    chatgpt_account_id: 'workspace-1',
    model_mapping: { 'gpt-5.5': 'gpt-5.5' },
    compact_model_mapping: { 'gpt-5.5': 'gpt-5.5-compact' },
    ...((overrides.credentials as Record<string, unknown> | undefined) || {})
  }
  if (clientId) credentials.client_id = clientId
  return {
    id: 11,
    name: 'OpenAI OAuth',
    notes: '',
    platform: 'openai',
    type: 'oauth',
    parent_account_id: null,
    ...overrides,
    credentials,
    extra: { unrelated_marker: 'keep', openai_compact_mode: 'force_on', ...extra },
    proxy_id: null,
    concurrency: 1,
    priority: 1,
    rate_multiplier: 1,
    status: 'active',
    group_ids: [],
    expires_at: null,
    auto_pause_on_expired: false
  } as any
}

function mountModal(account: any) {
  return mount(EditAccountModal, {
    props: { show: true, account, proxies: [], groups: [] },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        Select: SelectStub,
        Icon: true,
        ProxySelector: true,
        GroupSelector: true,
        ModelWhitelistSelector: true
      }
    }
  })
}

const codexOnlySelectors = [
  '[data-testid="edit-codex-identity"]',
  '[data-testid="edit-codex-fingerprint-mode-select"]',
  '[data-testid="edit-openai-ws-mode-select"]',
  '[data-testid="edit-openai-flatten-namespaces-toggle"]',
  '[data-testid="codex-privacy"]',
  '[data-testid="codex-image-tool-inherit"]',
  '[data-testid="auto-pause-5h-disabled"]',
  '[data-testid="auto-reset-credit-settings"]'
]

describe('EditAccountModal BPS (Excel client) accounts and upstream client timezone', () => {
  beforeEach(() => {
    updateAccountMock.mockReset().mockImplementation(async (_id: number, payload: any) => payload)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })
    showErrorMock.mockReset()
  })

  it('shows the BPS identity and dedicated options while hiding Codex-only, flattenNamespaces, and native quota controls', () => {
    const wrapper = mountModal(buildOAuthAccount(OPENAI_EXCEL_OAUTH_CLIENT_ID))

    const identity = wrapper.get('[data-testid="edit-openai-bps-identity"]')
    expect(identity.text()).toContain('admin.accounts.oauth.openai.bps.identityTitle')
    expect(identity.text()).toContain('admin.accounts.oauth.openai.bps.scopeNote')
    expect(wrapper.get('[data-testid="edit-openai-bps-client-id"]').text()).toBe(OPENAI_EXCEL_OAUTH_CLIENT_ID)
    expect(identity.text()).toContain('workspace-1')
    expect(wrapper.find('[data-testid="openai-bps-options-section"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="openai-bps-client-tools-hint"]').text()).toContain(
      'admin.accounts.bpsAccount.options.clientToolsHint'
    )
    for (const selector of codexOnlySelectors) {
      expect(wrapper.find(selector).exists(), selector).toBe(false)
    }
  })

  it('keeps Codex-only controls for an ordinary Codex OAuth account', () => {
    const wrapper = mountModal(buildOAuthAccount(undefined))

    expect(wrapper.find('[data-testid="edit-openai-bps-identity"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="openai-bps-options-section"]').exists()).toBe(false)
    for (const selector of codexOnlySelectors) {
      expect(wrapper.find(selector).exists(), selector).toBe(true)
    }
  })

  it('clears stale native options (including flattenNamespaces and auto-reset) with their existing off semantics and keeps the Excel client', async () => {
    const wrapper = mountModal(buildOAuthAccount(OPENAI_EXCEL_OAUTH_CLIENT_ID, staleCodexExtra))

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    const payload = updateAccountMock.mock.calls[0]?.[1]
    const extra = payload?.extra ?? {}
    expect(extra.openai_oauth_responses_websockets_v2_mode).toBe('off')
    expect(extra.openai_oauth_responses_websockets_v2_enabled).toBe(false)
    expect(extra).not.toHaveProperty('openai_passthrough')
    expect(extra).not.toHaveProperty('openai_responses_flatten_namespaces')
    expect(extra.codex_cli_only).toBe(false)
    expect(extra).not.toHaveProperty('codex_cli_only_allow_app_server')
    expect(extra).not.toHaveProperty('codex_fingerprint_mode')
    expect(extra).not.toHaveProperty('codex_identity_mode')
    expect(extra.codex_privacy_mode).toBe('off')
    expect(extra).not.toHaveProperty('codex_image_generation_bridge')
    expect(extra).not.toHaveProperty('auto_pause_5h_threshold')
    expect(extra).not.toHaveProperty('auto_pause_7d_disabled')
    expect(extra).not.toHaveProperty('auto_reset_credit_enabled')
    expect(extra).not.toHaveProperty('auto_reset_credit_5h_threshold')
    expect(extra).not.toHaveProperty('auto_reset_credit_7d_threshold')
    // Unrelated and hidden-but-inert settings are kept as stored.
    expect(extra.unrelated_marker).toBe('keep')
    expect(extra.openai_compact_mode).toBe('force_on')

    const credentials = payload?.credentials ?? {}
    expect(credentials.client_id).toBe(OPENAI_EXCEL_OAUTH_CLIENT_ID)
    expect(credentials.model_mapping).toEqual({ 'gpt-5.5': 'gpt-5.5' })
    expect(credentials.compact_model_mapping).toEqual({ 'gpt-5.5': 'gpt-5.5-compact' })
  })

  it('leaves a Codex account with the same stored options unchanged', async () => {
    const wrapper = mountModal(
      buildOAuthAccount(undefined, {
        openai_oauth_responses_websockets_v2_mode: 'ctx_pool',
        openai_oauth_responses_websockets_v2_enabled: true,
        openai_responses_flatten_namespaces: true,
        codex_fingerprint_mode: 'full'
      })
    )

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    const payload = updateAccountMock.mock.calls[0]?.[1]
    expect(payload?.extra?.openai_oauth_responses_websockets_v2_mode).toBe('ctx_pool')
    expect(payload?.extra?.openai_responses_flatten_namespaces).toBe(true)
    expect(payload?.extra?.codex_fingerprint_mode).toBe('full')
    expect(payload?.credentials).not.toHaveProperty('client_id')
  })

  it('saves BPS dedicated options and clears previously stored non-default BPS options explicitly', async () => {
    const wrapper = mountModal(buildOAuthAccount(OPENAI_EXCEL_OAUTH_CLIENT_ID))

    expect(wrapper.get('[data-testid="openai-bps-skip-historical-encrypted-warning"]').text()).toContain(
      'admin.accounts.bpsAccount.options.skipHistoricalEncryptedWarning'
    )
    await wrapper.get('[data-testid="openai-bps-skip-historical-encrypted-toggle"]').trigger('click')
    await wrapper.get('[data-testid="openai-bps-hosted-tools-policy-select"]').setValue('reject')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    const savedExtra = updateAccountMock.mock.calls[0]?.[1]?.extra ?? {}
    expect(savedExtra.openai_bps_skip_historical_encrypted).toBe(true)
    expect(savedExtra.openai_bps_hosted_tools_policy).toBe('reject')

    // Reopen with those stored values and toggle back to defaults -> writes explicit false / 'omit'
    updateAccountMock.mockClear()
    const wrapper2 = mountModal(
      buildOAuthAccount(OPENAI_EXCEL_OAUTH_CLIENT_ID, {
        openai_bps_skip_historical_encrypted: true,
        openai_bps_hosted_tools_policy: 'reject'
      })
    )
    await wrapper2.get('[data-testid="openai-bps-skip-historical-encrypted-toggle"]').trigger('click')
    await wrapper2.get('[data-testid="openai-bps-hosted-tools-policy-select"]').setValue('omit')
    await wrapper2.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    const clearedExtra = updateAccountMock.mock.calls[0]?.[1]?.extra ?? {}
    expect(clearedExtra.openai_bps_skip_historical_encrypted).toBe(false)
    expect(clearedExtra.openai_bps_hosted_tools_policy).toBe('omit')
  })

  it('requires explicit BPS model configuration (never supportsAllModels) and prefills gpt-6-astra and gpt-6.1-sol', async () => {
    const wrapper = mountModal(
      buildOAuthAccount(
        OPENAI_EXCEL_OAUTH_CLIENT_ID,
        {},
        { credentials: { model_mapping: {} } }
      )
    )

    expect(wrapper.find('[data-testid="edit-bps-sync-unavailable"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="edit-bps-explicit-models-hint"]').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('admin.accounts.supportsAllModels')

    await wrapper.get('[data-testid="edit-bps-prefill-models"]').trigger('click')
    expect(wrapper.find('[data-testid="edit-bps-explicit-models-hint"]').exists()).toBe(false)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    const credentials = updateAccountMock.mock.calls[0]?.[1]?.credentials ?? {}
    expect(credentials.model_mapping).toEqual({
      'gpt-6-astra': 'gpt-6-astra',
      'gpt-6.1-sol': 'gpt-6.1-sol'
    })
  })

  it('validates upstream client timezone, retains manual draft across mode toggles, and renders runtime status without pretending unsaved edits are active', async () => {
    const wrapper = mountModal(
      buildOAuthAccount(
        OPENAI_EXCEL_OAUTH_CLIENT_ID,
        {
          upstream_client_timezone_mode: 'auto'
        },
        {
          upstream_client_timezone: {
            mode: 'auto',
            status: 'ready',
            timezone: 'Asia/Tokyo',
            source: 'proxy_exit',
            updated_at: '2026-11-13T04:00:00Z'
          }
        }
      )
    )

    // Runtime DTO reflects saved server state ('ready' / 'Asia/Tokyo' / 'proxy_exit') without unsaved note initially
    expect(wrapper.get('[data-testid="upstream-client-timezone-runtime-zone"]').text()).toBe('Asia/Tokyo')
    expect(wrapper.get('[data-testid="upstream-client-timezone-runtime-source"]').text()).toContain(
      'admin.accounts.upstreamClientTimezone.source.proxy_exit'
    )
    expect(wrapper.find('[data-testid="upstream-client-timezone-unsaved-note"]').exists()).toBe(false)

    // Switch to manual mode -> unsaved note appears while runtime zone still shows persisted Asia/Tokyo
    await wrapper.get('[data-testid="edit-upstream-client-timezone-mode"]').setValue('manual')
    expect(wrapper.find('[data-testid="upstream-client-timezone-unsaved-note"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="upstream-client-timezone-runtime-zone"]').text()).toBe('Asia/Tokyo')

    // Invalid raw offset is blocked before API call
    await wrapper.get('[data-testid="edit-upstream-client-timezone-input"]').setValue('+08:00')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()
    expect(updateAccountMock).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="upstream-client-timezone-error"]').text()).toContain(
      'admin.accounts.upstreamClientTimezone.errorInvalid'
    )

    // Enter valid IANA zone, toggle to auto -> manual draft is kept in memory, only mode='auto' is sent
    await wrapper.get('[data-testid="edit-upstream-client-timezone-input"]').setValue('Europe/Berlin')
    await wrapper.get('[data-testid="edit-upstream-client-timezone-mode"]').setValue('auto')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    const autoExtra = updateAccountMock.mock.calls[0]?.[1]?.extra ?? {}
    expect(autoExtra.upstream_client_timezone_mode).toBe('auto')
    expect(autoExtra).not.toHaveProperty('upstream_client_timezone')

    // Switch back to manual -> 'Europe/Berlin' is still in the input and submitted cleanly
    updateAccountMock.mockClear()
    await wrapper.get('[data-testid="edit-upstream-client-timezone-mode"]').setValue('manual')
    expect(
      (wrapper.get('[data-testid="edit-upstream-client-timezone-input"]').element as HTMLInputElement).value
    ).toBe('Europe/Berlin')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    const manualExtra = updateAccountMock.mock.calls[0]?.[1]?.extra ?? {}
    expect(manualExtra.upstream_client_timezone_mode).toBe('manual')
    expect(manualExtra.upstream_client_timezone).toBe('Europe/Berlin')
  })
})
