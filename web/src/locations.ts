// The app's addresses of the board and of a ticket, built in one place. Both
// keep the board's filters (and any other key the address carries); a
// ticket's own keys, `tab` and `comment`, belong to one ticket and never carry
// over to the board or to another ticket. Every link and navigation to the
// board or a ticket goes through these two functions.
import type { LocationQuery, LocationQueryRaw } from 'vue-router'

/** The keys that belong to the open ticket: the tab shown and the comment to reveal. */
export const TICKET_KEYS = ['tab', 'comment'] as const

/** What a ticket's address may say besides its number. */
export interface TicketKeys {
  /** The panel tab to show; none shows Comments. */
  tab?: string
  /** A comment to scroll to and outline. */
  comment?: number
}

export interface AppLocation {
  path: string
  query: LocationQueryRaw
}

/** Where the address comes from: the current route, or anything with its query. */
export interface From {
  readonly query: LocationQuery
}

function kept(from: From): LocationQueryRaw {
  const query: LocationQueryRaw = {}
  for (const [k, v] of Object.entries(from.query)) if (!(TICKET_KEYS as readonly string[]).includes(k)) query[k] = v
  return query
}

/** Ticket #n, with the filters of `from` and only the ticket keys given. */
export function ticketLocation(n: number, from: From, keys: TicketKeys = {}): AppLocation {
  const query = kept(from)
  if (keys.tab !== undefined) query.tab = keys.tab
  if (keys.comment !== undefined) query.comment = String(keys.comment)
  return { path: `/${n}`, query }
}

/** The board, with the filters of `from`. */
export function boardLocation(from: From): AppLocation {
  return { path: '/', query: kept(from) }
}
