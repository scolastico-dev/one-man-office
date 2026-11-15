import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import type { SettingsPatch } from '../../../api/types'
import { harness, mountWith, ok, refuse } from '../../create/dialogTesting'
import ProjectsPage from './ProjectsPage.vue'
import { answerReads, settingsWith } from './projectsTesting'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
})

const button = (w: VueWrapper, text: string) => w.findAll('button').find((b) => b.text() === text)!
const roots = (w: VueWrapper) => w.findAll('[data-root]').map((r) => (r.get('input[data-root-path]').element as HTMLInputElement).value)

test('adding opens the picker; a new folder starts with the default ignore patterns; Save sends only workspace_roots with the revision', async () => {
  const h = await harness()
  const reads = answerReads(h, {
    settings: settingsWith([{ path: '~/workspace', max_depth: 3, ignore: ['archive/*'] }], 'rev-1'),
    folders: { '': { path: '/home/you', parent: '/home', folders: [{ name: 'srv', path: '/home/you/srv', repository: false, hidden: false, link: false }] } },
  })
  const w = await mountWith(h, ProjectsPage, {})
  mounted = w
  expect(roots(w)).toEqual(['~/workspace'])

  await w.get('[data-root-add]').trigger('click')
  await flushPromises()
  expect(reads.asked).toContain('/api/folders')
  await w.get('[data-folder="srv"]').trigger('click')
  await flushPromises()
  await w.get('[data-picker-choose]').trigger('click')
  await flushPromises()
  expect(roots(w)).toEqual(['~/workspace', '/home/you/srv'])
  const added = w.get('[data-root="1"]')
  expect(added.findAll('[data-part="input"]').map((i) => (i.element as HTMLInputElement).value)).toEqual(['node_modules', '.*'])
  expect((added.get('input[data-root-depth]').element as HTMLInputElement).value).toBe('4')

  h.respond.write = ({ body }) => ok(settingsWith((body as SettingsPatch).workspace_roots as never, 'rev-2'))
  await button(w, 'Save').trigger('click')
  await flushPromises()
  expect(h.sent).toEqual([
    {
      method: 'PATCH',
      path: '/api/settings',
      body: {
        workspace_roots: [
          { path: '~/workspace', max_depth: 3, ignore: ['archive/*'] },
          { path: '/home/you/srv', max_depth: 4, ignore: ['node_modules', '.*'] },
        ],
      },
    },
  ])
  expect(w.get('[data-save-state]').text()).toContain('Saved')
})

test('closing the picker adds nothing; Browse… replaces the path of its row; Remove drops the row', async () => {
  const h = await harness()
  answerReads(h, {
    settings: settingsWith([
      { path: '/a', max_depth: 4, ignore: [] },
      { path: '/b', max_depth: 4, ignore: [] },
    ]),
    folders: { '/a': { path: '/a', parent: '/', folders: [] } },
  })
  const w = await mountWith(h, ProjectsPage, {})
  mounted = w
  await w.get('[data-root-add]').trigger('click')
  await flushPromises()
  await button(w, 'Cancel').trigger('click')
  await flushPromises()
  expect(roots(w)).toEqual(['/a', '/b'])

  await w.get('[data-root="0"] [data-root-browse]').trigger('click')
  await flushPromises()
  expect(w.get('input[name="folder"]').element).toHaveProperty('value', '/a')
  await w.get('input[name="folder"]').setValue('/c')
  await w.get('form.picker__path').trigger('submit')
  await flushPromises()
  await w.get('[data-picker-choose]').trigger('click')
  await flushPromises()
  expect(roots(w)).toEqual(['/c', '/b'])

  await w.get('[data-root="1"] [data-root-remove]').trigger('click')
  expect(roots(w)).toEqual(['/c'])
})

