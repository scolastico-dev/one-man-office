import { readdirSync, readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, test } from 'vitest'
import type { ApiEvent, Column, Project, ReviewStage, TicketList, TicketSummary, TicketType } from '../api/types'
import { applyEvent, compareDone, emptyState, installSnapshot, mergeTickets, type BoardState } from './boardState'

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

function project(id: string, over: Partial<Project> = {}): Project {
  return { id, name: id, path: `/src/${id}`, enabled: true, missing: false, root: '', merge: { story: '', task: '', story_task: '' }, effective_merge: { story: 'merge', task: 'merge', story_task: 'merge' }, story_merge: 'merge', ...over }
}

const stamp = (i: number) => `2026-11-15T10:${String(Math.floor(i / 60)).padStart(2, '0')}:${String(i % 60).padStart(2, '0')}.000000000Z`

/**
 * A small server: every operation changes its tickets the way the board does
 * and records the events it would stream, plus the snapshot `GET /api/tickets`
 * would return right after each event.
 */
class FakeServer {
  tickets = new Map<number, TicketSummary>()
  projects = new Map<string, Project>()
  paused = false
  events: ApiEvent[] = []
  snapshots: Array<{ list: TicketList; projects: Project[]; paused: boolean }> = []
  private clock = 0
  doneLimit = 50

  private emit(kind: string, ticket: number | null, data: unknown): void {
    const id = this.events.length + 1
    this.clock++
    this.events.push({ id, kind, ticket, actor: { role: 'user' }, at: stamp(this.clock), data: structuredClone(data) })
    this.snapshots[id] = { list: this.list(), projects: [...this.projects.values()].map((p) => ({ ...p })), paused: this.paused }
  }

  list(eventId = this.events.length): TicketList {
    const all = [...this.tickets.values()].map((t) => structuredClone(t))
    const open = all.filter((t) => t.column !== 'done')
    const done = all.filter((t) => t.column === 'done').sort(compareDone)
    const page = done.slice(0, this.doneLimit)
    const out: TicketList = { tickets: [...open, ...page], event_id: eventId }
    if (done.length > this.doneLimit) out.next_done_before = page[page.length - 1]!.number
    return out
  }

  donePage(before: number, eventId = this.events.length): TicketList {
    const done = [...this.tickets.values()].filter((t) => t.column === 'done').sort(compareDone)
    const at = done.findIndex((t) => t.number === before)
    const page = done.slice(at + 1, at + 1 + this.doneLimit).map((t) => structuredClone(t))
    const out: TicketList = { tickets: page, event_id: eventId }
    if (at + 1 + this.doneLimit < done.length) out.next_done_before = page[page.length - 1]!.number
    return out
  }

  private set(n: number, patch: Partial<TicketSummary>): TicketSummary {
    const t = { ...this.tickets.get(n)!, ...patch, updated_at: stamp(this.clock + 1) }
    this.tickets.set(n, t)
    return t
  }

  create(n: number, type: TicketType = 'task', story: number | null = null): void {
    const t = summary(n, { type, story, created_at: stamp(this.clock + 1), updated_at: stamp(this.clock + 1) })
    this.tickets.set(n, t)
    this.emit('ticket.created', n, { ticket: t })
    if (story !== null) this.derived(story)
  }

  update(n: number, patch: Partial<TicketSummary>): void {
    this.emit('ticket.updated', n, { ticket: this.set(n, patch), fields: Object.keys(patch) })
  }

  derived(n: number): void {
    const tasks = [...this.tickets.values()].filter((t) => t.story === n)
    const progress = { done: tasks.filter((t) => t.column === 'done').length, total: tasks.length }
    this.emit('ticket.updated', n, { ticket: this.set(n, { progress }), fields: ['derived'] })
  }

  move(n: number, to: Column, action: string, stage: ReviewStage = '', resolution: TicketSummary['resolution'] = ''): void {
    const from = this.tickets.get(n)!.column
    const patch: Partial<TicketSummary> = { column: to, review_stage: stage }
    if (to !== from) patch.rank = this.endRank(to)
    if (to === 'done') Object.assign(patch, { closed_at: stamp(this.clock + 1), resolution: resolution || 'completed' })
    const t = this.set(n, patch)
    this.emit('ticket.moved', n, { ticket: t, from, to, action })
    if (t.story !== null) this.derived(t.story)
  }

