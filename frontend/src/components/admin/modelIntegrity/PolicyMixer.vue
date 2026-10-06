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
      <span class="mixer-effect">{{ effectText(policy) }}</span>
      <!-- Every card uses the same bars. A bar reads how strongly a factor
           decides the order (sort key, threshold strictness, custom share),
           never a fixed backend weight; each row's tooltip says which. Strict
           steps and unused factors are stated in words instead of drawn. -->
      <span class="mixer-meters" data-testid="mixer-meters">
        <span
          v-for="meter in metersFor(policy)"
          :key="meter.factor"
          class="mixer-meter"
          :title="roleText(meter)"
          :data-testid="`mixer-meter-${policyKey(policy)}-${meter.factor}`"
          :data-role="meter.role"
        >
          <span class="mixer-meter-label">{{ t(`admin.modelIntegrity.scheduling.policy.factors.${meter.factor}`) }}</span>
          <span
            v-if="TEXT_METER_ROLES.includes(meter.role)"
            class="mixer-meter-mode"
            :class="`mixer-meter-mode-${meter.role}`"
            :data-testid="meter.factor === 'quality' ? `mixer-quality-${policyKey(policy)}` : undefined"
          >{{ modeText(meter) }}</span>
          <span
            v-else
            class="mixer-meter-track"
            role="img"
            :aria-label="t('admin.modelIntegrity.scheduling.policy.meter.aria', { factor: t(`admin.modelIntegrity.scheduling.policy.factors.${meter.factor}`), role: roleText(meter) })"
          >
            <span
              v-for="segment in 4"
              :key="segment"
              class="mixer-segment"
              :class="segment <= meter.level ? `mixer-segment-${meter.factor}` : ''"
            />
          </span>
        </span>
      </span>
    </label>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import type { OpenAIEvalPolicyWeights, OpenAIEvalSchedulingPolicy, OpenAIEvalSchedulingThresholds } from '@/api/admin/accounts'
import { POLICIES, TEXT_METER_ROLES, absolutePriorities, errorRatePercentText, policyKey, policyMeters, type PolicyMeter } from '@/views/admin/modelIntegrity/modelIntegrity'

const props = withDefaults(defineProps<{
  modelValue: OpenAIEvalSchedulingPolicy
  name: string
  label: string
  disabled?: boolean
  /** The default custom weights, so the custom balance card describes its real order. */
  customBalance?: OpenAIEvalPolicyWeights | null
  /** The edited runtime thresholds, so preset bars follow how strict they are. */
  thresholds?: OpenAIEvalSchedulingThresholds | null
}>(), { disabled: false, customBalance: null, thresholds: null })

const emit = defineEmits<{ (e: 'update:modelValue', value: OpenAIEvalSchedulingPolicy): void }>()
const { t } = useI18n()

function metersFor(policy: OpenAIEvalSchedulingPolicy) {
  return policyMeters(policy, props.customBalance, props.thresholds)
}

/** The visible words for a factor that is a strict step or unused. */
function modeText(meter: PolicyMeter) {
  if (meter.role === 'priority') return t('admin.modelIntegrity.scheduling.policy.custom.priorities.badge', { n: meter.value })
  return t(`admin.modelIntegrity.scheduling.policy.qualityMode.${meter.role}`)
}

/** What the bar means for this factor under this policy, as the row tooltip. */
function roleText(meter: PolicyMeter) {
  const key = `admin.modelIntegrity.scheduling.policy.meter.${meter.role}`
  if (meter.role.startsWith('gate_')) {
    const limit = meter.factor === 'errors'
      ? t('admin.modelIntegrity.scheduling.policy.meter.percent', { value: errorRatePercentText(meter.value ?? 0) })
      : t('admin.modelIntegrity.scheduling.policy.meter.seconds', { value: meter.value })
    return t(key, { limit })
  }
  return t(key, { n: meter.value, share: meter.value })
}

/** With priorities, custom balance is no longer a pure weighted sort, so it says so. */
function effectText(policy: OpenAIEvalSchedulingPolicy) {
  const key = policyKey(policy)
  if (policy === 'custom_balance' && absolutePriorities(props.customBalance).length) {
    return t(`admin.modelIntegrity.scheduling.policy.options.${key}.effectWithPriorities`)
  }
  return t(`admin.modelIntegrity.scheduling.policy.options.${key}.effect`)
}
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
/* The pass rate tier and custom priorities are strict steps, never a weight price could trade against. */
.mixer-meter-mode-tier { @apply font-semibold text-violet-700 dark:text-violet-300; }
.mixer-meter-mode-priority { @apply font-semibold text-gray-800 dark:text-gray-200; }
.mixer-meter-mode-ignored { @apply text-gray-400 dark:text-gray-500; }
.mixer-segment { @apply rounded-[2px] bg-gray-200 dark:bg-dark-600; }
.mixer-segment-quality { @apply bg-violet-600 dark:bg-violet-400; }
.mixer-segment-price { @apply bg-amber-500 dark:bg-amber-400; }
.mixer-segment-errors { @apply bg-primary-600 dark:bg-primary-500; }
.mixer-segment-speed { @apply bg-sky-600 dark:bg-sky-500; }
.mixer-segment-load { @apply bg-gray-500 dark:bg-gray-400; }
.mixer-option:not(.mixer-option-active) .mixer-segment-quality,
.mixer-option:not(.mixer-option-active) .mixer-segment-price,
.mixer-option:not(.mixer-option-active) .mixer-segment-errors,
.mixer-option:not(.mixer-option-active) .mixer-segment-speed,
.mixer-option:not(.mixer-option-active) .mixer-segment-load { @apply opacity-40; }
@media (prefers-reduced-motion: no-preference) {
  .mixer-segment { transition: opacity 180ms ease, background-color 180ms ease; }
}
</style>
