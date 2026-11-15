import { mount } from '@vue/test-utils'
import { describe, expect, test } from 'vitest'
import { h } from 'vue'
import UiTabs from './UiTabs.vue'

const tabs = [
  { id: 'ceo', label: 'CEO', hint: 'Starts the CEO' },
  { id: 'agents', label: 'Agents', count: 3 },
  { id: 'terminals', label: 'Terminals' },
]

function setup(orientation?: 'horizontal' | 'vertical') {
  const wrapper = mount(UiTabs, {
    attachTo: document.body,
    props: {
      tabs,
      label: 'Sidebar',
      modelValue: 'agents',
      ...(orientation ? { orientation } : {}),
      'onUpdate:modelValue': (v: string) => wrapper.setProps({ modelValue: v }),
    },
    slots: { panel: ({ active }: { active: string }) => h('p', { 'data-shown': active }, `panel of ${active}`) },
  })
  const tab = (id: string) => wrapper.get(`[data-tab="${id}"]`)
  const focused = () => (document.activeElement as HTMLElement | null)?.dataset.tab
  return { wrapper, tab, focused }
}

describe('UiTabs', () => {
  test('a tablist named by its label, one tab selected and in the tab order', () => {
    const { wrapper, tab } = setup()
    const list = wrapper.get('[role="tablist"]')
    expect(list.attributes('aria-label')).toBe('Sidebar')
    expect(list.attributes('aria-orientation')).toBe('horizontal')
    expect(tab('agents').attributes('aria-selected')).toBe('true')
    expect(tab('agents').attributes('tabindex')).toBe('0')
    expect(tab('ceo').attributes('aria-selected')).toBe('false')
    expect(tab('ceo').attributes('tabindex')).toBe('-1')
    expect(tab('agents').text()).toContain('3')
    wrapper.unmount()
  })

  test('arrow keys move focus without activating; Enter activates', async () => {
    const { wrapper, tab, focused } = setup()
    ;(tab('agents').element as HTMLElement).focus()
    await tab('agents').trigger('keydown', { key: 'ArrowRight' })
    expect(focused()).toBe('terminals')
    expect(tab('terminals').attributes('tabindex')).toBe('0')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    // Wraps around, past the tab with side effects, without activating it.
    await tab('terminals').trigger('keydown', { key: 'ArrowRight' })
    expect(focused()).toBe('ceo')
    await tab('ceo').trigger('keydown', { key: 'ArrowLeft' })
    expect(focused()).toBe('terminals')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    expect(tab('agents').attributes('aria-selected')).toBe('true')

    await tab('terminals').trigger('keydown', { key: 'Enter' })
    expect(wrapper.emitted('update:modelValue')).toEqual([['terminals']])
    expect(tab('terminals').attributes('aria-selected')).toBe('true')
    await tab('terminals').trigger('keydown', { key: 'ArrowLeft' })
    await tab('agents').trigger('keydown', { key: ' ' })
    expect(wrapper.emitted('update:modelValue')).toEqual([['terminals'], ['agents']])
    wrapper.unmount()
  })

  test('Home and End move focus to the first and the last tab', async () => {
    const { wrapper, tab, focused } = setup()
    ;(tab('agents').element as HTMLElement).focus()
    await tab('agents').trigger('keydown', { key: 'End' })
    expect(focused()).toBe('terminals')
    await tab('terminals').trigger('keydown', { key: 'Home' })
    expect(focused()).toBe('ceo')
    expect(wrapper.emitted('update:modelValue')).toBeUndefined()
    wrapper.unmount()
  })

  test('vertical tabs use the up and down arrows', async () => {
    const { wrapper, tab, focused } = setup('vertical')
    expect(wrapper.get('[role="tablist"]').attributes('aria-orientation')).toBe('vertical')
    ;(tab('agents').element as HTMLElement).focus()
    await tab('agents').trigger('keydown', { key: 'ArrowDown' })
    expect(focused()).toBe('terminals')
    await tab('terminals').trigger('keydown', { key: 'ArrowUp' })
    expect(focused()).toBe('agents')
    wrapper.unmount()
  })

  test('a click activates', async () => {
    const { wrapper, tab } = setup()
    await tab('ceo').trigger('click')
    expect(wrapper.emitted('update:modelValue')).toEqual([['ceo']])
    wrapper.unmount()
  })

  test('the panel is labelled by its tab and gets the active id', async () => {
    const { wrapper, tab } = setup()
    const panel = wrapper.get('[role="tabpanel"]')
    expect(panel.attributes('aria-labelledby')).toBe(tab('agents').attributes('id'))
    expect(tab('agents').attributes('aria-controls')).toBe(panel.attributes('id'))
    expect(panel.get('[data-shown]').text()).toBe('panel of agents')
    await tab('terminals').trigger('click')
    expect(wrapper.get('[role="tabpanel"]').attributes('aria-labelledby')).toBe(tab('terminals').attributes('id'))
    expect(wrapper.get('[data-shown]').text()).toBe('panel of terminals')
    wrapper.unmount()
  })

  test('a hint describes its tab', () => {
    const { wrapper, tab } = setup()
    const described = tab('ceo').attributes('aria-describedby')
    expect(described).toBeTruthy()
    expect(wrapper.get(`[id="${described}"]`).text()).toBe('Starts the CEO')
    wrapper.unmount()
  })

  test('without a panel slot there is no tab panel', () => {
    const wrapper = mount(UiTabs, { props: { tabs, label: 'Sidebar', modelValue: 'ceo' } })
    expect(wrapper.find('[role="tabpanel"]').exists()).toBe(false)
    expect(wrapper.get('[data-tab="ceo"]').attributes('aria-controls')).toBeUndefined()
  })
})
