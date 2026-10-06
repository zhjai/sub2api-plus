<template>
  <BaseDialog :show="show" :title="t('admin.accounts.prism.catalog.title', { name: account?.name || '' })" width="wide" @close="emit('close')">
    <div class="space-y-5" data-testid="prism-catalog-dialog">
      <div class="flex flex-wrap items-start justify-between gap-3">
        <p class="max-w-prose text-sm text-gray-600 dark:text-gray-300">{{ t('admin.accounts.prism.catalog.intro') }}</p>
        <div class="flex flex-wrap gap-2">
          <button type="button" class="btn btn-secondary" :disabled="loading || refreshingCredentials" data-testid="prism-catalog-refresh" @click="load(true)">
            <Icon name="refresh" size="sm" class="mr-1.5" :class="{ 'animate-spin': loading }" />
            {{ t('admin.accounts.prism.catalog.refresh') }}
          </button>
          <button type="button" class="btn btn-secondary" :disabled="loading || refreshingCredentials" data-testid="prism-credentials-refresh" @click="refreshCredentials">
            <Icon name="key" size="sm" class="mr-1.5" :class="{ 'animate-pulse': refreshingCredentials }" />
            {{ refreshingCredentials ? t('admin.accounts.prism.catalog.refreshingCredentials') : t('admin.accounts.prism.catalog.refreshCredentials') }}
          </button>
        </div>
      </div>

      <div v-if="loadError" class="rounded-lg border border-red-200 bg-red-50 p-3 text-sm text-red-700 dark:border-red-800 dark:bg-red-900/20 dark:text-red-300" role="alert" data-testid="prism-catalog-error">
        <p>{{ loadError }}</p>
        <button type="button" class="mt-2 text-sm font-medium underline-offset-2 hover:underline" @click="emit('relogin', account!)">
          {{ t('admin.accounts.prism.catalog.signInAgain') }}
        </button>
      </div>

      <!-- Catalog -->
      <section :aria-busy="loading">
        <div class="mb-2 flex items-baseline justify-between gap-2">
          <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.accounts.prism.catalog.modelsHeading') }}</h3>
          <span v-if="loadedAt" class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.prism.catalog.loadedAt', { time: loadedAt }) }}</span>
        </div>
        <div v-if="loading && !models.length" class="space-y-2">
          <div v-for="i in 3" :key="i" class="h-10 animate-pulse rounded-lg bg-gray-100 dark:bg-dark-700"></div>
        </div>
        <p v-else-if="!models.length && !loadError" class="rounded-lg border border-dashed border-gray-300 px-4 py-6 text-center text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400">
          {{ t('admin.accounts.prism.catalog.empty') }}
        </p>
        <div v-else-if="models.length" class="overflow-hidden rounded-lg border border-gray-200 dark:border-dark-600">
          <table class="w-full text-sm">
            <thead class="bg-gray-50 text-left text-xs text-gray-500 dark:bg-dark-800 dark:text-gray-400">
              <tr>
                <th class="px-3 py-2 font-medium">{{ t('admin.accounts.prism.catalog.model') }}</th>
                <th class="px-3 py-2 font-medium">{{ t('admin.accounts.prism.catalog.efforts') }}</th>
              </tr>
            </thead>
            <tbody class="divide-y divide-gray-100 dark:divide-dark-700">
              <tr v-for="model in models" :key="model.id" data-testid="prism-catalog-row">
                <td class="px-3 py-2 align-top">
                  <span class="block font-mono text-xs text-gray-900 dark:text-gray-100">{{ model.id }}</span>
                  <span v-if="model.label && model.label !== model.id" class="block text-xs text-gray-500 dark:text-gray-400">{{ model.label }}</span>
                </td>
                <td class="px-3 py-2 align-top">
                  <div v-if="model.reasoning_efforts.length" class="flex flex-wrap gap-1">
                    <span
                      v-for="effort in model.reasoning_efforts"
                      :key="effort"
                      class="rounded px-1.5 py-0.5 text-xs"
                      :class="effort === model.default_reasoning_effort
                        ? 'bg-fuchsia-100 font-medium text-fuchsia-800 dark:bg-fuchsia-900/40 dark:text-fuchsia-200'
                        : 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'"
                      :title="effort === model.default_reasoning_effort ? t('admin.accounts.prism.catalog.defaultEffort') : undefined"
                    >{{ effort }}</span>
                  </div>
                  <span v-else class="text-xs text-gray-400">{{ t('admin.accounts.prism.catalog.noEfforts') }}</span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
        <p v-if="models.length" class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.prism.catalog.effortNote') }}</p>
      </section>

      <!-- Aliases -->
      <section class="space-y-3 border-t border-gray-200 pt-4 dark:border-dark-600" data-testid="prism-aliases">
        <div>
          <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.accounts.prism.aliases.heading') }}</h3>
          <p class="mt-1 max-w-prose text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.prism.aliases.intro') }}</p>
        </div>
        <p v-if="aliasRows.length" class="rounded-lg bg-amber-50 px-3 py-2 text-xs text-amber-800 dark:bg-amber-900/20 dark:text-amber-300" data-testid="prism-alias-restrict">
          {{ t('admin.accounts.prism.aliases.restrictNote') }}
        </p>
        <div v-for="(row, index) in aliasRows" :key="index" class="space-y-1" data-testid="prism-alias-row">
          <div class="flex items-center gap-2">
            <input
              v-model="row.alias"
              type="text"
              class="input min-w-0 flex-1 font-mono text-xs"
              :placeholder="t('admin.accounts.prism.aliases.aliasPlaceholder')"
              :aria-label="t('admin.accounts.prism.aliases.alias')"
              :disabled="saving"
            />
            <Icon name="arrowRight" size="sm" class="shrink-0 text-gray-400" />
            <select v-model="row.target" class="input min-w-0 flex-1 font-mono text-xs" :aria-label="t('admin.accounts.prism.aliases.target')" :disabled="saving">
              <option value="" disabled>{{ t('admin.accounts.prism.aliases.targetPlaceholder') }}</option>
              <option v-if="row.target && !catalogIds.has(row.target)" :value="row.target">{{ t('admin.accounts.prism.aliases.missingOption', { model: row.target }) }}</option>
              <option v-for="model in models" :key="model.id" :value="model.id">{{ model.id }}</option>
            </select>
            <button type="button" class="btn btn-secondary px-2" :disabled="saving" :aria-label="t('admin.accounts.prism.aliases.remove')" @click="aliasRows.splice(index, 1)">
              <Icon name="trash" size="sm" />
            </button>
          </div>
          <p v-if="aliasIssues[index]" class="text-xs text-red-600 dark:text-red-400">{{ t(`admin.accounts.prism.aliases.issues.${aliasIssues[index]}`) }}</p>
        </div>
        <div class="flex flex-wrap items-center gap-2">
          <button type="button" class="btn btn-secondary" :disabled="saving || !models.length" data-testid="prism-alias-add" @click="aliasRows.push({ alias: '', target: '' })">
            <Icon name="plus" size="sm" class="mr-1.5" />
            {{ t('admin.accounts.prism.aliases.add') }}
          </button>
          <button
            v-if="aliasRows.length && missingDirectNames.length"
            type="button"
            class="btn btn-secondary"
            :disabled="saving"
            data-testid="prism-alias-keep-direct"
            @click="addDirectNames"
          >
            {{ t('admin.accounts.prism.aliases.keepDirect', { count: missingDirectNames.length }) }}
          </button>
        </div>
      </section>
    </div>

    <template #footer>
      <div class="flex justify-end gap-3">
        <button type="button" class="btn btn-secondary" :disabled="saving" @click="emit('close')">{{ t('common.close') }}</button>
        <button
          type="button"
          class="btn btn-primary"
          :disabled="saving || !aliasesDirty || hasAliasIssues"
          data-testid="prism-alias-save"
          @click="saveAliases"
        >
          {{ saving ? t('common.saving') : t('admin.accounts.prism.aliases.save') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { adminAPI } from '@/api/admin'
import { prismAPI, type PrismCatalogModel } from '@/api/admin/prism'
import { useAppStore } from '@/stores/app'
import { formatDateTime } from '@/utils/format'
import type { Account } from '@/types'
import {
  aliasRowIssues,
  aliasRowsFromMapping,
  isRequestCanceled,
  mappingFromAliasRows,
  prismErrorText,
  prismRequestErrorText,
  sameMapping,
  type PrismAliasRow
} from './prismText'

const props = defineProps<{
  show: boolean
  account: Account | null
}>()

const emit = defineEmits<{
  close: []
  updated: [account: Account]
  relogin: [account: Account]
}>()

const { t, te } = useI18n()
const appStore = useAppStore()

const models = ref<PrismCatalogModel[]>([])
const loading = ref(false)
const loadError = ref('')
const loadedAt = ref('')
const refreshingCredentials = ref(false)
const saving = ref(false)
const aliasRows = ref<PrismAliasRow[]>([])
const savedMapping = ref<Record<string, string>>({})
let controller: AbortController | null = null

const catalogIds = computed(() => new Set(models.value.map((model) => model.id)))
const aliasIssues = computed(() => aliasRowIssues(aliasRows.value, catalogIds.value))
const hasAliasIssues = computed(() => aliasIssues.value.some(Boolean))
const aliasesDirty = computed(() => !sameMapping(mappingFromAliasRows(aliasRows.value), savedMapping.value))
const missingDirectNames = computed(() => {
  const aliases = new Set(aliasRows.value.map((row) => row.alias.trim()))
  return models.value.filter((model) => !aliases.has(model.id)).map((model) => model.id)
})

function resetAliases(account: Account | null) {
  const mapping = account?.credentials?.model_mapping
  aliasRows.value = aliasRowsFromMapping(mapping)
  savedMapping.value = mappingFromAliasRows(aliasRows.value)
}

async function load(refresh = false) {
  if (!props.account) return
  controller?.abort()
  controller = new AbortController()
  loading.value = true
  loadError.value = ''
  try {
    const catalog = await prismAPI.getAccountModels(props.account.id, { refresh, signal: controller.signal })
    models.value = catalog.models
    loadedAt.value = formatDateTime(new Date())
  } catch (error) {
    if (isRequestCanceled(error)) return
    // Keep the last loaded list visible but never present it as current.
    loadError.value = prismRequestErrorText(t, te, error)
  } finally {
    loading.value = false
  }
}

async function refreshCredentials() {
  if (!props.account || refreshingCredentials.value) return
  refreshingCredentials.value = true
  try {
    const result = await prismAPI.refreshAccount(props.account.id)
    if (result.code) {
      appStore.showError(prismErrorText(t, te, result.code, result.message))
      return
    }
    appStore.showSuccess(t('admin.accounts.prism.catalog.credentialsRefreshed'))
    await load(true)
  } catch (error) {
    appStore.showError(prismRequestErrorText(t, te, error))
  } finally {
    refreshingCredentials.value = false
  }
}

function addDirectNames() {
  for (const id of missingDirectNames.value) aliasRows.value.push({ alias: id, target: id })
}

async function saveAliases() {
  if (!props.account || saving.value || hasAliasIssues.value) return
  saving.value = true
  try {
    // Re-read so the update starts from the latest stored (redacted) credentials;
    // the server keeps secret keys that are absent from the payload.
    const latest = await adminAPI.accounts.getById(props.account.id)
    const credentials: Record<string, unknown> = { ...(latest.credentials || {}) }
    const mapping = mappingFromAliasRows(aliasRows.value)
    if (Object.keys(mapping).length) credentials.model_mapping = mapping
    else delete credentials.model_mapping
    const updated = await adminAPI.accounts.update(props.account.id, { credentials })
    savedMapping.value = mapping
    appStore.showSuccess(t('admin.accounts.prism.aliases.saved'))
    emit('updated', updated)
  } catch (error) {
    appStore.showError(prismRequestErrorText(t, te, error))
  } finally {
    saving.value = false
  }
}

watch(
  () => [props.show, props.account?.id] as const,
  ([open]) => {
    if (!open) {
      controller?.abort()
      return
    }
    models.value = []
    loadedAt.value = ''
    resetAliases(props.account)
    void load(false)
  },
  { immediate: true }
)

onBeforeUnmount(() => controller?.abort())
</script>
