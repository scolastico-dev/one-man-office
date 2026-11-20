import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test, vi } from 'vitest'
import type { PluginTask, ProxyForward } from '../../../api/types'
import { harness, mountWith, ok, refuse } from '../../create/dialogTesting'
import PluginTasks from '../../plugins/PluginTasks.vue'
let mounted: VueWrapper | null = null
afterEach(() => { mounted?.unmount(); mounted = null; vi.restoreAllMocks() })
const task = (o: Partial<PluginTask> = {}): PluginTask => ({ id: 'run-1', name: 'editor', ticket: 7, started_at: '2026-11-18T10:00:00Z', state: 'running', output: ['hello <script>‮'], ...o })
const forward = (o: Partial<ProxyForward> = {}): ProxyForward => ({ id: 'a'.repeat(32), plugin: 'demo', task_id: 'run-1', ticket: 7, name: 'VS Code · alpha', host: '127.0.0.1', port: 40001, scheme: 'http', start_path: '/', strip_prefix: true, verify_tls: false, base_path: '/proxy/aaa/', address: 'http://board/proxy/aaa/', ...o })
async function setup(options: { tasks?: PluginTask[]; forwards?: ProxyForward[]; runningOnly?: boolean } = {}) {
  const h = await harness(omo => { omo.forwards = options.forwards ?? [] })
  const data = { tasks: options.tasks ?? [task()], reads: 0 }
  const base = h.session.client.get.bind(h.session.client)
  h.session.client.get = ((path: string, q?: never) => {
    if (path !== '/api/plugins/demo/tasks') return base(path, q)
    data.reads++
    return Promise.resolve(ok({ tasks: data.tasks }))
  }) as typeof base
  const w = await mountWith(h, PluginTasks, { name: 'demo', runningOnly: options.runningOnly })
  mounted = w
  await flushPromises()
  return { h, w, data }
}

test('shows name, associated ticket, elapsed time and bounded visible output; Stop refetches authoritative records', async () => {
  const { w, h, data } = await setup()
  expect(w.text()).toContain('editor')
  expect(w.text()).toContain('#7')
  expect(w.get('pre').text()).toContain('hello <script>\\u{202E}')
  expect(w.find('script').exists()).toBe(false)
  expect(w.find('[data-task-elapsed]').exists()).toBe(true)
  h.respond.write = () => { data.tasks = [task({ state: 'exited', reason: 'stopped', ended_at: '2026-11-18T10:05:00Z', output: [] })]; return ok(undefined) }
  await w.get('[data-task-stop]').trigger('click')
  await flushPromises()
  expect(h.sent).toEqual([{ method: 'POST', path: '/api/plugins/demo/tasks/editor/stop', body: {} }])
  expect(w.find('[data-task-stop]').exists()).toBe(false)
  expect(w.text()).toContain('stopped')
  expect(w.text()).toContain('No output yet')
})

test('task invalidations and reconnect/snapshot recover output, failures and empty lists', async () => {
  const { w, h, data } = await setup()
  data.tasks = [task({ state: 'exited', error: 'Failed <b>oops</b>', output: ['x'.repeat(2000), ...Array.from({ length: 110 }, (_, i) => `line ${i}`)] })]
  h.omo.emit('plugin.tasks_changed', { name: 'other' })
  await flushPromises()
  expect(w.text()).not.toContain('Failed')
  h.omo.emit('plugin.tasks_changed', { name: 'demo' })
  await flushPromises()
  expect(w.text()).toContain('Failed <b>oops</b>')
  expect(w.get('pre').text().split('\n')).toHaveLength(100)
  expect(w.find('b').exists()).toBe(false)
  data.tasks = []
  h.omo.tickets = []
  h.omo.stream!.onResync(h.omo.eventId)
  await flushPromises()
  expect(w.text()).toContain('No tasks')
})

test('reconnect reads authoritative state and a lost Stop answer never retries the process action', async () => {
  const { w, h, data } = await setup()
  h.respond.write = () => refuse('network', 'Connection lost', undefined, 0)
  await w.get('[data-task-stop]').trigger('click')
  await flushPromises()
  expect(h.sent).toHaveLength(1)
  expect(w.text()).toContain('may have stopped')
  data.tasks = [task({ state: 'exited', reason: 'stopped' })]
  h.omo.stream!.onState('connecting')
  await flushPromises()
  h.omo.stream!.onState('live')
  await flushPromises()
  expect(w.find('[data-task-stop]').exists()).toBe(false)
  expect(h.sent).toHaveLength(1)
})

