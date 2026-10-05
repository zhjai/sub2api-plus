import type { OpenAIEvalModelCatalog, OpenAIEvalRun, OpenAIEvalSampleRecord } from '@/api/admin/accounts'
import { attributionReinterpretation, candyExpectedAnswer, failedSampleCount, firstSampleFailure, redactSecrets, runSamples, runStatusKey, sampleFailed } from './modelIntegrity'

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
    if (failure) {
      const base = reasonText(t, failure, run, catalog)
      const detail = stateProbeFailureDetail(t, run)
      return detail ? joinSentences(base, detail) : base
    }
    const key = `admin.modelIntegrity.reason.stateProbe.${verdict}`
    const text = t(key)
    return text === key ? statusLabel(t, verdict) : text
  }
  if (run.test_type === 'modeltrace' && run.outcome.modeltrace?.prediction) {
    const trace = run.outcome.modeltrace
    // Older runs (status "attributed") carry no attribution verdict reason.
    const reason = run.outcome.reason && run.outcome.reason !== 'modeltrace_insufficient_outputs' ? run.outcome.reason : 'modeltrace_behavioral_attribution'
    const verdict = joinSentences(t('admin.modelIntegrity.tests.detail.modeltraceMetric', { model: trace.prediction, probability: (trace.probability ?? 0).toFixed(2) }), reasonText(t, reason, run, catalog))
    // A partial attribution keeps its verdict; failed requests are named as request failures, not degradation.
    const failure = runFailureDetail(t, run) || (run.error ? t('admin.modelIntegrity.tests.samples.runError', { error: redactSecrets(run.error) }) : '')
    return failure ? joinSentences(verdict, failure) : verdict
  }
  const nearest = run.test_type === 'fingerprint' ? run.outcome.fingerprint?.nearest_model : ''
  if (nearest && (run.status === 'suspected_normal' || run.status === 'warning')) {
    return joinSentences(t('admin.modelIntegrity.tests.detail.fingerprintNearest', { model: nearest }), reasonText(t, run.outcome.reason, run, catalog))
  }
  const base = reasonText(t, run.outcome.reason, run, catalog)
  // Insufficient evidence and errors keep the upstream cause next to the verdict.
  const failure = run.status === 'insufficient' || run.status === 'uncertain' || run.status === 'error' ? runFailureDetail(t, run) : ''
  return failure ? joinSentences(base, failure) : base
}

/**
 * "Originally recorded as … under rule …" for a run the server now reads under
 * a newer attribution rule. Empty when the verdict is unchanged; never implies
 * the test ran again.
 */
export function reinterpretationText(t: Translate, run: OpenAIEvalRun): string {
  const original = attributionReinterpretation(run)
  if (!original) return ''
  return t('admin.modelIntegrity.tests.detail.reinterpreted', {
    status: statusLabel(t, runStatusKey({ status: original.status, outcome: { ...run.outcome, reason: original.reason } })),
    rule: original.rule || '—',
    current: original.currentRule || '—'
  })
}

/** Chinese sentences end in full-width punctuation and are joined without a space. */
function joinSentences(first: string, second: string): string {
  return /[。！？；]$/.test(first) ? `${first}${second}` : `${first} ${second}`
}

export function reasonText(t: Translate, reason: string | undefined, run: OpenAIEvalRun, catalog?: OpenAIEvalModelCatalog | null): string {
  if (!reason) {
    return run.status === 'running' ? t('admin.modelIntegrity.reason.running') : t('admin.modelIntegrity.reason.noDetail')
  }
  const expected = run.test_type === 'candy' ? candyExpectedAnswer(run, catalog) : null
  if (reason === 'one_or_more_public_candy_variants_failed' && expected === null) {
    return t('admin.modelIntegrity.reason.one_or_more_public_candy_variants_failed_unversioned', { count: run.outcome.expected_count ?? run.outcome.sample_count ?? '-' })
  }
  const key = `admin.modelIntegrity.reason.${reason}`
  const text = t(key, {
    valid: run.outcome.sample_count ?? '-',
    required: run.outcome.expected_count ?? '-',
    count: run.outcome.expected_count ?? run.outcome.sample_count ?? '-',
    answer: expected ?? '-',
    target: run.requested_model || '-'
  })
  return text === key ? t('admin.modelIntegrity.reason.unknown', { code: reason }) : text
}

