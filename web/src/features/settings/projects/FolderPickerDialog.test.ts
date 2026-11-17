import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import { ApiError, type Query } from '../../../api/client'
import type { FolderEntry, FolderListing } from '../../../api/types'
import { harness, mountWith, ok, type Harness } from '../../create/dialogTesting'
import FolderPickerDialog from './FolderPickerDialog.vue'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
})

const entry = (path: string, o: Partial<FolderEntry> = {}): FolderEntry => ({
  name: path.slice(path.lastIndexOf('/') + 1),
  path,
  repository: false,
  hidden: false,
  link: false,
  ...o,
})

/** Answers GET /api/folders from `tree` (path → listing); records the paths asked for. */
function folders(h: Harness, tree: Record<string, Partial<FolderListing> | ApiError>): (string | undefined)[] {
  const asked: (string | undefined)[] = []
  const base = h.session.client.get
  h.session.client.get = (async (path: string, query?: Query) => {
    if (path !== '/api/folders') return base(path, query)
    const p = query?.path as string | undefined
    asked.push(p)
    const at = tree[p ?? '']
    if (at === undefined) throw new ApiError(404, 'not_found', `${p}: no such file or directory`)
    if (at instanceof ApiError) throw at
    return ok({ path: p ?? '/home/you', parent: '', home: '/home/you', folders: [], more: false, ...at })
  }) as typeof base
  return asked
}

const names = (w: VueWrapper) => w.findAll('[data-folder]').map((b) => b.find('.picker__name').text())

test('lists the folders of the start folder, marks repositories and links, and hides hidden folders until asked', async () => {
  const h = await harness()
  const asked = folders(h, {
    '/w': {
      path: '/w',
      parent: '/',
      folders: [entry('/w/app', { repository: true }), entry('/w/docs', { link: true }), entry('/w/.cache', { hidden: true })],
    },
  })
  const w = await mountWith(h, FolderPickerDialog, { open: true, title: 'Choose a workspace folder', start: '/w' })
  mounted = w
  expect(asked).toEqual(['/w'])
  expect(names(w)).toEqual(['app', 'docs'])
  expect(w.get('[data-folder="app"]').text()).toContain('git repository')
  expect(w.get('[data-folder="docs"]').text()).toContain('link')
  expect(w.get('[data-picker-where]').text()).toBe('2 folders in /w')
  const hidden = w.get('[data-picker-hidden]')
  expect(hidden.text()).toContain('Show hidden folders (1)')
  await hidden.trigger('click')
  expect(names(w)).toEqual(['app', 'docs', '.cache'])
})

test('opens a folder, goes up, and loads a typed path on Enter', async () => {
  const h = await harness()
  const asked = folders(h, {
    '': { path: '/home/you', parent: '/home', folders: [entry('/home/you/code')] },
    '/home/you': { path: '/home/you', parent: '/home', folders: [entry('/home/you/code')] },
    '/home/you/code': { path: '/home/you/code', parent: '/home/you', folders: [] },
    '/home': { path: '/home', parent: '/', folders: [entry('/home/you')] },
    '/srv': { path: '/srv', parent: '/', folders: [entry('/srv/git')] },
  })
  const w = await mountWith(h, FolderPickerDialog, { open: true, title: 'Pick' })
  mounted = w
  await w.get('[data-folder="code"]').trigger('click')
  await flushPromises()
  expect(w.text()).toContain('No folders here.')
  expect((w.get('input[name="folder"]').element as HTMLInputElement).value).toBe('/home/you/code')
  await w.get('[data-picker-up]').trigger('click')
  await flushPromises()
  await w.get('[data-picker-up]').trigger('click')
  await flushPromises()
  expect(names(w)).toEqual(['you'])
  await w.get('input[name="folder"]').setValue('  /srv ')
  await w.get('form').trigger('submit')
  await flushPromises()
  expect(asked).toEqual([undefined, '/home/you/code', '/home/you', '/home', '/srv'])
  expect(names(w)).toEqual(['git'])
})

test('names arrive escaped and paths exact: both are shown as visible text, and the exact path is chosen', async () => {
  const h = await harness()
  const odd = '/w/evil‮txt.exe'
  folders(h, {
    '/w': { path: '/w', parent: '/', folders: [entry(odd, { name: 'evil\\u{202E}txt.exe' })] },
    [odd]: { path: odd, parent: '/w', folders: [] },
  })
  const w = await mountWith(h, FolderPickerDialog, { open: true, title: 'Pick', start: '/w' })
  mounted = w
  expect(names(w)).toEqual(['evil\\u{202E}txt.exe'])
  await w.get('[data-folder]').trigger('click')
  await flushPromises()
  expect(w.get('[data-picker-where]').text()).toBe('0 folders in /w/evil\\u{202E}txt.exe')
  expect(w.html()).not.toContain('‮')
  await w.get('[data-picker-choose]').trigger('click')
  expect(w.findComponent(FolderPickerDialog).emitted('choose')).toEqual([[odd]])
})

test('refusals are shown under the path in clear words, and the last listing stays', async () => {
  const h = await harness()
  folders(h, {
    '/w': { path: '/w', parent: '/', folders: [entry('/w/app')] },
    relative: new ApiError(422, 'validation', 'the path must be absolute'),
    '/slow': new ApiError(503, 'unavailable', 'The folder did not answer in time.'),
  })
  const w = await mountWith(h, FolderPickerDialog, { open: true, title: 'Pick', start: '/w' })
  mounted = w
  const error = () => w.get('.omo-field__error').text()
  for (const [typed, words] of [
    ['/gone', 'There is no folder /gone.'],
    ['relative', 'the path must be absolute'],
    ['/slow', 'The folder did not answer in time. Try again, or open another folder.'],
  ] as const) {
    await w.get('input[name="folder"]').setValue(typed)
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(error()).toBe(words)
    expect(w.get('input[name="folder"]').attributes('aria-invalid')).toBe('true')
    expect(names(w)).toEqual(['app'])
  }
  // Choosing still means the folder shown.
  await w.get('[data-picker-choose]').trigger('click')
  expect(w.findComponent(FolderPickerDialog).emitted('choose')).toEqual([['/w']])
})

test('an older answer never replaces a newer one', async () => {
  const h = await harness()
  const answers: Record<string, (v: unknown) => void> = {}
  h.session.client.get = ((_path: string, query?: Query) =>
    new Promise<unknown>((resolve) => {
      answers[String(query?.path ?? '')] = (v) => resolve(ok(v))
    })) as typeof h.session.client.get
  const w = await mountWith(h, FolderPickerDialog, { open: true, title: 'Pick', start: '/a' })
  mounted = w
  await w.get('input[name="folder"]').setValue('/b')
  await w.get('form').trigger('submit')
  answers['/b']!({ path: '/b', parent: '/', home: '/h', folders: [entry('/b/new')], more: false })
  await flushPromises()
  answers['/a']!({ path: '/a', parent: '/', home: '/h', folders: [entry('/a/old')], more: false })
  await flushPromises()
  expect(names(w)).toEqual(['new'])
})
