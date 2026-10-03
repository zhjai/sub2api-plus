<template>
  <BaseDialog :show="show" :title="title" width="normal" @close="emit('close')">
    <form id="bps-account-form" class="space-y-5" data-testid="bps-dialog" @submit.prevent="submit">
      <label v-if="isNew" class="flex flex-col gap-1">
        <span class="input-label">{{ t('admin.modelIntegrity.scheduling.bps.dialog.account') }}</span>
        <select v-model.number="draft.account_id" class="input" required data-testid="bps-account">
          <option :value="0" disabled>{{ t('admin.modelIntegrity.scheduling.bps.dialog.pickAccount') }}</option>
          <option v-for="account in candidates" :key="account.id" :value="account.id">{{ account.name }} #{{ account.id }}</option>
        </select>
        <span v-if="!candidates.length" class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.modelIntegrity.scheduling.bps.dialog.noCandidates') }}</span>
      </label>

      <div v-else class="bps-status" :class="`bps-status-${lane}`" data-testid="bps-dialog-status">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <span class="lane" :class="`lane-${lane}`">{{ t(`admin.modelIntegrity.scheduling.bps.state.${lane}`) }}</span>
          <button
            v-if="canReset"
            type="button"
            class="btn btn-secondary btn-sm"
            :disabled="resetting"
            data-testid="bps-dialog-reset"
            @click="emit('reset')"
          >{{ t('admin.modelIntegrity.scheduling.bps.reset') }}</button>
        </div>
        <p v-if="lockText" class="mt-2 text-sm text-rose-700 dark:text-rose-300">{{ lockText }}</p>
        <p class="mt-2 text-xs tabular-nums text-gray-600 dark:text-gray-400">
          {{ t('admin.modelIntegrity.scheduling.bps.counters', {
            degraded: item?.degraded_streak ?? 0, failure: item?.failure_threshold ?? draft.failure_threshold,
            healthy: item?.healthy_streak ?? 0, recovery: item?.recovery_threshold ?? draft.recovery_threshold
          }) }}
          <template v-if="item?.updated_at"> · {{ t('admin.modelIntegrity.scheduling.bps.updatedAt', { time: formatTime(item.updated_at) }) }}</template>
        </p>
      </div>

      <fieldset class="space-y-2">
        <legend class="input-label">{{ t('admin.modelIntegrity.scheduling.bps.dialog.mode') }}</legend>
        <div class="grid gap-2 sm:grid-cols-3">
          <label v-for="mode in BPS_MODES" :key="mode" class="mode-option" :class="{ 'mode-option-active': draft.mode === mode }">
            <input v-model="draft.mode" type="radio" name="bps-mode" :value="mode" class="sr-only" :data-testid="`bps-mode-${mode}`" />
            <span class="block text-sm font-medium text-gray-900 dark:text-white">{{ t(`admin.modelIntegrity.scheduling.bps.modes.${mode}`) }}</span>
            <span class="mt-0.5 block text-xs leading-relaxed text-gray-500 dark:text-gray-400">{{ t(`admin.modelIntegrity.scheduling.bps.modeHints.${mode}`) }}</span>
          </label>
        </div>
        <p v-if="draft.mode === 'auto' && !autoEnabled" class="text-xs text-amber-700 dark:text-amber-300">{{ t('admin.modelIntegrity.scheduling.bps.warnings.masterOff') }}</p>
      </fieldset>

      <label class="flex flex-col gap-1">
        <span class="input-label">{{ t('admin.modelIntegrity.scheduling.bps.dialog.probeModel') }}</span>
        <select v-model="draft.probe_model" class="input" data-testid="bps-probe-model">
          <option value="">{{ t('admin.modelIntegrity.scheduling.bps.dialog.defaultModel') }}</option>
          <option v-for="model in catalog?.items || []" :key="model.id" :value="model.id">{{ model.display_name || model.id }}</option>
        </select>
        <span class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.modelIntegrity.scheduling.bps.dialog.probeModelHint') }}</span>
      </label>

      <fieldset class="grid gap-3 sm:grid-cols-3">
        <legend class="sr-only">{{ t('admin.modelIntegrity.scheduling.bps.dialog.thresholds') }}</legend>
        <label class="flex flex-col gap-1">
          <span class="input-label">{{ t('admin.modelIntegrity.scheduling.bps.dialog.failure') }}</span>
          <input v-model.number="draft.failure_threshold" type="number" class="input tabular-nums" :min="BPS_THRESHOLD_MIN" :max="BPS_THRESHOLD_MAX" step="1" required data-testid="bps-failure" />
        </label>
        <label class="flex flex-col gap-1">
          <span class="input-label">{{ t('admin.modelIntegrity.scheduling.bps.dialog.recovery') }}</span>
          <input v-model.number="draft.recovery_threshold" type="number" class="input tabular-nums" :min="BPS_THRESHOLD_MIN" :max="BPS_THRESHOLD_MAX" step="1" required data-testid="bps-recovery" />
        </label>
        <label class="flex flex-col gap-1">
          <span class="input-label">{{ t('admin.modelIntegrity.scheduling.bps.dialog.interval') }}</span>
          <select v-model="intervalChoice" class="input" data-testid="bps-interval">
            <option v-for="seconds in BPS_INTERVALS" :key="seconds" :value="seconds">{{ intervalText(seconds) }}</option>
            <option :value="CUSTOM_INTERVAL">{{ t('admin.modelIntegrity.scheduling.bps.interval.customOption') }}</option>
          </select>
        </label>
        <label v-if="customIntervalSelected" class="flex flex-col gap-1">
          <span class="input-label">{{ t('admin.modelIntegrity.scheduling.bps.interval.customMinutes', { max: MAX_INTERVAL_MINUTES.toLocaleString() }) }}</span>
          <input v-model.number="customIntervalMinutes" type="number" min="5" :max="MAX_INTERVAL_MINUTES" step="1" class="input tabular-nums" data-testid="bps-custom-interval" required />
        </label>
      </fieldset>
      <p class="-mt-2 text-xs text-gray-500 dark:text-gray-400">
        {{ draft.mode === 'auto'
          ? t('admin.modelIntegrity.scheduling.bps.ruleFor', { failure: draft.failure_threshold || '?', recovery: draft.recovery_threshold || '?' })
          : t('admin.modelIntegrity.scheduling.bps.dialog.thresholdsManual') }}
      </p>

      <section v-if="!isNew" class="space-y-2" aria-labelledby="bps-history-title">
        <h4 id="bps-history-title" class="input-label">{{ t('admin.modelIntegrity.scheduling.bps.history.title') }}</h4>
        <p v-if="historyLoading" class="text-xs text-gray-500" role="status">{{ t('common.loading') }}</p>
        <p v-else-if="!history.length" class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.modelIntegrity.scheduling.bps.history.empty') }}</p>
        <ol v-else class="history" data-testid="bps-history">
          <li v-for="entry in history" :key="entry.key" class="history-row">
            <time class="tabular-nums text-gray-500 dark:text-gray-400" :datetime="entry.at">{{ formatTime(entry.at) }}</time>
            <span class="font-medium" :class="entry.tone">{{ entry.label }}</span>
            <span class="min-w-0 truncate text-gray-500 dark:text-gray-400">{{ entry.detail }}</span>
          </li>
        </ol>
      </section>
    </form>

    <template #footer>
      <div class="flex flex-wrap items-center justify-between gap-2">
        <button v-if="!isNew" type="button" class="btn btn-secondary text-rose-700 dark:text-rose-300" data-testid="bps-remove" @click="emit('remove')">
          {{ t('admin.modelIntegrity.scheduling.bps.dialog.remove') }}
        </button>
        <span v-else />
        <div class="flex gap-2">
          <button type="button" class="btn btn-secondary" @click="emit('close')">{{ t('admin.modelIntegrity.common.cancel') }}</button>
          <button type="submit" form="bps-account-form" class="btn btn-primary" :disabled="!valid" data-testid="bps-apply">
            {{ t('admin.modelIntegrity.scheduling.bps.dialog.apply') }}
          </button>
        </div>
      </div>
      <p class="mt-2 text-right text-xs text-gray-500 dark:text-gray-400">{{ t('admin.modelIntegrity.scheduling.bps.dialog.applyHint') }}</p>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { accountsAPI, type OpenAIEvalBPSAccountConfig, type OpenAIEvalModelCatalog } from '@/api/admin/accounts'
