import { describe, expect, test } from 'vitest'
import type { UsageSettings } from '../../api/types'
import { judgeWindow, readingCounts, staleReading, windowThresholds } from './usageVerdict'

const NOW = Date.parse('2026-11-15T12:00:00Z')
const fresh = { error: '', fetched_at: '2026-11-15T11:58:00Z' }
const settings = (over: Partial<UsageSettings> = {}): UsageSettings => ({ soft_percent: 80, hard_percent: 95, refresh_interval: '5m', windows: {}, ...over })

describe('windowThresholds', () => {
  test("a script's own override comes first, then the window's of every script, then the general value, as the server judges", () => {
    const u = settings({ windows: { weekly: { soft_percent: 60, hard_percent: 70 }, 'claude/weekly': { soft_percent: 40 }, 'codex/session': { hard_percent: 50 } } })
    expect(windowThresholds('weekly', u, 'claude')).toEqual({ soft: 40, hard: 70 })
    expect(windowThresholds('weekly', u, 'codex')).toEqual({ soft: 60, hard: 70 })
    expect(windowThresholds('session', u, 'codex')).toEqual({ soft: 80, hard: 80 })
    expect(windowThresholds('session', u, 'claude')).toEqual({ soft: 80, hard: 95 })
    expect(windowThresholds('weekly', u)).toEqual({ soft: 60, hard: 70 })
    // An old file with bare names only.
    expect(windowThresholds('weekly', settings({ windows: { weekly: { soft_percent: 60 } } }), 'claude')).toEqual({ soft: 60, hard: 95 })
    // judgeWindow takes the script from the reading.
    const w = { name: 'weekly', label: '', used_percent: 50, resets_at: null }
    expect(judgeWindow(w, { ...fresh, script: 'claude' }, u, NOW).state).toBe('soft')
    expect(judgeWindow(w, { ...fresh, script: 'codex' }, u, NOW).state).toBe('ok')
  })

  test('the window override wins, then the general value, then 90 and 98', () => {
    expect(windowThresholds('weekly', null)).toEqual({ soft: 90, hard: 98 })
    expect(windowThresholds('weekly', settings())).toEqual({ soft: 80, hard: 95 })
    expect(windowThresholds('weekly', settings({ windows: { weekly: { soft_percent: 50 } } }))).toEqual({ soft: 50, hard: 95 })
    expect(windowThresholds('weekly', settings({ windows: { weekly: { hard_percent: 99 } } }))).toEqual({ soft: 80, hard: 99 })
    expect(windowThresholds('session', settings({ windows: { weekly: { soft_percent: 50 } } }))).toEqual({ soft: 80, hard: 95 })
  })

  test('values outside 0 < v <= 100 fall back like the server does, and hard is never below soft', () => {
    expect(windowThresholds('w', settings({ soft_percent: 0, hard_percent: 101 }))).toEqual({ soft: 90, hard: 98 })
    expect(windowThresholds('w', settings({ windows: { w: { soft_percent: -5, hard_percent: 200 } } }))).toEqual({ soft: 80, hard: 95 })
    expect(windowThresholds('w', settings({ windows: { w: { soft_percent: 97, hard_percent: 60 } } }))).toEqual({ soft: 97, hard: 97 })
    // An inherited name of an object is no window.
    expect(windowThresholds('constructor', settings())).toEqual({ soft: 80, hard: 95 })
  })
})

