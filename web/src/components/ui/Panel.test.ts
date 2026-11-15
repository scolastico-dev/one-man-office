import { mount } from '@vue/test-utils'
import { expect, test } from 'vitest'
import Panel from './Panel.vue'

test('Panel: label is an h2 and labels the section', () => {
  const wrapper = mount(Panel, { props: { label: 'Ready For Dev', count: 3 }, slots: { default: '<p>cards</p>' } })
  const section = wrapper.get('section')
  const heading = wrapper.get('h2')
  expect(heading.attributes('id')).toBeTruthy()
  expect(section.attributes('aria-labelledby')).toBe(heading.attributes('id'))
  expect(heading.text().replace(/\s+/g, ' ')).toBe('Ready For Dev 3')
  expect(wrapper.text()).toContain('cards')
})

test('Panel: renders the requested element and uses a given label id', () => {
  const wrapper = mount(Panel, { props: { label: 'Planning', as: 'li', labelId: 'col-planning' } })
  expect(wrapper.element.tagName).toBe('LI')
  expect(wrapper.attributes('aria-labelledby')).toBe('col-planning')
  expect(wrapper.get('h2').attributes('id')).toBe('col-planning')
})
