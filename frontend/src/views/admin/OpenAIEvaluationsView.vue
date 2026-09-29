<template>
  <AppLayout>
    <div class="evaluation-page">
      <header class="evaluation-header">
        <div>
      <p class="eyebrow">{{ t('admin.accounts.evaluations.eyebrow') }}</p>
          <h1>{{ t('admin.accounts.evaluations.title') }}</h1>
          <p class="description">{{ t('admin.accounts.evaluations.description') }}</p>
        </div>
        <div class="header-actions">
          <label class="switch-row">
            <input v-model="config.effects_enabled" type="checkbox" />
            <span>{{ t('admin.accounts.evaluations.effectsEnabled') }}</span>
          </label>
          <button class="btn btn-primary" :disabled="saving || loading" @click="save">
            <Icon name="check" size="sm" />
            {{ saving ? t('common.saving') : t('common.save') }}
          </button>
        </div>
      </header>

      <div class="notice notice-blue">
        <Icon name="infoCircle" size="sm" />
        <span>{{ catalog?.evaluation_notice || t('admin.accounts.evaluations.notice') }}</span>
      </div>

      <section class="route-toolbar" aria-labelledby="route-title">
        <div>
          <h2 id="route-title">{{ t('admin.accounts.evaluations.routeTitle') }}</h2>
          <p>{{ t('admin.accounts.evaluations.routeHint') }}</p>
        </div>
        <div class="route-controls">
          <select v-model.number="newRoute.account_id" :disabled="loading">
            <option :value="0">{{ t('admin.accounts.evaluations.selectAccount') }}</option>
            <option v-for="account in openaiAccounts" :key="account.id" :value="account.id">
              #{{ account.id }} · {{ account.name }}
            </option>
          </select>
          <select v-model="newRoute.requested_model" :disabled="loading">
            <option value="">{{ t('admin.accounts.evaluations.selectModel') }}</option>
            <option v-for="model in catalog?.items || []" :key="model.id" :value="model.id">
              {{ model.display_name || model.id }}
            </option>
          </select>
          <select v-model="newRoute.reasoning_effort" :disabled="loading">
            <option v-for="effort in catalog?.reasoning_efforts || []" :key="effort" :value="effort">
              {{ effort || t('admin.accounts.evaluations.defaultEffort') }}
            </option>
          </select>
          <button class="btn btn-secondary" :disabled="!canAddRoute" @click="addRoute">
            <Icon name="plus" size="sm" />
            {{ t('admin.accounts.evaluations.addRoute') }}
          </button>
        </div>
      </section>

      <div v-if="loading" class="empty-state">{{ t('common.loading') }}</div>
      <div v-else-if="config.accounts.length === 0" class="empty-state empty-state-dashed">
        <Icon name="beaker" size="xl" class="text-gray-400" />
        <strong>{{ t('admin.accounts.evaluations.emptyTitle') }}</strong>
        <span>{{ t('admin.accounts.evaluations.emptyHint') }}</span>
      </div>

      <section v-else class="route-list" aria-label="Evaluation routes">
        <article v-for="(route, index) in config.accounts" :key="routeKey(route)" class="route-row">
          <div class="route-identity">
            <div class="route-id">#{{ route.account_id }}</div>
            <div class="route-model">
              <strong>{{ route.requested_model }}</strong>
              <span>{{ route.reasoning_effort || t('admin.accounts.evaluations.defaultEffort') }}</span>
            </div>
          </div>
          <div class="test-column">
            <div class="test-heading">
              <span class="test-mark candy-mark">C</span>
              <div><strong>{{ t('admin.accounts.evaluations.candy') }}</strong><small>{{ t('admin.accounts.evaluations.candyHint') }}</small></div>
            </div>
            <div class="test-controls">
              <label class="switch-row"><input v-model="route.candy_schedule.enabled" type="checkbox" /><span>{{ t('admin.accounts.evaluations.automatic') }}</span></label>
              <select v-model.number="route.candy_schedule.interval_seconds" :disabled="!route.candy_schedule.enabled">
                <option :value="900">15 min</option><option :value="3600">1 h</option><option :value="21600">6 h</option><option :value="86400">24 h</option>
              </select>
              <input v-model.number="route.candy_schedule.jitter_seconds" class="jitter-input" type="number" min="0" :max="Math.max(0, route.candy_schedule.interval_seconds - 900)" :disabled="!route.candy_schedule.enabled" :aria-label="t('admin.accounts.evaluations.jitter')" />
              <button class="icon-button" :title="t('admin.accounts.evaluations.runNow')" @click="runNow(route, 'candy')"><Icon name="play" size="sm" /></button>
            </div>
          </div>
          <div class="test-column">
            <div class="test-heading">
              <span class="test-mark fingerprint-mark">F</span>
              <div><strong>{{ t('admin.accounts.evaluations.fingerprint') }}</strong><small>{{ t('admin.accounts.evaluations.fingerprintHint') }}</small></div>
            </div>
            <div class="test-controls">
              <label class="switch-row"><input v-model="route.fingerprint_schedule.enabled" type="checkbox" /><span>{{ t('admin.accounts.evaluations.automatic') }}</span></label>
              <select v-model="route.fingerprint_schedule.sample_mode" :disabled="!route.fingerprint_schedule.enabled">
                <option v-for="mode in catalog?.fingerprint_modes || []" :key="mode.id" :value="mode.id">{{ mode.id }} · {{ mode.samples }}</option>
              </select>
              <input v-model.number="route.fingerprint_schedule.jitter_seconds" class="jitter-input" type="number" min="0" :max="Math.max(0, route.fingerprint_schedule.interval_seconds - 86400)" :disabled="!route.fingerprint_schedule.enabled" :aria-label="t('admin.accounts.evaluations.jitter')" />
              <button class="icon-button" :title="t('admin.accounts.evaluations.runNow')" @click="runNow(route, 'fingerprint')"><Icon name="play" size="sm" /></button>
            </div>
            <small class="schedule-note">{{ t('admin.accounts.evaluations.fingerprintMinimum') }} · {{ t('admin.accounts.evaluations.jitter') }}: {{ route.fingerprint_schedule.jitter_seconds }}s</small>
          </div>
          <button class="icon-button remove-button" :title="t('admin.accounts.evaluations.removeRoute')" @click="config.accounts.splice(index, 1)"><Icon name="trash" size="sm" /></button>
        </article>
      </section>

      <section class="history-section">
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
                <td>{{ run.test_type === 'candy' ? t('admin.accounts.evaluations.candy') : t('admin.accounts.evaluations.fingerprint') }}</td>
                <td><span class="status" :class="statusClass(run.status)">{{ run.status }}</span><small v-if="run.outcome.reason">{{ run.outcome.reason }}</small><small v-if="run.outcome.fingerprint">JSD {{ formatMetric(run.outcome.fingerprint.mean_jsd) }} · p {{ formatMetric(run.outcome.fingerprint.p_value) }}</small></td>
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
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import { accountsAPI, type OpenAIEvalConfig, type OpenAIEvalModelCatalog, type OpenAIEvalRouteConfig, type OpenAIEvalRun } from '@/api/admin/accounts'
import type { AccountListItem } from '@/types'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'

