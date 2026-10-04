<template>
  <div>
    <!-- These rows are real request selections. The offline evaluation's
         recommended order is shown on the ranking board above, never here. -->
    <p class="ledger-banner" data-testid="dispatch-banner">
      <Icon name="bolt" size="sm" class="mt-0.5 shrink-0" />
      <span>{{ t('admin.modelIntegrity.scheduling.decisions.actualDispatch') }}</span>
    </p>
    <div class="ledger-toolbar">
      <select v-model="modelFilter" class="input ledger-select" :aria-label="t('admin.modelIntegrity.scheduling.decisions.filterModel')">
        <option value="">{{ t('admin.modelIntegrity.scheduling.decisions.filterModel') }}</option>
        <option v-for="model in models" :key="model" :value="model">{{ model }}</option>
      </select>
      <label class="ledger-check">
        <input v-model="onlyProblems" type="checkbox" class="rounded border-gray-300 text-primary-600 focus:ring-primary-500 dark:border-dark-500" />
        {{ t('admin.modelIntegrity.scheduling.decisions.onlyProblems') }}
      </label>
    </div>

    <p v-if="traces.length === 0" class="ledger-empty">{{ t('admin.modelIntegrity.scheduling.decisions.empty') }}</p>
    <p v-else-if="visible.length === 0" class="ledger-empty">{{ t('admin.modelIntegrity.scheduling.decisions.noMatch') }}</p>

    <ol v-else class="ledger">
      <li v-for="(trace, index) in visible" :key="`${trace.at}-${index}`" class="ledger-row" :class="{ 'ledger-row-problem': isProblem(trace) }" data-testid="decision-row">
        <div class="ledger-main">
          <time class="ledger-time" :datetime="trace.at" :title="formatAbsolute(trace.at)">{{ formatClock(trace.at) }}</time>
          <div class="min-w-0 flex-1">
            <p class="ledger-headline">
              <span v-if="trace.selected_account_id" class="ledger-chosen">{{ t('admin.modelIntegrity.scheduling.decisions.chosen', { account: accountLabel(trace.selected_account_id) }) }}</span>
              <span v-else class="ledger-none">{{ t('admin.modelIntegrity.scheduling.decisions.noneChosen') }}</span>
              <span class="ledger-route">
                {{ trace.requested_model || '—' }}<template v-if="trace.requested_reasoning_effort"> · {{ t('admin.modelIntegrity.scheduling.decisions.effort', { effort: trace.requested_reasoning_effort }) }}</template>
              </span>
            </p>
            <p class="ledger-why">{{ decisionText(trace) }}</p>
            <p class="ledger-meta">
              <span>{{ t('admin.modelIntegrity.scheduling.decisions.policyUsed', { policy: policyName(trace.scheduling_policy) }) }}</span>
              <span v-if="trace.candidates?.length">{{ t('admin.modelIntegrity.scheduling.decisions.counts', countsOf(trace)) }}</span>
              <span v-if="trace.ranking_basis" class="ledger-basis" :class="`ledger-basis-${basisKey(trace)}`" data-testid="dispatch-basis">
                {{ t(`admin.modelIntegrity.scheduling.decisions.basis.${basisKey(trace)}`) }}
              </span>
              <span v-if="trace.selected_rank != null" data-testid="dispatch-rank">{{ t('admin.modelIntegrity.scheduling.decisions.selectedRank', { rank: trace.selected_rank }) }}</span>
              <span v-if="trace.snapshot_evaluated_at">{{ t('admin.modelIntegrity.scheduling.decisions.snapshotAt', { time: formatAbsolute(trace.snapshot_evaluated_at) }) }}</span>
              <span v-if="trace.config_revision != null">{{ t('admin.modelIntegrity.scheduling.rank.revision', { revision: trace.config_revision }) }}</span>
              <span v-if="trace.route_migration_active">{{ t('admin.modelIntegrity.scheduling.decisions.migration', { from: trace.migration_from_rate_multiplier ?? '—', to: trace.selected_rate_multiplier ?? '—' }) }}</span>
            </p>
            <p v-if="trace.ranking_basis === 'live_fallback'" class="ledger-fallback" data-testid="dispatch-fallback">
              {{ t(`admin.modelIntegrity.scheduling.rank.fallbackReason.${fallbackReasonKey(trace.ranking_fallback_reason)}`) }}
            </p>
            <p v-else-if="trace.ranking_basis === 'owner'" class="ledger-fallback" data-testid="dispatch-owner">
              {{ t('admin.modelIntegrity.scheduling.decisions.ownerOverride') }}
            </p>
            <details v-if="trace.error" class="ledger-error">
              <summary class="cursor-pointer">{{ t('admin.modelIntegrity.scheduling.decisions.errorLabel') }}</summary>
              <code class="mt-1 block whitespace-pre-wrap break-all font-mono">{{ trace.error }}</code>
            </details>
          </div>
          <button
            v-if="trace.candidates?.length"
            type="button"
            class="ledger-toggle"
            :aria-expanded="expanded.has(index)"
            @click="toggle(index)"
          >
            {{ expanded.has(index) ? t('admin.modelIntegrity.scheduling.decisions.hideCandidates') : t('admin.modelIntegrity.scheduling.decisions.showCandidates') }}
            <Icon name="chevronDown" size="xs" :class="expanded.has(index) ? 'rotate-180' : ''" />
          </button>
        </div>

        <div v-if="expanded.has(index) && trace.candidates?.length" class="ledger-detail">
          <p v-if="isAffinityOnly(trace)" class="ledger-note">{{ t('admin.modelIntegrity.scheduling.decisions.affinityOnly') }}</p>
          <p class="ledger-note">{{ t('admin.modelIntegrity.scheduling.decisions.scoreHint') }}</p>
          <div class="overflow-x-auto">
            <table class="cand-table">
              <thead>
                <tr>
                  <th scope="col">{{ t('admin.modelIntegrity.scheduling.decisions.columns.account') }}</th>
                  <th scope="col" class="num">{{ t('admin.modelIntegrity.scheduling.decisions.columns.rank') }}</th>
                  <th scope="col">{{ t('admin.modelIntegrity.scheduling.decisions.columns.verdict') }}</th>
                  <th scope="col" class="cand-why">{{ t('admin.modelIntegrity.scheduling.decisions.columns.why') }}</th>
                  <th scope="col" class="num">{{ t('admin.modelIntegrity.scheduling.decisions.columns.rate') }}</th>
                  <th scope="col" class="num">{{ t('admin.modelIntegrity.scheduling.decisions.columns.errors') }}</th>
                  <th scope="col" class="num">{{ t('admin.modelIntegrity.scheduling.decisions.columns.ttft') }}</th>
                  <th scope="col" class="num">{{ t('admin.modelIntegrity.scheduling.decisions.columns.load') }}</th>
                  <th v-if="hasQuality(trace)" scope="col" class="num" :title="t('admin.modelIntegrity.scheduling.decisions.qualityHint')">{{ t('admin.modelIntegrity.scheduling.decisions.columns.quality') }}</th>
                  <th scope="col">{{ t('admin.modelIntegrity.scheduling.decisions.columns.score') }}</th>
                  <th scope="col" class="num">{{ t('admin.modelIntegrity.scheduling.decisions.columns.contribution') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="candidate in orderCandidates(trace.candidates)" :key="candidate.account_id" :class="candidateRowClass(candidate)" data-testid="candidate-row">
                  <td class="font-medium text-gray-900 dark:text-gray-100">{{ accountLabel(candidate.account_id) }}</td>
                  <td class="num">
                    <span v-if="candidate.rank != null" data-testid="candidate-rank">{{ candidate.rank }}</span>
                    <span v-else class="text-gray-400">—</span>
                  </td>
                  <td><span class="verdict" :class="`verdict-${verdictOf(candidate)}`">{{ t(`admin.modelIntegrity.scheduling.decisions.verdict.${verdictOf(candidate)}`) }}</span></td>
                  <td class="cand-why">{{ candidateWhy(candidate) }}</td>
                  <td class="num">{{ candidate.rate_multiplier != null ? `${formatNumber(candidate.rate_multiplier)}x` : '—' }}</td>
                  <td class="num">{{ candidate.eligible && candidate.error_rate != null ? formatPercent(candidate.error_rate) : '—' }}</td>
                  <td class="num">{{ candidate.eligible && candidate.ttft_ms ? `${Math.round(candidate.ttft_ms)} ms` : '—' }}</td>
                  <td class="num">{{ candidate.eligible && candidate.load_rate != null ? `${candidate.load_rate}%` : '—' }}</td>
                  <td v-if="hasQuality(trace)" class="num" data-testid="candidate-quality">
                    <template v-if="hasRatio(candidate)">
                      <span class="block">{{ formatPercent(candidate.quality_ratio) }}</span>
                      <span class="block text-[11px] text-gray-500 dark:text-gray-400">{{ t('admin.modelIntegrity.scheduling.decisions.qualityCounts', { passed: (candidate.pass_count ?? 0) + (candidate.suspected_pass_count ?? 0), evaluated: candidate.evaluated_count }) }}</span>
                      <span v-if="candidate.suspected_pass_count" class="block text-[11px] text-gray-500 dark:text-gray-400">{{ t('admin.modelIntegrity.scheduling.decisions.qualitySplit', { pass: candidate.pass_count ?? 0, suspected: candidate.suspected_pass_count }) }}</span>
                      <span v-if="candidate.quality_contribution" class="block text-[11px] text-gray-500 dark:text-gray-400" data-testid="candidate-quality-contribution">{{ t('admin.modelIntegrity.scheduling.decisions.qualityContribution', { value: formatSigned(candidate.quality_contribution) }) }}</span>
                    </template>
                    <span v-else-if="candidate.eligible" class="text-gray-500 dark:text-gray-400" data-testid="candidate-quality-unknown">{{ t('admin.modelIntegrity.scheduling.decisions.qualityUnknown') }}</span>
                    <span v-else class="text-gray-400">—</span>
                  </td>
                  <td>
                    <span v-if="candidate.eligible && candidateScore(candidate) != null" class="score">
                      <span class="score-bar" aria-hidden="true"><span class="score-fill" :style="{ width: `${scoreWidth(trace, candidateScore(candidate) as number)}%` }" /></span>
                      <span class="score-num">{{ formatNumber(candidateScore(candidate) as number) }}</span>
                    </span>
                    <span v-else class="text-gray-400">—</span>
                  </td>
                  <td class="num">
                    <span v-if="candidate.contributions" class="contrib" data-testid="candidate-contribution" :title="contributionTitle(candidate)">
                      <span v-for="factor in RANKING_FACTORS" :key="factor" class="contrib-part" :class="{ 'contrib-zero': !(candidate.contributions[factor] > 0) }">
                        {{ Math.round((candidate.contributions[factor] ?? 0) * 10) / 10 }}
                      </span>
                    </span>
                    <span v-else class="text-gray-400">—</span>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <p v-if="trace.candidates_truncated" class="ledger-note">{{ t('admin.modelIntegrity.scheduling.decisions.truncated') }}</p>
        </div>
      </li>
    </ol>
  </div>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { SchedulerDecisionCandidate, SchedulerDecisionTrace } from '@/api/admin/accounts'
import { candidateReasonKey, decisionKey, exclusionKey, fallbackReasonKey, orderCandidates, policyKey, RANKING_FACTORS } from '@/views/admin/modelIntegrity/modelIntegrity'

const KNOWN_BASES = new Set(['snapshot', 'live_fallback', 'legacy', 'owner'])

/** Which order the request actually used, so an offline recommendation is never implied. */
function basisKey(trace: SchedulerDecisionTrace) {
  return trace.ranking_basis && KNOWN_BASES.has(trace.ranking_basis) ? trace.ranking_basis : 'legacy'
}

/**
 * The published total score. Older traces only carry `score`, so it remains the
 * fallback; a priority_score of exactly 0 is a real score and is kept.
 */
function candidateScore(candidate: SchedulerDecisionCandidate): number | null {
  if (candidate.priority_score != null) return candidate.priority_score
  return candidate.score ?? null
}

function contributionTitle(candidate: SchedulerDecisionCandidate) {
  if (!candidate.contributions) return ''
  return RANKING_FACTORS
    .map(factor => `${t(`admin.modelIntegrity.scheduling.rank.weights.${factor}`)} ${candidate.contributions?.[factor] ?? 0}`)
    .join(t('admin.modelIntegrity.scheduling.rules.weightsSeparator'))
}

const props = defineProps<{
  traces: SchedulerDecisionTrace[]
  accountName: (id: number) => string
}>()

const { t } = useI18n()
const modelFilter = ref('')
const onlyProblems = ref(false)
const expanded = reactive(new Set<number>())

const models = computed(() => [...new Set(props.traces.map(trace => trace.requested_model).filter((model): model is string => Boolean(model)))].sort())
const visible = computed(() => props.traces.filter(trace => (!modelFilter.value || trace.requested_model === modelFilter.value) && (!onlyProblems.value || isProblem(trace))))

watch([modelFilter, onlyProblems, () => props.traces], () => expanded.clear())

function toggle(index: number) {
  if (expanded.has(index)) expanded.delete(index)
  else expanded.add(index)
}

function isProblem(trace: SchedulerDecisionTrace) {
  // An owner override is a deliberate binding, not a routing failure.
  if (trace.ranking_basis === 'owner' && trace.selected_account_id) return Boolean(trace.error)
  return !trace.selected_account_id || Boolean(trace.error) || trace.reason_code === 'no_selection' || trace.reason_code === 'selection_error'
}

function hasRatio(candidate: SchedulerDecisionCandidate): candidate is SchedulerDecisionCandidate & { quality_ratio: number } {
  // Counts are selected test types; an 'unassessed' state means unknown even if a stale ratio is present.
  return candidate.quality_state !== 'unassessed' && candidate.quality_ratio != null && Number(candidate.evaluated_count) > 0
}

/**
 * The pass-rate column appears when the decision used a policy that reads it
 * (so a missing rate is shown as unknown, never as 100 %), or when the server
 * reported a rate for any candidate.
 */
function hasQuality(trace: SchedulerDecisionTrace) {
  if (trace.scheduling_policy === 'avoid_degradation') return true
  return (trace.candidates ?? []).some(candidate => hasRatio(candidate) || Boolean(candidate.quality_contribution))
}

const formatSigned = (value: number) => `${value > 0 ? '+' : ''}${formatNumber(value)}`

function isAffinityOnly(trace: SchedulerDecisionTrace) {
  return ['previous_response_id', 'session_hash', 'guardian_parent'].includes(trace.layer) && trace.reason_code !== 'sticky_escape'
}

function accountLabel(id: number) {
  const name = props.accountName(id)
  return name.startsWith('#') ? name : `${name} #${id}`
}

function policyName(policy?: string) {
  return t(`admin.modelIntegrity.scheduling.policy.options.${policyKey(policy)}.name`)
}

function decisionText(trace: SchedulerDecisionTrace) {
  const key = decisionKey(trace.reason_code)
  return t(`admin.modelIntegrity.scheduling.decision.${key}`, { code: trace.reason_code || '—' })
}

function countsOf(trace: SchedulerDecisionTrace) {
  const candidates = trace.candidates ?? []
  const eligible = candidates.filter(candidate => candidate.eligible).length
  return { eligible, excluded: candidates.length - eligible }
}

type Verdict = 'selected' | 'topK' | 'eligible' | 'excluded'
function verdictOf(candidate: SchedulerDecisionCandidate): Verdict {
  if (candidate.selected) return 'selected'
  if (!candidate.eligible) return 'excluded'
  return candidate.in_top_k ? 'topK' : 'eligible'
}

function candidateRowClass(candidate: SchedulerDecisionCandidate) {
  return { 'cand-selected': candidate.selected, 'cand-excluded': !candidate.eligible }
}

function candidateWhy(candidate: SchedulerDecisionCandidate) {
  if (!candidate.eligible) {
    const key = exclusionKey(candidate.exclusion_reason)
    return t(`admin.modelIntegrity.scheduling.exclusion.${key}`, { code: candidate.exclusion_reason || '—' })
  }
  if (candidate.evaluation_penalty && candidate.evaluation_penalty > 0) {
    return t('admin.modelIntegrity.scheduling.exclusion.evaluation_hard_failure')
  }
  const key = candidateReasonKey(candidate.decision_reason)
  if (!key) return '—'
  if (key.startsWith('decision.')) return t(`admin.modelIntegrity.scheduling.${key}`, { code: candidate.decision_reason })
  return t(`admin.modelIntegrity.scheduling.candidateReason.${key}`)
}

function scoreWidth(trace: SchedulerDecisionTrace, score: number) {
  const scores = (trace.candidates ?? []).filter(candidate => candidate.eligible && candidate.score != null).map(candidate => candidate.score as number)
  if (!scores.length) return 0
  const max = Math.max(...scores)
  const min = Math.min(0, ...scores)
  if (max === min) return 100
  return Math.max(4, Math.round(((score - min) / (max - min)) * 100))
}

const formatNumber = (value: number) => (Math.abs(value) >= 100 ? value.toFixed(0) : Number(value.toFixed(3)).toString())
const formatPercent = (value: number) => `${(value * 100).toFixed(value > 0 && value < 0.01 ? 2 : 1)}%`
const formatClock = (value: string) => {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })
}
const formatAbsolute = (value: string) => {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}
</script>