import type { AccountListItem } from '@/types'
import {
  BPS_INTERVALS, BPS_MODES, BPS_THRESHOLD_MAX, BPS_THRESHOLD_MIN, CUSTOM_INTERVAL, DAY, HOUR, MAX_INTERVAL_MINUTES,
  bpsAccountLane, bpsDisabledKey, canResetBPSAccount, newBPSAccount, resultTone, type ResultTone
} from '@/views/admin/modelIntegrity/modelIntegrity'

const props = defineProps<{
  show: boolean
  /** Existing entry, or null to add a new account. */
  item: OpenAIEvalBPSAccountConfig | null
  accountLabel: string
  candidates: AccountListItem[]
  catalog: OpenAIEvalModelCatalog | null
  autoEnabled: boolean
  resetting: boolean
}>()
const emit = defineEmits<{
  close: []
  apply: [value: OpenAIEvalBPSAccountConfig]
  remove: []
  reset: []
}>()

const { t } = useI18n()
const isNew = computed(() => props.item === null)
const draft = reactive<OpenAIEvalBPSAccountConfig>(newBPSAccount(0))
const title = computed(() => isNew.value
  ? t('admin.modelIntegrity.scheduling.bps.dialog.addTitle')
  : t('admin.modelIntegrity.scheduling.bps.dialog.editTitle', { account: props.accountLabel }))

