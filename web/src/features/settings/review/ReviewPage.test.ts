import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import { ApiError } from '../../../api/client'
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
  await select('Review', 'Tasks without a story').setValue('human')
  await select('Review', 'Tasks in a story').setValue('skipped')
  await select('Tasks are released by', 'Tasks in a story').setValue('user')
  await w!.get('button.btn-primary, button[class*="primary"]').trigger('click')
  await flushPromises()
  const patch = h.sent.find((s) => s.method === 'PATCH')!
  expect(patch.path).toBe('/api/settings')
  expect(Object.keys(patch.body as object).sort()).toEqual(['review_defaults', 'tasks', 'when_approved_defaults'])
  expect(patch.body).toMatchObject({
    review_defaults: { story: 'human', task: 'human', story_task: 'skipped' },
    tasks: { require_user_release: true },
  })
})

test('tasks in a story have their own review, a fixed "Merge locally" with its reason, and their field errors', async () => {
  const h = await mountPage()
  const group = w!.findAll('fieldset').find((f) => f.find('legend').text() === 'Tasks in a story')!
  expect((select('Review', 'Tasks in a story').element as HTMLSelectElement).value).toBe(general.review_defaults.story_task)
  const fixed = group.get<HTMLInputElement>('input[data-field="when_approved_defaults.story_task"]')
  expect(fixed.element.value).toBe('Merge locally')
  expect(fixed.element.disabled).toBe(true)
  expect(group.text()).toContain("A task in a story always merges into the story's branch")

  h.respond.write = () => {
    throw new ApiError(422, 'validation', 'review_defaults.story_task must be one of skipped, agent, human, agent_then_human', { field: 'review_defaults.story_task' })
  }
  await select('Review', 'Tasks in a story').setValue('human')
  await w!.get('button.btn-primary, button[class*="primary"]').trigger('click')
  await flushPromises()
  const field = select('Review', 'Tasks in a story')
  const errorId = field.attributes('aria-describedby')!.split(' ').find((id) => w!.find(`#${id}`).text().includes('must be one of'))
  expect(errorId).toBeDefined()
  expect(group.find(`#${errorId}`).exists()).toBe(true)
  expect(w!.find('[data-form-errors]').exists()).toBe(false)
})
