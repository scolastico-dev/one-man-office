import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, expect, test } from 'vitest'
import { ticket } from '../board/testing'
import { project } from '../../testing/fakeOmo'
import type { SchedulerState } from '../../api/types'
import AgentsTab from './AgentsTab.vue'
import { agent, emitAgent, mountSidebar, sidebarHarness } from './sidebarTesting'
import { resetSidebar, useSidebar } from './useSidebar'

let mounted: VueWrapper | null = null
beforeEach(() => {
  resetSidebar()
  localStorage.clear()
})
afterEach(() => {
  mounted?.unmount()
  mounted = null
})

async function open(over: { scheduler?: SchedulerState } = {}) {
  const hn = await sidebarHarness((omo) => {
    omo.projects = [project('demo', 'Demo project')]
    omo.tickets = [ticket({ number: 12, title: 'Fix <b>login</b>', column: 'in_progress' })]
  })
  hn.agents.list = [
    agent({ id: 'a', ticket: 12, project: 'demo', name: 'Ada‮lovelace', model: 'claude-sonnet-4-6', state: 'running' }),
    agent({ id: 'b', role: 'reviewer', ticket: 12, name: 'Bo', state: 'waiting', review_round: 2, started_at: '2026-11-15T09:00:00Z' }),
  ]
  if (over.scheduler) hn.scheduler.state = over.scheduler
  const { wrapper, router } = await mountSidebar(hn, AgentsTab)
  mounted = wrapper
  await flushPromises()
  return { hn, w: wrapper, router }
}

test('the load reads "2 of 4 tasks · 1 of 2 stories" in work and says paused', async () => {
  const { w, hn } = await open({ scheduler: { tasks: { in_work: 2, limit: 4 }, stories: { in_work: 1, limit: 2 }, paused: false } })
  expect(w.get('[data-slots]').text()).toBe('In work: 2 of 4 tasks · 1 of 2 stories')
  hn.omo.emit('scheduler.changed', { state: { tasks: { in_work: 5, limit: 4 }, stories: { in_work: 0, limit: 2 }, paused: true } })
  await flushPromises()
  expect(w.get('[data-slots]').text()).toBe('In work: 5 of 4 tasks · 0 of 2 stories · Paused')
  expect(w.text()).toContain('A ticket holds its slot from In Progress until Done.')
  expect(w.text()).not.toContain('omo ai wait')
})

test('a row shows role, name, state, ticket, project, model and time as plain text', async () => {
  const { w } = await open()
  const rows = w.findAll('[data-agent-row]')
  expect(rows).toHaveLength(2)
  const a = rows[0]!
  expect(a.text()).toContain('developer')
  // The right-to-left override in the name is written out, not obeyed.
  expect(a.text()).toContain('Ada\\u{202E}lovelace')
  expect(a.text()).toContain('#12 Fix <b>login</b>')
  expect(a.text()).toContain('Demo project · claude-sonnet-4-6')
  expect(a.find('b').exists()).toBe(false)
  expect(a.find('time').exists()).toBe(true)
  // The parked reviewer says so in the card's words, and its round.
  const b = rows[1]!
  expect(b.text()).toContain('parked')
  expect(b.text()).toContain('review round 2')
  expect(b.element.tagName).toBe('BUTTON')
  // A reviewer parked after its verdict is kept for questions; a working agent is not.
  expect(b.get('[data-kept]').text()).toBe('Available for questions about its review until the next review round.')
  expect(a.find('[data-kept]').exists()).toBe(false)
})

test('a row opens the agent with a way back and a link to its ticket; an ended agent says so', async () => {
  const { w, hn } = await open()
  await w.get('[data-session="a"]').trigger('click')
  expect(useSidebar().selection.value).toBe('a')
  expect(w.find('[data-agent-row]').exists()).toBe(false)
  expect(w.get('[data-agent-ticket]').attributes('href')).toBe('/ticket/12')
  expect(w.get('[data-agent-ticket]').text()).toBe('Open #12')
  expect(w.text()).toContain('Typing here types into the agent.')

  emitAgent(hn.omo, 'agent.ended', agent({ id: 'a', ticket: 12, state: 'ended' }))
  await flushPromises()
  expect(w.get('[data-agent-ended]').text()).toBe('This agent ended.')
  expect(w.text()).not.toContain('Typing here')

  await w.get('[data-agent-back]').trigger('click')
  await flushPromises()
  expect(useSidebar().selection.value).toBeNull()
  expect(w.findAll('[data-agent-row]')).toHaveLength(1)
})

test('an empty list says no agent is running, and a failed read shows the server words', async () => {
  const hn = await sidebarHarness()
  hn.agents.fail = 'omo cannot list agents'
  const { wrapper } = await mountSidebar(hn, AgentsTab)
  mounted = wrapper
  await flushPromises()
  expect(wrapper.get('[data-agents-error]').text()).toBe('omo cannot list agents')
  hn.agents.fail = ''
  hn.omo.stream!.onResync(hn.omo.eventId)
  await flushPromises()
  expect(wrapper.find('[data-agents-error]').exists()).toBe(false)
  expect(wrapper.get('[data-agents-empty]').text()).toBe('No agent is running.')
})

test('an agent that used omo’s MCP endpoint carries a small MCP chip; the others do not', async () => {
  const hn = await sidebarHarness((omo) => {
    omo.projects = [project('demo', 'Demo project')]
    omo.tickets = [ticket({ number: 12, title: 'Fix login', column: 'in_progress' })]
  })
  hn.agents.list = [agent({ id: 'a', ticket: 12, name: 'Ada', state: 'running', mcp: 'connected' }), agent({ id: 'b', ticket: 12, name: 'Bo', state: 'running' })]
  const { wrapper } = await mountSidebar(hn, AgentsTab)
  mounted = wrapper
  await flushPromises()
  const rows = wrapper.findAll('[data-agent-row]')
  expect(rows[0]!.get('[data-agent-mcp]').text()).toBe('MCP')
  expect(rows[0]!.get('[data-agent-mcp]').attributes('title')).toBe("This agent uses omo's MCP tools")
  expect(rows[1]!.find('[data-agent-mcp]').exists()).toBe(false)
})
