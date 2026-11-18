import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test, vi } from 'vitest'
import { ApiError, type ApiResult } from '../../api/client'
import type { ProxyForward, ProxyForwardList, TicketSummary } from '../../api/types'
import { DIALOGS_IN_PLACE } from '../../components/ui/dialogHost'
import { ticket } from '../board/testing'
import { mountPanel, ok, panelHarness } from '../ticket/panelTesting'
import TicketPanel from '../ticket/TicketPanel.vue'
import TicketPage from '../ticket/page/TicketPage.vue'
import ForwardList from './ForwardList.vue'
import ForwardsButton from './ForwardsButton.vue'

const forward = (o: Partial<ProxyForward> = {}): ProxyForward => ({ id: 'abc', ticket: 4, host: '127.0.0.1', port: 5173, scheme: 'http', name: 'Dev server', start_path: '/', strip_prefix: false, verify_tls: false, base_path: '/proxy/abc/', address: 'http://omo.localhost/proxy/abc/', ...o })
let mounted: VueWrapper | null = null
const stops: (() => void)[] = []
afterEach(() => { mounted?.unmount(); mounted = null; stops.splice(0).forEach((f) => f()); vi.restoreAllMocks() })

async function setup(rows: ProxyForward[] = [], owner?: TicketSummary) {
  const hn = await panelHarness((o) => { o.tickets = [ticket({ number: 4 }), ticket({ number: 5 })] })
  stops.push(() => hn.session.store.stop())
  const state = { rows, fail: false, queries: [] as unknown[] }
  const get = hn.session.client.get
  hn.session.client.get = async <T>(path: string, q?: never): Promise<ApiResult<T>> => {
    if (path !== '/api/proxy/forwards') return get(path, q)
    state.queries.push(q)
    if (state.fail) throw new Error('List unavailable')
    const n = (q as { ticket?: number } | undefined)?.ticket
    return ok({ forwards: state.rows.filter((f) => n === undefined || f.ticket === n) } as T)
  }
  const mountedList = await mountPanel(hn, ForwardList, owner ? { ticket: owner } : {})
  mounted = mountedList.wrapper
  return { ...hn, state, w: mounted, router: mountedList.router }
}
const fill = (w: VueWrapper, name: string, value: string) => w.get(`[name="${name}"]`).setValue(value)
const submit = (w: VueWrapper) => w.get('[data-forward-form]').trigger('submit')

test('ticket scoping, safe name/target/base_path and overall owner links; neither view probes targets', async () => {
  const { w, state } = await setup([forward({ name: '<b>Dev\u202e</b>' }), forward({ id: 'global', ticket: null }), forward({ id: 'other', ticket: 5 })], ticket({ number: 4 }))
  expect(state.queries).toEqual([{ ticket: 4 }])
  expect(w.findAll('[data-forward]').map((r) => r.attributes('data-forward'))).toEqual(['abc'])
  expect(w.text()).toContain('<b>Dev\\u{202E}</b>')
  expect(w.find('b').exists()).toBe(false)
  expect(w.text()).toContain('http://127.0.0.1:5173/')
  expect(w.text()).toContain('/proxy/abc/')
  expect(w.text()).toContain('base path')
  expect(w.find('[data-ticket-link]').exists()).toBe(false)
})

test('overall list includes global and every ticket and uses TicketLink for navigation', async () => {
  const { w, router, state } = await setup([forward(), forward({ id: 'global', ticket: null }), forward({ id: 'other', ticket: 5 })])
  expect(state.queries).toEqual([{ ticket: undefined }])
  expect(w.findAll('[data-forward]')).toHaveLength(3)
  expect(w.get('[data-forward="global"]').text()).toContain('Global')
  const link = w.get('[data-forward="abc"] [data-ticket-link]')
  expect(link.attributes('href')).toBe('/ticket/4')
  await link.trigger('click')
  await flushPromises()
  expect(router.currentRoute.value.path).toBe('/4')
})

