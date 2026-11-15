import { mount } from '@vue/test-utils'
import { expect, test } from 'vitest'
import FormSection from './FormSection.vue'

test('a fieldset named by its legend and described by its description', () => {
  const wrapper = mount(FormSection, {
    attachTo: document.body,
    props: { legend: 'Health', description: 'When omo nudges an agent.' },
    slots: { default: '<input aria-label="x" />' },
  })
  const fieldset = wrapper.get('fieldset')
  expect(fieldset.get('legend').text()).toBe('Health')
  expect(fieldset.get('legend h2').text()).toBe('Health')
  const desc = document.getElementById(fieldset.attributes('aria-describedby')!)!
  expect(desc.textContent).toBe('When omo nudges an agent.')
  wrapper.unmount()
})

test('the description slot wins over the prop; no description, no aria-describedby', () => {
  const slotted = mount(FormSection, { props: { legend: 'Stories' }, slots: { description: 'Rich <code>text</code>' } })
  expect(slotted.get('.form-section__description').text()).toBe('Rich text')
  const bare = mount(FormSection, { props: { legend: 'Tasks', level: 3 } })
  expect(bare.get('fieldset').attributes('aria-describedby')).toBeUndefined()
  expect(bare.find('legend h3').exists()).toBe(true)
})
