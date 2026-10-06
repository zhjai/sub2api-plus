import { extractApiErrorCode, extractApiErrorMessage } from '@/utils/apiError'

type Translate = (key: string, params?: Record<string, unknown>) => string
type TranslationExists = (key: string) => boolean

const ERROR_NAMESPACE = 'admin.accounts.prism.errors'

/**
 * Maps a Prism error code to localized, actionable copy. Unknown codes fall
 * back to the server message, which never carries credentials.
 */
export function prismErrorText(
  t: Translate,
  te: TranslationExists,
  code: string | undefined,
  message: string | undefined
): string {
  if (code) {
    const key = `${ERROR_NAMESPACE}.${code}`
    if (te(key)) return t(key)
  }
  const fallback = (message || '').trim()
  return fallback || t(`${ERROR_NAMESPACE}.generic`)
}

export function prismRequestErrorText(t: Translate, te: TranslationExists, error: unknown): string {
  const code = extractApiErrorCode(error)
  return prismErrorText(t, te, code, extractApiErrorMessage(error, ''))
}

export function isRequestCanceled(error: unknown): boolean {
  if (!error || typeof error !== 'object') return false
  const e = error as { code?: unknown; name?: unknown }
  return e.code === 'ERR_CANCELED' || e.name === 'CanceledError' || e.name === 'AbortError'
}

const CALLBACK_PREFIX = 'http://localhost:1455/auth/callback'

export type CallbackIssue = 'empty' | 'wrong_address' | 'missing_code' | null

/**
 * Local pre-check only; the server validates state, uniqueness and origin.
 */
export function callbackIssue(raw: string): CallbackIssue {
  const value = raw.trim()
  if (!value) return 'empty'
  if (!value.startsWith(`${CALLBACK_PREFIX}?`)) return 'wrong_address'
  let params: URLSearchParams
  try {
    params = new URL(value).searchParams
  } catch {
    return 'wrong_address'
  }
  if (params.get('error')) return null
  if (!params.get('code') || !params.get('state')) return 'missing_code'
  return null
}

export interface PrismAliasRow {
  alias: string
  target: string
}

export type AliasIssue = 'empty_alias' | 'wildcard' | 'duplicate' | 'missing_target' | 'unknown_target' | null

export function aliasRowsFromMapping(mapping: unknown): PrismAliasRow[] {
  if (!mapping || typeof mapping !== 'object' || Array.isArray(mapping)) return []
  return Object.entries(mapping as Record<string, unknown>)
    .filter(([, target]) => typeof target === 'string')
    .map(([alias, target]) => ({ alias, target: target as string }))
    .sort((a, b) => a.alias.localeCompare(b.alias))
}

/**
 * Account aliases resolve by exact name, so wildcards are rejected, and every
 * target must exist in the account's current catalog. A target that left the
 * catalog is reported rather than swapped for another model.
 */
export function aliasRowIssues(rows: PrismAliasRow[], catalogIds: ReadonlySet<string>): AliasIssue[] {
  const counts = new Map<string, number>()
  for (const row of rows) {
    const alias = row.alias.trim()
    if (alias) counts.set(alias, (counts.get(alias) ?? 0) + 1)
  }
  return rows.map((row) => {
    const alias = row.alias.trim()
    const target = row.target.trim()
    if (!alias) return 'empty_alias'
    if (alias.includes('*')) return 'wildcard'
    if ((counts.get(alias) ?? 0) > 1) return 'duplicate'
    if (!target) return 'missing_target'
    if (!catalogIds.has(target)) return 'unknown_target'
    return null
  })
}

export function mappingFromAliasRows(rows: PrismAliasRow[]): Record<string, string> {
  const mapping: Record<string, string> = {}
  for (const row of rows) {
    const alias = row.alias.trim()
    const target = row.target.trim()
    if (alias && target) mapping[alias] = target
  }
  return mapping
}

export function sameMapping(a: Record<string, string>, b: Record<string, string>): boolean {
  const keys = Object.keys(a)
  if (keys.length !== Object.keys(b).length) return false
  return keys.every((key) => b[key] === a[key])
}
