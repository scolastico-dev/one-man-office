import type { ApiClient } from './client'
import { SseParser, type SseFrame } from './sse'
import type { ApiEvent } from './types'

/**
 * `connecting`: a request is under way. `live`: the server accepted the
 * stream. `retrying`: the connection failed or ended and the next attempt is
 * scheduled. `unauthorized`: the server refused the credentials; nothing is
 * retried until `reconnectNow()`. `closed`: `close()` was called.
 */
export type StreamState = 'connecting' | 'live' | 'retrying' | 'unauthorized' | 'closed'

export interface StreamHandlers {
  /** A streamed event, in id order, each at most once per connection. */
  onEvent(ev: ApiEvent): void
  /**
   * The server cannot continue gap-free from the resume point and continues
   * after `latest` instead; the connection stays open. The consumer must take
   * a new snapshot, hold back the events that arrive meanwhile and apply only
   * those newer than the snapshot.
   */
  onResync(latest: number): void
  /**
   * The server refused the resume point (400). The consumer must take a new
   * snapshot and then call `reconnectNow()`; the stream resumes from
   * `resumeFrom()`. Without that call the reader retries after its usual delay.
   */
  onResumeRejected(): void
  /** Every state change; `retryInMs` is the delay before the next attempt when `retrying`. */
  onState(s: StreamState, retryInMs?: number): void
}

export interface StreamTimers {
  setTimeout(fn: () => void, ms: number): unknown
  clearTimeout(handle: unknown): void
}

export interface EventStreamOptions {
  client: ApiClient
  /** The id of the newest event the consumer has applied; asked at every (re)connect. */
  resumeFrom(): number | null
  handlers: StreamHandlers
  /** Source of jitter, `Math.random` by default. */
  random?: () => number
  /** Clock in milliseconds, `Date.now` by default. */
  now?: () => number
  /** A connection silent for this long is dropped and reconnected; default 45000. */
  watchdogMs?: number
  /** Timer functions, the global ones by default. */
  timers?: StreamTimers
}

export interface EventStreamConnection {
  /** Aborts the connection and stops for good. */
  close(): void
  /** Connects at once, dropping the current connection or the pending delay. */
  reconnectNow(): void
}

/** Delay before the first retry until the server sent its own `retry:`. */
export const DEFAULT_RETRY_MS = 3000
/** The longest delay between attempts. */
export const MAX_RETRY_MS = 30_000
/** The shortest base delay, whatever the server asks for. */
export const MIN_RETRY_MS = 1000
export const DEFAULT_WATCHDOG_MS = 45_000
const JITTER = 0.2

interface Connection {
  ctrl: AbortController
  reader: ReadableStreamDefaultReader<string> | null
  /** Whether the connection delivered an event, a resync or a ping. */
  delivered: boolean
  /** Ids at or below this were already delivered on this connection. */
  lastId: number
}

type Outcome = 'retry' | 'unauthorized' | 'rejected'

class ProtocolError extends Error {}

function isEventStream(res: Response): boolean {
  const type = res.headers.get('Content-Type') ?? ''
  return type.trim().toLowerCase().startsWith('text/event-stream')
}

function discard(res: Response): void {
  res.body?.cancel().catch(() => {})
}

/**
 * Reads `GET /api/events` with `fetch` streaming, so the Authorization header
 * can be sent. Resumes after every disconnect from `resumeFrom()`, with a
 * delay that starts at the server's `retry:` value and doubles (±20 %
 * jitter) up to 30 s; a connection that delivered an event or a ping resets
 * it. A connection silent for `watchdogMs` is treated as dead. Coming back
 * online or becoming visible reconnects at once while retrying.
 */
