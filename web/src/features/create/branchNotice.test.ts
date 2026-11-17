import { expect, test } from 'vitest'
import { branchNotice } from './branchNotice'

test('a notice names both branches only when they differ and both are known', () => {
  expect(branchNotice({ current_branch: 'feature-x', default_branch: 'main' })).toEqual({ current: 'feature-x', default: 'main' })
  expect(branchNotice({ current_branch: 'main', default_branch: 'main' })).toBeNull()
  expect(branchNotice({ current_branch: 'feature-x' })).toBeNull()
  expect(branchNotice({ current_branch: 'feature-x', default_branch: '' })).toBeNull()
  expect(branchNotice({ default_branch: 'main' })).toBeNull()
  expect(branchNotice({ current_branch: '', default_branch: 'main' })).toBeNull()
  expect(branchNotice({})).toBeNull()
})
