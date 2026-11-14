import { describe, expect, it } from 'vitest'
import {
  DEFAULT_OPENAI_BPS_CHANNEL_OPTIONS,
  DEFAULT_OPENAI_BPS_OPTIONS,
  OPENAI_BPS_HOSTED_TOOLS_POLICY_KEY,
  OPENAI_BPS_RECOMMENDED_MODELS,
  OPENAI_BPS_SKIP_HISTORICAL_ENCRYPTED_KEY,
  OPENAI_EXCEL_BPS_403_DISABLED_AT_KEY,
  OPENAI_EXCEL_BPS_403_RECOVERY_INTERVAL_MINUTES_KEY,
  OPENAI_EXCEL_BPS_403_TARGET_GROUP_ID_KEY,
  OPENAI_EXCEL_BPS_AUTO_DISABLE_ON_403_KEY,
  OPENAI_EXCEL_BPS_AUTO_MOVE_ON_403_KEY,
  OPENAI_EXCEL_BPS_AUTO_RECOVER_ON_403_KEY,
  OPENAI_EXCEL_BPS_CACHE_CREATION_AS_INPUT_KEY,
  OPENAI_EXCEL_BPS_CREDENTIALS_KEY,
  OPENAI_EXCEL_BPS_ENABLED_KEY,
  OPENAI_EXCEL_BPS_GRANT_INVALID_KEY,
  OPENAI_EXCEL_BPS_MODELS_KEY,
  OPENAI_EXCEL_BPS_SESSION_PROXY_ENABLED_KEY,
  OPENAI_EXCEL_BPS_SESSION_PROXY_IDS_KEY,
  applyOpenAIBPSChannelOptions,
  applyOpenAIBPSOptions,
  hasExplicitOpenAIBPSModels,
  inspectOpenAIBPSAccountState,
  mergeRecommendedBPSMappings,
  mergeRecommendedBPSWhitelist,
  readOpenAIBPSChannelOptions,
  readOpenAIBPSOptions,
  validateOpenAIBPSChannelOptions
} from '../openaiBPS'
import { OPENAI_EXCEL_OAUTH_CLIENT_ID } from '../openaiOAuthClient'

