<template>
  <AppLayout>
    <ModelIntegrityShell
      :title="t('admin.modelIntegrity.scheduling.title')"
      :description="t('admin.modelIntegrity.scheduling.description')"
      :dirty="dirty"
      :saving="saving"
      :conflict="conflict"
      @save="handleSave"
      @reload="handleReload"
    >
      <div v-if="loading" class="sched-loading" role="status">{{ t('common.loading') }}</div>
      <div v-else-if="!loaded" class="sched-failed" role="alert">
        <p>{{ t('admin.modelIntegrity.common.loadFailed') }}</p>
        <button type="button" class="btn btn-secondary" @click="initialLoad">{{ t('admin.modelIntegrity.common.retry') }}</button>
      </div>

      <template v-else>
        <div class="sched-top">
          <section class="sched-section" aria-labelledby="policy-title">
            <div class="sched-section-head">
              <h2 id="policy-title" class="sched-h2">{{ t('admin.modelIntegrity.scheduling.policy.title') }}</h2>
              <p class="sched-hint">{{ t('admin.modelIntegrity.scheduling.policy.hint') }}</p>
            </div>
            <PolicyMixer
              v-model="defaultPolicy"
              name="default-policy"
              :label="t('admin.modelIntegrity.scheduling.policy.defaultLabel')"
            />
            <p class="sched-note">
              {{ t('admin.modelIntegrity.scheduling.policy.sharedNote') }}
              <template v-if="usesAvoidDegradation"> {{ t('admin.modelIntegrity.scheduling.policy.avoidNote') }}</template>
            </p>

            <div class="rules">
              <div class="rules-head">
                <div>
                  <h3 class="sched-h3">{{ t('admin.modelIntegrity.scheduling.rules.title') }}</h3>
                  <p class="sched-hint">{{ t('admin.modelIntegrity.scheduling.rules.hint') }}</p>
                </div>
                <button type="button" class="btn btn-secondary btn-sm" data-testid="add-rule" @click="addRule">
                  <Icon name="plus" size="sm" />{{ t('admin.modelIntegrity.scheduling.rules.add') }}
                </button>
              </div>
              <p v-if="!rules.length" class="rules-empty">{{ t('admin.modelIntegrity.scheduling.rules.empty') }}</p>
              <ul v-else class="rules-list">
                <li v-for="(rule, index) in rules" :key="index" class="rule-row" data-testid="policy-rule">
                  <label class="rule-field">
                    <span class="rule-label">{{ t('admin.modelIntegrity.scheduling.rules.model') }}</span>
                    <select v-model="rule.requested_model" class="input rule-input">
                      <option value="" disabled>{{ t('admin.modelIntegrity.scheduling.rules.pickModel') }}</option>
                      <option v-for="model in catalog?.items || []" :key="model.id" :value="model.id">{{ model.display_name || model.id }}</option>
                    </select>
                  </label>
                  <label class="rule-field">
                    <span class="rule-label">{{ t('admin.modelIntegrity.scheduling.rules.effort') }}</span>
                    <select v-model="rule.reasoning_effort" class="input rule-input">
                      <option v-for="effort in efforts" :key="effort" :value="effort">{{ effort || t('admin.modelIntegrity.common.allEfforts') }}</option>
                    </select>
                  </label>
                  <label class="rule-field">
                    <span class="rule-label">{{ t('admin.modelIntegrity.scheduling.rules.policy') }}</span>
                    <select v-model="rule.policy" class="input rule-input">
                      <option v-for="policy in RULE_POLICIES" :key="policy" :value="policy">{{ t(`admin.modelIntegrity.scheduling.policy.options.${policy}.name`) }}</option>
                    </select>
                  </label>
                  <button type="button" class="rule-remove" :title="t('admin.modelIntegrity.scheduling.rules.remove')" :aria-label="t('admin.modelIntegrity.scheduling.rules.remove')" @click="rules.splice(index, 1)">
                    <Icon name="trash" size="sm" />
                  </button>
                  <p v-if="duplicateRuleIndexes.has(index)" class="rule-error" role="alert">{{ t('admin.modelIntegrity.scheduling.rules.duplicate') }}</p>
                </li>
              </ul>
            </div>
          </section>

          <aside class="sched-gates" aria-labelledby="gates-title">
            <h2 id="gates-title" class="sched-h2">{{ t('admin.modelIntegrity.scheduling.gates.title') }}</h2>
            <p class="sched-hint">{{ t('admin.modelIntegrity.scheduling.gates.hint') }}</p>
            <ol class="gates">
              <li v-for="gate in GATES" :key="gate">{{ t(`admin.modelIntegrity.scheduling.gates.${gate}`) }}</li>
            </ol>
          </aside>
        </div>

        <section class="sched-section" aria-labelledby="bps-title">
          <div class="bps-head">
            <div class="sched-section-head">
              <h2 id="bps-title" class="sched-h2">{{ t('admin.modelIntegrity.scheduling.bps.title') }}</h2>
              <p class="sched-hint">{{ t('admin.modelIntegrity.scheduling.bps.hint') }}</p>
            </div>
            <label class="bps-master">
              <Toggle v-model="config.bps_auto_enabled" data-testid="bps-master" :aria-label="t('admin.modelIntegrity.scheduling.bps.master')" />
              <span>
                <span class="block font-medium text-gray-900 dark:text-white">{{ t('admin.modelIntegrity.scheduling.bps.master') }}</span>
                <span class="block text-xs text-gray-500 dark:text-gray-400">{{ t('admin.modelIntegrity.scheduling.bps.masterHint') }}</span>
              </span>
            </label>
          </div>
          <p class="sched-note">{{ t('admin.modelIntegrity.scheduling.bps.rule') }}</p>

          <div v-if="!bpsRoutes.length" class="bps-empty">
            <p>{{ t('admin.modelIntegrity.scheduling.bps.empty') }}</p>
            <router-link to="/admin/model-integrity/tests" class="btn btn-secondary btn-sm">{{ t('admin.modelIntegrity.scheduling.bps.goTests') }}</router-link>
          </div>
          <div v-else class="overflow-x-auto">
            <table class="bps-table">
              <thead>
                <tr>
                  <th scope="col">{{ t('admin.modelIntegrity.scheduling.bps.columns.target') }}</th>
                  <th scope="col">{{ t('admin.modelIntegrity.scheduling.bps.columns.mode') }}</th>
                  <th scope="col">{{ t('admin.modelIntegrity.scheduling.bps.columns.state') }}</th>
                  <th scope="col">{{ t('admin.modelIntegrity.scheduling.bps.columns.probe') }}</th>
                  <th scope="col"><span class="sr-only">{{ t('admin.modelIntegrity.scheduling.bps.reset') }}</span></th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="route in bpsRoutes" :key="routeKey(route)" data-testid="bps-row">
                  <td>
                    <span class="block font-medium text-gray-900 dark:text-white">{{ accountName(route.account_id) }}</span>
                    <span class="block text-xs text-gray-500 dark:text-gray-400">{{ route.requested_model }}</span>
                  </td>
                  <td>
                    <select
                      class="input bps-mode"
                      :value="bpsModeOf(route)"
                      :aria-label="`${accountName(route.account_id)} ${route.requested_model}`"
                      @change="setMode(route, ($event.target as HTMLSelectElement).value as OpenAIEvalBPSMode)"
                    >
                      <option value="auto">{{ t('admin.modelIntegrity.scheduling.bps.modes.auto') }}</option>
                      <option value="force_off">{{ t('admin.modelIntegrity.scheduling.bps.modes.force_off') }}</option>
                      <option value="force_on">{{ t('admin.modelIntegrity.scheduling.bps.modes.force_on') }}</option>
                    </select>
                    <p v-if="bpsModeOf(route) === 'auto' && !config.bps_auto_enabled" class="bps-warn">{{ t('admin.modelIntegrity.scheduling.bps.warnings.masterOff') }}</p>
                    <p v-else-if="bpsModeOf(route) !== 'force_off' && !route.state_probe_schedule.enabled" class="bps-warn">{{ t('admin.modelIntegrity.scheduling.bps.warnings.noProbe') }}</p>
                  </td>
                  <td>
                    <span class="lane" :class="`lane-${laneOf(route)}`">{{ t(`admin.modelIntegrity.scheduling.bps.state.${laneOf(route)}`) }}</span>
                    <p v-if="route.bps_state?.disabled_reason" class="bps-reason">{{ disabledText(route.bps_state.disabled_reason) }}</p>
                  </td>
                  <td class="text-xs text-gray-600 dark:text-gray-300">
                    <template v-if="route.bps_state">{{ t('admin.modelIntegrity.scheduling.bps.streak', { degraded: route.bps_state.degraded_streak, healthy: route.bps_state.healthy_streak }) }}</template>
                    <template v-else>—</template>
                  </td>
                  <td class="text-right">
                    <button
                      v-if="canReset(route)"
                      type="button"
                      class="btn btn-secondary btn-sm"
                      :disabled="resetting === routeKey(route)"
                      @click="pendingReset = route"
                    >{{ t('admin.modelIntegrity.scheduling.bps.reset') }}</button>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <p v-if="ineligibleCount" class="sched-note">{{ t('admin.modelIntegrity.scheduling.bps.ineligible', { count: ineligibleCount }) }}</p>
        </section>

        <section class="sched-section" aria-labelledby="decisions-title">
          <div class="decisions-head">
            <div class="sched-section-head">
              <h2 id="decisions-title" class="sched-h2">{{ t('admin.modelIntegrity.scheduling.decisions.title') }}</h2>
              <p class="sched-hint">{{ t('admin.modelIntegrity.scheduling.decisions.hint', { limit: TRACE_LIMIT }) }}</p>
            </div>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="tracesLoading" @click="loadTraces">
              <Icon name="refresh" size="sm" :class="tracesLoading ? 'motion-safe:animate-spin' : ''" />{{ t('admin.modelIntegrity.common.refresh') }}
            </button>
          </div>
          <DecisionLedger :traces="traces" :account-name="accountName" />
        </section>
      </template>
    </ModelIntegrityShell>

    <ConfirmDialog
      :show="pendingReset !== null"
      :title="t('admin.modelIntegrity.scheduling.bps.resetTitle')"
      :message="pendingReset ? t('admin.modelIntegrity.scheduling.bps.resetBody', { target: `${accountName(pendingReset.account_id)} · ${pendingReset.requested_model}` }) : ''"
      :confirm-text="t('admin.modelIntegrity.scheduling.bps.reset')"
      @confirm="confirmReset"
      @cancel="pendingReset = null"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { onBeforeRouteLeave } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import Toggle from '@/components/common/Toggle.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import ModelIntegrityShell from '@/components/admin/modelIntegrity/ModelIntegrityShell.vue'
