import { flushPromises } from '@vue/test-utils'
import { effectScope } from 'vue'
import { afterEach, expect, test, vi } from 'vitest'
import type { ApiClient } from '../../api/client'
import { createAgents, REREAD_DELAY_MS, type Agents } from './useAgents'
import { agent, emitAgent, sidebarHarness } from './sidebarTesting'

let stop: (() => void) | null = null
afterEach(() => {
  stop?.()
  stop = null
  vi.useRealTimers()
})

async function open(setup?: Parameters<typeof sidebarHarness>[0]) {
  const hn = await sidebarHarness(setup)
  const shared = createAgents({ client: hn.session.client, store: hn.session.store })
  const scope = effectScope()
  let agents!: Agents
  let release = () => {}
  scope.run(() => {
    release = shared.acquire()
    agents = shared.agents
  })
  stop = () => {
    release()
    scope.stop()
  }
  await flushPromises()
  return { hn, agents }
}

const ids = (a: Agents) => a.sessions.value.map((s) => s.id)

test('agents list follows agent events and scheduler.changed', async () => {
  const { hn, agents } = await open()
  hn.agents.list = [agent({ id: 'a', started_at: '2026-11-15T10:00:00Z' })]
  hn.scheduler.state = { running: 1, limit: 4, paused: false }
  agents.refresh()
  await flushPromises()
  expect(ids(agents)).toEqual(['a'])
  expect(agents.scheduler.value).toEqual({ running: 1, limit: 4, paused: false })
  const reads = hn.reads.length

  // A new agent shows from the event alone, newest first.
  emitAgent(hn.omo, 'agent.created', agent({ id: 'b', state: 'starting', started_at: '2026-11-15T11:00:00Z', terminal_id: 'tb' }))
  expect(ids(agents)).toEqual(['b', 'a'])
  expect(agents.sessions.value[0]!.state).toBe('starting')
  emitAgent(hn.omo, 'agent.updated', agent({ id: 'b', state: 'waiting', started_at: '2026-11-15T11:00:00Z', terminal_id: 'tb' }))
  expect(agents.sessions.value[0]!.state).toBe('waiting')

  // An ended agent leaves at once, whether the event is `ended` or an update to the ended state.
  emitAgent(hn.omo, 'agent.ended', agent({ id: 'a', state: 'ended' }))
  expect(ids(agents)).toEqual(['b'])
  emitAgent(hn.omo, 'agent.updated', agent({ id: 'b', state: 'ended' }))
  expect(ids(agents)).toEqual([])

  hn.omo.emit('scheduler.changed', { state: { running: 3, limit: 4, paused: true } })
  expect(agents.scheduler.value).toEqual({ running: 3, limit: 4, paused: true })
  // Nothing was read for any of it: the events carried the data.
  expect(hn.reads.length).toBe(reads)
})

test('an agent whose terminal the stream did not name is read again once, and its terminal is kept', async () => {
  vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout'] })
  const { hn, agents } = await open()
  const reads = hn.reads.length
  hn.agents.list = [agent({ id: 'n', terminal_id: 'tn' })]
  emitAgent(hn.omo, 'agent.created', agent({ id: 'n', terminal_id: undefined as unknown as string }))
  emitAgent(hn.omo, 'agent.updated', agent({ id: 'n', terminal_id: undefined as unknown as string }))
  expect(agents.sessions.value[0]!.terminal_id).toBeUndefined()
  await vi.advanceTimersByTimeAsync(REREAD_DELAY_MS)
  await flushPromises()
  expect(hn.reads.length).toBe(reads + 1)
  expect(agents.sessions.value[0]!.terminal_id).toBe('tn')
  // A later update without the terminal keeps the one read.
  emitAgent(hn.omo, 'agent.updated', agent({ id: 'n', terminal_id: undefined as unknown as string, state: 'waiting' }))
  expect(agents.sessions.value[0]).toMatchObject({ state: 'waiting', terminal_id: 'tn' })
})

