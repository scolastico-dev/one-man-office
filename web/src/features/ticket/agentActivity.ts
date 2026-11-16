// What the ticket's live agents are doing, in words, from their session
// states: an agent that runs is working, one that starts is starting, and
// one parked in `omo ai wait` waits — for an answer while the ticket has an
// open question, else it is kept for questions or waits for its turn. A
// parked agent is never said to be working.
//
// omo keeps agents while their ticket's work goes on: the developer of a
// task (the product manager of a story) from In Progress until the ticket
// is done, parked while it is in review; a reviewer after its verdict until
// the next review round of its project begins or the ticket is done. A
// mention wakes a kept agent, which answers in the thread.
import type { AgentRef, TicketSummary } from '../../api/types'
import { ROLE_LABELS } from '../board/model'

const who = (a: AgentRef) => (a.purpose === 'export' ? 'export job' : (ROLE_LABELS[a.role] ?? a.role))

/** Whether a parked agent is kept for questions: a reviewer after its verdict, the working agent while its ticket is in review. */
export function keptAgent(a: Pick<AgentRef, 'role' | 'state' | 'purpose'>, t: Pick<TicketSummary, 'column'>): boolean {
  if (a.state !== 'waiting' || a.purpose === 'export') return false
  if (a.role === 'reviewer') return true
  return (a.role === 'developer' || a.role === 'pm') && t.column === 'review'
}

function sentence(a: AgentRef, t: TicketSummary): string {
  const name = `The ${who(a)}`
  if (a.state === 'running') return `${name} is working on this ticket.`
  if (a.state === 'starting') return `${name} is starting on this ticket.`
  if (t.open_questions > 0) return `${name} waits for an answer to its question.`
  if (keptAgent(a, t)) {
    return a.role === 'reviewer'
      ? `${name} has given its verdict and can answer questions about its review until the next review round.`
      : `${name} is parked while the ticket is reviewed and can answer questions.`
  }
  return `${name} waits for its turn.`
}

const ORDER: Record<string, number> = { running: 0, starting: 1, waiting: 2 }

/** One sentence per live agent (working ones first, each said once); null when no agent is live. */
export function agentActivity(t: TicketSummary): string | null {
  const live = t.agents.filter((a) => a.state !== 'ended').sort((a, b) => (ORDER[a.state] ?? 3) - (ORDER[b.state] ?? 3))
  if (live.length === 0) return null
  return [...new Set(live.map((a) => sentence(a, t)))].join(' ')
}
