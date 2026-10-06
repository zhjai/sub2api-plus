import { describe, expect, it } from 'vitest'
import type { OpenAIEvalConfig, OpenAIEvalPolicyWeights } from '@/api/admin/accounts'
import {
  absolutePriorities,
  customBalancePayload,
  foldLegacyStability,
  isValidCustomBalance,
  normalizeCustomBalance,
  toSavePayload
} from '../modelIntegrity'

type Priorities = OpenAIEvalPolicyWeights['absolute_priorities']
const weights = (extra: Partial<OpenAIEvalPolicyWeights> = {}): OpenAIEvalPolicyWeights => ({ cost: 0.2, error_rate: 0.43, ttft: 0.27, load: 0.1, quality: 0, ...extra })
const zero = (extra: Partial<OpenAIEvalPolicyWeights> = {}): OpenAIEvalPolicyWeights => ({ cost: 0, error_rate: 0, ttft: 0, load: 0, quality: 0, ...extra })

describe('absolute priorities', () => {
  it('keeps the order, drops repeats and unknown names, and returns a new array', () => {
    const input = ['cost', 'error_rate', 'cost', 'stability', 'quality']
    const result = absolutePriorities({ absolute_priorities: input })
    expect(result).toEqual(['cost', 'error_rate', 'quality'])
    expect(result).not.toBe(input)
    expect(absolutePriorities(undefined)).toEqual([])
    expect(absolutePriorities(null)).toEqual([])
  })

  it('survives folding a legacy stability weight, in a copy', () => {
    const legacy = { ...weights({ error_rate: 0.1, ttft: 0.1 }), stability: 0.2, absolute_priorities: ['cost', 'error_rate'] as Priorities }
    const folded = foldLegacyStability(legacy)
    expect(folded.absolute_priorities).toEqual(['cost', 'error_rate'])
    expect(folded.absolute_priorities).not.toBe(legacy.absolute_priorities)
    expect(folded.error_rate).toBeCloseTo(0.22)
    expect(foldLegacyStability(folded)).toEqual(folded)
  })

  it('is left absent when there is none, but an empty list is still sent so the server clears it', () => {
    expect(foldLegacyStability(weights())).not.toHaveProperty('absolute_priorities')
    expect(normalizeCustomBalance(weights({ absolute_priorities: [] }))).not.toHaveProperty('absolute_priorities')
    expect(customBalancePayload(weights({ absolute_priorities: [] })).absolute_priorities).toEqual([])
  })

  it('keeps all-zero weights as they are when a priority order is set', () => {
    const allZero = zero({ absolute_priorities: ['quality', 'ttft'] })
    expect(normalizeCustomBalance(allZero)).toEqual({ ...zero(), stability: 0, absolute_priorities: ['quality', 'ttft'] })
    expect(isValidCustomBalance(allZero)).toBe(true)
    expect(normalizeCustomBalance(weights({ absolute_priorities: ['cost', 'cost', 'load'] })).absolute_priorities).toEqual(['cost', 'load'])
  })

  it('still replaces all-zero weights without a priority, and never accepts them', () => {
    expect(normalizeCustomBalance(zero())).toEqual({ ...weights(), stability: 0 })
    expect(isValidCustomBalance(zero())).toBe(false)
    expect(isValidCustomBalance(zero({ absolute_priorities: [] }))).toBe(false)
  })

  it('round-trips in the default and every custom rule, with arrays that are never shared', () => {
    const shared = ['cost', 'error_rate'] as Priorities
    const config: OpenAIEvalConfig = {
      effects_enabled: true,
      bps_auto_enabled: false,
      scheduling_policy: 'custom_balance',
      custom_balance: weights({ absolute_priorities: shared }),
      policies: [
        // All-zero weights with a priority are saved as they are.
        { requested_model: 'gpt-5', policy: 'custom_balance', custom_balance: zero({ absolute_priorities: ['quality'] }) },
        // A custom rule without weights is filled from the default, priorities included.
        { requested_model: 'gpt-5-mini', policy: 'custom_balance' },
        { requested_model: 'gpt-4.1', policy: 'cost_first' }
      ],
      accounts: []
    }
    const payload = toSavePayload(config)
    expect(payload.custom_balance?.absolute_priorities).toEqual(['cost', 'error_rate'])
    expect(payload.policies?.[0].custom_balance).toEqual({ ...zero(), stability: 0, absolute_priorities: ['quality'] })
    expect(payload.policies?.[1].custom_balance?.absolute_priorities).toEqual(['cost', 'error_rate'])
    expect(payload.policies?.[2]).not.toHaveProperty('custom_balance')
    expect(payload.custom_balance?.absolute_priorities).not.toBe(shared)
    expect(payload.policies?.[1].custom_balance?.absolute_priorities).not.toBe(payload.custom_balance?.absolute_priorities)
    // Saving again from the echoed payload sends the same thing.
    expect(toSavePayload(payload)).toEqual(payload)
  })
})