describe('openaiBPS utilities', () => {
  it('reads defaults (reject hosted tools, skipHistoricalEncrypted=false) when extra is missing or invalid', () => {
    expect(DEFAULT_OPENAI_BPS_OPTIONS).toEqual({
      skipHistoricalEncrypted: false,
      hostedToolsPolicy: 'reject'
    })
    expect(readOpenAIBPSOptions(undefined)).toEqual(DEFAULT_OPENAI_BPS_OPTIONS)
    expect(readOpenAIBPSOptions(null)).toEqual(DEFAULT_OPENAI_BPS_OPTIONS)
    expect(
      readOpenAIBPSOptions({
        [OPENAI_BPS_SKIP_HISTORICAL_ENCRYPTED_KEY]: 'yes',
        [OPENAI_BPS_HOSTED_TOOLS_POLICY_KEY]: 'unknown'
      })
    ).toEqual({
      skipHistoricalEncrypted: false,
      hostedToolsPolicy: 'reject'
    })
  })

  it('reads stored BPS options accurately', () => {
    expect(
      readOpenAIBPSOptions({
        [OPENAI_BPS_SKIP_HISTORICAL_ENCRYPTED_KEY]: true,
        [OPENAI_BPS_HOSTED_TOOLS_POLICY_KEY]: 'omit'
      })
    ).toEqual({
      skipHistoricalEncrypted: true,
      hostedToolsPolicy: 'omit'
    })
  })

  it('omits defaults when unset, writes explicit defaults when previously stored, and removes keys on non-BPS', () => {
    const fresh: Record<string, unknown> = { keep: 1 }
    applyOpenAIBPSOptions(fresh, { skipHistoricalEncrypted: false, hostedToolsPolicy: 'reject' }, {})
    expect(fresh).toEqual({ keep: 1 })

    const updated: Record<string, unknown> = {}
    applyOpenAIBPSOptions(updated, { skipHistoricalEncrypted: true, hostedToolsPolicy: 'omit' }, {})
    expect(updated).toEqual({
      [OPENAI_BPS_SKIP_HISTORICAL_ENCRYPTED_KEY]: true,
      [OPENAI_BPS_HOSTED_TOOLS_POLICY_KEY]: 'omit'
    })

    const cleared: Record<string, unknown> = { ...updated }
    applyOpenAIBPSOptions(cleared, { skipHistoricalEncrypted: false, hostedToolsPolicy: 'reject' }, updated)
    expect(cleared).toEqual({
      [OPENAI_BPS_SKIP_HISTORICAL_ENCRYPTED_KEY]: false,
      [OPENAI_BPS_HOSTED_TOOLS_POLICY_KEY]: 'reject'
    })

    applyOpenAIBPSOptions(cleared, null, updated)
    expect(cleared).toEqual({})
  })

  it('preserves legacy absent semantics and serializes all approved BPS channel options cleanly', () => {
    // Legacy BPS-only absent openai_excel_bps implies enabled=true and modelsMode='all'
    const legacyBps = readOpenAIBPSChannelOptions({}, { isBPSOnly: true })
    expect(legacyBps).toMatchObject({
      enabled: true,
      modelsMode: 'all',
      models: [],
      autoDisableOn403: true,
      autoRecoverOn403: true,
      recoveryIntervalMinutes: 60,
      autoMoveOn403: false,
      targetGroupId: null,
      cacheCreationAsInput: true,
      sessionProxyEnabled: false,
      sessionProxyIds: [],
      skipHistoricalEncrypted: false,
      hostedToolsPolicy: 'reject'
    })

    // Native primary account absent openai_excel_bps implies enabled=false; untouched save leaves extra clean
    const nativeOpts = readOpenAIBPSChannelOptions({}, { isBPSOnly: false })
    expect(nativeOpts.enabled).toBe(false)
    const untouchedNativeExtra: Record<string, unknown> = { keep_native: true }
    applyOpenAIBPSChannelOptions(untouchedNativeExtra, nativeOpts, {}, { isBPSOnly: false, hasIndependentGrant: false })
    expect(untouchedNativeExtra).toEqual({ keep_native: true })

    // Explicit empty array [] means selected mode with 0 models (none routed to BPS)
    const explicitNone = readOpenAIBPSChannelOptions({ [OPENAI_EXCEL_BPS_MODELS_KEY]: [] }, { isBPSOnly: true })
    expect(explicitNone.modelsMode).toBe('selected')
    expect(explicitNone.models).toEqual([])

    // Dual-grant configuration writes explicit keys and strips server-owned runtime markers from client payload
    const dualExtra: Record<string, unknown> = {
      [OPENAI_EXCEL_BPS_403_DISABLED_AT_KEY]: '2026-11-13T00:00:00Z',
      [OPENAI_EXCEL_BPS_GRANT_INVALID_KEY]: 'fp-stale'
    }
    applyOpenAIBPSChannelOptions(
      dualExtra,
      {
        ...DEFAULT_OPENAI_BPS_CHANNEL_OPTIONS,
        enabled: true,
        modelsMode: 'selected',
        models: ['gpt-6-astra', 'gpt-6.1-sol'],
        autoDisableOn403: true,
        autoRecoverOn403: true,
        recoveryIntervalMinutes: 120,
        autoMoveOn403: true,
        targetGroupId: 42,
        cacheCreationAsInput: false,
        sessionProxyEnabled: true,
        sessionProxyIds: [7, 3, 7],
        skipHistoricalEncrypted: false,
        hostedToolsPolicy: 'reject'
      },
      dualExtra,
      { isBPSOnly: false, hasIndependentGrant: true }
    )
    expect(dualExtra).toEqual({
      [OPENAI_EXCEL_BPS_ENABLED_KEY]: true,
      [OPENAI_EXCEL_BPS_MODELS_KEY]: ['gpt-6-astra', 'gpt-6.1-sol'],
      [OPENAI_EXCEL_BPS_AUTO_DISABLE_ON_403_KEY]: true,
      [OPENAI_EXCEL_BPS_AUTO_RECOVER_ON_403_KEY]: true,
      [OPENAI_EXCEL_BPS_403_RECOVERY_INTERVAL_MINUTES_KEY]: 120,
      [OPENAI_EXCEL_BPS_AUTO_MOVE_ON_403_KEY]: true,
      [OPENAI_EXCEL_BPS_403_TARGET_GROUP_ID_KEY]: 42,
      [OPENAI_EXCEL_BPS_CACHE_CREATION_AS_INPUT_KEY]: false,
      [OPENAI_EXCEL_BPS_SESSION_PROXY_ENABLED_KEY]: true,
      [OPENAI_EXCEL_BPS_SESSION_PROXY_IDS_KEY]: [3, 7]
    })
    expect(dualExtra).not.toHaveProperty(OPENAI_BPS_SKIP_HISTORICAL_ENCRYPTED_KEY)
    expect(dualExtra).not.toHaveProperty(OPENAI_BPS_HOSTED_TOOLS_POLICY_KEY)
  })

  it('validates session proxy pool, 403 target group, and recovery interval', () => {
    expect(
      validateOpenAIBPSChannelOptions({
        ...DEFAULT_OPENAI_BPS_CHANNEL_OPTIONS,
        sessionProxyEnabled: true,
        sessionProxyIds: []
      })
    ).toBe('session_proxy_pool_empty')

    expect(
      validateOpenAIBPSChannelOptions(
        {
          ...DEFAULT_OPENAI_BPS_CHANNEL_OPTIONS,
          sessionProxyEnabled: true,
          sessionProxyIds: [99]
        },
        { activeProxyIds: new Set([1, 2]) }
      )
    ).toBe('session_proxy_inactive')

    expect(
      validateOpenAIBPSChannelOptions({
        ...DEFAULT_OPENAI_BPS_CHANNEL_OPTIONS,
        autoMoveOn403: true,
        targetGroupId: null
      })
    ).toBe('target_group_required')

    expect(
      validateOpenAIBPSChannelOptions(
        {
          ...DEFAULT_OPENAI_BPS_CHANNEL_OPTIONS,
          autoMoveOn403: true,
          targetGroupId: 8
        },
        { eligibleGroupIds: new Set([3, 4]) }
      )
    ).toBe('target_group_invalid')

    expect(
      validateOpenAIBPSChannelOptions({
        ...DEFAULT_OPENAI_BPS_CHANNEL_OPTIONS,
        recoveryIntervalMinutes: 0
      })
    ).toBe('recovery_interval_out_of_range')
  })

  it('inspects BPS-only, dual-grant standby/active, 403 disable, and grant validation vs local expiry', () => {
    const now = Date.parse('2026-11-14T00:00:00Z')

    const standbyDual = inspectOpenAIBPSAccountState(
      {
        platform: 'openai',
        type: 'oauth',
        credentials: {
          client_id: 'codex-client',
          [OPENAI_EXCEL_BPS_CREDENTIALS_KEY]: {
            client_id: OPENAI_EXCEL_OAUTH_CLIENT_ID,
            access_token: 'nested-at'
          }
        },
        extra: {}
      },
      now
    )
    expect(standbyDual.grantKind).toBe('dual_grant')
    expect(standbyDual.hasIndependentGrant).toBe(true)
    expect(standbyDual.routeRequested).toBe(false)
    expect(standbyDual.routeActive).toBe(false)

    const disabled403 = inspectOpenAIBPSAccountState(
      {
        platform: 'openai',
        type: 'oauth',
        credentials: { client_id: OPENAI_EXCEL_OAUTH_CLIENT_ID },
        extra: {
          [OPENAI_EXCEL_BPS_403_DISABLED_AT_KEY]: '2026-11-13T23:00:00Z'
        }
      },
      now
    )
    expect(disabled403.grantKind).toBe('bps_only')
    expect(disabled403.routeRequested).toBe(true)
    expect(disabled403.is403Disabled).toBe(true)
    expect(disabled403.routeActive).toBe(false)

    const expiredVsInvalid = inspectOpenAIBPSAccountState(
      {
        platform: 'openai',
        type: 'oauth',
        credentials: {
          client_id: OPENAI_EXCEL_OAUTH_CLIENT_ID,
          expires_at: '2026-11-13T20:00:00Z'
        },
        extra: {}
      },
      now
    )
    expect(expiredVsInvalid.isPrimaryTokenExpired).toBe(true)
    expect(expiredVsInvalid.isGrantInvalid).toBe(false)
  })

  it('detects explicit BPS models and rejects empty or wildcard-only configurations', () => {
    expect(hasExplicitOpenAIBPSModels([], [])).toBe(false)
    expect(hasExplicitOpenAIBPSModels(['  ', 'gpt-*'], [{ from: '*', to: 'gpt-6-astra' }])).toBe(false)
    expect(hasExplicitOpenAIBPSModels([], [{ from: 'gpt-6-astra', to: '   ' }])).toBe(false)
    expect(hasExplicitOpenAIBPSModels(['gpt-6-astra'], [])).toBe(true)
    expect(hasExplicitOpenAIBPSModels([], [{ from: 'gpt-6-astra', to: 'gpt-6.1-sol' }])).toBe(true)
  })

  it('merges recommended BPS models without duplicating existing whitelist or mapping entries', () => {
    expect(OPENAI_BPS_RECOMMENDED_MODELS).toEqual(['gpt-6-astra', 'gpt-6.1-sol'])

    const wl1 = mergeRecommendedBPSWhitelist(['custom-model', 'gpt-6-astra'])
    expect(wl1).toEqual({
      models: ['custom-model', 'gpt-6-astra', 'gpt-6.1-sol'],
      added: 1
    })
    expect(mergeRecommendedBPSWhitelist(wl1.models)).toEqual({
      models: ['custom-model', 'gpt-6-astra', 'gpt-6.1-sol'],
      added: 0
    })

    const mp1 = mergeRecommendedBPSMappings([{ from: 'gpt-6.1-sol', to: 'gpt-6.1-sol-custom' }])
    expect(mp1).toEqual({
      rows: [
        { from: 'gpt-6.1-sol', to: 'gpt-6.1-sol-custom' },
        { from: 'gpt-6-astra', to: 'gpt-6-astra' }
      ],
      added: 1
    })
  })
})
