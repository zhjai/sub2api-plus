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

            <!-- Activation is explicit and sits next to the policy it activates. -->
            <div class="effects" data-testid="effects-panel">
              <label class="effects-switch">
                <Toggle v-model="config.effects_enabled" data-testid="effects-toggle" @update:model-value="markExplicitEffects" />
                <span>
                  <span class="effects-switch-label">{{ t('admin.modelIntegrity.scheduling.evaluation.effects.toggle') }}</span>
                  <span class="effects-switch-hint">{{ t('admin.modelIntegrity.scheduling.evaluation.effects.hint') }}</span>
                </span>
              </label>
              <p v-if="!config.effects_enabled" class="effects-off" data-testid="effects-explicit-off">{{ t('admin.modelIntegrity.scheduling.evaluation.effects.explicitOff') }}</p>
              <p v-if="activationNote" class="effects-note" role="status" data-testid="effects-auto-note">{{ activationNote }}</p>
            </div>

            <PolicyMixer
              v-model="policyChoice"
              name="default-policy"
              :label="t('admin.modelIntegrity.scheduling.policy.defaultLabel')"
              :custom-balance="config.custom_balance"
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

            <!-- 调度评估间隔. The page's only 立即评估 lives on this row. -->
            <div class="refresh" data-testid="quality-refresh">
              <div class="min-w-0">
                <h3 class="sched-h3">{{ t('admin.modelIntegrity.scheduling.quality.title') }}</h3>
                <p class="sched-hint">{{ t('admin.modelIntegrity.scheduling.quality.hint') }}</p>
              </div>
              <div class="refresh-row">
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
                <button
                  type="button"
                  class="btn btn-primary btn-sm refresh-run"
                  :disabled="evaluating || saving || conflict"
                  :aria-busy="evaluating ? 'true' : undefined"
                  :title="t('admin.modelIntegrity.scheduling.evaluation.runHint')"
                  data-testid="evaluate-now"
                  @click="runEvaluation"
                >
                  <Icon name="bolt" size="sm" :class="evaluating ? 'motion-safe:animate-pulse' : ''" />
                  {{ evaluating ? t('admin.modelIntegrity.scheduling.evaluation.running') : t('admin.modelIntegrity.scheduling.evaluation.run') }}
                </button>
              </div>
              <p class="refresh-status" data-testid="quality-refresh-status" aria-live="polite">
                <span class="effects-pill" :class="`effects-pill-${effectiveTone}`" data-testid="effects-state">{{ t(`admin.modelIntegrity.scheduling.evaluation.effective.${effectiveKey}`) }}</span>
                <span v-if="lastEvaluatedAt" data-testid="last-evaluated">{{ t('admin.modelIntegrity.scheduling.quality.lastRefreshTrigger', { time: formatDateTime(lastEvaluatedAt), trigger: lastTriggerText }) }}</span>
                <span v-else>{{ t('admin.modelIntegrity.scheduling.quality.neverRefreshed') }}</span>
                <span v-if="nextEvaluationAt" data-testid="next-evaluation">{{ t('admin.modelIntegrity.scheduling.quality.nextRefresh', { time: formatDateTime(nextEvaluationAt) }) }}</span>
              </p>
              <p v-if="evaluating" class="evaluate-progress" role="status" data-testid="evaluate-progress">
                <Icon name="refresh" size="sm" class="motion-safe:animate-spin" />{{ t('admin.modelIntegrity.scheduling.evaluation.runningHint') }}
              </p>
              <p v-if="intervalPending" class="refresh-pending" data-testid="quality-interval-pending">{{ t('admin.modelIntegrity.scheduling.quality.pending', { interval: intervalText(savedQualityRefreshInterval) }) }}</p>
              <p v-if="dirty" class="evaluate-note" data-testid="evaluate-dirty">{{ t('admin.modelIntegrity.scheduling.evaluation.dirtyNote') }}</p>
              <p v-if="conflict" class="evaluate-note" data-testid="evaluate-blocked">{{ t('admin.modelIntegrity.scheduling.evaluation.blocked') }}</p>
              <p v-if="evaluationMessage" class="evaluate-message" :class="evaluationError ? 'evaluate-message-error' : 'evaluate-message-ok'" :role="evaluationError ? 'alert' : 'status'" data-testid="evaluate-message">{{ evaluationMessage }}</p>
              <p v-if="evaluationKept" class="sched-note" data-testid="evaluate-kept">{{ t('admin.modelIntegrity.scheduling.evaluation.failedKept') }}</p>
              <p v-if="confirmationMessage" class="evaluate-message evaluate-message-error" role="status" data-testid="save-evaluation-error">
                {{ confirmationMessage }}
                <span class="block">{{ t('admin.modelIntegrity.scheduling.evaluation.savedButFailedHint') }}</span>
              </p>
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

            <div class="thresholds" data-testid="scheduling-thresholds">
              <div class="sched-section-head">
                <h3 class="sched-h3">{{ t('admin.modelIntegrity.scheduling.thresholds.title') }}</h3>
                <p class="sched-hint">{{ t('admin.modelIntegrity.scheduling.thresholds.hint') }}</p>
              </div>
              <SchedulingThresholdsEditor
                ref="thresholdsEditor"
                v-model="config.scheduling_thresholds"
                :legend="t('admin.modelIntegrity.scheduling.thresholds.title')"
                :in-use="thresholdPoliciesInUse"
              />
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

        <section class="sched-section sched-section-overflow" aria-labelledby="ranking-title" data-testid="account-leaderboard">
          <div class="sched-section-head">
            <h2 id="ranking-title" class="sched-h2">{{ t('admin.modelIntegrity.scheduling.board.title') }}</h2>
            <p class="sched-hint">{{ t('admin.modelIntegrity.scheduling.board.hint') }}</p>
          </div>
          <p v-if="isInactiveStatus(ranking.effectiveStatus)" class="sched-warn" role="note" data-testid="ranking-effects-off">
            <strong>{{ t('admin.modelIntegrity.scheduling.evaluation.effectsOffTitle') }}</strong>
            {{ t(`admin.modelIntegrity.scheduling.board.inactive.${ranking.effectiveStatus}`) }}
          </p>
          <AccountLeaderboard
            ref="leaderboard"
            :groups="groups"
            :account-label="accountLabel"
            :rules="savedRules"
            :current-revision="config.revision ?? null"
            @overview="onOverview"
          />
        </section>

        <section class="sched-section" aria-labelledby="records-title">
          <div class="decisions-head">
            <h2 id="records-title" class="sched-h2">{{ t('admin.modelIntegrity.scheduling.records.title') }}</h2>
            <div class="tabs" role="tablist" :aria-label="t('admin.modelIntegrity.scheduling.records.title')">
              <button
                v-for="tab in RECORD_TABS"
                :id="`records-tab-${tab}`"
                :key="tab"
                type="button"
                role="tab"
                class="tab"
                :class="{ 'tab-active': recordTab === tab }"
                :aria-selected="recordTab === tab"
                :aria-controls="`records-panel-${tab}`"
                :tabindex="recordTab === tab ? 0 : -1"
                :data-testid="`records-tab-${tab}`"
                @click="recordTab = tab"
                @keydown.left.prevent="moveTab(-1)"
                @keydown.right.prevent="moveTab(1)"
              >{{ t(`admin.modelIntegrity.scheduling.records.tabs.${tab}`) }}</button>
            </div>
          </div>

          <div v-if="recordTab === 'evaluations'" id="records-panel-evaluations" role="tabpanel" aria-labelledby="records-tab-evaluations" data-testid="records-evaluations">
            <EvaluationRecords :current="recordCurrent" :previous="recordPrevious" :error="ranking.error" :in-progress="evaluating || ranking.inProgress" />
          </div>
          <div v-else id="records-panel-requests" role="tabpanel" aria-labelledby="records-tab-requests" class="space-y-3" data-testid="records-requests">
            <div class="decisions-head">
              <p class="sched-hint">{{ t('admin.modelIntegrity.scheduling.decisions.hint', { window: TRACE_RETAINED }) }}</p>
              <div class="decisions-controls">
                <!-- The server filters its retained records by the group the request was routed in. -->
                <select v-model="traceGroupId" class="input decisions-group" :aria-label="t('admin.modelIntegrity.scheduling.decisions.filterGroupLabel')" data-testid="requests-group">
                  <option :value="null">{{ t('admin.modelIntegrity.scheduling.decisions.filterGroup') }}</option>
                  <option v-for="group in groups" :key="group.id" :value="group.id">{{ group.name }}</option>
                  <!-- Keeps a chosen group selectable when the list no longer has it. -->
                  <option v-if="traceGroupId !== null && !groups.some(group => group.id === traceGroupId)" :value="traceGroupId">{{ groupFilterName(traceGroupId) }}</option>
                </select>
                <button type="button" class="btn btn-secondary btn-sm h-9" :disabled="tracesLoading" :aria-busy="tracesLoading ? 'true' : undefined" data-testid="requests-refresh" @click="loadTraces">
                  <Icon name="refresh" size="sm" :class="tracesLoading ? 'motion-safe:animate-spin' : ''" />{{ t('admin.modelIntegrity.common.refresh') }}
                </button>
              </div>
            </div>
            <p v-if="groupsFailed" class="sched-note" data-testid="requests-groups-failed">{{ t('admin.modelIntegrity.scheduling.decisions.groupsUnavailable') }}</p>
            <p v-if="tracesError" class="evaluate-message evaluate-message-error" role="alert" data-testid="requests-error">
              {{ tracesError }}
              <span v-if="traces.length" class="block text-xs" data-testid="requests-kept">{{ t('admin.modelIntegrity.scheduling.records.requestsFailedKept') }}</span>
            </p>
            <p v-if="tracesScope" class="sched-note" aria-live="polite" data-testid="requests-scope">{{ tracesScope }}</p>
            <DecisionLedger
              :traces="traces"
              :account-name="accountName"
              :retained="TRACE_RETAINED"
              :loading="tracesLoading"
              :failed="Boolean(tracesError)"
              :group-filter="traceGroupId === null ? null : groupFilterName(traceGroupId)"
              @clear-group="traceGroupId = null"
            />
          </div>
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
import AccountLeaderboard from '@/components/admin/modelIntegrity/AccountLeaderboard.vue'
import EvaluationRecords from '@/components/admin/modelIntegrity/EvaluationRecords.vue'
import BpsAccountDialog from '@/components/admin/modelIntegrity/BpsAccountDialog.vue'
import PolicyWeightsEditor from '@/components/admin/modelIntegrity/PolicyWeightsEditor.vue'
import SchedulingThresholdsEditor from '@/components/admin/modelIntegrity/SchedulingThresholdsEditor.vue'
import { accountsAPI, listSchedulerDecisions, type OpenAIEvalAccountOverview, type OpenAIEvalBPSAccountConfig, type OpenAIEvalRankingSummary, type OpenAIEvalSchedulingPolicy, type OpenAIEvalSchedulingPolicyRule, type SchedulerDecisionTrace } from '@/api/admin/accounts'
import groupsAPI from '@/api/admin/groups'
import type { AdminGroup } from '@/types'
import { useAppStore } from '@/stores/app'
import { extractApiErrorMessage } from '@/utils/apiError'
import { CUSTOM_FACTORS, CUSTOM_INTERVAL, DAY, EFFECTIVE_KEYS, HOUR, MAX_INTERVAL_MINUTES, QUALITY_REFRESH_INTERVALS, THRESHOLD_POLICIES, absolutePriorities, customBalanceIssue, customBalanceIssueKey, invalidThresholdFields,bpsAccountLane, bpsDisabledKey, bpsModeOf, canResetBPSAccount, customBalanceShares, effectiveTone as effectiveToneOf, hasNoPositiveWeight, isDirectOAuthRoute, isInactiveStatus, isValidCustomBalance, normalizeBPSAccount, normalizeCustomBalance, normalizeQualityRefreshInterval, rankingErrorText, rankingTriggerKey, type BPSLane, type RankingReadOutcome } from './modelIntegrity'
import { useModelIntegrityConfig } from './useModelIntegrityConfig'