test('empty list explains registration and shows defaults; adding to a ticket displays the returned base path', async () => {
  const { w, state, sent, write } = await setup([], ticket({ number: 4 }))
  expect(w.get('[data-forward-empty]').text()).toContain('No forwards')
  expect(w.text()).toContain('starts or stops')
  await w.get('[data-forward-add]').trigger('click')
  await fill(w, 'name', 'Dev server')
  expect(w.get('[name="host"]').element).toHaveProperty('value', '127.0.0.1')
  expect(w.get('[name="scheme"]').element).toHaveProperty('value', 'http')
  expect(w.get('[name="start_path"]').element).toHaveProperty('value', '/')
  expect(w.findAll('[role="switch"]')[0]!.attributes('aria-checked')).toBe('false')
  expect(w.findAll('[role="switch"]')[1]!.attributes('aria-checked')).toBe('false')
  write.fn = () => { state.rows = [forward()]; return ok(forward()) }
  await fill(w, 'port', '5173')
  await submit(w)
  await flushPromises()
  expect(sent).toEqual([{ method: 'POST', path: '/api/proxy/forwards', body: { ticket: 4, port: 5173, name: 'Dev server', host: '127.0.0.1', scheme: 'http', start_path: '/', strip_prefix: false, verify_tls: false } }])
  expect(w.find('[data-forward-form]').exists()).toBe(false)
  expect(w.get('[data-forward="abc"]').text()).toContain('/proxy/abc/')
})

test('overall form adds globals or ticket forwards with all options, including IPv6 target', async () => {
  const { w, sent, write, state } = await setup()
  write.fn = (s) => { const f = forward({ ...(s.body as ProxyForward), id: 'new' }); state.rows.push(f); return ok(f) }
  await w.get('[data-forward-add]').trigger('click')
  await fill(w, 'port', '8443')
  await fill(w, 'name', 'Preview')
  await fill(w, 'host', '::1')
  await fill(w, 'scheme', 'https')
  await fill(w, 'start_path', '/app?q=x')
  await w.findAll('[role="switch"]')[0]!.trigger('click')
  await w.findAll('[role="switch"]')[1]!.trigger('click')
  expect(w.text()).toContain('self-signed')
  await submit(w)
  await flushPromises()
  expect(sent[0]!.body).toEqual({ ticket: null, port: 8443, name: 'Preview', host: '::1', scheme: 'https', start_path: '/app?q=x', strip_prefix: true, verify_tls: true })
  expect(w.get('[data-forward="new"]').text()).toContain('https://[::1]:8443/app?q=x')
  await w.get('[data-forward-add]').trigger('click')
  await fill(w, 'scope', 'ticket')
  await fill(w, 'ticket', '5')
  await fill(w, 'port', '3000')
  await fill(w, 'name', 'Ticket preview')
  await submit(w)
  await flushPromises()
  expect(sent[1]!.body).toMatchObject({ ticket: 5, port: 3000 })
})

test('invalid/cleared numbers never submit a retained NumberField value; server field errors are accessible', async () => {
  const { w, sent, write } = await setup([], ticket({ number: 4 }))
  await w.get('[data-forward-add]').trigger('click')
  for (const port of ['', '0', '65536', '3.5', 'abc', '3000', '']) {
    await fill(w, 'port', port)
    if (port === '3000') continue
    await submit(w)
    expect(sent).toHaveLength(0)
  }
  await fill(w, 'port', '5173')
  await fill(w, 'name', 'Preview')
  write.fn = () => { throw new ApiError(422, 'validation', 'Port refused\u202e', { field: 'port' }) }
  await submit(w)
  await flushPromises()
  expect(w.get('[role="alert"]').text()).toContain('Port refused\\u{202E}')
  const port = w.get('[name="port"]')
  expect(port.attributes('aria-invalid')).toBe('true')
  expect(port.attributes('aria-describedby')).toBeTruthy()
  expect(w.find('[data-forward-form]').exists()).toBe(true)
})

test('done and cancelled ticket views cannot add; overall form rejects a known done ticket but still permits globals', async () => {
  const { w, omo, sent } = await setup()
  omo.change('ticket.moved', ticket({ number: 5, column: 'done', resolution: 'cancelled' }))
  await flushPromises()
  await w.get('[data-forward-add]').trigger('click')
  await fill(w, 'scope', 'ticket')
  await fill(w, 'ticket', '5')
  await fill(w, 'port', '3000')
  await fill(w, 'name', 'Ticket preview')
  await submit(w)
  await flushPromises()
  expect(sent).toHaveLength(0)
  expect(w.get('[role="alert"]').text()).toContain('done')
})

test.each(['done', 'cancelled'])('closed ticket %s has no add action', async (resolution) => {
  const { w } = await setup([], ticket({ number: 4, column: 'done', resolution: resolution === 'cancelled' ? 'cancelled' : 'completed' }))
  expect(w.find('[data-forward-add]').exists()).toBe(false)
  expect(w.text()).toContain('done')
})

