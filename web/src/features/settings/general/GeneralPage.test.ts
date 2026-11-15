import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import { general } from '../../../testing/fakeOmo'
import { harness, mountWith, ok } from '../../create/dialogTesting'
import GeneralPage from './GeneralPage.vue'

let w: VueWrapper | null = null
const original = structuredClone(general)
afterEach(() => {
  w?.unmount()
  w = null
  Object.assign(general, structuredClone(original))
  document.documentElement.removeAttribute('data-theme')
})

async function mountPage(ceoOn: boolean) {
  general.ceo.enabled = ceoOn
  const h = await harness()
  h.respond.write = (s) =>
    ok({
      settings: general,
      errors: [],
      revision: 'r2',
      home: '/h',
      ...(s.body as object),
    })
  w = await mountWith(h, GeneralPage, {})
  await flushPromises()
  return h
}

const ceoSwitch = () => w!.get('[data-field="ceo.enabled"]')
const save = () => w!.findAll('button').find((b) => b.text() === 'Save')!

test('turning the CEO off asks first; turning it on does not', async () => {
  const h = await mountPage(true)
  expect(ceoSwitch().attributes('aria-checked')).toBe('true')
  await ceoSwitch().trigger('click')
  expect(w!.find('[data-ceo-switch]').exists()).toBe(true)
  expect(w!.text()).toContain('a running CEO session ends at the next scheduling pass')
  // Keeping it on changes nothing.
  await w!.get('[data-ceo-keep]').trigger('click')
  expect(ceoSwitch().attributes('aria-checked')).toBe('true')
  expect(save().attributes('disabled')).toBeDefined()

  await ceoSwitch().trigger('click')
  await w!.get('[data-ceo-confirm]').trigger('click')
  expect(ceoSwitch().attributes('aria-checked')).toBe('false')
  await save().trigger('click')
  await flushPromises()
  expect(h.sent.find((s) => s.method === 'PATCH')!.body).toEqual({
    ceo: { enabled: false, may_approve_stories: false },
  })
})

test('turning the CEO on asks nothing and only sends the CEO keys', async () => {
  const h = await mountPage(false)
  await ceoSwitch().trigger('click')
  expect(w!.find('dialog[open]').exists()).toBe(false)
  expect(ceoSwitch().attributes('aria-checked')).toBe('true')
  await save().trigger('click')
  await flushPromises()
  expect(h.sent.find((s) => s.method === 'PATCH')!.body).toEqual({
    ceo: { enabled: true, may_approve_stories: false },
  })
})

test('the server facts show the home folder and the theme applies at once', async () => {
  await mountPage(false)
  expect(w!.get('[data-server-facts]').text()).toContain('/home/sample/.local/omo')
  const colours = w!.findAll('select').find((s) => w!.find(`label[for="${s.attributes('id')}"]`).text() === 'Colours')!
  await colours.setValue('dark')
  expect(document.documentElement.getAttribute('data-theme')).toBe('dark')
})