const { t } = useI18n()
const appStore = useAppStore()
const loading = ref(true)
const saving = ref(false)
const historyLoading = ref(false)
const catalog = ref<OpenAIEvalModelCatalog | null>(null)
const openaiAccounts = ref<AccountListItem[]>([])
const runs = ref<OpenAIEvalRun[]>([])
const config = reactive<OpenAIEvalConfig>({ effects_enabled: false, accounts: [] })
const newRoute = reactive({ account_id: 0, requested_model: '', reasoning_effort: '' })

const canAddRoute = computed(() => newRoute.account_id > 0 && newRoute.requested_model.length > 0)
const routeKey = (route: OpenAIEvalRouteConfig) => `${route.account_id}:${route.requested_model}:${route.reasoning_effort}`
const emptySchedule = (type: 'candy' | 'fingerprint') => ({ enabled: false, interval_seconds: type === 'candy' ? 900 : 86400, jitter_seconds: 0, ...(type === 'fingerprint' ? { sample_mode: 'quick' } : {}) })

function addRoute() {
  if (!canAddRoute.value || config.accounts.some(route => route.account_id === newRoute.account_id && route.requested_model === newRoute.requested_model && route.reasoning_effort === newRoute.reasoning_effort)) return
  config.accounts.push({ account_id: newRoute.account_id, requested_model: newRoute.requested_model, reasoning_effort: newRoute.reasoning_effort, candy_schedule: emptySchedule('candy'), fingerprint_schedule: emptySchedule('fingerprint') })
}