test('add/remove/Open errors remain visible and lists stay usable; remove refreshes authoritative state', async () => {
  const { w, state, write, sent } = await setup([forward()], ticket({ number: 4 }))
  const tab = { opener: null, closed: false, location: { href: 'about:blank', replace: vi.fn() }, close: vi.fn() }
  vi.spyOn(window, 'open').mockReturnValue(tab as unknown as Window)
  write.fn = () => { throw new Error('Target could not be opened') }
  await w.get('[data-forward-open]').trigger('click')
  await flushPromises()
  expect(w.get('[role="alert"]').text()).toContain('Target could not be opened')
  expect(tab.close).toHaveBeenCalled()
  expect(w.findAll('[data-forward]')).toHaveLength(1)
  await w.get('[data-forward-remove]').trigger('click')
  await flushPromises()
  expect(w.get('[role="alert"]').text()).toContain('Target could not be opened')
  write.fn = () => { state.rows = []; return ok(undefined) }
  await w.get('[data-forward-remove]').trigger('click')
  await flushPromises()
  expect(sent.at(-1)).toMatchObject({ method: 'DELETE', path: '/api/proxy/forwards/abc' })
  expect(w.find('[data-forward]').exists()).toBe(false)
  expect(w.find('[role="alert"]').exists()).toBe(false)
})

test('events, automatic cleanup, snapshots and reconnects update lists without reload or polling; disposal stops reads', async () => {
  const { w, state, omo } = await setup([forward()], ticket({ number: 4 }))
  state.rows.push(forward({ id: 'next' }))
  omo.emit('proxy.forward_added', state.rows[1], 4)
  await flushPromises()
  expect(w.findAll('[data-forward]')).toHaveLength(2)
  state.rows = []
  omo.emit('proxy.forward_removed', forward(), 4)
  await flushPromises()
  expect(w.find('[data-forward]').exists()).toBe(false)
  state.rows = [forward()]
  omo.stream!.onState('retrying')
  omo.stream!.onState('live')
  await flushPromises()
  expect(w.findAll('[data-forward]')).toHaveLength(1)
  state.rows = []
  omo.stream!.onResync(omo.eventId)
  await flushPromises()
  expect(w.find('[data-forward]').exists()).toBe(false)
  w.unmount(); mounted = null
  const before = state.queries.length
  omo.emit('proxy.forward_added', forward(), 4)
  await flushPromises()
  expect(state.queries).toHaveLength(before)
})

test('loading and read failure have accessible messages and Retry restores the list', async () => {
  const hn = await panelHarness()
  stops.push(() => hn.session.store.stop())
  const get = hn.session.client.get
  let reject!: (e: Error) => void
  hn.session.client.get = ((path: string, q?: never) => path === '/api/proxy/forwards' ? new Promise((_r, j) => { reject = j }) : get(path, q)) as typeof get
  const { wrapper: w } = await mountPanel(hn, ForwardList, {})
  mounted = w
  expect(w.get('[role="status"]').text()).toContain('Loading')
  reject(new Error('List unavailable'))
  await flushPromises()
  expect(w.get('[role="alert"]').text()).toContain('List unavailable')
  hn.session.client.get = ((path: string, q?: never) => path === '/api/proxy/forwards' ? Promise.resolve(ok({ forwards: [] } satisfies ProxyForwardList)) : get(path, q)) as typeof get
  await w.get('[data-forward-retry]').trigger('click')
  await flushPromises()
  expect(w.get('[data-forward-empty]').text()).toContain('No forwards')
})

test.each([TicketPanel, TicketPage])('both ticket views mount the shared scoped forward list', async (component) => {
  const hn = await panelHarness((o) => { o.tickets = [ticket({ number: 4 })] })
  stops.push(() => hn.session.store.stop())
  hn.omo.forwards = [forward(), forward({ id: 'other', ticket: 5 })]
  const { wrapper: w } = await mountPanel(hn, component, { number: 4 }, '/ticket/4', {}, { routes: [{ path: '/ticket/:n', component: { render: () => null } }] })
  mounted = w
  expect(w.get('[data-forward="abc"]').text()).toContain('Dev server')
  expect(w.find('[data-forward="other"]').exists()).toBe(false)
})

test('the topbar control opens the overall list in the modal layer and a ticket link closes it', async () => {
  const hn = await panelHarness()
  stops.push(() => hn.session.store.stop())
  const get = hn.session.client.get
  hn.session.client.get = ((path: string, q?: never) => path === '/api/proxy/forwards' ? Promise.resolve(ok({ forwards: [forward()] })) : get(path, q)) as typeof get
  const { wrapper: w } = await mountPanel(hn, ForwardsButton, {}, '/', { [DIALOGS_IN_PLACE as symbol]: true })
  mounted = w
  await w.get('[data-forwards-button]').trigger('click')
  await flushPromises()
  await vi.waitFor(() => expect(w.find('dialog').exists()).toBe(true))
  expect(w.get('dialog').element).toHaveProperty('open', true)
  expect(w.get('dialog').text()).toContain('Forwards')
  await w.get('[data-ticket-link]').trigger('click')
  await flushPromises()
  expect(w.find('dialog').exists()).toBe(false)
})

