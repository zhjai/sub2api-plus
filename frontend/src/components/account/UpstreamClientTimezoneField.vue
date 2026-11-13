<template>
  <div
    class="space-y-3 border-t border-gray-200 pt-4 dark:border-dark-600"
    data-testid="upstream-client-timezone-field"
  >
    <div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
      <div class="min-w-0">
        <label :for="`${idPrefix}-upstream-client-timezone-mode`" class="input-label mb-0">
          {{ t('admin.accounts.upstreamClientTimezone.title') }}
        </label>
        <p class="mt-1 text-xs leading-relaxed text-gray-500 dark:text-gray-400">
          {{ t('admin.accounts.upstreamClientTimezone.desc') }}
        </p>
      </div>
      <div class="w-full flex-shrink-0 sm:w-56">
        <Select
          :id="`${idPrefix}-upstream-client-timezone-mode`"
          :model-value="mode"
          :options="modeOptions"
          :data-testid="`${idPrefix}-upstream-client-timezone-mode`"
          @update:model-value="onModeChange"
        />
      </div>
    </div>

    <p
      v-if="mode !== 'off'"
      class="rounded-lg border border-gray-200 bg-gray-50/80 px-3 py-2 text-xs leading-relaxed text-gray-600 dark:border-dark-600 dark:bg-dark-700/50 dark:text-gray-300"
      data-testid="upstream-client-timezone-boundary-note"
    >
      {{ t('admin.accounts.upstreamClientTimezone.boundaryNote') }}
    </p>

    <!-- Manual IANA timezone input (value stays in parent memory across mode switches) -->
    <div v-if="mode === 'manual'" class="space-y-2 pl-0 sm:border-l-2 sm:border-gray-200 sm:pl-4 dark:sm:border-dark-600">
      <label
        :for="`${idPrefix}-upstream-client-timezone-input`"
        class="block text-xs font-medium text-gray-700 dark:text-gray-300"
      >
        {{ t('admin.accounts.upstreamClientTimezone.manualLabel') }}
      </label>
      <input
        :id="`${idPrefix}-upstream-client-timezone-input`"
        :value="timezone"
        type="text"
        :list="`${idPrefix}-upstream-client-timezone-datalist`"
        autocomplete="off"
        spellcheck="false"
        class="input font-mono text-sm"
        :class="effectiveErrorMessage ? 'border-red-400 focus:border-red-500 focus:ring-red-500 dark:border-red-500/70' : ''"
        :placeholder="t('admin.accounts.upstreamClientTimezone.manualPlaceholder')"
        :data-testid="`${idPrefix}-upstream-client-timezone-input`"
        @input="onTimezoneInput"
      />
      <datalist :id="`${idPrefix}-upstream-client-timezone-datalist`">
        <option v-for="zone in suggestions" :key="zone" :value="zone" />
      </datalist>

      <div class="flex flex-wrap gap-1.5" data-testid="upstream-client-timezone-quick-picks">
        <button
          v-for="zone in quickPickZones"
          :key="zone"
          type="button"
          class="rounded border px-2 py-0.5 font-mono text-[11px] transition-colors"
          :class="
            timezone.trim() === zone
              ? 'border-primary-300 bg-primary-50 text-primary-700 dark:border-primary-700 dark:bg-primary-900/30 dark:text-primary-300'
              : 'border-gray-200 bg-white text-gray-600 hover:border-gray-300 hover:bg-gray-50 dark:border-dark-600 dark:bg-dark-700 dark:text-gray-300 dark:hover:border-dark-500'
          "
          @click="emit('update:timezone', zone)"
        >
          {{ zone }}
        </button>
      </div>

      <p
        v-if="effectiveErrorMessage"
        class="text-xs text-red-600 dark:text-red-400"
        role="alert"
        data-testid="upstream-client-timezone-error"
      >
        {{ effectiveErrorMessage }}
      </p>
      <p v-else class="text-xs text-gray-500 dark:text-gray-400">
        {{ t('admin.accounts.upstreamClientTimezone.manualHint') }}
      </p>
    </div>

    <p
      v-else-if="serverError"
      class="text-xs text-red-600 dark:text-red-400"
      role="alert"
      data-testid="upstream-client-timezone-error"
    >
      {{ serverError }}
    </p>

    <!-- Read-only persisted runtime state from backend DTO (never pretends unsaved edits are active) -->
    <div
      v-if="runtime"
      class="rounded-lg border border-gray-200 bg-gray-50/70 px-3 py-2.5 text-xs dark:border-dark-600 dark:bg-dark-700/40"
      data-testid="upstream-client-timezone-runtime"
    >
      <div class="flex flex-wrap items-center justify-between gap-2">
        <div class="flex flex-wrap items-center gap-2">
          <span class="font-medium text-gray-700 dark:text-gray-300">
            {{ t('admin.accounts.upstreamClientTimezone.runtimeTitle') }}
          </span>
          <span
            class="inline-flex items-center rounded px-1.5 py-0.5 text-[11px] font-medium"
            :class="runtimeStatusBadgeClass"
            data-testid="upstream-client-timezone-runtime-status"
          >
            {{ runtimeStatusLabel }}
          </span>
          <code
            v-if="runtime.timezone"
            class="rounded bg-white px-1.5 py-0.5 font-mono text-[11px] text-gray-800 ring-1 ring-gray-200 dark:bg-dark-800 dark:text-gray-200 dark:ring-dark-500"
            data-testid="upstream-client-timezone-runtime-zone"
          >
            {{ runtime.timezone }}
          </code>
          <span
            v-if="runtimeSourceLabel"
            class="text-[11px] text-gray-500 dark:text-gray-400"
            data-testid="upstream-client-timezone-runtime-source"
          >
            · {{ runtimeSourceLabel }}
          </span>
        </div>
        <span
          v-if="formattedUpdatedAt"
          class="text-[11px] text-gray-400 dark:text-gray-500"
        >
          {{ t('admin.accounts.upstreamClientTimezone.updatedAt', { time: formattedUpdatedAt }) }}
        </span>
      </div>

      <p
        v-if="runtime.error"
        class="mt-1.5 text-[11px] text-red-600 dark:text-red-400"
        data-testid="upstream-client-timezone-runtime-error"
      >
        {{ runtime.error }}
      </p>

      <p
        v-if="isDirty"
        class="mt-1.5 text-[11px] text-amber-700 dark:text-amber-300"
        data-testid="upstream-client-timezone-unsaved-note"
      >
        {{ t('admin.accounts.upstreamClientTimezone.unsavedDraftNote') }}
      </p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import Select from '@/components/common/Select.vue'
