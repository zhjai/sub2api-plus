import {
  OPENAI_EXCEL_BPS_CREDENTIALS_KEY,
  hasAnyOpenAIBPSGrant,
  hasIndependentOpenAIBPSGrant,
  isNativeOpenAIOAuthEligibleForBPSGrant,
  isOpenAIBPSAccount,
  type OpenAIBPSAccountLike
} from './openaiOAuthClient'

export { OPENAI_EXCEL_BPS_CREDENTIALS_KEY }

/** Suggested model IDs for a BPS route. Suggestions only; upstream decides entitlement. */
export const OPENAI_BPS_RECOMMENDED_MODELS: readonly string[] = ['gpt-6-astra', 'gpt-6.1-sol']

export const OPENAI_EXCEL_BPS_ENABLED_KEY = 'openai_excel_bps'
export const OPENAI_EXCEL_BPS_MODELS_KEY = 'openai_excel_bps_models'
export const OPENAI_EXCEL_BPS_AUTO_DISABLE_ON_403_KEY = 'openai_excel_bps_auto_disable_on_403'
export const OPENAI_EXCEL_BPS_AUTO_RECOVER_ON_403_KEY = 'openai_excel_bps_auto_recover_on_403'
export const OPENAI_EXCEL_BPS_403_RECOVERY_INTERVAL_MINUTES_KEY =
  'openai_excel_bps_403_recovery_interval_minutes'
export const OPENAI_EXCEL_BPS_AUTO_MOVE_ON_403_KEY = 'openai_excel_bps_auto_move_on_403'
export const OPENAI_EXCEL_BPS_403_TARGET_GROUP_ID_KEY = 'openai_excel_bps_403_target_group_id'
export const OPENAI_EXCEL_BPS_CACHE_CREATION_AS_INPUT_KEY =
  'openai_excel_bps_cache_creation_as_input'
export const OPENAI_EXCEL_BPS_SESSION_PROXY_ENABLED_KEY = 'openai_excel_bps_session_proxy_enabled'
export const OPENAI_EXCEL_BPS_SESSION_PROXY_IDS_KEY = 'openai_excel_bps_session_proxy_ids'
export const OPENAI_BPS_SKIP_HISTORICAL_ENCRYPTED_KEY = 'openai_bps_skip_historical_encrypted'
export const OPENAI_BPS_HOSTED_TOOLS_POLICY_KEY = 'openai_bps_hosted_tools_policy'

// Server-owned runtime markers in `extra` (read-only for UI display; never written by client forms).
export const OPENAI_EXCEL_BPS_403_DISABLED_AT_KEY = 'openai_excel_bps_403_disabled_at'
export const OPENAI_EXCEL_BPS_403_LAST_PROBE_AT_KEY = 'openai_excel_bps_403_last_probe_at'
export const OPENAI_EXCEL_BPS_403_DISABLED_REASON_KEY = 'openai_excel_bps_403_disabled_reason'
export const OPENAI_EXCEL_BPS_403_PROBE_MODEL_KEY = 'openai_excel_bps_403_probe_model'
export const OPENAI_EXCEL_BPS_403_MOVED_AT_KEY = 'openai_excel_bps_403_moved_at'
export const OPENAI_EXCEL_BPS_403_MOVED_GROUP_ID_KEY = 'openai_excel_bps_403_moved_group_id'
export const OPENAI_EXCEL_BPS_GRANT_INVALID_KEY = 'openai_excel_bps_grant_invalid'
export const OPENAI_EXCEL_BPS_RATE_LIMIT_UNTIL_KEY = 'openai_excel_bps_rate_limit_until'
export const OPENAI_EXCEL_BPS_GRANT_REFRESH_UNTIL_KEY = 'openai_excel_bps_grant_refresh_until'
export const OPENAI_EXCEL_BPS_MODEL_RATE_LIMITS_KEY = 'openai_excel_bps_model_rate_limits'

export const OPENAI_BPS_MIN_RECOVERY_INTERVAL_MINUTES = 1
export const OPENAI_BPS_MAX_RECOVERY_INTERVAL_MINUTES = 10080
export const OPENAI_BPS_DEFAULT_RECOVERY_INTERVAL_MINUTES = 60
export const OPENAI_BPS_MAX_MODELS = 100
export const OPENAI_BPS_MAX_SESSION_PROXIES = 128

