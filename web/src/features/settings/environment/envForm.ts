// What the Environment page needs besides the shared table: the variables omo
// ships, how a stored answer becomes the form's value, and how the answers of
// the check are matched to the rows they belong to.
import type { AgentEnvReport, EnvVarCheck, General } from '../../../api/types'

/** The variables a new home starts with; the same names and values as the server's defaults. */
export const SHIPPED_ENV: Readonly<Record<string, string>> = {
  GIT_AUTHOR_NAME: 'OMO - AI Orchestrator',
  GIT_AUTHOR_EMAIL: 'omo@scolasti.co',
  GIT_COMMITTER_NAME: '${GIT_COMMITTER_NAME:${GIT_AUTHOR_NAME:`git config user.name`}}',
  GIT_COMMITTER_EMAIL: '${GIT_COMMITTER_EMAIL:${GIT_AUTHOR_EMAIL:`git config user.email`}}',
  GIT_CONFIG_PARAMETERS: "'commit.gpgSign=false' ${GIT_CONFIG_PARAMETERS:-}",
}

/** What the page edits: the `agent_env` key of the general settings. */
export interface EnvForm {
  agent_env: Record<string, string>
}

export const KEY = 'agent_env'

export function formOf(g: Pick<General, 'agent_env'>): EnvForm {
  return { agent_env: { ...g.agent_env } }
}

/** The shipped defaults as a fresh copy. */
export function shippedCopy(): Record<string, string> {
  return { ...SHIPPED_ENV }
}

export function isShipped(env: Record<string, string>): boolean {
  const names = Object.keys(env)
  return names.length === Object.keys(SHIPPED_ENV).length && names.every((n) => Object.hasOwn(SHIPPED_ENV, n) && SHIPPED_ENV[n] === env[n])
}

/** The variable a refusal names (`agent_env.NAME`), or null for any other field. */
export function variableOf(field: string): string | null {
  const prefix = `${KEY}.`
  return field.startsWith(prefix) && field.length > prefix.length ? field.slice(prefix.length) : null
}

/** Refusals by field (dotted key) turned into messages by variable name, for the table's rows. */
export function rowErrors(fieldErrors: Readonly<Record<string, string>>): Record<string, string> {
  const out: Record<string, string> = {}
  for (const [field, message] of Object.entries(fieldErrors)) {
    const name = variableOf(field)
    if (name !== null) out[name] = message
  }
  return out
}

/** Refusals of the form that no row owns (the key as a whole). */
export function formErrors(fieldErrors: Readonly<Record<string, string>>): string[] {
  return Object.entries(fieldErrors)
    .filter(([field]) => variableOf(field) === null)
    .map(([, message]) => message)
}

export const STATE_WORDS = { set: 'set', empty: 'empty', failed: 'failed' } as const

/** One answer of a check with the value the variable had when it was asked. */
export interface Checked {
  check: EnvVarCheck
  /** The row's text at the time of the check. */
  value: string
}

/** Pairs the server's answer with the draft that was sent. */
export function checkedOf(report: AgentEnvReport, sent: Record<string, string>): Checked[] {
  return report.variables.filter((v) => Object.hasOwn(sent, v.name)).map((v) => ({ check: v, value: sent[v.name]! }))
}

/** The answers that still describe the rows: a row edited since the check has no answer any more. */
export function currentChecks(results: readonly Checked[], env: Record<string, string>): EnvVarCheck[] {
  return results.filter((r) => Object.hasOwn(env, r.check.name) && env[r.check.name] === r.value).map((r) => r.check)
}

/** The title of the result: counts in words. */
export function summaryOf(checks: readonly EnvVarCheck[]): string {
  const failed = checks.filter((c) => c.state === 'failed').length
  const empty = checks.filter((c) => c.state === 'empty').length
  const of = `${checks.length} ${checks.length === 1 ? 'variable' : 'variables'}`
  if (failed > 0) return `${failed} of ${of} failed`
  if (empty > 0) return `${empty} of ${of} empty`
  return `${of} checked, all set`
}

export function toneOf(checks: readonly EnvVarCheck[]): 'info' | 'warn' | 'danger' {
  if (checks.some((c) => c.state === 'failed')) return 'danger'
  if (checks.some((c) => c.state === 'empty')) return 'warn'
  return 'info'
}