test('an agent that ends while the list is being read does not come back', async () => {
  const { hn, agents } = await open()
  hn.agents.list = [agent({ id: 'x' }), agent({ id: 'y', started_at: '2026-11-15T09:00:00Z' })]
  agents.refresh()
  // The answer is old by the time it arrives: x ended and z started since.
  emitAgent(hn.omo, 'agent.ended', agent({ id: 'x', state: 'ended' }))
  emitAgent(hn.omo, 'agent.created', agent({ id: 'z', started_at: '2026-11-15T12:00:00Z' }))
  await flushPromises()
  expect(ids(agents)).toEqual(['z', 'y'])
})

test('a resync reads both lists again; an error is kept until a read works', async () => {
  const { hn, agents } = await open()
  hn.agents.list = [agent({ id: 'k' })]
  hn.scheduler.state = { running: 1, limit: 2, paused: false }
  // The store reads its snapshot, and the sidebar reads its lists once that is installed.
  hn.omo.stream!.onResync(hn.omo.eventId)
  await flushPromises()
  expect(ids(agents)).toEqual(['k'])
  expect(agents.scheduler.value?.limit).toBe(2)

  hn.agents.fail = 'omo is stopping'
  agents.refresh()
  await flushPromises()
  expect(agents.error.value).toBe('omo is stopping')
  // The list stays as it was while the read fails.
  expect(ids(agents)).toEqual(['k'])
  hn.agents.fail = ''
  agents.refresh()
  await flushPromises()
  expect(agents.error.value).toBe('')
})

test('the work stops with the last caller', async () => {
  const hn = await sidebarHarness()
  const shared = createAgents({ client: hn.session.client, store: hn.session.store })
  const a = shared.acquire()
  const b = shared.acquire()
  await flushPromises()
  a()
  emitAgent(hn.omo, 'agent.created', agent({ id: 'q' }))
  expect(shared.agents.sessions.value.map((s) => s.id)).toEqual(['q'])
  b()
  emitAgent(hn.omo, 'agent.created', agent({ id: 'r' }))
  expect(shared.agents.sessions.value.map((s) => s.id)).toEqual(['q'])
})

/** A client whose agent and scheduler reads wait until the test answers them. */
function heldReads() {
  const held: Array<{ path: string; answer(data: unknown): void }> = []
  const client = {
    get: (path: string) =>
      new Promise((resolve) => {
        held.push({ path, answer: (data) => resolve({ data, eventId: null }) })
      }),
  } as unknown as ApiClient
  const take = (path: string) => {
    const i = held.findIndex((h) => h.path === path)
    return held.splice(i, 1)[0]!
  }
  return { client, take }
}

async function openHeld() {
  const hn = await sidebarHarness()
  const reads = heldReads()
  const shared = createAgents({ client: reads.client, store: hn.session.store })
  const scope = effectScope()
  let release = () => {}
  scope.run(() => (release = shared.acquire()))
  stop = () => {
    release()
    scope.stop()
  }
  return { hn, agents: shared.agents, ...reads }
}

test('an older scheduler answer never replaces a newer event or a newer answer', async () => {
  const { hn, agents, take } = await openHeld()
  const first = take('/api/scheduler')
  hn.omo.emit('scheduler.changed', { state: { running: 2, limit: 4, paused: false } })
  agents.refresh()
  const second = take('/api/scheduler')
  // Read before the event: older than it.
  first.answer({ running: 1, limit: 4, paused: false })
  await flushPromises()
  expect(agents.scheduler.value).toEqual({ running: 2, limit: 4, paused: false })
  second.answer({ running: 3, limit: 4, paused: false })
  await flushPromises()
  expect(agents.scheduler.value).toEqual({ running: 3, limit: 4, paused: false })

  // Two reads answered the other way round: the newer one stays.
  agents.refresh()
  const older = take('/api/scheduler')
  agents.refresh()
  const newer = take('/api/scheduler')
  newer.answer({ running: 4, limit: 4, paused: true })
  await flushPromises()
  older.answer({ running: 0, limit: 4, paused: false })
  await flushPromises()
  expect(agents.scheduler.value).toEqual({ running: 4, limit: 4, paused: true })
})

test('an older agent list never replaces a newer one', async () => {
  const { agents, take } = await openHeld()
  const older = take('/api/agents')
  agents.refresh()
  const newer = take('/api/agents')
  newer.answer([agent({ id: 'now' })])
  await flushPromises()
  older.answer([agent({ id: 'before' })])
  await flushPromises()
  expect(ids(agents)).toEqual(['now'])
})