const lane = computed(() => props.item ? bpsAccountLane(props.item, props.autoEnabled) : 'inactive')
const canReset = computed(() => Boolean(props.item && canResetBPSAccount(props.item)))
const lockText = computed(() => {
  const reason = props.item?.disabled_reason
  if (!reason) return ''
  return t(`admin.modelIntegrity.scheduling.bps.disabled.${bpsDisabledKey(reason)}`, { reason })
})

const customIntervalSelected = ref(false)
const intervalChoice = computed<string | number>({
  get: () => customIntervalSelected.value ? CUSTOM_INTERVAL : draft.interval_seconds,
  set: value => {
    if (value === CUSTOM_INTERVAL) {
      customIntervalSelected.value = true
      return
    }
    customIntervalSelected.value = false
    draft.interval_seconds = Number(value)
  }
})
const customIntervalMinutes = computed({
  get: () => Math.max(5, Math.round((draft.interval_seconds || BPS_INTERVALS[0]) / 60)),
  set: (value: number) => { draft.interval_seconds = Number.isFinite(value) ? Math.min(Math.max(5, Math.trunc(value)), MAX_INTERVAL_MINUTES) * 60 : BPS_INTERVALS[0] }
})

const inRange = (value: number) => Number.isInteger(value) && value >= BPS_THRESHOLD_MIN && value <= BPS_THRESHOLD_MAX
const valid = computed(() => draft.account_id > 0
  && inRange(draft.failure_threshold)
  && inRange(draft.recovery_threshold)
  && Number.isSafeInteger(draft.interval_seconds)
  && draft.interval_seconds >= BPS_INTERVALS[0]
  && draft.interval_seconds <= MAX_INTERVAL_MINUTES * 60)

function intervalText(seconds: number) {
  if (seconds === DAY) return t('admin.modelIntegrity.scheduling.bps.every.hours', { n: 24 })
  if (seconds % DAY === 0) return t('admin.modelIntegrity.scheduling.bps.every.days', { n: seconds / DAY })
  if (seconds % HOUR === 0) return t('admin.modelIntegrity.scheduling.bps.every.hours', { n: Math.round(seconds / HOUR) })
  return t('admin.modelIntegrity.scheduling.bps.every.minutes', { n: Math.round(seconds / 60) })
}

