import { expect, test } from 'vitest'
import { LINE_BREAK, lineBreakKey } from './terminalKeys'

const key = (key: string, mods: Partial<Record<'shiftKey' | 'ctrlKey' | 'altKey' | 'metaKey', boolean>> = {}) =>
  ({ key, shiftKey: false, ctrlKey: false, altKey: false, metaKey: false, ...mods })

test('Shift+Enter and Ctrl+Enter are a line break (ESC CR), not a submit', () => {
  expect(LINE_BREAK).toBe('\x1b\r')
  expect(lineBreakKey(key('Enter', { shiftKey: true }))).toBe('\x1b\r')
  expect(lineBreakKey(key('Enter', { ctrlKey: true }))).toBe('\x1b\r')
  expect(lineBreakKey(key('Enter', { ctrlKey: true, shiftKey: true }))).toBe('\x1b\r')
})

test('Enter, Alt+Enter and Meta+Enter stay with xterm (CR, ESC CR)', () => {
  expect(lineBreakKey(key('Enter'))).toBeNull()
  expect(lineBreakKey(key('Enter', { altKey: true }))).toBeNull()
  expect(lineBreakKey(key('Enter', { shiftKey: true, altKey: true }))).toBeNull()
  expect(lineBreakKey(key('Enter', { metaKey: true }))).toBeNull()
})

test('other keys are not touched, Shift+Tab included (xterm sends ESC [ Z)', () => {
  expect(lineBreakKey(key('Tab', { shiftKey: true }))).toBeNull()
  expect(lineBreakKey(key('j', { ctrlKey: true }))).toBeNull()
  expect(lineBreakKey(key('a', { shiftKey: true }))).toBeNull()
})
