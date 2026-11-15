import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { createTerminalConnection, openTerminalSocket, type ConnectionState } from './terminalSocket'

const TOKEN = 'ab'.repeat(32)

class FakeSocket {
  static all: FakeSocket[] = []
  readyState = 0
  binaryType = ''
  sent: unknown[] = []
  closed = false
  onopen: (() => void) | null = null
  onmessage: ((e: { data: unknown }) => void) | null = null
  onclose: ((e: { code: number }) => void) | null = null
  constructor(
    public url: string,
    public protocols: string[],
  ) {
    FakeSocket.all.push(this)
  }
  send(d: unknown) {
    this.sent.push(d)
  }
  close() {
    this.closed = true
    this.readyState = 3
  }
  open() {
    this.readyState = 1
    this.onopen?.()
  }
  message(data: unknown) {
    this.onmessage?.({ data })
  }
  drop(code = 1006) {
    this.readyState = 3
    this.onclose?.({ code })
  }
}

const deps = (fetch?: unknown) => ({
  WebSocket: FakeSocket as unknown as typeof WebSocket,
  location: { protocol: 'https:', host: 'omo.test:8090' },
  ...(fetch ? { fetch: fetch as typeof globalThis.fetch } : {}),
})

beforeEach(() => {
  FakeSocket.all = []
  vi.useFakeTimers()
})
afterEach(() => vi.useRealTimers())

function make(fetchStatus = 503) {
  const states: [ConnectionState, number | undefined][] = []
  const data: Uint8Array[] = []
  const errors: string[] = []
  const fetch = vi.fn(async (_url: string, _init?: unknown) => ({ status: fetchStatus, body: null }))
  const c = createTerminalConnection({
    path: '/api/terminals/t1/socket',
    getToken: () => TOKEN,
    onData: (d) => data.push(d),
    onState: (s, w) => states.push([s, w]),
    onError: (e) => errors.push(e),
    deps: deps(fetch),
  })
  return { c, states, data, errors, fetch, sock: () => FakeSocket.all[FakeSocket.all.length - 1]! }
}

describe('terminal socket', () => {
  test('offers omo and omo-token.<t>; never puts the token in the URL', () => {
    const s = openTerminalSocket('/api/terminals/t1/socket', TOKEN, deps()) as unknown as FakeSocket
    expect(s.url).toBe('wss://omo.test:8090/api/terminals/t1/socket')
    expect(s.url).not.toContain(TOKEN)
    expect(s.protocols).toEqual(['omo', `omo-token.${TOKEN}`])
    expect(s.binaryType).toBe('arraybuffer')
    const plain = openTerminalSocket('/x', null, { ...deps(), location: { protocol: 'http:', host: 'h' } }) as unknown as FakeSocket
    expect(plain.url).toBe('ws://h/x')
    expect(plain.protocols).toEqual(['omo'])
  })

  test('output is delivered; input-ack paces input; resize sends rows and cols', () => {
    const { c, sock, data } = make()
    const s = sock()
    c.resize(30, 100)
    expect(s.sent).toEqual([])
    s.open()
    expect(s.sent).toEqual([JSON.stringify({ rows: 30, cols: 100 })])
    s.message(new Uint8Array([104, 105]).buffer)
    expect(Array.from(data[0]!)).toEqual([104, 105])
    s.sent = []
    c.send('a')
    c.send('b')
    expect(s.sent).toHaveLength(1)
    s.message('{"type":"input-ack"}')
    expect(s.sent).toHaveLength(2)
    c.resize(40, 90)
    expect(s.sent[2]).toBe(JSON.stringify({ rows: 40, cols: 90 }))
  })

  test('a normal close is ended and does not reconnect', () => {
    const { c, sock, states } = make()
    sock().open()
    sock().drop(1000)
    expect(states.at(-1)?.[0]).toBe('ended')
    vi.advanceTimersByTime(60000)
    expect(FakeSocket.all).toHaveLength(1)
    c.send('x')
  })

  test('a refused handshake answered 404 is ended', async () => {
    const { sock, states, fetch } = make(404)
    sock().drop(1006)
    await vi.advanceTimersByTimeAsync(0)
    expect(fetch).toHaveBeenCalled()
    const [, init] = fetch.mock.calls[0] as unknown as [string, { headers: Record<string, string> }]
    expect((fetch.mock.calls as unknown as string[][])[0]![0]).not.toContain(TOKEN)
    expect(init.headers.Authorization).toBe(`Bearer ${TOKEN}`)
    expect(states.at(-1)?.[0]).toBe('ended')
    await vi.advanceTimersByTimeAsync(60000)
    expect(FakeSocket.all).toHaveLength(1)
  })

  test('a dropped connection reconnects with growing delays, then gives up', async () => {
    const { c, sock, states } = make()
    sock().open()
    sock().drop()
    expect(states.at(-1)).toEqual(['disconnected', 1000])
    await vi.advanceTimersByTimeAsync(999)
    expect(FakeSocket.all).toHaveLength(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(FakeSocket.all).toHaveLength(2)
    sock().drop()
    await vi.advanceTimersByTimeAsync(0)
    expect(states.at(-1)).toEqual(['disconnected', 2000])
    for (const wait of [2000, 4000, 8000, 16000]) {
      await vi.advanceTimersByTimeAsync(wait)
      sock().drop()
      await vi.advanceTimersByTimeAsync(0)
    }
    expect(states.at(-1)).toEqual(['disconnected', undefined])
    const n = FakeSocket.all.length
    await vi.advanceTimersByTimeAsync(120000)
    expect(FakeSocket.all).toHaveLength(n)
    c.reconnect()
    expect(FakeSocket.all).toHaveLength(n + 1)
    sock().open()
    expect(states.at(-1)?.[0]).toBe('live')
  })

  test('input queued on a dead connection is not replayed into the next one', async () => {
    const { c, sock } = make()
    sock().open()
    c.send('x'.repeat(40000))
    sock().drop()
    await vi.advanceTimersByTimeAsync(1000)
    const next = sock()
    next.open()
    expect(next.sent).toEqual([])
  })

  test('dispose closes the socket and stops reconnecting', async () => {
    const { c, sock, states } = make()
    const s = sock()
    s.open()
    s.drop()
    c.dispose()
    await vi.advanceTimersByTimeAsync(60000)
    expect(FakeSocket.all).toHaveLength(1)
    const t = make()
    t.sock().open()
    t.c.dispose()
    expect(t.sock().closed).toBe(true)
    expect(states.at(-1)?.[0]).toBe('disconnected')
  })

  test('a socket that cannot be built reports disconnected without the reason', () => {
    const states: string[] = []
    createTerminalConnection({
      path: '/x',
      getToken: () => TOKEN,
      onData: () => {},
      onState: (s) => states.push(s),
      onError: () => {},
      deps: {
        WebSocket: class {
          constructor() {
            throw new SyntaxError(`bad protocol omo-token.${TOKEN}`)
          }
        } as unknown as typeof WebSocket,
        location: { protocol: 'http:', host: 'h' },
      },
    })
    expect(states.at(-1)).toBe('disconnected')
  })
})
