<template>
  <article class="tt" :class="{ 'tt-unavailable': !available }" data-testid="test-pelican">
    <header class="tt-head">
      <div class="min-w-0">
        <h3 class="tt-name">
          {{ t('admin.modelIntegrity.tests.types.pelican.name') }}
          <span class="pp-manual">{{ t('admin.modelIntegrity.tests.pelican.manualOnly') }}</span>
        </h3>
        <p class="tt-what">{{ t('admin.modelIntegrity.tests.types.pelican.what') }}</p>
      </div>
      <button
        type="button"
        class="btn btn-secondary btn-sm shrink-0"
        :disabled="!available || running || !variantID || Boolean(runBlocked)"
        data-testid="run-pelican"
        @click="emit('run', { variant: variantID })"
      >
        <Icon name="play" size="xs" :class="running ? 'motion-safe:animate-pulse' : ''" />
        {{ running ? t('admin.modelIntegrity.tests.running') : t('admin.modelIntegrity.tests.pelican.draw') }}
      </button>
    </header>

    <p v-if="!available" class="tt-unavailable-note" data-testid="unavailable-note">{{ unavailableReason || t('admin.modelIntegrity.tests.onlyDirectOAuth') }}</p>

    <template v-else>
      <p v-if="runBlocked" class="tt-auto-note" data-testid="manual-budget-note">{{ runBlocked }}</p>
      <div class="tt-controls">
        <fieldset class="pp-variants">
          <legend class="tt-label">{{ t('admin.modelIntegrity.tests.pelican.variant') }}</legend>
          <div class="pp-variant-row" role="radiogroup">
            <label v-for="variant in variants" :key="variant.id" class="pp-variant" :class="{ 'pp-variant-on': variantID === variant.id }">
              <input v-model="variantID" type="radio" class="sr-only" name="pelican-variant" :value="variant.id" :data-testid="`pelican-variant-${variant.id}`" />
              {{ pelicanVariantLabel(variant) }}
            </label>
          </div>
        </fieldset>
        <p class="pp-effort" data-testid="pelican-effort">{{ t('admin.modelIntegrity.tests.pelican.effort', { effort: route.reasoning_effort || t('admin.modelIntegrity.common.defaultEffort') }) }}</p>
      </div>
      <details v-if="selectedVariant?.prompt" class="pp-prompt">
        <summary class="pp-prompt-summary">{{ t('admin.modelIntegrity.tests.pelican.prompt') }}</summary>
        <p class="pp-prompt-text" data-testid="pelican-prompt">{{ selectedVariant.prompt }}</p>
      </details>

      <div v-if="running" class="tt-progress" role="status" data-testid="test-progress">
        <div class="tt-progress-head"><span>{{ t('admin.modelIntegrity.tests.pelican.drawing') }}</span></div>
        <div class="tt-progress-track" aria-hidden="true"><span class="tt-progress-value tt-progress-indeterminate" /></div>
        <p v-if="retriedRequests !== null" class="tt-progress-requests" data-testid="test-progress-requests">{{ t('admin.modelIntegrity.tests.progress.requests', { count: retriedRequests }) }}</p>
      </div>

      <p class="tt-cost">
        <span data-testid="per-run">{{ perRunText }}</span>
        <span>{{ t('admin.modelIntegrity.tests.pelican.notScored') }}</span>
      </p>
    </template>

    <footer class="pp-result">
      <template v-if="latest && latest.status === 'completed'">
        <div class="pp-specimen">
          <PelicanFrame :run-id="latest.id" mode="preview" :title="frameTitle(latest)" />
          <button type="button" class="pp-open" data-testid="pelican-open" :aria-label="t('admin.modelIntegrity.tests.pelican.openLabel', { time: formatTime(latest.finished_at || latest.started_at) })" @click="emit('open', latest)" />
        </div>
        <p class="pp-caption">
          <span class="pp-caption-status" data-testid="latest-status">{{ runStatusLabel(t, latest) }}</span>
          <span>{{ variantName(latest) }}</span>
          <time :datetime="latest.finished_at || latest.started_at">{{ formatTime(latest.finished_at || latest.started_at) }}</time>
          <button type="button" class="pp-caption-open" @click="emit('open', latest)">{{ t('admin.modelIntegrity.tests.pelican.open') }}</button>
        </p>
      </template>
      <button v-else-if="latest" type="button" class="pp-failed" data-testid="pelican-latest" @click="emit('open', latest)">
        <span class="pp-caption-status" data-testid="latest-status">{{ runStatusLabel(t, latest) }}</span>
        <span class="pp-failed-text" data-testid="latest-explanation">{{ runExplanation(t, latest, catalog) }}</span>
        <time class="pp-failed-time" :datetime="latest.finished_at || latest.started_at">{{ formatTime(latest.finished_at || latest.started_at) }}</time>
      </button>
      <span v-else class="pp-never">{{ t('admin.modelIntegrity.tests.neverRun') }}</span>
    </footer>
  </article>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import PelicanFrame from './PelicanFrame.vue'
