import { expect, test } from 'vitest'
import type { Model } from '../../api/types'
import { listText, modelItems, startsWithOther, withOnlyModel } from './ceoModel'
import { agent } from './sidebarTesting'

const model = (id: string, over: Partial<Model> = {}): Model => ({
  id,
  profile: id.split('/')[0]!,
  name: id.split('/')[1]!,
  cli: 'claude',
  description: '',
  selectable: true,
  ...over,
})

test('the button names the one model, a count for several, none for an empty list', () => {
  expect(listText(['claude/fable'])).toBe('claude/fable')
  expect(listText(['claude/fable', 'claude/fable'])).toBe('claude/fable')
  expect(listText(['claude/fable', 'codex/astra'])).toBe('2 models')
  expect(listText([])).toBe('none')
})

test('every model is offered, sorted, those agents may not choose too; the one in use is marked', () => {
  const items = modelItems([model('codex/astra'), model('claude/fable', { selectable: false, description: 'strongest' })], ['claude/fable'])
  expect(items).toEqual([
    { id: 'claude/fable', label: 'claude/fable (in use)', detail: 'strongest' },
    { id: 'codex/astra', label: 'codex/astra' },
  ])
  expect(modelItems([model('a/x')], ['a/x', 'b/y']).map((i) => i.label)).toEqual(['a/x'])
})

test('a choice makes the model the list’s only entry and keeps the assignment', () => {
  expect(withOnlyModel({ name: 'ceo', models: ['a/x', 'b/y'], assignment: 'failover', revision: 'r1' }, 'c/z')).toEqual({
    name: 'ceo',
    models: ['c/z'],
    assignment: 'failover',
  })
})

test('choosing a CEO model keeps its configured reset weight', () => {
  const role = { name: 'ceo', models: ['a/x'], assignment: 'super_smart', super_smart_reset_weight: '6h' }
  expect(withOnlyModel(role, 'c/z')).toEqual({ ...role, models: ['c/z'] })
})

test('a running CEO on a model the list no longer names starts with another one next time', () => {
  const live = agent({ role: 'ceo', model: 'a/x' })
  expect(startsWithOther(live, ['b/y'])).toBe(true)
  expect(startsWithOther(live, ['a/x', 'b/y'])).toBe(false)
  expect(startsWithOther(null, ['b/y'])).toBe(false)
  expect(startsWithOther(live, [])).toBe(false)
})
