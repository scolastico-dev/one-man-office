import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import { project } from '../../../testing/fakeOmo'
import { harness, mountWith, ok, refuse } from '../../create/dialogTesting'
import ProjectsPage from './ProjectsPage.vue'
import { answerReads, settingsWith } from './projectsTesting'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
})

const ROOT_REFUSAL = '"a/one" lies under the workspace root /w and would be found again at the next scan. Switch it off, or add an ignore pattern to the root.'

async function page(projects = [project('a', 'alpha')], roots = settingsWith([])) {
  const h = await harness((omo) => {
    omo.projects = projects
  })
  answerReads(h, { settings: roots })
  const w = await mountWith(h, ProjectsPage, {})
  mounted = w
  return { h, w }
}

const row = (w: VueWrapper, id: string) => w.get(`tr[data-project="${id}"]`)
const nameInput = (w: VueWrapper) => w.get('input[data-project-rename]')

test('rename saves on Enter; Escape puts the stored name back', async () => {
  const { h, w } = await page()
  await row(w, 'a').get('[data-project-edit]').trigger('click')
  expect(row(w, 'a').get('[data-project-edit]').attributes('aria-expanded')).toBe('true')
  await nameInput(w).setValue('typo')
  await nameInput(w).trigger('keydown', { key: 'Escape' })
  expect((nameInput(w).element as HTMLInputElement).value).toBe('alpha')

  h.respond.write = () => ok({ ...project('a', 'Alpha app') })
  await nameInput(w).setValue('  Alpha app ')
  await nameInput(w).trigger('keydown', { key: 'Enter' })
  await flushPromises()
  expect(h.sent).toEqual([{ method: 'PATCH', path: '/api/projects/a', body: { name: 'Alpha app' } }])
  // The list shows what the stream brings.
  h.omo.emit('project.changed', { project: project('a', 'Alpha app') })
  await flushPromises()
  expect(row(w, 'a').get('th').text()).toBe('Alpha app')
})

test('a refused name is restored and the server says why in the row', async () => {
  const { h, w } = await page()
  await row(w, 'a').get('[data-project-edit]').trigger('click')
  h.respond.write = () => refuse('validation', 'name must not hold control, invisible or direction characters', { field: 'name' })
  await nameInput(w).setValue('bad‮name')
  await nameInput(w).trigger('keydown', { key: 'Enter' })
  await flushPromises()
  expect((nameInput(w).element as HTMLInputElement).value).toBe('alpha')
  expect(w.get('[data-project-error]').text()).toBe('name must not hold control, invisible or direction characters')
})

test('the switch saves at once; a refusal puts it back', async () => {
  const { h, w } = await page()
  h.respond.write = () => refuse('validation', 'nope')
  await row(w, 'a').get('[data-project-enabled]').trigger('click')
  await flushPromises()
  expect(h.sent).toEqual([{ method: 'PATCH', path: '/api/projects/a', body: { enabled: false } }])
  expect(row(w, 'a').get('[data-project-enabled]').attributes('aria-checked')).toBe('true')
  expect(w.get('[data-project-error]').text()).toBe('nope')
})

const mergeSelect = (w: VueWrapper, kind: string) => w.get(`select[data-project-merge="${kind}"]`)
const optionTexts = (w: VueWrapper, kind: string) => mergeSelect(w, kind).findAll('option').map((o) => o.text())

test('each kind of merge has its own select, starting on the global default in words', async () => {
  const { w } = await page()
  await row(w, 'a').get('[data-project-edit]').trigger('click')
  const labels = ['story', 'task', 'story_task'].map((k) => w.get(`label[for="${mergeSelect(w, k).attributes('id')}"]`).text())
  expect(labels).toEqual(['When a story is merged', 'When a task without a story is merged', 'When a task is merged into its story'])
  for (const k of ['story', 'task', 'story_task']) {
    expect(optionTexts(w, k)).toEqual(['Use global default (Merge commit)', 'Merge commit', 'Squash into one commit'])
    expect((mergeSelect(w, k).element as HTMLSelectElement).value).toBe('')
  }
  expect(row(w, 'a').find('[data-project-merge-override]').exists()).toBe(false)
})

test('a merge mode is saved per kind; the global default is saved as empty', async () => {
  const { h, w } = await page()
  h.respond.write = () => ok(project('a', 'alpha'))
  await row(w, 'a').get('[data-project-edit]').trigger('click')
  await mergeSelect(w, 'task').setValue('squash')
  await flushPromises()
  await mergeSelect(w, 'story_task').setValue('merge')
  await flushPromises()
  await mergeSelect(w, 'story').setValue('')
  await flushPromises()
  expect(h.sent).toEqual([
    { method: 'PATCH', path: '/api/projects/a', body: { merge: { task: 'squash' } } },
    { method: 'PATCH', path: '/api/projects/a', body: { merge: { story_task: 'merge' } } },
  ])
})

