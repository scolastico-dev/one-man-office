import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import { ApiError, type ApiClient, type ApiResult, type ClientOptions, type Query } from '../../api/client'
import type { UserTerminal } from '../../api/types'
import { createTokenStore } from '../../auth/token'
import { createSession } from '../../session'
import { FakeOmo, MemoryStorage, project } from '../../testing/fakeOmo'
import { mountSidebar, type SidebarHarness } from './sidebarTesting'
import TerminalsTab from './TerminalsTab.vue'
import { ticket } from '../board/testing'
import type { ActionContext } from '../ticket/actions'
import { createOpenTerminalAction } from './openTerminalAction'
import { resetSidebar, useSidebar } from './useSidebar'

// The emulator is not part of this test: a stand-in that shows what it was given.
vi.mock('../terminal/LazyTerminal', () => ({
  LazyTerminal: defineComponent({
    props: { src: { type: String, required: true }, label: { type: String, required: true }, endedText: { type: String, default: '' } },
    emits: ['state'],
    setup(p, { expose }) {
      expose({ focus: () => undefined })
      return () => h('div', { 'data-fake-terminal': p.src, 'aria-label': p.label }, p.endedText)
    },
  }),
}))

function shell(over: Partial<UserTerminal> = {}): UserTerminal {
  return {
    id: 't1',
    title: 'demo',
    dir: '/home/me/demo',
    project: 'demo',
    ticket: null,
    state: 'running',
    exit_error: '',
    started_at: '2026-11-15T10:00:00Z',
    ended_at: null,
    ...over,
  }
}

let mounted: VueWrapper | null = null
beforeEach(() => {
  resetSidebar()
  localStorage.clear()
})
afterEach(() => {
  mounted?.unmount()
  mounted = null
})

interface Server {
  list: UserTerminal[]
  sent: string[]
  refuse: ApiError | null
  /** POST answers without sending terminal.opened; the test sends it. */
  quiet: boolean
}

/** The tab as the sidebar shows it: with the sidebar's selection. */
const Bound = defineComponent({
  setup() {
    const s = useSidebar()
    return () => h(TerminalsTab, { selected: s.selection.value })
  },
})

async function open(list: UserTerminal[], selected: string | null = null, component: typeof TerminalsTab | typeof Bound = TerminalsTab) {
  const omo = new FakeOmo()
  omo.projects = [project('demo', 'Demo project'), { ...project('off', 'Switched off'), enabled: false }, { ...project('gone', 'Gone'), missing: true }]
  const server: Server = { list, sent: [], refuse: null, quiet: false }
  const makeClient = (o: ClientOptions): ApiClient => {
    const base = omo.makeClient(o)
    return {
      ...base,
      get: async <T>(path: string, query?: Query): Promise<ApiResult<T>> =>
        path === '/api/terminals' ? { data: structuredClone(server.list) as T, eventId: null } : base.get<T>(path, query),
      send: async <T>(method: string, path: string, body?: unknown): Promise<ApiResult<T>> => {
        server.sent.push(`${method} ${path} ${JSON.stringify(body ?? null)}`)
        if (server.refuse) throw server.refuse
        if (method === 'POST') {
          const t = shell({ id: 'new', title: 'Demo project', ...(body as object) })
          server.list = [t, ...server.list]
          if (!server.quiet) omo.emit('terminal.opened', { terminal: t })
          return { data: t as T, eventId: null }
        }
        const id = decodeURIComponent(path.split('/').pop()!)
        server.list = server.list.filter((x) => x.id !== id)
        omo.emit('terminal.closed', { terminal: shell({ id }) })
        return { data: undefined as T, eventId: null }
      },
    }
  }
  const session = createSession({ tokens: createTokenStore(() => new MemoryStorage()), makeClient, connect: omo.connect })
  session.start()
  await flushPromises()
  const hn = { omo, session } as unknown as SidebarHarness
  const { wrapper } = await mountSidebar(hn, component, component === TerminalsTab ? { selected } : {})
  mounted = wrapper
  await flushPromises()
  return { omo, server, session, w: wrapper }
}

const openSelected = (list: UserTerminal[], id: string) => open(list, id)

