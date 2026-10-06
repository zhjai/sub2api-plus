<template>
  <article class="tt" :class="{ 'tt-unavailable': !available }" :data-testid="`test-${type}`">
    <header class="tt-head">
      <div class="min-w-0">
        <h3 class="tt-name">{{ t(`admin.modelIntegrity.tests.types.${type}.name`) }}</h3>
        <p class="tt-what">{{ t(`admin.modelIntegrity.tests.types.${type}.what`, { count: perRun, candidates: catalog?.modeltrace?.candidate_count ?? '—' }) }}</p>
      </div>
      <button
        type="button"
        class="btn btn-secondary btn-sm shrink-0"
        :disabled="!available || running"
        :data-testid="`run-${type}`"
        @click="emit('run')"
      >
        <Icon name="play" size="xs" :class="running ? 'motion-safe:animate-pulse' : ''" />
        {{ running ? t('admin.modelIntegrity.tests.running') : t('admin.modelIntegrity.tests.runNow') }}
      </button>
    </header>

    <p v-if="!available" class="tt-unavailable-note">{{ t('admin.modelIntegrity.tests.onlyDirectOAuth') }}</p>

    <template v-else>
      <div class="tt-controls">
        <label class="tt-switch">
          <Toggle v-model="schedule.enabled" :aria-label="`${t(`admin.modelIntegrity.tests.types.${type}.name`)} ${t('admin.modelIntegrity.tests.auto')}`" />
          <span>{{ t('admin.modelIntegrity.tests.auto') }}</span>
        </label>
        <label class="tt-field">
          <span class="tt-label">{{ t('admin.modelIntegrity.tests.every') }}</span>
          <select v-model="intervalChoice" class="input tt-input" :disabled="!schedule.enabled">
            <option v-for="seconds in intervals" :key="seconds" :value="seconds">{{ intervalLabel(seconds) }}</option>
            <option :value="CUSTOM_INTERVAL">{{ t('admin.modelIntegrity.tests.interval.customOption') }}</option>
          </select>
        </label>
        <label v-if="customIntervalSelected" class="tt-field">
          <span class="tt-label">{{ t('admin.modelIntegrity.tests.interval.customMinutes', { max: MAX_INTERVAL_MINUTES.toLocaleString() }) }}</span>
          <input
            v-model.number="customIntervalMinutes"
            type="number"
            min="5"
            :max="MAX_INTERVAL_MINUTES"
            step="1"
            class="input tt-input tt-custom-interval"
            data-testid="custom-interval"
            :disabled="!schedule.enabled"
            @change="normalize"
          />
        </label>
        <label v-if="type === 'candy'" class="tt-field">
          <span class="tt-label">{{ t('admin.modelIntegrity.tests.candySamples') }}</span>
          <input
            v-model.number="candySampleCount"
            type="number"
            min="1"
            max="10"
            step="1"
            class="input tt-input tt-samples"
            data-testid="candy-sample-count"
            :title="t('admin.modelIntegrity.tests.candySamplesHint')"
            @change="normalize"
          />
        </label>
        <label v-if="type === 'fingerprint'" class="tt-field">
          <span class="tt-label">{{ t('admin.modelIntegrity.tests.sampleMode') }}</span>
          <select v-model="schedule.sample_mode" class="input tt-input" data-testid="fingerprint-sample-mode">
            <option v-if="schedule.sample_mode && !modes.some(mode => mode.id === schedule.sample_mode)" :value="schedule.sample_mode">{{ t('admin.modelIntegrity.tests.sampleModes.custom', { mode: schedule.sample_mode }) }}</option>
            <option v-for="mode in modes" :key="mode.id" :value="mode.id">{{ t('admin.modelIntegrity.tests.manualSampleOption', { mode: modeLabel(mode.id), count: mode.samples }) }}</option>
          </select>
        </label>
        <label class="tt-field">
          <span class="tt-label">{{ t('admin.modelIntegrity.tests.jitter') }}</span>
          <input
            v-model.number="jitterMinutes"
            type="number"
            min="0"
            :max="maxJitterMinutes"
            class="input tt-input tt-jitter"
            :disabled="!schedule.enabled || maxJitterMinutes === 0"
            :title="t('admin.modelIntegrity.tests.jitterHint', { max: maxJitterMinutes })"
          />
        </label>
      </div>

      <div v-if="running" class="tt-progress" role="status" :aria-label="progressText" data-testid="test-progress">
        <div class="tt-progress-head">
          <span>{{ progressText }}</span>
          <span v-if="progressPercent !== null" class="tabular-nums">{{ progressPercent }}%</span>
        </div>
        <div class="tt-progress-track" aria-hidden="true">
          <span v-if="progressPercent !== null" class="tt-progress-value" :style="{ width: `${progressPercent}%` }" />
          <span v-else class="tt-progress-value tt-progress-indeterminate" />
        </div>
        <p v-if="physicalRequests !== null" class="tt-progress-requests" data-testid="test-progress-requests">{{ t('admin.modelIntegrity.tests.progress.requests', { count: physicalRequests }) }}</p>
      </div>

      <p class="tt-cost">
        <span data-testid="per-run">{{ perRunText }}</span>
        <span :class="schedule.enabled ? 'tt-cost-strong' : ''">{{ schedule.enabled ? t('admin.modelIntegrity.tests.perDay', { count: formatDaily(perDay) }) : t('admin.modelIntegrity.tests.perDayOff') }}</span>
        <span v-if="schedule.enabled && schedule.next_run_at">{{ t('admin.modelIntegrity.tests.nextRun', { time: formatTime(schedule.next_run_at) }) }}</span>
      </p>
      <p v-if="type === 'state_probe' && route.reasoning_effort" class="tt-link-note text-gray-500 dark:text-gray-400" data-testid="state-probe-effort">{{ t('admin.modelIntegrity.tests.stateProbeDefaultEffort') }}</p>
      <p v-if="type === 'state_probe'" class="tt-link-note">
        <router-link to="/admin/model-integrity/scheduling" class="tt-link">{{ t('admin.modelIntegrity.tests.goScheduling') }}</router-link>
      </p>
    </template>

    <footer class="tt-result" :class="latest ? `tt-result-${resultTone(latest.status)}` : ''">
      <template v-if="latest">
        <button type="button" class="tt-result-btn" @click="emit('open', latest)">
          <span class="tt-result-status" data-testid="latest-status">{{ statusText }}</span>
          <span class="tt-result-text" data-testid="latest-explanation">{{ explanation }}</span>
          <time class="tt-result-time" :datetime="latest.finished_at || latest.started_at">{{ formatTime(latest.finished_at || latest.started_at) }}</time>
        </button>
        <div v-if="type === 'candy' && (expectedAnswer !== null || candySamples.length)" class="tt-answers" data-testid="latest-answers">
          <p v-if="expectedAnswer !== null" class="tt-answers-expected">
            {{ t('admin.modelIntegrity.tests.detail.expected') }}
            <strong class="tabular-nums">{{ expectedAnswer }}</strong>
          </p>
          <ul v-if="candySamples.length" class="tt-answer-list">
            <li v-for="sample in candySamples" :key="sample.index" class="tt-answer-row" data-testid="latest-answer">
              <span class="tt-answer-value" data-testid="latest-answer-value">
                <template v-if="candySamples.length > 1">{{ t('admin.modelIntegrity.tests.samples.index', { n: sample.index + 1 }) }}{{ t('admin.modelIntegrity.tests.samples.labelSeparator') }}</template>
                <template v-if="sample.failed">{{ t('admin.modelIntegrity.tests.samples.state.error') }}</template>
                <template v-else-if="sample.extracted">{{ t('admin.modelIntegrity.tests.samples.extractedAnswer') }} <strong class="tabular-nums">{{ sample.extracted }}</strong></template>
                <template v-else>{{ t('admin.modelIntegrity.tests.samples.noExtractedAnswer') }}</template>
              </span>
              <details v-if="sample.reply" class="tt-answer-reply">
                <summary class="tt-answer-summary">{{ t('admin.modelIntegrity.tests.samples.fullModelReply') }}</summary>
                <p class="tt-answer-text" data-testid="latest-answer-reply">{{ redactSecrets(sample.reply) }}</p>
              </details>
            </li>
          </ul>
        </div>
      </template>
      <span v-else class="tt-never">{{ t('admin.modelIntegrity.tests.neverRun') }}</span>
    </footer>
  </article>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import Toggle from '@/components/common/Toggle.vue'
