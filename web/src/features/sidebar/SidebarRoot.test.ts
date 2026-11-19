import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { ticket } from '../board/testing'
import { agent, emitAgent, mountSidebar, sidebarHarness } from './sidebarTesting'
import SidebarRoot from './SidebarRoot.vue'
import { LazyTerminal } from '../terminal/LazyTerminal'
import { resetSidebar, SIDEBAR_KEY, useSidebar } from './useSidebar'
import { general, project } from '../../testing/fakeOmo'
import type { PluginInfo, PluginTask, PluginsResponse } from '../../api/types'
import type { PluginFacade } from '../plugins/frontendRuntime'
import { disposePluginExtensions } from '../plugins/usePluginExtensions'

let mounted: VueWrapper | null = null
beforeEach(() => {
  resetSidebar()
  localStorage.clear()
})
afterEach(() => {
  mounted?.unmount()
  mounted = null
  vi.unstubAllGlobals()
})

async function open(path = '/') {
  const hn = await sidebarHarness((omo) => {
    omo.tickets = [ticket({ number: 3, title: 'Three', column: 'in_progress' })]
  })
  hn.agents.list = [agent({ id: 'a', ticket: 3 })]
  const { wrapper, router } = await mountSidebar(hn, SidebarRoot, {}, path)
  mounted = wrapper
  await flushPromises()
  return { hn, w: wrapper, router }
}

const tabNames = (w: VueWrapper) => w.findAll('[role="tab"]').map((t) => t.text())

test.each([false, true])('plugin tabs isolate controls, search projects and stop running instances (narrow: %s)', async (narrow) => {
  vi.stubGlobal('matchMedia', () => ({ matches: narrow, addEventListener() {}, removeEventListener() {} }))
  const hn = await sidebarHarness(omo => {
    omo.settings = { ...general, ceo: { ...general.ceo, enabled: true } }
    omo.projects = [project('one', 'First project'), project('two', 'Second project'), { ...project('off', 'Disabled'), enabled: false }, { ...project('gone', 'Missing'), missing: true }]
  })
  hn.agents.list = [agent({ role: 'ceo' })]
  const plugin: PluginInfo = { name: 'editor', state: 'ready', content_id: 'a'.repeat(64), commit: '', version: '1', description: '', message: '', last_event: '', last_run_at: null, config_fields: [], hooks: [{ event: 'manual', name: 'open', scope: 'global', description: '', interval: '' }], buttons: [], frontend: { script: 'entry.js' } }
  const response: PluginsResponse = { plugins: [plugin], settings: [{ name: 'editor', enabled: true, source: '/repo', subpath: '', ref: '', config: {} }], errors: [] }
  let tasks: PluginTask[] = [
    { id: 'run-1', name: 'first-editor', state: 'running', started_at: '2026-11-18T10:00:00Z', output: [] },
    { id: 'run-2', name: 'old-editor', state: 'exited', started_at: '2026-11-18T10:00:00Z', output: [] },
    { id: 'run-3', name: 'second-editor', ticket: 3, state: 'running', started_at: '2026-11-18T10:00:00Z', output: [] },
  ]
  const base = hn.session.client.get.bind(hn.session.client)
  hn.session.client.get = ((path: string, q?: never) => path === '/api/plugins' ? Promise.resolve({ data: response, eventId: null }) : path === '/api/plugins/editor/tasks' ? Promise.resolve({ data: { tasks }, eventId: null }) : base(path, q)) as typeof base
  const send = vi.fn(async (_method, path, _body) => {
    if (path.endsWith('/stop')) tasks = tasks.map(t => t.id === 'run-1' ? { ...t, state: 'exited' } : t)
    return { data: { message: 'Opened' }, eventId: null }
  })
  hn.session.client.send = send as typeof hn.session.client.send
  const append = document.head.append.bind(document.head)
  const noFetch = vi.spyOn(document.head, 'append').mockImplementation((...nodes) => {
    for (const node of nodes) if (node instanceof HTMLScriptElement) node.type = 'text/plain'
    append(...nodes)
  })
  const { wrapper: w } = await mountSidebar(hn, SidebarRoot)
  mounted = w
  try {
    const entry = document.querySelector<HTMLScriptElement & { omo: PluginFacade }>('script[data-omo-plugin="editor"]')!
    entry.omo.initialize(omo => {
      omo.addSidebarTab({ id: 'editor', label: 'Editor', tasks: true })
      omo.addButton({ id: 'open', label: 'Open editor', place: 'sidebar', tab: 'editor', pick_project: true, action: 'open' })
      omo.addPanel({ id: 'progress', title: 'Progress', text: 'In this tab only', place: 'sidebar', tab: 'editor' })
    })
    entry.dispatchEvent(new Event('load'))
    useSidebar().openOn('ceo')
    await flushPromises()
    expect(w.find('[data-plugin-control]').exists()).toBe(false)
    expect(w.text()).not.toContain('In this tab only')
    expect(tabNames(w)).toContain('Editor')
    await w.get('[data-tab="plugin:editor:editor"]').trigger('click')
    await flushPromises()
    expect(w.findAll('[data-plugin-control]')).toHaveLength(1)
    expect(w.text()).toContain('In this tab only')
    expect(w.text()).not.toContain('old-editor')
    expect(w.findAll('[data-task-stop]')).toHaveLength(2)
    await w.get('[data-plugin-control] button').trigger('click')
    await w.get('input[role="combobox"]').setValue('second')
    expect(w.findAll('[role="option"]')).toHaveLength(1)
    await w.get('[role="option"]').trigger('click')
    await flushPromises()
    expect(send).toHaveBeenCalledWith('POST', '/api/plugins/editor/actions/open', { project: 'two' })
    await w.findAll('[data-task-stop]')[0]!.trigger('click')
    await flushPromises()
    expect(send).toHaveBeenCalledWith('POST', '/api/plugins/editor/tasks/first-editor/stop', {})
    expect(w.findAll('[data-task-stop]')).toHaveLength(1)
    expect(w.text()).toContain('second-editor')
    plugin.state = 'disabled'
    hn.omo.emit('settings.changed', { section: 'plugins', name: 'editor' })
    await flushPromises()
    expect(tabNames(w)).not.toContain('Editor')
    expect(w.get('[role="tab"][aria-selected="true"]').text()).toMatch(/^Agents/)
  } finally { disposePluginExtensions(hn.session.store); noFetch.mockRestore() }
})

