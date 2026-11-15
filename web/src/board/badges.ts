import type { StartBlocked, TicketSummary, WaitingReason } from '../api/types'
import type { Badge } from '../features/board/model'

const STEP_TEXT: Record<string, string> = {
  start: 'starting work failed',
  agent: 'the agent keeps failing',
  merge: 'merge blocked',
}

const START_BLOCKED: Record<Exclude<StartBlocked, ''>, string> = {
  slot: 'waiting for a slot',
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

/**
 * The one badge a card shows: the most urgent of needs attention (also when
 * a step failed), a question for you, your review, waiting for a ticket, and
 * the reason the server gives for a Ready ticket that has not started.
 */
export function badgeFor(t: TicketSummary): Badge | null {
  if (t.failed_step) {
    const step = has(STEP_TEXT, t.failed_step) ? STEP_TEXT[t.failed_step] : t.failed_step
    return { tone: 'danger', text: 'needs attention', title: t.attention ? `${step}: ${t.attention}` : step }
  }
  if (t.attention) return { tone: 'danger', text: 'needs attention', title: t.attention }
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
  if (t.start_blocked && has(START_BLOCKED, t.start_blocked)) return { tone: 'muted', text: START_BLOCKED[t.start_blocked] }
  return null
}

/** The short state text next to the badge. */
export function captionFor(t: TicketSummary): string | undefined {
  if (t.busy) return t.column === 'review' ? 'merging…' : 'starting…'
  if (t.column === 'review') {
    if (t.review_stage === 'agent') return 'agent review'
    if (t.review_stage === 'merging') return 'merging…'
    if (t.review_stage === 'exporting') return 'exporting…'
  }
  if (t.resolution === 'cancelled') return 'cancelled'
  if (t.resolution === 'exported') return 'exported'
  return undefined
}
