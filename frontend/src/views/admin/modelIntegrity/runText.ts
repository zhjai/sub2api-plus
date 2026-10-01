import type { OpenAIEvalModelCatalog, OpenAIEvalRun } from '@/api/admin/accounts'

type Translate = (key: string, params?: Record<string, unknown>) => string

export function statusLabel(t: Translate, status: string): string {
  const key = `admin.modelIntegrity.status.${status}`
  const text = t(key)
  return text === key ? status : text
}

/** One plain sentence explaining a run; never exposes internal reason codes unless unknown. */
export function runExplanation(t: Translate, run: OpenAIEvalRun, catalog?: OpenAIEvalModelCatalog | null): string {
  if (run.test_type === 'state_probe' && run.outcome.state_probe) {
    const verdict = run.outcome.state_probe.verdict
    const failure = run.outcome.state_probe.failure
    if (failure) return reasonText(t, failure, run, catalog)
    const key = `admin.modelIntegrity.reason.stateProbe.${verdict}`
    const text = t(key)
    return text === key ? statusLabel(t, verdict) : text
  }
  if (run.test_type === 'modeltrace' && run.outcome.modeltrace?.prediction) {
    const trace = run.outcome.modeltrace
    return `${t('admin.modelIntegrity.tests.detail.modeltraceMetric', { model: trace.prediction, probability: (trace.probability ?? 0).toFixed(2) })} ${reasonText(t, 'modeltrace_behavioral_attribution', run, catalog)}`
  }
  return reasonText(t, run.outcome.reason, run, catalog)
}

export function reasonText(t: Translate, reason: string | undefined, run: OpenAIEvalRun, catalog?: OpenAIEvalModelCatalog | null): string {
  if (!reason) {
    return run.status === 'running' ? t('admin.modelIntegrity.reason.running') : t('admin.modelIntegrity.reason.noDetail')
  }
  const key = `admin.modelIntegrity.reason.${reason}`
  const text = t(key, {
    valid: run.outcome.sample_count ?? '-',
    required: run.outcome.expected_count ?? '-',
    answer: catalog?.candy?.expected_answer ?? '-'
  })
  return text === key ? t('admin.modelIntegrity.reason.unknown', { code: reason }) : text
}