const CLIENT_BPS_OPTION_KEYS = [
  OPENAI_EXCEL_BPS_ENABLED_KEY,
  OPENAI_EXCEL_BPS_MODELS_KEY,
  OPENAI_EXCEL_BPS_AUTO_DISABLE_ON_403_KEY,
  OPENAI_EXCEL_BPS_AUTO_RECOVER_ON_403_KEY,
  OPENAI_EXCEL_BPS_403_RECOVERY_INTERVAL_MINUTES_KEY,
  OPENAI_EXCEL_BPS_AUTO_MOVE_ON_403_KEY,
  OPENAI_EXCEL_BPS_403_TARGET_GROUP_ID_KEY,
  OPENAI_EXCEL_BPS_CACHE_CREATION_AS_INPUT_KEY,
  OPENAI_EXCEL_BPS_SESSION_PROXY_ENABLED_KEY,
  OPENAI_EXCEL_BPS_SESSION_PROXY_IDS_KEY,
  OPENAI_BPS_SKIP_HISTORICAL_ENCRYPTED_KEY,
  OPENAI_BPS_HOSTED_TOOLS_POLICY_KEY
] as const

const SERVER_BPS_MARKER_KEYS = [
  OPENAI_EXCEL_BPS_403_DISABLED_AT_KEY,
  OPENAI_EXCEL_BPS_403_LAST_PROBE_AT_KEY,
  OPENAI_EXCEL_BPS_403_DISABLED_REASON_KEY,
  OPENAI_EXCEL_BPS_403_PROBE_MODEL_KEY,
  OPENAI_EXCEL_BPS_403_MOVED_AT_KEY,
  OPENAI_EXCEL_BPS_403_MOVED_GROUP_ID_KEY,
  OPENAI_EXCEL_BPS_GRANT_INVALID_KEY,
  OPENAI_EXCEL_BPS_RATE_LIMIT_UNTIL_KEY,
  OPENAI_EXCEL_BPS_GRANT_REFRESH_UNTIL_KEY,
  OPENAI_EXCEL_BPS_MODEL_RATE_LIMITS_KEY
] as const

export type OpenAIBPSHostedToolsPolicy = 'reject' | 'omit'
export type OpenAIBPSModelsMode = 'all' | 'selected'

export interface OpenAIBPSOptions {
  skipHistoricalEncrypted: boolean
  hostedToolsPolicy: OpenAIBPSHostedToolsPolicy
}

export interface OpenAIBPSChannelOptions extends OpenAIBPSOptions {
  enabled: boolean
  modelsMode: OpenAIBPSModelsMode
  models: string[]
  autoDisableOn403: boolean
  autoRecoverOn403: boolean
  recoveryIntervalMinutes: number
  autoMoveOn403: boolean
  targetGroupId: number | null
  cacheCreationAsInput: boolean
  sessionProxyEnabled: boolean
  sessionProxyIds: number[]
}

export const DEFAULT_OPENAI_BPS_OPTIONS: Readonly<OpenAIBPSOptions> = Object.freeze({
  skipHistoricalEncrypted: false,
  hostedToolsPolicy: 'reject'
})

export const DEFAULT_OPENAI_BPS_CHANNEL_OPTIONS: Readonly<OpenAIBPSChannelOptions> = Object.freeze({
  enabled: false,
  modelsMode: 'selected',
  models: [],
  autoDisableOn403: true,
  autoRecoverOn403: true,
  recoveryIntervalMinutes: OPENAI_BPS_DEFAULT_RECOVERY_INTERVAL_MINUTES,
  autoMoveOn403: false,
  targetGroupId: null,
  cacheCreationAsInput: true,
  sessionProxyEnabled: false,
  sessionProxyIds: [],
  skipHistoricalEncrypted: false,
  hostedToolsPolicy: 'reject'
})

type ExtraLike = Record<string, unknown> | null | undefined

function hasOwn(obj: ExtraLike, key: string): boolean {
  return obj != null && Object.prototype.hasOwnProperty.call(obj, key)
}

function parseExactInteger(raw: unknown): number | null {
  if (typeof raw === 'number' && Number.isInteger(raw) && Number.isFinite(raw)) {
    return raw
  }
  if (typeof raw === 'string' && raw.trim() !== '') {
    const trimmed = raw.trim()
    if (/^-?\d+$/.test(trimmed)) {
      const parsed = Number(trimmed)
      if (Number.isSafeInteger(parsed)) return parsed
    }
  }
  return null
}

