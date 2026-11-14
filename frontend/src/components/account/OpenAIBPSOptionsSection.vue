<template>
  <div
    class="space-y-4 border-t border-gray-200 pt-4 dark:border-dark-600"
    data-testid="openai-bps-options-section"
  >
    <div>
      <h4 class="input-label mb-1">{{ t('admin.accounts.bpsAccount.options.sectionTitle') }}</h4>
      <div
        class="rounded-lg border border-emerald-200/80 bg-emerald-50/70 px-3 py-2 text-xs leading-relaxed text-emerald-800 dark:border-emerald-800/50 dark:bg-emerald-950/30 dark:text-emerald-200"
        data-testid="openai-bps-client-tools-hint"
      >
        {{ t('admin.accounts.bpsAccount.options.clientToolsHint') }}
      </div>
    </div>

    <!-- Independent BPS OAuth Grant management (shown on native primary OpenAI OAuth accounts) -->
    <div
      v-if="!isBpsOnly"
      class="rounded-lg border border-gray-200 bg-gray-50/60 p-3.5 dark:border-dark-600 dark:bg-dark-700/40"
      data-testid="openai-bps-independent-grant-card"
    >
      <div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
        <div class="min-w-0 flex-1">
          <div class="flex flex-wrap items-center gap-2">
            <span class="text-sm font-medium text-gray-900 dark:text-white">
              {{ t('admin.accounts.bpsAccount.grant.sectionTitle') }}
            </span>
            <span
              data-testid="openai-bps-grant-status"
              :class="[
                'inline-flex items-center rounded border px-2 py-0.5 text-[11px] font-medium',
                grantStatusBadge.classes
              ]"
            >
              {{ grantStatusBadge.label }}
            </span>
          </div>
          <p class="mt-1 text-xs leading-relaxed text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.bpsAccount.grant.sectionDesc') }}
          </p>
          <p
            class="mt-1 text-xs text-gray-500 dark:text-gray-400"
            data-testid="openai-bps-grant-never-enables-hint"
          >
            {{ t('admin.accounts.bpsAccount.grant.neverAutoEnablesHint') }}
          </p>
          <p class="mt-0.5 text-[11px] text-gray-400 dark:text-gray-500">
            {{ t('admin.accounts.bpsAccount.grant.noTokensDisplayedHint') }}
          </p>
        </div>

        <div class="flex flex-wrap items-center gap-2">
          <button
            type="button"
            data-testid="openai-bps-attach-grant-btn"
            class="rounded-lg border border-primary-300 bg-white px-3 py-1.5 text-xs font-medium text-primary-700 transition-colors hover:bg-primary-50 dark:border-primary-700 dark:bg-dark-700 dark:text-primary-300 dark:hover:bg-primary-900/30"
            @click="emit('attach-grant')"
          >
            {{
              effectiveHasGrant
                ? t('admin.accounts.bpsAccount.grant.reauthAction')
                : t('admin.accounts.bpsAccount.grant.attachAction')
            }}
          </button>
          <button
            v-if="hasSavedAccount && hasSavedIndependentGrant && stagedGrantState === 'none'"
            type="button"
            data-testid="openai-bps-refresh-grant-btn"
            :disabled="refreshingGrant"
            class="rounded-lg border border-blue-200 bg-white px-3 py-1.5 text-xs font-medium text-blue-700 transition-colors hover:bg-blue-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-blue-800 dark:bg-dark-700 dark:text-blue-300 dark:hover:bg-blue-900/30"
            @click="emit('refresh-grant')"
          >
            {{
              refreshingGrant
                ? t('admin.accounts.bpsAccount.grant.refreshingAction')
                : t('admin.accounts.bpsAccount.grant.refreshAction')
            }}
          </button>
          <button
            v-if="(hasSavedIndependentGrant && stagedGrantState === 'none') || stagedGrantState === 'attached'"
            type="button"
            data-testid="openai-bps-remove-grant-btn"
            class="rounded-lg border border-red-200 bg-white px-3 py-1.5 text-xs font-medium text-red-600 transition-colors hover:bg-red-50 dark:border-red-800/70 dark:bg-dark-700 dark:text-red-400 dark:hover:bg-red-900/20"
            @click="emit('remove-grant')"
          >
            {{ t('admin.accounts.bpsAccount.grant.removeAction') }}
          </button>
          <button
            v-if="stagedGrantState !== 'none'"
            type="button"
            data-testid="openai-bps-cancel-staged-grant-btn"
            class="rounded-lg border border-gray-300 bg-white px-3 py-1.5 text-xs font-medium text-gray-700 transition-colors hover:bg-gray-50 dark:border-dark-500 dark:bg-dark-700 dark:text-gray-300 dark:hover:bg-dark-600"
            @click="emit('cancel-staged-grant')"
          >
            {{ t('admin.accounts.bpsAccount.grant.cancelStagedAction') }}
          </button>
        </div>
      </div>

      <div
        v-if="stagedGrantState === 'attached'"
        class="mt-2.5 rounded-md border border-emerald-200 bg-emerald-50/80 px-3 py-2 text-xs text-emerald-800 dark:border-emerald-800/60 dark:bg-emerald-900/20 dark:text-emerald-300"
        data-testid="openai-bps-staged-attach-banner"
      >
        {{ t('admin.accounts.bpsAccount.grant.stagedReadyHint') }}
      </div>
      <div
        v-else-if="stagedGrantState === 'removed'"
        class="mt-2.5 rounded-md border border-amber-200 bg-amber-50/80 px-3 py-2 text-xs text-amber-800 dark:border-amber-800/60 dark:bg-amber-900/20 dark:text-amber-300"
        data-testid="openai-bps-staged-remove-banner"
      >
        {{ t('admin.accounts.bpsAccount.grant.stagedRemoveHint') }}
      </div>

      <slot name="independent-grant-extra" />
    </div>

    <!-- Server runtime banners (403 auto-disable, grant validation vs local expiry, rate limit) -->
    <div
      v-if="runtimeState?.is403Disabled"
      class="rounded-lg border border-amber-300 bg-amber-50/90 p-3 text-xs leading-relaxed text-amber-900 dark:border-amber-700/70 dark:bg-amber-950/30 dark:text-amber-200"
      data-testid="openai-bps-runtime-403-banner"
    >
      <div class="font-semibold">{{ t('admin.accounts.bpsAccount.runtime.disabled403Title') }}</div>
      <p class="mt-1">{{ t('admin.accounts.bpsAccount.runtime.disabled403Desc') }}</p>
      <p class="mt-1 font-mono text-[11px] opacity-90">
        {{
          t('admin.accounts.bpsAccount.runtime.disabled403Meta', {
            disabledAt: runtimeState.disabled403At || '-',
            reason: runtimeState.disabled403Reason || 'http_403',
            model: runtimeState.probeModel || '-',
            lastProbeAt: runtimeState.lastProbeAt || '-'
          })
        }}
      </p>
      <p v-if="runtimeState.moved403GroupId" class="mt-1 font-medium">
        {{
          t('admin.accounts.bpsAccount.runtime.disabled403MovedGroup', {
            groupId: runtimeState.moved403GroupId
          })
        }}
      </p>
      <p class="mt-1 opacity-90">
        {{ t('admin.accounts.bpsAccount.runtime.disabled403ManualResetHint') }}
      </p>
    </div>

    <div
      v-if="runtimeState?.isGrantInvalid"
      class="rounded-lg border border-red-300 bg-red-50/90 p-3 text-xs leading-relaxed text-red-900 dark:border-red-800/70 dark:bg-red-950/30 dark:text-red-200"
      data-testid="openai-bps-runtime-grant-invalid-banner"
    >
      <div class="font-semibold">{{ t('admin.accounts.bpsAccount.runtime.grantInvalidTitle') }}</div>
      <p class="mt-1">{{ t('admin.accounts.bpsAccount.runtime.grantInvalidDesc') }}</p>
    </div>

    <div
      v-else-if="runtimeState?.isPrimaryTokenExpired"
      class="rounded-lg border border-amber-200 bg-amber-50/80 p-3 text-xs leading-relaxed text-amber-800 dark:border-amber-800/60 dark:bg-amber-950/20 dark:text-amber-200"
      data-testid="openai-bps-runtime-token-expired-banner"
    >
      <div class="font-semibold">{{ t('admin.accounts.bpsAccount.runtime.primaryExpiredTitle') }}</div>
      <p class="mt-1">{{ t('admin.accounts.bpsAccount.runtime.primaryExpiredDesc') }}</p>
    </div>

    <div
      v-if="runtimeState?.rateLimitUntil"
      class="rounded-lg border border-amber-200 bg-amber-50/80 px-3 py-2 text-xs text-amber-800 dark:border-amber-800/60 dark:bg-amber-950/20 dark:text-amber-200"
      data-testid="openai-bps-runtime-ratelimit-banner"
    >
      <span class="font-semibold">{{ t('admin.accounts.bpsAccount.runtime.rateLimitedTitle') }}:</span>
      <span class="ml-1">
        {{ t('admin.accounts.bpsAccount.runtime.rateLimitedDesc', { until: runtimeState.rateLimitUntil }) }}
      </span>
    </div>

    <!-- extra.openai_excel_bps (Explicit route enable) -->
    <div class="rounded-lg border border-gray-200 bg-gray-50/60 p-3.5 dark:border-dark-600 dark:bg-dark-700/40">
      <div class="flex items-start justify-between gap-4">
        <div class="min-w-0">
          <label
            :for="`${idPrefix}-bps-route-enabled`"
            class="input-label mb-0 cursor-pointer"
          >
            {{ t('admin.accounts.bpsAccount.options.routeEnabled') }}
          </label>
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.bpsAccount.options.routeEnabledDesc') }}
          </p>
          <p v-if="!isBpsOnly" class="mt-1 text-xs text-teal-700 dark:text-teal-300">
            {{ t('admin.accounts.bpsAccount.options.routeEnabledDualNote') }}
          </p>
        </div>
        <button
          :id="`${idPrefix}-bps-route-enabled`"
          type="button"
          role="switch"
          :aria-checked="currentOptions.enabled"
          data-testid="openai-bps-route-enabled-toggle"
          :class="[
            'relative inline-flex h-6 w-11 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2',
            currentOptions.enabled ? 'bg-primary-600' : 'bg-gray-200 dark:bg-dark-600'
          ]"
          @click="patchOptions({ enabled: !currentOptions.enabled })"
        >
          <span
            :class="[
              'pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out',
              currentOptions.enabled ? 'translate-x-5' : 'translate-x-0'
            ]"
          />
        </button>
      </div>
    </div>

    <!-- extra.openai_excel_bps_models (Mapped UPSTREAM models routed to BPS; distinct from model_mapping) -->
    <div
      class="rounded-lg border border-gray-200 bg-gray-50/60 p-3.5 dark:border-dark-600 dark:bg-dark-700/40"
      data-testid="openai-bps-models-card"
    >
      <div class="mb-2 flex flex-wrap items-start justify-between gap-2">
        <div class="min-w-0 flex-1">
          <label class="input-label mb-0">
            {{ t('admin.accounts.bpsAccount.options.modelsTitle') }}
          </label>
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.bpsAccount.options.modelsDesc') }}
          </p>
          <p
            class="mt-1 text-xs text-indigo-700 dark:text-indigo-300"
            data-testid="openai-bps-models-distinct-note"
          >
            {{ t('admin.accounts.bpsAccount.options.modelsDistinctNote') }}
          </p>
        </div>
        <button
          v-if="canSyncModels"
          type="button"
          data-testid="openai-bps-sync-models-btn"
          :disabled="syncingModels"
          class="inline-flex shrink-0 items-center gap-1 rounded-lg border border-blue-200 bg-white px-2.5 py-1 text-xs font-medium text-blue-700 transition-colors hover:bg-blue-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-blue-800 dark:bg-dark-700 dark:text-blue-300 dark:hover:bg-blue-900/30"
          @click="emit('sync-models')"
        >
          {{
            syncingModels
              ? t('admin.accounts.syncUpstreamModelsLoading')
              : t('admin.accounts.syncUpstreamModels')
          }}
        </button>
      </div>

      <div class="mb-3 grid grid-cols-1 gap-2 sm:grid-cols-2">
        <button
          type="button"
          data-testid="openai-bps-models-mode-all"
          :class="[
            'rounded-lg border px-3 py-2 text-left text-xs font-medium transition-all',
            currentOptions.modelsMode === 'all'
              ? 'border-primary-500 bg-primary-50 text-primary-700 dark:bg-primary-900/30 dark:text-primary-300'
              : 'border-gray-300 bg-white text-gray-700 hover:bg-gray-50 dark:border-dark-500 dark:bg-dark-700 dark:text-gray-300 dark:hover:bg-dark-600'
          ]"
          @click="patchOptions({ modelsMode: 'all' })"
        >
          {{ t('admin.accounts.bpsAccount.options.modelsModeAll') }}
        </button>
        <button
          type="button"
          data-testid="openai-bps-models-mode-selected"
          :class="[
            'rounded-lg border px-3 py-2 text-left text-xs font-medium transition-all',
            currentOptions.modelsMode === 'selected'
              ? 'border-primary-500 bg-primary-50 text-primary-700 dark:bg-primary-900/30 dark:text-primary-300'
              : 'border-gray-300 bg-white text-gray-700 hover:bg-gray-50 dark:border-dark-500 dark:bg-dark-700 dark:text-gray-300 dark:hover:bg-dark-600'
          ]"
          @click="patchOptions({ modelsMode: 'selected' })"
        >
          {{ t('admin.accounts.bpsAccount.options.modelsModeSelected') }}
        </button>
      </div>

      <div v-if="currentOptions.modelsMode === 'selected'" class="space-y-2.5">
        <div v-if="currentOptions.models.length > 0" class="flex flex-wrap gap-1.5">
          <span
            v-for="model in currentOptions.models"
            :key="model"
            data-testid="openai-bps-selected-model-chip"
            class="inline-flex items-center gap-1 rounded bg-white px-2 py-1 text-xs font-medium text-gray-800 ring-1 ring-gray-200 dark:bg-dark-700 dark:text-gray-200 dark:ring-dark-500"
          >
            <span>{{ model }}</span>
            <button
              type="button"
              class="rounded-full p-0.5 text-gray-400 hover:bg-gray-100 hover:text-gray-700 dark:hover:bg-dark-600 dark:hover:text-gray-200"
              :aria-label="`Remove ${model}`"
              @click="removeBPSModel(model)"
            >
              ×
            </button>
          </span>
        </div>
        <div
          v-else
          class="rounded border border-amber-200 bg-amber-50/80 px-2.5 py-1.5 text-xs text-amber-800 dark:border-amber-800/50 dark:bg-amber-900/20 dark:text-amber-300"
          data-testid="openai-bps-models-empty-hint"
        >
          {{ t('admin.accounts.bpsAccount.options.modelsEmptySelectedHint') }}
        </div>

        <!-- Candidate chips from account/synced models + suggested models -->
        <div v-if="mergedCandidateModels.length > 0" class="flex flex-wrap items-center gap-1.5">
          <button
            v-for="item in mergedCandidateModels"
            :key="item.value"
            type="button"
            :data-testid="`openai-bps-candidate-model-${item.value}`"
            :class="[
              'inline-flex items-center gap-1 rounded border px-2 py-1 text-xs transition-colors',
              currentOptions.models.includes(item.value)
                ? 'border-emerald-400 bg-emerald-50 text-emerald-800 dark:border-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300'
                : 'border-gray-300 bg-white text-gray-700 hover:bg-gray-50 dark:border-dark-500 dark:bg-dark-700 dark:text-gray-300 dark:hover:bg-dark-600'
            ]"
            @click="toggleBPSModel(item.value)"
          >
            <span>{{ currentOptions.models.includes(item.value) ? '✓' : '+' }} {{ item.value }}</span>
            <span
              v-if="item.suggested"
              class="rounded bg-gray-100 px-1 text-[10px] text-gray-500 dark:bg-dark-600 dark:text-gray-400"
            >
              {{ t('admin.accounts.bpsAccount.options.modelsSuggestedBadge') }}
            </span>
          </button>
        </div>

        <div class="flex flex-wrap items-center gap-2">
          <button
            type="button"
            data-testid="openai-bps-models-prefill-suggested"
            class="rounded-lg border border-emerald-200 bg-white px-2.5 py-1 text-xs font-medium text-emerald-700 hover:bg-emerald-50 dark:border-emerald-800/60 dark:bg-dark-700 dark:text-emerald-300 dark:hover:bg-emerald-900/30"
            @click="prefillSuggestedModels"
          >
            + {{ t('admin.accounts.bpsAccount.options.modelsPrefillSuggested') }}
          </button>
          <span class="text-[11px] text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.bpsAccount.models.prefillRecommendedHint') }}
          </span>
        </div>

        <div class="flex gap-2">
          <input
            v-model="customBPSModel"
            type="text"
            data-testid="openai-bps-models-input"
            class="input flex-1 text-xs"
            :placeholder="t('admin.accounts.bpsAccount.options.modelsPlaceholder')"
            @keydown.enter.prevent="addCustomBPSModel"
          />
          <button
            type="button"
            data-testid="openai-bps-models-add-btn"
            class="rounded-lg bg-primary-50 px-3 py-1.5 text-xs font-medium text-primary-600 hover:bg-primary-100 dark:bg-primary-900/30 dark:text-primary-400 dark:hover:bg-primary-900/50"
            @click="addCustomBPSModel"
          >
            {{ t('admin.accounts.bpsAccount.options.modelsAddButton') }}
          </button>
        </div>
      </div>
    </div>

    <!-- 403 Protection, Recovery, Group Move & Cache Billing -->
    <div
      class="space-y-3 rounded-lg border border-gray-200 bg-gray-50/60 p-3.5 dark:border-dark-600 dark:bg-dark-700/40"
      data-testid="openai-bps-403-protection-card"
    >
      <div class="text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">
        {{ t('admin.accounts.bpsAccount.options.protectionSectionTitle') }}
      </div>

      <!-- openai_excel_bps_auto_disable_on_403 -->
      <div class="flex items-start justify-between gap-4">
        <div class="min-w-0">
          <label
            :for="`${idPrefix}-bps-auto-disable-403`"
            class="input-label mb-0 cursor-pointer"
          >
            {{ t('admin.accounts.bpsAccount.options.autoDisableOn403') }}
          </label>
          <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.bpsAccount.options.autoDisableOn403Desc') }}
          </p>
        </div>
        <button
          :id="`${idPrefix}-bps-auto-disable-403`"
          type="button"
          role="switch"
          :aria-checked="currentOptions.autoDisableOn403"
          data-testid="openai-bps-auto-disable-403-toggle"
          :class="[
            'relative inline-flex h-6 w-11 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2',
            currentOptions.autoDisableOn403 ? 'bg-primary-600' : 'bg-gray-200 dark:bg-dark-600'
          ]"
          @click="patchOptions({ autoDisableOn403: !currentOptions.autoDisableOn403 })"
        >
          <span
            :class="[
              'pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out',
              currentOptions.autoDisableOn403 ? 'translate-x-5' : 'translate-x-0'
            ]"
          />
        </button>
      </div>

      <!-- openai_excel_bps_auto_recover_on_403 + openai_excel_bps_403_recovery_interval_minutes -->
      <div class="border-t border-gray-200/80 pt-3 dark:border-dark-600">
        <div class="flex items-start justify-between gap-4">
          <div class="min-w-0">
            <label
              :for="`${idPrefix}-bps-auto-recover-403`"
              class="input-label mb-0 cursor-pointer"
            >
              {{ t('admin.accounts.bpsAccount.options.autoRecoverOn403') }}
            </label>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.accounts.bpsAccount.options.autoRecoverOn403Desc') }}
            </p>
          </div>
          <button
            :id="`${idPrefix}-bps-auto-recover-403`"
            type="button"
            role="switch"
            :aria-checked="currentOptions.autoRecoverOn403"
            data-testid="openai-bps-auto-recover-403-toggle"
            :class="[
              'relative inline-flex h-6 w-11 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2',
              currentOptions.autoRecoverOn403 ? 'bg-primary-600' : 'bg-gray-200 dark:bg-dark-600'
            ]"
            @click="patchOptions({ autoRecoverOn403: !currentOptions.autoRecoverOn403 })"
          >
            <span
              :class="[
                'pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out',
                currentOptions.autoRecoverOn403 ? 'translate-x-5' : 'translate-x-0'
              ]"
            />
          </button>
        </div>

        <div
          v-if="currentOptions.autoRecoverOn403"
          class="mt-2.5 flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between"
        >
          <div class="min-w-0">
            <label :for="`${idPrefix}-bps-recovery-interval`" class="text-xs font-medium text-gray-700 dark:text-gray-300">
              {{ t('admin.accounts.bpsAccount.options.recoveryIntervalMinutes') }}
            </label>
            <p class="text-[11px] text-gray-500 dark:text-gray-400">
              {{ t('admin.accounts.bpsAccount.options.recoveryIntervalHint') }}
            </p>
          </div>
          <input
            :id="`${idPrefix}-bps-recovery-interval`"
            type="number"
            :min="OPENAI_BPS_MIN_RECOVERY_INTERVAL_MINUTES"
            :max="OPENAI_BPS_MAX_RECOVERY_INTERVAL_MINUTES"
            step="1"
            :value="currentOptions.recoveryIntervalMinutes"
            data-testid="openai-bps-recovery-interval-input"
            class="input w-full sm:w-32 text-xs"
            @input="onRecoveryIntervalInput"
          />
        </div>
      </div>

      <!-- openai_excel_bps_auto_move_on_403 + openai_excel_bps_403_target_group_id -->
      <div class="border-t border-gray-200/80 pt-3 dark:border-dark-600">
        <div class="flex items-start justify-between gap-4">
          <div class="min-w-0">
            <label
              :for="`${idPrefix}-bps-auto-move-403`"
              class="input-label mb-0 cursor-pointer"
            >
              {{ t('admin.accounts.bpsAccount.options.autoMoveOn403') }}
            </label>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.accounts.bpsAccount.options.autoMoveOn403Desc') }}
            </p>
          </div>
          <button
            :id="`${idPrefix}-bps-auto-move-403`"
            type="button"
            role="switch"
            :aria-checked="currentOptions.autoMoveOn403"
            data-testid="openai-bps-auto-move-403-toggle"
            :class="[
              'relative inline-flex h-6 w-11 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2',
              currentOptions.autoMoveOn403 ? 'bg-primary-600' : 'bg-gray-200 dark:bg-dark-600'
            ]"
            @click="patchOptions({ autoMoveOn403: !currentOptions.autoMoveOn403 })"
          >
            <span
              :class="[
                'pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out',
                currentOptions.autoMoveOn403 ? 'translate-x-5' : 'translate-x-0'
              ]"
            />
          </button>
        </div>

        <div v-if="currentOptions.autoMoveOn403" class="mt-2.5">
          <label class="mb-1 block text-xs font-medium text-gray-700 dark:text-gray-300">
            {{ t('admin.accounts.bpsAccount.options.targetGroupLabel') }}
          </label>
          <Select
            v-if="targetGroupSelectOptions.length > 0"
            :model-value="currentOptions.targetGroupId"
            :options="targetGroupSelectOptions"
            :placeholder="t('admin.accounts.bpsAccount.options.targetGroupPlaceholder')"
            data-testid="openai-bps-target-group-select"
            @update:model-value="onTargetGroupChange"
          />
          <p
            v-else
            class="rounded border border-amber-200 bg-amber-50/80 px-2.5 py-1.5 text-xs text-amber-800 dark:border-amber-800/60 dark:bg-amber-900/20 dark:text-amber-300"
            data-testid="openai-bps-target-group-empty"
          >
            {{ t('admin.accounts.bpsAccount.options.targetGroupEmptyHint') }}
          </p>
        </div>
      </div>

      <!-- openai_excel_bps_cache_creation_as_input -->
      <div class="flex items-start justify-between gap-4 border-t border-gray-200/80 pt-3 dark:border-dark-600">
        <div class="min-w-0">
          <label
            :for="`${idPrefix}-bps-cache-creation-input`"
            class="input-label mb-0 cursor-pointer"
          >
            {{ t('admin.accounts.bpsAccount.options.cacheCreationAsInput') }}
          </label>
          <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.bpsAccount.options.cacheCreationAsInputDesc') }}
          </p>
        </div>
        <button
          :id="`${idPrefix}-bps-cache-creation-input`"
          type="button"
          role="switch"
          :aria-checked="currentOptions.cacheCreationAsInput"
          data-testid="openai-bps-cache-creation-input-toggle"
          :class="[
            'relative inline-flex h-6 w-11 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2',
            currentOptions.cacheCreationAsInput ? 'bg-primary-600' : 'bg-gray-200 dark:bg-dark-600'
          ]"
          @click="patchOptions({ cacheCreationAsInput: !currentOptions.cacheCreationAsInput })"
        >
          <span
            :class="[
              'pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out',
              currentOptions.cacheCreationAsInput ? 'translate-x-5' : 'translate-x-0'
            ]"
          />
        </button>
      </div>
    </div>

    <!-- Session Proxy Pool (openai_excel_bps_session_proxy_enabled + openai_excel_bps_session_proxy_ids) -->
    <div
      class="rounded-lg border border-gray-200 bg-gray-50/60 p-3.5 dark:border-dark-600 dark:bg-dark-700/40"
      data-testid="openai-bps-session-proxy-card"
    >
      <div class="flex items-start justify-between gap-4">
        <div class="min-w-0">
          <label
            :for="`${idPrefix}-bps-session-proxy-enabled`"
            class="input-label mb-0 cursor-pointer"
          >
            {{ t('admin.accounts.bpsAccount.options.sessionProxyEnabled') }}
          </label>
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.bpsAccount.options.sessionProxyDesc') }}
          </p>
        </div>
        <button
          :id="`${idPrefix}-bps-session-proxy-enabled`"
          type="button"
          role="switch"
          :aria-checked="currentOptions.sessionProxyEnabled"
          data-testid="openai-bps-session-proxy-toggle"
          :class="[
            'relative inline-flex h-6 w-11 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2',
            currentOptions.sessionProxyEnabled ? 'bg-primary-600' : 'bg-gray-200 dark:bg-dark-600'
          ]"
          @click="patchOptions({ sessionProxyEnabled: !currentOptions.sessionProxyEnabled })"
        >
          <span
            :class="[
              'pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out',
              currentOptions.sessionProxyEnabled ? 'translate-x-5' : 'translate-x-0'
            ]"
          />
        </button>
      </div>

      <div
        v-if="currentOptions.sessionProxyEnabled || currentOptions.sessionProxyIds.length > 0"
        class="mt-3 space-y-2"
      >
        <div class="flex items-center justify-between text-xs text-gray-600 dark:text-gray-300">
          <span>
            {{
              t('admin.accounts.bpsAccount.options.sessionProxySelectedCount', {
                count: currentOptions.sessionProxyIds.length
              })
            }}
          </span>
        </div>

        <div
          v-if="activeProxies.length > 0"
          class="max-h-40 space-y-1 overflow-y-auto rounded-lg border border-gray-200 bg-white p-2 dark:border-dark-500 dark:bg-dark-700"
          data-testid="openai-bps-session-proxy-list"
        >
          <label
            v-for="proxy in activeProxies"
            :key="proxy.id"
            :data-testid="`openai-bps-session-proxy-option-${proxy.id}`"
            class="flex cursor-pointer items-center justify-between gap-2 rounded px-2 py-1.5 text-xs hover:bg-gray-50 dark:hover:bg-dark-600"
          >
            <span class="flex min-w-0 items-center gap-2">
              <input
                type="checkbox"
                class="h-3.5 w-3.5 rounded border-gray-300 text-primary-600 focus:ring-primary-500"
                :checked="currentOptions.sessionProxyIds.includes(proxy.id)"
                @change="toggleSessionProxy(proxy.id)"
              />
              <span class="truncate font-medium text-gray-900 dark:text-white">{{ proxy.name }}</span>
              <span class="truncate font-mono text-[11px] text-gray-500 dark:text-gray-400">
                {{ proxy.protocol }}://{{ proxy.host }}:{{ proxy.port }}
              </span>
            </span>
            <span
              v-if="proxy.country_code"
              class="shrink-0 rounded bg-gray-100 px-1.5 py-0.5 text-[10px] text-gray-600 dark:bg-dark-600 dark:text-gray-300"
            >
              {{ proxy.country_code }}
            </span>
          </label>
        </div>
        <div
          v-else
          class="rounded border border-amber-200 bg-amber-50/80 px-2.5 py-1.5 text-xs text-amber-800 dark:border-amber-800/60 dark:bg-amber-900/20 dark:text-amber-300"
          data-testid="openai-bps-session-proxy-no-nodes"
        >
          {{ t('admin.accounts.bpsAccount.options.sessionProxyNoActiveNodes') }}
        </div>

        <div
          v-if="currentOptions.sessionProxyEnabled && currentOptions.sessionProxyIds.length === 0"
          class="rounded border border-red-200 bg-red-50/90 px-2.5 py-1.5 text-xs font-medium text-red-700 dark:border-red-800/70 dark:bg-red-950/30 dark:text-red-300"
          role="alert"
          data-testid="openai-bps-session-proxy-error"
        >
          {{ t('admin.accounts.bpsAccount.options.sessionProxyEmptyPoolError') }}
        </div>
      </div>
    </div>

    <!-- extra.openai_bps_skip_historical_encrypted -->
    <div class="rounded-lg border border-gray-200 bg-gray-50/60 p-3.5 dark:border-dark-600 dark:bg-dark-700/40">
      <div class="flex items-start justify-between gap-4">
        <div class="min-w-0">
          <label
            :for="`${idPrefix}-bps-skip-historical-encrypted`"
            class="input-label mb-0 cursor-pointer"
          >
            {{ t('admin.accounts.bpsAccount.options.skipHistoricalEncrypted') }}
          </label>
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.bpsAccount.options.skipHistoricalEncryptedDesc') }}
          </p>
        </div>
        <button
          :id="`${idPrefix}-bps-skip-historical-encrypted`"
          type="button"
          role="switch"
          :aria-checked="currentOptions.skipHistoricalEncrypted"
          data-testid="openai-bps-skip-historical-encrypted-toggle"
          :class="[
            'relative inline-flex h-6 w-11 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2',
            currentOptions.skipHistoricalEncrypted ? 'bg-primary-600' : 'bg-gray-200 dark:bg-dark-600'
          ]"
          @click="patchOptions({ skipHistoricalEncrypted: !currentOptions.skipHistoricalEncrypted })"
        >
          <span
            :class="[
              'pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out',
              currentOptions.skipHistoricalEncrypted ? 'translate-x-5' : 'translate-x-0'
            ]"
          />
        </button>
      </div>
      <div
        class="mt-2.5 rounded-md border border-amber-200 bg-amber-50/90 px-3 py-2 text-xs leading-relaxed text-amber-800 dark:border-amber-800/60 dark:bg-amber-900/20 dark:text-amber-300"
        data-testid="openai-bps-skip-historical-encrypted-warning"
      >
        {{ t('admin.accounts.bpsAccount.options.skipHistoricalEncryptedWarning') }}
      </div>
    </div>

    <!-- extra.openai_bps_hosted_tools_policy -->
    <div class="space-y-2">
      <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div class="min-w-0">
          <label class="input-label mb-0">
            {{ t('admin.accounts.bpsAccount.options.hostedToolsPolicy') }}
          </label>
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.bpsAccount.options.hostedToolsPolicyDesc') }}
          </p>
        </div>
        <div class="w-full flex-shrink-0 sm:w-52">
          <Select
            :model-value="currentOptions.hostedToolsPolicy"
            :options="hostedToolsPolicyOptions"
            data-testid="openai-bps-hosted-tools-policy-select"
            @update:model-value="onPolicyChange"
          />
        </div>
      </div>
      <div
        v-if="currentOptions.hostedToolsPolicy === 'omit'"
        class="rounded-md border border-amber-200 bg-amber-50/90 px-3 py-2 text-xs leading-relaxed text-amber-800 dark:border-amber-800/60 dark:bg-amber-900/20 dark:text-amber-300"
        data-testid="openai-bps-hosted-tools-omit-warning"
      >
        {{ t('admin.accounts.bpsAccount.options.hostedToolsOmitLastResortWarning') }}
      </div>
    </div>

    <div
      v-if="validationErrorMessage"
      class="rounded-lg border border-red-300 bg-red-50/90 px-3 py-2 text-xs font-medium text-red-700 dark:border-red-800/70 dark:bg-red-950/30 dark:text-red-300"
      role="alert"
      data-testid="openai-bps-validation-error"
    >
      {{ validationErrorMessage }}
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import type { Group, Proxy } from '@/types'
import {
  DEFAULT_OPENAI_BPS_CHANNEL_OPTIONS,
  OPENAI_BPS_MAX_RECOVERY_INTERVAL_MINUTES,
  OPENAI_BPS_MIN_RECOVERY_INTERVAL_MINUTES,
  OPENAI_BPS_RECOMMENDED_MODELS,
  filterActiveBPSProxies,
  filterEligibleBPSTargetGroups,
  normalizeOpenAIBPSModels,
  normalizeOpenAIBPSSessionProxyIds,
  type OpenAIBPSAccountRuntimeState,
  type OpenAIBPSChannelOptions,
  type OpenAIBPSHostedToolsPolicy,
  type OpenAIBPSValidationErrorCode
} from '@/utils/openaiBPS'

