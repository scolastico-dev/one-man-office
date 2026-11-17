import { afterEach, beforeEach, describe, expect, test, vi, type Mock } from 'vitest'
import { ApiError, type ApiClient, type ApiResult, type Query } from '../api/client'
import type { connectEventStream, EventStreamOptions, StreamHandlers } from '../api/eventStream'
import type { ApiEvent, Column, General, Project, ServerInfo, TicketList, TicketSummary } from '../api/types'
import { CONFIRM_TIMEOUT_MS, createBoardStore, type BoardStore } from './board'

function summary(n: number, over: Partial<TicketSummary> = {}): TicketSummary {
  return {
    number: n,
    type: 'task',
    title: `Ticket ${n}`,
    column: 'planning',
    resolution: '',
    rank: n * 1024,
    story: null,
    projects: [],
    supervisor: 'user',
    created_by: { role: 'user' },
    model: '',
    review: null,
    effective_review: 'agent',
    when_approved: null,
    effective_when_approved: 'merge',
    task_release: null,
    effective_task_release: null,
    max_parallel: null,
    review_stage: '',
    attention: '',
    busy: false,
    failed_step: '',
    start_blocked: '',
    export_url: '',
    export_merged: false,
    workdir: '',
    depends_on: [],
    blocks: [],
    waiting_for: [],
    open_questions: 0,
    questions_for_user: 0,
    progress: null,
    agents: [],
    description_version: 0,
    created_at: '2026-11-15T10:00:00.000000000Z',
    updated_at: '2026-11-15T10:00:00.000000000Z',
    closed_at: null,
    ...over,
  }
}

const general = (max: number): General => ({
  limits: { tasks: max, stories: 2 },
  ceo: { enabled: true, may_approve_stories: false, tickets_need_user_approval: true },
  tasks: { require_user_release: false },
  ui: { open_tickets_as_page: false, screen_reader_mode: false },
  branches: { template: 'omo/{slug}' },
  plugin_lists: [],
  mcp: { enabled: true, inject: { claude: true, codex: true } },
  review_defaults: { story: 'human', task: 'agent', story_task: 'agent' },
  when_approved_defaults: { story: 'merge', task: 'merge', story_task: 'merge' },
  merge_defaults: { story: 'merge', task: 'merge', story_task: 'merge' },
  waiting: { timeouts_before_investigate: 3 },
  health: { silent_after: '15m', question_unanswered_after: '1h' },
  images: { keep_days_after_done: 60 },
  stop: { handoff_timeout: '1m' },
  workspace_roots: [],
  rescan_interval: '1h',
  agent_env: {},
  usage: { soft_percent: 80, hard_percent: 95, refresh_interval: '5m', windows: {} },
})

const project = (id: string): Project => ({ id, name: id, path: `/src/${id}`, enabled: true, missing: false, root: '', merge: { story: '', task: '', story_task: '' }, effective_merge: { story: 'merge', task: 'merge', story_task: 'merge' }, story_merge: 'merge' })

function ev(id: number, kind: string, data: unknown, ticket: number | null = null): ApiEvent {
  return { id, kind, ticket, actor: { role: 'user' }, at: '2026-11-15T10:00:00.000Z', data }
}
const moved = (id: number, t: TicketSummary, from: Column, action = 'approve') =>
  ev(id, 'ticket.moved', { ticket: t, from, to: t.column, action }, t.number)
const updated = (id: number, t: TicketSummary) => ev(id, 'ticket.updated', { ticket: t, fields: ['title'] }, t.number)

