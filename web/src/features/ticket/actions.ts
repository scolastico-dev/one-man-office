// The actions a ticket's panel offers. Which apply follows the ticket's state:
// approving uses the same rules the board uses for dragging, the others the
// fields the server sends. An action that applies but cannot run now says why
// and stays disabled. Later features add actions with registerTicketAction.
import { shallowReactive } from 'vue'
import type { ApiClient } from '../../api/client'
import { markMerged, retryTicket } from '../../api/endpoints'
import type { Column, FailedStep, TicketSummary } from '../../api/types'
import { canReject, type Target } from '../../board/moveRules'
import type { DialogScope } from '../../components/ui/mountDialog'
import { visibleText } from '../../components/ui/visibleText'
import { exportEffect } from './exportState'
import { withoutProject } from './fieldRules'
import type { BoardStore } from '../../store'

/** The dialogs an action can open; the panel shows them. */
export type ActionDialog = 'approve' | 'export' | 'reject' | 'cancel'

/** What an action may use besides the ticket. */
export interface ActionContext {
  /** What dropping the ticket on each column means, from the board's rules. */
  targets: Record<Column, Target>
  /** omo is starting work on, merging or exporting the ticket (for a story: one of its tasks). */
  busy: boolean
  client: ApiClient
  store: BoardStore
  /** Opens one of the panel's dialogs, which asks before anything is sent. */
  ask(dialog: ActionDialog): void
  /** Where an action shows a dialog of its own (mountDialog): over the panel, closed with it. */
  dialogs: DialogScope
}

export interface TicketAction {
  id: string
  /** The button's text; a trailing `…` means a dialog asks first. */
  label: string | ((t: TicketSummary) => string)
  tone?: 'primary' | 'danger'
  /** Whether the action belongs on the ticket in its current state. */
  applies(t: TicketSummary, ctx: ActionContext): boolean
  /** Why it cannot run right now, in words; the button is then disabled and says so. */
  unavailable?(t: TicketSummary, ctx: ActionContext): string | undefined
  /** What it does, in one sentence. */
  describe?(t: TicketSummary, ctx: ActionContext): string
  /** Show that sentence beside the button, not only to screen readers and as a tooltip (for an action whose effect is not plain from its label). */
  explain?: boolean
  /**
   * Opens its dialog, or sends the request; a returned promise rejects with
   * the server's error, and resolves with 'cancelled' when the user gave up.
   */
  run(t: TicketSummary, ctx: ActionContext): void | Promise<unknown>
}

export function busyReason(n: number): string {
  return `omo is starting work on, merging or exporting #${n}; try again in a moment.`
}

export const STEP_LABEL: Record<Exclude<FailedStep, ''>, string> = { start: 'start', agent: 'agent', merge: 'merge' }

const STEP_HINT: Record<Exclude<FailedStep, ''>, string> = {
  start: 'Tries again to start work on it: the branch, the worktree and the agent.',
  agent: 'Starts its agent again.',
  merge: 'Tries the merge again.',
}

/** What approving a review does for this ticket; `nameOf` names a project (its id by default). */
export function approvalEffect(t: TicketSummary, nameOf: (id: string) => string = (id) => id): string {
  if (withoutProject(t)) return 'It is done: a task without a project has nothing to merge or export. Its working folder stays until you remove it.'
  if (t.effective_when_approved === 'export') return exportEffect(t, nameOf)
  if (t.story !== null) return `omo merges it into the branch of story #${t.story}.`
  const into = t.projects.map((p) => (p.base_branch ? `${p.branch || 'its branch'} into ${p.base_branch}` : (p.branch || 'its branch'))).join(', ')
  return into ? `omo merges ${into} locally.` : 'omo merges it locally.'
}

/** A project's name as the board knows it, else its id. */
export const projectNames = (store: Pick<BoardStore, 'state'>) => (id: string) => visibleText(store.state.projects.get(id)?.name ?? id)

const humanReview = (t: TicketSummary) => t.column === 'review' && t.review_stage === 'human'

