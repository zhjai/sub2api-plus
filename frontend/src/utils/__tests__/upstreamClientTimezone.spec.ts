import { describe, expect, it } from 'vitest'
import {
  UPSTREAM_CLIENT_TIMEZONE_KEY,
  UPSTREAM_CLIENT_TIMEZONE_MODE_KEY,
  applyUpstreamClientTimezone,
  canonicalIANATimezone,
  isValidIANATimezone,
  readUpstreamClientTimezone,
  upstreamClientTimezoneSuggestions,
  validateUpstreamClientTimezone
} from '../upstreamClientTimezone'

describe('upstreamClientTimezone utilities', () => {
  it('validates canonical IANA timezone names and rejects raw offsets or invalid names', () => {
    expect(isValidIANATimezone('Asia/Singapore')).toBe(true)
    expect(isValidIANATimezone('America/Los_Angeles')).toBe(true)
    expect(isValidIANATimezone('UTC')).toBe(true)
    expect(canonicalIANATimezone('  Asia/Tokyo ')).toBe('Asia/Tokyo')

    expect(isValidIANATimezone('')).toBe(false)
    expect(isValidIANATimezone('+08:00')).toBe(false)
    expect(isValidIANATimezone('UTC+8')).toBe(false)
    expect(isValidIANATimezone('Mars/Olympus_Mons')).toBe(false)
  })

  it('validates state only when mode is manual', () => {
    expect(validateUpstreamClientTimezone({ mode: 'off', timezone: '' })).toBeNull()
    expect(validateUpstreamClientTimezone({ mode: 'auto', timezone: 'not-a-zone' })).toBeNull()
    expect(validateUpstreamClientTimezone({ mode: 'manual', timezone: '   ' })).toBe('required')
    expect(validateUpstreamClientTimezone({ mode: 'manual', timezone: '+08:00' })).toBe('invalid')
    expect(validateUpstreamClientTimezone({ mode: 'manual', timezone: 'Europe/London' })).toBeNull()
  })

  it('reads and writes upstream client timezone extra keys cleanly across mode transitions', () => {
    expect(readUpstreamClientTimezone(undefined)).toEqual({ mode: 'off', timezone: '' })
    expect(
      readUpstreamClientTimezone({
        [UPSTREAM_CLIENT_TIMEZONE_MODE_KEY]: 'manual',
        [UPSTREAM_CLIENT_TIMEZONE_KEY]: 'Asia/Singapore'
      })
    ).toEqual({ mode: 'manual', timezone: 'Asia/Singapore' })

    const extra: Record<string, unknown> = {}
    applyUpstreamClientTimezone(extra, { mode: 'off', timezone: 'Asia/Singapore' }, {})
    expect(extra).toEqual({})

    applyUpstreamClientTimezone(extra, { mode: 'manual', timezone: ' Asia/Singapore ' }, {})
    expect(extra).toEqual({
      [UPSTREAM_CLIENT_TIMEZONE_MODE_KEY]: 'manual',
      [UPSTREAM_CLIENT_TIMEZONE_KEY]: 'Asia/Singapore'
    })

    // Switching to auto removes the manual timezone key from the payload
    const autoExtra: Record<string, unknown> = { ...extra }
    applyUpstreamClientTimezone(autoExtra, { mode: 'auto', timezone: 'Asia/Singapore' }, extra)
    expect(autoExtra).toEqual({
      [UPSTREAM_CLIENT_TIMEZONE_MODE_KEY]: 'auto'
    })

    // Switching back to off when previously stored writes explicit 'off'
    const offExtra: Record<string, unknown> = { ...autoExtra }
    applyUpstreamClientTimezone(offExtra, { mode: 'off', timezone: 'Asia/Singapore' }, autoExtra)
    expect(offExtra).toEqual({
      [UPSTREAM_CLIENT_TIMEZONE_MODE_KEY]: 'off'
    })
  })

  it('provides non-empty IANA suggestions including UTC', () => {
    const suggestions = upstreamClientTimezoneSuggestions()
    expect(suggestions.length).toBeGreaterThan(0)
    expect(suggestions).toContain('UTC')
  })
})
