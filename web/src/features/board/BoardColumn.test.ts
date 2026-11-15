import { mount } from '@vue/test-utils'
import { expect, test } from 'vitest'
import BoardColumn from './BoardColumn.vue'
import { card, column } from './testing'

const text = (s: string) => s.replace(/\s+/g, ' ').trim()

test('BoardColumn header reads "Ready For Dev" with its count', () => {
  const wrapper = mount(BoardColumn, {
    props: { model: column('ready', [card({ number: 1 }), card({ number: 2 }), card({ number: 3 })]) },
  })
  const root = wrapper.get('[data-drop-column="ready"]')
  const heading = wrapper.get('h2')
  expect(text(heading.text())).toBe('Ready For Dev 3')
  expect(root.attributes('aria-labelledby')).toBe(heading.attributes('id'))
  expect(wrapper.findAll('ul[role="list"] > li[data-ticket]')).toHaveLength(3)
})

test('BoardColumn refused state sets aria-disabled and shows the reason', () => {
  const reason = 'A ticket enters Review when its agent hands it in.'
  const wrapper = mount(BoardColumn, {
    props: { model: column('review', [card()], { dropState: 'refused', reason }) },
  })
  const root = wrapper.get('[data-drop-column="review"]')
  expect(root.attributes('aria-disabled')).toBe('true')
  expect(root.classes()).toContain('column--refused')
  expect(wrapper.get('[data-reason]').text()).toBe(reason)
})

test('a column with stories and tasks shows the Stories and Tasks headings', () => {
  const wrapper = mount(BoardColumn, {
    props: {
      model: column('in_progress', [
        card({ number: 1, type: 'story' }),
        card({ number: 2 }),
        card({ number: 3 }),
      ]),
    },
  })
  const headings = wrapper.findAll('h3').map((h) => text(h.text()))
  expect(headings).toEqual(['Stories 1', 'Tasks 2'])
  const lists = wrapper.findAll('ul[role="list"]')
  expect(lists.map((l) => l.findAll('li[data-ticket]').map((li) => li.attributes('data-ticket')))).toEqual([
    ['1'],
    ['2', '3'],
  ])
})

test('a column with only tasks shows no group headings', () => {
  const wrapper = mount(BoardColumn, { props: { model: column('planning', [card({ number: 2 })]) } })
  expect(wrapper.findAll('h3')).toHaveLength(0)
})

test('an empty column says so and Done offers to load older tickets', async () => {
  const wrapper = mount(BoardColumn, { props: { model: column('done', [], { more: true }) } })
  expect(wrapper.text()).toContain('No tickets')
  await wrapper.get('button[data-load-more]').trigger('click')
  expect(wrapper.emitted('load-more')).toHaveLength(1)
})
