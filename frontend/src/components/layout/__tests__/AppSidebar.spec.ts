import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import zh from '@/i18n/locales/zh'
import en from '@/i18n/locales/en'
import AppSidebar from '../AppSidebar.vue'

vi.mock('vue-router', () => ({
  useRoute: () => ({ path: '/admin/model-integrity/tests' }),
  useRouter: () => ({ push: vi.fn() })
}))

vi.mock('vue-i18n', async (importOriginal) => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: (key: string) => key })
}))

const mockAppStore = vi.hoisted(() => ({
  sidebarCollapsed: false,
  mobileOpen: false,
  sidebarScrollTop: 0,
  siteName: 'Sub2API',
  siteLogo: '',
  siteVersion: '',
  publicSettingsLoaded: true,
  cachedPublicSettings: null,
  backendModeEnabled: false,
  setMobileOpen: vi.fn(),
  toggleSidebar: vi.fn()
}))

vi.mock('@/stores/app', () => ({ useAppStore: () => mockAppStore }))

vi.mock('@/stores', () => ({
  useAppStore: () => mockAppStore,
  useAuthStore: () => ({ isAdmin: true, isSimpleMode: false }),
  useOnboardingStore: () => ({ isCurrentStep: () => false, nextStep: vi.fn() }),
  useAdminSettingsStore: () => ({
    customMenuItems: [],
    opsMonitoringEnabled: true,
    paymentEnabled: true,
    fetch: vi.fn()
  })
}))

vi.mock('@/composables/useBatchImageAccess', async () => {
  const { computed } = await import('vue')
  return {
    useBatchImageAccess: () => ({ canUseBatchImage: computed(() => false), refreshBatchImageAccess: vi.fn() })
  }
})

const componentPath = resolve(dirname(fileURLToPath(import.meta.url)), '../AppSidebar.vue')
const componentSource = readFileSync(componentPath, 'utf8')
const stylePath = resolve(dirname(fileURLToPath(import.meta.url)), '../../../style.css')
const styleSource = readFileSync(stylePath, 'utf8')

describe('AppSidebar custom SVG styles', () => {
  it('does not override uploaded SVG fill or stroke colors', () => {
    expect(componentSource).toContain('.sidebar-svg-icon {')
    expect(componentSource).toContain('color: currentColor;')
    expect(componentSource).toContain('display: block;')
    expect(componentSource).not.toContain('stroke: currentColor;')
    expect(componentSource).not.toContain('fill: none;')
  })
})

describe('AppSidebar scroll position persistence', () => {
  it('binds a template ref to the sidebar nav element', () => {
    expect(componentSource).toContain('ref="sidebarNavRef"')
    expect(componentSource).toContain('sidebar-nav')
  })

  it('declares sidebarNavRef in script setup', () => {
    expect(componentSource).toContain("const sidebarNavRef = ref<HTMLElement | null>(null)")
  })

  it('saves scroll position on beforeUnmount', () => {
    expect(componentSource).toContain('onBeforeUnmount')
    expect(componentSource).toContain('appStore.sidebarScrollTop')
    expect(componentSource).toContain('sidebarNavRef.value.scrollTop')
  })

  it('restores scroll position on mount', () => {
    expect(componentSource).toContain('onMounted')
    expect(componentSource).toContain('appStore.sidebarScrollTop')
    expect(componentSource).toContain('nextTick')
  })
})

describe('AppSidebar collapsible groups', () => {
  it('lets the user collapse a group even while a child route is active', () => {
    // The expand state must come from the user's override first, falling back
    // to the active-route heuristic only when the user has not clicked yet.
    expect(componentSource).toContain('const groupExpandOverrides = ref<Map<string, boolean>>(new Map())')
    expect(componentSource).not.toContain('expandedGroups.value.has(item.path) || isGroupActive(item)')
  })
})

describe('AppSidebar account navigation', () => {
  it('keeps Account Management as a standalone top-level item', () => {
    expect(componentSource).toMatch(/\{ path: '\/admin\/accounts', label: t\('nav\.accounts'\), icon: GlobeIcon \}/)
    expect(componentSource).not.toMatch(/path: '\/admin\/accounts',[\s\S]{0,180}children:/)
  })
})

describe('AppSidebar header styles', () => {
  it('does not clip the version badge dropdown', () => {
    const sidebarHeaderBlockMatch = styleSource.match(/\.sidebar-header\s*\{[\s\S]*?\n {2}\}/)
    const sidebarBrandBlockMatch = componentSource.match(/\.sidebar-brand\s*\{[\s\S]*?\n\}/)

    expect(sidebarHeaderBlockMatch).not.toBeNull()
    expect(sidebarBrandBlockMatch).not.toBeNull()
    expect(sidebarHeaderBlockMatch?.[0]).not.toContain('@apply overflow-hidden;')
    expect(sidebarBrandBlockMatch?.[0]).not.toContain('overflow: hidden;')
  })
})

describe('AppSidebar subscription feature flag', () => {
  it('gates the My Subscriptions entry behind the subscription public-settings flag', () => {
    expect(componentSource).toContain('const flagSubscription = makeSidebarFlag(FeatureFlags.subscription)')
    expect(componentSource).toMatch(/path: '\/subscriptions'[^\n]*featureFlag: flagSubscription/)
  })

  it('also hides the admin Subscription Management entry on recharge-only sites', () => {
    expect(componentSource).toMatch(/path: '\/admin\/subscriptions'[^\n]*featureFlag: flagSubscription/)
  })

  it('derives the purchase entry label from the site billing mode', () => {
    expect(componentSource).toContain("import { resolveSiteBillingMode } from '@/utils/siteBillingMode'")
    expect(componentSource).toMatch(/case 'recharge_only':\s*return t\('nav\.recharge'\)/)
    expect(componentSource).toMatch(/case 'subscription_only':\s*return t\('nav\.subscribe'\)/)
    expect(componentSource).toMatch(/path: '\/purchase'[^\n]*label: purchaseNavLabel\.value/)
  })
})