export function connectEventStream(o: EventStreamOptions): EventStreamConnection {
  const { client, handlers } = o
  const random = o.random ?? Math.random
  const now = o.now ?? (() => Date.now())
  const watchdogMs = o.watchdogMs ?? DEFAULT_WATCHDOG_MS
  const timers: StreamTimers = o.timers ?? {
    setTimeout: (fn, ms) => setTimeout(fn, ms),
    clearTimeout: (h) => clearTimeout(h as ReturnType<typeof setTimeout>),
  }

  let state: StreamState | null = null
  let closed = false
  let serverRetry = DEFAULT_RETRY_MS
  let failures = 0
  let current: Connection | null = null
  let retryTimer: unknown = null
  let watchdog: unknown = null
  let lastActivity = now()

  function setState(s: StreamState, retryInMs?: number): void {
    if (s === state && retryInMs === undefined) return
    state = s
    if (retryInMs === undefined) handlers.onState(s)
    else handlers.onState(s, retryInMs)
  }

  function clearRetry(): void {
    if (retryTimer !== null) timers.clearTimeout(retryTimer)
    retryTimer = null
  }

  function clearWatchdog(): void {
    if (watchdog !== null) timers.clearTimeout(watchdog)
    watchdog = null
  }

  /** Records activity on conn and restarts its watchdog. */
  function touch(conn: Connection): void {
    lastActivity = now()
    clearWatchdog()
    watchdog = timers.setTimeout(() => {
      watchdog = null
      if (current === conn) abort(conn)
    }, watchdogMs)
  }

  function abort(conn: Connection): void {
    conn.ctrl.abort()
    conn.reader?.cancel().catch(() => {})
  }

  function nextDelay(): number {
    const base = Math.min(MAX_RETRY_MS, serverRetry * 2 ** Math.min(failures, 16))
    const jittered = base * (1 - JITTER + 2 * JITTER * random())
    return Math.round(Math.min(MAX_RETRY_MS, jittered))
  }

  function scheduleRetry(): void {
    const delay = nextDelay()
    failures++
    retryTimer = timers.setTimeout(() => {
      retryTimer = null
      connect()
    }, delay)
    setState('retrying', delay)
  }

  function connect(): void {
    if (closed) return
    clearRetry()
    const resume = o.resumeFrom()
    const conn: Connection = {
      ctrl: new AbortController(),
      reader: null,
      delivered: false,
      lastId: resume ?? Number.NEGATIVE_INFINITY,
    }
    current = conn
    setState('connecting')
    touch(conn)
    void run(conn, resume)
  }

  async function run(conn: Connection, resume: number | null): Promise<void> {
    let outcome: Outcome
    try {
      outcome = await session(conn, resume)
    } catch {
      outcome = 'retry'
    }
    if (current !== conn) return // closed or replaced meanwhile
    current = null
    clearWatchdog()
    abort(conn)
    if (outcome === 'unauthorized') {
      setState('unauthorized')
      return
    }
    if (conn.delivered) failures = 0
    scheduleRetry()
    if (outcome === 'rejected') handlers.onResumeRejected()
  }

  async function session(conn: Connection, resume: number | null): Promise<Outcome> {
    const res = await client.openStream(resume, conn.ctrl.signal)
    if (current !== conn) {
      discard(res)
      return 'retry'
    }
    if (res.status === 401 || res.status === 403) {
      discard(res)
      return 'unauthorized'
    }
    if (res.status === 400) {
      discard(res)
      return 'rejected'
    }
    if (res.status !== 200 || !isEventStream(res) || res.body === null) {
      discard(res)
      return 'retry'
    }
    const reader = res.body.pipeThrough(new TextDecoderStream()).getReader()
    conn.reader = reader
    setState('live')
    touch(conn)
    const parser = new SseParser()
    for (;;) {
      const { value, done } = await reader.read()
      if (done || current !== conn) return 'retry'
      touch(conn)
      const comments = parser.comments
      const frames = parser.push(value)
      if (parser.retry !== null) serverRetry = Math.min(MAX_RETRY_MS, Math.max(MIN_RETRY_MS, parser.retry))
      if (parser.comments > comments) conn.delivered = true
      for (const f of frames) {
        if (current !== conn) return 'retry'
        handleFrame(conn, f)
      }
    }
  }

  function handleFrame(conn: Connection, f: SseFrame): void {
    if (f.event === 'resync') {
      const latest = parseResync(f.data)
      conn.delivered = true
      conn.lastId = latest
      handlers.onResync(latest)
      return
    }
    if (f.id === null || !/^\d+$/.test(f.id)) return // not an event of the log
    const id = Number(f.id)
    let ev: unknown
    try {
      ev = JSON.parse(f.data)
    } catch {
      throw new ProtocolError('event data is not JSON')
    }
    if (!isEvent(ev) || ev.id !== id) throw new ProtocolError('event data does not match its id')
    conn.delivered = true
    if (id <= conn.lastId) return
    conn.lastId = id
    handlers.onEvent(ev)
  }

  function parseResync(data: string): number {
    let v: unknown
    try {
      v = JSON.parse(data)
    } catch {
      throw new ProtocolError('resync data is not JSON')
    }
    const latest = (v as { latest?: unknown } | null)?.latest
    if (typeof latest !== 'number' || !Number.isInteger(latest) || latest < 0) {
      throw new ProtocolError('resync without latest')
    }
    return latest
  }

  function reconnectNow(): void {
    if (closed) return
    const conn = current
    current = null
    clearWatchdog()
    if (conn) abort(conn)
    connect()
  }

  /** `online` or the tab becoming visible: retry now, or replace a connection that went silent while asleep. */
  function wake(): void {
    if (closed) return
    if (state === 'retrying') reconnectNow()
    else if ((state === 'live' || state === 'connecting') && now() - lastActivity >= watchdogMs) reconnectNow()
  }

  const onOnline = (): void => wake()
  const onVisibility = (): void => {
    if (typeof document !== 'undefined' && document.visibilityState === 'hidden') return
    wake()
  }
  const win = typeof window !== 'undefined' ? window : null
  const doc = typeof document !== 'undefined' ? document : null
  win?.addEventListener('online', onOnline)
  doc?.addEventListener('visibilitychange', onVisibility)

  connect()

  return {
    close() {
      if (closed) return
      closed = true
      const conn = current
      current = null
      clearRetry()
      clearWatchdog()
      if (conn) abort(conn)
      win?.removeEventListener('online', onOnline)
      doc?.removeEventListener('visibilitychange', onVisibility)
      setState('closed')
    },
    reconnectNow,
  }
}

function isEvent(v: unknown): v is ApiEvent {
  if (typeof v !== 'object' || v === null) return false
  const e = v as { id?: unknown; kind?: unknown }
  return typeof e.id === 'number' && typeof e.kind === 'string'
}
