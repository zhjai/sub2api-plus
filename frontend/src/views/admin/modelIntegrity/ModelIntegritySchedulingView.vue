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
            <div v-if="defaultPolicy === 'custom_balance'" class="custom-balance" data-testid="custom-balance">
              <div class="custom-balance-head">
                <h3 class="sched-h3">{{ t('admin.modelIntegrity.scheduling.policy.custom.title') }}</h3>
                <span class="sched-hint">{{ t('admin.modelIntegrity.scheduling.policy.custom.hint') }}</span>
              </div>
              <PolicyWeightsEditor v-model="config.custom_balance" :legend="t('admin.modelIntegrity.scheduling.policy.custom.title')" />
            </div>
            <p v-if="foldedLegacyStability" class="sched-note" data-testid="legacy-stability-folded">{{ t('admin.modelIntegrity.scheduling.policy.custom.legacyFolded') }}</p>
            <p class="sched-note">
              {{ t('admin.modelIntegrity.scheduling.policy.sharedNote') }}
              <template v-if="usesAvoidDegradation"> {{ t('admin.modelIntegrity.scheduling.policy.avoidNote') }}</template>
            </p>
            <p v-if="usesQuality && !config.effects_enabled" class="sched-warn" role="note" data-testid="quality-effects-off">{{ t('admin.modelIntegrity.scheduling.quality.effectsOff') }}</p>

            <div class="refresh" data-testid="quality-refresh">
              <div class="refresh-head">
                <div class="min-w-0">
                  <h3 class="sched-h3">{{ t('admin.modelIntegrity.scheduling.quality.title') }}</h3>
                  <p class="sched-hint">{{ t('admin.modelIntegrity.scheduling.quality.hint') }}</p>
                </div>
                <button
                  type="button"
                  class="btn btn-secondary btn-sm shrink-0"
                  :disabled="refreshing"
                  :aria-busy="refreshing ? 'true' : undefined"
                  data-testid="quality-refresh-now"
                  @click="refreshQuality"
                >
                  <Icon name="refresh" size="sm" :class="refreshing ? 'motion-safe:animate-spin' : ''" />
                  {{ refreshing ? t('admin.modelIntegrity.scheduling.quality.refreshing') : t('admin.modelIntegrity.scheduling.quality.refreshNow') }}
                </button>
              </div>
              <div class="refresh-controls">
                <label class="rule-field">
                  <span class="rule-label">{{ t('admin.modelIntegrity.scheduling.quality.interval') }}</span>
                  <select v-model="refreshChoice" class="input rule-input refresh-select" data-testid="quality-interval">
                    <option v-for="seconds in QUALITY_REFRESH_INTERVALS" :key="seconds" :value="seconds">{{ intervalText(seconds) }}</option>
                    <option :value="CUSTOM_INTERVAL">{{ t('admin.modelIntegrity.tests.interval.customOption') }}</option>
                  </select>
                </label>
                <label v-if="refreshCustom" class="rule-field">
                  <span class="rule-label">{{ t('admin.modelIntegrity.tests.interval.customMinutes', { max: MAX_INTERVAL_MINUTES.toLocaleString() }) }}</span>
                  <input
                    v-model.number="refreshMinutes"
                    type="number"
                    min="5"
                    :max="MAX_INTERVAL_MINUTES"
                    step="1"
                    inputmode="numeric"
                    class="input rule-input refresh-minutes tabular-nums"
                    data-testid="quality-interval-minutes"
                    @change="commitRefreshMinutes"
                  />
                </label>
              </div>
              <p class="refresh-status" data-testid="quality-refresh-status" aria-live="polite">
                <span v-if="config.quality_refreshed_at">{{ t('admin.modelIntegrity.scheduling.quality.lastRefresh', { time: formatDateTime(config.quality_refreshed_at) }) }}</span>
                <span v-else>{{ t('admin.modelIntegrity.scheduling.quality.neverRefreshed') }}</span>
                <span v-if="config.quality_next_refresh_at">{{ t('admin.modelIntegrity.scheduling.quality.nextRefresh', { time: formatDateTime(config.quality_next_refresh_at) }) }}</span>
                <span v-if="lastRefreshRoutes !== null" data-testid="quality-refresh-routes">{{ t('admin.modelIntegrity.scheduling.quality.routes', { count: lastRefreshRoutes }) }}</span>
              </p>
              <p v-if="intervalPending" class="refresh-pending" data-testid="quality-interval-pending">{{ t('admin.modelIntegrity.scheduling.quality.pending', { interval: intervalText(savedQualityRefreshInterval) }) }}</p>
              <p v-if="refreshError" class="refresh-error" role="alert" data-testid="quality-refresh-error">{{ refreshError }}</p>
              <p class="sched-note">{{ t('admin.modelIntegrity.scheduling.quality.liveChecks') }}</p>
            </div>

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
                    <select :value="rule.policy" class="input rule-input" data-testid="rule-policy" @change="setRulePolicy(rule, ($event.target as HTMLSelectElement).value as OpenAIEvalSchedulingPolicyRule['policy'])">
                      <option v-for="policy in RULE_POLICIES" :key="policy" :value="policy">{{ t(`admin.modelIntegrity.scheduling.policy.options.${policy}.name`) }}</option>
                    </select>
                  </label>
                  <button type="button" class="rule-remove" :title="t('admin.modelIntegrity.scheduling.rules.remove')" :aria-label="t('admin.modelIntegrity.scheduling.rules.remove')" @click="rules.splice(index, 1)">
                    <Icon name="trash" size="sm" />
                  </button>
                  <div v-if="rule.policy === 'custom_balance'" class="rule-weights" data-testid="rule-weights">
                    <div class="rule-weights-head">
                      <p class="rule-weights-summary">
                        <span class="rule-weights-title">{{ t('admin.modelIntegrity.scheduling.rules.weights') }}</span>
                        <span :class="{ 'rule-weights-invalid': !isValidCustomBalance(rule.custom_balance) }" data-testid="rule-weights-summary">{{ weightSummary(rule) }}</span>
                      </p>
                      <button
                        type="button"
                        class="btn btn-secondary btn-sm"
                        :aria-expanded="isEditingWeights(rule)"
                        :aria-controls="`rule-weights-editor-${index}`"
                        data-testid="rule-weights-toggle"
                        @click="toggleWeights(rule)"
                      >
                        {{ isEditingWeights(rule) ? t('admin.modelIntegrity.scheduling.rules.doneWeights') : t('admin.modelIntegrity.scheduling.rules.editWeights') }}
                      </button>
                    </div>
                    <div v-if="isEditingWeights(rule)" :id="`rule-weights-editor-${index}`" class="space-y-2">
                      <PolicyWeightsEditor
                        v-model="rule.custom_balance"
                        :legend="t('admin.modelIntegrity.scheduling.rules.weightsFor', { model: rule.requested_model || t('admin.modelIntegrity.scheduling.rules.pickModel'), effort: rule.reasoning_effort || t('admin.modelIntegrity.common.allEfforts') })"
                      />
                      <p class="sched-note">{{ t('admin.modelIntegrity.scheduling.rules.weightsHint') }}</p>
                    </div>
                  </div>
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

        <section class="sched-section sched-section-overflow" aria-labelledby="bps-title">
          <div class="bps-head">
            <div class="sched-section-head">
              <h2 id="bps-title" class="sched-h2">{{ t('admin.modelIntegrity.scheduling.bps.title') }}</h2>
              <p class="sched-hint">{{ t('admin.modelIntegrity.scheduling.bps.hint') }}</p>
            </div>
            <div class="flex flex-wrap items-start gap-4">
              <label class="bps-master">
                <Toggle v-model="config.bps_auto_enabled" data-testid="bps-master" :aria-label="t('admin.modelIntegrity.scheduling.bps.master')" />
                <span>
                  <span class="block font-medium text-gray-900 dark:text-white">{{ t('admin.modelIntegrity.scheduling.bps.master') }}</span>
                  <span class="block text-xs text-gray-500 dark:text-gray-400">{{ t('admin.modelIntegrity.scheduling.bps.masterHint') }}</span>
                </span>
              </label>
              <button type="button" class="btn btn-secondary btn-sm" data-testid="bps-add" :disabled="!bpsCandidates.length" @click="openBPS(null)">
                <Icon name="plus" size="sm" />{{ t('admin.modelIntegrity.scheduling.bps.add') }}
              </button>
            </div>
          </div>

          <p v-if="bpsAccounts.length" class="bps-summary" data-testid="bps-summary">
            <span>{{ t('admin.modelIntegrity.scheduling.bps.summary.total', { count: bpsAccounts.length }) }}</span>
            <span class="lane lane-bps">{{ t('admin.modelIntegrity.scheduling.bps.summary.bps', { count: laneCount.bps }) }}</span>
            <span class="lane lane-native">{{ t('admin.modelIntegrity.scheduling.bps.summary.native', { count: laneCount.native }) }}</span>
            <span v-if="laneCount.locked" class="lane lane-locked">{{ t('admin.modelIntegrity.scheduling.bps.summary.locked', { count: laneCount.locked }) }}</span>
            <span v-if="laneCount.inactive" class="lane lane-inactive">{{ t('admin.modelIntegrity.scheduling.bps.summary.inactive', { count: laneCount.inactive }) }}</span>
          </p>

          <div v-if="!bpsAccounts.length" class="bps-empty">
            <p>{{ bpsCandidates.length ? t('admin.modelIntegrity.scheduling.bps.empty') : t('admin.modelIntegrity.scheduling.bps.emptyNoOAuth') }}</p>
          </div>
          <div v-else class="max-w-full overflow-x-auto">
            <table class="bps-table">
              <thead>
                <tr>
                  <th scope="col">{{ t('admin.modelIntegrity.scheduling.bps.columns.account') }}</th>
                  <th scope="col">{{ t('admin.modelIntegrity.scheduling.bps.columns.mode') }}</th>
                  <th scope="col">{{ t('admin.modelIntegrity.scheduling.bps.columns.state') }}</th>
                  <th scope="col">{{ t('admin.modelIntegrity.scheduling.bps.columns.counters') }}</th>
                  <th scope="col">{{ t('admin.modelIntegrity.scheduling.bps.columns.probe') }}</th>
                  <th scope="col"><span class="sr-only">{{ t('admin.modelIntegrity.scheduling.bps.columns.actions') }}</span></th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="item in bpsAccounts" :key="item.account_id" data-testid="bps-row" :class="{ 'bps-row-locked': laneOf(item) === 'locked' }">
                  <td class="font-medium text-gray-900 dark:text-white">{{ accountLabel(item.account_id) }}</td>
                  <td>{{ t(`admin.modelIntegrity.scheduling.bps.modes.${item.mode}`) }}</td>
                  <td>
                    <span class="lane" :class="`lane-${laneOf(item)}`">{{ t(`admin.modelIntegrity.scheduling.bps.state.${laneOf(item)}`) }}</span>
                    <p v-if="item.disabled_reason" class="bps-reason">{{ disabledText(item.disabled_reason) }}</p>
                    <p v-else-if="item.mode === 'auto' && !config.bps_auto_enabled" class="bps-warn">{{ t('admin.modelIntegrity.scheduling.bps.warnings.masterOffShort') }}</p>
                  </td>
                  <td class="tabular-nums text-xs text-gray-600 dark:text-gray-300">
                    <template v-if="item.mode === 'auto'">{{ t('admin.modelIntegrity.scheduling.bps.counters', { degraded: item.degraded_streak ?? 0, failure: item.failure_threshold, healthy: item.healthy_streak ?? 0, recovery: item.recovery_threshold }) }}</template>
                    <template v-else>—</template>
                  </td>
                  <td class="text-xs text-gray-600 dark:text-gray-300">
                    <span class="block">{{ item.probe_model || '—' }}</span>
                    <span class="block text-gray-500 dark:text-gray-400">{{ intervalText(item.interval_seconds) }}</span>
                    <span v-if="item.last_run_at" class="block text-gray-500 dark:text-gray-400">{{ t('admin.modelIntegrity.scheduling.bps.lastProbe', { time: formatDateTime(item.last_run_at) }) }}</span>
                    <span v-if="item.next_run_at" class="block text-gray-500 dark:text-gray-400">{{ t('admin.modelIntegrity.scheduling.bps.nextProbe', { time: formatDateTime(item.next_run_at) }) }}</span>
                  </td>
                  <td class="whitespace-nowrap text-right">
                    <button
                      v-if="canResetBPSAccount(item)"
                      type="button"
                      class="btn btn-sm mr-2"
                      :class="laneOf(item) === 'locked' ? 'btn-primary' : 'btn-secondary'"
                      :disabled="resetting === item.account_id"
                      data-testid="bps-reset"
                      @click="pendingReset = item"
                    >{{ t('admin.modelIntegrity.scheduling.bps.reset') }}</button>
                    <button type="button" class="btn btn-secondary btn-sm" data-testid="bps-edit" @click="openBPS(item)">{{ t('admin.modelIntegrity.scheduling.bps.edit') }}</button>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <p v-if="legacyRouteCount" class="sched-note">{{ t('admin.modelIntegrity.scheduling.bps.legacyRoutes', { count: legacyRouteCount }) }}</p>
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

    <BpsAccountDialog
      :show="bpsDialogOpen"
      :item="editingBPS"
      :account-label="editingBPS ? accountLabel(editingBPS.account_id) : ''"
      :candidates="bpsCandidates"
      :catalog="catalog"
      :auto-enabled="config.bps_auto_enabled"
      :resetting="editingBPS !== null && resetting === editingBPS.account_id"
      @close="closeBPS"
      @apply="applyBPS"
      @remove="pendingRemove = editingBPS"
      @reset="pendingReset = editingBPS"
    />

    <ConfirmDialog
      :show="pendingRemove !== null"
      :title="t('admin.modelIntegrity.scheduling.bps.removeTitle')"
      :message="pendingRemove ? t('admin.modelIntegrity.scheduling.bps.removeBody', { account: accountLabel(pendingRemove.account_id) }) : ''"
      :confirm-text="t('admin.modelIntegrity.common.remove')"
      :danger="true"
      @confirm="confirmRemove"
      @cancel="pendingRemove = null"
    />

    <ConfirmDialog
      :show="pendingReset !== null"
      :title="t('admin.modelIntegrity.scheduling.bps.resetTitle')"
      :message="pendingReset ? t('admin.modelIntegrity.scheduling.bps.resetBody', { account: accountLabel(pendingReset.account_id) }) : ''"
      :confirm-text="t('admin.modelIntegrity.scheduling.bps.reset')"
      @confirm="confirmReset"
      @cancel="pendingReset = null"
    />
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { onBeforeRouteLeave } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import Toggle from '@/components/common/Toggle.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import ModelIntegrityShell from '@/components/admin/modelIntegrity/ModelIntegrityShell.vue'
import PolicyMixer from '@/components/admin/modelIntegrity/PolicyMixer.vue'
import DecisionLedger from '@/components/admin/modelIntegrity/DecisionLedger.vue'
import BpsAccountDialog from '@/components/admin/modelIntegrity/BpsAccountDialog.vue'
import PolicyWeightsEditor from '@/components/admin/modelIntegrity/PolicyWeightsEditor.vue'
import { accountsAPI, listSchedulerDecisions, type OpenAIEvalBPSAccountConfig, type OpenAIEvalSchedulingPolicy, type OpenAIEvalSchedulingPolicyRule, type SchedulerDecisionTrace } from '@/api/admin/accounts'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { CUSTOM_FACTORS, CUSTOM_INTERVAL, DAY, HOUR, MAX_INTERVAL_MINUTES, QUALITY_REFRESH_INTERVALS, bpsAccountLane, bpsDisabledKey, bpsModeOf, canResetBPSAccount, customBalanceShares, isDirectOAuthRoute, isValidCustomBalance, normalizeBPSAccount, normalizeCustomBalance, normalizeQualityRefreshInterval, type BPSLane } from './modelIntegrity'
import { useModelIntegrityConfig } from './useModelIntegrityConfig'

