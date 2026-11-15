import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { ApiError, type ApiClient } from './client'
import { connectEventStream, type StreamState } from './eventStream'
import type { ApiEvent } from './types'

const enc = new TextEncoder()

/** One request the reader made, answered by the test. */
class Call {
  readonly signal: AbortSignal
  readonly lastEventId: number | null
  cancelled = false
  private resolve!: (r: Response) => void
  private reject!: (e: unknown) => void
  private ctrl: ReadableStreamDefaultController<Uint8Array> | null = null
  readonly answer: Promise<Response>

  constructor(lastEventId: number | null, signal: AbortSignal) {
    this.lastEventId = lastEventId
    this.signal = signal
    this.answer = new Promise((res, rej) => {
      this.resolve = res
      this.reject = rej
    })
    // Like fetch: an abort rejects a pending request and errors an open body.
    signal.addEventListener('abort', () => {
      this.reject(signal.reason)
      try {
        this.ctrl?.error(signal.reason)
      } catch {
        // already closed
      }
    })
  }

  open(contentType = 'text/event-stream'): void {
    const body = new ReadableStream<Uint8Array>({
      start: (c) => {
        this.ctrl = c
      },
      cancel: () => {
        this.cancelled = true
      },
    })
    this.resolve(new Response(body, { status: 200, headers: { 'Content-Type': contentType } }))
  }

  status(status: number, body = '{"error":{"code":"x","message":"y"}}'): void {
    this.resolve(new Response(body, { status, headers: { 'Content-Type': 'application/json' } }))
  }

  fail(e: unknown = new ApiError(0, 'network', 'Cannot reach omo.')): void {
    this.reject(e)
  }

  send(chunk: string | Uint8Array): void {
    this.ctrl?.enqueue(typeof chunk === 'string' ? enc.encode(chunk) : chunk)
  }

  end(): void {
    this.ctrl?.close()
  }

  error(): void {
    this.ctrl?.error(new TypeError('network error'))
  }
}

function ev(id: number, kind = 'ticket.updated', extra: Record<string, unknown> = {}): ApiEvent {
  return { id, kind, ticket: 1, actor: { role: 'user' }, at: '2026-11-15T00:00:00.000000000Z', data: { n: id }, ...extra }
}

function frame(e: ApiEvent): string {
  return `id: ${e.id}\nevent: ${e.kind}\ndata: ${JSON.stringify(e)}\n\n`
}

const resync = (latest: number) => `event: resync\ndata: {"latest":${latest}}\n\n`

async function flush(): Promise<void> {
  for (let i = 0; i < 30; i++) await Promise.resolve()
}

async function advance(ms: number): Promise<void> {
  await vi.advanceTimersByTimeAsync(ms)
  await flush()
}

function setup(opts: { resume?: number | null; random?: () => number; now?: () => number } = {}) {
  const calls: Call[] = []
  const client: ApiClient = {
    get: () => Promise.reject(new Error('not used')),
    send: () => Promise.reject(new Error('not used')),
    getText: () => Promise.reject(new Error('not used')),
    openStream(lastEventId, signal) {
      const c = new Call(lastEventId, signal)
      calls.push(c)
      return c.answer
    },
  }
  const log = {
    events: [] as ApiEvent[],
    resyncs: [] as number[],
    rejected: 0,
    states: [] as Array<[StreamState, number | undefined]>,
    resumeCalls: 0,
  }
  const resume = { value: opts.resume === undefined ? 10 : opts.resume }
  const stream = connectEventStream({
    client,
    resumeFrom: () => {
      log.resumeCalls++
      return resume.value
    },
    handlers: {
      onEvent: (e) => log.events.push(e),
      onResync: (latest) => log.resyncs.push(latest),
      onResumeRejected: () => log.rejected++,
      onState: (s, ms) => log.states.push([s, ms]),
    },
    random: opts.random ?? (() => 0.5),
    ...(opts.now ? { now: opts.now } : {}),
  })
  const last = (): Call => {
    const c = calls[calls.length - 1]
    if (!c) throw new Error('no call yet')
    return c
  }
  const state = (): StreamState | undefined => log.states[log.states.length - 1]?.[0]
  return { calls, log, resume, stream, last, state }
}

/** A connected, live stream. */
async function live(opts: Parameters<typeof setup>[0] = {}) {
  const s = setup(opts)
  await flush()
  s.last().open()
  await flush()
  return s
}

beforeEach(() => {
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
})

