import { expect, test } from 'vitest'
import type { AgentRef } from '../../api/types'
import { ticket } from '../board/testing'
import { agentActivity, keptAgent } from './agentActivity'

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
  expect(agentActivity(t)).toBe('The reviewer is working on this ticket. The developer is parked while the ticket is reviewed and can answer questions.')
  const parkedOnly = ticket({ column: 'review', review_stage: 'agent', agents: [agent('developer', 'waiting')] })
  expect(agentActivity(parkedOnly)).toBe('The developer is parked while the ticket is reviewed and can answer questions.')
  expect(agentActivity(parkedOnly)).not.toMatch(/working/)
})

test('an export job is named as such; two agents of one role in one state are said once', () => {
  expect(agentActivity(ticket({ agents: [agent('developer', 'running', { purpose: 'export' })] }))).toBe('The export job is working on this ticket.')
  expect(agentActivity(ticket({ agents: [agent('developer', 'running', { session: 'a' }), agent('developer', 'running', { session: 'b' })] }))).toBe(
    'The developer is working on this ticket.',
  )
})

test('agents kept for questions say so: a reviewer after its verdict, the developer or product manager during the review', () => {
  const human = ticket({ column: 'review', review_stage: 'human', agents: [agent('reviewer', 'waiting'), agent('developer', 'waiting')] })
  expect(agentActivity(human)).toBe(
    'The reviewer has given its verdict and can answer questions about its review until the next review round. The developer is parked while the ticket is reviewed and can answer questions.',
  )
  // Rejected: the ticket is back in progress, the reviewer stays; the developer works again.
  const rejected = ticket({ column: 'in_progress', agents: [agent('developer', 'running'), agent('reviewer', 'waiting')] })
  expect(agentActivity(rejected)).toBe(
    'The developer is working on this ticket. The reviewer has given its verdict and can answer questions about its review until the next review round.',
  )
  const story = ticket({ type: 'story', column: 'review', review_stage: 'merging', agents: [agent('pm', 'waiting')] })
  expect(agentActivity(story)).toBe('The product manager is parked while the ticket is reviewed and can answer questions.')
  // Waiting for an answer comes first; a developer in progress waits for its turn.
  expect(agentActivity(ticket({ column: 'review', review_stage: 'human', agents: [agent('reviewer', 'waiting')], open_questions: 1 }))).toBe(
    'The reviewer waits for an answer to its question.',
  )
  expect(keptAgent(agent('reviewer', 'waiting'), human)).toBe(true)
  expect(keptAgent(agent('developer', 'waiting'), ticket({ column: 'in_progress' }))).toBe(false)
  expect(keptAgent(agent('developer', 'waiting', { purpose: 'export' }), human)).toBe(false)
  expect(keptAgent(agent('reviewer', 'running'), human)).toBe(false)
})
