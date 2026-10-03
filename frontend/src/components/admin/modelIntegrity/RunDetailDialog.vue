<template>
  <BaseDialog :show="run !== null" :title="t('admin.modelIntegrity.tests.detail.title')" width="normal" @close="emit('close')">
    <div v-if="run" class="space-y-4 text-sm">
      <div>
        <p class="font-medium text-gray-900 dark:text-white">{{ target }}</p>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t(`admin.modelIntegrity.tests.types.${run.test_type}.name`) }} · {{ formatTime(run.finished_at || run.started_at) }}</p>
      </div>
      <div class="flex items-start gap-2">
        <span class="tone" :class="`tone-${resultTone(run.status)}`" data-testid="detail-status">{{ runStatusLabel(t, run) }}</span>
        <p class="text-gray-700 dark:text-gray-300">{{ runExplanation(t, run, catalog) }}</p>
      </div>
      <p v-if="isAttributionRun(run)" class="attribution-note" data-testid="attribution-note">{{ t('admin.modelIntegrity.tests.detail.attributionNote') }}</p>

      <dl class="facts">
        <div><dt>{{ t('admin.modelIntegrity.tests.detail.logicalSamples') }}</dt><dd data-testid="detail-logical">{{ run.outcome.sample_count }}/{{ run.outcome.expected_count }}</dd></div>
        <div><dt>{{ t('admin.modelIntegrity.tests.detail.physicalRequests') }}</dt><dd data-testid="detail-physical">{{ run.request_count }}</dd></div>
        <div v-if="run.test_type === 'candy' && expectedAnswer !== null"><dt>{{ t('admin.modelIntegrity.tests.detail.expected') }}</dt><dd data-testid="detail-expected">{{ expectedAnswer }}</dd></div>
        <div><dt>{{ t('admin.modelIntegrity.tests.detail.tokens') }}</dt><dd>{{ run.input_tokens.toLocaleString() }} / {{ run.output_tokens.toLocaleString() }}</dd></div>
        <div><dt>{{ t('admin.modelIntegrity.tests.detail.duration') }}</dt><dd>{{ formatDuration(run.duration_ms) }}</dd></div>
        <div><dt>{{ t('admin.modelIntegrity.tests.detail.cost') }}</dt><dd>{{ run.cost_estimate_usd == null ? t('admin.modelIntegrity.tests.costUnknown') : `$${run.cost_estimate_usd.toFixed(4)}` }}</dd></div>
        <div v-if="run.upstream_model"><dt>{{ t('admin.modelIntegrity.tests.detail.upstreamModel') }}</dt><dd>{{ run.upstream_model }}</dd></div>
        <div v-if="run.outcome.fingerprint?.nearest_model"><dt>{{ t('admin.modelIntegrity.tests.detail.nearestModel') }}</dt><dd class="break-all">{{ run.outcome.fingerprint.nearest_model }}</dd></div>
        <div v-if="run.baseline_version"><dt>{{ t('admin.modelIntegrity.tests.detail.baseline') }}</dt><dd class="break-all">{{ run.baseline_version }}</dd></div>
      </dl>

      <p v-if="run.outcome.fingerprint && run.outcome.fingerprint.mean_jsd != null" class="text-xs text-gray-600 dark:text-gray-300">
        {{ t('admin.modelIntegrity.tests.detail.fingerprintMetric', { jsd: formatMetric(run.outcome.fingerprint.mean_jsd), p: formatMetric(run.outcome.fingerprint.p_value) }) }}
      </p>
      <p v-if="run.outcome.state_probe" class="text-xs text-gray-600 dark:text-gray-300">
        {{ t('admin.modelIntegrity.tests.detail.stateProbeMetric', { mint: run.outcome.state_probe.mint_status ?? '—', cont: run.outcome.state_probe.continue_status ?? '—', ticket: probeTicket }) }}
      </p>
      <div v-if="run.outcome.modeltrace?.candidates?.length">
        <p class="input-label">{{ t('admin.modelIntegrity.tests.detail.candidates') }}</p>
        <ul class="mt-1 space-y-1">
          <li v-for="candidate in run.outcome.modeltrace.candidates.slice(0, 5)" :key="candidate.model" class="flex items-center gap-3 text-xs">
            <span class="w-40 truncate text-gray-700 dark:text-gray-300">{{ candidate.display_name || candidate.model }}</span>
            <span class="h-1.5 flex-1 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700" aria-hidden="true"><span class="block h-full bg-primary-600" :style="{ width: `${Math.round(candidate.probability * 100)}%` }" /></span>
            <span class="w-10 text-right tabular-nums text-gray-500">{{ candidate.probability.toFixed(2) }}</span>
          </li>
        </ul>
      </div>

      <p v-if="run.test_type === 'candy' && historical" class="attribution-note" data-testid="detail-historical">{{ t('admin.modelIntegrity.tests.detail.historical', { version: run.data_version }) }}</p>
      <p v-else-if="run.test_type === 'candy' && expectedAnswer === null" class="attribution-note" data-testid="detail-historical">{{ t('admin.modelIntegrity.tests.detail.expectedUnknown') }}</p>

      <section v-if="run.status !== 'running'" data-testid="detail-samples">
        <p class="input-label">{{ t('admin.modelIntegrity.tests.samples.title') }}</p>
        <p v-if="!samples.length" class="mt-1 text-xs text-gray-500 dark:text-gray-400" data-testid="samples-none">{{ t('admin.modelIntegrity.tests.samples.none') }}</p>
        <ol v-else class="samples">
          <li v-for="item in visibleSamples" :key="item.index" class="sample" :class="`sample-${item.state}`" data-testid="sample">
            <details :open="item.state === 'error' && item.index === firstErrorIndex">
              <summary class="sample-summary">
                <span class="sample-index">{{ item.title }}</span>
                <span class="sample-state" data-testid="sample-state">{{ item.stateLabel }}</span>
                <span v-if="item.state === 'error'" class="sample-brief">{{ sampleErrorHeadline(t, item.sample) }}</span>
                <span v-else-if="isCandy && (item.extracted || !item.legacy)" class="sample-brief" data-testid="sample-extracted">
                  <template v-if="item.extracted">{{ t('admin.modelIntegrity.tests.samples.extractedAnswer') }} <strong class="tabular-nums">{{ item.extracted }}</strong></template>
                  <template v-else>{{ t('admin.modelIntegrity.tests.samples.noExtractedAnswer') }}</template>
                </span>
                <span v-else-if="item.answer" class="sample-brief">{{ item.answer }}</span>
                <span v-else-if="isProbe && item.sample.http_status" class="sample-brief">{{ t('admin.modelIntegrity.tests.samples.http', { status: item.sample.http_status }) }}</span>
                <!-- The probe's two linked requests never retry; a per-request attempt count would suggest otherwise. -->
                <span v-if="isProbe && item.sample.attempts === 0" class="sample-attempts" data-testid="sample-not-sent">{{ t('admin.modelIntegrity.tests.samples.stateProbe.notSent') }}</span>
                <span v-else-if="!isProbe && item.sample.attempts != null" class="sample-attempts">{{ t('admin.modelIntegrity.tests.samples.attempts', { count: item.sample.attempts }) }}</span>
              </summary>
              <div class="sample-body">
                <p v-if="item.legacy" class="text-gray-500 dark:text-gray-400" data-testid="sample-legacy">{{ t('admin.modelIntegrity.tests.samples.legacy') }}</p>
                <template v-else>
                  <p v-if="isProbe && item.state !== 'error' && !item.answer" class="text-gray-500 dark:text-gray-400" data-testid="sample-completed">{{ t('admin.modelIntegrity.tests.samples.stateProbe.completedNote') }}</p>
                  <!-- Candy: the full reply is an annotation, collapsed even when the sample itself is open. -->
                  <details v-else-if="isCandy && item.reply" class="sample-reply" data-testid="sample-reply">
                    <summary class="sample-reply-summary">{{ t('admin.modelIntegrity.tests.samples.fullModelReply') }}</summary>
                    <p class="sample-text sample-reply-text" data-testid="sample-answer">{{ item.reply }}</p>
                  </details>
                  <p v-else-if="isCandy && item.state !== 'error' && !item.extracted" class="text-gray-500 dark:text-gray-400" data-testid="sample-answer">{{ t('admin.modelIntegrity.tests.samples.noAnswer') }}</p>
                  <div v-else-if="!isCandy && (item.state !== 'error' || item.answer)">
                    <p class="sample-label">{{ t('admin.modelIntegrity.tests.samples.answer') }}</p>
                    <p class="sample-text" data-testid="sample-answer">{{ item.answer || t('admin.modelIntegrity.tests.samples.noAnswer') }}</p>
                  </div>
                  <div v-if="item.message">
                    <p class="sample-label">{{ t('admin.modelIntegrity.tests.samples.error') }}<template v-if="item.sample.http_status"> · HTTP {{ item.sample.http_status }}</template></p>
                    <p class="sample-text sample-error" data-testid="sample-error">{{ item.message }}</p>
                  </div>
                  <div v-if="!isProbe && item.sample.attempt_errors?.length">
                    <p class="sample-label">{{ t('admin.modelIntegrity.tests.samples.attemptLog') }}</p>
                    <ul class="sample-attempt-log">
                      <li v-for="attempt in item.sample.attempt_errors" :key="attempt.attempt" data-testid="sample-attempt">
                        <span class="whitespace-nowrap font-medium">{{ t('admin.modelIntegrity.tests.samples.attempt', { attempt: attempt.attempt }) }}</span>
                        <span v-if="attempt.http_status" class="whitespace-nowrap tabular-nums">HTTP {{ attempt.http_status }}</span>
                        <span class="sample-text">{{ redactSecrets(attempt.message || attempt.code) }}</span>
                      </li>
                    </ul>
                  </div>
                </template>
              </div>
            </details>
          </li>
        </ol>
        <button v-if="samples.length > visibleSamples.length" type="button" class="mt-2 text-xs font-medium text-primary-700 hover:underline dark:text-primary-300" @click="showAll = true">
          {{ t('admin.modelIntegrity.tests.samples.showAll', { count: samples.length }) }}
        </button>
      </section>

      <details v-if="run.error || run.outcome.reason" class="text-xs text-gray-500 dark:text-gray-400">
        <summary class="cursor-pointer">{{ t('admin.modelIntegrity.tests.detail.technical') }}</summary>
        <code class="mt-2 block whitespace-pre-wrap break-all font-mono">{{ [run.outcome.reason, run.error].filter(Boolean).join('\n') }}</code>
      </details>
    </div>
    <template #footer>
      <div class="flex justify-end">
        <button type="button" class="btn btn-secondary" @click="emit('close')">{{ t('admin.modelIntegrity.common.close') }}</button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import type { OpenAIEvalModelCatalog, OpenAIEvalRun } from '@/api/admin/accounts'
