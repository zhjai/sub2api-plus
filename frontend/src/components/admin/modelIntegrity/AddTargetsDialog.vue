<template>
  <BaseDialog :show="show" :title="t('admin.modelIntegrity.tests.add.title')" width="normal" @close="emit('close')">
    <form id="add-targets-form" class="space-y-4" @submit.prevent="submit">
      <div v-if="hasPrismAccounts" class="source-switch" role="radiogroup" :aria-label="t('admin.modelIntegrity.tests.add.source')">
        <button
          v-for="option in sourceOptions"
          :key="option.value"
          type="button"
          role="radio"
          class="source-option"
          :class="{ 'source-option-active': source === option.value }"
          :aria-checked="source === option.value"
          :data-testid="`add-source-${option.value}`"
          @click="source = option.value"
        >
          <PlatformIcon :platform="option.value" size="sm" />
          {{ option.label }}
        </button>
      </div>

      <fieldset class="space-y-2">
        <legend class="input-label">{{ t('admin.modelIntegrity.tests.add.accounts') }}</legend>
        <p class="text-xs text-gray-500 dark:text-gray-400">
          {{ source === 'prism' ? t('admin.modelIntegrity.tests.add.prismAccountsHint') : t('admin.modelIntegrity.tests.add.accountsHint') }}
        </p>
        <input v-model="query" type="search" class="input" :placeholder="t('admin.modelIntegrity.tests.add.searchAccounts')" :aria-label="t('admin.modelIntegrity.tests.add.searchAccounts')" />
        <div class="pick-list">
          <p v-if="!filtered.length" class="px-3 py-6 text-center text-sm text-gray-500">
            {{ source === 'prism' ? t('admin.modelIntegrity.tests.add.noPrismAccounts') : t('admin.modelIntegrity.tests.add.noAccounts') }}
          </p>
          <label v-for="account in filtered" :key="account.id" class="pick-row">
            <input v-model="selected" type="checkbox" :value="account.id" class="rounded border-gray-300 text-primary-600 focus:ring-primary-500 dark:border-dark-500" data-testid="add-account" />
            <span class="min-w-0 flex-1 truncate">{{ account.name }}</span>
            <span class="text-xs tabular-nums text-gray-400">#{{ account.id }}</span>
            <span v-if="source === 'prism' && selected.includes(account.id)" class="catalog-state" :data-state="catalogOf(account.id)?.status ?? 'loading'" data-testid="add-catalog-state">
              {{ catalogStateText(account.id) }}
            </span>
            <span v-else class="text-xs text-gray-500">{{ account.type }}</span>
          </label>
        </div>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.modelIntegrity.tests.add.selected', { count: selected.length }) }}</p>
      </fieldset>

      <div v-if="source === 'prism' && failedCatalogs.length" class="prism-catalog-error" role="alert" data-testid="add-catalog-error">
        <p>{{ t('admin.modelIntegrity.tests.add.prismCatalogFailed', { accounts: failedCatalogs.map(item => item.name).join(', ') }) }}</p>
        <button type="button" class="font-medium underline-offset-2 hover:underline" data-testid="add-catalog-retry" @click="retryFailed">
          {{ t('admin.modelIntegrity.common.retry') }}
        </button>
      </div>

      <div class="grid gap-3 sm:grid-cols-2">
        <label class="flex flex-col gap-1">
          <span class="input-label">{{ t('admin.modelIntegrity.tests.add.model') }}</span>
          <select v-model="model" class="input" required :disabled="modelsDisabled" data-testid="add-model">
            <option value="" disabled>{{ modelPlaceholder }}</option>
            <option v-for="item in modelOptions" :key="item.id" :value="item.id">{{ item.label }}</option>
          </select>
        </label>
        <label class="flex flex-col gap-1">
          <span class="input-label">{{ t('admin.modelIntegrity.tests.add.effort') }}</span>
          <select v-model="effort" class="input" :disabled="source === 'prism' && !model" data-testid="add-effort">
            <option v-for="value in effortOptions" :key="value" :value="value">{{ effortLabel(value) }}</option>
          </select>
        </label>
      </div>
      <p v-if="source === 'prism' && prismNoSharedModels" class="text-xs text-amber-700 dark:text-amber-300" data-testid="add-no-shared">
        {{ selected.length > 1 ? t('admin.modelIntegrity.tests.add.prismNoSharedModels') : t('admin.modelIntegrity.tests.add.prismNoModels') }}
      </p>
      <p v-if="source === 'prism'" class="text-xs leading-relaxed text-gray-500 dark:text-gray-400" data-testid="add-prism-note">
        {{ t('admin.modelIntegrity.tests.add.prismNote') }}
      </p>
    </form>
    <template #footer>
      <div class="flex justify-end gap-2">
        <button type="button" class="btn btn-secondary" @click="emit('close')">{{ t('admin.modelIntegrity.common.cancel') }}</button>
        <button type="submit" form="add-targets-form" class="btn btn-primary" :disabled="!canSubmit" data-testid="add-submit">
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
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import type { OpenAIEvalModelCatalog } from '@/api/admin/accounts'
import type { PrismCatalogModel } from '@/api/admin/prism'
import type { AccountListItem } from '@/types'
import {
  isPrismAccount,
  sharedPrismDefaultEffort,
  sharedPrismEfforts,
  sharedPrismModels,
  type PrismEvalCatalogs
} from '@/views/admin/modelIntegrity/prismTargets'

