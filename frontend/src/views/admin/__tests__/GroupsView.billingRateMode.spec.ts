import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import type { AdminGroup } from '@/types'
import GroupsView from '@/views/admin/GroupsView.vue'

const {
  listGroups,
  duplicateGroup,
  createGroup,
  updateGroup,
  getModelAllowlistCandidates,
  getUsageSummary,
  getCapacitySummary,
  getLiveCapability,
  showSuccess,
  showError
} = vi.hoisted(() => ({
  listGroups: vi.fn(),
  duplicateGroup: vi.fn(),
  createGroup: vi.fn(),
  updateGroup: vi.fn(),
  getModelAllowlistCandidates: vi.fn(),
  getUsageSummary: vi.fn(),
  getCapacitySummary: vi.fn(),
  getLiveCapability: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn()
}))

const authState = vi.hoisted(() => ({ isSimpleMode: false }))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    groups: {
      list: listGroups,
      duplicate: duplicateGroup,
      getModelAllowlistCandidates,
      getUsageSummary,
      getCapacitySummary,
      getLiveCapability,
      getAll: vi.fn(),
      create: createGroup,
      update: updateGroup,
      delete: vi.fn(),
      updateSortOrder: vi.fn()
    },
    accounts: {
      list: vi.fn(),
      getById: vi.fn()
    }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showSuccess, showError })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => authState
}))

