import { mount } from '@vue/test-utils'
import { expect, test } from 'vitest'
import DurationField from './DurationField.vue'

test('a labelled text field with examples in its hint; the value is trimmed, nothing else is checked', async () => {
  const wrapper = mount(DurationField, {
    props: {
      label: 'Silent after',
      hint: 'An agent silent this long gets a nudge.',
      modelValue: '15m',
      'onUpdate:modelValue': (v: string) => wrapper.setProps({ modelValue: v }),
    },
  })
  const input = wrapper.get('input')
  expect(wrapper.get('label').text()).toBe('Silent after')
  const hint = wrapper.get('.omo-field__hint')
  expect(hint.text()).toBe('An agent silent this long gets a nudge. For example 45s, 15m, 1h30m.')
  expect(input.attributes('aria-describedby')).toBe(hint.attributes('id'))
  expect(input.attributes('spellcheck')).toBe('false')

  await input.setValue('  2h ')
  expect(wrapper.props('modelValue')).toBe('2h')
  await input.setValue('soon')
  expect(wrapper.props('modelValue')).toBe('soon')
  expect(wrapper.find('.omo-field__error').exists()).toBe(false)

  await wrapper.setProps({ error: 'time: invalid duration "soon"' })
  expect(input.attributes('aria-invalid')).toBe('true')
  expect(wrapper.get('.omo-field__error').text()).toBe('time: invalid duration "soon"')
})
