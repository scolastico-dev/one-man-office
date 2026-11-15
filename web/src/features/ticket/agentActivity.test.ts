import { expect, test } from 'vitest'
import type { AgentRef } from '../../api/types'
import { ticket } from '../board/testing'
import { agentActivity } from './agentActivity'

const agent = (role: AgentRef['role'], state: AgentRef['state'], over: Partial<AgentRef> = {}): AgentRef => ({ session: `${role}-${state}`, role, name: role, state, purpose: 'work', ...over })

test('no live agent: nothing to say', () => {
  expect(agentActivity(ticket({ agents: [] }))).toBeNull()
  expect(agentActivity(ticket({ agents: [agent('developer', 'ended')] }))).toBeNull()
})

test('says what each live agent does, from its session state', () => {
  expect(agentActivity(ticket({ agents: [agent('developer', 'running')] }))).toBe('The developer is working on this ticket.')
  expect(agentActivity(ticket({ agents: [agent('developer', 'starting')] }))).toBe('The developer is starting on this ticket.')
  expect(agentActivity(ticket({ agents: [agent('pm', 'waiting')], open_questions: 1 }))).toBe('The product manager waits for an answer to its question.')
  expect(agentActivity(ticket({ agents: [agent('developer', 'waiting')], open_questions: 0 }))).toBe('The developer waits for its turn.')
})

test('a ticket in Review whose developer is parked while the reviewer works says both, and never that the parked one works', () => {
  const t = ticket({ column: 'review', review_stage: 'agent', agents: [agent('developer', 'waiting'), agent('reviewer', 'running')] })
  expect(agentActivity(t)).toBe('The reviewer is working on this ticket. The developer waits for its turn.')
  const parkedOnly = ticket({ column: 'review', review_stage: 'agent', agents: [agent('developer', 'waiting')] })
  expect(agentActivity(parkedOnly)).toBe('The developer waits for its turn.')
  expect(agentActivity(parkedOnly)).not.toMatch(/working/)
})

test('an export job is named as such; two agents of one role in one state are said once', () => {
  expect(agentActivity(ticket({ agents: [agent('developer', 'running', { purpose: 'export' })] }))).toBe('The export job is working on this ticket.')
  expect(agentActivity(ticket({ agents: [agent('developer', 'running', { session: 'a' }), agent('developer', 'running', { session: 'b' })] }))).toBe(
    'The developer is working on this ticket.',
  )
})
