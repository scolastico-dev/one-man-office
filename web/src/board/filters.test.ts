import { describe, expect, test } from 'vitest'
import type { TicketSummary } from '../api/types'
import { ticket } from '../features/board/testing'
import { matches, matchesText, needsMe } from './filters'

const proj = (id: string) => ({ project: id, base_branch: 'main', branch: '', worktree: '', export_url: '', export_state: '' as const, export_error: '' })

describe('needsMe', () => {
  test('question to the user, attention on a ticket the user supervises, or a human review', () => {
    expect(needsMe(ticket({ questions_for_user: 1 }))).toBe(true)
    expect(needsMe(ticket({ open_questions: 2 }))).toBe(false)
    expect(needsMe(ticket({ attention: 'stuck', supervisor: 'user' }))).toBe(true)
    expect(needsMe(ticket({ attention: 'stuck', supervisor: 'pm' }))).toBe(false)
    expect(needsMe(ticket({ column: 'review', review_stage: 'human' }))).toBe(true)
    expect(needsMe(ticket({ column: 'review', review_stage: 'agent' }))).toBe(false)
    expect(needsMe(ticket({}))).toBe(false)
  })
})

describe('matchesText', () => {
  const t = ticket({ number: 12, title: 'Fix the Login für Größe' })
  test('text search matches #12, 12 and the title case-insensitively, or a server hit', () => {
    expect(matchesText(t, '#12')).toBe(true)
    expect(matchesText(t, '12')).toBe(true)
    expect(matchesText(t, ' # 12')).toBe(false)
    expect(matchesText(t, '#1')).toBe(false)
    expect(matchesText(t, 'login')).toBe(true)
    expect(matchesText(t, 'LOGIN')).toBe(true)
    expect(matchesText(t, 'missing')).toBe(false)
    expect(matches(t, { q: 'unrelated' })).toBe(false)
    expect(matches(t, { q: 'unrelated' }, new Set([12]))).toBe(true)
    expect(matches(t, { q: 'unrelated' }, new Set([13]))).toBe(false)
  })
  test('extra spaces do not matter', () => {
    expect(matchesText(t, '  fix   the\tlogin ')).toBe(true)
    expect(matchesText(ticket({ title: 'fix    the  login' }), 'fix the login')).toBe(true)
  })
  test('German umlauts match themselves, in either spelling, and in capitals', () => {
    expect(matchesText(t, 'für')).toBe(true)
    expect(matchesText(t, 'FÜR')).toBe(true)
    expect(matchesText(t, 'größe')).toBe(true)
    expect(matchesText(t, 'GRÖSSE')).toBe(false)
    expect(matchesText(t, 'fur')).toBe(false)
    expect(matchesText(t, 'für')).toBe(true)
  })
  test('an empty search matches everything', () => {
    expect(matchesText(t, '   ')).toBe(true)
  })
})

describe('matches', () => {
  const tickets: TicketSummary[] = [
    ticket({ number: 1, type: 'story', title: 'S', projects: [proj('a'), proj('b')], supervisor: 'pm' }),
    ticket({ number: 2, story: 1, title: 'T1', projects: [proj('a')], supervisor: 'pm' }),
    ticket({ number: 3, title: 'T2', projects: [proj('b')], supervisor: 'user', questions_for_user: 1 }),
  ]
  const pick = (f: Parameters<typeof matches>[1]) => tickets.filter((t) => matches(t, f)).map((t) => t.number)
  test('no filter matches everything', () => {
    expect(pick({})).toEqual([1, 2, 3])
    expect(pick({ q: '  ' })).toEqual([1, 2, 3])
  })
  test('project, story, supervisor and needs me', () => {
    expect(pick({ project: 'a' })).toEqual([1, 2])
    expect(pick({ project: 'b' })).toEqual([1, 3])
    expect(pick({ story: 1 })).toEqual([1, 2])
    expect(pick({ supervisor: 'user' })).toEqual([3])
    expect(pick({ needsMe: true })).toEqual([3])
    expect(pick({ needsMe: false })).toEqual([1, 2, 3])
  })
  test('filters combine', () => {
    expect(pick({ project: 'b', supervisor: 'pm' })).toEqual([1])
    expect(pick({ story: 1, q: 't1' })).toEqual([2])
    expect(matches(tickets[2]!, { project: 'a' }, new Set([3]))).toBe(false)
  })
})
