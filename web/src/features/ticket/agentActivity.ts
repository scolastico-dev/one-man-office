// What the ticket's live agents are doing, in words, from their session
// states: an agent that runs is working, one that starts is starting, and
// one parked in `omo ai wait` waits — for an answer while the ticket has an
// open question, else for its turn (a developer during the review). A
// parked agent is never said to be working.
import type { AgentRef, TicketSummary } from '../../api/types'
import { ROLE_LABELS } from '../board/model'

const who = (a: AgentRef) => (a.purpose === 'export' ? 'export job' : (ROLE_LABELS[a.role] ?? a.role))

function sentence(a: AgentRef, t: TicketSummary): string {
  const name = `The ${who(a)}`
  if (a.state === 'running') return `${name} is working on this ticket.`
  if (a.state === 'starting') return `${name} is starting on this ticket.`
  return t.open_questions > 0 ? `${name} waits for an answer to its question.` : `${name} waits for its turn.`
}

const ORDER: Record<string, number> = { running: 0, starting: 1, waiting: 2 }

/** One sentence per live agent (working ones first, each said once); null when no agent is live. */
export function agentActivity(t: TicketSummary): string | null {
  const live = t.agents.filter((a) => a.state !== 'ended').sort((a, b) => (ORDER[a.state] ?? 3) - (ORDER[b.state] ?? 3))
  if (live.length === 0) return null
  return [...new Set(live.map((a) => sentence(a, t)))].join(' ')
}
