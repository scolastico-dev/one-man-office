import { mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import { nextTick } from 'vue'
import type { DiffFile } from '../../api/types'
import DiffTree from './DiffTree.vue'
import { buildFileTree } from './fileTree'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
})

const file = (path: string, more: Partial<DiffFile> = {}): DiffFile => ({
  path,
  old_path: '',
  status: 'modified',
  binary: false,
  additions: 3,
  deletions: 1,
  patch: '',
  truncated: false,
  ...more,
})

function tree(selected: string | null = null) {
  const nodes = buildFileTree(
    [
      { key: 'a', file: file('src/a.ts', { status: 'added' }), project: 'p', uncommitted: false },
      { key: 'b', file: file('src/b.ts'), project: 'p', uncommitted: false },
      { key: 'c', file: file('c\u202e.txt', { status: 'renamed', old_path: 'old.txt' }), project: 'p', uncommitted: false },
    ],
    'p',
  )
  const w = mount(DiffTree, { props: { nodes, selected, label: 'Changed files' }, attachTo: document.body })
  mounted = w
  return w
}

const rows = (w: VueWrapper) => w.findAll('[role="treeitem"]')
const focused = () => (document.activeElement as HTMLElement | null)?.textContent?.replace(/\s+/g, ' ').trim()

test('a tree of rows with level, place and state; the shown file is selected and holds the tab stop; names through visibleText', () => {
  const w = tree('b')
  expect(w.get('[role="tree"]').attributes('aria-label')).toBe('Changed files')
  const r = rows(w)
  expect(r.map((x) => [x.attributes('aria-level'), x.attributes('aria-posinset'), x.attributes('aria-setsize')])).toEqual([
    ['1', '1', '2'],
    ['2', '1', '2'],
    ['2', '2', '2'],
    ['1', '2', '2'],
  ])
  expect(r[0]!.attributes('aria-expanded')).toBe('true')
  expect(r[0]!.attributes('aria-selected')).toBeUndefined()
  expect(r[2]!.attributes('aria-selected')).toBe('true')
  expect(r[1]!.attributes('aria-selected')).toBe('false')
  expect(r.map((x) => x.attributes('tabindex'))).toEqual(['-1', '-1', '0', '-1'])
  expect(r[1]!.text()).toContain('added')
  expect(r[1]!.text()).toContain('+3 −1')
  expect(r[3]!.get('[data-tree-name]').text()).toBe('c\\u{202E}.txt')
  expect(r[3]!.attributes('title')).toBe('c\\u{202E}.txt, renamed from old.txt')
  expect(w.text()).not.toContain('\u202e')
})

test('arrow keys move the focus, Left closes the folder and Right opens it, Enter and a click open a file', async () => {
  const w = tree()
  const r = () => rows(w)
  ;(r()[0]!.element as HTMLElement).focus()
  await r()[0]!.trigger('keydown', { key: 'ArrowDown' })
  await nextTick()
  expect(focused()).toContain('a.ts')
  await r()[1]!.trigger('keydown', { key: 'End' })
  await nextTick()
  expect(focused()).toContain('.txt')
  await r()[3]!.trigger('keydown', { key: 'Enter' })
  expect(w.emitted('open')).toEqual([['c']])
  await r()[3]!.trigger('keydown', { key: 'Home' })
  await nextTick()
  await r()[0]!.trigger('keydown', { key: 'ArrowLeft' })
  await nextTick()
  expect(r()).toHaveLength(2)
  expect(r()[0]!.attributes('aria-expanded')).toBe('false')
  await r()[0]!.trigger('keydown', { key: 'ArrowRight' })
  await nextTick()
  expect(r()).toHaveLength(4)
  await r()[2]!.trigger('click')
  expect(w.emitted('open')).toEqual([['c'], ['b']])
  // A click on a folder opens or closes it.
  await r()[0]!.trigger('click')
  expect(r()).toHaveLength(2)
})

test('while filtering every folder shows open', async () => {
  const w = tree()
  await rows(w)[0]!.trigger('keydown', { key: 'ArrowLeft' })
  expect(rows(w)).toHaveLength(2)
  await w.setProps({ filtering: true })
  expect(rows(w)).toHaveLength(4)
})