export type OpenAIBPSStagedGrantState = 'none' | 'attached' | 'removed'

const props = withDefaults(
  defineProps<{
    options?: OpenAIBPSChannelOptions
    skipHistoricalEncrypted?: boolean
    hostedToolsPolicy?: OpenAIBPSHostedToolsPolicy
    idPrefix?: string
    isBpsOnly?: boolean
    hasSavedAccount?: boolean
    hasSavedIndependentGrant?: boolean
    stagedGrantState?: OpenAIBPSStagedGrantState
    refreshingGrant?: boolean
    runtimeState?: OpenAIBPSAccountRuntimeState | null
    groups?: readonly Group[]
    proxies?: readonly Proxy[]
    candidateModels?: readonly string[]
    canSyncModels?: boolean
    syncingModels?: boolean
    validationError?: OpenAIBPSValidationErrorCode | null
  }>(),
  {
    options: undefined,
    skipHistoricalEncrypted: false,
    hostedToolsPolicy: 'reject',
    idPrefix: 'account',
    isBpsOnly: true,
    hasSavedAccount: false,
    hasSavedIndependentGrant: false,
    stagedGrantState: 'none',
    refreshingGrant: false,
    runtimeState: null,
    groups: () => [],
    proxies: () => [],
    candidateModels: () => [],
    canSyncModels: false,
    syncingModels: false,
    validationError: null
  }
)

