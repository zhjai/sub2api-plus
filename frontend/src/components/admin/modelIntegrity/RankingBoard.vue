<template>
  <div class="rank-board">
    <!-- Read-only status: what the gateway is applying right now. -->
    <div class="rank-status" :class="`rank-status-${tone}`" data-testid="ranking-status">
      <span class="rank-status-pill">{{ t(`admin.modelIntegrity.scheduling.effective.${effectiveKey}`) }}</span>
      <p class="rank-status-body">
        <template v-if="summary">
          <span>{{ t('admin.modelIntegrity.scheduling.rank.evaluatedAt', { time: formatDateTime(summary.evaluated_at) }) }}</span>
          <span>{{ t('admin.modelIntegrity.scheduling.rank.publishedAt', { time: formatDateTime(summary.published_at) }) }}</span>
          <span>{{ t('admin.modelIntegrity.scheduling.rank.revision', { revision: summary.config_revision }) }}</span>
          <span>{{ t('admin.modelIntegrity.scheduling.rank.trigger', { trigger: t(`admin.modelIntegrity.scheduling.rank.triggerValue.${triggerKey}`) }) }}</span>
          <span v-if="nextReasonText">{{ nextReasonText }}</span>
        </template>
        <span v-else>{{ snapshot?.ranking_error ? t('admin.modelIntegrity.scheduling.evaluation.failed', { reason: snapshot.ranking_error.message }) : t('admin.modelIntegrity.scheduling.evaluation.none') }}</span>
      </p>
      <p v-if="offBody" class="rank-status-note">{{ offBody }}</p>
      <p v-if="statusError" class="rank-status-error" role="alert">{{ statusError.message }}</p>
    </div>

    <div class="rank-filters">
      <label class="rank-field">
        <span class="rank-label">{{ t('admin.modelIntegrity.scheduling.rank.selectGroup') }}</span>
        <select v-model="groupChoice" class="input rank-input" data-testid="ranking-group">
          <option value="">{{ t('admin.modelIntegrity.scheduling.rank.anyGroup') }}</option>
          <option value="0">{{ t('admin.modelIntegrity.scheduling.rank.noGroup') }}</option>
          <option v-for="group in groupOptions" :key="group.id" :value="String(group.id)">{{ group.name }}</option>
        </select>
      </label>
      <label class="rank-field">
        <span class="rank-label">{{ t('admin.modelIntegrity.scheduling.rank.selectModel') }}</span>
        <select v-model="modelChoice" class="input rank-input" data-testid="ranking-model">
          <option value="">{{ t('admin.modelIntegrity.scheduling.rank.anyModel') }}</option>
          <option v-for="model in modelOptions" :key="model" :value="model">{{ model }}</option>
        </select>
      </label>
      <label class="rank-field">
        <span class="rank-label">{{ t('admin.modelIntegrity.scheduling.rank.selectEffort') }}</span>
        <select v-model="effortChoice" class="input rank-input" data-testid="ranking-effort">
          <option value="__any__">{{ t('admin.modelIntegrity.scheduling.rank.anyEffort') }}</option>
          <option value="">{{ t('admin.modelIntegrity.scheduling.rank.unspecifiedEffort') }}</option>
          <option v-for="effort in effortOptions" :key="effort" :value="effort">{{ effort }}</option>
        </select>
      </label>
      <button type="button" class="btn btn-secondary btn-sm" :disabled="loading" data-testid="ranking-reload" @click="refresh()">
        <Icon name="refresh" size="sm" :class="loading ? 'motion-safe:animate-spin' : ''" />{{ t('admin.modelIntegrity.common.refresh') }}
      </button>
    </div>

    <p v-if="loadError" class="rank-error" role="alert" data-testid="ranking-error">{{ loadError }}</p>
    <p v-if="stale" class="rank-stale" role="note" data-testid="ranking-stale">
      {{ t('admin.modelIntegrity.scheduling.rank.stale', { built: summary?.config_revision, current: currentRevision }) }}
    </p>

    <!-- Scope summaries. Accounts are only listed for a fully filtered read. -->
    <div class="rank-scopes">
      <div class="rank-scopes-head">
        <h3 class="rank-h3">{{ t('admin.modelIntegrity.scheduling.rank.scope.title') }}</h3>
        <p class="rank-hint">{{ t('admin.modelIntegrity.scheduling.rank.scope.hint') }}</p>
      </div>
      <p v-if="!dimensions.length" class="rank-empty">{{ t('admin.modelIntegrity.scheduling.rank.scope.empty') }}</p>
      <div v-else class="overflow-x-auto">
        <table class="rank-scope-table">
          <thead>
            <tr>
              <th scope="col">{{ t('admin.modelIntegrity.scheduling.rank.scope.group') }}</th>
              <th scope="col">{{ t('admin.modelIntegrity.scheduling.rank.scope.model') }}</th>
              <th scope="col">{{ t('admin.modelIntegrity.scheduling.rank.scope.effort') }}</th>
              <th scope="col">{{ t('admin.modelIntegrity.scheduling.rank.scope.policy') }}</th>
              <th scope="col">{{ t('admin.modelIntegrity.scheduling.rank.scope.weights') }}</th>
              <th scope="col">{{ t('admin.modelIntegrity.scheduling.rank.scope.sources') }}</th>
              <th scope="col" class="num">{{ t('admin.modelIntegrity.scheduling.rank.scope.candidates') }}</th>
              <th scope="col" class="num">{{ t('admin.modelIntegrity.scheduling.rank.scope.eligible') }}</th>
              <th scope="col">{{ t('admin.modelIntegrity.scheduling.rank.scope.coverage') }}</th>
              <th scope="col"><span class="sr-only">{{ t('admin.modelIntegrity.scheduling.rank.scope.state') }}</span></th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="dimension in dimensions" :key="dimension.dimension_id" :class="{ 'rank-scope-active': isInspecting(dimension) }" data-testid="ranking-scope">
              <td class="font-medium text-gray-900 dark:text-white">{{ dimension.group_name || t('admin.modelIntegrity.scheduling.rank.noGroup') }}</td>
              <td>{{ dimension.requested_model || '—' }}</td>
              <td>{{ dimension.reasoning_effort || t('admin.modelIntegrity.scheduling.rank.unspecifiedEffort') }}</td>
              <td>{{ t(`admin.modelIntegrity.scheduling.policy.options.${policyKey(dimension.policy)}.name`) }}</td>
              <td>
                <span class="rank-weights" :title="weightsSummary(dimension)">
                  <span v-for="factor in weightedFactors(dimension)" :key="factor" class="rank-weight-chip">
                    {{ t(`admin.modelIntegrity.scheduling.rank.weights.${factor}`) }} {{ Math.round((dimension.weights[factor] ?? 0) * 100) }}%
                  </span>
                  <span v-if="dimension.policy === 'avoid_degradation'" class="rank-weight-tier">{{ t('admin.modelIntegrity.scheduling.rank.weights.qualityTier') }}</span>
                </span>
              </td>
              <td>
                <span class="rank-sources">
                  <span v-for="source in dimension.sources" :key="source" class="rank-source">{{ t(`admin.modelIntegrity.scheduling.rank.source.${rankingSourceKey(source)}`) }}</span>
                </span>
              </td>
              <td class="num">{{ dimension.candidate_count ?? '—' }}</td>
              <td class="num">{{ dimension.eligible_count ?? '—' }}</td>
              <td>
                <span class="rank-coverage" :class="`rank-coverage-${coverageStatusKey(dimension.coverage_status)}`">
                  {{ t(`admin.modelIntegrity.scheduling.rank.coverageStatus.${coverageStatusKey(dimension.coverage_status)}`) }}
                </span>
                <p v-if="dimension.coverage_status === 'live_fallback'" class="rank-scope-reason">{{ fallbackText(dimension) }}</p>
                <p v-if="dimension.accounts_truncated" class="rank-scope-reason">{{ t('admin.modelIntegrity.scheduling.rank.scope.accountsTruncated') }}</p>
              </td>
              <td class="whitespace-nowrap text-right">
                <button type="button" class="btn btn-secondary btn-sm" :disabled="loading" @click="inspect(dimension)">
                  {{ isInspecting(dimension) ? t('admin.modelIntegrity.scheduling.rank.scope.inspecting') : t('admin.modelIntegrity.scheduling.rank.scope.inspect') }}
                </button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>

    <!-- The ranked accounts. Every value here came from the server. -->
    <div v-if="selected" class="rank-detail">
      <div class="rank-detail-head">
        <div class="min-w-0">
          <h3 class="rank-h3">
            {{ selected.group_name || t('admin.modelIntegrity.scheduling.rank.noGroup') }}
            <span class="rank-detail-sep">·</span>{{ selected.requested_model || '—' }}
            <span class="rank-detail-sep">·</span>{{ selected.reasoning_effort || t('admin.modelIntegrity.scheduling.rank.unspecifiedEffort') }}
          </h3>
          <p class="rank-hint">{{ t(`admin.modelIntegrity.scheduling.rank.ordering.${orderingKey(selected.ordering)}`) }}</p>
          <p class="rank-hint">{{ t('admin.modelIntegrity.scheduling.rank.weights.loadAtEvaluation') }}</p>
          <p v-if="selected.valid_until" class="rank-hint">{{ t('admin.modelIntegrity.scheduling.rank.scope.validUntil', { time: formatDateTime(selected.valid_until) }) }}</p>
        </div>
        <p v-if="selected.accounts_next_cursor" class="rank-counts">{{ t('admin.modelIntegrity.scheduling.rank.table.shown', { shown: accounts.length, total: selected.candidate_count ?? accounts.length }) }}</p>
      </div>
      <!-- First-output latency is measured from real requests and scheduled
           tests. Channel monitor data stays diagnostic and never becomes it. -->
      <p class="rank-evidence-note" data-testid="ranking-v1-note">
        {{ t('admin.modelIntegrity.scheduling.rank.evidence.v1Note') }}
      </p>

      <p v-if="selected.coverage_status === 'no_candidates'" class="rank-empty">{{ t('admin.modelIntegrity.scheduling.rank.table.empty') }}</p>
      <p v-else-if="!accounts.length" class="rank-empty">{{ t('admin.modelIntegrity.scheduling.rank.table.emptyFiltered') }}</p>
      <div v-else class="overflow-x-auto">
        <table class="rank-table">
          <thead>
            <tr>
              <th scope="col" class="num">{{ t('admin.modelIntegrity.scheduling.rank.table.rank') }}</th>
              <th scope="col">{{ t('admin.modelIntegrity.scheduling.rank.table.account') }}</th>
              <th scope="col" class="num">{{ t('admin.modelIntegrity.scheduling.rank.table.score') }}</th>
              <th scope="col" class="num">{{ t('admin.modelIntegrity.scheduling.rank.table.price') }}</th>
              <th scope="col" class="num">{{ t('admin.modelIntegrity.scheduling.rank.table.error') }}</th>
              <th scope="col" class="num">{{ t('admin.modelIntegrity.scheduling.rank.table.ttft') }}</th>
              <th scope="col" class="num">{{ t('admin.modelIntegrity.scheduling.rank.table.load') }}</th>
              <th scope="col" class="num">{{ t('admin.modelIntegrity.scheduling.rank.table.quality') }}</th>
              <th scope="col">{{ t('admin.modelIntegrity.scheduling.rank.table.source') }}</th>
              <th scope="col">{{ t('admin.modelIntegrity.scheduling.rank.table.state') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in accounts" :key="row.account_id" :class="{ 'rank-row-excluded': !row.eligible, 'rank-row-first': row.rank === 1 }" data-testid="ranking-row">
              <td class="num font-semibold">
                <span v-if="row.rank != null" class="rank-rank">{{ row.rank }}</span>
                <span v-else class="text-gray-400" :title="t('admin.modelIntegrity.scheduling.rank.table.unranked')">—</span>
              </td>
              <td>
                <span class="block font-medium text-gray-900 dark:text-white">{{ row.account_name || accountLabel(row.account_id) }}</span>
                <span class="block text-[11px] text-gray-500 dark:text-gray-400">#{{ row.account_id }}<template v-if="row.upstream_models.length"> · {{ t('admin.modelIntegrity.scheduling.rank.table.modelCount', { count: row.upstream_models.length }) }}</template></span>
              </td>
              <td class="num">
                <span v-if="row.priority_score != null" class="rank-score">{{ formatScore(row.priority_score) }}</span>
                <span v-else class="text-gray-400">—</span>
              </td>
              <td class="num">
                <span v-if="row.factors.price.known" class="block">{{ row.factors.price.rate_multiplier != null ? `${formatNumber(row.factors.price.rate_multiplier)}x` : '—' }}</span>
                <span v-else class="block text-gray-500 dark:text-gray-400" data-testid="factor-unknown-price">{{ t('admin.modelIntegrity.scheduling.rank.table.unknown') }}</span>
              </td>
              <td class="num">
                <span v-if="row.factors.error_rate.known" class="block">{{ row.factors.error_rate.value != null ? formatPercent(row.factors.error_rate.value) : '—' }}</span>
                <span v-else class="block text-gray-500 dark:text-gray-400" data-testid="factor-unknown-error">{{ t('admin.modelIntegrity.scheduling.rank.table.unknown') }}</span>
              </td>
              <td class="num">
                <span v-if="row.factors.ttft.known" class="block">{{ row.factors.ttft.ms != null ? `${Math.round(row.factors.ttft.ms)} ms` : '—' }}</span>
                <span v-else class="block text-gray-500 dark:text-gray-400" data-testid="factor-unknown-ttft">{{ t('admin.modelIntegrity.scheduling.rank.table.unknown') }}</span>
              </td>
              <td class="num">
                <span v-if="row.factors.load.known" class="block">{{ row.factors.load.load_rate != null ? `${formatNumber(row.factors.load.load_rate)}%` : '—' }}</span>
                <span v-else class="block text-gray-500 dark:text-gray-400" data-testid="factor-unknown-load">{{ t('admin.modelIntegrity.scheduling.rank.table.unknown') }}</span>
              </td>
              <td class="num">
                <template v-if="assessedRatio(row) !== null">
                  <span class="block">{{ formatPercent(assessedRatio(row) as number) }}</span>
                  <span class="block text-[11px] text-gray-500 dark:text-gray-400">{{ t('admin.modelIntegrity.scheduling.rank.table.qualityCounts', qualityCounts(row)) }}</span>
                </template>
                <span v-else class="block text-gray-500 dark:text-gray-400" data-testid="ranking-quality-unknown" :title="qualityUnknownHint(row)">{{ t('admin.modelIntegrity.scheduling.rank.table.unknown') }}</span>
              </td>
              <td class="text-xs text-gray-600 dark:text-gray-300">
                <span v-if="row.factors.price.source">{{ t(`admin.modelIntegrity.scheduling.rank.source.${rankingSourceKey(row.factors.price.source)}`) }}</span>
                <span v-else class="text-gray-400">—</span>
              </td>
              <td>
                <span class="rank-state" :class="row.eligible ? 'rank-state-ok' : 'rank-state-out'">
                  {{ row.eligible ? t('admin.modelIntegrity.scheduling.rank.table.stateEligible') : t('admin.modelIntegrity.scheduling.rank.table.stateExcluded') }}
                </span>
                <ul v-if="!row.eligible && exclusionList(row).length" class="rank-exclusions">
                  <li v-for="(item, index) in exclusionList(row)" :key="`${item.code}-${index}`">
                    <span class="rank-exclusion-scope">{{ t(`admin.modelIntegrity.scheduling.rank.table.scopeLabel.${exclusionScopeKey(item.scope)}`) }}</span>
                    {{ exclusionText(item.code) }}
                  </li>
                </ul>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <div v-if="selected.accounts_next_cursor" class="rank-more">
        <button type="button" class="btn btn-secondary btn-sm" :disabled="loading" data-testid="ranking-load-more" @click="loadMore">
          <Icon name="chevronDown" size="sm" />{{ loading ? t('admin.modelIntegrity.scheduling.rank.table.loadingMore') : t('admin.modelIntegrity.scheduling.rank.table.loadMore') }}
        </button>
      </div>
      <p v-if="pageError" class="rank-error" role="alert" data-testid="ranking-page-error">{{ pageError }}</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import {
  accountsAPI,
  type OpenAIEvalRankedAccount,
  type OpenAIEvalRankingDimension,
  type OpenAIEvalRankingSnapshot,
  type OpenAIEvalRankingWeights
} from '@/api/admin/accounts'
import type { AdminGroup } from '@/types'
import {
  assessedQualityRatio,
  canAppendPage,
  coverageStatusKey,
  effectiveTone,
  EFFECTIVE_KEYS,
  exclusionKey,
  exclusionScopeKey,
  fallbackReasonKey,
  isInactiveStatus,
  isStaleEvaluation,
  mergeRankedAccounts,
  orderingKey,
  policyKey,
  qualityTestCounts,
  RANKING_FACTORS,
  rankingSourceKey,
  rankingTriggerKey,
  unknownReasonKey,
  weightsForPolicy
} from '@/views/admin/modelIntegrity/modelIntegrity'

const props = defineProps<{
  snapshot: OpenAIEvalRankingSnapshot | null
  effectiveStatus: string | null
  summaryRevision: number | null
  currentRevision: number | null
  groups: AdminGroup[]
  catalogModels: string[]
  efforts: string[]
  accountLabel: (id: number) => string
}>()

const emit = defineEmits<{ (e: 'snapshot', value: OpenAIEvalRankingSnapshot): void }>()
const { t } = useI18n()

const loading = ref(false)
const loadError = ref('')
const pageError = ref('')
/** The fully-filtered dimension whose accounts are on screen. */
const selected = ref<OpenAIEvalRankingDimension | null>(null)
const accounts = ref<OpenAIEvalRankedAccount[]>([])

const ANY = '__any__'
const groupChoice = ref('')
const modelChoice = ref('')
/** ANY keeps the filter off; '' is a real filter value meaning "not specified". */
const effortChoice = ref(ANY)

const summary = computed(() => props.snapshot?.summary ?? null)
const tone = computed(() => effectiveTone(props.effectiveStatus as never))
const statusError = computed(() => props.snapshot?.ranking_error ?? null)
const effectiveKey = computed(() =>
  props.effectiveStatus && EFFECTIVE_KEYS.includes(props.effectiveStatus as never) ? props.effectiveStatus : 'inactive_legacy_policy'
)
const triggerKey = computed(() => rankingTriggerKey(summary.value?.trigger))
const stale = computed(() => isStaleEvaluation(props.summaryRevision, props.currentRevision))
const dimensions = computed(() => props.snapshot?.dimensions ?? [])

const offBody = computed(() =>
  isInactiveStatus(props.effectiveStatus as never)
    ? t('admin.modelIntegrity.scheduling.evaluation.effectsOffBody')
    : ''
)

const nextReasonText = computed(() => {
  if (!summary.value?.next_evaluation_at) return ''
  return `${t('admin.modelIntegrity.scheduling.rank.nextAt', { time: formatDateTime(summary.value.next_evaluation_at) })} (${t(`admin.modelIntegrity.scheduling.rank.nextReason.${summary.value.next_evaluation_reason}`)})`
})

/** Group options prefer the groups the evaluation itself reported. */
const groupOptions = computed(() => {
  const byId = new Map<number, { id: number; name: string }>()
  for (const group of props.groups) byId.set(group.id, { id: group.id, name: group.name })
  for (const group of (props.snapshot?.groups ?? [])) if (group.group_id != null && !byId.has(group.group_id)) byId.set(group.group_id, { id: group.group_id, name: group.group_name })
  for (const dimension of dimensions.value) if (dimension.group_id != null && !byId.has(dimension.group_id)) byId.set(dimension.group_id, { id: dimension.group_id, name: dimension.group_name })
  return [...byId.values()].sort((a, b) => a.name.localeCompare(b.name))
})

/** Models come from the catalog; any extra model the evaluation produced is added. */
const modelOptions = computed(() => {
  const models = new Set<string>(props.catalogModels.filter(Boolean))
  for (const dimension of dimensions.value) if (dimension.requested_model) models.add(dimension.requested_model)
  return [...models].sort()
})

const effortOptions = computed(() => {
  const efforts = new Set<string>(props.efforts.filter(effort => effort !== ''))
  for (const dimension of dimensions.value) if (dimension.reasoning_effort) efforts.add(dimension.reasoning_effort)
  return [...efforts].sort()
})

async function refresh(query?: Record<string, unknown>) {
  if (loading.value) return
  loading.value = true
  loadError.value = ''
  try {
    const result = await accountsAPI.getOpenAIEvalRankings(query ?? { limit: 200 })
    emit('snapshot', result)
    return result
  } catch (error) {
    loadError.value = extractMessage(error, t('admin.modelIntegrity.scheduling.rank.loadFailed'))
    return null
  } finally {
    loading.value = false
  }
}

/**
 * Loads the ranked accounts for one scope. The server returns the whole
 * dimension here; the ranking itself is never recomputed in the browser.
 */
async function inspect(dimension: OpenAIEvalRankingDimension) {
  selected.value = dimension
  accounts.value = []
  pageError.value = ''
  const result = await refresh({
    group_id: dimension.group_id ?? 0,
    requested_model: dimension.requested_model,
    reasoning_effort: dimension.reasoning_effort,
    limit: 200
  })
  if (!result) return
  const match = result.dimensions.find(item => item.dimension_id === dimension.dimension_id) ?? result.dimensions[0]
  if (match) {
    selected.value = match
    accounts.value = match.accounts ?? []
  }
}

/**
 * Reads the next page. A cursor belongs to one evaluation generation, so a
 * page from a different generation restarts the list instead of being
 * appended to a build that no longer exists.
 */
async function loadMore() {
  const dimension = selected.value
  const cursor = dimension?.accounts_next_cursor
  if (!dimension || !cursor || loading.value) return
  const before = props.snapshot
  loading.value = true
  pageError.value = ''
  try {
    const result = await accountsAPI.getOpenAIEvalRankingAccounts({
      group_id: dimension.group_id ?? 0,
      requested_model: dimension.requested_model,
      reasoning_effort: dimension.reasoning_effort,
      cursor,
      limit: 200
    })
    emit('snapshot', result)
    const match = result.dimensions.find(item => item.dimension_id === dimension.dimension_id) ?? result.dimensions[0]
    if (!match) return
    // A cursor is bound to the published generation, which the summary names.
    const generation = (value: OpenAIEvalRankingSnapshot | null) => value?.summary?.evaluation_id ?? ''
    const sameGeneration = Boolean(before?.summary?.evaluation_id) && canAppendPage(
      { evaluation_id: generation(before), group_id: dimension.group_id, requested_model: dimension.requested_model, reasoning_effort: dimension.reasoning_effort },
      { evaluation_id: generation(result), group_id: match.group_id, requested_model: match.requested_model, reasoning_effort: match.reasoning_effort }
    )
    accounts.value = sameGeneration ? mergeRankedAccounts(accounts.value, match.accounts ?? []) : (match.accounts ?? [])
    selected.value = match
  } catch (error) {
    // A reclaimed generation answers 409; say so and let the next load restart.
    pageError.value = extractMessage(error, t('admin.modelIntegrity.scheduling.rank.loadFailed'))
  } finally {
    loading.value = false
  }
}

watch(() => props.snapshot, value => {
  if (!value) return
  // Keep the open scope in sync with the newly published build, or drop it when
  // the build no longer contains that scope.
  if (!selected.value) return
  const match = value.dimensions.find(item => item.dimension_id === selected.value?.dimension_id)
  if (!match) {
    selected.value = null
    accounts.value = []
    return
  }
  selected.value = match
  if (!match.accounts?.length) return
  accounts.value = match.accounts
})

function isInspecting(dimension: OpenAIEvalRankingDimension) {
  return selected.value?.dimension_id === dimension.dimension_id
}

function weightedFactors(dimension: OpenAIEvalRankingDimension): (keyof OpenAIEvalRankingWeights)[] {
  return RANKING_FACTORS.filter(factor => (dimension.weights[factor] ?? 0) > 0)
}

function weightsSummary(dimension: OpenAIEvalRankingDimension) {
  const weights = weightsForPolicy(dimension.policy, null) ?? dimension.weights
  return RANKING_FACTORS.map(factor => `${t(`admin.modelIntegrity.scheduling.rank.weights.${factor}`)} ${Math.round((weights[factor] ?? 0) * 100)}%`).join(', ')
}

function fallbackText(dimension: OpenAIEvalRankingDimension) {
  const key = fallbackReasonKey(dimension.fallback_reason)
  return t(`admin.modelIntegrity.scheduling.rank.fallbackReason.${key}`)
}

function assessedRatio(row: OpenAIEvalRankedAccount) {
  return assessedQualityRatio(row.factors)
}

function qualityCounts(row: OpenAIEvalRankedAccount) {
  const counts = qualityTestCounts(row.factors)
  return { passed: counts.passed, selected: counts.selected }
}

/** The reason the pass rate is unknown, so an empty cell is never read as healthy. */
function qualityUnknownHint(row: OpenAIEvalRankedAccount) {
  const reason = row.factors.quality.unknown_reason
  return `${t('admin.modelIntegrity.scheduling.rank.table.unknownHint')} ${t(`admin.modelIntegrity.scheduling.rank.unknown.${unknownReasonKey(reason)}`)}`
}

function exclusionList(row: OpenAIEvalRankedAccount) {
  if (row.exclusion_reasons?.length) return row.exclusion_reasons
  if (!row.exclusion_reason) return []
  return [{ code: row.exclusion_reason, scope: 'route', observed_at: '' }]
}

function exclusionText(code: string) {
  return t(`admin.modelIntegrity.scheduling.exclusion.${exclusionKey(code)}`, { code })
}

function extractMessage(error: unknown, fallback: string) {
  const message = (error as { response?: { data?: { error?: { message?: string }; message?: string } }; message?: string })?.response?.data?.error?.message ??
    (error as { response?: { data?: { message?: string } } })?.response?.data?.message ??
    (error as { message?: string })?.message
  return message?.trim() ? message : fallback
}

function formatDateTime(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

const formatNumber = (value: number) => (Math.abs(value) >= 100 ? value.toFixed(0) : Number(value.toFixed(3)).toString())
const formatScore = (value: number) => value.toFixed(1)
const formatPercent = (value: number) => `${(value * 100).toFixed(1)}%`
</script>

<style scoped>
.rank-board { @apply space-y-4; }
.rank-status { @apply rounded-lg border px-4 py-3; }
.rank-status-active { @apply border-primary-200 bg-primary-50/60 dark:border-primary-900 dark:bg-primary-950/20; }
.rank-status-partial { @apply border-violet-200 bg-violet-50/60 dark:border-violet-900 dark:bg-violet-950/20; }
.rank-status-off { @apply border-gray-200 bg-gray-50 dark:border-dark-600 dark:bg-dark-900/50; }
.rank-status-error { @apply border-rose-300 bg-rose-50 dark:border-rose-900 dark:bg-rose-950/30; }
.rank-status-pill { @apply inline-flex items-center rounded-full px-2 py-0.5 text-xs font-semibold; }
.rank-status-active .rank-status-pill { @apply bg-primary-600 text-white dark:bg-primary-500; }
.rank-status-partial .rank-status-pill { @apply bg-violet-600 text-white dark:bg-violet-500; }
.rank-status-off .rank-status-pill { @apply bg-gray-200 text-gray-700 dark:bg-dark-700 dark:text-gray-300; }
.rank-status-error .rank-status-pill { @apply bg-rose-600 text-white dark:bg-rose-500; }
.rank-status-body { @apply mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs tabular-nums text-gray-700 dark:text-gray-300; }
.rank-status-note { @apply mt-2 max-w-[80ch] text-xs leading-relaxed text-gray-600 dark:text-gray-400; }
.rank-status-error { @apply mt-2 break-words text-xs text-rose-800 [overflow-wrap:anywhere] dark:text-rose-300; }
.rank-filters { @apply flex flex-wrap items-end gap-3; }
.rank-field { @apply flex min-w-0 flex-col gap-1; }
.rank-label { @apply text-xs text-gray-500 dark:text-gray-400; }
.rank-input { @apply h-9 w-auto min-w-[11rem] py-1 text-sm; }
.rank-error { @apply break-words text-xs text-rose-700 [overflow-wrap:anywhere] dark:text-rose-300; }
.rank-stale { @apply rounded-md bg-amber-50 px-3 py-2 text-xs leading-relaxed text-amber-900 dark:bg-amber-950/30 dark:text-amber-200; }
.rank-scopes { @apply space-y-3 rounded-xl border border-gray-200 bg-white p-5 dark:border-dark-700 dark:bg-dark-800/60 sm:p-6; }
.rank-scopes-head { @apply space-y-1; }
.rank-h3 { @apply text-sm font-semibold text-gray-900 dark:text-white; }
.rank-hint { @apply max-w-[72ch] text-xs leading-relaxed text-gray-600 dark:text-gray-400; }
.rank-empty { @apply rounded-lg border border-dashed border-gray-300 px-4 py-6 text-center text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400; }
.rank-scope-table { @apply w-full min-w-[64rem] text-left text-xs; }
.rank-scope-table th { @apply whitespace-nowrap border-b border-gray-200 px-2 py-2 font-medium text-gray-500 dark:border-dark-700 dark:text-gray-400; }
.rank-scope-table td { @apply border-b border-gray-100 px-2 py-2 align-top text-gray-700 dark:border-dark-800 dark:text-gray-300; }
.rank-scope-table tr:last-child td { @apply border-b-0; }
.rank-scope-table .num { @apply whitespace-nowrap text-right tabular-nums; }
.rank-scope-active td { @apply bg-primary-50/50 dark:bg-primary-950/20; }
.rank-weights { @apply flex flex-wrap gap-1; }
.rank-weight-chip { @apply inline-flex whitespace-nowrap rounded bg-gray-100 px-1.5 py-0.5 text-[11px] tabular-nums text-gray-700 dark:bg-dark-700 dark:text-gray-300; }
.rank-weight-tier { @apply text-[11px] leading-snug text-violet-700 dark:text-violet-300; }
.rank-sources { @apply flex flex-wrap gap-1; }
.rank-source { @apply inline-flex whitespace-nowrap rounded border border-gray-200 px-1.5 py-0.5 text-[11px] text-gray-600 dark:border-dark-600 dark:text-gray-400; }
.rank-coverage { @apply inline-block whitespace-nowrap rounded px-1.5 py-0.5 font-medium; }
.rank-coverage-complete { @apply bg-primary-100 text-primary-800 dark:bg-primary-900/50 dark:text-primary-200; }
.rank-coverage-live_fallback { @apply bg-violet-100 text-violet-800 dark:bg-violet-900/50 dark:text-violet-200; }
.rank-coverage-no_candidates { @apply bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300; }
.rank-coverage-legacy { @apply bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300; }
.rank-scope-reason { @apply mt-1 max-w-[22rem] text-[11px] leading-snug text-gray-500 dark:text-gray-400; }
.rank-detail { @apply space-y-3 rounded-xl border border-gray-200 bg-white p-5 dark:border-dark-700 dark:bg-dark-800/60 sm:p-6; }
.rank-detail-head { @apply flex flex-wrap items-start justify-between gap-3; }
.rank-detail-sep { @apply px-1 text-gray-400; }
.rank-counts { @apply text-xs tabular-nums text-gray-500 dark:text-gray-400; }
.rank-table { @apply w-full min-w-[62rem] text-left text-xs; }
.rank-table th { @apply whitespace-nowrap border-b border-gray-200 px-2 py-2 font-medium text-gray-500 dark:border-dark-700 dark:text-gray-400; }
.rank-table td { @apply border-b border-gray-100 px-2 py-2 align-top text-gray-700 dark:border-dark-800 dark:text-gray-300; }
.rank-table tr:last-child td { @apply border-b-0; }
.rank-table .num { @apply whitespace-nowrap text-right tabular-nums; }
.rank-row-excluded td { @apply text-gray-500 dark:text-gray-400; }
.rank-row-first td { @apply bg-primary-50/40 dark:bg-primary-950/20; }
.rank-rank { @apply inline-flex h-6 min-w-6 items-center justify-center rounded-full bg-gray-100 px-1.5 font-semibold text-gray-800 dark:bg-dark-700 dark:text-gray-200; }
.rank-row-first .rank-rank { @apply bg-primary-600 text-white dark:bg-primary-500; }
.rank-score { @apply font-semibold text-gray-900 dark:text-white; }
.rank-state { @apply inline-block whitespace-nowrap rounded px-1.5 py-0.5 font-medium; }
.rank-state-ok { @apply bg-primary-100 text-primary-800 dark:bg-primary-900/50 dark:text-primary-200; }
.rank-state-out { @apply bg-rose-50 text-rose-700 dark:bg-rose-950/40 dark:text-rose-300; }
.rank-exclusions { @apply mt-1 space-y-0.5 text-[11px] leading-snug text-gray-600 dark:text-gray-400; }
.rank-exclusions li { @apply max-w-[24rem]; }
.rank-exclusion-scope { @apply mr-1 rounded bg-gray-100 px-1 py-0.5 text-[10px] font-medium uppercase tracking-wide text-gray-500 dark:bg-dark-700 dark:text-gray-400; }
.rank-more { @apply flex justify-center pt-1; }
.rank-evidence-note { @apply max-w-[86ch] text-[11px] leading-relaxed text-gray-500 dark:text-gray-400; }
</style>