test('collapsed it is a strip of tabs with counts; choosing one expands it on that tab and remembers it', async () => {
  const { w } = await open()
  expect(w.get('[data-sidebar]').attributes('data-open')).toBe('false')
  expect(tabNames(w)).toEqual(['Agents 1', 'Terminals 0'])
  expect(w.find('[role="tabpanel"]').exists()).toBe(false)
  await w.get('[data-tab="agents"]').trigger('click')
  await flushPromises()
  expect(w.get('[data-sidebar]').attributes('data-open')).toBe('true')
  expect(w.get('[role="tab"][aria-selected="true"]').text()).toBe('Agents 1')
  expect(w.findAll('[data-agent-row]')).toHaveLength(1)
  expect(JSON.parse(localStorage.getItem(SIDEBAR_KEY)!)).toMatchObject({ open: true, tab: 'agents' })
  await w.get('[data-sidebar-toggle]').trigger('click')
  expect(w.get('[data-sidebar]').attributes('data-open')).toBe('false')
  expect(w.get('[data-sidebar-toggle]').attributes('aria-label')).toBe('Expand sidebar')
})

test('the CEO tab shows only while the CEO is enabled', async () => {
  const hn = await sidebarHarness()
  const { wrapper } = await mountSidebar(hn, SidebarRoot)
  mounted = wrapper
  expect(tabNames(wrapper)).not.toContain('CEO')
  hn.omo.emit('settings.changed', { section: 'general', name: '' })
  await flushPromises()
  // The remembered CEO tab falls back to Agents while the tab is hidden.
  localStorage.setItem(SIDEBAR_KEY, JSON.stringify({ open: true, tab: 'ceo', width: 480 }))
  resetSidebar()
  wrapper.unmount()
  const again = await mountSidebar(hn, SidebarRoot)
  mounted = again.wrapper
  expect(again.wrapper.get('[role="tab"][aria-selected="true"]').text()).toMatch(/^Agents/)
})

test('the splitter sets the width in 16 px steps within its limits and the width is remembered', async () => {
  localStorage.setItem(SIDEBAR_KEY, JSON.stringify({ open: true, width: 480, tab: 'agents' }))
  const { w } = await open()
  const sep = w.get('[role="separator"]')
  expect(sep.attributes('aria-valuenow')).toBe('480')
  await sep.trigger('keydown', { key: 'ArrowRight' })
  expect(w.get('[role="separator"]').attributes('aria-valuenow')).toBe('496')
  expect((w.get('.side').element as HTMLElement).style.width).toBe('496px')
  expect(JSON.parse(localStorage.getItem(SIDEBAR_KEY)!).width).toBe(496)
  await sep.trigger('keydown', { key: 'Home' })
  expect(w.get('[role="separator"]').attributes('aria-valuenow')).toBe('320')
  await sep.trigger('keydown', { key: 'ArrowLeft' })
  expect(w.get('[role="separator"]').attributes('aria-valuenow')).toBe('320')
})