describe('AppSidebar model integrity navigation', () => {
  it('shows 降智调度 as its own top-level group, next to Account Management', () => {
    const accountsIndex = componentSource.indexOf("{ path: '/admin/accounts', label: t('nav.accounts'), icon: GlobeIcon }")
    const groupIndex = componentSource.indexOf("path: '/admin/model-integrity',")
    expect(accountsIndex).toBeGreaterThan(-1)
    expect(groupIndex).toBeGreaterThan(accountsIndex)
    expect(componentSource).toMatch(/path: '\/admin\/model-integrity',\s*label: t\('nav\.modelIntegrity'\)[\s\S]{0,80}expandOnly: true/)
  })

  it('has exactly the two pages 降智测试 and 调度策略 as children', () => {
    const block = componentSource.match(/path: '\/admin\/model-integrity',[\s\S]*?children: \[([\s\S]*?)\],/)
    expect(block).not.toBeNull()
    const children = block?.[1].match(/path: '[^']+'/g) ?? []
    expect(children).toEqual(["path: '/admin/model-integrity/tests'", "path: '/admin/model-integrity/scheduling'"])
    expect(block?.[1]).toContain("label: t('nav.modelIntegrityTests')")
    expect(block?.[1]).toContain("label: t('nav.modelIntegrityScheduling')")
  })

  it('names the group and its two children distinctly in both locales', () => {
    expect([zh.nav.modelIntegrity, zh.nav.modelIntegrityTests, zh.nav.modelIntegrityScheduling]).toEqual(['降智调度', '降智测试', '调度策略'])
    expect(zh.admin.modelIntegrity.tests.title).toBe(zh.nav.modelIntegrityTests)
    expect(zh.admin.modelIntegrity.scheduling.title).toBe(zh.nav.modelIntegrityScheduling)
    expect([en.nav.modelIntegrity, en.nav.modelIntegrityTests, en.nav.modelIntegrityScheduling]).toEqual(['Model integrity', 'Integrity tests', 'Scheduling policy'])
    expect(en.admin.modelIntegrity.scheduling.title).toBe(en.nav.modelIntegrityScheduling)
  })

  it('no longer links the legacy evaluation page directly', () => {
    expect(componentSource).not.toContain("path: '/admin/evaluations'")
  })
})

describe('AppSidebar model integrity icons (rendered)', () => {
  function mountSidebar() {
    return mount(AppSidebar, {
      global: {
        stubs: {
          VersionBadge: true,
          RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' }
        }
      }
    })
  }

  function iconFor(wrapper: ReturnType<typeof mountSidebar>, selector: string) {
    const row = wrapper.find(selector)
    expect(row.exists()).toBe(true)
    const svg = row.find('svg')
    expect(svg.exists()).toBe(true)
    return svg
  }

  function sizeClasses(svg: ReturnType<ReturnType<typeof mountSidebar>['find']>) {
    return svg.classes().filter(c => /^[hw]-\d/.test(c)).sort()
  }

  function paths(svg: ReturnType<ReturnType<typeof mountSidebar>['find']>) {
    return svg.findAll('path').map(p => p.attributes('d')).join(' ')
  }

  it('renders both child icons in the same 16px box with no leftover 20px class', () => {
    const wrapper = mountSidebar()
    const tests = iconFor(wrapper, 'a[href="/admin/model-integrity/tests"]')
    const scheduling = iconFor(wrapper, 'a[href="/admin/model-integrity/scheduling"]')

    expect(sizeClasses(tests)).toEqual(['h-4', 'w-4'])
    expect(sizeClasses(scheduling)).toEqual(['h-4', 'w-4'])
    expect(tests.attributes('viewBox')).toBe(scheduling.attributes('viewBox'))
    expect(tests.attributes('stroke-width')).toBe(scheduling.attributes('stroke-width'))

    const testsRow = wrapper.find('a[href="/admin/model-integrity/tests"]')
    const schedulingRow = wrapper.find('a[href="/admin/model-integrity/scheduling"]')
    const rowClasses = (row: typeof testsRow) => row.classes().filter(c => c !== 'sidebar-link-active').sort()
    expect(rowClasses(testsRow)).toEqual(rowClasses(schedulingRow))
  })

  it('keeps the group icon at 20px and gives tests a different glyph than the group', () => {
    const wrapper = mountSidebar()
    const groupButton = wrapper.findAll('button.sidebar-link').find(b => b.text().includes('nav.modelIntegrity'))
    expect(groupButton).toBeDefined()
    const groupIcon = groupButton!.find('svg')
    expect(sizeClasses(groupIcon)).toEqual(['h-5', 'w-5'])

    const tests = iconFor(wrapper, 'a[href="/admin/model-integrity/tests"]')
    const scheduling = iconFor(wrapper, 'a[href="/admin/model-integrity/scheduling"]')
    const groupD = paths(groupIcon)
    expect(paths(tests)).not.toBe(groupD)
    expect(paths(scheduling)).not.toBe(groupD)
    expect(paths(tests)).not.toBe(paths(scheduling))
  })
})
