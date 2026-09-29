<template>
  <AppLayout>
    <div class="mx-auto w-full max-w-6xl space-y-5 px-1 py-2 sm:px-2">
      <header class="page-header mb-0 flex flex-wrap items-center justify-between gap-3 rounded-3xl bg-white p-5 shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-800 dark:ring-dark-700 sm:p-6">
        <div>
          <h1 class="page-title flex items-center gap-2 text-xl font-black text-gray-900 dark:text-white">
            <span class="inline-flex h-8 w-8 items-center justify-center rounded-xl bg-blue-50 text-blue-500 dark:bg-blue-900/30 dark:text-blue-400"><Icon name="chart" size="sm" /></span>
            {{ t('admin.accounts.evaluations.title') }}
          </h1>
          <p class="page-description mt-1.5 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.evaluations.description') }}</p>
        </div>
        <button class="btn btn-primary" :disabled="saving || loading || !configLoaded || !dirty" @click="save">
            <Icon name="check" size="sm" />
            {{ saving ? t('common.saving') : t('common.save') }}
        </button>
      </header>

      <div class="flex items-start gap-2 rounded-2xl border border-blue-200 bg-blue-50/80 px-4 py-3 text-sm text-blue-800 dark:border-blue-900/60 dark:bg-blue-950/30 dark:text-blue-200">
        <Icon name="infoCircle" size="sm" />
        <span>{{ catalog?.evaluation_notice || t('admin.accounts.evaluations.notice') }}</span>
      </div>

      <nav class="tabs w-full sm:w-auto" :aria-label="t('admin.accounts.evaluations.tabsAria')">
        <button type="button" class="tab" :class="activeTab === 'settings' ? 'tab-active' : ''" @click="activeTab = 'settings'">
          {{ t('admin.accounts.evaluations.settingsTab') }}
        </button>
        <button type="button" class="tab" :class="activeTab === 'history' ? 'tab-active' : ''" @click="activeTab = 'history'">
          {{ t('admin.accounts.evaluations.historyTab') }}
          <span v-if="runs.length" class="tab-count">{{ runs.length }}</span>
        </button>
      </nav>

      <template v-if="activeTab === 'settings'">
      <section class="card divide-y divide-gray-100 overflow-hidden !rounded-3xl !border-0 shadow-sm ring-1 ring-gray-900/5 dark:divide-dark-700 dark:!bg-dark-800 dark:ring-dark-700" aria-labelledby="effects-title">
        <div class="flex flex-wrap items-center justify-between gap-4 px-5 py-4">
          <div>
            <h2 id="effects-title" class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.accounts.evaluations.effectsTitle') }}</h2>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">{{ t('admin.accounts.evaluations.effectsHint') }}</p>
          </div>
          <div class="flex items-center gap-2 text-xs text-gray-600 dark:text-gray-300">
            <span>{{ t('admin.accounts.evaluations.effectsEnabled') }}</span>
            <Toggle v-model="config.effects_enabled" />
          </div>
        </div>
      </section>
      <section class="card divide-y divide-gray-100 overflow-hidden !rounded-3xl !border-0 shadow-sm ring-1 ring-gray-900/5 dark:divide-dark-700 dark:!bg-dark-800 dark:ring-dark-700" aria-labelledby="route-title">
        <div class="flex flex-wrap items-center justify-between gap-4 px-5 py-4">
        <div>
          <h2 id="route-title" class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.accounts.evaluations.routeTitle') }}</h2>
          <p class="mt-0.5 text-xs text-gray-500 dark:text-dark-400">{{ t('admin.accounts.evaluations.routeHint') }}</p>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <select class="input min-w-[170px]" v-model.number="newRoute.account_id" :disabled="loading">
            <option :value="0">{{ t('admin.accounts.evaluations.selectAccount') }}</option>
            <option v-for="account in openaiAccounts" :key="account.id" :value="account.id">
              #{{ account.id }} · {{ account.name }}
            </option>
          </select>
          <select class="input min-w-[170px]" v-model="newRoute.requested_model" :disabled="loading">
            <option value="">{{ t('admin.accounts.evaluations.selectModel') }}</option>
            <option v-for="model in catalog?.items || []" :key="model.id" :value="model.id">
              {{ model.display_name || model.id }}
            </option>
          </select>
          <select class="input min-w-[130px]" v-model="newRoute.reasoning_effort" :disabled="loading">
            <option v-for="effort in catalog?.reasoning_efforts || []" :key="effort" :value="effort">
              {{ effort || t('admin.accounts.evaluations.defaultEffort') }}
            </option>
          </select>
          <button class="btn btn-secondary" :disabled="!canAddRoute" @click="addRoute">
            <Icon name="plus" size="sm" />
            {{ t('admin.accounts.evaluations.addRoute') }}
          </button>
        </div></div>
      </section>

      <div v-if="loading" class="card flex min-h-[160px] items-center justify-center !rounded-3xl !border-0 text-sm text-gray-400 shadow-sm ring-1 ring-gray-900/5 dark:!bg-dark-800 dark:ring-dark-700"><span class="animate-pulse">{{ t('common.loading') }}</span></div>
      <div v-else-if="config.accounts.length === 0" class="empty-state empty-state-dashed">
        <Icon name="beaker" size="xl" class="text-gray-400" />
        <strong>{{ t('admin.accounts.evaluations.emptyTitle') }}</strong>
        <span>{{ t('admin.accounts.evaluations.emptyHint') }}</span>
      </div>

      <section v-else class="route-list" :aria-label="t('admin.accounts.evaluations.routesAria')">
        <article v-for="(route, index) in config.accounts" :key="routeKey(route)" class="route-row">
          <div class="route-identity">
            <div class="route-id">{{ accountName(route.account_id) }}</div>
            <div class="route-model">
              <strong>{{ route.requested_model }}</strong>
              <span>{{ route.reasoning_effort || t('admin.accounts.evaluations.defaultEffort') }}</span>
            </div>
          </div>
          <div class="test-column">
            <div class="test-heading">
              <div><strong>{{ t('admin.accounts.evaluations.candy') }}</strong><small>{{ t('admin.accounts.evaluations.candyHint') }}</small></div>
            </div>
            <div class="test-controls">
              <div class="switch-row"><Toggle v-model="route.candy_schedule.enabled" /><span>{{ t('admin.accounts.evaluations.automatic') }}</span></div>
              <select v-model.number="route.candy_schedule.interval_seconds" class="input min-h-9" :disabled="!route.candy_schedule.enabled" @change="normalizeSchedule(route.candy_schedule, 'candy')">
                <option v-if="!candyIntervals.includes(route.candy_schedule.interval_seconds)" :value="route.candy_schedule.interval_seconds">{{ customIntervalLabel(route.candy_schedule.interval_seconds) }}</option>
                <option :value="900">{{ t('admin.accounts.evaluations.interval15m') }}</option><option :value="3600">{{ t('admin.accounts.evaluations.interval1h') }}</option><option :value="21600">{{ t('admin.accounts.evaluations.interval6h') }}</option><option :value="86400">{{ t('admin.accounts.evaluations.interval24h') }}</option>
              </select>
              <input v-model.number="route.candy_schedule.jitter_seconds" class="input jitter-input" type="number" min="0" :max="Math.max(0, route.candy_schedule.interval_seconds - 900)" :disabled="!route.candy_schedule.enabled || route.candy_schedule.interval_seconds <= 900" :aria-label="t('admin.accounts.evaluations.jitter')" @blur="normalizeSchedule(route.candy_schedule, 'candy')" />
              <button class="icon-button" :disabled="dirty || isRunning(route, 'candy')" :title="t('admin.accounts.evaluations.runNow')" @click="requestRun(route, 'candy')"><Icon name="play" size="sm" /></button>
            </div>
            <small v-if="route.candy_schedule.enabled && route.candy_schedule.interval_seconds <= 900" class="schedule-note">{{ t('admin.accounts.evaluations.jitterMinimum') }}</small>
            <div v-if="latestRun(route, 'candy')" class="mt-2 flex items-center gap-1 text-xs"><span class="status" :class="statusClass(latestRun(route, 'candy')!.status)"><Icon :name="statusIcon(latestRun(route, 'candy')!.status)" size="xs" /> {{ statusLabel(latestRun(route, 'candy')!.status) }}</span><span class="result-explanation">{{ reasonLabel(latestRun(route, 'candy')!.outcome.reason, latestRun(route, 'candy')!.status, latestRun(route, 'candy')!) }}</span></div>
          </div>
          <div class="test-column">
            <div class="test-heading">
              <div><strong>{{ t('admin.accounts.evaluations.fingerprint') }}</strong><small>{{ t('admin.accounts.evaluations.fingerprintHint') }}</small></div>
            </div>
            <div class="test-controls">
              <div class="switch-row"><Toggle v-model="route.fingerprint_schedule.enabled" /><span>{{ t('admin.accounts.evaluations.automatic') }}</span></div>
              <select v-model="route.fingerprint_schedule.sample_mode" class="input min-h-9" :disabled="!route.fingerprint_schedule.enabled">
                <option v-if="route.fingerprint_schedule.sample_mode && !fingerprintModes.includes(route.fingerprint_schedule.sample_mode)" :value="route.fingerprint_schedule.sample_mode">{{ customModeLabel(route.fingerprint_schedule.sample_mode) }}</option>
                <option v-for="mode in catalog?.fingerprint_modes || []" :key="mode.id" :value="mode.id">{{ fingerprintModeLabel(mode.id) }} · {{ mode.samples }}</option>
              </select>
              <select v-model.number="route.fingerprint_schedule.interval_seconds" class="input min-h-9" :disabled="!route.fingerprint_schedule.enabled" @change="normalizeSchedule(route.fingerprint_schedule, 'fingerprint')">
                <option v-if="!fingerprintIntervals.includes(route.fingerprint_schedule.interval_seconds)" :value="route.fingerprint_schedule.interval_seconds">{{ customIntervalLabel(route.fingerprint_schedule.interval_seconds) }}</option>
                <option :value="86400">{{ t('admin.accounts.evaluations.interval24h') }}</option><option :value="259200">{{ t('admin.accounts.evaluations.interval3d') }}</option><option :value="604800">{{ t('admin.accounts.evaluations.interval7d') }}</option>
              </select>
              <input v-model.number="route.fingerprint_schedule.jitter_seconds" class="input jitter-input" type="number" min="0" :max="Math.max(0, route.fingerprint_schedule.interval_seconds - 86400)" :disabled="!route.fingerprint_schedule.enabled || route.fingerprint_schedule.interval_seconds <= 86400" :aria-label="t('admin.accounts.evaluations.jitter')" @blur="normalizeSchedule(route.fingerprint_schedule, 'fingerprint')" />
              <button class="icon-button" :disabled="dirty || isRunning(route, 'fingerprint')" :title="t('admin.accounts.evaluations.runNow')" @click="requestRun(route, 'fingerprint')"><Icon name="play" size="sm" /></button>
            </div>
            <div v-if="latestRun(route, 'fingerprint')" class="mt-2 flex items-center gap-1 text-xs"><span class="status" :class="statusClass(latestRun(route, 'fingerprint')!.status)"><Icon :name="statusIcon(latestRun(route, 'fingerprint')!.status)" size="xs" /> {{ statusLabel(latestRun(route, 'fingerprint')!.status) }}</span><span class="result-explanation">{{ reasonLabel(latestRun(route, 'fingerprint')!.outcome.reason, latestRun(route, 'fingerprint')!.status, latestRun(route, 'fingerprint')!) }}</span></div>
            <small class="schedule-note">{{ t('admin.accounts.evaluations.fingerprintMinimum') }} · {{ t('admin.accounts.evaluations.jitter') }}: {{ route.fingerprint_schedule.jitter_seconds }}s</small>
            <small v-if="route.fingerprint_schedule.enabled && route.fingerprint_schedule.interval_seconds <= 86400" class="schedule-note">{{ t('admin.accounts.evaluations.jitterMinimum') }}</small>
          </div>
          <div class="test-column">
            <div class="test-heading"><div><strong>{{ t('admin.accounts.evaluations.modelTrace') }}</strong><small>{{ t('admin.accounts.evaluations.modelTraceHint') }}</small></div></div>
            <div class="test-controls">
              <div class="switch-row"><Toggle v-model="route.modeltrace_schedule.enabled" /><span>{{ t('admin.accounts.evaluations.automatic') }}</span></div>
              <select v-model.number="route.modeltrace_schedule.interval_seconds" class="input min-h-9" :disabled="!route.modeltrace_schedule.enabled" @change="normalizeSchedule(route.modeltrace_schedule, 'modeltrace')">
                <option v-if="!modelTraceIntervals.includes(route.modeltrace_schedule.interval_seconds)" :value="route.modeltrace_schedule.interval_seconds">{{ customIntervalLabel(route.modeltrace_schedule.interval_seconds) }}</option>
                <option :value="86400">{{ t('admin.accounts.evaluations.interval24h') }}</option><option :value="259200">{{ t('admin.accounts.evaluations.interval3d') }}</option><option :value="604800">{{ t('admin.accounts.evaluations.interval7d') }}</option>
              </select>
              <input v-model.number="route.modeltrace_schedule.jitter_seconds" class="input jitter-input" type="number" min="0" :max="Math.max(0, route.modeltrace_schedule.interval_seconds - 86400)" :disabled="!route.modeltrace_schedule.enabled" :aria-label="t('admin.accounts.evaluations.jitter')" @blur="normalizeSchedule(route.modeltrace_schedule, 'modeltrace')" />
              <button class="icon-button" :disabled="dirty || isRunning(route, 'modeltrace')" :title="t('admin.accounts.evaluations.runNow')" @click="requestRun(route, 'modeltrace')"><Icon name="play" size="sm" /></button>
            </div>
            <small class="schedule-note">{{ t('admin.accounts.evaluations.modelTraceAlertOnly') }}</small>
            <div v-if="latestRun(route, 'modeltrace')" class="mt-2 text-xs"><span class="status" :class="statusClass(latestRun(route, 'modeltrace')!.status)">{{ statusLabel(latestRun(route, 'modeltrace')!.status) }}</span><span class="result-explanation ml-1">{{ modelTraceSummary(latestRun(route, 'modeltrace')!) }}</span></div>
          </div>
          <button class="icon-button remove-button" :title="t('admin.accounts.evaluations.removeRoute')" @click="config.accounts.splice(index, 1)"><Icon name="trash" size="sm" /></button>
        </article>
      </section>
      </template>

      <section v-if="activeTab === 'history'" class="history-section">
        <div class="section-heading">
          <div><h2>{{ t('admin.accounts.evaluations.historyTitle') }}</h2><p>{{ t('admin.accounts.evaluations.historyHint') }}</p></div>
          <button class="btn btn-secondary" :disabled="historyLoading" @click="loadHistory"><Icon name="refresh" size="sm" />{{ t('common.refresh') }}</button>
        </div>
        <div v-if="historyLoading" class="empty-state">{{ t('common.loading') }}</div>
        <div v-else-if="runs.length === 0" class="empty-state">{{ t('admin.accounts.evaluations.noHistory') }}</div>
        <div v-else class="history-table-wrap">
          <table class="history-table">
            <thead><tr><th>{{ t('admin.accounts.evaluations.route') }}</th><th>{{ t('admin.accounts.evaluations.type') }}</th><th>{{ t('admin.accounts.evaluations.result') }}</th><th>{{ t('admin.accounts.evaluations.samples') }}</th><th>{{ t('admin.accounts.evaluations.cost') }}</th><th>{{ t('admin.accounts.evaluations.time') }}</th></tr></thead>
            <tbody>
              <tr v-for="run in runs" :key="run.id">
                <td>#{{ run.account_id }} · {{ run.requested_model }} · {{ run.reasoning_effort || '-' }}</td>
                <td>{{ run.test_type === 'candy' ? t('admin.accounts.evaluations.candy') : run.test_type === 'fingerprint' ? t('admin.accounts.evaluations.fingerprint') : t('admin.accounts.evaluations.modelTrace') }}</td>
                <td>
                  <span class="status" :class="statusClass(run.status)">{{ statusLabel(run.status) }}</span>
                  <small class="result-explanation">{{ reasonLabel(run.outcome.reason, run.status, run) }}</small>
                  <small v-if="run.outcome.fingerprint" class="result-metric">JSD {{ formatMetric(run.outcome.fingerprint.mean_jsd) }} · p {{ formatMetric(run.outcome.fingerprint.p_value) }}</small>
                  <small v-if="run.error" class="result-debug">{{ run.error }}</small>
                </td>
                <td>{{ run.outcome.sample_count }}/{{ run.outcome.expected_count }}</td>
                <td>{{ formatCost(run.cost_estimate_usd) }}</td>
                <td>{{ formatTime(run.finished_at || run.started_at) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <p class="footer-note">{{ t('admin.accounts.evaluations.baselineNote', { version: catalog?.baseline_version || '-' }) }}</p>
    </div>

    <BaseDialog :show="showRunResult" :title="t('admin.accounts.evaluations.resultTitle')" @close="showRunResult = false">
      <div v-if="lastRun" class="space-y-3 text-sm">
        <div class="flex items-center gap-2">
          <span class="status" :class="statusClass(lastRun.status)"><Icon :name="statusIcon(lastRun.status)" size="sm" /> <strong>{{ statusLabel(lastRun.status) }}</strong></span>
        </div>
        <p class="text-gray-600 dark:text-gray-300">{{ reasonLabel(lastRun.outcome.reason, lastRun.status, lastRun) }}</p>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.evaluations.samples') }}: {{ lastRun.outcome.sample_count }}/{{ lastRun.outcome.expected_count }} · {{ formatCost(lastRun.cost_estimate_usd) }}</p>
        <details v-if="lastRun.error" class="text-xs text-gray-500">
          <summary>{{ t('admin.accounts.evaluations.debugDetails') }}</summary>
          <code class="mt-2 block whitespace-pre-wrap font-mono">{{ lastRun.error }}</code>
        </details>
      </div>
      <template #footer>
        <button class="btn btn-secondary" type="button" @click="showRunResult = false">{{ t('common.close') }}</button>
      </template>
    </BaseDialog>

    <BaseDialog :show="showRunSetup" :title="t('admin.accounts.evaluations.runSetupTitle')" width="narrow" @close="closeRunSetup">
      <div v-if="pendingRun" class="space-y-4 text-sm">
        <p class="text-gray-600 dark:text-gray-300">{{ accountName(pendingRun.route.account_id) }} · {{ pendingRun.route.requested_model }}</p>
        <label class="block">
          <span class="input-label">{{ t('admin.accounts.evaluations.sampleModeLabel') }}</span>
          <select v-model="manualSampleMode" class="input mt-1 w-full">
            <option v-for="mode in catalog?.fingerprint_modes || []" :key="mode.id" :value="mode.id">{{ fingerprintModeLabel(mode.id) }} · {{ mode.samples }} {{ t('admin.accounts.evaluations.requests') }}</option>
          </select>
        </label>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.evaluations.runCostHint') }}</p>
      </div>
      <template #footer>
        <div class="flex justify-end gap-2">
          <button class="btn btn-secondary" type="button" @click="closeRunSetup">{{ t('common.cancel') }}</button>
          <button class="btn btn-primary" type="button" :disabled="!pendingRun" @click="confirmRun">{{ t('admin.accounts.evaluations.runNow') }}</button>
        </div>
      </template>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Toggle from '@/components/common/Toggle.vue'
import { accountsAPI, type OpenAIEvalConfig, type OpenAIEvalModelCatalog, type OpenAIEvalRouteConfig, type OpenAIEvalRun } from '@/api/admin/accounts'
import type { AccountListItem } from '@/types'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const appStore = useAppStore()
const loading = ref(true)
const configLoaded = ref(false)
const activeTab = ref<'settings' | 'history'>('settings')
const saving = ref(false)
const historyLoading = ref(false)
const catalog = ref<OpenAIEvalModelCatalog | null>(null)
const openaiAccounts = ref<AccountListItem[]>([])
const runs = ref<OpenAIEvalRun[]>([])
const runningKeys = reactive(new Set<string>())
const config = reactive<OpenAIEvalConfig>({ effects_enabled: false, accounts: [] })
const savedSnapshot = ref('')
const lastRun = ref<OpenAIEvalRun | null>(null)
const showRunResult = ref(false)
const showRunSetup = ref(false)
const manualSampleMode = ref('quick')
const pendingRun = ref<{ route: OpenAIEvalRouteConfig; testType: 'candy' | 'fingerprint' } | null>(null)
const newRoute = reactive({ account_id: 0, requested_model: '', reasoning_effort: '' })

const canAddRoute = computed(() => newRoute.account_id > 0 && newRoute.requested_model.length > 0)
const dirty = computed(() => savedSnapshot.value !== JSON.stringify(config))
const routeKey = (route: OpenAIEvalRouteConfig) => `${route.account_id}:${route.requested_model}:${route.reasoning_effort}`
const emptySchedule = (type: 'candy' | 'fingerprint' | 'modeltrace') => ({ enabled: false, interval_seconds: type === 'candy' ? 900 : 86400, jitter_seconds: 0, ...(type === 'fingerprint' ? { sample_mode: 'quick' } : {}) })
const candyIntervals = [900, 3600, 21600, 86400]
const fingerprintIntervals = [86400, 259200, 604800]
const modelTraceIntervals = [86400, 259200, 604800]
const fingerprintModes = ['quick', 'standard', 'strict']

function fingerprintModeLabel(mode: string) {
  const key = `admin.accounts.evaluations.sampleMode.${mode}`
  const translated = t(key)
  return translated === key ? mode : translated
}

function customModeLabel(mode: string) {
  return t('admin.accounts.evaluations.customMode', { mode })
}

function customIntervalLabel(seconds: number) {
  return t('admin.accounts.evaluations.customInterval', { value: seconds >= 86400 ? `${(seconds / 86400).toFixed(1)} d` : `${Math.round(seconds / 3600)} h` })
}

function normalizeSchedule(schedule: OpenAIEvalRouteConfig['candy_schedule'], type: 'candy' | 'fingerprint' | 'modeltrace') {
  const minimum = type === 'candy' ? 15 * 60 : 24 * 60 * 60
  const rawInterval = Number(schedule.interval_seconds)
  const interval = Number.isFinite(rawInterval) ? Math.trunc(rawInterval) : minimum
  schedule.interval_seconds = Math.min(Math.max(interval, minimum), 30 * 24 * 60 * 60)
  const rawJitter = Number(schedule.jitter_seconds)
  const jitter = Number.isFinite(rawJitter) ? Math.trunc(rawJitter) : 0
  schedule.jitter_seconds = Math.min(Math.max(jitter, 0), Math.max(0, schedule.interval_seconds - minimum))
}

function normalizeConfig() {
  for (const route of config.accounts) {
    route.modeltrace_schedule ||= emptySchedule('modeltrace')
    normalizeSchedule(route.candy_schedule, 'candy')
    normalizeSchedule(route.fingerprint_schedule, 'fingerprint')
    normalizeSchedule(route.modeltrace_schedule, 'modeltrace')
  }
}

function addRoute() {
  if (!canAddRoute.value || config.accounts.some(route => route.account_id === newRoute.account_id && route.requested_model === newRoute.requested_model && route.reasoning_effort === newRoute.reasoning_effort)) return
  config.accounts.push({ account_id: newRoute.account_id, requested_model: newRoute.requested_model, reasoning_effort: newRoute.reasoning_effort, candy_schedule: emptySchedule('candy'), fingerprint_schedule: emptySchedule('fingerprint'), modeltrace_schedule: emptySchedule('modeltrace') })
}

function accountName(accountID: number) {
  const account = openaiAccounts.value.find(item => item.id === accountID)
  return account ? `${account.name} · #${account.id}` : `#${accountID}`
}

function latestRun(route: OpenAIEvalRouteConfig, testType: 'candy' | 'fingerprint' | 'modeltrace') {
  return runs.value.find(run => run.account_id === route.account_id && run.requested_model === route.requested_model && run.reasoning_effort === route.reasoning_effort && run.test_type === testType)
}

async function load() {
  loading.value = true
  configLoaded.value = false
  try {
    const [meta, saved, accountPage] = await Promise.all([accountsAPI.getOpenAIEvalModels(), accountsAPI.getOpenAIEvalConfig(), accountsAPI.list(1, 500, { platform: 'openai', lite: '1' })])
    catalog.value = meta
    openaiAccounts.value = accountPage.items
    config.effects_enabled = saved.effects_enabled
    config.accounts = saved.accounts
    normalizeConfig()
    savedSnapshot.value = JSON.stringify(config)
    configLoaded.value = true
    newRoute.reasoning_effort = meta.reasoning_efforts[0] || ''
    await loadHistory().catch(() => undefined)
  } finally {
    loading.value = false
  }
}

async function save() {
  if (!configLoaded.value) return
  normalizeConfig()
  saving.value = true
  try {
    const saved = await accountsAPI.saveOpenAIEvalConfig({ effects_enabled: config.effects_enabled, accounts: config.accounts })
    config.effects_enabled = saved.effects_enabled
    config.accounts = saved.accounts
    savedSnapshot.value = JSON.stringify(config)
    appStore.showSuccess(t('admin.accounts.evaluations.saveSuccess'))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.accounts.evaluations.saveFailed')))
  } finally { saving.value = false }
}

async function runNow(route: OpenAIEvalRouteConfig, testType: 'candy' | 'fingerprint' | 'modeltrace', sampleMode?: string) {
  const key = `${routeKey(route)}:${testType}`
  if (runningKeys.has(key)) return
  runningKeys.add(key)
  try {
    const request: Parameters<typeof accountsAPI.runOpenAIEval>[0] = { account_id: route.account_id, requested_model: route.requested_model, reasoning_effort: route.reasoning_effort, test_type: testType }
    if (testType === 'fingerprint') request.sample_mode = sampleMode || route.fingerprint_schedule.sample_mode || 'quick'
    lastRun.value = await accountsAPI.runOpenAIEval(request)
    showRunResult.value = true
    appStore.showSuccess(t('admin.accounts.evaluations.runFinished'))
    await loadHistory().catch(() => undefined)
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.accounts.evaluations.runFailed')))
  } finally { runningKeys.delete(key) }
}

function requestRun(route: OpenAIEvalRouteConfig, testType: 'candy' | 'fingerprint' | 'modeltrace') {
  if (!configLoaded.value || dirty.value || isRunning(route, testType)) return
  if (testType === 'fingerprint') {
    manualSampleMode.value = route.fingerprint_schedule.sample_mode || 'quick'
    pendingRun.value = { route, testType }
    showRunSetup.value = true
    return
  }
  void runNow(route, testType)
}

function closeRunSetup() {
  showRunSetup.value = false
  pendingRun.value = null
}

function confirmRun() {
  const request = pendingRun.value
  if (!request) return
  closeRunSetup()
  void runNow(request.route, request.testType, manualSampleMode.value)
}

const isRunning = (route: OpenAIEvalRouteConfig, testType: 'candy' | 'fingerprint' | 'modeltrace') => runningKeys.has(`${routeKey(route)}:${testType}`)

function modelTraceSummary(run: OpenAIEvalRun) {
  const trace = run.outcome.modeltrace
  if (!trace) return t('admin.accounts.evaluations.modelTraceInsufficient')
  return `${trace.prediction || '-'} · ${(trace.probability || 0).toFixed(2)}`
}

async function loadHistory() {
  historyLoading.value = true
  try { runs.value = (await accountsAPI.listOpenAIEvalRuns({ limit: 100 })).items } finally { historyLoading.value = false }
}

const statusLabel = (status: string) => {
  const key = `admin.accounts.evaluations.status.${status}`
  const translated = t(key)
  return translated === key ? status : translated
}
const reasonLabel = (reason: string | undefined, status: string, run?: OpenAIEvalRun) => {
  if (!reason) return status === 'running' ? t('admin.accounts.evaluations.reason.running') : t('admin.accounts.evaluations.reason.noDetail')
  const key = `admin.accounts.evaluations.reason.${reason}`
  const translated = t(key)
  if (translated === key) return t('admin.accounts.evaluations.reason.unknown', { code: reason })
  return t(key, { valid: run?.outcome.sample_count ?? '-', required: run?.outcome.expected_count ?? '-', answer: catalog.value?.candy.expected_answer ?? '-' })
}
const statusClass = (status: string) => {
  if (status === 'pass' || status === 'consistent') return 'status-ok'
  if (status === 'warning' || status === 'different') return 'status-warn'
  // error/insufficient/uncertain mean that the run cannot establish a verdict;
  // keep them neutral so an operational retry is not presented as degradation.
  if (status === 'running') return 'status-running'
  return 'status-neutral'
}
const statusIcon = (status: string) => status === 'pass' || status === 'consistent' ? 'check' : status === 'warning' || status === 'different' ? 'exclamationTriangle' : status === 'running' ? 'refresh' : 'infoCircle'
const formatCost = (value?: number | null) => value == null ? t('admin.accounts.evaluations.costPending') : `$${value.toFixed(4)}`
const formatMetric = (value?: number | null) => value == null ? '-' : value.toFixed(4)
const formatTime = (value: string) => new Date(value).toLocaleString()
onMounted(() => { load().catch((error) => { loading.value = false; appStore.showError(extractApiErrorMessage(error, t('admin.accounts.evaluations.loadFailed'))) }) })
</script>

<style scoped>
.test-controls, .switch-row { @apply flex items-center gap-2; }
.switch-row { @apply text-xs text-gray-600 dark:text-gray-300; }
.tab-count { @apply rounded-full bg-gray-100 px-1.5 py-0.5 text-[11px] text-gray-500 dark:bg-dark-700 dark:text-gray-300; }
.jitter-input { @apply w-20; }
.route-list { @apply divide-y divide-gray-100 overflow-hidden rounded-3xl bg-white shadow-sm ring-1 ring-gray-900/5 dark:divide-dark-700 dark:bg-dark-800 dark:ring-dark-700; }
.route-row { @apply grid gap-4 px-5 py-5 lg:grid-cols-[190px_repeat(3,minmax(0,1fr))_32px] lg:items-center; }
.route-identity { @apply flex items-center gap-3; }
.route-id { @apply text-xs font-semibold text-gray-500 dark:text-gray-400; }
.route-model { @apply flex min-w-0 flex-col; }
.route-model strong { @apply truncate text-sm text-gray-900 dark:text-white; }
.route-model span, .test-heading small, .schedule-note { @apply text-xs text-gray-500 dark:text-gray-400; }
.test-column { @apply min-w-0 rounded-2xl border border-gray-100 bg-gray-50/70 p-3 dark:border-dark-700 dark:bg-dark-900/30; }
.test-heading { @apply mb-2 flex items-center gap-2; }
.test-heading div { @apply flex min-w-0 flex-col; }
.test-heading strong { @apply text-sm text-gray-800 dark:text-gray-100; }
.icon-button { @apply inline-flex h-8 w-8 items-center justify-center border border-gray-300 text-gray-600 hover:bg-gray-100 dark:border-dark-600 dark:text-gray-300 dark:hover:bg-dark-700; }
.remove-button { @apply justify-self-end text-gray-400 hover:text-red-600; }
.schedule-note { @apply mt-2 block; }
.history-section { @apply space-y-3; }
.history-table-wrap { @apply overflow-x-auto rounded-3xl bg-white shadow-sm ring-1 ring-gray-900/5 dark:bg-dark-800 dark:ring-dark-700; }
.history-table { @apply min-w-full text-left text-xs; }
.history-table th { @apply whitespace-nowrap border-b border-gray-200 bg-gray-50 px-3 py-2 font-semibold text-gray-500 dark:border-dark-700 dark:bg-dark-800 dark:text-gray-400; }
.history-table td { @apply border-b border-gray-100 px-3 py-2 text-gray-700 last:border-0 dark:border-dark-800 dark:text-gray-300; }
.history-table td small { @apply mt-0.5 block text-gray-500; }
.status { @apply inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 font-medium; }
.status-ok { @apply bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300; }
.status-warn { @apply bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-200; }
.status-neutral { @apply bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300; }
.status-running { @apply bg-blue-100 text-blue-700 dark:bg-blue-900/30 dark:text-blue-300; }
.result-explanation { @apply text-gray-600 dark:text-gray-300; }
.result-metric { @apply text-gray-500 dark:text-gray-400; }
.result-debug { @apply font-mono text-[10px] text-gray-400; }
.empty-state { @apply flex min-h-24 flex-col items-center justify-center gap-2 py-8 text-sm text-gray-500 dark:text-gray-400; }
.empty-state-dashed { @apply border border-dashed border-gray-300 dark:border-dark-600; }
.footer-note { @apply text-xs text-gray-500 dark:text-gray-400; }
@media (max-width: 640px) { .test-controls { @apply flex-wrap; } }
</style>
