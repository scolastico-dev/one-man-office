import { describe, expect, test } from 'vitest'
import type { TicketSummary } from '../api/types'
import { ticket } from '../features/board/testing'
import { badgeFor, captionFor } from './badges'

const t = (over: Partial<TicketSummary>) => ticket(over)

describe('badgeFor', () => {
  test('a quiet ticket has no badge', () => {
    expect(badgeFor(t({}))).toBeNull()
  })

  test('badge priority: attention or failed_step, question for you, your review, waiting for #n, start_blocked', () => {
    const all: Partial<TicketSummary> = {
      attention: 'stuck',
      failed_step: 'agent',
      questions_for_user: 2,
      column: 'review',
      review_stage: 'human',
      waiting_for: [{ ticket: 4, reason: 'not_done' }],
      start_blocked: 'slot',
    }
    expect(badgeFor(t(all))?.text).toBe('needs attention')
    const { attention: _a, failed_step: _f, ...noAttention } = all
    expect(badgeFor(t(noAttention))?.text).toBe('question for you')
    const { questions_for_user: _q, ...noQuestion } = noAttention
    expect(badgeFor(t(noQuestion))?.text).toBe('your review')
    const { column: _c, review_stage: _r, ...noReview } = noQuestion
    expect(badgeFor(t(noReview))?.text).toBe('waiting for #4')
    const { waiting_for: _w, ...onlyBlocked } = noReview
    expect(badgeFor(t(onlyBlocked))?.text).toBe('waiting for a slot')
  })

  test('failed_step counts as needs attention with the step in the tooltip', () => {
    expect(badgeFor(t({ failed_step: 'start', attention: 'no worktree' }))).toMatchObject({ tone: 'danger', text: 'needs attention', title: 'starting work failed: no worktree' })
    expect(badgeFor(t({ failed_step: 'agent' }))?.title).toBe('the agent keeps failing')
    expect(badgeFor(t({ failed_step: 'merge', attention: 'conflict' }))?.title).toBe('merge blocked: conflict')
    expect(badgeFor(t({ attention: 'stuck' }))?.title).toBe('stuck')
    expect(badgeFor(t({ failed_step: '' }))).toBeNull()
  })

  test('question for you counts the questions', () => {
    expect(badgeFor(t({ questions_for_user: 1 }))?.title).toBe('1 open question for you')
    expect(badgeFor(t({ questions_for_user: 3 }))?.title).toBe('3 open questions for you')
    expect(badgeFor(t({ open_questions: 2 }))).toBeNull()
  })

  test('your review only in Review at the human stage', () => {
    expect(badgeFor(t({ column: 'review', review_stage: 'human' }))?.text).toBe('your review')
    expect(badgeFor(t({ column: 'review', review_stage: 'agent' }))).toBeNull()
  })

  test('waiting badge texts for not_done, export_not_merged and cancelled', () => {
    const w = (reason: 'not_done' | 'export_not_merged' | 'cancelled') => badgeFor(t({ waiting_for: [{ ticket: 12, reason }, { ticket: 13, reason: 'not_done' }] }))?.text
    expect(w('not_done')).toBe('waiting for #12')
    expect(w('export_not_merged')).toBe('waiting for #12 · PR not merged')
    expect(w('cancelled')).toBe('waiting for #12 · cancelled')
  })

  test('start_blocked slot/paused/no_model/usage give their badge; absent or empty gives none', () => {
    expect(badgeFor(t({ start_blocked: 'slot' }))?.text).toBe('waiting for a slot')
    expect(badgeFor(t({ start_blocked: 'paused' }))?.text).toBe('paused')
    expect(badgeFor(t({ start_blocked: 'no_model' }))?.text).toBe('no usable model')
    expect(badgeFor(t({ start_blocked: 'usage' }))?.text).toBe('waiting for usage')
    expect(badgeFor(t({ start_blocked: '' }))).toBeNull()
    expect(badgeFor(t({}))).toBeNull()
    // A reason this version does not know is not guessed at.
    expect(badgeFor(t({ start_blocked: 'later' as never }))).toBeNull()
  })

  test('the reason a ticket has not started is shown in every column: work that waits for usage in In Progress and Review too', () => {
    expect(badgeFor(t({ column: 'in_progress', start_blocked: 'usage' }))?.text).toBe('waiting for usage')
    expect(badgeFor(t({ column: 'review', review_stage: 'agent', start_blocked: 'usage' }))?.text).toBe('waiting for usage')
  })

  test('a Ready ticket that is not blocked shows nothing, whatever else holds for it', () => {
    expect(badgeFor(t({ column: 'ready', agents: [], busy: false }))).toBeNull()
  })

  test('every badge has words', () => {
    for (const over of [{ attention: 'x' }, { questions_for_user: 1 }, { column: 'review', review_stage: 'human' }, { waiting_for: [{ ticket: 1, reason: 'cancelled' }] }, { start_blocked: 'paused' }] as Partial<TicketSummary>[]) {
      expect(badgeFor(t(over))?.text.length).toBeGreaterThan(0)
    }
  })
})

describe('captionFor', () => {
  test('busy shows starting or merging instead of the stage', () => {
    expect(captionFor(t({ column: 'ready', busy: true }))).toBe('starting…')
    expect(captionFor(t({ column: 'review', review_stage: 'merging', busy: true }))).toBe('merging…')
    expect(captionFor(t({ column: 'review', review_stage: 'agent', busy: true }))).toBe('merging…')
  })
  test('review stages and done resolutions', () => {
    expect(captionFor(t({ column: 'review', review_stage: 'agent' }))).toBe('agent review')
    expect(captionFor(t({ column: 'review', review_stage: 'merging' }))).toBe('merging…')
    expect(captionFor(t({ column: 'review', review_stage: 'exporting' }))).toBe('exporting…')
    expect(captionFor(t({ column: 'review', review_stage: 'human' }))).toBeUndefined()
    expect(captionFor(t({ column: 'done', resolution: 'cancelled' }))).toBe('cancelled')
    expect(captionFor(t({ column: 'done', resolution: 'exported' }))).toBe('exported')
    expect(captionFor(t({ column: 'done', resolution: 'completed' }))).toBeUndefined()
    expect(captionFor(t({ column: 'planning' }))).toBeUndefined()
  })
})
