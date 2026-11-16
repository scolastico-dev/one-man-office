import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import { general } from '../../../testing/fakeOmo'
import { harness, mountWith, ok, refuse } from '../../create/dialogTesting'
import GeneralPage from './GeneralPage.vue'

let w: VueWrapper | null = null
const original = structuredClone(general)
afterEach(() => {
  w?.unmount()
  w = null
  Object.assign(general, structuredClone(original))
  document.documentElement.removeAttribute('data-theme')
})

async function mountPage(ceoOn: boolean) {
  general.ceo.enabled = ceoOn
  const h = await harness()
  h.respond.write = (s) =>
    ok({
      settings: general,
      errors: [],
      revision: 'r2',
      home: '/h',
      ...(s.body as object),
    })
  w = await mountWith(h, GeneralPage, {})
  await flushPromises()
  return h
}

const ceoSwitch = () => w!.get('[data-field="ceo.enabled"]')
const save = () => w!.findAll('button').find((b) => b.text() === 'Save')!

test('turning the CEO off asks first; turning it on does not', async () => {
  const h = await mountPage(true)
  expect(ceoSwitch().attributes('aria-checked')).toBe('true')
  await ceoSwitch().trigger('click')
  expect(w!.find('[data-ceo-switch]').exists()).toBe(true)
  expect(w!.text()).toContain('a running CEO session ends at the next scheduling pass')
  // Keeping it on changes nothing.
  await w!.get('[data-ceo-keep]').trigger('click')
  expect(ceoSwitch().attributes('aria-checked')).toBe('true')
  expect(save().attributes('disabled')).toBeDefined()

  await ceoSwitch().trigger('click')
  await w!.get('[data-ceo-confirm]').trigger('click')
  expect(ceoSwitch().attributes('aria-checked')).toBe('false')
  await save().trigger('click')
  await flushPromises()
  expect(h.sent.find((s) => s.method === 'PATCH')!.body).toEqual({
    ceo: { enabled: false, may_approve_stories: false },
    merge_defaults: { story: 'merge', task: 'merge', story_task: 'merge' },
    ui: { open_tickets_as_page: false },
  })
})

test("turning the CEO on asks nothing and only sends the page's keys", async () => {
  const h = await mountPage(false)
  await ceoSwitch().trigger('click')
  expect(w!.find('dialog[open]').exists()).toBe(false)
  expect(ceoSwitch().attributes('aria-checked')).toBe('true')
  await save().trigger('click')
  await flushPromises()
  expect(h.sent.find((s) => s.method === 'PATCH')!.body).toEqual({
    ceo: { enabled: true, may_approve_stories: false },
    merge_defaults: { story: 'merge', task: 'merge', story_task: 'merge' },
    ui: { open_tickets_as_page: false },
  })
})

test('the server facts show the home folder and the theme applies at once', async () => {
  await mountPage(false)
  expect(w!.get('[data-server-facts]').text()).toContain('/home/sample/.local/omo')
  const colours = w!.findAll('select').find((s) => w!.find(`label[for="${s.attributes('id')}"]`).text() === 'Colours')!
  await colours.setValue('dark')
  expect(document.documentElement.getAttribute('data-theme')).toBe('dark')
})

test('the merge defaults are one group of three selects, saved with the page', async () => {
  const h = await mountPage(false)
  const group = w!.get('[data-merge-defaults]')
  expect(group.find('legend').text()).toBe('Merge defaults')
  const select = (k: string) => group.get(`select[data-field="merge_defaults.${k}"]`)
  const labels = ['story', 'task', 'story_task'].map((k) => group.get(`label[for="${select(k).attributes('id')}"]`).text())
  expect(labels).toEqual(['When a story is merged', 'When a task without a story is merged', 'When a task is merged into its story'])
  expect(select('task').findAll('option').map((o) => o.text())).toEqual(['Merge commit', 'Squash into one commit'])
  await select('story_task').setValue('squash')
  await save().trigger('click')
  await flushPromises()
  expect(h.sent.find((s) => s.method === 'PATCH')!.body).toEqual({
    ceo: { enabled: false, may_approve_stories: false },
    merge_defaults: { story: 'merge', task: 'merge', story_task: 'squash' },
    ui: { open_tickets_as_page: false },
  })
})

test('a refused merge default is shown under its select', async () => {
  const h = await mountPage(false)
  h.respond.write = () => refuse('invalid_settings', 'merge_defaults.task must be merge or squash', { field: 'merge_defaults.task' })
  const select = w!.get('select[data-field="merge_defaults.task"]')
  await select.setValue('squash')
  await save().trigger('click')
  await flushPromises()
  expect(select.attributes('aria-invalid')).toBe('true')
  expect(w!.get('[data-merge-defaults]').text()).toContain('merge_defaults.task must be merge or squash')
})

test('opening tickets on their own page is a switch, off by default, saved with the page', async () => {
  const h = await mountPage(false)
  const sw = w!.get('[data-field="ui.open_tickets_as_page"]')
  expect(sw.attributes('role')).toBe('switch')
  expect(sw.attributes('aria-checked')).toBe('false')
  expect(w!.text()).toContain('Open tickets on their own page')
  expect(w!.text()).toContain("A click on a ticket opens its page instead of the panel over the board. The panel stays reachable with 'Open in panel'.")
  await sw.trigger('click')
  expect(sw.attributes('aria-checked')).toBe('true')
  await save().trigger('click')
  await flushPromises()
  expect(h.sent.find((s) => s.method === 'PATCH')!.body).toEqual({
    ceo: { enabled: false, may_approve_stories: false },
    merge_defaults: { story: 'merge', task: 'merge', story_task: 'merge' },
    ui: { open_tickets_as_page: true },
  })
})
