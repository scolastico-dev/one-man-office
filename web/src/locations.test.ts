import { expect, test } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { boardLocation, isSettingsSection, onTicketPage, SETTINGS_SECTIONS_IN_ORDER, settingsLocation, terminalWindowLocation, TICKET_PAGE_ROUTE, ticketHere, ticketLocation, ticketPageLocation } from './locations'
import { createAppRouter } from './router'

const at = (query: Record<string, string | string[]>) => ({ query })

test('a ticket keeps the board filters and drops the keys of the ticket it came from', () => {
  const from = at({ project: 'demo', story: '4', supervisor: 'user', needs_me: '1', q: 'x', tab: 'diff', comment: '55' })
  expect(ticketLocation(7, from)).toEqual({ path: '/7', query: { project: 'demo', story: '4', supervisor: 'user', needs_me: '1', q: 'x' } })
})

test('a ticket gets the tab and comment it is given, after the filters', () => {
  const from = at({ q: 'x', tab: 'diff', comment: '55' })
  expect(ticketLocation(7, from, { tab: 'comments', comment: 9 })).toEqual({ path: '/7', query: { q: 'x', tab: 'comments', comment: '9' } })
  expect(ticketLocation(7, from, { tab: 'agents' })).toEqual({ path: '/7', query: { q: 'x', tab: 'agents' } })
})

test('the board keeps the filters and drops the ticket keys', () => {
  expect(boardLocation(at({ q: 'x', story: '4', tab: 'diff', comment: '55' }))).toEqual({ path: '/', query: { q: 'x', story: '4' } })
  expect(boardLocation(at({}))).toEqual({ path: '/', query: {} })
})

test('resolved by the router, the addresses read as expected', () => {
  const r = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { render: () => null } }] })
  const from = at({ needs_me: '1', tab: 'diff', comment: '55' })
  expect(r.resolve(ticketLocation(3, from, { tab: 'comments', comment: 12 })).fullPath).toBe('/3?needs_me=1&tab=comments&comment=12')
  expect(r.resolve(ticketLocation(3, from)).fullPath).toBe('/3?needs_me=1')
  expect(r.resolve(boardLocation(from)).fullPath).toBe('/?needs_me=1')
})

test('settingsLocation keeps the board filters and drops the ticket keys', () => {
  const from = at({ project: 'demo', story: '4', needs_me: '1', q: 'x', tab: 'diff', comment: '55' })
  expect(settingsLocation('limits', from)).toEqual({ path: '/settings/limits', query: { project: 'demo', story: '4', needs_me: '1', q: 'x' } })
  expect(settingsLocation(null, from)).toEqual({ path: '/settings', query: { project: 'demo', story: '4', needs_me: '1', q: 'x' } })
  // From one section to another and back to the board, the filters stay.
  const r = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { render: () => null } }] })
  const settings = r.resolve(settingsLocation('roles', from))
  expect(settings.fullPath).toBe('/settings/roles?project=demo&story=4&needs_me=1&q=x')
  expect(r.resolve(settingsLocation('prompts', settings)).fullPath).toBe('/settings/prompts?project=demo&story=4&needs_me=1&q=x')
  expect(r.resolve(boardLocation(settings)).fullPath).toBe('/?project=demo&story=4&needs_me=1&q=x')
})

test('settingsLocation builds /settings/usage, /settings/skills, /settings/plugins', () => {
  const r = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { render: () => null } }] })
  for (const id of ['usage', 'skills', 'plugins'] as const) {
    expect(isSettingsSection(id)).toBe(true)
    expect(r.resolve(settingsLocation(id, { query: {} })).fullPath).toBe(`/settings/${id}`)
  }
})

test('the settings sections are known by name', () => {
  expect(SETTINGS_SECTIONS_IN_ORDER).toEqual(['projects', 'agents', 'roles', 'usage', 'prompts', 'skills', 'mcp', 'lsp', 'environment', 'limits', 'review', 'plugins', 'general', 'backup'])
  expect(isSettingsSection('roles')).toBe(true)
  expect(isSettingsSection('nope')).toBe(false)
  expect(isSettingsSection(undefined)).toBe(false)
})

