import { expect, test } from 'vitest'
import type { ApiEvent } from '../api/types'
import { concernsClients, concernsTakeover } from './mcpEvents'

const changed = (section: string): ApiEvent => ({ id: 1, kind: 'settings.changed', ticket: null, actor: { role: 'user' }, at: '', data: { section, name: '', source: 'app' } })

test('the agent programs are read again when the profiles or the general settings change', () => {
  expect(concernsClients(changed('profiles'))).toBe(true)
  expect(concernsClients(changed('general'))).toBe(true)
  expect(concernsClients(changed('roles'))).toBe(false)
  expect(concernsClients({ ...changed('profiles'), kind: 'mcp.changed' })).toBe(false)
})

test('the take-over is read again when omo’s servers or the profiles change', () => {
  expect(concernsTakeover(changed('mcpservers'))).toBe(true)
  expect(concernsTakeover(changed('profiles'))).toBe(true)
  expect(concernsTakeover(changed('general'))).toBe(false)
})
