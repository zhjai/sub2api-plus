<template>
  <BaseDialog :show="route !== null" :title="t('admin.modelIntegrity.tests.edit.title')" width="normal" @close="emit('close')">
    <form v-if="route" id="edit-target-form" class="space-y-4" @submit.prevent="submit">
      <div>
        <span class="input-label">{{ t('admin.modelIntegrity.tests.edit.account') }}</span>
        <p class="text-sm text-gray-900 dark:text-gray-100" data-testid="edit-account">{{ accountLabel }}</p>
      </div>
      <div v-if="prism && prismState?.status !== 'ready'" class="text-xs" :class="prismState?.status === 'error' ? 'text-rose-600 dark:text-rose-400' : 'text-gray-500 dark:text-gray-400'" role="status" data-testid="edit-prism-catalog">
        <template v-if="prismState?.status === 'error'">
          {{ t('admin.modelIntegrity.tests.edit.prismCatalogFailed') }}
          <button type="button" class="ml-1 font-medium underline-offset-2 hover:underline" @click="emit('reload-prism')">{{ t('admin.modelIntegrity.common.retry') }}</button>
        </template>
        <template v-else>{{ t('admin.modelIntegrity.tests.add.prismLoadingModels') }}</template>
      </div>
      <div class="grid gap-3 sm:grid-cols-2">
        <label class="flex flex-col gap-1">
          <span class="input-label">{{ t('admin.modelIntegrity.tests.add.model') }}</span>
          <select v-model="model" class="input" required data-testid="edit-model">
            <option v-for="item in models" :key="item.id" :value="item.id">{{ item.label }}</option>
          </select>
        </label>
        <label class="flex flex-col gap-1">
          <span class="input-label">{{ t('admin.modelIntegrity.tests.add.effort') }}</span>
          <select v-model="effort" class="input" data-testid="edit-effort">
            <option v-for="value in efforts" :key="value" :value="value">{{ value || t('admin.modelIntegrity.common.defaultEffort') }}</option>
          </select>
        </label>
      </div>

      <p v-if="duplicate" class="input-error-text" role="alert" data-testid="edit-duplicate">
        {{ t('admin.modelIntegrity.tests.edit.duplicate', { target: `${model} · ${effort || t('admin.modelIntegrity.common.defaultEffort')}` }) }}
      </p>
      <p v-if="explainsStateProbe" class="text-xs leading-relaxed text-gray-600 dark:text-gray-300" data-testid="edit-state-probe">{{ t('admin.modelIntegrity.tests.edit.stateProbeDefault') }}</p>
      <p class="text-xs leading-relaxed text-gray-500 dark:text-gray-400">{{ t('admin.modelIntegrity.tests.edit.hint') }}</p>
    </form>
    <template #footer>
      <div class="flex justify-end gap-2">
        <button type="button" class="btn btn-secondary" data-testid="edit-cancel" @click="emit('close')">{{ t('admin.modelIntegrity.common.cancel') }}</button>
        <button type="submit" form="edit-target-form" class="btn btn-primary" :disabled="!canSubmit" data-testid="edit-submit">
          {{ t('admin.modelIntegrity.tests.edit.submit') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import type { OpenAIEvalModelCatalog, OpenAIEvalRouteConfig } from '@/api/admin/accounts'
import { routeKey } from '@/views/admin/modelIntegrity/modelIntegrity'
import type { PrismCatalogState } from '@/views/admin/modelIntegrity/prismTargets'

const props = defineProps<{
  route: OpenAIEvalRouteConfig | null
  routes: OpenAIEvalRouteConfig[]
  catalog: OpenAIEvalModelCatalog | null
  accountLabel: string
  /** Set for a Prism target: its account catalog replaces the OpenAI list. */
  prism?: boolean
  prismState?: PrismCatalogState
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'save', payload: { model: string; effort: string }): void
  (e: 'reload-prism'): void
}>()

const { t } = useI18n()
const model = ref('')
const effort = ref('')

// A saved target may use a model or effort the current catalog no longer
// lists; keep it selectable so opening the dialog never changes it silently.
const prismModels = computed(() => (props.prismState?.status === 'ready' ? props.prismState.selectable : []))
const models = computed(() => {
  const items = props.prism
    ? prismModels.value.map(item => ({ id: item.id, label: item.label && item.label !== item.id ? `${item.id} · ${item.label}` : item.id }))
    : (props.catalog?.items ?? []).map(item => ({ id: item.id, label: item.display_name || item.id }))
  const current = props.route?.requested_model
  if (current && !items.some(item => item.id === current)) {
    const missing = props.prism && props.prismState?.status === 'ready'
    items.unshift({ id: current, label: missing ? t('admin.modelIntegrity.tests.edit.prismNotInCatalog', { model: current }) : current })
  }
  return items
})
const efforts = computed(() => {
  let values: string[]
  if (props.prism) {
    const entry = prismModels.value.find(item => item.id === model.value)
    values = ['', ...(entry?.reasoning_efforts ?? [])]
  } else {
    values = props.catalog?.reasoning_efforts?.length ? [...props.catalog.reasoning_efforts] : ['']
  }
  const current = props.route?.reasoning_effort ?? ''
  if (!values.includes(current) && model.value === props.route?.requested_model) values.push(current)
  return values
})
watch(efforts, values => { if (!values.includes(effort.value)) effort.value = '' })

const nextKey = computed(() => props.route ? routeKey({ account_id: props.route.account_id, requested_model: model.value, reasoning_effort: effort.value }) : '')
const unchanged = computed(() => Boolean(props.route) && nextKey.value === routeKey(props.route!))
const duplicate = computed(() => !unchanged.value && props.routes.some(item => item !== props.route && routeKey(item) === nextKey.value))
/** State Probe ignores the target effort and keeps its schedule; say so when an explicit effort is chosen. */
const explainsStateProbe = computed(() => Boolean(props.route?.state_probe_schedule.enabled) && props.route?.direct_oauth_eligible === true && effort.value !== '')
const canSubmit = computed(() => Boolean(model.value) && !unchanged.value && !duplicate.value)

watch(() => props.route, route => {
  if (!route) return
  model.value = route.requested_model
  effort.value = route.reasoning_effort ?? ''
}, { immediate: true })

function submit() {
  if (!canSubmit.value) return
  emit('save', { model: model.value, effort: effort.value })
}
</script>
