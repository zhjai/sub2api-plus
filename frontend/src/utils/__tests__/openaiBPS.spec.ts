import { describe, expect, it } from 'vitest'
import {
  DEFAULT_OPENAI_BPS_OPTIONS,
  OPENAI_BPS_HOSTED_TOOLS_POLICY_KEY,
  OPENAI_BPS_RECOMMENDED_MODELS,
  OPENAI_BPS_SKIP_HISTORICAL_ENCRYPTED_KEY,
  applyOpenAIBPSOptions,
  hasExplicitOpenAIBPSModels,
  mergeRecommendedBPSMappings,
  mergeRecommendedBPSWhitelist,
  readOpenAIBPSOptions
} from '../openaiBPS'

describe('openaiBPS utilities', () => {
  it('reads defaults when extra is missing or invalid', () => {
    expect(readOpenAIBPSOptions(undefined)).toEqual(DEFAULT_OPENAI_BPS_OPTIONS)
    expect(readOpenAIBPSOptions(null)).toEqual(DEFAULT_OPENAI_BPS_OPTIONS)
    expect(
      readOpenAIBPSOptions({
        [OPENAI_BPS_SKIP_HISTORICAL_ENCRYPTED_KEY]: 'yes',
        [OPENAI_BPS_HOSTED_TOOLS_POLICY_KEY]: 'unknown'
      })
    ).toEqual({
      skipHistoricalEncrypted: false,
      hostedToolsPolicy: 'omit'
    })
  })

  it('reads stored BPS options accurately', () => {
    expect(
      readOpenAIBPSOptions({
        [OPENAI_BPS_SKIP_HISTORICAL_ENCRYPTED_KEY]: true,
        [OPENAI_BPS_HOSTED_TOOLS_POLICY_KEY]: 'reject'
      })
    ).toEqual({
      skipHistoricalEncrypted: true,
      hostedToolsPolicy: 'reject'
    })
  })

  it('omits defaults when unset, writes explicit defaults when previously stored, and removes keys on non-BPS', () => {
    const fresh: Record<string, unknown> = { keep: 1 }
    applyOpenAIBPSOptions(fresh, { skipHistoricalEncrypted: false, hostedToolsPolicy: 'omit' }, {})
    expect(fresh).toEqual({ keep: 1 })

    const updated: Record<string, unknown> = {}
    applyOpenAIBPSOptions(updated, { skipHistoricalEncrypted: true, hostedToolsPolicy: 'reject' }, {})
    expect(updated).toEqual({
      [OPENAI_BPS_SKIP_HISTORICAL_ENCRYPTED_KEY]: true,
      [OPENAI_BPS_HOSTED_TOOLS_POLICY_KEY]: 'reject'
    })

    const cleared: Record<string, unknown> = { ...updated }
    applyOpenAIBPSOptions(cleared, { skipHistoricalEncrypted: false, hostedToolsPolicy: 'omit' }, updated)
    expect(cleared).toEqual({
      [OPENAI_BPS_SKIP_HISTORICAL_ENCRYPTED_KEY]: false,
      [OPENAI_BPS_HOSTED_TOOLS_POLICY_KEY]: 'omit'
    })

    applyOpenAIBPSOptions(cleared, null, updated)
    expect(cleared).toEqual({})
  })

  it('detects explicit BPS models and rejects empty or wildcard-only configurations', () => {
    expect(hasExplicitOpenAIBPSModels([], [])).toBe(false)
    expect(hasExplicitOpenAIBPSModels(['  ', 'gpt-*'], [{ from: '*', to: 'gpt-6-astra' }])).toBe(false)
    expect(hasExplicitOpenAIBPSModels([], [{ from: 'gpt-6-astra', to: '   ' }])).toBe(false)
    expect(hasExplicitOpenAIBPSModels(['gpt-6-astra'], [])).toBe(true)
    expect(hasExplicitOpenAIBPSModels([], [{ from: 'gpt-6-astra', to: 'gpt-6-astra' }])).toBe(true)
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
