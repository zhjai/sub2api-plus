import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import AccountUsageCell from '../AccountUsageCell.vue'
import OpenAIBPSBadge from '../OpenAIBPSBadge.vue'
import OpenAIQuotaResetCell from '../OpenAIQuotaResetCell.vue'
import OpenAIReferralCell from '../OpenAIReferralCell.vue'
import AccountActionMenu from '@/components/admin/account/AccountActionMenu.vue'
import { OPENAI_EXCEL_OAUTH_CLIENT_ID } from '@/utils/openaiOAuthClient'
import type { Account } from '@/types'

const { getUsageMock } = vi.hoisted(() => ({
  getUsageMock: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      getUsage: getUsageMock,
      queryOpenAICodexResetCount: vi.fn(),
      queryOpenAIReferrals: vi.fn()
    }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showWarning: vi.fn(),
    showInfo: vi.fn()
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  const zhMap: Record<string, string> = {
    'admin.accounts.bpsAccount.badge': 'BPS',
    'admin.accounts.bpsAccount.badgeTitle': 'OpenAI 官方 Excel 登录（BPS HTTP Responses）',
    'admin.accounts.bpsAccount.usage.title': '本地用量',
    'admin.accounts.bpsAccount.usage.refresh': '刷新',
    'admin.accounts.bpsAccount.usage.noRequests': '0 次请求',
    'admin.accounts.bpsAccount.usage.empty': '暂无本地窗口记录',
    'admin.accounts.bpsAccount.usage.loadFailed': '加载本地用量失败'
  }
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => {
        if (key === 'admin.accounts.bpsAccount.usage.refreshedAt') {
          return `已刷新 ${params?.time ?? ''}`
        }
        return zhMap[key] || key
      }
    })
  }
})

function makeBPSAccount(overrides: Partial<Account> = {}): Account {
  return {
    id: 801,
    name: 'bps-01',
    platform: 'openai',
    type: 'oauth',
    credentials: {
      client_id: OPENAI_EXCEL_OAUTH_CLIENT_ID,
      chatgpt_account_id: 'ws-bps'
    },
    extra: {},
    proxy_id: null,
    concurrency: 5,
    priority: 1,
    status: 'active',
    error_message: null,
    last_used_at: null,
    expires_at: null,
    auto_pause_on_expired: false,
    created_at: '2026-11-12T00:00:00Z',
    updated_at: '2026-11-12T00:00:00Z',
    schedulable: true,
    rate_limited_at: null,
    rate_limit_reset_at: null,
    overload_until: null,
    temp_unschedulable_until: null,
    temp_unschedulable_reason: null,
    session_window_start: null,
    session_window_end: null,
    session_window_status: null,
    ...overrides
  }
}

describe('BPS badge, local usage branch, and native control guards', () => {
  beforeEach(() => {
    getUsageMock.mockReset()
  })

  it('renders OpenAIBPSBadge only for OpenAI OAuth accounts with the Excel client_id', () => {
    const bpsWrapper = mount(OpenAIBPSBadge, { props: { account: makeBPSAccount() } })
    expect(bpsWrapper.find('[data-testid="account-bps-badge"]').exists()).toBe(true)
    expect(bpsWrapper.text()).toBe('BPS')

    const codexWrapper = mount(OpenAIBPSBadge, {
      props: { account: makeBPSAccount({ credentials: { client_id: 'app_EMoamEEZ73f0CkXaXp7hrann' } }) }
    })
    expect(codexWrapper.find('[data-testid="account-bps-badge"]').exists()).toBe(false)

    const apiKeyWrapper = mount(OpenAIBPSBadge, {
      props: { account: makeBPSAccount({ type: 'apikey' }) }
    })
    expect(apiKeyWrapper.find('[data-testid="account-bps-badge"]').exists()).toBe(false)
  })

  it('renders BPS local usage (including meaningful zero requests), refreshes without active probe, and shows human-readable errors', async () => {
    getUsageMock
      .mockResolvedValueOnce({
        updated_at: '2026-11-13T05:00:00Z',
        source: 'bps_local',
        bps_local_usage: {
          five_hour: { requests: 0, tokens: 0, cost: 0, user_cost: 0 },
          seven_day: { requests: 0, tokens: 0, cost: 0, user_cost: 0 }
        },
        five_hour: null,
        seven_day: null
      })
      .mockResolvedValueOnce({
        updated_at: '2026-11-13T05:05:00Z',
        source: 'bps_local',
        bps_local_usage: {
          five_hour: { requests: 12, tokens: 4500, cost: 0.18, user_cost: 0.15 },
          seven_day: { requests: 48, tokens: 24000, cost: 0.92, user_cost: 0.8 }
        },
        five_hour: null,
        seven_day: null
      })
      .mockRejectedValueOnce({
        response: { data: { message: '数据库聚合超时，请稍后重试' } }
      })

    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeBPSAccount({ id: 802 })
      }
    })
    await flushPromises()

    const localUsage = wrapper.get('[data-testid="bps-local-usage"]')
    expect(localUsage.text()).toContain('本地用量')
    expect(localUsage.text()).toContain('0 次请求')
    // Must not render native quota percentage bars or credits claims
    expect(localUsage.text()).not.toContain('%')
    expect(getUsageMock).toHaveBeenCalledTimes(1)
    expect(getUsageMock.mock.calls[0]?.[1]).toBeUndefined()

    // Click manual refresh: must call getUsage(id, undefined, true), NEVER 'active'
    await wrapper.get('[data-testid="bps-local-usage-refresh"]').trigger('click')
    await flushPromises()

    expect(getUsageMock).toHaveBeenCalledTimes(2)
    expect(getUsageMock).toHaveBeenLastCalledWith(802, undefined, true)
    expect(wrapper.get('[data-testid="bps-local-usage-refreshed"]').text()).toContain('已刷新')
    expect(wrapper.get('[data-testid="bps-local-usage-5h"]').text()).toContain('12 req')
    expect(wrapper.get('[data-testid="bps-local-usage-7d"]').text()).toContain('48 req')

    // Refresh error shows human-readable API message inline
    await wrapper.get('[data-testid="bps-local-usage-refresh"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="bps-local-usage-error"]').text()).toContain('数据库聚合超时，请稍后重试')
  })

  it('excludes BPS accounts from OpenAIQuotaResetCell, OpenAIReferralCell, and Spark/privacy menu actions', () => {
    const bpsAccount = makeBPSAccount({ id: 803 })
    const codexAccount = makeBPSAccount({
      id: 804,
      credentials: { chatgpt_account_id: 'ws-codex' }
    })

    const bpsReset = mount(OpenAIQuotaResetCell, { props: { account: bpsAccount } })
    expect(bpsReset.html()).toBe('<!--v-if-->')

    const codexReset = mount(OpenAIQuotaResetCell, { props: { account: codexAccount } })
    expect(codexReset.html()).not.toBe('<!--v-if-->')

    const bpsReferral = mount(OpenAIReferralCell, { props: { account: bpsAccount } })
    expect(bpsReferral.html()).toBe('<!--v-if-->')

    const anchorRect = new DOMRect(80, 80, 24, 24)
    const bpsMenu = mount(AccountActionMenu, {
      props: { show: true, account: bpsAccount, anchorRect },
      attachTo: document.body
    })
    const menuText = document.body.textContent ?? ''
    expect(menuText).not.toContain('admin.accounts.createSparkShadow')
    expect(menuText).not.toContain('admin.accounts.setPrivacy')
    expect(menuText).toContain('admin.accounts.reAuthorize')
    expect(menuText).toContain('admin.accounts.testConnection')
    bpsMenu.unmount()
  })
})
