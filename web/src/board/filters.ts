import type { Role, TicketSummary } from '../api/types'

/** The project filter's value for tickets without a project; never a project id (ids start with a letter or digit). */
export const NO_PROJECT = '-'

export interface Filters {
  /** A project id, or NO_PROJECT. */
  project?: string
  story?: number
  supervisor?: Role
  needsMe?: boolean
  q?: string
}

/**
 * The tickets that wait for the user: an open question to the user,
 * attention on a ticket the user supervises, or a review waiting for the user.
 */
export function needsMe(t: TicketSummary): boolean {
  return t.questions_for_user > 0 || (t.attention !== '' && t.supervisor === 'user') || t.review_stage === 'human'
}

/** Lower case, composed, single spaces: what search compares. */
function normalize(s: string): string {
  return s.normalize('NFC').toLowerCase().replace(/\s+/g, ' ').trim()
}

/**
 * Whether the ticket's number or title contains the text. `#12` and `12`
 * both find ticket 12; case and extra spaces do not matter.
 */
export function matchesText(t: TicketSummary, q: string): boolean {
  const text = normalize(q)
  if (text === '') return true
  const m = /^#?(\d+)$/.exec(text)
  if (m !== null && Number(m[1]) === t.number) return true
  return normalize(t.title).includes(text)
}

/** Whether a card passes every filter that is set; `serverHits` are tickets the server found for the text. */
export function matches(t: TicketSummary, f: Filters, serverHits?: ReadonlySet<number>): boolean {
  if (f.project === NO_PROJECT) {
    if (t.projects.length > 0) return false
  } else if (f.project !== undefined && f.project !== '' && !t.projects.some((p) => p.project === f.project)) return false
  if (f.story !== undefined && t.number !== f.story && t.story !== f.story) return false
  if (f.supervisor !== undefined && t.supervisor !== f.supervisor) return false
  if (f.needsMe === true && !needsMe(t)) return false
  if (f.q !== undefined && normalize(f.q) !== '' && !matchesText(t, f.q) && serverHits?.has(t.number) !== true) return false
  return true
}
