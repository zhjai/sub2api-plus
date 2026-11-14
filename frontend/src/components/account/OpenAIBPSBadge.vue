<template>
  <span
    v-if="badgeMeta"
    data-testid="account-bps-badge"
    :data-bps-grant-kind="runtimeState.grantKind"
    :data-bps-state="badgeMeta.state"
    :class="[
      'inline-flex items-center rounded border px-1.5 py-0.5 text-[10px] font-semibold leading-4',
      badgeMeta.classes
    ]"
    :title="badgeMeta.tooltip"
  >
    {{ badgeMeta.label }}
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { inspectOpenAIBPSAccountState } from '@/utils/openaiBPS'
import type { OpenAIBPSAccountLike } from '@/utils/openaiOAuthClient'

const props = defineProps<{
  account: OpenAIBPSAccountLike | null | undefined
}>()

const { t } = useI18n()

const runtimeState = computed(() => inspectOpenAIBPSAccountState(props.account))

const badgeMeta = computed(() => {
  const s = runtimeState.value

  if (s.isBPSOnly) {
    if (s.isGrantInvalid) {
      return {
        state: 'bps_only_grant_invalid',
        label: t('admin.accounts.bpsAccount.badges.bpsOnlyGrantInvalid'),
        tooltip: t('admin.accounts.bpsAccount.badges.tooltipGrantInvalid'),
        classes:
          'border-red-200 bg-red-50 text-red-700 dark:border-red-800/60 dark:bg-red-900/30 dark:text-red-300'
      }
    }
    if (s.is403Disabled) {
      return {
        state: 'bps_only_403',
        label: t('admin.accounts.bpsAccount.badges.bpsOnly403'),
        tooltip: t('admin.accounts.bpsAccount.badges.tooltip403Disabled'),
        classes:
          'border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-800/60 dark:bg-amber-900/30 dark:text-amber-300'
      }
    }
    if (s.isPrimaryTokenExpired) {
      return {
        state: 'bps_only_token_expired',
        label: t('admin.accounts.bpsAccount.badges.bpsOnlyTokenExpired'),
        tooltip: t('admin.accounts.bpsAccount.badges.tooltipTokenExpired'),
        classes:
          'border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-800/60 dark:bg-amber-900/30 dark:text-amber-300'
      }
    }
    if (!s.routeRequested) {
      return {
        state: 'bps_only_disabled',
        label: t('admin.accounts.bpsAccount.badges.bpsOnlyDisabled'),
        tooltip: t('admin.accounts.bpsAccount.badges.tooltipBpsOnlyDisabled'),
        classes:
          'border-gray-200 bg-gray-100 text-gray-600 dark:border-dark-500 dark:bg-dark-700 dark:text-gray-300'
      }
    }
    return {
      state: 'bps_only_active',
      label: t('admin.accounts.bpsAccount.badge'),
      tooltip: t('admin.accounts.bpsAccount.badgeTitle'),
      classes:
        'border-emerald-200 bg-emerald-50 text-emerald-700 dark:border-emerald-800/60 dark:bg-emerald-900/30 dark:text-emerald-300'
    }
  }

  if (s.hasIndependentGrant) {
    if (s.isGrantInvalid) {
      return {
        state: 'dual_grant_invalid',
        label: t('admin.accounts.bpsAccount.badges.dualGrantInvalid'),
        tooltip: t('admin.accounts.bpsAccount.badges.tooltipGrantInvalid'),
        classes:
          'border-red-200 bg-red-50 text-red-700 dark:border-red-800/60 dark:bg-red-900/30 dark:text-red-300'
      }
    }
    if (s.is403Disabled) {
      return {
        state: 'dual_403',
        label: t('admin.accounts.bpsAccount.badges.dual403'),
        tooltip: t('admin.accounts.bpsAccount.badges.tooltip403Disabled'),
        classes:
          'border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-800/60 dark:bg-amber-900/30 dark:text-amber-300'
      }
    }
    if (s.routeRequested) {
      return {
        state: 'dual_active',
        label: t('admin.accounts.bpsAccount.badges.dualActive'),
        tooltip: t('admin.accounts.bpsAccount.badges.tooltipDualActive'),
        classes:
          'border-teal-200 bg-teal-50 text-teal-700 dark:border-teal-800/60 dark:bg-teal-900/30 dark:text-teal-300'
      }
    }
    return {
      state: 'dual_standby',
      label: t('admin.accounts.bpsAccount.badges.dualStandby'),
      tooltip: t('admin.accounts.bpsAccount.badges.tooltipDualStandby'),
      classes:
        'border-slate-200 bg-slate-100 text-slate-600 dark:border-slate-700 dark:bg-slate-800/60 dark:text-slate-300'
    }
  }

  if (s.grantKind === 'native_only' && s.routeRequested) {
    return {
      state: 'dual_missing_grant',
      label: t('admin.accounts.bpsAccount.badges.dualMissingGrant'),
      tooltip: t('admin.accounts.bpsAccount.badges.tooltipMissingGrant'),
      classes:
        'border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-800/60 dark:bg-amber-900/30 dark:text-amber-300'
    }
  }

  return null
})
</script>
