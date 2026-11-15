/// <reference types="node" />
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, test } from 'vitest'
import { COLUMNS, type Column, type Project, type ReviewStage, type TicketSummary } from '../api/types'
import { ticket } from '../features/board/testing'
import { dragTargets, type RuleContext } from './moveRules'

interface GoldenOutcome {
  ok: boolean
  action?: string
  column?: string
  stage?: string
  needs_comment?: boolean
  confirm?: boolean
  code?: string
}
interface GoldenCase {
  name: string
  ticket: {
    type: 'story' | 'task'
    in_story: boolean
    column: Column
    review_stage: ReviewStage
    live_agent: boolean
    projects: number
    base_branch: string
    project_state: 'ok' | 'missing' | 'disabled'
    when_approved: 'merge' | 'export'
    resolution?: TicketSummary['resolution']
    busy?: boolean
    failed_step?: TicketSummary['failed_step']
  }
  targets: Record<Column, GoldenOutcome>
}

// Read from the repository at test time, so a regenerated file fails this test until the rules follow.
const raw = readFileSync(resolve(import.meta.dirname, '../../../internal/board/testdata/user_drag_targets.json'), 'utf8')
const golden = (JSON.parse(raw) as { cases: GoldenCase[] }).cases

function project(state: 'ok' | 'missing' | 'disabled'): Project {
  return { id: 'api', name: 'api', path: '/src/api', enabled: state !== 'disabled', missing: state === 'missing', root: '', story_merge: 'merge' }
}

const noContext: RuleContext = { project: () => project('ok'), storyHasLiveAgent: () => false }

/** Builds the summary and context a case describes, the way the store would hold them. */
function build(c: GoldenCase): { t: TicketSummary; ctx: RuleContext } {
  const g = c.ticket
  const story = g.type === 'story'
  const t = ticket({
    number: 7,
    type: g.type,
    story: g.in_story ? 3 : null,
    column: g.column,
    review_stage: g.review_stage,
    resolution: g.resolution ?? '',
    effective_when_approved: g.when_approved,
    projects: Array.from({ length: g.projects }, () => ({ project: 'api', base_branch: g.base_branch, branch: '', worktree: '' })),
    // A live session on a story's task is not in the story's own list.
    agents: g.live_agent && !story ? [{ session: 's', role: 'developer', name: 'dev', state: 'running' }] : [],
    ...(g.busy !== undefined ? { busy: g.busy } : {}),
    ...(g.failed_step !== undefined ? { failed_step: g.failed_step } : {}),
  })
  // The golden file does not say whether a story's session is its own or its task's; both end up as "live".
  const ctx: RuleContext = {
    project: (id) => (id === 'api' ? project(g.project_state) : undefined),
    storyHasLiveAgent: () => story && g.live_agent,
  }
  return { t, ctx }
}