export function normalizeOpenAIBPSModels(raw: unknown): string[] {
  if (!Array.isArray(raw)) return []
  const seen = new Set<string>()
  const result: string[] = []
  for (const entry of raw) {
    if (typeof entry !== 'string') continue
    const trimmed = entry.trim()
    if (!trimmed || seen.has(trimmed)) continue
    seen.add(trimmed)
    result.push(trimmed)
  }
  return result
}

export function normalizeOpenAIBPSSessionProxyIds(raw: unknown): number[] {
  if (!Array.isArray(raw)) return []
  const seen = new Set<number>()
  const ids: number[] = []
  for (const entry of raw) {
    const id = parseExactInteger(entry)
    if (id == null || id <= 0 || seen.has(id)) continue
    seen.add(id)
    ids.push(id)
  }
  ids.sort((a, b) => a - b)
  return ids
}

export function readOpenAIBPSOptions(extra: ExtraLike): OpenAIBPSOptions {
  return {
    skipHistoricalEncrypted: extra?.[OPENAI_BPS_SKIP_HISTORICAL_ENCRYPTED_KEY] === true,
    hostedToolsPolicy: extra?.[OPENAI_BPS_HOSTED_TOOLS_POLICY_KEY] === 'omit' ? 'omit' : 'reject'
  }
}

export interface OpenAIBPSChannelContext {
  /** True when the primary account OAuth client is the Excel BPS client. */
  isBPSOnly?: boolean
  /** True when an independent `excel_bps_credentials` grant is attached or staged on a native account. */
  hasIndependentGrant?: boolean
}

export function readOpenAIBPSChannelOptions(
  extra: ExtraLike,
  context: OpenAIBPSChannelContext = {}
): OpenAIBPSChannelOptions {
  const isBPSOnly = Boolean(context.isBPSOnly)
  const base = readOpenAIBPSOptions(extra)

  const enabled =
    typeof extra?.[OPENAI_EXCEL_BPS_ENABLED_KEY] === 'boolean'
      ? (extra[OPENAI_EXCEL_BPS_ENABLED_KEY] as boolean)
      : isBPSOnly

  const hasExplicitModelsList = hasOwn(extra, OPENAI_EXCEL_BPS_MODELS_KEY) && Array.isArray(extra?.[OPENAI_EXCEL_BPS_MODELS_KEY])
  // Legacy BPS-only accounts and already-enabled accounts without `openai_excel_bps_models`
  // preserve absent = "all mapped upstream models" semantics; new/disabled native accounts
  // start in explicit selection mode (`[]` = none until configured).
  const modelsMode: OpenAIBPSModelsMode = hasExplicitModelsList
    ? 'selected'
    : isBPSOnly || extra?.[OPENAI_EXCEL_BPS_ENABLED_KEY] === true
      ? 'all'
      : 'selected'
  const models = hasExplicitModelsList ? normalizeOpenAIBPSModels(extra?.[OPENAI_EXCEL_BPS_MODELS_KEY]) : []

  const autoDisableOn403 =
    typeof extra?.[OPENAI_EXCEL_BPS_AUTO_DISABLE_ON_403_KEY] === 'boolean'
      ? (extra[OPENAI_EXCEL_BPS_AUTO_DISABLE_ON_403_KEY] as boolean)
      : DEFAULT_OPENAI_BPS_CHANNEL_OPTIONS.autoDisableOn403

  const autoRecoverOn403 =
    typeof extra?.[OPENAI_EXCEL_BPS_AUTO_RECOVER_ON_403_KEY] === 'boolean'
      ? (extra[OPENAI_EXCEL_BPS_AUTO_RECOVER_ON_403_KEY] as boolean)
      : DEFAULT_OPENAI_BPS_CHANNEL_OPTIONS.autoRecoverOn403

  const rawInterval = extra?.[OPENAI_EXCEL_BPS_403_RECOVERY_INTERVAL_MINUTES_KEY]
  const parsedInterval = typeof rawInterval === 'number' && Number.isInteger(rawInterval) ? rawInterval : null
  const recoveryIntervalMinutes =
    parsedInterval != null &&
    parsedInterval >= OPENAI_BPS_MIN_RECOVERY_INTERVAL_MINUTES &&
    parsedInterval <= OPENAI_BPS_MAX_RECOVERY_INTERVAL_MINUTES
      ? parsedInterval
      : DEFAULT_OPENAI_BPS_CHANNEL_OPTIONS.recoveryIntervalMinutes

  const autoMoveOn403 =
    typeof extra?.[OPENAI_EXCEL_BPS_AUTO_MOVE_ON_403_KEY] === 'boolean'
      ? (extra[OPENAI_EXCEL_BPS_AUTO_MOVE_ON_403_KEY] as boolean)
      : DEFAULT_OPENAI_BPS_CHANNEL_OPTIONS.autoMoveOn403

  const parsedTargetGroup = parseExactInteger(extra?.[OPENAI_EXCEL_BPS_403_TARGET_GROUP_ID_KEY])
  const targetGroupId = parsedTargetGroup != null && parsedTargetGroup > 0 ? parsedTargetGroup : null

  const cacheCreationAsInput =
    typeof extra?.[OPENAI_EXCEL_BPS_CACHE_CREATION_AS_INPUT_KEY] === 'boolean'
      ? (extra[OPENAI_EXCEL_BPS_CACHE_CREATION_AS_INPUT_KEY] as boolean)
      : DEFAULT_OPENAI_BPS_CHANNEL_OPTIONS.cacheCreationAsInput

  const sessionProxyEnabled = extra?.[OPENAI_EXCEL_BPS_SESSION_PROXY_ENABLED_KEY] === true
  const sessionProxyIds = normalizeOpenAIBPSSessionProxyIds(
    extra?.[OPENAI_EXCEL_BPS_SESSION_PROXY_IDS_KEY]
  )

  return {
    ...base,
    enabled,
    modelsMode,
    models,
    autoDisableOn403,
    autoRecoverOn403,
    recoveryIntervalMinutes,
    autoMoveOn403,
    targetGroupId,
    cacheCreationAsInput,
    sessionProxyEnabled,
    sessionProxyIds
  }
}

