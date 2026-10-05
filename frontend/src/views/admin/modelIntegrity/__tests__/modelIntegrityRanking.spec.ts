import { describe, expect, it } from 'vitest'
import type { OpenAIEvalRankingFactors, OpenAIEvalPolicyWeights } from '@/api/admin/accounts'
import {
  PRESET_WEIGHTS,
  RANKING_FACTORS,
  accountFactorSourceKind,
  accountQualityReference,
  assessedQualityRatio,
  candidateReasonKey,
  coverageStatusKey,
  dispatchQuality,
  effectiveTone,
  exclusionScopeKey,
  factorRawValue,
  factorSourceKind,
  fallbackReasonKey,
  isInactiveStatus,
  isQualityFirst,
  isQualityAssessed,
  isStaleEvaluation,
  mergeRankedAccounts,
  orderingKey,
  policyQualityEmphasis,
  qualityTestCounts,
  rankingSourceKey,
  rankingTriggerKey,
  ruleExceptionsFor,
  sameOverviewBinding,
  sourceKind,
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

  it('continues a page only within the same generation and group filter', () => {
    const bound = { evaluationId: 'i-1', groupId: 7 }
    expect(sameOverviewBinding(bound, { ...bound })).toBe(true)
    expect(sameOverviewBinding(bound, { ...bound, evaluationId: 'i-2' })).toBe(false)
    expect(sameOverviewBinding(bound, { ...bound, groupId: null })).toBe(false)
    expect(sameOverviewBinding(null, bound)).toBe(false)
  })

  it('deduplicates a boundary row instead of showing one account twice', () => {
    const row = (account_id: number, rank: number) => ({ account_id, rank } as never)
    const merged = mergeRankedAccounts([row(11, 1), row(12, 2)], [row(12, 2), row(13, 3)])
    expect(merged.map(item => item.account_id)).toEqual([11, 12, 13])
  })
})

describe('account overview factors', () => {
  it('labels each source honestly and never promotes an unlisted source to measured', () => {
    expect(sourceKind('request_metrics')).toBe('measured')
    expect(sourceKind('v1_matched_probe')).toBe('probe')
    expect(sourceKind('default')).toBe('default')
    expect(sourceKind('legacy_neutral')).toBe('neutral')
    expect(sourceKind('brand_new_source')).toBe('other')
    expect(sourceKind(null)).toBeNull()
  })

  it('labels a default only when the server applied one, and a partial default as mixed', () => {
    const f = factors()
    expect(factorSourceKind('ttft', f)).toBe('measured')
    expect(factorSourceKind('ttft', { ...f, ttft: { ...f.ttft, known: false, ms: null, score: 0.9, default_applied: true } })).toBe('default')
    expect(factorSourceKind('error_rate', { ...f, error_rate: { ...f.error_rate, default_applied: true } })).toBe('mixed')
    expect(factorSourceKind('price', { ...f, price: { ...f.price, known: false, rate_multiplier: null, source: 'optimistic_default', default_applied: true } })).toBe('default')
    const missing = { ...f, ttft: { ...f.ttft, known: false, ms: null } }
    // Without the server saying "default", a missing reading is unknown, not a default.
    expect(factorSourceKind('ttft', missing)).toBe('unknown')
    const probed = { ...f, error_rate: { ...f.error_rate, source: 'v1_matched_probe' } }
    expect(factorSourceKind('error_rate', probed)).toBe('probe')
    expect(factorSourceKind('error_rate', { ...f, error_rate: { ...f.error_rate, source: 'macro_evidence' } })).toBe('measured')
    expect(factorSourceKind('price', { ...f, price: { ...f.price, source: 'brand_new' } })).toBe('other')
  })

  it('refines the account error rate from the model cells it averages', () => {
    const f = factors()
    const account = { ...f, error_rate: { ...f.error_rate, source: 'macro_evidence' } }
    const real = { factors: { ...f, error_rate: { ...f.error_rate, source: 'request_ewma_account_model_effort' } } }
    const probe = { factors: { ...f, error_rate: { ...f.error_rate, source: 'v1_matched_probe' } } }
    expect(accountFactorSourceKind('error_rate', account, [real, real])).toBe('measured')
    expect(accountFactorSourceKind('error_rate', account, [probe])).toBe('probe')
    expect(accountFactorSourceKind('error_rate', account, [real, probe])).toBe('mixed')
    // Price is an account fact; model cells never change its source.
    expect(accountFactorSourceKind('price', { ...f, price: { ...f.price, source: 'account_rate' } }, [probe])).toBe('measured')
  })

  it('treats an unassessed pass rate as neutral only when it still scored points', () => {
    const unknown = factors({ state: 'unknown', ratio: null, known: false })
    expect(factorSourceKind('quality', unknown, { quality: 5 })).toBe('neutral')
    expect(factorSourceKind('quality', unknown, { quality: 0 })).toBe('unknown')
    expect(factorSourceKind('quality', factors())).toBe('measured')
  })

  it('keeps a missing raw value null instead of 0', () => {
    const f = factors()
    const missing = { ...f, error_rate: { ...f.error_rate, known: false, value: null }, ttft: { ...f.ttft, ms: null } }
    expect(factorRawValue('error_rate', missing)).toBeNull()
    expect(factorRawValue('ttft', missing)).toBeNull()
    expect(factorRawValue('price', f)).toBe(0.5)
    expect(factorRawValue('quality', factors({ state: 'insufficient' }))).toBeNull()
  })

  it('puts the pass rate first only for quality-first ordering', () => {
    expect(isQualityFirst('quality_then_score', 'avoid_degradation')).toBe(true)
    expect(isQualityFirst('score_desc', 'custom_balance')).toBe(false)
    expect(isQualityFirst(undefined, 'avoid_degradation')).toBe(true)
    expect(isQualityFirst(undefined, 'stability_first')).toBe(false)
  })

  it('lists only model rules that differ from the default and match an evidenced model', () => {
    const models = [{ requested_model: 'gpt-5.6', reasoning_effort: 'high' }, { requested_model: 'gpt-6', reasoning_effort: '' }]
    const rules = [
      { requested_model: 'GPT-5.6', reasoning_effort: '', policy: 'stability_first' as const },
      { requested_model: 'gpt-6', reasoning_effort: 'low', policy: 'cost_first' as const },
      { requested_model: 'gpt-6', reasoning_effort: '', policy: 'avoid_degradation' as const },
      { requested_model: 'luna', reasoning_effort: '', policy: 'stability_first' as const }
    ]
    expect(ruleExceptionsFor(rules, models, 'avoid_degradation').map(rule => rule.requested_model)).toEqual(['GPT-5.6'])
  })
})

