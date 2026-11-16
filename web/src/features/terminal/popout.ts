// Terminals in windows of their own.
//
// A terminal can be popped out into its own browser window (the page at
// `/terminal/<kind>/<id>`). While it is out, every other view of it in the
// app's windows shows a placeholder instead of a live view: two views of one
// terminal fight over its size, and the last resize wins.
//
// The windows of the app talk over one `BroadcastChannel` (same origin by
// definition): a terminal window says "here" when it loads, again every
// HEARTBEAT_MS and whenever a page asks "who" (after a reload, when the page
// lost its reference to the window), and "gone" on `pagehide`. The page that
// opened a window also holds its reference and polls `closed`, which covers a
// window the system closed without `pagehide`. A window that stops saying
// "here" (crashed, killed) counts as gone after EXPIRE_MS. A terminal whose
// process ended counts as back: the views show their normal ended state.

import { shallowRef, type Ref } from 'vue'
import { keyOf, windowNameOf, windowPathOf, type TerminalTarget, type WindowSizeKind } from './terminalTarget'

export const CHANNEL_NAME = 'omo.terminal-windows'
export const HEARTBEAT_MS = 2000
export const EXPIRE_MS = 6000
export const POLL_MS = 500
/** A window that said "gone" while this page holds no reference to it may only be loading again: it counts as open this long. */
export const GONE_GRACE_MS = 1000
export const SIZE_KEY = 'omo.terminalWindow.size'
export const DEFAULT_SIZE = { width: 960, height: 640 } as const
const MIN_SIZE = { width: 480, height: 320 } as const

/** What the windows of the app say to each other about terminal windows. */
export type WindowMessage =
  | { type: 'here'; key: string; instance: string; ended: boolean }
  | { type: 'gone'; key: string; instance: string }
  | { type: 'who' }
  | { type: 'close'; key: string }

/** The part of `BroadcastChannel` used here. */
export interface ChannelLike {
  postMessage(m: WindowMessage): void
  addEventListener(type: 'message', fn: (ev: MessageEvent) => void): void
  removeEventListener(type: 'message', fn: (ev: MessageEvent) => void): void
  close(): void
}

export interface Timers {
  setInterval(fn: () => void, ms: number): unknown
  clearInterval(h: unknown): void
}

/** How a terminal shows in this window: live in place, out in its own window, or live with the note that the window was blocked. */
export type PopoutState = 'inline' | 'out' | 'blocked'

export interface Popouts {
  /** How each terminal (by `keyOf`) shows; a terminal not listed is `inline`. */
  readonly states: Readonly<Ref<ReadonlyMap<string, PopoutState>>>
  stateOf(t: TerminalTarget): PopoutState
  /**
   * Opens the terminal in its own window, or shows the one already open.
   * `blocked` when the browser refused the window.
   */
  open(t: TerminalTarget, size: WindowSizeKind): 'opened' | 'shown' | 'blocked'
  /** Brings the terminal's window to the front. */
  show(t: TerminalTarget): void
  /** Closes the terminal's window; the terminal shows in place again at once. */
  bringBack(t: TerminalTarget): void
  /** Forgets that the browser blocked the window. */
  dismissBlocked(t: TerminalTarget): void
  dispose(): void
}

export interface PopoutDeps {
  /** `window.open`. */
  open(url: string, name: string, features: string): Window | null
  /** The channel; null when the browser has none (then only windows opened from this page are known). */
  channel: ChannelLike | null
  /** Messages posted to this window (`window.postMessage`), with their origin. */
  listen(fn: (data: unknown, origin: string) => void): () => void
  origin: string
  timers: Timers
  now(): number
  /** May throw (storage blocked). */
  storage(): Storage
  /** The available screen size, to keep a remembered size on screen. */
  screen?: { availWidth: number; availHeight: number }
}

interface Entry {
  /** The window this page opened, while it may still be open. */
  win: Window | null
  /** Terminal windows that said "here", with when they last did. */
  instances: Map<string, number>
  ended: boolean
  blocked: boolean
}

