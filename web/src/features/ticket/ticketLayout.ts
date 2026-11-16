// Which of the ticket's views a tab is shown in: the panel over the board, or
// the ticket's own page with room to spare. Provided by the page; a tab that
// lays itself out differently there (the Diff tab's file browser) reads it.
import { inject, provide, type InjectionKey } from 'vue'

export type TicketLayout = 'panel' | 'page'

const TICKET_LAYOUT: InjectionKey<TicketLayout> = Symbol('ticket-layout')

/** Says that the tabs below are shown on the ticket's page. */
export function provideTicketLayout(layout: TicketLayout): void {
  provide(TICKET_LAYOUT, layout)
}

/** The view the calling tab is shown in; the panel when nothing says otherwise. */
export function useTicketLayout(): TicketLayout {
  return inject(TICKET_LAYOUT, 'panel')
}
