import { describe, expect, it } from 'vitest'
import {
  isOfficialOpenAIBaseUrl,
  isOpenAIOpaqueUpstreamBaseUrl,
} from '../openaiBaseUrl'

describe('OpenAI base URL classification', () => {
  it.each([
    'https://api.openai.com',
    'https://api.openai.com/v1',
    'https://API.OPENAI.COM/v1/',
  ])('recognizes %s as the official endpoint', (value) => {
    expect(isOfficialOpenAIBaseUrl(value)).toBe(true)
    expect(isOpenAIOpaqueUpstreamBaseUrl(value)).toBe(false)
  })

  it.each([
    'https://sub2api.example/v1',
    'http://127.0.0.1:8317/v1',
    'https://relay.example/responses',
  ])('recognizes %s as an opaque upstream candidate', (value) => {
    expect(isOfficialOpenAIBaseUrl(value)).toBe(false)
    expect(isOpenAIOpaqueUpstreamBaseUrl(value)).toBe(true)
  })

  it.each(['', '   ', 'not a URL'])('does not enable the option for %s', (value) => {
    expect(isOpenAIOpaqueUpstreamBaseUrl(value)).toBe(false)
  })
})