import PolicyMixer from '@/components/admin/modelIntegrity/PolicyMixer.vue'
import DecisionLedger from '@/components/admin/modelIntegrity/DecisionLedger.vue'
import { accountsAPI, listSchedulerDecisions, type OpenAIEvalBPSMode, type OpenAIEvalRouteConfig, type OpenAIEvalSchedulingPolicy, type OpenAIEvalSchedulingPolicyRule, type SchedulerDecisionTrace } from '@/api/admin/accounts'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { bpsDisabledKey, bpsModeOf, isDirectOAuthRoute, routeKey } from './modelIntegrity'
import { useModelIntegrityConfig } from './useModelIntegrityConfig'

const TRACE_LIMIT = 50
const RULE_POLICIES: OpenAIEvalSchedulingPolicyRule['policy'][] = ['cost_first', 'stability_first', 'avoid_degradation']
const GATES = ['session', 'model', 'status', 'features', 'privacy', 'capacity'] as const

const { t } = useI18n()
const appStore = useAppStore()
const { config, catalog, loading, loaded, saving, conflict, dirty, load, reloadConfig, save, accountName } = useModelIntegrityConfig()

const traces = ref<SchedulerDecisionTrace[]>([])
const tracesLoading = ref(false)
const pendingReset = ref<OpenAIEvalRouteConfig | null>(null)
const resetting = ref('')

