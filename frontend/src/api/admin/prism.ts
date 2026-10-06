/**
 * Admin Prism account endpoints: cookie import, browser PKCE sign-in with a
 * pasted callback, credential refresh and the per-account model catalog.
 *
 * Credentials only ever travel in request bodies; no response type carries
 * them, and callers must not echo submitted cookies back into the UI.
 */

import { apiClient } from '../client'

/** Server caps one import request at 100 accounts. */
export const PRISM_IMPORT_MAX_ACCOUNTS = 100
/**
 * Each account is verified for up to 30s inside a 150s handler, so the UI
 * sends small chunks to keep every request inside the server deadline.
 */
export const PRISM_IMPORT_CHUNK_SIZE = 4

const IMPORT_TIMEOUT_MS = 160_000
const EXCHANGE_TIMEOUT_MS = 120_000
const REFRESH_TIMEOUT_MS = 70_000
const CATALOG_TIMEOUT_MS = 60_000

export interface PrismAccountSettings {
  name?: string
  group_ids?: number[]
  proxy_id?: number | null
  concurrency?: number
  priority?: number
}

export interface PrismImportEntry {
  cookies: string
  name?: string
}

export interface PrismImportRequest extends PrismAccountSettings {
  cookies?: string
  accounts?: PrismImportEntry[]
  update_existing?: boolean
  /** The server requires verification; it is always sent as true. */
  verify?: true
  /** Re-import onto this account; the verified identity must match. */
  account_id?: number
}

export type PrismImportAction = 'created' | 'updated' | 'skipped' | 'failed'

export interface PrismImportItem {
  index: number
  action: PrismImportAction | string
  account_id?: number
  code?: string
  message?: string
}

export interface PrismImportResult {
  total: number
  created: number
  updated: number
  failed: number
  items: PrismImportItem[]
}

export type PrismOAuthState = 'pending' | 'exchanging' | 'completed' | 'failed' | 'expired' | 'canceled'

export interface PrismOAuthStatus {
  session_id: string
  authorize_url?: string
  redirect_uri?: string
  expires_at: string
  status: PrismOAuthState
  /** The OpenAI token exchange succeeded. Says nothing about Prism access. */
  login_succeeded: boolean
  /** Prism confirmed the identity and returned a non-empty model catalog. */
  prism_verified: boolean
  account_id?: number
  code?: string
  message?: string
}

export interface PrismOAuthBeginRequest extends PrismAccountSettings {
  /** Sign in again for an existing Prism account. */
  account_id?: number
}

export interface PrismCatalogModel {
  id: string
  label: string
  reasoning_efforts: string[]
  default_reasoning_effort: string
}

export interface PrismCatalog {
  /** Actual upstream model IDs; the alias editor maps onto these. */
  models: PrismCatalogModel[]
  /**
   * Request names clients may use: verified aliases when the account has a
   * mapping, otherwise the raw IDs. `null` means an older server that does not
   * send the field; an empty array means no name is currently selectable.
   */
  public_models: PrismCatalogModel[] | null
  source: string
}

export function isPrismOAuthTerminal(state: PrismOAuthState): boolean {
  return state === 'completed' || state === 'failed' || state === 'expired' || state === 'canceled'
}

export function chunkPrismEntries<T>(entries: T[], size = PRISM_IMPORT_CHUNK_SIZE): T[][] {
  const chunks: T[][] = []
  for (let i = 0; i < entries.length; i += size) chunks.push(entries.slice(i, i + size))
  return chunks
}

/**
 * Splits pasted text into one cookie per non-empty line. A leading
 * "Cookie:" header name is kept; the server strips it.
 */
export function parsePrismCookieLines(raw: string): string[] {
  return raw
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
}

