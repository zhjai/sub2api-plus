<template>
  <section class="fa" data-testid="lm-fingerprint">
    <p v-if="comparison !== 'none'" class="fa-verdict" :class="`fa-verdict-${comparison}`" data-testid="lm-comparison">
      {{ t(`admin.modelIntegrity.tests.lm.comparison.${comparison}`) }}
    </p>

    <dl v-if="attribution && attribution.decision !== 'not_model'" class="fa-summary">
      <div class="fa-summary-lead">
        <dt>{{ t('admin.modelIntegrity.tests.lm.prediction') }}</dt>
        <dd data-testid="lm-prediction">
          <span class="fa-name">{{ attribution.prediction_name || attribution.prediction }}</span>
          <span v-if="attribution.prediction_name && attribution.prediction_name !== attribution.prediction" class="fa-id">{{ attribution.prediction }}</span>
        </dd>
      </div>
      <div>
        <dt>{{ t('admin.modelIntegrity.tests.lm.family') }}</dt>
        <dd data-testid="lm-family">{{ attribution.family_prediction_name || '—' }}</dd>
      </div>
      <div>
        <dt>{{ t('admin.modelIntegrity.tests.lm.probability') }}</dt>
        <dd data-testid="lm-probability">{{ attribution.probability != null ? formatProbability(attribution.probability) : t('admin.modelIntegrity.tests.lm.noProbability') }}</dd>
      </div>
      <div>
        <dt>{{ t('admin.modelIntegrity.tests.lm.usedOutputs') }}</dt>
        <dd data-testid="lm-used">{{ attribution.used_outputs }} / {{ requests }}</dd>
      </div>
    </dl>

    <p v-if="attribution?.decision === 'not_model'" class="fa-note" data-testid="lm-not-model">
      {{ t('admin.modelIntegrity.tests.lm.notModel') }}
      <template v-if="attribution.sequence">{{ t('admin.modelIntegrity.tests.lm.sequence', { ac: attribution.sequence.autocorrelation.toFixed(3), ss: formatProbability(attribution.sequence.small_step_share) }) }}</template>
    </p>
    <p v-if="attribution?.decision === 'partial'" class="fa-note" data-testid="lm-partial">{{ t('admin.modelIntegrity.tests.lm.partial') }}</p>
    <p v-if="aliasCandidate" class="fa-note" data-testid="lm-alias">{{ t('admin.modelIntegrity.tests.lm.aliasNote', { model: run.requested_model, candidate: aliasCandidate }) }}</p>
    <p v-if="lm && !lm.enrolled" class="fa-note" data-testid="lm-not-enrolled">{{ t('admin.modelIntegrity.tests.lm.notEnrolledNote', { model: run.requested_model }) }}</p>

    <div v-if="ranking.length" class="fa-lineup-wrap">
      <div class="fa-lineup-head">
        <h4 class="fa-h">{{ t('admin.modelIntegrity.tests.lm.lineup') }}</h4>
        <span class="fa-hint">{{ scored ? t('admin.modelIntegrity.tests.lm.lineupProbability') : t('admin.modelIntegrity.tests.lm.lineupScore') }}</span>
      </div>
      <ol class="fa-lineup" data-testid="lm-lineup">
        <li
          v-for="row in visibleRanking"
          :key="row.candidate.model"
          class="fa-row"
          :class="{ 'fa-row-target': row.target, 'fa-row-top': row.rank === 1, 'fa-row-gap': row.gapBefore }"
          :data-testid="row.target ? 'lm-target-row' : 'lm-row'"
        >
          <span class="fa-rank">{{ row.rank }}</span>
          <span class="fa-cand">
            <span class="fa-cand-name">{{ row.candidate.display_name || row.candidate.model }}</span>
            <span class="fa-cand-family">{{ row.candidate.family_name }}</span>
          </span>
          <span class="fa-bar" aria-hidden="true"><span class="fa-bar-fill" :style="{ width: `${row.width}%` }" /></span>
          <span class="fa-value">{{ scored ? formatProbability(row.candidate.probability) : row.candidate.score.toFixed(2) }}</span>
          <span v-if="row.target" class="fa-target-mark">{{ t('admin.modelIntegrity.tests.lm.targetMark') }}</span>
        </li>
      </ol>
      <button v-if="ranking.length > visibleRanking.length" type="button" class="fa-more" @click="showAll = true">
        {{ t('admin.modelIntegrity.tests.lm.showAll', { count: ranking.length }) }}
      </button>
    </div>
    <p v-else-if="attribution && attribution.decision !== 'not_model'" class="fa-note" data-testid="lm-no-ranking">{{ t('admin.modelIntegrity.tests.lm.noRanking') }}</p>

    <dl class="fa-facts">
      <div><dt>{{ t('admin.modelIntegrity.tests.lm.requestEffort') }}</dt><dd data-testid="lm-effort">{{ lm?.request_effort ? lm.request_effort : t('admin.modelIntegrity.tests.lm.effortNotSent') }}</dd></div>
      <div><dt>{{ t('admin.modelIntegrity.tests.lm.recordedUnder') }}</dt><dd>{{ run.reasoning_effort || t('admin.modelIntegrity.common.defaultEffort') }}</dd></div>
      <div v-if="lm?.concurrency"><dt>{{ t('admin.modelIntegrity.tests.lm.concurrency') }}</dt><dd data-testid="lm-concurrency">{{ lm.concurrency }}</dd></div>
      <div v-if="lm?.collection_status"><dt>{{ t('admin.modelIntegrity.tests.lm.collection') }}</dt><dd data-testid="lm-collection">{{ collectionLabel(lm.collection_status) }}</dd></div>
      <div v-if="lm?.compared_as"><dt>{{ t('admin.modelIntegrity.tests.lm.comparedAs') }}</dt><dd class="break-all" data-testid="lm-compared-as">{{ lm.compared_as }}</dd></div>
      <div v-if="lm?.detector_revision" class="fa-facts-wide"><dt>{{ t('admin.modelIntegrity.tests.lm.detector') }}</dt><dd class="break-all" data-testid="lm-detector">{{ lm.detector_revision }}</dd></div>
    </dl>

    <div v-if="samples.length" class="fa-challenges">
      <div class="fa-lineup-head">
        <h4 class="fa-h">{{ t('admin.modelIntegrity.tests.lm.challenges') }}</h4>
        <span class="fa-hint">{{ t('admin.modelIntegrity.tests.lm.challengeRequests', { count: totalAttempts }) }}</span>
      </div>
      <ol class="fa-challenge-list">
        <li v-for="(sample, index) in samples" :key="index" class="fa-challenge" data-testid="lm-sample">
          <details>
            <summary class="fa-challenge-summary">
              <span class="fa-challenge-index">{{ t('admin.modelIntegrity.tests.lm.challenge', { n: index + 1 }) }}</span>
              <span class="fa-challenge-count" data-testid="lm-sample-count">{{ t('admin.modelIntegrity.tests.lm.numbers', { parsed: sample.parsed_numbers, expected: sample.expected_count }) }}<template v-if="minimumFor(index) != null"> · {{ t('admin.modelIntegrity.tests.lm.minimum', { min: minimumFor(index) }) }}</template></span>
              <span v-if="(sample.attempts ?? 0) > 1" class="fa-challenge-retries">{{ t('admin.modelIntegrity.tests.lm.retries', { count: (sample.attempts ?? 1) - 1 }) }}</span>
              <span class="fa-challenge-state" :class="sample.accepted ? 'fa-ok' : 'fa-bad'" data-testid="lm-sample-state">{{ sample.accepted ? t('admin.modelIntegrity.tests.lm.accepted') : t('admin.modelIntegrity.tests.lm.rejected') }}</span>
            </summary>
            <div class="fa-challenge-body">
              <p v-if="sample.error" class="fa-error" data-testid="lm-sample-error">{{ redactSecrets(sample.error) }}</p>
              <div v-if="sample.instructions"><p class="fa-label">{{ t('admin.modelIntegrity.tests.lm.instructions') }}</p><p class="fa-text">{{ sample.instructions }}</p></div>
              <div v-if="sample.prompt"><p class="fa-label">{{ t('admin.modelIntegrity.tests.lm.prompt') }}</p><p class="fa-text">{{ sample.prompt }}</p></div>
              <div v-if="sample.text"><p class="fa-label">{{ t('admin.modelIntegrity.tests.lm.answer') }}</p><p class="fa-text fa-text-numbers">{{ redactSecrets(sample.text) }}</p></div>
            </div>
          </details>
        </li>
      </ol>
    </div>

    <p class="fa-identity">{{ t('admin.modelIntegrity.tests.lm.identityNote') }}</p>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { OpenAIEvalLMFingerprintCandidate, OpenAIEvalModelCatalog, OpenAIEvalRun } from '@/api/admin/accounts'