async function load() {
  loading.value = true
  try {
    const [meta, saved, accountPage] = await Promise.all([accountsAPI.getOpenAIEvalModels(), accountsAPI.getOpenAIEvalConfig(), accountsAPI.list(1, 500, { platform: 'openai', lite: '1' })])
    catalog.value = meta
    openaiAccounts.value = accountPage.items
    config.effects_enabled = saved.effects_enabled
    config.accounts = saved.accounts
    newRoute.reasoning_effort = meta.reasoning_efforts[0] || ''
    await loadHistory()
  } finally {
    loading.value = false
  }
}

async function save() {
  saving.value = true
  try {
    const saved = await accountsAPI.saveOpenAIEvalConfig({ effects_enabled: config.effects_enabled, accounts: config.accounts })
    config.effects_enabled = saved.effects_enabled
    config.accounts = saved.accounts
    appStore.showSuccess(t('admin.accounts.evaluations.saveSuccess'))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.accounts.evaluations.saveFailed')))
  } finally { saving.value = false }
}

async function runNow(route: OpenAIEvalRouteConfig, testType: 'candy' | 'fingerprint') {
  try {
    await accountsAPI.runOpenAIEval({ account_id: route.account_id, requested_model: route.requested_model, reasoning_effort: route.reasoning_effort, test_type: testType, sample_mode: route.fingerprint_schedule.sample_mode })
    appStore.showSuccess(t('admin.accounts.evaluations.runStarted'))
    await loadHistory()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.accounts.evaluations.runFailed')))
  }
}

async function loadHistory() {
  historyLoading.value = true
  try { runs.value = (await accountsAPI.listOpenAIEvalRuns({ limit: 100 })).items } finally { historyLoading.value = false }
}

const statusClass = (status: string) => status === 'pass' || status === 'consistent' ? 'status-ok' : status === 'warning' || status === 'uncertain' || status === 'insufficient' ? 'status-warn' : 'status-error'
const formatCost = (value?: number | null) => value == null ? t('admin.accounts.evaluations.costPending') : `$${value.toFixed(4)}`
const formatMetric = (value?: number | null) => value == null ? '-' : value.toFixed(4)
const formatTime = (value: string) => new Date(value).toLocaleString()
onMounted(() => { load().catch((error) => { loading.value = false; appStore.showError(extractApiErrorMessage(error, t('admin.accounts.evaluations.loadFailed'))) }) })
</script>