const TRACE_LIMIT = 50
const RULE_POLICIES: OpenAIEvalSchedulingPolicyRule['policy'][] = ['cost_first', 'stability_first', 'avoid_degradation', 'custom_balance']
const GATES = ['session', 'model', 'status', 'features', 'privacy', 'capacity'] as const

const { t } = useI18n()
const appStore = useAppStore()
const { config, catalog, accounts, loading, loaded, saving, conflict, dirty, load, reloadConfig, save, accountName, accountLabel, savedQualityRefreshInterval, applyQualityRefresh, foldedLegacyStability } = useModelIntegrityConfig()

const traces = ref<SchedulerDecisionTrace[]>([])
const tracesLoading = ref(false)
const pendingReset = ref<OpenAIEvalBPSAccountConfig | null>(null)
const pendingRemove = ref<OpenAIEvalBPSAccountConfig | null>(null)
const resetting = ref(0)
const bpsDialogOpen = ref(false)
const editingBPS = ref<OpenAIEvalBPSAccountConfig | null>(null)

const defaultPolicy = computed<OpenAIEvalSchedulingPolicy>({
  get: () => config.scheduling_policy ?? '',
  set: value => { config.scheduling_policy = value }
})
// useModelIntegrityConfig always initialises policies to an array.
const rules = computed(() => config.policies as OpenAIEvalSchedulingPolicyRule[])
const efforts = computed(() => catalog.value?.reasoning_efforts?.length ? catalog.value.reasoning_efforts : [''])
const usesAvoidDegradation = computed(() => defaultPolicy.value === 'avoid_degradation' || rules.value.some(rule => rule.policy === 'avoid_degradation'))
/** Any saved policy that reads the integrity pass rate. */
const usesQuality = computed(() => usesAvoidDegradation.value ||
  (defaultPolicy.value === 'custom_balance' && Number(config.custom_balance?.quality) > 0) ||
  rules.value.some(rule => rule.policy === 'custom_balance' && Number(rule.custom_balance?.quality) > 0))