const emit = defineEmits<{
  'update:options': [value: OpenAIBPSChannelOptions]
  'update:skipHistoricalEncrypted': [value: boolean]
  'update:hostedToolsPolicy': [value: OpenAIBPSHostedToolsPolicy]
  'attach-grant': []
  'refresh-grant': []
  'remove-grant': []
  'cancel-staged-grant': []
  'sync-models': []
}>()

const { t } = useI18n()

const customBPSModel = ref('')

const currentOptions = computed<OpenAIBPSChannelOptions>(() => {
  if (props.options) return props.options
  return {
    ...DEFAULT_OPENAI_BPS_CHANNEL_OPTIONS,
    enabled: props.isBpsOnly,
    modelsMode: props.isBpsOnly ? 'all' : 'selected',
    models: [],
    sessionProxyIds: [],
    skipHistoricalEncrypted: props.skipHistoricalEncrypted,
    hostedToolsPolicy: props.hostedToolsPolicy
  }
})

const patchOptions = (patch: Partial<OpenAIBPSChannelOptions>) => {
  const next: OpenAIBPSChannelOptions = {
    ...currentOptions.value,
    ...patch
  }
  emit('update:options', next)
  if ('skipHistoricalEncrypted' in patch && patch.skipHistoricalEncrypted !== undefined) {
    emit('update:skipHistoricalEncrypted', patch.skipHistoricalEncrypted)
  }
  if ('hostedToolsPolicy' in patch && patch.hostedToolsPolicy !== undefined) {
    emit('update:hostedToolsPolicy', patch.hostedToolsPolicy)
  }
}