function formatTime(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

interface HistoryEntry { key: string; at: string; label: string; detail: string; tone: string }
const history = ref<HistoryEntry[]>([])
const historyLoading = ref(false)
const TONE: Record<ResultTone, string> = {
  ok: 'text-emerald-700 dark:text-emerald-300',
  likely: 'text-emerald-700 dark:text-emerald-300',
  attention: 'text-amber-700 dark:text-amber-300',
  neutral: 'text-gray-700 dark:text-gray-300',
  error: 'text-rose-700 dark:text-rose-300',
  running: 'text-sky-700 dark:text-sky-300'
}

async function loadHistory(accountID: number) {
  history.value = []
  historyLoading.value = true
  try {
    const [runs, audit] = await Promise.allSettled([
      accountsAPI.listOpenAIEvalRuns({ account_id: accountID, test_type: 'state_probe', limit: 8 }),
      accountsAPI.listOpenAIEvalAudit()
    ])
    const entries: HistoryEntry[] = []
    if (runs.status === 'fulfilled') {
      for (const run of runs.value.items ?? []) {
        const verdict = run.outcome?.state_probe?.verdict || run.outcome?.status || run.status
        entries.push({
          key: `run-${run.id}`,
          at: run.finished_at || run.started_at,
          label: t('admin.modelIntegrity.scheduling.bps.history.probe', { status: t(`admin.modelIntegrity.status.${verdict}`) }),
          detail: [run.requested_model, run.outcome?.state_probe?.failure].filter(Boolean).join(' · '),
          tone: TONE[resultTone(verdict)]
        })
      }
    }
    if (audit.status === 'fulfilled') {
      for (const event of audit.value.items ?? []) {
        if (!event.action.startsWith('bps_') || Number(event.payload?.account_id) !== accountID) continue
        const known = event.action === 'bps_state_reset'
        entries.push({
          key: `audit-${event.id}`,
          at: event.created_at,
          label: known ? t('admin.modelIntegrity.scheduling.bps.history.reset') : event.action,
          detail: String(event.payload?.requested_model ?? event.payload?.probe_model ?? ''),
          tone: TONE.neutral
        })
      }
    }
    history.value = entries.sort((a, b) => Date.parse(b.at) - Date.parse(a.at)).slice(0, 8)
  } finally {
    historyLoading.value = false
  }
}

watch(() => [props.show, props.item] as const, ([show, item]) => {
  if (!show) return
  const base = item ?? newBPSAccount(props.candidates.length === 1 ? props.candidates[0].id : 0, props.catalog?.items?.[0]?.id ?? '')
  Object.assign(draft, {
    account_id: base.account_id,
    probe_model: base.probe_model ?? '',
    mode: base.mode,
    failure_threshold: base.failure_threshold,
    recovery_threshold: base.recovery_threshold,
    interval_seconds: base.interval_seconds
  })
  customIntervalSelected.value = !BPS_INTERVALS.includes(base.interval_seconds)
  if (item) void loadHistory(item.account_id)
  else history.value = []
}, { immediate: true })

function submit() {
  if (!valid.value) return
  emit('apply', { ...draft })
}
</script>

<style scoped>
.bps-status { @apply rounded-lg border px-4 py-3; }
.bps-status-locked { @apply border-rose-200 bg-rose-50 dark:border-rose-900/60 dark:bg-rose-950/30; }
.bps-status-bps { @apply border-sky-200 bg-sky-50 dark:border-sky-900/60 dark:bg-sky-950/30; }
.bps-status-native, .bps-status-inactive { @apply border-gray-200 bg-gray-50 dark:border-dark-700 dark:bg-dark-900/50; }
.mode-option { @apply block cursor-pointer rounded-lg border border-gray-200 px-3 py-2.5 transition-colors hover:border-gray-300 focus-within:ring-2 focus-within:ring-primary-500 dark:border-dark-600 dark:hover:border-dark-500; }
.mode-option-active { @apply border-primary-500 bg-primary-50/60 dark:border-primary-400 dark:bg-primary-950/30; }
.history { @apply divide-y divide-gray-100 rounded-lg border border-gray-200 text-xs dark:divide-dark-700 dark:border-dark-700; }
.history-row { @apply grid grid-cols-[auto_auto_minmax(0,1fr)] items-baseline gap-3 px-3 py-2; }
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
</style>
