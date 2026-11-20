import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import type { Model, RoleSettings } from '../../../api/types'
import { harness, mountWith, ok, refuse, type Harness } from '../../create/dialogTesting'
import RolesPage from './RolesPage.vue'

/** One role's form on the page. */
type Form = ReturnType<VueWrapper['get']>

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

/** The Roles page with the developer's role stored as given; `w` is the developer's form. */
async function open(models: string[], assignment = 'round_robin', extra: Partial<RoleSettings> = {}): Promise<{ w: Form; page: VueWrapper; h: Harness }> {
  const h = await harness()
  h.respond.models = MODELS
  h.respond.roles = [{ name: 'developer', models, assignment, revision: 'abc123', ...extra }]
  h.respond.write = ({ body }) => ok({ ...(body as object), revision: 'def456' })
  const page = await mountWith(h, RolesPage, {})
  mounted = page
  await flushPromises()
  return { w: page.get('[data-role-editor="developer"]'), page, h }
}

const shown = (w: Form) => w.findAll('[data-model]').map((m) => m.attributes('data-model'))
const button = (w: Form, text: string) => {
  const b = w.findAll('button').find((x) => x.attributes('aria-label')?.startsWith(text))
  if (!b) throw new Error(`no button ${text}`)
  return b
}
/** The page's one Save bar. */
const bar = () => mounted!.get('[role="region"][aria-label="Save changes"]')
const save = async () => {
  await bar().findAll('button').find((b) => b.text() === 'Save')!.trigger('click')
  await flushPromises()
}

test('super smart reset weight loads, saves, survives mode changes and can return to the default', async () => {
  const { w, h } = await open(['claude/sonnet'], 'super_smart', { super_smart_reset_weight: '6h' })
  const input = () => w.get('input[data-reset-weight]')
  expect((input().element as HTMLInputElement).value).toBe('6h')
  await input().setValue('0s')
  await w.get('select[data-assignment]').setValue('smart')
  expect(w.find('input[data-reset-weight]').exists()).toBe(false)
  await save()
  expect(h.sent.at(-1)?.body).toMatchObject({ super_smart_reset_weight: '0s' })
  await w.get('select[data-assignment]').setValue('super_smart')
  expect((input().element as HTMLInputElement).value).toBe('0s')
  await input().setValue('48h')
  await save()
  expect(h.sent.at(-1)?.body).toMatchObject({ super_smart_reset_weight: '48h' })
  await input().setValue('')
  await save()
  expect(h.sent.at(-1)?.body).not.toHaveProperty('super_smart_reset_weight')
  expect(w.text()).toContain('24h')
})

test('a reset weight refusal is shown next to the duration input', async () => {
  const { w, h } = await open([], 'super_smart')
  await w.get('input[data-reset-weight]').setValue('-1h')
  h.respond.write = () => refuse('validation', 'Reset weight must not be negative', { field: 'super_smart_reset_weight' })
  await save()
  expect(w.get('input[data-reset-weight]').attributes('aria-invalid')).toBe('true')
  expect(w.text()).toContain('Reset weight must not be negative')
})

test('the models are shown in the stored order, repeats as weights', async () => {
  const { w } = await open(['claude/sonnet', 'claude/opus', 'claude/sonnet'], 'random')
  expect(shown(w)).toEqual(['claude/sonnet', 'claude/opus', 'claude/sonnet'])
  expect(w.findAll('.role-model__weight').map((x) => x.text())).toEqual(['×2', '×2'])
  expect(w.text()).toContain('Everyday')
  expect(w.text()).toContain('How it picks: at random')
})

const NOTE = 'With round robin and random, a model listed twice is picked twice as often; with failover, smart and super smart it counts once.'

