import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { zhT } from '@/views/admin/modelIntegrity/__tests__/zhT'

const api = vi.hoisted(() => ({ createOpenAIEvalPelicanFrameToken: vi.fn() }))
vi.mock('@/api/admin/accounts', () => ({ default: api, accountsAPI: api }))
vi.mock('vue-i18n', async importOriginal => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: zhT })
}))

import PelicanFrame, { clearPelicanFrameGrants, safePelicanFrameURL } from '../PelicanFrame.vue'

const future = () => new Date(Date.now() + 5 * 60_000).toISOString()

beforeEach(() => {
  vi.clearAllMocks()
  clearPelicanFrameGrants()
})

describe('PelicanFrame', () => {
  it('loads the server frame URL in an opaque-origin sandbox with no referrer, never inline markup', async () => {
    api.createOpenAIEvalPelicanFrameToken.mockResolvedValue({ url: '/api/v1/openai-eval/pelican-frame/abc', expires_at: future() })
    const wrapper = mount(PelicanFrame, { props: { runId: 7, mode: 'play', title: '鹈鹕' } })
    await flushPromises()

    expect(api.createOpenAIEvalPelicanFrameToken).toHaveBeenCalledWith(7, 'play')
    const frame = wrapper.get('[data-testid="pelican-frame"]')
    // Exactly allow-scripts: no allow-same-origin, so the page cannot reach this origin.
    expect(frame.attributes('sandbox')).toBe('allow-scripts')
    expect(frame.attributes('referrerpolicy')).toBe('no-referrer')
    expect(frame.attributes('src')).toBe('/api/v1/openai-eval/pelican-frame/abc')
    expect(frame.attributes('srcdoc')).toBeUndefined()
    expect(wrapper.html()).not.toMatch(/srcdoc|blob:/)
    wrapper.unmount()
  })

  it('reuses a live grant for the same run and mode instead of asking again', async () => {
    api.createOpenAIEvalPelicanFrameToken.mockResolvedValue({ url: '/api/v1/openai-eval/pelican-frame/p1', expires_at: future() })
    const first = mount(PelicanFrame, { props: { runId: 9, mode: 'preview', title: 't' } })
    await flushPromises()
    const second = mount(PelicanFrame, { props: { runId: 9, mode: 'preview', title: 't' } })
    await flushPromises()
    expect(api.createOpenAIEvalPelicanFrameToken).toHaveBeenCalledTimes(1)
    expect(second.get('[data-testid="pelican-frame"]').attributes('src')).toBe('/api/v1/openai-eval/pelican-frame/p1')
    first.unmount()
    second.unmount()
  })

  it('refuses a non-http frame URL and offers a retry', async () => {
    api.createOpenAIEvalPelicanFrameToken.mockResolvedValue({ url: 'javascript:alert(1)', expires_at: future() })
    const wrapper = mount(PelicanFrame, { props: { runId: 3, mode: 'preview', title: 't' } })
    await flushPromises()
    expect(wrapper.find('[data-testid="pelican-frame"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="pelican-frame-error"]').text()).toContain('重新加载')
    expect(safePelicanFrameURL('blob:https://x/1')).toBeNull()
    expect(safePelicanFrameURL('data:text/html,hi')).toBeNull()
    expect(safePelicanFrameURL('/api/v1/openai-eval/pelican-frame/x')).toBe('/api/v1/openai-eval/pelican-frame/x')
    wrapper.unmount()
  })
})
