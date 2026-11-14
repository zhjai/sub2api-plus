import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import AccountUsageCell from '../AccountUsageCell.vue'
import OpenAIBPSBadge from '../OpenAIBPSBadge.vue'
import OpenAIQuotaResetCell from '../OpenAIQuotaResetCell.vue'
import OpenAIReferralCell from '../OpenAIReferralCell.vue'
import AccountActionMenu from '@/components/admin/account/AccountActionMenu.vue'
import {
  OPENAI_EXCEL_BPS_CREDENTIALS_KEY,
  OPENAI_EXCEL_OAUTH_CLIENT_ID
} from '@/utils/openaiOAuthClient'
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
    'admin.accounts.bpsAccount.badges.bpsOnlyOff': 'BPS 已关闭',
    'admin.accounts.bpsAccount.badges.bpsOnly403': 'BPS · 403 停用',
    'admin.accounts.bpsAccount.badges.bpsOnlyGrantInvalid': 'BPS · 授权失效',
    'admin.accounts.bpsAccount.badges.bpsOnlyExpired': 'BPS · 令牌过期',
    'admin.accounts.bpsAccount.badges.dualActive': 'Codex + BPS',
    'admin.accounts.bpsAccount.badges.dualStandby': 'BPS 待命',
    'admin.accounts.bpsAccount.usage.title': '本地用量',
    'admin.accounts.bpsAccount.usage.authoritativeTitle': '账户窗口快照',
    'admin.accounts.bpsAccount.usage.sourceWham': '来源：实时账户查询 (/wham/usage)',
    'admin.accounts.bpsAccount.usage.sourceSnapshot': '来源：已缓存账户快照',
    'admin.accounts.bpsAccount.usage.sourceDisclaimer':
      '注：上游配额反映所属账号窗口快照，并非保证独立的 BPS 专属余额。',
    'admin.accounts.bpsAccount.usage.quotaUnknown':
      '上游未返回可用百分比窗口；剩余额度未知（不会按 0% 显示）。',
    'admin.accounts.bpsAccount.usage.refresh': '刷新',
    'admin.accounts.bpsAccount.usage.noRequests': '0 次请求',
    'admin.accounts.bpsAccount.usage.empty': '暂无本地窗口记录',
    'admin.accounts.bpsAccount.usage.loadFailed': '加载本地用量失败',
    'admin.accounts.bpsAccount.usage.dualLocalLabel': 'BPS 本地'
  }
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string, params?: Record<string, unknown>) => {
        if (key === 'admin.accounts.bpsAccount.usage.refreshedAt') {
          return `已刷新 ${params?.time ?? ''}`
        }
        if (key === 'admin.accounts.bpsAccount.usage.quotaUpdatedAt') {
          return `快照时间 ${params?.time ?? ''}`
        }
        if (key === 'admin.accounts.bpsAccount.usage.quotaRefreshError') {
          return `配额刷新失败（保留上次快照）：${params?.error ?? ''}`
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

describe('BPS badge states, authoritative window vs local usage display, and native control guards', () => {
  beforeEach(() => {
    getUsageMock.mockReset()
  })

  it('distinguishes BPS-only, dual-grant active/standby, 403 auto-disable, grant invalid, and token expired badges', () => {
    const bpsWrapper = mount(OpenAIBPSBadge, { props: { account: makeBPSAccount() } })
    expect(bpsWrapper.find('[data-testid="account-bps-badge"]').exists()).toBe(true)
    expect(bpsWrapper.text()).toBe('BPS')

    const codexWrapper = mount(OpenAIBPSBadge, {
      props: { account: makeBPSAccount({ credentials: { client_id: 'app_EMoamEEZ73f0CkXaXp7hrann' } }) }
    })
    expect(codexWrapper.find('[data-testid="account-bps-badge"]').exists()).toBe(false)

    const dualStandbyWrapper = mount(OpenAIBPSBadge, {
      props: {
        account: makeBPSAccount({
          credentials: {
            client_id: 'app_EMoamEEZ73f0CkXaXp7hrann',
            [OPENAI_EXCEL_BPS_CREDENTIALS_KEY]: {
              client_id: OPENAI_EXCEL_OAUTH_CLIENT_ID,
              access_token: 'nested-at'
            }
          },
          extra: {}
        })
      }
    })
    expect(dualStandbyWrapper.text()).toBe('BPS 待命')

    const dualActiveWrapper = mount(OpenAIBPSBadge, {
      props: {
        account: makeBPSAccount({
          credentials: {
            client_id: 'app_EMoamEEZ73f0CkXaXp7hrann',
            [OPENAI_EXCEL_BPS_CREDENTIALS_KEY]: {
              client_id: OPENAI_EXCEL_OAUTH_CLIENT_ID,
              access_token: 'nested-at'
            }
          },
          extra: { openai_excel_bps: true }
        })
      }
    })
    expect(dualActiveWrapper.text()).toBe('Codex + BPS')

    const bps403Wrapper = mount(OpenAIBPSBadge, {
      props: {
        account: makeBPSAccount({
          extra: { openai_excel_bps_403_disabled_at: '2026-11-13T10:00:00Z' }
        })
      }
    })
    expect(bps403Wrapper.text()).toBe('BPS · 403 停用')

    const grantInvalidWrapper = mount(OpenAIBPSBadge, {
      props: {
        account: makeBPSAccount({
          extra: { openai_excel_bps_grant_invalid: 'fp-invalid' }
        })
      }
    })
    expect(grantInvalidWrapper.text()).toBe('BPS · 授权失效')

    const apiKeyWrapper = mount(OpenAIBPSBadge, {
      props: { account: makeBPSAccount({ type: 'apikey' }) }
    })
    expect(apiKeyWrapper.find('[data-testid="account-bps-badge"]').exists()).toBe(false)
  })

  it('renders BPS local usage without fabricated 0% when windows are absent, and refreshes read-only without active probe', async () => {
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
    // Unknown windows must NEVER render % or 0%
    expect(localUsage.text()).not.toContain('%')
    expect(wrapper.find('[data-testid="bps-authoritative-quota"]').exists()).toBe(false)
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

  it('displays authoritative quota windows with source, timestamp, non-BPS-balance disclaimer, and quota_refresh_error alongside retained stale snapshot + local stats', async () => {
    getUsageMock.mockResolvedValueOnce({
      updated_at: '2026-11-13T06:10:00Z',
      source: 'bps_local',
      quota_source: 'account_codex_snapshot',
      quota_updated_at: '2026-11-13T05:30:00Z',
      quota_refresh_error: 'wham /usage returned HTTP 503; showing cached snapshot',
      five_hour: { utilization: 37, resets_at: '2026-11-13T09:00:00Z' },
      seven_day: { utilization: 64, resets_at: '2026-11-18T00:00:00Z' },
      bps_local_usage: {
        five_hour: { requests: 9, tokens: 3200, cost: 0.14, user_cost: 0.12 },
        seven_day: { requests: 30, tokens: 12000, cost: 0.55, user_cost: 0.5 }
      }
    })

    const wrapper = mount(AccountUsageCell, {
      props: {
        account: makeBPSAccount({ id: 810 })
      }
    })
    await flushPromises()

    const quotaSection = wrapper.get('[data-testid="bps-authoritative-quota"]')
    expect(quotaSection.text()).toContain('37%')
    expect(quotaSection.text()).toContain('64%')
    expect(wrapper.get('[data-testid="bps-quota-source"]').text()).toBe('来源：已缓存账户快照')
    expect(wrapper.get('[data-testid="bps-quota-updated-at"]').text()).toContain('快照时间')
    expect(wrapper.get('[data-testid="bps-quota-scope-disclaimer"]').text()).toContain('并非保证独立的 BPS 专属余额')
    expect(wrapper.get('[data-testid="bps-quota-refresh-error"]').text()).toContain(
      'wham /usage returned HTTP 503; showing cached snapshot'
    )
    expect(wrapper.get('[data-testid="bps-local-usage-5h"]').text()).toContain('9 req')
  })

  it('excludes BPS-only accounts from OpenAIQuotaResetCell, OpenAIReferralCell, and Spark/privacy menu actions', () => {
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
