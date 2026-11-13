/**
 * Account-level upstream client timezone (the timezone presented in the client
 * environment sent upstream). This is unrelated to quota reset timezones.
 *
 * - off:    keep existing behavior, nothing is rewritten.
 * - manual: use the IANA zone stored in `upstream_client_timezone`.
 * - auto:   the server resolves the zone from the account's actual egress
 *           (its proxy, or the server's public egress when no proxy is set)
 *           and caches it; it is never the browser or server process timezone.
 */

export type UpstreamClientTimezoneMode = 'off' | 'manual' | 'auto'

export const UPSTREAM_CLIENT_TIMEZONE_MODE_KEY = 'upstream_client_timezone_mode'
export const UPSTREAM_CLIENT_TIMEZONE_KEY = 'upstream_client_timezone'

export interface UpstreamClientTimezoneState {
  mode: UpstreamClientTimezoneMode
  timezone: string
}

type ExtraLike = Record<string, unknown> | null | undefined

export function readUpstreamClientTimezone(extra: ExtraLike): UpstreamClientTimezoneState {
  const rawMode = extra?.[UPSTREAM_CLIENT_TIMEZONE_MODE_KEY]
  const mode: UpstreamClientTimezoneMode = rawMode === 'manual' || rawMode === 'auto' ? rawMode : 'off'
  const rawZone = extra?.[UPSTREAM_CLIENT_TIMEZONE_KEY]
  return { mode, timezone: typeof rawZone === 'string' ? rawZone : '' }
}

// IANA names are region/location paths ("Asia/Singapore", "America/Argentina/Salta")
// or the bare UTC aliases. Raw offsets such as "+08:00" are not IANA names.
const IANA_NAME_PATTERN = /^(?:[A-Za-z][A-Za-z0-9_+-]*)(?:\/[A-Za-z0-9_+-]+)*$/

/** Returns the canonical IANA name, or '' when the value is not a usable zone. */
export function canonicalIANATimezone(value: string): string {
  const trimmed = (value || '').trim()
  if (!trimmed || !IANA_NAME_PATTERN.test(trimmed)) return ''
  if (!trimmed.includes('/') && !/^(?:UTC|GMT|Etc)$/i.test(trimmed)) return ''
  try {
    return new Intl.DateTimeFormat('en-US', { timeZone: trimmed }).resolvedOptions().timeZone || ''
  } catch {
    return ''
  }
}

export function isValidIANATimezone(value: string): boolean {
  return canonicalIANATimezone(value) !== ''
}

const FALLBACK_TIMEZONE_SUGGESTIONS = [
  'UTC',
  'America/Los_Angeles',
  'America/New_York',
  'Europe/London',
  'Europe/Berlin',
  'Asia/Tokyo',
  'Asia/Singapore',
  'Asia/Hong_Kong',
  'Asia/Shanghai',
  'Australia/Sydney'
]

/** IANA names offered as input suggestions; the runtime list when available. */
export function upstreamClientTimezoneSuggestions(): string[] {
  const intl = Intl as typeof Intl & { supportedValuesOf?: (key: string) => string[] }
  try {
    const values = intl.supportedValuesOf?.('timeZone')
    if (Array.isArray(values) && values.length > 0) {
      return values.includes('UTC') ? values : ['UTC', ...values]
    }
  } catch {
    // Fall through to the static list.
  }
  return [...FALLBACK_TIMEZONE_SUGGESTIONS]
}

export type UpstreamClientTimezoneError = 'required' | 'invalid'

export function validateUpstreamClientTimezone(state: UpstreamClientTimezoneState): UpstreamClientTimezoneError | null {
  if (state.mode !== 'manual') return null
  if (!state.timezone.trim()) return 'required'
  return isValidIANATimezone(state.timezone) ? null : 'invalid'
}

/**
 * Writes the timezone setting into a mutable extra map. The manual zone is only
 * sent in manual mode; the UI keeps it in memory when switching modes.
 * `off` is stored explicitly only when a mode was previously stored.
 */
export function applyUpstreamClientTimezone(
  extra: Record<string, unknown>,
  state: UpstreamClientTimezoneState,
  previous?: ExtraLike
): void {
  const hadMode = previous != null && Object.prototype.hasOwnProperty.call(previous, UPSTREAM_CLIENT_TIMEZONE_MODE_KEY)
  if (state.mode === 'manual') {
    extra[UPSTREAM_CLIENT_TIMEZONE_MODE_KEY] = 'manual'
    extra[UPSTREAM_CLIENT_TIMEZONE_KEY] = canonicalIANATimezone(state.timezone) || state.timezone.trim()
    return
  }
  delete extra[UPSTREAM_CLIENT_TIMEZONE_KEY]
  if (state.mode === 'auto') {
    extra[UPSTREAM_CLIENT_TIMEZONE_MODE_KEY] = 'auto'
  } else if (hadMode) {
    extra[UPSTREAM_CLIENT_TIMEZONE_MODE_KEY] = 'off'
  } else {
    delete extra[UPSTREAM_CLIENT_TIMEZONE_MODE_KEY]
  }
}
