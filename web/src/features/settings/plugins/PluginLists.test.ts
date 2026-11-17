import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import { general } from '../../../testing/fakeOmo'
import { harness, mountWith, ok, refuse } from '../../create/dialogTesting'
import PluginLists from './PluginLists.vue'

let w: VueWrapper | null = null
const original = structuredClone(general)
afterEach(() => {
  w?.unmount()
  w = null
  Object.assign(general, structuredClone(original))
})

async function mountForm(lists: string[]) {
  general.plugin_lists = lists
  const h = await harness()
  const saved: number[] = []
  h.respond.write = (s) => ok({ settings: { ...general, ...(s.body as object) }, errors: [], revision: 'r2', home: '/h' })
  w = await mountWith(h, PluginLists, { onSaved: () => saved.push(1) })
  await flushPromises()
  return { h, saved }
}

const inputs = () => w!.findAll<HTMLInputElement>('[data-plugin-lists-field] input')
const save = () => w!.findAll('button').find((b) => b.text() === 'Save')!

test('the form lists the addresses, says what a list means and saves only plugin_lists', async () => {
  const { h, saved } = await mountForm(['https://a.example/list.yaml'])
  expect(w!.get('[data-plugin-lists]').text()).toContain('run commands on this computer as you')
  expect(inputs().map((i) => i.element.value)).toEqual(['https://a.example/list.yaml'])
  await w!.findAll('button').find((b) => b.text().includes('Add list'))!.trigger('click')
  await inputs()[1]!.setValue('https://raw.githubusercontent.com/someone/lists/main/omo.yaml')
  await save().trigger('click')
  await flushPromises()
  expect(h.sent).toEqual([
    { method: 'PATCH', path: '/api/settings', ifMatch: '0', body: { plugin_lists: ['https://a.example/list.yaml', 'https://raw.githubusercontent.com/someone/lists/main/omo.yaml'] } },
  ])
  expect(saved).toEqual([1])
})

test("a refused address shows the server's message under its row", async () => {
  const { h, saved } = await mountForm([])
  expect(w!.text()).toContain("None: only omo's own list.")
  await w!.findAll('button').find((b) => b.text().includes('Add list'))!.trigger('click')
  await inputs()[0]!.setValue('file:///etc/passwd')
  h.respond.write = () => refuse('invalid_settings', 'plugin_lists.0 must be an http:// or https:// address of a plugin list file', { field: 'plugin_lists.0' }, 422)
  await save().trigger('click')
  await flushPromises()
  expect(inputs()[0]!.attributes('aria-invalid')).toBe('true')
  expect(w!.get('[data-row="0"]').text()).toContain('must be an http:// or https:// address')
  expect(saved).toEqual([])
})
