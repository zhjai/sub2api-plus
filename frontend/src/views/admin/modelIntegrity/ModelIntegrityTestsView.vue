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
        <div class="budget-row">
          <p class="budget" data-testid="budget">
            <template v-if="planCount">{{ t('admin.modelIntegrity.tests.budget', { requests: formatCount(dailyTotal), plans: planCount }) }}<template v-if="dailyMax > dailyTotal"> {{ t('admin.modelIntegrity.tests.budgetRetry', { max: formatCount(dailyMax) }) }}</template></template>
            <template v-else>{{ t('admin.modelIntegrity.tests.budgetNone') }}</template>
            <span class="budget-hint">{{ t('admin.modelIntegrity.tests.budgetHint') }}</span>
            <span v-if="pausedAccounts.length" class="paused-summary" data-testid="paused-summary">
              <span class="paused-summary-label">{{ t('admin.modelIntegrity.tests.background.pausedSummary', { count: pausedAccounts.length }) }}</span>
              <button
                v-for="item in pausedAccounts"
                :key="item.accountID"
                type="button"
                class="paused-chip"
                :disabled="!item.firstKey"
                @click="selectedKey = item.firstKey"
              >{{ accountName(item.accountID) }}</button>
            </span>
          </p>
          <div class="attempts">
            <label class="attempts-field">
              <span class="attempts-label">{{ t('admin.modelIntegrity.tests.maxAttempts.label') }}</span>
              <input
                v-model.number="attemptsInput"
                type="number"
                :min="MIN_MAX_REQUEST_ATTEMPTS"
                :max="MAX_MAX_REQUEST_ATTEMPTS"
                step="1"
                inputmode="numeric"
                class="input attempts-input"
                aria-describedby="attempts-hint"
                data-testid="max-attempts"
                @change="commitAttempts"
                @blur="commitAttempts"
              />
              <span class="attempts-unit">{{ t('admin.modelIntegrity.tests.maxAttempts.unit') }}</span>
            </label>
            <p id="attempts-hint" class="attempts-hint">{{ t('admin.modelIntegrity.tests.maxAttempts.hint') }}</p>
          </div>
        </div>

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
                  <span class="target-name">
                    <PlatformIcon v-if="isPrismRoute(route)" platform="prism" size="xs" class="mr-1 inline text-fuchsia-600 dark:text-fuchsia-300" />{{ accountName(route.account_id) }}
                  </span>
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
                    <span class="target-auto">
                      <span v-if="isAccountPaused(route.account_id)" class="target-paused" data-testid="target-paused">{{ t('admin.modelIntegrity.tests.background.pausedTag') }}</span>
                      {{ autoCount(route) ? t('admin.modelIntegrity.tests.autoCount', { count: autoCount(route) }) : t('admin.modelIntegrity.tests.manualOnly') }}
                    </span>
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
                <p v-if="isPrismRoute(selected)" class="prism-tag" data-testid="prism-target-note">
                  <PlatformIcon platform="prism" size="xs" />
                  {{ prismTargetNote(selected) }}
                </p>
              </div>
              <div class="detail-actions">
                <button type="button" class="detail-edit" data-testid="edit-target" @click="editing = selected">
                  <Icon name="edit" size="sm" />{{ t('admin.modelIntegrity.tests.edit.open') }}
                </button>
                <button type="button" class="detail-remove" :title="t('admin.modelIntegrity.tests.removeTarget')" :aria-label="t('admin.modelIntegrity.tests.removeTarget')" data-testid="remove-target" @click="pendingRemove = selected">
                  <Icon name="trash" size="sm" />
                </button>
              </div>
            </header>
            <AccountAutoTestControls
              :account-id="selected.account_id"
              :account-name="accountName(selected.account_id)"
              :control="controlFor(selected.account_id)"
              :saved-control="savedControlFor(selected.account_id)"
              :routes="config.accounts.filter(route => route.account_id === selected!.account_id)"
              :catalog="catalog"
              :max-attempts="maxAttempts"
              :rpm-limit="accountOf(selected.account_id)?.rpm_limit"
              :applies="testAvailable"
              @update="updateControl"
            />
            <div class="detail-body">
              <TestTypePanel
                v-for="type in TEST_TYPES"
                :key="type"
                :route="selected"
                :type="type"
                :catalog="catalog"
                :latest="latestRunFor(panelRuns, selected, type)"
                :progress="runningProgress.get(runKey(selected, type))"
                :running="isRunning(selected, type)"
                :available="testAvailable(selected, type)"
                :unavailable-reason="unavailableReason(selected, type)"
                :notice="testNotice(selected, type)"
                :max-attempts="maxAttempts"
                :auto-note="autoNote(selected, type)"
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
                <option value="likely">{{ t('admin.modelIntegrity.tests.filterLikely') }}</option>
                <option value="neutral">{{ t('admin.modelIntegrity.tests.filterNeutral') }}</option>
              </select>
              <button type="button" class="btn btn-secondary btn-sm" :disabled="historyLoading" @click="loadHistory(); refreshRuntime()">
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
                  <td class="history-time whitespace-nowrap tabular-nums text-gray-500">{{ formatTime(run.finished_at || run.started_at) }}</td>
                  <td class="history-target">
                    <span class="block text-gray-900 dark:text-gray-100">{{ accountName(run.account_id) }}</span>
                    <span class="block text-xs text-gray-500">{{ run.requested_model }} · {{ run.reasoning_effort || t('admin.modelIntegrity.common.defaultEffort') }}</span>
                  </td>
                  <td class="history-test whitespace-nowrap">{{ t(`admin.modelIntegrity.tests.types.${run.test_type}.name`) }}</td>
                  <td class="history-result">
                    <span class="tone" :class="`tone-${resultTone(run.status)}`" data-testid="history-status">{{ runStatusLabel(t, run) }}</span>
                    <span class="mt-1 block text-xs text-gray-600 dark:text-gray-400">{{ runExplanation(t, run, catalog) }}</span>
                  </td>
                  <td class="history-meta num">{{ run.outcome.sample_count }}/{{ run.outcome.expected_count }}</td>
                  <td class="history-meta num">{{ run.cost_estimate_usd == null ? t('admin.modelIntegrity.tests.costUnknown') : `$${run.cost_estimate_usd.toFixed(4)}` }}</td>
                  <td class="history-trigger whitespace-nowrap text-gray-500">{{ triggerLabel(run.trigger_source) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
          <p class="tests-note">{{ t('admin.modelIntegrity.tests.baselineNote', { version: catalog?.baseline_version || '—' }) }}</p>
        </section>
      </template>
    </ModelIntegrityShell>

    <AddTargetsDialog :show="showAdd" :accounts="accounts" :catalog="catalog" :prism-catalogs="prismCatalogs" @close="showAdd = false" @add="addTargets" />
    <EditTargetDialog
      :route="editing"
      :routes="config.accounts"
      :catalog="catalog"
      :account-label="editing ? accountLabel(editing.account_id) : ''"
      :prism="editing ? isPrismRoute(editing) : false"
      :prism-state="editing ? prismCatalogs.get(editing.account_id) : undefined"
      @close="editing = null"
      @save="saveEdit"
      @reload-prism="editing && prismCatalogs.reload(editing.account_id)"
    />
    <RunDetailDialog :run="detailRun" :target="detailRun ? `${accountName(detailRun.account_id)} · ${detailRun.requested_model} · ${detailRun.reasoning_effort || t('admin.modelIntegrity.common.defaultEffort')}` : ''" :catalog="catalog" @close="detailRun = null" />

    <BaseDialog :show="pendingFingerprint !== null" :title="t('admin.modelIntegrity.tests.manualSampleTitle')" width="narrow" @close="pendingFingerprint = null">
      <fieldset class="space-y-2">
        <legend class="sr-only">{{ t('admin.modelIntegrity.tests.sampleMode') }}</legend>
        <label v-for="mode in fingerprintModes" :key="mode.id" class="sample-choice" :class="{ 'sample-choice-blocked': manualBlocked(mode.samples) }">
          <input v-model="manualSampleMode" type="radio" name="manual-sample" :value="mode.id" :disabled="manualBlocked(mode.samples)" class="text-primary-600 focus:ring-primary-500" />
          <span>
            {{ t('admin.modelIntegrity.tests.manualSampleOption', { mode: sampleModeLabel(mode.id), count: mode.samples }) }}
            <span v-if="manualBlocked(mode.samples)" class="block text-xs text-rose-700 dark:text-rose-300">{{ t('admin.modelIntegrity.tests.background.manualBlocked', { capacity: manualCapacity }) }}</span>
          </span>
        </label>
      </fieldset>
      <p class="mt-3 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.modelIntegrity.tests.manualSampleHint') }}</p>
      <p v-if="manualControl?.budget_enabled" class="mt-2 text-xs text-gray-500 dark:text-gray-400" data-testid="manual-budget-note">{{ t('admin.modelIntegrity.tests.background.manualBudgetNote') }}</p>
      <template #footer>
        <div class="flex justify-end gap-2">
          <button type="button" class="btn btn-secondary" @click="pendingFingerprint = null">{{ t('admin.modelIntegrity.common.cancel') }}</button>
          <button type="button" class="btn btn-primary" data-testid="confirm-fingerprint" :disabled="manualBlocked(selectedManualSamples)" @click="confirmFingerprint">{{ t('admin.modelIntegrity.tests.runNow') }}</button>
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
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { onBeforeRouteLeave } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import ModelIntegrityShell from '@/components/admin/modelIntegrity/ModelIntegrityShell.vue'
import TestTypePanel from '@/components/admin/modelIntegrity/TestTypePanel.vue'
import AddTargetsDialog from '@/components/admin/modelIntegrity/AddTargetsDialog.vue'
import EditTargetDialog from '@/components/admin/modelIntegrity/EditTargetDialog.vue'
import RunDetailDialog from '@/components/admin/modelIntegrity/RunDetailDialog.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import AccountAutoTestControls from '@/components/admin/modelIntegrity/AccountAutoTestControls.vue'
import { accountsAPI, type OpenAIEvalBackgroundControl, type OpenAIEvalRouteConfig, type OpenAIEvalRun } from '@/api/admin/accounts'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import {
  MAX_MAX_REQUEST_ATTEMPTS,
  MIN_MAX_REQUEST_ATTEMPTS,
  TEST_TYPES,
  activeScheduleCount,
  findBackgroundControl,
  isDirectOAuthAccount,
  isDirectOAuthRoute,
  latestRunFor,
  newRoute,
  normalizeMaxRequestAttempts,
  pausedUntil,
  redactSecrets,
  resultTone,
  routeKey,
  runFeasibility,
  scheduleOf,
  totalDailyMaxRequests,
  totalDailyRequests,
  requestsPerRun,
  maxRequestsPerRun,
  type EvalTestType
} from './modelIntegrity'
import { runExplanation, runStatusLabel } from './runText'
import { isPrismAccount, prismTestAvailability, prismUpstreamModel, usePrismEvalCatalogs } from './prismTargets'
import { useModelIntegrityConfig } from './useModelIntegrityConfig'

const { t } = useI18n()
const appStore = useAppStore()
const { config, catalog, accounts, loading, loaded, saving, conflict, dirty, load, reloadConfig, save, accountName, accountLabel, savedBackgroundControls, refreshBackgroundRuntime } = useModelIntegrityConfig()

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
const editing = ref<OpenAIEvalRouteConfig | null>(null)
const pendingFingerprint = ref<OpenAIEvalRouteConfig | null>(null)
const manualSampleMode = ref('quick')
const runningKeys = reactive(new Set<string>())
const runningProgress = reactive(new Map<string, OpenAIEvalRun>())
const prismCatalogs = usePrismEvalCatalogs()

const accountOf = (accountID: number) => accounts.value.find(item => item.id === accountID)
const isPrismRoute = (route: OpenAIEvalRouteConfig) => isPrismAccount(accountOf(route.account_id))
function prismAvailability(route: OpenAIEvalRouteConfig, type: EvalTestType) {
  return prismTestAvailability(type, accountOf(route.account_id), route.requested_model, catalog.value, prismCatalogs.get(route.account_id))
}
/** Applicability is per target: Prism by baseline coverage, OpenAI State Probe by direct OAuth. */
function testAvailable(route: OpenAIEvalRouteConfig, type: EvalTestType) {
  if (isPrismRoute(route)) return prismAvailability(route, type).available
  return type !== 'state_probe' || isDirectOAuthRoute(route)
}
function unavailableReason(route: OpenAIEvalRouteConfig, type: EvalTestType) {
  if (!isPrismRoute(route)) return undefined
  const result = prismAvailability(route, type)
  if (result.available) return undefined
  const model = prismUpstreamModel(accountOf(route.account_id), route.requested_model)
  if (result.reason === 'no_fingerprint_baseline') return t('admin.modelIntegrity.tests.prism.noFingerprintBaseline', { model })
  if (result.reason === 'no_modeltrace_baseline') return t('admin.modelIntegrity.tests.prism.noModelTraceBaseline', { model })
  return t(type === 'state_probe' ? 'admin.modelIntegrity.tests.prism.stateProbeUnsupported' : 'admin.modelIntegrity.tests.prism.unsupported')
}
function testNotice(route: OpenAIEvalRouteConfig, type: EvalTestType) {
  if (!isPrismRoute(route)) return undefined
  const result = prismAvailability(route, type)
  if (!result.available || !result.notice) return undefined
  if (result.notice === 'not_in_catalog') return t('admin.modelIntegrity.tests.prism.notInCatalog', { model: route.requested_model })
  return t('admin.modelIntegrity.tests.prism.modelTraceCoverageUnknown')
}
function prismTargetNote(route: OpenAIEvalRouteConfig) {
  const state = prismCatalogs.get(route.account_id)
  if (!state || state.status === 'loading') return t('admin.modelIntegrity.tests.prism.targetLoading')
  if (state.status === 'error') return t('admin.modelIntegrity.tests.prism.targetCatalogError')
  const upstream = prismUpstreamModel(accountOf(route.account_id), route.requested_model)
  return upstream !== route.requested_model
    ? t('admin.modelIntegrity.tests.prism.targetAlias', { model: upstream })
    : t('admin.modelIntegrity.tests.prism.target')
}
const progressTimers = new Set<ReturnType<typeof setInterval>>()

const filteredTargets = computed(() => {
  const needle = search.value.trim().toLowerCase()
  if (!needle) return config.accounts
  return config.accounts.filter(route => `${accountName(route.account_id)} #${route.account_id} ${route.requested_model} ${route.reasoning_effort}`.toLowerCase().includes(needle))
})
const selected = computed(() => config.accounts.find(route => routeKey(route) === selectedKey.value) ?? null)
const dailyTotal = computed(() => totalDailyRequests(config.accounts, catalog.value, testAvailable))
const maxAttempts = computed(() => normalizeMaxRequestAttempts(config.max_request_attempts))
const dailyMax = computed(() => totalDailyMaxRequests(config.accounts, maxAttempts.value, catalog.value, testAvailable))
// The field may hold an out-of-range draft while typing; the config only ever
// gets a clamped integer, applied on change/blur.
const attemptsInput = ref<number | string>(maxAttempts.value)
watch(maxAttempts, value => { attemptsInput.value = value })
function commitAttempts() {
  const value = normalizeMaxRequestAttempts(attemptsInput.value)
  config.max_request_attempts = value
  attemptsInput.value = value
}
const planCount = computed(() => activeScheduleCount(config.accounts, testAvailable))
const fingerprintModes = computed(() => catalog.value?.fingerprint_modes?.length ? catalog.value.fingerprint_modes : [{ id: 'quick', samples: 60 }, { id: 'standard', samples: 200 }, { id: 'strict', samples: 400 }])

// -- Account-wide automatic-test controls (pause and experimental limits) --
// They live in the shared config and persist with the page's Save. Pausing
// never disables a schedule, removes a target or touches results.
const controlFor = (accountID: number) => findBackgroundControl(config.background_controls, accountID)
const savedControlFor = (accountID: number) => findBackgroundControl(savedBackgroundControls.value, accountID)
const isAccountPaused = (accountID: number) => pausedUntil(controlFor(accountID)) !== null
function updateControl(control: OpenAIEvalBackgroundControl) {
  const list = config.background_controls ?? (config.background_controls = [])
  const index = list.findIndex(item => item.account_id === control.account_id)
  // The live runtime stays attached for display; it is stripped on save.
  const next = { ...control, runtime: savedControlFor(control.account_id)?.runtime ?? null }
  if (index >= 0) list.splice(index, 1, next)
  else list.push(next)
}
const pausedAccounts = computed(() => (config.background_controls ?? [])
  .filter(control => pausedUntil(control) !== null)
  .map(control => {
    const first = config.accounts.find(route => route.account_id === control.account_id)
    return { accountID: control.account_id, firstKey: first ? routeKey(first) : '' }
  }))
function autoNote(route: OpenAIEvalRouteConfig, type: EvalTestType) {
  if (!scheduleOf(route, type).enabled) return undefined
  const until = pausedUntil(controlFor(route.account_id))
  if (until) return t('admin.modelIntegrity.tests.background.panelPaused', { time: formatTime(until.toISOString()) })
  const control = controlFor(route.account_id)
  if (!control?.budget_enabled) return undefined
  const feasibility = runFeasibility(requestsPerRun(route, type, catalog.value), maxRequestsPerRun(route, type, maxAttempts.value, catalog.value), control, accountOf(route.account_id)?.rpm_limit)
  return feasibility.fits ? undefined : t('admin.modelIntegrity.tests.background.panelBlocked', { count: feasibility.nominal, capacity: feasibility.capacity })
}
// A manual run under enabled limits must fit the same window as an automatic one.
const manualControl = computed(() => (pendingFingerprint.value ? savedControlFor(pendingFingerprint.value.account_id) : undefined))
const manualCapacity = computed(() => (manualControl.value?.budget_enabled ? runFeasibility(0, 0, manualControl.value, pendingFingerprint.value ? accountOf(pendingFingerprint.value.account_id)?.rpm_limit : undefined).capacity : Number.POSITIVE_INFINITY))
const manualBlocked = (samples: number) => samples > manualCapacity.value
const selectedManualSamples = computed(() => fingerprintModes.value.find(mode => mode.id === manualSampleMode.value)?.samples ?? 0)

const panelRuns = computed(() => (selected.value && targetRuns.value?.key === routeKey(selected.value) ? [...targetRuns.value.items, ...runs.value] : runs.value))
const historyRuns = computed(() => {
  if (historyScope.value === 'target' && selected.value) {
    return targetRuns.value?.key === routeKey(selected.value) ? targetRuns.value.items : []
  }
  return runs.value
})
const visibleRuns = computed(() => historyRuns.value.filter(run => (!typeFilter.value || run.test_type === typeFilter.value) && (!toneFilter.value || resultTone(run.status) === toneFilter.value || (toneFilter.value === 'neutral' && ['running', 'error'].includes(resultTone(run.status))))))

watch(() => config.accounts.length, () => {
  if (!selected.value) selectedKey.value = config.accounts[0] ? routeKey(config.accounts[0]) : ''
})
watch(selectedKey, () => { void loadTargetRuns() })
// Prism targets need their account catalog for effort choices and coverage notes.
watch(() => config.accounts.filter(isPrismRoute).map(route => route.account_id), ids => {
  for (const id of new Set(ids)) void prismCatalogs.ensure(id)
}, { immediate: true })
watch(selected, value => { if (!value && historyScope.value === 'target') historyScope.value = 'all' })

function autoCount(route: OpenAIEvalRouteConfig) {
  return TEST_TYPES.filter(type => scheduleOf(route, type).enabled && testAvailable(route, type)).length
}

function signalRun(route: OpenAIEvalRouteConfig, type: EvalTestType) {
  return latestRunFor(runs.value, route, type)
}
function signalClass(route: OpenAIEvalRouteConfig, type: EvalTestType) {
  if (!testAvailable(route, type)) return 'signal-na'
  const run = signalRun(route, type)
  return run ? `signal-${resultTone(run.status)}` : 'signal-none'
}
function signalText(route: OpenAIEvalRouteConfig, type: EvalTestType) {
  if (!testAvailable(route, type)) return unavailableReason(route, type) || t('admin.modelIntegrity.tests.onlyDirectOAuth')
  const run = signalRun(route, type)
  return run ? runStatusLabel(t, run) : t('admin.modelIntegrity.tests.neverRun')
}
function signalLabel(route: OpenAIEvalRouteConfig) {
  return TEST_TYPES.map(type => `${t(`admin.modelIntegrity.tests.types.${type}.name`)}: ${signalText(route, type)}`).join('; ')
}

// Eligibility is an account capability derived by the server; until the next
// reload apply the same rules it does so State Probe is neither hidden nor offered wrongly.
function assumeDirectOAuth(accountID: number) {
  return isDirectOAuthAccount(accounts.value.find(item => item.id === accountID))
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
    route.direct_oauth_eligible = assumeDirectOAuth(accountID)
    if (isPrismAccount(accountOf(accountID))) disableUnavailableSchedules(route)
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

/**
 * Changes only the model and effort of one target. The route is replaced, not
 * mutated, so a run that is still in flight keeps the identity it started
 * with. Account, every schedule (State Probe included — it always uses the
 * account default effort) carry over. Past runs keep their own
 * model/effort and are not shown for the new target.
 */
function saveEdit(payload: { model: string; effort: string }) {
  const route = editing.value
  editing.value = null
  const index = route ? config.accounts.indexOf(route) : -1
  if (!route || index < 0) return
  const next: OpenAIEvalRouteConfig = { ...route, requested_model: payload.model, reasoning_effort: payload.effort }
  const key = routeKey(next)
  if (config.accounts.some(item => item !== route && routeKey(item) === key)) {
    appStore.showError(t('admin.modelIntegrity.tests.edit.duplicate', { target: `${payload.model} · ${payload.effort || t('admin.modelIntegrity.common.defaultEffort')}` }))
    return
  }
  if (isPrismRoute(next)) disableUnavailableSchedules(next)
  config.accounts.splice(index, 1, next)
  selectedKey.value = key
  appStore.showSuccess(t('admin.modelIntegrity.tests.edit.updated'))
}

/**
 * Automatic plans for tests a Prism target cannot run are switched off so the
 * scheduler never queues an unsupported probe.
 */
function disableUnavailableSchedules(route: OpenAIEvalRouteConfig) {
  route.bps_mode = 'force_off'
  route.bps_auto = false
  route.direct_oauth_eligible = false
  for (const type of TEST_TYPES) {
    if (!prismAvailability(route, type).available) scheduleOf(route, type).enabled = false
  }
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
const persistedRunFor = (route: OpenAIEvalRouteConfig, type: EvalTestType) => {
  const key = runKey(route, type)
  if (targetRuns.value?.key === routeKey(route) && targetRuns.value.items.some(run => run.test_type === type && run.status === 'running')) return true
  return runs.value.some(run => run.account_id === route.account_id && run.requested_model === route.requested_model && (type === 'state_probe' ? run.reasoning_effort === '' : run.reasoning_effort === route.reasoning_effort) && run.test_type === type && run.status === 'running') || runningKeys.has(key)
}
const isRunning = (route: OpenAIEvalRouteConfig, type: EvalTestType) => persistedRunFor(route, type)

function requestRun(route: OpenAIEvalRouteConfig, type: EvalTestType) {
  if (isRunning(route, type)) return
  if (type === 'fingerprint') {
    pendingFingerprint.value = route
    manualSampleMode.value = route.fingerprint_schedule.sample_mode || 'quick'
    if (manualBlocked(selectedManualSamples.value)) manualSampleMode.value = fingerprintModes.value.find(mode => !manualBlocked(mode.samples))?.id ?? manualSampleMode.value
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
  // Snapshot the identity: the target may be edited while this run is in flight.
  const target = { account_id: route.account_id, requested_model: route.requested_model, reasoning_effort: route.reasoning_effort }
  const key = runKey(route, type)
  runningKeys.add(key)
  let pollTimer: ReturnType<typeof setInterval> | undefined
  try {
    const request: Parameters<typeof accountsAPI.runOpenAIEval>[0] = { ...target, test_type: type }
    if (type === 'fingerprint') request.sample_mode = sampleMode || 'quick'
    if (type === 'candy') request.sample_count = Math.max(1, Math.trunc(Number(route.candy_schedule.sample_count) || 1))
    // State probe reads the same setting: the server clamps it to three chains.
    request.max_attempts = maxAttempts.value
    const pendingRun = accountsAPI.runOpenAIEval(request)
    const poll = async () => {
      try {
        const response = await accountsAPI.listOpenAIEvalRuns({
          account_id: target.account_id,
          requested_model: target.requested_model,
          reasoning_effort: type === 'state_probe' ? '' : target.reasoning_effort,
          test_type: type,
          limit: 5
        })
        const active = (response.items ?? []).find(item => item.status === 'running')
        if (active) runningProgress.set(key, active)
      } catch {
        // Progress is best-effort; the primary run request remains authoritative.
      }
    }
    pollTimer = setInterval(() => { void poll() }, 1000)
    progressTimers.add(pollTimer)
    void poll()
    const run = await pendingRun
    detailRun.value = run
    appStore.showSuccess(t('admin.modelIntegrity.tests.runDone'))
    await Promise.all([loadHistory(), loadTargetRuns(), refreshRuntime()])
  } catch (error) {
    // Upstream errors can echo credentials; the toast shows the cause with them masked.
    const status = (error as { response?: { status?: number }; status?: number })?.response?.status ?? (error as { status?: number })?.status
    if (status === 409 || /already running/i.test(extractApiErrorMessage(error, ''))) {
      appStore.showInfo(t('admin.modelIntegrity.tests.alreadyRunning'))
      await Promise.all([loadHistory(), loadTargetRuns()])
    } else {
      appStore.showError(redactSecrets(extractApiErrorMessage(error, t('admin.modelIntegrity.tests.runFailed'))))
    }
  } finally {
    if (pollTimer) {
      clearInterval(pollTimer)
      progressTimers.delete(pollTimer)
    }
    runningProgress.delete(key)
    runningKeys.delete(key)
  }
}

onBeforeUnmount(() => {
  for (const timer of progressTimers) clearInterval(timer)
  progressTimers.clear()
})

/** Best effort: runtime counters are informational and never block the page. */
async function refreshRuntime() {
  try {
    await refreshBackgroundRuntime()
  } catch {
    // Missing counters read as "not counted yet".
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
      for (const type of TEST_TYPES) {
        const running = targetRuns.value.items.find(run => run.test_type === type && run.status === 'running')
        if (running) runningProgress.set(runKey(route, type), running)
        else runningProgress.delete(runKey(route, type))
      }
    }
  } catch {
    // The global history still backs the panels; a failed narrow query is not fatal.
  }
}

async function handleSave() {
  const sentControls = (config.background_controls ?? []).length
  try {
    const result = await save()
    if (result === 'saved') appStore.showSuccess(t('admin.modelIntegrity.common.saved'))
    // An older server ignores the field; say so rather than showing a pause that is not in force.
    if ((result === 'saved' || result === 'saved_evaluation_failed') && sentControls > 0 && !savedBackgroundControls.value.length) {
      appStore.showError(t('admin.modelIntegrity.tests.background.notPersisted'))
    }
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
.prism-tag { @apply mt-1 flex items-center gap-1.5 text-xs text-fuchsia-700 dark:text-fuchsia-300; }
.budget-row { @apply flex flex-col gap-3 rounded-xl border border-gray-200 bg-white px-4 py-3 dark:border-dark-700 dark:bg-dark-800/60 md:flex-row md:items-start md:justify-between md:gap-6; }
.budget { @apply max-w-[80ch] text-sm text-gray-800 dark:text-gray-200; }
.budget-hint { @apply ml-1 text-gray-500 dark:text-gray-400; }
.attempts { @apply flex max-w-[30rem] flex-col gap-1 md:shrink-0; }
.attempts-field { @apply flex flex-wrap items-center gap-2 text-sm text-gray-700 dark:text-gray-300; }
.attempts-label { @apply font-medium; }
.attempts-input { @apply h-9 w-20 min-w-0 py-1 text-sm tabular-nums; }
.attempts-unit { @apply text-xs text-gray-500 dark:text-gray-400; }
.attempts-hint { @apply text-xs leading-relaxed text-gray-500 dark:text-gray-400; }
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
.signal-likely { @apply bg-emerald-100 ring-1 ring-inset ring-emerald-500 dark:bg-emerald-950/60 dark:ring-emerald-400; }
.signal-attention { @apply bg-amber-500; }
.signal-neutral { @apply bg-gray-400 dark:bg-dark-400; }
.signal-error { @apply bg-rose-500; }
.signal-running { @apply bg-sky-500; }
.signal-none { @apply border border-gray-300 dark:border-dark-500; }
.signal-na { background: repeating-linear-gradient(135deg, transparent 0 2px, theme('colors.gray.300') 2px 3px); @apply border border-gray-200 dark:border-dark-600; }
.detail { @apply rounded-xl border border-gray-200 bg-white px-5 dark:border-dark-700 dark:bg-dark-800/60 sm:px-6; }
.detail-placeholder { @apply flex items-center justify-center py-12 text-sm text-gray-500; }
.detail-head { @apply flex items-start justify-between gap-3 border-b border-gray-100 py-4 dark:border-dark-700; }
.detail-actions { @apply flex shrink-0 items-center gap-1; }
.detail-edit { @apply inline-flex h-9 items-center gap-1.5 rounded-md px-2.5 text-sm text-gray-600 hover:bg-gray-100 hover:text-gray-900 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:text-gray-300 dark:hover:bg-dark-700 dark:hover:text-white; }
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
/*
 * Phones: the 52rem table would push the result column off-screen. Each run
 * becomes a compact block (target and time, test and trigger, then the full
 * result) so the verdict is readable without horizontal scrolling. Samples
 * and cost stay one tap away in the run details.
 */
@media (max-width: 639px) {
  .history-table { min-width: 0; }
  .history-table thead { @apply sr-only; }
  .history-table tbody { @apply block; }
  .history-row { @apply grid grid-cols-[minmax(0,1fr)_auto] gap-x-3 gap-y-1 border-b border-gray-100 py-3 dark:border-dark-700; }
  .history-row:last-child { @apply border-b-0; }
  .history-table td { @apply block border-0 p-0; }
  .history-table .history-target { @apply col-start-1 row-start-1 min-w-0; }
  .history-table .history-time { @apply col-start-2 row-start-1 text-right text-xs; }
  .history-table .history-test { @apply col-start-1 row-start-2 text-xs text-gray-500 dark:text-gray-400; }
  .history-table .history-trigger { @apply col-start-2 row-start-2 text-right text-xs; }
  .history-table .history-result { @apply col-span-2 row-start-3 mt-1 min-w-0 max-w-none; }
  .history-table .history-meta { @apply hidden; }
}
.tone { @apply inline-flex whitespace-nowrap rounded px-1.5 py-0.5 text-xs font-medium; }
.tone-ok { @apply bg-emerald-50 text-emerald-800 dark:bg-emerald-950/40 dark:text-emerald-300; }
.tone-likely { @apply border border-dashed border-emerald-400 text-emerald-800 dark:border-emerald-600 dark:text-emerald-300; }
.tone-attention { @apply bg-amber-100 text-amber-900 dark:bg-amber-900/40 dark:text-amber-200; }
.tone-neutral { @apply bg-gray-100 text-gray-700 dark:bg-dark-700 dark:text-gray-300; }
.tone-error { @apply bg-rose-50 text-rose-800 dark:bg-rose-950/40 dark:text-rose-300; }
.tone-running { @apply bg-sky-50 text-sky-800 dark:bg-sky-950/40 dark:text-sky-300; }
.sample-choice { @apply flex cursor-pointer items-center gap-3 rounded-lg border border-gray-200 px-3 py-2.5 text-sm dark:border-dark-600; }
.sample-choice:has(input:checked) { @apply border-primary-600 bg-primary-50/50 dark:bg-primary-950/30; }
.sample-choice-blocked { @apply cursor-not-allowed opacity-70; }
.paused-summary { @apply mt-2 flex flex-wrap items-center gap-1.5; }
.paused-summary-label { @apply text-gray-700 dark:text-gray-300; }
.paused-chip { @apply rounded-md border border-gray-300 px-2 py-0.5 text-xs text-gray-700 hover:bg-gray-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 disabled:cursor-default disabled:hover:bg-transparent dark:border-dark-500 dark:text-gray-300 dark:hover:bg-dark-700; }
.target-paused { @apply mr-1 rounded bg-gray-100 px-1 py-px font-medium text-gray-700 dark:bg-dark-700 dark:text-gray-300; }
</style>
