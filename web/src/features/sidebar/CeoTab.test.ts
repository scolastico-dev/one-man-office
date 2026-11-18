import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { defineComponent, h } from 'vue'
import { ApiError } from '../../api/client'
import type { AgentSession } from '../../api/types'
import CeoTab from './CeoTab.vue'
import { visibleTabs } from './sidebarTabs'
import { agent, emitAgent, mountSidebar, sidebarHarness, type SidebarHarness } from './sidebarTesting'
import { resetSidebar, useSidebar } from './useSidebar'

// The emulator is not part of this test (it would open a socket): a stand-in that shows what it was given.
vi.mock('../terminal/LazyTerminal', () => ({
  LazyTerminal: defineComponent({
    props: { src: { type: String, required: true }, label: { type: String, required: true } },
    setup: (p) => () => h('div', { 'data-fake-terminal': p.src, 'aria-label': p.label }),
  }),
}))

let mounted: VueWrapper | null = null
beforeEach(() => {
  resetSidebar()
  localStorage.clear()
})
afterEach(() => {
  mounted?.unmount()
  mounted = null
  vi.useRealTimers()
  document.body.innerHTML = ''
})

const ceo = (over: Partial<AgentSession> = {}) => agent({ id: 'c1', role: 'ceo', name: 'Cleo', terminal_id: 'tc1', ...over })

interface Sent {
  method: string
  path: string
  body: unknown
}

async function open(how: 'user' | 'restored', live: AgentSession[] = [], stopped = false) {
  const hn: SidebarHarness = await sidebarHarness()
  hn.agents.list = live
  hn.ceo.state = { stopped }
  const sent: Sent[] = []
  const answers: {
    ceo: () => Promise<AgentSession>
    fresh: () => Promise<AgentSession>
  } = {
    ceo: async () => ceo(),
    fresh: async () => ceo({ id: 'c2', terminal_id: 'tc2' }),
  }
  const client = hn.session.client as unknown as {
    send: (m: string, p: string, b?: unknown) => Promise<unknown>
  }
  client.send = async (method, path, body) => {
    sent.push({ method, path, body })
    if (path === '/api/ceo/stop') return { data: { stopped: true }, eventId: null }
    if (method === 'PUT' && path === '/api/roles/ceo') return { data: { ...(body as object), revision: 'r2' }, eventId: null }
    const run = path === '/api/ceo' ? answers.ceo : path === '/api/ceo/new-session' ? answers.fresh : null
    if (!run) throw new Error(`unexpected ${path}`)
    return { data: await run(), eventId: null }
  }
  // The user's choice of the tab is the event that may start the CEO.
  if (how === 'user') useSidebar().setTab('ceo', 'user')
  const { wrapper } = await mountSidebar(hn, CeoTab)
  mounted = wrapper
  await flushPromises()
  const remount = async () => {
    wrapper.unmount()
    mounted = (await mountSidebar(hn, CeoTab)).wrapper
    await flushPromises()
    return mounted
  }
  return { hn, w: wrapper, sent, answers, remount }
}

test('a restored CEO tab sends no request; a click sends POST /api/ceo once', async () => {
  const { w, sent } = await open('restored')
  expect(sent).toEqual([])
  expect(w.text()).toContain('The CEO is not running.')
  const start = w.findAll('button').find((b) => b.text() === 'Start the CEO')!
  void start.trigger('click')
  void start.trigger('click')
  await flushPromises()
  expect(sent).toEqual([{ method: 'POST', path: '/api/ceo', body: {} }])
  expect(w.find('[data-ceo-live]').exists()).toBe(true)
  expect(w.text()).toContain('Write to the CEO in the box below the terminal.')
})

test('a tab the user opened starts the CEO by itself, once the live agents are known', async () => {
  const { sent } = await open('user')
  expect(sent).toEqual([{ method: 'POST', path: '/api/ceo', body: {} }])
})

test('the tab mounted again (collapse and expand, narrow and wide) never starts the CEO again', async () => {
  const { hn, sent, remount } = await open('user')
  expect(sent).toHaveLength(1)
  emitAgent(hn.omo, 'agent.ended', ceo({ state: 'ended' }))
  await flushPromises()
  expect(useSidebar().how.value).toBe('user')
  const again = await remount()
  expect(sent).toHaveLength(1)
  expect(again.text()).toContain('The CEO is not running.')
  // Choosing the tab again is a new choice.
  useSidebar().setTab('agents', 'user')
  useSidebar().setTab('ceo', 'user')
  await flushPromises()
  expect(sent).toHaveLength(2)
})

