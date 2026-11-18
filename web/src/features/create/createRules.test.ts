import { expect, test } from 'vitest'
import { ApiError } from '../../api/client'
import { problemOf } from './createRules'

test('field placement uses structured details regardless of the displayed message', () => {
  expect(problemOf(new ApiError(422, 'validation', 'Different wording', { field: 'model' }))).toEqual({ field: 'model', message: 'Different wording' })
  expect(problemOf(new ApiError(422, 'validation', 'the title must be valid'))).toEqual({ field: null, message: 'the title must be valid' })
  expect(problemOf(new ApiError(422, 'validation', 'model "x" is unavailable', { field: 'unknown' }))).toEqual({ field: null, message: 'model "x" is unavailable' })
})
