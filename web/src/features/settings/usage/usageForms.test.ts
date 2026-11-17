import { flushPromises } from '@vue/test-utils'
import { describe, expect, test } from 'vitest'
import { ref } from 'vue'
import { ApiError, type ApiClient } from '../../../api/client'
import type { General, SettingsResponse } from '../../../api/types'
import { general } from '../../../testing/fakeOmo'
import { createGeneralForm } from '../general/generalForms'
import {
  ENTER_NAME,
  ENTER_PERCENT,
  NAME_TWICE,
  fileOfScript,
  overrideError,
  overrideKeyWords,
  windowChoices,
  parsePercent,
  placedLimitField,
  rowsOf,
  scriptKind,
  testProfiles,
  usedByWords,
  windowsOf,
} from './usageForms'

test('usageForms: overrides rows map to the windows object and back', () => {
  const windows = { weekly: { hard_percent: 99 }, fable_weekly: { soft_percent: 80 }, session: {} }
  const rows = rowsOf(windows)
  expect(rows).toEqual([
    { name: 'fable_weekly', soft: '80', hard: '' },
    { name: 'session', soft: '', hard: '' },
    { name: 'weekly', soft: '', hard: '99' },
  ])
  expect(windowsOf(rows)).toEqual({ windows, problems: {} })
  expect(rowsOf(null)).toEqual([])

  rows[0]!.soft = '82.5'
  rows[1]!.name = ' extra '
  expect(windowsOf(rows).windows).toEqual({ fable_weekly: { soft_percent: 82.5 }, extra: {}, weekly: { hard_percent: 99 } })
})

test('usageForms: text that is no value is reported per row and field, never sent', () => {
  const { windows, problems } = windowsOf([
    { name: '', soft: '', hard: '' },
    { name: 'weekly', soft: '9o', hard: '' },
    { name: 'weekly', soft: '', hard: '-1' },
  ])
  expect(problems).toEqual({ '0.name': ENTER_NAME, '1.soft': ENTER_PERCENT, '2.name': NAME_TWICE, '2.hard': ENTER_PERCENT })
  expect(ENTER_PERCENT).toBe('Enter a number from 1 to 100, or leave it empty.')
  expect(windows).toEqual({ weekly: {} })
  expect(parsePercent(' ')).toBeUndefined()
  expect(parsePercent('100')).toBe(100)
  expect(parsePercent('1e2')).toBeNull()
  // Outside what the server takes: said the same way, before anything is sent.
  for (const t of ['0', '0.5', '-5', '100.1', '150']) expect(parsePercent(t), t).toBeNull()
  expect(parsePercent('1')).toBe(1)
})

test('usageForms: refusals land under the window and field the server names', () => {
  const fe = { 'usage.windows.weekly.hard_percent': 'too low', 'usage.soft_percent': 'bad' }
  expect(overrideError(fe, 'weekly', 'hard')).toBe('too low')
  expect(overrideError(fe, 'weekly', 'soft')).toBeUndefined()
  expect(placedLimitField('usage.soft_percent')).toBe(true)
  expect(placedLimitField('usage.windows')).toBe(true)
  expect(placedLimitField('usage.windows.weekly.soft_percent')).toBe(true)
  expect(placedLimitField('limits.tasks')).toBe(false)
})

test('usageForms: kinds, files and profiles', () => {
  expect(scriptKind({ has_default: true, customized: false })).toBe("omo's default")
  expect(scriptKind({ has_default: true, customized: true })).toBe('Customized')
  expect(scriptKind({ has_default: false, customized: true })).toBe('Yours')
  expect(fileOfScript('/h/omo/usage/claude.lua')).toBe('claude')
  expect(fileOfScript('/h/omo/prompts/claude.md')).toBeNull()
  expect(testProfiles(['codex', 'claude', 'work'], ['work', 'claude']).map((o) => o.value)).toEqual(['claude', 'work', 'codex'])
  expect(testProfiles(['claude'], ['claude'])[0]!.label).toBe('claude (uses this script)')
  expect(testProfiles([], [])).toEqual([{ value: '', label: 'No profile (no account folder)' }])
  expect(usedByWords([])).toBe('No profile uses it.')
  expect(usedByWords(['a', 'b'])).toBe('Used by a, b')
})