function isMessage(m: unknown): m is WindowMessage {
  if (typeof m !== 'object' || m === null) return false
  const o = m as Record<string, unknown>
  switch (o.type) {
    case 'here':
      return typeof o.key === 'string' && typeof o.instance === 'string' && typeof o.ended === 'boolean'
    case 'gone':
      return typeof o.key === 'string' && typeof o.instance === 'string'
    case 'who':
      return true
    case 'close':
      return typeof o.key === 'string'
    default:
      return false
  }
}

/** The remembered size of a terminal window of this kind; never throws. */
export function readWindowSize(storage: () => Storage, kind: WindowSizeKind, screen?: { availWidth: number; availHeight: number }): { width: number; height: number } {
  let size: { width: number; height: number } = { ...DEFAULT_SIZE }
  try {
    const raw = storage().getItem(`${SIZE_KEY}.${kind}`)
    const v = raw === null ? null : (JSON.parse(raw) as unknown)
    if (typeof v === 'object' && v !== null) {
      const { width, height } = v as Record<string, unknown>
      if (typeof width === 'number' && typeof height === 'number' && Number.isFinite(width) && Number.isFinite(height)) size = { width, height }
    }
  } catch {
    // Blocked or not readable: the default.
  }
  const maxW = screen && screen.availWidth > 0 ? screen.availWidth : Infinity
  const maxH = screen && screen.availHeight > 0 ? screen.availHeight : Infinity
  return {
    width: Math.round(Math.min(Math.max(size.width, MIN_SIZE.width), maxW)),
    height: Math.round(Math.min(Math.max(size.height, MIN_SIZE.height), maxH)),
  }
}

/** Remembers the size of a terminal window of this kind; never throws. */
export function writeWindowSize(storage: () => Storage, kind: WindowSizeKind, size: { width: number; height: number }): void {
  if (!(size.width > 0 && size.height > 0)) return
  try {
    storage().setItem(`${SIZE_KEY}.${kind}`, JSON.stringify({ width: Math.round(size.width), height: Math.round(size.height) }))
  } catch {
    // Not remembered; the next window gets the default size.
  }
}

/** Whether a window this page holds is closed; a window that cannot be asked counts as open. */
function isClosed(w: Window): boolean {
  try {
    return w.closed
  } catch {
    return false
  }
}

