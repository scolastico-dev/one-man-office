// The board store: the one place the board's data lives. It is filled from a
// snapshot and afterwards changed only by the event stream. Answers to the
// user's own requests never write ticket data; a move shows at once as an
// overlay and counts as done when the stream has delivered the event id the
// server answered with.

import { computed, ref, shallowReactive, type ComputedRef, type Ref } from 'vue'
import { ApiError, type ApiClient } from '../api/client'
import { logError } from '../api/log'
import { getServer, getSettings, listProjects, listTickets, moveTicket, rejectTicket } from '../api/endpoints'
import { connectEventStream, type EventStreamConnection, type StreamState, type StreamTimers } from '../api/eventStream'
import type { ApiEvent, Column, General, Project, ReviewStage, SettingsChangedData, TicketList, TicketSummary } from '../api/types'
import { COLUMNS } from '../api/types'
import { slotView, type SlotView } from '../board/slots'
import { applyEvent, compareDone, emptyState, installSnapshot, mergeTickets, staleTickets, type BoardState } from './boardState'

/**
 * How long a move waits for its event before the store shows what the server
 * says. The time only runs while the stream is live: it stops while the
 * stream reconnects or resyncs and starts over once it is live again.
 */
export const CONFIRM_TIMEOUT_MS = 10_000
/** First delay before a failed resync snapshot is asked for again; doubles up to the maximum. */
export const RESYNC_RETRY_MS = 3000
export const RESYNC_RETRY_MAX_MS = 30_000
/** Rank distance used for an overlay at either end of a column. */
const RANK_STEP = 1024

/** A move shown before the stream confirmed it. */
export interface PendingMove {
  column: Column
  rank: number
  /** The review stage shown; absent keeps the ticket's stage within its column and clears it elsewhere. */
  stage?: ReviewStage
  requestId: number
  /** The event id the server answered with; null while the request is under way. */
  waitFor: number | null
}

/**
 * How a move ended. `applied`: the server accepted it, and the stream
 * delivered it or the wait for it ran out. `unknown`: the answer never arrived
 * (a network error), so the server may or may not have made the move; the
 * board shows what the server did.
 */
export type MoveOutcome = 'applied' | 'unknown'

/** The cards of one column. `all` is in rank order (Done: most recently closed first); `stories` and `tasks` keep that order. */
export interface ColumnTickets {
  all: TicketSummary[]
  stories: TicketSummary[]
  tasks: TicketSummary[]
}

export interface BoardStore {
  state: Readonly<BoardState>
  connection: Readonly<Ref<StreamState>>
  /** The delay before the next connection attempt while `retrying`. */
  retryInMs: Readonly<Ref<number | null>>
  /** True while a snapshot is being loaded and live events are held back. */
  syncing: Readonly<Ref<boolean>>
  /** True when the board may lag behind the server: not live, or resyncing. */
  stale: Readonly<Ref<boolean>>
  pending: ReadonlyMap<number, PendingMove>
  /** Every loaded ticket by column, pending overlays applied. */
  columns: ComputedRef<Record<Column, ColumnTickets>>
  counts: ComputedRef<Record<Column, number>>
  /** Tasks and stories in work against their limits; null until the settings are loaded. */
  slots: ComputedRef<SlotView | null>
  /** The loaded tasks of a story, by number, pending overlays applied. */
  tasksOf(story: number): TicketSummary[]
  /** The summary with any pending overlay applied. */
  view(n: number): TicketSummary | undefined
  start(): Promise<void>
  stop(): void
  reconnectNow(): void
  /**
   * Loads the next older Done page. A page that stays stale for some of its
   * tickets after two more reads is merged without them and `doneBefore` stays
   * where it was, so the next call reads the page again.
   */
  loadOlderDone(): Promise<void>
  /**
   * Shows the move at once and sends it (`reject` sends a rejection). Resolves
   * with `applied` when the stream delivered the confirming event, or after
   * 10 s without it; rejects with the server's error at once when the server
   * refuses. When the answer is lost (the client's `network` error), the move
   * stays shown until the stream brings news of the ticket, or for up to 10 s,
   * and then resolves with `unknown`: the board shows what the server did.
   */
  requestMove(n: number, to: Column, before: number | null, opts?: { reject?: { comment: string } }): Promise<MoveOutcome>
  refreshProjects(): Promise<void>
  /**
   * Adds tickets from a list answer (search hits); never replaces newer
   * entries. Returns the numbers left out because an event about them newer
   * than the list was skipped earlier: fetch those again.
   */
  mergeTickets(list: TicketList): number[]
  /** Handlers for one kind, or `*` for every kind; they run after the store applied the event. */
  onEvent(kind: string, fn: (ev: ApiEvent) => void): () => void
  /** Runs after every snapshot is installed (start, resync); events before it may have been skipped. */
  onSnapshot(fn: () => void): () => void
}

