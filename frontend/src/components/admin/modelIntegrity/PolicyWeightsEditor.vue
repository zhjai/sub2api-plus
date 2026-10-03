<template>
  <fieldset class="weights">
    <legend class="sr-only">{{ legend }}</legend>
    <div class="weights-grid">
      <label v-for="factor in CUSTOM_FACTORS" :key="factor" class="weights-field">
        <span class="weights-label">{{ t(`admin.modelIntegrity.scheduling.policy.custom.${factor}`) }}</span>
        <input
          :value="percent(factor)"
          class="input weights-input tabular-nums"
          type="number"
          inputmode="numeric"
          min="0"
          max="100"
          step="1"
          :aria-invalid="invalid ? 'true' : undefined"
          :aria-describedby="invalid ? errorId : undefined"
          :data-testid="`weight-${factor}`"
          @input="update(factor, ($event.target as HTMLInputElement).value)"
          @change="($event.target as HTMLInputElement).value = String(percent(factor))"
        />
      </label>
    </div>
    <p v-if="invalid" :id="errorId" class="weights-error" role="alert" data-testid="weights-error">
      {{ t('admin.modelIntegrity.scheduling.policy.custom.zeroTotal') }}
    </p>
  </fieldset>
</template>

<script lang="ts">
let nextId = 0
</script>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { OpenAIEvalPolicyWeights } from '@/api/admin/accounts'
import { CUSTOM_FACTORS, DEFAULT_CUSTOM_BALANCE, isValidCustomBalance, type CustomFactor } from '@/views/admin/modelIntegrity/modelIntegrity'

const props = defineProps<{
  modelValue?: OpenAIEvalPolicyWeights
  legend: string
}>()
const emit = defineEmits<{ (e: 'update:modelValue', value: OpenAIEvalPolicyWeights): void }>()
const { t } = useI18n()

const errorId = `policy-weights-error-${++nextId}`
const invalid = computed(() => !isValidCustomBalance(props.modelValue))

function percent(factor: CustomFactor) {
  return Math.round(Number(props.modelValue?.[factor] ?? 0) * 100)
}

/**
 * Keeps each value within 0–100 % and always emits a fresh object so a rule
 * never shares weights with another rule or the default.
 */
function update(factor: CustomFactor, value: string) {
  const raw = Number(value)
  const next = Number.isFinite(raw) ? Math.min(Math.max(Math.round(raw), 0), 100) / 100 : 0
  emit('update:modelValue', { ...(props.modelValue ?? DEFAULT_CUSTOM_BALANCE), [factor]: next })
}
</script>

<style scoped>
.weights { @apply min-w-0 space-y-2; }
.weights-grid { @apply grid min-w-0 gap-3 sm:grid-cols-2 xl:grid-cols-5; }
.weights-field { @apply flex min-w-0 flex-col gap-1; }
.weights-label { @apply text-xs text-gray-500 dark:text-gray-400; }
.weights-input { @apply h-9 py-1 text-sm; }
.weights-input[aria-invalid='true'] { @apply border-rose-400 dark:border-rose-500; }
.weights-error { @apply text-xs text-rose-700 dark:text-rose-300; }
</style>
