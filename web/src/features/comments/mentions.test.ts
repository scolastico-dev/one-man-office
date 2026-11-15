import { expect, test } from 'vitest'
import type { AgentRef } from '../../api/types'
import { ticket } from '../board/testing'
import { askTargets, mentionNames, mentionOptions, MENTION_RULE } from './mentions'

const agent = (role: AgentRef['role'], name: string, session = `s-${name}`): AgentRef => ({ session, role, name, state: 'running', purpose: 'work' })
const roles = (o: { role: string }[]) => o.map((x) => x.role)

test('the rule text is the board rule', () => {
  expect(MENTION_RULE).toContain('@ceo only while the CEO is on')
})

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
