import { flushPromises, type DOMWrapper, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import type { Project, ProjectBatch } from '../../../api/types'
import { announcements } from '../../../components/ui/useAnnouncer'
import { project } from '../../../testing/fakeOmo'
import { harness, mountWith, ok, type Sent } from '../../create/dialogTesting'
import ProjectsPage from './ProjectsPage.vue'
import { answerReads, settingsWith } from './projectsTesting'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
  document.body.innerHTML = ''
})

const ROOT_REFUSAL = '"w/two" lies under the workspace root /w and would be found again at the next scan. Switch it off, or add an ignore pattern to the root.'

const at = (id: string, name: string, path: string, over: Partial<Project> = {}): Project => ({ ...project(id, name), path, ...over })
const PROJECTS = [
  at('one', 'w/one', '/w/one', { root: '/w' }),
  at('two', 'w/two', '/w/two', { root: '/w' }),
  at('api', 'api', '/src/clients/api'),
  at('web', 'web', '/src/clients/web'),
  at('tool', 'tool', '/src/tool'),
]

/** Answers the batch route like the server: per id, refusing the ids in `refuse`. */
function batchAnswer(refuse: Record<string, { code: string; message: string }> = {}) {
  return (s: Sent) => {
    const body = s.body as ProjectBatch
    return ok({ results: body.ids.map((id) => (refuse[id] ? { id, done: false, error: refuse[id] } : { id, done: true })) })
  }
}

async function page(projects = PROJECTS) {
  const h = await harness((omo) => {
    omo.projects = projects.map((p) => ({ ...p }))
  })
  answerReads(h, { settings: settingsWith([{ path: '/w', max_depth: 4, ignore: ['node_modules'] }]) })
  const w = await mountWith(h, ProjectsPage, {}, { teleport: false })
  await flushPromises()
  mounted = w
  return { h, w }
}

const names = (w: VueWrapper) => w.findAll('tr[data-project] [data-project-name]').map((t) => t.text())
const filter = (w: VueWrapper) => w.get('input[data-project-filter]')
const checkbox = (w: VueWrapper, id: string) => w.get(`tr[data-project="${id}"] input[data-project-select]`)
const checked = (w: VueWrapper) => w.findAll('input[data-project-select]').filter((c) => (c.element as HTMLInputElement).checked).map((c) => c.attributes('data-project-select'))
const button = (w: VueWrapper | DOMWrapper<Element>, text: string) => w.findAll('button').find((b) => b.text() === text)!
const count = (w: VueWrapper) => w.get('[data-selection-count]').text()

test('the filter narrows the list by name and folder and says when nothing matches', async () => {
  const { w } = await page()
  expect(names(w)).toEqual(['api', 'tool', 'w/one', 'w/two', 'web'])
  await filter(w).setValue('clients')
  expect(names(w)).toEqual(['api', 'web'])
  await filter(w).setValue('W/')
  expect(names(w)).toEqual(['w/one', 'w/two'])
  await filter(w).setValue('nothing-like-it')
  expect(names(w)).toEqual([])
  expect(w.get('[data-no-match]').text()).toBe('No project matches “nothing-like-it”.')
})

test('Select… shows a box per row; Select all takes the shown rows only; the count is announced; Escape leaves', async () => {
  const { w } = await page()
  expect(w.find('input[data-project-select]').exists()).toBe(false)
  await button(w, 'Select…').trigger('click')
  expect(w.findAll('input[data-project-select]')).toHaveLength(5)
  expect(count(w)).toBe('None selected')

  await filter(w).setValue('clients')
  await button(w, 'Select all 2 shown').trigger('click')
  await flushPromises()
  expect(checked(w)).toEqual(['api', 'web'])
  expect(count(w)).toBe('2 selected')
  expect(announcements.polite.value).toBe('2 selected')
  await filter(w).setValue('')
  expect(count(w)).toBe('2 selected')
  await filter(w).setValue('tool')
  expect(count(w)).toBe('2 selected, 2 not shown')

  await filter(w).setValue('')
  await w.get('[data-projects-list]').trigger('keydown', { key: 'Escape' })
  expect(w.find('input[data-project-select]').exists()).toBe(false)
  expect(w.find('[data-selection-count]').exists()).toBe(false)
})

test('shift-click and shift with an arrow key extend the selection over a range', async () => {
  const { w } = await page()
  await button(w, 'Select…').trigger('click')
  await checkbox(w, 'api').trigger('click')
  await checkbox(w, 'two').trigger('click', { shiftKey: true })
  expect(checked(w)).toEqual(['api', 'tool', 'one', 'two'])

  await checkbox(w, 'api').trigger('click')
  await checkbox(w, 'tool').trigger('click')
  await checkbox(w, 'one').trigger('click')
  await checkbox(w, 'two').trigger('click')
  expect(checked(w)).toEqual([])

  // The keyboard: a box takes space natively; shift with an arrow moves and selects.
  ;(checkbox(w, 'tool').element as HTMLInputElement).focus()
  await checkbox(w, 'tool').trigger('click')
  await checkbox(w, 'tool').trigger('keydown', { key: 'ArrowDown', shiftKey: true })
  await checkbox(w, 'one').trigger('keydown', { key: 'ArrowDown', shiftKey: true })
  expect(checked(w)).toEqual(['tool', 'one', 'two'])
  expect(document.activeElement).toBe(checkbox(w, 'two').element)
  // Without shift, an arrow only moves.
  await checkbox(w, 'two').trigger('keydown', { key: 'ArrowUp' })
  expect(document.activeElement).toBe(checkbox(w, 'one').element)
  expect(checked(w)).toEqual(['tool', 'one', 'two'])
})

