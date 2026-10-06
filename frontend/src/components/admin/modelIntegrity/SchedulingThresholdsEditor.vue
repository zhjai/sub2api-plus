<template>
  <fieldset ref="root" class="th">
    <legend class="sr-only">{{ legend }}</legend>

    <!-- One row per policy, one column per measured value. On narrow screens
         each input carries its own visible label instead of the header. -->
    <div class="th-grid">
      <div class="th-head" aria-hidden="true">
        <span />
        <span>{{ t('admin.modelIntegrity.scheduling.thresholds.columns.errorRate') }}</span>
        <span>{{ t('admin.modelIntegrity.scheduling.thresholds.columns.ttft') }}</span>
      </div>
      <div
        v-for="policy in THRESHOLD_POLICIES"
        :key="policy"
        class="th-row"
        :class="{ 'th-row-active': inUse.includes(policy) }"
        :data-testid="`threshold-row-${policy}`"
      >
        <p class="th-policy">
          <span class="th-policy-name">{{ policyName(policy) }}</span>
          <span v-if="inUse.includes(policy)" class="th-in-use" data-testid="threshold-in-use">{{ t('admin.modelIntegrity.scheduling.thresholds.inUse') }}</span>
        </p>
        <label v-for="metric in METRICS" :key="metric" class="th-field">
          <span class="th-field-label">{{ t(`admin.modelIntegrity.scheduling.thresholds.columns.${metric === 'error_rate' ? 'errorRate' : 'ttft'}`) }}</span>
          <span class="th-input-wrap">
            <input
              :value="text(`${policy}.${metric}`)"
              class="input th-input tabular-nums"
              type="number"
              inputmode="decimal"
              :min="metric === 'error_rate' ? 0 : undefined"
              :max="metric === 'error_rate' ? 100 : MAX_TTFT_THRESHOLD_SECONDS"
              step="any"
              :placeholder="defaultText(`${policy}.${metric}`)"
              :aria-label="t('admin.modelIntegrity.scheduling.thresholds.fieldAria', { policy: policyName(policy), field: t(`admin.modelIntegrity.scheduling.thresholds.columns.${metric === 'error_rate' ? 'errorRate' : 'ttft'}`) })"
              :aria-invalid="invalid.has(`${policy}.${metric}`) ? 'true' : undefined"
              :aria-describedby="invalid.has(`${policy}.${metric}`) ? errorId(`${policy}.${metric}`) : undefined"
              :data-testid="`threshold-${policy}-${metric}`"
              @input="update(`${policy}.${metric}`, ($event.target as HTMLInputElement).value)"
              @change="settle(`${policy}.${metric}`)"
            />
            <span class="th-unit" aria-hidden="true">{{ metric === 'error_rate' ? '%' : t('admin.modelIntegrity.scheduling.thresholds.units.seconds') }}</span>
          </span>
        </label>
        <template v-for="metric in METRICS" :key="`error-${metric}`">
          <p v-if="invalid.has(`${policy}.${metric}`)" :id="errorId(`${policy}.${metric}`)" class="th-error" data-testid="threshold-error">
            {{ t(`admin.modelIntegrity.scheduling.thresholds.errors.${metric}`, { policy: policyName(policy) }) }}
          </p>
        </template>
      </div>
    </div>
    <p class="th-note">{{ t('admin.modelIntegrity.scheduling.thresholds.zeroNote') }}</p>

    <div class="th-samples">
      <p class="th-samples-title">{{ t('admin.modelIntegrity.scheduling.thresholds.samples.title') }}</p>
      <div class="th-samples-grid">
        <label v-for="key in THRESHOLD_SAMPLE_KEYS" :key="key" class="th-sample">
          <span class="th-sample-label">{{ t(`admin.modelIntegrity.scheduling.thresholds.samples.${key}`) }}</span>
          <input
            :value="text(key)"
            class="input th-input th-sample-input tabular-nums"
            type="number"
            inputmode="numeric"
            :min="MIN_THRESHOLD_SAMPLES"
            :max="MAX_THRESHOLD_SAMPLES"
            step="1"
            :placeholder="defaultText(key)"
            :aria-invalid="invalid.has(key) ? 'true' : undefined"
            :aria-describedby="invalid.has(key) ? errorId(key) : undefined"
            :data-testid="`threshold-${key}`"
            @input="update(key, ($event.target as HTMLInputElement).value)"
            @change="settle(key)"
          />
          <span v-if="invalid.has(key)" :id="errorId(key)" class="th-error" data-testid="threshold-error">{{ t('admin.modelIntegrity.scheduling.thresholds.errors.samples') }}</span>
        </label>
      </div>
      <p class="th-note">{{ t('admin.modelIntegrity.scheduling.thresholds.samples.note') }}</p>
    </div>
  </fieldset>