describe('readingCounts', () => {
  test('a reading never taken and one older than 24 h do not count', () => {
    expect(readingCounts(fresh, NOW)).toBe(true)
    expect(readingCounts({ error: '', fetched_at: null }, NOW)).toBe(false)
    expect(readingCounts({ error: '', fetched_at: '2026-11-14T12:00:00Z' }, NOW)).toBe(true)
    expect(readingCounts({ error: '', fetched_at: '2026-11-14T11:59:59Z' }, NOW)).toBe(false)
    expect(readingCounts({ error: '', fetched_at: 'not a time' }, NOW)).toBe(false)
  })

  test('after a failed run the last windows count for 30 minutes from the first failure, then not', () => {
    const failed = (stale_since: string | null) => ({ error: 'line 3: boom', fetched_at: fresh.fetched_at, stale_since })
    expect(readingCounts(failed('2026-11-15T11:40:00Z'), NOW)).toBe(true)
    expect(readingCounts(failed('2026-11-15T11:30:00Z'), NOW)).toBe(true)
    expect(readingCounts(failed('2026-11-15T11:29:59Z'), NOW)).toBe(false)
    expect(readingCounts(failed(null), NOW)).toBe(false)
    expect(readingCounts({ error: 'line 3: boom', fetched_at: fresh.fetched_at }, NOW)).toBe(false)
    // An old reading is unknown even while the failure is recent.
    expect(readingCounts({ error: 'boom', fetched_at: '2026-11-14T11:00:00Z', stale_since: '2026-11-15T11:50:00Z' }, NOW)).toBe(false)
  })

  test('staleReading says whether the windows shown are those of before a failed run that still count', () => {
    expect(staleReading({ error: 'boom', fetched_at: fresh.fetched_at, stale_since: '2026-11-15T11:50:00Z' }, NOW)).toBe(true)
    expect(staleReading({ error: 'boom', fetched_at: fresh.fetched_at, stale_since: '2026-11-15T11:00:00Z' }, NOW)).toBe(false)
    expect(staleReading({ ...fresh, stale_since: null }, NOW)).toBe(false)
  })
})

describe('judgeWindow', () => {
  test('ok, soft and hard follow the thresholds that apply, at or over each', () => {
    const at = (used: number) => judgeWindow({ name: 'weekly', label: '', used_percent: used, resets_at: null }, fresh, settings(), NOW)
    expect(at(79.9)).toMatchObject({ state: 'ok', percent: 79.9, soft: 80, hard: 95 })
    expect(at(80)).toMatchObject({ state: 'soft', percent: 80 })
    expect(at(95)).toMatchObject({ state: 'hard', percent: 95 })
    expect(at(140)).toMatchObject({ state: 'hard', percent: 100 })
    expect(at(-3)).toMatchObject({ state: 'ok', percent: 0 })
  })

  test('a failed run keeps the window for 30 minutes after the first failure, then leaves it unknown without a number', () => {
    const w = { name: 'weekly', label: '', used_percent: 99, resets_at: '2026-11-15T15:00:00Z' }
    const kept = judgeWindow(w, { error: 'boom', fetched_at: fresh.fetched_at, stale_since: '2026-11-15T11:45:00Z' }, settings(), NOW)
    expect(kept).toEqual({ state: 'hard', percent: 99, resetsAt: '2026-11-15T15:00:00Z', soft: 80, hard: 95 })
    const gone = judgeWindow(w, { error: 'boom', fetched_at: fresh.fetched_at, stale_since: '2026-11-15T11:00:00Z' }, settings(), NOW)
    expect(gone).toEqual({ state: 'unknown', percent: null, resetsAt: null, soft: 80, hard: 95 })
    // A test run has no stale_since: its failure leaves the window unknown.
    expect(judgeWindow(w, { error: 'boom', fetched_at: fresh.fetched_at }, settings(), NOW).state).toBe('unknown')
  })

  test('a reading older than 24 h is unknown', () => {
    const v = judgeWindow({ name: 'weekly', label: '', used_percent: 99, resets_at: null }, { error: '', fetched_at: '2026-11-13T12:00:00Z' }, settings(), NOW)
    expect(v.state).toBe('unknown')
    expect(v.percent).toBeNull()
  })

  test('a window past its reset counts 0 % and has no reset time any more', () => {
    const past = judgeWindow({ name: 'weekly', label: '', used_percent: 99, resets_at: '2026-11-15T11:59:00Z' }, fresh, settings(), NOW)
    expect(past).toEqual({ state: 'ok', percent: 0, resetsAt: null, soft: 80, hard: 95 })
    const exactly = judgeWindow({ name: 'weekly', label: '', used_percent: 99, resets_at: '2026-11-15T12:00:00Z' }, fresh, settings(), NOW)
    expect(exactly.percent).toBe(0)
    const ahead = judgeWindow({ name: 'weekly', label: '', used_percent: 99, resets_at: '2026-11-15T12:00:01Z' }, fresh, settings(), NOW)
    expect(ahead).toEqual({ state: 'hard', percent: 99, resetsAt: '2026-11-15T12:00:01Z', soft: 80, hard: 95 })
  })

  test('a number that is no number is unknown', () => {
    expect(judgeWindow({ name: 'w', label: '', used_percent: Number.NaN, resets_at: null }, fresh, settings(), NOW).state).toBe('unknown')
  })
})
