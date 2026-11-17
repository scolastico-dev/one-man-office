import { describe, expect, it, vi } from 'vitest'
import {
  announceTerminalWindow,
  createPopouts,
  DEFAULT_SIZE,
  EXPIRE_MS,
  GONE_GRACE_MS,
  POLL_MS,
  readWindowSize,
  writeWindowSize,
  type ChannelLike,
  type PopoutDeps,
  type WindowMessage,
} from './popout'
import type { TerminalTarget } from './terminalTarget'

/** Channels on one hub deliver to every other channel, like a `BroadcastChannel`. */
function hub() {
  const members = new Set<{ fns: Set<(ev: MessageEvent) => void> }>()
  const sent: WindowMessage[] = []
  function channel(): ChannelLike {
    const me = { fns: new Set<(ev: MessageEvent) => void>() }
    members.add(me)
    return {
      postMessage(m) {
        sent.push(m)
        for (const other of members) if (other !== me) for (const fn of other.fns) fn({ data: structuredClone(m) } as MessageEvent)
      },
      addEventListener: (_t, fn) => me.fns.add(fn),
      removeEventListener: (_t, fn) => me.fns.delete(fn),
      close: () => members.delete(me),
    }
  }
  return { channel, sent }
}

/** Intervals run by hand, with the clock they share. */
function clock() {
  let now = 0
  const intervals = new Map<number, { fn: () => void; ms: number; next: number }>()
  let id = 0
  return {
    now: () => now,
    timers: {
      setInterval(fn: () => void, ms: number) {
        intervals.set(++id, { fn, ms, next: now + ms })
        return id
      },
      clearInterval(h: unknown) {
        intervals.delete(h as number)
      },
    },
    advance(ms: number) {
      const end = now + ms
      for (;;) {
        let due: { fn: () => void; ms: number; next: number } | null = null
        for (const t of intervals.values()) if (t.next <= end && (due === null || t.next < due.next)) due = t
        if (due === null) break
        now = due.next
        due.next += due.ms
        due.fn()
      }
      now = end
    },
    running: () => intervals.size,
  }
}

interface FakeWindow {
  closed: boolean
  focus: ReturnType<typeof vi.fn>
  close(): void
  location: { href: string }
}

function fakeWindow(href: string): FakeWindow {
  return {
    closed: false,
    focus: vi.fn(),
    close() {
      this.closed = true
    },
    location: { href },
  }
}

function memoryStorage(): Storage {
  const m = new Map<string, string>()
  return { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v), removeItem: (k: string) => void m.delete(k) } as unknown as Storage
}

const AGENT: TerminalTarget = { kind: 'agent', id: 't1' }
const SHELL: TerminalTarget = { kind: 'user', id: 's1' }

function setup(o: { blocked?: boolean; channel?: ChannelLike | null; c?: ReturnType<typeof clock>; h?: ReturnType<typeof hub> } = {}) {
  const c = o.c ?? clock()
  const h = o.h ?? hub()
  const windows = new Map<string, FakeWindow>()
  const opened: { url: string; name: string; features: string }[] = []
  const posted: ((data: unknown, origin: string) => void)[] = []
  const storage = memoryStorage()
  const deps: PopoutDeps = {
    open(url, name, features) {
      opened.push({ url, name, features })
      if (o.blocked) return null
      let w = windows.get(name)
      if (!w || w.closed) {
        w = fakeWindow(url === '' ? 'about:blank' : url)
        windows.set(name, w)
      } else if (url !== '') w.location.href = url
      return w as unknown as Window
    },
    channel: o.channel === undefined ? h.channel() : o.channel,
    listen(fn) {
      posted.push(fn)
      return () => posted.splice(posted.indexOf(fn), 1)
    },
    origin: 'http://omo.localhost:8090',
    timers: c.timers,
    now: c.now,
    storage: () => storage,
    screen: { availWidth: 1920, availHeight: 1080 },
  }
  const p = createPopouts(deps)
  return { p, c, h, windows, opened, posted, storage, deps }
}

/** The terminal window's side of a window `setup` opened. */
function windowSide(t: TerminalTarget, s: ReturnType<typeof setup>, instance = 'w1') {
  const close = vi.fn()
  let hide: () => void = () => {}
  const presence = announceTerminalWindow(t, {
    channel: s.h.channel(),
    opener: null,
    origin: s.deps.origin,
    timers: s.c.timers,
    close,
    onPageHide(fn) {
      hide = fn
      return () => (hide = () => {})
    },
    instance,
  })
  return { presence, close, pagehide: () => hide() }
}

