<template>
  <BaseDialog :show="run !== null" :title="t('admin.modelIntegrity.tests.detail.title')" width="normal" @close="emit('close')">
    <div v-if="run" class="space-y-4 text-sm">
      <div>
        <p class="font-medium text-gray-900 dark:text-white">{{ target }}</p>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t(`admin.modelIntegrity.tests.types.${run.test_type}.name`) }} · {{ formatTime(run.finished_at || run.started_at) }}</p>
      </div>
      <div class="flex items-start gap-2">
        <span class="tone" :class="`tone-${resultTone(run.status)}`" data-testid="detail-status">{{ runStatusLabel(t, run) }}</span>
        <p class="text-gray-700 dark:text-gray-300">{{ runExplanation(t, run, catalog) }}</p>
      </div>
      <p v-if="isAttributionRun(run)" class="attribution-note" data-testid="attribution-note">{{ t('admin.modelIntegrity.tests.detail.attributionNote') }}</p>

      <dl class="facts">
        <div><dt>{{ t('admin.modelIntegrity.tests.detail.samples') }}</dt><dd>{{ run.outcome.sample_count }}/{{ run.outcome.expected_count }}</dd></div>
        <div><dt>{{ t('admin.modelIntegrity.tests.detail.requests') }}</dt><dd>{{ run.request_count }}</dd></div>
        <div><dt>{{ t('admin.modelIntegrity.tests.detail.tokens') }}</dt><dd>{{ run.input_tokens.toLocaleString() }} / {{ run.output_tokens.toLocaleString() }}</dd></div>
        <div><dt>{{ t('admin.modelIntegrity.tests.detail.duration') }}</dt><dd>{{ formatDuration(run.duration_ms) }}</dd></div>
        <div><dt>{{ t('admin.modelIntegrity.tests.detail.cost') }}</dt><dd>{{ run.cost_estimate_usd == null ? t('admin.modelIntegrity.tests.costUnknown') : `$${run.cost_estimate_usd.toFixed(4)}` }}</dd></div>
        <div v-if="run.upstream_model"><dt>{{ t('admin.modelIntegrity.tests.detail.upstreamModel') }}</dt><dd>{{ run.upstream_model }}</dd></div>
        <div v-if="run.outcome.fingerprint?.nearest_model"><dt>{{ t('admin.modelIntegrity.tests.detail.nearestModel') }}</dt><dd class="break-all">{{ run.outcome.fingerprint.nearest_model }}</dd></div>
        <div v-if="run.baseline_version"><dt>{{ t('admin.modelIntegrity.tests.detail.baseline') }}</dt><dd class="break-all">{{ run.baseline_version }}</dd></div>
      </dl>

      <p v-if="run.outcome.fingerprint && run.outcome.fingerprint.mean_jsd != null" class="text-xs text-gray-600 dark:text-gray-300">
        {{ t('admin.modelIntegrity.tests.detail.fingerprintMetric', { jsd: formatMetric(run.outcome.fingerprint.mean_jsd), p: formatMetric(run.outcome.fingerprint.p_value) }) }}
      </p>
      <p v-if="run.outcome.state_probe" class="text-xs text-gray-600 dark:text-gray-300">
        {{ t('admin.modelIntegrity.tests.detail.stateProbeMetric', { mint: run.outcome.state_probe.mint_status ?? '—', cont: run.outcome.state_probe.continue_status ?? '—', ticket: run.outcome.state_probe.new_ticket ? t('admin.modelIntegrity.tests.detail.newTicket') : t('admin.modelIntegrity.tests.detail.sameTicket') }) }}
      </p>
      <div v-if="run.outcome.modeltrace?.candidates?.length">
        <p class="input-label">{{ t('admin.modelIntegrity.tests.detail.candidates') }}</p>
        <ul class="mt-1 space-y-1">
          <li v-for="candidate in run.outcome.modeltrace.candidates.slice(0, 5)" :key="candidate.model" class="flex items-center gap-3 text-xs">
            <span class="w-40 truncate text-gray-700 dark:text-gray-300">{{ candidate.display_name || candidate.model }}</span>
            <span class="h-1.5 flex-1 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700" aria-hidden="true"><span class="block h-full bg-primary-600" :style="{ width: `${Math.round(candidate.probability * 100)}%` }" /></span>
            <span class="w-10 text-right tabular-nums text-gray-500">{{ candidate.probability.toFixed(2) }}</span>
          </li>
        </ul>
      </div>

      <details v-if="run.error || run.outcome.reason" class="text-xs text-gray-500 dark:text-gray-400">
        <summary class="cursor-pointer">{{ t('admin.modelIntegrity.tests.detail.technical') }}</summary>
        <code class="mt-2 block whitespace-pre-wrap break-all font-mono">{{ [run.outcome.reason, run.error].filter(Boolean).join('\n') }}</code>
      </details>
    </div>
    <template #footer>
      <div class="flex justify-end">
        <button type="button" class="btn btn-secondary" @click="emit('close')">{{ t('admin.modelIntegrity.common.close') }}</button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import type { OpenAIEvalModelCatalog, OpenAIEvalRun } from '@/api/admin/accounts'
import { isAttributionRun, resultTone } from '@/views/admin/modelIntegrity/modelIntegrity'
import { runExplanation, runStatusLabel } from '@/views/admin/modelIntegrity/runText'

defineProps<{
  run: OpenAIEvalRun | null
  target: string
  catalog: OpenAIEvalModelCatalog | null
}>()
const emit = defineEmits<{ (e: 'close'): void }>()
const { t } = useI18n()

const formatMetric = (value?: number | null) => (value == null ? '—' : value.toFixed(4))
const formatDuration = (ms: number) => (ms >= 60_000 ? `${(ms / 60_000).toFixed(1)} min` : `${(ms / 1000).toFixed(1)} s`)
const formatTime = (value: string) => {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}
</script>

<style scoped>
.facts { @apply grid grid-cols-2 gap-x-6 gap-y-2 rounded-lg bg-gray-50 p-3 text-xs dark:bg-dark-900/50; }
.facts dt { @apply text-gray-500 dark:text-gray-400; }
.facts dd { @apply font-medium tabular-nums text-gray-800 dark:text-gray-200; }
.tone { @apply inline-flex shrink-0 whitespace-nowrap rounded px-1.5 py-0.5 text-xs font-medium; }
.tone-ok { @apply bg-emerald-50 text-emerald-800 dark:bg-emerald-950/40 dark:text-emerald-300; }
.tone-likely { @apply border border-dashed border-emerald-400 text-emerald-800 dark:border-emerald-600 dark:text-emerald-300; }
.attribution-note { @apply border-l-2 border-gray-300 pl-3 text-xs leading-relaxed text-gray-600 dark:border-dark-500 dark:text-gray-400; }
.tone-attention { @apply bg-amber-100 text-amber-900 dark:bg-amber-900/40 dark:text-amber-200; }
.tone-neutral { @apply bg-gray-100 text-gray-700 dark:bg-dark-700 dark:text-gray-300; }
.tone-running { @apply bg-sky-50 text-sky-800 dark:bg-sky-950/40 dark:text-sky-300; }
</style>
