import { readdirSync, readFileSync } from 'node:fs'
import { expect } from '@playwright/test'
import { AGENT_MARK, type AgentRole, type Omo } from './omo'

/** The fields of a session the tests read (the server sends more). */
export interface AgentSession {
  id: string
  role: AgentRole
  ticket: number | null
  name: string
  model: string
  state: 'starting' | 'running' | 'parked' | 'ended' | string
  workdir: string
  end_reason: string
  terminal_id?: string
}

/**
 * Joins the lines of a fake agent script (one op per line, fields separated by
 * `|`, `#` starts a comment; placeholders `$TICKET`, `$STORY`, `$PROJECT`,
 * `$NAME`). Give the result to `omoOptions.agents`:
 *
 *   test.use({ omoOptions: { agents: { developer: script('ready', 'print|hello', 'hang') } } })
 */
export function script(...lines: string[]): string {
  return lines.join('\n')
}

/** Every live session, as the user sees them (with `terminal_id` while the agent runs). */
export async function liveAgents(omo: Omo): Promise<AgentSession[]> {
  return omo.api<AgentSession[]>('GET', '/api/agents?live=true')
}

/** Waits until `role` has a live session on `ticket` in `state` and returns it. */
export async function waitForSession(omo: Omo, ticket: number, role: AgentRole, state: string = 'running'): Promise<AgentSession> {
  let found: AgentSession | undefined
  await expect
    .poll(
      async () => {
        found = (await liveAgents(omo)).find((s) => s.ticket === ticket && s.role === role && s.state === state)
        return found !== undefined
      },
      { message: `waiting for a ${state} ${role} on #${ticket}`, timeout: 30_000 },
    )
    .toBe(true)
  return found!
}

/** Waits until no session of `ticket` is live. */
export async function waitForNoLiveSession(omo: Omo, ticket: number): Promise<void> {
  await expect
    .poll(async () => (await liveAgents(omo)).filter((s) => s.ticket === ticket).length, {
      message: `waiting for the agents of #${ticket} to end`,
      timeout: 30_000,
    })
    .toBe(0)
}

/**
 * Ids of the processes that still carry this test's agent mark. Linux only
 * (reads /proc); elsewhere it reports none. Call after `omo.stop()`.
 */
export function agentProcesses(omo: Omo): number[] {
  if (process.platform !== 'linux') return []
  const want = `${AGENT_MARK}=${omo.agentMark}\0`
  const left: number[] = []
  for (const name of readdirSync('/proc')) {
    if (!/^\d+$/.test(name)) continue
    try {
      if (!readFileSync(`/proc/${name}/environ`, 'latin1').includes(want)) continue
      // A process that has exited but is not yet collected is no leftover.
      const stat = readFileSync(`/proc/${name}/stat`, 'latin1')
      if (stat.slice(stat.lastIndexOf(')') + 2, stat.lastIndexOf(')') + 3) === 'Z') continue
      left.push(Number(name))
    } catch {
      // gone, or not ours to read
    }
  }
  return left
}

/** Stops omo and expects that no agent process of this test is left (they end a moment after the server). */
export async function expectNoAgentProcesses(omo: Omo): Promise<void> {
  await omo.stop()
  await expect.poll(() => agentProcesses(omo), { message: 'agent processes left behind', timeout: 15_000 }).toEqual([])
}

/**
 * For a script that exits without handing in: the engine counts every exit as a failure and starts a fresh agent until the third
 * failure in a row flags the ticket (`failed_step`) and starts no more, with a moment between two attempts when nothing is live.
 * Waits for that final state (the ticket flagged, nothing live), and answers the saved sessions of `role` on `ticket`, all ended,
 * so that a test acts on one session it picked by its id and never on a row by its position, which a later attempt would take.
 */
export async function waitForRestartsExhausted(omo: Omo, ticket: number, role: AgentRole): Promise<AgentSession[]> {
  await expect
    .poll(async () => (await omo.api<{ failed_step?: string }>('GET', `/api/tickets/${ticket}`)).failed_step ?? '', {
      message: `waiting for the ${role} of #${ticket} to fail for the last time`,
      timeout: 30_000,
    })
    .not.toBe('')
  await waitForNoLiveSession(omo, ticket)
  const ended = (await omo.api<AgentSession[]>('GET', '/api/agents')).filter((s) => s.ticket === ticket && s.role === role)
  expect(ended.length, `the saved ${role} sessions of #${ticket}`).toBeGreaterThan(0)
  expect(ended.map((s) => s.state)).toEqual(ended.map(() => 'ended'))
  return ended
}
