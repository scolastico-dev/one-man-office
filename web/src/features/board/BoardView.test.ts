import { mount } from '@vue/test-utils'
import { expect, test } from 'vitest'
import { COLUMNS } from '../../api/types'
import BoardView from './BoardView.vue'
import ColumnSwitcher from './ColumnSwitcher.vue'
import { card, column } from './testing'

const text = (s: string) => s.replace(/\s+/g, ' ').trim()

const columns = () =>
  COLUMNS.map((c, i) => column(c, c === 'review' ? [card({ number: 9, column: c })] : [], { count: i }))

test('BoardView renders five columns in order', () => {
  const wrapper = mount(BoardView, { props: { columns: columns() } })
  const board = wrapper.get('[data-board]')
  expect(board.findAll('[data-drop-column]').map((c) => c.attributes('data-drop-column'))).toEqual([...COLUMNS])
  expect(board.findAll('h2').map((h) => text(h.text()))).toEqual([
    'Planning 0',
    'Ready For Dev 1',
    'In Progress 2',
    'Review 3',
    'Done 4',
  ])
})

test('BoardView forwards open and filter-story from cards', async () => {
  const wrapper = mount(BoardView, { props: { columns: columns() } })
  await wrapper.get('[data-ticket="9"] a').trigger('click')
  expect(wrapper.emitted('open')).toEqual([[9]])
})

test('the column switcher selects the shown column and every tab is a drop target', async () => {
  const wrapper = mount(BoardView, { props: { columns: columns() } })
  const tabs = wrapper.findAll('[role="tab"]')
  expect(tabs.map((t) => t.attributes('data-drop-column'))).toEqual([...COLUMNS])
  expect(text(tabs[3]!.text())).toBe('Review 3')
  expect(tabs[0]!.attributes('aria-selected')).toBe('true')
  await tabs[3]!.trigger('click')
  expect(wrapper.findAll('[role="tab"]')[3]!.attributes('aria-selected')).toBe('true')
  expect(wrapper.get('[data-board] [data-drop-column="review"]').classes()).toContain('column--active')
})

test('ColumnSwitcher emits select and marks refused columns', async () => {
  const cols = columns()
  cols[2]!.dropState = 'refused'
  const wrapper = mount(ColumnSwitcher, { props: { columns: cols, active: 'planning' } })
  const tab = wrapper.get('[role="tab"][data-drop-column="in_progress"]')
  expect(tab.attributes('aria-disabled')).toBe('true')
  await wrapper.get('[role="tab"][data-drop-column="done"]').trigger('click')
  expect(wrapper.emitted('select')).toEqual([['done']])
})