/** Records read per request; the server filters its retained window before applying it. */
const TRACE_LIMIT = 50
/** Records each instance keeps in memory, across all groups; cleared on restart. */
const TRACE_RETAINED = 256
const RULE_POLICIES: OpenAIEvalSchedulingPolicyRule['policy'][] = ['cost_first', 'stability_first', 'avoid_degradation', 'custom_balance']
const GATES = ['session', 'model', 'status', 'features', 'privacy', 'capacity'] as const
const RECORD_TABS = ['evaluations', 'requests'] as const
type RecordTab = typeof RECORD_TABS[number]

const { t } = useI18n()
const appStore = useAppStore()
const { config, catalog, accounts, loading, loaded, saving, conflict, dirty, ranking, load, reloadConfig, save, accountName, accountLabel, savedQualityRefreshInterval, savedRules, applyRankingSnapshot, applyRankingSummary, foldedLegacyStability } = useModelIntegrityConfig()

const traces = ref<SchedulerDecisionTrace[]>([])
const tracesLoading = ref(false)
const tracesError = ref('')
/** Request-record group filter; null is all groups. */
const traceGroupId = ref<number | null>(null)
/** The filter the records on screen were read with; undefined while none are. */
const tracesGroupId = ref<number | null | undefined>(undefined)
/** Only the newest read may update the records, whatever order responses arrive in. */
let traceRequest = 0
const recordTab = ref<RecordTab>('evaluations')
// -- Evaluation and ranking ------------------------------------------------
const groups = ref<AdminGroup[]>([])
/** The group list could not be read; the request filter then offers all groups only. */
const groupsFailed = ref(false)
const evaluating = ref(false)
const evaluationMessage = ref('')
const evaluationError = ref(false)
/** Set when a save was stored but its evaluation failed; shown until the next action. */
const confirmationMessage = ref('')
/** The leaderboard's latest successful read; it carries the previous record too. */
const overview = ref<OpenAIEvalAccountOverview | null>(null)
const leaderboard = ref<InstanceType<typeof AccountLeaderboard> | null>(null)
const pendingReset = ref<OpenAIEvalBPSAccountConfig | null>(null)
const pendingRemove = ref<OpenAIEvalBPSAccountConfig | null>(null)
const resetting = ref(0)
const bpsDialogOpen = ref(false)
const editingBPS = ref<OpenAIEvalBPSAccountConfig | null>(null)

