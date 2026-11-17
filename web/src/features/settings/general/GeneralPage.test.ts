import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test, vi } from 'vitest'
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

/** A stand-in for the server's preview: {slug} and {number} of #42 "Fix the login". */
function preview(template: string) {
  if (!template.includes('{slug}') && !template.includes('{number}'))
    return { branch: '', error: 'the branch template must contain {slug} or {number}; without either every ticket would get the same name' }
  return { branch: template.replace('{slug}', 'fix-the-login').replace('{number}', '42'), error: '' }
}

async function mountPage(ceoOn: boolean) {
  general.ceo.enabled = ceoOn
  const h = await harness()
  h.respond.write = (s) =>
    s.path === '/api/settings/branch-preview'
      ? ok(preview((s.body as { template: string }).template))
      : ok({
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
    ui: { open_tickets_as_page: false, screen_reader_mode: false },
    branches: { template: 'omo/{slug}' },
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
    ui: { open_tickets_as_page: false, screen_reader_mode: false },
    branches: { template: 'omo/{slug}' },
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
    ui: { open_tickets_as_page: false, screen_reader_mode: false },
    branches: { template: 'omo/{slug}' },
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
    ui: { open_tickets_as_page: true, screen_reader_mode: false },
    branches: { template: 'omo/{slug}' },
  })
})

test('screen reader mode is a global switch, off by default, saved with the page', async () => {
  const h = await mountPage(false)
  const sw = w!.get('[data-field="ui.screen_reader_mode"]')
  expect(sw.attributes('role')).toBe('switch')
  expect(sw.attributes('aria-checked')).toBe('false')
  expect(w!.get('[data-accessibility-section]').text()).toContain('text copy of the terminal output')
  await sw.trigger('click')
  await save().trigger('click')
  await flushPromises()
  expect((h.sent.find((s) => s.method === 'PATCH')!.body as { ui: unknown }).ui).toEqual({ open_tickets_as_page: false, screen_reader_mode: true })
})

const link = () => w!.find('[data-server-facts] a')

test('the version of a commit build links to the commit and keeps its text', async () => {
  const h = await harness((o) => {
    o.server = { version: '0.11.0-476-g8a63736', version_commit: '8a63736', version_url: 'https://github.com/scolastico-dev/one-man-office/commit/8a63736' }
  })
  h.respond.write = () => ok({ settings: general, errors: [], revision: 'r2', home: '/h' })
  w = await mountWith(h, GeneralPage, {})
  await flushPromises()
  expect(link().text()).toBe('0.11.0-476-g8a63736')
  expect(link().attributes()).toMatchObject({
    href: 'https://github.com/scolastico-dev/one-man-office/commit/8a63736',
    target: '_blank',
    rel: 'noopener noreferrer',
    'aria-label': 'omo 0.11.0-476-g8a63736, commit 8a63736 on GitHub',
  })
})

test('a release version links to its release page', async () => {
  const h = await harness((o) => {
    o.server = { version: '0.12.0', version_url: 'https://github.com/scolastico-dev/one-man-office/releases/tag/0.12.0' }
  })
  w = await mountWith(h, GeneralPage, {})
  await flushPromises()
  expect(link().attributes('href')).toBe('https://github.com/scolastico-dev/one-man-office/releases/tag/0.12.0')
  expect(link().attributes('aria-label')).toBe('omo 0.12.0, release on GitHub')
})

test('an unrecognised version is plain text, and only an https link is followed', async () => {
  for (const url of ['', 'javascript:alert(1)']) {
    const h = await harness((o) => {
      o.server = { version: 'dev', version_url: url }
    })
    w = await mountWith(h, GeneralPage, {})
    await flushPromises()
    expect(link().exists()).toBe(false)
    expect(w!.get('[data-server-facts]').text()).toContain('dev')
    w.unmount()
  }
  w = null
})

test('the address says how to set it, and where omo keeps listening', async () => {
  const h = await harness((o) => {
    o.server = { address: 'http://omo.localhost:8090/', listen: '127.0.0.1:8090' }
  })
  w = await mountWith(h, GeneralPage, {})
  await flushPromises()
  const facts = w.get('[data-server-facts]')
  expect(facts.text()).toContain('http://omo.localhost:8090/')
  expect(facts.get('[data-address-help]').text()).toBe('Set with omo config set public_url <address>; omo keeps listening on 127.0.0.1:8090.')
  w.unmount()

  const h2 = await harness((o) => {
    o.server = { address: 'https://omo.example.com/', address_configured: true, listen: '127.0.0.1:8090' }
  })
  w = await mountWith(h2, GeneralPage, {})
  await flushPromises()
  expect(w.get('[data-server-facts]').text()).toContain('https://omo.example.com/')
  expect(w.get('[data-address-help]').text()).toBe('From the public_url option; omo keeps listening on 127.0.0.1:8090.')
})

test('the branch template is a text field with its placeholders, an example from the server, saved with the page', async () => {
  vi.useFakeTimers()
  try {
    const h = await mountPage(false)
    await vi.runAllTimersAsync()
    const field = w!.get<HTMLInputElement>('input[data-field="branches.template"]')
    expect(field.element.value).toBe('omo/{slug}')
    const section = w!.get('[data-branches-section]').text()
    for (const p of ['{slug}', '{number}', '{type}', '{project}', '{host}', '{user}']) expect(section).toContain(p)
    expect(w!.get('[data-branch-preview]').text()).toBe('Example: omo/fix-the-login for #42 "Fix the login", a task')

    await field.setValue('feature/{number}-{slug}')
    await vi.runAllTimersAsync()
    expect(w!.get('[data-branch-preview]').text()).toContain('Example: feature/42-fix-the-login')
    const asked = h.sent.filter((s) => s.path === '/api/settings/branch-preview').map((s) => s.body)
    expect(asked).toEqual([{ template: 'omo/{slug}' }, { template: 'feature/{number}-{slug}' }])

    await field.setValue('main')
    await vi.runAllTimersAsync()
    expect(w!.get('[data-branch-preview-error]').text()).toContain('must contain {slug} or {number}')

    await field.setValue('{user}/{slug}')
    await vi.runAllTimersAsync()
    await save().trigger('click')
    await vi.runAllTimersAsync()
    expect(h.sent.find((s) => s.method === 'PATCH')!.body).toMatchObject({ branches: { template: '{user}/{slug}' } })
  } finally {
    vi.useRealTimers()
  }
})

test("a refused template shows the server's message under the field", async () => {
  const h = await mountPage(false)
  h.respond.write = (s) =>
    s.path === '/api/settings/branch-preview'
      ? ok({ branch: 'x', error: '' })
      : refuse('invalid_settings', 'the branch template uses the unknown placeholder {nope}', { field: 'branches.template' }, 422)
  await w!.get('input[data-field="branches.template"]').setValue('omo/{nope}')
  await save().trigger('click')
  await flushPromises()
  const field = w!.get('input[data-field="branches.template"]')
  expect(field.attributes('aria-invalid')).toBe('true')
  expect(w!.text()).toContain('the branch template uses the unknown placeholder {nope}')
})
