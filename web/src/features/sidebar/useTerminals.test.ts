import { flushPromises } from '@vue/test-utils'
import { effectScope } from 'vue'
import { afterEach, expect, test } from 'vitest'
import { ApiError, type ApiClient, type ApiResult, type ClientOptions, type Query } from '../../api/client'
import type { UserTerminal } from '../../api/types'
import { createTokenStore } from '../../auth/token'
import { createSession } from '../../session'
import { FakeOmo, MemoryStorage } from '../../testing/fakeOmo'
import { createTerminals, type Terminals } from './useTerminals'

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

let stop: (() => void) | null = null
afterEach(() => {
  stop?.()
  stop = null
})

async function open() {
  const omo = new FakeOmo()
  const server = { list: [] as UserTerminal[], fail: '', reads: 0, sent: [] as string[], refuse: null as ApiError | null }
  const makeClient = (o: ClientOptions): ApiClient => {
    const base = omo.makeClient(o)
    return {
      ...base,
      get: async <T>(path: string, query?: Query): Promise<ApiResult<T>> => {
        if (path !== '/api/terminals') return base.get<T>(path, query)
        server.reads++
        if (server.fail) throw new Error(server.fail)
        return { data: structuredClone(server.list) as T, eventId: null }
      },
      send: async <T>(method: string, path: string, body?: unknown): Promise<ApiResult<T>> => {
        server.sent.push(`${method} ${path} ${JSON.stringify(body ?? null)}`)
        if (server.refuse) throw server.refuse
        if (method === 'POST') return { data: shell({ id: 'new', ...(body as object) }) as T, eventId: null }
        return { data: undefined as T, eventId: null }
      },
    }
  }
  const session = createSession({ tokens: createTokenStore(() => new MemoryStorage()), makeClient, connect: omo.connect })
  session.start()
  await flushPromises()
  const shared = createTerminals({ client: session.client, store: session.store })
  const scope = effectScope()
  let terminals!: Terminals
  let release = () => {}
  scope.run(() => {
    release = shared.acquire()
    terminals = shared.terminals
  })
  stop = () => {
    release()
    scope.stop()
  }
  await flushPromises()
  return { omo, server, terminals }
}

const ids = (t: Terminals) => t.terminals.value.map((x) => x.id)

test('terminal events update the list without a request', async () => {
  const { omo, server, terminals } = await open()
  expect(terminals.loaded.value).toBe(true)
  const reads = server.reads

  omo.emit('terminal.opened', { terminal: shell({ id: 'a', started_at: '2026-11-15T10:00:00Z' }) })
  omo.emit('terminal.opened', { terminal: shell({ id: 'b', started_at: '2026-11-15T11:00:00Z' }) })
  expect(ids(terminals)).toEqual(['b', 'a'])
  expect(terminals.count.value).toBe(2)

  omo.emit('terminal.exited', { terminal: shell({ id: 'a', state: 'exited', ended_at: '2026-11-15T12:00:00Z' }) })
  expect(terminals.terminals.value.find((t) => t.id === 'a')!.state).toBe('exited')

  omo.emit('terminal.closed', { terminal: shell({ id: 'b' }) })
  expect(ids(terminals)).toEqual(['a'])
  // Nothing was asked of the server.
  expect(server.reads).toBe(reads)
  expect(server.sent).toEqual([])
})

test('the list is read again after a resync, and an event during the read is not undone by it', async () => {
  const { omo, server, terminals } = await open()
  server.list = [shell({ id: 'x' }), shell({ id: 'y' })]
  terminals.refresh()
  // `y` is closed while the read is under way: its answer is older.
  omo.emit('terminal.closed', { terminal: shell({ id: 'y' }) })
  await flushPromises()
  expect(ids(terminals)).toEqual(['x'])

  server.list = [shell({ id: 'z' })]
  const before = server.reads
  omo.stream!.onResync(omo.eventId)
  await flushPromises()
  expect(server.reads).toBeGreaterThan(before)
  expect(ids(terminals)).toEqual(['z'])
})

test('open and close send the requests; a refusal rejects with the server words and leaves the list', async () => {
  const { server, terminals } = await open()
  const t = await terminals.open({ project: 'demo', ticket: 12 })
  expect(server.sent).toEqual(['POST /api/terminals {"project":"demo","ticket":12}'])
  expect(ids(terminals)).toEqual(['new'])
  expect(t.id).toBe('new')

  server.refuse = new ApiError(409, 'conflict', '16 terminals are open; close one first.')
  await expect(terminals.close('new')).rejects.toThrow('16 terminals are open; close one first.')
  expect(ids(terminals)).toEqual(['new'])
  await expect(terminals.open({ project: 'demo' })).rejects.toThrow('16 terminals are open')
  expect(ids(terminals)).toEqual(['new'])

  server.refuse = null
  await terminals.close('new')
  expect(server.sent.at(-1)).toBe('DELETE /api/terminals/new null')
  expect(ids(terminals)).toEqual([])
})

test('a failed read keeps the server words', async () => {
  const { terminals, server } = await open()
  server.fail = 'omo cannot list terminals'
  terminals.refresh()
  await flushPromises()
  expect(terminals.error.value).toBe('omo cannot list terminals')
  server.fail = ''
  terminals.refresh()
  await flushPromises()
  expect(terminals.error.value).toBe('')
})

test('an older list never replaces a newer one when two reads answer the other way round', async () => {
  const omo = new FakeOmo()
  const held: Array<(data: UserTerminal[]) => void> = []
  const session = createSession({ tokens: createTokenStore(() => new MemoryStorage()), makeClient: omo.makeClient, connect: omo.connect })
  session.start()
  await flushPromises()
  const client = {
    get: () => new Promise((resolve) => held.push((data) => resolve({ data, eventId: null }))),
  } as unknown as ApiClient
  const shared = createTerminals({ client, store: session.store })
  const scope = effectScope()
  let release = () => {}
  scope.run(() => (release = shared.acquire()))
  stop = () => {
    release()
    scope.stop()
  }
  shared.terminals.refresh()
  const [older, newer] = held.splice(0)
  newer!([shell({ id: 'now' })])
  await flushPromises()
  older!([shell({ id: 'before' })])
  await flushPromises()
  expect(ids(shared.terminals)).toEqual(['now'])
})
