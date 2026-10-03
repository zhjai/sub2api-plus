<template>
  <div class="mixer" role="radiogroup" :aria-label="label">
    <label
      v-for="policy in POLICIES"
      :key="policyKey(policy)"
      class="mixer-option"
      :class="{ 'mixer-option-active': modelValue === policy, 'mixer-option-disabled': disabled }"
    >
      <input
        type="radio"
        class="sr-only"
        :name="name"
        :value="policy"
        :checked="modelValue === policy"
        :disabled="disabled"
        :data-testid="`policy-${policyKey(policy)}`"
        @change="emit('update:modelValue', policy)"
      />
      <span class="mixer-name">
        <span class="mixer-radio" aria-hidden="true" />
        {{ t(`admin.modelIntegrity.scheduling.policy.options.${policyKey(policy)}.name`) }}
      </span>
      <span class="mixer-effect">{{ t(`admin.modelIntegrity.scheduling.policy.options.${policyKey(policy)}.effect`) }}</span>
      <span class="mixer-meters">
        <span v-for="factor in FACTORS" :key="factor" class="mixer-meter">
          <span class="mixer-meter-label">{{ t(`admin.modelIntegrity.scheduling.policy.factors.${factor}`) }}</span>
          <!-- Quality is a strict first filter under avoid degradation and unused by the presets, so it is stated, not metered. -->
          <span
            v-if="factor === 'quality' && QUALITY_MODE[policy] !== 'weighted'"
            class="mixer-meter-mode"
            :class="`mixer-meter-mode-${QUALITY_MODE[policy]}`"
            :data-testid="`mixer-quality-${policyKey(policy)}`"
          >{{ t(`admin.modelIntegrity.scheduling.policy.qualityMode.${QUALITY_MODE[policy]}`) }}</span>
          <span
            v-else
            class="mixer-meter-track"
            role="img"
            :aria-label="t('admin.modelIntegrity.scheduling.policy.levelAria', { factor: t(`admin.modelIntegrity.scheduling.policy.factors.${factor}`), level: POLICY_EMPHASIS[policy][factor] })"
          >
            <span
              v-for="segment in 4"
              :key="segment"
              class="mixer-segment"
              :class="segment <= POLICY_EMPHASIS[policy][factor] ? `mixer-segment-${factor}` : ''"
            />
          </span>
        </span>
      </span>
    </label>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { OpenAIEvalSchedulingPolicy } from '@/api/admin/accounts'
import { POLICIES, POLICY_EMPHASIS, QUALITY_MODE, policyKey, type PolicyFactor } from '@/views/admin/modelIntegrity/modelIntegrity'

withDefaults(defineProps<{
  modelValue: OpenAIEvalSchedulingPolicy
  name: string
  label: string
  disabled?: boolean
}>(), { disabled: false })

const emit = defineEmits<{ (e: 'update:modelValue', value: OpenAIEvalSchedulingPolicy): void }>()
const { t } = useI18n()
const FACTORS: PolicyFactor[] = ['quality', 'price', 'errors', 'speed']
</script>

<style scoped>
.mixer { @apply grid gap-2 sm:grid-cols-2 xl:grid-cols-4; }
.mixer-option { @apply relative flex cursor-pointer flex-col gap-2 rounded-lg border border-gray-200 bg-white p-4 transition-colors hover:border-gray-300 dark:border-dark-600 dark:bg-dark-800 dark:hover:border-dark-500; }
.mixer-option:focus-within { @apply ring-2 ring-primary-500 ring-offset-2 dark:ring-offset-dark-900; }
.mixer-option-active { @apply border-primary-600 bg-primary-50/40 hover:border-primary-600 dark:border-primary-500 dark:bg-primary-950/30; box-shadow: inset 0 0 0 1px theme('colors.primary.600'); }
.mixer-option-disabled { @apply cursor-not-allowed opacity-60; }
.mixer-name { @apply flex items-center gap-2 text-[0.9375rem] font-semibold text-gray-900 dark:text-white; }
.mixer-radio { @apply inline-block h-3.5 w-3.5 shrink-0 rounded-full border-2 border-gray-300 dark:border-dark-500; }
.mixer-option-active .mixer-radio { @apply border-primary-600 dark:border-primary-400; box-shadow: inset 0 0 0 2px white; background: theme('colors.primary.600'); }
:global(.dark) .mixer-option-active .mixer-radio { box-shadow: inset 0 0 0 2px theme('colors.dark.800'); }
.mixer-effect { @apply min-h-[2.75rem] text-[0.8125rem] leading-relaxed text-gray-600 dark:text-gray-400; }
.mixer-meters { @apply mt-auto grid gap-1.5 border-t border-gray-100 pt-3 dark:border-dark-700; }
.mixer-meter { @apply grid grid-cols-[5.5rem_1fr] items-center gap-2 text-xs text-gray-500 dark:text-gray-400; }
.mixer-meter-track { @apply grid h-2 grid-cols-4 gap-0.5; }
.mixer-meter-mode { @apply text-xs leading-none; }
.mixer-meter-mode-tier { @apply font-semibold text-violet-700 dark:text-violet-300; }
.mixer-meter-mode-ignored { @apply text-gray-400 dark:text-gray-500; }
.mixer-segment { @apply rounded-[2px] bg-gray-200 dark:bg-dark-600; }
.mixer-segment-quality { @apply bg-violet-600 dark:bg-violet-400; }
.mixer-segment-price { @apply bg-amber-500 dark:bg-amber-400; }
.mixer-segment-errors { @apply bg-primary-600 dark:bg-primary-500; }
.mixer-segment-speed { @apply bg-sky-600 dark:bg-sky-500; }
.mixer-option:not(.mixer-option-active) .mixer-segment-quality,
.mixer-option:not(.mixer-option-active) .mixer-segment-price,
.mixer-option:not(.mixer-option-active) .mixer-segment-errors,
.mixer-option:not(.mixer-option-active) .mixer-segment-speed { @apply opacity-40; }
@media (prefers-reduced-motion: no-preference) {
  .mixer-segment { transition: opacity 180ms ease, background-color 180ms ease; }
}
</style>
