import { expect, test } from 'vitest'
import { MemoryStorage } from '../../testing/fakeOmo'
import { createSidebar, DEFAULT_WIDTH, MIN_WIDTH, SIDEBAR_KEY } from './useSidebar'

test('sidebar state survives a reload and broken storage', () => {
  const storage = new MemoryStorage()
  const a = createSidebar(() => storage)
  expect(a.open.value).toBe(false)
  expect(a.width.value).toBe(DEFAULT_WIDTH)
  expect(a.tab.value).toBe('agents')
  a.show()
  a.width.value = 400
  a.setTab('terminals', 'user')

  // A new page reads what the first one left.
  const b = createSidebar(() => storage)
  expect(b.open.value).toBe(true)
  expect(b.width.value).toBe(400)
  expect(b.tab.value).toBe('terminals')

  // Storage that throws, on read and on write, changes nothing for the page.
  const broken = (): Storage => {
    throw new Error('blocked')
  }
  const c = createSidebar(broken)
  c.show()
  c.width.value = 500
  c.setTab('agents', 'user')
  expect(c.open.value).toBe(true)
  expect(c.width.value).toBe(500)
  const refusing = {
    getItem: () => {
      throw new Error('no')
    },
    setItem: () => {
      throw new Error('full')
    },
  } as unknown as Storage
  const d = createSidebar(() => refusing)
  d.toggle()
  expect(d.open.value).toBe(true)
  // No storage at all.
  expect(createSidebar(() => null).tab.value).toBe('agents')
})

test('a plugin sidebar tab is remembered across reloads', () => {
  const storage = new MemoryStorage()
  createSidebar(() => storage).setTab('plugin:vscode:editor', 'user')
  expect(createSidebar(() => storage).tab.value).toBe('plugin:vscode:editor')
})

test('stored values that make no sense are replaced by the defaults', () => {
  const storage = new MemoryStorage()
  storage.setItem(SIDEBAR_KEY, JSON.stringify({ open: 'yes', width: 12, tab: '<b>' }))
  const s = createSidebar(() => storage)
  expect(s.open.value).toBe(false)
  expect(s.width.value).toBe(DEFAULT_WIDTH)
  expect(s.tab.value).toBe('agents')
  storage.setItem(SIDEBAR_KEY, 'not json')
  expect(createSidebar(() => storage).width.value).toBe(DEFAULT_WIDTH)
  storage.setItem(SIDEBAR_KEY, JSON.stringify({ width: MIN_WIDTH, open: true, tab: 'ceo' }))
  const ok = createSidebar(() => storage)
  expect([ok.width.value, ok.open.value, ok.tab.value]).toEqual([MIN_WIDTH, true, 'ceo'])
})

test('a restored tab is reported as restored, a click as user', () => {
  const storage = new MemoryStorage()
  storage.setItem(SIDEBAR_KEY, JSON.stringify({ open: true, width: 480, tab: 'ceo' }))
  const s = createSidebar(() => storage)
  expect(s.tab.value).toBe('ceo')
  expect(s.how.value).toBe('restored')
  s.setTab('agents', 'user')
  expect(s.how.value).toBe('user')
  s.setTab('ceo', 'user')
  expect(s.how.value).toBe('user')
  // Storing a tab the user did not choose is not done: restoring changes nothing in storage.
  const fresh = new MemoryStorage()
  createSidebar(() => fresh).setTab('ceo', 'restored')
  expect(fresh.getItem(SIDEBAR_KEY)).toBeNull()
})

test('openOn shows the sidebar on a tab with a selection and asks for the focus', () => {
  const s = createSidebar(() => new MemoryStorage())
  s.openOn('terminals', 'abc')
  expect([s.open.value, s.tab.value, s.selection.value, s.how.value, s.focusRequests.value]).toEqual([true, 'terminals', 'abc', 'user', 1])
  // Another tab drops the selection of the first.
  s.setTab('agents', 'user')
  expect(s.selection.value).toBeNull()
  // On a narrow window the sheet is shown, and the column stays as it was.
  s.hide()
  s.setNarrow(true)
  s.openOn('agents', 's9')
  expect([s.sheet.value, s.open.value, s.shown.value]).toEqual([true, false, true])
  s.setNarrow(false)
  expect(s.sheet.value).toBe(false)
})
