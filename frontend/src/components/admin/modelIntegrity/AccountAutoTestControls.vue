<template>
  <section class="aac" :class="{ 'aac-paused': paused }" :aria-label="t('admin.modelIntegrity.tests.background.title', { account: accountName })" data-testid="account-auto-controls">
    <div class="aac-head">
      <div class="min-w-0">
        <p class="aac-state" role="status" data-testid="auto-state">
          <span class="aac-dot" :class="stateDotClass" aria-hidden="true" />
          <span>{{ stateText }}</span>
          <span v-if="pending" class="aac-pending" data-testid="auto-pending">{{ t('admin.modelIntegrity.tests.background.pending') }}</span>
        </p>
        <p v-if="paused && draft?.pause_reason" class="aac-reason" data-testid="pause-reason">{{ t('admin.modelIntegrity.tests.background.reason', { reason: draft.pause_reason }) }}</p>
        <p class="aac-scope">{{ t('admin.modelIntegrity.tests.background.scope') }}</p>
      </div>
      <div class="aac-actions">
        <button v-if="paused" type="button" class="btn btn-secondary btn-sm" data-testid="resume-auto" @click="resume">
          <Icon name="play" size="xs" />{{ t('admin.modelIntegrity.tests.background.resume') }}
        </button>
        <button v-else type="button" class="btn btn-secondary btn-sm" data-testid="pause-auto" @click="openPause">
          <Icon name="clock" size="xs" />{{ t('admin.modelIntegrity.tests.background.pause') }}
        </button>
        <button type="button" class="btn btn-secondary btn-sm" data-testid="open-limits" @click="openLimits">
          <Icon name="cog" size="xs" />{{ t('admin.modelIntegrity.tests.background.limits') }}
        </button>
      </div>
    </div>

    <dl class="aac-metrics">
      <div class="aac-metric">
        <dt>{{ t('admin.modelIntegrity.tests.background.sentLastHour') }}</dt>
        <dd data-testid="metric-sent">{{ runtimeValue('sent_last_hour') }}</dd>
      </div>
      <div class="aac-metric">
        <dt>{{ t('admin.modelIntegrity.tests.background.reserved') }}</dt>
        <dd data-testid="metric-reserved">{{ runtimeValue('reserved') }}</dd>
      </div>
      <div class="aac-metric">
        <dt>{{ t('admin.modelIntegrity.tests.background.activeRuns') }}</dt>
        <dd data-testid="metric-active">{{ runtimeValue('active_runs') }}</dd>
      </div>
      <div class="aac-metric">
        <dt>{{ t('admin.modelIntegrity.tests.background.planned') }}</dt>
        <dd data-testid="metric-planned">
          {{ plan.nominal > 0 ? t('admin.modelIntegrity.tests.background.plannedValue', { count: formatCount(plan.nominal) }) : t('admin.modelIntegrity.tests.background.plannedNone') }}
          <span v-if="plan.max > plan.nominal" class="aac-sub">{{ t('admin.modelIntegrity.tests.background.plannedRetry', { max: formatCount(plan.max) }) }}</span>
        </dd>
      </div>
    </dl>

    <p v-if="runtime?.deferred_reason" class="aac-note" data-testid="deferred-reason">
      {{ t('admin.modelIntegrity.tests.background.deferred', { reason: deferredText(runtime.deferred_reason) }) }}
    </p>
    <p v-if="runtime?.next_send_at" class="aac-note">{{ t('admin.modelIntegrity.tests.background.nextSend', { time: formatTime(runtime.next_send_at) }) }}</p>
    <p v-if="!runtime" class="aac-note" data-testid="runtime-missing">
      <template v-if="runtimeUnavailableReason">{{ t('admin.modelIntegrity.tests.background.runtimeUnavailable', { reason: unavailableText(runtimeUnavailableReason) }) }}</template>
      <template v-else>{{ t('admin.modelIntegrity.tests.background.runtimeMissing') }}</template>
    </p>

    <p v-if="draft?.budget_enabled" class="aac-limits" data-testid="limits-summary">{{ limitsSummary(draft) }}<template v-if="remaining !== null">{{ t('admin.modelIntegrity.tests.background.remaining', { count: remaining }) }}</template></p>
    <ul v-if="draft?.budget_enabled && warnings.length" class="aac-warnings" data-testid="budget-warnings">
      <li v-for="item in warnings" :key="item.key" :class="item.blocking ? 'aac-warn-block' : 'aac-warn'">{{ item.text }}</li>
    </ul>

    <!-- Pause -->
    <BaseDialog :show="pauseOpen" :title="t('admin.modelIntegrity.tests.background.pauseTitle', { account: accountName })" width="narrow" @close="pauseOpen = false">
      <form id="pause-auto-form" class="space-y-4" @submit.prevent="confirmPause">
        <fieldset>
          <legend class="input-label">{{ t('admin.modelIntegrity.tests.background.pauseFor') }}</legend>
          <div class="aac-durations">
            <label v-for="seconds in PAUSE_DURATIONS" :key="seconds" class="aac-choice">
              <input v-model="pauseChoice" type="radio" name="pause-duration" :value="seconds" class="text-primary-600 focus:ring-primary-500" />
              <span>{{ durationLabel(seconds) }}</span>
            </label>
            <label class="aac-choice">
              <input v-model="pauseChoice" type="radio" name="pause-duration" value="custom" class="text-primary-600 focus:ring-primary-500" />
              <span>{{ t('admin.modelIntegrity.tests.background.customHours') }}</span>
            </label>
          </div>
          <label v-if="pauseChoice === 'custom'" class="mt-2 flex items-center gap-2 text-sm">
            <input v-model.number="pauseHours" type="number" min="1" :max="MAX_PAUSE_HOURS" step="1" class="input h-9 w-24 py-1 text-sm tabular-nums" :aria-label="t('admin.modelIntegrity.tests.background.customHours')" data-testid="pause-hours" />
            <span class="text-gray-500 dark:text-gray-400">{{ t('admin.modelIntegrity.tests.background.hoursUnit') }}</span>
          </label>
          <p v-if="pauseChoice === 'custom' && !pauseHoursValid" class="aac-error" role="alert">{{ t('admin.modelIntegrity.tests.background.hoursInvalid', { max: MAX_PAUSE_HOURS }) }}</p>
        </fieldset>
        <label class="block">
          <span class="input-label">{{ t('admin.modelIntegrity.tests.background.reasonLabel') }}</span>
          <input v-model="pauseReason" type="text" maxlength="160" class="input" :placeholder="t('admin.modelIntegrity.tests.background.reasonPlaceholder')" data-testid="pause-reason-input" />
        </label>
        <p class="aac-dialog-hint">{{ t('admin.modelIntegrity.tests.background.pauseHint') }}</p>
      </form>
      <template #footer>
        <div class="flex justify-end gap-2">
          <button type="button" class="btn btn-secondary" @click="pauseOpen = false">{{ t('admin.modelIntegrity.common.cancel') }}</button>
          <button type="submit" form="pause-auto-form" class="btn btn-primary" :disabled="!pauseHoursValid" data-testid="confirm-pause">{{ t('admin.modelIntegrity.tests.background.pauseConfirm') }}</button>
        </div>
      </template>
    </BaseDialog>

    <!-- Experimental limits -->
    <BaseDialog :show="limitsOpen" :title="t('admin.modelIntegrity.tests.background.limitsTitle', { account: accountName })" width="normal" @close="limitsOpen = false">
      <form id="limits-form" class="space-y-4" @submit.prevent="confirmLimits">
        <label class="flex cursor-pointer items-start gap-3">
          <Toggle v-model="limitDraft.budget_enabled" :aria-label="t('admin.modelIntegrity.tests.background.enableLimits')" data-testid="limits-enabled" />
          <span>
            <span class="block text-sm font-medium text-gray-900 dark:text-white">{{ t('admin.modelIntegrity.tests.background.enableLimits') }}</span>
            <span class="block text-xs leading-relaxed text-gray-500 dark:text-gray-400">{{ t('admin.modelIntegrity.tests.background.enableLimitsHint') }}</span>
          </span>
        </label>

        <div class="aac-fields" :class="{ 'opacity-60': !limitDraft.budget_enabled }">
          <label v-for="field in limitFields" :key="field.key" class="aac-field">
            <span class="input-label">{{ t(`admin.modelIntegrity.tests.background.fields.${field.key}`) }}</span>
            <span class="flex items-center gap-2">
              <input
                v-model="limitText[field.key]"
                type="number"
                :min="field.min"
                :max="field.max"
                step="1"
                inputmode="numeric"
                class="input h-9 w-28 py-1 text-sm tabular-nums"
                :class="{ 'border-rose-400 dark:border-rose-500': fieldInvalid(field.key) }"
                :aria-invalid="fieldInvalid(field.key) || undefined"
                :disabled="!limitDraft.budget_enabled"
                :data-testid="`limit-${field.key}`"
              />
              <span class="text-xs text-gray-500 dark:text-gray-400">{{ t(`admin.modelIntegrity.tests.background.units.${field.key}`) }}</span>
            </span>
            <span v-if="fieldInvalid(field.key)" class="aac-error" role="alert">{{ t('admin.modelIntegrity.tests.background.rangeInvalid', { min: field.min, max: field.max.toLocaleString() }) }}</span>
          </label>
        </div>
        <button type="button" class="text-sm font-medium text-primary-700 underline-offset-2 hover:underline disabled:opacity-50 dark:text-primary-300" :disabled="!limitDraft.budget_enabled" data-testid="limits-reset" @click="resetLimitText">
          {{ t('admin.modelIntegrity.tests.background.restoreDefaults') }}
        </button>

        <div v-if="limitDraft.budget_enabled && previewControl" class="aac-estimate" data-testid="limits-estimate">
          <p class="text-sm text-gray-800 dark:text-gray-200">{{ t('admin.modelIntegrity.tests.background.capacity', { count: previewCapacity.capacity, minutes: windowMinutes(previewControl) }) }}<template v-if="previewCapacity.limitedBy !== 'interval'"> {{ t(`admin.modelIntegrity.tests.background.limitedBy.${previewCapacity.limitedBy}`) }}</template></p>
          <p v-if="fingerprintBlocked" class="aac-warn-block mt-2" data-testid="fingerprint-infeasible">{{ fingerprintBlocked }}</p>
          <table v-if="previewRows.length" class="aac-table">
            <thead>
              <tr>
                <th scope="col">{{ t('admin.modelIntegrity.tests.background.estimate.test') }}</th>
                <th scope="col" class="num">{{ t('admin.modelIntegrity.tests.background.estimate.sends') }}</th>
                <th scope="col" class="num">{{ t('admin.modelIntegrity.tests.background.estimate.duration') }}</th>
                <th scope="col">{{ t('admin.modelIntegrity.tests.background.estimate.result') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="row in previewRows" :key="row.key">
                <td>
                  <span class="block">{{ row.test }}</span>
                  <span class="block text-xs text-gray-500 dark:text-gray-400">{{ row.target }}</span>
                </td>
                <td class="num">{{ row.feasibility.nominal }}<span v-if="row.feasibility.maxWithRetries > row.feasibility.nominal" class="block text-xs text-gray-500 dark:text-gray-400">{{ t('admin.modelIntegrity.tests.background.estimate.retryMax', { max: row.feasibility.maxWithRetries }) }}</span></td>
                <td class="num">{{ durationText(row.feasibility.minDurationSeconds) }}</td>
                <td>
                  <span v-if="!row.feasibility.fits" class="tone tone-error">{{ t('admin.modelIntegrity.tests.background.estimate.blocked') }}</span>
                  <span v-else-if="row.overInterval" class="tone tone-attention">{{ t('admin.modelIntegrity.tests.background.estimate.merged') }}</span>
                  <span v-else class="tone tone-ok">{{ t('admin.modelIntegrity.tests.background.estimate.fits') }}</span>
                </td>
              </tr>
            </tbody>
          </table>
          <p v-else class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.modelIntegrity.tests.background.estimate.none') }}</p>
          <p class="mt-2 text-xs leading-relaxed text-gray-500 dark:text-gray-400">{{ t('admin.modelIntegrity.tests.background.estimate.hint') }}</p>
        </div>
      </form>
      <template #footer>
        <div class="flex justify-end gap-2">
          <button type="button" class="btn btn-secondary" @click="limitsOpen = false">{{ t('admin.modelIntegrity.common.cancel') }}</button>
          <button type="submit" form="limits-form" class="btn btn-primary" :disabled="limitDraft.budget_enabled && anyFieldInvalid" data-testid="confirm-limits">{{ t('admin.modelIntegrity.tests.background.apply') }}</button>
        </div>
      </template>
    </BaseDialog>
  </section>
</template>

<script setup lang="ts">
import { computed, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Toggle from '@/components/common/Toggle.vue'
import Icon from '@/components/icons/Icon.vue'
import type { OpenAIEvalBackgroundControl, OpenAIEvalBackgroundRuntime, OpenAIEvalModelCatalog, OpenAIEvalRouteConfig } from '@/api/admin/accounts'
import {
  BACKGROUND_DEFAULTS,
  BACKGROUND_LIMITS,
  BACKGROUND_LIMIT_KEYS,
  DAY,
  HOUR,
  PAUSE_DURATIONS,
  TEST_TYPES,
  accountHourlyPlan,
  backgroundControlPayload,
  defaultBackgroundControl,
  maxRequestsPerRun,
  parseBackgroundLimit,
  pausedUntil,
  requestsPerRun,
  runFeasibility,
  sameBackgroundControl,
  scheduleOf,
  type BackgroundLimitKey,
  type RunFeasibility,
  type TestApplicability
} from '@/views/admin/modelIntegrity/modelIntegrity'

const props = defineProps<{
  accountId: number
  accountName: string
  /** The draft control for this account, if one exists. */
  control?: OpenAIEvalBackgroundControl
  /** The control as last saved, carrying the live runtime. */
  savedControl?: OpenAIEvalBackgroundControl
  /** Every target of this account. */
  routes: OpenAIEvalRouteConfig[]
  catalog: OpenAIEvalModelCatalog | null
  maxAttempts: number
  /** Account RPM cap (0 or missing = none). */
  rpmLimit?: number | null
  applies: TestApplicability
}>()

const emit = defineEmits<{ (e: 'update', control: OpenAIEvalBackgroundControl): void }>()
const { t } = useI18n()

const MAX_PAUSE_HOURS = 24 * 30
const draft = computed(() => props.control)
const paused = computed(() => pausedUntil(draft.value) !== null)
const pending = computed(() => !sameBackgroundControl(props.control, props.savedControl))
// Runtime belongs to what the server is running, i.e. the saved control.
const runtime = computed<OpenAIEvalBackgroundRuntime | null>(() => props.savedControl?.runtime ?? props.control?.runtime ?? null)
/** Why the server could not read live counters; the counters themselves stay unknown. */
const runtimeUnavailableReason = computed(() => (props.savedControl?.runtime_unavailable_reason ?? props.control?.runtime_unavailable_reason ?? '').trim())
function unavailableText(reason: string) {
  const key = `admin.modelIntegrity.tests.background.runtimeUnavailableReasons.${reason}`
  const text = t(key)
  return text === key ? reason : text
}
/** Sends the saved hourly budget still allows; unknown without runtime, never assumed full. */
const remaining = computed(() => {
  const saved = props.savedControl
  const live = runtime.value
  if (!saved?.budget_enabled || !live || typeof live.sent_last_hour !== 'number' || typeof live.reserved !== 'number') return null
  return Math.max(0, saved.max_requests_per_hour - live.sent_last_hour - live.reserved)
})
const plan = computed(() => accountHourlyPlan(props.routes, props.accountId, props.maxAttempts, props.catalog, props.applies))
const autoCount = computed(() => props.routes.reduce((sum, route) => sum + TEST_TYPES.filter(type => scheduleOf(route, type).enabled && props.applies(route, type)).length, 0))

const stateText = computed(() => {
  const until = pausedUntil(draft.value)
  if (until) return t('admin.modelIntegrity.tests.background.statePaused', { time: formatTime(until.toISOString()) })
  if (!autoCount.value) return t('admin.modelIntegrity.tests.background.stateNoAuto')
  if (draft.value?.budget_enabled) return t('admin.modelIntegrity.tests.background.stateLimited')
  return t('admin.modelIntegrity.tests.background.stateRunning')
})
const stateDotClass = computed(() => {
  if (paused.value) return 'aac-dot-paused'
  if (!autoCount.value) return 'aac-dot-idle'
  return runtime.value?.deferred_reason ? 'aac-dot-deferred' : 'aac-dot-on'
})

function runtimeValue(key: 'sent_last_hour' | 'reserved' | 'active_runs') {
  const value = runtime.value?.[key]
  return typeof value === 'number' && Number.isFinite(value) ? value.toLocaleString() : t('admin.modelIntegrity.tests.background.notCounted')
}

function deferredText(reason: string) {
  const key = `admin.modelIntegrity.tests.background.deferredReasons.${reason}`
  const text = t(key)
  return text === key ? reason : text
}

/** States the limits the server applies: out-of-range stored values (such as 0) read as its defaults. */
function limitsSummary(control: OpenAIEvalBackgroundControl) {
  const effective = backgroundControlPayload(control)
  return t('admin.modelIntegrity.tests.background.limitsSummary', {
    hourly: effective.max_requests_per_hour,
    interval: effective.min_send_interval_seconds,
    concurrency: effective.max_background_concurrency,
    minutes: windowMinutes(effective)
  })
}

const windowMinutes = (control: Pick<OpenAIEvalBackgroundControl, 'sampling_window_seconds'>) => Number((control.sampling_window_seconds / 60).toFixed(1))

interface EstimateRow { key: string; test: string; target: string; feasibility: RunFeasibility; overInterval: boolean }

function estimateRows(control: OpenAIEvalBackgroundControl): EstimateRow[] {
  const rows: EstimateRow[] = []
  for (const route of props.routes) {
    for (const type of TEST_TYPES) {
      const schedule = scheduleOf(route, type)
      if (!schedule.enabled || !props.applies(route, type)) continue
      const feasibility = runFeasibility(requestsPerRun(route, type, props.catalog), maxRequestsPerRun(route, type, props.maxAttempts, props.catalog), control, props.rpmLimit)
      rows.push({
        key: `${route.requested_model}:${route.reasoning_effort}:${type}`,
        test: t(`admin.modelIntegrity.tests.types.${type}.name`),
        target: `${route.requested_model} · ${route.reasoning_effort || t('admin.modelIntegrity.common.defaultEffort')}`,
        feasibility,
        overInterval: feasibility.minDurationSeconds > schedule.interval_seconds
      })
    }
  }
  return rows
}

const warnings = computed(() => {
  if (!draft.value?.budget_enabled) return []
  return estimateRows(draft.value)
    .filter(row => !row.feasibility.fits || row.overInterval)
    .map(row => ({
      key: row.key,
      blocking: !row.feasibility.fits,
      text: !row.feasibility.fits
        ? t('admin.modelIntegrity.tests.background.warnBlocked', { test: row.test, target: row.target, count: row.feasibility.nominal, capacity: row.feasibility.capacity })
        : t('admin.modelIntegrity.tests.background.warnMerged', { test: row.test, target: row.target, duration: durationText(row.feasibility.minDurationSeconds) })
    }))
})

// -- Pause dialog --
const pauseOpen = ref(false)
const pauseChoice = ref<number | 'custom'>(HOUR)
const pauseHours = ref<number | string>(2)
const pauseReason = ref('')
const pauseHoursValid = computed(() => {
  if (pauseChoice.value !== 'custom') return true
  const hours = Number(pauseHours.value)
  return Number.isInteger(hours) && hours >= 1 && hours <= MAX_PAUSE_HOURS
})

function base(): OpenAIEvalBackgroundControl {
  return props.control ? { ...props.control } : defaultBackgroundControl(props.accountId)
}

function openPause() {
  pauseChoice.value = HOUR
  pauseHours.value = 2
  pauseReason.value = ''
  pauseOpen.value = true
}

function confirmPause() {
  if (!pauseHoursValid.value) return
  const seconds = pauseChoice.value === 'custom' ? Number(pauseHours.value) * HOUR : pauseChoice.value
  emit('update', { ...base(), paused_until: new Date(Date.now() + seconds * 1000).toISOString(), pause_reason: pauseReason.value.trim() })
  pauseOpen.value = false
}

function resume() {
  emit('update', { ...base(), paused_until: null, pause_reason: '' })
}

// -- Limits dialog --
const limitsOpen = ref(false)
const limitDraft = reactive({ budget_enabled: false })
/** Fields edited as text so an out-of-range entry is shown, not silently clamped. Window is in minutes. */
const limitText = reactive<Record<BackgroundLimitKey, string>>({ max_requests_per_hour: '', min_send_interval_seconds: '', max_background_concurrency: '', sampling_window_seconds: '' })
const limitFields = computed(() => BACKGROUND_LIMIT_KEYS.map(key => {
  const { min, max } = BACKGROUND_LIMITS[key]
  return key === 'sampling_window_seconds' ? { key, min: min / 60, max: max / 60 } : { key, min, max }
}))

function toDisplay(key: BackgroundLimitKey, value: number) {
  return String(key === 'sampling_window_seconds' ? Number((value / 60).toFixed(2)) : value)
}

function parsedField(key: BackgroundLimitKey): number | null {
  const raw = String(limitText[key]).trim()
  if (raw === '') return null
  const value = key === 'sampling_window_seconds' ? Number(raw) * 60 : Number(raw)
  return parseBackgroundLimit(key, value)
}
const fieldInvalid = (key: BackgroundLimitKey) => limitDraft.budget_enabled && parsedField(key) === null
const anyFieldInvalid = computed(() => BACKGROUND_LIMIT_KEYS.some(key => parsedField(key) === null))

function openLimits() {
  const current = base()
  limitDraft.budget_enabled = current.budget_enabled
  // A stored value outside the accepted range (e.g. 0 kept while limits were off) opens as the default the server would apply.
  for (const key of BACKGROUND_LIMIT_KEYS) limitText[key] = toDisplay(key, parseBackgroundLimit(key, current[key]) ?? BACKGROUND_DEFAULTS[key])
  limitsOpen.value = true
}

function resetLimitText() {
  for (const key of BACKGROUND_LIMIT_KEYS) limitText[key] = toDisplay(key, BACKGROUND_DEFAULTS[key])
}

const previewControl = computed<OpenAIEvalBackgroundControl | null>(() => {
  if (anyFieldInvalid.value) return null
  const next = { ...base(), budget_enabled: limitDraft.budget_enabled }
  for (const key of BACKGROUND_LIMIT_KEYS) next[key] = parsedField(key) as number
  return next
})
const previewRows = computed(() => (previewControl.value ? estimateRows(previewControl.value) : []))
const previewCapacity = computed(() => runFeasibility(0, 0, previewControl.value ?? BACKGROUND_DEFAULTS, props.rpmLimit))
const fingerprintModes = computed(() => props.catalog?.fingerprint_modes?.length ? props.catalog.fingerprint_modes : [{ id: 'quick', samples: 60 }, { id: 'standard', samples: 200 }, { id: 'strict', samples: 400 }])
/** Fingerprint sample sizes are fixed, so say plainly which ones cannot fit. */
const fingerprintBlocked = computed(() => {
  if (!previewControl.value) return ''
  const capacity = previewCapacity.value.capacity
  const blocked = fingerprintModes.value.filter(mode => mode.samples > capacity)
  if (!blocked.length) return ''
  return t('admin.modelIntegrity.tests.background.fingerprintBlocked', { counts: blocked.map(mode => mode.samples).join(' / '), capacity })
})

function confirmLimits() {
  const next = base()
  next.budget_enabled = limitDraft.budget_enabled
  // Keep the last valid numbers when the limits are switched off.
  for (const key of BACKGROUND_LIMIT_KEYS) {
    const value = parsedField(key)
    if (value !== null) next[key] = value
    else if (limitDraft.budget_enabled) return
  }
  emit('update', next)
  limitsOpen.value = false
}

// -- Formatting --
function durationLabel(seconds: number) {
  if (seconds >= DAY) return t('admin.modelIntegrity.tests.background.days', { n: seconds / DAY })
  return t('admin.modelIntegrity.tests.background.hours', { n: seconds / HOUR })
}
function durationText(seconds: number) {
  if (seconds <= 0) return t('admin.modelIntegrity.tests.background.instant')
  if (seconds < 60) return t('admin.modelIntegrity.tests.background.seconds', { n: seconds })
  return t('admin.modelIntegrity.tests.background.minutes', { n: Number((seconds / 60).toFixed(1)) })
}
const formatCount = (value: number) => (value >= 10 ? Math.round(value).toLocaleString() : Number(value.toFixed(1)).toString())
const formatTime = (value: string) => {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString([], { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false })
}
</script>

<style scoped>
.aac { @apply space-y-2.5 border-b border-gray-100 py-4 dark:border-dark-700; }
.aac-head { @apply flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between; }
.aac-state { @apply flex flex-wrap items-center gap-2 text-sm font-medium text-gray-900 dark:text-white; }
.aac-dot { @apply inline-block h-2 w-2 shrink-0 rounded-full; }
.aac-dot-on { @apply bg-emerald-500; }
.aac-dot-deferred { @apply bg-amber-500; }
.aac-dot-paused { @apply bg-gray-400 ring-2 ring-gray-200 dark:ring-dark-600; }
.aac-dot-idle { @apply border border-gray-300 dark:border-dark-500; }
.aac-paused .aac-state { @apply text-gray-700 dark:text-gray-200; }
.aac-pending { @apply rounded bg-amber-100 px-1.5 py-0.5 text-xs font-medium text-amber-900 dark:bg-amber-900/40 dark:text-amber-200; }
.aac-reason { @apply mt-1 text-sm text-gray-700 dark:text-gray-300; }
.aac-scope { @apply mt-1 text-xs text-gray-500 dark:text-gray-400; }
.aac-actions { @apply flex shrink-0 flex-wrap gap-2; }
.aac-metrics { @apply grid grid-cols-2 gap-x-4 gap-y-2 rounded-md bg-gray-50 px-3 py-2.5 dark:bg-dark-800/70 sm:grid-cols-4; }
.aac-metric dt { @apply text-xs text-gray-500 dark:text-gray-400; }
.aac-metric dd { @apply mt-0.5 text-sm tabular-nums text-gray-900 dark:text-gray-100; }
.aac-sub { @apply block text-xs text-gray-500 dark:text-gray-400; }
.aac-note { @apply text-xs leading-relaxed text-gray-600 dark:text-gray-400; }
.aac-limits { @apply text-xs text-gray-700 dark:text-gray-300; }
.aac-warnings { @apply space-y-1 text-xs leading-relaxed; }
.aac-warn { @apply rounded-md bg-amber-50 px-2.5 py-1.5 text-amber-900 dark:bg-amber-950/30 dark:text-amber-200; }
.aac-warn-block { @apply rounded-md bg-rose-50 px-2.5 py-1.5 text-xs leading-relaxed text-rose-800 dark:bg-rose-950/30 dark:text-rose-300; }
.aac-durations { @apply mt-1 grid grid-cols-2 gap-2 sm:grid-cols-3; }
.aac-choice { @apply flex cursor-pointer items-center gap-2 rounded-lg border border-gray-200 px-3 py-2 text-sm dark:border-dark-600; }
.aac-choice:has(input:checked) { @apply border-primary-600 bg-primary-50/50 dark:bg-primary-950/30; }
.aac-dialog-hint { @apply text-xs leading-relaxed text-gray-500 dark:text-gray-400; }
.aac-error { @apply mt-1 block text-xs text-rose-700 dark:text-rose-300; }
.aac-fields { @apply grid gap-3 sm:grid-cols-2; }
.aac-field { @apply flex flex-col; }
.aac-estimate { @apply rounded-lg border border-gray-200 p-3 dark:border-dark-600; }
.aac-table { @apply mt-2 w-full text-left text-sm; }
.aac-table th { @apply border-b border-gray-200 px-2 py-1.5 text-xs font-medium text-gray-500 dark:border-dark-600 dark:text-gray-400; }
.aac-table td { @apply border-b border-gray-100 px-2 py-2 align-top text-gray-700 dark:border-dark-700 dark:text-gray-300; }
.aac-table .num { @apply whitespace-nowrap text-right tabular-nums; }
.tone { @apply inline-flex whitespace-nowrap rounded px-1.5 py-0.5 text-xs font-medium; }
.tone-ok { @apply bg-emerald-50 text-emerald-800 dark:bg-emerald-950/40 dark:text-emerald-300; }
.tone-attention { @apply bg-amber-100 text-amber-900 dark:bg-amber-900/40 dark:text-amber-200; }
.tone-error { @apply bg-rose-50 text-rose-800 dark:bg-rose-950/40 dark:text-rose-300; }
</style>