const defaultPolicy = computed<OpenAIEvalSchedulingPolicy>({
  get: () => config.scheduling_policy ?? '',
  set: value => { config.scheduling_policy = value }
})
// useModelIntegrityConfig always initialises policies to an array.
const rules = computed(() => config.policies as OpenAIEvalSchedulingPolicyRule[])
const efforts = computed(() => catalog.value?.reasoning_efforts?.length ? catalog.value.reasoning_efforts : [''])
const usesAvoidDegradation = computed(() => defaultPolicy.value === 'avoid_degradation' || rules.value.some(rule => rule.policy === 'avoid_degradation'))

const duplicateRuleIndexes = computed(() => {
  const seen = new Map<string, number>()
  const duplicates = new Set<number>()
  rules.value.forEach((rule, index) => {
    if (!rule.requested_model) return
    const key = `${rule.requested_model.toLowerCase()}\u0000${(rule.reasoning_effort || '').toLowerCase()}`
    if (seen.has(key)) duplicates.add(index)
    else seen.set(key, index)
  })
  return duplicates
})

const bpsRoutes = computed(() => config.accounts.filter(isDirectOAuthRoute))
const ineligibleCount = computed(() => config.accounts.length - bpsRoutes.value.length)

function addRule() {
  rules.value.push({ requested_model: '', reasoning_effort: '', policy: 'stability_first' })
}

