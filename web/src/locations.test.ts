import { expect, test } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { boardLocation, isSettingsSection, SETTINGS_SECTIONS_IN_ORDER, settingsLocation, ticketLocation } from './locations'

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
  expect(SETTINGS_SECTIONS_IN_ORDER).toEqual(['projects', 'agents', 'roles', 'usage', 'prompts', 'skills', 'environment', 'limits', 'review', 'plugins', 'general'])
  expect(isSettingsSection('roles')).toBe(true)
  expect(isSettingsSection('nope')).toBe(false)
  expect(isSettingsSection(undefined)).toBe(false)
})
