import { expect, test } from 'vitest'
import type { Actor, ApiEvent, Comment } from '../../api/types'
import { bodyPreview, BODY_PREVIEW, buildThread, isQuestion, mergeEvents, statusText } from './thread'

let id = 0
function ev(kind: string, data: unknown, o: Partial<ApiEvent> = {}): ApiEvent {
  return { id: ++id, kind, ticket: 12, actor: { role: 'user' }, at: '2026-11-15T10:00:00.000Z', data, ...o }
}
const withId = (e: ApiEvent, n: number): ApiEvent => ({ ...e, id: n })

function comment(n: number, o: Partial<Comment> = {}): Comment {
  return { id: n, ticket: 12, author: { role: 'developer', name: 'Ada' }, body: `body ${n}`, reply_to: null, question_to: '' as Comment['question_to'], answered_by: null, mentions: [], created_at: '2026-11-15T10:00:00.000Z', ...o }
}
const created = (c: Comment, actor?: Actor) => ev('comment.created', { comment: c }, actor ? { actor } : {})
const summary = { number: 12, column: 'planning' }

test('buildThread: words for each kind; ticket.moved within Review shows only review outcomes', () => {
  const dev: Actor = { role: 'developer', name: 'Ada' }
  const rev: Actor = { role: 'reviewer', name: 'Bo' }
  const text = (e: ApiEvent) => statusText(e)
  expect(text(ev('ticket.created', { ticket: summary }))).toBe('You created #12 in Planning')
  expect(text(ev('ticket.moved', { ticket: summary, from: 'planning', to: 'ready', action: 'approve' }))).toBe('You moved #12 from Planning to Ready For Dev (approved)')
  expect(text(ev('ticket.moved', { ticket: summary, from: 'review', to: 'review', action: 'verdict' }, { actor: rev }))).toBe('Reviewer Bo moved #12 within Review (review verdict)')
  expect(text(ev('ticket.moved', { ticket: summary, from: 'review', to: 'review', action: 'approve' }))).toBeNull()
  expect(text(ev('review.verdict', { ticket: 12, round: 2, project: 'p', verdict: 'approve' }, { actor: rev }))).toBe('Review approved by reviewer Bo (round 2)')
  const agent = { agent: { role: 'developer', name: 'Ada', end_reason: 'omo restarted' } }
  expect(text(ev('agent.created', agent, { actor: dev }))).toBe('Developer Ada started')
  expect(text(ev('agent.ended', agent))).toBe('Developer Ada ended: omo restarted')
  expect(text(ev('description.changed', { ticket: 12, version: { number: 4 } }))).toBe('You edited the description (version 4)')
  expect(text(ev('dependency.added', { ticket: 12, depends_on: 9 }))).toBe('#12 now depends on #9')
  expect(text(ev('dependency.removed', { ticket: 12, depends_on: 9 }))).toBe('#12 no longer depends on #9')
  expect(text(ev('ticket.retried', { ticket: 12, step: 'merge' }))).toBe('You retried the merge')
  expect(text(ev('ticket.updated', {}))).toBeNull()
  // The whole thread, in order, with only what shows.
  const items = buildThread([ev('ticket.moved', { ticket: summary, from: 'review', to: 'review', action: 'approve' }), created(comment(1)), ev('ticket.retried', { ticket: 12, step: 'start' })])
  expect(items.map((i) => i.kind)).toEqual(['comment', 'status'])
})

test('names in status texts are made visible', () => {
  const t = statusText(ev('agent.created', { agent: { role: 'developer', name: 'Ada‮evil' } }))
  expect(t).toBe('Developer Ada\\u{202E}evil started')
})

test('mergeEvents: replaying a page and the live events twice gives the same thread', () => {
  const page = [created(comment(1)), created(comment(2)), ev('ticket.retried', { ticket: 12, step: 'start' })]
  const live = [created(comment(3))]
  const once = mergeEvents(mergeEvents([], page), live)
  const twice = mergeEvents(mergeEvents(mergeEvents(once, page), live), live)
  expect(twice.map((e) => e.id)).toEqual(once.map((e) => e.id))
  expect(buildThread(twice)).toEqual(buildThread(once))
  expect(twice.map((e) => e.id)).toEqual([...twice.map((e) => e.id)].sort((a, b) => a - b))
})

test('mergeEvents: an older page joins without duplicates', () => {
  const all = Array.from({ length: 6 }, (_, i) => withId(created(comment(i + 1)), 100 + i))
  const newest = all.slice(3)
  const older = all.slice(0, 4) // overlaps by one
  const merged = mergeEvents(newest, older)
  expect(merged.map((e) => e.id)).toEqual([100, 101, 102, 103, 104, 105])
})

test('question flips to answered on question.answered', () => {
  const q = comment(5, { question_to: 'user' })
  const events = [created(q)]
  const open = buildThread(events)[0]!
  expect(open.kind === 'comment' && open.answered).toBe(false)
  const answered = buildThread([...events, created(comment(6, { reply_to: 5, author: { role: 'user' } })), ev('question.answered', { ticket: 12, question: 5, answer: 6 })])
  const first = answered[0]!
  expect(first.kind === 'comment' && first.answered && first.answeredBy).toBe(6)
  expect(answered).toHaveLength(2)
  expect(isQuestion(q)).toBe(true)
  expect(isQuestion(comment(7))).toBe(false)
})

test('bodyPreview: a long body is cut at a line end within the limit; short ones stay whole', () => {
  expect(bodyPreview('short', 100)).toEqual({ text: 'short', cut: false })
  const lines = Array.from({ length: 50 }, (_, i) => `line ${i}`).join('\n')
  const p = bodyPreview(lines, 100)
  expect(p.cut).toBe(true)
  expect(p.text.length).toBeLessThanOrEqual(100)
  expect(lines.startsWith(p.text)).toBe(true)
  expect(lines.charAt(p.text.length)).toBe('\n')
  // One long line is cut at the limit, never inside a surrogate pair.
  const snails = '🐌'.repeat(100)
  const q = bodyPreview(snails, 101)
  expect(q.cut).toBe(true)
  expect(q.text).toBe('🐌'.repeat(50))
  expect(BODY_PREVIEW).toBe(16 * 1024)
})
