<template>
  <fieldset class="weights">
    <legend class="sr-only">{{ legend }}</legend>

    <!-- Strict comparisons come before any weight, so they sit above the weights.
         The order is a real sequence: the numbers are the comparison order. -->
    <div class="priorities" role="group" :aria-labelledby="prioritiesTitleId" :aria-describedby="prioritiesRulesId" data-testid="priority-editor">
      <div class="priorities-head">
        <span :id="prioritiesTitleId" class="priorities-title">{{ t('admin.modelIntegrity.scheduling.policy.custom.priorities.title') }}</span>
        <span class="priorities-hint">{{ t('admin.modelIntegrity.scheduling.policy.custom.priorities.hint') }}</span>
      </div>

      <ol v-if="priorities.length" class="priorities-list" data-testid="priority-list">
        <li v-for="(factor, index) in priorities" :key="factor" class="priority-row" data-testid="priority-row">
          <span class="priority-num" aria-hidden="true">{{ index + 1 }}</span>
          <span class="priority-name">{{ t(`admin.modelIntegrity.scheduling.policy.order.priority.${factor}`) }}</span>
          <span class="priority-actions">
            <button
              :ref="el => setButton(`up-${factor}`, el)"
              type="button"
              class="priority-btn"
              :disabled="index === 0"
              :aria-label="t('admin.modelIntegrity.scheduling.policy.custom.priorities.moveUp', { factor: factorName(factor) })"
              :title="t('admin.modelIntegrity.scheduling.policy.custom.priorities.moveUp', { factor: factorName(factor) })"
              :data-testid="`priority-up-${factor}`"
              @click="move(index, -1)"
            >
              <Icon name="chevronUp" size="sm" />
            </button>
            <button
              :ref="el => setButton(`down-${factor}`, el)"
              type="button"
              class="priority-btn"
              :disabled="index === priorities.length - 1"
              :aria-label="t('admin.modelIntegrity.scheduling.policy.custom.priorities.moveDown', { factor: factorName(factor) })"
              :title="t('admin.modelIntegrity.scheduling.policy.custom.priorities.moveDown', { factor: factorName(factor) })"
              :data-testid="`priority-down-${factor}`"
              @click="move(index, 1)"
            >
              <Icon name="chevronDown" size="sm" />
            </button>
            <button
              :ref="el => setButton(`remove-${factor}`, el)"
              type="button"
              class="priority-btn priority-btn-remove"
              :aria-label="t('admin.modelIntegrity.scheduling.policy.custom.priorities.remove', { factor: factorName(factor) })"
              :title="t('admin.modelIntegrity.scheduling.policy.custom.priorities.remove', { factor: factorName(factor) })"
              :data-testid="`priority-remove-${factor}`"
              @click="remove(index)"
            >
              <Icon name="x" size="sm" />
            </button>
          </span>
        </li>
      </ol>
      <!-- Outside the list: the last tie-break is not a priority. With every
           weight at 0% there is no score, so account order breaks the tie. -->
      <p v-if="priorities.length" class="priority-row priority-then" data-testid="priority-then">
        <span class="priority-num" aria-hidden="true" />
        <span class="priority-name">{{ t(`admin.modelIntegrity.scheduling.policy.custom.priorities.${weightless ? 'thenAccountOrder' : 'then'}`) }}</span>
      </p>
      <p v-else class="priorities-empty" data-testid="priority-empty">{{ t('admin.modelIntegrity.scheduling.policy.custom.priorities.empty') }}</p>

      <div v-if="available.length" class="priorities-add">
        <label class="sr-only" :for="addSelectId">{{ t('admin.modelIntegrity.scheduling.policy.custom.priorities.addLabel') }}</label>
        <select :id="addSelectId" ref="addSelect" v-model="pending" class="input priorities-select" data-testid="priority-add-select">
          <option v-for="factor in available" :key="factor" :value="factor">{{ factorName(factor) }}</option>
        </select>
        <button type="button" class="btn btn-secondary btn-sm" data-testid="priority-add" @click="add">
          <Icon name="plus" size="sm" />{{ t('admin.modelIntegrity.scheduling.policy.custom.priorities.add') }}
        </button>
      </div>
      <p v-else class="priorities-empty" data-testid="priority-all-used">{{ t('admin.modelIntegrity.scheduling.policy.custom.priorities.allUsed') }}</p>

      <p :id="prioritiesRulesId" class="priorities-rules">{{ t('admin.modelIntegrity.scheduling.policy.custom.priorities.rules') }}</p>
      <p class="sr-only" aria-live="polite" data-testid="priority-announce">{{ announcement }}</p>
    </div>

    <div class="weights-grid">
      <label v-for="factor in CUSTOM_FACTORS" :key="factor" class="weights-field">
        <span class="weights-label">
          {{ t(`admin.modelIntegrity.scheduling.policy.custom.${factor}`) }}
          <span v-if="rankOf(factor)" class="weights-rank" :data-testid="`weight-rank-${factor}`">{{ t('admin.modelIntegrity.scheduling.policy.custom.priorities.badge', { n: rankOf(factor) }) }}</span>
        </span>
        <input
          :value="percent(factor)"
          class="input weights-input tabular-nums"
          type="number"
          inputmode="numeric"
          min="0"
          max="100"
          step="1"
          :aria-invalid="issue ? 'true' : undefined"
          :aria-describedby="issue ? errorId : undefined"
          :data-testid="`weight-${factor}`"
          @input="update(factor, ($event.target as HTMLInputElement).value)"
          @change="($event.target as HTMLInputElement).value = String(percent(factor))"
        />
        <span class="weights-help">{{ t(`admin.modelIntegrity.scheduling.policy.custom.help.${factor}`) }}</span>
      </label>
    </div>
    <p v-if="issue" :id="errorId" class="weights-error" role="alert" data-testid="weights-error">
      {{ t(customBalanceIssueKey(issue)) }}
    </p>
  </fieldset>