export interface BoardStoreDeps {
  client: ApiClient
  connect?: typeof connectEventStream
  timers?: StreamTimers
}

interface Snapshot {
  list: TicketList
  projects: Project[]
  paused: boolean
  general: General
}

interface Resync {
  /** The server's resume point; null after a rejected resume id. */
  latest: number | null
  buffer: ApiEvent[]
  /** Reconnect once installed (the old resume id was refused). */
  reconnect: boolean
  attempt: number
  delay: number
  timer: unknown
}

interface Waiter {
  n: number
  requestId: number
  /** Whether the stream has delivered what the waiter waits for. */
  done(): boolean
  outcome: MoveOutcome
  timer: unknown
  resolve(o: MoveOutcome): void
}

const byRank = (a: TicketSummary, b: TicketSummary) => a.rank - b.rank || a.number - b.number

function report(e: unknown): void {
  logError('board', e)
}

export function createBoardStore(deps: BoardStoreDeps): BoardStore {
  const { client } = deps
  const connect = deps.connect ?? connectEventStream
  const timers: StreamTimers = deps.timers ?? {
    setTimeout: (fn, ms) => setTimeout(fn, ms),
    clearTimeout: (h) => clearTimeout(h as ReturnType<typeof setTimeout>),
  }

  const state = shallowReactive<BoardState>({
    ...emptyState(),
    tickets: shallowReactive(new Map()),
    projects: shallowReactive(new Map()),
  })
  const pending = shallowReactive(new Map<number, PendingMove>())
  const connection = ref<StreamState>('closed')
  const retryInMs = ref<number | null>(null)
  const syncing = ref(false)
  const stale = computed(() => connection.value !== 'live' || syncing.value)

  const handlers = new Map<string, Set<(ev: ApiEvent) => void>>()
  const snapshotHooks = new Set<() => void>()
  const waiters = new Set<Waiter>()
  /** The end of the line of move and reject requests: they go out one after the other, whatever ticket they are for. */
  let queue: Promise<void> | null = null

  let stream: EventStreamConnection | null = null
  let starting: Promise<void> | null = null
  let resync: Resync | null = null
  /** Changes on stop(); work begun before is dropped when it finishes. */
  let epoch = 0
  let nextRequestId = 0
  /** Counts project changes, so a project list read across one is read again. */
  let projectMark = 0
  let settingsToken = 0
  let olderLoad: Promise<void> | null = null

  // Selectors

  function view(n: number): TicketSummary | undefined {
    const e = state.tickets.get(n)
    if (e === undefined) return undefined
    const p = pending.get(n)
    if (p === undefined) return e.t
    const stage = p.stage ?? (p.column === e.t.column ? e.t.review_stage : '')
    return { ...e.t, column: p.column, rank: p.rank, review_stage: stage }
  }

  const columns = computed(() => {
    const out = {} as Record<Column, ColumnTickets>
    for (const c of COLUMNS) out[c] = { all: [], stories: [], tasks: [] }
    for (const n of state.tickets.keys()) {
      const t = view(n)
      if (t !== undefined) out[t.column].all.push(t)
    }
    for (const c of COLUMNS) {
      const col = out[c]
      col.all.sort(c === 'done' ? compareDone : byRank)
      for (const t of col.all) (t.type === 'story' ? col.stories : col.tasks).push(t)
    }
    return out
  })

  const slots = computed(() => slotView([...state.tickets.values()].map((e) => e.t), state.general?.limits))

  const counts = computed(() => {
    const out = {} as Record<Column, number>
    for (const c of COLUMNS) out[c] = columns.value[c].all.length
    return out
  })

  const storyTasks = computed(() => {
    const out = new Map<number, TicketSummary[]>()
    for (const n of state.tickets.keys()) {
      const t = view(n)
      if (t === undefined || t.story === null) continue
      const list = out.get(t.story)
      if (list) list.push(t)
      else out.set(t.story, [t])
    }
    for (const list of out.values()) list.sort((a, b) => a.number - b.number)
    return out
  })

  // Loading

  async function loadSnapshot(): Promise<Snapshot> {
    const list = (await listTickets(client)).data
    // Read after the tickets, so the events after the snapshot cover any change in between.
    const [projects, server, settings] = await Promise.all([listProjects(client, true), getServer(client), getSettings(client)])
    return { list, projects: projects.data, paused: server.data.paused, general: settings.data.settings }
  }

  /** Installs a whole snapshot in one synchronous step. */
  function install(s: Snapshot): void {
    installSnapshot(state, s.list)
    state.projects.clear()
    for (const p of s.projects) state.projects.set(p.id, p)
    state.paused = s.paused
    state.general = s.general
    projectMark++
    settingsToken++
  }

  function afterSnapshot(): void {
    checkWaiters()
    for (const fn of [...snapshotHooks]) {
      try {
        fn()
      } catch (e) {
        report(e)
      }
    }
  }

  function resumeFrom(): number | null {
    if (resync === null) return state.lastEventId
    let from = resync.latest ?? state.lastEventId
    for (const ev of resync.buffer) from = Math.max(from, ev.id)
    return from
  }

  function start(): Promise<void> {
    if (starting) return starting
    const my = epoch
    connection.value = 'connecting'
    syncing.value = true
    const p = (async () => {
      let snap: Snapshot
      try {
        snap = await loadSnapshot()
      } catch (e) {
        if (my === epoch) {
          starting = null
          syncing.value = false
          connection.value = 'closed'
        }
        throw e
      }
      if (my !== epoch) return
      install(snap)
      syncing.value = false
      afterSnapshot()
      stream = connect({
        client,
        resumeFrom,
        handlers: {
          onEvent: (ev) => guard(() => receive(ev)),
          onResync: (latest) => guard(() => beginResync(latest, false)),
          onResumeRejected: () => guard(() => beginResync(null, true)),
          onState: (s, ms) => {
            connection.value = s
            retryInMs.value = s === 'retrying' && ms !== undefined ? ms : null
            armWaiters()
          },
        },
        ...(deps.timers ? { timers: deps.timers } : {}),
      })
    })()
    starting = p
    return p
  }

  /** The stream must never see an exception from its handlers. */
  function guard(fn: () => void): void {
    try {
      fn()
    } catch (e) {
      report(e)
    }
  }

  // Events

  function receive(ev: ApiEvent): void {
    if (resync !== null) {
      resync.buffer.push(ev)
      return
    }
    apply(ev)
    checkWaiters()
  }

  function apply(ev: ApiEvent): void {
    if (!applyEvent(state, ev)) return
    if (ev.kind === 'project.changed' || ev.kind === 'project.removed') projectMark++
    if (ev.kind === 'settings.changed' && (ev.data as SettingsChangedData | null)?.section === 'general') {
      void refreshSettings()
    }
    for (const kind of [ev.kind, '*']) {
      const set = handlers.get(kind)
      if (!set) continue
      for (const fn of [...set]) {
        try {
          fn(ev)
        } catch (e) {
          report(e)
        }
      }
    }
  }

  async function refreshSettings(): Promise<void> {
    const my = ++settingsToken
    try {
      const res = await getSettings(client)
      if (my === settingsToken) state.general = res.data.settings
    } catch (e) {
      report(e)
    }
  }

  // Resync: hold back live events, take a new snapshot, then apply the held
  // events newer than it.

  function beginResync(latest: number | null, reconnect: boolean): void {
    if (resync !== null) {
      resync.reconnect ||= reconnect
      // A refused resume id while a snapshot loads: that snapshot serves.
      if (latest === null) return
      // The server skipped events again; the snapshot under way may predate them.
      resync.latest = latest
      if (resync.timer !== null) timers.clearTimeout(resync.timer)
      resync.timer = null
      resync.delay = RESYNC_RETRY_MS
      void loadResync(resync)
      return
    }
    resync = { latest, buffer: [], reconnect, attempt: 0, delay: RESYNC_RETRY_MS, timer: null }
    syncing.value = true
    armWaiters()
    void loadResync(resync)
  }

  async function loadResync(r: Resync): Promise<void> {
    const attempt = ++r.attempt
    let snap: Snapshot
    try {
      snap = await loadSnapshot()
    } catch (e) {
      if (resync !== r || r.attempt !== attempt) return
      report(e)
      r.timer = timers.setTimeout(() => {
        r.timer = null
        if (resync === r && r.attempt === attempt) void loadResync(r)
      }, r.delay)
      r.delay = Math.min(RESYNC_RETRY_MAX_MS, r.delay * 2)
      return
    }
    if (resync !== r || r.attempt !== attempt) return
    resync = null
    install(snap)
    // installSnapshot set lastEventId to the snapshot's id, so the held events
    // it already reflects are skipped.
    for (const ev of r.buffer.sort((a, b) => a.id - b.id)) apply(ev)
    syncing.value = false
    afterSnapshot()
    armWaiters()
    if (r.reconnect) stream?.reconnectNow()
  }

  // Moves

  function overlayRank(n: number, column: Column, before: number | null): number {
    const cards = columns.value[column].all.filter((t) => t.number !== n)
    if (before !== null) {
      const i = cards.findIndex((t) => t.number === before)
      const next = cards[i]
      if (next !== undefined) {
        const prev = cards[i - 1]
        return prev === undefined ? next.rank - RANK_STEP : (prev.rank + next.rank) / 2
      }
    }
    const last = cards[cards.length - 1]
    return last === undefined ? RANK_STEP : last.rank + RANK_STEP
  }

  function endOverlay(n: number, requestId: number): void {
    if (pending.get(n)?.requestId === requestId) pending.delete(n)
  }

  function finish(w: Waiter): void {
    if (!waiters.delete(w)) return
    if (w.timer !== null) timers.clearTimeout(w.timer)
    endOverlay(w.n, w.requestId)
    w.resolve(w.outcome)
  }

  function checkWaiters(): void {
    if (resync !== null) return
    for (const w of [...waiters]) if (w.done()) finish(w)
  }

  /** Keeps the overlay until done() holds, or for CONFIRM_TIMEOUT_MS. */
  function wait(n: number, requestId: number, done: () => boolean, outcome: MoveOutcome): Promise<MoveOutcome> {
    return new Promise((resolve) => {
      if (resync === null && done()) {
        endOverlay(n, requestId)
        resolve(outcome)
        return
      }
      const w: Waiter = { n, requestId, done, outcome, timer: null, resolve }
      waiters.add(w)
      armWaiters()
    })
  }

  /** Runs the waiters' timeouts while the stream is live and stops them otherwise. */
  function armWaiters(): void {
    const live = connection.value === 'live' && resync === null
    for (const w of waiters) {
      if (live && w.timer === null) {
        w.timer = timers.setTimeout(() => {
          w.timer = null
          finish(w)
        }, CONFIRM_TIMEOUT_MS)
      } else if (!live && w.timer !== null) {
        timers.clearTimeout(w.timer)
        w.timer = null
      }
    }
  }

  function awaitEvent(n: number, requestId: number, eventId: number | null): Promise<MoveOutcome> {
    const p = pending.get(n)
    if (p?.requestId === requestId) pending.set(n, { ...p, waitFor: eventId })
    if (eventId === null) {
      endOverlay(n, requestId)
      return Promise.resolve('applied')
    }
    return wait(n, requestId, () => state.lastEventId >= eventId, 'applied')
  }

  /**
   * The answer to a move was lost. No event id is known, so any event about
   * the ticket newer than what the store had when the request went out ends
   * the wait: it is the move itself, or it shows the server's state after it.
   */
  function awaitUnknown(n: number, requestId: number, sentAt: number): Promise<MoveOutcome> {
    return wait(n, requestId, () => (state.tickets.get(n)?.at ?? 0) > sentAt, 'unknown')
  }

  function requestMove(n: number, to: Column, before: number | null, opts: { reject?: { comment: string } } = {}): Promise<MoveOutcome> {
    const cur = view(n)
    if (cur === undefined) return Promise.reject(new Error(`Ticket #${n} is not on the board.`))
    const requestId = ++nextRequestId
    const my = epoch
    const reject = opts.reject
    // Approving a human review keeps the card in Review until omo has merged or exported it; a task
    // without a project has nothing to merge or export and goes to Done at once.
    const approval = reject === undefined && cur.column === 'review' && to === 'done' && !(cur.type === 'task' && cur.projects.length === 0)
    const column: Column = reject !== undefined ? 'in_progress' : to
    const move: PendingMove = approval
      ? { column: 'review', rank: cur.rank, stage: cur.effective_when_approved === 'export' ? 'exporting' : 'merging', requestId, waitFor: null }
      : { column, rank: overlayRank(n, column, before), requestId, waitFor: null }
    pending.set(n, move)

    // Moves and rejections go out one after the other for all tickets: a move names a neighbour as `before`,
    // and the server must have settled the neighbour's own move by then.
    let sentAt = 0
    const send = () => {
      sentAt = resumeFrom() ?? state.lastEventId
      return reject !== undefined ? rejectTicket(client, n, reject.comment) : moveTicket(client, n, to, before)
    }
    const prev = queue
    const sent = prev === null ? send() : prev.then(send)
    const settled = sent.then(
      () => undefined,
      () => undefined,
    )
    queue = settled
    void settled.then(() => {
      if (queue === settled) queue = null
    })

    return sent.then(
      (res): MoveOutcome | Promise<MoveOutcome> => {
        if (my !== epoch) return 'applied'
        return awaitEvent(n, requestId, res.eventId)
      },
      (err: unknown): MoveOutcome | Promise<MoveOutcome> => {
        if (err instanceof ApiError && err.code === 'network') {
          if (my !== epoch) return 'unknown'
          return awaitUnknown(n, requestId, sentAt)
        }
        if (my === epoch) endOverlay(n, requestId)
        throw err
      },
    )
  }

  // Lists

  function loadOlderDone(): Promise<void> {
    if (olderLoad) return olderLoad
    const before = state.doneBefore
    if (before === null) return Promise.resolve()
    const p = (async () => {
      try {
        for (let attempt = 0; ; attempt++) {
          const page = (await listTickets(client, { column: 'done', done_before: before })).data
          // A snapshot replaced the Done pages meanwhile.
          if (state.doneBefore !== before) return
          // An event about a ticket on this page was left out after the page was read.
          if (attempt < 2 && staleTickets(state, page).length > 0) continue
          const stale = mergeTickets(state, page)
          if (stale.length === 0) state.doneBefore = page.next_done_before ?? null
          return
        }
      } finally {
        olderLoad = null
      }
    })()
    olderLoad = p
    return p
  }

  async function refreshProjects(): Promise<void> {
    for (let attempt = 0; ; attempt++) {
      const mark = projectMark
      const list = (await listProjects(client, true)).data
      if (mark !== projectMark && attempt < 2) continue
      state.projects.clear()
      for (const p of list) state.projects.set(p.id, p)
      return
    }
  }

  function stop(): void {
    epoch++
    stream?.close()
    stream = null
    starting = null
    if (resync?.timer != null) timers.clearTimeout(resync.timer)
    resync = null
    syncing.value = false
    for (const w of [...waiters]) finish(w)
    pending.clear()
    connection.value = 'closed'
    retryInMs.value = null
  }

  function onEvent(kind: string, fn: (ev: ApiEvent) => void): () => void {
    let set = handlers.get(kind)
    if (!set) handlers.set(kind, (set = new Set()))
    set.add(fn)
    return () => {
      set.delete(fn)
    }
  }

  function onSnapshot(fn: () => void): () => void {
    snapshotHooks.add(fn)
    return () => {
      snapshotHooks.delete(fn)
    }
  }

  return {
    state,
    connection,
    retryInMs,
    syncing,
    stale,
    pending,
    columns,
    counts,
    slots,
    tasksOf: (story) => storyTasks.value.get(story) ?? [],
    view,
    start,
    stop,
    reconnectNow: () => stream?.reconnectNow(),
    loadOlderDone,
    requestMove,
    refreshProjects,
    mergeTickets: (list) => mergeTickets(state, list),
    onEvent,
    onSnapshot,
  }
}
