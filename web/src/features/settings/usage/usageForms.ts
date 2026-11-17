// What the Usage page shows and sends, as plain functions. A usage script is
// code that runs as the user with the user's credentials; the page keeps its
// text exactly as typed. The server checks every value and every script; the
// browser only turns typed text into the numbers a field needs.
import type { UsageReading, UsageScriptInfo, UsageThreshold } from '../../../api/types'
import type { SelectOption } from '../../../components/ui/SelectField.vue'
import { visibleText } from '../../../components/ui/visibleText'
import { windowWithName } from '../../usage/windowLabel'

/** The kind of a script in words: omo's own text, omo's script with your text, or one only you have. */
export function scriptKind(s: Pick<UsageScriptInfo, 'has_default' | 'customized'>): "omo's default" | 'Customized' | 'Yours' {
  if (!s.has_default) return 'Yours'
  return s.customized ? 'Customized' : "omo's default"
}

/** One row of the per-window limits as the form edits it: the text of each field. */
export interface OverrideRow {
  /** The key: a window name (that window of every script) or `<script>/<window>`. */
  name: string
  soft: string
  hard: string
  /** The name is typed ("Other…") instead of chosen. */
  other?: boolean
}

const show = (n: number | undefined) => (n === undefined || n === null ? '' : String(n))

/** The stored per-window limits as rows, sorted by window name. */
export function rowsOf(windows: Readonly<Record<string, UsageThreshold>> | null | undefined): OverrideRow[] {
  return Object.keys(windows ?? {})
    .sort((a, b) => a.localeCompare(b))
    .map((name) => ({ name, soft: show(windows![name]!.soft_percent), hard: show(windows![name]!.hard_percent) }))
}

/** A percent as typed: `undefined` for an empty field (the general limit applies), `null` for text that is no number from 1 to 100. */
export function parsePercent(text: string): number | undefined | null {
  const t = text.trim()
  if (t === '') return undefined
  if (!/^\d+(\.\d+)?$/.test(t)) return null
  const n = Number(t)
  return n >= 1 && n <= 100 ? n : null
}

export const ENTER_NAME = 'Choose a window, or type its name under Other….'
export const ENTER_PERCENT = 'Enter a number from 1 to 100, or leave it empty.'
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
    if (soft === null) problems[`${i}.soft`] = ENTER_PERCENT
    else if (soft !== undefined) t.soft_percent = soft
    if (hard === null) problems[`${i}.hard`] = ENTER_PERCENT
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
  return /^usage\.(soft_percent|hard_percent|refresh_interval|windows)$/.test(field) || /^usage\.windows\..+\.(soft|hard)_percent$/.test(field)
}

