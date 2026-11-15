import { mount } from '@vue/test-utils'
import { describe, expect, test } from 'vitest'
import NumberField from './NumberField.vue'

function setup(props: Record<string, unknown> = {}) {
  const wrapper = mount(NumberField, {
    attachTo: document.body,
    props: {
      label: 'Max running agents',
      modelValue: 4 as number | null,
      'onUpdate:modelValue': (v: number | null) => wrapper.setProps({ modelValue: v }),
      ...props,
    },
  })
  const input = () => wrapper.get('input')
  const model = () => wrapper.props('modelValue')
  return { wrapper, input, model }
}

describe('NumberField', () => {
  test('a labelled spinbutton showing the value with its bounds', () => {
    const { wrapper, input } = setup({ min: 1, max: 64, hint: 'At most 64.' })
    expect(input().attributes('role')).toBe('spinbutton')
    expect(input().attributes('inputmode')).toBe('numeric')
    expect((input().element as HTMLInputElement).value).toBe('4')
    expect(input().attributes('aria-valuenow')).toBe('4')
    expect(input().attributes('aria-valuemin')).toBe('1')
    expect(input().attributes('aria-valuemax')).toBe('64')
    const label = wrapper.get('label')
    expect(label.text()).toBe('Max running agents')
    expect(label.attributes('for')).toBe(input().attributes('id'))
    const hint = wrapper.get('.omo-field__hint')
    expect(input().attributes('aria-describedby')).toBe(hint.attributes('id'))
    wrapper.unmount()
  })

  test('number field keeps unparsable text and reports it', async () => {
    const { wrapper, input, model } = setup()
    await input().setValue('12')
    expect(model()).toBe(12)
    await input().setValue('12a')
    // The text stays as typed; the model keeps the last number.
    expect((input().element as HTMLInputElement).value).toBe('12a')
    expect(model()).toBe(12)
    expect(input().attributes('aria-invalid')).toBe('true')
    const error = wrapper.get('.omo-field__error')
    expect(error.text()).toBe('Enter a whole number')
    expect(input().attributes('aria-describedby')).toContain(error.attributes('id'))
    expect(wrapper.emitted('parse')!.at(-1)).toEqual(['Enter a whole number'])

    await input().setValue(' 7 ')
    expect(model()).toBe(7)
    expect(wrapper.find('.omo-field__error').exists()).toBe(false)
    expect(input().attributes('aria-invalid')).toBeUndefined()
    expect(wrapper.emitted('parse')!.at(-1)).toEqual([null])
    wrapper.unmount()
  })

  test('an empty field is null; a decimal is not a whole number', async () => {
    const { wrapper, input, model } = setup()
    await input().setValue('')
    expect(model()).toBeNull()
    expect(input().attributes('aria-valuenow')).toBeUndefined()
    await input().setValue('1.5')
    expect(model()).toBeNull()
    expect(wrapper.get('.omo-field__error').text()).toBe('Enter a whole number')
    wrapper.unmount()
  })

  test('the server error shows below; the parse message wins while the text is not a number', async () => {
    const { wrapper, input } = setup({ error: 'max_running_agents must be at most 64' })
    expect(wrapper.get('.omo-field__error').text()).toBe('max_running_agents must be at most 64')
    expect(input().attributes('aria-invalid')).toBe('true')
    await input().setValue('x')
    expect(wrapper.findAll('.omo-field__error').map((e) => e.text())).toEqual(['Enter a whole number'])
    wrapper.unmount()
  })

  test('arrow keys step within the bounds; Home and End go to them', async () => {
    const { wrapper, input, model } = setup({ min: 1, max: 6, step: 2 })
    await input().trigger('keydown', { key: 'ArrowUp' })
    expect(model()).toBe(6)
    await input().trigger('keydown', { key: 'ArrowUp' })
    expect(model()).toBe(6)
    await input().trigger('keydown', { key: 'ArrowDown' })
    expect(model()).toBe(4)
    await input().trigger('keydown', { key: 'Home' })
    expect(model()).toBe(1)
    await input().trigger('keydown', { key: 'End' })
    expect(model()).toBe(6)
    expect((input().element as HTMLInputElement).value).toBe('6')
    wrapper.unmount()
  })

  test('a new value from outside replaces the text, also unparsable text', async () => {
    const { wrapper, input } = setup()
    await input().setValue('abc')
    await wrapper.setProps({ modelValue: 9 })
    expect((input().element as HTMLInputElement).value).toBe('9')
    expect(wrapper.find('.omo-field__error').exists()).toBe(false)
    wrapper.unmount()
  })
})
