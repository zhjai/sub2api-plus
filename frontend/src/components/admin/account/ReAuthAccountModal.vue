<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.reAuthorizeAccount')"
    width="normal"
    @close="handleClose"
  >
    <div v-if="account" class="space-y-4">
      <!-- Account Info -->
      <div
        class="rounded-lg border border-gray-200 bg-gray-50 p-4 dark:border-dark-600 dark:bg-dark-700"
      >
        <div class="flex items-center gap-3">
          <div
            :class="[
              'flex h-10 w-10 items-center justify-center rounded-lg bg-gradient-to-br',
              isOpenAILike
                ? 'from-green-500 to-green-600'
                : isGemini
                  ? 'from-blue-500 to-blue-600'
                  : isAntigravity
                    ? 'from-purple-500 to-purple-600'
                    : isGrok
                      ? 'from-zinc-700 to-zinc-900'
                      : 'from-orange-500 to-orange-600'
            ]"
          >
            <Icon name="sparkles" size="md" class="text-white" />
          </div>
          <div>
            <span class="block font-semibold text-gray-900 dark:text-white">{{
              account.name
            }}</span>
            <span class="text-sm text-gray-500 dark:text-gray-400">
              {{
                isOpenAI
                  ? t('admin.accounts.openaiAccount')
                  : isGemini
                    ? t('admin.accounts.geminiAccount')
                    : isAntigravity
                      ? t('admin.accounts.antigravityAccount')
                      : isGrok
                        ? t('admin.accounts.grokAccount')
                        : t('admin.accounts.claudeCodeAccount')
              }}
            </span>
          </div>
        </div>
      </div>

      <!-- Add Method Selection (Claude only) -->
      <fieldset v-if="isAnthropic" class="border-0 p-0">
        <legend class="input-label">{{ t('admin.accounts.oauth.authMethod') }}</legend>
        <div class="mt-2 flex gap-4">
          <label class="flex cursor-pointer items-center">
            <input
              v-model="addMethod"
              type="radio"
              value="oauth"
              class="mr-2 text-primary-600 focus:ring-primary-500"
            />
            <span class="text-sm text-gray-700 dark:text-gray-300">{{
              t('admin.accounts.types.oauth')
            }}</span>
          </label>
          <label class="flex cursor-pointer items-center">
            <input
              v-model="addMethod"
              type="radio"
              value="setup-token"
              class="mr-2 text-primary-600 focus:ring-primary-500"
            />
            <span class="text-sm text-gray-700 dark:text-gray-300">{{
              t('admin.accounts.setupTokenLongLived')
            }}</span>
          </label>
        </div>
      </fieldset>

      <!-- Gemini OAuth Type Display (read-only) -->
      <div v-if="isGemini" class="rounded-lg border border-gray-200 bg-gray-50 p-4 dark:border-dark-600 dark:bg-dark-700">
        <div class="mb-2 text-sm font-medium text-gray-700 dark:text-gray-300">
          {{ t('admin.accounts.oauth.gemini.oauthTypeLabel') }}
        </div>
        <div class="flex items-center gap-3">
          <div
            :class="[
              'flex h-8 w-8 shrink-0 items-center justify-center rounded-lg',
              geminiOAuthType === 'google_one'
                ? 'bg-purple-500 text-white'
                : geminiOAuthType === 'code_assist'
                  ? 'bg-blue-500 text-white'
                  : 'bg-amber-500 text-white'
            ]"
          >
            <Icon v-if="geminiOAuthType === 'google_one'" name="user" size="sm" />
            <Icon v-else-if="geminiOAuthType === 'code_assist'" name="cloud" size="sm" />
            <Icon v-else name="sparkles" size="sm" />
          </div>
          <div>
            <span class="block text-sm font-medium text-gray-900 dark:text-white">
              {{
                geminiOAuthType === 'google_one'
                  ? 'Google One'
                  : geminiOAuthType === 'code_assist'
                    ? t('admin.accounts.gemini.oauthType.builtInTitle')
                    : t('admin.accounts.gemini.oauthType.customTitle')
              }}
            </span>
            <span class="text-xs text-gray-500 dark:text-gray-400">
              {{
                geminiOAuthType === 'google_one'
                  ? t('admin.accounts.gemini.oauthType.googleOneDesc')
                  : geminiOAuthType === 'code_assist'
                    ? t('admin.accounts.gemini.oauthType.builtInDesc')
                    : t('admin.accounts.gemini.oauthType.customDesc')
              }}
            </span>
          </div>
        </div>
      </div>

      <!-- Dual-grant target selector for native primary OpenAI OAuth accounts -->
      <div
        v-if="isDualGrantCapable"
        class="space-y-2 rounded-lg border border-gray-200 bg-gray-50/70 p-3.5 dark:border-dark-600 dark:bg-dark-700/50"
        data-testid="reauth-grant-target-selector"
      >
        <div class="flex items-center justify-between gap-2">
          <span class="text-xs font-semibold text-gray-700 dark:text-gray-200">
            {{ t('admin.accounts.bpsAccount.grant.reauthTargetLabel') }}
          </span>
          <button
            type="button"
            data-testid="reauth-reset-selected-grant"
            class="rounded border border-gray-300 bg-white px-2 py-0.5 text-[11px] font-medium text-gray-600 transition-colors hover:bg-gray-100 dark:border-dark-500 dark:bg-dark-700 dark:text-gray-300 dark:hover:bg-dark-600"
            @click="resetSelectedGrantState"
          >
            {{ t('common.reset') }}
          </button>
        </div>
        <div class="grid grid-cols-1 gap-2 sm:grid-cols-2">
          <button
            type="button"
            data-testid="reauth-grant-target-primary"
            :class="[
              'rounded-lg border p-2.5 text-left transition-all',
              grantTarget === 'primary'
                ? 'border-primary-500 bg-primary-50 dark:bg-primary-900/25'
                : 'border-gray-200 bg-white hover:bg-gray-50 dark:border-dark-500 dark:bg-dark-700 dark:hover:bg-dark-600'
            ]"
            @click="switchGrantTarget('primary')"
          >
            <span class="block text-xs font-semibold text-gray-900 dark:text-white">
              {{ t('admin.accounts.bpsAccount.grant.reauthTargetPrimary') }}
            </span>
            <span class="mt-0.5 block text-[11px] leading-snug text-gray-500 dark:text-gray-400">
              {{ t('admin.accounts.bpsAccount.grant.reauthTargetPrimaryDesc') }}
            </span>
          </button>
          <button
            type="button"
            data-testid="reauth-grant-target-excel"
            :class="[
              'rounded-lg border p-2.5 text-left transition-all',
              grantTarget === 'excel'
                ? 'border-emerald-500 bg-emerald-50 dark:bg-emerald-900/25'
                : 'border-gray-200 bg-white hover:bg-gray-50 dark:border-dark-500 dark:bg-dark-700 dark:hover:bg-dark-600'
            ]"
            @click="switchGrantTarget('excel')"
          >
            <span class="block text-xs font-semibold text-gray-900 dark:text-white">
              {{ t('admin.accounts.bpsAccount.grant.reauthTargetExcel') }}
            </span>
            <span class="mt-0.5 block text-[11px] leading-snug text-gray-500 dark:text-gray-400">
              {{ t('admin.accounts.bpsAccount.grant.reauthTargetExcelDesc') }}
            </span>
          </button>
        </div>
        <p class="text-[11px] text-gray-500 dark:text-gray-400">
          {{ t('admin.accounts.bpsAccount.grant.reauthIsolatedResetHint') }}
        </p>
      </div>

      <!-- BPS (Excel) accounts reauthorize on the same login client -->
      <p
        v-if="isOpenAIBPS"
        data-testid="reauth-bps-note"
        class="rounded-lg bg-emerald-50 px-3 py-2 text-xs leading-relaxed text-emerald-900 dark:bg-emerald-900/20 dark:text-emerald-200"
      >
        {{ t('admin.accounts.oauth.openai.bps.reauthNote') }}
      </p>

      <OAuthAuthorizationFlow
        ref="oauthFlowRef"
        :add-method="addMethod"
        :oauth-client="effectiveOpenAIProfile"
        :auth-url="currentAuthUrl"
        :session-id="currentSessionId"
        :loading="currentLoading"
        :error="currentError"
        :show-help="isAnthropic"
        :show-proxy-warning="isAnthropic"
        :show-cookie-option="isAnthropic"
        :show-refresh-token-option="isOpenAI || isAntigravity || isGrok"
        :show-sso-option="isGrok"
        :show-email-password-option="false"
        :allow-multiple="false"
        :method-label="t('admin.accounts.inputMethod')"
        :platform="isOpenAI ? 'openai' : isGemini ? 'gemini' : isAntigravity ? 'antigravity' : isGrok ? 'grok' : 'anthropic'"
        :show-project-id="isGemini && geminiOAuthType === 'code_assist'"
        :initial-input-method="grokInitialInputMethod"
        @generate-url="handleGenerateUrl"
        @cookie-auth="handleCookieAuth"
        @validate-refresh-token="handleValidateRefreshToken"
        @import-sso="handleGrokImportSSO"
      />

    </div>

    <template #footer>
      <div v-if="account" class="flex justify-between gap-3">
        <button type="button" class="btn btn-secondary" @click="handleClose">
          {{ t('common.cancel') }}
        </button>
        <button
          v-if="isManualInputMethod"
          type="button"
          :disabled="!canExchangeCode"
          class="btn btn-primary"
          @click="handleExchangeCode"
        >
          <svg
            v-if="currentLoading"
            class="-ml-1 mr-2 h-4 w-4 animate-spin"
            fill="none"
            viewBox="0 0 24 24"
          >
            <circle
              class="opacity-25"
              cx="12"
              cy="12"
              r="10"
              stroke="currentColor"
              stroke-width="4"
            ></circle>
            <path
              class="opacity-75"
              fill="currentColor"
              d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"
            ></path>
          </svg>
          {{
            currentLoading
              ? t('admin.accounts.oauth.verifying')
              : t('admin.accounts.oauth.completeAuth')
          }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import {
  useAccountOAuth,
  type AddMethod,
  type AuthInputMethod
} from '@/composables/useAccountOAuth'
import { useOpenAIOAuth } from '@/composables/useOpenAIOAuth'
import {
  OPENAI_EXCEL_BPS_CREDENTIALS_KEY,
  OPENAI_EXCEL_OAUTH_CLIENT_ID,
  isNativeOpenAIOAuthEligibleForBPSGrant,
  openAIExcelCallbackErrorKey,
  parseOpenAIExcelCallback,
  resolveOpenAIOAuthClient,
  type OpenAIOAuthClient
} from '@/utils/openaiOAuthClient'
import { useGeminiOAuth } from '@/composables/useGeminiOAuth'
import { useAntigravityOAuth } from '@/composables/useAntigravityOAuth'
import { useGrokOAuth } from '@/composables/useGrokOAuth'
import type { Account } from '@/types'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import OAuthAuthorizationFlow from '@/components/account/OAuthAuthorizationFlow.vue'

// Type for exposed OAuthAuthorizationFlow component
// Note: defineExpose automatically unwraps refs, so we use the unwrapped types
interface OAuthFlowExposed {
  authCode: string
  oauthState: string
  projectId: string
  sessionKey: string
  inputMethod: AuthInputMethod
  reset: () => void
}

interface Props {
  show: boolean
  account: Account | null
  initialGrantTarget?: 'primary' | 'excel'
}

const props = withDefaults(defineProps<Props>(), {
  initialGrantTarget: 'primary'
})
const emit = defineEmits<{
  close: []
  reauthorized: [account: Account]
}>()

const appStore = useAppStore()
const { t } = useI18n()

// OAuth composables (separate OpenAI sessions for primary vs independent Excel BPS grant)
const claudeOAuth = useAccountOAuth()
const primaryOpenAIOAuth = useOpenAIOAuth()
const excelOpenAIOAuth = useOpenAIOAuth()
const geminiOAuth = useGeminiOAuth()
const antigravityOAuth = useAntigravityOAuth()
const grokOAuth = useGrokOAuth()

// Refs
const oauthFlowRef = ref<OAuthFlowExposed | null>(null)

// State
const addMethod = ref<AddMethod>('oauth')
const geminiOAuthType = ref<'code_assist' | 'google_one' | 'ai_studio'>('code_assist')
const grantTarget = ref<'primary' | 'excel'>('primary')

interface FlowInputSnapshot {
  authCode: string
  oauthState: string
  inputMethod: AuthInputMethod
}
const defaultFlowSnapshot = (): FlowInputSnapshot => ({
  authCode: '',
  oauthState: '',
  inputMethod: 'manual'
})
const flowSnapshots = ref<Record<'primary' | 'excel', FlowInputSnapshot>>({
  primary: defaultFlowSnapshot(),
  excel: defaultFlowSnapshot()
})

// Computed - check platform
const isOpenAI = computed(() => props.account?.platform === 'openai')
const isOpenAILike = computed(() => isOpenAI.value)
const isGemini = computed(() => props.account?.platform === 'gemini')
const isAnthropic = computed(() => props.account?.platform === 'anthropic')
const isAntigravity = computed(() => props.account?.platform === 'antigravity')
const isGrok = computed(() => props.account?.platform === 'grok')

const isDualGrantCapable = computed(() => isNativeOpenAIOAuthEligibleForBPSGrant(props.account))
const isIndependentExcelTarget = computed(
  () => isDualGrantCapable.value && grantTarget.value === 'excel'
)

// OpenAI primary login profile comes from saved client_id; dual-grant native accounts can also target independent Excel BPS.
const openaiProfile = computed<OpenAIOAuthClient>(() =>
  isOpenAI.value
    ? resolveOpenAIOAuthClient(props.account?.credentials as Record<string, unknown> | undefined)
    : 'codex'
)
const effectiveOpenAIProfile = computed<OpenAIOAuthClient>(() =>
  isIndependentExcelTarget.value ? 'excel' : openaiProfile.value
)
const isOpenAIBPS = computed(() => isOpenAI.value && openaiProfile.value === 'excel')
const usesExcelOAuthFlow = computed(() => isOpenAIBPS.value || isIndependentExcelTarget.value)
const activeOpenAIOAuth = computed(() =>
  isIndependentExcelTarget.value ? excelOpenAIOAuth : primaryOpenAIOAuth
)

/**
 * Grok reauth default tab (password auth is hidden):
 * - refresh_token when RT may still work
 * - SSO cookie otherwise
 */
const grokInitialInputMethod = computed<AuthInputMethod>(() => {
  if (!isGrok.value) return 'manual'
  const creds = (props.account?.credentials || {}) as Record<string, unknown>
  const hasRT =
    (typeof creds.refresh_token === 'string' && creds.refresh_token.trim() !== '') ||
    (typeof creds.has_refresh_token === 'boolean' && creds.has_refresh_token)
  if (hasRT) return 'refresh_token'
  return 'sso_cookie'
})

// Computed - current OAuth state based on platform
const currentAuthUrl = computed(() => {
  if (isOpenAILike.value) return activeOpenAIOAuth.value.authUrl.value
  if (isGemini.value) return geminiOAuth.authUrl.value
  if (isAntigravity.value) return antigravityOAuth.authUrl.value
  if (isGrok.value) return grokOAuth.authUrl.value
  return claudeOAuth.authUrl.value
})
const currentSessionId = computed(() => {
  if (isOpenAILike.value) return activeOpenAIOAuth.value.sessionId.value
  if (isGemini.value) return geminiOAuth.sessionId.value
  if (isAntigravity.value) return antigravityOAuth.sessionId.value
  if (isGrok.value) return grokOAuth.sessionId.value
  return claudeOAuth.sessionId.value
})
const currentLoading = computed(() => {
  if (isOpenAILike.value) return activeOpenAIOAuth.value.loading.value
  if (isGemini.value) return geminiOAuth.loading.value
  if (isAntigravity.value) return antigravityOAuth.loading.value
  if (isGrok.value) return grokOAuth.loading.value
  return claudeOAuth.loading.value
})
const currentError = computed(() => {
  if (isOpenAILike.value) return activeOpenAIOAuth.value.error.value
  if (isGemini.value) return geminiOAuth.error.value
  if (isAntigravity.value) return antigravityOAuth.error.value
  if (isGrok.value) return grokOAuth.error.value
  return claudeOAuth.error.value
})

// Computed — footer "complete auth" only for code-exchange flows, not SSO/password/RT.
const isManualInputMethod = computed(() => {
  const method = oauthFlowRef.value?.inputMethod
  if (method === 'sso_cookie' || method === 'email_password' || method === 'refresh_token') {
    return false
  }
  // OpenAI/Gemini/Antigravity/Grok use manual code paste by default (no cookie auth)
  return (
    isOpenAILike.value ||
    isGemini.value ||
    isAntigravity.value ||
    isGrok.value ||
    method === 'manual'
  )
})

const canExchangeCode = computed(() => {
  const authCode = oauthFlowRef.value?.authCode || ''
  const sessionId = currentSessionId.value
  const loading = currentLoading.value
  if (
    usesExcelOAuthFlow.value &&
    !parseOpenAIExcelCallback(authCode, activeOpenAIOAuth.value.oauthState.value).ok
  ) {
    return false
  }
  return authCode.trim() && sessionId && !loading
})

const applyFlowSnapshot = (snapshot: FlowInputSnapshot) => {
  const flow = oauthFlowRef.value as Record<string, any> | null
  if (!flow) return
  if ('authCode' in flow) flow.authCode = snapshot.authCode
  if ('oauthState' in flow) flow.oauthState = snapshot.oauthState
  if ('inputMethod' in flow) flow.inputMethod = snapshot.inputMethod
}

const switchGrantTarget = (nextTarget: 'primary' | 'excel') => {
  if (grantTarget.value === nextTarget) return
  const flow = oauthFlowRef.value
  if (flow) {
    flowSnapshots.value[grantTarget.value] = {
      authCode: flow.authCode || '',
      oauthState: flow.oauthState || '',
      inputMethod: flow.inputMethod || 'manual'
    }
  }
  grantTarget.value = nextTarget
  applyFlowSnapshot(flowSnapshots.value[nextTarget])
}

const resetSelectedGrantState = () => {
  activeOpenAIOAuth.value.resetState()
  flowSnapshots.value[grantTarget.value] = defaultFlowSnapshot()
  oauthFlowRef.value?.reset()
}

// Watchers
watch(
  () => props.show,
  (newVal) => {
    if (newVal && props.account) {
      grantTarget.value =
        isDualGrantCapable.value && props.initialGrantTarget === 'excel' ? 'excel' : 'primary'
      flowSnapshots.value = {
        primary: defaultFlowSnapshot(),
        excel: defaultFlowSnapshot()
      }
      // Initialize addMethod based on current account type (Claude only)
      if (
        isAnthropic.value &&
        (props.account.type === 'oauth' || props.account.type === 'setup-token')
      ) {
        addMethod.value = props.account.type as AddMethod
      }
      if (isGemini.value) {
        const creds = (props.account.credentials || {}) as Record<string, unknown>
        geminiOAuthType.value =
          creds.oauth_type === 'google_one'
            ? 'google_one'
            : creds.oauth_type === 'ai_studio'
              ? 'ai_studio'
              : 'code_assist'
      }
    } else {
      resetState()
    }
  }
)

// Methods
const resetState = () => {
  addMethod.value = 'oauth'
  geminiOAuthType.value = 'code_assist'
  grantTarget.value = 'primary'
  flowSnapshots.value = {
    primary: defaultFlowSnapshot(),
    excel: defaultFlowSnapshot()
  }
  claudeOAuth.resetState()
  primaryOpenAIOAuth.resetState()
  excelOpenAIOAuth.resetState()
  geminiOAuth.resetState()
  antigravityOAuth.resetState()
  grokOAuth.resetState()
  oauthFlowRef.value?.reset()
}

const handleClose = () => {
  emit('close')
}

const handleGenerateUrl = async () => {
  if (!props.account) return

  if (isOpenAILike.value) {
    if (usesExcelOAuthFlow.value) {
      await activeOpenAIOAuth.value.generateAuthUrl(props.account.proxy_id, undefined, 'excel')
    } else {
      await activeOpenAIOAuth.value.generateAuthUrl(props.account.proxy_id)
    }
  } else if (isGemini.value) {
    const creds = (props.account.credentials || {}) as Record<string, unknown>
    const tierId = typeof creds.tier_id === 'string' ? creds.tier_id : undefined
    const projectId = geminiOAuthType.value === 'code_assist' ? oauthFlowRef.value?.projectId : undefined
    await geminiOAuth.generateAuthUrl(props.account.proxy_id, projectId, geminiOAuthType.value, tierId)
  } else if (isAntigravity.value) {
    await antigravityOAuth.generateAuthUrl(props.account.proxy_id)
  } else if (isGrok.value) {
    await grokOAuth.generateAuthUrl(props.account.proxy_id)
  } else {
    await claudeOAuth.generateAuthUrl(addMethod.value, props.account.proxy_id)
  }
}

const handleExchangeCode = async () => {
  if (!props.account) return

  const authCode = oauthFlowRef.value?.authCode || ''
  if (!authCode.trim()) return

  if (isOpenAILike.value) {
    // OpenAI OAuth flow
    const oauthClient = activeOpenAIOAuth.value
    const sessionId = oauthClient.sessionId.value
    if (!sessionId) return
    // The session must have been generated for the active grant's login profile.
    if (oauthClient.authClient.value !== effectiveOpenAIProfile.value) {
      oauthClient.error.value = t('admin.accounts.oauth.openai.bps.clientMismatch')
      appStore.showError(oauthClient.error.value)
      return
    }
    let codeToUse = authCode.trim()
    let stateToUse = (oauthFlowRef.value?.oauthState || oauthClient.oauthState.value || '').trim()
    if (usesExcelOAuthFlow.value) {
      const callback = parseOpenAIExcelCallback(authCode, oauthClient.oauthState.value)
      if (!callback.ok) {
        oauthClient.error.value = t(openAIExcelCallbackErrorKey(callback.error), { error: callback.detail || '' })
        appStore.showError(oauthClient.error.value)
        return
      }
      codeToUse = callback.code
      stateToUse = callback.state
    }
    if (!stateToUse) {
      oauthClient.error.value = t('admin.accounts.oauth.authFailed')
      appStore.showError(oauthClient.error.value)
      return
    }

    const tokenInfo = await oauthClient.exchangeAuthCode(
      codeToUse,
      sessionId,
      stateToUse,
      props.account.proxy_id
    )
    if (!tokenInfo) return

    try {
      let updatedAccount: Account
      if (isIndependentExcelTarget.value) {
        // Persist ONLY nested `credentials.excel_bps_credentials` via account update:
        // never overwrite primary Codex credentials and never auto-enable `extra.openai_excel_bps`.
        const nestedGrant = oauthClient.buildIndependentExcelBPSCredentials(tokenInfo)
        updatedAccount = await adminAPI.accounts.update(props.account.id, {
          credentials: {
            [OPENAI_EXCEL_BPS_CREDENTIALS_KEY]: nestedGrant
          }
        })
        appStore.showSuccess(t('admin.accounts.bpsAccount.grant.reauthExcelSavedSuccess'))
      } else {
        const credentials = oauthClient.buildCredentials(tokenInfo)
        const extra = oauthClient.buildExtraInfo(tokenInfo)
        updatedAccount = await adminAPI.accounts.applyOAuthCredentials(props.account.id, {
          type: 'oauth',
          credentials,
          extra
        })
        appStore.showSuccess(t('admin.accounts.reAuthorizedSuccess'))
      }

      emit('reauthorized', updatedAccount)
      handleClose()
    } catch (error: any) {
      oauthClient.error.value =
        error.response?.data?.detail ||
        error.response?.data?.message ||
        t('admin.accounts.oauth.authFailed')
      appStore.showError(oauthClient.error.value)
    }
  } else if (isGemini.value) {
    const sessionId = geminiOAuth.sessionId.value
    if (!sessionId) return

    const stateFromInput = oauthFlowRef.value?.oauthState || ''
    const stateToUse = stateFromInput || geminiOAuth.state.value
    if (!stateToUse) return

    const tokenInfo = await geminiOAuth.exchangeAuthCode({
      code: authCode.trim(),
      sessionId,
      state: stateToUse,
      proxyId: props.account.proxy_id,
      oauthType: geminiOAuthType.value,
      tierId: typeof (props.account.credentials as any)?.tier_id === 'string' ? ((props.account.credentials as any).tier_id as string) : undefined
    })
    if (!tokenInfo) return

    const credentials = geminiOAuth.buildCredentials(tokenInfo)

    try {
      await adminAPI.accounts.update(props.account.id, {
        type: 'oauth',
        credentials
      })
      const updatedAccount = await adminAPI.accounts.clearError(props.account.id)
      appStore.showSuccess(t('admin.accounts.reAuthorizedSuccess'))
      emit('reauthorized', updatedAccount)
      handleClose()
    } catch (error: any) {
      geminiOAuth.error.value = error.response?.data?.detail || t('admin.accounts.oauth.authFailed')
      appStore.showError(geminiOAuth.error.value)
    }
  } else if (isAntigravity.value) {
    // Antigravity OAuth flow
    const sessionId = antigravityOAuth.sessionId.value
    if (!sessionId) return

    const stateFromInput = oauthFlowRef.value?.oauthState || ''
    const stateToUse = stateFromInput || antigravityOAuth.state.value
    if (!stateToUse) return

    const tokenInfo = await antigravityOAuth.exchangeAuthCode({
      code: authCode.trim(),
      sessionId,
      state: stateToUse,
      proxyId: props.account.proxy_id
    })
    if (!tokenInfo) return

    const credentials = antigravityOAuth.buildCredentials(tokenInfo)

    try {
      await adminAPI.accounts.update(props.account.id, {
        type: 'oauth',
        credentials
      })
      const updatedAccount = await adminAPI.accounts.clearError(props.account.id)
      appStore.showSuccess(t('admin.accounts.reAuthorizedSuccess'))
      emit('reauthorized', updatedAccount)
      handleClose()
    } catch (error: any) {
      antigravityOAuth.error.value = error.response?.data?.detail || t('admin.accounts.oauth.authFailed')
      appStore.showError(antigravityOAuth.error.value)
    }
  } else if (isGrok.value) {
    const sessionId = grokOAuth.sessionId.value
    if (!sessionId) return

    const stateFromInput = oauthFlowRef.value?.oauthState || ''
    const stateToUse = stateFromInput || grokOAuth.state.value
    if (!stateToUse) return

    const tokenInfo = await grokOAuth.exchangeAuthCode({
      code: authCode.trim(),
      sessionId,
      state: stateToUse,
      proxyId: props.account.proxy_id
    })
    if (!tokenInfo) return

    const credentials = grokOAuth.buildCredentials(tokenInfo)
    const extra = grokOAuth.buildExtraInfo(tokenInfo)

    try {
      const updatedAccount = await adminAPI.accounts.applyOAuthCredentials(props.account.id, {
        type: 'oauth',
        credentials,
        extra
      })

      appStore.showSuccess(t('admin.accounts.reAuthorizedSuccess'))
      emit('reauthorized', updatedAccount)
      handleClose()
    } catch (error: any) {
      grokOAuth.error.value = error.response?.data?.detail || t('admin.accounts.oauth.authFailed')
      appStore.showError(grokOAuth.error.value)
    }
  } else {
    // Claude OAuth flow
    const sessionId = claudeOAuth.sessionId.value
    if (!sessionId) return

    claudeOAuth.loading.value = true
    claudeOAuth.error.value = ''

    try {
      const proxyConfig = props.account.proxy_id ? { proxy_id: props.account.proxy_id } : {}
      const endpoint =
        addMethod.value === 'oauth'
          ? '/admin/accounts/exchange-code'
          : '/admin/accounts/exchange-setup-token-code'

      const tokenInfo = await adminAPI.accounts.exchangeCode(endpoint, {
        session_id: sessionId,
        code: authCode.trim(),
        ...proxyConfig
      })

      const extra = claudeOAuth.buildExtraInfo(tokenInfo)

      const updatedAccount = await adminAPI.accounts.applyOAuthCredentials(props.account.id, {
        type: addMethod.value as 'oauth' | 'setup-token',
        credentials: tokenInfo as unknown as Record<string, unknown>,
        extra
      })

      appStore.showSuccess(t('admin.accounts.reAuthorizedSuccess'))
      emit('reauthorized', updatedAccount)
      handleClose()
    } catch (error: any) {
      claudeOAuth.error.value = error.response?.data?.detail || t('admin.accounts.oauth.authFailed')
      appStore.showError(claudeOAuth.error.value)
    } finally {
      claudeOAuth.loading.value = false
    }
  }
}

const handleCookieAuth = async (sessionKey: string) => {
  if (!props.account || isOpenAILike.value) return

  claudeOAuth.loading.value = true
  claudeOAuth.error.value = ''

  try {
    const proxyConfig = props.account.proxy_id ? { proxy_id: props.account.proxy_id } : {}
    const endpoint =
      addMethod.value === 'oauth'
        ? '/admin/accounts/cookie-auth'
        : '/admin/accounts/setup-token-cookie-auth'

    const tokenInfo = await adminAPI.accounts.exchangeCode(endpoint, {
      session_id: '',
      code: sessionKey.trim(),
      ...proxyConfig
    })

    const extra = claudeOAuth.buildExtraInfo(tokenInfo)

    const updatedAccount = await adminAPI.accounts.applyOAuthCredentials(props.account.id, {
      type: addMethod.value as 'oauth' | 'setup-token',
      credentials: tokenInfo as unknown as Record<string, unknown>,
      extra
    })

    appStore.showSuccess(t('admin.accounts.reAuthorizedSuccess'))
    emit('reauthorized', updatedAccount)
    handleClose()
  } catch (error: any) {
    claudeOAuth.error.value =
      error.response?.data?.detail || t('admin.accounts.oauth.cookieAuthFailed')
  } finally {
    claudeOAuth.loading.value = false
  }
}

/** Apply Grok Build OAuth tokens onto the existing account (never store password/SSO). */
const applyGrokReauthTokenInfo = async (tokenInfo: {
  access_token?: string
  refresh_token?: string
  email?: string
  [key: string]: unknown
}) => {
  if (!props.account) return
  const credentials = grokOAuth.buildCredentials(tokenInfo as any)
  const extra = grokOAuth.buildExtraInfo(tokenInfo as any)
  const updatedAccount = await adminAPI.accounts.applyOAuthCredentials(props.account.id, {
    type: 'oauth',
    credentials,
    extra
  })
  appStore.showSuccess(t('admin.accounts.reAuthorizedSuccess'))
  emit('reauthorized', updatedAccount)
  handleClose()
}

/** Re-auth the existing account with one refresh token. */
const handleValidateRefreshToken = async (refreshTokenInput: string) => {
  if (!props.account) return
  if (isGrok.value) {
    await handleGrokValidateRefreshToken(refreshTokenInput)
    return
  }

  const refreshToken = refreshTokenInput
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)[0]
  if (!refreshToken) return

  if (isOpenAILike.value) {
    const oauthClient = activeOpenAIOAuth.value
    oauthClient.loading.value = true
    oauthClient.error.value = ''
    try {
      if (isIndependentExcelTarget.value) {
        const tokenInfo = await oauthClient.validateRefreshToken(
          refreshToken,
          props.account.proxy_id,
          OPENAI_EXCEL_OAUTH_CLIENT_ID,
          'excel'
        )
        if (!tokenInfo) return

        const updatedAccount = await adminAPI.accounts.update(props.account.id, {
          credentials: {
            [OPENAI_EXCEL_BPS_CREDENTIALS_KEY]:
              oauthClient.buildIndependentExcelBPSCredentials(tokenInfo)
          }
        })
        appStore.showSuccess(t('admin.accounts.bpsAccount.grant.reauthExcelSavedSuccess'))
        emit('reauthorized', updatedAccount)
        handleClose()
        return
      }

      // BPS-only accounts refresh against the Excel client; Codex accounts keep the default client.
      const excelClientId = isOpenAIBPS.value ? OPENAI_EXCEL_OAUTH_CLIENT_ID : undefined
      const tokenInfo = await oauthClient.validateRefreshToken(
        refreshToken,
        props.account.proxy_id,
        excelClientId
      )
      if (!tokenInfo) return

      const updatedAccount = await adminAPI.accounts.applyOAuthCredentials(props.account.id, {
        type: 'oauth',
        credentials: oauthClient.buildCredentials(tokenInfo, excelClientId),
        extra: oauthClient.buildExtraInfo(tokenInfo)
      })
      appStore.showSuccess(t('admin.accounts.reAuthorizedSuccess'))
      emit('reauthorized', updatedAccount)
      handleClose()
    } catch (error: any) {
      oauthClient.error.value =
        error.response?.data?.detail ||
        error.response?.data?.message ||
        error.message ||
        t('admin.accounts.oauth.authFailed')
      appStore.showError(oauthClient.error.value)
    } finally {
      oauthClient.loading.value = false
    }
    return
  }

  if (!isAntigravity.value) return
  antigravityOAuth.loading.value = true
  antigravityOAuth.error.value = ''
  try {
    const tokenInfo = await antigravityOAuth.validateRefreshToken(refreshToken, props.account.proxy_id)
    if (!tokenInfo) return

    const updatedAccount = await adminAPI.accounts.applyOAuthCredentials(props.account.id, {
      type: 'oauth',
      credentials: antigravityOAuth.buildCredentials(tokenInfo, refreshToken)
    })
    appStore.showSuccess(t('admin.accounts.reAuthorizedSuccess'))
    emit('reauthorized', updatedAccount)
    handleClose()
  } catch (error: any) {
    antigravityOAuth.error.value =
      error.response?.data?.detail ||
      error.response?.data?.message ||
      error.message ||
      t('admin.accounts.oauth.authFailed')
    appStore.showError(antigravityOAuth.error.value)
  } finally {
    antigravityOAuth.loading.value = false
  }
}

