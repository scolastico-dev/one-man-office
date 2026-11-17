import { expect, test } from 'vitest'
import type { AgentRef } from '../../api/types'
import { ticket } from '../board/testing'
import { askTargets, mentionNames, mentionOptions } from './mentions'

const agent = (role: AgentRef['role'], name: string, session = `s-${name}`): AgentRef => ({ session, role, name, state: 'running', purpose: 'work' })
const roles = (o: { role: string }[]) => o.map((x) => x.role)

test('mentionOptions: @ceo only while enabled; @pm only with a story; names beside roles', () => {
  const standalone = ticket({ number: 1, type: 'task', story: null })
  const inStory = ticket({ number: 2, type: 'task', story: 9 })
  const story = ticket({ number: 9, type: 'story' })
  const off = { ceoEnabled: false, agents: [] }
  const on = { ceoEnabled: true, agents: [agent('developer', 'calm-badger'), agent('pm', 'quiet-heron')] }
  expect(roles(mentionOptions(standalone, off))).toEqual(['developer', 'reviewer', 'supervisor', 'user'])
  expect(roles(mentionOptions(standalone, on))).toEqual(['developer', 'reviewer', 'supervisor', 'ceo', 'user'])
  expect(roles(mentionOptions(inStory, off))).toEqual(['developer', 'reviewer', 'pm', 'supervisor', 'user'])
  expect(roles(mentionOptions(story, off))).toEqual(['reviewer', 'pm', 'supervisor', 'user'])
  expect(mentionOptions(inStory, on).find((o) => o.role === 'developer')?.name).toBe('calm-badger')
  expect(mentionOptions(inStory, on).find((o) => o.role === 'pm')?.name).toBe('quiet-heron')
  expect(mentionOptions(inStory, on).find((o) => o.role === 'reviewer')?.name).toBeUndefined()
})

test('askTargets: the user cannot ask themselves; pm where one exists, ceo while enabled', () => {
  const standalone = ticket({ number: 1, type: 'task', story: null })
  const story = ticket({ number: 9, type: 'story' })
  expect(askTargets(standalone, { ceoEnabled: false, agents: [] })).toEqual([])
  expect(askTargets(story, { ceoEnabled: true, agents: [] }).map((t) => t.to)).toEqual(['pm', 'ceo'])
})

test('mentionNames: the agent behind the session; supervisor follows the ticket', () => {
  const t = ticket({ number: 2, type: 'task', story: 9, supervisor: 'pm' })
  const names = mentionNames([{ role: 'pm', session: 's-quiet' }, { role: 'user' }], t, { ceoEnabled: false, agents: [agent('pm', 'quiet', 's-quiet')] })
  expect(names).toEqual({ pm: 'quiet', supervisor: 'quiet', user: '' })
})

const proj = (id: string) => ({ project: id, base_branch: 'main', branch: `omo/${id}`, worktree: '', export_url: '', export_state: '' as const, export_error: '' })

test('mentionOptions: with several projects each reviewer can be mentioned on its own, after @reviewer', () => {
  const multi = ticket({ number: 9, type: 'story', projects: [proj('api'), proj('web')] })
  const ctx = {
    ceoEnabled: false,
    agents: [{ ...agent('reviewer', 'keen-owl'), project: 'web' }, { ...agent('reviewer', 'old-owl'), project: 'api', state: 'ended' as const }],
    projectName: (id: string) => (id === 'web' ? 'The Web' : id),
  }
  const options = mentionOptions(multi, ctx)
  expect(roles(options)).toEqual(['reviewer', 'reviewer:api', 'reviewer:web', 'pm', 'supervisor', 'user'])
  // @reviewer reaches both: no single agent beside it.
  expect(options[0]).toEqual({ role: 'reviewer' })
  expect(options.find((o) => o.role === 'reviewer:web')).toEqual({ role: 'reviewer:web', project: 'The Web', name: 'keen-owl' })
  // An ended reviewer is not behind the mention; without projectName the id stands in.
  expect(options.find((o) => o.role === 'reviewer:api')).toEqual({ role: 'reviewer:api', project: 'api' })
})

test('mentionOptions: one project, or none, offers @reviewer alone', () => {
  const ctx = { ceoEnabled: false, agents: [] }
  expect(roles(mentionOptions(ticket({ number: 1, projects: [proj('api')] }), ctx))).toEqual(['developer', 'reviewer', 'supervisor', 'user'])
  expect(roles(mentionOptions(ticket({ number: 1, projects: [] }), ctx))).toEqual(['developer', 'reviewer', 'supervisor', 'user'])
})
