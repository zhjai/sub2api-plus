import { describe, expect, it } from 'vitest'
import { candidateReasonKey, decisionKey, runtimeRecoveryView } from '../modelIntegrity'
import { zhT } from './zhT'

describe('runtimeRecoveryView', () => {
  it('is null when the server sent no recovery state', () => {
    expect(runtimeRecoveryView(undefined)).toBeNull()
    expect(runtimeRecoveryView(null)).toBeNull()
    expect(runtimeRecoveryView({})).toBeNull()
    expect(runtimeRecoveryView({ runtime_recovery: null })).toBeNull()
  })

  it('reads each known state with its next trial time', () => {
    for (const state of ['waiting', 'ready', 'in_flight'] as const) {
      expect(runtimeRecoveryView({ runtime_recovery: { state, next_trial_at: '2026-10-08T09:30:00Z' } })).toEqual({ state, code: state, nextTrialAt: '2026-10-08T09:30:00Z' })
    }
    expect(runtimeRecoveryView({ runtime_recovery: { state: 'waiting', next_trial_at: '' } })?.nextTrialAt).toBeNull()
  })

  it('keeps an unknown state as its code instead of mapping it to a known one', () => {
    expect(runtimeRecoveryView({ runtime_recovery: { state: 'recovered', next_trial_at: '2026-10-08T09:30:00Z' } })).toMatchObject({ state: 'other', code: 'recovered' })
  })

  it('never names any state, ready included, as recovered or healthy', () => {
    for (const state of ['waiting', 'ready', 'in_flight']) {
      const label = zhT(`admin.modelIntegrity.scheduling.board.recovery.${state}`)
      const detail = zhT(`admin.modelIntegrity.scheduling.board.recovery.detail.${state}`)
      for (const text of [label, detail]) {
        expect(text).not.toMatch(/已恢复|正常|健康/)
      }
    }
    expect(zhT('admin.modelIntegrity.scheduling.board.recovery.ready')).toBe('可试用')
    expect(zhT('admin.modelIntegrity.scheduling.board.recovery.detail.ready')).toContain('仍超过阈值，尚未恢复')
  })
})

describe('runtime_recovery_trial reason code', () => {
  it('is a known dispatch decision and candidate reason with its own copy', () => {
    expect(decisionKey('runtime_recovery_trial')).toBe('runtime_recovery_trial')
    expect(candidateReasonKey('runtime_recovery_trial')).toBe('runtime_recovery_trial')
    expect(zhT('admin.modelIntegrity.scheduling.decision.runtime_recovery_trial')).toContain('定时恢复试用')
    expect(zhT('admin.modelIntegrity.scheduling.decision.runtime_recovery_trial')).toContain('不代表账号已恢复')
    expect(zhT('admin.modelIntegrity.scheduling.candidateReason.runtime_recovery_trial')).toContain('未视为已恢复')
  })
})
