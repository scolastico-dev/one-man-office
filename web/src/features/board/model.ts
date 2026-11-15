import type { Column, Role, TicketSummary } from '../../api/types'

/** Colour family of a card badge; the badge always carries words as well. */
export type BadgeTone = 'danger' | 'warn' | 'accent' | 'muted'

/** The one status badge a card shows: the most urgent thing about the ticket. */
export interface Badge {
  tone: BadgeTone
  text: string
  /** Longer explanation, shown as a tooltip and, for urgent badges, as a line on the card. */
  title?: string
}

/** How a column looks while a card is dragged. */
export type DropState = 'idle' | 'allowed' | 'refused' | 'over'

/** Everything a card renders; built from the store by the board, or by hand for samples. */
export interface CardModel {
  ticket: TicketSummary
  /** Title of the parent story of a task, when known. */
  storyTitle?: string
  /** Display names of the ticket's projects, in the ticket's order. */
  projectNames: string[]
  badge: Badge | null
  /** Short state text next to the badge, for example `agent review` or `merging…`. */
  caption?: string
  /** The card shows a move that the server has not confirmed yet. */
  pending: boolean
  /** The server just refused a move of this card. */
  refusedFlash: boolean
}

export interface ColumnModel {
  column: Column
  title: string
  /** Cards in display order: stories first, then tasks. */
  cards: CardModel[]
  count: number
  dropState: DropState
  /** Why the column refuses the dragged card, in words. */
  reason?: string
  /** Older cards can be loaded (Done). */
  more?: boolean
}

export const COLUMN_TITLES: Record<Column, string> = {
  planning: 'Planning',
  ready: 'Ready For Dev',
  in_progress: 'In Progress',
  review: 'Review',
  done: 'Done',
}

/** Role names as a card shows them. */
export const ROLE_LABELS: Record<Role, string> = {
  user: 'you',
  ceo: 'CEO',
  pm: 'product manager',
  developer: 'developer',
  reviewer: 'reviewer',
  system: 'omo',
}

/**
 * Shortens `text` to at most `max` characters by cutting out the middle, so
 * both the start and the distinctive end stay readable. Paths keep their
 * first segment and as much of the end as fits: `github.com/…/not-three/ui`.
 */
export function shortenMiddle(text: string, max: number): string {
  if (text.length <= max) return text
  if (max < 3) return text.slice(0, max)
  const parts = text.split('/')
  if (parts.length > 2) {
    const head = parts[0]!
    let tail = parts[parts.length - 1]!
    for (let i = parts.length - 2; i > 0; i--) {
      const next = `${parts[i]}/${tail}`
      if (head.length + 2 + next.length > max) break
      tail = next
    }
    const short = `${head}/…/${tail}`
    if (short.length <= max) return short
  }
  const keep = max - 1
  const front = Math.ceil(keep / 2)
  return `${text.slice(0, front)}…${text.slice(text.length - (keep - front))}`
}
