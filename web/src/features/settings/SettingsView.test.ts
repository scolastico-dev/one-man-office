import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test, vi } from 'vitest'
import { computed, defineComponent, h, ref, type Component, type Ref } from 'vue'
import { createMemoryHistory, createRouter, RouterView, type Router } from 'vue-router'
import { DIALOGS_IN_PLACE } from '../../components/ui/dialogHost'
import { SESSION } from '../../session'
import { provideBoard } from '../../store'
import { general, project } from '../../testing/fakeOmo'
import { harness, type Harness } from '../create/dialogTesting'
import FileErrors from './FileErrors.vue'
import SettingsPage from './SettingsPage.vue'
import SettingsView from './SettingsView.vue'
import { SETTINGS_SECTIONS } from './settingsSections'
import { LEAVE_QUESTION, QuestionHost, useLeaveGuard } from './useLeaveGuard'
import type { SettingsResource } from './useSettingsResource'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
  vi.unstubAllGlobals()
})

function narrowScreen() {
  vi.stubGlobal('matchMedia', (q: string) => ({ matches: q.includes('max-width'), media: q, addEventListener() {}, removeEventListener() {} }))
}

const Board = defineComponent({ name: 'BoardStub', setup: () => () => h('p', { 'data-board-stub': '' }, 'board') })

/** The settings view under a router like the app's, with a session on a fake omo. */
async function mountSettings(at: string, o: { h?: Harness; settings?: Component } = {}): Promise<{ w: VueWrapper; router: Router; h: Harness }> {
  const h0 = o.h ?? (await harness())
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', name: 'board', component: Board },
      { path: '/settings/:section?', name: 'settings', component: o.settings ?? SettingsView },
    ],
  })
  await router.push(at)
  const Host = defineComponent({
    setup() {
      provideBoard(h0.session.store)
      return () => h(RouterView)
    },
  })
  const w = mount(Host, { attachTo: document.body, global: { plugins: [router], provide: { [SESSION as symbol]: h0.session, [DIALOGS_IN_PLACE as symbol]: true } } })
  mounted = w
  await flushPromises()
  // The section's page is loaded on demand.
  await vi.dynamicImportSettled()
  await flushPromises()
  return { w, router, h: h0 }
}

const links = (w: VueWrapper) => w.findAll('nav a')
const current = (w: VueWrapper) => w.findAll('nav a[aria-current="page"]').map((a) => a.attributes('data-section'))
const title = (w: VueWrapper) => w.find('h1:not(.sr-only)').text()

test('sections load lazily and the nav marks the current one', async () => {
  const { w, router } = await mountSettings('/settings/limits?project=demo&q=x')
  expect(w.get('nav').attributes('aria-labelledby')).toBeTruthy()
  expect(w.get(`#${w.get('nav').attributes('aria-labelledby')}`).text()).toBe('Settings')
  expect(links(w).map((a) => a.find('.settings-nav__label').text())).toEqual(['Projects', 'Agents', 'Roles', 'Usage', 'Prompts', 'Skill sets', 'Environment', 'Limits', 'Review', 'Plugins', 'General', 'Backup'])
  expect(links(w)[2]!.attributes('href')).toBe('/settings/roles?project=demo&q=x')
  expect(current(w)).toEqual(['limits'])
  expect(title(w)).toBe('Limits')
  expect(w.text()).toContain('Tasks and stories in work, health checks and time limits.')

  await links(w)[2]!.trigger('click')
  await flushPromises()
  await vi.dynamicImportSettled()
  await flushPromises()
  expect(router.currentRoute.value.fullPath).toBe('/settings/roles?project=demo&q=x')
  expect(current(w)).toEqual(['roles'])
  expect(title(w)).toBe('Roles')
  // The Roles page carries the agent setup.
  expect(w.find('[data-setup-report]').exists()).toBe(true)
  // No main landmark of its own: the app shell has it.
  expect(w.find('main').exists()).toBe(false)
})

test('every section opens its own page with its title', async () => {
  const { w, router } = await mountSettings('/settings/projects')
  for (const s of SETTINGS_SECTIONS) {
    await router.push(`/settings/${s.id}`)
    await flushPromises()
    await vi.dynamicImportSettled()
    await flushPromises()
    expect(title(w)).toBe(s.label)
    expect(current(w)).toEqual([s.id])
  }
})