test('usageForms: the page sends only usage, the windows map whole', async () => {
  const sent: { body: unknown; ifMatch?: string }[] = []
  let settings = structuredClone(general) as General
  settings.usage = { soft_percent: 90, hard_percent: 98, refresh_interval: '5m', windows: { weekly: { soft_percent: 85 } } }
  const client = {
    async get() {
      return { data: { settings: structuredClone(settings), errors: [], revision: 'r1', home: '/h' } satisfies SettingsResponse, eventId: null }
    },
    async send(_m: string, _p: string, body: unknown, opts?: { ifMatch?: string }) {
      sent.push({ body: structuredClone(body), ifMatch: opts?.ifMatch })
      if (opts?.ifMatch !== 'r1') throw new ApiError(409, 'stale_settings', 'stale')
      settings = { ...settings, ...(body as Partial<General>) }
      return { data: { settings: structuredClone(settings), errors: [], revision: 'r2', home: '/h' } satisfies SettingsResponse, eventId: 3 }
    },
  } as unknown as ApiClient
  const form = createGeneralForm(['usage'], client, {
    store: { onEvent: () => () => {}, onSnapshot: () => () => {}, connection: ref('live' as const) },
  })
  await flushPromises()
  form.draft.value!.usage.windows = windowsOf([{ name: 'session', soft: '70', hard: '' }]).windows
  expect(await form.save()).toBe('saved')
  expect(sent).toEqual([{ body: { usage: { soft_percent: 90, hard_percent: 98, refresh_interval: '5m', windows: { session: { soft_percent: 70 } } } }, ifMatch: 'r1' }])
  form.dispose()
})

describe('the windows a per-window limit can choose', () => {
  const w = (name: string, label = '') => ({ name, label, used_percent: 1, resets_at: null })
  const readings = [
    { script: 'claude', windows: [w('session', 'Session (5 hours)'), w('weekly', 'Weekly, all models'), w('fable_weekly', 'Weekly, Fable')] },
    { script: 'codex', windows: [w('session', 'Session'), w('weekly', 'Weekly')] },
    { script: 'local', windows: [w('monthly')] },
  ]

  test('per script each reported window; a window several scripts report also for every script', () => {
    expect(windowChoices(readings, [], '', new Set())).toEqual([
      { value: 'claude/session', label: 'Claude: Session (5 hours) (session)' },
      { value: 'claude/weekly', label: 'Claude: Weekly, all models (weekly)' },
      { value: 'claude/fable_weekly', label: 'Claude: Weekly, Fable (fable_weekly)' },
      { value: 'codex/session', label: 'Codex: Session (session)' },
      { value: 'codex/weekly', label: 'Codex: Weekly (weekly)' },
      { value: 'local/monthly', label: 'Local: monthly' },
      { value: 'session', label: 'session of every script' },
      { value: 'weekly', label: 'weekly of every script' },
    ])
  })

  test("the agy script's two quota-group windows are offered with their labels", () => {
    const agy = { script: 'agy', windows: [w('gemini-weekly', 'Gemini models, weekly'), w('3p-weekly', 'Claude and GPT models, weekly')] }
    const got = windowChoices([...readings, agy], ['gemini-weekly', '3p-weekly'], '', new Set())
    expect(got).toContainEqual({ value: 'agy/gemini-weekly', label: 'Agy: Gemini models, weekly (gemini-weekly)' })
    expect(got).toContainEqual({ value: 'agy/3p-weekly', label: 'Agy: Claude and GPT models, weekly (3p-weekly)' })
  })

  test("models' windows nothing reports, and the row's own key when nothing reports it, are offered as not reported now", () => {
    const got = windowChoices(readings, ['fable_weekly', 'opus_weekly', 'weekly'], 'claude/opus_weekly', new Set())
    expect(got).toContainEqual({ value: 'opus_weekly', label: 'opus_weekly (not reported now)' })
    expect(got).toContainEqual({ value: 'claude/opus_weekly', label: 'Claude: opus_weekly (not reported now)' })
    // Reported by one script: its own entry is enough.
    expect(got.map((c) => c.value)).not.toContain('fable_weekly')
    expect(windowChoices([], [], 'weekly', new Set())).toEqual([{ value: 'weekly', label: 'weekly (not reported now)' }])
  })

  test('a key another row has is not offered again; the row keeps its own', () => {
    const got = windowChoices(readings, [], 'codex/weekly', new Set(['claude/weekly', 'codex/weekly', 'weekly']))
    const values = got.map((c) => c.value)
    expect(values).toContain('codex/weekly')
    expect(values).not.toContain('claude/weekly')
    expect(values).not.toContain('weekly')
  })

  test('keys in words; names from the scripts are shown as text', () => {
    expect(overrideKeyWords('claude/weekly', readings)).toBe('Claude: Weekly, all models (weekly)')
    expect(overrideKeyWords('weekly', readings)).toBe('weekly of every script')
    expect(overrideKeyWords('fable_weekly', readings)).toBe('Weekly, Fable (fable_weekly) of every script')
    expect(overrideKeyWords('x/a', [{ script: 'x', windows: [w('a', 'A‮b')] }])).toBe('X: A\\u{202E}b (a)')
  })

  test('a refusal of a scoped key is placed under its row', () => {
    expect(placedLimitField('usage.windows.claude/weekly.soft_percent')).toBe(true)
    expect(placedLimitField('usage.windows.my.script/weekly.hard_percent')).toBe(true)
    expect(overrideError({ 'usage.windows.my.script/weekly.hard_percent': 'no' }, 'my.script/weekly', 'hard')).toBe('no')
  })
})
