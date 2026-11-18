import { expect, test } from 'vitest'
import type { PluginsResponse } from '../../api/types'
import { ticket } from '../board/testing'
import { controlsOf } from './controls'

const list = { plugins: [{ name: 'editor', state: 'ready', buttons: [
  { label: 'Edit', place: 'ticket', action: 'edit', per_worktree: true },
  { label: 'Project editor', place: 'project', action: 'project' },
  { label: 'Global', place: 'sidebar', action: 'global' },
] }], settings: [{ name: 'editor', enabled: true }] } as PluginsResponse

test('per-worktree ticket controls expand only recorded usable worktrees and send the selected project', () => {
  const t = ticket({ number: 7, projects: [
    { project: 'one', worktree: '/work/one' }, { project: 'two', worktree: '/work/two' }, { project: 'missing', worktree: '' },
  ] as never })
  const buttons = controlsOf(list, [], 'ticket', t)
  expect(buttons.map((b) => [b.label, b.ticket, b.project, b.why])).toEqual([
    ['Edit — one', 7, 'one', undefined], ['Edit — two', 7, 'two', undefined],
  ])
})

test('missing folders are explicit and projectless working folders never select another repository', () => {
  const t = ticket({ number: 7, projects: [], workdir: '' })
  expect(controlsOf(list, [], 'ticket', t)[0]?.why).toMatch(/working folder/i)
  t.workdir = '/work/7'
  const b = controlsOf(list, [], 'ticket', t)[0]!
  expect([b.ticket, b.project, b.why]).toEqual([7, undefined, undefined])
})

test('sidebar/project controls are distinct and disabled plugins have no controls', () => {
  expect(controlsOf(list, [], 'sidebar').map((b) => [b.label, b.ticket, b.project])).toEqual([['Global', undefined, undefined]])
  expect(controlsOf(list, [], 'project', undefined, 'one').map((b) => [b.label, b.ticket, b.project])).toEqual([['Project editor', undefined, 'one']])
  expect(controlsOf({ ...list, settings: [{ ...list.settings[0]!, enabled: false }] }, [], 'sidebar')).toEqual([])
})