export function stripOpenAIBPSServerMarkers(extra: Record<string, unknown>): void {
  for (const key of SERVER_BPS_MARKER_KEYS) {
    delete extra[key]
  }
}

export function hasStoredOpenAIBPSOptionKeys(extra: ExtraLike): boolean {
  if (!extra) return false
  return CLIENT_BPS_OPTION_KEYS.some((key) => hasOwn(extra, key))
}

/**
 * Legacy two-field helper retained for protocol-only callers and unit tests.
 */
export function applyOpenAIBPSOptions(
  extra: Record<string, unknown>,
  options: OpenAIBPSOptions | null,
  previous?: ExtraLike
): void {
  if (!options) {
    delete extra[OPENAI_BPS_SKIP_HISTORICAL_ENCRYPTED_KEY]
    delete extra[OPENAI_BPS_HOSTED_TOOLS_POLICY_KEY]
    return
  }

  if (options.skipHistoricalEncrypted) {
    extra[OPENAI_BPS_SKIP_HISTORICAL_ENCRYPTED_KEY] = true
  } else if (hasOwn(previous, OPENAI_BPS_SKIP_HISTORICAL_ENCRYPTED_KEY)) {
    extra[OPENAI_BPS_SKIP_HISTORICAL_ENCRYPTED_KEY] = false
  } else {
    delete extra[OPENAI_BPS_SKIP_HISTORICAL_ENCRYPTED_KEY]
  }

  if (options.hostedToolsPolicy === 'omit') {
    extra[OPENAI_BPS_HOSTED_TOOLS_POLICY_KEY] = 'omit'
  } else if (hasOwn(previous, OPENAI_BPS_HOSTED_TOOLS_POLICY_KEY)) {
    extra[OPENAI_BPS_HOSTED_TOOLS_POLICY_KEY] = 'reject'
  } else {
    delete extra[OPENAI_BPS_HOSTED_TOOLS_POLICY_KEY]
  }
}