function setMode(route: OpenAIEvalRouteConfig, mode: OpenAIEvalBPSMode) {
  route.bps_mode = mode
  route.bps_auto = mode !== 'force_off'
}

type Lane = 'bps' | 'native' | 'locked' | 'inactive'
function laneOf(route: OpenAIEvalRouteConfig): Lane {
  const state = route.bps_state
  if (state?.disabled_reason) return 'locked'
  if (bpsModeOf(route) === 'force_off') return 'inactive'
  if (bpsModeOf(route) === 'force_on') return 'bps'
  if (!config.bps_auto_enabled) return 'inactive'
  return state?.active ? 'bps' : 'native'
}

function disabledText(reason: string) {
  const key = bpsDisabledKey(reason)
  return t(`admin.modelIntegrity.scheduling.bps.disabled.${key}`, { reason })
}

function canReset(route: OpenAIEvalRouteConfig) {
  const state = route.bps_state
  return Boolean(state && (state.active || state.disabled_reason || state.degraded_streak > 0 || state.healthy_streak > 0))
}

async function confirmReset() {
  const route = pendingReset.value
  pendingReset.value = null
  if (!route) return
  resetting.value = routeKey(route)
  try {
    const { state } = await accountsAPI.resetOpenAIBPSState({ account_id: route.account_id, requested_model: route.requested_model })
    route.bps_state = state
    appStore.showSuccess(t('admin.modelIntegrity.scheduling.bps.resetDone'))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.modelIntegrity.scheduling.bps.resetFailed')))
  } finally {
    resetting.value = ''
  }
}

async function loadTraces() {
  tracesLoading.value = true
  try {
    traces.value = (await listSchedulerDecisions(TRACE_LIMIT)).items ?? []
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.modelIntegrity.common.loadFailed')))
  } finally {
    tracesLoading.value = false
  }
}

async function handleSave() {
  if (duplicateRuleIndexes.value.size || rules.value.some(rule => !rule.requested_model)) {
    appStore.showError(rules.value.some(rule => !rule.requested_model) ? t('admin.modelIntegrity.scheduling.rules.pickModel') : t('admin.modelIntegrity.scheduling.rules.duplicate'))
    return
  }
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
  await loadTraces()
}

onBeforeRouteLeave(() => {
  if (dirty.value && !window.confirm(t('admin.modelIntegrity.common.leaveConfirm'))) return false
  return true
})

onMounted(initialLoad)
</script>