<style scoped>
.evaluation-page { @apply mx-auto max-w-[1440px] space-y-5 px-4 py-5 lg:px-6; }
.evaluation-header { @apply flex flex-col gap-4 border-b border-gray-200 pb-5 sm:flex-row sm:items-end sm:justify-between dark:border-dark-700; }
.evaluation-header h1 { @apply text-xl font-semibold tracking-tight text-gray-900 dark:text-white; }
.eyebrow { @apply mb-1 text-xs font-semibold uppercase tracking-[0.14em] text-primary-600 dark:text-primary-400; }
.description { @apply mt-1 max-w-2xl text-sm text-gray-500 dark:text-gray-400; }
.header-actions, .route-controls, .test-controls, .switch-row { @apply flex items-center gap-2; }
.switch-row { @apply text-xs text-gray-600 dark:text-gray-300; }
.switch-row input { @apply h-4 w-4 accent-primary-600; }
.notice { @apply flex items-start gap-2 border px-3 py-2 text-sm; }
.notice-blue { @apply border-blue-200 bg-blue-50 text-blue-800 dark:border-blue-900/60 dark:bg-blue-950/30 dark:text-blue-200; }
.route-toolbar, .section-heading { @apply flex flex-col gap-3 border-b border-gray-200 pb-3 sm:flex-row sm:items-end sm:justify-between dark:border-dark-700; }
.route-toolbar h2, .section-heading h2 { @apply text-base font-semibold text-gray-900 dark:text-white; }
.route-toolbar p, .section-heading p { @apply mt-1 text-xs text-gray-500 dark:text-gray-400; }
.route-controls select, .test-controls select { @apply min-h-9 border border-gray-300 bg-white px-2 text-sm text-gray-700 outline-none focus:border-primary-500 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-200; }
.jitter-input { @apply min-h-9 w-20 border border-gray-300 bg-white px-2 text-xs text-gray-700 outline-none focus:border-primary-500 dark:border-dark-600 dark:bg-dark-800 dark:text-gray-200; }
.route-list { @apply divide-y divide-gray-200 border-y border-gray-200 dark:divide-dark-700 dark:border-dark-700; }
.route-row { @apply grid gap-4 py-4 lg:grid-cols-[190px_minmax(0,1fr)_minmax(0,1fr)_32px] lg:items-center; }
.route-identity { @apply flex items-center gap-3; }
.route-id { @apply text-xs font-semibold text-gray-500 dark:text-gray-400; }
.route-model { @apply flex min-w-0 flex-col; }
.route-model strong { @apply truncate text-sm text-gray-900 dark:text-white; }
.route-model span, .test-heading small, .schedule-note { @apply text-xs text-gray-500 dark:text-gray-400; }
.test-column { @apply min-w-0 border-l border-gray-100 pl-3 dark:border-dark-700; }
.test-heading { @apply mb-2 flex items-center gap-2; }
.test-heading div { @apply flex min-w-0 flex-col; }
.test-heading strong { @apply text-sm text-gray-800 dark:text-gray-100; }
.test-mark { @apply flex h-6 w-6 items-center justify-center text-xs font-bold; }
.candy-mark { @apply bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-200; }
.fingerprint-mark { @apply bg-cyan-100 text-cyan-800 dark:bg-cyan-900/40 dark:text-cyan-200; }
.icon-button { @apply inline-flex h-8 w-8 items-center justify-center border border-gray-300 text-gray-600 hover:bg-gray-100 dark:border-dark-600 dark:text-gray-300 dark:hover:bg-dark-700; }
.remove-button { @apply justify-self-end text-gray-400 hover:text-red-600; }
.schedule-note { @apply mt-2 block; }
.history-section { @apply space-y-3 pt-2; }
.history-table-wrap { @apply overflow-x-auto border-y border-gray-200 dark:border-dark-700; }
.history-table { @apply min-w-full text-left text-xs; }
.history-table th { @apply whitespace-nowrap border-b border-gray-200 bg-gray-50 px-3 py-2 font-semibold text-gray-500 dark:border-dark-700 dark:bg-dark-800 dark:text-gray-400; }
.history-table td { @apply border-b border-gray-100 px-3 py-2 text-gray-700 last:border-0 dark:border-dark-800 dark:text-gray-300; }
.history-table td small { @apply mt-0.5 block text-gray-500; }
.status { @apply inline-flex px-1.5 py-0.5 font-medium; }
.status-ok { @apply bg-emerald-100 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300; }
.status-warn { @apply bg-amber-100 text-amber-800 dark:bg-amber-900/30 dark:text-amber-200; }
.status-error { @apply bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-300; }
.empty-state { @apply flex min-h-24 flex-col items-center justify-center gap-2 py-8 text-sm text-gray-500 dark:text-gray-400; }
.empty-state-dashed { @apply border border-dashed border-gray-300 dark:border-dark-600; }
.footer-note { @apply text-xs text-gray-500 dark:text-gray-400; }
@media (max-width: 640px) { .route-controls { @apply flex-wrap; } .route-controls select { @apply min-w-[145px] flex-1; } .header-actions { @apply justify-between; } }
</style>
