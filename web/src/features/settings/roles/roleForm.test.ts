import { expect, test } from 'vitest'
import type { Model } from '../../../api/types'
import { addModel, addOptions, assignmentMeaning, assignmentOptions, markOf, modelErrorIndex, modelIndex, moveModel, removeModel, rows, weightText } from './roleForm'

const model = (id: string, selectable = true, description = ''): Model => {
  const [profile = '', name = ''] = id.split('/')
  return { id, profile, name, cli: 'claude', description, selectable }
}

test('repeats are shown as weights and the order is kept', () => {
  const list = ['claude/sonnet', 'claude/opus', 'claude/sonnet', 'codex/default']
  expect(weightText(list, 'claude/sonnet')).toBe('×2')
  expect(weightText(list, 'claude/opus')).toBe('')
  const shown = rows(list, modelIndex([model('claude/sonnet', true, 'Everyday'), model('claude/opus')]))
  expect(shown.map((r) => r.id)).toEqual(list)
  expect(shown[0]?.note).toBe('Everyday')
  expect(shown[3]?.note).toBe('')
})

test('marks: missing, switched off, fine; nothing is marked before the models are read', () => {
  const known = modelIndex([model('claude/sonnet'), model('claude/fable', false)])
  expect(markOf('claude/sonnet', known)).toBe('ok')
  expect(markOf('claude/fable', known)).toBe('off')
  expect(markOf('claude/gone', known)).toBe('missing')
  expect(markOf('claude/gone', null)).toBe('ok')
})

test('add, move and remove change only what they name', () => {
  const list = ['a/x', 'b/y', 'c/z']
  expect(addModel(list, 'a/x')).toEqual(['a/x', 'b/y', 'c/z', 'a/x'])
  expect(moveModel(list, 2, 0)).toEqual(['c/z', 'a/x', 'b/y'])
  expect(moveModel(list, 0, 1)).toEqual(['b/y', 'a/x', 'c/z'])
  expect(moveModel(list, 0, 5)).toEqual(list)
  expect(removeModel(['a/x', 'a/x', 'b/y'], 1)).toEqual(['a/x', 'b/y'])
  expect(list).toEqual(['a/x', 'b/y', 'c/z'])
})

test('the add options list every model and mark those switched off', () => {
  const options = addOptions([model('claude/sonnet'), model('claude/fable', false), model('claude/haiku')])
  expect(options.map((o) => o.value)).toEqual(['claude/fable', 'claude/haiku', 'claude/sonnet'])
  expect(options[0]?.label).toBe('claude/fable (switched off in its profile)')
  expect(options[1]?.label).toBe('claude/haiku')
})

test('every assignment is explained in words; an unknown one is kept', () => {
  expect(assignmentOptions('failover').map((o) => o.value)).toEqual(['round_robin', 'random', 'failover', 'smart'])
  expect(assignmentOptions('odd').map((o) => o.value)).toEqual(['round_robin', 'random', 'failover', 'smart', 'odd'])
  expect(assignmentMeaning('failover')).toContain('the next only when it cannot be used')
  expect(assignmentMeaning('round_robin')).toContain('in turn')
  expect(assignmentMeaning('smart')).toContain('otherwise like round robin')
  expect(assignmentMeaning('odd')).toBe('')
})

test('a refusal names a list entry', () => {
  expect(modelErrorIndex('models.3')).toBe(3)
  expect(modelErrorIndex('assignment')).toBe(-1)
})