test('a choice the tab did not see while it was hidden is forgotten', async () => {
  const hn = await sidebarHarness()
  useSidebar().setTab('ceo', 'user')
  useSidebar().hide()
  const sent: string[] = []
  ;(hn.session.client as unknown as { send: (m: string, p: string) => Promise<unknown> }).send = async (_m, p) => {
    sent.push(p)
    return { data: ceo(), eventId: null }
  }
  mounted = (await mountSidebar(hn, CeoTab)).wrapper
  await flushPromises()
  expect(sent).toEqual([])
})

test('a live CEO is attached without a request', async () => {
  const { w, sent } = await open('user', [ceo()])
  expect(sent).toEqual([])
  expect(w.find('[data-ceo-live]').exists()).toBe(true)
})

test('a failure shows the server message and offers Try again', async () => {
  const { w, sent, answers } = await open('restored')
  answers.ceo = async () => {
    throw new ApiError(409, 'conflict', 'The CEO has no model assigned.')
  }
  await w
    .findAll('button')
    .find((b) => b.text() === 'Start the CEO')!
    .trigger('click')
  await flushPromises()
  expect(w.get('[role="alert"]').text()).toBe('The CEO has no model assigned.')
  answers.ceo = async () => ceo()
  await w
    .findAll('button')
    .find((b) => b.text() === 'Try again')!
    .trigger('click')
  await flushPromises()
  expect(sent).toHaveLength(2)
  expect(w.find('[data-ceo-live]').exists()).toBe(true)
})

test('a slow start says so after 400 ms', async () => {
  vi.useFakeTimers()
  const { w, answers } = await open('restored')
  let release: (s: AgentSession) => void = () => {}
  answers.ceo = () => new Promise((r) => (release = r))
  await w
    .findAll('button')
    .find((b) => b.text() === 'Start the CEO')!
    .trigger('click')
  expect(w.text()).not.toContain('Starting the CEO')
  await vi.advanceTimersByTimeAsync(450)
  expect(w.text()).toContain('Starting the CEO…')
  release(ceo())
  await flushPromises()
  expect(w.text()).not.toContain('Starting the CEO')
})

test('when the CEO ends the tab says so and offers Start', async () => {
  const { w, hn, sent } = await open('user', [ceo()])
  emitAgent(hn.omo, 'agent.ended', ceo({ state: 'ended' }))
  await flushPromises()
  expect(w.text()).toContain('The CEO session ended.')
  await w
    .findAll('button')
    .find((b) => b.text() === 'Start the CEO')!
    .trigger('click')
  await flushPromises()
  expect(sent.map((s) => s.path)).toEqual(['/api/ceo'])
})

test('New session asks first and then sends new-session', async () => {
  const { w, hn, sent } = await open('user', [ceo()])
  await w
    .findAll('button')
    .find((b) => b.text() === 'New session…')!
    .trigger('click')
  await flushPromises()
  expect(sent).toEqual([])
  const dialog = document.querySelector('dialog')!
  expect(dialog.textContent).toContain("End the CEO's current session and start a fresh one? The new CEO reads the open tickets it supervises.")
  const confirm = [...dialog.querySelectorAll('button')].find((b) => b.textContent === 'Start a new session')!
  confirm.click()
  // The old session ends while the request is under way: no "ended" flash.
  emitAgent(hn.omo, 'agent.ended', ceo({ state: 'ended' }))
  await flushPromises()
  expect(sent).toEqual([{ method: 'POST', path: '/api/ceo/new-session', body: {} }])
  expect(w.text()).not.toContain('The CEO session ended.')
  expect(w.find('[data-ceo-live]').exists()).toBe(true)
})

test('declining New session sends nothing', async () => {
  const { w, sent } = await open('user', [ceo()])
  await w
    .findAll('button')
    .find((b) => b.text() === 'New session…')!
    .trigger('click')
  await flushPromises()
  const keep = [...document.querySelectorAll('dialog button')].find((b) => b.textContent === 'Keep the current session') as HTMLElement
  keep.click()
  await flushPromises()
  expect(sent).toEqual([])
})

test('the tab disappears when the CEO is switched off', () => {
  expect(visibleTabs({ ceoEnabled: true }).map((t) => t.id)).toContain('ceo')
  expect(visibleTabs({ ceoEnabled: false }).map((t) => t.id)).not.toContain('ceo')
})

const button = (w: VueWrapper, text: string) => w.findAll('button').find((b) => b.text() === text)!
const dialogButton = (text: string) => [...document.querySelectorAll('dialog[open] button')].find((b) => b.textContent === text) as HTMLElement

