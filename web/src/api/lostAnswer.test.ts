import { expect, test } from 'vitest'
import { ApiError } from './client'
import { failureText, isLostAnswer } from './lostAnswer'

const lost = new ApiError(0, 'network', 'Cannot reach omo. Check that it is still running.')

test('only the client\'s network error is a lost answer', () => {
  expect(isLostAnswer(lost)).toBe(true)
  expect(isLostAnswer(new ApiError(409, 'conflict', 'busy'))).toBe(false)
  expect(isLostAnswer(new Error('network'))).toBe(false)
})

test('a lost answer says the change may have happened; a refusal keeps the server words', () => {
  const o = { lost: 'It may have happened.', refused: 'omo refused it.' }
  expect(failureText(lost, o)).toBe('It may have happened.')
  expect(failureText(new ApiError(409, 'conflict', 'omo is merging #3'), o)).toBe('omo is merging #3')
  expect(failureText('odd', o)).toBe('omo refused it.')
})
