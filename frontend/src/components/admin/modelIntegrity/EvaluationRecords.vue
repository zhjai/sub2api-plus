<template>
  <div class="rec">
    <p class="rec-note">{{ t('admin.modelIntegrity.scheduling.records.evaluationsHint') }}</p>
    <p v-if="inProgress" class="rec-progress" role="status" data-testid="record-in-progress">
      <Icon name="refresh" size="sm" class="motion-safe:animate-spin" />{{ t('admin.modelIntegrity.scheduling.records.inProgress') }}
    </p>
    <div v-if="error" class="rec-failed" role="alert" data-testid="record-error">
      <p class="font-medium">{{ t('admin.modelIntegrity.scheduling.records.failed', { reason: error.message || error.code }) }}</p>
      <p>{{ current ? t('admin.modelIntegrity.scheduling.evaluation.failedKept') : t('admin.modelIntegrity.scheduling.records.failedNoPrevious') }}</p>
    </div>

    <p v-if="!records.length" class="rec-empty" data-testid="record-empty">{{ t('admin.modelIntegrity.scheduling.records.empty') }}</p>
    <ol v-else class="rec-list">
      <li v-for="record in records" :key="record.summary.evaluation_id" class="rec-row" data-testid="evaluation-record" :data-evaluation-id="record.summary.evaluation_id">
        <div class="rec-head">
          <span class="rec-tag" :class="record.current ? 'rec-tag-current' : 'rec-tag-previous'">
            {{ record.current ? t('admin.modelIntegrity.scheduling.records.current') : t('admin.modelIntegrity.scheduling.records.previous') }}
          </span>
          <time class="rec-time" :datetime="record.summary.published_at">{{ formatDateTime(record.summary.published_at || record.summary.evaluated_at) }}</time>
          <span class="rec-trigger">{{ t(`admin.modelIntegrity.scheduling.records.trigger.${rankingTriggerKey(record.summary.trigger)}`) }}</span>
        </div>
        <dl class="rec-facts">
          <div><dt>{{ t('admin.modelIntegrity.scheduling.records.accounts') }}</dt><dd data-testid="record-accounts">{{ record.summary.account_count }}</dd></div>
          <div><dt>{{ t('admin.modelIntegrity.scheduling.records.revision') }}</dt><dd>{{ record.summary.config_revision }}</dd></div>
          <div><dt>{{ t('admin.modelIntegrity.scheduling.records.inputsUntil') }}</dt><dd>{{ formatDateTime(record.summary.evaluated_at) }}</dd></div>
          <div><dt>{{ t('admin.modelIntegrity.scheduling.records.coverage') }}</dt><dd>{{ t(`admin.modelIntegrity.scheduling.rank.coverage.${coverageKey(record.summary)}`) }}</dd></div>
          <div>
            <dt>{{ t('admin.modelIntegrity.scheduling.records.applied') }}</dt>
            <dd :class="record.summary.effects_enabled ? '' : 'rec-off'" data-testid="record-applied">
              {{ record.summary.effects_enabled ? t('admin.modelIntegrity.scheduling.records.appliedYes') : t('admin.modelIntegrity.scheduling.records.appliedNo') }}
            </dd>
          </div>
        </dl>
        <p v-if="record.current && record.summary.next_evaluation_at" class="rec-next">
          {{ t('admin.modelIntegrity.scheduling.records.next', { time: formatDateTime(record.summary.next_evaluation_at) }) }}
        </p>
      </li>
    </ol>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import type { OpenAIEvalRankingSummary, RankingError } from '@/api/admin/accounts'
import { rankingTriggerKey } from '@/views/admin/modelIntegrity/modelIntegrity'

/**
 * Completed evaluations the server keeps (the current one and the one before
 * it). A record exists whether or not any real request has arrived; request
 * routing has its own tab.
 */
const props = defineProps<{
  current: OpenAIEvalRankingSummary | null
  previous: OpenAIEvalRankingSummary | null
  error: RankingError | null
  inProgress: boolean
}>()

const { t } = useI18n()

const records = computed(() => {
  const list: { summary: OpenAIEvalRankingSummary; current: boolean }[] = []
  if (props.current) list.push({ summary: props.current, current: true })
  if (props.previous && props.previous.evaluation_id !== props.current?.evaluation_id) list.push({ summary: props.previous, current: false })
  return list
})

function coverageKey(summary: OpenAIEvalRankingSummary) {
  const status = summary.coverage?.status
  return status === 'partial' || status === 'empty' ? status : 'complete'
}

function formatDateTime(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}
</script>

<style scoped>
.rec { @apply space-y-3; }
.rec-note { @apply max-w-[80ch] text-xs leading-relaxed text-gray-500 dark:text-gray-400; }
.rec-progress { @apply flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300; }
.rec-failed { @apply space-y-1 rounded-md bg-rose-50 px-3 py-2 text-xs leading-relaxed text-rose-900 dark:bg-rose-950/30 dark:text-rose-200; }
.rec-empty { @apply rounded-lg border border-dashed border-gray-300 px-4 py-8 text-center text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400; }
.rec-list { @apply divide-y divide-gray-100 rounded-lg border border-gray-200 dark:divide-dark-700 dark:border-dark-700; }
.rec-row { @apply space-y-2 px-4 py-3; }
.rec-head { @apply flex flex-wrap items-baseline gap-x-3 gap-y-1; }
.rec-tag { @apply rounded px-1.5 py-0.5 text-xs font-medium; }
.rec-tag-current { @apply bg-primary-100 text-primary-800 dark:bg-primary-900/50 dark:text-primary-200; }
.rec-tag-previous { @apply bg-gray-100 text-gray-700 dark:bg-dark-700 dark:text-gray-300; }
.rec-time { @apply text-sm font-medium tabular-nums text-gray-900 dark:text-white; }
.rec-trigger { @apply text-xs text-gray-500 dark:text-gray-400; }
.rec-facts { @apply grid grid-cols-2 gap-x-6 gap-y-2 text-xs sm:grid-cols-5; }
.rec-facts dt { @apply text-gray-500 dark:text-gray-400; }
.rec-facts dd { @apply mt-0.5 tabular-nums text-gray-800 dark:text-gray-200; }
.rec-off { @apply text-amber-800 dark:text-amber-300; }
.rec-next { @apply text-xs tabular-nums text-gray-500 dark:text-gray-400; }
</style>