  reorder(n: number, rank: number): void {
    const t = this.set(n, { rank })
    this.emit('ticket.reordered', n, { ticket: n, column: t.column, rank })
  }

  renumber(column: Column): void {
    const cards = [...this.tickets.values()].filter((t) => t.column === column).sort((a, b) => a.rank - b.rank || a.number - b.number)
    const ranks: Record<string, number> = {}
    cards.forEach((t, i) => {
      ranks[String(t.number)] = (i + 1) * 1024
      this.tickets.set(t.number, { ...t, rank: (i + 1) * 1024 })
    })
    this.emit('column.renumbered', null, { column, ranks })
  }

  /** An event the board does not apply. */
  other(kind: string, data: unknown): void {
    this.emit(kind, null, data)
  }

  comment(n: number): void {
    this.emit('comment.created', n, { comment: { id: this.events.length + 1, ticket: n, body: 'hi' } })
  }

  setProject(p: Project): void {
    this.projects.set(p.id, p)
    this.emit('project.changed', null, { project: p })
  }

  removeProject(id: string): void {
    this.projects.delete(id)
    this.emit('project.removed', null, { id })
  }

  pause(paused: boolean): void {
    this.paused = paused
    this.emit('server.paused', null, { paused })
  }

  private endRank(column: Column): number {
    const ranks = [...this.tickets.values()].filter((t) => t.column === column).map((t) => t.rank)
    return ranks.length === 0 ? 1024 : Math.max(...ranks) + 1024
  }
}

/** A recorded sequence covering every kind the board applies, and some it does not. */
function recorded(): FakeServer {
  const s = new FakeServer()
  s.setProject(project('api'))
  s.create(1, 'story')
  s.create(2, 'task', 1)
  s.create(3, 'task', 1)
  s.create(4)
  s.comment(2)
  s.update(4, { title: 'Renamed' })
  s.move(1, 'ready', 'approve')
  s.move(4, 'ready', 'approve')
  s.reorder(4, 512)
  s.move(1, 'in_progress', 'begin')
  s.move(2, 'ready', 'release')
  s.move(2, 'in_progress', 'begin')
  s.move(2, 'review', 'done', 'agent')
  s.move(2, 'review', 'verdict', 'human') // stays in Review
  s.pause(true)
  s.move(2, 'review', 'approve', 'merging') // stays in Review
  s.move(2, 'done', 'merged')
  s.renumber('ready')
  s.setProject(project('api', { name: 'API' }))
  s.setProject(project('web'))
  s.removeProject('web')
  s.move(4, 'planning', 'withdraw')
  s.move(3, 'done', 'cancel', '', 'cancelled')
  s.other('scheduler.changed', { state: {} })
  s.other('future.kind', { x: 1 })
  s.pause(false)
  s.create(5)
  return s
}

function stateAt(server: FakeServer, k: number): BoardState {
  const s = emptyState()
  if (k === 0) {
    installSnapshot(s, { tickets: [], event_id: 0 })
    return s
  }
  const snap = server.snapshots[k]!
  installSnapshot(s, structuredClone(snap.list))
  for (const p of snap.projects) s.projects.set(p.id, structuredClone(p))
  s.paused = snap.paused
  return s
}

/** What the board shows: every ticket once, the resume point, paging, projects and pause. */
function normalize(s: BoardState) {
  return {
    tickets: [...s.tickets.values()].map((e) => e.t).sort((a, b) => a.number - b.number),
    lastEventId: s.lastEventId,
    doneBefore: s.doneBefore,
    projects: [...s.projects.values()].sort((a, b) => a.id.localeCompare(b.id)),
    paused: s.paused,
    loaded: s.loaded,
  }
}

function applyAll(s: BoardState, events: ApiEvent[]): void {
  for (const ev of events) applyEvent(s, structuredClone(ev))
}

const ev = (id: number, kind: string, data: unknown, ticket: number | null = null): ApiEvent => ({
  id,
  kind,
  ticket,
  actor: { role: 'user' },
  at: stamp(id),
  data,
})

