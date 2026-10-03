import type { OpenAIEvalModelCatalog, OpenAIEvalRun } from '@/api/admin/accounts'
import { runStatusKey } from './modelIntegrity'

type Translate = (key: string, params?: Record<string, unknown>) => string

export function statusLabel(t: Translate, status: string): string {
  const key = `admin.modelIntegrity.status.${status}`
  const text = t(key)
  return text === key ? status : text
}

/** Label for a whole run: a Luna attribution reads "possible Luna", not a generic warning. */
export function runStatusLabel(t: Translate, run: OpenAIEvalRun): string {
  return statusLabel(t, runStatusKey(run))
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
    // Older runs (status "attributed") carry no attribution verdict reason.
    const reason = run.outcome.reason && run.outcome.reason !== 'modeltrace_insufficient_outputs' ? run.outcome.reason : 'modeltrace_behavioral_attribution'
    return joinSentences(t('admin.modelIntegrity.tests.detail.modeltraceMetric', { model: trace.prediction, probability: (trace.probability ?? 0).toFixed(2) }), reasonText(t, reason, run, catalog))
  }
  const nearest = run.test_type === 'fingerprint' ? run.outcome.fingerprint?.nearest_model : ''
  if (nearest && (run.status === 'suspected_normal' || run.status === 'warning')) {
    return joinSentences(t('admin.modelIntegrity.tests.detail.fingerprintNearest', { model: nearest }), reasonText(t, run.outcome.reason, run, catalog))
  }
  return reasonText(t, run.outcome.reason, run, catalog)
}

/** Chinese sentences end in full-width punctuation and are joined without a space. */
function joinSentences(first: string, second: string): string {
  return /[。！？；]$/.test(first) ? `${first}${second}` : `${first} ${second}`
}

export function reasonText(t: Translate, reason: string | undefined, run: OpenAIEvalRun, catalog?: OpenAIEvalModelCatalog | null): string {
  if (!reason) {
    return run.status === 'running' ? t('admin.modelIntegrity.reason.running') : t('admin.modelIntegrity.reason.noDetail')
  }
  const key = `admin.modelIntegrity.reason.${reason}`
  const text = t(key, {
    valid: run.outcome.sample_count ?? '-',
    required: run.outcome.expected_count ?? '-',
    count: run.outcome.expected_count ?? run.outcome.sample_count ?? '-',
    answer: catalog?.candy?.expected_answer ?? '-'
  })
  return text === key ? t('admin.modelIntegrity.reason.unknown', { code: reason }) : text
}