<style scoped>
.sched-loading, .sched-failed { @apply flex min-h-[12rem] flex-col items-center justify-center gap-3 rounded-lg border border-dashed border-gray-300 text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400; }
.sched-top { @apply grid gap-5 lg:grid-cols-[minmax(0,1fr)_18rem]; }
.sched-section { @apply space-y-4 rounded-xl border border-gray-200 bg-white p-5 dark:border-dark-700 dark:bg-dark-800/60 sm:p-6; }
.sched-section-head { @apply space-y-1; }
.sched-h2 { @apply text-base font-semibold text-gray-900 dark:text-white; }
.sched-h3 { @apply text-sm font-semibold text-gray-900 dark:text-white; }
.sched-hint { @apply max-w-[68ch] text-sm leading-relaxed text-gray-600 dark:text-gray-400; }
.sched-note { @apply max-w-[80ch] text-xs leading-relaxed text-gray-500 dark:text-gray-400; }
.sched-gates { @apply self-start rounded-xl bg-gray-50 p-5 dark:bg-dark-900/60 sm:p-6; }
.gates { @apply mt-4 space-y-3 text-sm leading-relaxed text-gray-700 dark:text-gray-300; counter-reset: gate; }
.gates li { @apply relative pl-8; counter-increment: gate; }
.gates li::before { content: counter(gate); @apply absolute left-0 top-0.5 flex h-5 w-5 items-center justify-center rounded-full border border-gray-300 text-[11px] font-semibold tabular-nums text-gray-600 dark:border-dark-500 dark:text-gray-300; }
.rules { @apply space-y-3 border-t border-gray-100 pt-5 dark:border-dark-700; }
.rules-head { @apply flex flex-wrap items-start justify-between gap-3; }
.rules-empty { @apply text-sm text-gray-500 dark:text-gray-400; }
.rules-list { @apply space-y-2; }
.rule-row { @apply grid items-end gap-2 rounded-lg bg-gray-50 p-3 dark:bg-dark-900/50 sm:grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)_minmax(0,1fr)_2.25rem]; }
.rule-field { @apply flex min-w-0 flex-col gap-1; }
.rule-label { @apply text-xs text-gray-500 dark:text-gray-400; }
.rule-input { @apply h-9 py-1 text-sm; }
.rule-remove { @apply inline-flex h-9 w-9 items-center justify-center rounded-md text-gray-400 hover:bg-rose-50 hover:text-rose-600 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:hover:bg-rose-950/40; }
.rule-error { @apply text-xs text-rose-700 dark:text-rose-300 sm:col-span-4; }
.bps-head { @apply flex flex-wrap items-start justify-between gap-4; }
.bps-master { @apply flex max-w-sm cursor-pointer items-start gap-3 text-sm; }
.bps-empty { @apply flex flex-wrap items-center justify-between gap-3 rounded-lg border border-dashed border-gray-300 px-4 py-4 text-sm text-gray-600 dark:border-dark-600 dark:text-gray-400; }
.bps-empty p { @apply max-w-[68ch]; }
.bps-table { @apply w-full min-w-[44rem] text-left text-sm; }
.bps-table th { @apply border-b border-gray-200 px-3 py-2 text-xs font-medium text-gray-500 dark:border-dark-700 dark:text-gray-400; }
.bps-table td { @apply border-b border-gray-100 px-3 py-3 align-top dark:border-dark-700; }
.bps-table tr:last-child td { @apply border-b-0; }
.bps-mode { @apply h-9 w-auto min-w-[10rem] py-1 text-sm; }
.bps-warn { @apply mt-1 max-w-[16rem] text-xs text-amber-700 dark:text-amber-300; }
.bps-reason { @apply mt-1 max-w-[20rem] text-xs text-rose-700 dark:text-rose-300; }
.lane { @apply inline-flex items-center gap-1.5 whitespace-nowrap text-sm font-medium; }
.lane::before { content: ''; @apply h-2 w-2 rounded-full; }
.lane-bps { @apply text-sky-700 dark:text-sky-300; }
.lane-bps::before { @apply bg-sky-500; }
.lane-native { @apply text-gray-800 dark:text-gray-200; }
.lane-native::before { @apply bg-primary-500; }
.lane-locked { @apply text-rose-700 dark:text-rose-300; }
.lane-locked::before { @apply bg-rose-500; }
.lane-inactive { @apply text-gray-500 dark:text-gray-400; }
.lane-inactive::before { @apply bg-gray-300 dark:bg-dark-500; }
.decisions-head { @apply flex flex-wrap items-start justify-between gap-3; }
</style>
