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
  const replays: boolean[] = []
  const errors: string[] = []
  /** What reached the view, in order: 'start' for each replay start, else the decoded output. */
  const seen: string[] = []
  const fetch = vi.fn(async (_url: string, _init?: unknown) => ({ status: fetchStatus, body: null }))
  const c = createTerminalConnection({
    path: '/api/terminals/t1/socket',
    getToken: () => TOKEN,
    onData: (d, replay) => {
      data.push(d)
      replays.push(replay)
      seen.push(new TextDecoder().decode(d))
    },
    onReplayStart: () => seen.push('start'),
    onState: (s, w) => states.push([s, w]),
    onError: (e) => errors.push(e),
    deps: deps(fetch),
  })
  return { c, states, data, replays, errors, seen, fetch, sock: () => FakeSocket.all[FakeSocket.all.length - 1]! }
}

describe('terminal socket', () => {
  test('output before the end-of-replay frame is marked as replay, on every new connection', async () => {
    const { c, data, replays, sock } = make()
    const bytes = (t: string) => new TextEncoder().encode(t).buffer
    sock().open()
    sock().message(bytes('old\x1b[c'))
    sock().message(JSON.stringify({ type: 'replay-end' }))
    sock().message(bytes('new'))
    expect(data.map((d) => new TextDecoder().decode(d))).toEqual(['old\x1b[c', 'new'])
    expect(replays).toEqual([true, false])
    sock().drop()
    await vi.advanceTimersByTimeAsync(1000)
    sock().open()
    sock().message(bytes('again'))
    sock().message(JSON.stringify({ type: 'replay-end' }))
    sock().message(bytes('live'))
    expect(replays).toEqual([true, false, true, false])
    c.dispose()
  })

  test('every connection announces its replay before the first output, so the view can clear what an earlier one showed', async () => {
    const { c, seen, sock } = make()
    const bytes = (t: string) => new TextEncoder().encode(t).buffer
    sock().open()
    sock().message(bytes('history'))
    sock().message(JSON.stringify({ type: 'replay-end' }))
    sock().message(bytes('more'))
    sock().drop()
    await vi.advanceTimersByTimeAsync(1000)
    sock().open()
    sock().message(bytes('history+more'))
    expect(seen).toEqual(['start', 'history', 'more', 'start', 'history+more'])
    c.dispose()
  })

  test('input on a dead connection is refused in words that name the way out', async () => {
    const { c, sock, errors } = make()
    sock().open()
    sock().drop()
    c.send('x')
    expect(errors).toEqual(['Terminal disconnected. This input was not sent; press Reconnect.'])
    c.dispose()
  })

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
      onReplayStart: () => {},
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

  describe('messages', () => {
    const frames = (sock: FakeSocket) => sock.sent.filter((d) => typeof d === 'string').map((d) => JSON.parse(d as string))

    test('a message goes out as one frame with its text and Enter, and resolves on its acknowledgement', async () => {
      const { c, sock } = make()
      sock().open()
      const done = c.sendMessage(['hello\nworld', '/image.png', 'tail'])
      const [frame] = frames(sock())
      expect(frame).toMatchObject({ type: 'message', parts: ['hello\nworld', '/image.png', 'tail'], keys: '\r' })
      sock().message(JSON.stringify({ type: 'message-ack', id: frame.id }))
      await expect(done).resolves.toBeUndefined()
      c.dispose()
    })

    test('a refusal in the acknowledgement rejects with its text, and an acknowledgement of another message does not resolve it', async () => {
      const { c, sock } = make()
      sock().open()
      const first = c.sendMessage(['a'])
      const second = c.sendMessage(['b'])
      const [f1, f2] = frames(sock())
      expect(f1.id).not.toBe(f2.id)
      sock().message(JSON.stringify({ type: 'message-ack', id: f2.id, error: 'the terminal input queue is full' }))
      await expect(second).rejects.toThrow('the terminal input queue is full')
      sock().message(JSON.stringify({ type: 'message-ack', id: f1.id }))
      await expect(first).resolves.toBeUndefined()
      c.dispose()
    })

    test('without an open connection the message is refused at once and never queued for a later one', async () => {
      const { c, sock } = make()
      await expect(c.sendMessage(['early'])).rejects.toThrow(/disconnected/i)
      sock().open()
      expect(frames(sock())).toEqual([])
      c.dispose()
    })

    test('a message whose connection drops before its acknowledgement is rejected', async () => {
      const { c, sock } = make()
      sock().open()
      const pending = c.sendMessage(['lost'])
      sock().drop()
      await expect(pending).rejects.toThrow(/did not confirm|disconnected/i)
      c.dispose()
    })

    test('an acknowledgement that never comes rejects after a while', async () => {
      const { c, sock } = make()
      sock().open()
      const pending = c.sendMessage(['slow'])
      const seen = expect(pending).rejects.toThrow(/did not confirm/i)
      await vi.advanceTimersByTimeAsync(60_000)
      await seen
      c.dispose()
    })
  })
})
