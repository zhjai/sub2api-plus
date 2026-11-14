<template>
  <div class="space-y-1.5" data-testid="bps-local-usage">
    <!-- Authoritative upstream quota windows (rendered ONLY when actual window utilization exists; unknown is NEVER 0%) -->
    <div
      v-if="hasFiveHourWindow || hasSevenDayWindow"
      class="space-y-1"
      data-testid="bps-authoritative-quota"
    >
      <UsageProgressBar
        v-if="hasFiveHourWindow && usage?.five_hour"
        data-testid="bps-quota-5h"
        label="5h"
        :utilization="usage.five_hour.utilization"
        :resets-at="usage.five_hour.resets_at"
        :show-now-when-idle="false"
        color="indigo"
      />
      <UsageProgressBar
        v-if="hasSevenDayWindow && usage?.seven_day"
        data-testid="bps-quota-7d"
        label="7d"
        :utilization="usage.seven_day.utilization"
        :resets-at="usage.seven_day.resets_at"
        :show-now-when-idle="false"
        color="emerald"
      />
    </div>
    <div
      v-else-if="showUnknownQuotaNotice"
      class="text-[10px] text-gray-500 dark:text-gray-400"
      data-testid="bps-quota-unknown"
    >
      {{ t('admin.accounts.bpsAccount.usage.quotaUnknown') }}
    </div>

    <!-- Upstream quota source & snapshot timestamp -->
    <div
      v-if="quotaSourceLabel || quotaUpdatedLabel"
      class="flex flex-wrap items-center gap-x-1.5 gap-y-0.5 text-[9px] text-gray-500 dark:text-gray-400"
      data-testid="bps-quota-meta"
    >
      <span
        v-if="quotaSourceLabel"
        data-testid="bps-quota-source"
        class="rounded bg-gray-100 px-1.5 py-0.5 dark:bg-gray-800"
        :title="t('admin.accounts.bpsAccount.usage.sourceDisclaimer')"
      >
        {{ quotaSourceLabel }}
      </span>
      <span v-if="quotaUpdatedLabel" data-testid="bps-quota-updated-at">
        {{ quotaUpdatedLabel }}
      </span>
    </div>
    <p
      v-if="hasFiveHourWindow || hasSevenDayWindow"
      class="max-w-[240px] text-[9px] leading-snug text-gray-400 dark:text-gray-500"
      data-testid="bps-quota-scope-disclaimer"
    >
      {{ t('admin.accounts.bpsAccount.usage.sourceDisclaimer') }}
    </p>

    <!-- Read-only upstream quota refresh error (shown alongside retained stale snapshot and local stats) -->
    <div
      v-if="quotaRefreshErrorMessage"
      class="max-w-[240px] rounded border border-amber-200 bg-amber-50/80 px-1.5 py-0.5 text-[10px] leading-snug text-amber-800 dark:border-amber-800/60 dark:bg-amber-900/20 dark:text-amber-300"
      role="status"
      data-testid="bps-quota-refresh-error"
      :title="quotaRefreshErrorMessage"
    >
      {{ quotaRefreshErrorMessage }}
    </div>

    <!-- Independent local gateway statistics -->
    <div class="flex items-center gap-1.5">
      <span
        class="text-[10px] font-medium text-gray-600 dark:text-gray-300"
        :title="t('admin.accounts.bpsAccount.usage.hint')"
      >
        {{ t('admin.accounts.bpsAccount.usage.title') }}
      </span>
      <button
        type="button"
        data-testid="bps-local-usage-refresh"
        class="inline-flex items-center gap-0.5 rounded px-1.5 py-0.5 text-[10px] font-medium text-blue-600 transition-colors hover:bg-blue-50 disabled:cursor-not-allowed disabled:opacity-50 dark:text-blue-400 dark:hover:bg-blue-900/30"
        :disabled="busy"
        @click="emit('refresh')"
      >
        <svg
          class="h-2.5 w-2.5"
          :class="{ 'animate-spin': refreshing }"
          fill="none"
          stroke="currentColor"
          viewBox="0 0 24 24"
          aria-hidden="true"
        >
          <path
            stroke-linecap="round"
            stroke-linejoin="round"
            stroke-width="2"
            d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"
          />
        </svg>
        {{ refreshing ? t('admin.accounts.bpsAccount.usage.refreshing') : t('admin.accounts.bpsAccount.usage.refresh') }}
      </button>
    </div>

    <div v-if="loading && !windows.length" class="flex items-center gap-1" aria-hidden="true">
      <div class="h-3 w-10 animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
      <div class="h-3 w-8 animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
      <div class="h-3 w-12 animate-pulse rounded bg-gray-200 dark:bg-gray-700"></div>
    </div>

    <template v-else>
      <div
        v-for="row in windows"
        :key="row.key"
        class="flex items-center gap-1.5 text-[9px] text-gray-500 dark:text-gray-400"
        :data-testid="`bps-local-usage-${row.key}`"
      >
        <span class="w-[18px] shrink-0 text-[10px] font-medium text-gray-500 dark:text-gray-400">{{ row.label }}</span>
        <span
          v-if="row.stats.requests <= 0"
          class="rounded bg-gray-100 px-1.5 py-0.5 dark:bg-gray-800"
        >
          {{ t('admin.accounts.bpsAccount.usage.noRequests') }}
        </span>
        <template v-else>
          <span class="rounded bg-gray-100 px-1.5 py-0.5 dark:bg-gray-800">
            {{ formatCompactNumber(row.stats.requests, { allowBillions: false }) }} req
          </span>
          <span class="rounded bg-gray-100 px-1.5 py-0.5 dark:bg-gray-800">
            {{ formatCompactNumber(row.stats.tokens) }}
          </span>
          <span class="rounded bg-gray-100 px-1.5 py-0.5 dark:bg-gray-800" :title="t('usage.accountBilled')">
            A ${{ formatCost(row.stats.cost) }}
          </span>
          <span
            v-if="row.stats.user_cost != null"
            class="rounded bg-gray-100 px-1.5 py-0.5 dark:bg-gray-800"
            :title="t('usage.userBilled')"
          >
            U ${{ formatCost(row.stats.user_cost) }}
          </span>
        </template>
      </div>
      <div
        v-if="!windows.length && !error"
        class="text-[10px] text-gray-400 dark:text-gray-500"
        data-testid="bps-local-usage-empty"
      >
        {{ t('admin.accounts.bpsAccount.usage.empty') }}
      </div>
    </template>

    <div
      v-if="error"
      class="max-w-[220px] text-[10px] text-red-600 dark:text-red-400"
      role="alert"
      data-testid="bps-local-usage-error"
      :title="error"
    >
      {{ error }}
    </div>
    <div
      v-else-if="refreshedLabel"
      class="text-[10px] text-emerald-600 dark:text-emerald-400"
      aria-live="polite"
      data-testid="bps-local-usage-refreshed"
    >
      {{ refreshedLabel }}
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { AccountUsageInfo, UsageProgress, WindowStats } from '@/types'
import { formatCompactNumber } from '@/utils/format'
import UsageProgressBar from './UsageProgressBar.vue'