test('the note on repeated models is shown once, above every role card and in none, in every assignment mode', async () => {
  for (const mode of ['round_robin', 'random']) {
    const { page } = await open(['claude/sonnet', 'claude/sonnet'], mode)
    const notes = page.findAll('[data-roles-note]')
    expect(notes).toHaveLength(1)
    expect(notes[0]!.text()).toContain(NOTE)
    expect(page.text().split(NOTE)).toHaveLength(2)
    const cards = page.findAll('[data-role-editor]')
    expect(cards).toHaveLength(4)
    for (const card of cards) {
      expect(card.element.contains(notes[0]!.element)).toBe(false)
      expect(card.text()).not.toContain(NOTE)
      expect(notes[0]!.element.compareDocumentPosition(card.element) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
    }
    page.unmount()
    mounted = null
  }
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
  await save()
  expect(h.sent).toEqual([
    { method: 'PUT', path: '/api/roles/developer', body: { name: 'developer', models: ['claude/haiku', 'claude/opus'], assignment: 'round_robin' }, ifMatch: 'abc123' },
  ])
  expect(bar().text()).toContain('Saved')
})

test('an unknown model id is marked and can be removed; a model agents may not choose for tickets is not', async () => {
  const { w, h } = await open(['claude/gone', 'claude/fable', 'claude/sonnet'])
  const gone = w.get('[data-model="claude/gone"]')
  expect(gone.attributes('data-mark')).toBe('missing')
  expect(gone.text()).toContain('does not exist')
  expect(w.get('[data-model="claude/fable"]').attributes('data-mark')).toBe('ok')
  expect(w.get('[data-model="claude/fable"]').text()).not.toContain('switched off')
  expect(w.get('[data-model="claude/sonnet"]').attributes('data-mark')).toBe('ok')
  await button(w, 'Remove claude/gone').trigger('click')
  expect(shown(w)).toEqual(['claude/fable', 'claude/sonnet'])
  await save()
  expect((h.sent[0]?.body as { models: string[] }).models).toEqual(['claude/fable', 'claude/sonnet'])
})

test('a refusal naming an entry shows under the list, verbatim, and the assignment refusal under its field', async () => {
  const { w, h } = await open(['claude/sonnet'])
  h.respond.write = () => refuse('validation', 'unknown model “claude/sonnet”', { field: 'models.0' })
  await w.get('select[data-assignment]').setValue('failover')
  await save()
  expect(w.get('[data-field-error="models"]').text()).toBe('unknown model “claude/sonnet”')
  expect(w.find('[aria-invalid="true"]').exists()).toBe(true)
  expect(w.get('[data-model="claude/sonnet"]').text()).toContain('refused')
  h.respond.write = () => refuse('validation', 'assignment must be one of …', { field: 'assignment' })
  await save()
  expect(w.get('select[data-assignment]').attributes('aria-invalid')).toBe('true')
  expect(w.text()).toContain('assignment must be one of …')
})

test('a refusal naming an entry by its place is dropped when the entries move, not shown on another one', async () => {
  const { w, h } = await open(['claude/sonnet', 'claude/opus'])
  h.respond.write = () => refuse('validation', 'unknown model “claude/opus”', { field: 'models.1' })
  await w.get('select[data-assignment]').setValue('failover')
  await save()
  expect(w.get('[data-model="claude/opus"]').text()).toContain('refused')
  await button(w, 'Move claude/opus up').trigger('click')
  await flushPromises()
  expect(shown(w)).toEqual(['claude/opus', 'claude/sonnet'])
  expect(w.get('[data-model="claude/sonnet"]').text()).not.toContain('refused')
  expect(w.find('[data-field-error="models"]').exists()).toBe(false)
})

test('each assignment is explained in words', async () => {
  const { w } = await open([], 'failover')
  expect(w.text()).toContain('the next only when it cannot be used')
  await w.get('select[data-assignment]').setValue('smart')
  expect(w.text()).toContain('otherwise like round robin')
  expect(w.text()).toContain('No models yet.')
})

test('a switched-off CEO says it needs no model and links to General', async () => {
  // The test server's CEO is switched off.
  const h = await harness()
  h.respond.models = MODELS
  h.respond.roles = [{ name: 'ceo', models: [], assignment: 'round_robin', revision: '0' }]
  mounted = await mountWith(h, RolesPage, {})
  await flushPromises()
  const w = mounted.get('[data-role-editor="ceo"]')
  expect(w.get('[data-role-off]').text()).toContain('The CEO is switched off; it needs no model.')
  expect(w.get('[data-role-off] a').attributes('href')).toBe('/settings/general')
})

test('a refusal naming a field the editor does not show is shown above the form, like on every settings page', async () => {
  const { w, h } = await open(['claude/sonnet'])
  h.respond.write = () => refuse('validation', 'name must be the role of this file', { field: 'name' })
  await w.get('select[data-assignment]').setValue('failover')
  await save()
  expect(w.get('[data-form-errors]').text()).toContain('name must be the role of this file')
})

test('one Save bar for the page saves every changed role; a refusal and a conflict stay under their roles, the others are saved', async () => {
  const { page, h } = await open(['claude/sonnet'])
  h.respond.roles = [
    { name: 'pm', models: [], assignment: 'round_robin', revision: 'p1' },
    { name: 'developer', models: ['claude/sonnet'], assignment: 'round_robin', revision: 'abc123' },
    { name: 'reviewer', models: [], assignment: 'round_robin', revision: 'r1' },
  ]
  // Only one Save bar on the page.
  expect(page.findAll('[role="region"]').filter((r) => r.attributes('aria-label')?.startsWith('Save changes'))).toHaveLength(1)
  const role = (name: string) => page.get(`[data-role-editor="${name}"]`)
  for (const name of ['pm', 'developer', 'reviewer']) {
    await role(name).get('select').setValue('claude/opus')
    await role(name).get('[data-add-model]').trigger('click')
  }
  expect(bar().text()).toContain('Unsaved changes')
  h.respond.write = ({ path, body }) => {
    if (path === '/api/roles/pm') return refuse('validation', 'unknown model “claude/opus”', { field: 'models.0' })
    if (path === '/api/roles/developer') return refuse('stale_settings', 'changed meanwhile', undefined, 409)
    return ok({ ...(body as object), revision: 'next' })
  }
  // The developer's file changed elsewhere meanwhile.
  h.respond.roles = [{ name: 'developer', models: ['claude/haiku'], assignment: 'failover', revision: 'abc999' }]
  await save()
  // In the page's order, each role once; the CEO was not changed.
  expect(h.sent.map((x) => x.path)).toEqual(['/api/roles/pm', '/api/roles/developer', '/api/roles/reviewer'])
  // The reviewer, after the refused one, is saved.
  expect(role('reviewer').find('[data-role-error]').exists()).toBe(false)
  // The refusal is under the product manager's list, the conflict on the developer's form.
  expect(role('pm').get('[data-field-error="models"]').text()).toBe('unknown model “claude/opus”')
  expect(role('developer').get('[data-role-conflict]').text()).toContain('This changed outside this page')
  expect(role('developer').find('[data-use-theirs]').exists()).toBe(true)
  expect(role('pm').find('[data-role-conflict]').exists()).toBe(false)
  expect(bar().text()).toContain('Changed outside this page')
  expect(bar().get('[data-save-error]').text()).toBe('Saved the reviewer. Not saved: the product manager and the developer; see the message under each.')
  // Use the new value takes the stored list for the developer only.
  await role('developer').get('[data-use-theirs]').trigger('click')
  expect(shown(role('developer'))).toEqual(['claude/haiku'])
  expect(shown(role('pm'))).toEqual(['claude/opus'])
})

test('super smart can be selected and saved as a known assignment', async () => {
  const { w, h } = await open(['claude/sonnet'])
  expect(w.get('select[data-assignment] option[value="super_smart"]').text()).toContain('Super smart')
  await w.get('select[data-assignment]').setValue('super_smart')
  expect(w.text()).toContain('reset')
  await save()
  expect(h.sent.at(-1)?.body).toEqual({ name: 'developer', models: ['claude/sonnet'], assignment: 'super_smart' })
})

test('missing roles use their role-specific defaults in the editor', async () => {
  const h = await harness()
  h.respond.roles = []
  mounted = await mountWith(h, RolesPage, {})
  await flushPromises()
  for (const [name, mode] of Object.entries({ ceo: 'failover', pm: 'super_smart', developer: 'super_smart', reviewer: 'random' })) {
    expect((mounted.get(`[data-role-editor="${name}"] select[data-assignment]`).element as HTMLSelectElement).value).toBe(mode)
  }
})
