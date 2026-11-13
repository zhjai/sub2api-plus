<template>
  <div
    class="space-y-4 border-t border-gray-200 pt-4 dark:border-dark-600"
    data-testid="openai-bps-options-section"
  >
    <div>
      <h4 class="input-label mb-1">{{ t('admin.accounts.bpsAccount.options.sectionTitle') }}</h4>
      <div
        class="rounded-lg border border-emerald-200/80 bg-emerald-50/70 px-3 py-2 text-xs leading-relaxed text-emerald-800 dark:border-emerald-800/50 dark:bg-emerald-950/30 dark:text-emerald-200"
        data-testid="openai-bps-client-tools-hint"
      >
        {{ t('admin.accounts.bpsAccount.options.clientToolsHint') }}
      </div>
    </div>

    <!-- extra.openai_bps_skip_historical_encrypted -->
    <div class="rounded-lg border border-gray-200 bg-gray-50/60 p-3.5 dark:border-dark-600 dark:bg-dark-700/40">
      <div class="flex items-start justify-between gap-4">
        <div class="min-w-0">
          <label
            :for="`${idPrefix}-bps-skip-historical-encrypted`"
            class="input-label mb-0 cursor-pointer"
          >
            {{ t('admin.accounts.bpsAccount.options.skipHistoricalEncrypted') }}
          </label>
          <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.bpsAccount.options.skipHistoricalEncryptedDesc') }}
          </p>
        </div>
        <button
          :id="`${idPrefix}-bps-skip-historical-encrypted`"
          type="button"
          role="switch"
          :aria-checked="skipHistoricalEncrypted"
          data-testid="openai-bps-skip-historical-encrypted-toggle"
          :class="[
            'relative inline-flex h-6 w-11 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2',
            skipHistoricalEncrypted ? 'bg-primary-600' : 'bg-gray-200 dark:bg-dark-600'
          ]"
          @click="emit('update:skipHistoricalEncrypted', !skipHistoricalEncrypted)"
        >
          <span
            :class="[
              'pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out',
              skipHistoricalEncrypted ? 'translate-x-5' : 'translate-x-0'
            ]"
          />
        </button>
      </div>
      <div
        class="mt-2.5 rounded-md border border-amber-200 bg-amber-50/90 px-3 py-2 text-xs leading-relaxed text-amber-800 dark:border-amber-800/60 dark:bg-amber-900/20 dark:text-amber-300"
        data-testid="openai-bps-skip-historical-encrypted-warning"
      >
        {{ t('admin.accounts.bpsAccount.options.skipHistoricalEncryptedWarning') }}
      </div>
    </div>

    <!-- extra.openai_bps_hosted_tools_policy -->
    <div class="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
      <div class="min-w-0">
        <label class="input-label mb-0">
          {{ t('admin.accounts.bpsAccount.options.hostedToolsPolicy') }}
        </label>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
          {{ t('admin.accounts.bpsAccount.options.hostedToolsPolicyDesc') }}
        </p>
      </div>
      <div class="w-full flex-shrink-0 sm:w-52">
        <Select
          :model-value="hostedToolsPolicy"
          :options="hostedToolsPolicyOptions"
          data-testid="openai-bps-hosted-tools-policy-select"
          @update:model-value="onPolicyChange"
        />
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import type { OpenAIBPSHostedToolsPolicy } from '@/utils/openaiBPS'

withDefaults(
  defineProps<{
    skipHistoricalEncrypted: boolean
    hostedToolsPolicy: OpenAIBPSHostedToolsPolicy
    idPrefix?: string
  }>(),
  {
    idPrefix: 'account'
  }
)

const emit = defineEmits<{
  'update:skipHistoricalEncrypted': [value: boolean]
  'update:hostedToolsPolicy': [value: OpenAIBPSHostedToolsPolicy]
}>()

const { t } = useI18n()

const hostedToolsPolicyOptions = computed(() => [
  {
    value: 'omit' as OpenAIBPSHostedToolsPolicy,
    label: t('admin.accounts.bpsAccount.options.hostedToolsOmit')
  },
  {
    value: 'reject' as OpenAIBPSHostedToolsPolicy,
    label: t('admin.accounts.bpsAccount.options.hostedToolsReject')
  }
])

const onPolicyChange = (value: string | number | boolean | null) => {
  emit('update:hostedToolsPolicy', value === 'reject' ? 'reject' : 'omit')
}
</script>