export function createPopouts(deps: PopoutDeps): Popouts {
  const entries = new Map<string, Entry>()
  const states = shallowRef<ReadonlyMap<string, PopoutState>>(new Map())
  let poll: unknown = null

  function entry(key: string): Entry {
    let e = entries.get(key)
    if (!e) {
      e = { win: null, instances: new Map(), ended: false, blocked: false }
      entries.set(key, e)
    }
    return e
  }

  function stateOfEntry(e: Entry): PopoutState {
    if (e.ended) return 'inline'
    if ((e.win !== null && !isClosed(e.win)) || e.instances.size > 0) return 'out'
    return e.blocked ? 'blocked' : 'inline'
  }

  function publish(): void {
    const next = new Map<string, PopoutState>()
    for (const [k, e] of entries) {
      const s = stateOfEntry(e)
      if (s !== 'inline') next.set(k, s)
    }
    const prev = states.value
    let same = prev.size === next.size
    if (same) for (const [k, s] of next) if (prev.get(k) !== s) same = false
    if (!same) states.value = next
    // The poll runs only while a window may be open.
    const watching = [...entries.values()].some((e) => !e.ended && (e.win !== null || e.instances.size > 0))
    if (watching && poll === null) poll = deps.timers.setInterval(tick, POLL_MS)
    else if (!watching && poll !== null) {
      deps.timers.clearInterval(poll)
      poll = null
    }
  }

  function tick(): void {
    const now = deps.now()
    for (const e of entries.values()) {
      if (e.win !== null && isClosed(e.win)) e.win = null
      for (const [id, seen] of e.instances) if (now - seen > EXPIRE_MS) e.instances.delete(id)
    }
    publish()
  }

  function onMessage(m: unknown): void {
    if (!isMessage(m)) return
    switch (m.type) {
      case 'here': {
        const e = entry(m.key)
        if (m.ended) {
          e.ended = true
          e.instances.delete(m.instance)
        } else {
          e.instances.set(m.instance, deps.now())
          e.blocked = false
        }
        break
      }
      case 'gone': {
        const e = entries.get(m.key)
        if (!e) return
        if (!e.instances.has(m.instance)) return
        // The window may only be loading again (a reload): the window this
        // page holds is asked by the poll; one it does not hold gets a moment
        // to say "here" again before the terminal shows in place.
        if (e.win !== null) e.instances.delete(m.instance)
        else e.instances.set(m.instance, deps.now() - EXPIRE_MS + GONE_GRACE_MS)
        break
      }
      default:
        return
    }
    publish()
  }

  const onChannel = (ev: MessageEvent) => onMessage(ev.data)
  deps.channel?.addEventListener('message', onChannel)
  const stopListening = deps.listen((data, origin) => {
    if (origin === deps.origin) onMessage(data)
  })
  // After a reload this page knows no window: the open ones answer.
  deps.channel?.postMessage({ type: 'who' })

  function features(kind: WindowSizeKind): string {
    const { width, height } = readWindowSize(deps.storage, kind, deps.screen)
    return `popup,width=${width},height=${height}`
  }

  /** The terminal's window by its name, without opening a new one; navigates a blank one to the terminal. */
  function findByName(t: TerminalTarget, kind: WindowSizeKind | null): Window | null {
    const w = deps.open('', windowNameOf(t), kind === null ? '' : features(kind))
    if (w === null) return null
    try {
      if (w.location.href === 'about:blank') w.location.href = windowPathOf(t)
    } catch {
      // Not readable: a page of another address; leave it.
    }
    return w
  }

  function show(t: TerminalTarget): void {
    const e = entries.get(keyOf(t))
    let w = e?.win && !isClosed(e.win) ? e.win : null
    if (w === null) {
      w = findByName(t, null)
      if (w !== null && e) e.win = w
    }
    w?.focus()
    publish()
  }

  return {
    states,
    stateOf: (t) => states.value.get(keyOf(t)) ?? 'inline',
    open(t, kind) {
      const key = keyOf(t)
      const e = entry(key)
      e.ended = false
      if ((e.win !== null && !isClosed(e.win)) || e.instances.size > 0) {
        show(t)
        return 'shown'
      }
      const w = deps.open(windowPathOf(t), windowNameOf(t), features(kind))
      if (w === null) {
        e.blocked = true
        publish()
        return 'blocked'
      }
      e.blocked = false
      e.win = w
      w.focus()
      publish()
      return 'opened'
    },
    show,
    bringBack(t) {
      const key = keyOf(t)
      deps.channel?.postMessage({ type: 'close', key })
      const e = entries.get(key)
      if (e) {
        if (e.win !== null) {
          try {
            e.win.close()
          } catch {
            // Gone already.
          }
        }
        e.win = null
        e.instances.clear()
        e.blocked = false
      }
      publish()
    },
    dismissBlocked(t) {
      const e = entries.get(keyOf(t))
      if (e) e.blocked = false
      publish()
    },
    dispose() {
      deps.channel?.removeEventListener('message', onChannel)
      deps.channel?.close()
      stopListening()
      if (poll !== null) deps.timers.clearInterval(poll)
      poll = null
    },
  }
}

function browserChannel(): ChannelLike | null {
  try {
    return typeof BroadcastChannel === 'function' ? (new BroadcastChannel(CHANNEL_NAME) as unknown as ChannelLike) : null
  } catch {
    return null
  }
}

let shared: Popouts | null = null

