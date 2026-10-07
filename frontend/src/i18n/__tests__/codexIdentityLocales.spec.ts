import { describe, expect, it } from 'vitest'
import zh from '@/i18n/locales/zh'
import en from '@/i18n/locales/en'

// The experiment allows a continuation started under the same credential, API
// key, mode and policy revision (openai_identity_continuation.go) and refuses
// unknown, expired or mismatched ones. The copy must not say all are refused.
describe('Codex identity experiment copy', () => {
  const copy = (locale: typeof zh) => (locale as any).admin.accounts.openai.codexIdentity

  it('describes validated continuations in Chinese', () => {
    const text: string = copy(zh).supportedPaths
    expect(text).toContain('同一凭据、同一 API Key、同一模式和策略修订')
    expect(text).toContain('可以继续')
    expect(text).toMatch(/未知、已过期或属于其他凭据、API Key、修订的续写会被拒绝/)
    expect(text).not.toMatch(/续写旧会话的请求会在发送前被明确拒绝/)
  })

  it('describes validated continuations in English', () => {
    const text: string = copy(en).supportedPaths
    expect(text).toContain('same credential, API key, mode and policy revision can continue')
    expect(text).toMatch(/unknown, expired, or other-credential, other-key or other-revision continuations are rejected/)
  })

  it('explains unsupported Prism budget transport in both locales', () => {
    const zhText: string = (zh as any).admin.modelIntegrity.tests.background.deferredReasons.evaluation_budget_transport_unsupported
    const enText: string = (en as any).admin.modelIntegrity.tests.background.deferredReasons.evaluation_budget_transport_unsupported
    expect(zhText).toContain('Prism')
    expect(zhText).toContain('实际发送计数')
    expect(enText).toContain('Prism')
    expect(enText).toContain('actual sends')
  })
})