describe('applying events', () => {
  test('snapshot then events apply in order', () => {
    const server = recorded()
    const s = stateAt(server, 0)
    applyAll(s, server.events)
    expect(normalize(s)).toEqual(normalize(stateAt(server, server.events.length)))
    expect(s.tickets.get(2)!.t.column).toBe('done')
    expect(s.tickets.get(4)!.t.title).toBe('Renamed')
    expect(s.tickets.get(1)!.t.progress).toEqual({ done: 2, total: 2 })
  })

  test('replaying the same events twice gives the same state', () => {
    const server = recorded()
    const once = stateAt(server, 0)
    applyAll(once, server.events)
    const twice = stateAt(server, 0)
    applyAll(twice, server.events)
    applyAll(twice, server.events)
    expect(normalize(twice)).toEqual(normalize(once))
  })

  test('every snapshot followed by any replayed suffix ends equal to a fresh snapshot', () => {
    const server = recorded()
    const n = server.events.length
    const want = normalize(stateAt(server, n))
    for (let k = 0; k <= n; k++) {
      // A reconnect replays from any point at or before the snapshot.
      for (let j = 0; j <= k; j++) {
        const s = stateAt(server, k)
        // Events the snapshot already reflects change nothing, not even for a moment.
        applyAll(s, server.events.slice(j, k))
        expect(normalize(s), `snapshot ${k}, old events from ${j + 1}`).toEqual(normalize(stateAt(server, k)))
        applyAll(s, server.events.slice(k))
        expect(normalize(s), `snapshot ${k}, replay from ${j + 1}`).toEqual(want)
      }
      // A reconnect that replays events twice after the snapshot.
      const s = stateAt(server, k)
      applyAll(s, server.events.slice(k))
      applyAll(s, server.events)
      expect(normalize(s), `snapshot ${k}, replayed twice`).toEqual(want)
    }
  })

  test('ticket.moved with from === to updates the stage and keeps the column', () => {
    const s = emptyState()
    installSnapshot(s, { tickets: [summary(7, { column: 'review', review_stage: 'agent', rank: 3000 })], event_id: 10 })
    const t = summary(7, { column: 'review', review_stage: 'human', rank: 3000 })
    expect(applyEvent(s, ev(11, 'ticket.moved', { ticket: t, from: 'review', to: 'review', action: 'verdict' }, 7))).toBe(true)
    expect(s.tickets.get(7)!.t.column).toBe('review')
    expect(s.tickets.get(7)!.t.review_stage).toBe('human')
    expect(s.tickets.get(7)!.t.rank).toBe(3000)
  })

  test('ticket.reordered and column.renumbered set ranks', () => {
    const s = emptyState()
    installSnapshot(s, { tickets: [summary(1, { rank: 100 }), summary(2, { rank: 200 }), summary(3, { rank: 300 })], event_id: 5 })
    applyEvent(s, ev(6, 'ticket.reordered', { ticket: 3, column: 'planning', rank: 50 }, 3))
    expect(s.tickets.get(3)!.t.rank).toBe(50)
    expect(s.tickets.get(3)!.at).toBe(6)
    applyEvent(s, ev(7, 'column.renumbered', { column: 'planning', ranks: { '3': 1024, '1': 2048, '2': 3072 } }))
    expect([1, 2, 3].map((n) => s.tickets.get(n)!.t.rank)).toEqual([2048, 3072, 1024])
    // A reorder of a ticket the store does not have is ignored.
    applyEvent(s, ev(8, 'ticket.reordered', { ticket: 99, column: 'planning', rank: 1 }, 99))
    expect(s.tickets.has(99)).toBe(false)
    expect(s.lastEventId).toBe(8)
  })

  test("events older than a ticket's entry are ignored", () => {
    const s = emptyState()
    installSnapshot(s, { tickets: [summary(1)], event_id: 30 })
    mergeTickets(s, { tickets: [summary(9, { column: 'done', title: 'From search', closed_at: stamp(1) })], event_id: 50 })
    // The stream is behind the search answer: event 40 is older than ticket 9's entry.
    applyEvent(s, ev(40, 'ticket.updated', { ticket: summary(9, { column: 'done', title: 'Old', closed_at: stamp(1) }), fields: ['title'] }, 9))
    expect(s.tickets.get(9)!.t.title).toBe('From search')
    expect(s.lastEventId).toBe(40)
    applyEvent(s, ev(51, 'ticket.updated', { ticket: summary(9, { column: 'done', title: 'New', closed_at: stamp(1) }), fields: ['title'] }, 9))
    expect(s.tickets.get(9)!.t.title).toBe('New')
    // A merge older than the entry leaves it alone.
    mergeTickets(s, { tickets: [summary(9, { column: 'done', title: 'Stale', closed_at: stamp(1) })], event_id: 45 })
    expect(s.tickets.get(9)!.t.title).toBe('New')
  })

  test('a ticket closed in another tab appears in Done', () => {
    const server = new FakeServer()
    server.create(1)
    const s = stateAt(server, 1)
    server.move(1, 'ready', 'approve')
    server.move(1, 'done', 'cancel', '', 'cancelled')
    applyAll(s, server.events)
    expect(s.tickets.get(1)!.t.column).toBe('done')
    expect(s.tickets.get(1)!.t.resolution).toBe('cancelled')
    expect(normalize(s)).toEqual(normalize(stateAt(server, server.events.length)))
  })

  test('project.changed, project.removed and server.paused update state', () => {
    const s = emptyState()
    applyEvent(s, ev(1, 'project.changed', { project: project('a') }))
    applyEvent(s, ev(2, 'project.changed', { project: project('a', { name: 'Alpha' }) }))
    applyEvent(s, ev(3, 'project.changed', { project: project('b') }))
    applyEvent(s, ev(4, 'project.removed', { id: 'b' }))
    applyEvent(s, ev(5, 'server.paused', { paused: true }))
    expect([...s.projects.values()]).toEqual([project('a', { name: 'Alpha' })])
    expect(s.paused).toBe(true)
  })

  test('unknown kinds are ignored and do not move lastEventId backwards', () => {
    const s = emptyState()
    installSnapshot(s, { tickets: [summary(1)], event_id: 20 })
    const before = normalize(s)
    expect(applyEvent(s, ev(21, 'something.new', { ticket: summary(1, { title: 'x' }) }, 1))).toBe(true)
    expect(applyEvent(s, ev(22, 'comment.created', { comment: {} }, 1))).toBe(true)
    expect(applyEvent(s, ev(23, 'scheduler.changed', null))).toBe(true)
    expect(normalize(s)).toEqual({ ...before, lastEventId: 23 })
    expect(applyEvent(s, ev(19, 'ticket.updated', { ticket: summary(1, { title: 'old' }), fields: [] }, 1))).toBe(false)
    expect(s.lastEventId).toBe(23)
    expect(s.tickets.get(1)!.t.title).toBe('Ticket 1')
  })

  test('malformed data is ignored without throwing', () => {
    const s = emptyState()
    installSnapshot(s, { tickets: [summary(1)], event_id: 1 })
    expect(() => {
      applyEvent(s, ev(2, 'ticket.updated', null))
      applyEvent(s, ev(3, 'ticket.moved', { ticket: { number: 'x' } }))
      applyEvent(s, ev(4, 'column.renumbered', { ranks: 5 }))
      applyEvent(s, ev(5, 'project.changed', { project: 3 }))
    }).not.toThrow()
    expect(s.tickets.get(1)!.t).toEqual(summary(1))
    expect(s.lastEventId).toBe(5)
  })
})

