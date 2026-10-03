import { computed, reactive, ref } from 'vue'
import { accountsAPI, type OpenAIEvalConfig, type OpenAIEvalModelCatalog } from '@/api/admin/accounts'
import type { AccountListItem } from '@/types'
import { DEFAULT_CUSTOM_BALANCE, DEFAULT_MAX_REQUEST_ATTEMPTS, DEFAULT_QUALITY_REFRESH_SECONDS, normalizeBPSAccount, normalizeCustomBalance, normalizeMaxRequestAttempts, normalizeQualityRefreshInterval, normalizeRoute, toSavePayload } from './modelIntegrity'

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
  const config = reactive<OpenAIEvalConfig>({ effects_enabled: false, bps_auto_enabled: false, scheduling_policy: '', custom_balance: { ...DEFAULT_CUSTOM_BALANCE }, policies: [], bps_accounts: [], max_request_attempts: DEFAULT_MAX_REQUEST_ATTEMPTS, quality_refresh_interval_seconds: DEFAULT_QUALITY_REFRESH_SECONDS, accounts: [] })
  const catalog = ref<OpenAIEvalModelCatalog | null>(null)
  const accounts = ref<AccountListItem[]>([])
  const loading = ref(true)
  const loaded = ref(false)
  const saving = ref(false)
  const conflict = ref(false)
  const loadError = ref('')
  const snapshot = ref('')
  /** True when the loaded config still had a legacy stability weight, which was folded on read. */
  const foldedLegacyStability = ref(false)
  /** The interval the server is using now; edits only apply after saving. */
  const savedQualityRefreshInterval = ref(DEFAULT_QUALITY_REFRESH_SECONDS)

  const serialized = () => JSON.stringify(toSavePayload(config))
  const dirty = computed(() => loaded.value && snapshot.value !== serialized())

  function apply(saved: OpenAIEvalConfig) {
    foldedLegacyStability.value = [saved.custom_balance, ...(saved.policies ?? []).map(rule => rule.custom_balance)]
      .some(weights => Number(weights?.stability) > 0)
    config.revision = saved.revision
    config.effects_enabled = Boolean(saved.effects_enabled)
    config.bps_auto_enabled = Boolean(saved.bps_auto_enabled)
    config.scheduling_policy = saved.scheduling_policy ?? ''
    config.custom_balance = normalizeCustomBalance(saved.custom_balance)
    config.policies = (saved.policies ?? []).map(rule => ({
      ...rule,
      reasoning_effort: rule.reasoning_effort || '',
      ...(rule.policy === 'custom_balance' ? { custom_balance: normalizeCustomBalance(rule.custom_balance ?? saved.custom_balance) } : {})
    }))
    config.bps_accounts = (saved.bps_accounts ?? []).map(item => normalizeBPSAccount({ ...item }))
    config.max_request_attempts = normalizeMaxRequestAttempts(saved.max_request_attempts)
    config.quality_refresh_interval_seconds = normalizeQualityRefreshInterval(saved.quality_refresh_interval_seconds)
    config.quality_refreshed_at = saved.quality_refreshed_at ?? null
    config.quality_next_refresh_at = saved.quality_next_refresh_at ?? null
    savedQualityRefreshInterval.value = config.quality_refresh_interval_seconds
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
    config.bps_accounts?.forEach(normalizeBPSAccount)
    config.max_request_attempts = normalizeMaxRequestAttempts(config.max_request_attempts)
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

  function accountLabel(accountID: number) {
    const account = accounts.value.find(item => item.id === accountID)
    return account ? `${account.name} #${accountID}` : `#${accountID}`
  }

  function accountName(accountID: number) {
    const account = accounts.value.find(item => item.id === accountID)
    return account ? account.name : `#${accountID}`
  }

  /**
   * Records a manual ranking refresh. Only the read-only timestamps change;
   * they are not part of the save payload, so unsaved edits stay unsaved.
   */
  function applyQualityRefresh(result: { refreshed_at: string; next_refresh_at?: string | null }) {
    config.quality_refreshed_at = result.refreshed_at
    if (result.next_refresh_at !== undefined) config.quality_next_refresh_at = result.next_refresh_at
  }

  return { config, catalog, accounts, loading, loaded, saving, conflict, loadError, dirty, load, reloadConfig, save, accountName, accountLabel, savedQualityRefreshInterval, applyQualityRefresh, foldedLegacyStability }
}
