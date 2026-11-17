import { describe, expect, test } from 'vitest'
import type { TicketSummary } from '../api/types'
import { ticket } from '../features/board/testing'
import { badgeFor, captionFor } from './badges'
import { needsMe } from './filters'
import { slotView } from './slots'

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
    expect(badgeFor(t(onlyBlocked))?.text).toBe('waiting for a task slot')
  })

  test('a wait for a slot names its kind and the numbers from the board; a full story says so', () => {
    const board = [
      t({ number: 1, type: 'task', column: 'in_progress' }),
      t({ number: 2, type: 'task', column: 'review', review_stage: 'human' }),
      t({ number: 3, type: 'task', column: 'ready', busy: true }),
      t({ number: 4, type: 'task', column: 'done' }),
      t({ number: 5, type: 'story', column: 'in_progress', max_parallel: 1 }),
      t({ number: 6, type: 'task', column: 'in_progress', story: 5 }),
      t({ number: 7, type: 'story', column: 'planning' }),
    ]
    const slots = slotView(board, { tasks: 4, stories: 1 })
    expect(slots).toMatchObject({ tasks: { in_work: 4, limit: 4 }, stories: { in_work: 1, limit: 1 } })
    const task = t({ number: 8, type: 'task', column: 'ready', start_blocked: 'slot' })
    expect(badgeFor(task, slots)).toEqual({ tone: 'muted', text: 'waiting for a task slot', title: 'Waiting for a free task slot (4 of 4 in work)' })
    const story = t({ number: 9, type: 'story', column: 'ready', start_blocked: 'slot' })
    expect(badgeFor(story, slots)).toEqual({ tone: 'muted', text: 'waiting for a story slot', title: 'Waiting for a free story slot (1 of 1 in work)' })
    const inStory = t({ number: 10, type: 'task', column: 'ready', story: 5, start_blocked: 'slot' })
    expect(badgeFor(inStory, slots)).toEqual({ tone: 'muted', text: 'story limit reached', title: 'Waiting for a free place in story #5 (1 of 1 tasks in work)' })
    // Without the settings the kind is still named.
    expect(badgeFor(task, null)?.title).toBe('Waiting for a free task slot')
    expect(slotView(board, null)).toBeNull()
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
    expect(badgeFor(t({ start_blocked: 'slot' }))?.text).toBe('waiting for a task slot')
    expect(badgeFor(t({ start_blocked: 'paused' }))?.text).toBe('paused')
    expect(badgeFor(t({ start_blocked: 'no_model' }))?.text).toBe('no usable model')
    expect(badgeFor(t({ start_blocked: 'usage' }))?.text).toBe('waiting for usage')
    // Its tooltip says which model it waits for; the panel says until when.
    expect(badgeFor(t({ start_blocked: 'usage', model: 'claude/opus' }))?.title).toBe('claude/opus is near its usage limit')
    expect(badgeFor(t({ start_blocked: 'usage', model: '' }))?.title).toBe('the models of its role are near their usage limits')
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

describe('a ticket that needs a person', () => {
  test('is "needs attention" for the user and names the supervisor otherwise, as the counters count only the user\'s', () => {
    expect(badgeFor(t({ attention: 'stuck' }))).toMatchObject({ tone: 'danger', text: 'needs attention' })
    expect(badgeFor(t({ attention: 'stuck', supervisor: 'ceo' }))).toMatchObject({ tone: 'warn', text: 'needs the CEO', title: 'stuck' })
    expect(badgeFor(t({ failed_step: 'agent', supervisor: 'pm' }))).toMatchObject({ tone: 'warn', text: 'needs the product manager' })
  })
  test('the card says "needs attention" exactly for the tickets Needs me counts', () => {
    for (const supervisor of ['user', 'ceo', 'pm'] as const) {
      const ticket = t({ attention: 'stuck', supervisor })
      expect(badgeFor(ticket)?.text === 'needs attention').toBe(needsMe(ticket))
    }
  })
})

describe('captionFor', () => {
  test('busy shows starting, merging or exporting instead of the stage', () => {
    expect(captionFor(t({ column: 'ready', busy: true }))).toBe('starting…')
    expect(captionFor(t({ column: 'review', review_stage: 'merging', busy: true }))).toBe('merging…')
    expect(captionFor(t({ column: 'review', review_stage: 'agent', busy: true }))).toBe('merging…')
    expect(captionFor(t({ column: 'review', review_stage: 'exporting', busy: true }))).toBe('exporting…')
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