// 调度评估间隔: how often the pass-rate snapshot is rebuilt, not how often tests run.
const refreshing = ref(false)
const refreshError = ref('')
const lastRefreshRoutes = ref<number | null>(null)
const refreshCustom = ref(false)
const qualityInterval = computed(() => normalizeQualityRefreshInterval(config.quality_refresh_interval_seconds))
const refreshChoice = computed<number | string>({
  get: () => (refreshCustom.value || !QUALITY_REFRESH_INTERVALS.includes(qualityInterval.value) ? CUSTOM_INTERVAL : qualityInterval.value),
  set: value => {
    if (value === CUSTOM_INTERVAL) {
      refreshCustom.value = true
      return
    }
    refreshCustom.value = false
    config.quality_refresh_interval_seconds = normalizeQualityRefreshInterval(Number(value))
  }
})
const refreshMinutes = ref<number | string>(qualityInterval.value / 60)
watch(qualityInterval, value => {
  refreshMinutes.value = value / 60
  if (!QUALITY_REFRESH_INTERVALS.includes(value)) refreshCustom.value = true
}, { immediate: true })
function commitRefreshMinutes() {
  const seconds = normalizeQualityRefreshInterval(Number(refreshMinutes.value) * 60)
  config.quality_refresh_interval_seconds = seconds
  refreshMinutes.value = seconds / 60
}
/** The edited interval is not live until the config is saved. */
const intervalPending = computed(() => qualityInterval.value !== savedQualityRefreshInterval.value)