test('Remove… names the count and the projects; the answer per project keeps the refused ones selected with their reason and ways out', async () => {
  const { h, w } = await page()
  await button(w, 'Select…').trigger('click')
  await filter(w).setValue('w/')
  await button(w, 'Select all 2 shown').trigger('click')
  await filter(w).setValue('clients')
  await button(w, 'Select all 2 shown').trigger('click')
  await filter(w).setValue('')
  await button(w, 'Remove…').trigger('click')
  const dialog = w.get('[data-batch-remove-dialog]')
  expect(dialog.text()).toContain('Remove 4 projects from omo?')
  expect(dialog.findAll('[data-batch-remove-list] li').map((l) => l.text())).toEqual(['api/src/clients/api', 'w/one/w/one', 'w/two/w/two', 'web/src/clients/web'])
  expect(h.sent).toEqual([])

  h.respond.write = batchAnswer({ two: { code: 'validation', message: ROOT_REFUSAL }, web: { code: 'in_use', message: 'Open tickets use this project: #4.' } })
  await button(w, 'Remove 4 projects').trigger('click')
  await flushPromises()
  expect(h.sent).toEqual([{ method: 'POST', path: '/api/projects/batch', body: { ids: ['api', 'one', 'two', 'web'], action: 'remove' } }])
  h.omo.emit('project.removed', { id: 'api' })
  h.omo.emit('project.removed', { id: 'one' })
  await flushPromises()

  const result = w.get('[data-batch-result]')
  expect(result.get('[data-batch-summary]').text()).toBe('Removed 2; 2 could not be removed.')
  const refused = result.findAll('[data-batch-refused]')
  expect(refused.map((r) => r.attributes('data-batch-refused'))).toEqual(['two', 'web'])
  expect(refused[0]!.text()).toContain('w/two')
  expect(refused[0]!.text()).toContain(ROOT_REFUSAL)
  expect(refused[1]!.text()).toContain('Open tickets use this project: #4.')
  // The ways out of the single row, for the project a workspace folder would find again.
  expect(refused[0]!.findAll('button').map((b) => b.text())).toEqual(['Switch it off', 'Add an ignore pattern'])
  expect(refused[1]!.findAll('button')).toHaveLength(0)
  expect(checked(w)).toEqual(['two', 'web'])
  expect(count(w)).toBe('2 selected')

  // Add an ignore pattern goes to the workspace folder's form.
  await button(refused[0]!, 'Add an ignore pattern').trigger('click')
  await flushPromises()
  const patterns = w.findAll('[data-root="0"] [data-part="input"]').map((i) => (i.element as HTMLInputElement).value)
  expect(patterns).toEqual(['node_modules', 'two'])

  // Switch it off sends that project alone.
  h.respond.write = batchAnswer()
  await button(w.get('[data-batch-refused="two"]'), 'Switch it off').trigger('click')
  await flushPromises()
  expect(h.sent.at(-1)).toEqual({ method: 'POST', path: '/api/projects/batch', body: { ids: ['two'], action: 'disable' } })
  expect(w.find('[data-batch-refused="two"]').exists()).toBe(false)
})

test('Switch off and Switch on send the selection; the selection stays for the way back', async () => {
  const { h, w } = await page()
  await button(w, 'Select…').trigger('click')
  await checkbox(w, 'api').trigger('click')
  await checkbox(w, 'web').trigger('click')
  h.respond.write = batchAnswer()
  await button(w, 'Switch off').trigger('click')
  await flushPromises()
  expect(h.sent.at(-1)).toEqual({ method: 'POST', path: '/api/projects/batch', body: { ids: ['api', 'web'], action: 'disable' } })
  expect(w.get('[data-batch-summary]').text()).toBe('Switched off 2.')
  expect(announcements.polite.value).toBe('Switched off 2.')
  expect(checked(w)).toEqual(['api', 'web'])
  await button(w, 'Switch on').trigger('click')
  await flushPromises()
  expect(h.sent.at(-1)).toEqual({ method: 'POST', path: '/api/projects/batch', body: { ids: ['api', 'web'], action: 'enable' } })
  expect(w.get('[data-batch-summary]').text()).toBe('Switched on 2.')
})

test('the actions wait for a selection', async () => {
  const { w } = await page()
  await button(w, 'Select…').trigger('click')
  for (const t of ['Remove…', 'Switch off', 'Switch on']) expect(button(w, t).attributes('disabled')).toBeDefined()
  await checkbox(w, 'tool').trigger('click')
  for (const t of ['Remove…', 'Switch off', 'Switch on']) expect(button(w, t).attributes('disabled')).toBeUndefined()
  // Done leaves the mode and forgets the selection.
  await button(w, 'Done').trigger('click')
  await button(w, 'Select…').trigger('click')
  expect(count(w)).toBe('None selected')
})
