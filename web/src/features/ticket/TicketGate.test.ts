import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import type { TicketGates, TicketSummary } from '../../api/types'
import { project } from '../../testing/fakeOmo'
import { ticket } from '../board/testing'
import TicketPanel from './TicketPanel.vue'
import { mountPanel, ok, panelHarness, refuse, type PanelHarness } from './panelTesting'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
  document.body.querySelectorAll('[data-gate-dialog], [data-plugin-answer-dialog]').forEach((d) => d.remove())
})

const held = (over: Partial<TicketGates> = {}): TicketGates => ({
  move: 'release',
  from: 'planning',
  to: 'ready',
  gates: [{ plugin: 'pull-base', state: 'held', reason: 'the base is behind', choices: [{ label: 'Pull now', action: 'pull' }] }],
  ...over,
})
const gated = (over: Partial<TicketSummary> = {}) => ticket({ number: 4, column: 'planning', gate: held(), ...over })

const seeded = (...tickets: TicketSummary[]) => (omo: { tickets: TicketSummary[]; projects: unknown[] }) => {
  omo.tickets = tickets
  omo.projects = [project('demo')]
}
async function open(hn: PanelHarness, n = 4) {
  const { wrapper } = await mountPanel(hn, TicketPanel, { number: n }, `/${n}`)
  mounted = wrapper
  await flushPromises()
  return wrapper
}
const dialog = () => document.body.querySelector<HTMLElement>('[data-gate-dialog]')
const press = async (sel: string) => {
  document.body.querySelector<HTMLElement>(sel)!.click()
  await flushPromises()
}

test('a held ticket shows each plugin with its state, reason and choices', async () => {
  const hn = await panelHarness(seeded(gated()))
  const w = await open(hn)
  expect(w.get('[data-gate-head]').text()).toBe('Held by a plugin: Planning to Ready For Dev')
  const row = w.get('[data-gate-plugin="pull-base"]')
  expect(row.get('[data-gate-state]').text()).toBe('holds the move')
  expect(row.get('[data-gate-reason]').text()).toBe('the base is behind')
  expect(row.get('[data-gate-choice="pull"]').text()).toBe('Pull now')
})

test('a gate that is only being asked shows the waiting plugin and no choices', async () => {
  const g = held({ gates: [{ plugin: 'pull-base', state: 'pending', reason: '', choices: [] }] })
  const hn = await panelHarness(seeded(gated({ gate: g })))
  const w = await open(hn)
  expect(w.get('[data-gate-head]').text()).toContain('Waiting for plugins')
  expect(w.get('[data-gate-state]').text()).toBe('has not answered yet')
  expect(w.find('[data-gate-choice]').exists()).toBe(false)
})

test('a plugin that passed shows as passed next to one that holds', async () => {
  const g = held({ gates: [{ plugin: 'a', state: 'passed', reason: '', choices: [] }, ...held().gates] })
  const hn = await panelHarness(seeded(gated({ gate: g })))
  const w = await open(hn)
  expect(w.get('[data-gate-plugin="a"] [data-gate-state]').text()).toBe('gave its go-ahead')
})

test('a choice runs the plugin hook for the ticket and shows its answer', async () => {
  const hn = await panelHarness(seeded(gated()))
  hn.write.fn = () => ok({ message: 'pulled', url: '', forward_id: '' })
  const w = await open(hn)
  await w.get('[data-gate-choice="pull"]').trigger('click')
  await flushPromises()
  expect(hn.sent).toEqual([{ method: 'POST', path: '/api/plugins/pull-base/actions/pull', body: { ticket: 4 } }])
  expect(document.body.querySelector('[data-plugin-answer]')?.textContent).toBe('pulled')
})

test("a refused choice is shown under the gate", async () => {
  const hn = await panelHarness(seeded(gated()))
  hn.write.fn = () => refuse('conflict', 'the plugin is busy', 409)
  const w = await open(hn)
  await w.get('[data-gate-choice="pull"]').trigger('click')
  await flushPromises()
  expect(w.get('[data-gate-error]').text()).toBe('the plugin is busy')
})

test('Continue anyway asks first, then overrides every hold', async () => {
  const hn = await panelHarness(seeded(gated()))
  hn.write.fn = () => ok({})
  const w = await open(hn)
  expect(w.get('[data-action-explain="gate-override"]').text()).toBe('Every plugin that has not given its go-ahead counts as having given it, and #4 goes on to Ready For Dev.')
  await w.get('[data-action="gate-override"]').trigger('click')
  await flushPromises()
  expect(hn.sent).toEqual([])
  await press('[data-gate-cancel]')
  expect(hn.sent).toEqual([])
  await w.get('[data-action="gate-override"]').trigger('click')
  await flushPromises()
  await press('[data-gate-confirm]')
  expect(hn.sent).toEqual([{ method: 'POST', path: '/api/tickets/4/gate/override', body: {} }])
  expect(dialog()).toBeNull()
})

