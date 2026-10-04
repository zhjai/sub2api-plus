<template>
  <div class="lb">
    <div class="lb-toolbar">
      <label class="lb-field">
        <span class="lb-label">{{ t('admin.modelIntegrity.scheduling.board.group') }}</span>
        <select v-model="groupChoice" class="input lb-select" data-testid="board-group">
          <option value="">{{ t('admin.modelIntegrity.scheduling.board.allGroups') }}</option>
          <option v-for="group in groupOptions" :key="group.id" :value="String(group.id)">{{ group.name }}</option>
          <option value="0">{{ t('admin.modelIntegrity.scheduling.board.ungrouped') }}</option>
        </select>
      </label>
      <p class="lb-meta" data-testid="board-meta">
        <span v-if="countText" data-testid="board-count">{{ countText }}</span>
        <span v-if="policyName">{{ t('admin.modelIntegrity.scheduling.board.rankedBy', { policy: policyName }) }}</span>
        <span v-if="summary">{{ t('admin.modelIntegrity.scheduling.board.evaluatedAt', { time: formatDateTime(summary.published_at || summary.evaluated_at) }) }}</span>
      </p>
      <button type="button" class="btn btn-secondary btn-sm lb-refresh" :disabled="loading" :aria-busy="loading ? 'true' : undefined" data-testid="board-refresh" @click="reload()">
        <Icon name="refresh" size="sm" :class="loading ? 'motion-safe:animate-spin' : ''" />{{ t('admin.modelIntegrity.common.refresh') }}
      </button>
    </div>

    <p v-if="orderingText" class="lb-note" data-testid="board-ordering">{{ orderingText }}</p>
    <p v-if="groupId !== null" class="lb-note" data-testid="board-filter-note">{{ t('admin.modelIntegrity.scheduling.board.filterNote') }}</p>
    <p v-if="notice" class="lb-warn" role="status" data-testid="board-notice">{{ notice }}</p>
    <div v-if="loadError" class="lb-error" role="alert" data-testid="board-error">
      <p>{{ loadError }}</p>
      <p v-if="rowsKept" data-testid="board-rows-kept">{{ rowsKeptText }}</p>
    </div>
    <p v-if="stale" class="lb-warn" role="note" data-testid="board-stale">
      {{ t('admin.modelIntegrity.scheduling.rank.stale', { built: summary?.config_revision, current: currentRevision }) }}
    </p>

    <p v-if="!rows.length && emptyText" class="lb-empty" :role="loading ? 'status' : undefined" data-testid="board-empty">{{ emptyText }}</p>

    <div v-else-if="rows.length" class="lb-table" :class="{ 'lb-table-dim': loading }">
      <div class="lb-head" aria-hidden="true">
        <span class="lb-h lb-h-num">{{ t('admin.modelIntegrity.scheduling.board.columns.rank') }}</span>
        <span class="lb-h">{{ t('admin.modelIntegrity.scheduling.board.columns.account') }}</span>
        <span class="lb-h lb-h-num">{{ qualityFirst ? t('admin.modelIntegrity.scheduling.board.columns.qualityFirst') : t('admin.modelIntegrity.scheduling.board.columns.score') }}</span>
        <span v-for="factor in RANKING_FACTORS" :key="factor" class="lb-h lb-h-num">{{ factorLabel(factor) }}</span>
        <span class="lb-h"><span class="sr-only">{{ t('admin.modelIntegrity.scheduling.board.columns.details') }}</span></span>
      </div>
      <ol class="lb-rows">
        <li v-for="row in rows" :key="row.account_id" class="lb-row" :class="{ 'lb-row-first': row.rank === 1, 'lb-row-out': !row.eligible }" data-testid="board-row" :data-account-id="row.account_id">
          <div class="lb-line">
            <div class="lb-rank">
              <span v-if="row.rank != null" class="lb-rank-num" data-testid="board-rank">{{ row.rank }}</span>
              <span v-else class="lb-muted" :title="t('admin.modelIntegrity.scheduling.rank.table.unranked')">—</span>
            </div>

            <div class="lb-account">
              <p class="lb-account-name">{{ row.account_name || accountLabel(row.account_id) }}</p>
              <p class="lb-account-sub">
                <span>#{{ row.account_id }}</span>
                <span v-for="name in groupNames(row)" :key="name" class="lb-group">{{ name }}</span>
                <span v-if="!row.eligible" class="lb-out" data-testid="board-ineligible">{{ t('admin.modelIntegrity.scheduling.board.ineligible') }}</span>
              </p>
              <p class="lb-account-sub" data-testid="board-coverage">{{ coverageText(row) }}</p>
            </div>

            <div class="lb-score" data-testid="board-score">
              <template v-if="qualityFirst">
                <span class="lb-score-main" :class="{ 'lb-score-unknown': accountQuality(row) === null }" data-testid="board-score-quality">
                  {{ accountQuality(row) === null ? t('admin.modelIntegrity.scheduling.board.qualityUnknown') : formatPercent(accountQuality(row) as number) }}
                </span>
                <span class="lb-score-sub" data-testid="board-score-operational">{{ t('admin.modelIntegrity.scheduling.board.operationalScore', { score: formatScore(operationalScore(row)) }) }}</span>
              </template>
              <template v-else>
                <span class="lb-score-main" data-testid="board-score-composite">{{ formatScore(row.priority_score) }}</span>
                <span class="lb-score-sub">{{ t('admin.modelIntegrity.scheduling.board.compositeOf') }}</span>
              </template>
            </div>

            <!-- display: contents on desktop, a compact three-column grid when stacked. -->
            <div class="lb-factors">
              <div v-for="factor in RANKING_FACTORS" :key="factor" class="lb-factor" :data-testid="`board-factor-${factor}`" :data-source="cellOf(row, factor).kind">
                <span class="lb-factor-label">{{ factorLabel(factor) }}</span>
                <span class="lb-factor-raw" :class="{ 'lb-muted': cellOf(row, factor).raw === null }">{{ cellOf(row, factor).rawText }}</span>
                <span class="lb-factor-meta">
                  <span v-if="cellOf(row, factor).scoreText" class="tabular-nums">{{ cellOf(row, factor).scoreText }}</span>
                  <span class="lb-source" :class="`lb-source-${cellOf(row, factor).kind}`" :title="sourceHint(cellOf(row, factor).kind)">{{ sourceLabel(cellOf(row, factor).kind) }}</span>
                </span>
              </div>
            </div>

            <div class="lb-toggle-cell">
              <button
                type="button"
                class="lb-toggle"
                :aria-expanded="expanded.has(row.account_id)"
                :aria-controls="`board-detail-${row.account_id}`"
                data-testid="board-toggle"
                @click="toggle(row.account_id)"
              >
                <span class="lb-toggle-text">{{ expanded.has(row.account_id) ? t('admin.modelIntegrity.scheduling.board.hideDetails') : t('admin.modelIntegrity.scheduling.board.showDetails') }}</span>
                <Icon name="chevronDown" size="sm" :class="expanded.has(row.account_id) ? 'rotate-180' : ''" />
              </button>
            </div>
          </div>

          <div v-if="expanded.has(row.account_id)" :id="`board-detail-${row.account_id}`" class="lb-detail" data-testid="board-detail">
            <p class="lb-detail-line">{{ coverageDetail(row) }}</p>
            <p v-if="row.quality_cell_count != null" class="lb-detail-line" data-testid="board-cells">
              {{ t('admin.modelIntegrity.scheduling.board.cells', { known: row.quality_cell_count, unknown: row.unknown_quality_cell_count ?? 0 }) }}
            </p>
            <p v-if="row.worst_quality_model" class="lb-detail-line" data-testid="board-worst">
              {{ t('admin.modelIntegrity.scheduling.board.worst', { model: row.worst_quality_model, ratio: row.worst_quality_ratio == null ? t('admin.modelIntegrity.scheduling.board.qualityUnknown') : formatPercent(row.worst_quality_ratio) }) }}
            </p>
            <p v-if="contributionParts(row).length" class="lb-detail-line" data-testid="board-contributions">
              <span class="lb-detail-key">{{ t('admin.modelIntegrity.scheduling.board.contributions') }}</span>
              <span v-for="part in contributionParts(row)" :key="part.factor" class="lb-chip">{{ part.text }}</span>
            </p>
            <ul v-if="exceptions(row).length" class="lb-list" data-testid="board-exceptions">
              <li v-for="item in exceptions(row)" :key="`${item.model}-${item.effort}`">
                {{ t('admin.modelIntegrity.scheduling.board.ruleException', { model: item.model, effort: item.effort || (item.anyEffort ? t('admin.modelIntegrity.common.allEfforts') : t('admin.modelIntegrity.scheduling.rank.unspecifiedEffort')), policy: policyLabel(item.policy) }) }}
              </li>
            </ul>
            <ul v-if="exclusionList(row).length" class="lb-list" data-testid="board-exclusions">
              <li v-for="(item, index) in exclusionList(row)" :key="`${item.code}-${index}`">
                {{ t(`admin.modelIntegrity.scheduling.board.exclusionScope.${exclusionScopeKey(item.scope)}`) }}{{ exclusionText(item.code) }}
              </li>
            </ul>
            <ul v-if="row.factors.monitoring?.length" class="lb-list" data-testid="board-probes">
              <li v-for="probe in row.factors.monitoring" :key="probe.monitor_id">
                {{ t('admin.modelIntegrity.scheduling.board.probe', { model: probe.model, time: formatDateTime(probe.observed_at) }) }}
                <template v-if="probe.latency_ms != null"> {{ t('admin.modelIntegrity.scheduling.board.probeLatency', { ms: probe.latency_ms }) }}</template>
              </li>
            </ul>

            <p v-if="!row.models.length" class="lb-detail-line lb-muted" data-testid="board-no-models">{{ t('admin.modelIntegrity.scheduling.board.noModels') }}</p>
            <div v-else class="lb-models-wrap">
              <table class="lb-models" data-testid="board-models">
                <thead>
                  <tr>
                    <th scope="col">{{ t('admin.modelIntegrity.scheduling.board.models.model') }}</th>
                    <th scope="col">{{ t('admin.modelIntegrity.scheduling.board.models.effort') }}</th>
                    <th scope="col">{{ t('admin.modelIntegrity.scheduling.board.models.upstream') }}</th>
                    <th v-for="factor in MODEL_FACTORS" :key="factor" scope="col" class="num">{{ factorLabel(factor) }}</th>
                    <th scope="col">{{ t('admin.modelIntegrity.scheduling.board.models.policy') }}</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="model in row.models" :key="`${model.requested_model}\u0000${model.reasoning_effort}`" data-testid="board-model-row">
                    <td class="font-medium text-gray-900 dark:text-white">{{ model.requested_model }}</td>
                    <td>{{ model.reasoning_effort || t('admin.modelIntegrity.scheduling.rank.unspecifiedEffort') }}</td>
                    <td class="lb-upstream">{{ model.upstream_models.length ? model.upstream_models.join(', ') : '—' }}</td>
                    <td v-for="factor in MODEL_FACTORS" :key="factor" class="num" :data-testid="`board-model-${factor}`">
                      <span class="block" :class="{ 'lb-muted': modelCell(model, factor).raw === null }">{{ modelCell(model, factor).rawText }}</span>
                      <span class="lb-source" :class="`lb-source-${modelCell(model, factor).kind}`" :title="sourceHint(modelCell(model, factor).kind)">{{ sourceLabel(modelCell(model, factor).kind) }}</span>
                    </td>
                    <td data-testid="board-model-policy">{{ modelPolicyText(model) }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>
        </li>
      </ol>
    </div>

    <div v-if="cursor && rows.length" class="lb-more">
      <span class="lb-meta">{{ t('admin.modelIntegrity.scheduling.board.shown', { shown: rows.length }) }}</span>
      <button type="button" class="btn btn-secondary btn-sm" :disabled="loading || loadingMore" data-testid="board-more" @click="loadMore">
        <Icon name="chevronDown" size="sm" />{{ loadingMore ? t('admin.modelIntegrity.scheduling.rank.table.loadingMore') : t('admin.modelIntegrity.scheduling.board.loadMore') }}
      </button>
    </div>
    <p v-if="pageError" class="lb-error" role="alert" data-testid="board-page-error">{{ pageError }}</p>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import {
  accountsAPI,
  type OpenAIEvalAccountOverview,
  type OpenAIEvalOverviewAccount,
  type OpenAIEvalOverviewModel,
  type OpenAIEvalRankingFactorKey,
  type OpenAIEvalRankingFactors,
  type OpenAIEvalRankingSummary,
  type OpenAIEvalSchedulingPolicy,
  type OpenAIEvalSchedulingPolicyRule
} from '@/api/admin/accounts'
import type { AdminGroup } from '@/types'
import {
  accountFactorSourceKind,
  exclusionKey,
  exclusionScopeKey,
  factorRawValue,
  factorSourceKind,
  isQualityFirst,
  isRankingSnapshotChanged,
  isStaleEvaluation,
  mergeRankedAccounts,
  orderingKey,
  OVERVIEW_PAGE_SIZE,
  policyKey,
  RANKING_FACTORS,
  rankingErrorText,
  ruleExceptionsFor,
  sameOverviewBinding,
  type FactorSourceKind,
  type OverviewBinding,
  type RankingReadOutcome
} from '@/views/admin/modelIntegrity/modelIntegrity'

/** Per-model factors; price and load are account facts and are shown once on the row. */
const MODEL_FACTORS: OpenAIEvalRankingFactorKey[] = ['error_rate', 'ttft', 'quality']

const props = defineProps<{
  groups: AdminGroup[]
  accountLabel: (id: number) => string
  /** Saved model rules, shown as exceptions to the overview order. */
  rules: OpenAIEvalSchedulingPolicyRule[]
  currentRevision: number | null
}>()

const emit = defineEmits<{ (e: 'overview', value: OpenAIEvalAccountOverview): void }>()
const { t } = useI18n()

/** '' is every group; otherwise a group id. */
const groupChoice = ref('')
const groupId = computed<number | null>(() => (groupChoice.value === '' ? null : Number(groupChoice.value)))

const rows = ref<OpenAIEvalOverviewAccount[]>([])
/** The generation and filter the rows on screen came from. */
const binding = ref<OverviewBinding | null>(null)
/** The last read's metadata; it describes the rows, never another generation. */
const meta = ref<OpenAIEvalAccountOverview | null>(null)
const cursor = ref<string | null>(null)
const loading = ref(false)
const loadingMore = ref(false)
const loadError = ref('')
/** True when a failed read left earlier rows on screen. */
const rowsKept = ref(false)
/** True when the kept rows are known to belong to an older evaluation. */
const keptSuperseded = ref(false)
const pageError = ref('')
const notice = ref('')
const expanded = reactive(new Set<number>())
/** Every group name a read reported, so a filter never shows a bare id. */
const seenGroups = ref(new Map<number, string>())

// Each first-page read takes a ticket; an answer to an older ticket is dropped.
let ticket = 0
let latestRead: Promise<RankingReadOutcome> | null = null
/** The generation whose pages were last discarded, so a refusal is retried once only. */
let discarded: string | null = null

const summary = computed<OpenAIEvalRankingSummary | null>(() => meta.value?.summary ?? null)
const qualityFirst = computed(() => isQualityFirst(meta.value?.ordering, meta.value?.policy))
const stale = computed(() => isStaleEvaluation(summary.value?.config_revision, props.currentRevision))
const policyName = computed(() => (meta.value ? policyLabel(meta.value.policy) : ''))
const orderingText = computed(() => {
  if (!meta.value || !rows.value.length) return ''
  return t(`admin.modelIntegrity.scheduling.board.ordering.${orderingKey(meta.value.ordering)}`)
})

const groupOptions = computed(() => {
  const byId = new Map<number, string>()
  for (const group of props.groups) byId.set(group.id, group.name)
  for (const [id, name] of seenGroups.value) if (!byId.has(id)) byId.set(id, name)
  // 0 is the ungrouped scope, which has its own option.
  byId.delete(0)
  return [...byId.entries()].map(([id, name]) => ({ id, name })).sort((a, b) => a.name.localeCompare(b.name))
})

const countText = computed(() => {
  if (!rows.value.length) return ''
  const total = summary.value?.account_count
  if (groupId.value === null && total != null) return t('admin.modelIntegrity.scheduling.board.count', { count: total })
  return t('admin.modelIntegrity.scheduling.board.countFiltered', { count: rows.value.length, more: cursor.value ? '+' : '' })
})

const rowsKeptText = computed(() => keptSuperseded.value
  ? t('admin.modelIntegrity.scheduling.board.rowsKeptPrevious')
  : t('admin.modelIntegrity.scheduling.board.rowsKept'))

const emptyText = computed(() => {
  if (loading.value) return t('admin.modelIntegrity.scheduling.board.loading')
  // After a failed read nothing is known, so no empty claim is made.
  if (loadError.value) return ''
  if (!meta.value) return ''
  if (!meta.value.summary) return t('admin.modelIntegrity.scheduling.board.none')
  return groupId.value === null ? t('admin.modelIntegrity.scheduling.board.empty') : t('admin.modelIntegrity.scheduling.board.emptyGroup')
})

function remember(page: OpenAIEvalAccountOverview) {
  for (const group of page.groups ?? []) if (group.group_id != null && group.group_name) seenGroups.value.set(group.group_id, group.group_name)
}

function clearRows() {
  rows.value = []
  binding.value = null
  cursor.value = null
  rowsKept.value = false
  keptSuperseded.value = false
  expanded.clear()
}

/**
 * Reads the first page for the current group. `invalidate` says the rows on
 * screen are known to be superseded (an evaluation or save just published);
 * they stay visible, labelled, until the new page replaces them or the read
 * fails, so an error never blanks the last good result.
 */
function reload(options: { invalidate?: boolean; notice?: string } = {}): Promise<RankingReadOutcome> {
  discarded = null
  return track(options)
}

function track(options: { invalidate?: boolean; notice?: string }): Promise<RankingReadOutcome> {
  const read = readFirst(options)
  latestRead = read
  return read
}

async function readFirst(options: { invalidate?: boolean; notice?: string }): Promise<RankingReadOutcome> {
  const own = ++ticket
  const group = groupId.value
  notice.value = options.notice ?? ''
  loading.value = true
  loadingMore.value = false
  loadError.value = ''
  pageError.value = ''
  rowsKept.value = false
  // Rows from another group are never shown under this filter.
  if (binding.value && binding.value.groupId !== group) clearRows()
  try {
    const page = await accountsAPI.getOpenAIEvalAccountOverview({ ...(group !== null ? { group_id: group } : {}), limit: OVERVIEW_PAGE_SIZE })
    if (own !== ticket) return latestRead ?? { ok: true, evaluationId: page.summary?.evaluation_id ?? '' }
    const evaluationId = page.summary?.evaluation_id ?? ''
    const next: OverviewBinding = { evaluationId, groupId: group }
    if (!sameOverviewBinding(binding.value, next)) expanded.clear()
    remember(page)
    meta.value = page
    rows.value = page.accounts ?? []
    binding.value = next
    cursor.value = page.next_cursor ?? null
    keptSuperseded.value = false
    loading.value = false
    emit('overview', page)
    return { ok: true, evaluationId }
  } catch (error) {
    if (own !== ticket) return latestRead ?? { ok: false, message: '' }
    loading.value = false
    const message = rankingErrorText(error, t('admin.modelIntegrity.scheduling.rank.loadFailed'))
    loadError.value = t('admin.modelIntegrity.scheduling.board.loadFailed', { reason: message })
    if (rows.value.length && binding.value?.groupId === group) {
      rowsKept.value = true
      keptSuperseded.value = keptSuperseded.value || Boolean(options.invalidate)
    } else {
      clearRows()
    }
    return { ok: false, message }
  }
}

/**
 * Reads the next page of the same generation and group. The server either
 * continues that build or refuses it; two builds are never spliced together.
 */
async function loadMore() {
  const bound = binding.value
  const next = cursor.value
  if (!bound || !next || loading.value || loadingMore.value) return
  if (bound.groupId !== groupId.value) return reload()
  const own = ticket
  loadingMore.value = true
  pageError.value = ''
  try {
    const page = await accountsAPI.getOpenAIEvalAccountOverview({
      ...(bound.groupId !== null ? { group_id: bound.groupId } : {}),
      ...(bound.evaluationId ? { evaluation_id: bound.evaluationId } : {}),
      cursor: next,
      limit: OVERVIEW_PAGE_SIZE
    })
    // A newer first-page read owns the list now.
    if (own !== ticket) return
    loadingMore.value = false
    if ((page.summary?.evaluation_id ?? '') !== bound.evaluationId) return restart(bound.evaluationId)
    remember(page)
    rows.value = mergeRankedAccounts(rows.value, page.accounts ?? [])
    cursor.value = page.next_cursor ?? null
  } catch (error) {
    if (own !== ticket) return
    loadingMore.value = false
    if (isRankingSnapshotChanged(error)) return restart(bound.evaluationId)
    pageError.value = rankingErrorText(error, t('admin.modelIntegrity.scheduling.rank.loadFailed'))
  }
}

/** The pages on screen belong to a generation the server no longer serves. */
function restart(stale: string) {
  // Refused twice for the same generation: say so instead of looping.
  if (discarded === stale) {
    pageError.value = t('admin.modelIntegrity.scheduling.board.restartFailed')
    return
  }
  discarded = stale
  void track({ invalidate: true, notice: t('admin.modelIntegrity.scheduling.board.restarted') })
}

watch(groupId, () => {
  void reload()
})

onMounted(() => {
  void reload()
})

defineExpose({ reload })

function toggle(id: number) {
  if (expanded.has(id)) expanded.delete(id)
  else expanded.add(id)
}

function factorLabel(factor: OpenAIEvalRankingFactorKey) {
  return t(`admin.modelIntegrity.scheduling.board.factors.${factor}`)
}

function policyLabel(policy: OpenAIEvalSchedulingPolicy | null | undefined) {
  return t(`admin.modelIntegrity.scheduling.policy.options.${policyKey(policy ?? '')}.name`)
}

function groupNames(row: OpenAIEvalOverviewAccount) {
  const names = (row.group_ids ?? []).map(id => id === 0
    ? t('admin.modelIntegrity.scheduling.board.ungrouped')
    : groupOptions.value.find(group => group.id === id)?.name ?? `#${id}`)
  return names.length > 3 ? [...names.slice(0, 3), t('admin.modelIntegrity.scheduling.board.moreGroups', { count: names.length - 3 })] : names
}

/**
 * The account's macro pass rate, only when the server marked it known; older
 * payloads without `priority` fall back to the quality factor.
 */
function accountQuality(row: OpenAIEvalOverviewAccount): number | null {
  if (row.priority) return row.priority.quality_known ? row.priority.quality_ratio : null
  return factorRawValue('quality', row.factors)
}

/** The weighted operational score, which alone never decides quality-first order. */
function operationalScore(row: OpenAIEvalOverviewAccount): number | null {
  return row.priority?.operational_score ?? row.priority_score
}

interface Cell {
  kind: FactorSourceKind
  raw: number | null
  rawText: string
  scoreText: string
}

/** Whether a factor counts toward the score under the ranked policy. */
function weighted(factor: OpenAIEvalRankingFactorKey) {
  if (factor === 'quality' && qualityFirst.value) return true
  return (meta.value?.weights?.[factor] ?? 0) > 0
}

function buildCell(factor: OpenAIEvalRankingFactorKey, factors: OpenAIEvalRankingFactors, kind: FactorSourceKind, withScore: boolean): Cell {
  const raw = factorRawValue(factor, factors)
  let rawText: string
  if (raw !== null) rawText = formatRaw(factor, raw)
  // Account first-output latency averages per-model normalised scores, so no
  // single millisecond value describes it; each model row has its own.
  else if (factor === 'ttft' && factors.ttft.known) rawText = t('admin.modelIntegrity.scheduling.board.raw.perModel')
  else rawText = t(`admin.modelIntegrity.scheduling.board.raw.${kind === 'default' || kind === 'neutral' ? 'none' : 'unknown'}`)
  let scoreText = ''
  if (withScore) {
    if (!weighted(factor)) scoreText = t('admin.modelIntegrity.scheduling.board.notWeighted')
    // Under quality-first ordering the pass rate is a tier, not points; the
    // main cell already shows it, so this cell says how many models it covers.
    else if (!(factor === 'quality' && qualityFirst.value)) scoreText = t('admin.modelIntegrity.scheduling.board.points', { score: Math.round((factors[factor].score ?? 0) * 100) })
  }
  return { kind, raw, rawText, scoreText }
}

function cellOf(row: OpenAIEvalOverviewAccount, factor: OpenAIEvalRankingFactorKey): Cell {
  const kind = accountFactorSourceKind(factor, row.factors, row.models ?? [], { quality: row.contributions?.quality ?? 0 })
  const cell = buildCell(factor, row.factors, kind, true)
  if (factor === 'quality' && qualityFirst.value && row.model_count) {
    cell.scoreText = t('admin.modelIntegrity.scheduling.board.modelsKnown', { known: row.quality_model_count, models: row.model_count })
  }
  return cell
}

function modelCell(model: OpenAIEvalOverviewModel, factor: OpenAIEvalRankingFactorKey): Cell {
  return buildCell(factor, model.factors, factorSourceKind(factor, model.factors), false)
}

function sourceLabel(kind: FactorSourceKind) {
  return t(`admin.modelIntegrity.scheduling.board.source.${kind}`)
}

function sourceHint(kind: FactorSourceKind) {
  return t(`admin.modelIntegrity.scheduling.board.sourceHint.${kind}`)
}

function coverageText(row: OpenAIEvalOverviewAccount) {
  if (!row.model_count) return t('admin.modelIntegrity.scheduling.board.coverageNone')
  return t('admin.modelIntegrity.scheduling.board.coverage', { models: row.model_count, known: row.quality_model_count })
}

function coverageDetail(row: OpenAIEvalOverviewAccount) {
  if (!row.model_count) return t('admin.modelIntegrity.scheduling.board.coverageNoneDetail')
  return t('admin.modelIntegrity.scheduling.board.coverageDetail', { models: row.model_count, known: row.quality_model_count, unknown: row.unknown_quality_model_count })
}

function contributionParts(row: OpenAIEvalOverviewAccount) {
  if (!row.contributions) return []
  return RANKING_FACTORS
    .filter(factor => (row.contributions[factor] ?? 0) > 0)
    .map(factor => ({ factor, text: t('admin.modelIntegrity.scheduling.board.contribution', { factor: factorLabel(factor), value: formatNumber(row.contributions[factor]) }) }))
}

/**
 * Models whose requests use a different policy from the overview. The
 * server's per-model policy is authoritative; saved rules are the fallback
 * for payloads without it.
 */
function exceptions(row: OpenAIEvalOverviewAccount): { model: string; effort: string; anyEffort: boolean; policy: OpenAIEvalSchedulingPolicy }[] {
  const overviewPolicy = meta.value?.policy ?? ''
  const models = row.models ?? []
  if (models.some(model => model.policy != null)) {
    return models
      .filter(model => model.policy != null && model.policy !== overviewPolicy)
      // A model cell's '' effort is the unspecified effort, not every effort.
      .map(model => ({ model: model.requested_model, effort: model.reasoning_effort, anyEffort: false, policy: model.policy as OpenAIEvalSchedulingPolicy }))
  }
  return ruleExceptionsFor(props.rules, models, overviewPolicy)
    .map(rule => ({ model: rule.requested_model, effort: rule.reasoning_effort || '', anyEffort: true, policy: rule.policy }))
}

/**
 * The policy a request for this model actually uses: the server's answer if
 * it sent one, otherwise the saved rule for the exact effort, then the rule
 * for every effort, then the default.
 */
function modelPolicyText(model: OpenAIEvalOverviewModel) {
  if (model.policy) return policyLabel(model.policy as OpenAIEvalSchedulingPolicy)
  const name = model.requested_model.toLowerCase()
  const effort = (model.reasoning_effort || '').toLowerCase()
  const matches = props.rules.filter(rule => rule.requested_model.toLowerCase() === name)
  const rule = matches.find(item => (item.reasoning_effort || '').toLowerCase() === effort && effort !== '') ?? matches.find(item => !item.reasoning_effort)
  return rule ? policyLabel(rule.policy) : t('admin.modelIntegrity.scheduling.board.models.defaultPolicy')
}

function exclusionList(row: OpenAIEvalOverviewAccount) {
  if (row.eligible) return []
  if (row.exclusion_reasons?.length) return row.exclusion_reasons
  return row.exclusion_reason ? [{ code: row.exclusion_reason, scope: 'account', observed_at: '' }] : []
}

function exclusionText(code: string) {
  return t(`admin.modelIntegrity.scheduling.exclusion.${exclusionKey(code)}`, { code })
}

function formatRaw(factor: OpenAIEvalRankingFactorKey, value: number) {
  switch (factor) {
    case 'price':
      return `${formatNumber(value)}x`
    case 'error_rate':
    case 'quality':
      return formatPercent(value)
    case 'ttft':
      return `${Math.round(value)} ms`
    case 'load':
      return `${formatNumber(value)}%`
  }
}

function formatDateTime(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

const formatNumber = (value: number) => (Math.abs(value) >= 100 ? value.toFixed(0) : Number(value.toFixed(2)).toString())
const formatScore = (value: number | null | undefined) => (value == null ? '—' : value.toFixed(1))
const formatPercent = (value: number) => `${Number((value * 100).toFixed(1))}%`
</script>

<style scoped>
.lb { @apply space-y-3; }
.lb-toolbar { @apply flex flex-wrap items-end gap-x-4 gap-y-2; }
.lb-field { @apply flex min-w-0 flex-col gap-1; }
.lb-label { @apply text-xs text-gray-500 dark:text-gray-400; }
.lb-select { @apply h-9 w-auto min-w-[11rem] py-1 text-sm; }
.lb-meta { @apply flex flex-wrap gap-x-4 gap-y-1 pb-2 text-xs tabular-nums text-gray-600 dark:text-gray-400; }
.lb-refresh { @apply ml-auto; }
.lb-note { @apply max-w-[80ch] text-xs leading-relaxed text-gray-500 dark:text-gray-400; }
.lb-warn { @apply max-w-[80ch] rounded-md bg-amber-50 px-3 py-2 text-xs leading-relaxed text-amber-900 dark:bg-amber-950/30 dark:text-amber-200; }
.lb-error { @apply space-y-1 break-words text-xs text-rose-700 [overflow-wrap:anywhere] dark:text-rose-300; }
.lb-empty { @apply rounded-lg border border-dashed border-gray-300 px-4 py-8 text-center text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400; }
.lb-muted { @apply text-gray-400 dark:text-gray-500; }

/* One bordered list; rows are divided by hairlines, not boxed as cards. */
.lb-table { @apply rounded-lg border border-gray-200 transition-opacity dark:border-dark-700; }
.lb-table-dim { @apply opacity-60; }
.lb-head,
.lb-line {
  display: grid;
  grid-template-columns: 2.75rem minmax(11rem, 1.5fr) 7.5rem repeat(5, minmax(5.75rem, 1fr)) 2.5rem;
  @apply items-start gap-x-3 px-3;
}
.lb-head { @apply hidden border-b border-gray-200 py-2 dark:border-dark-700 lg:grid; }
.lb-h { @apply text-xs font-medium text-gray-500 dark:text-gray-400; }
.lb-h-num { @apply text-right; }
.lb-rows { @apply divide-y divide-gray-100 dark:divide-dark-700; }
.lb-row { @apply py-3; }
.lb-row-out .lb-account-name { @apply text-gray-500 dark:text-gray-400; }

.lb-rank { @apply pt-0.5 text-right; }
.lb-rank-num { @apply inline-flex h-6 min-w-6 items-center justify-center rounded-full bg-gray-100 px-1.5 text-xs font-semibold tabular-nums text-gray-800 dark:bg-dark-700 dark:text-gray-200; }
.lb-row-first .lb-rank-num { @apply bg-primary-600 text-white dark:bg-primary-500; }
.lb-account { @apply min-w-0; }
.lb-account-name { @apply truncate text-sm font-medium text-gray-900 dark:text-white; }
.lb-account-sub { @apply mt-0.5 flex flex-wrap gap-x-2 gap-y-0.5 text-[11px] leading-snug text-gray-500 dark:text-gray-400; }
.lb-group { @apply rounded bg-gray-100 px-1 text-gray-600 dark:bg-dark-700 dark:text-gray-300; }
.lb-out { @apply font-medium text-rose-700 dark:text-rose-300; }

/* The one emphatic element: the value the order is actually sorted by. */
.lb-score { @apply flex flex-col items-end text-right; }
.lb-score-main { @apply text-xl font-semibold leading-none tabular-nums text-gray-900 dark:text-white; }
.lb-score-unknown { @apply text-base text-gray-500 dark:text-gray-400; }
.lb-score-sub { @apply mt-1 text-[11px] tabular-nums text-gray-500 dark:text-gray-400; }

.lb-factors { display: contents; }
.lb-factor { @apply flex min-w-0 flex-col items-end text-right; }
.lb-factor-label { @apply sr-only; }
.lb-factor-raw { @apply text-sm tabular-nums text-gray-800 dark:text-gray-200; }
.lb-factor-meta { @apply mt-0.5 flex flex-wrap justify-end gap-x-1.5 text-[11px] text-gray-500 dark:text-gray-400; }
.lb-source { @apply whitespace-nowrap; }
.lb-source-measured { @apply text-primary-700 dark:text-primary-300; }
.lb-source-probe { @apply text-violet-700 dark:text-violet-300; }
.lb-source-default,
.lb-source-mixed,
.lb-source-neutral { @apply text-amber-700 dark:text-amber-300; }
.lb-source-unknown,
.lb-source-other { @apply text-gray-500 dark:text-gray-400; }

.lb-toggle-cell { @apply flex justify-end; }
.lb-toggle { @apply inline-flex items-center gap-1 rounded-md p-1 text-xs font-medium text-primary-700 hover:bg-primary-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:text-primary-300 dark:hover:bg-primary-950/40; }
.lb-toggle-text { @apply sr-only; }

.lb-detail { @apply mx-3 mt-3 space-y-2 border-t border-dashed border-gray-200 pt-3 dark:border-dark-600 lg:ml-[3.5rem]; }
.lb-detail-line { @apply flex max-w-[90ch] flex-wrap items-baseline gap-x-2 gap-y-1 text-xs leading-relaxed text-gray-700 dark:text-gray-300; }
.lb-detail-key { @apply font-medium text-gray-800 dark:text-gray-200; }
.lb-chip { @apply rounded bg-gray-100 px-1.5 py-0.5 tabular-nums text-gray-700 dark:bg-dark-700 dark:text-gray-300; }
.lb-list { @apply list-disc space-y-0.5 pl-5 text-xs leading-relaxed text-gray-600 dark:text-gray-400; }
.lb-models-wrap { @apply max-w-full overflow-x-auto; }
.lb-models { @apply w-full min-w-[40rem] text-left text-xs; }
.lb-models th { @apply whitespace-nowrap border-b border-gray-200 px-2 py-1.5 font-medium text-gray-500 dark:border-dark-700 dark:text-gray-400; }
.lb-models td { @apply border-b border-gray-100 px-2 py-1.5 align-top text-gray-700 dark:border-dark-800 dark:text-gray-300; }
.lb-models tr:last-child td { @apply border-b-0; }
.lb-models .num { @apply whitespace-nowrap text-right tabular-nums; }
.lb-upstream { @apply max-w-[14rem] break-words; }
.lb-more { @apply flex flex-wrap items-center justify-center gap-3 pt-1; }

/* Below lg each row stacks: rank, account, score and toggle on top, factors in a grid beneath. */
@media (max-width: 1023px) {
  .lb-line {
    grid-template-columns: 2rem minmax(0, 1fr) auto 2rem;
    grid-auto-flow: row dense;
    @apply gap-y-3;
  }
  .lb-rank { grid-column: 1; grid-row: 1; }
  .lb-account { grid-column: 2; grid-row: 1; }
  .lb-score { grid-column: 3; grid-row: 1; }
  .lb-toggle-cell { grid-column: 4; grid-row: 1; }
  .lb-factors {
    display: grid;
    grid-column: 1 / -1;
    @apply grid-cols-3 gap-x-3 gap-y-2 border-t border-gray-100 pt-2 dark:border-dark-700;
  }
  .lb-factor { @apply items-start text-left; }
  .lb-factor-label { @apply not-sr-only text-[11px] text-gray-500 dark:text-gray-400; }
  .lb-factor-meta { @apply justify-start; }
  .lb-detail { @apply ml-3; }
}
</style>
