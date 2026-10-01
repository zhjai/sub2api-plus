import zh from '@/i18n/locales/zh'

/** Minimal Chinese translator so specs can assert the real copy admins read. */
export function zhT(key: string, params: Record<string, unknown> = {}): string {
  let current: unknown = zh
  for (const segment of key.split('.')) {
    if (current === null || typeof current !== 'object') return key
    current = (current as Record<string, unknown>)[segment]
  }
  if (typeof current !== 'string') return key
  return current.replace(/\{(\w+)\}/g, (_, name: string) => (name in params ? String(params[name]) : `{${name}}`))
}
