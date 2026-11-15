import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test, vi } from 'vitest'
import { project } from '../../testing/fakeOmo'
import { harness, mountWith, ok, refuse } from '../create/dialogTesting'
import AddProjectDialog from './AddProjectDialog.vue'
import ProjectsPage from '../settings/projects/ProjectsPage.vue'
import { answerReads } from '../settings/projects/projectsTesting'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
})

test('a project is added by its path; the dialog reports it and closes', async () => {
  const h = await harness()
  h.respond.write = ({ body }) => ok({ ...project('app', 'app'), path: (body as { path: string }).path })
  const w = await mountWith(h, AddProjectDialog, { open: true })
  mounted = w
  await w.get('input[name="path"]').setValue('  /work/app  ')
  await w.get('input[name="name"]').setValue('My app')
  await w.get('form').trigger('submit')
  await flushPromises()
  expect(h.sent).toEqual([{ method: 'POST', path: '/api/projects', body: { path: '/work/app', name: 'My app' } }])
  const dialog = w.findComponent(AddProjectDialog)
  expect(dialog.emitted('added')?.[0]?.[0]).toMatchObject({ id: 'app' })
  expect(dialog.emitted('close')).toHaveLength(1)
})

test('the server\'s answer for a folder that is not a repository is shown under the field and the text stays', async () => {
  const h = await harness()
  h.respond.write = () => refuse('validation', 'path /work/plain is not a git repository (no .git)')
  const w = await mountWith(h, AddProjectDialog, { open: true })
  mounted = w
  await w.get('input[name="path"]').setValue('/work/plain')
  await w.get('form').trigger('submit')
  await flushPromises()
  expect(w.get('.omo-field__error').text()).toBe('path /work/plain is not a git repository (no .git)')
  expect((w.get('input[name="path"]').element as HTMLInputElement).value).toBe('/work/plain')
  expect(w.findComponent(AddProjectDialog).emitted('close')).toBeUndefined()
  expect(w.get('button[type="submit"]').attributes('disabled')).toBeUndefined()
})

test('an empty path is not sent, and a double submit sends once', async () => {
  const h = await harness()
  let finish!: () => void
  h.respond.write = () => new Promise((resolve) => (finish = () => resolve(ok(project('a')))))
  const w = await mountWith(h, AddProjectDialog, { open: true })
  mounted = w
  await w.get('form').trigger('submit')
  expect(h.sent).toHaveLength(0)
  expect(w.get('.omo-field__error').text()).toBe('Enter the path of the folder.')
  await w.get('input[name="path"]').setValue('/x')
  await w.get('form').trigger('submit')
  await w.get('form').trigger('submit')
  expect(h.sent).toHaveLength(1)
  finish()
  await flushPromises()
})

test('the project list marks disabled and missing projects', async () => {
  const h = await harness((omo) => {
    omo.projects = [project('a', 'alpha'), { ...project('b', 'beta'), enabled: false }, { ...project('c', 'gamma'), missing: true }]
  })
  const w = await mountWith(h, ProjectsPage, {})
  mounted = w
  const states = w.findAll('tbody tr').map((r) => [r.get('th').text(), r.get('.projects__state').text()])
  expect(states).toEqual([
    ['alpha', 'enabled'],
    ['beta', 'disabled'],
    ['gamma', 'missing'],
  ])
})

test('the project list shows the current branch, with a notice when it is not the default', async () => {
  const h = await harness((omo) => {
    omo.projects = [
      { ...project('a', 'alpha'), current_branch: 'feature-x', default_branch: 'main' },
      { ...project('b', 'beta'), current_branch: 'main', default_branch: 'main' },
      project('c', 'gamma'),
    ]
  })
  const w = await mountWith(h, ProjectsPage, {})
  mounted = w
  const cells = w.findAll('tbody tr').map((r) => r.get('.projects__branch').text().replace(/\s+/g, ' '))
  expect(cells).toEqual(['feature-x Not its default branch main.', 'main', ''])
})

test('Browse… opens the folder picker and puts the chosen folder into the field', async () => {
  const h = await harness()
  const reads = answerReads(h, {
    folders: {
      '/work': { path: '/work', parent: '/', folders: [{ name: 'app', path: '/work/app', repository: true, hidden: false, link: false }] },
      '/work/app': { path: '/work/app', parent: '/work', folders: [] },
    },
  })
  const w = await mountWith(h, AddProjectDialog, { open: true })
  mounted = w
  await w.get('input[name="path"]').setValue('/work')
  await w.get('[data-add-project-browse]').trigger('click')
  await vi.dynamicImportSettled()
  await flushPromises()
  expect(reads.asked).toEqual(['/api/folders'])
  expect(w.get('[data-folder="app"]').text()).toContain('git repository')
  await w.get('[data-folder="app"]').trigger('click')
  await flushPromises()
  await w.get('[data-picker-choose]').trigger('click')
  await flushPromises()
  expect(w.find('[data-folder-picker]').exists()).toBe(false)
  expect((w.get('input[name="path"]').element as HTMLInputElement).value).toBe('/work/app')
  expect(h.sent).toEqual([])
})
