import { mount } from '@vue/test-utils'
import { expect, test } from 'vitest'
import { nextTick } from 'vue'
import UiMenu from './UiMenu.vue'

const items = [
  { id: 'planning', label: 'Planning' },
  { id: 'in_progress', label: 'In Progress', disabled: true, hint: 'Only omo starts work.' },
  { id: 'ready', label: 'Ready For Dev' },
]

function setup() {
  return mount(UiMenu, { props: { label: 'Move…', items }, attachTo: document.body })
}

test('UiMenu: arrow keys move, disabled items are skipped and announce their hint, Enter selects', async () => {
  const wrapper = setup()
  const button = wrapper.get('button')
  expect(button.attributes('aria-haspopup')).toBe('menu')

  await button.trigger('keydown', { key: 'ArrowDown' })
  await nextTick()
  expect(button.attributes('aria-expanded')).toBe('true')
  const menuItems = wrapper.findAll('[role="menuitem"]')
  expect(document.activeElement).toBe(menuItems[0]!.element)

  const menu = wrapper.get('[role="menu"]')
  await menu.trigger('keydown', { key: 'ArrowDown' })
  await nextTick()
  // The disabled item is reachable so its reason is read, but it cannot be chosen.
  const disabled = menuItems[1]!
  expect(document.activeElement).toBe(disabled.element)
  expect(disabled.attributes('aria-disabled')).toBe('true')
  const hint = wrapper.get(`#${disabled.attributes('aria-describedby')}`)
  expect(hint.text()).toBe('Only omo starts work.')
  await menu.trigger('keydown', { key: 'Enter' })
  expect(wrapper.emitted('select')).toBeUndefined()
  expect(button.attributes('aria-expanded')).toBe('true')

  await menu.trigger('keydown', { key: 'End' })
  await nextTick()
  expect(document.activeElement).toBe(menuItems[2]!.element)
  await menu.trigger('keydown', { key: 'ArrowDown' })
  await nextTick()
  expect(document.activeElement).toBe(menuItems[0]!.element)
  await menu.trigger('keydown', { key: 'Home' })
  await menu.trigger('keydown', { key: 'ArrowUp' })
  await nextTick()
  expect(document.activeElement).toBe(menuItems[2]!.element)

  await menu.trigger('keydown', { key: 'Enter' })
  expect(wrapper.emitted('select')).toEqual([['ready']])
  expect(button.attributes('aria-expanded')).toBe('false')
  expect(document.activeElement).toBe(button.element)
  wrapper.unmount()
})

test('UiMenu: Escape closes and returns focus to the button', async () => {
  const wrapper = setup()
  const button = wrapper.get('button')
  await button.trigger('keydown', { key: 'Enter' })
  await nextTick()
  await wrapper.get('[role="menu"]').trigger('keydown', { key: 'Escape' })
  expect(button.attributes('aria-expanded')).toBe('false')
  expect(document.activeElement).toBe(button.element)
  wrapper.unmount()
})

test('UiMenu: clicking an item selects it, clicking a disabled one does nothing', async () => {
  const wrapper = setup()
  await wrapper.get('button').trigger('click', { detail: 1 })
  const menuItems = wrapper.findAll('[role="menuitem"]')
  await menuItems[1]!.trigger('click')
  expect(wrapper.emitted('select')).toBeUndefined()
  await menuItems[0]!.trigger('click')
  expect(wrapper.emitted('select')).toEqual([['planning']])
  wrapper.unmount()
})