<style scoped>
.ledger-toolbar { @apply mb-3 flex flex-wrap items-center gap-3; }
.ledger-banner { @apply mb-3 flex items-start gap-2 rounded-lg border border-gray-200 bg-gray-50 px-3 py-2 text-xs leading-relaxed text-gray-600 dark:border-dark-700 dark:bg-dark-900/50 dark:text-gray-400; }
.ledger-basis { @apply rounded px-1.5 py-0.5 font-medium; }
.ledger-basis-snapshot { @apply bg-primary-100 text-primary-800 dark:bg-primary-900/50 dark:text-primary-200; }
.ledger-basis-live_fallback { @apply bg-violet-100 text-violet-800 dark:bg-violet-900/50 dark:text-violet-200; }
.ledger-basis-legacy { @apply bg-gray-100 text-gray-700 dark:bg-dark-700 dark:text-gray-300; }
.ledger-basis-owner { @apply bg-amber-100 text-amber-900 dark:bg-amber-900/40 dark:text-amber-200; }
.ledger-fallback { @apply mt-1 max-w-[72ch] text-xs leading-snug text-gray-500 dark:text-gray-400; }
.contrib { @apply inline-flex gap-1 text-[11px] tabular-nums; }
.contrib-part { @apply rounded bg-gray-100 px-1 py-0.5 text-gray-700 dark:bg-dark-700 dark:text-gray-300; }
.contrib-zero { @apply text-gray-400 dark:text-gray-500; }
.ledger-select { @apply h-9 w-auto min-w-[11rem] py-1 text-sm; }
.ledger-check { @apply inline-flex items-center gap-2 text-sm text-gray-600 dark:text-gray-300; }
.ledger-empty { @apply rounded-lg border border-dashed border-gray-300 px-4 py-8 text-center text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400; }
.ledger { @apply divide-y divide-gray-100 rounded-lg border border-gray-200 dark:divide-dark-700 dark:border-dark-700; }
.ledger-row { @apply px-4 py-3; }
.ledger-row-problem { box-shadow: inset 3px 0 0 theme('colors.rose.500'); }
.ledger-main { @apply flex items-start gap-4; }
.ledger-time { @apply w-[4.75rem] shrink-0 pt-0.5 text-xs tabular-nums text-gray-500 dark:text-gray-400; }
.ledger-headline { @apply flex flex-wrap items-baseline gap-x-3 gap-y-1 text-sm; }
.ledger-chosen { @apply font-semibold text-gray-900 dark:text-white; }
.ledger-none { @apply font-semibold text-rose-700 dark:text-rose-300; }
.ledger-route { @apply text-gray-500 dark:text-gray-400; }
.ledger-why { @apply mt-1 text-sm text-gray-700 dark:text-gray-300; }
.ledger-meta { @apply mt-1 flex flex-wrap gap-x-4 gap-y-0.5 text-xs text-gray-500 dark:text-gray-400; }
.ledger-error { @apply mt-1 text-xs text-gray-500 dark:text-gray-400; }
.ledger-toggle { @apply inline-flex shrink-0 items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-primary-700 hover:bg-primary-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:text-primary-300 dark:hover:bg-primary-950/40; }
.ledger-detail { @apply mt-3 space-y-2 sm:ml-[5.75rem]; }
.ledger-note { @apply text-xs text-gray-500 dark:text-gray-400; }
.cand-table { @apply w-full min-w-[46rem] text-left text-xs; }
.cand-table th { @apply whitespace-nowrap border-b border-gray-200 px-2 py-1.5 font-medium text-gray-500 dark:border-dark-700 dark:text-gray-400; }
.cand-table td { @apply border-b border-gray-100 px-2 py-1.5 align-top text-gray-700 dark:border-dark-800 dark:text-gray-300; }
.cand-table tr:last-child td { @apply border-b-0; }
.cand-table .num { @apply whitespace-nowrap text-right tabular-nums; }
.cand-why { @apply min-w-[12rem] max-w-[22rem]; }
.cand-selected td { @apply bg-primary-50/60 dark:bg-primary-950/30; }
.cand-excluded td { @apply text-gray-500 dark:text-gray-400; }
.verdict { @apply inline-block whitespace-nowrap rounded px-1.5 py-0.5 font-medium; }
.verdict-selected { @apply bg-primary-600 text-white dark:bg-primary-500; }
.verdict-topK { @apply bg-primary-100 text-primary-800 dark:bg-primary-900/50 dark:text-primary-200; }
.verdict-eligible { @apply bg-gray-100 text-gray-700 dark:bg-dark-700 dark:text-gray-300; }
.verdict-excluded { @apply bg-rose-50 text-rose-700 dark:bg-rose-950/40 dark:text-rose-300; }
.score { @apply inline-flex items-center gap-2; }
.score-bar { @apply block h-1.5 w-16 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700; }
.score-fill { @apply block h-full rounded-full bg-primary-600 dark:bg-primary-500; }
.score-num { @apply tabular-nums; }
@media (max-width: 640px) {
  .ledger-main { @apply flex-wrap gap-2; }
  .ledger-time { @apply w-auto; }
}
</style>
