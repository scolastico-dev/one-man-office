import { expect, test } from 'vitest'
import { MemoryStorage } from '../../../testing/fakeOmo'
import { FILES_WIDTH, PAGE_PREFS_KEY, readPagePrefs } from './pagePrefs'

const stored = (value: string | null) => {
  const s = new MemoryStorage()
  if (value !== null) s.setItem(PAGE_PREFS_KEY, value)
  return s
}

test('without storage or a stored choice: details and files shown, the file list at its first width', () => {
  const defaults = { details: true, files: true, filesWidth: FILES_WIDTH.initial }
  expect(readPagePrefs(null)).toEqual(defaults)
  expect(readPagePrefs(stored(null))).toEqual(defaults)
  expect(readPagePrefs(stored('not json'))).toEqual(defaults)
  expect(readPagePrefs(stored('"a string"'))).toEqual(defaults)
})

test('stored choices are read back, each checked on its own; a width is kept within its bounds', () => {
  expect(readPagePrefs(stored(JSON.stringify({ details: false, files: false, filesWidth: 420 })))).toEqual({ details: false, files: false, filesWidth: 420 })
  expect(readPagePrefs(stored(JSON.stringify({ details: 'no', filesWidth: 5 })))).toEqual({ details: true, files: true, filesWidth: FILES_WIDTH.min })
  expect(readPagePrefs(stored(JSON.stringify({ filesWidth: 99999 }))).filesWidth).toBe(FILES_WIDTH.max)
  expect(readPagePrefs(stored(JSON.stringify({ filesWidth: null }))).filesWidth).toBe(FILES_WIDTH.initial)
})