const props = withDefaults(
  defineProps<{
    usage: AccountUsageInfo | null
    loading?: boolean
    refreshing?: boolean
    error?: string | null
    refreshedAt?: Date | null
  }>(),
  {
    loading: false,
    refreshing: false,
    error: null,
    refreshedAt: null
  }
)

const emit = defineEmits<{ refresh: [] }>()

const { t } = useI18n()

const busy = computed(() => props.loading || props.refreshing)

function hasAuthoritativeWindow(window: UsageProgress | null | undefined): boolean {
  return window != null && typeof window.utilization === 'number' && Number.isFinite(window.utilization)
}

const hasFiveHourWindow = computed(() => hasAuthoritativeWindow(props.usage?.five_hour))
const hasSevenDayWindow = computed(() => hasAuthoritativeWindow(props.usage?.seven_day))

const showUnknownQuotaNotice = computed(() => {
  if (!props.usage || props.loading) return false
  if (hasFiveHourWindow.value || hasSevenDayWindow.value) return false
  return Boolean(
    props.usage.quota_source ||
      props.usage.quota_updated_at ||
      props.usage.quota_refresh_error
  )
})

const quotaSourceLabel = computed(() => {
  const source = props.usage?.quota_source
  if (source === 'chatgpt_wham_usage') {
    return t('admin.accounts.bpsAccount.usage.sourceWham')
  }
  if (source === 'account_codex_snapshot') {
    return t('admin.accounts.bpsAccount.usage.sourceSnapshot')
  }
  return ''
})

const quotaUpdatedLabel = computed(() => {
  const raw = props.usage?.quota_updated_at
  if (!raw || typeof raw !== 'string') return ''
  const parsed = new Date(raw)
  if (Number.isNaN(parsed.getTime())) return ''
  const timeText = parsed.toLocaleString(undefined, {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit'
  })
  return t('admin.accounts.bpsAccount.usage.quotaUpdatedAt', { time: timeText })
})

const quotaRefreshErrorMessage = computed(() => {
  const raw = props.usage?.quota_refresh_error?.trim()
  if (!raw) return ''
  return t('admin.accounts.bpsAccount.usage.quotaRefreshError', { error: raw })
})

const windows = computed(() => {
  const local = props.usage?.bps_local_usage
  if (!local) return []
  const rows: { key: '5h' | '7d'; label: string; stats: WindowStats }[] = []
  if (local.five_hour) rows.push({ key: '5h', label: '5h', stats: local.five_hour })
  if (local.seven_day) rows.push({ key: '7d', label: '7d', stats: local.seven_day })
  return rows
})

const formatCost = (value: number | null | undefined) => {
  const amount = typeof value === 'number' && Number.isFinite(value) ? value : 0
  return amount.toFixed(2)
}

const refreshedLabel = computed(() => {
  if (!props.refreshedAt) return ''
  return t('admin.accounts.bpsAccount.usage.refreshedAt', {
    time: props.refreshedAt.toLocaleTimeString()
  })
})
</script>
