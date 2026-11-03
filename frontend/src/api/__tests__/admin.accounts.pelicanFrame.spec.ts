import { beforeEach, describe, expect, it, vi } from 'vitest'

const { post } = vi.hoisted(() => ({
  post: vi.fn()
}))

vi.mock('@/api/client', () => ({
  apiClient: { post }
}))

import { createOpenAIEvalPelicanFrameToken } from '@/api/admin/accounts'

describe('Pelican frame token API', () => {
  beforeEach(() => {
    post.mockReset()
    post.mockResolvedValue({ data: { url: '/api/v1/openai-eval/pelican-frame/tok', expires_at: '2026-11-02T10:05:00Z' } })
  })

  it('asks the accounts evaluation prefix for a preview or play frame URL', async () => {
    const preview = await createOpenAIEvalPelicanFrameToken(42, 'preview')
    await createOpenAIEvalPelicanFrameToken(42, 'play')
    expect(post).toHaveBeenNthCalledWith(1, '/admin/accounts/evaluations/runs/42/pelican-frame-token', { mode: 'preview' })
    expect(post).toHaveBeenNthCalledWith(2, '/admin/accounts/evaluations/runs/42/pelican-frame-token', { mode: 'play' })
    expect(preview).toEqual({ url: '/api/v1/openai-eval/pelican-frame/tok', expires_at: '2026-11-02T10:05:00Z' })
  })
})
