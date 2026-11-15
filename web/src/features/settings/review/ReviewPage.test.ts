import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import { general } from '../../../testing/fakeOmo'
import { harness, mountWith, ok } from '../../create/dialogTesting'
import ReviewPage from './ReviewPage.vue'

let w: VueWrapper | null = null
const original = structuredClone(general)
afterEach(() => {
  w?.unmount()
  w = null
  Object.assign(general, structuredClone(original))
})

async function mountPage() {
  const h = await harness()
  h.respond.write = (s) =>
    ok({
      settings: general,
      errors: [],
      revision: 'r2',
      home: '/h',
      ...(s.body as object),
    })
  w = await mountWith(h, ReviewPage, {})
  await flushPromises()
  return h
}

const select = (label: string, section: string) =>
  w!
    .findAll('fieldset')
    .find((f) => f.find('legend').text() === section)!
    .findAll('select')
    .find((s) => w!.find(`label[for="${s.attributes('id')}"]`).text() === label)!

test('the story note shows for Skipped and Human, not for Agent', async () => {
  await mountPage()
  const note = () => w!.text().includes('No agent runs the full test suite for stories.')
  // The fixture's story default is Human.
  expect(note()).toBe(true)
  await select('Review', 'Stories').setValue('agent')
  expect(note()).toBe(false)
  await select('Review', 'Stories').setValue('skipped')
  expect(note()).toBe(true)
  await select('Review', 'Stories').setValue('agent_then_human')
  expect(note()).toBe(false)
})

test('each page sends only its own keys', async () => {
  const h = await mountPage()
  await select('Review', 'Tasks').setValue('human')
  await select('Tasks are released by', 'Tasks').setValue('user')
  await w!.get('button.btn-primary, button[class*="primary"]').trigger('click')
  await flushPromises()
  const patch = h.sent.find((s) => s.method === 'PATCH')!
  expect(patch.path).toBe('/api/settings')
  expect(Object.keys(patch.body as object).sort()).toEqual(['review_defaults', 'tasks', 'when_approved_defaults'])
  expect(patch.body).toMatchObject({
    review_defaults: { story: 'human', task: 'human' },
    tasks: { require_user_release: true },
  })
})