interface Deferred<T> {
  promise: Promise<T>
  resolve(v: T): void
  reject(e: unknown): void
}
function deferred<T>(): Deferred<T> {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

interface Request {
  method: string
  path: string
  query?: Query | undefined
  body?: unknown
  answer: Deferred<ApiResult<unknown>>
}

/** A client whose answers the test gives, request by request; GETs can also be answered automatically. */
class FakeClient implements ApiClient {
  requests: Request[] = []
  auto = new Map<string, () => unknown>()

  get<T>(path: string, query?: Query): Promise<ApiResult<T>> {
    return this.add('GET', path, query, undefined) as Promise<ApiResult<T>>
  }
  send<T>(method: 'POST' | 'PUT' | 'PATCH' | 'DELETE', path: string, body?: unknown): Promise<ApiResult<T>> {
    return this.add(method, path, undefined, body) as Promise<ApiResult<T>>
  }
  getText(): Promise<{ text: string; headers: Headers }> {
    return Promise.reject(new Error('not used'))
  }
  getBlob(): Promise<Blob> {
    return Promise.reject(new Error('not used'))
  }
  upload<T>(): Promise<ApiResult<T>> {
    return Promise.reject(new Error('not used'))
  }
  openStream(): Promise<Response> {
    return Promise.reject(new Error('the fake connector replaces the stream'))
  }

  private add(method: string, path: string, query: Query | undefined, body: unknown): Promise<ApiResult<unknown>> {
    const r: Request = { method, path, query, body, answer: deferred() }
    this.requests.push(r)
    const auto = method === 'GET' ? this.auto.get(path) : undefined
    if (auto) r.answer.resolve({ data: auto(), eventId: null })
    return r.answer.promise
  }

  open(method: string, path: string): Request[] {
    return this.requests.filter((r) => r.method === method && r.path === path)
  }
  last(method: string, path: string): Request {
    const all = this.open(method, path)
    const r = all[all.length - 1]
    if (!r) throw new Error(`no ${method} ${path}`)
    return r
  }
}

async function flush(): Promise<void> {
  for (let i = 0; i < 30; i++) await Promise.resolve()
}

interface Fixture {
  client: FakeClient
  store: BoardStore
  stream: { opts: EventStreamOptions; close: Mock<() => void>; reconnectNow: Mock<() => void> } | null
  connects: number
  h(): StreamHandlers
  /** Delivers events as the stream would. */
  send(...events: ApiEvent[]): void
  snapshot: { list: TicketList }
}

function setup(initial: TicketList, opts: { projects?: Project[]; paused?: boolean } = {}): Fixture {
  const client = new FakeClient()
  const f: Fixture = {
    client,
    store: undefined as unknown as BoardStore,
    stream: null,
    connects: 0,
    h() {
      if (!f.stream) throw new Error('not connected')
      return f.stream.opts.handlers
    },
    send(...events) {
      for (const e of events) f.h().onEvent(structuredClone(e))
    },
    snapshot: { list: initial },
  }
  client.auto.set('/api/tickets', () => structuredClone(f.snapshot.list))
  client.auto.set('/api/projects', () => opts.projects ?? [project('api')])
  client.auto.set('/api/server', (): ServerInfo => ({ version: 'x', paused: opts.paused ?? false, started_at: '', address: '', address_configured: false, listen: '', version_url: '', version_commit: '' }))
  client.auto.set('/api/settings', () => ({ settings: general(3), errors: [] }))
  const connect: typeof connectEventStream = (o) => {
    f.connects++
    f.stream = { opts: o, close: vi.fn<() => void>(), reconnectNow: vi.fn<() => void>() }
    return { close: f.stream.close, reconnectNow: f.stream.reconnectNow }
  }
  f.store = createBoardStore({ client, connect })
  return f
}

/** A started store, live, with the given snapshot. */
async function started(initial: TicketList, opts: Parameters<typeof setup>[1] = {}): Promise<Fixture> {
  const f = setup(initial, opts)
  await f.store.start()
  f.h().onState('live')
  return f
}

const ids = (f: Fixture, c: Column) => f.store.columns.value[c].all.map((t) => t.number)

beforeEach(() => {
  vi.useFakeTimers()
})
afterEach(() => {
  vi.useRealTimers()
})

describe('loading and the stream', () => {
  test('start takes the snapshot and resumes the stream from its event_id', async () => {
    const f = setup({ tickets: [summary(1), summary(2, { column: 'done', closed_at: '2026-11-15T11:00:00.000000000Z' })], event_id: 12 }, { paused: true })
    expect(f.store.state.loaded).toBe(false)
    await f.store.start()
    expect(f.store.state.loaded).toBe(true)
    expect(f.store.state.lastEventId).toBe(12)
    expect(f.store.state.paused).toBe(true)
    expect(f.store.state.general?.limits.tasks).toBe(3)
    expect([...f.store.state.projects.keys()]).toEqual(['api'])
    expect(f.client.last('GET', '/api/projects').query).toEqual({ all: true })
    // Projects, pause and settings are read after the tickets, so the events after the snapshot cover them.
    const order = f.client.requests.map((r) => r.path)
    expect(order[0]).toBe('/api/tickets')
    expect(f.connects).toBe(1)
    expect(f.stream!.opts.resumeFrom()).toBe(12)
    expect(ids(f, 'planning')).toEqual([1])
    expect(ids(f, 'done')).toEqual([2])
  })

  test('connection state follows the stream and marks the data as possibly stale', async () => {
    const f = setup({ tickets: [], event_id: 1 })
    await f.store.start()
    expect(f.store.connection.value).toBe('connecting')
    f.h().onState('live')
    expect(f.store.connection.value).toBe('live')
    expect(f.store.stale.value).toBe(false)
    f.h().onState('retrying', 3000)
    expect(f.store.connection.value).toBe('retrying')
    expect(f.store.retryInMs.value).toBe(3000)
    expect(f.store.stale.value).toBe(true)
    f.store.reconnectNow()
    expect(f.stream!.reconnectNow).toHaveBeenCalled()
    f.store.stop()
    expect(f.stream!.close).toHaveBeenCalled()
    expect(f.store.connection.value).toBe('closed')
  })

  test('events replayed after a reconnect apply once', async () => {
    const f = await started({ tickets: [summary(1)], event_id: 5 })
    const seen: number[] = []
    f.store.onEvent('*', (e) => seen.push(e.id))
    const t6 = summary(1, { title: 'six' })
    const t7 = summary(1, { column: 'ready' })
    f.send(updated(6, t6), moved(7, t7, 'planning'))
    f.h().onState('retrying', 3000)
    expect(f.stream!.opts.resumeFrom()).toBe(7)
    f.h().onState('live')
    f.send(updated(6, t6), moved(7, t7, 'planning'), ev(8, 'ticket.created', { ticket: summary(2) }, 2))
    expect(seen).toEqual([6, 7, 8])
    expect(ids(f, 'ready')).toEqual([1])
    expect(ids(f, 'planning')).toEqual([2])
  })

  test('resync buffers live events and applies only those after the snapshot', async () => {
    const f = await started({ tickets: [summary(1)], event_id: 10 })
    const seen: number[] = []
    f.store.onEvent('*', (e) => seen.push(e.id))
    f.client.auto.delete('/api/tickets')
    f.h().onResync(10)
    expect(f.store.syncing.value).toBe(true)
    expect(f.store.stale.value).toBe(true)
    // Events 11..15 arrive while the snapshot loads; 11..13 are in it.
    for (let id = 11; id <= 15; id++) f.send(updated(id, summary(1, { title: `v${id}` })))
    expect(f.store.state.tickets.get(1)!.t.title).toBe('Ticket 1')
    expect(f.store.state.lastEventId).toBe(10)
    // A reconnect meanwhile continues after what is held back.
    expect(f.stream!.opts.resumeFrom()).toBe(15)
    f.client.last('GET', '/api/tickets').answer.resolve({ data: { tickets: [summary(1, { title: 'v13' }), summary(2)], event_id: 13 }, eventId: null })
    await flush()
    expect(f.store.syncing.value).toBe(false)
    expect(f.store.state.tickets.get(1)!.t.title).toBe('v15')
    expect(f.store.state.tickets.has(2)).toBe(true)
    expect(f.store.state.lastEventId).toBe(15)
    expect(seen).toEqual([14, 15])
    expect(f.stream!.opts.resumeFrom()).toBe(15)
    expect(f.stream!.reconnectNow).not.toHaveBeenCalled()
  })

  test('a resync shows no half-installed snapshot: tickets, projects and pause change together', async () => {
    const f = await started({ tickets: [summary(1)], event_id: 10 })
    f.client.auto.delete('/api/projects')
    const snapshotHooks: number[] = []
    f.store.onSnapshot(() => snapshotHooks.push(f.store.state.lastEventId))
    f.snapshot.list = { tickets: [summary(2)], event_id: 20 }
    f.h().onResync(19)
    await flush()
    // Tickets are back, projects are not: nothing is installed yet.
    expect([...f.store.state.tickets.keys()]).toEqual([1])
    f.client.last('GET', '/api/projects').answer.resolve({ data: [project('web')], eventId: null })
    await flush()
    expect([...f.store.state.tickets.keys()]).toEqual([2])
    expect([...f.store.state.projects.keys()]).toEqual(['web'])
    expect(snapshotHooks).toEqual([20])
  })

  test('a second resync while the snapshot loads takes a newer snapshot', async () => {
    const f = await started({ tickets: [summary(1)], event_id: 10 })
    f.client.auto.delete('/api/tickets')
    f.h().onResync(10)
    await flush()
    const first = f.client.last('GET', '/api/tickets')
    f.h().onResync(700)
    await flush()
    const second = f.client.last('GET', '/api/tickets')
    expect(second).not.toBe(first)
    first.answer.resolve({ data: { tickets: [summary(1, { title: 'old' })], event_id: 40 }, eventId: null })
    await flush()
    expect(f.store.syncing.value).toBe(true)
    second.answer.resolve({ data: { tickets: [summary(1, { title: 'new' })], event_id: 701 }, eventId: null })
    await flush()
    expect(f.store.state.tickets.get(1)!.t.title).toBe('new')
    expect(f.store.state.lastEventId).toBe(701)
  })

  test('a failed resync snapshot is retried and events keep being held back', async () => {
    const f = await started({ tickets: [summary(1)], event_id: 10 })
    f.client.auto.delete('/api/tickets')
    f.h().onResync(10)
    await flush()
    f.client.last('GET', '/api/tickets').answer.reject(new ApiError(0, 'network', 'down'))
    await flush()
    expect(f.store.syncing.value).toBe(true)
    f.send(updated(12, summary(1, { title: 'v12' })))
    expect(f.client.open('GET', '/api/tickets')).toHaveLength(2)
    await vi.advanceTimersByTimeAsync(3000)
    expect(f.client.open('GET', '/api/tickets')).toHaveLength(3)
    f.client.last('GET', '/api/tickets').answer.resolve({ data: { tickets: [summary(1)], event_id: 11 }, eventId: null })
    await flush()
    expect(f.store.state.tickets.get(1)!.t.title).toBe('v12')
    expect(f.store.syncing.value).toBe(false)
  })

  test('a rejected resume id triggers a full resync', async () => {
    const f = await started({ tickets: [summary(1)], event_id: 900 })
    f.snapshot.list = { tickets: [summary(2)], event_id: 4 }
    f.h().onResumeRejected()
    await flush()
    expect([...f.store.state.tickets.keys()]).toEqual([2])
    // The server's log started over: its ids are lower now.
    expect(f.store.state.lastEventId).toBe(4)
    expect(f.stream!.reconnectNow).toHaveBeenCalledTimes(1)
    expect(f.stream!.opts.resumeFrom()).toBe(4)
  })

  test('scheduler.changed and ticket.retried reach onEvent and leave the board unchanged', async () => {
    const f = await started({ tickets: [summary(1, { failed_step: 'start' })], event_id: 1 })
    const seen: string[] = []
    f.store.onEvent('scheduler.changed', (e) => seen.push(e.kind))
    f.store.onEvent('ticket.retried', (e) => seen.push(e.kind))
    const before = structuredClone([...f.store.state.tickets.values()])
    f.send(ev(2, 'scheduler.changed', { state: { running: 1 } }), ev(3, 'ticket.retried', { ticket: 1, step: 'start' }, 1))
    expect(seen).toEqual(['scheduler.changed', 'ticket.retried'])
    expect([...f.store.state.tickets.values()]).toEqual(before)
    expect(f.store.state.lastEventId).toBe(3)
  })

  test('onEvent handlers see events after the store applied them', async () => {
    const f = await started({ tickets: [summary(1)], event_id: 1 })
    const seen: string[] = []
    const off = f.store.onEvent('ticket.updated', (e) => seen.push(`${e.id}:${f.store.view(1)!.title}`))
    f.store.onEvent('ticket.updated', () => {
      throw new Error('a broken handler')
    })
    const errors = vi.spyOn(console, 'error').mockImplementation(() => {})
    f.send(updated(2, summary(1, { title: 'B' })))
    expect(seen).toEqual(['2:B'])
    expect(errors).toHaveBeenCalled()
    off()
    f.send(updated(3, summary(1, { title: 'C' })))
    expect(seen).toEqual(['2:B'])
    errors.mockRestore()
  })

  test('settings.changed general refetches the settings', async () => {
    const f = await started({ tickets: [], event_id: 1 })
    f.client.auto.set('/api/settings', () => ({ settings: general(7), errors: [] }))
    const before = f.client.open('GET', '/api/settings').length
    f.send(ev(2, 'settings.changed', { section: 'roles', name: 'pm' }))
    await flush()
    expect(f.client.open('GET', '/api/settings')).toHaveLength(before)
    f.send(ev(3, 'settings.changed', { section: 'general', name: '' }))
    await flush()
    expect(f.client.open('GET', '/api/settings')).toHaveLength(before + 1)
    expect(f.store.state.general?.limits.tasks).toBe(7)
  })

  test('settings.changed general reads the projects again: their merge modes in effect may have changed', async () => {
    const f = await started({ tickets: [], event_id: 1 }, { projects: [project('api')] })
    const before = f.client.open('GET', '/api/projects').length
    f.client.auto.set('/api/projects', () => [{ ...project('api'), effective_merge: { story: 'pr', task: 'pr', story_task: 'merge' } }])
    f.send(ev(2, 'settings.changed', { section: 'general', name: '' }))
    await flush()
    expect(f.client.open('GET', '/api/projects')).toHaveLength(before + 1)
    expect(f.store.state.projects.get('api')?.effective_merge.task).toBe('pr')
  })

  test('a failed refreshProjects keeps the projects, logs and does not reject', async () => {
    const f = await started({ tickets: [], event_id: 1 }, { projects: [project('api')] })
    const errors = vi.spyOn(console, 'error').mockImplementation(() => {})
    f.client.auto.delete('/api/projects')
    const done = f.store.refreshProjects()
    f.client.last('GET', '/api/projects').answer.reject(new Error('503'))
    await expect(done).resolves.toBeUndefined()
    expect(f.store.state.projects.has('api')).toBe(true)
    expect(errors).toHaveBeenCalled()
    errors.mockRestore()
  })

  test('refreshProjects asks again when a project event arrived meanwhile', async () => {
    const f = await started({ tickets: [], event_id: 1 })
    f.client.auto.delete('/api/projects')
    const done = f.store.refreshProjects()
    await flush()
    f.send(ev(2, 'project.removed', { id: 'api' }))
    f.client.last('GET', '/api/projects').answer.resolve({ data: [project('api')], eventId: null })
    await flush()
    f.client.last('GET', '/api/projects').answer.resolve({ data: [], eventId: null })
    await done
    expect(f.client.open('GET', '/api/projects')).toHaveLength(3)
    expect(f.store.state.projects.size).toBe(0)
  })

  test('loadOlderDone appends the next page and stops when next_done_before is absent', async () => {
    const closed = (n: number) => summary(n, { column: 'done', closed_at: `2026-11-15T10:00:${String(n).padStart(2, '0')}.000000000Z` })
    const f = await started({ tickets: [summary(1), closed(9), closed(8)], event_id: 30, next_done_before: 8 })
    expect(f.store.state.doneBefore).toBe(8)
    f.client.auto.delete('/api/tickets')
    const p = f.store.loadOlderDone()
    const req = f.client.last('GET', '/api/tickets')
    expect(req.query).toMatchObject({ column: 'done', done_before: 8 })
    req.answer.resolve({ data: { tickets: [closed(7), closed(6)], event_id: 31, next_done_before: 6 }, eventId: null })
    await p
    expect(ids(f, 'done')).toEqual([9, 8, 7, 6])
    expect(f.store.state.doneBefore).toBe(6)
    const p2 = f.store.loadOlderDone()
    f.client.last('GET', '/api/tickets').answer.resolve({ data: { tickets: [closed(5)], event_id: 31 }, eventId: null })
    await p2
    expect(ids(f, 'done')).toEqual([9, 8, 7, 6, 5])
    expect(f.store.state.doneBefore).toBe(null)
    const count = f.client.open('GET', '/api/tickets').length
    await f.store.loadOlderDone()
    expect(f.client.open('GET', '/api/tickets')).toHaveLength(count)
  })

  test('a Done page read before a left-out event is read again', async () => {
    const closed = (n: number, over: Partial<TicketSummary> = {}) =>
      summary(n, { column: 'done', closed_at: `2026-11-15T10:00:${String(n).padStart(2, '0')}.000000000Z`, ...over })
    const f = await started({ tickets: [closed(9), closed(8)], event_id: 30, next_done_before: 8 })
    f.client.auto.delete('/api/tickets')
    const p = f.store.loadOlderDone()
    // While the page is under way, ticket 7 (on that page) is marked merged.
    f.send(updated(31, closed(7, { export_merged: true })))
    expect(f.store.state.tickets.has(7)).toBe(false)
    f.client.last('GET', '/api/tickets').answer.resolve({ data: { tickets: [closed(7)], event_id: 30 }, eventId: null })
    await flush()
    f.client.last('GET', '/api/tickets').answer.resolve({ data: { tickets: [closed(7, { export_merged: true })], event_id: 31 }, eventId: null })
    await p
    expect(f.store.view(7)!.export_merged).toBe(true)
  })

  test('a Done page that stays stale is merged without the stale ticket and read again next time', async () => {
    const closed = (n: number, over: Partial<TicketSummary> = {}) =>
      summary(n, { column: 'done', closed_at: `2026-11-15T10:00:${String(n).padStart(2, '0')}Z`, ...over })
    const f = await started({ tickets: [closed(9), closed(8)], event_id: 30, next_done_before: 8 })
    f.client.auto.delete('/api/tickets')
    const p = f.store.loadOlderDone()
    f.send(updated(31, closed(7, { title: 'renamed' })))
    // Every read still predates the change.
    for (let i = 0; i < 3; i++) {
      f.client.last('GET', '/api/tickets').answer.resolve({ data: { tickets: [closed(7), closed(6)], event_id: 30, next_done_before: 6 }, eventId: null })
      await flush()
    }
    await p
    expect(f.client.open('GET', '/api/tickets').filter((r) => r.query?.done_before === 8)).toHaveLength(3)
    expect(f.store.state.tickets.has(7)).toBe(false)
    expect(f.store.view(6)).toBeDefined()
    // The page is marked for reload: the cursor did not move.
    expect(f.store.state.doneBefore).toBe(8)
    const again = f.store.loadOlderDone()
    expect(f.client.last('GET', '/api/tickets').query).toMatchObject({ done_before: 8 })
    f.client.last('GET', '/api/tickets').answer.resolve({ data: { tickets: [closed(7, { title: 'renamed' }), closed(6)], event_id: 31, next_done_before: 6 }, eventId: null })
    await again
    expect(f.store.view(7)!.title).toBe('renamed')
    expect(f.store.state.doneBefore).toBe(6)
  })

  test('mergeTickets leaves out search hits older than a left-out event and reports them', async () => {
    const closed = (n: number, over: Partial<TicketSummary> = {}) =>
      summary(n, { column: 'done', closed_at: `2026-11-15T10:00:${String(n).padStart(2, '0')}Z`, ...over })
    const f = await started({ tickets: [closed(9), closed(8)], event_id: 30, next_done_before: 8 })
    f.send(updated(31, closed(3, { title: 'renamed' })))
    expect(f.store.state.tickets.has(3)).toBe(false)
    expect(f.store.mergeTickets({ tickets: [closed(3), closed(2)], event_id: 30 })).toEqual([3])
    expect(f.store.state.tickets.has(3)).toBe(false)
    expect(f.store.view(2)).toBeDefined()
    expect(f.store.mergeTickets({ tickets: [closed(3, { title: 'renamed' })], event_id: 31 })).toEqual([])
    expect(f.store.view(3)!.title).toBe('renamed')
  })

  test('columns group stories before tasks, each by rank then number; counts and tasks of a story are derived', async () => {
    const f = await started({
      tickets: [
        summary(1, { type: 'story', rank: 3000 }),
        summary(2, { story: 1, rank: 1000 }),
        summary(3, { type: 'story', rank: 2000 }),
        summary(4, { rank: 1000 }),
        summary(5, { story: 1, column: 'ready', rank: 10 }),
      ],
      event_id: 1,
    })
    const planning = f.store.columns.value.planning
    expect(planning.stories.map((t) => t.number)).toEqual([3, 1])
    expect(planning.tasks.map((t) => t.number)).toEqual([2, 4])
    expect(planning.all.map((t) => t.number)).toEqual([2, 4, 3, 1])
    expect(f.store.counts.value).toEqual({ planning: 4, ready: 1, in_progress: 0, review: 0, done: 0 })
    expect(f.store.tasksOf(1).map((t) => t.number)).toEqual([2, 5])
    expect(f.store.tasksOf(3)).toEqual([])
  })

  test('mergeTickets adds search hits and never overrides newer entries', async () => {
    const f = await started({ tickets: [summary(1)], event_id: 10 })
    f.send(updated(11, summary(1, { title: 'live' })))
    f.store.mergeTickets({ tickets: [summary(1, { title: 'stale' }), summary(2, { column: 'done', closed_at: 'x' })], event_id: 10 })
    expect(f.store.view(1)!.title).toBe('live')
    expect(f.store.view(2)!.column).toBe('done')
  })
})

describe('moves', () => {
  const three = (): TicketList => ({
    tickets: [summary(1, { rank: 1000 }), summary(2, { rank: 2000 }), summary(3, { column: 'ready', rank: 1000 })],
    event_id: 20,
  })

  test('an optimistic move shows the target at once and keeps it until the event with Omo-Event-Id arrived', async () => {
    const f = await started(three())
    let settled = false
    const p = f.store.requestMove(1, 'ready', 3).then(() => (settled = true))
    expect(f.store.view(1)!.column).toBe('ready')
    expect(f.store.view(1)!.rank).toBe(1000 - 1024) // above the card it goes before
    expect(ids(f, 'ready')).toEqual([1, 3])
    expect(f.store.pending.get(1)).toMatchObject({ column: 'ready', waitFor: null })
    const req = f.client.last('POST', '/api/tickets/1/move')
    expect(req.body).toEqual({ column: 'ready', before: 3 })
    req.answer.resolve({ data: summary(1, { column: 'ready', title: 'from the response' }), eventId: 22 })
    await flush()
    expect(f.store.pending.get(1)?.waitFor).toBe(22)
    // The response body is never written into the store.
    expect(f.store.state.tickets.get(1)!.t.column).toBe('planning')
    f.send(updated(21, summary(2, { title: 'other' })))
    expect(f.store.pending.has(1)).toBe(true)
    expect(f.store.view(1)!.column).toBe('ready')
    f.send(moved(22, summary(1, { column: 'ready', rank: 500 }), 'planning'))
    expect(f.store.pending.has(1)).toBe(false)
    expect(f.store.view(1)!.rank).toBe(500)
    await p
    expect(settled).toBe(true)
  })

  test('the overlay rank lies between the neighbours, or after the last card', async () => {
    const f = await started(three())
    void f.store.requestMove(3, 'planning', 2)
    expect(f.store.view(3)!.rank).toBe(1500)
    void f.store.requestMove(2, 'ready', null)
    expect(f.store.view(2)!.rank).toBe(1024) // an empty column
    void f.store.requestMove(1, 'ready', null)
    expect(f.store.view(1)!.rank).toBe(1024 + 1024)
  })

  test('a refused move removes the overlay at once and rejects with the server message', async () => {
    const f = await started(three())
    const p = f.store.requestMove(1, 'ready', null)
    expect(f.store.view(1)!.column).toBe('ready')
    f.client.last('POST', '/api/tickets/1/move').answer.reject(new ApiError(409, 'project_unavailable', 'Project api is missing.'))
    await expect(p).rejects.toThrow('Project api is missing.')
    expect(f.store.pending.has(1)).toBe(false)
    expect(f.store.view(1)!.column).toBe('planning')
  })

  test('the overlay is dropped after 10 s when no confirming event arrives', async () => {
    const f = await started(three())
    const p = f.store.requestMove(1, 'ready', null)
    f.client.last('POST', '/api/tickets/1/move').answer.resolve({ data: null, eventId: 25 })
    await flush()
    await vi.advanceTimersByTimeAsync(CONFIRM_TIMEOUT_MS - 1)
    expect(f.store.pending.has(1)).toBe(true)
    await vi.advanceTimersByTimeAsync(1)
    expect(f.store.pending.has(1)).toBe(false)
    await p
    expect(f.store.view(1)!.column).toBe('planning')
  })

  test('a response without an event id ends the overlay at once', async () => {
    const f = await started(three())
    const p = f.store.requestMove(1, 'planning', 1)
    f.client.last('POST', '/api/tickets/1/move').answer.resolve({ data: null, eventId: null })
    await p
    expect(f.store.pending.has(1)).toBe(false)
  })

  test('the confirming event may arrive before the response', async () => {
    const f = await started(three())
    const p = f.store.requestMove(1, 'ready', null)
    f.send(moved(21, summary(1, { column: 'ready' }), 'planning'))
    expect(f.store.pending.has(1)).toBe(true)
    f.client.last('POST', '/api/tickets/1/move').answer.resolve({ data: null, eventId: 21 })
    await p
    expect(f.store.pending.has(1)).toBe(false)
  })

  test('moves of the same ticket are serialized', async () => {
    const f = await started(three())
    const a = f.store.requestMove(1, 'ready', null)
    const b = f.store.requestMove(1, 'planning', null)
    // The latest move is what the card shows.
    expect(f.store.view(1)!.column).toBe('planning')
    await flush()
    expect(f.client.open('POST', '/api/tickets/1/move')).toHaveLength(1)
    f.client.last('POST', '/api/tickets/1/move').answer.resolve({ data: null, eventId: 21 })
    await flush()
    expect(f.client.open('POST', '/api/tickets/1/move')).toHaveLength(2)
    expect(f.client.last('POST', '/api/tickets/1/move').body).toEqual({ column: 'planning', before: null })
    f.send(moved(21, summary(1, { column: 'ready' }), 'planning'))
    await a
    // The first move is confirmed, the second one still shows.
    expect(f.store.view(1)!.column).toBe('planning')
    f.client.last('POST', '/api/tickets/1/move').answer.resolve({ data: null, eventId: 22 })
    f.send(moved(22, summary(1, { column: 'planning', rank: 9000 }), 'ready', 'withdraw'))
    await b
    expect(f.store.pending.has(1)).toBe(false)
    expect(f.store.view(1)).toMatchObject({ column: 'planning', rank: 9000 })
  })

  test('moves and rejections of different tickets also go out one after the other', async () => {
    const f = await started(three())
    const a = f.store.requestMove(1, 'ready', null)
    const b = f.store.requestMove(2, 'ready', 1)
    const c = f.store.requestMove(3, 'in_progress', null, { reject: { comment: 'again' } })
    await flush()
    expect(f.client.requests.filter((r) => r.method === 'POST')).toHaveLength(1)
    // Both overlays show at once, with no server answer yet.
    expect(f.store.pending.get(2)).toMatchObject({ waitFor: null })
    f.client.last('POST', '/api/tickets/1/move').answer.resolve({ data: null, eventId: 21 })
    await flush()
    expect(f.client.requests.filter((r) => r.method === 'POST')).toHaveLength(2)
    expect(f.client.last('POST', '/api/tickets/2/move').body).toEqual({ column: 'ready', before: 1 })
    f.client.last('POST', '/api/tickets/2/move').answer.reject(new ApiError(409, 'invalid_transition', 'no'))
    await expect(b).rejects.toThrow('no')
    await flush()
    // A refusal does not hold up the line.
    expect(f.client.last('POST', '/api/tickets/3/reject').body).toEqual({ comment: 'again' })
    f.client.last('POST', '/api/tickets/3/reject').answer.resolve({ data: null, eventId: 23 })
    f.send(moved(21, summary(1, { column: 'ready' }), 'planning'), moved(23, summary(3, { column: 'in_progress' }), 'ready', 'reject'))
    await a
    await c
  })

  test('a later move of the same ticket survives the refusal of an earlier one, and both end in the server state', async () => {
    const f = await started(three())
    const a = f.store.requestMove(1, 'ready', null)
    const b = f.store.requestMove(1, 'planning', 2)
    f.client.last('POST', '/api/tickets/1/move').answer.reject(new ApiError(409, 'invalid_transition', 'no'))
    await expect(a).rejects.toThrow('no')
    expect(f.store.pending.get(1)?.column).toBe('planning')
    await flush()
    f.client.last('POST', '/api/tickets/1/move').answer.reject(new ApiError(409, 'invalid_transition', 'no again'))
    await expect(b).rejects.toThrow('no again')
    expect(f.store.view(1)).toEqual(summary(1, { rank: 1000 }))
  })

  test("a pending move overtaken by someone else's change ends in the server state", async () => {
    const f = await started(three())
    const p = f.store.requestMove(1, 'ready', null)
    // Another tab moves the card on after our move: both events arrive before our response.
    f.send(moved(21, summary(1, { column: 'ready' }), 'planning'), moved(22, summary(1, { column: 'planning', rank: 7 }), 'ready', 'withdraw'))
    f.client.last('POST', '/api/tickets/1/move').answer.resolve({ data: null, eventId: 21 })
    await p
    expect(f.store.view(1)).toMatchObject({ column: 'planning', rank: 7 })
  })

  test('a resync while a move waits resolves it against the new snapshot', async () => {
    const f = await started(three())
    const p = f.store.requestMove(1, 'ready', null)
    f.client.last('POST', '/api/tickets/1/move').answer.resolve({ data: null, eventId: 21 })
    await flush()
    f.snapshot.list = { tickets: [summary(1, { column: 'in_progress' }), summary(2), summary(3, { column: 'ready' })], event_id: 600 }
    f.h().onResync(590)
    await p
    expect(f.store.pending.has(1)).toBe(false)
    expect(f.store.view(1)!.column).toBe('in_progress')
  })

  test('Review → Done keeps the card in Review with stage merging; a task without a project goes to Done', async () => {
    const api = [{ project: 'api', base_branch: 'main', branch: 'omo/1', worktree: '', export_url: '', export_state: '' as const, export_error: '' }]
    const f = await started({
      tickets: [
        summary(1, { column: 'review', review_stage: 'human', rank: 50, projects: api }),
        summary(2, { column: 'review', review_stage: 'human', effective_when_approved: 'export', projects: api }),
        summary(3, { column: 'review', review_stage: 'human', effective_when_approved: 'export', projects: [] }),
      ],
      event_id: 1,
    })
    void f.store.requestMove(1, 'done', null)
    void f.store.requestMove(2, 'done', null)
    void f.store.requestMove(3, 'done', null)
    expect(f.store.view(1)).toMatchObject({ column: 'review', review_stage: 'merging', rank: 50 })
    expect(f.store.view(2)).toMatchObject({ column: 'review', review_stage: 'exporting' })
    expect(f.store.view(3)).toMatchObject({ column: 'done' })
    expect(f.client.last('POST', '/api/tickets/1/move').body).toEqual({ column: 'done', before: null })
  })

  test('after an approved review, a later ticket.moved to done moves the card without any request', async () => {
    const f = await started({ tickets: [summary(1, { column: 'review', review_stage: 'human' })], event_id: 1 })
    const p = f.store.requestMove(1, 'done', null)
    f.client.last('POST', '/api/tickets/1/move').answer.resolve({ data: null, eventId: 2 })
    f.send(ev(2, 'ticket.moved', { ticket: summary(1, { column: 'review', review_stage: 'merging' }), from: 'review', to: 'review', action: 'approve' }, 1))
    await p
    expect(f.store.view(1)).toMatchObject({ column: 'review', review_stage: 'merging' })
    const requests = f.client.requests.length
    f.send(
      ev(3, 'ticket.moved', {
        ticket: summary(1, { column: 'done', resolution: 'completed', closed_at: '2026-11-15T12:00:00.000000000Z' }),
        from: 'review',
        to: 'done',
        action: 'merged',
      }, 1),
    )
    expect(ids(f, 'done')).toEqual([1])
    expect(ids(f, 'review')).toEqual([])
    expect(f.client.requests.length).toBe(requests)
  })

  test('a reject sends the comment and shows the card in In Progress', async () => {
    const f = await started({ tickets: [summary(1, { column: 'review', review_stage: 'human' })], event_id: 1 })
    void f.store.requestMove(1, 'in_progress', null, { reject: { comment: '@developer fix the tests' } })
    expect(f.store.view(1)).toMatchObject({ column: 'in_progress', review_stage: '' })
    expect(f.client.last('POST', '/api/tickets/1/reject').body).toEqual({ comment: '@developer fix the tests' })
  })

  test('a lost answer keeps the move shown until its event arrives, then resolves as unknown', async () => {
    const f = await started(three())
    let outcome: string | undefined
    const p = f.store.requestMove(1, 'ready', null).then((o) => (outcome = o))
    f.client.last('POST', '/api/tickets/1/move').answer.reject(new ApiError(0, 'network', 'Cannot reach omo.'))
    await flush()
    // Not reported as refused: the overlay stays and the promise waits.
    expect(outcome).toBeUndefined()
    expect(f.store.view(1)!.column).toBe('ready')
    // Events about other tickets do not end the wait.
    f.send(updated(21, summary(2, { title: 'other' })))
    expect(f.store.pending.has(1)).toBe(true)
    // The server had committed the move; its event arrives.
    f.send(moved(22, summary(1, { column: 'ready', rank: 4000 }), 'planning'))
    await p
    expect(outcome).toBe('unknown')
    expect(f.store.pending.has(1)).toBe(false)
    expect(f.store.view(1)).toMatchObject({ column: 'ready', rank: 4000 })
  })

  test('a lost answer for a move that failed ends in the server state after the timeout, as unknown', async () => {
    const f = await started(three())
    let outcome: string | undefined
    const p = f.store.requestMove(1, 'ready', null).then((o) => (outcome = o))
    f.client.last('POST', '/api/tickets/1/move').answer.reject(new ApiError(0, 'network', 'Cannot reach omo.'))
    await flush()
    await vi.advanceTimersByTimeAsync(CONFIRM_TIMEOUT_MS - 1)
    expect(f.store.view(1)!.column).toBe('ready')
    expect(outcome).toBeUndefined()
    await vi.advanceTimersByTimeAsync(1)
    await p
    expect(outcome).toBe('unknown')
    expect(f.store.pending.has(1)).toBe(false)
    expect(f.store.view(1)!.column).toBe('planning')
  })

  test('a lost answer during a reconnect is settled by the resync snapshot', async () => {
    const f = await started(three())
    f.h().onState('retrying', 3000)
    let outcome: string | undefined
    const p = f.store.requestMove(1, 'ready', null).then((o) => (outcome = o))
    f.client.last('POST', '/api/tickets/1/move').answer.reject(new ApiError(0, 'network', 'Cannot reach omo.'))
    await flush()
    expect(f.store.view(1)!.column).toBe('ready')
    // The stream comes back with a resync; the snapshot shows the move done.
    f.client.auto.delete('/api/tickets')
    f.h().onState('live')
    f.h().onResync(40)
    await flush()
    expect(outcome).toBeUndefined()
    f.client.last('GET', '/api/tickets').answer.resolve({
      data: { tickets: [summary(1, { column: 'ready', rank: 3000 }), summary(2), summary(3, { column: 'ready' })], event_id: 41 },
      eventId: null,
    })
    await p
    expect(outcome).toBe('unknown')
    expect(f.store.pending.has(1)).toBe(false)
    expect(f.store.view(1)).toMatchObject({ column: 'ready', rank: 3000 })
  })

  test('a confirmed move resolves as applied', async () => {
    const f = await started(three())
    const p = f.store.requestMove(1, 'ready', null)
    f.client.last('POST', '/api/tickets/1/move').answer.resolve({ data: null, eventId: 21 })
    f.send(moved(21, summary(1, { column: 'ready' }), 'planning'))
    await expect(p).resolves.toBe('applied')
  })

  test('the confirm timeout stops while the stream reconnects and starts over once it is live', async () => {
    const f = await started(three())
    const p = f.store.requestMove(1, 'ready', null)
    f.client.last('POST', '/api/tickets/1/move').answer.resolve({ data: null, eventId: 25 })
    await flush()
    await vi.advanceTimersByTimeAsync(CONFIRM_TIMEOUT_MS - 1000)
    // The tab sleeps; the stream is down for a long time.
    f.h().onState('retrying', 3000)
    await vi.advanceTimersByTimeAsync(10 * CONFIRM_TIMEOUT_MS)
    expect(f.store.view(1)!.column).toBe('ready')
    f.h().onState('connecting')
    await vi.advanceTimersByTimeAsync(CONFIRM_TIMEOUT_MS)
    expect(f.store.pending.has(1)).toBe(true)
    f.h().onState('live')
    await vi.advanceTimersByTimeAsync(CONFIRM_TIMEOUT_MS - 1)
    expect(f.store.pending.has(1)).toBe(true)
    await vi.advanceTimersByTimeAsync(1)
    expect(f.store.pending.has(1)).toBe(false)
    await expect(p).resolves.toBe('applied')
  })

  test('the confirm timeout stops while a resync loads, and the replayed event confirms the move', async () => {
    const f = await started(three())
    const p = f.store.requestMove(1, 'ready', null)
    f.client.last('POST', '/api/tickets/1/move').answer.resolve({ data: null, eventId: 25 })
    await flush()
    f.client.auto.delete('/api/tickets')
    f.h().onResync(22)
    await flush()
    // The move's event arrives while the snapshot still loads.
    f.send(moved(25, summary(1, { column: 'ready', rank: 4000 }), 'planning'))
    await vi.advanceTimersByTimeAsync(5 * CONFIRM_TIMEOUT_MS)
    expect(f.store.pending.has(1)).toBe(true)
    expect(f.store.view(1)!.column).toBe('ready')
    f.client.last('GET', '/api/tickets').answer.resolve({ data: { tickets: three().tickets, event_id: 23 }, eventId: null })
    await p
    expect(f.store.view(1)).toMatchObject({ column: 'ready', rank: 4000 })
  })

  test('after a resync without the event, the timeout starts over and then shows the server state', async () => {
    const f = await started(three())
    const p = f.store.requestMove(1, 'ready', null)
    f.client.last('POST', '/api/tickets/1/move').answer.resolve({ data: null, eventId: 25 })
    await flush()
    f.client.auto.delete('/api/tickets')
    f.h().onResync(22)
    await flush()
    await vi.advanceTimersByTimeAsync(3 * CONFIRM_TIMEOUT_MS)
    f.client.last('GET', '/api/tickets').answer.resolve({ data: { tickets: three().tickets, event_id: 23 }, eventId: null })
    await flush()
    await vi.advanceTimersByTimeAsync(CONFIRM_TIMEOUT_MS - 1)
    expect(f.store.pending.has(1)).toBe(true)
    await vi.advanceTimersByTimeAsync(1)
    await p
    expect(f.store.view(1)!.column).toBe('planning')
  })

  test('stop drops pending moves and their timers', async () => {
    const f = await started(three())
    const p = f.store.requestMove(1, 'ready', null)
    f.client.last('POST', '/api/tickets/1/move').answer.resolve({ data: null, eventId: 30 })
    await flush()
    f.store.stop()
    await p
    expect(f.store.pending.size).toBe(0)
    expect(vi.getTimerCount()).toBe(0)
  })
})