test('/settings shows Projects on a wide screen; an address naming no section shows Projects with a notice', async () => {
  const wide = await mountSettings('/settings')
  expect(current(wide.w)).toEqual(['projects'])
  expect(title(wide.w)).toBe('Projects')
  expect(wide.w.find('[data-settings-notice]').exists()).toBe(false)
  expect(wide.w.find('[data-settings-back]').exists()).toBe(false)
  mounted?.unmount()

  const odd = await mountSettings('/settings/nope‮')
  expect(current(odd.w)).toEqual(['projects'])
  expect(odd.w.get('[data-settings-notice]').text()).toContain('There is no settings section “nope\\u{202E}”. This is Projects.')
})

test('at 390 px the section list is a page of its own and each section links back to it', async () => {
  narrowScreen()
  const { w, router } = await mountSettings('/settings?needs_me=1')
  expect(w.find('[data-settings-page]').exists()).toBe(false)
  expect(w.get('h1').text()).toBe('Settings')
  expect(current(w)).toEqual([])

  await links(w)[7]!.trigger('click')
  await flushPromises()
  await vi.dynamicImportSettled()
  await flushPromises()
  expect(router.currentRoute.value.fullPath).toBe('/settings/limits?needs_me=1')
  expect(w.find('nav').exists()).toBe(false)
  expect(title(w)).toBe('Limits')
  const back = w.get('[data-settings-back]')
  expect(back.attributes('href')).toBe('/settings?needs_me=1')
  await back.trigger('click')
  await flushPromises()
  expect(router.currentRoute.value.fullPath).toBe('/settings?needs_me=1')
  expect(w.find('nav').exists()).toBe(true)
})

/** A page with a form whose dirtiness the test sets. */
function guardedPage(dirty: Ref<boolean>, more?: Ref<boolean>) {
  return defineComponent({
    setup() {
      useLeaveGuard(dirty)
      return () => h('div', [h(QuestionHost), more ? h(defineComponent({ setup: () => (useLeaveGuard(more), () => h('p', 'second form')) })) : null])
    },
  })
}

test('leave guard: a dirty page asks before leaving; a clean one does not', async () => {
  const dirty = ref(false)
  const { w, router } = await mountSettings('/settings/limits', { settings: guardedPage(dirty) })

  await router.push('/settings/roles')
  expect(router.currentRoute.value.path).toBe('/settings/roles')
  expect(w.find('[data-question]').exists()).toBe(false)

  dirty.value = true
  const leaving = router.push('/')
  await flushPromises()
  const dialog = w.get('dialog')
  expect(dialog.text()).toContain(LEAVE_QUESTION.title)
  expect(dialog.text()).toContain('Your changes are lost.')
  await w.get('[data-question-cancel]').trigger('click')
  await leaving
  expect(router.currentRoute.value.path).toBe('/settings/roles')
  expect(w.find('dialog').exists()).toBe(false)

  // Another section is leaving too.
  const switching = router.push('/settings/prompts')
  await flushPromises()
  await w.get('[data-question-confirm]').trigger('click')
  await switching
  expect(router.currentRoute.value.path).toBe('/settings/prompts')
})

test('leave guard: closing the tab is asked by the browser while dirty only', async () => {
  const dirty = ref(false)
  await mountSettings('/settings/limits', { settings: guardedPage(dirty) })
  const ev = () => {
    const e = new Event('beforeunload', { cancelable: true })
    window.dispatchEvent(e)
    return e.defaultPrevented
  }
  expect(ev()).toBe(false)
  dirty.value = true
  await flushPromises()
  expect(ev()).toBe(true)
  dirty.value = false
  await flushPromises()
  expect(ev()).toBe(false)
})

test('leave guard: two dirty forms on one page ask once', async () => {
  const a = ref(true)
  const b = ref(true)
  const { w, router } = await mountSettings('/settings/roles', { settings: guardedPage(a, b) })
  const leaving = router.push('/')
  await flushPromises()
  expect(w.findAll('dialog')).toHaveLength(1)
  await w.get('[data-question-confirm]').trigger('click')
  await leaving
  await flushPromises()
  expect(router.currentRoute.value.path).toBe('/')
})

