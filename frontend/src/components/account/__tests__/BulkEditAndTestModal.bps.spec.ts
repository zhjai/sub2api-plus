import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import BulkEditAccountModal from '../BulkEditAccountModal.vue'
import AccountTestModal from '../AccountTestModal.vue'
import AdminAccountTestModal from '@/components/admin/account/AccountTestModal.vue'
import { adminAPI } from '@/api/admin'
import { OPENAI_EXCEL_OAUTH_CLIENT_ID } from '@/utils/openaiOAuthClient'

const { bulkUpdateMock, getAvailableModelsMock, showErrorMock, showSuccessMock } = vi.hoisted(() => ({
  bulkUpdateMock: vi.fn(),
  getAvailableModelsMock: vi.fn(),
  showErrorMock: vi.fn(),
  showSuccessMock: vi.fn()
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: showErrorMock,
    showSuccess: showSuccessMock,
    showInfo: vi.fn()
  })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      bulkUpdate: bulkUpdateMock,
      checkMixedChannelRisk: vi.fn().mockResolvedValue({ has_risk: false }),
      getAvailableModels: getAvailableModelsMock
    }
  }
}))

vi.mock('@/api/admin/accounts', () => ({
  getAntigravityDefaultModelMapping: vi.fn().mockResolvedValue([])
}))

vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({
    copyToClipboard: vi.fn()
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

describe('BulkEditAccountModal and AccountTestModal BPS protections', () => {
  beforeEach(() => {
    bulkUpdateMock.mockReset().mockResolvedValue({ success: 2, failed: 0, results: [] })
    getAvailableModelsMock.mockReset()
    showErrorMock.mockReset()
    showSuccessMock.mockReset()
  })

  it('hides native Codex batch controls when hasSelectedBps is true while keeping model_mapping, priority, group, and proxy editable', async () => {
    const wrapper = mount(BulkEditAccountModal, {
      props: {
        show: true,
        accountIds: [101, 102],
        selectedPlatforms: ['openai'],
        selectedTypes: ['oauth'],
        hasSelectedBps: true,
        proxies: [],
        groups: []
      },
      global: {
        stubs: {
          BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' },
          ConfirmDialog: true,
          Select: {
            props: ['modelValue', 'options'],
            emits: ['update:modelValue'],
            template: '<select :value="modelValue" @change="$emit(\'update:modelValue\', $event.target.value)"><option v-for="o in options" :key="o.value" :value="o.value">{{ o.label }}</option></select>'
          },
          ProxySelector: true,
          GroupSelector: true,
          Icon: true
        }
      }
    })

    expect(wrapper.find('[data-testid="bulk-edit-bps-native-hidden-hint"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="bulk-edit-openai-flatten-namespaces-enable"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="bulk-edit-openai-ws-mode-enable"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="bulk-edit-codex-fingerprint-mode-enable"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="bulk-edit-openai-passthrough-enable"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('admin.accounts.supportsAllModels')

    // Priority + proxy + group remain editable for BPS selections without emitting native extra keys
    expect(wrapper.find('#bulk-edit-proxy-enabled').exists()).toBe(true)
    await wrapper.get('#bulk-edit-priority-enabled').setValue(true)
    await wrapper.get('#bulk-edit-priority').setValue('5')

    await wrapper.get('form#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(bulkUpdateMock).toHaveBeenCalledTimes(1)
    expect(vi.mocked(adminAPI.accounts.bulkUpdate)).toHaveBeenCalledWith(
      [101, 102],
      expect.objectContaining({
        priority: 5
      })
    )
    expect(vi.mocked(adminAPI.accounts.bulkUpdate).mock.calls[0]?.[1]?.extra).toBeUndefined()
  })

  it('conservatively hides native Codex controls when subtypeMetadataComplete is false without claiming targets are known BPS', async () => {
    const wrapper = mount(BulkEditAccountModal, {
      props: {
        show: true,
        accountIds: [],
        selectedPlatforms: [],
        selectedTypes: [],
        target: {
          mode: 'filtered',
          filters: { platform: 'openai', type: 'oauth' },
          previewCount: 101,
          selectedPlatforms: ['openai'],
          selectedTypes: ['oauth'],
          hasSelectedBps: false,
          subtypeMetadataComplete: false
        },
        proxies: [],
        groups: []
      },
      global: {
        stubs: {
          BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' },
          ConfirmDialog: true,
          Select: {
            props: ['modelValue', 'options'],
            emits: ['update:modelValue'],
            template: '<select :value="modelValue" @change="$emit(\'update:modelValue\', $event.target.value)"><option v-for="o in options" :key="o.value" :value="o.value">{{ o.label }}</option></select>'
          },
          ProxySelector: true,
          GroupSelector: true,
          Icon: true
        }
      }
    })

    expect(wrapper.find('[data-testid="bulk-edit-incomplete-subtype-hint"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="bulk-edit-bps-native-hidden-hint"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="bulk-edit-bps-explicit-models-hint"]').exists()).toBe(false)
    expect(wrapper.find('#bulk-edit-openai-codex-cli-only-enabled').exists()).toBe(false)
    expect(wrapper.find('#bulk-edit-openai-codex-app-server-enabled').exists()).toBe(false)
    expect(wrapper.find('#bulk-edit-openai-ws-mode-enabled').exists()).toBe(false)
    expect(wrapper.find('#bulk-edit-openai-codex-fingerprint-mode-enabled').exists()).toBe(false)
    expect(wrapper.find('#bulk-edit-openai-passthrough-enabled').exists()).toBe(false)
    expect(wrapper.find('#bulk-edit-openai-flatten-namespaces-enabled').exists()).toBe(false)
    expect(wrapper.find('#bulk-edit-openai-compact-mode-enabled').exists()).toBe(false)
    expect(wrapper.find('#bulk-edit-openai-compact-model-mapping-enabled').exists()).toBe(false)
  })

  it('never leaks previously toggled native extras or compact mappings when target becomes incomplete or BPS', async () => {
    const wrapper = mount(BulkEditAccountModal, {
      props: {
        show: true,
        accountIds: [1, 2],
        selectedPlatforms: ['openai'],
        selectedTypes: ['oauth'],
        hasSelectedBps: false,
        subtypeMetadataComplete: true,
        proxies: [],
        groups: []
      },
      global: {
        stubs: {
          BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' },
          ConfirmDialog: true,
          Select: {
            props: ['modelValue', 'options'],
            emits: ['update:modelValue'],
            template: '<select v-bind="$attrs" :value="modelValue" @change="$emit(\'update:modelValue\', $event.target.value)"><option v-for="o in options" :key="o.value" :value="o.value">{{ o.label }}</option></select>'
          },
          ProxySelector: {
            props: ['modelValue'],
            emits: ['update:modelValue'],
            template: '<button type="button" data-testid="pick-proxy" @click="$emit(\'update:modelValue\', 9)" />'
          },
          GroupSelector: {
            props: ['modelValue'],
            emits: ['update:modelValue'],
            template: '<button type="button" data-testid="pick-groups" @click="$emit(\'update:modelValue\', [3])" />'
          },
          Icon: true
        }
      }
    })

    // Toggle all native-only OpenAI controls while complete all-native selection is active
    await wrapper.get('#bulk-edit-openai-passthrough-enabled').setValue(true)
    await wrapper.get('#bulk-edit-openai-passthrough-toggle').trigger('click')
    await wrapper.get('#bulk-edit-openai-flatten-namespaces-enabled').setValue(true)
    await wrapper.get('#bulk-edit-openai-flatten-namespaces-toggle').trigger('click')
    await wrapper.get('#bulk-edit-openai-ws-mode-enabled').setValue(true)
    await wrapper.get('[data-testid="bulk-edit-openai-ws-mode-select"]').setValue('ctx_pool')
    await wrapper.get('#bulk-edit-openai-codex-cli-only-enabled').setValue(true)
    await wrapper.get('#bulk-edit-openai-codex-cli-only-toggle').trigger('click')
    await wrapper.get('#bulk-edit-openai-codex-app-server-enabled').setValue(true)
    await wrapper.get('#bulk-edit-openai-codex-app-server-toggle').trigger('click')
    await wrapper.get('#bulk-edit-openai-codex-fingerprint-mode-enabled').setValue(true)
    await wrapper.get('[data-testid="bulk-codex-fingerprint-mode-select"]').setValue('full')
    await wrapper.get('#bulk-edit-openai-compact-mode-enabled').setValue(true)
    await wrapper.get('[data-testid="bulk-edit-openai-compact-mode-select"]').setValue('force_on')
    await wrapper.get('#bulk-edit-openai-compact-model-mapping-enabled').setValue(true)
    await wrapper.get('[data-testid="bulk-edit-openai-compact-model-mapping-add"]').trigger('click')
    const compactInputs = wrapper.findAll('[data-testid="bulk-edit-openai-compact-model-mapping-input"]')
    await compactInputs[0].setValue('gpt-5.4')
    await compactInputs[1].setValue('gpt-5.4-compact')

    // Target expands to cross-page selection with unloaded subtype metadata (modal stays open)
    await wrapper.setProps({
      accountIds: [1, 2, 3],
      subtypeMetadataComplete: false
    })

    // Submitting with only hidden native toggles enabled must be rejected
    await wrapper.get('form#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()
    expect(bulkUpdateMock).not.toHaveBeenCalled()
    expect(showErrorMock).toHaveBeenCalledWith('admin.accounts.bulkEdit.noFieldsSelected')

    // Ordinary mapping, priority, group, and proxy edits still work and omit all hidden native keys
    await wrapper.get('#bulk-edit-model-restriction-enabled').setValue(true)
    const mappingModeBtn = wrapper
      .findAll('button')
      .find((btn) => btn.text().includes('admin.accounts.modelMapping'))!
    await mappingModeBtn.trigger('click')
    const addMappingBtn = wrapper
      .findAll('button')
      .find((btn) => btn.text().includes('admin.accounts.addMapping'))!
    await addMappingBtn.trigger('click')
    const mappingInputs = wrapper.findAll('#bulk-edit-model-restriction-body input.input')
    await mappingInputs[0].setValue('gpt-6-astra')
    await mappingInputs[1].setValue('gpt-6-astra')

    await wrapper.get('#bulk-edit-priority-enabled').setValue(true)
    await wrapper.get('#bulk-edit-priority').setValue('6')
    await wrapper.get('#bulk-edit-proxy-enabled').setValue(true)
    await wrapper.get('[data-testid="pick-proxy"]').trigger('click')
    await wrapper.get('#bulk-edit-groups-enabled').setValue(true)
    await wrapper.get('[data-testid="pick-groups"]').trigger('click')

    await wrapper.get('form#bulk-edit-account-form').trigger('submit.prevent')
    await flushPromises()

    expect(bulkUpdateMock).toHaveBeenCalledTimes(1)
    expect(vi.mocked(adminAPI.accounts.bulkUpdate)).toHaveBeenCalledWith([1, 2, 3], {
      credentials: {
        model_mapping: {
          'gpt-6-astra': 'gpt-6-astra'
        }
      },
      priority: 6,
      proxy_id: 9,
      group_ids: [3]
    })
    expect(vi.mocked(adminAPI.accounts.bulkUpdate).mock.calls[0]?.[1]?.extra).toBeUndefined()
  })

  it.each([
    ['admin AccountTestModal', AdminAccountTestModal],
    ['account AccountTestModal', AccountTestModal]
  ])('%s shows real catalog error and hides compact/image modes for BPS accounts', async (_label, ModalComp) => {
    getAvailableModelsMock.mockRejectedValueOnce({
      response: { data: { message: 'BPS 账号未配置显式模型或分组兜底模型' } }
    })

    const bpsAccount = {
      id: 501,
      name: 'bps-test',
      platform: 'openai',
      type: 'oauth',
      status: 'active',
      credentials: {
        client_id: OPENAI_EXCEL_OAUTH_CLIENT_ID
      }
    } as any

    const wrapper = mount(ModalComp as any, {
      props: { show: false, account: bpsAccount },
      global: {
        stubs: {
          BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' },
          Select: { template: '<div class="select-stub" />' },
          TextArea: { template: '<textarea />' },
          Icon: true
        }
      }
    })

    await wrapper.setProps({ show: true })
    await flushPromises()

    // Displays real API error inline instead of silently swallowing or falling back to native Codex catalog
    expect(wrapper.get('[data-testid="account-test-catalog-error"]').text()).toContain(
      'BPS 账号未配置显式模型或分组兜底模型'
    )
    // Hides compact mode and OpenAI image test prompt mode on BPS
    expect(wrapper.text()).not.toContain('admin.accounts.testModeCompact')
    expect(wrapper.find('[data-testid="test-prompt-mode-image"]').exists()).toBe(false)

    // When catalog succeeds with empty array, shows explicit BPS empty-models guidance
    getAvailableModelsMock.mockResolvedValueOnce([])
    await wrapper.setProps({ show: false })
    await wrapper.setProps({ show: true })
    await flushPromises()
    expect(wrapper.find('[data-testid="account-test-catalog-error"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="account-test-bps-empty-models"]').exists()).toBe(true)
  })
})