test('an override is marked in the row and its own value shown; the default names the general value', async () => {
  const own = { ...project('a', 'alpha'), merge: { story: 'squash', task: '', story_task: '' }, effective_merge: { story: 'squash', task: 'merge', story_task: 'merge' } } as const
  const { w } = await page([{ ...own, merge: { ...own.merge }, effective_merge: { ...own.effective_merge } }])
  expect(row(w, 'a').get('[data-project-merge-override]').text()).toBe('merge: overridden')
  await row(w, 'a').get('[data-project-edit]').trigger('click')
  expect((mergeSelect(w, 'story').element as HTMLSelectElement).value).toBe('squash')
  expect(optionTexts(w, 'story')[0]).toBe('Use global default (Merge commit)')
})

test('a refused merge mode shows the reason under its select', async () => {
  const { h, w } = await page()
  await row(w, 'a').get('[data-project-edit]').trigger('click')
  h.respond.write = () => refuse('validation', 'merge.task must be merge or squash', { field: 'merge.task' })
  await mergeSelect(w, 'task').setValue('squash')
  await flushPromises()
  const describedBy = mergeSelect(w, 'task').attributes('aria-describedby')!
  expect(w.get(`#${describedBy.split(' ').at(-1)}`).text()).toBe('merge.task must be merge or squash')
  expect(mergeSelect(w, 'task').attributes('aria-invalid')).toBe('true')
  expect(mergeSelect(w, 'story').attributes('aria-invalid')).toBeUndefined()
  expect(w.find('[data-project-error]').exists()).toBe(false)
  // The select shows the stored value again.
  expect((mergeSelect(w, 'task').element as HTMLSelectElement).value).toBe('')
})

test('after a change of the general defaults the words of "Use global default" follow', async () => {
  const { h, w } = await page()
  await row(w, 'a').get('[data-project-edit]').trigger('click')
  expect(optionTexts(w, 'task')[0]).toBe('Use global default (Merge commit)')
  // The server now answers the new defaults; only the settings event tells the page.
  h.omo.projects = [{ ...project('a', 'alpha'), effective_merge: { story: 'merge', task: 'squash', story_task: 'merge' } }]
  h.omo.emit('settings.changed', { section: 'general' })
  await flushPromises()
  expect(optionTexts(w, 'task')[0]).toBe('Use global default (Squash into one commit)')
})

test('missing rows say so, have no switch, and Delete all missing lists them before removing', async () => {
  const { h, w } = await page([project('a', 'alpha'), { ...project('g', 'gone'), missing: true }])
  expect(row(w, 'g').get('[data-project-missing]').text()).toBe('The folder is gone. New tickets cannot use it.')
  expect(row(w, 'g').find('[data-project-enabled]').exists()).toBe(false)
  expect(row(w, 'a').find('[data-project-missing]').exists()).toBe(false)

  await w.get('[data-remove-missing]').trigger('click')
  expect(w.get('[data-remove-missing-dialog]').text()).toContain('gone')
  expect(w.get('[data-remove-missing-dialog]').text()).not.toContain('alpha')
  h.respond.write = () => refuse('in_use', 'Open tickets use this project: #4.', undefined, 409)
  await w.get('[data-remove-missing-confirm]').trigger('click')
  await flushPromises()
  expect(h.sent).toEqual([{ method: 'DELETE', path: '/api/projects?missing=true', body: undefined }])
  expect(w.get('[data-remove-missing-error]').text()).toBe('Open tickets use this project: #4.')
})

test('a project found under a workspace folder cannot be removed: the row shows why and offers both ways out', async () => {
  const p = { ...project('a', 'a/one'), path: '/w/a/one', root: '/w' }
  const { h, w } = await page([p], settingsWith([{ path: '/w', max_depth: 4, ignore: ['node_modules'] }]))
  await row(w, 'a').get('[data-project-edit]').trigger('click')
  await w.get('[data-project-remove]').trigger('click')
  h.respond.write = () => refuse('validation', ROOT_REFUSAL)
  await w.get('[data-project-remove-confirm]').trigger('click')
  await flushPromises()
  expect(h.sent).toEqual([{ method: 'DELETE', path: '/api/projects/a', body: undefined }])
  expect(w.get('[data-project-error]').text()).toBe(ROOT_REFUSAL)

  // Ignore pattern: added to the folder's form (saved with the page's Save bar).
  await w.get('[data-project-ignore]').trigger('click')
  await flushPromises()
  const patterns = w.findAll('[data-root="0"] [data-part="input"]').map((i) => (i.element as HTMLInputElement).value)
  expect(patterns).toEqual(['node_modules', 'a/one'])
  expect(document.activeElement).toBe(w.findAll('[data-root="0"] [data-part="input"]')[1]!.element)

  // Switch it off.
  h.respond.write = () => ok({ ...p, enabled: false })
  await w.get('[data-project-switch-off]').trigger('click')
  await flushPromises()
  expect(h.sent.at(-1)).toEqual({ method: 'PATCH', path: '/api/projects/a', body: { enabled: false } })
})

test('a project added by hand says so; one found under a folder names it', async () => {
  const { w } = await page([project('a', 'alpha'), { ...project('b', 'beta'), root: '~/work' }])
  expect(row(w, 'a').get('[data-project-origin]').text()).toBe('added by hand')
  expect(row(w, 'b').get('[data-project-origin]').text()).toBe('found in ~/work')
})