</template>

<script lang="ts">
let nextId = 0
</script>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { OpenAIEvalSchedulingThresholds } from '@/api/admin/accounts'
import {
  DEFAULT_SCHEDULING_THRESHOLDS,
  MAX_THRESHOLD_SAMPLES,
  MAX_TTFT_THRESHOLD_SECONDS,
  MIN_THRESHOLD_SAMPLES,
  THRESHOLD_POLICIES,
  THRESHOLD_SAMPLE_KEYS,
  errorRatePercentText,
  invalidThresholdFields,
  isValidThresholdErrorRate,
  isValidThresholdSamples,
  isValidThresholdTTFT,
  normalizeSchedulingThresholds,
  parseThresholdInput,
  type ThresholdField,
  type ThresholdPolicy,
  type ThresholdSampleKey
} from '@/views/admin/modelIntegrity/modelIntegrity'

const props = withDefaults(defineProps<{
  modelValue?: OpenAIEvalSchedulingThresholds
  legend: string
  /** Policies the default or a model rule currently uses. */
  inUse?: ThresholdPolicy[]
}>(), { inUse: () => [] })
const emit = defineEmits<{ (e: 'update:modelValue', value: OpenAIEvalSchedulingThresholds): void }>()
const { t } = useI18n()

const METRICS = ['error_rate', 'ttft_seconds'] as const
const idPrefix = `scheduling-threshold-${++nextId}`
const root = ref<HTMLFieldSetElement | null>(null)

const current = computed(() => normalizeSchedulingThresholds(props.modelValue))
const invalid = computed(() => new Set<ThresholdField>(invalidThresholdFields(current.value)))

/**
 * What the administrator typed, kept while it differs from a clean rendering
 * of the stored number, so "2." or an out-of-range entry is never rewritten
 * under the cursor and stays visible next to its error.
 */
const drafts = reactive<Partial<Record<ThresholdField, string>>>({})

type Metric = typeof METRICS[number]

function splitField(field: ThresholdField): [ThresholdPolicy, Metric] | [ThresholdSampleKey, null] {
  const dot = field.indexOf('.')
  return dot < 0 ? [field as ThresholdSampleKey, null] : [field.slice(0, dot) as ThresholdPolicy, field.slice(dot + 1) as Metric]
}

function valueOf(source: OpenAIEvalSchedulingThresholds, field: ThresholdField): number {
  const [head, metric] = splitField(field)
  return metric ? source[head as ThresholdPolicy][metric] : source[head as ThresholdSampleKey]
}

function isValid(field: ThresholdField, value: number): boolean {
  const metric = splitField(field)[1]
  if (metric === 'error_rate') return isValidThresholdErrorRate(value)
  if (metric === 'ttft_seconds') return isValidThresholdTTFT(value)
  return isValidThresholdSamples(value)
}

/** Error rates are edited as percentages and stored as ratios. */
function parse(field: ThresholdField, raw: string): number {
  const value = parseThresholdInput(raw)
  return field.endsWith('.error_rate') ? value / 100 : value
}

function render(field: ThresholdField, value: number): string {
  if (field.endsWith('.error_rate')) return errorRatePercentText(value)
  return Number.isFinite(value) ? String(value) : ''
}

function text(field: ThresholdField): string {
  return drafts[field] ?? render(field, valueOf(current.value, field))
}

function defaultText(field: ThresholdField): string {
  return render(field, valueOf(DEFAULT_SCHEDULING_THRESHOLDS, field))
}

function policyName(policy: ThresholdPolicy): string {
  return t(`admin.modelIntegrity.scheduling.policy.options.${policy}.name`)
}

function errorId(field: ThresholdField): string {
  return `${idPrefix}-${field.replace('.', '-')}-error`
}

