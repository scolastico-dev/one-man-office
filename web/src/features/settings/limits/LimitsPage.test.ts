import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import { general } from '../../../testing/fakeOmo'
import { harness, mountWith, ok } from '../../create/dialogTesting'
import LimitsPage from './LimitsPage.vue'

let w: VueWrapper | null = null
const original = structuredClone(general)
afterEach(() => {
  w?.unmount()
  w = null
  Object.assign(general, structuredClone(original))
})

async function mountPage() {
  const h = await harness()
  h.respond.write = (s) => ok({ settings: { ...general, ...(s.body as object) }, errors: [], revision: 'r2', home: '/h' })
  w = await mountWith(h, LimitsPage, {})
  await flushPromises()
  return h
}

const save = () => w!.findAll('button').find((b) => b.text() === 'Save')!

test('the page shows the two limits and the rule in plain words, and nothing about omo ai wait', async () => {
  await mountPage()
  const tasks = w!.get<HTMLInputElement>('input[data-field="limits.tasks"]')
  const stories = w!.get<HTMLInputElement>('input[data-field="limits.stories"]')
  expect(tasks.element.value).toBe('4')
  expect(stories.element.value).toBe('2')
  const text = w!.text()
  expect(text).toContain('Tasks in work')
  expect(text).toContain('Stories in work')
  expect(text).toContain(
    'A task or story takes a slot when it moves to In Progress and gives it back when it is Done; waiting, review and questions do not free it. The CEO never counts. A story can also limit its own parallel tasks in its panel.',
  )
  expect(text).not.toContain('omo ai wait')
  expect(text).not.toContain('Max running agents')
})

test('a save sends both limits under limits', async () => {
  const h = await mountPage()
  await w!.get('input[data-field="limits.tasks"]').setValue('6')
  await w!.get('input[data-field="limits.stories"]').setValue('3')
  await save().trigger('click')
  await flushPromises()
  const body = h.sent.find((s) => s.method === 'PATCH')!.body as Record<string, unknown>
  expect(body.limits).toEqual({ tasks: 6, stories: 3 })
  expect(body).not.toHaveProperty('max_running_agents')
})

test('the hints say what the engine does: who is told to investigate, what is reminded and flagged, what is rescanned', async () => {
  await mountPage()
  const text = w!.text()
  expect(text).toContain('before the waiting agent is told to find out why nothing happens')
  expect(text).not.toContain('the product manager looks into it')
  expect(text).toContain('A question to an agent is reminded to it then and flagged on the ticket after twice this time.')
  expect(text).toContain('the exported pull requests are checked for a merge')
})