</template>

<script lang="ts">
let nextId = 0
</script>

<script setup lang="ts">
import { computed, nextTick, ref, watch, type ComponentPublicInstance } from 'vue'
import { useI18n } from 'vue-i18n'
import type { OpenAIEvalPolicyWeights } from '@/api/admin/accounts'
import Icon from '@/components/icons/Icon.vue'
import { CUSTOM_FACTORS, DEFAULT_CUSTOM_BALANCE, absolutePriorities, customBalanceIssue, customBalanceIssueKey, foldLegacyStability, hasNoPositiveWeight, type CustomFactor } from '@/views/admin/modelIntegrity/modelIntegrity'

const props = defineProps<{
  modelValue?: OpenAIEvalPolicyWeights
  legend: string
}>()
const emit = defineEmits<{ (e: 'update:modelValue', value: OpenAIEvalPolicyWeights): void }>()
const { t } = useI18n()

const id = ++nextId
const errorId = `policy-weights-error-${id}`
const prioritiesTitleId = `policy-priorities-title-${id}`
const prioritiesRulesId = `policy-priorities-rules-${id}`
const addSelectId = `policy-priorities-add-${id}`

const issue = computed(() => customBalanceIssue(props.modelValue))
const priorities = computed(() => absolutePriorities(props.modelValue))
const available = computed(() => CUSTOM_FACTORS.filter(factor => !priorities.value.includes(factor)))
const weightless = computed(() => hasNoPositiveWeight(props.modelValue))

const pending = ref<CustomFactor | ''>('')
const announcement = ref('')
const addSelect = ref<HTMLSelectElement | null>(null)
const buttons = new Map<string, HTMLButtonElement>()

// The add menu always points at a factor that can still be added.
watch(available, list => {
  if (!pending.value || !list.includes(pending.value)) pending.value = list[0] ?? ''
}, { immediate: true })

function setButton(key: string, el: Element | ComponentPublicInstance | null) {
  if (el instanceof HTMLButtonElement) buttons.set(key, el)
  else buttons.delete(key)
}

function factorName(factor: CustomFactor) {
  return t(`admin.modelIntegrity.scheduling.policy.custom.${factor}`)
}

function rankOf(factor: CustomFactor) {
  const index = priorities.value.indexOf(factor)
  return index >= 0 ? index + 1 : 0
}

function percent(factor: CustomFactor) {
  return Math.round(Number(props.modelValue?.[factor] ?? 0) * 100)
}

/** Fold first so a legacy stability weight never survives next to the edit. */
function base() {
  return foldLegacyStability(props.modelValue ?? DEFAULT_CUSTOM_BALANCE)
}

/**
 * Keeps each value within 0–100 % and always emits a fresh object so a rule
 * never shares weights with another rule or the default.
 */
function update(factor: CustomFactor, value: string) {
  const raw = Number(value)
  const next = Number.isFinite(raw) ? Math.min(Math.max(Math.round(raw), 0), 100) / 100 : 0
  emit('update:modelValue', { ...base(), [factor]: next })
}

function setPriorities(next: CustomFactor[]) {
  emit('update:modelValue', { ...base(), absolute_priorities: next })
}

