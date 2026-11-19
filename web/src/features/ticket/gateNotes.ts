// What the board remembers about a gate that is no longer on the ticket: the
// last refusal of a move. A refusal is an event and a comment on the ticket,
// the summary keeps nothing, so the panel reads it here; any later gate event
// of the ticket (held, released, overridden, cancelled), any move of the
// ticket and every snapshot ends it. The panel shows it only while the ticket
// is in Planning, where a refused move leaves it. Kept per board store, in
// memory only: after a reload the comment is the record.
import { shallowReactive } from 'vue'
import type { ApiEvent, GateMove, TicketGateData } from '../../api/types'
import { useSession } from '../../session'
import type { BoardStore } from '../../store'

export interface GateRefusal {
  plugin: string
  move: GateMove
  /** The plugin's reason, as the server cleaned it. */
  reason: string
}

export interface GateNotes {
  /** The last refusal of a move of ticket `n` that nothing has replaced, or undefined. */
  refusal(n: number): GateRefusal | undefined
}

const ENDING = ['ticket.held', 'ticket.released', 'ticket.overridden', 'ticket.hold_cancelled', 'ticket.moved'] as const

/** The notes of one store. Exported for tests; the app uses `useGateNotes`. */
export function createGateNotes(store: Pick<BoardStore, 'onEvent' | 'onSnapshot'>): GateNotes {
  const refusals = shallowReactive(new Map<number, GateRefusal>())
  const ticketOf = (ev: ApiEvent): number | null => {
    const t = (ev.data as Partial<TicketGateData> | null)?.ticket
    return typeof t?.number === 'number' ? t.number : null
  }
  store.onEvent('ticket.refused', (ev) => {
    const n = ticketOf(ev)
    if (n === null) return
    const d = ev.data as TicketGateData
    refusals.set(n, { plugin: d.plugin, move: d.move, reason: d.reason })
  })
  for (const kind of ENDING) {
    store.onEvent(kind, (ev) => {
      const n = ticketOf(ev)
      if (n !== null) refusals.delete(n)
    })
  }
  // Events before a snapshot may have been skipped: what a refusal said may no longer hold.
  store.onSnapshot(() => refusals.clear())
  return { refusal: (n) => refusals.get(n) }
}

const notes = new WeakMap<object, GateNotes>()

/** The notes of the session's store, shared by every caller; they start with the first call. */
export function useGateNotes(): GateNotes {
  const session = useSession()
  let n = notes.get(session.store)
  if (!n) {
    n = createGateNotes(session.store)
    notes.set(session.store, n)
  }
  return n
}
