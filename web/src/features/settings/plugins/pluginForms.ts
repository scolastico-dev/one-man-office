// What the Plugins page says about a plugin in words, and the small rules the
// browser needs: whether text is a JSON object (the server checks the
// configuration itself), which name to suggest for a source, which manual
// actions a plugin offers where. Plugin names, versions, descriptions and
// messages come from files below omo's home; they are shown through
// visibleText by the components, never as markup.
import type { FileError, PluginHook, PluginInfo, PluginSettings } from '../../../api/types'
import { shortCommit } from '../extensionWords'


const EVENT_WORDS: Record<string, string> = {
  ticket_created: 'When a ticket is created',
  ticket_moved: 'When a ticket is moved',
  comment_written: 'When a comment is written',
  question_asked: 'When an agent asks a question',
  ticket_needs_attention: 'When a ticket needs attention',
  agent_started: 'When an agent starts',
  agent_exited: 'When an agent ends',
  omo_paused: 'When omo is paused',
  omo_resumed: 'When the pause ends',
}

/** What a run was for, after "on". */
const EVENT_NOUNS: Record<string, string> = {
  ticket_created: 'a new ticket',
  ticket_moved: 'a moved ticket',
  comment_written: 'a comment',
  question_asked: 'a question',
  ticket_needs_attention: 'a ticket that needs attention',
  agent_started: 'an agent start',
  agent_exited: 'an agent end',
  omo_paused: 'the pause',
  omo_resumed: 'the end of the pause',
  timer: 'its timer',
  manual: 'a button',
}

/** One hook in words ("When a ticket is moved", "Every 1h", "Button on a ticket: …"); the caller shows it as visible text. */
export function hookWords(h: PluginHook): string {
  if (h.event === 'timer') return h.interval ? `Every ${h.interval}` : 'On a timer'
  if (h.event === 'manual') return `${h.scope === 'ticket' ? 'Button on a ticket' : 'Button here'}: ${h.description}`
  return EVENT_WORDS[h.event] ?? `On ${h.event}`
}

/** What the last run was for ("a moved ticket"), from the event's name. */
export function eventNoun(event: string): string {
  return EVENT_NOUNS[event] ?? event
}

/** Whether the plugin's hooks run: installed, enabled and loaded. */
export function runsHooks(state: string): boolean {
  return state === 'ready' || state === 'running' || state === 'error'
}

/** The manual actions of a plugin with the given scope, in the manifest's order. */
export function manualActions(info: Pick<PluginInfo, 'hooks'>, scope: 'ticket' | 'global'): PluginHook[] {
  return info.hooks.filter((h) => h.event === 'manual' && (h.scope || 'global') === scope && h.name !== '')
}

/** The configuration as the editor shows it: indented JSON. */
export function configText(config: Record<string, unknown> | null | undefined): string {
  return JSON.stringify(config ?? {}, null, 2)
}

export const CONFIG_PARSE_MESSAGE = 'Enter a JSON object, such as {"key": "value"}.'

/** The configuration in `text`, or the message why it is none. Only the shape is checked here; the server checks the rest. */
export function parseConfig(text: string): { ok: true; value: Record<string, unknown> } | { ok: false; message: string } {
  let v: unknown
  try {
    v = JSON.parse(text)
  } catch {
    return { ok: false, message: CONFIG_PARSE_MESSAGE }
  }
  if (typeof v !== 'object' || v === null || Array.isArray(v)) return { ok: false, message: CONFIG_PARSE_MESSAGE }
  return { ok: true, value: v as Record<string, unknown> }
}

/**
 * A name for a plugin from its source as typed: the last part of the address
 * without `.git`, with every character a name cannot hold replaced. The
 * server decides which names it takes.
 */
export function suggestName(source: string): string {
  let s = source.trim().replace(/[\\/]+$/, '')
  const last = s.split(/[\\/:]/).pop() ?? ''
  return last
    .replace(/\.git$/i, '')
    .replace(/[^A-Za-z0-9._-]+/g, '-')
    .replace(/^[^A-Za-z0-9]+/, '')
    .slice(0, 64)
}

/** The plugin a settings file belongs to (`…/plugins/<name>.yaml`), or null. */
export function fileOfPlugin(file: string): string | null {
  const m = /[\\/]plugins[\\/]([^\\/]+)\.ya?ml$/.exec(file)
  return m === null ? null : (m[1] ?? null)
}

/** The broken files that belong to the plugin `name`. */
export function errorsOfPlugin(errors: readonly FileError[], name: string): FileError[] {
  return errors.filter((e) => fileOfPlugin(e.file) === name)
}

/** The fields the install form shows; a refusal naming another is shown above them. */
export const INSTALL_FIELDS: readonly string[] = ['name', 'source', 'subpath', 'ref']

/** What an install or an update did, in words. */
export function installResult(info: Pick<PluginInfo, 'commit' | 'state' | 'message'>, installedBefore: boolean): { ok: boolean; text: string } {
  const verb = installedBefore ? 'Updated' : 'Installed'
  if (info.state === 'invalid' || info.state === 'not_installed') return { ok: false, text: `${verb}, but it does not load: ${info.message}` }
  if (info.commit === '') return { ok: true, text: `${verb}.` }
  return { ok: true, text: `${verb}; now at ${shortCommit(info.commit)}.` }
}

/** The local time of a log line, hours and minutes. */
export function logTime(at: string): string {
  const t = Date.parse(at)
  return Number.isNaN(t) ? at : new Date(t).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
}

/** Whether two settings of a plugin say the same, whatever their revision. */
export function sameSettings(a: PluginSettings, b: PluginSettings): boolean {
  return a.name === b.name && a.source === b.source && a.subpath === b.subpath && a.ref === b.ref && a.enabled === b.enabled && JSON.stringify(a.config) === JSON.stringify(b.config)
}

/** The state to show: a plugin that is off and has no installed files is not installed (the runtime only says it is off). */
export function shownState(info: Pick<PluginInfo, 'state' | 'commit'>): string {
  return info.state === 'disabled' && info.commit === '' ? 'not_installed' : info.state
}
