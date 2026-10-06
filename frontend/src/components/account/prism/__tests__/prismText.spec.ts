import { describe, expect, it } from 'vitest'
import {
  chunkPrismEntries,
  isPrismOAuthTerminal,
  normalizePrismCatalog,
  selectablePrismModels,
  parsePrismCookieLines,
  PRISM_IMPORT_CHUNK_SIZE
} from '@/api/admin/prism'
import {
  aliasRowIssues,
  aliasRowsFromMapping,
  callbackIssue,
  mappingFromAliasRows,
  prismErrorText,
  sameMapping
} from '@/components/account/prism/prismText'

const t = (key: string) => `t:${key}`
const known = new Set(['admin.accounts.prism.errors.no_entitlement', 'admin.accounts.prism.errors.generic'])
const te = (key: string) => known.has(key)

describe('Prism API helpers', () => {
  it('splits pasted cookies into one non-empty line per account', () => {
    expect(parsePrismCookieLines(' a=1; b=2 \n\n\r\nCookie: c=3\n  ')).toEqual(['a=1; b=2', 'Cookie: c=3'])
  })

  it('chunks imports so each request stays inside the server deadline', () => {
    const entries = Array.from({ length: 10 }, (_, i) => i)
    const chunks = chunkPrismEntries(entries)
    expect(chunks.every((chunk) => chunk.length <= PRISM_IMPORT_CHUNK_SIZE)).toBe(true)
    expect(chunks.flat()).toEqual(entries)
  })

  it('treats only finished sign-ins as terminal', () => {
    expect(isPrismOAuthTerminal('pending')).toBe(false)
    expect(isPrismOAuthTerminal('exchanging')).toBe(false)
    for (const state of ['completed', 'failed', 'expired', 'canceled'] as const) {
      expect(isPrismOAuthTerminal(state)).toBe(true)
    }
  })

  it('normalizes the catalog without inventing models or efforts', () => {
    const catalog = normalizePrismCatalog({
      source: 'prism_account_catalog',
      models: [
        { id: 'model-a', label: 'Model A', reasoning_efforts: ['low', 'high'], default_reasoning_effort: 'low' },
        { id: 'model-a', label: 'duplicate' },
        { id: '', label: 'no id' },
        { id: 'model-b' }
      ]
    })
    expect(catalog.source).toBe('prism_account_catalog')
    expect(catalog.models).toEqual([
      { id: 'model-a', label: 'Model A', reasoning_efforts: ['low', 'high'], default_reasoning_effort: 'low' },
      { id: 'model-b', label: '', reasoning_efforts: [], default_reasoning_effort: '' }
    ])
    expect(normalizePrismCatalog(null).models).toEqual([])
    // An older server without public_models is distinguishable from an empty list.
    expect(catalog.public_models).toBeNull()
    expect(selectablePrismModels(catalog).map(model => model.id)).toEqual(['model-a', 'model-b'])
  })

  it('keeps raw and public catalogs apart and selects public names when present', () => {
    const catalog = normalizePrismCatalog({
      source: 'prism_account_catalog',
      models: [{ id: 'raw-a', label: 'Raw A', reasoning_efforts: ['low'] }],
      public_models: [{ id: 'alias-a', object: 'model', type: 'model', display_name: 'Raw A', reasoning_efforts: ['low'], default_reasoning_effort: 'low' }]
    })
    expect(catalog.models.map(model => model.id)).toEqual(['raw-a'])
    expect(catalog.public_models).toEqual([{ id: 'alias-a', label: 'Raw A', reasoning_efforts: ['low'], default_reasoning_effort: 'low' }])
    expect(selectablePrismModels(catalog).map(model => model.id)).toEqual(['alias-a'])
    // An empty public list means nothing is selectable; it never falls back to raw IDs.
    expect(selectablePrismModels(normalizePrismCatalog({ models: [{ id: 'raw-a' }], public_models: [] }))).toEqual([])
  })
})

describe('Prism text helpers', () => {
  it('prefers localized copy for known codes and falls back to the server message', () => {
    expect(prismErrorText(t, te, 'no_entitlement', 'raw')).toBe('t:admin.accounts.prism.errors.no_entitlement')
    expect(prismErrorText(t, te, 'brand_new_code', 'Server says hi')).toBe('Server says hi')
    expect(prismErrorText(t, te, undefined, '')).toBe('t:admin.accounts.prism.errors.generic')
  })

  it('pre-checks the pasted callback address', () => {
    expect(callbackIssue('')).toBe('empty')
    expect(callbackIssue('https://example.com/?code=1&state=2')).toBe('wrong_address')
    expect(callbackIssue('http://localhost:1455/auth/callback?state=2')).toBe('missing_code')
    expect(callbackIssue('http://localhost:1455/auth/callback?code=1&state=2')).toBeNull()
    // Denials go to the server so it can close the session with a clear reason.
    expect(callbackIssue('http://localhost:1455/auth/callback?error=access_denied&state=2')).toBeNull()
  })

  it('rejects aliases that would substitute or guess a model', () => {
    const catalog = new Set(['model-a', 'model-b'])
    const issues = aliasRowIssues(
      [
        { alias: 'fast', target: 'model-a' },
        { alias: '', target: 'model-a' },
        { alias: 'gpt-*', target: 'model-a' },
        { alias: 'dup', target: 'model-a' },
        { alias: 'dup', target: 'model-b' },
        { alias: 'pending', target: '' },
        { alias: 'gone', target: 'model-retired' }
      ],
      catalog
    )
    expect(issues).toEqual([null, 'empty_alias', 'wildcard', 'duplicate', 'duplicate', 'missing_target', 'unknown_target'])
  })

  it('round-trips the stored model mapping', () => {
    const rows = aliasRowsFromMapping({ zeta: 'model-b', alpha: 'model-a', ignored: 3 })
    expect(rows).toEqual([
      { alias: 'alpha', target: 'model-a' },
      { alias: 'zeta', target: 'model-b' }
    ])
    const mapping = mappingFromAliasRows([...rows, { alias: '  ', target: 'x' }])
    expect(sameMapping(mapping, { alpha: 'model-a', zeta: 'model-b' })).toBe(true)
    expect(sameMapping(mapping, { alpha: 'model-a' })).toBe(false)
    expect(aliasRowsFromMapping(undefined)).toEqual([])
  })
})

describe('Prism model-integrity gating', () => {
  it('never treats a Prism account as eligible for the Codex ticket probe or BPS', async () => {
    const { isDirectOAuthAccount } = await import('@/views/admin/modelIntegrity/modelIntegrity')
    expect(isDirectOAuthAccount({ platform: 'prism', type: 'oauth', parent_account_id: null })).toBe(false)
    expect(isDirectOAuthAccount({ platform: 'openai', type: 'oauth', parent_account_id: null })).toBe(true)
  })
})