import type { OpenAIEvalModelCatalog, OpenAIEvalPelicanVariant, OpenAIEvalRouteConfig, OpenAIEvalRun } from '@/api/admin/accounts'
import { DEFAULT_MAX_REQUEST_ATTEMPTS, maxRequestsPerRun, pelicanVariantLabel, pelicanVariantName, requestsPerRun } from '@/views/admin/modelIntegrity/modelIntegrity'
import { runExplanation, runStatusLabel } from '@/views/admin/modelIntegrity/runText'

/**
 * Pelican is manual only: no schedule, no score. A finished drawing shows as a
 * frozen still; opening it plays the page in the run dialog.
 */
const props = defineProps<{
  route: OpenAIEvalRouteConfig
  catalog: OpenAIEvalModelCatalog | null
  latest?: OpenAIEvalRun
  /** The persisted run while it is in flight, polled by the page. */
  progress?: OpenAIEvalRun
  running: boolean
  available: boolean
  unavailableReason?: string
  /** Why a manual run would be refused right now (experimental limits). */
  runBlocked?: string
  maxAttempts?: number
}>()
const emit = defineEmits<{ (e: 'run', options: { variant: string }): void; (e: 'open', run: OpenAIEvalRun): void }>()
const { t } = useI18n()

/** Older catalogs do not list variants; the plugin ids are stable, so they are offered with local names. */
const variants = computed<Pick<OpenAIEvalPelicanVariant, 'id' | 'label' | 'name' | 'prompt'>[]>(() => {
  const listed = props.catalog?.pelican?.variants
  if (listed?.length) return listed
  return [
    { id: 'original', label: t('admin.modelIntegrity.tests.pelican.variants.original'), prompt: '' },
    { id: 'all-in-one', label: t('admin.modelIntegrity.tests.pelican.variants.allInOne'), prompt: '' }
  ]
})
const variantID = ref(variants.value[0]?.id ?? '')
watch(variants, list => {
  if (!list.some(variant => variant.id === variantID.value)) variantID.value = list[0]?.id ?? ''
})
const selectedVariant = computed(() => variants.value.find(variant => variant.id === variantID.value))
/** A drawing is one request; more upstream requests mean retries, shown once they happen. */
const retriedRequests = computed(() => {
  const count = props.progress?.request_count
  return count != null && count > requestsPerRun(props.route, 'pelican', props.catalog) ? count : null
})

const perRunText = computed(() => {
  const count = requestsPerRun(props.route, 'pelican', props.catalog)
  const max = maxRequestsPerRun(props.route, 'pelican', props.maxAttempts ?? DEFAULT_MAX_REQUEST_ATTEMPTS, props.catalog)
  return max > count ? t('admin.modelIntegrity.tests.perRunRetry', { count, max }) : t('admin.modelIntegrity.tests.perRun', { count })
})

