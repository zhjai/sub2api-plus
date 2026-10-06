<template>
  <BaseDialog :show="show" :title="dialogTitle" width="wide" @close="requestClose">
    <div class="space-y-5" data-testid="prism-account-dialog">
      <p class="text-sm text-gray-600 dark:text-gray-300">
        {{ relogin ? t('admin.accounts.prism.reloginIntro', { name: account?.name || `#${account?.id}` }) : t('admin.accounts.prism.intro') }}
      </p>

      <!-- Method switch (relogin is browser-only: identity must match the account) -->
      <div v-if="!relogin" class="flex rounded-lg bg-gray-100 p-1 dark:bg-dark-700" role="tablist">
        <button
          v-for="option in methodOptions"
          :key="option.value"
          type="button"
          role="tab"
          :aria-selected="method === option.value"
          :disabled="busy"
          :data-testid="`prism-method-${option.value}`"
          class="flex flex-1 items-center justify-center gap-2 rounded-md px-3 py-2 text-sm font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-60"
          :class="method === option.value
            ? 'bg-white text-fuchsia-700 shadow-sm dark:bg-dark-600 dark:text-fuchsia-300'
            : 'text-gray-600 hover:text-gray-900 dark:text-gray-400 dark:hover:text-gray-200'"
          @click="method = option.value"
        >
          <Icon :name="option.icon" size="sm" />
          {{ option.label }}
        </button>
      </div>

      <!-- Shared account settings (new accounts only) -->
      <section v-if="!relogin" class="space-y-4" :aria-label="t('admin.accounts.prism.settingsTitle')">
        <div class="grid gap-4 sm:grid-cols-2">
          <div>
            <label class="input-label" for="prism-name">{{ t('admin.accounts.accountName') }}</label>
            <input
              id="prism-name"
              v-model="settings.name"
              type="text"
              class="input"
              maxlength="100"
              :disabled="busy || nameLocked"
              :placeholder="t('admin.accounts.prism.namePlaceholder')"
              data-testid="prism-name"
            />
            <p class="input-hint">{{ nameLocked ? t('admin.accounts.prism.nameAutoBatch') : t('admin.accounts.prism.nameHint') }}</p>
          </div>
          <div>
            <label class="input-label">{{ t('admin.accounts.proxy') }}</label>
            <ProxySelector v-model="settings.proxy_id" :proxies="proxies" :disabled="busy" />
            <p class="input-hint">{{ t('admin.accounts.prism.proxyHint') }}</p>
          </div>
          <div>
            <label class="input-label" for="prism-concurrency">{{ t('admin.accounts.concurrency') }}</label>
            <input
              id="prism-concurrency"
              v-model.number="settings.concurrency"
              type="number"
              min="1"
              max="1000"
              step="1"
              class="input"
              :disabled="busy"
              data-testid="prism-concurrency"
            />
          </div>
          <div>
            <label class="input-label" for="prism-priority">{{ t('admin.accounts.priority') }}</label>
            <input
              id="prism-priority"
              v-model.number="settings.priority"
              type="number"
              min="0"
              step="1"
              class="input"
              :disabled="busy"
              data-testid="prism-priority"
            />
            <p class="input-hint">{{ t('admin.accounts.priorityHint') }}</p>
          </div>
        </div>
        <GroupSelector v-model="settings.group_ids" :groups="groups" platform="prism" />
        <p class="input-hint">{{ t('admin.accounts.prism.laterSettingsHint') }}</p>
        <p v-if="settingsIssue" class="text-sm text-red-600 dark:text-red-400" role="alert">{{ settingsIssue }}</p>
      </section>

      <!-- Browser sign-in -->
      <section v-if="method === 'browser'" class="space-y-4" data-testid="prism-browser">
        <ol class="space-y-4">
          <li class="flex gap-3">
            <span class="prism-step" :class="session ? 'prism-step-done' : 'prism-step-active'">1</span>
            <div class="min-w-0 flex-1 space-y-2">
              <p class="text-sm font-medium text-gray-900 dark:text-white">{{ t('admin.accounts.prism.browser.step1') }}</p>
              <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.prism.browser.step1Hint') }}</p>
              <div v-if="!session || isTerminal" class="flex flex-wrap gap-2">
                <button
                  type="button"
                  class="btn btn-primary"
                  :disabled="beginning || (!relogin && Boolean(settingsIssue))"
                  data-testid="prism-begin"
                  @click="begin"
                >
                  <Icon v-if="beginning" name="refresh" size="sm" class="mr-1.5 animate-spin" />
                  {{ session ? t('admin.accounts.prism.browser.restart') : t('admin.accounts.prism.browser.begin') }}
                </button>
              </div>
              <div v-else class="flex flex-wrap items-center gap-2">
                <a
                  :href="session.authorize_url"
                  target="_blank"
                  rel="noopener noreferrer"
                  class="btn btn-primary"
                  data-testid="prism-open-auth"
                >
                  <Icon name="externalLink" size="sm" class="mr-1.5" />
                  {{ t('admin.accounts.prism.browser.openPage') }}
                </a>
                <button type="button" class="btn btn-secondary" @click="copyAuthorizeUrl">
                  <Icon name="copy" size="sm" class="mr-1.5" />
                  {{ t('admin.accounts.prism.browser.copyLink') }}
                </button>
                <span v-if="remainingLabel" class="text-xs tabular-nums text-gray-500 dark:text-gray-400" data-testid="prism-expiry">
                  {{ remainingLabel }}
                </span>
              </div>
            </div>
          </li>

          <li class="flex gap-3">
            <span class="prism-step" :class="callbackStepClass">2</span>
            <div class="min-w-0 flex-1 space-y-2">
              <label class="text-sm font-medium text-gray-900 dark:text-white" for="prism-callback">{{ t('admin.accounts.prism.browser.step2') }}</label>
              <p class="text-xs text-gray-500 dark:text-gray-400">
                {{ t('admin.accounts.prism.browser.step2Hint', { uri: session?.redirect_uri || CALLBACK_EXAMPLE }) }}
              </p>
              <input
                id="prism-callback"
                v-model="callbackUrl"
                type="text"
                class="input font-mono text-xs"
                autocomplete="off"
                spellcheck="false"
                :disabled="!canExchange"
                :placeholder="`${CALLBACK_EXAMPLE}?code=…&state=…`"
                data-testid="prism-callback"
                @keydown.enter.prevent="exchange"
              />
              <p v-if="callbackHint" class="text-xs text-amber-700 dark:text-amber-300">{{ callbackHint }}</p>
              <div class="flex flex-wrap gap-2">
                <button
                  type="button"
                  class="btn btn-primary"
                  :disabled="!canExchange || Boolean(callbackHint) || !callbackUrl.trim()"
                  data-testid="prism-exchange"
                  @click="exchange"
                >
                  <Icon v-if="exchanging" name="refresh" size="sm" class="mr-1.5 animate-spin" />
                  {{ exchanging ? t('admin.accounts.prism.browser.verifying') : t('admin.accounts.prism.browser.complete') }}
                </button>
                <button
                  v-if="session && !isTerminal"
                  type="button"
                  class="btn btn-secondary"
                  :disabled="canceling"
                  data-testid="prism-cancel-session"
                  @click="cancelSession"
                >
                  {{ t('admin.accounts.prism.browser.cancel') }}
                </button>
              </div>
            </div>
          </li>
        </ol>

        <!-- Two independent checkpoints: token login, then Prism entitlement -->
        <div v-if="session" class="prism-gates" data-testid="prism-gates">
          <div class="prism-gate" :data-state="loginGate.state" data-testid="prism-gate-login">
            <span class="prism-gate-mark" aria-hidden="true">
              <Icon v-if="loginGate.state === 'done'" name="check" size="sm" :stroke-width="2.5" />
              <Icon v-else-if="loginGate.state === 'failed'" name="x" size="sm" :stroke-width="2.5" />
              <Icon v-else-if="loginGate.state === 'active'" name="refresh" size="sm" class="animate-spin" />
            </span>
            <div class="min-w-0">
              <p class="text-sm font-medium text-gray-900 dark:text-white">{{ t('admin.accounts.prism.gates.login') }}</p>
              <p class="text-xs text-gray-600 dark:text-gray-300">{{ loginGate.text }}</p>
            </div>
          </div>
          <div class="prism-gate-link" :data-state="loginGate.state" aria-hidden="true"></div>
          <div class="prism-gate" :data-state="accessGate.state" data-testid="prism-gate-access">
            <span class="prism-gate-mark" aria-hidden="true">
              <Icon v-if="accessGate.state === 'done'" name="check" size="sm" :stroke-width="2.5" />
              <Icon v-else-if="accessGate.state === 'failed'" name="x" size="sm" :stroke-width="2.5" />
              <Icon v-else-if="accessGate.state === 'active'" name="refresh" size="sm" class="animate-spin" />
            </span>
            <div class="min-w-0">
              <p class="text-sm font-medium text-gray-900 dark:text-white">{{ t('admin.accounts.prism.gates.access') }}</p>
              <p class="text-xs text-gray-600 dark:text-gray-300">{{ accessGate.text }}</p>
            </div>
          </div>
        </div>
        <p
          v-if="sessionMessage"
          class="rounded-lg px-3 py-2 text-sm"
          :class="session?.status === 'completed'
            ? 'bg-emerald-50 text-emerald-800 dark:bg-emerald-900/20 dark:text-emerald-300'
            : 'bg-red-50 text-red-700 dark:bg-red-900/20 dark:text-red-300'"
          role="status"
          data-testid="prism-session-message"
        >
          {{ sessionMessage }}
        </p>
      </section>

      <!-- Cookie import -->
      <section v-else class="space-y-3" data-testid="prism-cookies">
        <div>
          <label class="input-label" for="prism-cookie-input">{{ t('admin.accounts.prism.cookies.label') }}</label>
          <textarea
            id="prism-cookie-input"
            v-model="cookieText"
            rows="6"
            class="input font-mono text-xs"
            autocomplete="off"
            spellcheck="false"
            :disabled="importing"
            :placeholder="t('admin.accounts.prism.cookies.placeholder')"
            data-testid="prism-cookie-input"
          ></textarea>
          <div class="mt-1 flex flex-wrap items-center justify-between gap-2 text-xs">
            <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.prism.cookies.hint') }}</span>
            <span
              class="tabular-nums"
              :class="cookieLines.length > PRISM_IMPORT_MAX_ACCOUNTS ? 'text-red-600 dark:text-red-400' : 'text-gray-500 dark:text-gray-400'"
              data-testid="prism-cookie-count"
            >
              {{ t('admin.accounts.prism.cookies.count', { count: cookieLines.length, max: PRISM_IMPORT_MAX_ACCOUNTS }) }}
            </span>
          </div>
        </div>
        <label class="flex items-start gap-2 text-sm text-gray-700 dark:text-gray-300">
          <input
            v-model="updateExisting"
            type="checkbox"
            class="mt-0.5 h-4 w-4 rounded border-gray-300 text-primary-600 focus:ring-primary-500 dark:border-dark-500"
            :disabled="importing"
            data-testid="prism-update-existing"
          />
          <span>
            {{ t('admin.accounts.prism.cookies.updateExisting') }}
            <span class="block text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.prism.cookies.updateExistingHint') }}</span>
          </span>
        </label>
        <p class="flex items-start gap-2 text-xs text-gray-500 dark:text-gray-400">
          <Icon name="shield" size="sm" class="mt-px shrink-0 text-fuchsia-600 dark:text-fuchsia-300" />
          {{ t('admin.accounts.prism.cookies.verifyNote') }}
        </p>

        <div v-if="importing" class="space-y-1.5" role="status" data-testid="prism-import-progress">
          <div class="flex items-center justify-between text-xs text-gray-600 dark:text-gray-300">
            <span>{{ t('admin.accounts.prism.cookies.progress', { done: importDone, total: importTotal }) }}</span>
            <span class="text-gray-400">{{ t('admin.accounts.prism.cookies.stopHint') }}</span>
          </div>
          <div class="h-1.5 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700">
            <div class="h-full rounded-full bg-fuchsia-500 transition-[width] duration-300" :style="{ width: `${importPercent}%` }"></div>
          </div>
        </div>

        <div v-if="importSummary" class="space-y-2 rounded-lg border border-gray-200 p-3 dark:border-dark-600" data-testid="prism-import-result">
          <p class="text-sm font-medium text-gray-900 dark:text-white">{{ importSummary }}</p>
          <ul v-if="importRows.length" class="max-h-48 space-y-1 overflow-auto text-xs">
            <li v-for="row in importRows" :key="row.line" class="flex items-start gap-2">
              <span class="w-14 shrink-0 tabular-nums text-gray-400">{{ t('admin.accounts.prism.cookies.line', { line: row.line }) }}</span>
              <span class="shrink-0 rounded px-1.5 py-px font-medium" :class="actionClass(row.action)">{{ actionLabel(row.action) }}</span>
              <span class="min-w-0 flex-1 text-gray-600 dark:text-gray-300">
                <template v-if="row.accountId">#{{ row.accountId }}</template>
                <template v-if="row.message">{{ row.accountId ? ' ' : '' }}{{ row.message }}</template>
              </span>
            </li>
          </ul>
          <p v-if="keptFailedLines" class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.prism.cookies.keptFailed') }}</p>
        </div>
      </section>
    </div>

    <template #footer>
      <div class="flex justify-end gap-3">
        <button v-if="importing" type="button" class="btn btn-secondary" :disabled="stopping" data-testid="prism-stop-import" @click="stopImport">
          {{ stopping ? t('admin.accounts.prism.cookies.stopping') : t('admin.accounts.prism.cookies.stop') }}
        </button>
        <button v-else type="button" class="btn btn-secondary" :disabled="exchanging" @click="requestClose">
          {{ changed ? t('common.close') : t('common.cancel') }}
        </button>
        <button
          v-if="method === 'cookies' && !relogin"
          type="button"
          class="btn btn-primary"
          :disabled="importing || !cookieLines.length || cookieLines.length > PRISM_IMPORT_MAX_ACCOUNTS || Boolean(settingsIssue)"
          data-testid="prism-import"
          @click="runImport"
        >
          <Icon v-if="importing" name="refresh" size="sm" class="mr-1.5 animate-spin" />
          {{ t('admin.accounts.prism.cookies.submit', { count: cookieLines.length }) }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import GroupSelector from '@/components/common/GroupSelector.vue'
import ProxySelector from '@/components/common/ProxySelector.vue'
import Icon from '@/components/icons/Icon.vue'
import { useAppStore } from '@/stores/app'
import { useClipboard } from '@/composables/useClipboard'
import {
  PRISM_IMPORT_MAX_ACCOUNTS,
  chunkPrismEntries,
  isPrismOAuthTerminal,
  parsePrismCookieLines,
  prismAPI,
  type PrismImportEntry,
  type PrismOAuthStatus
} from '@/api/admin/prism'
import type { Account, Group, Proxy } from '@/types'
import { callbackIssue, isRequestCanceled, prismErrorText, prismRequestErrorText } from './prismText'

const CALLBACK_EXAMPLE = 'http://localhost:1455/auth/callback'
const STATUS_POLL_MS = 5000

type Method = 'browser' | 'cookies'
type GateState = 'idle' | 'active' | 'done' | 'failed'

const props = withDefaults(defineProps<{
  show: boolean
  proxies?: Proxy[]
  groups?: Group[]
  /** When set, sign in again for this Prism account instead of adding one. */
  account?: Account | null
}>(), {
  proxies: () => [],
  groups: () => [],
  account: null
})

const emit = defineEmits<{
  close: []
  /** Accounts were created or updated; the list should reload. */
  changed: []
}>()

const { t, te } = useI18n()
const appStore = useAppStore()
const { copyToClipboard } = useClipboard()

const relogin = computed(() => props.account != null)
const dialogTitle = computed(() => relogin.value ? t('admin.accounts.prism.reloginTitle') : t('admin.accounts.prism.title'))

const methodOptions = computed(() => [
  { value: 'browser' as Method, label: t('admin.accounts.prism.methods.browser'), icon: 'login' as const },
  { value: 'cookies' as Method, label: t('admin.accounts.prism.methods.cookies'), icon: 'key' as const }
])

const method = ref<Method>('browser')
const settings = reactive({
  name: '',
  group_ids: [] as number[],
  proxy_id: null as number | null,
  concurrency: 2,
  priority: 50
})
const changed = ref(false)

const settingsIssue = computed(() => {
  const c = settings.concurrency
  if (!Number.isInteger(c) || c < 1 || c > 1000) return t('admin.accounts.prism.concurrencyInvalid')
  const p = settings.priority
  if (!Number.isInteger(p) || p < 0) return t('admin.accounts.prism.priorityInvalid')
  return ''
})

function baseRequest() {
  return {
    group_ids: [...settings.group_ids],
    proxy_id: settings.proxy_id,
    concurrency: settings.concurrency,
    priority: settings.priority
  }
}

// ---------------- Browser sign-in ----------------

const session = ref<PrismOAuthStatus | null>(null)
const callbackUrl = ref('')
const beginning = ref(false)
const exchanging = ref(false)
const canceling = ref(false)
const now = ref(Date.now())
let exchangeController: AbortController | null = null
let pollTimer: ReturnType<typeof setInterval> | null = null
let clockTimer: ReturnType<typeof setInterval> | null = null
let pollInFlight = false

const isTerminal = computed(() => session.value ? isPrismOAuthTerminal(session.value.status) : false)
const canExchange = computed(() => session.value?.status === 'pending' && !exchanging.value)

const remainingLabel = computed(() => {
  if (!session.value || session.value.status !== 'pending') return ''
  const expires = Date.parse(session.value.expires_at)
  if (!Number.isFinite(expires)) return ''
  const seconds = Math.max(0, Math.floor((expires - now.value) / 1000))
  const mm = Math.floor(seconds / 60)
  const ss = String(seconds % 60).padStart(2, '0')
  return t('admin.accounts.prism.browser.expiresIn', { time: `${mm}:${ss}` })
})

const callbackHint = computed(() => {
  if (!callbackUrl.value.trim()) return ''
  const issue = callbackIssue(callbackUrl.value)
  return issue ? t(`admin.accounts.prism.callbackIssues.${issue}`) : ''
})

const callbackStepClass = computed(() => {
  if (!session.value) return 'prism-step-idle'
  if (session.value.status === 'pending') return 'prism-step-active'
  return 'prism-step-done'
})

const loginGate = computed<{ state: GateState; text: string }>(() => {
  const s = session.value
  if (!s) return { state: 'idle', text: '' }
  if (s.login_succeeded) return { state: 'done', text: t('admin.accounts.prism.gates.loginDone') }
  if (s.status === 'exchanging' || exchanging.value) return { state: 'active', text: t('admin.accounts.prism.gates.loginChecking') }
  if (s.status === 'pending') return { state: 'idle', text: t('admin.accounts.prism.gates.loginWaiting') }
  return { state: 'failed', text: t(`admin.accounts.prism.gates.ended.${s.status}`) }
})

const accessGate = computed<{ state: GateState; text: string }>(() => {
  const s = session.value
  if (!s) return { state: 'idle', text: '' }
  if (s.prism_verified) {
    return {
      state: 'done',
      text: s.account_id
        ? t('admin.accounts.prism.gates.accessDoneWithId', { id: s.account_id })
        : t('admin.accounts.prism.gates.accessDone')
    }
  }
  if (s.login_succeeded && s.status === 'failed') return { state: 'failed', text: t('admin.accounts.prism.gates.accessFailed') }
  if (s.status === 'exchanging' || exchanging.value) return { state: 'active', text: t('admin.accounts.prism.gates.accessChecking') }
  if (isTerminal.value) return { state: 'idle', text: t('admin.accounts.prism.gates.accessNotChecked') }
  return { state: 'idle', text: t('admin.accounts.prism.gates.accessWaiting') }
})

const sessionMessage = computed(() => {
  const s = session.value
  if (!s) return ''
  if (s.status === 'completed') {
    return relogin.value ? t('admin.accounts.prism.browser.reloginDone') : t('admin.accounts.prism.browser.completed')
  }
  if (s.status === 'failed' || s.status === 'expired') return prismErrorText(t, te, s.code, s.message)
  return ''
})

function startTimers() {
  stopTimers()
  clockTimer = setInterval(() => { now.value = Date.now() }, 1000)
  pollTimer = setInterval(pollStatus, STATUS_POLL_MS)
}

function stopTimers() {
  if (clockTimer) clearInterval(clockTimer)
  if (pollTimer) clearInterval(pollTimer)
  clockTimer = null
  pollTimer = null
}

function applySession(next: PrismOAuthStatus) {
  // A late poll must not move a finished session back to pending.
  if (session.value && session.value.session_id === next.session_id && isTerminal.value && !isPrismOAuthTerminal(next.status)) return
  session.value = { ...session.value, ...next }
  if (isPrismOAuthTerminal(next.status)) {
    stopTimers()
    callbackUrl.value = ''
    if (next.status === 'completed') {
      changed.value = true
      emit('changed')
    }
  }
}

async function begin() {
  if (beginning.value) return
  beginning.value = true
  try {
    const request = relogin.value && props.account
      ? { account_id: props.account.id }
      : { ...baseRequest(), name: settings.name.trim() || undefined }
    const started = await prismAPI.beginOAuth(request)
    session.value = started
    callbackUrl.value = ''
    now.value = Date.now()
    startTimers()
    if (started.authorize_url) window.open(started.authorize_url, '_blank', 'noopener,noreferrer')
  } catch (error) {
    appStore.showError(prismRequestErrorText(t, te, error))
  } finally {
    beginning.value = false
  }
}

async function pollStatus() {
  const s = session.value
  if (!s || isTerminal.value || exchanging.value || pollInFlight) return
  pollInFlight = true
  try {
    applySession(await prismAPI.getOAuthStatus(s.session_id))
  } catch {
    // The next tick retries; expiry is also tracked by the local clock.
  } finally {
    pollInFlight = false
  }
}

async function exchange() {
  const s = session.value
  if (!s || !canExchange.value || callbackHint.value || !callbackUrl.value.trim()) return
  exchanging.value = true
  exchangeController = new AbortController()
  session.value = { ...s, status: 'exchanging' }
  try {
    applySession(await prismAPI.exchangeOAuth(s.session_id, callbackUrl.value.trim(), { signal: exchangeController.signal }))
  } catch (error) {
    if (!isRequestCanceled(error)) {
      // The callback may have been rejected before the session was used, or
      // the connection dropped mid-exchange; the server status is the truth.
      try {
        applySession(await prismAPI.getOAuthStatus(s.session_id))
      } catch {
        session.value = { ...s }
      }
      if (session.value?.status === 'pending') appStore.showError(prismRequestErrorText(t, te, error))
    }
  } finally {
    exchanging.value = false
    exchangeController = null
  }
}

async function cancelSession() {
  const s = session.value
  if (!s || isTerminal.value || canceling.value) return
  canceling.value = true
  try {
    applySession(await prismAPI.cancelOAuth(s.session_id))
    exchangeController?.abort()
  } catch (error) {
    appStore.showError(prismRequestErrorText(t, te, error))
  } finally {
    canceling.value = false
  }
}

async function copyAuthorizeUrl() {
  if (session.value?.authorize_url) {
    await copyToClipboard(session.value.authorize_url, t('admin.accounts.prism.browser.linkCopied'))
  }
}

// ---------------- Cookie import ----------------

interface ImportRow {
  line: number
  action: string
  accountId?: number
  message?: string
}

const cookieText = ref('')
const updateExisting = ref(true)
const importing = ref(false)
const stopping = ref(false)
const importDone = ref(0)
const importTotal = ref(0)
const importRows = ref<ImportRow[]>([])
const importSummary = ref('')
const keptFailedLines = ref(false)
let importController: AbortController | null = null

/** Settings are sent when a sign-in starts, so they lock until it ends. */
const busy = computed(() =>
  importing.value || beginning.value || exchanging.value || Boolean(session.value && !isTerminal.value)
)

const cookieLines = computed(() => parsePrismCookieLines(cookieText.value))
const nameLocked = computed(() => method.value === 'cookies' && cookieLines.value.length > 1)
const importPercent = computed(() => importTotal.value ? Math.round((importDone.value / importTotal.value) * 100) : 0)

function actionLabel(action: string) {
  return te(`admin.accounts.prism.actions.${action}`) ? t(`admin.accounts.prism.actions.${action}`) : action
}

function actionClass(action: string) {
  switch (action) {
    case 'created': return 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300'
    case 'updated': return 'bg-sky-50 text-sky-700 dark:bg-sky-900/30 dark:text-sky-300'
    case 'skipped': return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'
    default: return 'bg-red-50 text-red-700 dark:bg-red-900/30 dark:text-red-300'
  }
}

async function runImport() {
  const lines = cookieLines.value
  if (importing.value || !lines.length || lines.length > PRISM_IMPORT_MAX_ACCOUNTS || settingsIssue.value) return
  const entries: PrismImportEntry[] = lines.map((cookies) => ({ cookies }))
  const name = settings.name.trim()
  if (entries.length === 1 && name) entries[0].name = name

  importing.value = true
  stopping.value = false
  importDone.value = 0
  importTotal.value = entries.length
  importRows.value = []
  importSummary.value = ''
  keptFailedLines.value = false
  importController = new AbortController()

  const rows: ImportRow[] = []
  const failedLines = new Set<number>()
  let created = 0
  let updated = 0
  let skipped = 0
  let stoppedAt: number | null = null
  let start = 0

  for (const chunk of chunkPrismEntries(entries)) {
    if (stopping.value) { stoppedAt = start; break }
    try {
      const result = await prismAPI.importAccounts(
        { ...baseRequest(), accounts: chunk, update_existing: updateExisting.value },
        { signal: importController.signal }
      )
      for (const item of result.items) {
        const line = start + item.index
        const row: ImportRow = { line, action: item.action, accountId: item.account_id }
        if (item.action === 'created') created++
        else if (item.action === 'updated') updated++
        else if (item.action === 'skipped') skipped++
        else {
          failedLines.add(line)
          row.message = prismErrorText(t, te, item.code, item.message)
        }
        rows.push(row)
      }
      if (result.created || result.updated) changed.value = true
    } catch (error) {
      if (isRequestCanceled(error)) { stoppedAt = start; break }
      const message = prismRequestErrorText(t, te, error)
      for (let i = 0; i < chunk.length; i++) {
        failedLines.add(start + i + 1)
        rows.push({ line: start + i + 1, action: 'failed', message })
      }
    }
    start += chunk.length
    importDone.value = Math.min(start, entries.length)
    importRows.value = [...rows]
  }

  const notRun = stoppedAt == null ? 0 : entries.length - stoppedAt
  importSummary.value = t('admin.accounts.prism.cookies.summary', {
    created,
    updated,
    skipped,
    failed: failedLines.size
  }) + (notRun ? ` ${t('admin.accounts.prism.cookies.notRun', { count: notRun })}` : '')

  // Keep only lines that still need attention so a retry does not resubmit saved accounts.
  const keepLines = new Set<number>(failedLines)
  if (stoppedAt != null) for (let line = stoppedAt + 1; line <= entries.length; line++) keepLines.add(line)
  cookieText.value = lines.filter((_, index) => keepLines.has(index + 1)).join('\n')
  keptFailedLines.value = keepLines.size > 0

  importing.value = false
  stopping.value = false
  importController = null
  if (changed.value) emit('changed')
}

function stopImport() {
  if (!importing.value) return
  stopping.value = true
  importController?.abort()
}

// ---------------- Lifecycle ----------------

function reset() {
  stopTimers()
  method.value = 'browser'
  settings.name = ''
  settings.group_ids = []
  settings.proxy_id = null
  settings.concurrency = 2
  settings.priority = 50
  session.value = null
  callbackUrl.value = ''
  cookieText.value = ''
  updateExisting.value = true
  importRows.value = []
  importSummary.value = ''
  keptFailedLines.value = false
  changed.value = false
}

/** Pending sign-ins are canceled on close so no session is left waiting for a callback. */
function abandonSession() {
  const s = session.value
  if (s && !isPrismOAuthTerminal(s.status) && !exchanging.value) {
    prismAPI.cancelOAuth(s.session_id).catch(() => undefined)
  }
  stopTimers()
}

function requestClose() {
  if (importing.value || exchanging.value) return
  abandonSession()
  emit('close')
}

watch(() => props.show, (open) => {
  if (open) reset()
  else abandonSession()
})

watch(() => props.account?.id, () => {
  if (props.show) reset()
})

onBeforeUnmount(() => {
  abandonSession()
  importController?.abort()
  exchangeController?.abort()
})
</script>

<style scoped>
.prism-step {
  @apply flex h-6 w-6 shrink-0 items-center justify-center rounded-full text-xs font-semibold tabular-nums;
}
.prism-step-idle {
  @apply bg-gray-100 text-gray-400 dark:bg-dark-700 dark:text-gray-500;
}
.prism-step-active {
  @apply bg-fuchsia-600 text-white;
}
.prism-step-done {
  @apply bg-fuchsia-100 text-fuchsia-700 dark:bg-fuchsia-900/40 dark:text-fuchsia-300;
}

.prism-gates {
  @apply flex flex-col gap-0 rounded-lg border border-gray-200 p-3 sm:flex-row sm:items-center sm:gap-3 dark:border-dark-600;
}
.prism-gate {
  @apply flex min-w-0 flex-1 items-start gap-3;
}
.prism-gate-mark {
  @apply mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-full border-2 border-gray-300 text-gray-400 dark:border-dark-500;
}
.prism-gate[data-state='active'] .prism-gate-mark {
  @apply border-fuchsia-500 text-fuchsia-600 dark:text-fuchsia-300;
}
.prism-gate[data-state='done'] .prism-gate-mark {
  @apply border-emerald-500 bg-emerald-500 text-white;
}
.prism-gate[data-state='failed'] .prism-gate-mark {
  @apply border-red-500 bg-red-500 text-white;
}
.prism-gate-link {
  @apply ml-3 h-4 w-0.5 bg-gray-200 sm:ml-0 sm:h-0.5 sm:w-8 dark:bg-dark-600;
}
.prism-gate-link[data-state='done'] {
  @apply bg-emerald-400;
}
</style>
