// What the fields of a ticket show and whether they can be changed now.
// These rules only explain: the server decides every change, and its own
// message is shown when it refuses one.
import type { Review, Role, TaskRelease, TicketSummary, TicketType, WaitingReason, WhenApproved } from '../../api/types'

export type FieldName =
  | 'projects'
  | 'story'
  | 'depends_on'
  | 'blocks'
  | 'supervisor'
  | 'model'
  | 'review'
  | 'when_approved'
  | 'task_release'
  | 'max_parallel'
  | 'base_branch'
  | 'branch'
  | 'dates'

export interface FieldState {
  visible: boolean
  editable: boolean
  /** Why a visible field cannot be changed now, in plain words. */
  lockedReason?: string
}

export interface FieldContext {
  ceoEnabled: boolean
}

export const DONE_REASON = 'This ticket is done; done tickets cannot be changed.'
export const BUSY_REASON = 'Locked while omo is working on this ticket. Try again in a moment.'

/** The first branch the ticket has in any of its projects, or an empty text. */
export function branchOf(t: TicketSummary): string {
  return t.projects.find((p) => p.branch !== '')?.branch ?? ''
}

export const inStory = (t: TicketSummary) => t.type === 'task' && t.story !== null

/** A task without a project: it works in a working folder, and approving it merges and exports nothing. */
export const withoutProject = (t: Pick<TicketSummary, 'type' | 'projects'>) => t.type === 'task' && t.projects.length === 0

/** The kind of ticket a default applies to: a story, a task without a story, or a task in a story. */
export type DefaultKind = 'story' | 'task' | 'story_task'

/** Which entry of `review_defaults` / `when_approved_defaults` applies to a ticket (or a form) of this type and story. */
export function defaultKind(t: { type: TicketType; story: number | null }): DefaultKind {
  if (t.type === 'story') return 'story'
  return t.story !== null ? 'story_task' : 'task'
}

const FACTS: readonly FieldName[] = ['blocks', 'branch', 'dates']

export function fieldState(t: TicketSummary, field: FieldName, ctx: FieldContext): FieldState {
  void ctx
  if (FACTS.includes(field)) return { visible: true, editable: false }
  const done = t.column === 'done'
  const visible = (() => {
    switch (field) {
      case 'story':
        return t.type === 'task'
      case 'when_approved':
        return !inStory(t) && !withoutProject(t)
      case 'task_release':
      case 'max_parallel':
        return t.type === 'story'
      case 'base_branch':
        return !inStory(t) && !withoutProject(t)
      default:
        return true
    }
  })()
  if (!visible) return { visible: false, editable: false }
  if (done) return { visible: true, editable: false, lockedReason: DONE_REASON }
  if (field === 'projects' || field === 'story' || field === 'base_branch') {
    const branch = branchOf(t)
    if (branch !== '') return { visible: true, editable: false, lockedReason: `Locked while the branch ${branch} exists.` }
    if (t.workdir !== '') return { visible: true, editable: false, lockedReason: 'Locked: work started without a project, in its working folder.' }
    if (t.busy === true) return { visible: true, editable: false, lockedReason: BUSY_REASON }
  }
  return { visible: true, editable: true }
}

/**
 * Whether a dependency can be added now (removing one stays allowed while omo works).
 * The server refuses only while omo holds a claim on the ticket itself: a story's summary is busy
 * whenever one of its tasks is, which does not stop a dependency on the story, so a story is
 * refused here only when none of its tasks explains the flag.
 */
export function addDependencyState(t: TicketSummary, tasksOf: (story: number) => TicketSummary[] = () => []): { allowed: boolean; reason?: string } {
  if (t.column === 'done') return { allowed: false, reason: DONE_REASON }
  if (t.busy === true && !(t.type === 'story' && tasksOf(t.number).some((x) => x.busy === true))) return { allowed: false, reason: BUSY_REASON }
  return { allowed: true }
}

/** Who can supervise the ticket: you, the CEO while it is switched on, the story's product manager for a task in a story. */
export function supervisorOptions(t: TicketSummary, ctx: FieldContext): Role[] {
  const out: Role[] = ['user']
  if (ctx.ceoEnabled || t.supervisor === 'ceo') out.push('ceo')
  if (inStory(t) || t.supervisor === 'pm') out.push('pm')
  return out
}

export const SUPERVISOR_LABEL: Record<string, string> = { user: 'You', ceo: 'CEO', pm: 'Product manager', developer: 'Developer', reviewer: 'Reviewer', system: 'omo' }
export const REVIEW_LABEL: Record<Review, string> = { skipped: 'Skipped', agent: 'Agent', human: 'Human', agent_then_human: 'Agent, then human' }
export const WHEN_LABEL: Record<WhenApproved, string> = { merge: 'Merge locally', export: 'Export as a pull request' }
export const RELEASE_LABEL: Record<TaskRelease, string> = { agents: 'The product manager or the CEO', user: 'You' }

/** Names of settings that the Create dialog, the ticket details and the settings pages all show. */
export const SETTING_LABEL = { taskRelease: 'Tasks are released by', maxParallel: 'Max parallel tasks' } as const

/** Shown for a story whose review does not run the full test suite. */
export const FULL_SUITE_NOTE = 'No agent runs the full test suite for this story.'

/** Whether a story with this review leaves the full test suite to nobody. */
export const lacksFullSuite = (t: TicketSummary, review: Review) => t.type === 'story' && (review === 'skipped' || review === 'human')

/** Tickets a ticket could depend on according to the rules, for the Add field. */
export function dependencyCandidates(t: TicketSummary, all: Iterable<TicketSummary>): TicketSummary[] {
  const out: TicketSummary[] = []
  for (const c of all) {
    if (c.number === t.number || c.resolution === 'cancelled') continue
    if (t.depends_on.includes(c.number)) continue
    // Depending on a ticket that depends on this one is a cycle at once.
    if (t.blocks.includes(c.number)) continue
    let scope: boolean
    if (inStory(t)) scope = c.type === 'task' && c.story === t.story
    else if (t.type === 'story') scope = c.type === 'story'
    else scope = c.type === 'story' || (c.type === 'task' && c.story === null)
    if (scope) out.push(c)
  }
  return out.sort((a, b) => a.number - b.number)
}

/** Candidates that match what was typed: a number (with or without `#`) or words of the title. */
export function matchCandidates(list: TicketSummary[], query: string): TicketSummary[] {
  const q = query.trim().toLowerCase()
  if (q === '') return list
  const digits = /^#?(\d+)$/u.exec(q)?.[1]
  const exact = digits === undefined ? [] : list.filter((c) => String(c.number) === digits)
  const rest = list.filter((c) => !exact.includes(c) && ((digits !== undefined && String(c.number).startsWith(digits)) || c.title.toLowerCase().includes(q) || `#${c.number}`.includes(q)))
  return [...exact, ...rest]
}

const WAITING_WORDS: Record<WaitingReason, (n: number) => string> = {
  not_done: (n) => `waiting for #${n}`,
  export_not_merged: () => 'pull request not merged',
  cancelled: () => 'cancelled',
}

/** Why a dependency is not satisfied, in the words the board uses; empty when it is. */
export function unsatisfiedWords(t: TicketSummary, dep: number): string {
  const w = t.waiting_for.find((x) => x.ticket === dep)
  if (w === undefined) return ''
  const words = Object.prototype.hasOwnProperty.call(WAITING_WORDS, w.reason) ? WAITING_WORDS[w.reason] : undefined
  return words === undefined ? `waiting for #${dep}` : words(dep)
}
