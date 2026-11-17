// How the browser judges one usage window, by the same rules the server
// uses for a model (`internal/usage/evaluate.go`). The server sends its
// verdict per model (`ModelUsage.state`); wherever a model is shown, that
// verdict is used as it comes. A window of an account, or of a test run, has
// no verdict of its own on the wire, so its meter is judged here:
//
//   - a reading never taken or one older than 24 hours does not count: the
//     window is unknown and shows no number;
//   - after a failed run the windows of the last successful run keep counting
//     for 30 minutes from the end of the first failed run (`stale_since`),
//     then they are unknown too;
//   - a window whose reset time has passed counts 0 % (the next reading
//     brings the new window);
//   - the thresholds are the script's own for the window (`<script>/<window>`),
//     else the window's of every script, else the general ones, else 90 and
//     98; a value that is not above 0 and at most 100 falls back the same
//     way, and the hard one is never below the soft one.
import { timeKey } from '../../api/time'
import type { UsageReading, UsageSettings, UsageWindow } from '../../api/types'

/** omo's thresholds when the settings give none that can be used. */
export const DEFAULT_SOFT_PERCENT = 90
export const DEFAULT_HARD_PERCENT = 98
/** How long a successful reading counts. */
export const MAX_READING_AGE_MS = 24 * 60 * 60_000
/** How long the windows of the last successful run keep counting after a failed run (the server's MaxStaleAfterFailure). */
export const MAX_STALE_AFTER_FAILURE_MS = 30 * 60_000

/** What the verdict needs of a reading; a test run has no `stale_since`. */
type ReadingTimes = Pick<UsageReading, 'error' | 'fetched_at'> & { stale_since?: string | null; script?: string }

export type WindowState = 'ok' | 'soft' | 'hard' | 'unknown'

export interface WindowVerdict {
  state: WindowState
  /** The share that counts, 0 to 100; null while unknown. */
  percent: number | null
  /** The reset time still ahead, as the server wrote it; null when none counts. */
  resetsAt: string | null
  soft: number
  hard: number
}

type Thresholds = Pick<UsageSettings, 'soft_percent' | 'hard_percent' | 'windows'>

function percentOr(v: unknown, fallback: number): number {
  return typeof v === 'number' && !Number.isNaN(v) && v > 0 && v <= 100 ? v : fallback
}

/** Milliseconds since the epoch of an RFC 3339 time, or null. */
export function timeMs(at: string | null | undefined): number | null {
  if (!at) return null
  const key = timeKey(at)
  if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{9}Z$/.test(key)) return null
  const t = Date.parse(`${key.slice(0, 23)}Z`)
  return Number.isNaN(t) ? null : t
}

/**
 * The soft and hard threshold of a window of a script: each value is the
 * first one set of the override `<script>/<window>`, the override `<window>`
 * (that window of every script), the general value, 90 and 98.
 */
export function windowThresholds(window: string, usage: Thresholds | null | undefined, script = ''): { soft: number; hard: number } {
  let soft = percentOr(usage?.soft_percent, DEFAULT_SOFT_PERCENT)
  let hard = percentOr(usage?.hard_percent, DEFAULT_HARD_PERCENT)
  const windows = usage?.windows
  for (const key of script === '' ? [window] : [window, `${script}/${window}`]) {
    const own = windows && Object.prototype.hasOwnProperty.call(windows, key) ? windows[key] : undefined
    if (own?.soft_percent !== undefined && own.soft_percent !== null) soft = percentOr(own.soft_percent, soft)
    if (own?.hard_percent !== undefined && own.hard_percent !== null) hard = percentOr(own.hard_percent, hard)
  }
  return { soft, hard: Math.max(hard, soft) }
}

/**
 * Whether a reading counts: it was taken within the last 24 hours, and its
 * last run did not fail, or failed (first) less than 30 minutes ago, while
 * its windows are those of the last successful run.
 */
export function readingCounts(r: ReadingTimes, now: number): boolean {
  const at = timeMs(r.fetched_at)
  if (at === null || now - at > MAX_READING_AGE_MS) return false
  if (r.error === '') return true
  const since = timeMs(r.stale_since)
  return since !== null && now - since <= MAX_STALE_AFTER_FAILURE_MS
}

/** Whether the windows shown are old ones that still count: the last run failed, those of the run before keep counting for a while. */
export function staleReading(r: ReadingTimes, now: number): boolean {
  return r.error !== '' && readingCounts(r, now)
}

/** The verdict on one window of a reading, as the server would judge a model counted by it alone. */
export function judgeWindow(w: UsageWindow, reading: ReadingTimes, usage: Thresholds | null | undefined, now: number): WindowVerdict {
  const { soft, hard } = windowThresholds(w.name, usage, reading.script ?? '')
  const unknown: WindowVerdict = { state: 'unknown', percent: null, resetsAt: null, soft, hard }
  if (!readingCounts(reading, now)) return unknown
  const used = w.used_percent
  if (typeof used !== 'number' || !Number.isFinite(used)) return unknown
  let percent = Math.min(Math.max(used, 0), 100)
  let resetsAt = w.resets_at
  const reset = timeMs(resetsAt)
  if (reset !== null && reset <= now) {
    percent = 0
    resetsAt = null
  }
  const state: WindowState = percent >= hard ? 'hard' : percent >= soft ? 'soft' : 'ok'
  return { state, percent, resetsAt, soft, hard }
}

/** A time as a clock: "23:12" today, "Mon 23:12" on another day; empty for no time. */
export function clockWords(at: number | null, now: number): string {
  if (at === null) return ''
  const t = new Date(at)
  const clock = t.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' })
  return t.toDateString() === new Date(now).toDateString() ? clock : `${t.toLocaleDateString(undefined, { weekday: 'short' })} ${clock}`
}

/**
 * What old readings are, in words: "as of 23:12; the last refresh failed:
 * <error>. They count until 23:42, then its models are unknown." Empty for
 * a reading whose windows are not old ones that still count.
 */
export function staleWords(r: ReadingTimes, now: number): string {
  if (!staleReading(r, now)) return ''
  const until = timeMs(r.stale_since)! + MAX_STALE_AFTER_FAILURE_MS
  const error = r.error.replace(/\.+$/, '')
  return `as of ${clockWords(timeMs(r.fetched_at), now)}; the last refresh failed: ${error}. They count until ${clockWords(until, now)}, then its models are unknown.`
}