const defaultPolicy = computed<OpenAIEvalSchedulingPolicy>({
  get: () => config.scheduling_policy ?? '',
  set: value => { config.scheduling_policy = value }
})
/** Weight selection is the event that may visibly turn effects on. */
const policyChoice = computed<OpenAIEvalSchedulingPolicy>({
  get: () => config.scheduling_policy ?? '',
  set: value => {
    const previous = config.scheduling_policy ?? ''
    config.scheduling_policy = value
    applyPolicyChoice(previous, value)
  }
})
/** True when the administrator turned the switch themselves; suppressing the
 *  automatic enable note in that case avoids claiming credit for their action. */
const explicitEffectsChange = ref(false)
const activationNote = ref('')

/** Every status the server can report, with an unknown value falling back safely. */
const effectiveKey = computed(() => (ranking.effectiveStatus && EFFECTIVE_KEYS.includes(ranking.effectiveStatus as never) ? ranking.effectiveStatus : 'inactive_legacy_policy'))
const effectiveTone = computed(() => effectiveToneOf(ranking.effectiveStatus))

function markExplicitEffects() {
  explicitEffectsChange.value = true
  activationNote.value = ''
}

/**
 * Selecting a real policy is the only event that may visibly switch effects on.
 * Loading configuration never does, and an explicit off stays off. The note
 * names what happened so the switch never moves without explanation.
 */
