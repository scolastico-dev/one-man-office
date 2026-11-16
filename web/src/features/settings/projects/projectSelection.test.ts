import { expect, test } from 'vitest'
import { batchSummary, matchesFilter, rangeIds, selectionText } from './projectSelection'

test('the filter matches every word in the name or the folder, ignoring case', () => {
  const p = { name: 'Billing API', path: '/work/clients/acme/billing' }
  expect(matchesFilter(p, '')).toBe(true)
  expect(matchesFilter(p, '  ')).toBe(true)
  expect(matchesFilter(p, 'billing')).toBe(true)
  expect(matchesFilter(p, 'ACME')).toBe(true)
  expect(matchesFilter(p, 'api acme')).toBe(true)
  expect(matchesFilter(p, 'api other')).toBe(false)
})

test('a range runs either way and falls back to the row alone', () => {
  const order = ['a', 'b', 'c', 'd']
  expect(rangeIds(order, 'b', 'd')).toEqual(['b', 'c', 'd'])
  expect(rangeIds(order, 'd', 'b')).toEqual(['b', 'c', 'd'])
  expect(rangeIds(order, null, 'c')).toEqual(['c'])
  expect(rangeIds(order, 'gone', 'c')).toEqual(['c'])
  expect(rangeIds(order, 'a', 'gone')).toEqual([])
})

test('the words of a selection and of a batch answer', () => {
  expect(selectionText(0, 0)).toBe('None selected')
  expect(selectionText(3, 0)).toBe('3 selected')
  expect(selectionText(3, 1)).toBe('3 selected, 1 not shown')
  expect(batchSummary('remove', 52, 3)).toBe('Removed 52; 3 could not be removed.')
  expect(batchSummary('remove', 2, 0)).toBe('Removed 2.')
  expect(batchSummary('disable', 0, 1)).toBe('The project could not be switched off.')
  expect(batchSummary('enable', 0, 4)).toBe('All 4 could not be switched on.')
  expect(batchSummary('enable', 5, 1)).toBe('Switched on 5; 1 could not be switched on.')
})
