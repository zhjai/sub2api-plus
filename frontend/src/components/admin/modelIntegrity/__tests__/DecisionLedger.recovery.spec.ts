import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import type { SchedulerDecisionTrace } from '@/api/admin/accounts'
import { zhT } from '@/views/admin/modelIntegrity/__tests__/zhT'

vi.mock('vue-i18n', async importOriginal => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: zhT })
}))

import DecisionLedger from '../DecisionLedger.vue'

function trace(overrides: Partial<SchedulerDecisionTrace> = {}): SchedulerDecisionTrace {
  return {
    at: '2026-10-08T09:31:00Z',
    layer: 'load_balance',
    reason_code: 'runtime_recovery_trial',
    reason_text: '',
    requested_model: 'gpt-5',
    requested_reasoning_effort: 'high',
    scheduling_policy: 'stability_first',
    record_type: 'actual_dispatch',
    ranking_basis: 'snapshot',
    selected_rank: 2,
    sticky_previous_hit: false,
    sticky_session_hit: false,
    candidate_count: 2,
    top_k: 1,
    latency_ms: 1,
    load_skew: 0,
    group_id: 7,
    group_name: 'openai-team',
    selected_account_id: 11,
    selected_account_name: 'oauth-a',
    selected_account_type: 'oauth',
    acquired: true,
    excluded_account_count: 0,
    previous_response_given: false,
    session_given: false,
    candidates: [
      { account_id: 11, eligible: true, selected: true, in_top_k: true, rank: 2, decision_reason: 'runtime_recovery_trial' },
      { account_id: 12, eligible: true, selected: false, in_top_k: true, rank: 1, decision_reason: 'explicit_policy_rank' }
    ],
    ...overrides
  }
}

function mountLedger(traces: SchedulerDecisionTrace[]) {
  return mount(DecisionLedger, { props: { traces, accountName: (id: number) => `acct-${id}` } })
}

describe('DecisionLedger scheduled recovery trial', () => {
  it('translates the dispatch reason and the selected candidate’s reason, without calling the account recovered', async () => {
    const wrapper = mountLedger([trace()])
    const row = wrapper.get('[data-testid="decision-row"]')
    const why = row.get('.ledger-why').text()
    expect(why).toContain('定时恢复试用')
    expect(why).toContain('不代表账号已恢复')
    expect(why).not.toContain('runtime_recovery_trial')
    // A trial is a normal selection, not a routing problem.
    expect(row.classes()).not.toContain('ledger-row-problem')

    await row.get('.ledger-toggle').trigger('click')
    const [selected] = wrapper.findAll('[data-testid="candidate-row"]')
    expect(selected.get('.cand-why').text()).toBe(zhT('admin.modelIntegrity.scheduling.candidateReason.runtime_recovery_trial'))
  })

  it('keeps showing an unknown reason by its code', () => {
    const wrapper = mountLedger([trace({ reason_code: 'runtime_recovery_future' })])
    expect(wrapper.get('.ledger-why').text()).toBe('其他原因（runtime_recovery_future）。')
  })
})
