import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import { extractApiErrorMessage, extractI18nErrorMessage } from '@/utils/apiError'
import {
  OPENAI_EXCEL_OAUTH_CLIENT_ID,
  isOpenAIExcelClientId,
  readOAuthStateFromAuthUrl,
  tokenMatchesOpenAIOAuthClient,
  type OpenAIOAuthClient
} from '@/utils/openaiOAuthClient'

export { OPENAI_EXCEL_OAUTH_CLIENT_ID, isOpenAIExcelClientId } from '@/utils/openaiOAuthClient'
export type { OpenAIOAuthClient } from '@/utils/openaiOAuthClient'

export interface OpenAITokenInfo {
  access_token?: string
  refresh_token?: string
  client_id?: string
  id_token?: string
  token_type?: string
  expires_in?: number
  expires_at?: number
  scope?: string
  email?: string
  name?: string
  plan_type?: string
  subscription_expires_at?: string
  privacy_mode?: string
  // OpenAI specific IDs (extracted from ID Token)
  chatgpt_account_id?: string
  chatgpt_user_id?: string
  organization_id?: string
  [key: string]: unknown
}

export type OpenAIOAuthPlatform = 'openai'

export function useOpenAIOAuth() {
  const appStore = useAppStore()
  const { t } = useI18n()
  const endpointPrefix = '/admin/openai'

  // State
  const authUrl = ref('')
  const sessionId = ref('')
  const oauthState = ref('')
  const loading = ref(false)
  const error = ref('')
  // Login client the current authorization session was generated for.
  const authClient = ref<OpenAIOAuthClient>('codex')

  // Reset state
  const resetState = () => {
    authUrl.value = ''
    sessionId.value = ''
    oauthState.value = ''
    authClient.value = 'codex'
    loading.value = false
    error.value = ''
  }

  // Generate auth URL for OpenAI OAuth.
  // oauthClient 'excel' requests the official Excel (BPS) login; the server uses the
  // fixed official redirect and PKCE, so redirectUri is not sent for it.
  const generateAuthUrl = async (
    proxyId?: number | null,
    redirectUri?: string,
    oauthClient: OpenAIOAuthClient = 'codex'
  ): Promise<boolean> => {
    loading.value = true
    authUrl.value = ''
    sessionId.value = ''
    oauthState.value = ''
    authClient.value = 'codex'
    error.value = ''

    try {
      const payload: Record<string, unknown> = {}
      if (proxyId) {
        payload.proxy_id = proxyId
      }
      if (oauthClient === 'excel') {
        payload.oauth_client = 'excel'
      } else if (redirectUri) {
        payload.redirect_uri = redirectUri
      }

      const response = await adminAPI.accounts.generateAuthUrl(
        `${endpointPrefix}/generate-auth-url`,
        payload
      )
      authUrl.value = response.auth_url
      sessionId.value = response.session_id
      oauthState.value = readOAuthStateFromAuthUrl(response.auth_url)
      authClient.value = oauthClient
      return true
    } catch (err: any) {
      error.value = extractApiErrorMessage(err, t('admin.accounts.oauth.openai.failedToGenerateUrl'))
      appStore.showError(error.value)
      return false
    } finally {
      loading.value = false
    }
  }

  // Exchange auth code for tokens
  const exchangeAuthCode = async (
    code: string,
    currentSessionId: string,
    state: string,
    proxyId?: number | null
  ): Promise<OpenAITokenInfo | null> => {
    if (!code.trim() || !currentSessionId || !state.trim()) {
      error.value = 'Missing auth code, session ID, or state'
      return null
    }

    loading.value = true
    error.value = ''

    try {
      const payload: { session_id: string; code: string; state: string; proxy_id?: number } = {
        session_id: currentSessionId,
        code: code.trim(),
        state: state.trim()
      }
      if (proxyId) {
        payload.proxy_id = proxyId
      }

      const tokenInfo = (await adminAPI.accounts.exchangeCode(
        `${endpointPrefix}/exchange-code`,
        payload
      )) as OpenAITokenInfo
      // Never save a grant under a different login client than the session asked for.
      if (!tokenMatchesOpenAIOAuthClient(tokenInfo?.client_id, authClient.value)) {
        error.value = t('admin.accounts.oauth.openai.bps.clientMismatch')
        appStore.showError(error.value)
        return null
      }
      return tokenInfo
    } catch (err: any) {
      error.value = extractI18nErrorMessage(
        err,
        t,
        'admin.accounts.oauth.openai.errors',
        t('admin.accounts.oauth.openai.failedToExchangeCode')
      )
      appStore.showError(error.value)
      return null
    } finally {
      loading.value = false
    }
  }

  // Validate refresh token and get full token info
  // clientId: 指定 OAuth client_id（用于第三方渠道获取的 RT，如 app_LlGpXReQgckcGGUo2JrYvtJK）
  const validateRefreshToken = async (
    refreshToken: string,
    proxyId?: number | null,
    clientId?: string,
    profile?: OpenAIOAuthClient
  ): Promise<OpenAITokenInfo | null> => {
    if (!refreshToken.trim()) {
      error.value = 'Missing refresh token'
      return null
    }

    loading.value = true
    error.value = ''

    try {
      // Use dedicated refresh-token endpoint
      const tokenInfo = await (profile === 'excel'
        ? adminAPI.accounts.refreshOpenAIToken(
            refreshToken.trim(),
            proxyId,
            `${endpointPrefix}/refresh-token`,
            clientId,
            'excel'
          )
        : adminAPI.accounts.refreshOpenAIToken(
            refreshToken.trim(),
            proxyId,
            `${endpointPrefix}/refresh-token`,
            clientId
          ))
      const info = tokenInfo as OpenAITokenInfo
      // An Excel refresh must stay on the Excel client.
      if (
        (isOpenAIExcelClientId(clientId) || profile === 'excel') &&
        info?.client_id &&
        !isOpenAIExcelClientId(info.client_id)
      ) {
        error.value = t('admin.accounts.oauth.openai.bps.clientMismatch')
        appStore.showError(error.value)
        return null
      }
      return info
    } catch (err: any) {
      error.value = extractI18nErrorMessage(
        err,
        t,
        'admin.accounts.oauth.openai.errors',
        t('admin.accounts.oauth.openai.failedToValidateRT')
      )
      appStore.showError(error.value)
      return null
    } finally {
      loading.value = false
    }
  }

  // Build credentials for OpenAI OAuth account (aligned with backend BuildAccountCredentials)
  // fallbackClientId is used only when the token response omits client_id
  // (e.g. a refresh validated against an explicit client).
  const buildCredentials = (
    tokenInfo: OpenAITokenInfo,
    fallbackClientId?: string
  ): Record<string, unknown> => {
    const creds: Record<string, unknown> = {
      access_token: tokenInfo.access_token,
      expires_at: tokenInfo.expires_at
    }

    // 仅在返回了新的 refresh_token 时才写入，防止用空值覆盖已有令牌
    if (tokenInfo.refresh_token) {
      creds.refresh_token = tokenInfo.refresh_token
    }
    if (tokenInfo.id_token) {
      creds.id_token = tokenInfo.id_token
    }
    if (tokenInfo.email) {
      creds.email = tokenInfo.email
    }
    if (tokenInfo.chatgpt_account_id) {
      creds.chatgpt_account_id = tokenInfo.chatgpt_account_id
    }
    if (tokenInfo.chatgpt_user_id) {
      creds.chatgpt_user_id = tokenInfo.chatgpt_user_id
    }
    if (tokenInfo.organization_id) {
      creds.organization_id = tokenInfo.organization_id
    }
    if (tokenInfo.plan_type) {
      creds.plan_type = tokenInfo.plan_type
    }
    if (tokenInfo.subscription_expires_at) {
      creds.subscription_expires_at = tokenInfo.subscription_expires_at
    }
    if (tokenInfo.client_id) {
      creds.client_id = tokenInfo.client_id
    } else if (fallbackClientId) {
      creds.client_id = fallbackClientId
    }

    return creds
  }

  // Build normalized nested OAuth map for `credentials.excel_bps_credentials` (no model_mapping or extra fields).
  const buildIndependentExcelBPSCredentials = (
    tokenInfo: OpenAITokenInfo
  ): Record<string, unknown> => {
    const nested = buildCredentials(tokenInfo, OPENAI_EXCEL_OAUTH_CLIENT_ID)
    nested.client_id = OPENAI_EXCEL_OAUTH_CLIENT_ID
    return nested
  }

  // Build extra info from token response
  const buildExtraInfo = (tokenInfo: OpenAITokenInfo): Record<string, string> | undefined => {
    const extra: Record<string, string> = {}
    if (tokenInfo.email) {
      extra.email = tokenInfo.email
    }
    if (tokenInfo.name) {
      extra.name = tokenInfo.name
    }
    if (tokenInfo.privacy_mode) {
      extra.privacy_mode = tokenInfo.privacy_mode
    }
    return Object.keys(extra).length > 0 ? extra : undefined
  }

  return {
    // State
    authUrl,
    sessionId,
    oauthState,
    authClient,
    loading,
    error,
    // Methods
    resetState,
    generateAuthUrl,
    exchangeAuthCode,
    validateRefreshToken,
    buildCredentials,
    buildIndependentExcelBPSCredentials,
    buildExtraInfo
  }
}