vi.mock('@/stores/onboarding', () => ({
  useOnboardingStore: () => ({
    isCurrentStep: vi.fn(() => false),
    nextStep: vi.fn()
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

const sourceGroup: AdminGroup = {
  id: 42,
  name: 'Primary',
  description: null,
  platform: 'openai',
  rate_multiplier: 1,
  rpm_limit: 0,
  is_exclusive: false,
  status: 'active',
  subscription_type: 'standard',
  daily_limit_usd: null,
  weekly_limit_usd: null,
  monthly_limit_usd: null,
  allow_image_generation: false,
  allow_batch_image_generation: false,
  image_rate_independent: false,
  image_rate_multiplier: 1,
  batch_image_discount_multiplier: 0.5,
  batch_image_hold_multiplier: 0.6,
  image_price_1k: null,
  image_price_2k: null,
  image_price_4k: null,
  video_rate_independent: false,
  video_rate_multiplier: 1,
  video_price_480p: null,
  video_price_720p: null,
  video_price_1080p: null,
  web_search_price_per_call: null,
  peak_rate_enabled: false,
  peak_start: '',
  peak_end: '',
  peak_rate_multiplier: 1,
  claude_code_only: false,
  fallback_group_id: null,
  fallback_group_id_on_invalid_request: null,
  allow_messages_dispatch: false,
  default_mapped_model: '',
  messages_dispatch_model_config: undefined,
  require_oauth_only: false,
  require_privacy_set: false,
  created_at: '2026-07-16T00:00:00Z',
  updated_at: '2026-07-16T00:00:00Z',
  model_routing: null,
  model_routing_enabled: false,
  mcp_xml_inject: true,
  supported_model_scopes: [],
  account_count: 1,
  active_account_count: 1,
  rate_limited_account_count: 0,
  model_allowlist: undefined,
  sort_order: 10
}

const AppLayoutStub = defineComponent({
  template: '<main><slot /></main>'
})

const TablePageLayoutStub = defineComponent({
  template: '<section><slot name="filters" /><slot name="table" /><slot name="pagination" /></section>'
})

const DataTableStub = defineComponent({
  props: {
    data: { type: Array, default: () => [] },
    columns: { type: Array, default: () => [] },
    loading: { type: Boolean, default: false }
  },
  template: '<div><div v-for="row in data" :key="row.id"><slot name="cell-actions" :row="row" /></div></div>'
})

const BaseDialogStub = defineComponent({
  props: {
    show: { type: Boolean, default: false }
  },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>'
})

function mountView() {
  return mount(GroupsView, {
    global: {
      stubs: {
        AppLayout: AppLayoutStub,
        TablePageLayout: TablePageLayoutStub,
        DataTable: DataTableStub,
        Pagination: true,
        BaseDialog: BaseDialogStub,
        ConfirmDialog: true,
        EmptyState: true,
        Select: true,
        PlatformIcon: true,
        Icon: true,
        GroupCapacityBadge: true,
        GroupRateMultipliersModal: true,
        GroupRPMOverridesModal: true,
        VueDraggable: true
      }
    }
  })
}

describe('GroupsView billing rate mode', () => {
  beforeEach(() => {
    authState.isSimpleMode = false
    localStorage.clear()
    vi.spyOn(console, 'error').mockImplementation(() => {})
    for (const fn of [
      listGroups,
      duplicateGroup,
      createGroup,
      updateGroup,
      getModelAllowlistCandidates,
      getUsageSummary,
      getCapacitySummary,
      getLiveCapability,
      showSuccess,
      showError
    ]) {
      fn.mockReset()
    }
    listGroups.mockResolvedValue({ items: [sourceGroup], total: 1, page: 1, page_size: 20, pages: 1 })
    createGroup.mockResolvedValue({ ...sourceGroup, id: 43 })
    updateGroup.mockResolvedValue(sourceGroup)
    getModelAllowlistCandidates.mockResolvedValue([])
    getUsageSummary.mockResolvedValue([])
    getCapacitySummary.mockResolvedValue([])
    getLiveCapability.mockResolvedValue({ supported: false })
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  async function openCreate() {
    const wrapper = mountView()
    await flushPromises()
    await wrapper.findAll('button').find((button) => button.text() === 'admin.groups.createGroup')!.trigger('click')
    await flushPromises()
    return wrapper
  }

  async function openEdit(group: AdminGroup) {
    listGroups.mockResolvedValue({ items: [group], total: 1, page: 1, page_size: 20, pages: 1 })
    const wrapper = mountView()
    await flushPromises()
    await wrapper.findAll('button').find((button) => button.text() === 'common.edit')!.trigger('click')
    await flushPromises()
    return wrapper
  }

  function checked(wrapper: ReturnType<typeof mountView>, testId: string) {
    return wrapper.get<HTMLInputElement>(`[data-testid="${testId}"]`).element.checked
  }

  it('defaults new groups to the fixed group multiplier and submits it', async () => {
    const wrapper = await openCreate()

    expect(wrapper.get('[data-testid="create-group-billing-rate-mode"]').text())
      .toContain('admin.groups.billingRateMode.group')
    expect(wrapper.get('[data-testid="create-group-billing-rate-mode"]').text())
      .toContain('admin.groups.billingRateMode.account')
    expect(checked(wrapper, 'create-group-billing-rate-mode-group')).toBe(true)
    expect(checked(wrapper, 'create-group-billing-rate-mode-account')).toBe(false)
    expect(wrapper.text()).toContain('admin.groups.rateMultiplierHint')

    await wrapper.get('#create-group-form input[type="text"]').setValue('Fixed rate')
    await wrapper.get('#create-group-form').trigger('submit')
    await flushPromises()

    expect(createGroup).toHaveBeenCalledWith(expect.objectContaining({ billing_rate_mode: 'group' }))
    wrapper.unmount()
  })

  it('submits the actual account multiplier mode when selected on create', async () => {
    const wrapper = await openCreate()

    await wrapper.get('[data-testid="create-group-billing-rate-mode-account"]').setValue(true)
    expect(checked(wrapper, 'create-group-billing-rate-mode-account')).toBe(true)
    expect(wrapper.text()).toContain('admin.groups.billingRateMode.rateMultiplierAccountHint')

    await wrapper.get('#create-group-form input[type="text"]').setValue('Account rate')
    await wrapper.get('#create-group-form').trigger('submit')
    await flushPromises()

    expect(createGroup).toHaveBeenCalledWith(expect.objectContaining({ billing_rate_mode: 'account' }))
    wrapper.unmount()
  })

  it('backfills the saved account mode on edit and saves a switch back to group', async () => {
    const wrapper = await openEdit({ ...sourceGroup, billing_rate_mode: 'account' })

    expect(checked(wrapper, 'edit-group-billing-rate-mode-account')).toBe(true)
    expect(wrapper.text()).toContain('admin.groups.billingRateMode.rateMultiplierAccountHint')

    await wrapper.get('[data-testid="edit-group-billing-rate-mode-group"]').setValue(true)
    expect(wrapper.text()).not.toContain('admin.groups.billingRateMode.rateMultiplierAccountHint')
    await wrapper.get('#edit-group-form').trigger('submit')
    await flushPromises()

    expect(updateGroup).toHaveBeenCalledWith(42, expect.objectContaining({ billing_rate_mode: 'group' }))
    wrapper.unmount()
  })

  it('treats groups without a saved mode as the fixed group multiplier', async () => {
    const wrapper = await openEdit({ ...sourceGroup, billing_rate_mode: undefined })

    expect(checked(wrapper, 'edit-group-billing-rate-mode-group')).toBe(true)
    await wrapper.get('#edit-group-form').trigger('submit')
    await flushPromises()

    expect(updateGroup).toHaveBeenCalledWith(42, expect.objectContaining({ billing_rate_mode: 'group' }))
    wrapper.unmount()
  })

  it('does not reuse the previous edit draft when another group is opened', async () => {
    const accountGroup = { ...sourceGroup, billing_rate_mode: 'account' as const }
    const fixedGroup = { ...sourceGroup, id: 7, name: 'Fixed', billing_rate_mode: 'group' as const }
    listGroups.mockResolvedValue({ items: [accountGroup, fixedGroup], total: 2, page: 1, page_size: 20, pages: 1 })
    const wrapper = mountView()
    await flushPromises()
    const editButtons = wrapper.findAll('button').filter((button) => button.text() === 'common.edit')

    await editButtons[0].trigger('click')
    await flushPromises()
    expect(checked(wrapper, 'edit-group-billing-rate-mode-account')).toBe(true)

    await editButtons[1].trigger('click')
    await flushPromises()
    expect(checked(wrapper, 'edit-group-billing-rate-mode-group')).toBe(true)
    wrapper.unmount()
  })

  it('hides the billing rate mode control in simple mode', async () => {
    authState.isSimpleMode = true
    const wrapper = await openCreate()

    expect(wrapper.find('[data-testid="create-group-billing-rate-mode"]').exists()).toBe(false)
    wrapper.unmount()
  })
})