describe('account-wide quality reference', () => {
  const prior = { ratio: 0.625, evaluation_id: 'eval-1', evaluated_at: '2026-10-01T07:00:00Z', expires_at: '2026-10-01T09:00:00Z', source_models: ['gpt-5-mini/high'] }

  it('reads the reference only from the server object behind the account_prior basis', () => {
    expect(accountQualityReference({ quality_basis: 'account_prior', account_quality_prior: prior }))
      .toEqual({ ratio: 0.625, evaluationId: 'eval-1', evaluatedAt: '2026-10-01T07:00:00Z', expiresAt: '2026-10-01T09:00:00Z', sourceModels: ['gpt-5-mini/high'] })
  })

  it('keeps a 0 % reference, which is a real value and not a missing one', () => {
    expect(accountQualityReference({ quality_basis: 'account_prior', account_quality_prior: { ...prior, ratio: 0, source_models: [] } })?.ratio).toBe(0)
  })

  it('keeps a full 100 % reference, which is still not this model\'s pass rate', () => {
    const full = { quality_basis: 'account_prior', account_quality_prior: { ...prior, ratio: 1 } }
    expect(accountQualityReference(full)?.ratio).toBe(1)
    expect(dispatchQuality({ ...full, eligible: true, selected: false, in_top_k: true })).toEqual({ kind: 'unknown', reason: 'quality_unknown' })
  })

  it('returns nothing when the basis is exact, missing or unrecognised', () => {
    expect(accountQualityReference({ quality_basis: 'exact', account_quality_prior: prior })).toBeNull()
    expect(accountQualityReference({ account_quality_prior: prior })).toBeNull()
    expect(accountQualityReference({ quality_basis: 'unknown_current_model' })).toBeNull()
    // A basis without its object cannot invent one.
    expect(accountQualityReference({ quality_basis: 'account_prior' })).toBeNull()
    expect(accountQualityReference({ quality_basis: 'account_prior', account_quality_prior: { ...prior, ratio: Number.NaN } })).toBeNull()
  })

  it('reports an account_prior tier as unknown even when exact counters are present', () => {
    const base = { eligible: true, selected: false, in_top_k: true, quality_state: 'assessed', evaluated_count: 3, pass_count: 3, suspected_pass_count: 0, quality_ratio: 1 }
    expect(dispatchQuality({ ...base, quality_basis: 'exact' })).toEqual({ kind: 'assessed', ratio: 1, pass: 3, suspected: 0, evaluated: 3 })
    // The same counters under an account-wide reference describe another model.
    expect(dispatchQuality({ ...base, quality_basis: 'account_prior' })).toEqual({ kind: 'unknown', reason: 'quality_unknown' })
  })

  it('reports an account_prior tier from factors as unknown pass-rate evidence too', () => {
    const candidate = { eligible: true, selected: false, in_top_k: true, quality_basis: 'account_prior', factors: factors() }
    expect(dispatchQuality(candidate)).toEqual({ kind: 'unknown', reason: 'quality_unknown' })
    // Without the basis the same measured factor is a real pass rate.
    expect(dispatchQuality({ ...candidate, quality_basis: 'exact' }).kind).toBe('assessed')
  })

  it('maps the account_prior decision reason to its own explanation', () => {
    expect(candidateReasonKey('account_prior_tier')).toBe('account_prior_tier')
    expect(candidateReasonKey('something_new')).toBeNull()
  })
})
