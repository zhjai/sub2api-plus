import { describe, expect, it } from 'vitest'
import { BACKGROUND_DEFAULTS, backgroundControlPayload, defaultBackgroundControl, pausedUntil, runFeasibility, toSavePayload } from '../modelIntegrity'

describe('background control helpers', () => {
  it('fits small runs and rejects fingerprint sizes under the 60/h, 60 s, 15 min defaults', () => {
    expect(runFeasibility(1, 3, BACKGROUND_DEFAULTS)).toMatchObject({ fits: true, capacity: 15, limitedBy: 'interval', minDurationSeconds: 0 })
    expect(runFeasibility(3, 9, BACKGROUND_DEFAULTS)).toMatchObject({ fits: true, minDurationSeconds: 120 })
    // Like the server, the last send must start strictly inside the window: 16 × 60 s does not fit 15 min.
    expect(runFeasibility(15, 15, BACKGROUND_DEFAULTS).fits).toBe(true)
    expect(runFeasibility(16, 16, BACKGROUND_DEFAULTS).fits).toBe(false)
    for (const samples of [60, 200, 400]) expect(runFeasibility(samples, samples * 3, BACKGROUND_DEFAULTS).fits).toBe(false)
  })

  it('lets the account RPM and the hourly budget cap the window like openAIEvalBudgetFeasible', () => {
    const fast = { ...BACKGROUND_DEFAULTS, min_send_interval_seconds: 1 }
    expect(runFeasibility(60, 60, fast)).toMatchObject({ fits: true, capacity: 60, limitedBy: 'hourly' })
    expect(runFeasibility(61, 61, { ...fast, sampling_window_seconds: 7200 }).fits).toBe(false)
    // floor((n-1)/rpm) × 60 < 900 s: with RPM 2, 30 sends need 14 min, 31 need 15 min.
    expect(runFeasibility(30, 30, fast, 2)).toMatchObject({ fits: true, capacity: 30, limitedBy: 'rpm', minDurationSeconds: 840 })
    expect(runFeasibility(31, 31, fast, 2).fits).toBe(false)
  })

  it('never treats a zero send interval as "no interval": the server applies 60 s', () => {
    const zero = { ...BACKGROUND_DEFAULTS, min_send_interval_seconds: 0 }
    expect(runFeasibility(16, 16, zero)).toMatchObject({ fits: false, capacity: 15, limitedBy: 'interval' })
    expect(backgroundControlPayload({ ...defaultBackgroundControl(3), budget_enabled: true, min_send_interval_seconds: 0 }).min_send_interval_seconds).toBe(60)
  })

  it('treats an expired or unreadable pause as not paused', () => {
    const now = Date.parse('2026-10-08T00:00:00Z')
    expect(pausedUntil({ paused_until: '2026-10-07T23:59:00Z' }, now)).toBeNull()
    expect(pausedUntil({ paused_until: 'nope' }, now)).toBeNull()
    expect(pausedUntil({ paused_until: '2026-10-08T01:00:00Z' }, now)?.toISOString()).toBe('2026-10-08T01:00:00.000Z')
  })

  it('strips runtime, drops a reason without a pause and falls back to defaults for bad numbers', () => {
    const control = { ...defaultBackgroundControl(7), pause_reason: 'old', max_requests_per_hour: Number.NaN, runtime: { sent_last_hour: 1, reserved: 0, active_runs: 0 }, runtime_unavailable_reason: 'evaluation_budget_unavailable' }
    expect(backgroundControlPayload(control)).toEqual({ ...defaultBackgroundControl(7), pause_reason: '' })
  })

  it('keeps background_controls absent or present exactly as loaded', () => {
    const base = { effects_enabled: false, bps_auto_enabled: false, accounts: [] }
    expect(toSavePayload(base)).not.toHaveProperty('background_controls')
    expect(toSavePayload({ ...base, background_controls: [] }).background_controls).toEqual([])
  })
})
