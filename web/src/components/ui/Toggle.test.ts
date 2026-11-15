import { mount } from '@vue/test-utils'
import { expect, test } from 'vitest'
import Toggle from './Toggle.vue'

test('Toggle: Space toggles and aria-checked follows', async () => {
  const wrapper = mount(Toggle, {
    props: { label: 'Pause', modelValue: false, 'onUpdate:modelValue': (v: boolean) => wrapper.setProps({ modelValue: v }) },
  })
  const button = wrapper.get('[role="switch"]')
  expect(button.attributes('aria-checked')).toBe('false')
  expect(button.text()).toBe('Pause')

  await button.trigger('keydown', { key: ' ' })
  expect(button.attributes('aria-checked')).toBe('true')

  await button.trigger('keydown', { key: ' ' })
  expect(button.attributes('aria-checked')).toBe('false')

  await button.trigger('click', { detail: 1 })
  expect(button.attributes('aria-checked')).toBe('true')
})

test('Toggle: a hidden label stays the accessible name', () => {
  const wrapper = mount(Toggle, { props: { label: 'Pause all agents', hideLabel: true } })
  expect(wrapper.get('[role="switch"]').attributes('aria-label')).toBe('Pause all agents')
})

test('Toggle: state words show the state for the eye while the label stays the accessible name', async () => {
  const wrapper = mount(Toggle, { props: { label: 'Paused', states: { on: 'Paused', off: 'Running' } } })
  const button = wrapper.get('[role="switch"]')
  expect(button.attributes('aria-label')).toBe('Paused')
  expect(button.text()).toBe('Running')
  // The word repeats aria-checked, so screen readers do not hear it twice.
  expect(button.get('[data-state-word]').attributes('aria-hidden')).toBe('true')
  await wrapper.setProps({ modelValue: true })
  expect(button.text()).toBe('Paused')
})