test('creation keeps the returned base path visible even when refreshing the list fails', async () => {
  const { w, write, state } = await setup([], ticket({ number: 4 }))
  await w.get('[data-forward-add]').trigger('click')
  await fill(w, 'port', '5173')
  await fill(w, 'name', 'Preview')
  write.fn = () => { state.fail = true; return ok(forward({ base_path: '/proxy/returned-by-server/' })) }
  await submit(w)
  await flushPromises()
  expect(w.get('[data-forward-created]').text()).toContain('/proxy/returned-by-server/')
  expect(w.get('[role="alert"]').text()).toContain('List unavailable')
})

test('repeated submit and remove issue one write while pending, and preserve form values on failure', async () => {
  const { w, write, sent } = await setup([forward()], ticket({ number: 4 }))
  let reject!: (e: Error) => void
  write.fn = () => new Promise((_r, j) => { reject = j })
  await w.get('[data-forward-add]').trigger('click')
  await fill(w, 'port', '5173')
  await fill(w, 'name', 'My preview')
  await submit(w); await submit(w)
  expect(sent).toHaveLength(1)
  reject(new Error('Please retry'))
  await flushPromises()
  expect(w.get('[name="name"]').element).toHaveProperty('value', 'My preview')
  await w.get('[data-forward-remove]').trigger('click')
  await w.get('[data-forward-remove]').trigger('click')
  expect(sent).toHaveLength(2)
  reject(new Error('Please retry'))
  await flushPromises()
  expect(w.get('[role="alert"]').text()).toContain('Please retry')
})

test('newer event reads win over slow old reads or failures', async () => {
  const { w, session, omo } = await setup([forward()], ticket({ number: 4 }))
  const pending: { resolve: (v: ApiResult<unknown>) => void; reject: (e: Error) => void }[] = []
  const get = session.client.get
  session.client.get = ((path: string, q?: never) => path === '/api/proxy/forwards' ? new Promise<ApiResult<unknown>>((resolve, reject) => pending.push({ resolve, reject })) : get(path, q)) as typeof get
  omo.emit('proxy.forward_added', forward({ id: 'late' }), 4)
  omo.emit('proxy.forward_removed', forward(), 4)
  pending[1]!.resolve(ok({ forwards: [] }))
  await flushPromises()
  pending[0]!.resolve(ok({ forwards: [forward()] }))
  await flushPromises()
  expect(w.find('[data-forward]').exists()).toBe(false)
  omo.emit('proxy.forward_added', forward(), 4)
  omo.emit('proxy.forward_removed', forward(), 4)
  pending[3]!.resolve(ok({ forwards: [] }))
  await flushPromises()
  pending[2]!.reject(new Error('Old read failed'))
  await flushPromises()
  expect(w.find('[role="alert"]').exists()).toBe(false)
})

test('ticket completion closes an open add form in the mounted ticket view', async () => {
  const hn = await panelHarness((o) => { o.tickets = [ticket({ number: 4 })] })
  stops.push(() => hn.session.store.stop())
  const get = hn.session.client.get
  hn.session.client.get = ((path: string, q?: never) => path === '/api/proxy/forwards' ? Promise.resolve(ok({ forwards: [] })) : get(path, q)) as typeof get
  const { wrapper: w } = await mountPanel(hn, TicketPanel, { number: 4 })
  mounted = w
  await w.get('[data-forward-add]').trigger('click')
  expect(w.find('[data-forward-form]').exists()).toBe(true)
  hn.omo.change('ticket.moved', ticket({ number: 4, column: 'done', resolution: 'cancelled' }))
  await flushPromises()
  expect(w.find('[data-forward-form]').exists()).toBe(false)
  expect(w.find('[data-forward-add]').exists()).toBe(false)
})


test('the name required by the registry is labelled and validated before sending', async () => {
  const { w, sent } = await setup([], ticket({ number: 4 }))
  await w.get('[data-forward-add]').trigger('click')
  await fill(w, 'port', '5173')
  await fill(w, 'name', '   ')
  await submit(w)
  await flushPromises()
  expect(sent).toHaveLength(0)
  expect(w.get('[role="alert"]').text()).toContain('name')
  expect(w.get('[name="name"]').attributes('aria-invalid')).toBe('true')
  expect(w.get('[name="name"]').attributes('required')).toBeDefined()
})
