// Which terminal a websocket path names, and the names a terminal's own window
// is known by: its address, its window name and the key the windows of the
// app use to tell each other about it.
import { agentTerminalPath, userTerminalPath } from '../../api/endpoints'
import { terminalWindowLocation, type TerminalWindowKind } from '../../locations'

export type { TerminalWindowKind }

/** An agent's terminal (by its terminal id) or a user shell (by its id). */
export interface TerminalTarget {
  kind: TerminalWindowKind
  id: string
}

/**
 * The remembered size of a terminal window is kept per kind: the CEO, the
 * other agents and the user's shells.
 */
export type WindowSizeKind = 'ceo' | 'agent' | 'user'

const AGENT = /^\/api\/agents\/([^/]+)\/terminal$/
const USER = /^\/api\/terminals\/([^/]+)\/socket$/

function decoded(s: string): string | null {
  try {
    return decodeURIComponent(s)
  } catch {
    return null
  }
}

/** The terminal behind a websocket path from `agentTerminalPath` or `userTerminalPath`; null for any other path. */
export function targetOfSrc(src: string): TerminalTarget | null {
  const agent = AGENT.exec(src)
  if (agent) {
    const id = decoded(agent[1]!)
    return id ? { kind: 'agent', id } : null
  }
  const user = USER.exec(src)
  if (user) {
    const id = decoded(user[1]!)
    return id ? { kind: 'user', id } : null
  }
  return null
}

/** The websocket path of a terminal. */
export function srcOf(t: TerminalTarget): string {
  return t.kind === 'agent' ? agentTerminalPath(t.id) : userTerminalPath(t.id)
}

/** The key the app's windows use for a terminal. */
export function keyOf(t: TerminalTarget): string {
  return `${t.kind}:${t.id}`
}

/** The window name of a terminal's own window: the same for the same terminal, so a second click finds it. */
export function windowNameOf(t: TerminalTarget): string {
  return `omo-terminal-${t.kind}-${t.id.replace(/[^0-9A-Za-z_-]/g, '_')}`
}

/** The address of a terminal's own window (path only). */
export function windowPathOf(t: TerminalTarget): string {
  return terminalWindowLocation(t.kind, t.id).path
}