/** Re-auth with a single SSO cookie → Build OAuth (not batch create). */
const handleGrokImportSSO = async (ssoInput: string) => {
  if (!props.account || !isGrok.value) return
  const ssoToken = ssoInput
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)[0]
  if (!ssoToken) return

  grokOAuth.loading.value = true
  grokOAuth.error.value = ''
  try {
    const tokenInfo = await grokOAuth.validateSSOToken(ssoToken, props.account.proxy_id)
    if (!tokenInfo) return
    await applyGrokReauthTokenInfo(tokenInfo)
  } catch (error: any) {
    grokOAuth.error.value =
      error.response?.data?.detail ||
      error.message ||
      t('admin.accounts.oauth.grok.failedToValidateSSO', 'Failed to validate Grok SSO')
    appStore.showError(grokOAuth.error.value)
  } finally {
    grokOAuth.loading.value = false
  }
}

/** Re-auth with a single refresh token. */
const handleGrokValidateRefreshToken = async (refreshTokenInput: string) => {
  if (!props.account || !isGrok.value) return
  const refreshToken = refreshTokenInput
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)[0]
  if (!refreshToken) {
    grokOAuth.error.value = t('admin.accounts.oauth.grok.pleaseEnterRefreshToken')
    return
  }

  grokOAuth.loading.value = true
  grokOAuth.error.value = ''
  try {
    const tokenInfo = await grokOAuth.validateRefreshToken(refreshToken, props.account.proxy_id)
    if (!tokenInfo) return
    await applyGrokReauthTokenInfo(tokenInfo)
  } catch (error: any) {
    grokOAuth.error.value =
      error.response?.data?.detail ||
      error.message ||
      t('admin.accounts.oauth.grok.failedToValidateRT')
    appStore.showError(grokOAuth.error.value)
  } finally {
    grokOAuth.loading.value = false
  }
}
</script>