describe('connectEventStream', () => {
  test('first connect sends resumeFrom() as Last-Event-ID', async () => {
    const s = setup({ resume: 42 })
    await flush()
    expect(s.calls).toHaveLength(1)
    expect(s.calls[0]?.lastEventId).toBe(42)
    expect(s.state()).toBe('connecting')
    s.last().open()
    await flush()
    expect(s.state()).toBe('live')
    s.last().send('retry: 3000\n\n' + frame(ev(43)) + frame(ev(44)))
    await flush()
    expect(s.log.events.map((e) => e.id)).toEqual([43, 44])
    s.stream.close()
  })

  test('without a resume point no Last-Event-ID is sent', async () => {
    const s = setup({ resume: null })
    await flush()
    expect(s.calls[0]?.lastEventId).toBeNull()
    s.stream.close()
  })

  test('reconnect after the stream ends asks resumeFrom() again', async () => {
    const s = await live({ resume: 10 })
    s.last().send(frame(ev(11)))
    await flush()
    s.resume.value = 11
    s.last().end()
    await flush()
    expect(s.state()).toBe('retrying')
    expect(s.log.states.at(-1)).toEqual(['retrying', 3000])
    await advance(2999)
    expect(s.calls).toHaveLength(1)
    await advance(1)
    expect(s.calls).toHaveLength(2)
    expect(s.calls[1]?.lastEventId).toBe(11)
    expect(s.log.resumeCalls).toBe(2)
    s.stream.close()
  })

  test('a frame cut off by a disconnect is not delivered', async () => {
    const s = await live({ resume: 10 })
    s.last().send(frame(ev(11)) + 'id: 12\nevent: ticket.updated\ndata: ' + JSON.stringify(ev(12)) + '\n')
    await flush()
    s.last().error()
    await flush()
    expect(s.log.events.map((e) => e.id)).toEqual([11])
    expect(s.state()).toBe('retrying')
    s.stream.close()
  })

  test('delays: server retry first, then doubling with jitter up to 30 s, reset after a live frame', async () => {
    let r = 0.5
    const s = setup({ random: () => r })
    const delays = (): Array<number | undefined> => s.log.states.filter(([st]) => st === 'retrying').map(([, ms]) => ms)
    await flush()
    s.last().open()
    await flush()
    // Only the preamble: the connection delivered nothing, no reset.
    s.last().send('retry: 5000\n\n')
    s.last().end()
    await flush()
    expect(delays()).toEqual([5000])
    await advance(5000)
    s.last().status(503)
    await flush()
    expect(delays()).toEqual([5000, 10000])
    await advance(10000)
    s.last().fail()
    await flush()
    expect(delays()).toEqual([5000, 10000, 20000])
    r = 1 // +20 % would exceed the cap
    await advance(20000)
    s.last().status(502, 'Bad Gateway')
    await flush()
    expect(delays()).toEqual([5000, 10000, 20000, 30000])
    r = 0 // -20 %
    await advance(30000)
    s.last().fail()
    await flush()
    expect(delays()).toEqual([5000, 10000, 20000, 30000, 24000])
    r = 0.5
    await advance(24000)
    // A connection that delivered a ping resets the delay.
    s.last().open()
    await flush()
    s.last().send(': ping\n\n')
    s.last().end()
    await flush()
    expect(delays().at(-1)).toBe(5000)
    await advance(5000)
    s.last().fail()
    await flush()
    expect(delays().at(-1)).toBe(10000)
    await advance(10000)
    // So does an event.
    s.last().open()
    await flush()
    s.last().send(frame(ev(11)))
    s.last().error()
    await flush()
    expect(delays().at(-1)).toBe(5000)
    s.stream.close()
  })

  test('jitter spreads the delay by 20 % either way', async () => {
    const s = setup({ random: () => 0.25 })
    await flush()
    s.last().fail()
    await flush()
    expect(s.log.states.at(-1)).toEqual(['retrying', 2700])
    s.stream.close()
  })

  test('watchdog aborts after 45 s without bytes; a ping resets it', async () => {
    const s = await live()
    const c = s.last()
    await advance(44999)
    expect(c.signal.aborted).toBe(false)
    c.send(': ping\n\n')
    await flush()
    await advance(44999)
    expect(c.signal.aborted).toBe(false)
    await advance(1)
    expect(c.signal.aborted).toBe(true)
    expect(s.state()).toBe('retrying')
    expect(s.calls).toHaveLength(1)
    // The silent connection had delivered a ping: the delay starts over.
    expect(s.log.states.at(-1)).toEqual(['retrying', 3000])
    await advance(3000)
    expect(s.calls).toHaveLength(2)
    s.stream.close()
  })

  test('watchdog also aborts a request whose answer never arrives', async () => {
    const s = setup()
    await flush()
    await advance(45000)
    expect(s.calls[0]?.signal.aborted).toBe(true)
    expect(s.state()).toBe('retrying')
    s.stream.close()
  })

  test('resync frame calls onResync(latest) and keeps the connection', async () => {
    const s = await live({ resume: 900 })
    s.last().send('retry: 3000\n\n' + resync(500))
    await flush()
    expect(s.log.resyncs).toEqual([500])
    // After a resync the stream continues after latest, even below the old resume point.
    s.last().send(frame(ev(501)) + frame(ev(502)))
    await flush()
    expect(s.log.events.map((e) => e.id)).toEqual([501, 502])
    expect(s.calls).toHaveLength(1)
    expect(s.state()).toBe('live')
    expect(s.last().signal.aborted).toBe(false)
    s.stream.close()
  })

  test('a malformed resync frame or event is not skipped: the reader reconnects', async () => {
    const s = await live()
    s.last().send('event: resync\ndata: nope\n\n')
    await flush()
    expect(s.log.resyncs).toEqual([])
    expect(s.state()).toBe('retrying')
    await advance(3000)
    s.last().open()
    await flush()
    s.last().send('id: 11\nevent: ticket.updated\ndata: {"id":12}\n\n')
    await flush()
    expect(s.log.events).toEqual([])
    expect(s.state()).toBe('retrying')
    s.stream.close()
  })

  test('no event is delivered twice within a connection', async () => {
    const s = await live({ resume: 10 })
    s.last().send(frame(ev(10)) + frame(ev(11)) + frame(ev(11)) + frame(ev(12)))
    await flush()
    expect(s.log.events.map((e) => e.id)).toEqual([11, 12])
    s.stream.close()
  })

  test('the same stream split at every byte position delivers the same events', async () => {
    const text =
      'retry: 3000\n\n' +
      frame(ev(11, 'ticket.updated', { data: { title: 'Grüße – 日本 🎉' } })) +
      ': ping\r\n\r\n' +
      resync(11) +
      frame(ev(12, 'ticket.moved', { data: { from: 'review', to: 'review' } }))
    const bytes = enc.encode(text)
    for (let i = 0; i <= bytes.length; i++) {
      const s = await live({ resume: 10 })
      s.last().send(bytes.subarray(0, i))
      await flush()
      s.last().send(bytes.subarray(i))
      await flush()
      expect(s.log.events.map((e) => [e.id, e.kind, e.data]), `split at ${i}`).toEqual([
        [11, 'ticket.updated', { title: 'Grüße – 日本 🎉' }],
        [12, 'ticket.moved', { from: 'review', to: 'review' }],
      ])
      expect(s.log.resyncs).toEqual([11])
      s.stream.close()
    }
  })

  test('401 reports unauthorized and stops reconnecting', async () => {
    const s = setup()
    await flush()
    s.last().status(401)
    await flush()
    expect(s.state()).toBe('unauthorized')
    await advance(10 * 60_000)
    window.dispatchEvent(new Event('online'))
    await flush()
    expect(s.calls).toHaveLength(1)
    expect(vi.getTimerCount()).toBe(0)
    // After a new login the consumer may start again.
    s.stream.reconnectNow()
    await flush()
    expect(s.calls).toHaveLength(2)
    s.stream.close()
  })

  test('403 (not the user) also stops reconnecting', async () => {
    const s = setup()
    await flush()
    s.last().status(403)
    await flush()
    expect(s.state()).toBe('unauthorized')
    await advance(10 * 60_000)
    expect(s.calls).toHaveLength(1)
    s.stream.close()
  })

  test('400 on the resume id calls onResumeRejected', async () => {
    const s = setup({ resume: 10 })
    await flush()
    s.last().status(400)
    await flush()
    expect(s.log.rejected).toBe(1)
    expect(s.state()).toBe('retrying')
    // The consumer installs a new snapshot and reconnects from it.
    s.resume.value = 77
    s.stream.reconnectNow()
    await flush()
    expect(s.calls).toHaveLength(2)
    expect(s.calls[1]?.lastEventId).toBe(77)
    s.stream.close()
  })

  test('a response that is not an event stream is retried', async () => {
    const s = setup()
    await flush()
    s.last().open('text/html')
    await flush()
    expect(s.state()).toBe('retrying')
    expect(s.last().cancelled).toBe(true)
    expect(s.log.states.map(([st]) => st)).not.toContain('live')
    s.stream.close()
  })

  test('the server shutting down (503) is retried with backoff', async () => {
    const s = setup()
    await flush()
    s.last().status(503)
    await flush()
    expect(s.log.states.at(-1)).toEqual(['retrying', 3000])
    await advance(3000)
    expect(s.calls).toHaveLength(2)
    s.stream.close()
  })

  test('close() aborts and never reconnects; reconnectNow() skips the delay', async () => {
    const s = await live()
    const c = s.last()
    s.stream.close()
    await flush()
    expect(c.signal.aborted).toBe(true)
    expect(s.state()).toBe('closed')
    expect(vi.getTimerCount()).toBe(0)
    await advance(10 * 60_000)
    window.dispatchEvent(new Event('online'))
    s.stream.reconnectNow()
    await flush()
    expect(s.calls).toHaveLength(1)
    expect(s.log.states.filter(([st]) => st === 'closed')).toHaveLength(1)

    const t = setup()
    await flush()
    t.last().fail()
    await flush()
    expect(t.state()).toBe('retrying')
    t.stream.reconnectNow()
    await flush()
    expect(t.calls).toHaveLength(2)
    expect(t.state()).toBe('connecting')
    // The skipped timer is gone: only the new connection's watchdog remains.
    expect(vi.getTimerCount()).toBe(1)
    t.stream.close()
    expect(vi.getTimerCount()).toBe(0)
  })

  test('close() while retrying or connecting leaves no timer and no request behind', async () => {
    const s = setup()
    await flush()
    const pending = s.last()
    s.stream.close()
    await flush()
    expect(pending.signal.aborted).toBe(true)
    expect(vi.getTimerCount()).toBe(0)

    const t = setup()
    await flush()
    t.last().fail()
    await flush()
    t.stream.close()
    expect(vi.getTimerCount()).toBe(0)
    await advance(60_000)
    expect(t.calls).toHaveLength(1)
  })

  test('reconnectNow() while live replaces the connection', async () => {
    const s = await live()
    const first = s.last()
    s.stream.reconnectNow()
    await flush()
    expect(first.signal.aborted).toBe(true)
    expect(s.calls).toHaveLength(2)
    // The old connection's end does not schedule anything.
    await advance(40_000)
    expect(s.calls).toHaveLength(2)
    s.stream.close()
  })

  test('online and visible events reconnect at once while retrying', async () => {
    const s = setup()
    await flush()
    s.last().fail()
    await flush()
    window.dispatchEvent(new Event('online'))
    await flush()
    expect(s.calls).toHaveLength(2)
    s.last().fail()
    await flush()
    expect(s.state()).toBe('retrying')
    document.dispatchEvent(new Event('visibilitychange'))
    await flush()
    expect(s.calls).toHaveLength(3)
    // While live, neither reconnects.
    s.last().open()
    await flush()
    window.dispatchEvent(new Event('online'))
    document.dispatchEvent(new Event('visibilitychange'))
    await flush()
    expect(s.calls).toHaveLength(3)
    s.stream.close()
    window.dispatchEvent(new Event('online'))
    await flush()
    expect(s.calls).toHaveLength(3)
  })

  test('a hidden tab does not reconnect on visibilitychange', async () => {
    const s = setup()
    await flush()
    s.last().fail()
    await flush()
    Object.defineProperty(document, 'visibilityState', { value: 'hidden', configurable: true })
    try {
      document.dispatchEvent(new Event('visibilitychange'))
      await flush()
      expect(s.calls).toHaveLength(1)
    } finally {
      Object.defineProperty(document, 'visibilityState', { value: 'visible', configurable: true })
    }
    s.stream.close()
  })

  test('a tab that slept past the watchdog reconnects as soon as it is visible again', async () => {
    let clock = 1_000_000
    const s = await live({ now: () => clock })
    const c = s.last()
    // The timers did not run while the machine slept; only the clock moved.
    clock += 60_000
    document.dispatchEvent(new Event('visibilitychange'))
    await flush()
    expect(c.signal.aborted).toBe(true)
    expect(s.calls).toHaveLength(2)
    s.stream.close()
  })
})