/** The terminal windows as this browser window knows them; one per page. */
export function usePopouts(): Popouts {
  if (shared === null) {
    shared = createPopouts({
      open: (url, name, f) => window.open(url, name, f),
      channel: browserChannel(),
      listen(fn) {
        const h = (ev: MessageEvent) => fn(ev.data, ev.origin)
        window.addEventListener('message', h)
        return () => window.removeEventListener('message', h)
      },
      origin: window.location.origin,
      timers: { setInterval: (fn, ms) => setInterval(fn, ms), clearInterval: (h) => clearInterval(h as ReturnType<typeof setInterval>) },
      now: () => Date.now(),
      storage: () => window.localStorage,
      screen: window.screen,
    })
  }
  return shared
}

/** Forgets the page's instance (tests). */
export function resetPopouts(): void {
  shared?.dispose()
  shared = null
}

export interface TerminalWindowDeps {
  channel: ChannelLike | null
  /** The window that opened this one, when it is of this origin. */
  opener: { postMessage(m: WindowMessage, origin: string): void } | null
  origin: string
  timers: Timers
  /** Closes this window; the browser may refuse for a window it did not open from a script. */
  close(): void
  /** `pagehide` of this window. */
  onPageHide(fn: () => void): () => void
  instance: string
}

export interface TerminalWindowPresence {
  /** The terminal's process ended: the other windows show it in place again. */
  ended(): void
  /** Another window asked this one to close (Bring it back). */
  onCloseAsked(fn: () => void): void
  /** Stops saying "here" (the window is about to close, or shows the terminal no more). */
  dispose(): void
}

/** The terminal window's side: says "here" until it goes. */
export function announceTerminalWindow(t: TerminalTarget, deps: TerminalWindowDeps): TerminalWindowPresence {
  const key = keyOf(t)
  let ended = false
  let disposed = false
  let closeAsked: () => void = () => deps.close()
  const here = (): WindowMessage => ({ type: 'here', key, instance: deps.instance, ended })
  const say = (m: WindowMessage) => {
    deps.channel?.postMessage(m)
    try {
      deps.opener?.postMessage(m, deps.origin)
    } catch {
      // The opener went away or is of another origin now.
    }
  }
  const onChannel = (ev: MessageEvent) => {
    const m: unknown = ev.data
    if (!isMessage(m)) return
    if (m.type === 'who') say(here())
    else if (m.type === 'close' && m.key === key) closeAsked()
  }
  deps.channel?.addEventListener('message', onChannel)
  const beat = deps.timers.setInterval(() => say(here()), HEARTBEAT_MS)
  const gone = () => say({ type: 'gone', key, instance: deps.instance })
  const stopHide = deps.onPageHide(gone)
  say(here())

  return {
    ended() {
      if (ended || disposed) return
      ended = true
      say(here())
    },
    onCloseAsked(fn) {
      closeAsked = fn
    },
    dispose() {
      if (disposed) return
      disposed = true
      deps.timers.clearInterval(beat)
      stopHide()
      gone()
      deps.channel?.removeEventListener('message', onChannel)
      deps.channel?.close()
    },
  }
}

/** The browser's side of `announceTerminalWindow`. */
export function browserTerminalWindowDeps(): TerminalWindowDeps {
  let opener: TerminalWindowDeps['opener'] = null
  try {
    // Reading the origin of another origin's window throws.
    if (window.opener && (window.opener as Window).location.origin === window.location.origin) opener = window.opener as Window
  } catch {
    opener = null
  }
  return {
    channel: browserChannel(),
    opener,
    origin: window.location.origin,
    timers: { setInterval: (fn, ms) => setInterval(fn, ms), clearInterval: (h) => clearInterval(h as ReturnType<typeof setInterval>) },
    close: () => window.close(),
    onPageHide(fn) {
      window.addEventListener('pagehide', fn)
      return () => window.removeEventListener('pagehide', fn)
    },
    instance: Math.random().toString(36).slice(2) + Date.now().toString(36),
  }
}