const effectiveHasGrant = computed(() => {
  if (props.stagedGrantState === 'attached') return true
  if (props.stagedGrantState === 'removed') return false
  return props.hasSavedIndependentGrant
})

const grantStatusBadge = computed(() => {
  if (props.stagedGrantState === 'attached') {
    return {
      label: t('admin.accounts.bpsAccount.grant.statusStagedAttach'),
      classes:
        'border-emerald-300 bg-emerald-50 text-emerald-700 dark:border-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300'
    }
  }
  if (props.stagedGrantState === 'removed') {
    return {
      label: t('admin.accounts.bpsAccount.grant.statusStagedRemove'),
      classes:
        'border-amber-300 bg-amber-50 text-amber-700 dark:border-amber-700 dark:bg-amber-900/30 dark:text-amber-300'
    }
  }
  if (props.runtimeState?.isGrantInvalid) {
    return {
      label: t('admin.accounts.bpsAccount.grant.statusGrantInvalid'),
      classes:
        'border-red-300 bg-red-50 text-red-700 dark:border-red-700 dark:bg-red-900/30 dark:text-red-300'
    }
  }
  if (props.hasSavedIndependentGrant) {
    if (currentOptions.value.enabled) {
      return {
        label: t('admin.accounts.bpsAccount.grant.statusAttachedActive'),
        classes:
          'border-teal-300 bg-teal-50 text-teal-700 dark:border-teal-700 dark:bg-teal-900/30 dark:text-teal-300'
      }
    }
    return {
      label: t('admin.accounts.bpsAccount.grant.statusAttachedStandby'),
      classes:
        'border-slate-300 bg-slate-100 text-slate-700 dark:border-slate-600 dark:bg-slate-800 dark:text-slate-300'
    }
  }
  return {
    label: t('admin.accounts.bpsAccount.grant.statusNotAttached'),
    classes:
      'border-gray-300 bg-white text-gray-600 dark:border-dark-500 dark:bg-dark-700 dark:text-gray-400'
  }
})

