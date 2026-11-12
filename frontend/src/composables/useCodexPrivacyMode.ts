import { computed, ref } from 'vue'
import { extractApiErrorCode, extractApiErrorMessage } from '@/utils/apiError'

/**
 * Client fingerprint protection (extra.codex_privacy_mode).
 *
 * Mirrors the backend contract in backend/internal/service/openai_codex_privacy.go:
 * - values are `off` or `randomized`; missing means off (explicit opt-in);
 * - supported on OpenAI OAuth, setup-token and API key accounts, never on
 *   shadows (parent_account_id) or Agent Identity credentials;
 * - mutually exclusive with codex_identity_mode=preserve_client and with any
 *   device/session/full codex_fingerprint_mode — validated against the raw
 *   extra, so this also applies to API key accounts that have no convergence UI;
 * - an update that omits the key keeps the stored value, so turning the switch
 *   off on a saved account must send `off` explicitly.
 */

export const CODEX_PRIVACY_MODE_KEY = 'codex_privacy_mode'
export type CodexPrivacyMode = 'off' | 'randomized'
export type CodexPrivacyConflict = 'fingerprint' | 'preserve_client'
export type CodexConvergenceMode = 'off' | 'device' | 'session' | 'full'

export interface CodexPrivacyAccountLike {
  platform?: string | null
  type?: string | null
  parent_account_id?: number | null
  credentials?: Record<string, unknown> | null
}

export interface CodexPrivacyConflictSources {
  fingerprintMode: unknown
  identityMode: unknown
}

export interface CodexPrivacyServerError {
  code: string
  message: string
}

const SUPPORTED_TYPES = new Set(['oauth', 'setup-token', 'apikey'])
const AGENT_IDENTITY_AUTH_MODE = 'agentidentity'

export function isCodexPrivacyEligible(account: CodexPrivacyAccountLike | null | undefined): boolean {
  if (!account || account.platform !== 'openai') return false
  if (!SUPPORTED_TYPES.has(String(account.type ?? ''))) return false
  if (account.parent_account_id != null) return false
  const authMode = account.credentials?.auth_mode
  return !(typeof authMode === 'string' && authMode.trim().toLowerCase() === AGENT_IDENTITY_AUTH_MODE)
}

export function readCodexPrivacyMode(extra: Record<string, unknown> | null | undefined): CodexPrivacyMode {
  return extra?.[CODEX_PRIVACY_MODE_KEY] === 'randomized' ? 'randomized' : 'off'
}

/** Same normalization as codexFingerprintModeFromExtra: unknown values count as off. */
export function effectiveCodexFingerprintMode(value: unknown): CodexConvergenceMode {
  const mode = typeof value === 'string' ? value.trim() : ''
  return mode === 'device' || mode === 'session' || mode === 'full' ? mode : 'off'
}

export function findCodexPrivacyConflicts(sources: CodexPrivacyConflictSources): CodexPrivacyConflict[] {
  const conflicts: CodexPrivacyConflict[] = []
  if (effectiveCodexFingerprintMode(sources.fingerprintMode) !== 'off') conflicts.push('fingerprint')
  if (sources.identityMode === 'preserve_client') conflicts.push('preserve_client')
  return conflicts
}

export function isCodexPrivacyError(error: unknown): boolean {
  const code = extractApiErrorCode(error) ?? ''
  if (/^CODEX_PRIVACY_/i.test(code)) return true
  const message = typeof error === 'object' && error !== null ? String((error as { message?: unknown }).message ?? '') : ''
  return /codex_privacy|randomized metadata/i.test(message)
}

/**
 * Account-level switch state shared by the create and edit modals.
 * `getConflictSources` returns the values that will actually be saved next to
 * codex_privacy_mode, so the check matches what the server validates.
 */
export function useCodexPrivacyMode(getConflictSources: () => CodexPrivacyConflictSources) {
  const enabled = ref(false)
  const savedKeyPresent = ref(false)
  const saveAttempted = ref(false)
  const serverError = ref<CodexPrivacyServerError | null>(null)

  const conflicts = computed<CodexPrivacyConflict[]>(() =>
    enabled.value ? findCodexPrivacyConflicts(getConflictSources()) : []
  )
  const saveBlocked = computed(() => saveAttempted.value && conflicts.value.length > 0)

  function clearServerError() {
    serverError.value = null
  }

  function reset() {
    enabled.value = false
    savedKeyPresent.value = false
    saveAttempted.value = false
    serverError.value = null
  }

  function load(extra: Record<string, unknown> | null | undefined) {
    reset()
    savedKeyPresent.value = !!extra && Object.prototype.hasOwnProperty.call(extra, CODEX_PRIVACY_MODE_KEY)
    enabled.value = readCodexPrivacyMode(extra) === 'randomized'
  }

  function setEnabled(value: boolean) {
    enabled.value = value
    saveAttempted.value = false
    serverError.value = null
  }

  function toggle() {
    setEnabled(!enabled.value)
  }

  /** Returns false (and marks the inline conflict as blocking) when saving would be rejected. */
  function validateBeforeSave(): boolean {
    if (conflicts.value.length > 0) {
      saveAttempted.value = true
      return false
    }
    saveAttempted.value = false
    return true
  }

  /** Off is the default and is not written on new rows; a stored value is replaced explicitly. */
  function writeTo(extra: Record<string, unknown>) {
    if (enabled.value) {
      extra[CODEX_PRIVACY_MODE_KEY] = 'randomized'
    } else if (savedKeyPresent.value) {
      extra[CODEX_PRIVACY_MODE_KEY] = 'off'
    } else {
      delete extra[CODEX_PRIVACY_MODE_KEY]
    }
  }

  /** Records a CODEX_PRIVACY_* rejection with the server's own reason. Returns the message, or null. */
  function captureServerError(error: unknown, fallback = 'Unknown error'): string | null {
    if (!isCodexPrivacyError(error)) return null
    const message = extractApiErrorMessage(error, fallback)
    serverError.value = { code: extractApiErrorCode(error) ?? '', message }
    return message
  }

  return {
    enabled,
    savedKeyPresent,
    conflicts,
    saveBlocked,
    serverError,
    reset,
    load,
    setEnabled,
    toggle,
    validateBeforeSave,
    writeTo,
    captureServerError,
    clearServerError
  }
}