function hasNonDefaultChannelSelections(options: OpenAIBPSChannelOptions): boolean {
  return (
    options.enabled ||
    options.models.length > 0 ||
    options.autoDisableOn403 !== DEFAULT_OPENAI_BPS_CHANNEL_OPTIONS.autoDisableOn403 ||
    options.autoRecoverOn403 !== DEFAULT_OPENAI_BPS_CHANNEL_OPTIONS.autoRecoverOn403 ||
    options.recoveryIntervalMinutes !== DEFAULT_OPENAI_BPS_CHANNEL_OPTIONS.recoveryIntervalMinutes ||
    options.autoMoveOn403 !== DEFAULT_OPENAI_BPS_CHANNEL_OPTIONS.autoMoveOn403 ||
    options.targetGroupId != null ||
    options.cacheCreationAsInput !== DEFAULT_OPENAI_BPS_CHANNEL_OPTIONS.cacheCreationAsInput ||
    options.sessionProxyEnabled ||
    options.sessionProxyIds.length > 0 ||
    options.skipHistoricalEncrypted !== DEFAULT_OPENAI_BPS_CHANNEL_OPTIONS.skipHistoricalEncrypted ||
    options.hostedToolsPolicy !== DEFAULT_OPENAI_BPS_CHANNEL_OPTIONS.hostedToolsPolicy
  )
}

/**
 * Serializes all BPS channel, 403-handling, billing, session-proxy, and bridge
 * options into a mutable `extra` map while preserving legacy absent semantics
 * and stripping server-owned runtime markers.
 *
 * Adding an independent BPS grant (`hasIndependentGrant: true`) NEVER sets
 * `openai_excel_bps: true` unless `options.enabled` was explicitly enabled.
 */
export function applyOpenAIBPSChannelOptions(
  extra: Record<string, unknown>,
  options: OpenAIBPSChannelOptions | null,
  previous?: ExtraLike,
  context: OpenAIBPSChannelContext = {}
): void {
  stripOpenAIBPSServerMarkers(extra)

  if (!options) {
    for (const key of CLIENT_BPS_OPTION_KEYS) {
      delete extra[key]
    }
    return
  }

  const isBPSOnly = Boolean(context.isBPSOnly)
  const hasIndependentGrant = Boolean(context.hasIndependentGrant)
  const hadPreviousBPS = hasStoredOpenAIBPSOptionKeys(previous)

  // Leave pure native Codex accounts untouched when no BPS grant/route/options are active.
  if (!isBPSOnly && !hasIndependentGrant && !hadPreviousBPS && !hasNonDefaultChannelSelections(options)) {
    for (const key of CLIENT_BPS_OPTION_KEYS) {
      delete extra[key]
    }
    return
  }

  // 1. Route enable (`openai_excel_bps`)
  if (isBPSOnly) {
    if (!options.enabled) {
      extra[OPENAI_EXCEL_BPS_ENABLED_KEY] = false
    } else if (hasOwn(previous, OPENAI_EXCEL_BPS_ENABLED_KEY)) {
      extra[OPENAI_EXCEL_BPS_ENABLED_KEY] = true
    } else {
      delete extra[OPENAI_EXCEL_BPS_ENABLED_KEY]
    }
  } else if (options.enabled) {
    extra[OPENAI_EXCEL_BPS_ENABLED_KEY] = true
  } else if (hasOwn(previous, OPENAI_EXCEL_BPS_ENABLED_KEY)) {
    extra[OPENAI_EXCEL_BPS_ENABLED_KEY] = false
  } else {
    delete extra[OPENAI_EXCEL_BPS_ENABLED_KEY]
  }

  // 2. Mapped upstream BPS models (`openai_excel_bps_models`: absent => all, `[]` => none)
  if (options.modelsMode === 'all') {
    delete extra[OPENAI_EXCEL_BPS_MODELS_KEY]
  } else {
    const normalizedModels = normalizeOpenAIBPSModels(options.models)
    if (
      isBPSOnly ||
      options.enabled ||
      normalizedModels.length > 0 ||
      hasOwn(previous, OPENAI_EXCEL_BPS_MODELS_KEY)
    ) {
      extra[OPENAI_EXCEL_BPS_MODELS_KEY] = normalizedModels
    } else {
      delete extra[OPENAI_EXCEL_BPS_MODELS_KEY]
    }
  }

  // 3. 403 auto-disable / recovery / auto-move & cache billing options
  extra[OPENAI_EXCEL_BPS_AUTO_DISABLE_ON_403_KEY] = Boolean(options.autoDisableOn403)
  extra[OPENAI_EXCEL_BPS_AUTO_RECOVER_ON_403_KEY] = Boolean(options.autoRecoverOn403)
  extra[OPENAI_EXCEL_BPS_403_RECOVERY_INTERVAL_MINUTES_KEY] = Math.trunc(options.recoveryIntervalMinutes)
  extra[OPENAI_EXCEL_BPS_AUTO_MOVE_ON_403_KEY] = Boolean(options.autoMoveOn403)
  if (options.targetGroupId != null && options.targetGroupId > 0) {
    extra[OPENAI_EXCEL_BPS_403_TARGET_GROUP_ID_KEY] = Math.trunc(options.targetGroupId)
  } else {
    delete extra[OPENAI_EXCEL_BPS_403_TARGET_GROUP_ID_KEY]
  }
  extra[OPENAI_EXCEL_BPS_CACHE_CREATION_AS_INPUT_KEY] = Boolean(options.cacheCreationAsInput)

  // 4. Session proxy pool (`openai_excel_bps_session_proxy_enabled` + `openai_excel_bps_session_proxy_ids`)
  const normalizedProxyIds = normalizeOpenAIBPSSessionProxyIds(options.sessionProxyIds)
  if (options.sessionProxyEnabled) {
    extra[OPENAI_EXCEL_BPS_SESSION_PROXY_ENABLED_KEY] = true
    extra[OPENAI_EXCEL_BPS_SESSION_PROXY_IDS_KEY] = normalizedProxyIds
  } else {
    if (hasOwn(previous, OPENAI_EXCEL_BPS_SESSION_PROXY_ENABLED_KEY)) {
      extra[OPENAI_EXCEL_BPS_SESSION_PROXY_ENABLED_KEY] = false
    } else {
      delete extra[OPENAI_EXCEL_BPS_SESSION_PROXY_ENABLED_KEY]
    }
    if (normalizedProxyIds.length > 0) {
      extra[OPENAI_EXCEL_BPS_SESSION_PROXY_IDS_KEY] = normalizedProxyIds
    } else {
      delete extra[OPENAI_EXCEL_BPS_SESSION_PROXY_IDS_KEY]
    }
  }

  // 5. Bridge encrypted history & hosted tools policy (`reject` default; `omit` manual last-resort)
  applyOpenAIBPSOptions(extra, options, previous)
}