function asString(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

/**
 * The catalog response shape is owned by the backend; normalize it here so
 * components never depend on the raw payload.
 */
function normalizeModelList(list: unknown[]): PrismCatalogModel[] {
  const seen = new Set<string>()
  const models: PrismCatalogModel[] = []
  for (const item of list) {
    if (!item || typeof item !== 'object') continue
    const entry = item as Record<string, unknown>
    const id = asString(entry.id)
    if (!id || seen.has(id)) continue
    seen.add(id)
    const rawEfforts = entry.reasoning_efforts ?? entry.efforts
    const efforts = Array.isArray(rawEfforts) ? rawEfforts.map(asString).filter(Boolean) : []
    models.push({
      id,
      label: asString(entry.label) || asString(entry.display_name),
      reasoning_efforts: efforts,
      default_reasoning_effort: asString(entry.default_reasoning_effort)
    })
  }
  return models
}

export function normalizePrismCatalog(raw: unknown): PrismCatalog {
  const body = (raw && typeof raw === 'object' ? raw : {}) as Record<string, unknown>
  const list = Array.isArray(body.models) ? body.models : Array.isArray(raw) ? (raw as unknown[]) : []
  return {
    models: normalizeModelList(list),
    public_models: Array.isArray(body.public_models) ? normalizeModelList(body.public_models) : null,
    source: asString(body.source)
  }
}

/** Names a quality target may request; raw IDs only for servers without public_models. */
export function selectablePrismModels(catalog: Pick<PrismCatalog, 'models' | 'public_models'>): PrismCatalogModel[] {
  return catalog.public_models ?? catalog.models
}

export async function importAccounts(
  request: PrismImportRequest,
  options?: { signal?: AbortSignal }
): Promise<PrismImportResult> {
  const { data } = await apiClient.post<PrismImportResult>(
    '/admin/accounts/prism/import',
    { ...request, verify: true },
    { signal: options?.signal, timeout: IMPORT_TIMEOUT_MS }
  )
  return { ...data, items: data.items ?? [] }
}

export async function beginOAuth(request: PrismOAuthBeginRequest = {}): Promise<PrismOAuthStatus> {
  const { data } = await apiClient.post<PrismOAuthStatus>('/admin/accounts/prism/oauth/begin', request)
  return data
}

export async function exchangeOAuth(
  sessionId: string,
  callbackUrl: string,
  options?: { signal?: AbortSignal }
): Promise<PrismOAuthStatus> {
  const { data } = await apiClient.post<PrismOAuthStatus>(
    '/admin/accounts/prism/oauth/exchange',
    { session_id: sessionId, callback_url: callbackUrl },
    { signal: options?.signal, timeout: EXCHANGE_TIMEOUT_MS }
  )
  return data
}

export async function getOAuthStatus(sessionId: string, options?: { signal?: AbortSignal }): Promise<PrismOAuthStatus> {
  const { data } = await apiClient.get<PrismOAuthStatus>('/admin/accounts/prism/oauth/status', {
    params: { session_id: sessionId },
    signal: options?.signal
  })
  return data
}

export async function cancelOAuth(sessionId: string): Promise<PrismOAuthStatus> {
  const { data } = await apiClient.post<PrismOAuthStatus>('/admin/accounts/prism/oauth/cancel', {
    session_id: sessionId
  })
  return data
}

export async function refreshAccount(id: number): Promise<PrismImportItem> {
  const { data } = await apiClient.post<PrismImportItem>(`/admin/accounts/${id}/prism/refresh`, undefined, {
    timeout: REFRESH_TIMEOUT_MS
  })
  return data
}

export async function getAccountModels(
  id: number,
  options?: { refresh?: boolean; signal?: AbortSignal }
): Promise<PrismCatalog> {
  const { data } = await apiClient.get<unknown>(`/admin/accounts/${id}/prism/models`, {
    params: options?.refresh ? { refresh: 'true' } : undefined,
    signal: options?.signal,
    timeout: CATALOG_TIMEOUT_MS
  })
  return normalizePrismCatalog(data)
}

export const prismAPI = {
  importAccounts,
  beginOAuth,
  exchangeOAuth,
  getOAuthStatus,
  cancelOAuth,
  refreshAccount,
  getAccountModels
}

export default prismAPI
