import { describe, expect, it } from 'vitest'
import type { OpenAIEvalRankingFactors, OpenAIEvalPolicyWeights } from '@/api/admin/accounts'
import {
  PRESET_WEIGHTS,
  RANKING_FACTORS,
  assessedQualityRatio,
  canAppendPage,
  coverageStatusKey,
  effectiveTone,
  exclusionScopeKey,
  fallbackReasonKey,
  isInactiveStatus,
  isQualityAssessed,
  isStaleEvaluation,
  mergeRankedAccounts,
  orderingKey,
  policyQualityEmphasis,
  qualityTestCounts,
  rankingSourceKey,
  rankingTriggerKey,
  unknownReasonKey,
  weightedFactorCount,
  weightsForPolicy
} from '../modelIntegrity'

function factors(overrides: Partial<OpenAIEvalRankingFactors['quality']> = {}): OpenAIEvalRankingFactors {
  return {
    price: { score: 0.5, known: true, observed_at: null, unknown_reason: null, rate_multiplier: 0.5, source: 'catalog' },
    error_rate: { score: 0.5, known: true, observed_at: null, unknown_reason: null, value: 0.01, sample_count: 10 },
    ttft: { score: 0.5, known: true, observed_at: null, unknown_reason: null, ms: 400, sample_count: 10 },
    load: { score: 0.5, known: true, observed_at: null, unknown_reason: null, load_rate: 20, waiting: 0, current_concurrency: 2 },
    quality: { score: 0.5, known: true, observed_at: null, unknown_reason: null, state: 'assessed', pass: 1, suspected_pass: 1, selected: 3, evaluated: 3, ratio: 2 / 3, expires_at: null, ...overrides }
  }
}

describe('preset weights', () => {
  it('mirrors the frozen contract, including load at evaluation time', () => {
    expect(PRESET_WEIGHTS.cost_first).toEqual({ price: 0.6, error_rate: 0.2, ttft: 0.1, load: 0.1, quality: 0 })
    expect(PRESET_WEIGHTS.stability_first).toEqual({ price: 0.1, error_rate: 0.5, ttft: 0.3, load: 0.1, quality: 0 })
    expect(PRESET_WEIGHTS.avoid_degradation).toEqual({ price: 0.4, error_rate: 0.35, ttft: 0.15, load: 0.1, quality: 0 })
    for (const weights of Object.values(PRESET_WEIGHTS)) {
      expect(RANKING_FACTORS.reduce((sum, factor) => sum + weights[factor], 0)).toBeCloseTo(1, 6)
    }
  })

  it('gives stability first no quality weight, so degradation cannot influence it', () => {
    expect(policyQualityEmphasis('stability_first')).toBe('ignored')
    expect(weightsForPolicy('stability_first')?.quality).toBe(0)
  })

  it('treats avoid degradation as a tier filter rather than a price-tradable weight', () => {
    expect(policyQualityEmphasis('avoid_degradation')).toBe('tier')
    // The tier decides first, so the weighted quality term is deliberately zero.
    expect(weightsForPolicy('avoid_degradation')?.quality).toBe(0)
  })

  it('weighs quality only under custom balance and normalizes the saved values', () => {
    const custom: OpenAIEvalPolicyWeights = { cost: 0.3, error_rate: 0.1, ttft: 0.1, load: 0.1, quality: 0.4 }
    expect(policyQualityEmphasis('custom_balance')).toBe('weighted')
    const weights = weightsForPolicy('custom_balance', custom)
    expect(weights?.quality).toBeCloseTo(0.4, 6)
    expect(RANKING_FACTORS.reduce((sum, factor) => sum + (weights?.[factor] ?? 0), 0)).toBeCloseTo(1, 6)
  })

  it('has no preset for the system default policy and counts only non-zero weights', () => {
    expect(weightsForPolicy('')).toBeNull()
    expect(weightedFactorCount(PRESET_WEIGHTS.cost_first)).toBe(4)
  })
})

