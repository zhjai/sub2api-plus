import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import AccountsView from '../AccountsView.vue'
import { OPENAI_EXCEL_OAUTH_CLIENT_ID } from '@/utils/openaiOAuthClient'

const {
  listAccounts,
  listWithEtag,
  getUpstreamBillingRatesWithEtag,
  getBatchTodayStats,
  getUpstreamBillingProbeSettings,
  getAllProxies,
  getAllGroups,
  bulkUpdate,
  checkMixedChannelRisk
} = vi.hoisted(() => ({
  listAccounts: vi.fn(),
  listWithEtag: vi.fn(),
  getUpstreamBillingRatesWithEtag: vi.fn(),
  getBatchTodayStats: vi.fn(),
  getUpstreamBillingProbeSettings: vi.fn(),
  getAllProxies: vi.fn(),
  getAllGroups: vi.fn(),
  bulkUpdate: vi.fn(),
  checkMixedChannelRisk: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      list: listAccounts,
      listWithEtag,
      getUpstreamBillingRatesWithEtag,
      getBatchTodayStats,
      getUpstreamBillingProbeSettings,
      bulkUpdate,
      checkMixedChannelRisk,
      delete: vi.fn(),
      batchClearError: vi.fn(),
      batchRefresh: vi.fn(),
      probeUpstreamBilling: vi.fn(),
      probeUpstreamBillingBatch: vi.fn(),
      toggleSchedulable: vi.fn()
    },
    proxies: {
      getAll: getAllProxies
    },
    groups: {
      getAll: getAllGroups
    }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showInfo: vi.fn()
  })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    token: 'test-token'
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

const DataTableStub = {
  props: ['columns', 'data'],
  template: `
    <div>
      <div v-for="row in data" :key="row.id" :data-test="'row-' + row.id">
        <div data-test="select-cell"><slot name="cell-select" :row="row" /></div>
        <div data-test="platform-type-cell"><slot name="cell-platform_type" :row="row" /></div>
      </div>
    </div>
  `
}

const PlatformTypeBadgeStub = {
  props: ['platform', 'type', 'privacyMode'],
  template: `
    <span
      data-test="platform-type-badge"
      :data-platform="platform"
      :data-type="type"
      :data-privacy-mode="privacyMode || ''"
    >{{ platform }}/{{ type }}</span>
  `
}

const BulkEditAccountModalStub = {
  props: ['show', 'hasSelectedBps', 'subtypeMetadataComplete'],
  template: `
    <div
      data-test="bulk-edit-modal"
      :data-show="String(show)"
      :data-has-selected-bps="String(Boolean(hasSelectedBps))"
      :data-subtype-metadata-complete="String(Boolean(subtypeMetadataComplete))"
    />
  `
}

const makeNativeCodexAccount = (id: number) => ({
  id,
  name: `codex-${id}`,
  platform: 'openai' as const,
  type: 'oauth' as const,
  status: 'active' as const,
  schedulable: true,
  credentials: {},
  extra: {},
  created_at: '2026-11-12T00:00:00Z',
  updated_at: '2026-11-12T00:00:00Z'
})

const makeBpsAccount = (id: number) => ({
  id,
  name: `bps-${id}`,
  platform: 'openai' as const,
  type: 'oauth' as const,
  status: 'active' as const,
  schedulable: true,
  credentials: {
    client_id: OPENAI_EXCEL_OAUTH_CLIENT_ID
  },
  extra: {},
  created_at: '2026-11-12T00:00:00Z',
  updated_at: '2026-11-12T00:00:00Z'
})