test('a terminal window has its own address, which the app\'s router names', () => {
  expect(terminalWindowLocation('agent', 't1')).toEqual({ path: '/terminal/agent/t1', query: {} })
  expect(terminalWindowLocation('user', 'a/b c')).toEqual({ path: '/terminal/user/a%2Fb%20c', query: {} })
  const r = createAppRouter({ history: createMemoryHistory() })
  const agent = r.resolve(terminalWindowLocation('agent', 'abc123'))
  expect(agent.name).toBe('terminal')
  expect(agent.params).toEqual({ kind: 'agent', id: 'abc123' })
  const user = r.resolve(terminalWindowLocation('user', 'a/b c'))
  expect(user.name).toBe('terminal')
  expect(user.params).toEqual({ kind: 'user', id: 'a/b c' })
  expect(r.resolve('/terminal/other/x').name).toBe('unknown')
})

test('a ticket page keeps the board filters, drops the keys of the ticket it came from and takes the ones given', () => {
  const from = at({ project: 'demo', needs_me: '1', q: 'x', tab: 'diff', comment: '55' })
  expect(ticketPageLocation(7, from)).toEqual({ path: '/ticket/7', query: { project: 'demo', needs_me: '1', q: 'x' } })
  expect(ticketPageLocation(7, from, { tab: 'comments', comment: 9 })).toEqual({ path: '/ticket/7', query: { project: 'demo', needs_me: '1', q: 'x', tab: 'comments', comment: '9' } })
})

test('from the panel to the page and back to the board, the filters stay', () => {
  const r = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { render: () => null } }] })
  const panel = r.resolve(ticketLocation(3, at({ story: '4', q: 'x' }), { tab: 'diff' }))
  const page = r.resolve(ticketPageLocation(3, panel, { tab: 'diff' }))
  expect(page.fullPath).toBe('/ticket/3?story=4&q=x&tab=diff')
  expect(r.resolve(ticketLocation(3, page)).fullPath).toBe('/3?story=4&q=x')
  expect(r.resolve(boardLocation(page)).fullPath).toBe('/?story=4&q=x')
})

test('ticketHere stays in the view the user is in', () => {
  const onPage = { name: TICKET_PAGE_ROUTE, query: { q: 'x', tab: 'agents' } }
  const onPanel = { name: 'ticket', query: { q: 'x', tab: 'agents' } }
  expect(onTicketPage(onPage)).toBe(true)
  expect(onTicketPage(onPanel)).toBe(false)
  expect(onTicketPage({ query: {} })).toBe(false)
  expect(ticketHere(5, onPage)).toEqual({ path: '/ticket/5', query: { q: 'x' } })
  expect(ticketHere(5, onPanel, { tab: 'diff' })).toEqual({ path: '/5', query: { q: 'x', tab: 'diff' } })
  expect(ticketHere(5, { name: 'board', query: {} })).toEqual({ path: '/5', query: {} })
})

test('with the setting on, a click opens the page, except from the panel', () => {
  const board = { name: 'board', query: { q: 'x' } }
  const settings = { name: 'settings', query: {} }
  const terminal = { name: 'terminal', query: {} }
  const onPage = { name: TICKET_PAGE_ROUTE, query: { q: 'x', tab: 'agents' } }
  const onPanel = { name: 'ticket', query: { q: 'x', tab: 'agents' } }
  // From the board and every other view without a ticket: the page.
  expect(ticketHere(5, board, {}, true)).toEqual({ path: '/ticket/5', query: { q: 'x' } })
  expect(ticketHere(5, board, { tab: 'diff', comment: 3 }, true)).toEqual({ path: '/ticket/5', query: { q: 'x', tab: 'diff', comment: '3' } })
  expect(ticketHere(5, settings, {}, true).path).toBe('/ticket/5')
  expect(ticketHere(5, terminal, {}, true).path).toBe('/ticket/5')
  expect(ticketHere(5, { query: {} }, {}, true).path).toBe('/ticket/5')
  // From the page: the page. From the panel: the panel stays.
  expect(ticketHere(5, onPage, {}, true)).toEqual({ path: '/ticket/5', query: { q: 'x' } })
  expect(ticketHere(5, onPanel, {}, true)).toEqual({ path: '/5', query: { q: 'x' } })
})

test('with the setting off or not given, a click opens the panel except from a page', () => {
  const board = { name: 'board', query: {} }
  const onPage = { name: TICKET_PAGE_ROUTE, query: {} }
  const onPanel = { name: 'ticket', query: {} }
  for (const asPage of [undefined, false]) {
    expect(ticketHere(5, board, {}, asPage).path).toBe('/5')
    expect(ticketHere(5, onPanel, {}, asPage).path).toBe('/5')
    expect(ticketHere(5, onPage, {}, asPage).path).toBe('/ticket/5')
  }
})