describe('opening', () => {
  it('opens the terminal page in a window named for the terminal, sized as remembered, and shows it as out', () => {
    const s = setup()
    expect(s.p.stateOf(AGENT)).toBe('inline')
    expect(s.p.open(AGENT, 'agent')).toBe('opened')
    expect(s.opened).toEqual([{ url: '/terminal/agent/t1', name: 'omo-terminal-agent-t1', features: `popup,width=${DEFAULT_SIZE.width},height=${DEFAULT_SIZE.height}` }])
    expect(s.p.stateOf(AGENT)).toBe('out')
    expect(s.windows.get('omo-terminal-agent-t1')!.focus).toHaveBeenCalled()
  })

  it('a second press brings the open window to the front instead of opening another', () => {
    const s = setup()
    s.p.open(AGENT, 'agent')
    const w = s.windows.get('omo-terminal-agent-t1')!
    w.focus.mockClear()
    expect(s.p.open(AGENT, 'agent')).toBe('shown')
    expect(s.opened).toHaveLength(1)
    expect(w.focus).toHaveBeenCalledTimes(1)
    expect(w.location.href).toBe('/terminal/agent/t1')
  })

  it('several terminals are out at once, each in its own window', () => {
    const s = setup()
    s.p.open(AGENT, 'agent')
    s.p.open(SHELL, 'user')
    expect(s.windows.size).toBe(2)
    expect(s.p.stateOf(AGENT)).toBe('out')
    expect(s.p.stateOf(SHELL)).toBe('out')
    s.p.bringBack(AGENT)
    expect(s.p.stateOf(AGENT)).toBe('inline')
    expect(s.p.stateOf(SHELL)).toBe('out')
  })

  it('a window the browser blocked: the terminal stays in place with the blocked note, until dismissed or opened', () => {
    const s = setup({ blocked: true })
    expect(s.p.open(AGENT, 'agent')).toBe('blocked')
    expect(s.p.stateOf(AGENT)).toBe('blocked')
    expect(s.c.running()).toBe(0)
    s.p.dismissBlocked(AGENT)
    expect(s.p.stateOf(AGENT)).toBe('inline')
  })

  it('the window size is remembered per kind, kept on the screen, and blocked storage gives the default', () => {
    const s = setup()
    writeWindowSize(() => s.storage, 'ceo', { width: 1200, height: 5000 })
    expect(readWindowSize(() => s.storage, 'ceo', { availWidth: 1920, availHeight: 1080 })).toEqual({ width: 1200, height: 1080 })
    expect(readWindowSize(() => s.storage, 'agent')).toEqual(DEFAULT_SIZE)
    s.p.open(AGENT, 'ceo')
    expect(s.opened[0]!.features).toBe('popup,width=1200,height=1080')
    const blocked = () => {
      throw new DOMException('blocked', 'SecurityError')
    }
    expect(readWindowSize(blocked, 'ceo')).toEqual(DEFAULT_SIZE)
    expect(() => writeWindowSize(blocked, 'ceo', { width: 1, height: 1 })).not.toThrow()
    s.storage.setItem('omo.terminalWindow.size.user', 'nonsense')
    expect(readWindowSize(() => s.storage, 'user')).toEqual(DEFAULT_SIZE)
  })
})

describe('closing', () => {
  it('closed by message: the window says gone on pagehide, and the poll sees it closed', () => {
    const s = setup()
    s.p.open(AGENT, 'agent')
    const side = windowSide(AGENT, s)
    expect(s.p.stateOf(AGENT)).toBe('out')
    // A reload of the window says gone too, but the window is still open.
    side.pagehide()
    s.c.advance(POLL_MS)
    expect(s.p.stateOf(AGENT)).toBe('out')
    s.windows.get('omo-terminal-agent-t1')!.closed = true
    s.c.advance(POLL_MS)
    expect(s.p.stateOf(AGENT)).toBe('inline')
    expect(s.c.running()).toBe(1) // the window's own heartbeat; this page's poll stopped
  })

  it('closed by poll: a window the system closed without a word', () => {
    const s = setup()
    s.p.open(AGENT, 'agent')
    s.windows.get('omo-terminal-agent-t1')!.closed = true
    expect(s.p.stateOf(AGENT)).toBe('out')
    s.c.advance(POLL_MS)
    expect(s.p.stateOf(AGENT)).toBe('inline')
    expect(s.c.running()).toBe(0)
  })

  it('Bring it back closes the window and asks every window of the terminal to close', () => {
    const s = setup()
    s.p.open(AGENT, 'agent')
    const side = windowSide(AGENT, s)
    s.p.bringBack(AGENT)
    expect(s.windows.get('omo-terminal-agent-t1')!.closed).toBe(true)
    expect(side.close).toHaveBeenCalledTimes(1)
    expect(s.p.stateOf(AGENT)).toBe('inline')
  })

  it('a terminal whose process ended in its window shows in place again, with the window still open', () => {
    const s = setup()
    s.p.open(AGENT, 'agent')
    const side = windowSide(AGENT, s)
    side.presence.ended()
    expect(s.p.stateOf(AGENT)).toBe('inline')
    s.c.advance(EXPIRE_MS * 2)
    expect(s.p.stateOf(AGENT)).toBe('inline')
  })
})

