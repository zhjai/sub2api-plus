const OFFICIAL_OPENAI_HOSTNAMES = new Set(['api.openai.com'])

function parseBaseUrl(value: string): URL | null {
  const trimmed = value.trim()
  if (!trimmed) return null
  try {
    const parsed = new URL(trimmed)
    if (parsed.protocol !== 'http:' && parsed.protocol !== 'https:') return null
    return parsed
  } catch {
    return null
  }
}

export function isOfficialOpenAIBaseUrl(value: string): boolean {
  const parsed = parseBaseUrl(value)
  return parsed !== null && OFFICIAL_OPENAI_HOSTNAMES.has(parsed.hostname.toLowerCase())
}

export function isOpenAIOpaqueUpstreamBaseUrl(value: string): boolean {
  return parseBaseUrl(value) !== null && !isOfficialOpenAIBaseUrl(value)
}