test('with storage that fails the page renders and works', async () => {
  vi.stubGlobal('localStorage', {
    getItem: () => {
      throw new Error('blocked')
    },
    setItem: () => {
      throw new Error('blocked')
    },
  })
  const { w } = await open()
  await w.get('[data-tab="agents"]').trigger('click')
  await flushPromises()
  expect(w.findAll('[data-agent-row]')).toHaveLength(1)
})

test('an agent that ends leaves the tab count and the list', async () => {
  localStorage.setItem(SIDEBAR_KEY, JSON.stringify({ open: true, width: 480, tab: 'agents' }))
  const { w, hn } = await open()
  expect(tabNames(w)[0]).toBe('Agents 1')
  emitAgent(hn.omo, 'agent.ended', agent({ id: 'a', ticket: 3, state: 'ended' }))
  await flushPromises()
  expect(tabNames(w)[0]).toBe('Agents 0')
  expect(w.find('[data-agent-row]').exists()).toBe(false)
})

test('the sidebar stays usable while a ticket panel is open: never inert', async () => {
  const { w, router } = await open('/')
  expect(w.get('[data-sidebar]').attributes('inert')).toBeUndefined()
  await router.push('/3')
  await flushPromises()
  expect(w.get('[data-sidebar]').attributes('inert')).toBeUndefined()
})

test('an open agent terminal stays mounted while a ticket panel is open', async () => {
  const { w, router } = await open('/')
  useSidebar().openOn('agents', 'a')
  await flushPromises()
  expect(w.findComponent(LazyTerminal).exists()).toBe(true)
  await router.push('/3')
  await flushPromises()
  expect(w.findComponent(LazyTerminal).exists()).toBe(true)
  expect(w.find('[data-terminal-covered]').exists()).toBe(false)
})

test('the keyboard opens the sidebar into its tabs and Collapse brings the focus to the toggle', async () => {
  const { w } = await open()
  const strip = w.get('[data-tab="agents"]')
  ;(strip.element as HTMLElement).focus()
  await strip.trigger('keydown', { key: 'Enter' })
  await flushPromises()
  expect(document.activeElement).toBe(w.get('[role="tab"][aria-selected="true"]').element)
  await w.get('[data-sidebar-toggle]').trigger('click')
  await flushPromises()
  expect(document.activeElement).toBe(w.get('[data-sidebar-toggle]').element)
})

test('openOn from another feature expands the sidebar and moves the focus into it', async () => {
  const { w } = await open()
  useSidebar().openOn('agents', 'a')
  await flushPromises()
  expect(w.get('[data-sidebar]').attributes('data-open')).toBe('true')
  expect(document.activeElement).toBe(w.get('[role="tab"][aria-selected="true"]').element)
})

test('on a narrow window the column is gone and the sheet shows the same tabs', async () => {
  vi.stubGlobal('matchMedia', (q: string) => ({ matches: q.includes('max-width'), media: q, addEventListener() {}, removeEventListener() {} }))
  const { w } = await open()
  expect(w.find('[data-sidebar]').exists()).toBe(false)
  useSidebar().show()
  await flushPromises()
  const dialog = document.body.querySelector('dialog[open], dialog')!
  expect(dialog.querySelector('h2')!.textContent).toBe('Agents and terminals')
  expect([...dialog.querySelectorAll('[role="tab"]')].map((t) => t.textContent)).toEqual(['Agents 1', 'Terminals 0'])
  expect(dialog.querySelectorAll('[data-agent-row]')).toHaveLength(1)
})

test('on a narrow window the closed sheet keeps nothing mounted, and opening it puts the focus on the selected tab', async () => {
  vi.stubGlobal('matchMedia', (q: string) => ({ matches: q.includes('max-width'), media: q, addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {} }))
  const { w } = await open()
  const sheet = () => document.body.querySelector('dialog')!
  expect(sheet().querySelectorAll('[role="tab"]')).toHaveLength(0)
  useSidebar().openOn('agents', 'a')
  await flushPromises()
  expect(w.findComponent(LazyTerminal).exists()).toBe(true)
  expect(document.activeElement?.getAttribute('role')).toBe('tab')
  expect(document.activeElement?.textContent).toBe('Agents 1')
  useSidebar().hide()
  await flushPromises()
  expect(w.findComponent(LazyTerminal).exists()).toBe(false)
  expect(sheet().querySelectorAll('[role="tab"]')).toHaveLength(0)
  useSidebar().show()
  await flushPromises()
  expect(document.activeElement?.textContent).toBe('Agents 1')
})
