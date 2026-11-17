import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import type { PluginHook, PluginInfo, PluginsResponse, TicketSummary } from '../../api/types'
import { project } from '../../testing/fakeOmo'
import { ticket } from '../board/testing'
import { createPluginList, ticketActionsOf } from './pluginActions'
import TicketPanel from './TicketPanel.vue'
import { mountPanel, ok, panelHarness, refuse, type PanelHarness } from './panelTesting'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
  document.body.querySelectorAll('[data-plugin-answer-dialog]').forEach((d) => d.closest('div')?.remove())
})

const manual = (name: string, description: string, scope = 'ticket'): PluginHook => ({ event: 'manual', name, description, scope, interval: '' })
const info = (name: string, o: Partial<PluginInfo> = {}): PluginInfo => ({
  name,
  version: '1.0.0',
  description: '',
  state: 'ready',
  message: '',
  commit: 'abc',
  last_event: '',
  last_run_at: null,
  hooks: [manual('mark', 'Mark this ticket')],
  ...o,
})
const response = (plugins: PluginInfo[]): PluginsResponse => ({
  plugins,
  settings: plugins.map((p) => ({ name: p.name, source: '/x', subpath: '', ref: '', enabled: p.state !== 'disabled', config: {}, revision: 'r' })),
  errors: [],
})

/** A panel harness whose omo answers GET /api/plugins with `list.value` and counts the reads. */
async function harness(list: { value: PluginsResponse; reads: number }, ...tickets: TicketSummary[]): Promise<PanelHarness> {
  const hn = await panelHarness((omo) => {
    omo.tickets = tickets
    omo.projects = [project('demo')]
  })
  const client = hn.session.client
  const baseGet = client.get.bind(client)
  client.get = ((path: string, q?: never) => {
    if (path !== '/api/plugins') return baseGet(path, q)
    list.reads++
    return Promise.resolve(ok(list.value))
  }) as typeof client.get
  return hn
}

async function open(hn: PanelHarness, n: number) {
  const { wrapper } = await mountPanel(hn, TicketPanel, { number: n }, `/${n}`)
  mounted = wrapper
  await flushPromises()
  return wrapper
}

const answerDialog = () => document.body.querySelector<HTMLElement>('[data-plugin-answer-dialog]')

test('ticket actions come from enabled, loaded plugins with a ticket hook only', () => {
  const d = response([
    info('a', { hooks: [manual('mark', 'Mark it'), manual('sum', 'Summary', 'global'), { event: 'ticket_moved', name: '', description: '', scope: '', interval: '' }] }),
    info('b', { state: 'disabled' }),
    info('c', { state: 'invalid' }),
    info('d', { state: 'not_installed' }),
    info('e', { state: 'error' }),
  ])
  expect(ticketActionsOf(d)).toEqual([
    { plugin: 'a', action: 'mark', description: 'Mark it' },
    { plugin: 'e', action: 'mark', description: 'Mark this ticket' },
  ])
  expect(ticketActionsOf(null)).toEqual([])
})

test('the panel shows "<plugin>: <description>" and running it shows the answer as text', async () => {
  const list = { value: response([info('log'), info('off', { state: 'disabled' })]), reads: 0 }
  const hn = await harness(list, ticket({ number: 5, column: 'in_progress' }))
  const w = await open(hn, 5)
  expect(w.findAll('[data-action^="plugin:"]').map((b) => b.text())).toEqual(['log: Mark this ticket'])
  hn.write.fn = () => ok({ message: 'hello <b>#5</b>‮' })
  await w.get('[data-action="plugin:log:mark"]').trigger('click')
  await flushPromises()
  expect(hn.sent).toEqual([{ method: 'POST', path: '/api/plugins/log/actions/mark', body: { ticket: 5 } }])
  const shown = answerDialog()!.querySelector('[data-plugin-answer]')!
  expect(shown.getAttribute('role')).toBe('status')
  expect(shown.textContent).toBe('hello <b>#5</b>\\u{202E}')
  expect(shown.querySelector('b')).toBeNull()
  document.body.querySelector<HTMLElement>('[data-plugin-answer-close]')!.click()
  await flushPromises()
  expect(answerDialog()).toBeNull()
})

test('the answer goes away with the panel', async () => {
  const list = { value: response([info('log')]), reads: 0 }
  const hn = await harness(list, ticket({ number: 5, column: 'in_progress' }))
  const w = await open(hn, 5)
  hn.write.fn = () => ok({ message: 'done' })
  await w.get('[data-action="plugin:log:mark"]').trigger('click')
  await flushPromises()
  expect(answerDialog()).not.toBeNull()
  w.unmount()
  await flushPromises()
  expect(answerDialog()).toBeNull()
})

test("a refusal is shown in the panel in the server's words", async () => {
  const list = { value: response([info('log')]), reads: 0 }
  const hn = await harness(list, ticket({ number: 5, column: 'in_progress' }))
  const w = await open(hn, 5)
  hn.write.fn = () => refuse('validation', 'the action failed: line 3: boom', 422)
  await w.get('[data-action="plugin:log:mark"]').trigger('click')
  await flushPromises()
  expect(w.get('[data-action-error]').text()).toBe('the action failed: line 3: boom')
  expect(answerDialog()).toBeNull()
})

test('plugin.changed updates the actions without a read; a snapshot reads again; opening panels reads nothing more', async () => {
  const list = { value: response([info('log')]), reads: 0 }
  const hn = await harness(list, ticket({ number: 5, column: 'in_progress' }), ticket({ number: 6, column: 'ready' }))
  let w = await open(hn, 5)
  expect(list.reads).toBe(1)
  expect(w.find('[data-action="plugin:log:mark"]').exists()).toBe(true)

  hn.omo.emit('plugin.changed', info('log', { state: 'disabled' }))
  await flushPromises()
  expect(w.find('[data-action="plugin:log:mark"]').exists()).toBe(false)
  expect(list.reads).toBe(1)

  mounted?.unmount()
  w = await open(hn, 6)
  expect(list.reads).toBe(1)

  list.value = response([info('log'), info('other', { hooks: [manual('ping', 'Ping')] })])
  hn.omo.stream!.onResync(hn.omo.eventId)
  await flushPromises()
  expect(list.reads).toBe(2)
  expect(w.findAll('[data-action^="plugin:"]').map((b) => b.text())).toEqual(['log: Mark this ticket', 'other: Ping'])
})

test('a plugin state from an event is kept over a read that started before it', async () => {
  const hn = await panelHarness()
  let gate: Promise<void> = Promise.resolve()
  const client = hn.session.client
  client.get = (async (path: string) => {
    if (path !== '/api/plugins') throw new Error(path)
    await gate
    return ok(response([info('log')]))
  }) as typeof client.get
  const l = createPluginList({ client, store: hn.session.store })
  await flushPromises()
  expect(l.data.value?.plugins.map((p) => p.state)).toEqual(['ready'])
  let release!: () => void
  gate = new Promise<void>((r) => (release = r))
  const reading = l.reload()
  l.take(info('log', { state: 'disabled' }))
  expect(l.data.value?.plugins.map((p) => p.state)).toEqual(['disabled'])
  release()
  await reading
  expect(l.data.value?.plugins.map((p) => p.state)).toEqual(['disabled'])
  // A read that starts after the event is newer than it.
  gate = Promise.resolve()
  await l.reload()
  expect(l.data.value?.plugins.map((p) => p.state)).toEqual(['ready'])
})