describe('found again after a reload', () => {
  it('a page that lost its reference asks who is open and shows the placeholder; Show the window finds it by name without reloading it', () => {
    const c = clock()
    const h = hub()
    const first = setup({ c, h })
    first.p.open(AGENT, 'agent')
    const side = windowSide(AGENT, first)
    first.p.dispose()

    // The reloaded page: a fresh instance, the same windows.
    const again = setup({ c, h })
    for (const [k, w] of first.windows) again.windows.set(k, w)
    expect(again.p.stateOf(AGENT)).toBe('out')
    again.p.show(AGENT)
    expect(again.opened).toEqual([{ url: '', name: 'omo-terminal-agent-t1', features: '' }])
    const w = again.windows.get('omo-terminal-agent-t1')!
    expect(w.location.href).toBe('/terminal/agent/t1')
    expect(w.focus).toHaveBeenCalled()

    // Its heartbeat keeps it out; a window that crashed falls silent and expires.
    c.advance(EXPIRE_MS * 2)
    expect(again.p.stateOf(AGENT)).toBe('out')
    side.presence.dispose()
    expect(again.p.stateOf(AGENT)).toBe('out') // held: the poll asks the window
    w.closed = true
    c.advance(POLL_MS)
    expect(again.p.stateOf(AGENT)).toBe('inline')
  })

  it('without a reference, a window that said gone gets a short grace (it may be reloading), then the terminal shows in place', () => {
    const c = clock()
    const h = hub()
    const s = setup({ c, h })
    // A window opened by address in another tab: this page never held it.
    const leaving = windowSide(SHELL, s, 'b')
    expect(s.p.stateOf(SHELL)).toBe('out')
    leaving.presence.dispose()
    c.advance(POLL_MS)
    expect(s.p.stateOf(SHELL)).toBe('out')
    c.advance(GONE_GRACE_MS)
    expect(s.p.stateOf(SHELL)).toBe('inline')
    // A reload says here again within the grace and stays out.
    const reloading = windowSide(AGENT, s, 'c')
    reloading.presence.dispose()
    windowSide(AGENT, s, 'd')
    c.advance(GONE_GRACE_MS * 3)
    expect(s.p.stateOf(AGENT)).toBe('out')
  })

  it('a window that crashed stops beating and expires', () => {
    const c = clock()
    const h = hub()
    const s = setup({ c, h })
    // Only the channel, no timers: one "here" and then silence.
    const ch = h.channel()
    ch.postMessage({ type: 'here', key: 'agent:t1', instance: 'x', ended: false })
    expect(s.p.stateOf(AGENT)).toBe('out')
    c.advance(EXPIRE_MS - POLL_MS)
    expect(s.p.stateOf(AGENT)).toBe('out')
    c.advance(POLL_MS * 2)
    expect(s.p.stateOf(AGENT)).toBe('inline')
  })

  it('a window message counts only from this origin, and garbage is ignored', () => {
    const s = setup({ channel: null })
    const here = { type: 'here', key: 'agent:t1', instance: 'x', ended: false }
    for (const fn of s.posted) fn(here, 'http://evil.example')
    for (const fn of s.posted) fn({ type: 'here', key: 1 }, s.deps.origin)
    for (const fn of s.posted) fn('here', s.deps.origin)
    expect(s.p.stateOf(AGENT)).toBe('inline')
    for (const fn of s.posted) fn(here, s.deps.origin)
    expect(s.p.stateOf(AGENT)).toBe('out')
  })
})

describe('the terminal window', () => {
  it('says here, answers who, beats, closes when asked for its terminal only, and says gone when it goes', () => {
    const c = clock()
    const h = hub()
    const listen = h.channel()
    const got: WindowMessage[] = []
    listen.addEventListener('message', (ev) => got.push(ev.data as WindowMessage))
    const opener = { postMessage: vi.fn() }
    const close = vi.fn()
    const presence = announceTerminalWindow(AGENT, {
      channel: h.channel(),
      opener,
      origin: 'http://omo.localhost:8090',
      timers: c.timers,
      close,
      onPageHide: () => () => {},
      instance: 'w1',
    })
    expect(got).toEqual([{ type: 'here', key: 'agent:t1', instance: 'w1', ended: false }])
    expect(opener.postMessage).toHaveBeenCalledWith({ type: 'here', key: 'agent:t1', instance: 'w1', ended: false }, 'http://omo.localhost:8090')
    listen.postMessage({ type: 'who' })
    expect(got).toHaveLength(2)
    c.advance(2000)
    expect(got).toHaveLength(3)
    listen.postMessage({ type: 'close', key: 'user:t1' })
    expect(close).not.toHaveBeenCalled()
    listen.postMessage({ type: 'close', key: 'agent:t1' })
    expect(close).toHaveBeenCalledTimes(1)
    presence.ended()
    expect(got.at(-1)).toEqual({ type: 'here', key: 'agent:t1', instance: 'w1', ended: true })
    presence.dispose()
    expect(got.at(-1)).toEqual({ type: 'gone', key: 'agent:t1', instance: 'w1' })
    expect(c.running()).toBe(0)
  })
})