/** The name of the script a settings file belongs to (`…/usage/<name>.lua`), or null. */
export function fileOfScript(file: string): string | null {
  const m = /(?:^|[\\/])usage[\\/]([^\\/]+)\.lua$/.exec(file)
  return m === null ? null : (m[1] ?? null)
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
-- account_dir, home and os. Return a list of windows: name, used (percent),
-- if known resets_at (RFC 3339 text or Unix seconds) and, if you like, a
-- label for people.
function usage(account)
  return {
    -- { name = "session", label = "Session (5 hours)", used = 42, resets_at = "2026-12-01T10:00:00Z" },
  }
end
`

/** What usage(account) returns, in one sentence. */
export const CONTRACT =
  'usage(account) returns a list of at most 32 windows: { name, used, resets_at?, label? }. name: 1 to 40 of a-z, 0-9, - and _, what models and limits refer to; used: percent; resets_at: RFC 3339 text or Unix seconds; label: what people read instead of the name, one line, at most 60 characters (shown as "label (name)" here).' 

/** One line per helper a script can call. */
export const HELPERS: readonly (readonly [call: string, what: string])[] = [
  ['omo.http{url, method?, headers?, json?|form?|body?, timeout?}', 'https anywhere, http on this machine only; 8 calls, 1 MiB each'],
  ['omo.read_file(path)', 'a file below / or ~/, at most 1 MiB'],
  ['omo.env(name)', 'a variable of the agent environment of this profile'],
  ['omo.keychain(service)', 'a macOS keychain entry; nil elsewhere'],
  ['omo.rate_limited(retry_after)', 'ends the run as rate limited; omo waits Retry-After (1 min to 1 h), else 2, 4, 8 … min'],
  ['omo.codex_refresh(auth_path)', "renews a Codex login with the profile's own command"],
  ['omo.agy_usage()', "the JSON of agy's /usage command, run with the profile's own command"],
  ['omo.json_decode(text), omo.json_encode(value)', 'JSON to tables and back'],
  ['omo.time_parse(rfc3339), omo.now()', 'Unix seconds'],
  ['omo.sha256_hex(text), omo.nfc(text)', 'hash and Unicode normal form'],
  ['omo.log(text)', 'a line for the test below; 50 lines per run'],
]

// The window of a per-window limit is chosen from the windows omo knows. A
// limit is keyed by a window name, which means that window of every script,
// or by `<script>/<window>`, which means that script's window only.

/** What the limits table needs of the current readings. */
export type ReportedWindows = Pick<UsageReading, 'script' | 'windows'>

/** The value of the last option, which shows a field to type a name in. */
export const OTHER_WINDOW = '\u0000other'

const scriptWords = (script: string) => {
  const s = visibleText(script)
  return s.charAt(0).toUpperCase() + s.slice(1)
}

/** The scripts that report each window name now, with the window as each reports it. */
function reportedBy(readings: readonly ReportedWindows[]): Map<string, Map<string, { name: string; label: string }>> {
  const out = new Map<string, Map<string, { name: string; label: string }>>()
  for (const r of readings) {
    for (const w of r.windows) {
      let by = out.get(w.name)
      if (!by) out.set(w.name, (by = new Map()))
      if (!by.has(r.script)) by.set(r.script, { name: w.name, label: w.label ?? '' })
    }
  }
  return out
}

/**
 * A limit's key in words: "Claude: Weekly, all models (weekly)" for a
 * script's window, "weekly of every script" for a bare name (with its label
 * where only one script reports it).
 */
export function overrideKeyWords(key: string, readings: readonly ReportedWindows[]): string {
  const k = key.trim()
  const slash = k.indexOf('/')
  const by = reportedBy(readings)
  if (slash > 0) {
    const script = k.slice(0, slash)
    const window = k.slice(slash + 1)
    const w = by.get(window)?.get(script) ?? { name: window, label: '' }
    return `${scriptWords(script)}: ${windowWithName(w)}`
  }
  const scripts = by.get(k)
  if (scripts && scripts.size === 1) return `${windowWithName([...scripts.values()][0]!)} of every script`
  return `${visibleText(k)} of every script`
}

/** Whether the current readings report the window a key means. */
function reported(key: string, readings: readonly ReportedWindows[]): boolean {
  const slash = key.indexOf('/')
  const by = reportedBy(readings)
  return slash > 0 ? (by.get(key.slice(slash + 1))?.has(key.slice(0, slash)) ?? false) : by.has(key)
}

/**
 * The windows a row can choose: per script each window the readings report
 * ("Claude: Weekly, all models (weekly)"), and for a window more than one
 * script reports also the window of every script ("weekly of every
 * script"); the windows models list that no script reports now; the row's
 * own key when nothing reports it now ("… (not reported now)"). Keys other
 * rows have are left out. Sorted by their words; "Other…" is not included.
 */
export function windowChoices(readings: readonly ReportedWindows[], listed: readonly string[], current: string, taken: ReadonlySet<string>): SelectOption[] {
  const out = new Map<string, string>()
  for (const [name, scripts] of reportedBy(readings)) {
    for (const script of scripts.keys()) out.set(`${script}/${name}`, overrideKeyWords(`${script}/${name}`, readings))
    if (scripts.size > 1) out.set(name, overrideKeyWords(name, readings))
  }
  for (const name of listed) if (!out.has(name) && ![...out.keys()].some((k) => k.endsWith('/' + name))) out.set(name, `${visibleText(name)} (not reported now)`)
  const own = current.trim()
  if (own !== '' && !out.has(own)) out.set(own, reported(own, readings) ? overrideKeyWords(own, readings) : `${overrideKeyWords(own, []).replace(/ of every script$/, '')} (not reported now)`)
  return [...out]
    .filter(([value]) => value === own || !taken.has(value))
    .map(([value, label]) => ({ value, label }))
    .sort((a, b) => a.label.localeCompare(b.label))
}