test('a row shows the title and folder as text, the state in words and the hold note', async () => {
  const { w } = await open([
    shell({ id: 'a', title: 'Fix <b>it</b>‮x', dir: '/home/me/very/long/path/to/some/deep/folder/with/many/parts/worktrees/demo/12', ticket: 12 }),
    shell({ id: 'b', title: 'old', state: 'exited', started_at: '2026-11-15T09:00:00Z', ended_at: '2026-11-15T09:30:00Z', ticket: 7 }),
  ])
  const rows = w.findAll('[data-terminal-row]')
  expect(rows).toHaveLength(2)
  expect(rows[0]!.text()).toContain('Fix <b>it</b>\\u{202E}x')
  expect(rows[0]!.find('b').exists()).toBe(false)
  expect(rows[0]!.text()).toContain('running')
  expect(rows[0]!.text()).toContain('…')
  expect(rows[0]!.text()).toContain('Keeps the worktree of #12 until it is closed.')
  expect(rows[1]!.text()).toContain('exited')
  // An exited shell holds nothing.
  expect(rows[1]!.text()).not.toContain('Keeps the worktree')
  expect(w.text()).toContain('Your terminals keep running when the browser closes.')
})

test('a shell opened from a ticket shows at once, also when the answer comes before its event', async () => {
  const { w, server, omo, session } = await open([], null, Bound)
  server.quiet = true
  let gone = false
  const watcher = new MutationObserver(() => {
    if (w.element.querySelector('[data-terminal-gone]')) gone = true
  })
  watcher.observe(w.element, { childList: true, subtree: true })
  const action = createOpenTerminalAction({ closePanel: () => undefined })
  const t = ticket({ number: 12, column: 'in_progress', projects: [{ project: 'demo', base_branch: 'main', branch: 'omo/12', worktree: '/wt/demo/12', export_url: '', export_state: '' as const, export_error: '', export_remote: '', export_branch: '', export_base: '', export_commit: '', export_target_commit: '' }] })
  const ctx = { targets: {}, busy: false, client: session.client, store: session.store, ask: () => undefined } as unknown as ActionContext
  await action.run(t, ctx)
  await flushPromises()
  expect(useSidebar().selection.value).toBe('new')
  expect(w.find('[data-fake-terminal]').exists()).toBe(true)
  omo.emit('terminal.opened', { terminal: server.list[0] })
  await flushPromises()
  watcher.disconnect()
  expect(gone).toBe(false)
  expect(w.find('[data-terminal-gone]').exists()).toBe(false)
})

test('an empty list says so', async () => {
  const { w } = await open([])
  expect(w.get('[data-terminals-empty]').text()).toBe('No terminal is open.')
})

/** Opens the New terminal picker and returns its search field. */
async function picker(w: VueWrapper) {
  await w.get('button[aria-haspopup]').trigger('click')
  return w.get('input[role="combobox"]')
}
const pickLabels = (w: VueWrapper) => w.findAll('[role="option"] [data-pick-label]').map((o) => o.text())

test('New terminal searches the enabled projects that exist, with Home folder first, and opens a shell there', async () => {
  const { w, server } = await open([])
  const search = await picker(w)
  expect(document.activeElement).toBe(search.element)
  expect(pickLabels(w)).toEqual(['Home folder', 'Demo project'])
  expect(w.get('[data-pick-count]').text()).toBe('1 project')
  await search.setValue('/src/demo')
  expect(pickLabels(w)).toEqual(['Home folder', 'Demo project'])
  await search.setValue('nothing')
  expect(pickLabels(w)).toEqual(['Home folder'])
  expect(w.get('[data-pick-count]').text()).toBe('0 of 1 projects')
  await search.setValue('demo')
  await search.trigger('keydown', { key: 'ArrowDown' })
  await search.trigger('keydown', { key: 'Enter' })
  await flushPromises()
  expect(server.sent).toEqual(['POST /api/terminals {"project":"demo"}'])
  // The new shell is selected, like a row the user chose.
  expect(useSidebar().selection.value).toBe('new')
})

test('Home folder opens a shell in the home folder', async () => {
  const { w, server } = await open([])
  const search = await picker(w)
  await search.trigger('keydown', { key: 'Enter' })
  await flushPromises()
  expect(server.sent).toEqual(['POST /api/terminals {"home":true}'])
  expect(useSidebar().selection.value).toBe('new')
})

test('without a usable project, Home folder is still offered', async () => {
  const { w, omo } = await open([])
  omo.emit('project.removed', { id: 'demo' })
  await flushPromises()
  await picker(w)
  expect(pickLabels(w)).toEqual(['Home folder'])
})

