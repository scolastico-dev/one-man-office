// Where a click that opens a ticket goes, with the installation's setting
// "Open tickets on their own page" taken into account. Every click handler
// that opens a ticket pushes this location; `locations.guard.test.ts` refuses
// any other way.
import type { AppLocation, Here, TicketKeys } from '../../locations'
import { ticketHere } from '../../locations'
import type { BoardStore } from '../../store'

/** Whether the installation opens tickets on their own page (off until the settings are read). */
export function opensAsPage(store: BoardStore): boolean {
  return store.state.general?.ui.open_tickets_as_page === true
}

/** Ticket #n, as a click opens it from `here`. */
export function openTicketLocation(store: BoardStore, n: number, here: Here, keys: TicketKeys = {}): AppLocation {
  return ticketHere(n, here, keys, opensAsPage(store))
}