function applyPolicyChoice(previous: OpenAIEvalSchedulingPolicy, next: OpenAIEvalSchedulingPolicy) {
  if (next === previous) return
  if (next !== '' && !config.effects_enabled) {
    config.effects_enabled = true
    activationNote.value = t('admin.modelIntegrity.scheduling.evaluation.effects.selectionEnabled')
    return
  }
  activationNote.value = next === '' && config.effects_enabled && explicitEffectsChange.value
    ? t('admin.modelIntegrity.scheduling.evaluation.effects.selectionEnabledOff')
    : ''
}

const publishedTime = (summary: OpenAIEvalRankingSummary | null | undefined) => {
  const time = summary ? Date.parse(summary.published_at || summary.evaluated_at) : NaN
  return Number.isNaN(time) ? -Infinity : time
}

/**
 * Keeps the shared status projection in step with what the leaderboard read.
 * A read that lags behind a summary this page already received (another
 * instance, or a read racing the evaluation) never moves the record backwards.
 */
function onOverview(page: OpenAIEvalAccountOverview) {
  overview.value = page
  const older = ranking.summary && page.summary && publishedTime(page.summary) < publishedTime(ranking.summary)
  applyRankingSnapshot(older ? { ...page, summary: undefined } : page)
}

/**
 * The newest completed evaluation. An evaluate or save response can be newer
 * than the last leaderboard read (for example when that read failed), so the
 * record appears at once, whatever the leaderboard managed to load.
 */