import type { OpenAIEvalModelCatalog, OpenAIEvalRouteConfig, OpenAIEvalRun } from '@/api/admin/accounts'
import { CANDY_MAX_SAMPLES, CANDY_MIN_SAMPLES, CUSTOM_INTERVAL, DAY, DEFAULT_MAX_REQUEST_ATTEMPTS, HOUR, MAX_INTERVAL_MINUTES, TEST_TYPE_META, candyExpectedAnswer, candyExtractedAnswer, candyFullReply, dailyRequests, maxRequestsPerRun, maxScheduleJitterSeconds, normalizeSchedule, redactSecrets, requestsPerRun, resultTone, runSamples, sampleFailed, sampleLacksDetail, scheduleOf, stateProbeChains, type EvalTestType } from '@/views/admin/modelIntegrity/modelIntegrity'
import { runExplanation, runStatusLabel } from '@/views/admin/modelIntegrity/runText'

const props = defineProps<{
  route: OpenAIEvalRouteConfig
  type: EvalTestType
  catalog: OpenAIEvalModelCatalog | null
  latest?: OpenAIEvalRun
  progress?: OpenAIEvalRun
  running: boolean
  available: boolean
  /**
   * Shared attempts-per-sample setting. State Probe reads it too, but one
   * attempt there is a whole mint/continue chain, so the server caps it at
   * three chains — the panel shows that capped ceiling, never 2 × the value.
   */
  maxAttempts?: number
}>()

