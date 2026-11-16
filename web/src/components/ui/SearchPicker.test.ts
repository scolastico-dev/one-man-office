import { mount } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import { nextTick } from 'vue'
import SearchPicker, { type PickItem } from './SearchPicker.vue'
import { filterPicks } from './searchPick'

const items: PickItem[] = [
  { id: 'home', label: 'Home folder', detail: '~', always: true },
  { id: 'api', label: 'api', detail: '/src/clients/api' },
  { id: 'web', label: 'web', detail: '/src/clients/web' },
  { id: 'tool', label: 'tool', detail: '/src/tool', disabled: true, hint: 'Switched off.' },
]

let w: ReturnType<typeof setup> | null = null
afterEach(() => {
  w?.unmount()
  w = null
})

function setup() {
  return mount(SearchPicker, { props: { label: 'New terminal', searchLabel: 'Search projects', items, empty: 'No project matches.' }, attachTo: document.body })
}

const input = () => w!.get('input')
const options = () => w!.findAll('[role="option"]')
const active = () => w!.get(`#${input().attributes('aria-activedescendant')}`)

test('filterPicks keeps the items that are always shown and matches every word in label and detail', () => {
  expect(filterPicks(items, '').map((i) => i.id)).toEqual(['home', 'api', 'web', 'tool'])
  expect(filterPicks(items, 'CLIENTS').map((i) => i.id)).toEqual(['home', 'api', 'web'])
  expect(filterPicks(items, 'clients w').map((i) => i.id)).toEqual(['home', 'web'])
  expect(filterPicks(items, 'nothing').map((i) => i.id)).toEqual(['home'])
})

test('opening focuses the search; typing filters; arrows move; Enter chooses and closes with focus back on the button', async () => {
  w = setup()
  const button = w.get('button')
  expect(button.attributes('aria-expanded')).toBe('false')
  await button.trigger('click')
  await nextTick()
  expect(button.attributes('aria-expanded')).toBe('true')
  expect(document.activeElement).toBe(input().element)
  expect(input().attributes('role')).toBe('combobox')
  expect(input().attributes('aria-label')).toBe('Search projects')
  expect(options().map((o) => o.get('[data-pick-label]').text())).toEqual(['Home folder', 'api', 'web', 'tool'])
  expect(active().text()).toContain('Home folder')
  expect(active().attributes('aria-selected')).toBe('true')

  await input().setValue('clie')
  expect(options().map((o) => o.get('[data-pick-label]').text())).toEqual(['Home folder', 'api', 'web'])
  await input().trigger('keydown', { key: 'ArrowDown' })
  await input().trigger('keydown', { key: 'ArrowDown' })
  expect(active().text()).toContain('web')
  await input().trigger('keydown', { key: 'ArrowDown' })
  expect(active().text()).toContain('Home folder')
  await input().trigger('keydown', { key: 'ArrowUp' })
  expect(active().text()).toContain('web')
  await input().trigger('keydown', { key: 'Enter' })
  expect(w.emitted('select')).toEqual([['web']])
  expect(button.attributes('aria-expanded')).toBe('false')
  expect(document.activeElement).toBe(button.element)
})

test('Escape closes without choosing; reopening starts with an empty search', async () => {
  w = setup()
  await w.get('button').trigger('click')
  await input().setValue('api')
  await input().trigger('keydown', { key: 'Escape' })
  expect(w.emitted('select')).toBeUndefined()
  expect(w.get('button').attributes('aria-expanded')).toBe('false')
  expect(document.activeElement).toBe(w.get('button').element)
  await w.get('button').trigger('click')
  expect((input().element as HTMLInputElement).value).toBe('')
})

test('a disabled item can be reached and read but not chosen; a click chooses an enabled one', async () => {
  w = setup()
  await w.get('button').trigger('click')
  await input().setValue('tool')
  await input().trigger('keydown', { key: 'ArrowDown' })
  expect(active().attributes('aria-disabled')).toBe('true')
  expect(w.get(`#${active().attributes('aria-describedby')}`).text()).toBe('Switched off.')
  await input().trigger('keydown', { key: 'Enter' })
  expect(w.emitted('select')).toBeUndefined()
  await options()[1]!.trigger('click')
  expect(w.emitted('select')).toBeUndefined()
  await options()[0]!.trigger('click')
  expect(w.emitted('select')).toEqual([['home']])
})

test('nothing but the fixed items: the empty text says so', async () => {
  w = mount(SearchPicker, { props: { label: 'New terminal', searchLabel: 'Search projects', items: items.slice(1), empty: 'No project matches.' }, attachTo: document.body })
  await w.get('button').trigger('click')
  await input().setValue('zzz')
  expect(options()).toHaveLength(0)
  expect(w.get('[data-pick-empty]').text()).toBe('No project matches.')
  expect(input().attributes('aria-activedescendant')).toBeUndefined()
  await input().trigger('keydown', { key: 'Enter' })
  expect(w.emitted('select')).toBeUndefined()
})

test('a shortened detail is shown, the whole one is searched and kept as the tooltip', async () => {
  const long: PickItem[] = [{ id: 'deep', label: 'deep', detail: '/very/long/path/to/deep', detailShown: '/very/…/deep' }]
  w = mount(SearchPicker, { props: { label: 'New terminal', searchLabel: 'Search projects', items: long }, attachTo: document.body })
  await w.get('button').trigger('click')
  await input().setValue('path/to')
  expect(options()).toHaveLength(1)
  expect(options()[0]!.get('.pick__detail').text()).toBe('/very/…/deep')
  expect(options()[0]!.get('.pick__detail').attributes('title')).toBe('/very/long/path/to/deep')
})

test('the count says how many items match, leaving out the ones always shown', async () => {
  w = mount(SearchPicker, { props: { label: 'New terminal', searchLabel: 'Search projects', items, countText: (n: number, all: number) => `${n} of ${all}` }, attachTo: document.body })
  await w.get('button').trigger('click')
  expect(w.get('[data-pick-count]').text()).toBe('3 of 3')
  expect(w.get('[data-pick-count]').attributes('role')).toBe('status')
  await input().setValue('clients')
  expect(w.get('[data-pick-count]').text()).toBe('2 of 3')
})