/**
 * Rebuilds the ranking snapshot on the server from stored automatic results
 * with the saved config. Unsaved edits on this page are left as they are.
 */
async function refreshQuality() {
  if (refreshing.value) return
  refreshing.value = true
  refreshError.value = ''
  try {
    const result = await accountsAPI.refreshOpenAIEvalQuality()
    applyQualityRefresh(result)
    lastRefreshRoutes.value = Number(result.route_count) || 0
    appStore.showSuccess(t('admin.modelIntegrity.scheduling.quality.refreshDone', { count: lastRefreshRoutes.value }))
    void loadTraces()
  } catch (error) {
    refreshError.value = extractApiErrorMessage(error, t('admin.modelIntegrity.scheduling.quality.refreshFailed'))
    appStore.showError(refreshError.value)
  } finally {
    refreshing.value = false
  }
}
// Rules whose weight editor is open. Holds the reactive rule objects, so it
// follows a rule when another one above it is removed.
const editingWeights = ref<OpenAIEvalSchedulingPolicyRule[]>([])

function isEditingWeights(rule: OpenAIEvalSchedulingPolicyRule) {
  return editingWeights.value.includes(rule)
}

function toggleWeights(rule: OpenAIEvalSchedulingPolicyRule) {
  const index = editingWeights.value.indexOf(rule)
  if (index >= 0) editingWeights.value.splice(index, 1)
  else editingWeights.value.push(rule)
}