export type OpenAIBPSValidationErrorCode =
  | 'recovery_interval_out_of_range'
  | 'target_group_required'
  | 'target_group_invalid'
  | 'session_proxy_pool_empty'
  | 'session_proxy_pool_too_large'
  | 'session_proxy_inactive'
  | 'too_many_models'
  | 'invalid_model_name'

export interface OpenAIBPSValidationContext {
  eligibleGroupIds?: ReadonlySet<number>
  activeProxyIds?: ReadonlySet<number>
}

export function validateOpenAIBPSChannelOptions(
  options: OpenAIBPSChannelOptions,
  context: OpenAIBPSValidationContext = {}
): OpenAIBPSValidationErrorCode | null {
  const interval = options.recoveryIntervalMinutes
  if (
    typeof interval !== 'number' ||
    !Number.isInteger(interval) ||
    interval < OPENAI_BPS_MIN_RECOVERY_INTERVAL_MINUTES ||
    interval > OPENAI_BPS_MAX_RECOVERY_INTERVAL_MINUTES
  ) {
    return 'recovery_interval_out_of_range'
  }

  if (options.autoMoveOn403) {
    if (
      options.targetGroupId == null ||
      !Number.isInteger(options.targetGroupId) ||
      options.targetGroupId <= 0
    ) {
      return 'target_group_required'
    }
    if (context.eligibleGroupIds && !context.eligibleGroupIds.has(options.targetGroupId)) {
      return 'target_group_invalid'
    }
  }

  if (options.modelsMode === 'selected') {
    if (options.models.length > OPENAI_BPS_MAX_MODELS) {
      return 'too_many_models'
    }
    for (const model of options.models) {
      const trimmed = model.trim()
      if (!trimmed || trimmed.length > 256) {
        return 'invalid_model_name'
      }
    }
  }

  const proxyIds = normalizeOpenAIBPSSessionProxyIds(options.sessionProxyIds)
  if (proxyIds.length > OPENAI_BPS_MAX_SESSION_PROXIES) {
    return 'session_proxy_pool_too_large'
  }
  if (options.sessionProxyEnabled) {
    if (proxyIds.length === 0) {
      return 'session_proxy_pool_empty'
    }
    if (context.activeProxyIds) {
      for (const id of proxyIds) {
        if (!context.activeProxyIds.has(id)) {
          return 'session_proxy_inactive'
        }
      }
    }
  }

  return null
}