const recordCurrent = computed<OpenAIEvalRankingSummary | null>(() => {
  const read = overview.value?.summary ?? null
  const own = ranking.summary
  if (!read) return own
  if (!own) return read
  return publishedTime(own) > publishedTime(read) ? own : read
})

const recordPrevious = computed<OpenAIEvalRankingSummary | null>(() => {
  const current = recordCurrent.value
  const read = overview.value?.summary ?? null
  // The read summary was superseded by a newer response: it is now the previous one.
  if (read && current && read.evaluation_id !== current.evaluation_id) return read
  const previous = overview.value?.previous_summary ?? null
  return previous && previous.evaluation_id !== current?.evaluation_id ? previous : null
})

const lastTriggerText = computed(() => t(`admin.modelIntegrity.scheduling.records.trigger.${rankingTriggerKey(recordCurrent.value?.trigger)}`))

function moveTab(step: number) {
  const index = RECORD_TABS.indexOf(recordTab.value)
  recordTab.value = RECORD_TABS[(index + step + RECORD_TABS.length) % RECORD_TABS.length]
  document.getElementById(`records-tab-${recordTab.value}`)?.focus()
}

/** True when the evaluation itself failed and the previous order is still in force. */
const evaluationKept = ref(false)

/**
 * Runs the full evaluation with the saved configuration from the interval
 * row's 立即评估. It sends no body, so unsaved edits are neither sent nor
 * affected.
 */
async function runEvaluation() {
  if (evaluating.value || saving.value || conflict.value) return
  evaluating.value = true
  evaluationMessage.value = ''
  evaluationError.value = false
  evaluationKept.value = false
  confirmationMessage.value = ''
  let summary: OpenAIEvalRankingSummary
  try {
    summary = await accountsAPI.evaluateOpenAIEvalRanking()
  } catch (error) {
    evaluationError.value = true
    evaluationKept.value = true
    evaluationMessage.value = evaluationFailureText(error)
    evaluating.value = false
    return
  }
  // The record is the server's own summary, shown before any leaderboard read.
  applyRankingSummary(summary)
  evaluating.value = false
  const read = await reloadRanking()
  const time = formatDateTime(summary.published_at || summary.evaluated_at)
  if (read && !read.ok) {
    evaluationError.value = true
    evaluationMessage.value = t('admin.modelIntegrity.scheduling.evaluation.doneReadFailed', { time, reason: read.message })
  } else {
    evaluationMessage.value = summary.coverage.status === 'complete'
      ? t('admin.modelIntegrity.scheduling.evaluation.done', { time })
      : t('admin.modelIntegrity.scheduling.evaluation.donePartial', { time })
  }
}

