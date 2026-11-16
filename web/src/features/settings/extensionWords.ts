// Words the skill set and plugin pages share: a commit shown short, and a
// state shown as a badge with its tone. One table per kind of state; a state
// the server may add later is shown in its own words.
import type { PluginState, SkillSetCheckout } from '../../api/types'

export type StateTone = 'ok' | 'danger' | 'muted' | 'accent' | 'warn'

export interface StateWords {
  tone: StateTone
  text: string
}

/** The first seven characters of a commit. */
export function shortCommit(commit: string): string {
  return commit.slice(0, 7)
}

/** A skill set's checkout. */
export const SKILL_SET_STATES: Readonly<Record<SkillSetCheckout, StateWords>> = {
  ok: { tone: 'ok', text: 'checked out' },
  updating: { tone: 'accent', text: 'updating' },
  failed: { tone: 'danger', text: 'failed' },
  missing: { tone: 'muted', text: 'not checked out' },
}

/** A plugin in the runtime. */
export const PLUGIN_STATES: Readonly<Record<PluginState, StateWords>> = {
  ready: { tone: 'ok', text: 'ready' },
  running: { tone: 'accent', text: 'running' },
  error: { tone: 'danger', text: 'error' },
  disabled: { tone: 'muted', text: 'off' },
  not_installed: { tone: 'warn', text: 'not installed' },
  invalid: { tone: 'danger', text: 'invalid' },
}

/** The words of `state` in `table`; a state the table does not know yet in its own words. */
export function stateWords(table: Readonly<Record<string, StateWords>>, state: string): StateWords {
  return Object.prototype.hasOwnProperty.call(table, state) ? table[state]! : { tone: 'muted', text: state.replace(/_/g, ' ') }
}