/**
 * A rule switched to custom balance starts from a copy of the current default
 * weights and is edited on its own from then on; it never tracks the default.
 */
function setRulePolicy(rule: OpenAIEvalSchedulingPolicyRule, policy: OpenAIEvalSchedulingPolicyRule['policy']) {
  rule.policy = policy
  if (policy !== 'custom_balance') return
  rule.custom_balance = normalizeCustomBalance(rule.custom_balance ?? config.custom_balance)
  if (!isEditingWeights(rule)) editingWeights.value.push(rule)
}

function weightSummary(rule: OpenAIEvalSchedulingPolicyRule) {
  if (!isValidCustomBalance(rule.custom_balance)) return t('admin.modelIntegrity.scheduling.policy.custom.zeroTotal')
  const shares = customBalanceShares(rule.custom_balance)
  return CUSTOM_FACTORS.map(factor => `${t(`admin.modelIntegrity.scheduling.policy.custom.${factor}`)} ${shares[factor]}%`).join(t('admin.modelIntegrity.scheduling.rules.weightsSeparator'))
}

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

// useModelIntegrityConfig always initialises bps_accounts to an array.
const bpsAccounts = computed(() => config.bps_accounts as OpenAIEvalBPSAccountConfig[])
/** Accounts that can be added: OpenAI OAuth accounts not listed yet. The server rejects shadow/agent identities. */
const bpsCandidates = computed(() => {
  const listed = new Set(bpsAccounts.value.map(item => item.account_id))
  return accounts.value.filter(account => account.platform === 'openai' && account.type === 'oauth' && !listed.has(account.id))
})
const laneCount = computed(() => {
  const count: Record<BPSLane, number> = { bps: 0, native: 0, locked: 0, inactive: 0 }
  for (const item of bpsAccounts.value) count[laneOf(item)]++
  return count
})
/** Route-level BPS settings saved before BPS became account-scoped. This page leaves them untouched. */
const legacyRouteCount = computed(() => config.accounts.filter(route => isDirectOAuthRoute(route) && bpsModeOf(route) !== 'force_off').length)