import { candyExpectedAnswer, candyExtractedAnswer, candyFullReply, isAttributionRun, isHistoricalDataVersion, redactSecrets, resultTone, sampleAnswer, sampleLacksDetail, sampleState, type EvalTestType } from '@/views/admin/modelIntegrity/modelIntegrity'
import { diagnosticSamples, runExplanation, runStatusLabel, sampleErrorHeadline, stateProbeRequestName } from '@/views/admin/modelIntegrity/runText'

const props = defineProps<{
  run: OpenAIEvalRun | null
  target: string
  catalog: OpenAIEvalModelCatalog | null
}>()
const emit = defineEmits<{ (e: 'close'): void }>()
const { t } = useI18n()

/** Fingerprint runs hold hundreds of samples; show a readable slice first. */
const SAMPLE_PREVIEW = 20
const showAll = ref(false)
watch(() => props.run?.id, () => { showAll.value = false })

const expectedAnswer = computed(() => (props.run ? candyExpectedAnswer(props.run, props.catalog) : null))
const historical = computed(() => Boolean(props.run) && isHistoricalDataVersion(props.run!, props.catalog))
const isProbe = computed(() => props.run?.test_type === 'state_probe')
const isCandy = computed(() => props.run?.test_type === 'candy')
/** Only a probe that reached a verdict may say whether the route changed; a failed one says nothing. */
const probeTicket = computed(() => {
  const probe = props.run?.outcome.state_probe
  if (!probe || probe.failure || (probe.verdict !== 'healthy' && probe.verdict !== 'degraded')) return t('admin.modelIntegrity.tests.detail.ticketUnknown')
  return probe.new_ticket ? t('admin.modelIntegrity.tests.detail.newTicket') : t('admin.modelIntegrity.tests.detail.sameTicket')
})
// Every string below is rendered with text interpolation (escaped); never v-html.
const samples = computed(() => {
  const run = props.run
  if (!run) return []
  return diagnosticSamples(run).map((sample, index) => {
    const state = sampleState(sample, run.test_type as EvalTestType)
    const probe = run.test_type === 'state_probe'
    return {
      index,
      sample,
      state,
      title: (probe && stateProbeRequestName(t, sample)) || t('admin.modelIntegrity.tests.samples.index', { n: index + 1 }),
      stateLabel: probe && state === 'valid' ? t('admin.modelIntegrity.tests.samples.stateProbe.completed') : t(`admin.modelIntegrity.tests.samples.state.${state}`),
      answer: sampleAnswer(sample),
      extracted: candyExtractedAnswer(sample, expectedAnswer.value),
      reply: redactSecrets(candyFullReply(sample)),
      message: sample.error_message ? redactSecrets(sample.error_message) : '',
      legacy: sampleLacksDetail(sample)
    }
  })
})
const firstErrorIndex = computed(() => samples.value.find(item => item.state === 'error')?.index ?? -1)
const visibleSamples = computed(() => {
  if (showAll.value || samples.value.length <= SAMPLE_PREVIEW) return samples.value
  // Keep every failure visible even when the list is shortened.
  return samples.value.filter((item, position) => position < SAMPLE_PREVIEW || item.state === 'error')
})