/**
 * "HTTP 502" or the short error code. A 2xx status is not the cause of a
 * failure (the stream broke after the upstream accepted it), so the code wins.
 */
export function sampleErrorHeadline(t: Translate, sample: OpenAIEvalSampleRecord): string {
  if (sample.http_status && sample.http_status >= 400) return t('admin.modelIntegrity.tests.samples.http', { status: sample.http_status })
  const code = sample.error_code || sample.attempt_errors?.at(-1)?.code
  if (!code) return t('admin.modelIntegrity.tests.samples.errorNoCode')
  const key = `admin.modelIntegrity.reason.${code}`
  const text = t(key)
  return text === key ? code : text
}

/**
 * Concrete reason a run has no verdict: the first failed sample's upstream
 * status and sanitised message, plus how many attempts it used. Empty when
 * the server stored no per-sample failure (older runs), so nothing is invented.
 */
export function runFailureDetail(t: Translate, run: OpenAIEvalRun): string {
  const sample = firstSampleFailure(run)
  if (!sample) return ''
  const parts = [sampleErrorHeadline(t, sample)]
  if (sample.error_message) parts.push(redactSecrets(sample.error_message))
  const failed = failedSampleCount(run)
  return t('admin.modelIntegrity.tests.samples.failureSummary', {
    count: failed,
    error: parts.join(t('admin.modelIntegrity.tests.samples.separator')),
    attempts: sample.attempts ?? sample.attempt_errors?.length ?? '—'
  })
}

type StateProbeWithSamples = NonNullable<OpenAIEvalRun['outcome']['state_probe']> & { samples?: OpenAIEvalSampleRecord[] }

/**
 * Per-request records shown in the detail dialog. State Probe stores its
 * two linked requests (mint, continue) on the run and inside its outcome;
 * some records only carry the latter.
 */
export function diagnosticSamples(run: OpenAIEvalRun): OpenAIEvalSampleRecord[] {
  if (run.test_type === 'state_probe' && !run.samples?.length) return (run.outcome.state_probe as StateProbeWithSamples | undefined)?.samples ?? []
  return runSamples(run)
}

/** "First request" / "Linked request" for State Probe records, null for anything else. */
export function stateProbeRequestName(t: Translate, sample: OpenAIEvalSampleRecord): string | null {
  if (sample.probe_id === 'state-probe-mint') return t('admin.modelIntegrity.tests.samples.stateProbe.mint')
  if (sample.probe_id === 'state-probe-continue') return t('admin.modelIntegrity.tests.samples.stateProbe.continue')
  return null
}

/**
 * Which of the two linked requests failed, with its upstream status and
 * sanitised message. The probe never retries, so no attempt count is given;
 * a record with zero attempts means nothing was sent (e.g. no OAuth token).
 */
function stateProbeFailureDetail(t: Translate, run: OpenAIEvalRun): string {
  const sample = diagnosticSamples(run).find(sampleFailed)
  if (!sample) return ''
  const parts: string[] = []
  if (sample.http_status && sample.http_status >= 400) parts.push(t('admin.modelIntegrity.tests.samples.http', { status: sample.http_status }))
  const message = sample.error_message || sample.error_code
  if (message) parts.push(redactSecrets(message))
  if (!parts.length) return ''
  const error = parts.join(t('admin.modelIntegrity.tests.samples.separator'))
  if (sample.attempts === 0) return t('admin.modelIntegrity.tests.samples.stateProbe.notSentFailure', { error })
  const request = stateProbeRequestName(t, sample) ?? t('admin.modelIntegrity.tests.samples.errorNoCode')
  return t('admin.modelIntegrity.tests.samples.stateProbe.failure', { request, error })
}
