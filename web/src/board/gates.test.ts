import { describe, expect, test } from 'vitest'
import type { TicketGate, TicketGates } from '../api/types'
import { ticket } from '../features/board/testing'
import { badgeFor } from './badges'
import { allPassed, gateTarget, heldGates, holdSummary } from './gates'

const gate = (over: Partial<TicketGate> = {}): TicketGate => ({ plugin: 'pull-base', state: 'held', reason: 'the base is behind', choices: [], ...over })
const gates = (list: TicketGate[], over: Partial<TicketGates> = {}): TicketGates => ({ move: 'start', from: 'ready', to: 'in_progress', gates: list, ...over })

describe('heldGates', () => {
  test('lists only the plugins that hold the move', () => {
    const g = gates([gate({ plugin: 'a' }), gate({ plugin: 'b', state: 'passed' }), gate({ plugin: 'c', state: 'pending' })])
    expect(heldGates(g).map((x) => x.plugin)).toEqual(['a'])
    expect(heldGates(null)).toEqual([])
  })
})

describe('holdSummary', () => {
  test('is null without a gate and for a gate that has no hold', () => {
    expect(holdSummary(null)).toBeNull()
  })

  test('a hold names its plugin and reason', () => {
    expect(holdSummary(gates([gate()]))).toEqual({ held: true, text: 'held by pull-base', title: 'pull-base: the base is behind' })
  })

  test('several holds name the first and count the rest; every reason is in the title', () => {
    const s = holdSummary(gates([gate({ plugin: 'a', reason: 'one' }), gate({ plugin: 'b', reason: 'two' })]))
    expect(s).toEqual({ held: true, text: 'held by a and 1 more', title: 'a: one\nb: two' })
  })

  test('a gate that still waits for answers is not a hold', () => {
    expect(holdSummary(gates([gate({ state: 'pending', reason: '' }), gate({ plugin: 'b', state: 'passed' })]))).toEqual({
      held: false,
      text: 'waiting for a plugin',
      title: 'Waiting for pull-base to answer',
    })
  })

  test('a gate whose plugins all passed has nothing to hold or wait for', () => {
    expect(holdSummary(gates([gate({ state: 'passed' }), gate({ plugin: 'b', state: 'passed' })]))).toBeNull()
    expect(allPassed(gates([gate({ state: 'passed' })]))).toBe(true)
    expect(allPassed(gates([gate({ state: 'passed' }), gate({ plugin: 'b', state: 'pending' })]))).toBe(false)
    expect(allPassed(null)).toBe(false)
  })

  test('plugin names and reasons are shown as visible text', () => {
    expect(holdSummary(gates([gate({ plugin: 'a‮b', reason: 'x\u0007y' })]))?.title).not.toMatch(/[‮\u0007]/)
  })
})

describe('gateTarget', () => {
  test('is the column the gated move goes to, for a ticket that is in the column the move leaves', () => {
    expect(gateTarget(ticket({ column: 'ready', gate: gates([gate()]) }))).toBe('in_progress')
    expect(gateTarget(ticket({ column: 'planning', gate: gates([gate()], { move: 'release', from: 'planning', to: 'ready' }) }))).toBe('ready')
    expect(gateTarget(ticket({ column: 'planning' }))).toBeNull()
  })
})

describe('badgeFor with a gate', () => {
  test('a held ticket shows who holds it, also in Planning where start_blocked says nothing', () => {
    const b = badgeFor(ticket({ column: 'planning', gate: gates([gate()], { move: 'release', from: 'planning', to: 'ready' }) }))
    expect(b).toMatchObject({ tone: 'warn', text: 'held by pull-base', title: 'pull-base: the base is behind' })
  })

  test('a held start takes the place of the generic wait for a plugin', () => {
    expect(badgeFor(ticket({ column: 'ready', start_blocked: 'gate', gate: gates([gate()]) }))?.text).toBe('held by pull-base')
  })

  test('a gate that is still being asked is a quiet wait', () => {
    expect(badgeFor(ticket({ column: 'ready', start_blocked: 'gate', gate: gates([gate({ state: 'pending' })]) }))).toMatchObject({ tone: 'muted', text: 'waiting for a plugin' })
  })

  test('attention and a question for you come before a hold', () => {
    const gate1 = gates([gate()])
    expect(badgeFor(ticket({ attention: 'x', gate: gate1 }))?.text).toBe('needs attention')
    expect(badgeFor(ticket({ questions_for_user: 1, gate: gate1 }))?.text).toBe('question for you')
  })
})
