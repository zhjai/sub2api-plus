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
    // The first request completed but carried no ticket: inconclusive evidence, not a failed request.
    if (stateProbeMintTicketMissing(run)) return t('admin.modelIntegrity.reason.stateProbe.mintMissingTicket')
    if (failure) {
      const base = reasonText(t, failure, run, catalog)
      const detail = stateProbeFailureDetail(t, run)
      const text = detail ? joinSentences(base, detail) : base
      return stateProbeLegacyLinkedMissingTicket(run) ? joinSentences(text, t('admin.modelIntegrity.reason.stateProbe.legacyLinkedMissingTicket')) : text
    }
    const observation = stateProbeTicketObservation(run.outcome.state_probe)
    if (observation) return t(`admin.modelIntegrity.reason.stateProbe.ticket.${observation}`)
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
 * Per-request records shown in the detail dialog. State Probe stores the
 * linked requests (mint, continue) of every chain it sent on the run and
 * inside its outcome; some records only carry the latter.
 */
export function diagnosticSamples(run: OpenAIEvalRun): OpenAIEvalSampleRecord[] {
  if (run.test_type === 'state_probe' && !run.samples?.length) return (run.outcome.state_probe as StateProbeWithSamples | undefined)?.samples ?? []
  return runSamples(run)
}

/**
 * Step and chain of a State Probe record. Retrying servers number each chain
 * ("state-probe-2-mint"); older ones sent a single unnumbered chain.
 */
export function stateProbeRecordStep(sample: OpenAIEvalSampleRecord): { step: 'mint' | 'continue'; chain: number } | null {
  const match = /^state-probe-(?:(\d+)-)?(mint|continue)$/.exec(sample.probe_id ?? '')
  if (!match) return null
  return { step: match[2] as 'mint' | 'continue', chain: match[1] ? Number(match[1]) : 1 }
}

type StateProbe = NonNullable<OpenAIEvalRun['outcome']['state_probe']>

/**
 * v2 and later read a completed linked request without a ticket as valid
 * (the upstream kept the old one) instead of failing it. Records without a
 * version predate that.
 */
export function isStateProbeV2(probe: StateProbe | undefined): boolean {
  const match = /^codex-turn-state-v(\d+)$/.exec(probe?.version ?? '')
  return Boolean(match) && Number(match![1]) >= 2
}

/**
 * What the linked request of the last chain returned, only when the record
 * says so: v2 omits a zero continue_ticket_length, so its absence means no
 * ticket was issued. Older records without lengths say nothing either way.
 */
export function stateProbeTicketObservation(probe: StateProbe | undefined): 'none' | 'same' | 'different' | null {
  if (!probe || probe.failure || (probe.verdict !== 'healthy' && probe.verdict !== 'degraded')) return null
  const length = Number(probe.continue_ticket_length) || 0
  if (!isStateProbeV2(probe) && length <= 0) return null
  if (length <= 0) return 'none'
  return probe.new_ticket ? 'different' : 'same'
}

/**
 * Step of the last chain that failed: the server's last_failure_step, or the
 * last failed record's step on records that predate it.
 */
export function stateProbeFailureStep(run: OpenAIEvalRun): 'mint' | 'continue' | null {
  const probe = run.outcome.state_probe
  if (!probe?.failure) return null
  if (probe.last_failure_step === 'mint' || probe.last_failure_step === 'continue') return probe.last_failure_step
  const sample = diagnosticSamples(run).filter(sampleFailed).at(-1)
  return sample ? stateProbeRecordStep(sample)?.step ?? null : null
}

/** A first request that completed (HTTP 200) without returning a ticket, so the chain could not continue. */
export function stateProbeSampleTicketMissing(sample: OpenAIEvalSampleRecord): boolean {
  return sample.error_code === 'missing_ticket' && sample.http_status === 200 && stateProbeRecordStep(sample)?.step === 'mint'
}

/** The probe stopped because the first request of its last chain completed without a ticket. */
export function stateProbeMintTicketMissing(run: OpenAIEvalRun): boolean {
  return run.outcome.state_probe?.failure === 'missing_ticket' && stateProbeFailureStep(run) === 'mint'
}

/**
 * Before v2 a linked request that completed without a new ticket was failed
 * as missing_ticket; v2 reads it as valid. The stored result is kept as is,
 * with a note to run the probe again.
 */
export function stateProbeLegacyLinkedMissingTicket(run: OpenAIEvalRun): boolean {
  const probe = run.outcome.state_probe
  return probe?.failure === 'missing_ticket' && !isStateProbeV2(probe) && stateProbeFailureStep(run) === 'continue'
}

/** A failed State Probe request; a completed first request without a ticket is inconclusive, not failed. */
export function stateProbeSampleFailed(sample: OpenAIEvalSampleRecord): boolean {
  return sampleFailed(sample) && !stateProbeSampleTicketMissing(sample)
}

/** Chains the run actually sent, read from its records when the outcome omits the count. */
export function stateProbeChainCount(run: OpenAIEvalRun): number {
  const reported = Number(run.outcome.state_probe?.attempts)
  if (Number.isFinite(reported) && reported > 0) return reported
  return Math.max(1, ...diagnosticSamples(run).map(sample => stateProbeRecordStep(sample)?.chain ?? 1))
}

/**
 * "First request" / "Linked request" for State Probe records, null for
 * anything else. When the run sent more than one chain, the chain is named
 * too, so a retried mint is not mistaken for the first one.
 */
export function stateProbeRequestName(t: Translate, sample: OpenAIEvalSampleRecord, chains = 1): string | null {
  const record = stateProbeRecordStep(sample)
  if (!record) return null
  const request = t(`admin.modelIntegrity.tests.samples.stateProbe.${record.step}`)
  return chains > 1 ? t('admin.modelIntegrity.tests.samples.stateProbe.inChain', { chain: record.chain, request }) : request
}

/**
 * Which request of the chain failed, with its upstream status and sanitised
 * message. A failed chain is retried as a whole rather than re-sending the
 * failed request, so every record shows a single send and its attempt count
 * says nothing; a record with zero attempts means nothing was sent (e.g. no
 * OAuth token). The last failed record is the one the verdict reports: an
 * earlier chain's failure was already superseded by the retry.
 */
function stateProbeFailureDetail(t: Translate, run: OpenAIEvalRun): string {
  const sample = diagnosticSamples(run).filter(stateProbeSampleFailed).at(-1)
  if (!sample) return ''
  const parts: string[] = []
  if (sample.http_status && sample.http_status >= 400) parts.push(t('admin.modelIntegrity.tests.samples.http', { status: sample.http_status }))
  const message = sample.error_message || sample.error_code
  if (message) parts.push(redactSecrets(message))
  if (!parts.length) return ''
  const error = parts.join(t('admin.modelIntegrity.tests.samples.separator'))
  if (sample.attempts === 0) return t('admin.modelIntegrity.tests.samples.stateProbe.notSentFailure', { error })
  const request = stateProbeRequestName(t, sample, stateProbeChainCount(run)) ?? t('admin.modelIntegrity.tests.samples.errorNoCode')
  return t('admin.modelIntegrity.tests.samples.stateProbe.failure', { request, error })
}