test('leave guard: the tab close is asked while any form is dirty, not only the last one that changed', async () => {
  const a = ref(true)
  const b = ref(true)
  const show = ref(true)
  const second = defineComponent({ setup: () => (useLeaveGuard(b), () => h('p', 'second form')) })
  const page = defineComponent({ setup: () => (useLeaveGuard(a), () => h('div', [h(QuestionHost), show.value ? h(second) : null])) })
  await mountSettings('/settings/roles', { settings: page })
  const ev = () => {
    const e = new Event('beforeunload', { cancelable: true })
    window.dispatchEvent(e)
    return e.defaultPrevented
  }
  expect(ev()).toBe(true)
  // One of two dirty forms is saved: the other still asks.
  b.value = false
  await flushPromises()
  expect(ev()).toBe(true)
  b.value = true
  await flushPromises()
  a.value = false
  await flushPromises()
  expect(ev()).toBe(true)
  // A dirty form that goes away stops asking; the page's own clean form does not ask.
  show.value = false
  await flushPromises()
  expect(ev()).toBe(false)
})

/** The Projects page with one workspace folder, whose path the test edits to make the page dirty. */
async function dirtyProjects(at: string) {
  general.workspace_roots = [{ path: '/src', max_depth: 4, ignore: [] }]
  try {
    const m = await mountSettings(at)
    await m.w.get('[data-root-path] input, input[data-root-path]').setValue('/elsewhere')
    await flushPromises()
    return m
  } finally {
    general.workspace_roots = []
  }
}

test('leave guard: an address that shows the same page asks nothing', async () => {
  const wide = await dirtyProjects('/settings')
  await wide.router.push('/settings/projects')
  await flushPromises()
  expect(wide.router.currentRoute.value.path).toBe('/settings/projects')
  expect(wide.w.find('[data-question-confirm]').exists()).toBe(false)
  expect((wide.w.get('input[data-root-path]').element as HTMLInputElement).value).toBe('/elsewhere')
  mounted?.unmount()

  const odd = await dirtyProjects('/settings/nope')
  await odd.router.push('/settings/projects')
  await flushPromises()
  expect(odd.router.currentRoute.value.path).toBe('/settings/projects')
  expect(odd.w.find('[data-question-confirm]').exists()).toBe(false)

  // Another section is another page: asked.
  const leaving = odd.router.push('/settings/limits')
  await flushPromises()
  expect(odd.w.find('[data-question-confirm]').exists()).toBe(true)
  await odd.w.get('[data-question-cancel]').trigger('click')
  await leaving
  expect(odd.router.currentRoute.value.path).toBe('/settings/projects')
})

test('leave guard: narrowing the window at /settings keeps a dirty page on screen', async () => {
  const listeners: ((e: { matches: boolean }) => void)[] = []
  let narrowNow = false
  vi.stubGlobal('matchMedia', (q: string) => ({
    get matches() {
      return q.includes('max-width') && narrowNow
    },
    media: q,
    addEventListener: (_: string, f: (e: { matches: boolean }) => void) => listeners.push(f),
    removeEventListener: (_: string, f: (e: { matches: boolean }) => void) => listeners.splice(listeners.indexOf(f), 1),
  }))
  const { w, router } = await dirtyProjects('/settings?q=x')
  narrowNow = true
  for (const f of [...listeners]) f({ matches: true })
  await flushPromises()
  expect(w.find('[data-question-confirm]').exists()).toBe(false)
  expect(router.currentRoute.value.fullPath).toBe('/settings/projects?q=x')
  expect((w.get('input[data-root-path]').element as HTMLInputElement).value).toBe('/elsewhere')
  expect(w.find('[data-settings-back]').exists()).toBe(true)
})

/** A resource as the page sees it, with fixed state. */
function fakeResource(over: Partial<Record<string, unknown>> = {}): SettingsResource<unknown> {
  const base = {
    value: ref({ n: 1 }),
    revision: ref('r1'),
    draft: ref({ n: 2 }),
    dirty: computed(() => true),
    loading: ref(false),
    saving: ref(false),
    error: ref(''),
    fieldErrors: ref({}),
    conflict: ref(null),
    broken: computed(() => []),
    changedOnDisk: ref(null),
    status: computed(() => null),
    reload: vi.fn(async () => {}),
    save: vi.fn(async () => 'saved' as const),
    discard: vi.fn(),
    useTheirs: vi.fn(),
    keepMine: vi.fn(),
  }
  return { ...base, ...over } as unknown as SettingsResource<unknown>
}

function pageWith(resource: SettingsResource<unknown>) {
  return defineComponent({ setup: () => () => h(SettingsPage, { title: 'Limits', description: 'Limits of things', resource }, () => h('p', { 'data-form': '' }, 'the form')) })
}