const approve: TicketAction = {
  id: 'approve',
  label: (t) =>
    t.column === 'planning' ? 'Approve' : withoutProject(t) ? 'Approve…' : t.effective_when_approved === 'export' ? 'Approve and export…' : 'Approve and merge…',
  tone: 'primary',
  applies: (t) => t.column === 'planning' || humanReview(t),
  unavailable: (t, ctx) => {
    const target = ctx.targets[t.column === 'planning' ? 'ready' : 'done']
    return target.allowed ? undefined : target.reason ?? 'It cannot be approved right now.'
  },
  describe: (t, ctx) => (t.column === 'planning' ? releaseEffect(t, ctx.store) : approvalEffect(t, projectNames(ctx.store))),
  run: (t, ctx) => {
    if (t.column === 'planning') return ctx.store.requestMove(t.number, 'ready', null)
    ctx.ask('approve')
  },
}

/** What approving a ticket in Planning does: a task waits in Ready For Dev until its story is In Progress. */
function releaseEffect(t: TicketSummary, store: BoardStore): string {
  const story = t.story === null ? undefined : store.state.tickets.get(t.story)?.t
  if (story !== undefined && story.column !== 'in_progress') {
    return `Gives the green light: it moves to Ready For Dev and waits there until its story #${story.number} is In Progress.`
  }
  return 'Gives the green light: it moves to Ready For Dev, where an agent starts on it as soon as nothing holds it back.'
}

const reject: TicketAction = {
  id: 'reject',
  label: 'Reject…',
  tone: 'danger',
  applies: canReject,
  unavailable: (_t, ctx) => (ctx.targets.in_progress.allowed ? undefined : ctx.targets.in_progress.reason),
  describe: (t) => `Sends it back to In Progress with your comment for its ${t.type === 'story' ? 'product manager' : 'developer'}.`,
  run: (_t, ctx) => ctx.ask('reject'),
}

const cancel: TicketAction = {
  id: 'cancel',
  label: 'Cancel ticket…',
  tone: 'danger',
  applies: (t) => t.column !== 'done',
  unavailable: (t, ctx) => (ctx.busy ? busyReason(t.number) : undefined),
  describe: (t) =>
    t.type === 'story'
      ? 'Closes it and its open tasks as cancelled; you choose what happens to the branch.'
      : withoutProject(t)
        ? 'Closes it as cancelled; its working folder stays.'
        : 'Closes it as cancelled; you choose what happens to the branch.',
  run: (_t, ctx) => ctx.ask('cancel'),
}

const merged: TicketAction = {
  id: 'mark-merged',
  label: 'Mark as merged',
  applies: (t) => (t.column === 'review' && t.review_stage === 'awaiting_merge') || (t.resolution === 'exported' && !t.export_merged),
  describe: (t) =>
    t.projects.filter((p) => p.export_url).length > 1
      ? 'Tells omo the pull requests were merged, so tickets that depend on it can start.'
      : 'Tells omo the pull request was merged, so tickets that depend on it can start.',
  run: (t, ctx) => markMerged(ctx.client, t.number),
}

const retry: TicketAction = {
  id: 'retry',
  label: (t) => `Retry ${t.failed_step ? (STEP_LABEL[t.failed_step] ?? t.failed_step) : ''}`.trim(),
  tone: 'primary',
  applies: (t) => Boolean(t.failed_step),
  unavailable: (t, ctx) => (ctx.busy ? busyReason(t.number) : undefined),
  describe: (t) => (t.failed_step ? STEP_HINT[t.failed_step] : ''),
  run: (t, ctx) => retryTicket(ctx.client, t.number),
}

const BUILT_IN: readonly TicketAction[] = [retry, approve, reject, merged, cancel]

const registered = shallowReactive<TicketAction[]>([])

/** Adds an action to every ticket panel; returns a function that removes it again. */
export function registerTicketAction(a: TicketAction): () => void {
  registered.push(a)
  return () => {
    const i = registered.indexOf(a)
    if (i >= 0) registered.splice(i, 1)
  }
}

export function labelOf(a: TicketAction, t: TicketSummary): string {
  return typeof a.label === 'string' ? a.label : a.label(t)
}

/** The actions that apply to `t` now, in the order the panel shows them. */
export function ticketActions(t: TicketSummary, ctx: ActionContext): TicketAction[] {
  return [...BUILT_IN, ...registered].filter((a) => a.applies(t, ctx))
}