export function filterEligibleBPSTargetGroups<
  T extends { id: number; platform?: string; status?: string }
>(groups: readonly T[] | null | undefined): T[] {
  if (!Array.isArray(groups)) return []
  return groups.filter(
    (g) =>
      g &&
      g.id > 0 &&
      g.status === 'active' &&
      (g.platform === 'openai' || g.platform === 'composite')
  )
}

export function filterActiveBPSProxies<T extends { id: number; status?: string }>(
  proxies: readonly T[] | null | undefined
): T[] {
  if (!Array.isArray(proxies)) return []
  return proxies.filter((p) => p && p.id > 0 && p.status === 'active')
}

export type OpenAIBPSGrantKind = 'bps_only' | 'dual_grant' | 'native_only' | 'ineligible'

export interface OpenAIBPSAccountRuntimeState {
  grantKind: OpenAIBPSGrantKind
  hasGrant: boolean
  hasIndependentGrant: boolean
  isBPSOnly: boolean
  routeRequested: boolean
  routeActive: boolean
  is403Disabled: boolean
  disabled403At: string | null
  disabled403Reason: string | null
  lastProbeAt: string | null
  probeModel: string | null
  moved403At: string | null
  moved403GroupId: number | null
  isGrantInvalid: boolean
  isPrimaryTokenExpired: boolean
  rateLimitUntil: string | null
}

function parseTimestampMs(raw: unknown): number | null {
  if (typeof raw === 'number' && Number.isFinite(raw) && raw > 0) {
    return raw > 1e12 ? raw : raw * 1000
  }
  if (typeof raw === 'string' && raw.trim() !== '') {
    const trimmed = raw.trim()
    if (/^\d+$/.test(trimmed)) {
      const n = Number(trimmed)
      return n > 1e12 ? n : n * 1000
    }
    const ms = Date.parse(trimmed)
    return Number.isNaN(ms) ? null : ms
  }
  return null
}

/**
 * Computes normalized grant & runtime status for UI badges and banners,
 * explicitly distinguishing:
 * - BPS-only account vs dual-grant account vs native-only account
 * - Enabled BPS route vs standby grant (grant attached, route disabled)
 * - Automatic 403 channel disable vs manual route disable
 * - Local access-token expiry vs upstream grant validation failure (`openai_excel_bps_grant_invalid`)
 */