test('running instances show their project and ticket and reopen the existing attached forward', async () => {
  const tab = { opener: window, closed: false, location: { href: 'about:blank', replace: vi.fn() }, close: vi.fn() }
  const reserve = vi.spyOn(window, 'open').mockReturnValue(tab as unknown as Window)
  const { w, h } = await setup({ runningOnly: true,
    tasks: [task(), task({ id: 'run-2', name: 'opaque-project', ticket: undefined }), task({ id: 'run-3', name: 'opaque-workdir', ticket: 9 })],
    forwards: [forward(), forward({ id: 'b'.repeat(32), task_id: 'run-2', ticket: null, name: 'VS Code · beta' }), forward({ id: 'c'.repeat(32), task_id: 'run-3', ticket: 9, name: 'VS Code · No project' })],
  })
  const rows = w.findAll('article')
  expect(rows.map(row => row.get('strong').text())).toEqual(['VS Code · alpha', 'VS Code · beta', 'VS Code · No project'])
  expect(rows[0]!.text()).toContain('Ticket #7')
  expect(rows[1]!.text()).not.toContain('Ticket #')
  expect(rows[2]!.text()).toContain('Ticket #9')
  h.respond.write = () => ok({ url: 'https://apps.example/_omo/proxy/bootstrap#fresh-grant' })
  await rows[1]!.get('[data-task-open]').trigger('click')
  await flushPromises()
  expect(reserve).toHaveBeenCalledTimes(1)
  expect(tab.opener).toBeNull()
  expect(h.sent).toEqual([{ method: 'POST', path: `/api/proxy/forwards/${'b'.repeat(32)}/open`, body: {} }])
  expect(tab.location.replace).toHaveBeenCalledWith('https://apps.example/_omo/proxy/bootstrap#fresh-grant')
})

test('settings keep the process name and output for background task inspection', async () => {
  const { w } = await setup({ forwards: [forward()] })
  expect(w.get('strong').text()).toBe('editor')
  expect(w.get('pre').text()).toContain('hello')
  expect(w.find('[data-task-open]').exists()).toBe(false)
})

test('only a live forward of the same plugin, stable run and ticket supplies a label or Open', async () => {
  const { w, h, data } = await setup({ runningOnly: true, forwards: [forward({ plugin: 'other' }), forward({ id: 'b'.repeat(32), ticket: 8 }), forward({ id: 'c'.repeat(32), task_id: 'old-run' })] })
  expect(w.get('strong').text()).toBe('editor')
  expect(w.find('[data-task-open]').exists()).toBe(false)
  h.omo.forwards = [forward()]
  h.omo.emit('proxy.forward_updated', h.omo.forwards[0])
  await flushPromises()
  expect(w.get('strong').text()).toBe('VS Code · alpha')
  expect(w.find('[data-task-open]').exists()).toBe(true)
  h.omo.forwards = []
  h.omo.emit('proxy.forward_removed', forward())
  await flushPromises()
  expect(w.find('[data-task-open]').exists()).toBe(false)
  data.tasks = [task({ id: 'replacement-run' })]
  h.omo.forwards = [forward()]
  h.omo.emit('plugin.tasks_changed', { name: 'demo' })
  await flushPromises()
  expect(w.get('strong').text()).toBe('editor')
  expect(w.find('[data-task-open]').exists()).toBe(false)
})

test('a failed reopen reports the error and closes its reserved blank tab', async () => {
  const tab = { opener: window, closed: false, location: { href: 'about:blank', replace: vi.fn() }, close: vi.fn() }
  vi.spyOn(window, 'open').mockReturnValue(tab as unknown as Window)
  const { w, h } = await setup({ runningOnly: true, forwards: [forward()] })
  h.respond.write = () => refuse('not_found', 'This instance has stopped.', undefined, 404)
  await w.get('[data-task-open]').trigger('click')
  await flushPromises()
  expect(w.get('[role="alert"]').text()).toContain('This instance has stopped.')
  expect(tab.close).toHaveBeenCalledTimes(1)
  expect(tab.location.replace).not.toHaveBeenCalled()
  expect(h.sent).toHaveLength(1)
})