/** Distinguishes a superseded run, an unavailable service and a timeout from a plain failure. */
function evaluationFailureText(error: unknown) {
  const status = (error as { status?: number; response?: { status?: number } })?.response?.status ?? (error as { status?: number })?.status
  if (status === 409) return t('admin.modelIntegrity.scheduling.evaluation.superseded')
  if (status === 503) return t('admin.modelIntegrity.scheduling.evaluation.unavailable')
  if (status === 504) return t('admin.modelIntegrity.scheduling.evaluation.timedOut')
  return t('admin.modelIntegrity.scheduling.evaluation.failed', { reason: rankingErrorText(error, extractApiErrorMessage(error, t('admin.modelIntegrity.common.loadFailed'))) })
}

/**
 * Has the leaderboard read the published generation again. After an
 * evaluation or a save the rows on screen are known to be superseded; they
 * stay, labelled as the previous result, only if the new read fails.
 */
async function reloadRanking(options: { invalidate?: boolean } = { invalidate: true }): Promise<RankingReadOutcome | null> {
  return leaderboard.value ? leaderboard.value.reload(options) : null
}
// useModelIntegrityConfig always initialises policies to an array.
const rules = computed(() => config.policies as OpenAIEvalSchedulingPolicyRule[])
const efforts = computed(() => catalog.value?.reasoning_efforts?.length ? catalog.value.reasoning_efforts : [''])
const usesAvoidDegradation = computed(() => defaultPolicy.value === 'avoid_degradation' || rules.value.some(rule => rule.policy === 'avoid_degradation'))
/** Any saved policy that reads the integrity pass rate. */
const usesQuality = computed(() => usesAvoidDegradation.value ||
  (defaultPolicy.value === 'custom_balance' && Number(config.custom_balance?.quality) > 0) ||
  rules.value.some(rule => rule.policy === 'custom_balance' && Number(rule.custom_balance?.quality) > 0))
/** Threshold rows the default policy or a model rule currently uses, as edited. */
const thresholdPoliciesInUse = computed(() => {
  const used = new Set<string>([defaultPolicy.value, ...rules.value.map(rule => rule.policy)])
  return THRESHOLD_POLICIES.filter(policy => used.has(policy))
})
const thresholdsEditor = ref<InstanceType<typeof SchedulingThresholdsEditor> | null>(null)

// 调度评估间隔: how often the pass-rate snapshot is rebuilt, not how often tests run.
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

/** The last published evaluation, whichever trigger built it. */
const lastEvaluatedAt = computed(() => ranking.summary?.published_at || ranking.summary?.evaluated_at || config.quality_refreshed_at || null)
const nextEvaluationAt = computed(() => ranking.summary?.next_evaluation_at || config.quality_next_refresh_at || null)
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
  const issue = customBalanceIssue(rule.custom_balance)
  if (issue) return t(customBalanceIssueKey(issue))
  const shares = customBalanceShares(rule.custom_balance)
  const weights = CUSTOM_FACTORS.map(factor => `${t(`admin.modelIntegrity.scheduling.policy.custom.${factor}`)} ${shares[factor]}%`).join(t('admin.modelIntegrity.scheduling.rules.weightsSeparator'))
  const priorities = absolutePriorities(rule.custom_balance)
  if (!priorities.length) return weights
  const list = priorities.map(factor => t(`admin.modelIntegrity.scheduling.policy.custom.${factor}`)).join(t('admin.modelIntegrity.scheduling.policy.custom.priorities.summaryJoin'))
  // Every weight at 0% has no score to list; account order breaks the ties.
  if (hasNoPositiveWeight(rule.custom_balance)) return t('admin.modelIntegrity.scheduling.policy.custom.priorities.summaryAccountOrder', { list })
  return t('admin.modelIntegrity.scheduling.policy.custom.priorities.summary', { list, weights })
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
  const groupId = traceGroupId.value
  const request = ++traceRequest
  // Records read with another filter never stand in for this one, not even while it loads.
  if (tracesGroupId.value !== groupId) {
    traces.value = []
    tracesGroupId.value = undefined
  }
  tracesLoading.value = true
  tracesError.value = ''
  try {
    const page = await listSchedulerDecisions(TRACE_LIMIT, groupId)
    if (request !== traceRequest) return
    traces.value = page.items ?? []
    tracesGroupId.value = groupId
  } catch (error) {
    if (request !== traceRequest) return
    // Records already read with this filter stay; the failure is stated next to them.
    tracesError.value = t('admin.modelIntegrity.scheduling.records.requestsFailed', { reason: extractApiErrorMessage(error, t('admin.modelIntegrity.common.loadFailed')) })
  } finally {
    if (request === traceRequest) tracesLoading.value = false
  }
}

