<template>
  <div class="space-y-1" data-testid="bps-local-usage">
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
import type { AccountUsageInfo, WindowStats } from '@/types'
import { formatCompactNumber } from '@/utils/format'

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
