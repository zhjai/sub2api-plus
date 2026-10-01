import { describe, expect, it } from 'vitest'
import type { OpenAIEvalConfig, OpenAIEvalModelCatalog, OpenAIEvalRouteConfig } from '@/api/admin/accounts'
import {
  activeScheduleCount,
  bpsModeOf,
  dailyRequests,
  exclusionKey,
  newRoute,
  normalizeRoute,
  normalizeSchedule,
  orderCandidates,
  requestsPerRun,
  resultTone,
  toSavePayload,
  totalDailyRequests
} from '../modelIntegrity'

const catalog = {
  items: [{ id: 'gpt-5' }],
  baseline_version: 'v1',
  baseline_models: [],
  candy: { expected_answer: 21, confidence: 'low', scheduling: 'alert_only' },
  evaluation_notice: '',
  reasoning_efforts: ['', 'high'],
  fingerprint_modes: [{ id: 'quick', samples: 60 }, { id: 'standard', samples: 200 }, { id: 'strict', samples: 400 }],
  modeltrace: { requests: 3, bank_revision: 'x', candidate_count: 16, scheduling: 'alert_only' }
} satisfies OpenAIEvalModelCatalog

function oauthRoute(overrides: Partial<OpenAIEvalRouteConfig> = {}): OpenAIEvalRouteConfig {
  return { ...newRoute(1, 'gpt-5', ''), direct_oauth_eligible: true, ...overrides }
}

describe('request budget', () => {
  it('uses the fixed per-run request counts the backend enforces', () => {
    const route = oauthRoute()
    expect(requestsPerRun(route, 'candy', catalog)).toBe(5)
    expect(requestsPerRun(route, 'state_probe', catalog)).toBe(2)
    expect(requestsPerRun(route, 'modeltrace', catalog)).toBe(3)
    expect(requestsPerRun(route, 'fingerprint', catalog)).toBe(60)
    expect(requestsPerRun(route, 'fingerprint', catalog, 'strict')).toBe(400)
  })

  it('estimates daily requests only for enabled schedules', () => {
    const route = oauthRoute()
    expect(dailyRequests(route, 'candy', catalog)).toBe(0)
    route.candy_schedule.enabled = true
    route.candy_schedule.interval_seconds = 900
    expect(dailyRequests(route, 'candy', catalog)).toBe(480)
    route.fingerprint_schedule.enabled = true
    route.fingerprint_schedule.sample_mode = 'standard'
    route.fingerprint_schedule.interval_seconds = 86400
    expect(totalDailyRequests([route], catalog)).toBe(680)
    expect(activeScheduleCount([route])).toBe(2)
  })

  it('does not count State Probe schedules the server will never run', () => {
    const route = oauthRoute({ direct_oauth_eligible: false })
    route.state_probe_schedule.enabled = true
    expect(dailyRequests(route, 'state_probe', catalog)).toBe(0)
    expect(activeScheduleCount([route])).toBe(0)
  })
})

describe('schedule normalisation', () => {
  it('clamps interval to the per-type minimum and jitter to the available slack', () => {
    const schedule = { enabled: true, interval_seconds: 60, jitter_seconds: 999999 }
    normalizeSchedule(schedule, 'candy')
    expect(schedule.interval_seconds).toBe(900)
    expect(schedule.jitter_seconds).toBe(0)
    const fingerprint = { enabled: true, interval_seconds: 3 * 86400, jitter_seconds: 10 * 86400 }
    normalizeSchedule(fingerprint, 'fingerprint')
    expect(fingerprint.jitter_seconds).toBe(2 * 86400)
    expect((fingerprint as { sample_mode?: string }).sample_mode).toBe('quick')
  })
})

describe('BPS mode compatibility', () => {
  it('maps the legacy bps_auto boolean when no explicit mode exists', () => {
    expect(bpsModeOf({ bps_auto: true })).toBe('auto')
    expect(bpsModeOf({ bps_auto: false })).toBe('force_off')
    expect(bpsModeOf({ bps_auto: false, bps_mode: 'force_on' })).toBe('force_on')
  })

  it('fills missing schedules on routes saved by older versions', () => {
    const legacy = { account_id: 3, requested_model: 'gpt-5', reasoning_effort: '', candy_schedule: { enabled: true, interval_seconds: 3600, jitter_seconds: 0 }, fingerprint_schedule: { enabled: false, interval_seconds: 86400, jitter_seconds: 0 }, bps_auto: true } as unknown as OpenAIEvalRouteConfig
    const route = normalizeRoute(legacy)
    expect(route.modeltrace_schedule.interval_seconds).toBe(86400)
    expect(route.state_probe_schedule.interval_seconds).toBe(21600)
    expect(route.bps_mode).toBe('auto')
  })
})

describe('save payload', () => {
  it('keeps fields the page does not edit and strips read-only runtime state', () => {
    const route = oauthRoute({ bps_mode: 'auto', bps_state: { active: true, degraded_streak: 3, healthy_streak: 0 } })
    const config: OpenAIEvalConfig = {
      revision: 7,
      effects_enabled: true,
      bps_auto_enabled: true,
      scheduling_policy: 'cost_first',
      policies: [{ requested_model: 'gpt-5', policy: 'avoid_degradation' }],
      accounts: [route]
    }
    const payload = toSavePayload(config)
    expect(payload.revision).toBe(7)
    expect(payload.effects_enabled).toBe(true)
    expect(payload.scheduling_policy).toBe('cost_first')
    expect(payload.policies).toEqual([{ requested_model: 'gpt-5', reasoning_effort: '', policy: 'avoid_degradation' }])
    expect(payload.accounts[0]).not.toHaveProperty('bps_state')
    expect(payload.accounts[0]).not.toHaveProperty('direct_oauth_eligible')
    expect(payload.accounts[0].bps_mode).toBe('auto')
    expect(payload.accounts[0].bps_auto).toBe(true)
  })
})

describe('result tone', () => {
  it('only marks warning/different as needing attention', () => {
    expect(resultTone('pass')).toBe('ok')
    expect(resultTone('warning')).toBe('attention')
    expect(resultTone('different')).toBe('attention')
    expect(resultTone('insufficient')).toBe('neutral')
    expect(resultTone('error')).toBe('neutral')
  })
})

describe('scheduler explanations', () => {
  it('groups prefixed exclusion codes and falls back for unknown ones', () => {
    expect(exclusionKey('model_not_supported')).toBe('model_not_supported')
    expect(exclusionKey('quota_auto_pause_7d')).toBe('quota_auto_pause')
    expect(exclusionKey('grok_free_quota_soft_gate')).toBe('platform_quota')
    expect(exclusionKey('something_new')).toBe('unknown')
    expect(exclusionKey(undefined)).toBe('unknown')
  })

  it('lists the chosen account first, then servable ones by score, then excluded', () => {
    const ordered = orderCandidates([
      { account_id: 1, eligible: false, selected: false },
      { account_id: 2, eligible: true, selected: false, score: 0.2 },
      { account_id: 3, eligible: true, selected: false, score: 0.9 },
      { account_id: 4, eligible: true, selected: true, score: 0.5 }
    ])
    expect(ordered.map(item => item.account_id)).toEqual([4, 3, 2, 1])
  })
})
