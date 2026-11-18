import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test, vi } from 'vitest'
import { nextTick } from 'vue'
import { createMemoryHistory } from 'vue-router'
import App from './App.vue'
import { createTokenStore } from './auth/token'
import { activeToasts, useToast } from './components/ui/useToast'
import { ticket } from './features/board/testing'
import { createAppRouter } from './router'
import { createSession, SESSION } from './session'
import { FakeOmo, MemoryStorage, project } from './testing/fakeOmo'

const TOKEN = 'a'.repeat(64)
const OTHER = 'b'.repeat(64)
const text = (s: string) => s.replace(/\s+/g, ' ').trim()

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
  for (const t of [...activeToasts]) useToast().dismiss(t.id)
})

async function boot(omo: FakeOmo, opts: { path?: string; stored?: string } = {}) {
  const storage = new MemoryStorage()
  if (opts.stored) storage.setItem('omo.token', opts.stored)
  const tokens = createTokenStore(() => storage)
  const notices: string[] = []
  const router = createAppRouter({ history: createMemoryHistory(), onUnknownPath: (p) => notices.push(p) })
  await router.push(opts.path ?? '/')
  await router.isReady()
  const session = createSession({ tokens, makeClient: omo.makeClient, connect: omo.connect })
  const wrapper = mount(App, { attachTo: document.body, global: { plugins: [router], provide: { [SESSION as symbol]: session } } })
  mounted = wrapper
  await flushPromises()
  return { wrapper, router, session, storage, notices }
}

const headings = (w: VueWrapper) => w.findAll('[data-board] h2').map((h) => text(h.text()))
const cardsIn = (w: VueWrapper, column: string) =>
  w.findAll(`[data-board] [data-drop-column="${column}"] [data-ticket]`).map((c) => Number(c.attributes('data-ticket')))

function omoWithBoard(): FakeOmo {
  const omo = new FakeOmo()
  omo.projects = [project('ui', 'acme-corp/ui')]
  omo.tickets = [
    ticket({ number: 1, type: 'story', title: 'Story one', rank: 1 }),
    ticket({ number: 2, title: 'Task two', rank: 2, story: 1, projects: [{ project: 'ui', base_branch: 'main', branch: '', worktree: '', export_url: '', export_state: '' as const, export_error: '' }] }),
    ticket({ number: 3, title: 'Ready task', column: 'ready', rank: 1 }),
    ticket({ number: 4, title: 'Closed', column: 'done', closed_at: '2026-11-14T10:00:00Z' }),
  ]
  return omo
}

test('without a token and a 401 the locked screen is shown; pasting the printed address unlocks and loads the board', async () => {
  const omo = omoWithBoard()
  omo.token = TOKEN
  const { wrapper, storage } = await boot(omo)

  expect(wrapper.find('[data-board]').exists()).toBe(false)
  expect(wrapper.get('h1').text()).toBe('omo is locked')
  expect(wrapper.find('[data-rejected]').exists()).toBe(false)

  const input = wrapper.get('input')
  await input.setValue('not an address')
  await wrapper.get('form').trigger('submit')
  expect(wrapper.text()).toContain('That is not an omo access address or token.')

  await input.setValue(`http://omo.localhost:4100/#${TOKEN}`)
  await wrapper.get('form').trigger('submit')
  await flushPromises()

  expect(storage.getItem('omo.token')).toBe(TOKEN)
  expect(headings(wrapper)).toEqual(['Planning 2', 'Ready For Dev 1', 'In Progress 0', 'Review 0', 'Done 1'])
})

test('a rejected stored token shows the rotate hint', async () => {
  const omo = omoWithBoard()
  omo.token = TOKEN
  const { wrapper, storage } = await boot(omo, { stored: OTHER })

  expect(wrapper.get('h1').text()).toBe('omo is locked')
  expect(wrapper.get('[data-rejected]').text()).toContain('omo config rotate-token')
  expect(storage.getItem('omo.token')).toBeNull()
})