test('a stopped CEO is not started by a choice of the tab; its Start button starts it', async () => {
  const { w, sent } = await open('user', [], true)
  expect(sent).toEqual([])
  expect(w.get('[data-ceo-stopped]').text()).toContain('The CEO is stopped.')
  await button(w, 'Start the CEO').trigger('click')
  await flushPromises()
  expect(sent).toEqual([{ method: 'POST', path: '/api/ceo', body: {} }])
  expect(w.find('[data-ceo-stopped]').exists()).toBe(false)
  expect(w.find('[data-ceo-live]').exists()).toBe(true)
})

test('a restored tab says the CEO is stopped', async () => {
  const { w, sent } = await open('restored', [], true)
  expect(sent).toEqual([])
  expect(w.find('[data-ceo-stopped]').exists()).toBe(true)
})

test('Stop asks first, then stops the CEO and shows it stopped; choosing the tab again starts nothing', async () => {
  const { w, hn, sent } = await open('user', [ceo()])
  await button(w, 'Stop…').trigger('click')
  await flushPromises()
  expect(sent).toEqual([])
  dialogButton('Keep it running').click()
  await flushPromises()
  expect(sent).toEqual([])
  await button(w, 'Stop…').trigger('click')
  await flushPromises()
  dialogButton('Stop the CEO').click()
  emitAgent(hn.omo, 'agent.ended', ceo({ state: 'ended', end_reason: 'stopped by the user' }))
  hn.ceo.state = { stopped: true }
  await flushPromises()
  expect(sent).toEqual([{ method: 'POST', path: '/api/ceo/stop', body: {} }])
  expect(w.find('[data-ceo-stopped]').exists()).toBe(true)
  expect(w.find('[data-ceo-live]').exists()).toBe(false)
  useSidebar().setTab('agents', 'user')
  useSidebar().setTab('ceo', 'user')
  await flushPromises()
  expect(sent).toHaveLength(1)
})

test('choosing a model saves it as the CEO role’s only model; a running CEO offers Restart now', async () => {
  const { w, sent } = await open('user', [ceo({ model: 'fake/default' })])
  const picker = w.get('[data-ceo-model] button')
  expect(picker.text()).toBe('Model: fake/default')
  await picker.trigger('click')
  await flushPromises()
  const option = w.findAll('[role="option"]').find((o) => o.text().includes('fake/strong'))!
  await option.trigger('click')
  await flushPromises()
  expect(sent).toEqual([{ method: 'PUT', path: '/api/roles/ceo', body: { name: 'ceo', models: ['fake/strong'], assignment: 'round_robin' } }])
  expect(w.get('[data-ceo-model] button').text()).toBe('Model: fake/strong')
  expect(w.get('[data-ceo-model-next]').text()).toContain('The CEO runs fake/default; fake/strong takes effect at its next start.')
  await button(w, 'Restart now').trigger('click')
  await flushPromises()
  expect(sent.map((s) => s.path)).toEqual(['/api/roles/ceo', '/api/ceo/new-session'])
})

test('without a running CEO a model choice says nothing about a restart', async () => {
  const { w, sent } = await open('restored')
  await w.get('[data-ceo-model] button').trigger('click')
  await flushPromises()
  await w
    .findAll('[role="option"]')
    .find((o) => o.text().includes('fake/strong'))!
    .trigger('click')
  await flushPromises()
  expect(sent.map((s) => `${s.method} ${s.path}`)).toEqual(['PUT /api/roles/ceo'])
  expect(w.find('[data-ceo-model-next]').exists()).toBe(false)
})

test('after a gap in the board connection the CEO role is read again, so the next choice carries its new revision', async () => {
  const { hn, w, sent } = await open('restored')
  // Settings, Roles changed the CEO's role while this page did not hear it.
  hn.ceo.roles = [{ name: 'ceo', models: ['fake/strong'], assignment: 'round_robin', revision: 'r5' }]
  hn.omo.stream!.onState('retrying', 1000)
  hn.omo.stream!.onState('live')
  await flushPromises()
  expect(w.get('[data-ceo-model] button').text()).toBe('Model: fake/strong')
  const client = hn.session.client as unknown as { send: (m: string, p: string, b?: unknown, o?: { ifMatch?: string }) => Promise<unknown> }
  const send = client.send
  const matches: (string | undefined)[] = []
  client.send = (m, p, b, o) => {
    matches.push(o?.ifMatch)
    return send(m, p, b, o)
  }
  await w.get('[data-ceo-model] button').trigger('click')
  await flushPromises()
  await w
    .findAll('[role="option"]')
    .find((o) => o.text().includes('fake/default'))!
    .trigger('click')
  await flushPromises()
  expect(sent.map((s) => `${s.method} ${s.path}`)).toEqual(['PUT /api/roles/ceo'])
  expect(matches).toEqual(['r5'])
})
