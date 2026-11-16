import { expect, test } from 'vitest'
import type { BoardStore } from '../../store'
import { opensAsPage, openTicketLocation } from './openTicket'

const store = (general: { ui: { open_tickets_as_page: boolean } } | null) => ({ state: { general } }) as unknown as BoardStore

test('the setting is read from the board store, off until the settings are known', () => {
  expect(opensAsPage(store(null))).toBe(false)
  expect(opensAsPage(store({ ui: { open_tickets_as_page: false } }))).toBe(false)
  expect(opensAsPage(store({ ui: { open_tickets_as_page: true } }))).toBe(true)
})

test('a click on a ticket follows the setting of the store, and a change applies at once', () => {
  const s = { state: { general: { ui: { open_tickets_as_page: false } } } }
  const board = { name: 'board', query: {} }
  expect(openTicketLocation(s as unknown as BoardStore, 4, board).path).toBe('/4')
  s.state.general.ui.open_tickets_as_page = true
  expect(openTicketLocation(s as unknown as BoardStore, 4, board).path).toBe('/ticket/4')
})