test('a page with a resource shows broken files, a disk change and the Save bar with the conflict choices', async () => {
  const r = fakeResource({
    broken: computed(() => [{ file: 'settings.yaml', message: 'line 3: bad.' }]),
    changedOnDisk: ref('2026-11-15T14:02:00.000000000Z'),
    conflict: ref({ value: { n: 3 }, revision: 'r2' }),
    status: computed(() => 'conflict'),
  })
  const { w } = await mountSettings('/settings/limits', { settings: pageWith(r) })
  expect(w.get('[data-file-error]').text()).toContain('settings.yaml')
  expect(w.get('[data-file-error]').text()).toContain('Cannot be read: line 3: bad. omo uses the last version it could read.')
  expect(w.get('[data-changed-on-disk]').text()).toMatch(/Changed on disk at .+, not through omo\./)
  expect(w.get('[data-form]').text()).toBe('the form')
  expect(w.get('[data-save-state]').text()).toBe('Changed outside this page')
  await w.get('[data-use-theirs]').trigger('click')
  expect(r.useTheirs).toHaveBeenCalledOnce()
  await w.get('[data-keep-mine]').trigger('click')
  expect(r.keepMine).toHaveBeenCalledOnce()
  const save = w.findAll('button').find((b) => b.text() === 'Save')!
  await save.trigger('click')
  expect(r.save).toHaveBeenCalledOnce()
})

test('a page with a resource waits for the first read and offers to read again after a failure', async () => {
  const loading = fakeResource({ loading: ref(true), value: ref(null), draft: ref(null), dirty: computed(() => false) })
  const one = await mountSettings('/settings/limits', { settings: pageWith(loading) })
  expect(one.w.find('[data-settings-loading]').exists()).toBe(true)
  expect(one.w.find('[data-form]').exists()).toBe(false)
  expect(one.w.find('[data-save-state]').exists()).toBe(false)
  mounted?.unmount()

  const failed = fakeResource({ value: ref(null), draft: ref(null), dirty: computed(() => false), error: ref('boom') })
  const two = await mountSettings('/settings/limits', { settings: pageWith(failed) })
  expect(two.w.get('[data-settings-load-error]').text()).toContain('boom')
  await two.w.get('[data-settings-load-error] button').trigger('click')
  expect(failed.reload).toHaveBeenCalledOnce()
})

test('a refused save moves the focus to the first field with an error', async () => {
  const r = fakeResource({ save: vi.fn(async () => 'refused' as const) })
  const Page = defineComponent({
    setup: () => () =>
      h(SettingsPage, { title: 'Limits', resource: r }, () => [h('input', { 'data-ok': '' }), h('input', { 'data-bad': '', 'aria-invalid': 'true' })]),
  })
  const { w } = await mountSettings('/settings/limits', { settings: Page })
  await w.findAll('button').find((b) => b.text() === 'Save')!.trigger('click')
  await flushPromises()
  expect(document.activeElement).toBe(w.get('[data-bad]').element)
})

test('file errors show the path and the message as visible text', () => {
  const w = mount(FileErrors, { props: { errors: [{ file: 'roles/‮pm.yaml', message: 'bad\u0007 value' }] } })
  expect(w.text()).toContain('roles/\\u{202E}pm.yaml')
  expect(w.text()).toContain('bad\\u{7} value')
  w.unmount()
})

test('the Projects page lists the projects with their state and branch, names as visible text', async () => {
  const h0 = await harness((omo) => {
    omo.projects = [
      { ...project('a', 'alpha'), current_branch: 'feature-x', default_branch: 'main' },
      { ...project('b', 'beta'), enabled: false, current_branch: 'main', default_branch: 'main' },
      { ...project('c', 'gamma‮'), missing: true },
    ]
  })
  const { w } = await mountSettings('/settings/projects', { h: h0 })
  const rows = w.findAll('tbody tr').map((r) => [r.get('th').text(), r.get('.projects__state').text(), r.get('.projects__branch').text().replace(/\s+/g, ' ')])
  expect(rows).toEqual([
    ['alpha', 'enabled', 'feature-x Not its default branch main.'],
    ['beta', 'disabled', 'main'],
    ['gamma\\u{202E}', 'missing', ''],
  ])
  expect(w.findAll('button').some((b) => b.text() === 'Add project')).toBe(true)
})
