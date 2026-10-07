import { reactive } from 'vue'
import { prismAPI, selectablePrismModels, type PrismCatalogModel } from '@/api/admin/prism'
import type { OpenAIEvalModelCatalog, OpenAIEvalRouteConfig } from '@/api/admin/accounts'
import type { AccountListItem } from '@/types'
import type { EvalTestType } from './modelIntegrity'

/**
 * Prism quality evaluation runs natively against each account's own model
 * catalog. A target requests a public name (a verified alias, or the raw ID
 * when the account has no mapping); every coverage check uses the actual
 * upstream model that name resolves to. Candy works for any catalog model;
 * Fingerprint and ModelTrace need a versioned baseline for the actual model.
 * The Codex ticket probe is an OpenAI OAuth feature and never applies.
 */

export type PrismCatalogState =
  | { status: 'loading' }
  | {
      status: 'ready'
      /** Actual upstream models. */
      models: PrismCatalogModel[]
      /** Names a target may request. */
      selectable: PrismCatalogModel[]
    }
  | { status: 'error'; message: string }

export type EvalAccountPick = Pick<AccountListItem, 'id' | 'platform' | 'type' | 'credentials'>

export function isPrismAccount(account: Pick<AccountListItem, 'platform'> | null | undefined): boolean {
  return account?.platform === 'prism'
}

/** Accounts that can own a quality-test target. */
export function isEvalTargetAccount(account: Pick<AccountListItem, 'platform' | 'type'>): boolean {
  return account.platform === 'openai' || (account.platform === 'prism' && account.type === 'oauth')
}

/**
 * Model actually evaluated for a Prism request name. Only an exact account
 * alias is resolved here; the server remains the authority.
 */
export function prismUpstreamModel(account: EvalAccountPick | null | undefined, requested: string): string {
  const mapping = account?.credentials?.model_mapping
  if (mapping && typeof mapping === 'object' && !Array.isArray(mapping)) {
    const target = (mapping as Record<string, unknown>)[requested]
    if (typeof target === 'string' && target.trim()) return target.trim()
  }
  return requested
}

export type PrismTestAvailability =
  | { available: true; notice?: 'modeltrace_coverage_unknown' | 'not_in_catalog' }
  | { available: false; reason: 'no_fingerprint_baseline' | 'no_modeltrace_baseline' | 'not_supported' }

export function prismTestAvailability(
  type: EvalTestType,
  account: EvalAccountPick | null | undefined,
  requested: string,
  evalCatalog: OpenAIEvalModelCatalog | null,
  prismCatalog?: PrismCatalogState
): PrismTestAvailability {
  if (type === 'state_probe') return { available: false, reason: 'not_supported' }
  const model = prismUpstreamModel(account, requested)
  if (type === 'fingerprint' && !(evalCatalog?.baseline_models ?? []).includes(model)) {
    return { available: false, reason: 'no_fingerprint_baseline' }
  }
  // An older server does not publish ModelTrace coverage; say so instead of guessing.
  const traceModels = evalCatalog?.modeltrace?.models
  if (type === 'modeltrace' && traceModels && !traceModels.includes(model)) {
    return { available: false, reason: 'no_modeltrace_baseline' }
  }
  if (prismCatalog?.status === 'ready' && !prismCatalog.selectable.some(item => item.id === requested)) {
    return { available: true, notice: 'not_in_catalog' }
  }
  if (type === 'modeltrace' && !traceModels) return { available: true, notice: 'modeltrace_coverage_unknown' }
  return { available: true }
}

/** Models every given catalog offers, in the order of the first one. */
export function sharedPrismModels(catalogs: PrismCatalogModel[][]): PrismCatalogModel[] {
  if (!catalogs.length) return []
  const [first, ...rest] = catalogs
  return first.filter(model => rest.every(list => list.some(item => item.id === model.id)))
}

/**
 * Efforts every account accepts for the model. '' (the account default) is
 * always valid because the server substitutes the model's own default.
 */
export function sharedPrismEfforts(catalogs: PrismCatalogModel[][], modelID: string): string[] {
  const lists = catalogs.map(list => list.find(item => item.id === modelID)?.reasoning_efforts ?? [])
  if (!lists.length) return ['']
  const [first, ...rest] = lists
  return ['', ...first.filter(effort => effort && rest.every(list => list.includes(effort)))]
}

/** The default effort shared by every account, or '' when they differ or none is declared. */
export function sharedPrismDefaultEffort(catalogs: PrismCatalogModel[][], modelID: string): string {
  const defaults = new Set(catalogs.map(list => list.find(item => item.id === modelID)?.default_reasoning_effort ?? ''))
  if (defaults.size !== 1) return ''
  return [...defaults][0]
}

/**
 * Shared per-account catalog cache. Each account is fetched once per page
 * visit; `reload` refetches after an error or on request.
 */
export function usePrismEvalCatalogs() {
  const catalogs = reactive(new Map<number, PrismCatalogState>())
  const pending = new Map<number, Promise<void>>()

  function ensure(accountID: number, force = false): Promise<void> {
    const current = catalogs.get(accountID)
    if (!force && current && current.status !== 'error') return pending.get(accountID) ?? Promise.resolve()
    if (pending.has(accountID)) return pending.get(accountID)!
    catalogs.set(accountID, { status: 'loading' })
    const request = prismAPI.getAccountModels(accountID, { refresh: force })
      .then(result => { catalogs.set(accountID, { status: 'ready', models: result.models, selectable: selectablePrismModels(result) }) })
      .catch((error: unknown) => {
        const message = typeof error === 'object' && error && 'message' in error ? String((error as { message?: unknown }).message ?? '') : ''
        catalogs.set(accountID, { status: 'error', message })
      })
      .finally(() => { pending.delete(accountID) })
    pending.set(accountID, request)
    return request
  }

  function get(accountID: number): PrismCatalogState | undefined {
    return catalogs.get(accountID)
  }

  return { catalogs, ensure, reload: (accountID: number) => ensure(accountID, true), get }
}

export type PrismEvalCatalogs = ReturnType<typeof usePrismEvalCatalogs>

export function isPrismRouteOf(accounts: AccountListItem[], route: Pick<OpenAIEvalRouteConfig, 'account_id'>): boolean {
  return isPrismAccount(accounts.find(item => item.id === route.account_id))
}