test('the refusal of the server stays in the tab in its words', async () => {
  const { w, server } = await open([])
  server.refuse = new ApiError(409, 'conflict', '16 terminals are open; close one first.')
  let search = await picker(w)
  await search.trigger('keydown', { key: 'Enter' })
  await flushPromises()
  expect(w.get('[data-terminal-error]').text()).toBe('16 terminals are open; close one first.')

  server.refuse = new ApiError(503, 'unavailable', 'omo is stopping.')
  search = await picker(w)
  await search.setValue('demo')
  await search.trigger('keydown', { key: 'ArrowDown' })
  await search.trigger('keydown', { key: 'Enter' })
  await flushPromises()
  expect(w.get('[data-terminal-error]').text()).toBe('omo is stopping.')
})

test('a shell shows its terminal on its own path, with a link back to the ticket', async () => {
  const { w } = await openSelected([shell({ id: 'a/b', ticket: 12 })], 'a/b')
  expect(w.get('[data-fake-terminal]').attributes('data-fake-terminal')).toBe('/api/terminals/a%2Fb/socket')
  expect(w.get('[data-fake-terminal]').text()).toBe('The shell exited.')
  expect(w.get('[data-terminal-ticket]').attributes('href')).toBe('/ticket/12')
  expect(w.get('[data-terminal-hold]').text()).toBe('Keeps the worktree of #12 until it is closed.')
  expect(w.get('[data-terminal-agent-files]').text()).toBe("The files in this folder are written by #12's agents; check what a command runs before you run it here.")
})

test('a shell in a project folder has no note about agents', async () => {
  const { w } = await openSelected([shell({ id: 'p' })], 'p')
  expect(w.find('[data-fake-terminal]').exists()).toBe(true)
  expect(w.find('[data-terminal-agent-files]').exists()).toBe(false)
})

test('Back returns to the list; a shell closed elsewhere says so', async () => {
  const { w, omo } = await openSelected([shell({ id: 'a' })], 'a')
  omo.emit('terminal.closed', { terminal: shell({ id: 'a' }) })
  await flushPromises()
  expect(w.get('[data-terminal-gone]').text()).toBe('This terminal is closed.')
  await w.get('[data-terminal-back]').trigger('click')
  expect(useSidebar().selection.value).toBeNull()
})

test('closing a running shell asks first and sends nothing until confirmed', async () => {
  const { w, server } = await openSelected([shell({ id: 'a' })], 'a')
  expect(w.get('[data-terminal-close]').text()).toBe('Close…')
  await w.get('[data-terminal-close]').trigger('click')
  expect(w.get('[data-terminal-confirm]').text()).toContain('Close this terminal? The shell and everything running in it stop.')
  expect(server.sent).toEqual([])

  // Keeping it open sends nothing either.
  await w.findAll('[data-terminal-confirm] button').find((b) => b.text() === 'Keep it open')!.trigger('click')
  expect(w.find('[data-terminal-confirm]').exists()).toBe(false)
  expect(server.sent).toEqual([])

  await w.get('[data-terminal-close]').trigger('click')
  await w.get('[data-terminal-confirm-yes]').trigger('click')
  await flushPromises()
  expect(server.sent).toEqual(['DELETE /api/terminals/a null'])
})

test('an exited shell is removed at once, without asking', async () => {
  const { w, server } = await openSelected([shell({ id: 'a', state: 'exited', ended_at: '2026-11-15T10:05:00Z' })], 'a')
  expect(w.get('[data-terminal-close]').text()).toBe('Remove')
  await w.get('[data-terminal-close]').trigger('click')
  await flushPromises()
  expect(w.find('[data-terminal-confirm]').exists()).toBe(false)
  expect(server.sent).toEqual(['DELETE /api/terminals/a null'])
})

test('a refused close keeps the shell and shows the reason', async () => {
  const { w, server } = await openSelected([shell({ id: 'a' })], 'a')
  server.refuse = new ApiError(404, 'not_found', 'No such terminal.')
  await w.get('[data-terminal-close]').trigger('click')
  await w.get('[data-terminal-confirm-yes]').trigger('click')
  await flushPromises()
  expect(w.get('[data-terminal-error]').text()).toBe('No such terminal.')
  expect(w.find('[data-fake-terminal]').exists()).toBe(true)
})
