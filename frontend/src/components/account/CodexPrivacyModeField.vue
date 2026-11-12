<template>
  <div
    ref="rootRef"
    class="border-t border-gray-200 pt-4 dark:border-dark-600"
    data-testid="codex-privacy"
  >
    <div class="flex items-center justify-between gap-4">
      <div class="min-w-0">
        <span :id="labelId" class="input-label mb-0">{{ t('admin.accounts.openai.codexPrivacy.title') }}</span>
        <p :id="descId" class="mt-1 text-xs text-gray-500 dark:text-gray-400">
          {{ t('admin.accounts.openai.codexPrivacy.desc') }}
        </p>
        <p :id="continuityId" class="mt-1 text-xs text-gray-500 dark:text-gray-400" data-testid="codex-privacy-continuity">
          {{ t('admin.accounts.openai.codexPrivacy.continuity') }}
        </p>
      </div>
      <button
        type="button"
        role="switch"
        data-testid="codex-privacy-toggle"
        :aria-checked="enabled"
        :aria-labelledby="labelId"
        :aria-describedby="`${descId} ${continuityId}`"
        :class="[
          'relative inline-flex h-6 w-11 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 ease-in-out focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 motion-reduce:transition-none',
          enabled ? 'bg-primary-600' : 'bg-gray-200 dark:bg-dark-600'
        ]"
        @click="emit('toggle')"
      >
        <span
          :class="[
            'pointer-events-none inline-block h-5 w-5 transform rounded-full bg-white shadow ring-0 transition duration-200 ease-in-out motion-reduce:transition-none',
            enabled ? 'translate-x-5' : 'translate-x-0'
          ]"
        />
      </button>
    </div>

    <ul
      v-if="enabled"
      class="mt-3 space-y-1 border-l-2 border-gray-200 pl-4 text-xs leading-relaxed text-gray-600 dark:border-dark-600 dark:text-gray-300"
      data-testid="codex-privacy-scope"
    >
      <li>{{ t('admin.accounts.openai.codexPrivacy.limits') }}</li>
      <li data-testid="codex-privacy-overrides">{{ t('admin.accounts.openai.codexPrivacy.overrides') }}</li>
    </ul>

    <div
      v-if="conflicts.length > 0"
      class="mt-3 rounded-md bg-amber-50 px-3 py-2 text-xs leading-relaxed text-amber-900 dark:bg-amber-950/30 dark:text-amber-200"
      :role="blocked ? 'alert' : 'status'"
      data-testid="codex-privacy-conflict"
    >
      <p>{{ t('admin.accounts.openai.codexPrivacy.conflictIntro') }}</p>
      <ul class="mt-1 list-disc space-y-0.5 pl-4">
        <li v-if="conflicts.includes('fingerprint')" data-testid="codex-privacy-conflict-fingerprint">
          {{ t('admin.accounts.openai.codexPrivacy.conflictFingerprint', { mode: fingerprintLabel }) }}
        </li>
        <li v-if="conflicts.includes('preserve_client')" data-testid="codex-privacy-conflict-preserve">
          {{ t('admin.accounts.openai.codexPrivacy.conflictPreserve') }}
        </li>
      </ul>
      <p v-if="hiddenSettings" class="mt-1">{{ t('admin.accounts.openai.codexPrivacy.conflictHidden') }}</p>
      <p v-if="blocked" class="mt-1 font-medium" data-testid="codex-privacy-conflict-blocked">
        {{ t('admin.accounts.openai.codexPrivacy.conflictBlocked') }}
      </p>
      <div class="mt-1.5 flex flex-wrap gap-x-4 gap-y-1">
        <button
          ref="resolveRef"
          type="button"
          class="rounded font-medium underline underline-offset-2 focus:outline-none focus:ring-2 focus:ring-primary-500"
          data-testid="codex-privacy-resolve"
          @click="emit('resolve')"
        >
          {{ t('admin.accounts.openai.codexPrivacy.resolve') }}
        </button>
        <button
          type="button"
          class="rounded underline underline-offset-2 focus:outline-none focus:ring-2 focus:ring-primary-500"
          data-testid="codex-privacy-turn-off"
          @click="emit('toggle')"
        >
          {{ t('admin.accounts.openai.codexPrivacy.turnOff') }}
        </button>
      </div>
    </div>

    <p
      v-else-if="cleanupPending && enabled"
      class="mt-3 rounded-md bg-gray-50 px-3 py-2 text-xs text-gray-600 dark:bg-dark-800/70 dark:text-gray-300"
      data-testid="codex-privacy-cleanup-pending"
    >
      {{ t('admin.accounts.openai.codexPrivacy.cleanupPending') }}
    </p>

    <div
      v-if="serverError"
      class="mt-2 rounded-md bg-rose-50 px-3 py-2 text-xs text-rose-800 dark:bg-rose-950/30 dark:text-rose-300"
      role="alert"
      data-testid="codex-privacy-server-error"
    >
      <p>{{ t('admin.accounts.openai.codexPrivacy.serverRejected', { reason: serverError.message }) }}</p>
      <p v-if="serverError.code" class="mt-0.5 text-rose-700/80 dark:text-rose-300/80">
        {{ t('admin.accounts.openai.codexPrivacy.serverCode', { code: serverError.code }) }}
      </p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { nextTick, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CodexPrivacyConflict, CodexPrivacyServerError } from '@/composables/useCodexPrivacyMode'

withDefaults(defineProps<{
  enabled: boolean
  conflicts: CodexPrivacyConflict[]
  /** Localized label of the convergence mode that conflicts. */
  fingerprintLabel?: string
  /** True when the conflicting settings have no control on this page (API key / setup-token). */
  hiddenSettings?: boolean
  /** True after the user chose to drop hidden conflicting settings on the next save. */
  cleanupPending?: boolean
  blocked?: boolean
  serverError?: CodexPrivacyServerError | null
}>(), {
  fingerprintLabel: '',
  hiddenSettings: false,
  cleanupPending: false,
  blocked: false,
  serverError: null
})

const emit = defineEmits<{
  (e: 'toggle'): void
  (e: 'resolve'): void
}>()

const { t } = useI18n()

const instanceId = `codex-privacy-${Math.random().toString(36).slice(2, 10)}`
const labelId = `${instanceId}-label`
const descId = `${instanceId}-desc`
const continuityId = `${instanceId}-continuity`

const rootRef = ref<HTMLElement | null>(null)
const resolveRef = ref<HTMLButtonElement | null>(null)

/** Brings a blocking conflict into view after a rejected save attempt. */
async function revealConflict() {
  await nextTick()
  const root = rootRef.value
  if (root && typeof root.scrollIntoView === 'function') {
    root.scrollIntoView({ block: 'center', behavior: 'smooth' })
  }
  resolveRef.value?.focus({ preventScroll: true })
}

defineExpose({ revealConflict })
</script>