function add() {
  const factor = pending.value
  if (!factor || priorities.value.includes(factor)) return
  const next = [...priorities.value, factor]
  setPriorities(next)
  announcement.value = t('admin.modelIntegrity.scheduling.policy.custom.priorities.added', { factor: factorName(factor), n: next.length })
  // The menu loses the added option; keep focus there for the next pick, or on
  // the new row once every factor is used and the menu is gone.
  nextTick(() => (addSelect.value ?? buttons.get(`remove-${factor}`))?.focus())
}

/** Swaps with the neighbour and keeps focus on the same control as it moves. */
function move(index: number, step: -1 | 1) {
  const target = index + step
  if (target < 0 || target >= priorities.value.length) return
  const next = [...priorities.value]
  const factor = next[index]
  next[index] = next[target]
  next[target] = factor
  setPriorities(next)
  announcement.value = t('admin.modelIntegrity.scheduling.policy.custom.priorities.moved', { factor: factorName(factor), n: target + 1 })
  nextTick(() => {
    // At an end the pressed button is disabled, so focus its counterpart instead.
    const atEnd = step < 0 ? target === 0 : target === next.length - 1
    const key = atEnd ? (step < 0 ? `down-${factor}` : `up-${factor}`) : (step < 0 ? `up-${factor}` : `down-${factor}`)
    buttons.get(key)?.focus()
  })
}

function remove(index: number) {
  const factor = priorities.value[index]
  const next = priorities.value.filter((_, i) => i !== index)
  setPriorities(next)
  announcement.value = t('admin.modelIntegrity.scheduling.policy.custom.priorities.removed', { factor: factorName(factor) })
  // Focus the row that took its place, or the add menu when the list is empty.
  nextTick(() => {
    const neighbour = next[index] ?? next[index - 1]
    if (neighbour) buttons.get(`remove-${neighbour}`)?.focus()
    else addSelect.value?.focus()
  })
}
</script>

<style scoped>
.weights { @apply min-w-0 space-y-3; }
.weights-grid { @apply grid min-w-0 gap-3 sm:grid-cols-2 xl:grid-cols-5; }
.weights-field { @apply flex min-w-0 flex-col gap-1; }
.weights-label { @apply flex flex-wrap items-baseline gap-x-1.5 text-xs text-gray-500 dark:text-gray-400; }
/* Ties the weight back to its strict rank, so a 0% there reads as deliberate. */
.weights-rank { @apply text-[11px] font-medium text-primary-700 dark:text-primary-300; }
.weights-help { @apply text-[11px] leading-snug text-gray-500 dark:text-gray-400; }
.weights-input { @apply h-9 py-1 text-sm; }
.weights-input[aria-invalid='true'] { @apply border-rose-400 dark:border-rose-500; }
.weights-error { @apply text-xs text-rose-700 dark:text-rose-300; }

.priorities { @apply min-w-0 space-y-2 rounded-md border border-gray-200 bg-white p-3 dark:border-dark-600 dark:bg-dark-800; }
.priorities-head { @apply flex flex-wrap items-baseline gap-x-2 gap-y-0.5; }
.priorities-title { @apply text-sm font-medium text-gray-800 dark:text-gray-200; }
.priorities-hint { @apply text-xs text-gray-500 dark:text-gray-400; }
.priorities-list { @apply grid gap-1; }
.priority-row { @apply grid min-h-[2.25rem] grid-cols-[1.25rem_1fr_auto] items-center gap-2 rounded-md bg-gray-50 px-2 py-1 text-sm text-gray-800 dark:bg-dark-900/60 dark:text-gray-200; }
.priority-num { @apply text-center text-xs font-semibold tabular-nums text-primary-700 dark:text-primary-300; }
.priority-name { @apply min-w-0; }
.priority-actions { @apply flex items-center gap-0.5; }
.priority-btn { @apply inline-flex h-8 w-8 items-center justify-center rounded-md text-gray-500 hover:bg-gray-200 hover:text-gray-800 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 disabled:cursor-not-allowed disabled:opacity-30 disabled:hover:bg-transparent dark:text-gray-400 dark:hover:bg-dark-700 dark:hover:text-white; }
.priority-btn-remove { @apply hover:text-rose-700 dark:hover:text-rose-300; }
/* The weighted score is the last tie-break, not a priority, so it is unnumbered and quieter. */
.priority-then { @apply bg-transparent text-xs text-gray-500 dark:bg-transparent dark:text-gray-400; }
.priorities-empty { @apply text-xs text-gray-500 dark:text-gray-400; }
.priorities-add { @apply flex flex-wrap items-center gap-2; }
.priorities-select { @apply h-8 w-auto min-w-[10rem] py-0 text-sm; }
.priorities-rules { @apply text-[11px] leading-snug text-gray-500 dark:text-gray-400; }
</style>
