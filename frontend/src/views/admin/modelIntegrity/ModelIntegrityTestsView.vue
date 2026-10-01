<template>
  <AppLayout>
    <ModelIntegrityShell
      :title="t('admin.modelIntegrity.tests.title')"
      :description="t('admin.modelIntegrity.tests.description')"
      :dirty="dirty"
      :saving="saving"
      :conflict="conflict"
      @save="handleSave"
      @reload="handleReload"
    >
      <template #actions>
        <button type="button" class="btn btn-secondary" :disabled="!loaded" data-testid="open-add" @click="showAdd = true">
          <Icon name="plus" size="sm" />{{ t('admin.modelIntegrity.tests.addTargets') }}
        </button>
      </template>

      <div v-if="loading" class="tests-state" role="status">{{ t('common.loading') }}</div>
      <div v-else-if="!loaded" class="tests-state" role="alert">
        <p>{{ t('admin.modelIntegrity.common.loadFailed') }}</p>
        <button type="button" class="btn btn-secondary" @click="initialLoad">{{ t('admin.modelIntegrity.common.retry') }}</button>
      </div>

      <template v-else>
        <p class="budget" data-testid="budget">
          <template v-if="planCount">{{ t('admin.modelIntegrity.tests.budget', { requests: formatCount(dailyTotal), plans: planCount }) }}</template>
          <template v-else>{{ t('admin.modelIntegrity.tests.budgetNone') }}</template>
          <span class="budget-hint">{{ t('admin.modelIntegrity.tests.budgetHint') }}</span>
        </p>

        <div v-if="!config.accounts.length" class="tests-empty">
          <Icon name="beaker" size="lg" class="text-gray-400" />
          <p class="font-medium text-gray-900 dark:text-white">{{ t('admin.modelIntegrity.tests.noTargets') }}</p>
          <p class="text-sm text-gray-600 dark:text-gray-400">{{ t('admin.modelIntegrity.tests.noTargetsHint') }}</p>
          <button type="button" class="btn btn-primary" @click="showAdd = true"><Icon name="plus" size="sm" />{{ t('admin.modelIntegrity.tests.addTargets') }}</button>
        </div>

        <div v-else class="workbench">
          <section class="targets" aria-labelledby="targets-title">
            <div class="targets-head">
              <h2 id="targets-title" class="tests-h2">{{ t('admin.modelIntegrity.tests.targets') }}</h2>
              <p class="tests-hint">{{ t('admin.modelIntegrity.tests.targetsHint') }}</p>
              <input v-model="search" type="search" class="input h-9 py-1 text-sm" :placeholder="t('admin.modelIntegrity.tests.search')" :aria-label="t('admin.modelIntegrity.tests.search')" />
            </div>
            <p v-if="!filteredTargets.length" class="px-4 py-6 text-center text-sm text-gray-500">{{ t('admin.modelIntegrity.tests.noMatch') }}</p>
            <ul v-else class="target-list">
              <li v-for="route in filteredTargets" :key="routeKey(route)">
                <button
                  type="button"
                  class="target"
                  :class="{ 'target-active': selectedKey === routeKey(route) }"
                  :aria-current="selectedKey === routeKey(route) ? 'true' : undefined"
                  data-testid="target"
                  @click="selectedKey = routeKey(route)"
                >
                  <span class="target-name">{{ accountName(route.account_id) }}</span>
                  <span class="target-route">{{ route.requested_model }} · {{ route.reasoning_effort || t('admin.modelIntegrity.common.defaultEffort') }}</span>
                  <span class="target-foot">
                    <span class="signals" role="img" :aria-label="signalLabel(route)">
                      <span
                        v-for="type in TEST_TYPES"
                        :key="type"
                        class="signal"
                        :class="signalClass(route, type)"
                        :title="`${t(`admin.modelIntegrity.tests.types.${type}.name`)}: ${signalText(route, type)}`"
                      />
                    </span>
                    <span class="target-auto">{{ autoCount(route) ? t('admin.modelIntegrity.tests.autoCount', { count: autoCount(route) }) : t('admin.modelIntegrity.tests.manualOnly') }}</span>
                  </span>
                </button>
              </li>
            </ul>
          </section>

          <section v-if="selected" class="detail" aria-labelledby="detail-title">
            <header class="detail-head">
              <div class="min-w-0">
                <h2 id="detail-title" class="tests-h2 truncate">{{ accountName(selected.account_id) }} <span class="font-normal text-gray-400">#{{ selected.account_id }}</span></h2>
                <p class="tests-hint">{{ selected.requested_model }} · {{ selected.reasoning_effort || t('admin.modelIntegrity.common.defaultEffort') }}</p>
              </div>
              <button type="button" class="detail-remove" :title="t('admin.modelIntegrity.tests.removeTarget')" :aria-label="t('admin.modelIntegrity.tests.removeTarget')" data-testid="remove-target" @click="pendingRemove = selected">
                <Icon name="trash" size="sm" />
              </button>
            </header>
            <div class="detail-body">
              <TestTypePanel
                v-for="type in TEST_TYPES"
                :key="type"
                :route="selected"
                :type="type"
                :catalog="catalog"
                :latest="latestRunFor(panelRuns, selected, type)"
                :running="isRunning(selected, type)"
                :available="type !== 'state_probe' || isDirectOAuthRoute(selected)"
                @run="requestRun(selected, type)"
                @open="detailRun = $event"
              />
            </div>
          </section>
          <p v-else class="detail detail-placeholder">{{ t('admin.modelIntegrity.tests.selectTarget') }}</p>
        </div>

        <section class="history" aria-labelledby="history-title">
          <div class="history-head">
            <div>
              <h2 id="history-title" class="tests-h2">{{ t('admin.modelIntegrity.tests.history') }}</h2>
              <p class="tests-hint">{{ t('admin.modelIntegrity.tests.historyHint') }}</p>
            </div>
            <div class="history-filters">
              <div class="scope" role="group" :aria-label="t('admin.modelIntegrity.tests.history')">
                <button type="button" class="scope-btn" :class="{ 'scope-btn-active': historyScope === 'all' }" :aria-pressed="historyScope === 'all'" @click="historyScope = 'all'">{{ t('admin.modelIntegrity.tests.historyScopeAll') }}</button>
                <button type="button" class="scope-btn" :class="{ 'scope-btn-active': historyScope === 'target' }" :aria-pressed="historyScope === 'target'" :disabled="!selected" @click="historyScope = 'target'">{{ t('admin.modelIntegrity.tests.historyScopeTarget') }}</button>
              </div>
              <select v-model="typeFilter" class="input h-9 w-auto py-1 text-sm" :aria-label="t('admin.modelIntegrity.tests.filterType')">
                <option value="">{{ t('admin.modelIntegrity.tests.filterType') }}</option>
                <option v-for="type in TEST_TYPES" :key="type" :value="type">{{ t(`admin.modelIntegrity.tests.types.${type}.name`) }}</option>
              </select>
              <select v-model="toneFilter" class="input h-9 w-auto py-1 text-sm" :aria-label="t('admin.modelIntegrity.tests.filterResult')">
                <option value="">{{ t('admin.modelIntegrity.tests.filterResult') }}</option>
                <option value="attention">{{ t('admin.modelIntegrity.tests.filterAttention') }}</option>
                <option value="ok">{{ t('admin.modelIntegrity.tests.filterOk') }}</option>
                <option value="neutral">{{ t('admin.modelIntegrity.tests.filterNeutral') }}</option>
              </select>
              <button type="button" class="btn btn-secondary btn-sm" :disabled="historyLoading" @click="loadHistory">
                <Icon name="refresh" size="sm" :class="historyLoading ? 'motion-safe:animate-spin' : ''" />{{ t('admin.modelIntegrity.common.refresh') }}
              </button>
            </div>
          </div>

          <p v-if="!historyRuns.length" class="history-empty">{{ t('admin.modelIntegrity.tests.noHistory') }}</p>
          <p v-else-if="!visibleRuns.length" class="history-empty">{{ t('admin.modelIntegrity.tests.noHistoryMatch') }}</p>
          <div v-else class="overflow-x-auto">
            <table class="history-table">
              <thead>
                <tr>
                  <th scope="col">{{ t('admin.modelIntegrity.tests.columns.time') }}</th>
                  <th scope="col">{{ t('admin.modelIntegrity.tests.columns.target') }}</th>
                  <th scope="col">{{ t('admin.modelIntegrity.tests.columns.test') }}</th>
                  <th scope="col">{{ t('admin.modelIntegrity.tests.columns.result') }}</th>
                  <th scope="col" class="num">{{ t('admin.modelIntegrity.tests.columns.samples') }}</th>
                  <th scope="col" class="num">{{ t('admin.modelIntegrity.tests.columns.cost') }}</th>
                  <th scope="col">{{ t('admin.modelIntegrity.tests.columns.trigger') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="run in visibleRuns" :key="run.id" class="history-row" tabindex="0" data-testid="history-row" @click="detailRun = run" @keydown.enter="detailRun = run">
                  <td class="whitespace-nowrap tabular-nums text-gray-500">{{ formatTime(run.finished_at || run.started_at) }}</td>
                  <td class="history-target">
                    <span class="block text-gray-900 dark:text-gray-100">{{ accountName(run.account_id) }}</span>
                    <span class="block text-xs text-gray-500">{{ run.requested_model }} · {{ run.reasoning_effort || t('admin.modelIntegrity.common.defaultEffort') }}</span>
                  </td>
                  <td class="whitespace-nowrap">{{ t(`admin.modelIntegrity.tests.types.${run.test_type}.name`) }}</td>
                  <td class="history-result">
                    <span class="tone" :class="`tone-${resultTone(run.status)}`">{{ statusLabel(t, run.status) }}</span>
                    <span class="mt-1 block text-xs text-gray-600 dark:text-gray-400">{{ runExplanation(t, run, catalog) }}</span>
                  </td>
                  <td class="num">{{ run.outcome.sample_count }}/{{ run.outcome.expected_count }}</td>
                  <td class="num">{{ run.cost_estimate_usd == null ? t('admin.modelIntegrity.tests.costUnknown') : `$${run.cost_estimate_usd.toFixed(4)}` }}</td>
                  <td class="whitespace-nowrap text-gray-500">{{ triggerLabel(run.trigger_source) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <p class="tests-note">{{ t('admin.modelIntegrity.tests.baselineNote', { version: catalog?.baseline_version || '—' }) }}</p>
        </section>
      </template>
    </ModelIntegrityShell>

    <AddTargetsDialog :show="showAdd" :accounts="accounts" :catalog="catalog" @close="showAdd = false" @add="addTargets" />
    <RunDetailDialog :run="detailRun" :target="detailRun ? `${accountName(detailRun.account_id)} · ${detailRun.requested_model} · ${detailRun.reasoning_effort || t('admin.modelIntegrity.common.defaultEffort')}` : ''" :catalog="catalog" @close="detailRun = null" />

    <BaseDialog :show="pendingFingerprint !== null" :title="t('admin.modelIntegrity.tests.manualSampleTitle')" width="narrow" @close="pendingFingerprint = null">
      <fieldset class="space-y-2">
        <legend class="sr-only">{{ t('admin.modelIntegrity.tests.sampleMode') }}</legend>
        <label v-for="mode in fingerprintModes" :key="mode.id" class="sample-choice">
          <input v-model="manualSampleMode" type="radio" name="manual-sample" :value="mode.id" class="text-primary-600 focus:ring-primary-500" />
          <span>{{ t('admin.modelIntegrity.tests.manualSampleOption', { mode: sampleModeLabel(mode.id), count: mode.samples }) }}</span>
        </label>
      </fieldset>
      <p class="mt-3 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.modelIntegrity.tests.manualSampleHint') }}</p>
      <template #footer>
        <div class="flex justify-end gap-2">
          <button type="button" class="btn btn-secondary" @click="pendingFingerprint = null">{{ t('admin.modelIntegrity.common.cancel') }}</button>
          <button type="button" class="btn btn-primary" data-testid="confirm-fingerprint" @click="confirmFingerprint">{{ t('admin.modelIntegrity.tests.runNow') }}</button>
        </div>
      </template>
    </BaseDialog>

    <ConfirmDialog
      :show="pendingRemove !== null"
      :title="t('admin.modelIntegrity.tests.removeConfirmTitle')"
      :message="pendingRemove ? t('admin.modelIntegrity.tests.removeConfirm', { target: `${accountName(pendingRemove.account_id)} · ${pendingRemove.requested_model}` }) : ''"
      :confirm-text="t('admin.modelIntegrity.common.remove')"
      danger
      @confirm="confirmRemove"
      @cancel="pendingRemove = null"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { onBeforeRouteLeave } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import ModelIntegrityShell from '@/components/admin/modelIntegrity/ModelIntegrityShell.vue'
import TestTypePanel from '@/components/admin/modelIntegrity/TestTypePanel.vue'
import AddTargetsDialog from '@/components/admin/modelIntegrity/AddTargetsDialog.vue'
import RunDetailDialog from '@/components/admin/modelIntegrity/RunDetailDialog.vue'
import { accountsAPI, type OpenAIEvalRouteConfig, type OpenAIEvalRun } from '@/api/admin/accounts'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import {
  TEST_TYPES,
  activeScheduleCount,
  isDirectOAuthRoute,
  latestRunFor,
  newRoute,
  resultTone,
  routeKey,
  scheduleOf,
  totalDailyRequests,
  type EvalTestType
} from './modelIntegrity'
import { runExplanation, statusLabel } from './runText'
import { useModelIntegrityConfig } from './useModelIntegrityConfig'

const { t } = useI18n()
const appStore = useAppStore()
const { config, catalog, accounts, loading, loaded, saving, conflict, dirty, load, reloadConfig, save, accountName } = useModelIntegrityConfig()

const search = ref('')
const selectedKey = ref('')
const showAdd = ref(false)
const runs = ref<OpenAIEvalRun[]>([])
const targetRuns = ref<{ key: string; items: OpenAIEvalRun[] } | null>(null)
const historyLoading = ref(false)
const historyScope = ref<'all' | 'target'>('all')
const typeFilter = ref<'' | EvalTestType>('')
const toneFilter = ref('')
const detailRun = ref<OpenAIEvalRun | null>(null)
const pendingRemove = ref<OpenAIEvalRouteConfig | null>(null)
const pendingFingerprint = ref<OpenAIEvalRouteConfig | null>(null)
const manualSampleMode = ref('quick')
const runningKeys = reactive(new Set<string>())

const filteredTargets = computed(() => {
  const needle = search.value.trim().toLowerCase()
  if (!needle) return config.accounts
  return config.accounts.filter(route => `${accountName(route.account_id)} #${route.account_id} ${route.requested_model} ${route.reasoning_effort}`.toLowerCase().includes(needle))
})
const selected = computed(() => config.accounts.find(route => routeKey(route) === selectedKey.value) ?? null)
const dailyTotal = computed(() => totalDailyRequests(config.accounts, catalog.value))
const planCount = computed(() => activeScheduleCount(config.accounts))
const fingerprintModes = computed(() => catalog.value?.fingerprint_modes?.length ? catalog.value.fingerprint_modes : [{ id: 'quick', samples: 60 }, { id: 'standard', samples: 200 }, { id: 'strict', samples: 400 }])

const panelRuns = computed(() => (selected.value && targetRuns.value?.key === routeKey(selected.value) ? [...targetRuns.value.items, ...runs.value] : runs.value))
const historyRuns = computed(() => {
  if (historyScope.value === 'target' && selected.value) {
    return targetRuns.value?.key === routeKey(selected.value) ? targetRuns.value.items : []
  }
  return runs.value
})
const visibleRuns = computed(() => historyRuns.value.filter(run => (!typeFilter.value || run.test_type === typeFilter.value) && (!toneFilter.value || resultTone(run.status) === toneFilter.value || (toneFilter.value === 'neutral' && resultTone(run.status) === 'running'))))

watch(() => config.accounts.length, () => {
  if (!selected.value) selectedKey.value = config.accounts[0] ? routeKey(config.accounts[0]) : ''
})
watch(selectedKey, () => { void loadTargetRuns() })
watch(selected, value => { if (!value && historyScope.value === 'target') historyScope.value = 'all' })

function autoCount(route: OpenAIEvalRouteConfig) {
  return TEST_TYPES.filter(type => scheduleOf(route, type).enabled && (type !== 'state_probe' || isDirectOAuthRoute(route))).length
}

function signalRun(route: OpenAIEvalRouteConfig, type: EvalTestType) {
  return latestRunFor(runs.value, route, type)
}
function signalClass(route: OpenAIEvalRouteConfig, type: EvalTestType) {
  if (type === 'state_probe' && !isDirectOAuthRoute(route)) return 'signal-na'
  const run = signalRun(route, type)
  return run ? `signal-${resultTone(run.status)}` : 'signal-none'
}
function signalText(route: OpenAIEvalRouteConfig, type: EvalTestType) {
  if (type === 'state_probe' && !isDirectOAuthRoute(route)) return t('admin.modelIntegrity.tests.onlyDirectOAuth')
  const run = signalRun(route, type)
  return run ? statusLabel(t, run.status) : t('admin.modelIntegrity.tests.neverRun')
}
function signalLabel(route: OpenAIEvalRouteConfig) {
  return TEST_TYPES.map(type => `${t(`admin.modelIntegrity.tests.types.${type}.name`)}: ${signalText(route, type)}`).join('; ')
}

function addTargets(payload: { accountIDs: number[]; model: string; effort: string }) {
  const existing = new Set(config.accounts.map(routeKey))
  let added = 0
  let firstKey = ''
  for (const accountID of payload.accountIDs) {
    const route = newRoute(accountID, payload.model, payload.effort)
    const key = routeKey(route)
    if (existing.has(key)) continue
    existing.add(key)
    const account = accounts.value.find(item => item.id === accountID)
    // Eligibility is derived by the server; until the next reload assume the
    // same rules it applies so the State Probe row is not shown as unusable.
    route.direct_oauth_eligible = payload.effort === '' && account?.type === 'oauth' && account.parent_account_id == null
    config.accounts.push(route)
    firstKey ||= key
    added++
  }
  showAdd.value = false
  if (added) {
    selectedKey.value = firstKey
    appStore.showSuccess(t('admin.modelIntegrity.tests.add.added', { count: added }))
  }
  const skipped = payload.accountIDs.length - added
  if (skipped) appStore.showInfo(t('admin.modelIntegrity.tests.add.skipped', { count: skipped }))
}

function confirmRemove() {
  const route = pendingRemove.value
  pendingRemove.value = null
  if (!route) return
  const index = config.accounts.indexOf(route)
  if (index >= 0) config.accounts.splice(index, 1)
  selectedKey.value = config.accounts[Math.min(index, config.accounts.length - 1)] ? routeKey(config.accounts[Math.min(index, config.accounts.length - 1)]) : ''
}

const runKey = (route: OpenAIEvalRouteConfig, type: EvalTestType) => `${routeKey(route)}:${type}`
const isRunning = (route: OpenAIEvalRouteConfig, type: EvalTestType) => runningKeys.has(runKey(route, type))

function requestRun(route: OpenAIEvalRouteConfig, type: EvalTestType) {
  if (isRunning(route, type)) return
  if (type === 'fingerprint') {
    manualSampleMode.value = route.fingerprint_schedule.sample_mode || 'quick'
    pendingFingerprint.value = route
    return
  }
  void runNow(route, type)
}

function confirmFingerprint() {
  const route = pendingFingerprint.value
  pendingFingerprint.value = null
  if (route) void runNow(route, 'fingerprint', manualSampleMode.value)
}

async function runNow(route: OpenAIEvalRouteConfig, type: EvalTestType, sampleMode?: string) {
  const key = runKey(route, type)
  runningKeys.add(key)
  try {
    const request: Parameters<typeof accountsAPI.runOpenAIEval>[0] = { account_id: route.account_id, requested_model: route.requested_model, reasoning_effort: route.reasoning_effort, test_type: type }
    if (type === 'fingerprint') request.sample_mode = sampleMode || 'quick'
    const run = await accountsAPI.runOpenAIEval(request)
    detailRun.value = run
    appStore.showSuccess(t('admin.modelIntegrity.tests.runDone'))
    await Promise.all([loadHistory(), loadTargetRuns()])
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.modelIntegrity.tests.runFailed')))
  } finally {
    runningKeys.delete(key)
  }
}

async function loadHistory() {
  historyLoading.value = true
  try {
    runs.value = (await accountsAPI.listOpenAIEvalRuns({ limit: 200 })).items ?? []
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.modelIntegrity.common.loadFailed')))
  } finally {
    historyLoading.value = false
  }
}

async function loadTargetRuns() {
  const route = selected.value
  if (!route) return
  const key = routeKey(route)
  try {
    const { items } = await accountsAPI.listOpenAIEvalRuns({ account_id: route.account_id, requested_model: route.requested_model, limit: 50 })
    if (selected.value && routeKey(selected.value) === key) {
      const effort = (route.reasoning_effort || '').toLowerCase()
      targetRuns.value = { key, items: (items ?? []).filter(run => run.test_type === 'state_probe' || (run.reasoning_effort || '').toLowerCase() === effort) }
    }
  } catch {
    // The global history still backs the panels; a failed narrow query is not fatal.
  }
}

async function handleSave() {
  try {
    const result = await save()
    if (result === 'saved') appStore.showSuccess(t('admin.modelIntegrity.common.saved'))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.modelIntegrity.common.saveFailed')))
  }
}

async function handleReload() {
  try {
    await reloadConfig()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.modelIntegrity.common.loadFailed')))
  }
}

async function initialLoad() {
  try {
    await load()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.modelIntegrity.common.loadFailed')))
    return
  }
  if (!selected.value && config.accounts[0]) selectedKey.value = routeKey(config.accounts[0])
  await loadHistory()
}

function sampleModeLabel(id: string) {
  const key = `admin.modelIntegrity.tests.sampleModes.${id}`
  const text = t(key)
  return text === key ? id : text
}
function triggerLabel(source: string) {
  const key = `admin.modelIntegrity.tests.trigger.${source}`
  const text = t(key)
  return text === key ? source : text
}
const formatCount = (value: number) => (value >= 10 ? Math.round(value).toLocaleString() : Number(value.toFixed(1)).toString())
const formatTime = (value: string) => {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString([], { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false })
}

onBeforeRouteLeave(() => {
  if (dirty.value && !window.confirm(t('admin.modelIntegrity.common.leaveConfirm'))) return false
  return true
})

onMounted(initialLoad)
</script>

<style scoped>
.tests-state { @apply flex min-h-[12rem] flex-col items-center justify-center gap-3 rounded-lg border border-dashed border-gray-300 text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400; }
.tests-empty { @apply flex flex-col items-center gap-2 rounded-xl border border-dashed border-gray-300 px-6 py-12 text-center dark:border-dark-600; }
.tests-h2 { @apply text-base font-semibold text-gray-900 dark:text-white; }
.tests-hint { @apply mt-1 text-sm leading-relaxed text-gray-600 dark:text-gray-400; }
.tests-note { @apply text-xs text-gray-500 dark:text-gray-400; }
.budget { @apply max-w-[80ch] text-sm text-gray-800 dark:text-gray-200; }
.budget-hint { @apply ml-1 text-gray-500 dark:text-gray-400; }
.workbench { @apply grid gap-5 lg:grid-cols-[20rem_minmax(0,1fr)]; }
.targets { @apply flex max-h-[44rem] flex-col overflow-hidden rounded-xl border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800/60; }
.targets-head { @apply space-y-3 border-b border-gray-100 p-4 dark:border-dark-700; }
.target-list { @apply flex-1 divide-y divide-gray-100 overflow-y-auto dark:divide-dark-700; }
.target { @apply flex w-full flex-col gap-1 px-4 py-3 text-left hover:bg-gray-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary-500 dark:hover:bg-dark-700/40; }
.target-active { @apply bg-primary-50/70 hover:bg-primary-50/70 dark:bg-primary-950/30; box-shadow: inset 3px 0 0 theme('colors.primary.600'); }
.target-name { @apply truncate text-sm font-medium text-gray-900 dark:text-white; }
.target-route { @apply truncate text-xs text-gray-500 dark:text-gray-400; }
.target-foot { @apply mt-1 flex items-center justify-between gap-2; }
.target-auto { @apply text-xs text-gray-500 dark:text-gray-400; }
.signals { @apply inline-flex gap-1; }
.signal { @apply h-2.5 w-5 rounded-sm; }
.signal-ok { @apply bg-emerald-500; }
.signal-attention { @apply bg-amber-500; }
.signal-neutral { @apply bg-gray-400 dark:bg-dark-400; }
.signal-running { @apply bg-sky-500; }
.signal-none { @apply border border-gray-300 dark:border-dark-500; }
.signal-na { background: repeating-linear-gradient(135deg, transparent 0 2px, theme('colors.gray.300') 2px 3px); @apply border border-gray-200 dark:border-dark-600; }
.detail { @apply rounded-xl border border-gray-200 bg-white px-5 dark:border-dark-700 dark:bg-dark-800/60 sm:px-6; }
.detail-placeholder { @apply flex items-center justify-center py-12 text-sm text-gray-500; }
.detail-head { @apply flex items-start justify-between gap-3 border-b border-gray-100 py-4 dark:border-dark-700; }
.detail-remove { @apply inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-md text-gray-400 hover:bg-rose-50 hover:text-rose-600 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:hover:bg-rose-950/40; }
.detail-body { @apply divide-y divide-gray-100 dark:divide-dark-700; }
.history { @apply space-y-3 rounded-xl border border-gray-200 bg-white p-5 dark:border-dark-700 dark:bg-dark-800/60 sm:p-6; }
.history-head { @apply flex flex-wrap items-start justify-between gap-3; }
.history-filters { @apply flex flex-wrap items-center gap-2; }
.scope { @apply inline-flex rounded-lg bg-gray-100 p-0.5 text-sm dark:bg-dark-900; }
.scope-btn { @apply rounded-md px-2.5 py-1 text-gray-600 disabled:opacity-50 dark:text-gray-400; }
.scope-btn-active { @apply bg-white font-medium text-gray-900 shadow-sm dark:bg-dark-600 dark:text-white; }
.history-empty { @apply rounded-lg border border-dashed border-gray-300 px-4 py-8 text-center text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400; }
.history-table { @apply w-full min-w-[52rem] text-left text-sm; }
.history-table th { @apply whitespace-nowrap border-b border-gray-200 px-3 py-2 text-xs font-medium text-gray-500 dark:border-dark-700 dark:text-gray-400; }
.history-table td { @apply border-b border-gray-100 px-3 py-2.5 align-top text-gray-700 dark:border-dark-700 dark:text-gray-300; }
.history-table .num { @apply whitespace-nowrap text-right tabular-nums; }
.history-row { @apply cursor-pointer hover:bg-gray-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary-500 dark:hover:bg-dark-700/40; }
.history-target { @apply min-w-[12rem]; }
.history-result { @apply min-w-[16rem] max-w-[28rem]; }
.tone { @apply inline-flex whitespace-nowrap rounded px-1.5 py-0.5 text-xs font-medium; }
.tone-ok { @apply bg-emerald-50 text-emerald-800 dark:bg-emerald-950/40 dark:text-emerald-300; }
.tone-attention { @apply bg-amber-100 text-amber-900 dark:bg-amber-900/40 dark:text-amber-200; }
.tone-neutral { @apply bg-gray-100 text-gray-700 dark:bg-dark-700 dark:text-gray-300; }
.tone-running { @apply bg-sky-50 text-sky-800 dark:bg-sky-950/40 dark:text-sky-300; }
.sample-choice { @apply flex cursor-pointer items-center gap-3 rounded-lg border border-gray-200 px-3 py-2.5 text-sm dark:border-dark-600; }
.sample-choice:has(input:checked) { @apply border-primary-600 bg-primary-50/50 dark:bg-primary-950/30; }
</style>
