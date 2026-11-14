import { describe, expect, it, vi } from 'vitest'

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn()
  })
}))

vi.mock('vue-i18n', () => ({
  useI18n: () => ({
    t: (key: string) => {
      const messages: Record<string, string> = {
        'admin.accounts.oauth.openai.failedToExchangeCode': 'OpenAI 授权码兑换失败',
        'admin.accounts.oauth.openai.errors.OPENAI_OAUTH_PROXY_REQUIRED':
          '未设置代理，当前服务器无法直连 OpenAI，导致 OpenAI OAuth 请求失败。请先选择可访问 OpenAI 的代理后重试；如果授权码已失效，请重新生成授权链接。'
      }
      return messages[key] ?? key
    }
  })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      generateAuthUrl: vi.fn(),
      exchangeCode: vi.fn(),
      refreshOpenAIToken: vi.fn()
    }
  }
}))

import { useOpenAIOAuth } from '@/composables/useOpenAIOAuth'
import { adminAPI } from '@/api/admin'
import { OPENAI_EXCEL_OAUTH_CLIENT_ID } from '@/utils/openaiOAuthClient'

describe('useOpenAIOAuth Excel profile', () => {
  it('requests the Excel profile without a custom redirect and keeps the generated state', async () => {
    vi.mocked(adminAPI.accounts.generateAuthUrl).mockResolvedValueOnce({
      auth_url: 'https://auth.openai.com/api/accounts/authorize?state=opaque',
      session_id: 'excel-session',
    })
    const oauth = useOpenAIOAuth()
    expect(await oauth.generateAuthUrl(12, 'https://ignored.invalid', 'excel')).toBe(true)
    expect(adminAPI.accounts.generateAuthUrl).toHaveBeenLastCalledWith(
      '/admin/openai/generate-auth-url', { proxy_id: 12, oauth_client: 'excel' }
    )
    expect(oauth.oauthState.value).toBe('opaque')
    expect(oauth.authClient.value).toBe('excel')

    vi.mocked(adminAPI.accounts.exchangeCode).mockResolvedValueOnce({
      client_id: 'codex-client', access_token: 'wrong-client-token',
    })
    expect(await oauth.exchangeAuthCode('code', 'excel-session', 'opaque')).toBeNull()
    expect(oauth.error.value).toBe('admin.accounts.oauth.openai.bps.clientMismatch')
  })

  it('preserves the Excel client across refresh and account credential save', async () => {
    vi.mocked(adminAPI.accounts.refreshOpenAIToken).mockResolvedValueOnce({
      access_token: 'excel-at', refresh_token: 'excel-rt', client_id: OPENAI_EXCEL_OAUTH_CLIENT_ID,
    })
    const oauth = useOpenAIOAuth()
    const result = await oauth.validateRefreshToken('excel-rt', 12, OPENAI_EXCEL_OAUTH_CLIENT_ID)
    expect(result).not.toBeNull()
    expect(adminAPI.accounts.refreshOpenAIToken).toHaveBeenLastCalledWith(
      'excel-rt', 12, '/admin/openai/refresh-token', OPENAI_EXCEL_OAUTH_CLIENT_ID
    )
    expect(oauth.buildCredentials(result!).client_id).toBe(OPENAI_EXCEL_OAUTH_CLIENT_ID)
    oauth.resetState()
    expect(oauth.authClient.value).toBe('codex')
    expect(oauth.sessionId.value).toBe('')
  })

  it('passes profile=excel when validating an independent Excel refresh token and builds nested Excel credentials', async () => {
    vi.mocked(adminAPI.accounts.refreshOpenAIToken).mockResolvedValueOnce({
      access_token: 'nested-excel-at',
      refresh_token: 'nested-excel-rt',
      id_token: 'nested-excel-id',
      client_id: OPENAI_EXCEL_OAUTH_CLIENT_ID,
      expires_at: 1700003600,
      chatgpt_account_id: 'ws-nested',
      chatgpt_user_id: 'usr-nested',
      organization_id: 'org-nested',
      plan_type: 'plus',
      email: 'excel-grant@example.com',
      subscription_expires_at: '2026-12-01T00:00:00Z'
    })
    const oauth = useOpenAIOAuth()
    const result = await oauth.validateRefreshToken('nested-excel-rt', 15, OPENAI_EXCEL_OAUTH_CLIENT_ID, 'excel')
    expect(result).not.toBeNull()
    expect(adminAPI.accounts.refreshOpenAIToken).toHaveBeenLastCalledWith(
      'nested-excel-rt',
      15,
      '/admin/openai/refresh-token',
      OPENAI_EXCEL_OAUTH_CLIENT_ID,
      'excel'
    )

    const nested = oauth.buildIndependentExcelBPSCredentials(result!)
    expect(nested).toMatchObject({
      access_token: 'nested-excel-at',
      refresh_token: 'nested-excel-rt',
      id_token: 'nested-excel-id',
      client_id: OPENAI_EXCEL_OAUTH_CLIENT_ID,
      chatgpt_account_id: 'ws-nested',
      chatgpt_user_id: 'usr-nested',
      organization_id: 'org-nested',
      plan_type: 'plus',
      email: 'excel-grant@example.com',
      subscription_expires_at: '2026-12-01T00:00:00Z'
    })
    expect(typeof nested.expires_at).toBe('number')
  })
})

describe('useOpenAIOAuth.buildCredentials', () => {
  it('should keep client_id when token response contains it', () => {
    const oauth = useOpenAIOAuth()
    const creds = oauth.buildCredentials({
      access_token: 'at',
      refresh_token: 'rt',
      client_id: 'app_test_client',
      expires_at: 1700000000
    })

    expect(creds.client_id).toBe('app_test_client')
    expect(creds.access_token).toBe('at')
    expect(creds.refresh_token).toBe('rt')
  })

  it('should keep legacy behavior when client_id is missing', () => {
    const oauth = useOpenAIOAuth()
    const creds = oauth.buildCredentials({
      access_token: 'at',
      refresh_token: 'rt',
      expires_at: 1700000000
    })

    expect(Object.prototype.hasOwnProperty.call(creds, 'client_id')).toBe(false)
    expect(creds.access_token).toBe('at')
    expect(creds.refresh_token).toBe('rt')
  })

  it('should keep ChatGPT subscription expiration from token response', () => {
    const oauth = useOpenAIOAuth()
    const creds = oauth.buildCredentials({
      access_token: 'at',
      refresh_token: 'rt',
      expires_at: 1700000000,
      plan_type: 'team',
      subscription_expires_at: '2026-07-20T19:22:48+00:00'
    })

    expect(creds.plan_type).toBe('team')
    expect(creds.subscription_expires_at).toBe('2026-07-20T19:22:48+00:00')
  })
})

describe('useOpenAIOAuth.exchangeAuthCode', () => {
  it('shows a clear proxy hint when code exchange fails without a proxy', async () => {
    vi.mocked(adminAPI.accounts.exchangeCode).mockRejectedValueOnce({
      status: 502,
      reason: 'OPENAI_OAUTH_PROXY_REQUIRED',
      message: 'OpenAI OAuth token exchange failed: no proxy is configured.'
    })
    const oauth = useOpenAIOAuth()

    const tokenInfo = await oauth.exchangeAuthCode('code', 'session-id', 'state')

    expect(tokenInfo).toBeNull()
    expect(oauth.error.value).toBe(
      '未设置代理，当前服务器无法直连 OpenAI，导致 OpenAI OAuth 请求失败。请先选择可访问 OpenAI 的代理后重试；如果授权码已失效，请重新生成授权链接。'
    )
  })
})
