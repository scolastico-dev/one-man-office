import { expect, test } from 'vitest'
import { PLUGIN_STATES, SKILL_SET_STATES, shortCommit, stateWords } from './extensionWords'

test('a commit is shown with seven characters; bundled stays bundled', () => {
  expect(shortCommit('9c2e4f1aabbcc')).toBe('9c2e4f1')
  expect(shortCommit('bundled')).toBe('bundled')
  expect(shortCommit('')).toBe('')
})

test('states in words, the same way for skill sets and plugins; one the server adds later in its own words', () => {
  expect(stateWords(PLUGIN_STATES, 'disabled')).toEqual({ tone: 'muted', text: 'off' })
  expect(stateWords(PLUGIN_STATES, 'not_installed')).toEqual({ tone: 'warn', text: 'not installed' })
  expect(stateWords(SKILL_SET_STATES, 'ok')).toEqual({ tone: 'ok', text: 'checked out' })
  expect(stateWords(SKILL_SET_STATES, 'later_state')).toEqual({ tone: 'muted', text: 'later state' })
  expect(stateWords(PLUGIN_STATES, 'constructor').text).toBe('constructor')
})