test('a refused folder shows the server message under the field the server names, and focus moves there', async () => {
  const h = await harness()
  answerReads(h, { settings: settingsWith([{ path: '/a', max_depth: 4, ignore: ['x'] }]) })
  const w = await mountWith(h, ProjectsPage, {})
  mounted = w
  await w.get('[data-root="0"] input[data-root-path]').setValue('relative/path')
  h.respond.write = () => refuse('invalid_settings', 'workspace_roots.0.path must be absolute or start with ~', { field: 'workspace_roots.0.path' })
  await button(w, 'Save').trigger('click')
  await flushPromises()
  const field = w.get('[data-root="0"] input[data-root-path]')
  expect(field.attributes('aria-invalid')).toBe('true')
  expect(w.get('[data-root="0"] .omo-field__error').text()).toBe('workspace_roots.0.path must be absolute or start with ~')
  expect(document.activeElement).toBe(field.element)

  // A refused ignore pattern is shown on its row.
  h.respond.write = () => refuse('invalid_settings', 'workspace_roots.0.ignore.0 must be a folder name', { field: 'workspace_roots.0.ignore.0' })
  await w.get('[data-root="0"] input[data-root-path]').setValue('/b')
  await button(w, 'Save').trigger('click')
  await flushPromises()
  expect(w.get('[data-root="0"] [data-row="0"] input').attributes('aria-invalid')).toBe('true')
  expect(w.get('[data-root="0"] [data-row="0"] .omo-field__error').text()).toBe('workspace_roots.0.ignore.0 must be a folder name')
})

test('a hand edit of the settings while the page shows no draft replaces the folders at once', async () => {
  const h = await harness()
  const reads = answerReads(h, { settings: settingsWith([{ path: '/a', max_depth: 4, ignore: [] }]) })
  const w = await mountWith(h, ProjectsPage, {})
  mounted = w
  reads.settings = settingsWith([{ path: '/a', max_depth: 4, ignore: [] }, { path: '/disk', max_depth: 2, ignore: [] }], 'r2')
  h.omo.emit('settings.changed', { section: 'general', name: 'settings', source: 'disk' })
  await flushPromises()
  expect(roots(w)).toEqual(['/a', '/disk'])
  expect(w.find('[data-changed-on-disk]').exists()).toBe(true)
})

test('a refusal of a folder by its number does not move to another folder when one is removed', async () => {
  const h = await harness()
  answerReads(h, {
    settings: settingsWith([
      { path: '/one', max_depth: 3, ignore: ['a', 'b'] },
      { path: '/two', max_depth: 3, ignore: [] },
      { path: '/three', max_depth: 3, ignore: [] },
    ]),
  })
  const w = await mountWith(h, ProjectsPage, {})
  mounted = w
  await w.get('[data-root="1"] input[data-root-path]').setValue('/two/x')
  h.respond.write = () => refuse('invalid_settings', 'workspace_roots.1.path is not a folder', { field: 'workspace_roots.1.path' })
  await button(w, 'Save').trigger('click')
  await flushPromises()
  expect(w.get('[data-root="1"]').text()).toContain('workspace_roots.1.path is not a folder')
  await w.get('[data-root="0"] [data-root-remove]').trigger('click')
  await flushPromises()
  expect(roots(w)).toEqual(['/two/x', '/three'])
  expect(w.text()).not.toContain('is not a folder')
  expect(w.find('[aria-invalid="true"]').exists()).toBe(false)
})

test('a refusal of an ignore pattern by its number does not move to another pattern when one is removed', async () => {
  const h = await harness()
  answerReads(h, { settings: settingsWith([{ path: '/one', max_depth: 3, ignore: ['a', 'b/', 'c'] }]) })
  const w = await mountWith(h, ProjectsPage, {})
  mounted = w
  await w.get('[data-root="0"] input[data-root-path]').setValue('/one/x')
  h.respond.write = () => refuse('invalid_settings', 'bad pattern', { field: 'workspace_roots.0.ignore.1' })
  await button(w, 'Save').trigger('click')
  await flushPromises()
  expect(w.get('[data-root="0"] [data-row="1"]').text()).toContain('bad pattern')
  await w.get('[data-root="0"] [data-row="0"]').findAll('button').at(-1)!.trigger('click')
  await flushPromises()
  expect(w.get('[data-root="0"]').findAll('[data-part="input"]').map((i) => (i.element as HTMLInputElement).value)).toEqual(['b/', 'c'])
  expect(w.text()).not.toContain('bad pattern')
})
