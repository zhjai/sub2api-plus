import { computed, reactive, ref } from 'vue'
import { accountsAPI, type OpenAIEvalConfig, type OpenAIEvalModelCatalog } from '@/api/admin/accounts'
import type { AccountListItem } from '@/types'
import { normalizeRoute, toSavePayload } from './modelIntegrity'

export type SaveResult = 'saved' | 'conflict' | 'failed'

function isConflict(error: unknown): boolean {
  if (!error || typeof error !== 'object') return false
  const e = error as { status?: number; response?: { status?: number } }
  return e.status === 409 || e.response?.status === 409
}

/**
 * One source of truth for the shared evaluation config. Both Model Integrity
 * pages load and save the same object (/admin/accounts/evaluations/config);
 * the page that saves always sends every field back together with the
 * revision it loaded, so a stale tab gets a 409 instead of silently
 * overwriting what the other page changed.
 */
export function useModelIntegrityConfig() {
  const config = reactive<OpenAIEvalConfig>({ effects_enabled: false, bps_auto_enabled: false, scheduling_policy: '', policies: [], accounts: [] })
  const catalog = ref<OpenAIEvalModelCatalog | null>(null)
  const accounts = ref<AccountListItem[]>([])
  const loading = ref(true)
  const loaded = ref(false)
  const saving = ref(false)
  const conflict = ref(false)
  const loadError = ref('')
  const snapshot = ref('')

  const serialized = () => JSON.stringify(toSavePayload(config))
  const dirty = computed(() => loaded.value && snapshot.value !== serialized())

  function apply(saved: OpenAIEvalConfig) {
    config.revision = saved.revision
    config.effects_enabled = Boolean(saved.effects_enabled)
    config.bps_auto_enabled = Boolean(saved.bps_auto_enabled)
    config.scheduling_policy = saved.scheduling_policy ?? ''
    config.policies = (saved.policies ?? []).map(rule => ({ ...rule, reasoning_effort: rule.reasoning_effort || '' }))
    config.accounts = (saved.accounts ?? []).map(route => normalizeRoute({ ...route }))
    snapshot.value = serialized()
  }

  async function load() {
    loading.value = true
    loadError.value = ''
    try {
      const [meta, saved, accountPage] = await Promise.all([
        accountsAPI.getOpenAIEvalModels(),
        accountsAPI.getOpenAIEvalConfig(),
        accountsAPI.list(1, 500, { platform: 'openai', lite: '1' })
      ])
      catalog.value = meta
      accounts.value = accountPage.items
      apply(saved)
      conflict.value = false
      loaded.value = true
    } catch (error) {
      loadError.value = error instanceof Error ? error.message : String((error as { message?: string })?.message ?? '')
      throw error
    } finally {
      loading.value = false
    }
  }

  /** Re-reads the server config while keeping the catalog and account list. */
  async function reloadConfig() {
    apply(await accountsAPI.getOpenAIEvalConfig())
    conflict.value = false
  }

  async function save(): Promise<SaveResult> {
    if (!loaded.value || saving.value) return 'failed'
    config.accounts.forEach(normalizeRoute)
    saving.value = true
    try {
      const saved = await accountsAPI.saveOpenAIEvalConfig(toSavePayload(config))
      // Runtime-only fields (BPS state, OAuth eligibility) are not echoed by
      // every server version, so re-read them after a successful save.
      try {
        apply(await accountsAPI.getOpenAIEvalConfig())
      } catch {
        apply(saved)
      }
      conflict.value = false
      return 'saved'
    } catch (error) {
      if (isConflict(error)) {
        conflict.value = true
        return 'conflict'
      }
      throw error
    } finally {
      saving.value = false
    }
  }

  function accountName(accountID: number) {
    const account = accounts.value.find(item => item.id === accountID)
    return account ? account.name : `#${accountID}`
  }

  return { config, catalog, accounts, loading, loaded, saving, conflict, loadError, dirty, load, reloadConfig, save, accountName }
}
