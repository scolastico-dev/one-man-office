// What the Usage page shows and sends, as plain functions. A usage script is
// code that runs as the user with the user's credentials; the page keeps its
// text exactly as typed. The server checks every value and every script; the
// browser only turns typed text into the numbers a field needs.
import type { FileError, UsageScriptInfo, UsageSettings, UsageThreshold } from '../../../api/types'
import type { SelectOption } from '../../../components/ui/SelectField.vue'

/** The kind of a script in words: omo's own text, omo's script with your text, or one only you have. */
export function scriptKind(s: Pick<UsageScriptInfo, 'has_default' | 'customized'>): "omo's default" | 'Customized' | 'Yours' {
  if (!s.has_default) return 'Yours'
  return s.customized ? 'Customized' : "omo's default"
}

/** One row of the per-window limits as the form edits it: the text of each field. */
export interface OverrideRow {
  name: string
  soft: string
  hard: string
}

const show = (n: number | undefined) => (n === undefined || n === null ? '' : String(n))

/** The stored per-window limits as rows, sorted by window name. */
export function rowsOf(windows: Readonly<Record<string, UsageThreshold>> | null | undefined): OverrideRow[] {
  return Object.keys(windows ?? {})
    .sort((a, b) => a.localeCompare(b))
    .map((name) => ({ name, soft: show(windows![name]!.soft_percent), hard: show(windows![name]!.hard_percent) }))
}

/** A percent as typed: `undefined` for an empty field (the general limit applies), `null` for text that is no number. */
export function parsePercent(text: string): number | undefined | null {
  const t = text.trim()
  if (t === '') return undefined
  return /^\d+(\.\d+)?$/.test(t) ? Number(t) : null
}

export const ENTER_NAME = 'Enter the name of the window.'
export const ENTER_NUMBER = 'Enter a number, or leave it empty.'
export const NAME_TWICE = 'Another row has this window; only one of them can be kept.'

/** What the rows say: the windows map to send, and per row and field (`<i>.name|soft|hard`) the text that is no value. */
export function windowsOf(rows: readonly OverrideRow[]): { windows: Record<string, UsageThreshold>; problems: Record<string, string> } {
  const windows: Record<string, UsageThreshold> = {}
  const problems: Record<string, string> = {}
  const names = rows.map((r) => r.name.trim())
  rows.forEach((r, i) => {
    const name = names[i]!
    if (name === '') problems[`${i}.name`] = ENTER_NAME
    else if (names.indexOf(name) !== i) problems[`${i}.name`] = NAME_TWICE
    const t: UsageThreshold = {}
    const soft = parsePercent(r.soft)
    const hard = parsePercent(r.hard)
    if (soft === null) problems[`${i}.soft`] = ENTER_NUMBER
    else if (soft !== undefined) t.soft_percent = soft
    if (hard === null) problems[`${i}.hard`] = ENTER_NUMBER
    else if (hard !== undefined) t.hard_percent = hard
    if (name !== '' && !(name in windows)) windows[name] = t
  })
  return { windows, problems }
}

/** The server's refusal of one field of a window's limits (`usage.windows.<name>.soft_percent`). */
export function overrideError(fieldErrors: Readonly<Record<string, string>>, name: string, which: 'soft' | 'hard'): string | undefined {
  return fieldErrors[`usage.windows.${name.trim()}.${which}_percent`]
}

/** The refusals the limits form shows under a field of its own; others go above the form. */
export function placedLimitField(field: string): boolean {
  return /^usage\.(soft_percent|hard_percent|refresh_interval|windows)$/.test(field) || /^usage\.windows\.[^.]+\.(soft|hard)_percent$/.test(field)
}

/** The thresholds that apply to a window: its own, else the general ones. */
export function thresholdsOf(window: string, usage: Pick<UsageSettings, 'soft_percent' | 'hard_percent' | 'windows'> | null | undefined): { soft?: number; hard?: number } {
  if (!usage) return {}
  const own = usage.windows?.[window] ?? {}
  return { soft: own.soft_percent ?? usage.soft_percent, hard: own.hard_percent ?? usage.hard_percent }
}

/** The name of the script a settings file belongs to (`…/usage/<name>.lua`), or null. */
export function fileOfScript(file: string): string | null {
  const m = /(?:^|[\\/])usage[\\/]([^\\/]+)\.lua$/.exec(file)
  return m === null ? null : (m[1] ?? null)
}

/** The broken files that belong to the script `name`. */
export function errorsOfScript(errors: readonly FileError[], name: string): FileError[] {
  return errors.filter((e) => fileOfScript(e.file) === name)
}

/** The profiles a test can run as: those that use the script first, then the others; none, "No profile". */
export function testProfiles(profiles: readonly string[], usedBy: readonly string[]): SelectOption[] {
  const using = profiles.filter((p) => usedBy.includes(p))
  const others = profiles.filter((p) => !usedBy.includes(p))
  const out = [...using.map((p) => ({ value: p, label: `${p} (uses this script)` })), ...others.map((p) => ({ value: p, label: p }))]
  return out.length > 0 ? out : [{ value: '', label: 'No profile (no account folder)' }]
}

/** "Used by a, b", or that no profile uses it. */
export function usedByWords(usedBy: readonly string[]): string {
  return usedBy.length > 0 ? `Used by ${usedBy.join(', ')}` : 'No profile uses it.'
}

/** Where a new script starts: a usage function that reports nothing yet. */
export const NEW_SCRIPT = `-- Tells omo how full one account is. omo calls usage(account) once per
-- account of the profiles that use this script; account has profile, cli,
-- account_dir, home and os. Return a list of windows: name, used (percent)
-- and, if known, resets_at (RFC 3339 text or Unix seconds).
function usage(account)
  return {
    -- { name = "session", used = 42, resets_at = "2026-12-01T10:00:00Z" },
  }
end
`

/** One line per helper a script can call. */
export const HELPERS: readonly (readonly [call: string, what: string])[] = [
  ['omo.http{url, method?, headers?, json?|form?|body?, timeout?}', 'https anywhere, http on this machine only; 8 calls, 1 MiB each'],
  ['omo.read_file(path)', 'a file below / or ~/, at most 1 MiB'],
  ['omo.env(name)', 'a variable of the agent environment of this profile'],
  ['omo.keychain(service)', 'a macOS keychain entry; nil elsewhere'],
  ['omo.codex_refresh(auth_path)', "renews a Codex login with the profile's own command"],
  ['omo.json_decode(text), omo.json_encode(value)', 'JSON to tables and back'],
  ['omo.time_parse(rfc3339), omo.now()', 'Unix seconds'],
  ['omo.sha256_hex(text), omo.nfc(text)', 'hash and Unicode normal form'],
  ['omo.log(text)', 'a line for the test below; 50 lines per run'],
]
