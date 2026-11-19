import { describe, expect, test } from 'vitest'
import type { ApiEvent, GateMove, TicketGateData } from '../../api/types'
import { ticket } from '../board/testing'
import { createGateNotes } from './gateNotes'

type Fn = (ev: ApiEvent) => void

/** The part of the board store the notes use: handlers per event kind. */
function fakeStore() {
  const handlers = new Map<string, Set<Fn>>()
  const snapshots = new Set<() => void>()
  return {
    snapshot() {
      for (const fn of snapshots) fn()
    },
    onSnapshot(fn: () => void) {
      snapshots.add(fn)
      return () => snapshots.delete(fn)
    },
    onEvent(kind: string, fn: Fn) {
      const set = handlers.get(kind) ?? new Set<Fn>()
      handlers.set(kind, set)
      set.add(fn)
      return () => set.delete(fn)
    },
    emit(kind: string, data: Partial<TicketGateData> & { n?: number; to?: string }) {
      const n = data.n ?? 5
      const d = { ticket: ticket({ number: n }), move: 'start' as GateMove, plugin: 'p', reason: '', choices: [], ...data }
      const ev: ApiEvent = { id: 1, kind, ticket: n, actor: { role: 'system' }, at: '', data: d }
      for (const fn of handlers.get(kind) ?? []) fn(ev)
    },
  }
}

describe('gate notes', () => {
  test('a refusal is kept with its plugin, move and reason', () => {
    const s = fakeStore()
    const notes = createGateNotes(s)
    s.emit('ticket.refused', { plugin: 'lint', reason: 'the branch is dirty', move: 'release' })
    expect(notes.refusal(5)).toEqual({ plugin: 'lint', move: 'release', reason: 'the branch is dirty' })
    expect(notes.refusal(6)).toBeUndefined()
  })

  test.each(['ticket.held', 'ticket.released', 'ticket.overridden', 'ticket.hold_cancelled'])('%s ends the memory of an earlier refusal', (kind) => {
    const s = fakeStore()
    const notes = createGateNotes(s)
    s.emit('ticket.refused', { reason: 'no' })
    s.emit(kind, {})
    expect(notes.refusal(5)).toBeUndefined()
  })

  test('a newer refusal replaces an older one; other tickets are not touched', () => {
    const s = fakeStore()
    const notes = createGateNotes(s)
    s.emit('ticket.refused', { reason: 'first' })
    s.emit('ticket.refused', { n: 8, reason: 'other' })
    s.emit('ticket.refused', { reason: 'second' })
    expect(notes.refusal(5)?.reason).toBe('second')
    expect(notes.refusal(8)?.reason).toBe('other')
  })

  test('a move of the ticket ends the memory of a refusal, though no gate event comes', () => {
    const s = fakeStore()
    const notes = createGateNotes(s)
    s.emit('ticket.refused', { reason: 'no' })
    s.emit('ticket.moved', { to: 'ready' })
    expect(notes.refusal(5)).toBeUndefined()
  })

  test('the move that carries a refused start back to Planning does not end the note, a later move out of Planning does', () => {
    const s = fakeStore()
    const notes = createGateNotes(s)
    s.emit('ticket.refused', { move: 'start', plugin: 'lint', reason: 'dirty' })
    s.emit('ticket.moved', { to: 'planning' })
    expect(notes.refusal(5)).toEqual({ plugin: 'lint', move: 'start', reason: 'dirty' })
    s.emit('ticket.moved', { to: 'ready' })
    expect(notes.refusal(5)).toBeUndefined()
  })

  test('a snapshot ends every memory of a refusal', () => {
    const s = fakeStore()
    const notes = createGateNotes(s)
    s.emit('ticket.refused', { reason: 'no' })
    s.emit('ticket.refused', { n: 8, reason: 'no' })
    s.snapshot()
    expect(notes.refusal(5)).toBeUndefined()
    expect(notes.refusal(8)).toBeUndefined()
  })

  test('malformed event data is ignored', () => {
    const s = fakeStore()
    const notes = createGateNotes(s)
    expect(() => s.emit('ticket.refused', { ticket: undefined as never })).not.toThrow()
    expect(notes.refusal(5)).toBeUndefined()
  })
})
