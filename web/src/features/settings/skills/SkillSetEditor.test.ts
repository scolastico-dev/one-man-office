import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import type { SkillSet, SkillSetState, SkillSetsResponse } from '../../../api/types'
import { harness, mountWith, ok, refuse, type Harness } from '../../create/dialogTesting'
import SkillSetEditor from './SkillSetEditor.vue'
import { superpowersPreset } from './skillForms'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
})

const set = (o: Partial<SkillSet> = {}): SkillSet => ({
  name: 'sp',
  repository: 'https://example.invalid/sp.git',
  ref: 'main',
  update: 'at_start',
  enabled: true,
  instructions: { developer: 'Use TDD.' },
  revision: 'rev1',
  ...o,
})
const state = (o: Partial<SkillSetState> = {}): SkillSetState => ({ name: 'sp', state: 'ok', commit: 'a41c9e0123456', path: '/home/x/skills/sp', updated_at: '2026-11-15T10:00:00Z', message: '', ...o })

async function open(sets: SkillSet[], props: Record<string, unknown> = {}): Promise<{ w: VueWrapper; h: Harness; read: { value: SkillSetsResponse } }> {
  const h = await harness()
  const read = { value: { skill_sets: sets, states: [], errors: [] } as SkillSetsResponse }
  const client = h.session.client
  const baseGet = client.get.bind(client)
  client.get = ((path: string, q?: never) => (path === '/api/skillsets' ? Promise.resolve(ok(read.value)) : baseGet(path, q))) as typeof client.get
  h.respond.write = ({ body }) => ok({ ...(body as object), revision: 'rev2' })
  const w = await mountWith(h, SkillSetEditor, { name: 'sp', ...props })
  mounted = w
  await flushPromises()
  return { w, h, read }
}

const bar = (w: VueWrapper) => w.get('[role="region"]')
const save = async (w: VueWrapper) => {
  await bar(w).findAll('button').find((b) => b.text() === 'Save')!.trigger('click')
  await flushPromises()
}

test('save sends If-Match with the revision the form was read with, and only roles with a text', async () => {
  const { w, h } = await open([set()])
  await w.get('[data-skillset-text="reviewer"]').setValue('Check the diff.')
  await save(w)
  expect(h.sent).toEqual([
    {
      method: 'PUT',
      path: '/api/skillsets/sp',
      body: { name: 'sp', repository: 'https://example.invalid/sp.git', ref: 'main', update: 'at_start', enabled: true, instructions: { developer: 'Use TDD.', reviewer: 'Check the diff.' } },
      ifMatch: 'rev1',
    },
  ])
  expect(w.text()).toContain('Saved')
})

test('a set not stored yet starts unsaved and is created with If-Match "0"', async () => {
  const { w, h } = await open([], { initial: superpowersPreset('sp') })
  expect(w.text()).toContain('(not saved yet)')
  expect(bar(w).text()).toContain('Unsaved changes')
  expect(w.find('[data-skillset-delete]').exists()).toBe(false)
  expect(w.find('[data-skillset-update]').exists()).toBe(false)
  await save(w)
  expect(h.sent[0]?.ifMatch).toBe('0')
  expect((h.sent[0]?.body as SkillSet).repository).toBe('https://github.com/obra/superpowers.git')
})

test('a refused repository shows the server message under it; one naming a key the form lacks is shown at the top', async () => {
  const { w, h } = await open([set()])
  h.respond.write = () => refuse('validation', 'repository must be an https:// or ssh:// address', { field: 'repository' })
  await w.get('[data-skillset-repository]').setValue('ext::sh -c boom')
  await save(w)
  const field = w.get('[data-skillset-repository]').element.closest('.omo-field')!
  expect(field.textContent).toContain('repository must be an https:// or ssh:// address')
  expect(w.get('[data-skillset-repository]').attributes('aria-invalid')).toBe('true')
  expect(w.find('[data-form-errors]').exists()).toBe(false)

  h.respond.write = () => refuse('validation', 'instructions.developer must be at most 16 KiB', { field: 'instructions.developer' })
  await save(w)
  expect(w.get('[data-skillset-text="developer"]').attributes('aria-invalid')).toBe('true')
  expect(w.text()).toContain('instructions.developer must be at most 16 KiB')

  h.respond.write = () => refuse('validation', 'the skill set name must match ^[a-z0-9]$', { field: 'name' })
  await save(w)
  expect(w.get('[data-form-errors] .callout__body').text()).toBe('the skill set name must match ^[a-z0-9]$')
})