test('renders five columns from the snapshot with counts', async () => {
  const { wrapper } = await boot(omoWithBoard())
  expect(headings(wrapper)).toEqual(['Planning 2', 'Ready For Dev 1', 'In Progress 0', 'Review 0', 'Done 1'])
  expect(cardsIn(wrapper, 'planning')).toEqual([1, 2])
  const task = wrapper.get('[data-board] [data-ticket="2"]')
  expect(task.text()).toContain('acme-corp/ui')
  expect(task.text()).toContain('#1 Story one')
  expect(wrapper.get('h1').text()).toBe('Board')
})

test('a ticket.moved event moves the card without a reload', async () => {
  const omo = omoWithBoard()
  const { wrapper } = await boot(omo)
  omo.stream!.onState('live')
  await flushPromises()
  const reads = omo.reads.length

  omo.change('ticket.moved', ticket({ ...omo.tickets[1]!, column: 'ready', rank: 0 }))
  await flushPromises()

  expect(cardsIn(wrapper, 'planning')).toEqual([1])
  expect(cardsIn(wrapper, 'ready')).toEqual([2, 3])
  expect(omo.reads.length).toBe(reads)
})

test('Done shows "Load older" while there are older tickets and appends them', async () => {
  const omo = new FakeOmo()
  omo.donePage = 2
  for (let n = 1; n <= 3; n++) omo.tickets.push(ticket({ number: n, column: 'done', closed_at: `2026-11-1${n}T10:00:00Z` }))
  const { wrapper } = await boot(omo)

  expect(cardsIn(wrapper, 'done')).toEqual([3, 2])
  await wrapper.get('[data-drop-column="done"] [data-load-more]').trigger('click')
  await flushPromises()

  expect(cardsIn(wrapper, 'done')).toEqual([3, 2, 1])
  expect(wrapper.find('[data-drop-column="done"] [data-load-more]').exists()).toBe(false)
})

test('connection status shows "reconnecting in 3 s" and Retry now reconnects', async () => {
  const omo = omoWithBoard()
  const { wrapper } = await boot(omo)
  const status = () => wrapper.get('[data-connection]')
  expect(text(status().text())).toBe('connecting…')

  omo.stream!.onState('live')
  await flushPromises()
  expect(text(status().text())).toBe('live')

  omo.stream!.onState('retrying', 3000)
  await flushPromises()
  expect(text(status().text())).toBe('reconnecting in 3 s')
  expect(status().get('button').attributes('aria-label')).toBe('Retry now')
  expect(wrapper.get('[data-announcer="polite"]').text()).toBe('Connection to omo lost. Reconnecting.')
  // The cards stay while the board may be stale.
  expect(cardsIn(wrapper, 'planning')).toEqual([1, 2])

  await status().get('button').trigger('click')
  expect(omo.reconnects).toBe(1)

  omo.stream!.onState('live')
  await flushPromises()
  expect(text(status().text())).toBe('live')
  expect(wrapper.get('[data-announcer="polite"]').text()).toBe('The board is live again.')
})

test('a resync keeps every card shown once and catches up without a reload', async () => {
  const omo = omoWithBoard()
  const { wrapper } = await boot(omo)
  omo.stream!.onState('live')
  // Changes the board missed, then the server says to resync.
  omo.tickets.push(ticket({ number: 5, title: 'Missed', rank: 3 }))
  omo.eventId += 600
  omo.stream!.onResync(omo.eventId)
  await nextTick()
  expect(text(wrapper.get('[data-connection]').text())).toBe('catching up…')
  expect(cardsIn(wrapper, 'planning')).toEqual([1, 2])
  await flushPromises()

  expect(cardsIn(wrapper, 'planning')).toEqual([1, 2, 5])
  expect(wrapper.findAll('[data-board] [data-ticket]').map((c) => c.attributes('data-ticket'))).toEqual(['1', '2', '5', '3', '4'])
  expect(text(wrapper.get('[data-connection]').text())).toBe('live')
})

test('/123 opens the ticket panel over the board and Escape returns to / keeping the query', async () => {
  const omo = omoWithBoard()
  omo.tickets.push(ticket({ number: 123, title: 'Deep link', rank: 9 }))
  const { wrapper, router } = await boot(omo, { path: '/123?project=ui' })

  expect(wrapper.find('[data-board]').exists()).toBe(true)
  const panel = wrapper.get('dialog[data-ticket-panel]')
  expect((panel.element as HTMLDialogElement).open).toBe(true)
  expect(panel.text()).toContain('Deep link')

  await panel.trigger('keydown', { key: 'Escape' })
  await flushPromises()
  expect(router.currentRoute.value.fullPath).toBe('/?project=ui')
  expect(wrapper.find('dialog[data-ticket-panel]').exists()).toBe(false)
})

