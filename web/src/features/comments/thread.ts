// The comment thread of a ticket, built from the ticket's own event log: one
// ordered source, so nothing has to be merged by timestamps. Pure functions:
// `mergeEvents` keeps each event once in id order, `buildThread` turns the
// events into entries (comments and greyed status lines).
import type {
  Actor,
  AgentData,
  ApiEvent,
  Column,
  Comment,
  CommentCreatedData,
  DependencyData,
  DescriptionChangedData,
  QuestionAnsweredData,
  ReviewVerdictData,
  TicketCreatedData,
  TicketMovedData,
  TicketRetriedData,
} from '../../api/types'
import { visibleText } from '../../components/ui/visibleText'
import { COLUMN_TITLES, ROLE_LABELS } from '../board/model'

/** How much of a comment's body is shown before "Show all", in UTF-16 code units. */
export const BODY_PREVIEW = 16 * 1024

/**
 * The start of a long body to render first: at most `limit` code units, cut
 * at the last line end in the second half of that room (else at the limit,
 * never inside a surrogate pair). A short body is returned whole.
 */
export function bodyPreview(body: string, limit = BODY_PREVIEW): { text: string; cut: boolean } {
  if (body.length <= limit) return { text: body, cut: false }
  let end = limit
  const last = body.charCodeAt(end - 1)
  if (last >= 0xd800 && last <= 0xdbff) end--
  const nl = body.lastIndexOf('\n', end - 1)
  if (nl >= limit / 2) end = nl
  return { text: body.slice(0, end), cut: true }
}

/** The event kinds the thread shows; the server's thread route sends only these. */
export const THREAD_KINDS: readonly string[] = [
  'ticket.created',
  'ticket.moved',
  'comment.created',
  'question.answered',
  'review.verdict',
  'description.changed',
  'dependency.added',
  'dependency.removed',
  'agent.created',
  'agent.ended',
  'ticket.retried',
]

export function isThreadKind(kind: string): boolean {
  return THREAD_KINDS.includes(kind)
}

export type ThreadItem =
  | {
      kind: 'comment'
      comment: Comment
      eventId: number
      /** The question has an answer; false for comments that are no question. */
      answered: boolean
      /** The comment that answered the question. */
      answeredBy: number | null
    }
  | { kind: 'status'; eventId: number; at: string; actor: Actor; text: string }

/** Both lists joined by event id: each id once, ascending; a later copy of an id wins. */
export function mergeEvents(known: readonly ApiEvent[], incoming: readonly ApiEvent[]): ApiEvent[] {
  const byId = new Map<number, ApiEvent>()
  for (const ev of known) byId.set(ev.id, ev)
  for (const ev of incoming) byId.set(ev.id, ev)
  return [...byId.values()].sort((a, b) => a.id - b.id)
}

/** Who acted, in words: `you`, `omo`, `developer calm-badger`. */
export function actorLabel(a: Actor): string {
  if (a.role === 'user') return 'you'
  if (a.role === 'system') return 'omo'
  const role = ROLE_LABELS[a.role] ?? visibleText(String(a.role))
  return a.name ? `${role} ${visibleText(a.name)}` : role
}

const up = (s: string) => s.charAt(0).toUpperCase() + s.slice(1)

const column = (c: Column) => COLUMN_TITLES[c] ?? visibleText(String(c))

const MOVE_WORDS: Record<string, string> = {
  approve: 'approved',
  withdraw: 'withdrawn',
  begin_work: 'work began',
  stop: 'stopped',
  hand_in: 'handed in',
  verdict: 'review verdict',
  reject: 'rejected',
  merged: 'merged',
  conflict: 'merge conflict',
  exported: 'exported',
  export_failed: 'export failed',
  cancel: 'cancelled',
}

/** Moves that show although the ticket stayed in its column: the outcomes inside Review. */
const REVIEW_OUTCOMES = ['verdict', 'merged', 'conflict', 'exported', 'export_failed', 'reject']

function moveText(ev: ApiEvent, d: TicketMovedData): string | null {
  if (d.from === d.to && !REVIEW_OUTCOMES.includes(d.action)) return null
  const why = MOVE_WORDS[d.action] ?? visibleText(String(d.action).replace(/_/g, ' '))
  const n = d.ticket?.number ?? ev.ticket
  if (d.from === d.to) return `${up(actorLabel(ev.actor))} moved #${n} within ${column(d.to)} (${why})`
  return `${up(actorLabel(ev.actor))} moved #${n} from ${column(d.from)} to ${column(d.to)} (${why})`
}

function agentLabel(d: AgentData): string {
  const a = d.agent
  return a ? actorLabel({ role: a.role, name: a.name }) : 'an agent'
}

/** The words of a status line, or null for an event the thread does not show as one. */
export function statusText(ev: ApiEvent): string | null {
  const who = up(actorLabel(ev.actor))
  switch (ev.kind) {
    case 'ticket.created': {
      const d = ev.data as TicketCreatedData
      return `${who} created #${d.ticket?.number ?? ev.ticket} in ${column(d.ticket?.column ?? 'planning')}`
    }
    case 'ticket.moved':
      return moveText(ev, ev.data as TicketMovedData)
    case 'review.verdict': {
      const d = ev.data as ReviewVerdictData
      const word = d.verdict === 'approve' ? 'approved' : d.verdict === 'reject' ? 'rejected' : visibleText(String(d.verdict))
      return `Review ${word} by ${actorLabel(ev.actor)} (round ${d.round})`
    }
    case 'description.changed': {
      const d = ev.data as DescriptionChangedData
      return `${who} edited the description (version ${d.version?.number})`
    }
    case 'dependency.added': {
      const d = ev.data as DependencyData
      return `#${d.ticket} now depends on #${d.depends_on}`
    }
    case 'dependency.removed': {
      const d = ev.data as DependencyData
      return `#${d.ticket} no longer depends on #${d.depends_on}`
    }
    case 'agent.created':
      return `${up(agentLabel(ev.data as AgentData))} started`
    case 'agent.ended': {
      const d = ev.data as AgentData
      const reason = d.agent?.end_reason ? `: ${visibleText(d.agent.end_reason)}` : ''
      return `${up(agentLabel(d))} ended${reason}`
    }
    case 'ticket.retried': {
      const d = ev.data as TicketRetriedData
      const step: Record<string, string> = { start: 'the start', agent: 'the agent', merge: 'the merge' }
      return `${who} retried ${step[d.step] ?? 'the failed step'}`
    }
    default:
      return null
  }
}

/** The entries of the thread, oldest first. A question is answered once `question.answered` or the comment itself says so. */
export function buildThread(events: readonly ApiEvent[]): ThreadItem[] {
  const answers = new Map<number, number>()
  for (const ev of events) {
    if (ev.kind !== 'question.answered') continue
    const d = ev.data as QuestionAnsweredData
    answers.set(d.question, d.answer)
  }
  const items: ThreadItem[] = []
  for (const ev of events) {
    if (ev.kind === 'comment.created') {
      const c = (ev.data as CommentCreatedData).comment
      const by = c.answered_by ?? answers.get(c.id) ?? null
      items.push({ kind: 'comment', comment: c, eventId: ev.id, answered: by !== null, answeredBy: by })
      continue
    }
    if (ev.kind === 'question.answered') continue
    const text = statusText(ev)
    if (text !== null) items.push({ kind: 'status', eventId: ev.id, at: ev.at, actor: ev.actor, text })
  }
  return items
}

/** Whether a comment is a question that is addressed to someone. */
export function isQuestion(c: Comment): boolean {
  return String(c.question_to) !== ''
}
