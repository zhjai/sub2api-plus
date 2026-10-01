<template>
  <BaseDialog :show="show" :title="t('admin.modelIntegrity.tests.add.title')" width="normal" @close="emit('close')">
    <form id="add-targets-form" class="space-y-4" @submit.prevent="submit">
      <div class="grid gap-3 sm:grid-cols-2">
        <label class="flex flex-col gap-1">
          <span class="input-label">{{ t('admin.modelIntegrity.tests.add.model') }}</span>
          <select v-model="model" class="input" required data-testid="add-model">
            <option value="" disabled>{{ t('admin.modelIntegrity.tests.add.modelPlaceholder') }}</option>
            <option v-for="item in catalog?.items || []" :key="item.id" :value="item.id">{{ item.display_name || item.id }}</option>
          </select>
        </label>
        <label class="flex flex-col gap-1">
          <span class="input-label">{{ t('admin.modelIntegrity.tests.add.effort') }}</span>
          <select v-model="effort" class="input" data-testid="add-effort">
            <option v-for="value in efforts" :key="value" :value="value">{{ value || t('admin.modelIntegrity.common.defaultEffort') }}</option>
          </select>
        </label>
      </div>

      <fieldset class="space-y-2">
        <legend class="input-label">{{ t('admin.modelIntegrity.tests.add.accounts') }}</legend>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.modelIntegrity.tests.add.accountsHint') }}</p>
        <input v-model="query" type="search" class="input" :placeholder="t('admin.modelIntegrity.tests.add.searchAccounts')" :aria-label="t('admin.modelIntegrity.tests.add.searchAccounts')" />
        <div class="pick-list">
          <p v-if="!filtered.length" class="px-3 py-6 text-center text-sm text-gray-500">{{ t('admin.modelIntegrity.tests.add.noAccounts') }}</p>
          <label v-for="account in filtered" :key="account.id" class="pick-row">
            <input v-model="selected" type="checkbox" :value="account.id" class="rounded border-gray-300 text-primary-600 focus:ring-primary-500 dark:border-dark-500" data-testid="add-account" />
            <span class="min-w-0 flex-1 truncate">{{ account.name }}</span>
            <span class="text-xs tabular-nums text-gray-400">#{{ account.id }}</span>
            <span class="text-xs text-gray-500">{{ account.type }}</span>
          </label>
        </div>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.modelIntegrity.tests.add.selected', { count: selected.length }) }}</p>
      </fieldset>
    </form>
    <template #footer>
      <div class="flex justify-end gap-2">
        <button type="button" class="btn btn-secondary" @click="emit('close')">{{ t('admin.modelIntegrity.common.cancel') }}</button>
        <button type="submit" form="add-targets-form" class="btn btn-primary" :disabled="!model || !selected.length" data-testid="add-submit">
          {{ t('admin.modelIntegrity.tests.add.submit', { count: selected.length }) }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import type { OpenAIEvalModelCatalog } from '@/api/admin/accounts'
import type { AccountListItem } from '@/types'

const props = defineProps<{
  show: boolean
  accounts: AccountListItem[]
  catalog: OpenAIEvalModelCatalog | null
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'add', payload: { accountIDs: number[]; model: string; effort: string }): void
}>()

const { t } = useI18n()
const query = ref('')
const model = ref('')
const effort = ref('')
const selected = ref<number[]>([])

const efforts = computed(() => props.catalog?.reasoning_efforts?.length ? props.catalog.reasoning_efforts : [''])
const filtered = computed(() => {
  const needle = query.value.trim().toLowerCase()
  if (!needle) return props.accounts
  return props.accounts.filter(account => account.name.toLowerCase().includes(needle) || String(account.id) === needle.replace(/^#/, ''))
})

watch(() => props.show, open => {
  if (!open) return
  query.value = ''
  selected.value = []
  effort.value = efforts.value[0] ?? ''
})

function submit() {
  if (!model.value || !selected.value.length) return
  emit('add', { accountIDs: [...selected.value], model: model.value, effort: effort.value })
}
</script>

<style scoped>
.pick-list { @apply max-h-64 divide-y divide-gray-100 overflow-y-auto rounded-lg border border-gray-200 dark:divide-dark-700 dark:border-dark-600; }
.pick-row { @apply flex cursor-pointer items-center gap-3 px-3 py-2 text-sm text-gray-800 hover:bg-gray-50 dark:text-gray-200 dark:hover:bg-dark-700/50; }
</style>
