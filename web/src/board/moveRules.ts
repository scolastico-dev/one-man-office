import type { Column, Project, ReviewStage, TicketSummary } from '../api/types'
import { COLUMNS } from '../api/types'

export type MoveAction = 'reorder' | 'approve' | 'withdraw' | 'stop' | 'reject'

/** What dropping a card on one column means. */
export interface Target {
  column: Column
  allowed: boolean
  action?: MoveAction
  /** The drop asks first: a comment for a rejection, a confirmation for a stop. */
  confirm?: 'reject' | 'stop'
  /** The column the card is in after the move (an approval from Review stays in Review while it finishes). */
  resultColumn?: Column
  resultStage?: ReviewStage
  /** Why the column refuses the card, in words. */
  reason?: string
}

/** What the rules need to know beyond the ticket itself. */
export interface RuleContext {
  project(id: string): Project | undefined
  /** A live agent works on one of the story's tasks. */
  storyHasLiveAgent(story: number): boolean
}

const TO_IN_PROGRESS = 'A ticket enters In Progress when its agent starts or its review is rejected.'
const TO_REVIEW = 'A ticket enters Review when its agent hands it in.'
const TO_DONE = 'Only a review waiting for you can be dragged to Done. To close a ticket, cancel it.'
const FROM_DONE = 'Done tickets stay where they are.'
const NOT_HUMAN_REVIEW = 'Only a review waiting for you, or one whose merge failed, can be rejected.'
const REVIEW_BACK = 'Approve it, or reject it back to In Progress.'
const STARTED = 'An agent has started on it.'

function busyReason(n: number): string {
  return `omo is starting work on, merging or exporting #${n}; try again in a moment.`
}

function refuse(column: Column, reason: string): Target {
  return { column, allowed: false, reason }
}

function reorder(column: Column, stage: ReviewStage): Target {
  const t: Target = { column, allowed: true, action: 'reorder', resultColumn: column }
  if (stage !== '') t.resultStage = stage
  return t
}

/** Why a ticket cannot be approved out of Planning, or `undefined` when it can. */
function approveBlock(t: TicketSummary, ctx: RuleContext): string | undefined {
  // A task without a project needs none; a story does.
  if (t.projects.length === 0 && t.type === 'story') return 'Add a project to the story first.'
  // A task inside a story uses the story's base branch.
  if (t.story === null && t.projects.some((p) => p.base_branch === '')) return 'Set a base branch first.'
  for (const tp of t.projects) {
    const p = ctx.project(tp.project)
    if (p === undefined || p.missing) return `Project ${p?.name ?? tp.project} is missing.`
    if (!p.enabled) return `Project ${p.name} is disabled.`
  }
  return undefined
}

function planningTarget(t: TicketSummary, to: Column, ctx: RuleContext): Target {
  switch (to) {
    case 'planning':
      return reorder('planning', '')
    case 'ready': {
      const why = approveBlock(t, ctx)
      if (why !== undefined) return refuse(to, why)
      return { column: to, allowed: true, action: 'approve', resultColumn: 'ready' }
    }
    case 'in_progress':
      return refuse(to, TO_IN_PROGRESS)
    case 'review':
      return refuse(to, TO_REVIEW)
    case 'done':
      return refuse(to, TO_DONE)
  }
}

function readyTarget(to: Column, live: boolean): Target {
  switch (to) {
    case 'planning':
      if (live) return refuse(to, STARTED)
      return { column: to, allowed: true, action: 'withdraw', resultColumn: 'planning' }
    case 'ready':
      return reorder('ready', '')
    case 'in_progress':
      return refuse(to, TO_IN_PROGRESS)
    case 'review':
      return refuse(to, TO_REVIEW)
    case 'done':
      return refuse(to, TO_DONE)
  }
}

function inProgressTarget(to: Column): Target {
  switch (to) {
    case 'planning':
    case 'ready':
      return { column: to, allowed: true, action: 'stop', confirm: 'stop', resultColumn: to }
    case 'in_progress':
      return reorder('in_progress', '')
    case 'review':
      return refuse(to, TO_REVIEW)
    case 'done':
      return refuse(to, TO_DONE)
  }
}

/**
 * Whether the user may reject `t`: a review waiting for them, or an approved
 * ticket stuck at merging because its merge failed (it goes back to work).
 */
export function canReject(t: TicketSummary): boolean {
  if (t.column !== 'review') return false
  return t.review_stage === 'human' || (t.review_stage === 'merging' && t.failed_step === 'merge')
}

function reviewTarget(t: TicketSummary, to: Column): Target {
  switch (to) {
    case 'planning':
    case 'ready':
      return refuse(to, REVIEW_BACK)
    case 'in_progress':
      if (!canReject(t)) return refuse(to, NOT_HUMAN_REVIEW)
      return { column: to, allowed: true, action: 'reject', confirm: 'reject', resultColumn: 'in_progress' }
    case 'review':
      return reorder('review', t.review_stage)
    case 'done':
      if (t.review_stage !== 'human') return refuse(to, TO_DONE)
      // A task without a project has nothing to merge or export: approving it is Done.
      if (t.type === 'task' && t.projects.length === 0) return { column: to, allowed: true, action: 'approve', resultColumn: 'done' }
      return {
        column: to,
        allowed: true,
        action: 'approve',
        resultColumn: 'review',
        resultStage: t.effective_when_approved === 'export' ? 'exporting' : 'merging',
      }
  }
}

const REVIEW_STAGES: readonly ReviewStage[] = ['agent', 'human', 'merging', 'exporting']

/**
 * Which columns accept a drop of `t` and what each drop does. The rules
 * copy what the server accepts; whatever they cannot be sure of is
 * refused, and the server still has the last word on every drop.
 */
export function dragTargets(t: TicketSummary, ctx: RuleContext): Record<Column, Target> {
  const out = {} as Record<Column, Target>
  const story = t.type === 'story'
  const live = t.agents.length > 0 || (story && ctx.storyHasLiveAgent(t.number))
  // A story's summary is busy while one of its tasks is.
  const busy = t.busy === true
  for (const to of COLUMNS) {
    let target: Target
    switch (t.column) {
      case 'planning':
        target = planningTarget(t, to, ctx)
        break
      case 'ready':
        target = readyTarget(to, live)
        break
      case 'in_progress':
        target = inProgressTarget(to)
        break
      case 'review':
        target = REVIEW_STAGES.includes(t.review_stage)
          ? reviewTarget(t, to)
          : refuse(to, to === t.column ? 'This ticket cannot be moved right now.' : REVIEW_BACK)
        break
      case 'done':
        target = refuse(to, FROM_DONE)
        break
      default:
        target = refuse(to, 'This ticket cannot be moved right now.')
    }
    // While omo works on a ticket nothing but a change of order is safe.
    if (busy && target.allowed && target.action !== 'reorder') target = refuse(to, busyReason(t.number))
    out[to] = target
  }
  return out
}
