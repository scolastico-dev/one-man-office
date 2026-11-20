import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'
import type { AgentSession } from '../../../api/types'
import { ticket } from '../../board/testing'
import { project } from '../../../testing/fakeOmo'
import { agent, emitAgent } from '../../sidebar/sidebarTesting'
import { mountPanel, ok, panelHarness, type PanelHarness } from '../panelTesting'
import AgentsTab from './AgentsTab.vue'

vi.mock('../../terminal/LazyTerminal', () => ({
  LazyTerminal: { props: ['src', 'label', 'endedText'], template: '<div data-terminal :data-src="src" :aria-label="label" />' },
}))
vi.mock('../../terminal/LogView.vue', () => ({
  default: { props: ['sessionId'], template: '<div data-log-view :data-id="sessionId" />' },
}))

let mounted: VueWrapper | null = null
beforeEach(() => vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] }))
afterEach(() => {
  mounted?.unmount()
  mounted = null
  vi.useRealTimers()
})

const reads = (hn: PanelHarness, list: { value: AgentSession[] }, count: { n: number }) => {
  const get = hn.session.client.get
  hn.session.client.get = (async (path: string, q?: never) => {
    if (path === '/api/tickets/12/agents') {
      count.n++
      return ok(structuredClone(list.value))
    }
    return get(path, q)
  }) as typeof get
}

async function open(initial: AgentSession[]) {
  const hn = await panelHarness((omo) => {
    omo.projects = [project('demo')]
    omo.tickets = [ticket({ number: 12 })]
  })
  const list = { value: initial }
  const count = { n: 0 }
  reads(hn, list, count)
  const { wrapper } = await mountPanel(hn, AgentsTab, { ticket: hn.omo.tickets[0] })
  mounted = wrapper
  await flushPromises()
  return { hn, w: wrapper, list, count }
}

const running = agent({ id: 'r', ticket: 12, name: 'Ada', terminal_id: 't9', started_at: '2026-11-15T11:00:00Z' })
const ended = agent({ id: 'e', role: 'reviewer', ticket: 12, name: 'Bo', state: 'ended', end_reason: 'finished', started_at: '2026-11-15T09:00:00Z', terminal_id: undefined })

test('ended status and log control share their agent header, separate from the next agent', async () => {
  const { w } = await open([{ ...ended, started_at: '2026-11-15T12:00:00Z' }, running])
  const row = w.get('[data-session="e"] [data-agent-row]')
  const header = row.element.parentElement!
  expect(header.tagName).not.toBe('LI')
  expect(header.contains(w.get('[data-ended]').element)).toBe(true)
  expect(header.contains(w.get('[data-show-log]').element)).toBe(true)
  expect(header.contains(w.get('[data-session="r"] [data-agent-row]').element)).toBe(false)
})

test('agents tab: running agents get a terminal, ended ones a log; list follows agent events of this ticket only', async () => {
  const { hn, w, list, count } = await open([ended, running])
  expect(w.findAll('[data-agent-row]').map((r) => r.attributes('data-session'))).toEqual(['r', 'e'])

  // Running: the terminal opens in place, one at a time, with the note and a name.
  await w.get('[data-session="r"] [data-agent-row]').trigger('click')
  const term = w.get('[data-terminal]')
  expect(term.attributes('data-src')).toBe('/api/agents/t9/terminal')
  expect(term.attributes('aria-label')).toBe('Terminal of developer Ada on #12')
  expect(w.text()).toContain('Write to the agent in the box below the terminal.')

  // Ended: Show log replaces the open terminal with the log.
  await w.get('[data-show-log]').trigger('click')
  expect(w.find('[data-terminal]').exists()).toBe(false)
  expect(w.get('[data-log-view]').attributes('data-id')).toBe('e')
  expect(w.get('[data-ended]').text()).toBe('Ended (finished)')
  await w.get('[data-show-log]').trigger('click')
  expect(w.find('[data-log-view]').exists()).toBe(false)

  // Events of this ticket re-read once, debounced; others do nothing.
  const before = count.n
  list.value = [ended, { ...running, state: 'ended' }]
  emitAgent(hn.omo, 'agent.updated', { ...running, ticket: 99 })
  vi.advanceTimersByTime(500)
  expect(count.n).toBe(before)
  emitAgent(hn.omo, 'agent.updated', running)
  emitAgent(hn.omo, 'agent.ended', { ...running, state: 'ended' })
  vi.advanceTimersByTime(500)
  await flushPromises()
  expect(count.n).toBe(before + 1)
  expect(w.findAll('[data-show-log]')).toHaveLength(2)
})

test('agents tab: a resync reads the list again; the empty list says so', async () => {
  const { hn, w, list, count } = await open([])
  expect(w.get('[data-agents-empty]').text()).toBe('No agent has worked on #12 yet.')
  const before = count.n
  list.value = [running]
  hn.omo.stream!.onResync(hn.omo.eventId)
  await flushPromises()
  vi.advanceTimersByTime(500)
  await flushPromises()
  expect(count.n).toBeGreaterThan(before)
  expect(w.findAll('[data-agent-row]')).toHaveLength(1)
})

test('agents tab: names and reasons are text', async () => {
  const { w } = await open([{ ...ended, name: 'Bo<img src=x>', end_reason: '<b>x</b>' }])
  expect(w.find('img').exists()).toBe(false)
  expect(w.find('b').exists()).toBe(false)
  expect(w.get('[data-ended]').text()).toBe('Ended (<b>x</b>)')
})
