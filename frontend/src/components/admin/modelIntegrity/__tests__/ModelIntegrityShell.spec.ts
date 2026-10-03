import { mount, RouterLinkStub } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { zhT } from '@/views/admin/modelIntegrity/__tests__/zhT'

vi.mock('vue-i18n', async importOriginal => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: zhT })
}))

import ModelIntegrityShell from '../ModelIntegrityShell.vue'

describe('ModelIntegrityShell', () => {
  it('renders the page heading without a second set of page-switch tabs', () => {
    const wrapper = mount(ModelIntegrityShell, {
      props: { title: zhT('admin.modelIntegrity.scheduling.title'), description: 'd' },
      global: { stubs: { RouterLink: RouterLinkStub } }
    })

    expect(wrapper.get('h1').text()).toBe('调度策略')
    // The sidebar is the only navigation between 降智测试 and 调度策略.
    expect(wrapper.find('nav').exists()).toBe(false)
    expect(wrapper.findAllComponents(RouterLinkStub)).toHaveLength(0)
    expect(wrapper.text()).not.toContain('降智测试')
  })
})