import { FINGERPRINT_REQUESTS, fingerprintAliasNote, fingerprintComparison, fingerprintModelID, redactSecrets } from '@/views/admin/modelIntegrity/modelIntegrity'
import { formatProbability } from '@/views/admin/modelIntegrity/runText'

const props = defineProps<{ run: OpenAIEvalRun; catalog: OpenAIEvalModelCatalog | null }>()
const { t } = useI18n()

/** The lineup shows the head of the ranking, plus the tested model's row wherever it falls. */
const LINEUP_HEAD = 8
const showAll = ref(false)
watch(() => props.run.id, () => { showAll.value = false })

const lm = computed(() => props.run.outcome.lm_fingerprint)
const attribution = computed(() => lm.value?.attribution ?? null)
const comparison = computed(() => fingerprintComparison(props.run, props.catalog))
const aliasCandidate = computed(() => fingerprintAliasNote(props.run))
const requests = computed(() => props.run.outcome.expected_count || props.catalog?.fingerprint?.requests || FINGERPRINT_REQUESTS)
const scored = computed(() => attribution.value?.probability != null)
const samples = computed(() => lm.value?.samples ?? [])
const totalAttempts = computed(() => samples.value.reduce((sum, sample) => sum + (sample.attempts ?? 1), 0))

interface LineupRow { candidate: OpenAIEvalLMFingerprintCandidate; rank: number; width: number; target: boolean; gapBefore: boolean }

