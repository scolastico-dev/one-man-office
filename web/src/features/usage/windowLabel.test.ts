import { describe, expect, test } from 'vitest'
import { labelsOf, modelWindowWords, nameWithLabel, reportedWords, windowTitle, windowWithName } from './windowLabel'

const fable = { name: 'fable_weekly', label: 'Weekly, Fable', used_percent: 43, resets_at: null }
const plain = { name: 'weekly', label: '', used_percent: 85, resets_at: null }

describe('window labels', () => {
  test('a window reads as its label, else its name', () => {
    expect(windowTitle(fable)).toBe('Weekly, Fable')
    expect(windowTitle(plain)).toBe('weekly')
    expect(windowTitle({ name: 'session' })).toBe('session')
  })

  test('with the name: "label (name)" where there is a label', () => {
    expect(windowWithName(fable)).toBe('Weekly, Fable (fable_weekly)')
    expect(windowWithName(plain)).toBe('weekly')
  })

  test('hidden characters in a label are shown as escapes', () => {
    expect(windowTitle({ name: 'a', label: 'Week‮ly' })).toBe('Week\\u{202E}ly')
    expect(windowWithName({ name: 'a', label: 'x​y' })).toBe('x\\u{200B}y (a)')
  })

  test('labels of the current readings by window name; the first labelled one wins', () => {
    const labels = labelsOf([{ windows: [plain, fable] }, { windows: [{ ...fable, label: 'Other' }, { name: 'session', label: 'Session (5 hours)', used_percent: 1, resets_at: null }] }])
    expect([...labels]).toEqual([
      ['fable_weekly', 'Weekly, Fable'],
      ['session', 'Session (5 hours)'],
    ])
    expect(nameWithLabel('fable_weekly', labels)).toBe('Weekly, Fable (fable_weekly)')
    expect(nameWithLabel('weekly', labels)).toBe('weekly')
    expect(labelsOf(null).size).toBe(0)
  })

  test('the windows a profile reports, for the model editor', () => {
    const readings = [{ profiles: ['claude', 'claude-2'], windows: [plain, fable] }]
    expect(reportedWords('claude-2', readings)).toBe('Its script reports now: weekly; Weekly, Fable (fable_weekly).')
    expect(reportedWords('codex', readings)).toBe('')
    expect(reportedWords('claude', [{ profiles: ['claude'], windows: [] }])).toBe('')
  })
})

test('modelWindowWords: the label with the fill, nothing without a label', () => {
  expect(modelWindowWords({ window_label: 'Weekly, Fable', used_percent: 98.6 })).toBe('Weekly, Fable at 99 %')
  expect(modelWindowWords({ window_label: 'Weekly, Fable', used_percent: null })).toBe('Weekly, Fable')
  expect(modelWindowWords({ window_label: '', used_percent: 50 })).toBe('')
})