test('Cancel move says what becomes of the ticket and sends the user cancel', async () => {
  const hn = await panelHarness(seeded(gated()))
  hn.write.fn = () => ok({})
  const w = await open(hn)
  expect(w.get('[data-action-explain="gate-cancel"]').text()).toBe('The move to Ready For Dev is not made; #4 stays in Planning.')
  await w.get('[data-action="gate-cancel"]').trigger('click')
  await flushPromises()
  await press('[data-gate-confirm]')
  expect(hn.sent).toEqual([{ method: 'POST', path: '/api/tickets/4/gate/cancel', body: {} }])
})

test('cancelling a held start says the ticket goes back to Planning', async () => {
  const g = held({ move: 'start', from: 'ready', to: 'in_progress' })
  const hn = await panelHarness(seeded(gated({ column: 'ready', gate: g, start_blocked: 'gate' })))
  const w = await open(hn)
  expect(w.get('[data-action-explain="gate-cancel"]').text()).toBe('The start is cancelled and #4 goes back to Planning.')
})

test('the server refusing Continue anyway is shown in the panel', async () => {
  const hn = await panelHarness(seeded(gated()))
  hn.write.fn = () => refuse('conflict', '#4 has no move that waits for a plugin', 409)
  const w = await open(hn)
  await w.get('[data-action="gate-override"]').trigger('click')
  await flushPromises()
  await press('[data-gate-confirm]')
  expect(w.get('[data-action-error]').text()).toBe('#4 has no move that waits for a plugin')
})

test('a ticket without a gate shows none of it', async () => {
  const hn = await panelHarness(seeded(gated({ gate: null })))
  const w = await open(hn)
  expect(w.find('[data-gate]').exists()).toBe(false)
  expect(w.find('[data-action="gate-override"]').exists()).toBe(false)
  expect(w.find('[data-action="gate-cancel"]').exists()).toBe(false)
})

test('a refusal event shows the plugin and its reason until the ticket changes again', async () => {
  const hn = await panelHarness(seeded(gated({ gate: null })))
  const w = await open(hn)
  hn.omo.emit('ticket.refused', { ticket: gated({ gate: null }), move: 'release', plugin: 'lint', reason: 'the branch is dirty', choices: [] }, 4)
  await flushPromises()
  expect(w.get('[data-gate-refusal]').text()).toContain('lint refused the move')
  expect(w.get('[data-gate-refusal-reason]').text()).toBe('the branch is dirty')
  hn.omo.emit('ticket.held', { ticket: gated(), move: 'release', plugin: 'pull-base', reason: 'x', choices: [] }, 4)
  await flushPromises()
  expect(w.find('[data-gate-refusal]').exists()).toBe(false)
  expect(w.find('[data-gate]').exists()).toBe(true)
})

test('plugin text is shown as visible text', async () => {
  const g = held({ gates: [{ plugin: 'p', state: 'held', reason: 'a‮b', choices: [] }] })
  const hn = await panelHarness(seeded(gated({ gate: g })))
  const w = await open(hn)
  expect(w.get('[data-gate-reason]').text()).not.toContain('‮')
})

test('a refusal is shown only while the ticket is in Planning, where a refused move leaves it', async () => {
  const hn = await panelHarness(seeded(gated({ gate: null })))
  const w = await open(hn)
  hn.omo.emit('ticket.refused', { ticket: gated({ gate: null }), move: 'start', plugin: 'lint', reason: 'branch dirty', choices: [] }, 4)
  await flushPromises()
  expect(w.find('[data-gate-refusal]').exists()).toBe(true)
  // The user fixes it and moves the ticket on: only ticket.moved arrives.
  hn.omo.emit('ticket.moved', { ticket: gated({ gate: null, column: 'ready' }), from: 'planning', to: 'ready', action: 'move' }, 4)
  await flushPromises()
  expect(w.find('[data-gate-refusal]').exists()).toBe(false)
})

test('a refusal does not outlive a column change that no event of its own announced', async () => {
  const hn = await panelHarness(seeded(gated({ gate: null })))
  const w = await open(hn)
  hn.omo.emit('ticket.refused', { ticket: gated({ gate: null }), move: 'start', plugin: 'lint', reason: 'branch dirty', choices: [] }, 4)
  await flushPromises()
  hn.omo.emit('ticket.updated', { ticket: gated({ gate: null, column: 'in_progress' }), fields: ['column'] }, 4)
  await flushPromises()
  expect(w.find('[data-gate-refusal]').exists()).toBe(false)
})

test('when every plugin passed, the panel says omo starts the ticket and offers no way out', async () => {
  const g = held({ move: 'start', from: 'ready', to: 'in_progress', gates: [{ plugin: 'pull-base', state: 'passed', reason: '', choices: [] }] })
  const hn = await panelHarness(seeded(gated({ column: 'ready', gate: g, start_blocked: 'gate' })))
  const w = await open(hn)
  expect(w.get('[data-gate-head]').text()).toBe('Every plugin gave its go-ahead: omo starts #4 when it can')
  expect(w.get('[data-gate-state]').text()).toBe('gave its go-ahead')
  expect(w.find('[data-action="gate-override"]').exists()).toBe(false)
  expect(w.find('[data-action="gate-cancel"]').exists()).toBe(false)
})