const ranking = computed<LineupRow[]>(() => {
  const results = attribution.value?.results ?? []
  if (!results.length) return []
  const target = (lm.value?.compared_as || fingerprintModelID(props.run.requested_model, props.catalog?.fingerprint?.aliases)).toLowerCase()
  // Without probabilities (partial runs) bars show each score's place between the lowest and highest.
  const scores = results.map(candidate => candidate.score)
  const low = Math.min(...scores)
  const span = Math.max(...scores) - low
  return results.map((candidate, index) => ({
    candidate,
    rank: index + 1,
    width: scored.value
      ? Math.max(0, Math.min(100, (candidate.probability ?? 0) * 100))
      : span > 0 ? ((candidate.score - low) / span) * 100 : 100,
    target: lm.value?.enrolled !== false && candidate.model.toLowerCase() === target,
    gapBefore: false
  }))
})

const visibleRanking = computed(() => {
  if (showAll.value || ranking.value.length <= LINEUP_HEAD) return ranking.value
  const head = ranking.value.slice(0, LINEUP_HEAD)
  const target = ranking.value.find(row => row.target && row.rank > LINEUP_HEAD)
  return target ? [...head, { ...target, gapBefore: true }] : head
})

function minimumFor(index: number) {
  return attribution.value?.diagnostics?.find(item => item.index === index)?.minimum_numbers ?? null
}

function collectionLabel(status: string) {
  const key = `admin.modelIntegrity.tests.lm.collectionStatus.${status}`
  const text = t(key)
  return text === key ? status : text
}
</script>

<style scoped>
.fa { @apply space-y-3; }
.fa-h { @apply text-xs font-semibold text-gray-800 dark:text-gray-200; }
.fa-hint { @apply text-xs text-gray-500 dark:text-gray-400; }
.fa-note { @apply border-l-2 border-gray-300 pl-3 text-xs leading-relaxed text-gray-600 dark:border-dark-500 dark:text-gray-400; }
.fa-verdict { @apply rounded-md px-3 py-2 text-sm font-medium; }
.fa-verdict-match { @apply bg-emerald-50 text-emerald-800 dark:bg-emerald-950/40 dark:text-emerald-300; }
.fa-verdict-mismatch, .fa-verdict-not_model { @apply bg-amber-100 text-amber-900 dark:bg-amber-900/40 dark:text-amber-200; }
.fa-verdict-not_enrolled { @apply bg-gray-100 text-gray-700 dark:bg-dark-700 dark:text-gray-300; }

.fa-summary { @apply grid grid-cols-2 gap-x-6 gap-y-3 sm:grid-cols-[minmax(0,2fr)_repeat(3,minmax(0,1fr))]; }
.fa-summary dt { @apply text-xs text-gray-500 dark:text-gray-400; }
.fa-summary dd { @apply mt-0.5 text-sm font-medium tabular-nums text-gray-900 dark:text-gray-100; }
.fa-summary-lead { @apply col-span-2 sm:col-span-1; }
.fa-name { @apply block text-base font-semibold leading-snug; }
.fa-id { @apply block break-all text-xs font-normal text-gray-500 dark:text-gray-400; }