const mountWithRealBulkModal = () =>
  mount(AccountsView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        TablePageLayout: {
          template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>'
        },
        DataTable: DataTableStub,
        Pagination: {
          emits: ['update:page'],
          template: '<button data-test="next-page" @click="$emit(\'update:page\', 2)">next</button>'
        },
        BaseDialog: {
          props: ['show'],
          template: '<div v-if="show" data-test="real-bulk-dialog"><slot /><slot name="footer" /></div>'
        },
        ConfirmDialog: true,
        Select: {
          props: ['modelValue', 'options'],
          emits: ['update:modelValue'],
          template: '<select v-bind="$attrs" :value="modelValue" @change="$emit(\'update:modelValue\', $event.target.value)"><option v-for="o in options" :key="o.value" :value="o.value">{{ o.label }}</option></select>'
        },
        ProxySelector: {
          props: ['modelValue'],
          emits: ['update:modelValue'],
          template: '<button type="button" data-test="pick-proxy" @click="$emit(\'update:modelValue\', 4)" />'
        },
        GroupSelector: {
          props: ['modelValue'],
          emits: ['update:modelValue'],
          template: '<button type="button" data-test="pick-groups" @click="$emit(\'update:modelValue\', [8])" />'
        },
        AccountTableActions: { template: '<div><slot name="beforeCreate" /><slot name="after" /></div>' },
        AccountTableFilters: true,
        AccountBulkActionsBar: {
          props: ['selectedIds'],
          emits: ['edit-selected', 'edit-filtered', 'select-all-results'],
          template: `
            <div>
              <button data-test="select-all-results" @click="$emit('select-all-results')">select all</button>
              <button data-test="edit-selected" @click="$emit('edit-selected')">edit selected</button>
              <button data-test="edit-filtered" @click="$emit('edit-filtered')">edit filtered</button>
            </div>
          `
        },
        AccountActionMenu: true,
        ImportDataModal: true,
        ReAuthAccountModal: true,
        AccountTestModal: true,
        AccountStatsModal: true,
        ScheduledTestsPanel: true,
        SyncFromCrsModal: true,
        TempUnschedStatusModal: true,
        ErrorPassthroughRulesModal: true,
        TLSFingerprintProfilesModal: true,
        CreateAccountModal: true,
        EditAccountModal: true,
        PlatformTypeBadge: PlatformTypeBadgeStub,
        AccountCapacityCell: true,
        AccountStatusIndicator: true,
        AccountTodayStatsCell: true,
        AccountGroupsCell: true,
        AccountUsageCell: true,
        Icon: true
      }
    }
  })