const emit = defineEmits<{ (e: 'run'): void; (e: 'open', run: OpenAIEvalRun): void }>()
const { t } = useI18n()

const schedule = computed(() => scheduleOf(props.route, props.type))
const intervals = computed(() => TEST_TYPE_META[props.type].intervals)
const modes = computed(() => props.catalog?.fingerprint_modes?.length ? props.catalog.fingerprint_modes : [{ id: 'quick', samples: 60 }, { id: 'standard', samples: 200 }, { id: 'strict', samples: 400 }])
const perRun = computed(() => requestsPerRun(props.route, props.type, props.catalog))
const perRunMax = computed(() => maxRequestsPerRun(props.route, props.type, props.maxAttempts ?? DEFAULT_MAX_REQUEST_ATTEMPTS, props.catalog))
const perRunText = computed(() => {
  if (props.type === 'state_probe') {
    const chains = stateProbeChains(props.maxAttempts ?? DEFAULT_MAX_REQUEST_ATTEMPTS)
    if (chains < 2) return t('admin.modelIntegrity.tests.perRun', { count: perRun.value })
    return t('admin.modelIntegrity.tests.perRunRetryChains', { count: perRun.value, chains, max: perRunMax.value })
  }
  if (perRunMax.value > perRun.value) return t('admin.modelIntegrity.tests.perRunRetry', { count: perRun.value, max: perRunMax.value })
  return t('admin.modelIntegrity.tests.perRun', { count: perRun.value })
})
const perDay = computed(() => dailyRequests(props.route, props.type, props.catalog))
const maxJitterMinutes = computed(() => Math.floor(maxScheduleJitterSeconds(schedule.value.interval_seconds) / 60))
const customIntervalSelected = ref(!intervals.value.includes(schedule.value.interval_seconds))

watch(() => `${props.route.account_id}:${props.route.requested_model}:${props.route.reasoning_effort}:${props.type}`, () => {
  customIntervalSelected.value = !intervals.value.includes(schedule.value.interval_seconds)
})

const intervalChoice = computed<string | number>({
  get: () => customIntervalSelected.value ? CUSTOM_INTERVAL : schedule.value.interval_seconds,
  set: value => {
    if (value === CUSTOM_INTERVAL) {
      customIntervalSelected.value = true
      return
    }
    customIntervalSelected.value = false
    schedule.value.interval_seconds = Number(value)
    normalize()
  }
})

const customIntervalMinutes = computed({
  get: () => Math.max(5, Math.round((schedule.value.interval_seconds || TEST_TYPE_META[props.type].minInterval) / 60)),
  set: (value: number) => {
    const minutes = Number.isFinite(value) ? Math.min(Math.max(5, Math.trunc(value)), MAX_INTERVAL_MINUTES) : 5
    schedule.value.interval_seconds = minutes * 60
  }
})

const candySampleCount = computed({
  get: () => Math.min(CANDY_MAX_SAMPLES, Math.max(CANDY_MIN_SAMPLES, Math.trunc(Number(schedule.value.sample_count) || CANDY_MIN_SAMPLES))),
  set: (value: number) => {
    const samples = Number.isFinite(value) ? Math.min(CANDY_MAX_SAMPLES, Math.max(CANDY_MIN_SAMPLES, Math.trunc(value))) : CANDY_MIN_SAMPLES
    schedule.value.sample_count = samples
  }
})