const formatMetric = (value?: number | null) => (value == null ? '—' : value.toFixed(4))
const formatDuration = (ms: number) => (ms >= 60_000 ? `${(ms / 60_000).toFixed(1)} min` : `${(ms / 1000).toFixed(1)} s`)
const formatTime = (value: string) => {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}
</script>

<style scoped>
.facts { @apply grid grid-cols-2 gap-x-6 gap-y-2 rounded-lg bg-gray-50 p-3 text-xs dark:bg-dark-900/50; }
.facts dt { @apply text-gray-500 dark:text-gray-400; }
.facts dd { @apply font-medium tabular-nums text-gray-800 dark:text-gray-200; }
.tone { @apply inline-flex shrink-0 whitespace-nowrap rounded px-1.5 py-0.5 text-xs font-medium; }
.tone-ok { @apply bg-emerald-50 text-emerald-800 dark:bg-emerald-950/40 dark:text-emerald-300; }
.tone-likely { @apply border border-dashed border-emerald-400 text-emerald-800 dark:border-emerald-600 dark:text-emerald-300; }
.attribution-note { @apply border-l-2 border-gray-300 pl-3 text-xs leading-relaxed text-gray-600 dark:border-dark-500 dark:text-gray-400; }
.tone-attention { @apply bg-amber-100 text-amber-900 dark:bg-amber-900/40 dark:text-amber-200; }
.tone-neutral { @apply bg-gray-100 text-gray-700 dark:bg-dark-700 dark:text-gray-300; }
.tone-error { @apply bg-rose-50 text-rose-800 dark:bg-rose-950/40 dark:text-rose-300; }
.tone-running { @apply bg-sky-50 text-sky-800 dark:bg-sky-950/40 dark:text-sky-300; }
.samples { @apply mt-1 divide-y divide-gray-100 rounded-lg border border-gray-200 text-xs dark:divide-dark-700 dark:border-dark-700; }
.sample { @apply min-w-0; }
.sample-summary { @apply flex cursor-pointer flex-wrap items-baseline gap-x-3 gap-y-1 px-3 py-2 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary-500; }
.sample-index { @apply whitespace-nowrap tabular-nums text-gray-500 dark:text-gray-400; }
.sample-state { @apply whitespace-nowrap font-medium text-gray-800 dark:text-gray-200; }
.sample-correct .sample-state, .sample-valid .sample-state { @apply text-emerald-700 dark:text-emerald-300; }
.sample-wrong .sample-state, .sample-invalid .sample-state { @apply text-amber-800 dark:text-amber-300; }
.sample-error .sample-state { @apply text-rose-700 dark:text-rose-300; }
.sample-brief { @apply min-w-0 flex-1 truncate text-gray-600 dark:text-gray-300; }
.sample-attempts { @apply ml-auto whitespace-nowrap tabular-nums text-gray-500 dark:text-gray-400; }
.sample-body { @apply space-y-2 px-3 pb-3; }
.sample-label { @apply mb-0.5 text-gray-500 dark:text-gray-400; }
/* Raw model text and upstream messages can be long and unbroken; wrap anywhere and cap the height. */
.sample-text { @apply max-h-48 min-w-0 overflow-y-auto whitespace-pre-wrap break-words text-gray-800 [overflow-wrap:anywhere] dark:text-gray-200; }
.sample-error { @apply text-rose-800 dark:text-rose-300; }
.sample-reply-summary { @apply inline-flex cursor-pointer text-gray-500 underline-offset-2 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:text-gray-400; }
.sample-reply-text { @apply mt-1 max-h-72; }
.sample-attempt-log { @apply space-y-1; }
.sample-attempt-log li { @apply flex flex-wrap items-baseline gap-x-2; }
</style>
