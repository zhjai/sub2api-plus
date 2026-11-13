<template>
  <span
    v-if="isBPS"
    data-testid="account-bps-badge"
    class="inline-flex items-center rounded border border-emerald-200 bg-emerald-50 px-1.5 py-0.5 text-[10px] font-semibold leading-4 text-emerald-700 dark:border-emerald-800/60 dark:bg-emerald-900/30 dark:text-emerald-300"
    :title="t('admin.accounts.bpsAccount.badgeTitle')"
  >
    {{ t('admin.accounts.bpsAccount.badge') }}
  </span>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { isOpenAIBPSAccount } from '@/utils/openaiOAuthClient'

const props = defineProps<{
  account: { platform?: string; type?: string; credentials?: Record<string, unknown> | null } | null | undefined
}>()

const { t } = useI18n()

const isBPS = computed(() => isOpenAIBPSAccount(props.account))
</script>