const jitterMinutes = computed({
  get: () => Math.round((schedule.value.jitter_seconds || 0) / 60),
  set: (value: number) => {
    const minutes = Number.isFinite(value) ? Math.min(Math.max(Math.trunc(value), 0), maxJitterMinutes.value) : 0
    schedule.value.jitter_seconds = minutes * 60
  }
})

const explanation = computed(() => (props.latest ? runExplanation(t, props.latest, props.catalog) : ''))
const statusText = computed(() => (props.latest ? runStatusLabel(t, props.latest) : ''))
const expectedAnswer = computed(() => (props.latest && props.type === 'candy' ? candyExpectedAnswer(props.latest, props.catalog) : null))
const candySamples = computed(() => {
  if (!props.latest || props.type !== 'candy') return []
  return runSamples(props.latest)
    .map((sample, index) => ({
      index,
      failed: sampleFailed(sample),
      extracted: candyExtractedAnswer(sample, expectedAnswer.value),
      reply: candyFullReply(sample),
      legacy: sampleLacksDetail(sample)
    }))
    // Records from before answers were stored have nothing to show, not a failed extraction.
    .filter(sample => sample.extracted || !sample.legacy)
})
const progressDone = computed(() => props.progress?.completed_samples ?? props.progress?.outcome.sample_count ?? 0)
const progressTotal = computed(() => props.progress?.expected_samples ?? props.progress?.outcome.expected_count ?? 0)
/** Upstream requests so far, shown only once retries make it differ from the sample count. */
const physicalRequests = computed(() => {
  const count = props.progress?.request_count
  return count != null && count > progressDone.value ? count : null
})
const progressPercent = computed(() => progressTotal.value > 0
  ? Math.min(100, Math.max(0, Math.round(progressDone.value * 100 / progressTotal.value)))
  : null)
const progressText = computed(() => progressTotal.value > 0
  ? t('admin.modelIntegrity.tests.progress.samples', { done: progressDone.value, total: progressTotal.value })
  : t('admin.modelIntegrity.tests.progress.starting'))

function normalize() {
  normalizeSchedule(schedule.value, props.type)
}

function modeLabel(id: string) {
  const key = `admin.modelIntegrity.tests.sampleModes.${id}`
  const text = t(key)
  return text === key ? id : text
}

function intervalLabel(seconds: number) {
  const map: Record<number, string> = { 300: 'm5', 600: 'm10', 1800: 'm30', [HOUR]: 'h1', [6 * HOUR]: 'h6', [12 * HOUR]: 'h12', [DAY]: 'h24', [3 * DAY]: 'd3', [7 * DAY]: 'd7' }
  return map[seconds] ? t(`admin.modelIntegrity.tests.interval.${map[seconds]}`) : humanInterval(seconds)
}

function humanInterval(seconds: number) {
  if (seconds >= DAY) return `${Number((seconds / DAY).toFixed(1))} d`
  if (seconds >= HOUR) return `${Number((seconds / HOUR).toFixed(1))} h`
  return `${Math.round(seconds / 60)} min`
}

const formatDaily = (value: number) => (value >= 10 ? Math.round(value).toLocaleString() : Number(value.toFixed(1)).toString())
const formatTime = (value: string) => {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString([], { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false })
}
</script>

