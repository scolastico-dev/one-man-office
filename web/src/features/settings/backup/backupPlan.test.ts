import { expect, test } from 'vitest'
import type { ImportPlan } from '../../../api/types'
import { agentsRunText, changesAnything, exportFileName, planGroups, resultLines } from './backupPlan'

const plan = (over: Partial<ImportPlan> = {}): ImportPlan => ({
  id: 'x',
  manifest: { format: 1, omo_version: 'v1.0.0', schema_version: 6, created_at: '2026-11-17T10:00:00Z', database: false },
  files: [
    { path: 'settings.yaml', change: 'replace' },
    { path: 'profiles/a.yaml', change: 'add' },
    { path: 'roles/ceo.yaml', change: 'same' },
  ],
  server: '',
  database: false,
  agents_running: 0,
  ...over,
})

test('the file name carries the local date', () => {
  expect(exportFileName(new Date(2026, 0, 5, 23, 59))).toBe('omo-settings-2026-01-05.zip')
})

test('the plan in groups', () => {
  expect(planGroups(plan())).toEqual({ replace: ['settings.yaml'], add: ['profiles/a.yaml'], same: 1 })
})

test('an import changes something only with a changed file or a chosen extra', () => {
  const same = plan({ files: [{ path: 'roles/ceo.yaml', change: 'same' }], server: 'replace', database: true })
  expect(changesAnything(plan(), false, false)).toBe(true)
  expect(changesAnything(same, false, false)).toBe(false)
  expect(changesAnything(same, true, false)).toBe(true)
  expect(changesAnything(same, false, true)).toBe(true)
  expect(changesAnything({ ...same, server: 'same' }, true, false)).toBe(false)
})

test('agents in words', () => {
  expect(agentsRunText(1)).toBe('1 agent runs')
  expect(agentsRunText(3)).toBe('3 agents run')
})

test('the result in sentences', () => {
  expect(resultLines({ written: ['settings.yaml'], backup: '/h/backups/x', database_pending: false, restart_needed: false })).toEqual([
    'Imported 1 file.',
    'The replaced files are kept in /h/backups/x.',
  ])
  const db = resultLines({ written: [], backup: '', database_pending: true, restart_needed: true })
  expect(db[0]).toBe('No settings file changed.')
  expect(db[1]).toMatch(/^omo is paused\. Restart omo/)
  expect(resultLines({ written: ['server.yaml', 'a', 'b'], backup: '', database_pending: false, restart_needed: true })).toEqual([
    'Imported 3 files.',
    'The server settings apply at the next start of omo.',
  ])
})
