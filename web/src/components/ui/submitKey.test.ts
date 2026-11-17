import { expect, test } from 'vitest'
import { submitKeyName } from './submitKey'

test('the submit key is named for the platform', () => {
  expect(submitKeyName(true)).toBe('Cmd+Enter')
  expect(submitKeyName(false)).toBe('Ctrl+Enter')
})