test('update now shows updating, then the result; a failure shows the cleaned message as text', async () => {
  const states: SkillSetState[] = []
  const { w, h } = await open([set()], { state: state(), onState: (s: SkillSetState) => states.push(s) })
  let release!: (s: SkillSetState) => void
  h.respond.write = () => new Promise((res) => (release = (s) => res(ok(s))))
  await w.get('[data-skillset-update]').trigger('click')
  const btn = w.get('[data-skillset-update]')
  expect(btn.text()).toBe('Updating…')
  expect(btn.attributes('disabled')).toBeDefined()
  expect(h.sent.at(-1)).toMatchObject({ method: 'POST', path: '/api/skillsets/sp/update' })
  release(state({ commit: 'bbbbbbb999' }))
  await flushPromises()
  expect(w.get('[data-skillset-result]').text()).toBe('Updated; now at bbbbbbb.')
  expect(w.get('[data-skillset-update]').text()).toBe('Update now')
  expect(states[0]).toEqual(state({ commit: 'bbbbbbb999' }))

  h.respond.write = () => ok(state({ state: 'failed', message: 'fatal: <b>no</b>‮' }))
  await w.get('[data-skillset-update]').trigger('click')
  await flushPromises()
  const r = w.get('[data-skillset-result]')
  expect(r.text()).toBe('The update failed: fatal: <b>no</b>\\u{202E}')
  expect(r.find('b').exists()).toBe(false)
})

test('a refused update shows the refusal', async () => {
  const { w, h } = await open([set()], { state: state() })
  h.respond.write = () => refuse('not_found', 'no skill set "sp"', undefined, 404)
  await w.get('[data-skillset-update]').trigger('click')
  await flushPromises()
  expect(w.get('[data-skillset-result]').text()).toBe('no skill set "sp"')
})

test('the checkout is shown as it is: commit and path, or the failure with the kept commit', async () => {
  const { w } = await open([set()], { state: state({ state: 'failed', message: 'boom', commit: 'a41c9e0123456' }) })
  const text = w.get('[data-skillset-checkout]').text()
  expect(text).toContain('The last update failed (still at a41c9e0): boom')
  await w.setProps({ state: state() })
  expect(w.get('[data-skillset-checkout]').text()).toContain('Checked out at a41c9e0')
  expect(w.get('[data-skillset-checkout]').text()).toContain('/home/x/skills/sp')
})

test('delete asks first, then deletes with the revision', async () => {
  let deleted = 0
  const { w, h } = await open([set()], { onDeleted: () => deleted++ })
  window.confirm = () => true
  await w.get('[data-skillset-delete]').trigger('click')
  await flushPromises()
  expect(h.sent).toEqual([{ method: 'DELETE', path: '/api/skillsets/sp', body: undefined, ifMatch: 'rev1' }])
  expect(deleted).toBe(1)
})

test('the repository is shown as stored, in a field, never as markup', async () => {
  const { w } = await open([set({ repository: 'https://user:token@example.invalid/<b>x</b>.git' })])
  expect((w.get('[data-skillset-repository]').element as HTMLInputElement).value).toBe('https://user:token@example.invalid/<b>x</b>.git')
  expect(w.find('b').exists()).toBe(false)
})

test('the form says it is busy while Update now runs, so the page keeps it open', async () => {
  const busy: boolean[] = []
  const { w, h } = await open([set()], { state: state(), onBusy: (b: boolean) => busy.push(b) })
  let release!: (s: SkillSetState) => void
  h.respond.write = () => new Promise((res) => (release = (s) => res(ok(s))))
  await w.get('[data-skillset-update]').trigger('click')
  await flushPromises()
  expect(busy).toEqual([true])
  release(state())
  await flushPromises()
  expect(busy).toEqual([true, false])
})
