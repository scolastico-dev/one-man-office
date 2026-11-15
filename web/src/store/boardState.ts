// The board's data and the rules that change it. Framework-free: every
// function mutates the state it is given, and the store wraps that state in
// Vue reactivity. Entries are replaced, never mutated in place, so the store
// can keep them shallow.

import type {
  ApiEvent,
  ColumnRenumberedData,
  Column,
  General,
  Project,
  ProjectChangedData,
  ProjectRemovedData,
  ServerPausedData,
  TicketList,
  TicketReorderedData,
  TicketSummary,
} from '../api/types'
import { COLUMNS } from '../api/types'

/** A ticket as the store knows it, and the id of the event or snapshot it reflects. */
export interface Entry {
  t: TicketSummary
  at: number
}

export interface BoardState {
  tickets: Map<number, Entry>
  /** The newest event id the state reflects. */
  lastEventId: number
  /** The `done_before` value of the next older Done page; null when every Done ticket is loaded. */
  doneBefore: number | null
  projects: Map<string, Project>
  paused: boolean
  general: General | null
  loaded: boolean
  /**
   * The newest event about a Done ticket beyond the loaded Done pages that was
   * left out (see `applyEvent`). A Done page read before this id may be stale.
   */
  doneSkippedAt: number
}

export function emptyState(): BoardState {
  return {
    tickets: new Map(),
    lastEventId: 0,
    doneBefore: null,
    projects: new Map(),
    paused: false,
    general: null,
    loaded: false,
    doneSkippedAt: 0,
  }
}

/**
 * Replaces every ticket with the snapshot: the open tickets and the first
 * Done page. Older Done pages loaded before are dropped, because tickets
 * closed meanwhile may lie between them and the new first page.
 *
 * The state then reflects exactly the snapshot, so `lastEventId` becomes its
 * `event_id`, also when that is lower than before (a server whose event log
 * started over).
 */
export function installSnapshot(s: BoardState, list: TicketList): void {
  s.tickets.clear()
  for (const t of list.tickets) s.tickets.set(t.number, { t, at: list.event_id })
  s.lastEventId = list.event_id
  s.doneBefore = list.next_done_before ?? null
  s.loaded = true
}

/**
 * Merges a Done page or search results: inserts tickets the store lacks and
 * replaces entries that are older than the list. Leaves `doneBefore` alone.
 */
export function mergeTickets(s: BoardState, list: TicketList): void {
  for (const t of list.tickets) {
    const e = s.tickets.get(t.number)
    if (e === undefined || e.at < list.event_id) s.tickets.set(t.number, { t, at: list.event_id })
  }
}

/** Done order: most recently closed first, then the higher number first. Negative when a comes first. */
export function compareDone(a: TicketSummary, b: TicketSummary): number {
  const ca = a.closed_at ?? ''
  const cb = b.closed_at ?? ''
  if (ca !== cb) return ca > cb ? -1 : 1
  return b.number - a.number
}

/** Whether t is a Done ticket that belongs to a Done page not loaded yet. */
function beyondLoadedDone(s: BoardState, t: TicketSummary): boolean {
  if (t.column !== 'done' || s.doneBefore === null) return false
  const last = s.tickets.get(s.doneBefore)?.t
  return last !== undefined && compareDone(t, last) > 0
}

function isObject(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null
}

function isSummary(v: unknown): v is TicketSummary {
  return isObject(v) && typeof v.number === 'number' && typeof v.column === 'string' && typeof v.rank === 'number'
}

function isColumn(v: unknown): v is Column {
  return typeof v === 'string' && (COLUMNS as readonly string[]).includes(v)
}

/** Replaces a ticket with the summary an event carries, unless the entry is already as new. */
function upsert(s: BoardState, t: TicketSummary, id: number): void {
  const e = s.tickets.get(t.number)
  if (e !== undefined) {
    if (e.at < id) s.tickets.set(t.number, { t, at: id })
    return
  }
  // A Done ticket on a page that is not loaded comes with that page; taking
  // it now would show it detached from its neighbours.
  if (beyondLoadedDone(s, t)) {
    s.doneSkippedAt = Math.max(s.doneSkippedAt, id)
    return
  }
  s.tickets.set(t.number, { t, at: id })
}

function setRank(s: BoardState, n: number, rank: number, column: Column | null, id: number): void {
  const e = s.tickets.get(n)
  if (e === undefined || e.at >= id) return
  const t: TicketSummary = column === null ? { ...e.t, rank } : { ...e.t, rank, column }
  s.tickets.set(n, { t, at: id })
}

/**
 * Applies one streamed event. Returns false when the event was already
 * reflected (`id <= lastEventId`) and changed nothing; true otherwise, also
 * for kinds the board does not use, which only advance `lastEventId`.
 *
 * Ticket summaries are replaced wholesale; `ticket.moved` may stay in the
 * same column and nothing is inferred from its `from` and `to`. Malformed
 * data is ignored rather than thrown.
 */
export function applyEvent(s: BoardState, ev: ApiEvent): boolean {
  if (ev.id <= s.lastEventId) return false
  s.lastEventId = ev.id
  const d = ev.data
  if (!isObject(d)) return true
  switch (ev.kind) {
    case 'ticket.created':
    case 'ticket.updated':
    case 'ticket.moved':
      if (isSummary(d.ticket)) upsert(s, d.ticket, ev.id)
      break
    case 'ticket.reordered': {
      const r = d as unknown as TicketReorderedData
      if (typeof r.ticket === 'number' && typeof r.rank === 'number') {
        setRank(s, r.ticket, r.rank, isColumn(r.column) ? r.column : null, ev.id)
      }
      break
    }
    case 'column.renumbered': {
      const r = d as unknown as ColumnRenumberedData
      if (!isObject(r.ranks)) break
      for (const [key, rank] of Object.entries(r.ranks)) {
        const n = Number(key)
        if (Number.isInteger(n) && typeof rank === 'number') setRank(s, n, rank, null, ev.id)
      }
      break
    }
    case 'project.changed': {
      const p = (d as unknown as ProjectChangedData).project
      if (isObject(p) && typeof p.id === 'string') s.projects.set(p.id, p)
      break
    }
    case 'project.removed': {
      const id = (d as unknown as ProjectRemovedData).id
      if (typeof id === 'string') s.projects.delete(id)
      break
    }
    case 'server.paused': {
      const p = (d as unknown as ServerPausedData).paused
      if (typeof p === 'boolean') s.paused = p
      break
    }
  }
  return true
}
