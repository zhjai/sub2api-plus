import { describe, expect, it } from 'vitest'
import { ref } from 'vue'
import {
  effectiveCodexFingerprintMode,
  findCodexPrivacyConflicts,
  isCodexPrivacyEligible,
  isCodexPrivacyError,
  useCodexPrivacyMode
} from '../useCodexPrivacyMode'

describe('useCodexPrivacyMode helpers', () => {
  it('matches the server account eligibility', () => {
    expect(isCodexPrivacyEligible({ platform: 'openai', type: 'oauth' })).toBe(true)
    expect(isCodexPrivacyEligible({ platform: 'openai', type: 'setup-token' })).toBe(true)
    expect(isCodexPrivacyEligible({ platform: 'openai', type: 'apikey' })).toBe(true)
    expect(isCodexPrivacyEligible({ platform: 'openai', type: 'upstream' })).toBe(false)
    expect(isCodexPrivacyEligible({ platform: 'anthropic', type: 'oauth' })).toBe(false)
    expect(isCodexPrivacyEligible({ platform: 'openai', type: 'oauth', parent_account_id: 3 })).toBe(false)
    expect(isCodexPrivacyEligible({ platform: 'openai', type: 'oauth', credentials: { auth_mode: ' AgentIdentity ' } })).toBe(false)
    expect(isCodexPrivacyEligible(null)).toBe(false)
  })

  it('normalizes convergence like codexFingerprintModeFromExtra', () => {
    expect(effectiveCodexFingerprintMode(' session ')).toBe('session')
    expect(effectiveCodexFingerprintMode('bogus')).toBe('off')
    expect(effectiveCodexFingerprintMode(undefined)).toBe('off')
    expect(findCodexPrivacyConflicts({ fingerprintMode: 'full', identityMode: 'preserve_client' })).toEqual(['fingerprint', 'preserve_client'])
    expect(findCodexPrivacyConflicts({ fingerprintMode: 'off', identityMode: 'isolated' })).toEqual([])
  })

  it('recognizes privacy rejections by reason', () => {
    expect(isCodexPrivacyError({ reason: 'CODEX_PRIVACY_INVALID_MODE', message: 'x' })).toBe(true)
    expect(isCodexPrivacyError({ reason: 'CODEX_IDENTITY_NAMESPACE_CONFLICT', message: 'x' })).toBe(false)
    expect(isCodexPrivacyError({ status: 400, code: 400, message: 'codex_privacy_mode must be off or randomized' })).toBe(true)
  })
})

describe('useCodexPrivacyMode state', () => {
  it('writes off only when a value was already stored', () => {
    const state = useCodexPrivacyMode(() => ({ fingerprintMode: 'off', identityMode: 'isolated' }))
    const fresh: Record<string, unknown> = {}
    state.writeTo(fresh)
    expect(fresh).not.toHaveProperty('codex_privacy_mode')

    state.load({ codex_privacy_mode: 'randomized' })
    expect(state.enabled.value).toBe(true)
    state.toggle()
    const saved: Record<string, unknown> = { codex_privacy_mode: 'randomized' }
    state.writeTo(saved)
    expect(saved.codex_privacy_mode).toBe('off')
  })

  it('only reports conflicts while enabled and blocks save until resolved', () => {
    const fingerprint = ref('device')
    const state = useCodexPrivacyMode(() => ({ fingerprintMode: fingerprint.value, identityMode: 'isolated' }))
    expect(state.conflicts.value).toEqual([])
    state.setEnabled(true)
    expect(state.validateBeforeSave()).toBe(false)
    expect(state.saveBlocked.value).toBe(true)
    fingerprint.value = 'off'
    expect(state.saveBlocked.value).toBe(false)
    expect(state.validateBeforeSave()).toBe(true)
  })

  it('keeps the server reason and code', () => {
    const state = useCodexPrivacyMode(() => ({ fingerprintMode: 'off', identityMode: 'isolated' }))
    expect(state.captureServerError({ reason: 'CODEX_IDENTITY_X', message: 'other' })).toBeNull()
    expect(state.captureServerError({ reason: 'CODEX_PRIVACY_SEED_REQUIRED', message: 'Save the account again' })).toBe('Save the account again')
    expect(state.serverError.value).toEqual({ code: 'CODEX_PRIVACY_SEED_REQUIRED', message: 'Save the account again' })
  })
})
