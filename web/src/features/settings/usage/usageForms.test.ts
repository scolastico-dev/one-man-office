import { flushPromises } from '@vue/test-utils'
import { expect, test } from 'vitest'
import { ref } from 'vue'
import { ApiError, type ApiClient } from '../../../api/client'
import type { General, SettingsResponse } from '../../../api/types'
import { general } from '../../../testing/fakeOmo'
import { createGeneralForm } from '../general/generalForms'
import {
  ENTER_NAME,
  ENTER_NUMBER,
  NAME_TWICE,
  errorsOfScript,
  fileOfScript,
  overrideError,
  parsePercent,
  placedLimitField,
  rowsOf,
  scriptKind,
  testProfiles,
  thresholdsOf,
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
  expect(problems).toEqual({ '0.name': ENTER_NAME, '1.soft': ENTER_NUMBER, '2.name': NAME_TWICE, '2.hard': ENTER_NUMBER })
  expect(windows).toEqual({ weekly: {} })
  expect(parsePercent(' ')).toBeUndefined()
  expect(parsePercent('100')).toBe(100)
  expect(parsePercent('1e2')).toBeNull()
})

test('usageForms: refusals land under the window and field the server names', () => {
  const fe = { 'usage.windows.weekly.hard_percent': 'too low', 'usage.soft_percent': 'bad' }
  expect(overrideError(fe, 'weekly', 'hard')).toBe('too low')
  expect(overrideError(fe, 'weekly', 'soft')).toBeUndefined()
  expect(placedLimitField('usage.soft_percent')).toBe(true)
  expect(placedLimitField('usage.windows')).toBe(true)
  expect(placedLimitField('usage.windows.weekly.soft_percent')).toBe(true)
  expect(placedLimitField('max_running_agents')).toBe(false)
})

test('usageForms: thresholds, kinds, files and profiles', () => {
  const usage = { soft_percent: 90, hard_percent: 98, windows: { fable_weekly: { soft_percent: 80 } } }
  expect(thresholdsOf('fable_weekly', usage)).toEqual({ soft: 80, hard: 98 })
  expect(thresholdsOf('session', usage)).toEqual({ soft: 90, hard: 98 })
  expect(thresholdsOf('session', null)).toEqual({})
  expect(scriptKind({ has_default: true, customized: false })).toBe("omo's default")
  expect(scriptKind({ has_default: true, customized: true })).toBe('Customized')
  expect(scriptKind({ has_default: false, customized: true })).toBe('Yours')
  expect(fileOfScript('/h/omo/usage/claude.lua')).toBe('claude')
  expect(fileOfScript('/h/omo/prompts/claude.md')).toBeNull()
  expect(errorsOfScript([{ file: '/h/omo/usage/a.lua', message: 'x' }, { file: '/h/omo/usage/b.lua', message: 'y' }], 'b')).toEqual([{ file: '/h/omo/usage/b.lua', message: 'y' }])
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