/*
 * The lineup: a ranked list read top-down. Rank numbers are real order, so
 * they stay. The tested model's candidate carries a solid left rule and its
 * own label wherever it lands; when it sits below the head of the list it is
 * pinned after a gap, so a mismatch is visible without scrolling.
 */
.fa-lineup-head { @apply flex flex-wrap items-baseline justify-between gap-2; }
.fa-lineup { @apply mt-1.5 overflow-hidden rounded-lg border border-gray-200 dark:border-dark-700; }
.fa-row { @apply relative grid grid-cols-[1.75rem_minmax(0,11rem)_minmax(3rem,1fr)_3.25rem] items-center gap-x-3 border-t border-gray-100 px-3 py-1.5 text-xs dark:border-dark-700; }
.fa-row:first-child { @apply border-t-0; }
.fa-row-gap { @apply border-t-2 border-dashed border-gray-200 dark:border-dark-600; }
.fa-rank { @apply text-right tabular-nums text-gray-400; }
.fa-cand { @apply min-w-0; }
.fa-cand-name { @apply block truncate text-gray-800 dark:text-gray-200; }
.fa-cand-family { @apply block truncate text-[0.6875rem] text-gray-400; }
.fa-bar { @apply h-1.5 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700; }
.fa-bar-fill { @apply block h-full rounded-full bg-gray-400 dark:bg-dark-400; }
.fa-value { @apply text-right tabular-nums text-gray-600 dark:text-gray-300; }
.fa-row-top .fa-cand-name { @apply font-semibold text-gray-900 dark:text-white; }
.fa-row-top .fa-bar-fill { @apply bg-gray-700 dark:bg-gray-300; }
.fa-row-target { @apply bg-primary-50/60 dark:bg-primary-950/30; box-shadow: inset 3px 0 0 theme('colors.primary.600'); }
.fa-row-target .fa-bar-fill { @apply bg-primary-600 dark:bg-primary-400; }
.fa-target-mark { @apply col-start-2 col-end-5 -mt-0.5 text-[0.6875rem] font-medium text-primary-700 dark:text-primary-300; }
.fa-more { @apply mt-1.5 text-xs font-medium text-primary-700 hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:text-primary-300; }

.fa-facts { @apply grid grid-cols-2 gap-x-6 gap-y-2 rounded-lg bg-gray-50 p-3 text-xs dark:bg-dark-900/50; }
.fa-facts dt { @apply text-gray-500 dark:text-gray-400; }
.fa-facts dd { @apply font-medium tabular-nums text-gray-800 dark:text-gray-200; }
.fa-facts-wide { @apply col-span-2; }

.fa-challenge-list { @apply mt-1.5 divide-y divide-gray-100 rounded-lg border border-gray-200 text-xs dark:divide-dark-700 dark:border-dark-700; }
.fa-challenge-summary { @apply flex cursor-pointer flex-wrap items-baseline gap-x-3 gap-y-1 px-3 py-2 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary-500; }
.fa-challenge-index { @apply whitespace-nowrap font-medium text-gray-800 dark:text-gray-200; }
.fa-challenge-count { @apply tabular-nums text-gray-600 dark:text-gray-300; }
.fa-challenge-retries { @apply tabular-nums text-gray-500 dark:text-gray-400; }
.fa-challenge-state { @apply ml-auto whitespace-nowrap font-medium; }
.fa-ok { @apply text-emerald-700 dark:text-emerald-300; }
.fa-bad { @apply text-amber-800 dark:text-amber-300; }
.fa-challenge-body { @apply space-y-2 px-3 pb-3; }
.fa-label { @apply mb-0.5 text-gray-500 dark:text-gray-400; }
.fa-text { @apply max-h-48 min-w-0 overflow-y-auto whitespace-pre-wrap break-words text-gray-800 [overflow-wrap:anywhere] dark:text-gray-200; }
.fa-text-numbers { @apply tabular-nums; }
.fa-error { @apply text-rose-800 dark:text-rose-300; }
.fa-identity { @apply text-xs leading-relaxed text-gray-500 dark:text-gray-400; }
</style>
