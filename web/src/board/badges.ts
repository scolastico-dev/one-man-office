import type { StartBlocked, TicketSummary, WaitingReason } from '../api/types'
import { visibleText } from '../components/ui/visibleText'
import type { Badge } from '../features/board/model'
import type { SlotView } from './slots'

const STEP_TEXT: Record<string, string> = {
  start: 'starting work failed',
  agent: 'the agent stopped working',
  merge: 'merge blocked',
}

const START_BLOCKED: Record<Exclude<StartBlocked, '' | 'slot'>, string> = {
  paused: 'paused',
  no_model: 'no usable model',
  usage: 'waiting for usage',
}

const WAITING_SUFFIX: Record<WaitingReason, string> = {
  not_done: '',
  export_not_merged: ' · PR not merged',
  cancelled: ' · cancelled',
}

function has<T extends object>(table: T, key: string): key is Extract<keyof T, string> {
  return Object.prototype.hasOwnProperty.call(table, key)
}

/** A failed step in the card's words: "merge blocked"; an unknown step as it is. */
export function failedStepText(step: string): string {
  return has(STEP_TEXT, step) ? (STEP_TEXT[step] ?? step) : step
}

/**
 * Why a ticket waits for a slot: its story's own limit of parallel tasks
 * when that is reached, else the limit of its kind, with the numbers when
 * the slots are known.
 */
function slotBadge(t: TicketSummary, slots: SlotView | null | undefined): Badge {
  const max = t.story === null ? undefined : slots?.maxParallel.get(t.story)
  const inStory = t.story === null ? 0 : (slots?.perStory.get(t.story) ?? 0)
  if (t.type === 'task' && t.story !== null && max !== undefined && inStory >= max) {
    return { tone: 'muted', text: 'story limit reached', title: `Waiting for a free place in story #${t.story} (${inStory} of ${max} tasks in work)` }
  }
  const kind = t.type === 'story' ? 'story' : 'task'
  const load = slots ? (kind === 'story' ? slots.stories : slots.tasks) : null
  const title = load ? `Waiting for a free ${kind} slot (${load.in_work} of ${load.limit} in work)` : `Waiting for a free ${kind} slot`
  return { tone: 'muted', text: `waiting for a ${kind} slot`, title }
}

/** Who a ticket with attention is waiting for, in words ("the CEO"); empty when it is the user, whom "needs attention" is about. */
export function attentionOwner(t: TicketSummary): string {
  return t.supervisor === 'ceo' ? 'the CEO' : t.supervisor === 'pm' ? 'the product manager' : ''
}

/**
 * What the card and the panel call a ticket that needs a person: "needs
 * attention" for a ticket the user supervises, which the bell and Needs me
 * count too; for the ticket of another supervisor, who is told and may retry
 * it, "needs the CEO" and no alarm, so the cards and the counters name the
 * same tickets as the user's own.
 */
export function attentionWords(t: TicketSummary): { text: string; tone: 'danger' | 'warn' } {
  const owner = attentionOwner(t)
  return owner === '' ? { text: 'needs attention', tone: 'danger' } : { text: `needs ${owner}`, tone: 'warn' }
}

/**
 * The one badge a card shows: the most urgent of needs attention (also when
 * a step failed), a question for you, your review, waiting for a ticket, and
 * the reason the server gives why work on the ticket has not started (new
 * work in Ready, or work that goes on in In Progress and Review, such as
 * waiting for usage), in whatever column the ticket is. slots, the board's
 * tasks and stories in work, puts numbers into a wait for a slot.
 */
export function badgeFor(t: TicketSummary, slots?: SlotView | null): Badge | null {
  if (t.failed_step) {
    const step = failedStepText(t.failed_step)
    return { ...attentionWords(t), title: t.attention ? `${step}: ${t.attention}` : step }
  }
  if (t.attention) return { ...attentionWords(t), title: t.attention }
  if (t.questions_for_user > 0) {
    const n = t.questions_for_user
    return { tone: 'warn', text: 'question for you', title: `${n} open question${n === 1 ? '' : 's'} for you` }
  }
  if (t.column === 'review' && t.review_stage === 'human') return { tone: 'accent', text: 'your review' }
  const w = t.waiting_for[0]
  if (w !== undefined) {
    const suffix = has(WAITING_SUFFIX, w.reason) ? WAITING_SUFFIX[w.reason] : ''
    return { tone: 'muted', text: `waiting for #${w.ticket}${suffix}` }
  }
  if (t.start_blocked === 'usage') {
    const title = t.model ? `${visibleText(t.model)} is near its usage limit` : 'the models of its role are near their usage limits'
    return { tone: 'muted', text: START_BLOCKED.usage, title }
  }
  if (t.start_blocked === 'slot') return slotBadge(t, slots)
  if (t.start_blocked && has(START_BLOCKED, t.start_blocked)) return { tone: 'muted', text: START_BLOCKED[t.start_blocked] }
  return null
}

/** The short state text next to the badge. */
export function captionFor(t: TicketSummary): string | undefined {
  if (t.busy) return t.column !== 'review' ? 'starting…' : t.review_stage === 'exporting' ? 'exporting…' : 'merging…'
  if (t.column === 'review') {
    if (t.review_stage === 'agent') return 'agent review'
    if (t.review_stage === 'merging') return 'merging…'
    if (t.review_stage === 'exporting') return 'exporting…'
  }
  if (t.resolution === 'cancelled') return 'cancelled'
  if (t.resolution === 'exported') return 'exported'
  return undefined
}
