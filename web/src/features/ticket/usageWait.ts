// Why a ticket that waits for usage waits, in one sentence: which model is
// full, until when, and what omo does next. From the usage state (useUsage)
// and the ticket's own model; without a reading, what the wait itself says.
import type { ModelUsage, TicketSummary, UsageState } from '../../api/types'
import { visibleText } from '../../components/ui/visibleText'

const DAY = 24 * 60 * 60 * 1000
const WEEK = 7 * DAY

/** "until 23:40" today, "until Mon 09:15" within the week, "until 30 Nov 2026" later; empty for no time or a time passed. */
export function untilWords(at: string | null, now: number): string {
  if (at === null) return ''
  const t = new Date(at)
  if (Number.isNaN(t.getTime()) || t.getTime() <= now) return ''
  const clock = t.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
  if (t.toDateString() === new Date(now).toDateString()) return `until ${clock}`
  if (t.getTime() - now < WEEK) return `until ${t.toLocaleDateString(undefined, { weekday: 'short' })} ${clock}`
  return `until ${t.toLocaleDateString(undefined, { dateStyle: 'medium' })}`
}

const full = (m: ModelUsage) => m.state === 'soft' || m.state === 'hard'

/** "claude/opus is at its usage limit until 23:40" */
function howFull(m: ModelUsage, now: number): string {
  const until = untilWords(m.resets_at, now)
  return `${visibleText(m.model)} is ${m.state === 'hard' ? 'at' : 'near'} its usage limit${until ? ` ${until}` : ''}`
}

/** "a, b and c" */
function list(parts: string[]): string {
  return parts.length < 2 ? (parts[0] ?? '') : `${parts.slice(0, -1).join(', ')} and ${parts[parts.length - 1]}`
}

/** The sentence for a ticket that waits for usage; empty for any other ticket. */
export function usageWaitText(t: TicketSummary, usage: UsageState | null, now: number = Date.now()): string {
  if (t.start_blocked !== 'usage') return ''
  if (t.model !== '') {
    const own = usage?.models.find((m) => m.model === t.model)
    const what = own && full(own) ? howFull(own, now) : `${visibleText(t.model)} is near its usage limit`
    const next = own?.state === 'hard' ? 'when the model has room again' : 'when the model has room again, or now with Start anyway'
    return `${what}; omo starts this ticket ${next}.`
  }
  const over = (usage?.models ?? []).filter(full)
  if (over.length === 0) return 'The models of its role are near their usage limits; omo starts this ticket when one has room.'
  return `${list(over.map((m) => howFull(m, now)))}; omo starts this ticket when a model of its role has room.`
}
