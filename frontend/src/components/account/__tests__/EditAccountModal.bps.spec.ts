import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'

const {
  updateAccountMock,
  checkMixedChannelRiskMock,
  syncUpstreamModelsMock,
  refreshOpenAIAccountTokenMock,
  showErrorMock,
  showSuccessMock,
  showWarningMock
} = vi.hoisted(() => ({
  updateAccountMock: vi.fn(),
  checkMixedChannelRiskMock: vi.fn(),
  syncUpstreamModelsMock: vi.fn(),
  refreshOpenAIAccountTokenMock: vi.fn(),
  showErrorMock: vi.fn(),
  showSuccessMock: vi.fn(),
  showWarningMock: vi.fn()
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: showErrorMock,
    showSuccess: showSuccessMock,
    showWarning: showWarningMock,
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
      checkMixedChannelRisk: checkMixedChannelRiskMock,
      syncUpstreamModels: syncUpstreamModelsMock,
      refreshOpenAIAccountToken: refreshOpenAIAccountTokenMock
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
import {
  OPENAI_EXCEL_BPS_CREDENTIALS_KEY,
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

// Stale native-Codex-only keys that a BPS-only edit must clear, while preserving CLI-only & codex_privacy_mode.
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
    access_token: 'at-secret-never-render',
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

function mountModal(
  account: any,
  extraProps: { proxies?: any[]; groups?: any[]; unstubWhitelist?: boolean } = {}
) {
  return mount(EditAccountModal, {
    props: {
      show: true,
      account,
      proxies: extraProps.proxies ?? [],
      groups: extraProps.groups ?? []
    },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        Select: SelectStub,
        Icon: true,
        ProxySelector: true,
        GroupSelector: true,
        ReAuthAccountModal: true,
        ModelWhitelistSelector: extraProps.unstubWhitelist ? false : true
      }
    }
  })
}

const nativeCodexOnlySelectors = [
  '[data-testid="edit-codex-identity"]',
  '[data-testid="edit-codex-fingerprint-mode-select"]',
  '[data-testid="edit-openai-ws-mode-select"]',
  '[data-testid="edit-openai-flatten-namespaces-toggle"]',
  '[data-testid="codex-image-tool-inherit"]',
  '[data-testid="auto-pause-5h-disabled"]',
  '[data-testid="auto-reset-credit-settings"]'
]

describe('EditAccountModal BPS parity, dual-grant controls, and upstream client timezone', () => {
  beforeEach(() => {
    updateAccountMock.mockReset().mockImplementation(async (_id: number, payload: any) => payload)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })
    syncUpstreamModelsMock.mockReset().mockResolvedValue({
      models: ['gpt-6-astra', 'gpt-6.1-sol'],
      discovery_source: 'bps_access'
    })
    refreshOpenAIAccountTokenMock.mockReset()
    showErrorMock.mockReset()
    showSuccessMock.mockReset()
    showWarningMock.mockReset()
  })

  it('shows BPS identity, BPS channel options, local CLI-only, and fingerprint privacy while hiding native-only transport/quota controls on BPS-only accounts', () => {
    const wrapper = mountModal(buildOAuthAccount(OPENAI_EXCEL_OAUTH_CLIENT_ID))

    const identity = wrapper.get('[data-testid="edit-openai-bps-identity"]')
    expect(identity.text()).toContain('admin.accounts.oauth.openai.bps.identityTitle')
    expect(identity.text()).toContain('admin.accounts.oauth.openai.bps.scopeNote')
    expect(wrapper.get('[data-testid="edit-openai-bps-client-id"]').text()).toBe(OPENAI_EXCEL_OAUTH_CLIENT_ID)
    expect(identity.text()).toContain('workspace-1')
    expect(wrapper.text()).not.toContain('at-secret-never-render')
    expect(wrapper.find('[data-testid="openai-bps-options-section"]').exists()).toBe(true)
    // Local CLI-only and fingerprint privacy apply to BPS-only accounts too
    expect(wrapper.find('[data-testid="codex-privacy"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="edit-codex-cli-only-toggle"]').exists()).toBe(true)
    for (const selector of nativeCodexOnlySelectors) {
      expect(wrapper.find(selector).exists(), selector).toBe(false)
    }
  })

  it('keeps native Codex controls AND exposes independent BPS grant attachment for an ordinary Codex OAuth account without polluting extra when untouched', async () => {
    const wrapper = mountModal(
      buildOAuthAccount(undefined, {
        openai_oauth_responses_websockets_v2_mode: 'ctx_pool',
        openai_oauth_responses_websockets_v2_enabled: true,
        openai_responses_flatten_namespaces: true,
        codex_fingerprint_mode: 'full'
      })
    )

    expect(wrapper.find('[data-testid="edit-openai-bps-identity"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="openai-bps-options-section"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="openai-bps-independent-grant-card"]').exists()).toBe(true)
    for (const selector of nativeCodexOnlySelectors) {
      expect(wrapper.find(selector).exists(), selector).toBe(true)
    }
    expect(wrapper.find('[data-testid="codex-privacy"]').exists()).toBe(true)

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    const payload = updateAccountMock.mock.calls[0]?.[1]
    expect(payload?.extra?.openai_oauth_responses_websockets_v2_mode).toBe('ctx_pool')
    expect(payload?.extra?.openai_responses_flatten_namespaces).toBe(true)
    expect(payload?.extra?.codex_fingerprint_mode).toBe('full')
    // Untouched native account does not write BPS keys or auto-enable BPS
    expect(payload?.extra).not.toHaveProperty('openai_excel_bps')
    expect(payload?.credentials).not.toHaveProperty('client_id')
  })

  it('clears stale native transport/quota options on BPS-only while preserving local CLI-only and codex_privacy_mode', async () => {
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
    expect(extra).not.toHaveProperty('codex_fingerprint_mode')
    expect(extra).not.toHaveProperty('codex_identity_mode')
    expect(extra).not.toHaveProperty('codex_image_generation_bridge')
    expect(extra).not.toHaveProperty('auto_pause_5h_threshold')
    expect(extra).not.toHaveProperty('auto_pause_7d_disabled')
    expect(extra).not.toHaveProperty('auto_reset_credit_enabled')
    expect(extra).not.toHaveProperty('auto_reset_credit_5h_threshold')
    expect(extra).not.toHaveProperty('auto_reset_credit_7d_threshold')
    // Local CLI-only and fingerprint privacy remain active and persisted on BPS-only
    expect(extra.codex_cli_only).toBe(true)
    expect(extra.codex_cli_only_allow_app_server).toBe(true)
    expect(extra.codex_privacy_mode).toBe('randomized')
    expect(extra.unrelated_marker).toBe('keep')
    expect(extra.openai_compact_mode).toBe('force_on')

    const credentials = payload?.credentials ?? {}
    expect(credentials.client_id).toBe(OPENAI_EXCEL_OAUTH_CLIENT_ID)
    expect(credentials.model_mapping).toEqual({ 'gpt-5.5': 'gpt-5.5' })
    expect(credentials.compact_model_mapping).toEqual({ 'gpt-5.5': 'gpt-5.5-compact' })
  })

  it('defaults hostedToolsPolicy to reject, warns when omit is manually chosen, and persists all BPS channel options', async () => {
    const activeProxy = { id: 7, name: 'hk-node-1', protocol: 'socks5', host: '127.0.0.1', port: 1080, status: 'active' }
    const targetGroup = { id: 21, name: 'openai-fallback', platform: 'openai', status: 'active' }
    const wrapper = mountModal(buildOAuthAccount(OPENAI_EXCEL_OAUTH_CLIENT_ID), {
      proxies: [activeProxy],
      groups: [targetGroup]
    })

    expect(wrapper.find('[data-testid="openai-bps-hosted-tools-omit-warning"]').exists()).toBe(false)
    await wrapper.get('[data-testid="openai-bps-skip-historical-encrypted-toggle"]').trigger('click')
    await wrapper.get('[data-testid="openai-bps-hosted-tools-policy-select"]').setValue('omit')
    expect(wrapper.find('[data-testid="openai-bps-hosted-tools-omit-warning"]').exists()).toBe(true)

    // Enable session proxy pool: empty pool blocks save with setup error (never silent direct)
    await wrapper.get('[data-testid="openai-bps-session-proxy-toggle"]').trigger('click')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()
    expect(updateAccountMock).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="openai-bps-validation-error"]').text()).toContain(
      'admin.accounts.bpsAccount.options.validation.session_proxy_pool_empty'
    )

    // Select active proxy node + 403 group move + explicit mapped BPS models
    await wrapper.get('[data-testid="openai-bps-session-proxy-option-7"] input[type="checkbox"]').setValue(true)
    await wrapper.get('[data-testid="openai-bps-auto-move-403-toggle"]').trigger('click')
    await wrapper.get('[data-testid="openai-bps-target-group-select"]').setValue('21')
    await wrapper.get('[data-testid="openai-bps-models-mode-selected"]').trigger('click')
    await wrapper.get('[data-testid="openai-bps-models-prefill-suggested"]').trigger('click')

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    const savedExtra = updateAccountMock.mock.calls[0]?.[1]?.extra ?? {}
    expect(savedExtra).toMatchObject({
      openai_excel_bps_models: ['gpt-6-astra', 'gpt-6.1-sol'],
      openai_excel_bps_auto_disable_on_403: true,
      openai_excel_bps_auto_recover_on_403: true,
      openai_excel_bps_403_recovery_interval_minutes: 60,
      openai_excel_bps_auto_move_on_403: true,
      openai_excel_bps_403_target_group_id: 21,
      openai_excel_bps_cache_creation_as_input: true,
      openai_excel_bps_session_proxy_enabled: true,
      openai_excel_bps_session_proxy_ids: [7],
      openai_bps_skip_historical_encrypted: true,
      openai_bps_hosted_tools_policy: 'omit'
    })
    // Legacy BPS-only absent openai_excel_bps means enabled (preserved when not previously stored)
    expect(savedExtra.openai_excel_bps).toBeUndefined()
  })

  it('supports real upstream model sync (bps_access) for saved BPS accounts and dual-grant accounts without auto-enabling BPS', async () => {
    // 1. Saved BPS-only account: no fake sync-unavailable banner; live sync works and prefills suggestions
    const bpsWrapper = mountModal(
      buildOAuthAccount(
        OPENAI_EXCEL_OAUTH_CLIENT_ID,
        {},
        { credentials: { model_mapping: {} } }
      ),
      { unstubWhitelist: true }
    )

    expect(bpsWrapper.find('[data-testid="edit-bps-sync-unavailable"]').exists()).toBe(false)
    expect(bpsWrapper.find('[data-testid="edit-bps-explicit-models-hint"]').exists()).toBe(true)
    expect(bpsWrapper.find('[data-testid="openai-bps-sync-models-btn"]').exists()).toBe(true)
    expect(bpsWrapper.text()).not.toContain('admin.accounts.supportsAllModels')

    await bpsWrapper.get('[data-testid="edit-bps-prefill-models"]').trigger('click')
    expect(bpsWrapper.find('[data-testid="edit-bps-explicit-models-hint"]').exists()).toBe(false)

    await bpsWrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()
    expect(updateAccountMock.mock.calls[0]?.[1]?.credentials?.model_mapping).toEqual({
      'gpt-6-astra': 'gpt-6-astra',
      'gpt-6.1-sol': 'gpt-6.1-sol'
    })

    // 2. Dual-grant native Codex account: preserves native controls, shows dual channel note, syncs Excel catalog via profile='excel', refreshes & removes grant cleanly
    updateAccountMock.mockClear()
    const dualAccount = buildOAuthAccount(
      'codex-primary-client',
      {
        openai_oauth_responses_websockets_v2_mode: 'ctx_pool',
        openai_oauth_responses_websockets_v2_enabled: true
      },
      {
        credentials: {
          [OPENAI_EXCEL_BPS_CREDENTIALS_KEY]: {
            client_id: OPENAI_EXCEL_OAUTH_CLIENT_ID,
            access_token: 'nested-secret-token'
          }
        }
      }
    )
    refreshOpenAIAccountTokenMock.mockResolvedValueOnce(dualAccount)

    const dualWrapper = mountModal(dualAccount)
    expect(dualWrapper.find('[data-testid="edit-dual-grant-channel-note"]').exists()).toBe(true)
    expect(dualWrapper.text()).not.toContain('nested-secret-token')

    await dualWrapper.get('[data-testid="openai-bps-sync-models-btn"]').trigger('click')
    await flushPromises()
    expect(syncUpstreamModelsMock).toHaveBeenCalledWith(11, 'excel')

    await dualWrapper.get('[data-testid="openai-bps-refresh-grant-btn"]').trigger('click')
    await flushPromises()
    expect(refreshOpenAIAccountTokenMock).toHaveBeenCalledWith(11, 'excel')

    // Stage grant removal -> sends credentials.excel_bps_credentials: null without erasing native WS mode or primary credentials
    await dualWrapper.get('[data-testid="openai-bps-remove-grant-btn"]').trigger('click')
    await dualWrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    const dualSaved = updateAccountMock.mock.calls[0]?.[1]
    expect(dualSaved?.credentials?.[OPENAI_EXCEL_BPS_CREDENTIALS_KEY]).toBeNull()
    expect(dualSaved?.extra?.openai_oauth_responses_websockets_v2_mode).toBe('ctx_pool')
    expect(dualSaved?.extra?.openai_excel_bps).not.toBe(true)
  })

  it('renders runtime 403 auto-disable and grant-invalid banners distinctly from local token expiry', () => {
    const wrapper403 = mountModal(
      buildOAuthAccount(OPENAI_EXCEL_OAUTH_CLIENT_ID, {
        openai_excel_bps_403_disabled_at: '2026-11-13T10:00:00Z',
        openai_excel_bps_403_disabled_reason: 'http_403',
        openai_excel_bps_403_probe_model: 'gpt-6-astra',
        openai_excel_bps_grant_invalid: 'sha256-fp'
      })
    )
    expect(wrapper403.find('[data-testid="openai-bps-runtime-403-banner"]').exists()).toBe(true)
    expect(wrapper403.find('[data-testid="openai-bps-runtime-grant-invalid-banner"]').exists()).toBe(true)
    expect(wrapper403.find('[data-testid="openai-bps-runtime-token-expired-banner"]').exists()).toBe(false)
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

    expect(wrapper.get('[data-testid="upstream-client-timezone-runtime-zone"]').text()).toBe('Asia/Tokyo')
    expect(wrapper.get('[data-testid="upstream-client-timezone-runtime-source"]').text()).toContain(
      'admin.accounts.upstreamClientTimezone.source.proxy_exit'
    )
    expect(wrapper.find('[data-testid="upstream-client-timezone-unsaved-note"]').exists()).toBe(false)

    await wrapper.get('[data-testid="edit-upstream-client-timezone-mode"]').setValue('manual')
    expect(wrapper.find('[data-testid="upstream-client-timezone-unsaved-note"]').exists()).toBe(true)
    expect(wrapper.get('[data-testid="upstream-client-timezone-runtime-zone"]').text()).toBe('Asia/Tokyo')

    await wrapper.get('[data-testid="edit-upstream-client-timezone-input"]').setValue('+08:00')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()
    expect(updateAccountMock).not.toHaveBeenCalled()
    expect(wrapper.get('[data-testid="upstream-client-timezone-error"]').text()).toContain(
      'admin.accounts.upstreamClientTimezone.errorInvalid'
    )

    await wrapper.get('[data-testid="edit-upstream-client-timezone-input"]').setValue('Europe/Berlin')
    await wrapper.get('[data-testid="edit-upstream-client-timezone-mode"]').setValue('auto')
    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    const autoExtra = updateAccountMock.mock.calls[0]?.[1]?.extra ?? {}
    expect(autoExtra.upstream_client_timezone_mode).toBe('auto')
    expect(autoExtra).not.toHaveProperty('upstream_client_timezone')

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