describe('Done paging', () => {
  function closedServer(count: number): FakeServer {
    const server = new FakeServer()
    server.doneLimit = 3
    for (let n = 1; n <= count; n++) {
      server.create(n)
      server.move(n, 'done', 'cancel', '', 'cancelled')
    }
    return server
  }

  test('a snapshot keeps the first Done page and the cursor for the next', () => {
    const server = closedServer(5)
    const s = stateAt(server, server.events.length)
    expect([...s.tickets.keys()].sort()).toEqual([3, 4, 5])
    expect(s.doneBefore).toBe(3)
  })

  test('an event about a Done ticket beyond the loaded pages is left for its page', () => {
    const server = closedServer(5)
    const s = stateAt(server, server.events.length)
    server.update(1, { export_merged: true })
    applyAll(s, server.events)
    expect(s.tickets.has(1)).toBe(false)
    expect(s.skipped.get(1)).toBe(server.events.length)
    expect(normalize(s)).toEqual(normalize(stateAt(server, server.events.length)))
    // Its page brings it, with the change.
    mergeTickets(s, server.donePage(3))
    expect(s.tickets.get(1)!.t.export_merged).toBe(true)
  })

  test('fetched data older than a left-out event is not merged and is reported', () => {
    const server = closedServer(5)
    const s = stateAt(server, server.events.length)
    const page = server.donePage(3) // read before the change below
    server.update(1, { title: 'Changed' })
    applyAll(s, server.events)
    expect(mergeTickets(s, page)).toEqual([1])
    expect(s.tickets.has(1)).toBe(false)
    expect(s.tickets.has(2)).toBe(true)
    // A newer read is merged and clears the record.
    expect(mergeTickets(s, server.donePage(3))).toEqual([])
    expect(s.tickets.get(1)!.t.title).toBe('Changed')
    expect(s.skipped.has(1)).toBe(false)
  })

  test('a newly closed ticket is not beyond the page and is shown', () => {
    const server = closedServer(5)
    const s = stateAt(server, server.events.length)
    server.create(6)
    server.move(6, 'done', 'cancel', '', 'cancelled')
    applyAll(s, server.events)
    expect(s.tickets.get(6)!.t.column).toBe('done')
  })

  test('a resync snapshot drops older Done pages and resets the cursor', () => {
    const server = closedServer(5)
    const s = stateAt(server, server.events.length)
    mergeTickets(s, server.donePage(3))
    s.doneBefore = null
    expect(s.tickets.size).toBe(5)
    installSnapshot(s, server.list())
    expect(s.tickets.size).toBe(3)
    expect(s.doneBefore).toBe(3)
  })

  test('a snapshot from a server whose log started over lowers lastEventId', () => {
    const s = emptyState()
    installSnapshot(s, { tickets: [summary(1)], event_id: 900 })
    installSnapshot(s, { tickets: [summary(2)], event_id: 4 })
    expect(s.lastEventId).toBe(4)
    expect([...s.tickets.keys()]).toEqual([2])
    expect(applyEvent(s, ev(5, 'ticket.created', { ticket: summary(3) }, 3))).toBe(true)
    expect(s.tickets.has(3)).toBe(true)
  })
})

