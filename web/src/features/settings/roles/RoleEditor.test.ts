import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import type { Model } from '../../../api/types'
import { harness, mountWith, ok, refuse, type Harness } from '../../create/dialogTesting'
import RoleEditor from './RoleEditor.vue'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
})

const model = (id: string, selectable = true, description = ''): Model => {
  const [profile = '', name = ''] = id.split('/')
  return { id, profile, name, cli: 'claude', description, selectable }
}
const MODELS = [model('claude/sonnet', true, 'Everyday'), model('claude/opus'), model('claude/haiku'), model('claude/fable', false)]

async function open(models: string[], assignment = 'round_robin'): Promise<{ w: VueWrapper; h: Harness }> {
  const h = await harness()
  h.respond.models = MODELS
  h.respond.roles = [{ name: 'developer', models, assignment, revision: 'abc123' }]
  h.respond.write = ({ body }) => ok({ ...(body as object), revision: 'def456' })
  const w = await mountWith(h, RoleEditor, { role: 'developer', models: MODELS })
  mounted = w
  await flushPromises()
  return { w, h }
}

const shown = (w: VueWrapper) => w.findAll('[data-model]').map((m) => m.attributes('data-model'))
const button = (w: VueWrapper, text: string) => {
  const b = w.findAll('button').find((x) => x.attributes('aria-label')?.startsWith(text))
  if (!b) throw new Error(`no button ${text}`)
  return b
}
const save = async (w: VueWrapper) => {
  const bar = w.get('[role="region"]')
  await bar.findAll('button').find((b) => b.text() === 'Save')!.trigger('click')
  await flushPromises()
}

test('the models are shown in the stored order, repeats as weights', async () => {
  const { w } = await open(['claude/sonnet', 'claude/opus', 'claude/sonnet'], 'random')
  expect(shown(w)).toEqual(['claude/sonnet', 'claude/opus', 'claude/sonnet'])
  expect(w.findAll('.role-model__weight').map((x) => x.text())).toEqual(['×2', '×2'])
  expect(w.text()).toContain('Everyday')
  expect(w.text()).toContain('A model listed twice is picked twice as often.')
  expect(w.text()).toContain('How it picks: at random')
})

test('add, move and remove send the list in the shown order, with the revision the form was built on', async () => {
  const { w, h } = await open(['claude/sonnet', 'claude/opus'])
  await w.get('select').setValue('claude/haiku')
  await w.get('[data-add-model]').trigger('click')
  expect(shown(w)).toEqual(['claude/sonnet', 'claude/opus', 'claude/haiku'])
  await button(w, 'Move claude/haiku up').trigger('click')
  expect(shown(w)).toEqual(['claude/sonnet', 'claude/haiku', 'claude/opus'])
  await button(w, 'Remove claude/sonnet').trigger('click')
  expect(shown(w)).toEqual(['claude/haiku', 'claude/opus'])
  await save(w)
  expect(h.sent).toEqual([{ method: 'PUT', path: '/api/roles/developer', body: { name: 'developer', models: ['claude/haiku', 'claude/opus'], assignment: 'round_robin' } }])
  expect(w.text()).toContain('Saved')
})

test('an unknown model id is marked and can be removed; a model switched off is marked too', async () => {
  const { w, h } = await open(['claude/gone', 'claude/fable', 'claude/sonnet'])
  const gone = w.get('[data-model="claude/gone"]')
  expect(gone.attributes('data-mark')).toBe('missing')
  expect(gone.text()).toContain('does not exist')
  expect(w.get('[data-model="claude/fable"]').text()).toContain('switched off in its profile')
  expect(w.get('[data-model="claude/sonnet"]').attributes('data-mark')).toBe('ok')
  await button(w, 'Remove claude/gone').trigger('click')
  expect(shown(w)).toEqual(['claude/fable', 'claude/sonnet'])
  await save(w)
  expect((h.sent[0]?.body as { models: string[] }).models).toEqual(['claude/fable', 'claude/sonnet'])
})

test('a refusal naming an entry shows under the list, verbatim, and the assignment refusal under its field', async () => {
  const { w, h } = await open(['claude/sonnet'])
  h.respond.write = () => refuse('validation', 'unknown model “claude/sonnet”', { field: 'models.0' })
  await w.get('select[data-assignment]').setValue('failover')
  await save(w)
  expect(w.get('[data-field-error="models"]').text()).toBe('unknown model “claude/sonnet”')
  expect(w.find('[aria-invalid="true"]').exists()).toBe(true)
  expect(w.get('[data-model="claude/sonnet"]').text()).toContain('refused')
  h.respond.write = () => refuse('validation', 'assignment must be one of …', { field: 'assignment' })
  await save(w)
  expect(w.get('select[data-assignment]').attributes('aria-invalid')).toBe('true')
  expect(w.text()).toContain('assignment must be one of …')
})

test('each assignment is explained in words', async () => {
  const { w } = await open([], 'failover')
  expect(w.text()).toContain('the next only when it cannot be used')
  await w.get('select[data-assignment]').setValue('smart')
  expect(w.text()).toContain('otherwise like round robin')
  expect(w.text()).toContain('No models yet.')
})

test('a switched-off CEO says it needs no model and links to General', async () => {
  const h = await harness()
  h.respond.roles = [{ name: 'ceo', models: [], assignment: 'round_robin', revision: '0' }]
  const w = await mountWith(h, RoleEditor, { role: 'ceo', models: MODELS, enabled: false })
  mounted = w
  await flushPromises()
  expect(w.get('[data-role-off]').text()).toContain('The CEO is switched off; it needs no model.')
  expect(w.get('[data-role-off] a').attributes('href')).toBe('/settings/general')
})
