import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import { ApiError } from '../../api/client'
import type { AgentSession } from '../../api/types'
import CeoTab from './CeoTab.vue'
import { visibleTabs } from './sidebarTabs'
import { agent, emitAgent, mountSidebar, sidebarHarness, type SidebarHarness } from './sidebarTesting'
import { resetSidebar } from './useSidebar'

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

async function open(how: 'user' | 'restored', live: AgentSession[] = []) {
  const hn: SidebarHarness = await sidebarHarness()
  hn.agents.list = live
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
    const run = path === '/api/ceo' ? answers.ceo : path === '/api/ceo/new-session' ? answers.fresh : null
    if (!run) throw new Error(`unexpected ${path}`)
    return { data: await run(), eventId: null }
  }
  const { wrapper } = await mountSidebar(hn, CeoTab, { how })
  mounted = wrapper
  await flushPromises()
  return { hn, w: wrapper, sent, answers }
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
  expect(w.text()).toContain('Typing here types into the CEO.')
})

test('a tab the user opened starts the CEO by itself, once the live agents are known', async () => {
  const { sent } = await open('user')
  expect(sent).toEqual([{ method: 'POST', path: '/api/ceo', body: {} }])
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