const mergedCandidateModels = computed(() => {
  const seen = new Set<string>()
  const result: { value: string; suggested: boolean }[] = []
  const explicitSet = new Set(normalizeOpenAIBPSModels(props.candidateModels))

  for (const model of explicitSet) {
    seen.add(model)
    result.push({ value: model, suggested: false })
  }
  for (const suggested of OPENAI_BPS_RECOMMENDED_MODELS) {
    if (seen.has(suggested)) continue
    seen.add(suggested)
    result.push({ value: suggested, suggested: true })
  }
  return result
})

const toggleBPSModel = (model: string) => {
  const trimmed = model.trim()
  if (!trimmed) return
  const exists = currentOptions.value.models.includes(trimmed)
  const nextModels = exists
    ? currentOptions.value.models.filter((m) => m !== trimmed)
    : normalizeOpenAIBPSModels([...currentOptions.value.models, trimmed])
  patchOptions({ models: nextModels })
}

const removeBPSModel = (model: string) => {
  patchOptions({
    models: currentOptions.value.models.filter((m) => m !== model)
  })
}

const addCustomBPSModel = () => {
  const trimmed = customBPSModel.value.trim()
  if (!trimmed) return
  const nextModels = normalizeOpenAIBPSModels([...currentOptions.value.models, trimmed])
  patchOptions({ models: nextModels })
  customBPSModel.value = ''
}