describe('dragTargets', () => {
  test('matches every golden case', () => {
    expect(golden.length).toBeGreaterThanOrEqual(48)
    for (const c of golden) {
      const { t, ctx } = build(c)
      const got = dragTargets(t, ctx)
      for (const col of COLUMNS) {
        const want = c.targets[col]
        const target = got[col]
        const label = `${c.name} -> ${col}`
        expect({ label, allowed: target.allowed }).toEqual({ label, allowed: want.ok })
        if (!want.ok) continue
        expect({ label, action: target.action, column: target.resultColumn, stage: target.resultStage ?? '' }).toEqual({
          label,
          action: want.action,
          column: want.column,
          stage: want.stage ?? '',
        })
        expect({ label, reject: target.confirm === 'reject' }).toEqual({ label, reject: want.needs_comment === true })
        expect({ label, stop: target.confirm === 'stop' }).toEqual({ label, stop: want.confirm === true })
      }
    }
  })

  test('every refused column explains itself and every target names its column', () => {
    for (const c of golden) {
      const { t, ctx } = build(c)
      for (const col of COLUMNS) {
        const target = dragTargets(t, ctx)[col]
        expect(target.column).toBe(col)
        if (!target.allowed) expect(target.reason).toBeTruthy()
      }
    }
  })

  test('a Done ticket refuses every column, its own included', () => {
    for (const resolution of ['completed', 'cancelled', 'exported'] as const) {
      const targets = dragTargets(ticket({ column: 'done', resolution }), noContext)
      for (const col of COLUMNS) expect(targets[col]).toMatchObject({ allowed: false, reason: 'Done tickets stay where they are.' })
    }
  })

  test('Review → In Progress asks for a reject comment; In Progress → Planning asks for confirmation', () => {
    const review = dragTargets(ticket({ column: 'review', review_stage: 'human' }), noContext)
    expect(review.in_progress).toMatchObject({ allowed: true, action: 'reject', confirm: 'reject' })
    const working = dragTargets(ticket({ column: 'in_progress' }), noContext)
    expect(working.planning).toMatchObject({ allowed: true, action: 'stop', confirm: 'stop' })
    expect(working.ready).toMatchObject({ allowed: true, action: 'stop', confirm: 'stop' })
  })

  test('approving out of Review shows the stage the ticket will be in', () => {
    const merge = dragTargets(ticket({ column: 'review', review_stage: 'human' }), noContext)
    expect(merge.done).toMatchObject({ action: 'approve', resultColumn: 'review', resultStage: 'merging' })
    const exp = dragTargets(ticket({ column: 'review', review_stage: 'human', effective_when_approved: 'export' }), noContext)
    expect(exp.done).toMatchObject({ resultStage: 'exporting' })
  })

  test('refusal reasons are the ones the board shows', () => {
    const planning = dragTargets(ticket({ column: 'planning', type: 'story' }), noContext)
    expect(planning.ready.reason).toBe('Add a project to the story first.')
    expect(planning.in_progress.reason).toBe('A ticket enters In Progress when its agent starts or its review is rejected.')
    expect(planning.review.reason).toBe('A ticket enters Review when its agent hands it in.')
    expect(planning.done.reason).toBe('Only a review waiting for you can be dragged to Done. To close a ticket, cancel it.')
    const proj = [{ project: 'api', base_branch: '', branch: '', worktree: '' }]
    expect(dragTargets(ticket({ projects: proj }), noContext).ready.reason).toBe('Set a base branch first.')
    const withBranch = [{ project: 'api', base_branch: 'main', branch: '', worktree: '' }]
    const gone: RuleContext = { ...noContext, project: () => project('missing') }
    expect(dragTargets(ticket({ projects: withBranch }), gone).ready.reason).toBe('Project api is missing.')
    const off: RuleContext = { ...noContext, project: () => project('disabled') }
    expect(dragTargets(ticket({ projects: withBranch }), off).ready.reason).toBe('Project api is disabled.')
    const agentReview = dragTargets(ticket({ column: 'review', review_stage: 'agent' }), noContext)
    expect(agentReview.in_progress.reason).toBe('Only a review waiting for you can be rejected.')
    expect(agentReview.ready.reason).toBe('Approve it, or reject it back to In Progress.')
    const started = dragTargets(ticket({ column: 'ready', agents: [{ session: 's', role: 'developer', name: 'd', state: 'running' }] }), noContext)
    expect(started.planning.reason).toBe('An agent has started on it.')
  })

  test('busy refuses withdraw and stop with the busy reason (story: also a busy task); failed_step refuses nothing; absent fields refuse nothing', () => {
    const busyReason = 'omo is starting work on or merging #1; try again in a moment.'
    const ready = dragTargets(ticket({ column: 'ready', busy: true }), noContext)
    expect(ready.planning).toMatchObject({ allowed: false, reason: busyReason })
    expect(ready.ready.allowed).toBe(true)
    const running = dragTargets(ticket({ column: 'in_progress', busy: true }), noContext)
    expect(running.planning).toMatchObject({ allowed: false, reason: busyReason })
    expect(running.ready.allowed).toBe(false)
    // A story is busy while one of its tasks is; the server says so in the story's own summary.
    expect(dragTargets(ticket({ type: 'story', column: 'in_progress', busy: true }), noContext).planning).toMatchObject({ allowed: false, reason: busyReason })
    expect(dragTargets(ticket({ type: 'story', column: 'in_progress' }), noContext).planning.allowed).toBe(true)
    const failed = dragTargets(ticket({ column: 'in_progress', failed_step: 'agent' }), noContext)
    expect(failed.planning.allowed).toBe(true)
    expect(dragTargets(ticket({ column: 'ready', failed_step: 'start' }), noContext).planning.allowed).toBe(true)
    expect(dragTargets(ticket({ column: 'ready' }), noContext).planning.allowed).toBe(true)
  })

  test('a ticket the cases do not cover is refused rather than offered', () => {
    const stage = dragTargets(ticket({ column: 'review', review_stage: '' }), noContext)
    for (const col of COLUMNS) expect(stage[col].allowed).toBe(false)
    const odd = dragTargets(ticket({ column: 'archive' as Column }), noContext)
    for (const col of COLUMNS) expect(odd[col].allowed).toBe(false)
    // Busy while waiting for the user's review: only a change of order is offered.
    const busyHuman = dragTargets(ticket({ column: 'review', review_stage: 'human', busy: true }), noContext)
    expect(busyHuman.done.allowed).toBe(false)
    expect(busyHuman.in_progress.allowed).toBe(false)
    expect(busyHuman.review.allowed).toBe(true)
    // A project the client does not know is not approved.
    const unknown = dragTargets(ticket({ projects: [{ project: 'x', base_branch: 'main', branch: '', worktree: '' }] }), { ...noContext, project: () => undefined })
    expect(unknown.ready.allowed).toBe(false)
  })
})
