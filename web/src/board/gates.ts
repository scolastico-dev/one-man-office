// What a plugin gate of a ticket means for the board: who holds the move,
// in words, and which column the held move goes to. Pure; the wire types are
// in api/types.
import type { Column, TicketGate, TicketGates, TicketSummary } from '../api/types'
import { visibleText } from '../components/ui/visibleText'

/** The plugins that hold the move (their gate hook asked to wait), in the server's order. */
export function heldGates(g: TicketGates | null): TicketGate[] {
  return g === null ? [] : g.gates.filter((x) => x.state === 'held')
}

/** A gate in words for a badge: `held` is false while plugins have not answered yet. */
export interface HoldSummary {
  held: boolean
  text: string
  title: string
}

/**
 * The words for a ticket's gate, or null when it has none. A hold names the
 * first plugin (and how many more), the title has every plugin's reason;
 * a gate nobody holds but some plugin has not answered is a plain wait.
 * Everything a plugin wrote goes through `visibleText`.
 */
export function holdSummary(g: TicketGates | null): HoldSummary | null {
  if (g === null) return null
  const held = heldGates(g)
  const [first, ...rest] = held
  if (first !== undefined) {
    const more = rest.length === 0 ? '' : ` and ${rest.length} more`
    return {
      held: true,
      text: `held by ${visibleText(first.plugin)}${more}`,
      title: held.map((x) => `${visibleText(x.plugin)}: ${visibleText(x.reason)}`).join('\n'),
    }
  }
  const pending = g.gates.filter((x) => x.state === 'pending')
  const [one] = pending
  if (one === undefined) return null
  const names = pending.map((x) => visibleText(x.plugin))
  return { held: false, text: 'waiting for a plugin', title: `Waiting for ${names.join(', ')} to answer` }
}

/** The column ticket `t`'s gated move goes to, or null when no move waits for plugins from where `t` is. */
export function gateTarget(t: TicketSummary): Column | null {
  const g = t.gate
  return g !== null && g.from === t.column ? g.to : null
}

/**
 * One line for a card's notice: who holds the move and why (the first
 * plugin's reason when several hold), or whom the board still waits for.
 */
export function holdNotice(g: TicketGates | null): string | null {
  const s = holdSummary(g)
  if (s === null || g === null) return null
  const [first] = heldGates(g)
  if (first !== undefined) return `${s.text}: ${visibleText(first.reason)}`
  return s.title.replace(/^Waiting/, 'waiting')
}

/** Whether every plugin of a gate gave its go-ahead: nothing holds the move, and the board only waits for a slot or the engine. */
export function allPassed(g: TicketGates | null): boolean {
  return g !== null && g.gates.length > 0 && g.gates.every((x) => x.state === 'passed')
}