watch(traceGroupId, loadTraces)

/** The filter's label; the filter itself is by ID, so today's name is right here. */
function groupFilterName(id: number) {
  return groups.value.find(group => group.id === id)?.name || t('admin.modelIntegrity.scheduling.decisions.groupId', { id })
}

/** What the list covers: the retained window, the group, and the per-read cap. */
const tracesScope = computed(() => {
  if (!traces.value.length || tracesGroupId.value === undefined) return ''
  const count = traces.value.length
  const scope = tracesGroupId.value === null
    ? t('admin.modelIntegrity.scheduling.decisions.scopeAll', { window: TRACE_RETAINED, count })
    : t('admin.modelIntegrity.scheduling.decisions.scopeGroup', { window: TRACE_RETAINED, count, group: groupFilterName(tracesGroupId.value) })
  return count >= TRACE_LIMIT ? `${scope}${t('admin.modelIntegrity.scheduling.decisions.scopeCapped', { limit: TRACE_LIMIT })}` : scope
})

async function handleSave() {
  if (duplicateRuleIndexes.value.size || rules.value.some(rule => !rule.requested_model)) {
    appStore.showError(rules.value.some(rule => !rule.requested_model) ? t('admin.modelIntegrity.scheduling.rules.pickModel') : t('admin.modelIntegrity.scheduling.rules.duplicate'))
    return
  }
  // Only fills a rule that never had weights; existing rule weights are kept as edited.
  for (const rule of rules.value) {
    if (rule.policy === 'custom_balance' && !rule.custom_balance) rule.custom_balance = normalizeCustomBalance(config.custom_balance)
  }
  // A rejected set would fail the whole save or be replaced by the defaults, so stop and show where it is.
  const invalidRules = rules.value.filter(rule => rule.policy === 'custom_balance' && !isValidCustomBalance(rule.custom_balance))
  const defaultIssue = defaultPolicy.value === 'custom_balance' ? customBalanceIssue(config.custom_balance) : null
  if (defaultIssue || invalidRules.length) {
    for (const rule of invalidRules) if (!isEditingWeights(rule)) editingWeights.value.push(rule)
    appStore.showError(t(customBalanceIssueKey(defaultIssue ?? customBalanceIssue(invalidRules[0].custom_balance) ?? 'zero_total')))
    return
  }
  // The server would reject the whole save; stop here so nothing else is lost or reset.
  if (invalidThresholdFields(config.scheduling_thresholds).length) {
    appStore.showError(t('admin.modelIntegrity.scheduling.thresholds.invalid'))
    thresholdsEditor.value?.focusFirstInvalid()
    return
  }
  try {
    const result = await save()
    if (result === 'saved' || result === 'saved_evaluation_failed') {
      // An earlier evaluation result no longer describes what is in force.
      evaluationMessage.value = ''
      evaluationError.value = false
      evaluationKept.value = false
    }
    if (result === 'saved') {
      confirmationMessage.value = ''
      // Saving already evaluated; its summary is the new record.
      appStore.showSuccess(t('admin.modelIntegrity.scheduling.evaluation.savedAndEvaluated'))
      await reloadRanking()
    } else if (result === 'saved_evaluation_failed') {
      // Saved is not the same as in force: name both, with the server's reason.
      const reason = ranking.error?.message || t('admin.modelIntegrity.common.saveFailed')
      confirmationMessage.value = t('admin.modelIntegrity.scheduling.evaluation.savedButFailed', { reason })
      appStore.showError(confirmationMessage.value)
      // The previous order is still in force, so the rows on screen stay valid.
      await reloadRanking({ invalidate: false })
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
    return
  }
  // Another revision may have published a new order; the board keeps its rows
  // only if the generation is unchanged.
  await reloadRanking({ invalidate: false })
}

async function initialLoad() {
  try {
    await load()
  } catch (error) {
    appStore.showError(extractApiErrorMessage(error, t('admin.modelIntegrity.common.loadFailed')))
    return
  }
  // The ranking board reads its first page itself when it mounts.
  // Records do not depend on the group list, so neither waits for the other.
  await Promise.all([loadGroups(), loadTraces()])
}

/** Group names for the ranking filter; a failure here leaves the filter empty. */
async function loadGroups() {
  try {
    groups.value = await groupsAPI.getAll('openai')
    groupsFailed.value = false
  } catch {
    groups.value = []
    groupsFailed.value = true
  }
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
.thresholds { @apply space-y-3 border-t border-gray-100 pt-5 dark:border-dark-700; }
.refresh-row { @apply flex flex-wrap items-end gap-3; }
/* The page's single evaluate action, pushed to the right end of the interval row. */
.refresh-run { @apply ml-auto h-9; }
.refresh-select { @apply w-auto min-w-[9rem]; }
.refresh-minutes { @apply w-32; }
.refresh-status { @apply flex flex-wrap items-center gap-x-4 gap-y-1 text-xs tabular-nums text-gray-600 dark:text-gray-300; }
.refresh-pending { @apply text-xs text-amber-800 dark:text-amber-300; }
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
.decisions-head { @apply flex flex-wrap items-center justify-between gap-3; }
.decisions-controls { @apply flex w-full flex-wrap items-center gap-2 sm:w-auto; }
.decisions-group { @apply h-9 min-w-0 flex-1 py-1 text-sm sm:w-auto sm:min-w-[11rem] sm:flex-none; }
.effects { @apply space-y-2 rounded-lg border border-gray-200 px-4 py-3 dark:border-dark-700; }
.effects-switch { @apply flex cursor-pointer items-start gap-3 text-sm; }
.effects-switch-label { @apply block font-medium text-gray-900 dark:text-white; }
.effects-switch-hint { @apply mt-0.5 block max-w-[72ch] text-xs leading-relaxed text-gray-500 dark:text-gray-400; }
.effects-off { @apply max-w-[72ch] text-xs leading-relaxed text-amber-800 dark:text-amber-300; }
.effects-pill { @apply inline-flex items-center rounded-full px-2 py-0.5 text-xs font-semibold; }
.effects-pill-active { @apply bg-primary-600 text-white dark:bg-primary-500; }
.effects-pill-partial { @apply bg-violet-600 text-white dark:bg-violet-500; }
.effects-pill-off { @apply bg-gray-200 text-gray-700 dark:bg-dark-700 dark:text-gray-300; }
.effects-pill-error { @apply bg-rose-600 text-white dark:bg-rose-500; }
.effects-note { @apply rounded-md bg-sky-50 px-3 py-2 text-xs leading-relaxed text-sky-900 dark:bg-sky-950/30 dark:text-sky-200; }
.evaluate-progress { @apply flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300; }
.evaluate-note { @apply text-xs leading-relaxed text-amber-800 dark:text-amber-300; }
.evaluate-message { @apply max-w-[80ch] rounded-md px-3 py-2 text-sm leading-relaxed; }
.evaluate-message-ok { @apply bg-primary-50 text-primary-900 dark:bg-primary-950/30 dark:text-primary-100; }
.evaluate-message-error { @apply bg-rose-50 text-rose-900 dark:bg-rose-950/30 dark:text-rose-200; }
</style>
