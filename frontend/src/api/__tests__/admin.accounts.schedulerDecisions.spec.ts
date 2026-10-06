import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get } = vi.hoisted(() => ({
  get: vi.fn()
}))

vi.mock('@/api/client', () => ({
  apiClient: { get }
}))

import { listSchedulerDecisions } from '@/api/admin/accounts'

describe('scheduler decisions API', () => {
  beforeEach(() => {
    get.mockReset()
    get.mockResolvedValue({ data: { items: [], limit: 50 } })
  })

  it('omits group_id for all groups', async () => {
    await listSchedulerDecisions(50, null)
    await listSchedulerDecisions(50)
    expect(get).toHaveBeenNthCalledWith(1, '/admin/accounts/scheduler-decisions', { params: { limit: 50 } })
    expect(get).toHaveBeenNthCalledWith(2, '/admin/accounts/scheduler-decisions', { params: { limit: 50 } })
  })

  it('sends the selected group as group_id', async () => {
    await listSchedulerDecisions(50, 7)
    expect(get).toHaveBeenCalledWith('/admin/accounts/scheduler-decisions', { params: { limit: 50, group_id: 7 } })
  })

  it('never sends a group_id that is not a positive integer', async () => {
    for (const id of [0, -3, 1.5, Number.NaN]) await listSchedulerDecisions(50, id)
    expect(get).toHaveBeenCalledTimes(4)
    for (const call of get.mock.calls) expect(call).toEqual(['/admin/accounts/scheduler-decisions', { params: { limit: 50 } }])
  })
})