describe('AccountsView BPS row identity and bulk selection guards', () => {
  beforeEach(() => {
    localStorage.clear()
    listAccounts.mockReset()
    listWithEtag.mockReset().mockResolvedValue({ notModified: true, etag: null, data: null })
    getUpstreamBillingRatesWithEtag.mockReset().mockResolvedValue({ notModified: true, etag: null, data: null })
    getBatchTodayStats.mockReset().mockResolvedValue({ stats: {} })
    getUpstreamBillingProbeSettings.mockReset().mockResolvedValue({ enabled: false, interval_minutes: 30 })
    getAllProxies.mockReset().mockResolvedValue([])
    getAllGroups.mockReset().mockResolvedValue([])
    bulkUpdate.mockReset().mockResolvedValue({ success: 2, failed: 0, results: [] })
    checkMixedChannelRisk.mockReset().mockResolvedValue({ has_risk: false })
  })

  it('shows OpenAIBPSBadge alongside OpenAI OAuth identity, hides Compact/privacy on BPS rows, and passes hasSelectedBps to bulk edit', async () => {
    listAccounts.mockResolvedValue({
      items: [
        {
          id: 71,
          name: 'bps-01',
          platform: 'openai',
          type: 'oauth',
          status: 'active',
          schedulable: true,
          credentials: {
            client_id: OPENAI_EXCEL_OAUTH_CLIENT_ID
          },
          extra: {
            openai_compact_mode: 'force_on',
            privacy_mode: 'training_off'
          },
          created_at: '2026-11-12T00:00:00Z',
          updated_at: '2026-11-12T00:00:00Z'
        },
        {
          id: 72,
          name: 'codex-01',
          platform: 'openai',
          type: 'oauth',
          status: 'active',
          schedulable: true,
          credentials: {},
          extra: {
            openai_compact_mode: 'force_on',
            privacy_mode: 'training_off'
          },
          created_at: '2026-11-12T00:00:00Z',
          updated_at: '2026-11-12T00:00:00Z'
        }
      ],
      total: 2,
      page: 1,
      page_size: 20,
      pages: 1
    })

    const wrapper = mount(AccountsView, {
      global: {
        stubs: {
          AppLayout: { template: '<div><slot /></div>' },
          TablePageLayout: {
            template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>'
          },
          DataTable: DataTableStub,
          Pagination: true,
          ConfirmDialog: true,
          AccountTableActions: { template: '<div><slot name="beforeCreate" /><slot name="after" /></div>' },
          AccountTableFilters: true,
          AccountBulkActionsBar: {
            props: ['selectedIds'],
            emits: ['edit-selected'],
            template: '<button data-test="edit-selected" @click="$emit(\'edit-selected\')">bulk edit</button>'
          },
          AccountActionMenu: true,
          ImportDataModal: true,
          ReAuthAccountModal: true,
          AccountTestModal: true,
          AccountStatsModal: true,
          ScheduledTestsPanel: true,
          SyncFromCrsModal: true,
          TempUnschedStatusModal: true,
          ErrorPassthroughRulesModal: true,
          TLSFingerprintProfilesModal: true,
          CreateAccountModal: true,
          EditAccountModal: true,
          BulkEditAccountModal: BulkEditAccountModalStub,
          PlatformTypeBadge: PlatformTypeBadgeStub,
          AccountCapacityCell: true,
          AccountStatusIndicator: true,
          AccountTodayStatsCell: true,
          AccountGroupsCell: true,
          AccountUsageCell: true,
          Icon: true
        }
      }
    })

    await flushPromises()

    const bpsRow = wrapper.get('[data-test="row-71"]')
    const bpsPlatformBadge = bpsRow.get('[data-test="platform-type-badge"]')
    expect(bpsPlatformBadge.attributes('data-platform')).toBe('openai')
    expect(bpsPlatformBadge.attributes('data-type')).toBe('oauth')
    expect(bpsPlatformBadge.attributes('data-privacy-mode')).toBe('')
    expect(bpsRow.find('[data-testid="account-bps-badge"]').exists()).toBe(true)
    expect(bpsRow.text()).not.toContain('admin.accounts.openai.compactSupported')

    const codexRow = wrapper.get('[data-test="row-72"]')
    const codexPlatformBadge = codexRow.get('[data-test="platform-type-badge"]')
    expect(codexPlatformBadge.attributes('data-platform')).toBe('openai')
    expect(codexPlatformBadge.attributes('data-type')).toBe('oauth')
    expect(codexPlatformBadge.attributes('data-privacy-mode')).toBe('training_off')
    expect(codexRow.find('[data-testid="account-bps-badge"]').exists()).toBe(false)
    expect(codexRow.text()).toContain('admin.accounts.openai.compactSupported')

    // Select BPS row and open bulk edit -> hasSelectedBps is true
    await bpsRow.get('[data-test="select-cell"] input[type="checkbox"]').setValue(true)
    await wrapper.get('[data-test="edit-selected"]').trigger('click')
    expect(wrapper.get('[data-test="bulk-edit-modal"]').attributes('data-show')).toBe('true')
    expect(wrapper.get('[data-test="bulk-edit-modal"]').attributes('data-has-selected-bps')).toBe('true')
    expect(wrapper.get('[data-test="bulk-edit-modal"]').attributes('data-subtype-metadata-complete')).toBe('true')
  })

  it('hides native Codex controls while keeping ordinary bulk edits available when selected IDs include an unloaded later-page BPS account', async () => {
    listAccounts.mockImplementation(async (page: number, pageSize: number) => {
      if (pageSize === 1000) {
        return {
          items: [makeNativeCodexAccount(1), makeBpsAccount(2)],
          total: 2,
          page: 1,
          page_size: 1000,
          pages: 1
        }
      }
      return {
        items: page === 1 ? [makeNativeCodexAccount(1)] : [makeBpsAccount(2)],
        total: 2,
        page,
        page_size: 1,
        pages: 2
      }
    })

    const wrapper = mountWithRealBulkModal()
    await flushPromises()

    // Select all matching results across pages without visiting page 2
    await wrapper.get('[data-test="select-all-results"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="edit-selected"]').trigger('click')
    await flushPromises()

    const dialog = wrapper.get('[data-test="real-bulk-dialog"]')
    expect(dialog.find('[data-testid="bulk-edit-incomplete-subtype-hint"]').exists()).toBe(true)
    expect(dialog.find('[data-testid="bulk-edit-bps-native-hidden-hint"]').exists()).toBe(false)
    expect(dialog.find('#bulk-edit-openai-codex-cli-only-enabled').exists()).toBe(false)
    expect(dialog.find('#bulk-edit-openai-ws-mode-enabled').exists()).toBe(false)
    expect(dialog.find('#bulk-edit-openai-passthrough-enabled').exists()).toBe(false)
    expect(dialog.find('#bulk-edit-openai-flatten-namespaces-enabled').exists()).toBe(false)
    expect(dialog.find('#bulk-edit-openai-codex-fingerprint-mode-enabled').exists()).toBe(false)
    expect(dialog.find('#bulk-edit-openai-compact-mode-enabled').exists()).toBe(false)

    // Ordinary mapping, priority, group, and proxy bulk edits remain available
    await dialog.get('#bulk-edit-model-restriction-enabled').setValue(true)
    const mappingBtn = dialog
      .findAll('button')
      .find((btn) => btn.text().includes('admin.accounts.modelMapping'))!
    await mappingBtn.trigger('click')
    const addMappingBtn = dialog
      .findAll('button')
      .find((btn) => btn.text().includes('admin.accounts.addMapping'))!
    await addMappingBtn.trigger('click')
    const mappingInputs = dialog.findAll('#bulk-edit-model-restriction-body input.input')
    await mappingInputs[0].setValue('gpt-6-astra')
    await mappingInputs[1].setValue('gpt-6-astra')

    await dialog.get('#bulk-edit-priority-enabled').setValue(true)
    await dialog.get('#bulk-edit-priority').setValue('5')
    await dialog.get('#bulk-edit-proxy-enabled').setValue(true)
    await dialog.get('[data-test="pick-proxy"]').trigger('click')
    await dialog.get('#bulk-edit-groups-enabled').setValue(true)
    await dialog.get('[data-test="pick-groups"]').trigger('click')

    await dialog.get('form#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(bulkUpdate).toHaveBeenCalledTimes(1)
    expect(bulkUpdate).toHaveBeenCalledWith([1, 2], {
      credentials: {
        model_mapping: {
          'gpt-6-astra': 'gpt-6-astra'
        }
      },
      priority: 5,
      proxy_id: 4,
      group_ids: [8]
    })
    expect(bulkUpdate.mock.calls[0]?.[1]?.extra).toBeUndefined()
  })

  it('hides native Codex controls in filtered bulk edit when total is 101 and only 100 native preview rows are returned', async () => {
    const previewHundredNative = Array.from({ length: 100 }, (_, index) => makeNativeCodexAccount(index + 1))
    listAccounts.mockImplementation(async (_page: number, pageSize: number) => {
      if (pageSize === 100) {
        return {
          items: previewHundredNative,
          total: 101,
          page: 1,
          page_size: 100,
          pages: 2
        }
      }
      return {
        items: previewHundredNative.slice(0, 20),
        total: 101,
        page: 1,
        page_size: 20,
        pages: 6
      }
    })

    const wrapper = mountWithRealBulkModal()
    await flushPromises()

    await wrapper.get('[data-test="edit-filtered"]').trigger('click')
    await flushPromises()

    const dialog = wrapper.get('[data-test="real-bulk-dialog"]')
    expect(dialog.find('[data-testid="bulk-edit-incomplete-subtype-hint"]').exists()).toBe(true)
    expect(dialog.find('[data-testid="bulk-edit-bps-native-hidden-hint"]').exists()).toBe(false)
    expect(dialog.find('#bulk-edit-openai-codex-cli-only-enabled').exists()).toBe(false)
    expect(dialog.find('#bulk-edit-openai-ws-mode-enabled').exists()).toBe(false)
    expect(dialog.find('#bulk-edit-openai-passthrough-enabled').exists()).toBe(false)
    expect(dialog.find('#bulk-edit-openai-flatten-namespaces-enabled').exists()).toBe(false)
    expect(dialog.find('#bulk-edit-openai-codex-fingerprint-mode-enabled').exists()).toBe(false)
    expect(dialog.find('#bulk-edit-openai-compact-mode-enabled').exists()).toBe(false)

    await dialog.get('#bulk-edit-priority-enabled').setValue(true)
    await dialog.get('#bulk-edit-priority').setValue('3')
    await dialog.get('form#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(bulkUpdate).toHaveBeenCalledTimes(1)
    expect(bulkUpdate).toHaveBeenCalledWith(
      expect.objectContaining({
        filters: expect.any(Object),
        priority: 3
      })
    )
    expect(bulkUpdate.mock.calls[0]?.[0]?.extra).toBeUndefined()
  })

  it('retains native Codex controls when all selected accounts have complete native metadata', async () => {
    listAccounts.mockImplementation(async (page: number, pageSize: number) => {
      if (pageSize === 1000) {
        return {
          items: [makeNativeCodexAccount(1), makeNativeCodexAccount(2)],
          total: 2,
          page: 1,
          page_size: 1000,
          pages: 1
        }
      }
      return {
        items: page === 1 ? [makeNativeCodexAccount(1)] : [makeNativeCodexAccount(2)],
        total: 2,
        page,
        page_size: 1,
        pages: 2
      }
    })

    const wrapper = mountWithRealBulkModal()
    await flushPromises()

    // Select page 1 native account, navigate to page 2 so page 2 metadata is also known, and select page 2 native account
    await wrapper.get('[data-test="row-1"] [data-test="select-cell"] input[type="checkbox"]').setValue(true)
    await wrapper.get('[data-test="next-page"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="row-2"] [data-test="select-cell"] input[type="checkbox"]').setValue(true)

    await wrapper.get('[data-test="edit-selected"]').trigger('click')
    await flushPromises()

    const dialog = wrapper.get('[data-test="real-bulk-dialog"]')
    expect(dialog.find('[data-testid="bulk-edit-incomplete-subtype-hint"]').exists()).toBe(false)
    expect(dialog.find('[data-testid="bulk-edit-bps-native-hidden-hint"]').exists()).toBe(false)
    expect(dialog.find('#bulk-edit-openai-codex-cli-only-enabled').exists()).toBe(true)
    expect(dialog.find('#bulk-edit-openai-ws-mode-enabled').exists()).toBe(true)
    expect(dialog.find('#bulk-edit-openai-passthrough-enabled').exists()).toBe(true)
    expect(dialog.find('#bulk-edit-openai-flatten-namespaces-enabled').exists()).toBe(true)
    expect(dialog.find('#bulk-edit-openai-codex-fingerprint-mode-enabled').exists()).toBe(true)
    expect(dialog.find('#bulk-edit-openai-compact-mode-enabled').exists()).toBe(true)

    await dialog.get('#bulk-edit-openai-codex-cli-only-enabled').setValue(true)
    await dialog.get('#bulk-edit-openai-codex-cli-only-toggle').trigger('click')
    await dialog.get('form#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(bulkUpdate).toHaveBeenCalledWith([1, 2], {
      extra: {
        codex_cli_only: true
      }
    })
  })

  it('keeps known BPS selection protected across pages with the definite BPS notice', async () => {
    listAccounts
      .mockResolvedValueOnce({
        items: [makeBpsAccount(1)],
        total: 2,
        page: 1,
        page_size: 1,
        pages: 2
      })
      .mockResolvedValueOnce({
        items: [makeNativeCodexAccount(2)],
        total: 2,
        page: 2,
        page_size: 1,
        pages: 2
      })

    const wrapper = mountWithRealBulkModal()
    await flushPromises()

    // Select known BPS on page 1, navigate to page 2 (only native row visible on current page), select native row
    await wrapper.get('[data-test="row-1"] [data-test="select-cell"] input[type="checkbox"]').setValue(true)
    await wrapper.get('[data-test="next-page"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="row-2"] [data-test="select-cell"] input[type="checkbox"]').setValue(true)

    await wrapper.get('[data-test="edit-selected"]').trigger('click')
    await flushPromises()

    const dialog = wrapper.get('[data-test="real-bulk-dialog"]')
    expect(dialog.find('[data-testid="bulk-edit-bps-native-hidden-hint"]').exists()).toBe(true)
    expect(dialog.find('[data-testid="bulk-edit-incomplete-subtype-hint"]').exists()).toBe(false)
    // Local CLI-only applies to both native Codex and BPS OAuth when subtype metadata is complete
    expect(dialog.find('#bulk-edit-openai-codex-cli-only-enabled').exists()).toBe(true)
    expect(dialog.find('#bulk-edit-openai-ws-mode-enabled').exists()).toBe(false)
    expect(dialog.find('#bulk-edit-openai-passthrough-enabled').exists()).toBe(false)
  })
})