describe('Done order with the server\'s timestamp format', () => {
  // Go writes RFC 3339 with nanoseconds and drops trailing zeros, so the text
  // has no fixed width.
  const done = (n: number, closed: string) => summary(n, { column: 'done', closed_at: closed })

  test('tickets closed within the same second sort by value, then by number', () => {
    const a = done(1, '2026-11-15T10:00:05Z')
    const b = done(2, '2026-11-15T10:00:05.5Z')
    const c = done(3, '2026-11-15T10:00:05.1Z')
    const d = done(4, '2026-11-15T10:00:05.12Z')
    const e = done(5, '2026-11-15T10:00:05.12Z')
    const f = done(6, '2026-11-15T10:00:04.999999999Z')
    expect([a, b, c, d, e, f].sort(compareDone).map((t) => t.number)).toEqual([2, 5, 4, 3, 1, 6])
  })

  test('a ticket in the same second as the page boundary but closed later is not beyond the loaded pages', () => {
    const s = emptyState()
    // The first page ends with #10, closed at …:05.5; the next page starts after it.
    installSnapshot(s, { tickets: [done(11, '2026-11-15T10:00:06Z'), done(10, '2026-11-15T10:00:05.5Z')], event_id: 20, next_done_before: 10 })
    // #3 was closed at …:05.75, after #10: it belongs on the loaded page.
    applyEvent(s, ev(21, 'ticket.updated', { ticket: done(3, '2026-11-15T10:00:05.75Z'), fields: ['title'] }, 3))
    expect(s.tickets.has(3)).toBe(true)
    // #4 was closed at …:05 sharp, before #10: it belongs on the next page.
    applyEvent(s, ev(22, 'ticket.updated', { ticket: done(4, '2026-11-15T10:00:05Z'), fields: ['title'] }, 4))
    expect(s.tickets.has(4)).toBe(false)
    expect(s.skipped.get(4)).toBe(22)
    // #12 was closed at …:05.12, also before #10.
    applyEvent(s, ev(23, 'ticket.updated', { ticket: done(12, '2026-11-15T10:00:05.12Z'), fields: ['title'] }, 12))
    expect(s.tickets.has(12)).toBe(false)
  })

  test('the store compares ticket timestamps only through timeKey', () => {
    const dir = resolve(import.meta.dirname)
    const offenders: string[] = []
    for (const f of readdirSync(dir)) {
      if (!f.endsWith('.ts') || f.endsWith('.test.ts')) continue
      readFileSync(resolve(dir, f), 'utf8')
        .split('\n')
        .forEach((line, i) => {
          if (/\b(closed_at|created_at|updated_at)\b/.test(line) && !/timeKey\(/.test(line) && !/^\s*(\/\/|\*)/.test(line)) {
            offenders.push(`${f}:${i + 1}: ${line.trim()}`)
          }
        })
    }
    expect(offenders).toEqual([])
  })
})