describe('effective status', () => {
  it('never presents a saved-but-inactive policy as live', () => {
    expect(effectiveTone('active')).toBe('active')
    expect(effectiveTone('active_partial')).toBe('partial')
    expect(effectiveTone('live_fallback')).toBe('partial')
    expect(effectiveTone('error')).toBe('error')
    for (const status of ['inactive_effects_off', 'inactive_legacy_policy', 'no_targets'] as const) {
      expect(effectiveTone(status)).toBe('off')
      expect(isInactiveStatus(status)).toBe(true)
    }
    expect(isInactiveStatus('active')).toBe(false)
    // An unknown or absent status is treated as inactive, never as live.
    expect(effectiveTone(null)).toBe('off')
    expect(effectiveTone('not_a_status' as never)).toBe('off')
  })
})

describe('quality evidence', () => {
  it('counts each selected test once, whatever its sample count', () => {
    expect(qualityTestCounts(factors())).toEqual({ passed: 2, selected: 3, evaluated: 3 })
    expect(assessedQualityRatio(factors())).toBeCloseTo(2 / 3, 6)
    // Two selected with one passing is 50%, three selected with one is 33.3%.
    expect(assessedQualityRatio(factors({ pass: 1, suspected_pass: 0, selected: 2, evaluated: 2, ratio: 0.5 }))).toBe(0.5)
  })

  it('keeps every non-assessed state unknown rather than healthy', () => {
    for (const state of ['unknown', 'insufficient', 'stale'] as const) {
      expect(isQualityAssessed(state)).toBe(false)
      // A stale ratio next to a non-assessed state must not be shown as a rate.
      expect(assessedQualityRatio(factors({ state, ratio: 1 }))).toBeNull()
    }
    expect(assessedQualityRatio(undefined)).toBeNull()
  })
})

describe('key mapping', () => {
  it('maps unknown reasons, sources and scopes to safe labels', () => {
    expect(unknownReasonKey('quality_insufficient')).toBe('quality_insufficient')
    expect(unknownReasonKey('something_new')).toBe('generic')
    expect(unknownReasonKey(null)).toBe('generic')
    expect(rankingSourceKey('observed_route')).toBe('observed_route')
    expect(rankingSourceKey('mystery')).toBe('unknown')
    expect(exclusionScopeKey('live')).toBe('live')
    expect(exclusionScopeKey(undefined)).toBe('account')
    expect(coverageStatusKey('live_fallback')).toBe('live_fallback')
    expect(orderingKey('quality_then_score')).toBe('quality_then_score')
    expect(orderingKey('nope')).toBe('score_desc')
    expect(rankingTriggerKey('policy_saved')).toBe('policy_saved')
    expect(rankingTriggerKey(undefined)).toBe('manual')
    expect(fallbackReasonKey('dimension_not_covered')).toBe('dimension_not_covered')
    expect(fallbackReasonKey('brand_new')).toBe('generic')
  })
})

describe('generations and paging', () => {
  it('flags a ranking built for another revision as stale', () => {
    expect(isStaleEvaluation(3, 4)).toBe(true)
    expect(isStaleEvaluation(4, 4)).toBe(false)
    // Nothing to compare against is not evidence of staleness.
    expect(isStaleEvaluation(null, 4)).toBe(false)
    expect(isStaleEvaluation(4, null)).toBe(false)
  })

  it('appends a page only within the same generation and filters', () => {
    const dimension = { evaluation_id: 'i-1', group_id: 7, requested_model: 'gpt-5', reasoning_effort: 'high' }
    expect(canAppendPage(dimension, { ...dimension })).toBe(true)
    expect(canAppendPage(dimension, { ...dimension, evaluation_id: 'i-2' })).toBe(false)
    expect(canAppendPage(dimension, { ...dimension, reasoning_effort: '' })).toBe(false)
    expect(canAppendPage(null, dimension)).toBe(false)
  })

  it('deduplicates a boundary row instead of showing one account twice', () => {
    const row = (account_id: number, rank: number) => ({ account_id, rank } as never)
    const merged = mergeRankedAccounts([row(11, 1), row(12, 2)], [row(12, 2), row(13, 3)])
    expect(merged.map(item => item.account_id)).toEqual([11, 12, 13])
  })
})
