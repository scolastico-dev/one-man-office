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