/** The last object emitted, until the parent passes it back; two quick edits never drop one another. */
let pending: OpenAIEvalSchedulingThresholds | null = null

/** Emits a fresh object; an empty or non-numeric entry is stored as NaN, which validation rejects. */
function update(field: ThresholdField, raw: string) {
  drafts[field] = raw
  const next = normalizeSchedulingThresholds(pending ?? props.modelValue)
  const value = parse(field, raw)
  const [head, metric] = splitField(field)
  if (metric) next[head as ThresholdPolicy] = { ...next[head as ThresholdPolicy], [metric]: value }
  else next[head as ThresholdSampleKey] = value
  pending = next
  emit('update:modelValue', next)
}

/**
 * On leaving a valid field its text is tidied ("07" → "7"); an invalid one
 * keeps what was typed. Judged from the draft itself, since the parent's
 * update may not have reached the props yet.
 */
function settle(field: ThresholdField) {
  const draft = drafts[field]
  if (draft !== undefined && isValid(field, parse(field, draft))) delete drafts[field]
}

// A reload or another page's save replaces the object: drop drafts it no longer matches.
watch(() => props.modelValue, value => {
  pending = null
  const source = normalizeSchedulingThresholds(value)
  for (const field of Object.keys(drafts) as ThresholdField[]) {
    if (!Object.is(parse(field, drafts[field] ?? ''), valueOf(source, field))) delete drafts[field]
  }
})

/** Moves keyboard focus to the first value that blocks saving. */
function focusFirstInvalid() {
  root.value?.querySelector<HTMLInputElement>('input[aria-invalid="true"]')?.focus()
}

defineExpose({ focusFirstInvalid })
</script>

<style scoped>
.th { @apply min-w-0 space-y-3; }
.th-grid { @apply min-w-0 overflow-hidden rounded-lg border border-gray-200 dark:border-dark-700; }
.th-head { @apply hidden border-b border-gray-200 bg-gray-50 px-3 py-2 text-xs font-medium text-gray-500 dark:border-dark-700 dark:bg-dark-900/60 dark:text-gray-400 sm:grid sm:grid-cols-[minmax(0,1fr)_9rem_9rem] sm:gap-3; }
.th-row { @apply grid grid-cols-2 items-center gap-x-3 gap-y-2 border-b border-gray-100 px-3 py-2.5 last:border-b-0 dark:border-dark-700 sm:grid-cols-[minmax(0,1fr)_9rem_9rem]; }
/* Ties the matrix to the policy cards above: the rows actually in force carry the same accent. */
.th-row-active { @apply bg-primary-50/40 dark:bg-primary-950/20; box-shadow: inset 3px 0 0 theme('colors.primary.600'); }
.th-policy { @apply col-span-2 flex min-w-0 flex-wrap items-baseline gap-x-2 sm:col-span-1; }
.th-policy-name { @apply text-sm font-medium text-gray-900 dark:text-white; }
.th-in-use { @apply text-xs font-medium text-primary-700 dark:text-primary-300; }
.th-field { @apply flex min-w-0 flex-col gap-1; }
.th-field-label { @apply text-xs text-gray-500 dark:text-gray-400 sm:sr-only; }
.th-input-wrap { @apply relative block; }
.th-input { @apply h-9 py-1 text-sm; }
.th-input-wrap .th-input { @apply pr-9; }
.th-input[aria-invalid='true'] { @apply border-rose-400 dark:border-rose-500; }
.th-unit { @apply pointer-events-none absolute right-3 top-1/2 -translate-y-1/2 text-xs text-gray-500 dark:text-gray-400; }
.th-error { @apply col-span-2 text-xs text-rose-700 dark:text-rose-300 sm:col-span-3; }
.th-note { @apply max-w-[80ch] text-xs leading-relaxed text-gray-500 dark:text-gray-400; }
.th-samples { @apply space-y-2 pt-1; }
.th-samples-title { @apply text-xs font-medium text-gray-700 dark:text-gray-300; }
.th-samples-grid { @apply grid gap-3 sm:grid-cols-2 lg:max-w-2xl; }
.th-sample { @apply flex min-w-0 flex-col gap-1; }
.th-sample-label { @apply text-xs text-gray-500 dark:text-gray-400; }
.th-sample-input { @apply w-full sm:w-40; }
</style>
