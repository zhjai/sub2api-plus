<template>
  <div ref="root" class="pf" :class="`pf-${mode}`" :style="{ '--pf-scale': String(scale) }" data-testid="pelican-frame-box">
    <iframe
      v-if="url"
      :key="url"
      :src="url"
      sandbox="allow-scripts"
      referrerpolicy="no-referrer"
      :loading="mode === 'preview' ? 'lazy' : 'eager'"
      :tabindex="mode === 'preview' ? -1 : 0"
      :title="title"
      class="pf-frame"
      data-testid="pelican-frame"
    />
    <div v-else-if="error" class="pf-state" role="alert" data-testid="pelican-frame-error">
      <span>{{ t('admin.modelIntegrity.tests.pelican.loadFailed', { error }) }}</span>
      <button type="button" class="pf-retry" @click="load(true)">{{ t('admin.modelIntegrity.tests.pelican.retry') }}</button>
    </div>
    <div v-else class="pf-state" role="status">
      <Icon name="refresh" size="sm" class="motion-safe:animate-spin" />
      <span class="sr-only">{{ t('admin.modelIntegrity.tests.pelican.loading') }}</span>
    </div>
  </div>
</template>

<script lang="ts">
import type { OpenAIEvalPelicanFrameMode } from '@/api/admin/accounts'

interface FrameGrant { url: string; expires: number }

/**
 * Frame URLs are short-lived capabilities. A list re-render must not ask for a
 * new one each time, so grants are reused until shortly before they expire.
 */
const grants = new Map<string, FrameGrant>()
const GRANT_MARGIN_MS = 30_000

export function clearPelicanFrameGrants() {
  grants.clear()
}

export function pelicanGrantKey(runID: number, mode: OpenAIEvalPelicanFrameMode) {
  return `${runID}:${mode}`
}

/**
 * Only an http(s) URL from the token endpoint may become the frame source;
 * anything else (javascript:, data:, blob:) is refused rather than loaded.
 */
export function safePelicanFrameURL(value: unknown): string | null {
  if (typeof value !== 'string' || !value.trim()) return null
  try {
    const parsed = new URL(value, window.location.origin)
    return parsed.protocol === 'https:' || parsed.protocol === 'http:' ? value : null
  } catch {
    return null
  }
}
</script>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import { accountsAPI } from '@/api/admin/accounts'
import { extractApiErrorMessage } from '@/utils/apiError'

/**
 * The stored Pelican page, served by the backend's frame endpoint with its own
 * CSP. The page runs in an opaque origin (sandbox without allow-same-origin):
 * its scripts and animations work, but it cannot reach this page, its storage,
 * the network or the top window. No markup is ever injected here — no srcdoc,
 * no blob URL — the iframe only gets the capability URL.
 *
 * 'preview' asks the server for the frozen still; 'play' for the running page.
 * Both draw on an 800 × 600 canvas scaled to the box width.
 */
const props = defineProps<{
  runId: number
  mode: OpenAIEvalPelicanFrameMode
  title: string
}>()

const { t } = useI18n()
const root = ref<HTMLElement | null>(null)
const url = ref('')
const error = ref('')
const scale = ref(1)
const CANVAS_WIDTH = 800
let observer: ResizeObserver | null = null
let request = 0

async function load(force = false) {
  const key = pelicanGrantKey(props.runId, props.mode)
  const cached = grants.get(key)
  if (!force && cached && cached.expires - GRANT_MARGIN_MS > Date.now()) {
    url.value = cached.url
    error.value = ''
    return
  }
  const id = ++request
  url.value = ''
  error.value = ''
  try {
    const grant = await accountsAPI.createOpenAIEvalPelicanFrameToken(props.runId, props.mode)
    if (id !== request) return
    const frameURL = safePelicanFrameURL(grant?.url)
    if (!frameURL) {
      error.value = t('admin.modelIntegrity.tests.pelican.loadFailedGeneric')
      return
    }
    const expires = Date.parse(grant.expires_at)
    grants.set(key, { url: frameURL, expires: Number.isFinite(expires) ? expires : Date.now() + 60_000 })
    url.value = frameURL
  } catch (failure) {
    if (id !== request) return
    error.value = extractApiErrorMessage(failure, t('admin.modelIntegrity.tests.pelican.loadFailedGeneric'))
  }
}

function measure() {
  const width = root.value?.clientWidth ?? 0
  scale.value = width > 0 ? width / CANVAS_WIDTH : 1
}

watch(() => `${props.runId}:${props.mode}`, () => { void load() })

onMounted(() => {
  void load()
  measure()
  if (typeof ResizeObserver !== 'undefined' && root.value) {
    observer = new ResizeObserver(measure)
    observer.observe(root.value)
  }
})

onBeforeUnmount(() => {
  request++
  observer?.disconnect()
  // Unmounting drops the iframe, which also stops a playing page.
  url.value = ''
})
</script>

<style scoped>
/* The box keeps the canvas's 4:3 shape; the page inside is drawn at 800 × 600 and scaled. */
.pf { @apply relative w-full overflow-hidden bg-white dark:bg-slate-100; aspect-ratio: 4 / 3; }
.pf-frame {
  @apply absolute left-0 top-0 block border-0;
  width: 800px;
  height: 600px;
  transform: scale(var(--pf-scale, 1));
  transform-origin: 0 0;
}
.pf-preview .pf-frame { pointer-events: none; }
.pf-state { @apply absolute inset-0 flex flex-col items-center justify-center gap-2 px-3 text-center text-xs text-slate-500; }
.pf-retry { @apply rounded border border-slate-300 px-2 py-0.5 text-slate-700 hover:bg-slate-50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary-500; }
</style>
