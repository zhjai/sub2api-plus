import { describe, expect, it, vi } from 'vitest'

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ checkAuth: vi.fn(), isAuthenticated: false, isAdmin: false, isSimpleMode: false }),
}))
vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ siteName: 'Sub2API', backendModeEnabled: false, cachedPublicSettings: null }),
}))
vi.mock('@/stores/adminSettings', () => ({
  useAdminSettingsStore: () => ({ customMenuItems: [] }),
}))
vi.mock('@/composables/useNavigationLoading', () => ({
  useNavigationLoadingState: () => ({ startNavigation: vi.fn(), endNavigation: vi.fn(), isLoading: { value: false } }),
}))
vi.mock('@/composables/useRoutePrefetch', () => ({
  useRoutePrefetch: () => ({ triggerPrefetch: vi.fn(), cancelPendingPrefetch: vi.fn(), resetPrefetchState: vi.fn() }),
}))

describe('model integrity routes', () => {
  it('registers the two admin pages', async () => {
    const { default: router } = await import('@/router')
    const tests = router.getRoutes().find(record => record.name === 'AdminModelIntegrityTests')
    const scheduling = router.getRoutes().find(record => record.name === 'AdminModelIntegrityScheduling')
    expect(tests?.path).toBe('/admin/model-integrity/tests')
    expect(scheduling?.path).toBe('/admin/model-integrity/scheduling')
    for (const record of [tests, scheduling]) {
      expect(record?.meta.requiresAuth).toBe(true)
      expect(record?.meta.requiresAdmin).toBe(true)
    }
    expect(tests?.meta.titleKey).toBe('admin.modelIntegrity.tests.title')
    expect(scheduling?.meta.titleKey).toBe('admin.modelIntegrity.scheduling.title')
  })

  it.each([
    ['/admin/evaluations'],
    ['/admin/accounts/evaluations'],
    ['/admin/model-integrity'],
  ])('redirects the old or bare entry %s to the tests page', async (path) => {
    const { default: router } = await import('@/router')
    const record = router.getRoutes().find(item => item.path === path)
    expect(record?.redirect).toBe('/admin/model-integrity/tests')
  })

  it('keeps the legacy route name resolvable for old router.push calls', async () => {
    const { default: router } = await import('@/router')
    expect(router.hasRoute('AdminOpenAIEvaluations')).toBe(true)
  })
})
