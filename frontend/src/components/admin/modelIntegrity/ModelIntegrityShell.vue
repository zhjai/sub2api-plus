<template>
  <div class="mi-page">
    <header class="mi-head">
      <nav class="mi-subnav" :aria-label="t('admin.modelIntegrity.nav.aria')">
        <router-link
          v-for="link in links"
          :key="link.to"
          :to="link.to"
          class="mi-subnav-link"
          :class="{ 'mi-subnav-link-active': route.path === link.to }"
          :aria-current="route.path === link.to ? 'page' : undefined"
        >{{ link.label }}</router-link>
      </nav>
      <div class="mi-head-row">
        <div class="min-w-0">
          <h1 class="mi-title">{{ title }}</h1>
          <p class="mi-description">{{ description }}</p>
        </div>
        <div class="mi-actions">
          <span v-if="dirty" class="mi-unsaved" role="status">
            <span class="mi-unsaved-dot" aria-hidden="true" />{{ t('admin.modelIntegrity.common.unsaved') }}
          </span>
          <slot name="actions" />
          <button
            v-if="showSave"
            type="button"
            class="btn btn-primary"
            :disabled="!dirty || saving || conflict"
            @click="emit('save')"
          >
            <Icon name="check" size="sm" />
            {{ saving ? t('admin.modelIntegrity.common.saving') : t('admin.modelIntegrity.common.save') }}
          </button>
        </div>
      </div>
    </header>

    <div v-if="conflict" class="mi-banner mi-banner-warn" role="alert">
      <Icon name="exclamationTriangle" size="sm" class="mt-0.5 shrink-0" />
      <span class="flex-1">{{ t('admin.modelIntegrity.common.conflict') }}</span>
      <button type="button" class="btn btn-secondary btn-sm" @click="emit('reload')">{{ t('admin.modelIntegrity.common.reload') }}</button>
    </div>

    <slot />
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import Icon from '@/components/icons/Icon.vue'

withDefaults(defineProps<{
  title: string
  description: string
  dirty?: boolean
  saving?: boolean
  conflict?: boolean
  showSave?: boolean
}>(), { dirty: false, saving: false, conflict: false, showSave: true })

const emit = defineEmits<{ (e: 'save'): void; (e: 'reload'): void }>()

const { t } = useI18n()
const route = useRoute()

const links = computed(() => [
  { to: '/admin/model-integrity/tests', label: t('admin.modelIntegrity.nav.tests') },
  { to: '/admin/model-integrity/scheduling', label: t('admin.modelIntegrity.nav.scheduling') }
])
</script>

<style scoped>
.mi-page { @apply mx-auto w-full max-w-7xl space-y-5 pb-10; }
.mi-head { @apply space-y-4 border-b border-gray-200 pb-5 dark:border-dark-700; }
.mi-subnav { @apply inline-flex gap-1 rounded-lg bg-gray-100 p-1 text-sm dark:bg-dark-800; }
.mi-subnav-link { @apply rounded-md px-3 py-1.5 font-medium text-gray-600 transition-colors hover:text-gray-900 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500 dark:text-gray-400 dark:hover:text-white; }
.mi-subnav-link-active { @apply bg-white text-gray-900 shadow-sm dark:bg-dark-600 dark:text-white; }
.mi-head-row { @apply flex flex-wrap items-end justify-between gap-4; }
.mi-title { @apply text-[1.375rem] font-semibold leading-tight tracking-tight text-gray-900 dark:text-white; }
.mi-description { @apply mt-1.5 max-w-[62ch] text-sm leading-relaxed text-gray-600 dark:text-gray-400; }
.mi-actions { @apply flex flex-wrap items-center gap-2; }
.mi-unsaved { @apply inline-flex items-center gap-1.5 text-xs font-medium text-amber-700 dark:text-amber-300; }
.mi-unsaved-dot { @apply h-1.5 w-1.5 rounded-full bg-amber-500; }
.mi-banner { @apply flex flex-wrap items-start gap-3 rounded-lg border px-4 py-3 text-sm; }
.mi-banner-warn { @apply border-amber-300 bg-amber-50 text-amber-900 dark:border-amber-800/70 dark:bg-amber-950/30 dark:text-amber-100; }
</style>