function addRule() {
  rules.value.push({ requested_model: '', reasoning_effort: '', policy: 'stability_first' })
}

function laneOf(item: OpenAIEvalBPSAccountConfig): BPSLane {
  return bpsAccountLane(item, config.bps_auto_enabled)
}

function intervalText(seconds: number) {
  if (seconds === DAY) return t('admin.modelIntegrity.scheduling.bps.every.hours', { n: 24 })
  if (seconds % DAY === 0) return t('admin.modelIntegrity.scheduling.bps.every.days', { n: seconds / DAY })
  if (seconds % HOUR === 0) return t('admin.modelIntegrity.scheduling.bps.every.hours', { n: Math.round(seconds / HOUR) })
  return t('admin.modelIntegrity.scheduling.bps.every.minutes', { n: Math.round(seconds / 60) })
}

function formatDateTime(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

function disabledText(reason: string) {
  const key = bpsDisabledKey(reason)
  return t(`admin.modelIntegrity.scheduling.bps.disabled.${key}`, { reason })
}

function openBPS(item: OpenAIEvalBPSAccountConfig | null) {
  editingBPS.value = item
  bpsDialogOpen.value = true
}

function closeBPS() {
  bpsDialogOpen.value = false
  editingBPS.value = null
}

/** Only touches bps_accounts; test targets in config.accounts stay as they are. */
function applyBPS(value: OpenAIEvalBPSAccountConfig) {
  const existing = bpsAccounts.value.find(item => item.account_id === value.account_id)
  if (existing) Object.assign(existing, normalizeBPSAccount({ ...existing, ...value }))
  else bpsAccounts.value.push(normalizeBPSAccount({ ...value }))
  closeBPS()
}

function confirmRemove() {
  const target = pendingRemove.value
  pendingRemove.value = null
  if (!target) return
  const index = bpsAccounts.value.findIndex(item => item.account_id === target.account_id)
  if (index >= 0) bpsAccounts.value.splice(index, 1)
  closeBPS()
}

async function confirmReset() {
  const item = pendingReset.value
  pendingReset.value = null
  if (!item) return
  resetting.value = item.account_id
  try {
    const { state } = await accountsAPI.resetOpenAIBPSState({ account_id: item.account_id })
    // Runtime fields are not part of the save payload, so this does not mark the page dirty.
    Object.assign(item, {
      active: Boolean(state?.active),
      state: state?.active ? 'bps' : 'native',
      disabled_reason: state?.disabled_reason ?? '',
      degraded_streak: state?.degraded_streak ?? 0,
      healthy_streak: state?.healthy_streak ?? 0,
      updated_at: state?.updated_at ?? item.updated_at
    })
    appStore.showSuccess(t('admin.modelIntegrity.scheduling.bps.resetDone'))
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.modelIntegrity.scheduling.bps.resetFailed')))
  } finally {
    resetting.value = 0
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
  // Only fills a rule that never had weights; existing rule weights are kept as edited.
  for (const rule of rules.value) {
    if (rule.policy === 'custom_balance' && !rule.custom_balance) rule.custom_balance = normalizeCustomBalance(config.custom_balance)
  }
  // An all-zero set would be silently replaced by the defaults on save, so stop and show where it is.
  const invalidRules = rules.value.filter(rule => rule.policy === 'custom_balance' && !isValidCustomBalance(rule.custom_balance))
  if ((defaultPolicy.value === 'custom_balance' && !isValidCustomBalance(config.custom_balance)) || invalidRules.length) {
    for (const rule of invalidRules) if (!isEditingWeights(rule)) editingWeights.value.push(rule)
    appStore.showError(t('admin.modelIntegrity.scheduling.policy.custom.zeroTotal'))
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
.sched-section { @apply min-w-0 space-y-4 rounded-xl border border-gray-200 bg-white p-5 dark:border-dark-700 dark:bg-dark-800/60 sm:p-6; }
.sched-section-overflow { contain: paint; @apply overflow-x-hidden; }
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
.rule-weights { @apply min-w-0 space-y-3 border-t border-gray-200 pt-3 dark:border-dark-700 sm:col-span-4; }
.rule-weights-head { @apply flex flex-wrap items-center justify-between gap-2; }
.rule-weights-summary { @apply flex min-w-0 flex-wrap items-baseline gap-x-2 text-xs tabular-nums text-gray-600 dark:text-gray-300; }
.rule-weights-invalid { @apply text-rose-700 dark:text-rose-300; }
.rule-weights-title { @apply font-medium text-gray-800 dark:text-gray-200; }
.bps-head { @apply flex flex-wrap items-start justify-between gap-4; }
.sched-warn { @apply max-w-[80ch] rounded-md bg-amber-50 px-3 py-2 text-xs leading-relaxed text-amber-900 dark:bg-amber-950/30 dark:text-amber-200; }
.refresh { @apply space-y-3 border-t border-gray-100 pt-5 dark:border-dark-700; }
.refresh-head { @apply flex flex-wrap items-start justify-between gap-3; }
.refresh-controls { @apply flex flex-wrap items-end gap-3; }
.refresh-select { @apply w-auto min-w-[9rem]; }
.refresh-minutes { @apply w-32; }
.refresh-status { @apply flex flex-wrap gap-x-4 gap-y-1 text-xs tabular-nums text-gray-600 dark:text-gray-300; }
.refresh-pending { @apply text-xs text-amber-800 dark:text-amber-300; }
.refresh-error { @apply break-words text-xs text-rose-700 [overflow-wrap:anywhere] dark:text-rose-300; }
.custom-balance { @apply mt-4 rounded-lg border border-gray-200 bg-gray-50/70 p-4 dark:border-dark-600 dark:bg-dark-800/60; }
.custom-balance-head { @apply mb-3 flex flex-wrap items-baseline justify-between gap-2; }
.bps-master { @apply flex max-w-sm cursor-pointer items-start gap-3 text-sm; }
.bps-empty { @apply flex flex-wrap items-center justify-between gap-3 rounded-lg border border-dashed border-gray-300 px-4 py-4 text-sm text-gray-600 dark:border-dark-600 dark:text-gray-400; }
.bps-empty p { @apply max-w-[68ch]; }
.bps-summary { @apply flex flex-wrap items-center gap-x-5 gap-y-1 text-sm text-gray-600 dark:text-gray-300; }
.bps-row-locked td { @apply bg-rose-50/60 dark:bg-rose-950/20; }
.bps-table { @apply w-full min-w-[52rem] text-left text-sm; }
.bps-table th { @apply border-b border-gray-200 px-3 py-2 text-xs font-medium text-gray-500 dark:border-dark-700 dark:text-gray-400; }
.bps-table td { @apply border-b border-gray-100 px-3 py-3 align-top dark:border-dark-700; }
.bps-table tr:last-child td { @apply border-b-0; }
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
