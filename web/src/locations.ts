// The app's addresses of the board, of a ticket and of the settings, built in
// one place. All keep the board's filters (and any other key the address
// carries), so a visit to a ticket or to the settings and back finds the
// board as it was; a ticket's own keys, `tab` and `comment`, belong to one
// ticket and never carry over to the board, the settings or another ticket.
// Every link and navigation to the board, a ticket or the settings goes
// through these functions.
import type { LocationQuery, LocationQueryRaw, RouteRecordNameGeneric } from 'vue-router'

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

/** The name of the route of a ticket's own page. */
export const TICKET_PAGE_ROUTE = 'ticket-page'

/**
 * Ticket #n on a page of its own (in place of the board's columns), with the
 * filters of `from` and only the ticket keys given. Its address is the one a
 * ticket link carries, so a link opened in a new tab shows the page.
 */
export function ticketPageLocation(n: number, from: From, keys: TicketKeys = {}): AppLocation {
  return { ...ticketLocation(n, from, keys), path: `/ticket/${n}` }
}

/** Where the address comes from, when it matters which view of a ticket shows: its route's name. */
export interface Here extends From {
  readonly name?: RouteRecordNameGeneric | null
}

/** Whether `here` is a ticket's own page. */
export function onTicketPage(here: Here): boolean {
  return here.name === TICKET_PAGE_ROUTE
}

/** Whether `here` is a ticket's panel over the board. */
export function onTicketPanel(here: Here): boolean {
  return here.name === 'ticket'
}

/**
 * Where a click that opens ticket #n goes: the one rule for every such click.
 * The user stays in the view they are in: from a ticket's page another
 * ticket's page, from a ticket's panel another ticket's panel. From anywhere
 * else (the board, the settings, a terminal's window) it is the panel, or
 * with `asPage` (the setting "Open tickets on their own page") the page.
 */
export function ticketHere(n: number, here: Here, keys: TicketKeys = {}, asPage = false): AppLocation {
  const page = onTicketPage(here) || (asPage && !onTicketPanel(here))
  return page ? ticketPageLocation(n, here, keys) : ticketLocation(n, here, keys)
}

/** The board, with the filters of `from`. */
export function boardLocation(from: From): AppLocation {
  return { path: '/', query: kept(from) }
}

/** The sections of the settings, in the order the section list shows them. */
export const SETTINGS_SECTIONS_IN_ORDER = ['projects', 'agents', 'roles', 'usage', 'prompts', 'skills', 'environment', 'limits', 'review', 'plugins', 'general'] as const

/** One section of the settings: the last part of `/settings/<section>`. */
export type SettingsSection = (typeof SETTINGS_SECTIONS_IN_ORDER)[number]

/** Whether `s` names a section of the settings. */
export function isSettingsSection(s: unknown): s is SettingsSection {
  return typeof s === 'string' && (SETTINGS_SECTIONS_IN_ORDER as readonly string[]).includes(s)
}

/**
 * A section of the settings, or with `null` the list of sections (a page of
 * its own on narrow screens), with the filters of `from`.
 */
export function settingsLocation(section: SettingsSection | null, from: From): AppLocation {
  return { path: section === null ? '/settings' : `/settings/${section}`, query: kept(from) }
}

/** What a terminal's own window shows: an agent's terminal or one of the user's shells. */
export type TerminalWindowKind = 'agent' | 'user'

/**
 * The page of one terminal in a window of its own: `/terminal/agent/<terminal id>`
 * or `/terminal/user/<shell id>`. It carries no board filters: the window shows
 * nothing of the board.
 */
export function terminalWindowLocation(kind: TerminalWindowKind, id: string): AppLocation {
  return { path: `/terminal/${kind}/${encodeURIComponent(id)}`, query: {} }
}
