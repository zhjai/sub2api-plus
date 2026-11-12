import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { OPENAI_EXCEL_OAUTH_CLIENT_ID, OPENAI_EXCEL_CALLBACK_ORIGIN, OPENAI_EXCEL_CALLBACK_PATH } from '@/utils/openaiOAuthClient'

const mocks = vi.hoisted(() => ({
  generateAuthUrl: vi.fn(),
  exchangeCode: vi.fn(),
  refreshOpenAIToken: vi.fn(),
  applyOAuthCredentials: vi.fn(),
  showError: vi.fn(),
  showSuccess: vi.fn()
}))

vi.mock('@/stores/app', () => ({ useAppStore: () => mocks }))
vi.mock('@/api/admin', () => ({ adminAPI: { accounts: mocks } }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key })
}))
vi.mock('@/composables/useAccountOAuth', () => ({ useAccountOAuth: () => ({ resetState: vi.fn() }) }))
vi.mock('@/composables/useGeminiOAuth', () => ({ useGeminiOAuth: () => ({ resetState: vi.fn() }) }))
vi.mock('@/composables/useAntigravityOAuth', () => ({ useAntigravityOAuth: () => ({ resetState: vi.fn() }) }))
vi.mock('@/composables/useGrokOAuth', () => ({ useGrokOAuth: () => ({ resetState: vi.fn() }) }))

import ReAuthAccountModal from '../ReAuthAccountModal.vue'

const FlowStub = defineComponent({
  name: 'OAuthAuthorizationFlow',
  props: ['oauthClient', 'authUrl', 'showRefreshTokenOption'],
  emits: ['generate-url', 'validate-refresh-token'],
  data: () => ({ authCode: '', oauthState: '', inputMethod: 'manual' }),
  methods: { reset() { this.authCode = ''; this.oauthState = ''; this.inputMethod = 'manual' } },
  template: '<div><button data-testid="generate" @click="$emit(\'generate-url\')">Generate</button><button data-testid="refresh" @click="$emit(\'validate-refresh-token\', \'excel-rt\')">Refresh</button></div>'
})
const DialogStub = defineComponent({
  props: ['show'],
  template: '<div v-if="show"><slot/><slot name="footer"/></div>'
})

describe('BPS account reauthorization', () => {
  const mounts: ReturnType<typeof mount>[] = []
  const account = {
    id: 42, name: 'BPS test', platform: 'openai', type: 'oauth', proxy_id: 9,
    credentials: { client_id: OPENAI_EXCEL_OAUTH_CLIENT_ID }
  }
  async function open(clientId = OPENAI_EXCEL_OAUTH_CLIENT_ID) {
    const wrapper = mount(ReAuthAccountModal, {
      props: { show: false, account: { ...account, credentials: { client_id: clientId } } as any },
      global: { stubs: { BaseDialog: DialogStub, OAuthAuthorizationFlow: FlowStub, Icon: true } }
    })
    mounts.push(wrapper)
    await wrapper.setProps({ show: true })
    return wrapper
  }
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.generateAuthUrl.mockResolvedValue({ auth_url: 'https://auth.openai.com/authorize?state=server-state', session_id: 'auth-session' })
    mocks.exchangeCode.mockResolvedValue({ access_token: 'excel-at', refresh_token: 'excel-rt', client_id: OPENAI_EXCEL_OAUTH_CLIENT_ID })
    mocks.refreshOpenAIToken.mockResolvedValue({ access_token: 'excel-at', refresh_token: 'excel-rt', client_id: OPENAI_EXCEL_OAUTH_CLIENT_ID })
    mocks.applyOAuthCredentials.mockResolvedValue(account)
  })
  afterEach(() => mounts.splice(0).forEach(wrapper => wrapper.unmount()))

  it('keeps the saved Excel login profile for callback exchange and account update', async () => {
    const wrapper = await open()
    expect(wrapper.find('[data-testid="reauth-bps-note"]').exists()).toBe(true)
    const flow = wrapper.getComponent(FlowStub)
    expect(flow.props('oauthClient')).toBe('excel')
    expect(flow.props('showRefreshTokenOption')).toBe(true)
    await wrapper.get('[data-testid="generate"]').trigger('click')
    await flushPromises()
    expect(mocks.generateAuthUrl).toHaveBeenCalledWith('/admin/openai/generate-auth-url', { proxy_id: 9, oauth_client: 'excel' })
    flow.vm.authCode = OPENAI_EXCEL_CALLBACK_ORIGIN + OPENAI_EXCEL_CALLBACK_PATH + '?code=excel-code&state=server-state'
    await flushPromises()
    const complete = wrapper.findAll('button').find(button => button.text() === 'admin.accounts.oauth.completeAuth')!
    expect(complete.attributes('disabled')).toBeUndefined()
    await complete.trigger('click')
    await flushPromises()
    expect(mocks.exchangeCode).toHaveBeenCalledWith('/admin/openai/exchange-code', {
      session_id: 'auth-session', code: 'excel-code', state: 'server-state', proxy_id: 9
    })
    expect(mocks.applyOAuthCredentials).toHaveBeenCalledWith(42, expect.objectContaining({
      credentials: expect.objectContaining({ client_id: OPENAI_EXCEL_OAUTH_CLIENT_ID })
    }))
    expect(wrapper.emitted('reauthorized')).toHaveLength(1)
  })

  it('does not send a callback belonging to another authorization session', async () => {
    const wrapper = await open()
    await wrapper.get('[data-testid="generate"]').trigger('click')
    await flushPromises()
    wrapper.getComponent(FlowStub).vm.authCode = OPENAI_EXCEL_CALLBACK_ORIGIN + OPENAI_EXCEL_CALLBACK_PATH + '?code=excel-code&state=other-state'
    await flushPromises()
    const complete = wrapper.findAll('button').find(button => button.text() === 'admin.accounts.oauth.completeAuth')!
    expect(complete.attributes('disabled')).toBeDefined()
    expect(mocks.exchangeCode).not.toHaveBeenCalled()
    expect(mocks.applyOAuthCredentials).not.toHaveBeenCalled()
  })

  it('uses the Excel refresh client and retains it on the existing account', async () => {
    const wrapper = await open()
    await wrapper.get('[data-testid="refresh"]').trigger('click')
    await flushPromises()
    expect(mocks.refreshOpenAIToken).toHaveBeenCalledWith('excel-rt', 9, '/admin/openai/refresh-token', OPENAI_EXCEL_OAUTH_CLIENT_ID)
    expect(mocks.applyOAuthCredentials).toHaveBeenCalledWith(42, expect.objectContaining({
      credentials: expect.objectContaining({ client_id: OPENAI_EXCEL_OAUTH_CLIENT_ID, refresh_token: 'excel-rt' })
    }))
  })

  it('keeps ordinary Codex reauthorization on its original login profile', async () => {
    const wrapper = await open('codex-client')
    expect(wrapper.find('[data-testid="reauth-bps-note"]').exists()).toBe(false)
    expect(wrapper.getComponent(FlowStub).props('oauthClient')).toBe('codex')
    await wrapper.get('[data-testid="generate"]').trigger('click')
    await flushPromises()
    expect(mocks.generateAuthUrl).toHaveBeenCalledWith('/admin/openai/generate-auth-url', { proxy_id: 9 })
  })
})
