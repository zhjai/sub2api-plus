import { beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'

const { updateAccountMock, checkMixedChannelRiskMock } = vi.hoisted(() => ({
  updateAccountMock: vi.fn(),
  checkMixedChannelRiskMock: vi.fn()
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
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
  codex_cli_only: true,
  codex_cli_only_allow_app_server: true,
  codex_fingerprint_mode: 'full',
  codex_identity_mode: 'preserve_client',
  codex_privacy_mode: 'randomized',
  codex_image_generation_bridge: true
}

function buildOAuthAccount(clientId: string | undefined, extra: Record<string, unknown> = {}) {
  const credentials: Record<string, unknown> = {
    access_token: 'at',
    chatgpt_account_id: 'workspace-1',
    model_mapping: { 'gpt-5.5': 'gpt-5.5' },
    compact_model_mapping: { 'gpt-5.5': 'gpt-5.5-compact' }
  }
  if (clientId) credentials.client_id = clientId
  return {
    id: 11,
    name: 'OpenAI OAuth',
    notes: '',
    platform: 'openai',
    type: 'oauth',
    parent_account_id: null,
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
  '[data-testid="codex-privacy"]',
  '[data-testid="codex-image-tool-inherit"]'
]

describe('EditAccountModal BPS (Excel client) accounts', () => {
  beforeEach(() => {
    updateAccountMock.mockReset().mockImplementation(async (_id: number, payload: any) => payload)
    checkMixedChannelRiskMock.mockReset().mockResolvedValue({ has_risk: false })
  })

  it('shows the BPS identity and hides Codex-only identity and native transport controls', () => {
    const wrapper = mountModal(buildOAuthAccount(OPENAI_EXCEL_OAUTH_CLIENT_ID))

    const identity = wrapper.get('[data-testid="edit-openai-bps-identity"]')
    expect(identity.text()).toContain('admin.accounts.oauth.openai.bps.identityTitle')
    expect(identity.text()).toContain('admin.accounts.oauth.openai.bps.scopeNote')
    expect(wrapper.get('[data-testid="edit-openai-bps-client-id"]').text()).toBe(OPENAI_EXCEL_OAUTH_CLIENT_ID)
    expect(identity.text()).toContain('workspace-1')
    for (const selector of codexOnlySelectors) {
      expect(wrapper.find(selector).exists(), selector).toBe(false)
    }
  })

  it('keeps Codex-only controls for an ordinary Codex OAuth account', () => {
    const wrapper = mountModal(buildOAuthAccount(undefined))

    expect(wrapper.find('[data-testid="edit-openai-bps-identity"]').exists()).toBe(false)
    for (const selector of codexOnlySelectors) {
      expect(wrapper.find(selector).exists(), selector).toBe(true)
    }
  })

  it('clears stale native options with their existing off semantics and keeps the Excel client', async () => {
    const wrapper = mountModal(buildOAuthAccount(OPENAI_EXCEL_OAUTH_CLIENT_ID, staleCodexExtra))

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(updateAccountMock).toHaveBeenCalledTimes(1)
    const payload = updateAccountMock.mock.calls[0]?.[1]
    const extra = payload?.extra ?? {}
    expect(extra.openai_oauth_responses_websockets_v2_mode).toBe('off')
    expect(extra.openai_oauth_responses_websockets_v2_enabled).toBe(false)
    expect(extra).not.toHaveProperty('openai_passthrough')
    expect(extra.codex_cli_only).toBe(false)
    expect(extra).not.toHaveProperty('codex_cli_only_allow_app_server')
    expect(extra).not.toHaveProperty('codex_fingerprint_mode')
    expect(extra).not.toHaveProperty('codex_identity_mode')
    expect(extra.codex_privacy_mode).toBe('off')
    expect(extra).not.toHaveProperty('codex_image_generation_bridge')
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
        codex_fingerprint_mode: 'full'
      })
    )

    await wrapper.get('form#edit-account-form').trigger('submit.prevent')
    await flushPromises()

    const payload = updateAccountMock.mock.calls[0]?.[1]
    expect(payload?.extra?.openai_oauth_responses_websockets_v2_mode).toBe('ctx_pool')
    expect(payload?.extra?.codex_fingerprint_mode).toBe('full')
    expect(payload?.credentials).not.toHaveProperty('client_id')
  })
})
