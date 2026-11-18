import { join } from 'node:path'
import { expect } from '@playwright/test'
import { type AgentRole, type Omo } from './omo'
import { recordedProcesses } from './process'

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

/** Where this test's fake agents leave the id of their process (internal/fakeagent EnvPIDDir). */
export function agentPidDir(omo: Omo): string {
  return join(omo.home, 'agent-pids')
}

/**
 * Ids of the agent processes of this test's server that still exist. Each fake agent recorded its own process id when
 * it started; the operating system is asked about every one, so this works on every platform and, with no agent
 * started, says so by answering an empty list from a folder that is there.
 */
export function agentProcesses(omo: Omo): number[] {
  return recordedProcesses(agentPidDir(omo))
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
