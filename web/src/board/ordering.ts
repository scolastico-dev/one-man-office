import type { TicketSummary } from '../api/types'
import { compareTime } from '../api/time'

/** Distance between ranks of neighbouring cards when the server hands them out. */
const RANK_STEP = 1024

/** Rank order; tickets with the same rank are ordered by number, as the server does. */
function byRank(a: TicketSummary, b: TicketSummary): number {
  return a.rank - b.rank || a.number - b.number
}

/**
 * Done order: most recently closed first, then the higher number first.
 * Closing times are compared by value, not as text; a ticket without one
 * comes last.
 */
function byClosing(a: TicketSummary, b: TicketSummary): number {
  if (a.closed_at !== b.closed_at) {
    if (a.closed_at === null) return 1
    if (b.closed_at === null) return -1
    const c = compareTime(b.closed_at, a.closed_at)
    if (c !== 0) return c
  }
  return b.number - a.number
}

/** The cards of the Done column, most recently closed first. */
export function doneOrder(tickets: readonly TicketSummary[]): TicketSummary[] {
  return tickets.filter((t) => t.column === 'done').sort(byClosing)
}

/**
 * The `before` value to send when `dragged` is dropped at position
 * `groupIndex` among the other cards of its own group (stories among
 * stories, tasks among tasks; the dragged card itself not counted, 0 is
 * the top). It is the group's card at that position; at the end of the
 * group it is the card that follows the group's last card in the column's
 * full rank order, or `null` at the very end, so the card lands right after
 * its new predecessor and its place relative to the other group changes as
 * little as possible. `column` holds every card of the column in any order.
 *
 * `skip` names cards that must not be the answer: a card whose own move has
 * no server answer yet may not exist where it is shown, and the server
 * refuses a `before` it cannot place. The next card in line is named instead.
 */
export function beforeFor(column: readonly TicketSummary[], dragged: TicketSummary, groupIndex: number, skip?: (n: number) => boolean): number | null {
  const others = column.filter((t) => t.number !== dragged.number).sort(byRank)
  const usable = (t: TicketSummary | undefined): t is TicketSummary => t !== undefined && !(skip?.(t.number) ?? false)
  const group = others.filter((t) => t.type === dragged.type)
  const index = Math.max(0, Math.min(Math.trunc(groupIndex), group.length))
  const next = group.slice(index).find(usable)
  if (next !== undefined) return next.number
  const last = group[group.length - 1]
  if (last === undefined) {
    // Alone in its group: keep the place it has.
    return others.find((t) => byRank(t, dragged) > 0 && usable(t))?.number ?? null
  }
  return others.slice(others.indexOf(last) + 1).find(usable)?.number ?? null
}

/**
 * The rank a card shows while its move is not confirmed yet. `column` holds
 * the cards the card is moved among; a card listed there under the number
 * `dragged` is left out.
 */
export function displayRank(column: readonly TicketSummary[], before: number | null, dragged?: number): number {
  const cards = column.filter((t) => t.number !== dragged).sort(byRank)
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
