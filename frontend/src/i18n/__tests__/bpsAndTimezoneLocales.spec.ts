import { describe, expect, it } from 'vitest'
import zhAccounts from '../locales/zh/admin/accounts'
import enAccounts from '../locales/en/admin/accounts'

function collectLeafPaths(obj: Record<string, unknown>, prefix = ''): string[] {
  const paths: string[] = []
  for (const [key, value] of Object.entries(obj)) {
    const next = prefix ? `${prefix}.${key}` : key
    if (value && typeof value === 'object' && !Array.isArray(value)) {
      paths.push(...collectLeafPaths(value as Record<string, unknown>, next))
    } else {
      paths.push(next)
      expect(typeof value, `Expected string at ${next}`).toBe('string')
      expect((value as string).trim().length, `Expected non-empty translation at ${next}`).toBeGreaterThan(0)
    }
  }
  return paths.sort()
}

describe('BPS account and upstream client timezone i18n parity (zh/en)', () => {
  it('maintains identical non-empty key trees for bpsAccount in zh and en', () => {
    const zhPaths = collectLeafPaths(zhAccounts.accounts.bpsAccount as Record<string, unknown>)
    const enPaths = collectLeafPaths(enAccounts.accounts.bpsAccount as Record<string, unknown>)
    expect(zhPaths).toEqual(enPaths)

    expect(zhAccounts.accounts.bpsAccount.usage.title).toBe('本地用量')
    expect(zhAccounts.accounts.bpsAccount.options.skipHistoricalEncrypted).toBe('忽略历史加密内容')
    expect(zhAccounts.accounts.bpsAccount.options.hostedToolsOmit).toBe('省略并提示')
    expect(zhAccounts.accounts.bpsAccount.options.hostedToolsReject).toBe('拒绝请求')
    expect(zhAccounts.accounts.bpsAccount.models.prefillRecommended).toContain('gpt-6-astra')
    expect(zhAccounts.accounts.bpsAccount.models.prefillRecommended).toContain('gpt-6.1-sol')
    expect(zhAccounts.accounts.bpsAccount.bulk.incompleteSubtypeHiddenHint).toContain('无法确认其授权子类型')
    expect(enAccounts.accounts.bpsAccount.bulk.incompleteSubtypeHiddenHint).toContain('authorization subtype cannot be verified')
  })

  it('maintains accurate bilingual BPS OAuth scope copy for upstream Compact, limited gpt-image-2 subset, routing boundaries, and history requirements', () => {
    const zhOAuthPaths = collectLeafPaths(zhAccounts.accounts.oauth.openai.bps as Record<string, unknown>)
    const enOAuthPaths = collectLeafPaths(enAccounts.accounts.oauth.openai.bps as Record<string, unknown>)
    expect(zhOAuthPaths).toEqual(enOAuthPaths)

    const zhScope = zhAccounts.accounts.oauth.openai.bps.scopeNote
    const enScope = enAccounts.accounts.oauth.openai.bps.scopeNote

    expect(zhScope).toContain('真实上游 Compact')
    expect(zhScope).toContain('gpt-image-2')
    expect(zhScope).toContain('PNG')
    expect(zhScope).toContain('单图编辑')
    expect(zhScope).toContain('不支持 WebSocket')
    expect(zhScope).toContain('previous_response_id')
    expect(zhScope).toContain('兼容的原生授权或其他兼容账号')
    expect(zhScope).toContain('不保证任何模型')
    expect(zhScope).not.toContain('不支持原生 compact、WebSocket、图片')

    expect(enScope).toContain('genuine upstream Compact')
    expect(enScope).toContain('gpt-image-2')
    expect(enScope).toContain('PNG')
    expect(enScope).toContain('single-image-edit')
    expect(enScope).toContain('WebSocket')
    expect(enScope).toContain('previous_response_id')
    expect(enScope).toContain('compatible native grant or another compatible account')
    expect(enScope).toContain('not guaranteed')
    expect(enScope).not.toContain('Native compact, WebSocket, images, Chat Completions and Messages are not available')
  })

  it('maintains identical non-empty key trees for upstreamClientTimezone in zh and en without false anonymity claims', () => {
    const zhPaths = collectLeafPaths(zhAccounts.accounts.upstreamClientTimezone as Record<string, unknown>)
    const enPaths = collectLeafPaths(enAccounts.accounts.upstreamClientTimezone as Record<string, unknown>)
    expect(zhPaths).toEqual(enPaths)

    expect(zhAccounts.accounts.upstreamClientTimezone.desc).toContain('配额定时重置时区')
    expect(enAccounts.accounts.upstreamClientTimezone.desc).toContain('quota reset timezone')
    expect(enAccounts.accounts.upstreamClientTimezone.desc.toLowerCase()).not.toContain('total anonymity')
    expect(enAccounts.accounts.upstreamClientTimezone.boundaryNote.toLowerCase()).not.toContain('total anonymity')
  })
})
