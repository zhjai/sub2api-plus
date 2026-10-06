import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import type { OpenAIEvalPolicyWeights } from '@/api/admin/accounts'
import { zhT } from '@/views/admin/modelIntegrity/__tests__/zhT'

vi.mock('vue-i18n', async importOriginal => ({
  ...(await importOriginal<typeof import('vue-i18n')>()),
  useI18n: () => ({ t: zhT })
}))

import PolicyWeightsEditor from '../PolicyWeightsEditor.vue'

const base: OpenAIEvalPolicyWeights = { cost: 0.2, error_rate: 0.43, ttft: 0.27, load: 0.1, quality: 0 }

/** Mounts the editor as v-model would drive it, so every emit is applied. */
function mountEditor(initial: OpenAIEvalPolicyWeights = base) {
  const wrapper = mount(PolicyWeightsEditor, {
    attachTo: document.body,
    props: {
      modelValue: initial,
      legend: '自定义权重',
      'onUpdate:modelValue': (value: OpenAIEvalPolicyWeights) => wrapper.setProps({ modelValue: value })
    }
  })
  return wrapper
}

type Wrapper = ReturnType<typeof mountEditor>
const last = (wrapper: Wrapper) => wrapper.emitted('update:modelValue')!.at(-1)![0] as OpenAIEvalPolicyWeights
const order = (wrapper: Wrapper) => wrapper.findAll('[data-testid="priority-row"]').map(row => row.get('.priority-name').text())

async function add(wrapper: Wrapper, factor: string) {
  await wrapper.get('[data-testid="priority-add-select"]').setValue(factor)
  await wrapper.get('[data-testid="priority-add"]').trigger('click')
}

describe('PolicyWeightsEditor absolute priorities', () => {
  it('starts empty, says the weighted score decides, and explains ties and unknown evidence', () => {
    const wrapper = mountEditor()
    expect(wrapper.get('[data-testid="priority-empty"]').text()).toBe('未设置优先因素，账号只按加权分排序。')
    expect(wrapper.find('[data-testid="priority-then"]').exists()).toBe(false)
    const rules = wrapper.get('.priorities-rules').text()
    expect(rules).toContain('只有前面所有优先因素都相同时，后面的优先因素才起作用')
    expect(rules).toContain('缺少该项数据的账号排在有数据的账号之后')
    expect(rules).toContain('全部为 0% 时，所有优先因素都相同的账号按账号 ID 排序')
    // Ordered controls, not a set of checkboxes.
    expect(wrapper.findAll('input[type="checkbox"]')).toHaveLength(0)
    expect(wrapper.findAll('input').map(input => input.attributes('data-testid'))).toEqual(['weight-cost', 'weight-error_rate', 'weight-ttft', 'weight-load', 'weight-quality'])
    wrapper.unmount()
  })

  it('adds price as the first and error rate as the second priority, and marks both', async () => {
    const wrapper = mountEditor()
    await add(wrapper, 'cost')
    await add(wrapper, 'error_rate')

    expect(order(wrapper)).toEqual(['价格更低', '错误率更低'])
    expect(wrapper.findAll('.priority-num').slice(0, 2).map(num => num.text())).toEqual(['1', '2'])
    expect(wrapper.get('[data-testid="weight-rank-cost"]').text()).toBe('第 1 优先')
    expect(wrapper.get('[data-testid="weight-rank-error_rate"]').text()).toBe('第 2 优先')
    expect(wrapper.get('[data-testid="priority-announce"]').text()).toBe('已将错误率添加为第 2 优先。')
    expect(wrapper.findAll('[data-testid="priority-add-select"] option').map(option => option.attributes('value'))).toEqual(['ttft', 'load', 'quality'])

    // Weights are carried along untouched.
    expect(last(wrapper)).toEqual({ ...base, stability: 0, absolute_priorities: ['cost', 'error_rate'] })
    wrapper.unmount()
  })

  it('reorders with labelled up and down buttons and keeps focus on the moved control', async () => {
    const wrapper = mountEditor({ ...base, absolute_priorities: ['cost', 'error_rate', 'quality'] })
    expect(wrapper.get('[data-testid="priority-up-cost"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="priority-down-quality"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="priority-up-quality"]').attributes('aria-label')).toBe('将降智通过率上移')

    await wrapper.get('[data-testid="priority-up-quality"]').trigger('click')
    await nextTick()
    expect(order(wrapper)).toEqual(['价格更低', '降智通过率更高', '错误率更低'])
    expect(document.activeElement?.getAttribute('data-testid')).toBe('priority-up-quality')
    expect(wrapper.get('[data-testid="priority-announce"]').text()).toBe('降智通过率现为第 2 优先。')

    await wrapper.get('[data-testid="priority-down-cost"]').trigger('click')
    expect(last(wrapper).absolute_priorities).toEqual(['quality', 'cost', 'error_rate'])
    wrapper.unmount()
  })

  it('removes a priority and sends an explicit empty list once none is left', async () => {
    const wrapper = mountEditor({ ...base, absolute_priorities: ['cost', 'error_rate'] })
    await wrapper.get('[data-testid="priority-remove-cost"]').trigger('click')
    expect(order(wrapper)).toEqual(['错误率更低'])
    expect(wrapper.get('[data-testid="weight-rank-error_rate"]').text()).toBe('第 1 优先')
    await wrapper.get('[data-testid="priority-remove-error_rate"]').trigger('click')
    expect(last(wrapper).absolute_priorities).toEqual([])
    expect(wrapper.find('[data-testid="priority-empty"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('accepts every weight at 0 with a priority, and says account order breaks the ties', async () => {
    const wrapper = mountEditor({ ...base, absolute_priorities: ['cost'] })
    expect(wrapper.get('[data-testid="priority-then"]').text()).toBe('之后仍相同时，由加权分更高者优先。')
    for (const factor of ['cost', 'error_rate', 'ttft', 'load']) await wrapper.get(`[data-testid="weight-${factor}"]`).setValue('0')
    expect(last(wrapper)).toEqual({ cost: 0, error_rate: 0, ttft: 0, load: 0, quality: 0, stability: 0, absolute_priorities: ['cost'] })
    expect(wrapper.find('[data-testid="weights-error"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="weight-cost"]').attributes('aria-invalid')).toBeUndefined()
    expect(wrapper.get('[data-testid="priority-then"]').text()).toBe('所有权重均为 0%，之后仍相同时按账号 ID 排序。')

    // Without the priority, the same zero weights cannot be saved.
    await wrapper.get('[data-testid="priority-remove-cost"]').trigger('click')
    expect(last(wrapper).absolute_priorities).toEqual([])
    const error = wrapper.get('[data-testid="weights-error"]')
    expect(error.text()).toBe('权重合计须大于 0，请至少为一项设置权重，或添加优先因素。')
    expect(wrapper.get('[data-testid="weight-cost"]').attributes('aria-describedby')).toBe(error.attributes('id'))
    wrapper.unmount()
  })

  it('never hands the parent the array it was given', async () => {
    const priorities: OpenAIEvalPolicyWeights['absolute_priorities'] = ['load']
    const wrapper = mountEditor({ ...base, absolute_priorities: priorities })
    await wrapper.get('[data-testid="weight-load"]').setValue('30')
    expect(last(wrapper).absolute_priorities).toEqual(['load'])
    expect(last(wrapper).absolute_priorities).not.toBe(priorities)
    wrapper.unmount()
  })
})