function variantName(run: OpenAIEvalRun) {
  const id = run.outcome.pelican?.variant
  return props.catalog?.pelican?.variants?.length ? pelicanVariantName(id, props.catalog) : pelicanVariantLabel(variants.value.find(variant => variant.id === id)) || id || ''
}
function frameTitle(run: OpenAIEvalRun) {
  return t('admin.modelIntegrity.tests.pelican.frameTitle', { variant: variantName(run) })
}
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
.tt-controls { @apply flex flex-wrap items-end gap-x-5 gap-y-3; }
.tt-label { @apply mb-1 text-xs text-gray-500 dark:text-gray-400; }
.tt-cost { @apply flex flex-wrap gap-x-4 gap-y-1 text-xs tabular-nums text-gray-500 dark:text-gray-400; }
.tt-progress { @apply rounded-md bg-gray-50 px-3 py-2.5 dark:bg-dark-800/70; }
.tt-progress-head { @apply mb-2 text-xs font-medium text-gray-700 dark:text-gray-300; }
.tt-progress-track { @apply relative h-2 overflow-hidden rounded-full bg-gray-200 dark:bg-dark-600; }
.tt-progress-value { @apply block h-full rounded-full bg-primary-500; }
.tt-progress-indeterminate { width: 35%; animation: progress-slide 1.2s ease-in-out infinite; }
.tt-progress-requests { @apply mt-1.5 text-xs tabular-nums text-gray-500 dark:text-gray-400; }
.tt-auto-note { @apply text-xs leading-relaxed text-amber-800 dark:text-amber-300; }
@media (prefers-reduced-motion: reduce) { .tt-progress-indeterminate { animation: none; width: 100%; opacity: 0.5; } }
@keyframes progress-slide { from { transform: translateX(-110%); } to { transform: translateX(320%); } }

.pp-manual { @apply rounded border border-gray-300 px-1.5 py-px text-[0.6875rem] font-normal text-gray-600 dark:border-dark-500 dark:text-gray-400; }
.pp-variants { @apply min-w-0; }
.pp-variant-row { @apply inline-flex rounded-lg bg-gray-100 p-0.5 text-sm dark:bg-dark-900; }
.pp-variant { @apply cursor-pointer rounded-md px-3 py-1 text-gray-600 dark:text-gray-400; }
.pp-variant:focus-within { @apply ring-2 ring-primary-500; }
.pp-variant-on { @apply bg-white font-medium text-gray-900 shadow-sm dark:bg-dark-600 dark:text-white; }
.pp-effort { @apply pb-1.5 text-xs text-gray-500 dark:text-gray-400; }
.pp-prompt { @apply text-xs; }
.pp-prompt-summary { @apply inline-flex cursor-pointer text-gray-500 underline-offset-2 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:text-gray-400; }
.pp-prompt-text { @apply mt-1 max-h-56 overflow-y-auto whitespace-pre-wrap break-words rounded border border-gray-200 bg-gray-50 px-2.5 py-2 text-gray-800 [overflow-wrap:anywhere] dark:border-dark-600 dark:bg-dark-900/50 dark:text-gray-200; }

/*
 * A finished drawing is shown as a specimen on a plain slate mat — never in
 * the green of a passed test — because a page that was made is not a verdict.
 */
.pp-result { @apply text-xs; }
.pp-specimen { @apply relative max-w-md overflow-hidden rounded-md border border-slate-300 bg-slate-200 p-1.5 dark:border-slate-600 dark:bg-slate-700; }
.pp-open { @apply absolute inset-0 cursor-zoom-in rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary-500; }
.pp-caption { @apply mt-1.5 flex max-w-md flex-wrap items-baseline gap-x-3 gap-y-1 tabular-nums text-gray-500 dark:text-gray-400; }
.pp-caption-status { @apply text-sm font-semibold text-gray-900 dark:text-white; }
.pp-caption-open { @apply ml-auto font-medium text-primary-700 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:text-primary-300; }
.pp-failed { @apply grid w-full grid-cols-[auto_minmax(0,1fr)] items-start gap-x-3 gap-y-1 rounded-md border border-rose-200 bg-rose-50 px-3 py-3 text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:border-rose-900/60 dark:bg-rose-950/25 sm:grid-cols-[auto_minmax(0,1fr)_auto]; }
.pp-failed-text { @apply leading-relaxed text-gray-600 dark:text-gray-300; }
.pp-failed-time { @apply col-start-2 tabular-nums text-gray-400 sm:col-start-auto; }
.pp-never { @apply text-gray-400; }
</style>