test('opening a card goes to its address and back closes it', async () => {
  const omo = omoWithBoard()
  const { wrapper, router } = await boot(omo)
  await wrapper.get('[data-ticket="3"] a').trigger('click', { button: 0 })
  await flushPromises()
  expect(router.currentRoute.value.fullPath).toBe('/3')
  expect(wrapper.get('dialog[data-ticket-panel]').text()).toContain('Ready task')

  router.back()
  await flushPromises()
  await new Promise((r) => setTimeout(r, 0))
  await flushPromises()
  expect(router.currentRoute.value.fullPath).toBe('/')
  expect(wrapper.find('dialog[data-ticket-panel]').exists()).toBe(false)
})

test('an unknown address shows the board and says so', async () => {
  const { wrapper, router, notices } = await boot(omoWithBoard(), { path: '/nope/here?q=x' })
  expect(router.currentRoute.value.fullPath).toBe('/?q=x')
  expect(notices).toEqual(['/nope/here'])
  expect(wrapper.find('[data-board]').exists()).toBe(true)
})

test('an omo that cannot be reached shows a page with Try again, which loads the board', async () => {
  const omo = omoWithBoard()
  omo.down = true
  const { wrapper } = await boot(omo)
  expect(wrapper.find('[data-board]').exists()).toBe(false)
  expect(wrapper.get('h1').text()).toBe('The board could not be loaded')
  expect(wrapper.get('[data-problem]').text()).toContain('does not answer')

  omo.down = false
  await wrapper.get('button').trigger('click')
  await flushPromises()
  expect(headings(wrapper)[0]).toBe('Planning 2')
})

test('an empty board shows the first steps', async () => {
  const { wrapper } = await boot(new FakeOmo())
  expect(wrapper.find('[data-board]').exists()).toBe(true)
  expect(wrapper.text()).toContain('Add a project')
})

test('a lock keeps the open settings page with its draft under the lock screen, inert and hidden; unlocking shows it again', async () => {
  const omo = omoWithBoard()
  omo.token = TOKEN
  const { wrapper, session } = await boot(omo, { stored: TOKEN, path: '/settings/limits' })
  await vi.dynamicImportSettled()
  await flushPromises()
  const field = () => wrapper.get('input[data-field="limits.tasks"]')
  await field().setValue('9')
  await flushPromises()

  // The token is rotated: the next request is refused and the app locks.
  omo.token = OTHER
  await session.client.get('/api/server').catch(() => undefined)
  await flushPromises()
  expect(session.phase.value).toBe('locked')
  expect(wrapper.get('main.locked h1').text()).toBe('omo is locked')
  const kept = wrapper.get('[data-board-kept]')
  expect(kept.attributes('inert')).toBeDefined()
  expect(kept.attributes('aria-hidden')).toBe('true')
  expect((kept.element as HTMLElement).style.display).toBe('none')
  expect((field().element as HTMLInputElement).value).toBe('9')
  // Nothing of the board listens to omo meanwhile, and its keys do nothing.
  expect(omo.closes).toBeGreaterThan(0)
  document.body.dispatchEvent(new KeyboardEvent('keydown', { key: 'c', bubbles: true }))
  await flushPromises()

  await wrapper.get('main.locked input').setValue(OTHER)
  await wrapper.get('main form').trigger('submit')
  await flushPromises()
  expect(session.phase.value).toBe('ready')
  const shown = wrapper.get('[data-board-kept]')
  expect(shown.attributes('inert')).toBeUndefined()
  expect(shown.attributes('aria-hidden')).toBeUndefined()
  expect((shown.element as HTMLElement).style.display).toBe('')
  expect((field().element as HTMLInputElement).value).toBe('9')
  expect(wrapper.text()).toContain('Unsaved changes')
  expect(document.querySelector('dialog[open]')).toBeNull()
})