const prefillSuggestedModels = () => {
  const nextModels = normalizeOpenAIBPSModels([
    ...currentOptions.value.models,
    ...OPENAI_BPS_RECOMMENDED_MODELS
  ])
  patchOptions({ modelsMode: 'selected', models: nextModels })
}

const onRecoveryIntervalInput = (event: Event) => {
  const raw = (event.target as HTMLInputElement).value
  const parsed = raw.trim() === '' ? Number.NaN : Number(raw)
  patchOptions({ recoveryIntervalMinutes: parsed })
}

const eligibleTargetGroups = computed(() => filterEligibleBPSTargetGroups(props.groups))

const targetGroupSelectOptions = computed(() =>
  eligibleTargetGroups.value.map((group) => ({
    value: group.id,
    label: `${group.name} (#${group.id} · ${group.platform})`
  }))
)

const onTargetGroupChange = (value: string | number | boolean | null) => {
  const num = typeof value === 'number' ? value : typeof value === 'string' && value !== '' ? Number(value) : null
  patchOptions({ targetGroupId: num != null && Number.isInteger(num) && num > 0 ? num : null })
}

const activeProxies = computed(() => filterActiveBPSProxies(props.proxies))

const toggleSessionProxy = (proxyId: number) => {
  const current = currentOptions.value.sessionProxyIds
  const next = current.includes(proxyId)
    ? current.filter((id) => id !== proxyId)
    : normalizeOpenAIBPSSessionProxyIds([...current, proxyId])
  patchOptions({ sessionProxyIds: next })
}

const hostedToolsPolicyOptions = computed(() => [
  {
    value: 'reject' as OpenAIBPSHostedToolsPolicy,
    label: t('admin.accounts.bpsAccount.options.hostedToolsReject')
  },
  {
    value: 'omit' as OpenAIBPSHostedToolsPolicy,
    label: t('admin.accounts.bpsAccount.options.hostedToolsOmit')
  }
])

const onPolicyChange = (value: string | number | boolean | null) => {
  patchOptions({ hostedToolsPolicy: value === 'omit' ? 'omit' : 'reject' })
}

const validationErrorMessage = computed(() => {
  if (!props.validationError) return ''
  return t(`admin.accounts.bpsAccount.options.validation.${props.validationError}`)
})
</script>