export function inspectOpenAIBPSAccountState(
  account: OpenAIBPSAccountLike | null | undefined,
  nowMs: number = Date.now()
): OpenAIBPSAccountRuntimeState {
  const isBPSOnly = isOpenAIBPSAccount(account)
  const hasIndependentGrant = hasIndependentOpenAIBPSGrant(account)
  const hasGrant = hasAnyOpenAIBPSGrant(account)
  const eligibleNative = isNativeOpenAIOAuthEligibleForBPSGrant(account)

  const grantKind: OpenAIBPSGrantKind = isBPSOnly
    ? 'bps_only'
    : hasIndependentGrant
      ? 'dual_grant'
      : eligibleNative
        ? 'native_only'
        : 'ineligible'

  const extra =
    account?.extra && typeof account.extra === 'object' && !Array.isArray(account.extra)
      ? (account.extra as Record<string, unknown>)
      : undefined
  const routeRequested =
    (isBPSOnly || eligibleNative) &&
    (typeof extra?.[OPENAI_EXCEL_BPS_ENABLED_KEY] === 'boolean'
      ? (extra[OPENAI_EXCEL_BPS_ENABLED_KEY] as boolean)
      : isBPSOnly)

  const disabled403At =
    typeof extra?.[OPENAI_EXCEL_BPS_403_DISABLED_AT_KEY] === 'string' &&
    extra[OPENAI_EXCEL_BPS_403_DISABLED_AT_KEY].trim() !== ''
      ? extra[OPENAI_EXCEL_BPS_403_DISABLED_AT_KEY].trim()
      : null
  const is403Disabled = Boolean(disabled403At) && extra?.[OPENAI_EXCEL_BPS_ENABLED_KEY] !== false

  const disabled403Reason =
    typeof extra?.[OPENAI_EXCEL_BPS_403_DISABLED_REASON_KEY] === 'string' &&
    extra[OPENAI_EXCEL_BPS_403_DISABLED_REASON_KEY].trim() !== ''
      ? extra[OPENAI_EXCEL_BPS_403_DISABLED_REASON_KEY].trim()
      : null
  const lastProbeAt =
    typeof extra?.[OPENAI_EXCEL_BPS_403_LAST_PROBE_AT_KEY] === 'string' &&
    extra[OPENAI_EXCEL_BPS_403_LAST_PROBE_AT_KEY].trim() !== ''
      ? extra[OPENAI_EXCEL_BPS_403_LAST_PROBE_AT_KEY].trim()
      : null
  const probeModel =
    typeof extra?.[OPENAI_EXCEL_BPS_403_PROBE_MODEL_KEY] === 'string' &&
    extra[OPENAI_EXCEL_BPS_403_PROBE_MODEL_KEY].trim() !== ''
      ? extra[OPENAI_EXCEL_BPS_403_PROBE_MODEL_KEY].trim()
      : null
  const moved403At =
    typeof extra?.[OPENAI_EXCEL_BPS_403_MOVED_AT_KEY] === 'string' &&
    extra[OPENAI_EXCEL_BPS_403_MOVED_AT_KEY].trim() !== ''
      ? extra[OPENAI_EXCEL_BPS_403_MOVED_AT_KEY].trim()
      : null
  const moved403GroupId = parseExactInteger(extra?.[OPENAI_EXCEL_BPS_403_MOVED_GROUP_ID_KEY])

  const isGrantInvalid =
    typeof extra?.[OPENAI_EXCEL_BPS_GRANT_INVALID_KEY] === 'string' &&
    extra[OPENAI_EXCEL_BPS_GRANT_INVALID_KEY].trim() !== ''

  const rawRateLimitUntil = extra?.[OPENAI_EXCEL_BPS_RATE_LIMIT_UNTIL_KEY]
  const rateLimitMs = parseTimestampMs(rawRateLimitUntil)
  const rateLimitUntil =
    rateLimitMs != null && rateLimitMs > nowMs && typeof rawRateLimitUntil === 'string'
      ? rawRateLimitUntil
      : null

  // Primary token expiry applies to BPS-only accounts where `credentials.expires_at` is the BPS token.
  const primaryExpiryMs = isBPSOnly ? parseTimestampMs(account?.credentials?.expires_at) : null
  const isPrimaryTokenExpired = primaryExpiryMs != null && primaryExpiryMs <= nowMs

  const routeActive =
    routeRequested && hasGrant && !is403Disabled && !isGrantInvalid && !rateLimitUntil

  return {
    grantKind,
    hasGrant,
    hasIndependentGrant,
    isBPSOnly,
    routeRequested,
    routeActive,
    is403Disabled,
    disabled403At,
    disabled403Reason,
    lastProbeAt,
    probeModel,
    moved403At,
    moved403GroupId: moved403GroupId != null && moved403GroupId > 0 ? moved403GroupId : null,
    isGrantInvalid,
    isPrimaryTokenExpired,
    rateLimitUntil
  }
}

export interface ModelMappingRow {
  from: string
  to: string
}

/** True when the account names at least one concrete model of its own. */
export function hasExplicitOpenAIBPSModels(
  allowedModels: readonly string[],
  mappings: readonly ModelMappingRow[]
): boolean {
  const concrete = (value: string) => {
    const trimmed = value.trim()
    return trimmed !== '' && !trimmed.includes('*')
  }
  return allowedModels.some(concrete) || mappings.some((row) => concrete(row.from) && row.to.trim() !== '')
}

/** Adds the recommended models to a whitelist without duplicating existing entries. */
export function mergeRecommendedBPSWhitelist(
  current: readonly string[]
): { models: string[]; added: number } {
  const models = [...current]
  let added = 0
  for (const model of OPENAI_BPS_RECOMMENDED_MODELS) {
    if (!models.includes(model)) {
      models.push(model)
      added++
    }
  }
  return { models, added }
}

/** Adds identity mappings for the recommended models without touching existing rows. */
export function mergeRecommendedBPSMappings(
  current: readonly ModelMappingRow[]
): { rows: ModelMappingRow[]; added: number } {
  const rows = current.map((row) => ({ ...row }))
  const existing = new Set(rows.map((row) => row.from.trim()))
  let added = 0
  for (const model of OPENAI_BPS_RECOMMENDED_MODELS) {
    if (!existing.has(model)) {
      rows.push({ from: model, to: model })
      existing.add(model)
      added++
    }
  }
  return { rows, added }
}