<style scoped>
.tt { @apply flex flex-col gap-3 py-4; }
.tt-head { @apply flex items-start justify-between gap-3; }
.tt-name { @apply flex flex-wrap items-center gap-2 text-sm font-semibold text-gray-900 dark:text-white; }
.tt-what { @apply mt-1 max-w-[62ch] text-[0.8125rem] leading-relaxed text-gray-600 dark:text-gray-400; }
.tt-unavailable .tt-name { @apply text-gray-500 dark:text-gray-400; }
.tt-unavailable-note { @apply text-xs text-gray-500 dark:text-gray-400; }
.tt-controls { @apply flex flex-wrap items-end gap-3; }
.tt-switch { @apply flex h-9 cursor-pointer items-center gap-2 pr-2 text-sm text-gray-700 dark:text-gray-300; }
.tt-field { @apply flex flex-col gap-1; }
.tt-label { @apply text-xs text-gray-500 dark:text-gray-400; }
.tt-input { @apply h-9 w-auto min-w-[8.5rem] py-1 text-sm; }
.tt-custom-interval { @apply min-w-0 w-32; }
.tt-samples { @apply min-w-0 w-24; }
.tt-jitter { @apply min-w-0 w-24; }
.tt-cost { @apply flex flex-wrap gap-x-4 gap-y-1 text-xs tabular-nums text-gray-500 dark:text-gray-400; }
.tt-cost-strong { @apply font-medium text-gray-800 dark:text-gray-200; }
.tt-link-note { @apply text-xs; }
.tt-link { @apply font-medium text-primary-700 underline-offset-2 hover:underline dark:text-primary-300; }
.tt-progress { @apply rounded-md bg-gray-50 px-3 py-2.5 dark:bg-dark-800/70; }
.tt-progress-head { @apply mb-2 flex items-center justify-between gap-3 text-xs font-medium text-gray-700 dark:text-gray-300; }
.tt-progress-track { @apply relative h-2 overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600; }
.tt-progress-value { @apply block h-full rounded-full bg-primary-500 transition-[width] duration-300; }
.tt-progress-indeterminate { width: 35%; animation: progress-slide 1.2s ease-in-out infinite; }
.tt-result { @apply rounded-md border border-gray-200 bg-gray-50 text-xs dark:border-dark-700 dark:bg-dark-800/60; }
.tt-result-ok { @apply border-emerald-200 bg-emerald-50 dark:border-emerald-900/60 dark:bg-emerald-950/25; }
.tt-result-likely { @apply border-dashed border-emerald-300 bg-white dark:border-emerald-800 dark:bg-dark-800/40; }
.tt-result-likely .tt-result-status { @apply text-emerald-800 dark:text-emerald-300; }
.tt-result-attention { @apply border-amber-200 bg-amber-50 dark:border-amber-900/60 dark:bg-amber-950/25; }
.tt-result-error { @apply border-rose-200 bg-rose-50 dark:border-rose-900/60 dark:bg-rose-950/25; }
.tt-result-error .tt-result-status { @apply text-rose-800 dark:text-rose-300; }
.tt-progress-requests { @apply mt-1.5 text-xs tabular-nums text-gray-500 dark:text-gray-400; }
.tt-answers { @apply space-y-2 border-t border-black/5 px-3 pb-3 pt-2 text-gray-700 dark:border-white/10 dark:text-gray-300; }
.tt-answers-expected { @apply tabular-nums; }
.tt-answer-list { @apply space-y-1.5; }
.tt-answer-row { @apply min-w-0; }
.tt-answer-value { @apply tabular-nums; }
.tt-answer-reply { @apply mt-0.5 text-xs; }
.tt-answer-summary { @apply inline-flex cursor-pointer items-center gap-1 text-gray-500 underline-offset-2 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:text-gray-400; }
/* The whole stored reply stays reachable; the box scrolls instead of the card growing. */
.tt-answer-text { @apply mt-1 max-h-40 overflow-y-auto whitespace-pre-wrap break-words rounded border border-gray-200 bg-white/70 px-2 py-1.5 text-gray-800 [overflow-wrap:anywhere] dark:border-dark-600 dark:bg-dark-900/50 dark:text-gray-200; }
/* Narrow screens: status beside the explanation, time on its own line, so the text keeps a readable width. */
.tt-result-btn { @apply grid w-full grid-cols-[auto_minmax(0,1fr)] items-start gap-x-3 gap-y-1 rounded-md px-3 py-3 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 sm:grid-cols-[auto_minmax(0,1fr)_auto] sm:gap-y-3; }
.tt-result-status { @apply text-sm font-semibold text-gray-900 dark:text-white; min-width: 4rem; }
.tt-result-text { @apply min-w-0 flex-1 leading-relaxed text-gray-600 dark:text-gray-300; }
.tt-result-time { @apply col-start-2 shrink-0 tabular-nums text-gray-400 sm:col-start-auto; }
.tt-never { @apply text-gray-400; }
.tone { @apply inline-flex shrink-0 items-center gap-1 whitespace-nowrap rounded px-1.5 py-0.5 font-medium; }
.tone-ok { @apply bg-emerald-50 text-emerald-800 dark:bg-emerald-950/40 dark:text-emerald-300; }
.tone-likely { @apply border border-dashed border-emerald-400 text-emerald-800 dark:border-emerald-600 dark:text-emerald-300; }
.tone-attention { @apply bg-amber-100 text-amber-900 dark:bg-amber-900/40 dark:text-amber-200; }
.tone-neutral { @apply bg-gray-100 text-gray-700 dark:bg-dark-700 dark:text-gray-300; }
.tone-running { @apply bg-sky-50 text-sky-800 dark:bg-sky-950/40 dark:text-sky-300; }
@keyframes progress-slide { from { transform: translateX(-110%); } to { transform: translateX(320%); } }
</style>
