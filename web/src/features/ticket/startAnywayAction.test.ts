import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import type { TicketSummary } from '../../api/types'
import { project, type FakeOmo } from '../../testing/fakeOmo'
import { ticket } from '../board/testing'
import { startAnywayAction, startAnywayUnavailable } from './startAnywayAction'
import TicketPanel from './TicketPanel.vue'
import { mountPanel, ok, panelHarness, refuse, type PanelHarness } from './panelTesting'
import type { ActionContext } from './actions'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
  document.body.querySelectorAll('[data-start-anyway-dialog]').forEach((d) => d.remove())
})

const ctx = {} as unknown as ActionContext
const waiting = (over: Partial<TicketSummary> = {}) => ticket({ number: 4, column: 'ready', start_blocked: 'usage', model: 'fake/b', ...over })

async function open(hn: PanelHarness, n: number) {
  const { wrapper } = await mountPanel(hn, TicketPanel, { number: n }, `/${n}`)
  mounted = wrapper
  await flushPromises()
  return wrapper
}

const seeded = (...tickets: TicketSummary[]) => (omo: { tickets: TicketSummary[]; projects: unknown[] }) => {
  omo.tickets = tickets
  omo.projects = [project('demo')]
}

/** The ticket's model in the given usage state. */
const modelAt = (state: 'soft' | 'hard') => (omo: FakeOmo) => {
  seeded(waiting())(omo)
  omo.usage = { readings: [], models: [{ model: 'fake/b', state, window: 'weekly', used_percent: state === 'hard' ? 99 : 91, resets_at: null }] }
}

const dialog = () => document.body.querySelector<HTMLElement>('[data-start-anyway-dialog]')

test('applies only to a ticket that waits for usage; disabled with the reason without a model of its own', () => {
  for (const column of ['ready', 'in_progress', 'review'] as const) expect(startAnywayAction.applies(waiting({ column }), ctx)).toBe(true)
  for (const blocked of ['', 'slot', 'paused', 'no_model'] as const) expect(startAnywayAction.applies(waiting({ start_blocked: blocked }), ctx)).toBe(false)
  expect(startAnywayUnavailable(waiting())).toBeUndefined()
  expect(startAnywayUnavailable(waiting({ model: '' }))).toBe('#4 has no model of its own; it waits until a model of its role has room.')
  expect(startAnywayAction.describe?.(waiting(), ctx)).toBe('Starts it on fake/b although the model is near its usage limit.')
})

test('asks first and sends nothing when the user gives up', async () => {
  const hn = await panelHarness(seeded(waiting()))
  hn.write.fn = () => ok({})
  const w = await open(hn, 4)
  await w.get('[data-action="start-anyway"]').trigger('click')
  await flushPromises()
  expect(dialog()?.textContent).toContain('Starts it on fake/b although the model is near its usage limit.')
  document.body.querySelector<HTMLElement>('[data-start-anyway-cancel]')!.click()
  await flushPromises()
  expect(dialog()).toBeNull()
  expect(hn.sent).toEqual([])
})

test('confirming sends the request once', async () => {
  const hn = await panelHarness(seeded(waiting()))
  hn.write.fn = () => ok({})
  const w = await open(hn, 4)
  await w.get('[data-action="start-anyway"]').trigger('click')
  await flushPromises()
  document.body.querySelector<HTMLElement>('[data-start-anyway-confirm]')!.click()
  await flushPromises()
  expect(hn.sent).toEqual([{ method: 'POST', path: '/api/tickets/4/start-anyway', body: {} }])
  expect(dialog()).toBeNull()
  expect(w.find('[data-action-error]').exists()).toBe(false)
})

test("the server's refusal is shown in the panel as it is", async () => {
  const hn = await panelHarness(seeded(waiting()))
  hn.write.fn = () => refuse('invalid_transition', '#4 does not wait for usage', 409)
  const w = await open(hn, 4)
  await w.get('[data-action="start-anyway"]').trigger('click')
  await flushPromises()
  document.body.querySelector<HTMLElement>('[data-start-anyway-confirm]')!.click()
  await flushPromises()
  expect(w.get('[data-action-error]').text()).toBe('#4 does not wait for usage')
})

test('without a model of its own the button is disabled and says why', async () => {
  const hn = await panelHarness(seeded(waiting({ model: '' })))
  const w = await open(hn, 4)
  expect(w.get('[data-action="start-anyway"]').attributes('disabled')).toBeDefined()
  expect(w.get('[data-action-why="start-anyway"]').text()).toContain('has no model of its own')
})

test('the dialog goes away with the panel and nothing is sent', async () => {
  const hn = await panelHarness(seeded(waiting()))
  hn.write.fn = () => ok({})
  const w = await open(hn, 4)
  await w.get('[data-action="start-anyway"]').trigger('click')
  await flushPromises()
  expect(dialog()).not.toBeNull()
  w.unmount()
  mounted = null
  await flushPromises()
  expect(dialog()).toBeNull()
  expect(hn.sent).toEqual([])
})

test('near its limit the question says so; at its hard limit the action is disabled: the agent would be stopped again at once', async () => {
  const soft = await panelHarness(modelAt('soft'))
  const w = await open(soft, 4)
  await w.get('[data-action="start-anyway"]').trigger('click')
  await flushPromises()
  expect(dialog()?.textContent).toContain('Starts it on fake/b although the model is near its usage limit.')
  document.body.querySelector<HTMLElement>('[data-start-anyway-cancel]')!.click()
  await flushPromises()
  w.unmount()
  mounted = null

  const hard = await panelHarness(modelAt('hard'))
  const h = await open(hard, 4)
  const button = h.get('[data-action="start-anyway"]')
  expect(button.attributes('disabled')).toBeDefined()
  expect(h.get('[data-action-why="start-anyway"]').text()).toBe('fake/b is at its usage limit: its agent would be stopped again at once.')
})

test('a model that reaches its hard limit while the question is open changes the question and its button', async () => {
  const hn = await panelHarness(modelAt('soft'))
  hn.write.fn = () => ok({})
  const w = await open(hn, 4)
  await w.get('[data-action="start-anyway"]').trigger('click')
  await flushPromises()
  hn.omo.emit('usage.changed', { readings: [], models: [{ model: 'fake/b', state: 'hard', window: 'weekly', used_percent: 99, resets_at: null }] })
  await flushPromises()
  expect(dialog()?.textContent).toContain('Starts it on fake/b although the model is at its usage limit: its agent would be stopped again at once.')
  expect(document.body.querySelector<HTMLButtonElement>('[data-start-anyway-confirm]')!.disabled).toBe(true)
})
