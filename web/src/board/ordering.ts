import type { Column, TicketSummary } from '../api/types'

/** Distance between ranks of neighbouring cards when the server hands them out. */
const RANK_STEP = 1024

/** Rank order; tickets with the same rank are ordered by number, as the server does. */
function byRank(a: TicketSummary, b: TicketSummary): number {
  return a.rank - b.rank || a.number - b.number
}

/** Done order: most recently closed first, then the higher number first. */
function byClosing(a: TicketSummary, b: TicketSummary): number {
  const ca = a.closed_at ?? ''
  const cb = b.closed_at ?? ''
  if (ca !== cb) return ca > cb ? -1 : 1
  return b.number - a.number
}

/** The cards of the Done column, most recently closed first. */
export function doneOrder(tickets: readonly TicketSummary[]): TicketSummary[] {
  return tickets.filter((t) => t.column === 'done').sort(byClosing)
}

/**
 * The cards of one column in display order: stories first, then tasks, each
 * group in rank order. Done is the exception: closing time decides, and the
 * two groups follow that order.
 */
export function columnOrder(tickets: readonly TicketSummary[], column: Column): { stories: TicketSummary[]; tasks: TicketSummary[] } {
  const inColumn = column === 'done' ? doneOrder(tickets) : tickets.filter((t) => t.column === column).sort(byRank)
  return {
    stories: inColumn.filter((t) => t.type === 'story'),
    tasks: inColumn.filter((t) => t.type === 'task'),
  }
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
 */
export function beforeFor(column: readonly TicketSummary[], dragged: TicketSummary, groupIndex: number): number | null {
  const others = column.filter((t) => t.number !== dragged.number).sort(byRank)
  const group = others.filter((t) => t.type === dragged.type)
  const index = Math.max(0, Math.min(Math.trunc(groupIndex), group.length))
  const next = group[index]
  if (next !== undefined) return next.number
  const last = group[group.length - 1]
  if (last === undefined) {
    // Alone in its group: keep the place it has.
    const after = others.find((t) => byRank(t, dragged) > 0)
    return after === undefined ? null : after.number
  }
  const after = others[others.indexOf(last) + 1]
  return after === undefined ? null : after.number
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