import type { UpstreamClientTimezoneRuntime } from '@/types'
import {
  canonicalIANATimezone,
  upstreamClientTimezoneSuggestions,
  type UpstreamClientTimezoneError,
  type UpstreamClientTimezoneMode,
  type UpstreamClientTimezoneState
} from '@/utils/upstreamClientTimezone'

const props = withDefaults(
  defineProps<{
    mode: UpstreamClientTimezoneMode
    timezone: string
    validationError?: UpstreamClientTimezoneError | null
    serverError?: string
    runtime?: UpstreamClientTimezoneRuntime | null
    savedState?: UpstreamClientTimezoneState | null
    idPrefix?: string
  }>(),
  {
    validationError: null,
    serverError: '',
    runtime: null,
    savedState: null,
    idPrefix: 'account'
  }
)

const emit = defineEmits<{
  'update:mode': [mode: UpstreamClientTimezoneMode]
  'update:timezone': [timezone: string]
}>()

const { t } = useI18n()

const quickPickZones = [
  'UTC',
  'America/Los_Angeles',
  'America/New_York',
  'Europe/London',
  'Asia/Singapore',
  'Asia/Tokyo'
]

const suggestions = computed(() => upstreamClientTimezoneSuggestions())

const modeOptions = computed(() => [
  { value: 'off' as UpstreamClientTimezoneMode, label: t('admin.accounts.upstreamClientTimezone.modeOff') },
  { value: 'manual' as UpstreamClientTimezoneMode, label: t('admin.accounts.upstreamClientTimezone.modeManual') },
  { value: 'auto' as UpstreamClientTimezoneMode, label: t('admin.accounts.upstreamClientTimezone.modeAuto') }
])

const onModeChange = (val: string | number | boolean | null) => {
  const next: UpstreamClientTimezoneMode = val === 'manual' || val === 'auto' ? val : 'off'
  emit('update:mode', next)
}

const onTimezoneInput = (event: Event) => {
  const target = event.target as HTMLInputElement | null
  emit('update:timezone', target?.value ?? '')
}

const effectiveErrorMessage = computed(() => {
  if (props.serverError) return props.serverError
  if (props.validationError === 'required') {
    return t('admin.accounts.upstreamClientTimezone.errorRequired')
  }
  if (props.validationError === 'invalid') {
    return t('admin.accounts.upstreamClientTimezone.errorInvalid')
  }
  return ''
})

const isDirty = computed(() => {
  if (!props.savedState) return false
  if (props.mode !== props.savedState.mode) return true
  if (props.mode === 'manual') {
    const currentZone = canonicalIANATimezone(props.timezone) || props.timezone.trim()
    const savedZone = canonicalIANATimezone(props.savedState.timezone) || props.savedState.timezone.trim()
    return currentZone !== savedZone
  }
  return false
})

const runtimeStatusLabel = computed(() => {
  const status = props.runtime?.status || 'off'
  switch (status) {
    case 'pending':
      return t('admin.accounts.upstreamClientTimezone.status.pending')
    case 'ready':
      return t('admin.accounts.upstreamClientTimezone.status.ready')
    case 'stale':
      return t('admin.accounts.upstreamClientTimezone.status.stale')
    case 'error':
      return t('admin.accounts.upstreamClientTimezone.status.error')
    default:
      return t('admin.accounts.upstreamClientTimezone.status.off')
  }
})

const runtimeStatusBadgeClass = computed(() => {
  switch (props.runtime?.status) {
    case 'ready':
      return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300'
    case 'pending':
      return 'bg-blue-100 text-blue-700 dark:bg-blue-900/40 dark:text-blue-300'
    case 'stale':
      return 'bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300'
    case 'error':
      return 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300'
    default:
      return 'bg-gray-200/80 text-gray-600 dark:bg-dark-600 dark:text-gray-300'
  }
})

const runtimeSourceLabel = computed(() => {
  switch (props.runtime?.source) {
    case 'manual':
      return t('admin.accounts.upstreamClientTimezone.source.manual')
    case 'proxy_exit':
      return t('admin.accounts.upstreamClientTimezone.source.proxy_exit')
    case 'server_exit':
      return t('admin.accounts.upstreamClientTimezone.source.server_exit')
    default:
      return ''
  }
})

const formattedUpdatedAt = computed(() => {
  const raw = props.runtime?.updated_at
  if (!raw) return ''
  const parsed = new Date(raw)
  if (Number.isNaN(parsed.getTime())) return raw
  return parsed.toLocaleString()
})
</script>