type Source = 'openai' | 'prism'

const props = defineProps<{
  show: boolean
  accounts: AccountListItem[]
  catalog: OpenAIEvalModelCatalog | null
  /** Per-account Prism catalogs; omitted where Prism targets are not offered. */
  prismCatalogs?: PrismEvalCatalogs
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'add', payload: { accountIDs: number[]; model: string; effort: string }): void
}>()

const { t } = useI18n()
const source = ref<Source>('openai')
const query = ref('')
const model = ref('')
const effort = ref('')
const selected = ref<number[]>([])

const prismAccounts = computed(() => props.accounts.filter(account => isPrismAccount(account) && account.type === 'oauth'))
const openaiAccounts = computed(() => props.accounts.filter(account => account.platform === 'openai'))
const hasPrismAccounts = computed(() => Boolean(props.prismCatalogs) && prismAccounts.value.length > 0)
const sourceOptions = computed(() => [
  { value: 'openai' as const, label: 'OpenAI' },
  { value: 'prism' as const, label: 'Prism' }
])

const pool = computed(() => (source.value === 'prism' ? prismAccounts.value : openaiAccounts.value))
const filtered = computed(() => {
  const needle = query.value.trim().toLowerCase()
  if (!needle) return pool.value
  return pool.value.filter(account => account.name.toLowerCase().includes(needle) || String(account.id) === needle.replace(/^#/, ''))
})

const catalogOf = (accountID: number) => props.prismCatalogs?.get(accountID)

const selectedCatalogs = computed(() => selected.value.map(id => ({ id, state: catalogOf(id) })))
const prismLoading = computed(() => selectedCatalogs.value.some(item => !item.state || item.state.status === 'loading'))
const failedCatalogs = computed(() => selectedCatalogs.value
  .filter(item => item.state?.status === 'error')
  .map(item => ({ id: item.id, name: props.accounts.find(account => account.id === item.id)?.name ?? `#${item.id}` })))
const readyLists = computed<PrismCatalogModel[][]>(() => selectedCatalogs.value.flatMap(item => (item.state?.status === 'ready' ? [item.state.selectable] : [])))
const prismReady = computed(() => selected.value.length > 0 && !prismLoading.value && failedCatalogs.value.length === 0)
const prismModels = computed(() => (prismReady.value ? sharedPrismModels(readyLists.value) : []))
const prismNoSharedModels = computed(() => prismReady.value && prismModels.value.length === 0)

const modelOptions = computed(() => {
  if (source.value === 'prism') return prismModels.value.map(item => ({ id: item.id, label: item.label && item.label !== item.id ? `${item.id} · ${item.label}` : item.id }))
  return (props.catalog?.items ?? []).map(item => ({ id: item.id, label: item.display_name || item.id }))
})
const modelsDisabled = computed(() => source.value === 'prism' && !prismModels.value.length)
const modelPlaceholder = computed(() => {
  if (source.value !== 'prism') return t('admin.modelIntegrity.tests.add.modelPlaceholder')
  if (!selected.value.length) return t('admin.modelIntegrity.tests.add.prismPickAccountFirst')
  if (prismLoading.value) return t('admin.modelIntegrity.tests.add.prismLoadingModels')
  return t('admin.modelIntegrity.tests.add.modelPlaceholder')
})

const prismDefaultEffort = computed(() => (model.value ? sharedPrismDefaultEffort(readyLists.value, model.value) : ''))
const effortOptions = computed(() => {
  if (source.value === 'prism') return model.value ? sharedPrismEfforts(readyLists.value, model.value) : ['']
  return props.catalog?.reasoning_efforts?.length ? props.catalog.reasoning_efforts : ['']
})
function effortLabel(value: string) {
  if (value) return value
  if (source.value === 'prism' && prismDefaultEffort.value) {
    return t('admin.modelIntegrity.tests.add.prismDefaultEffort', { effort: prismDefaultEffort.value })
  }
  return t('admin.modelIntegrity.common.defaultEffort')
}

function catalogStateText(accountID: number) {
  const state = catalogOf(accountID)
  if (!state || state.status === 'loading') return t('admin.modelIntegrity.tests.add.prismCatalogLoading')
  if (state.status === 'error') return t('admin.modelIntegrity.tests.add.prismCatalogError')
  return t('admin.modelIntegrity.tests.add.prismCatalogCount', { count: state.selectable.length })
}

function retryFailed() {
  for (const item of failedCatalogs.value) void props.prismCatalogs?.reload(item.id)
}

const canSubmit = computed(() => {
  if (!model.value || !selected.value.length) return false
  if (source.value !== 'prism') return true
  return prismReady.value && prismModels.value.some(item => item.id === model.value) && effortOptions.value.includes(effort.value)
})

watch(selected, ids => {
  if (source.value !== 'prism') return
  for (const id of ids) void props.prismCatalogs?.ensure(id)
})

// A choice that no longer fits the selected accounts is cleared, never swapped for another model.
watch(modelOptions, options => {
  if (model.value && !options.some(item => item.id === model.value)) model.value = ''
})
watch(effortOptions, options => {
  if (!options.includes(effort.value)) effort.value = options[0] ?? ''
})

watch(source, () => {
  selected.value = []
  query.value = ''
  model.value = ''
  effort.value = effortOptions.value[0] ?? ''
})

watch(() => props.show, open => {
  if (!open) return
  source.value = 'openai'
  query.value = ''
  selected.value = []
  model.value = ''
  effort.value = effortOptions.value[0] ?? ''
})

function submit() {
  if (!canSubmit.value) return
  emit('add', { accountIDs: [...selected.value], model: model.value, effort: effort.value })
}
</script>

<style scoped>
.pick-list { @apply max-h-64 divide-y divide-gray-100 overflow-y-auto rounded-lg border border-gray-200 dark:divide-dark-700 dark:border-dark-600; }
.pick-row { @apply flex cursor-pointer items-center gap-3 px-3 py-2 text-sm text-gray-800 hover:bg-gray-50 dark:text-gray-200 dark:hover:bg-dark-700/50; }
.source-switch { @apply flex rounded-lg bg-gray-100 p-1 dark:bg-dark-700; }
.source-option { @apply flex flex-1 items-center justify-center gap-2 rounded-md px-3 py-1.5 text-sm font-medium text-gray-600 hover:text-gray-900 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:text-gray-400 dark:hover:text-gray-200; }
.source-option-active { @apply bg-white text-gray-900 shadow-sm dark:bg-dark-600 dark:text-white; }
.catalog-state { @apply shrink-0 text-xs text-gray-500 dark:text-gray-400; }
.catalog-state[data-state='error'] { @apply text-rose-600 dark:text-rose-400; }
.catalog-state[data-state='ready'] { @apply text-fuchsia-700 dark:text-fuchsia-300; }
.prism-catalog-error { @apply flex flex-wrap items-center justify-between gap-2 rounded-lg border border-rose-200 bg-rose-50 px-3 py-2 text-sm text-rose-700 dark:border-rose-800 dark:bg-rose-950/30 dark:text-rose-300; }
</style>
