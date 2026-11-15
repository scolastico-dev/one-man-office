import { mount } from '@vue/test-utils'
import { describe, expect, test } from 'vitest'
import SaveBar from './SaveBar.vue'

function setup(props: Record<string, unknown> = {}) {
  const wrapper = mount(SaveBar, { props: { dirty: false, busy: false, ...props } })
  const button = (name: string) => wrapper.findAll('button').find((b) => b.text() === name)!
  return { wrapper, button }
}

describe('SaveBar', () => {
  test('save bar disables Save while clean or busy and names its region', async () => {
    const { wrapper, button } = setup()
    const region = wrapper.get('[role="region"]')
    expect(region.attributes('aria-label')).toBe('Save changes')
    expect(button('Save').attributes('disabled')).toBeDefined()
    expect(button('Discard').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-save-state]').text()).toBe('No unsaved changes')

    await wrapper.setProps({ dirty: true })
    expect(button('Save').attributes('disabled')).toBeUndefined()
    expect(button('Discard').attributes('disabled')).toBeUndefined()
    expect(wrapper.get('[data-save-state]').text()).toBe('Unsaved changes')

    await wrapper.setProps({ busy: true })
    expect(button('Save').attributes('disabled')).toBeDefined()
    expect(button('Discard').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-save-state]').text()).toBe('Saving…')
    expect(region.attributes('aria-busy')).toBe('true')
  })

  test('Save and Discard emit', async () => {
    const { wrapper, button } = setup({ dirty: true })
    await button('Save').trigger('click')
    await button('Discard').trigger('click')
    expect(wrapper.emitted('save')).toHaveLength(1)
    expect(wrapper.emitted('discard')).toHaveLength(1)
  })

  test('saved and conflict are said in words; the slot holds the conflict choices', () => {
    const saved = setup({ status: 'saved' })
    expect(saved.wrapper.get('[data-save-state]').text()).toBe('Saved')
    const conflict = mount(SaveBar, {
      props: { dirty: true, busy: false, status: 'conflict' },
      slots: { default: '<button>Use the new value</button><button>Keep mine</button>' },
    })
    expect(conflict.get('[data-save-state]').text()).toBe('Changed outside this page')
    expect(conflict.text()).toContain('Keep mine')
  })

  test('an error shows in the bar and describes the Save button', () => {
    const { wrapper, button } = setup({ dirty: true, error: 'settings.yaml changed since you loaded it.' })
    const error = wrapper.get('[data-save-error]')
    expect(error.text()).toBe('settings.yaml changed since you loaded it.')
    expect(button('Save').attributes('aria-describedby')).toBe(error.attributes('id'))
  })
})
