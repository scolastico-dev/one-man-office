import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import type { PluginTask } from '../../../api/types'
import { harness, mountWith, ok, refuse } from '../../create/dialogTesting'
import PluginTasks from '../../plugins/PluginTasks.vue'
let mounted: VueWrapper | null = null
afterEach(() => { mounted?.unmount(); mounted = null })
const task = (o: Partial<PluginTask> = {}): PluginTask => ({ id: 'run-1', name: 'editor', ticket: 7, started_at: '2026-11-18T10:00:00Z', state: 'running', output: ['hello <script>‮'], ...o })
async function setup() {
  const h = await harness()
  const data = { tasks: [task()], reads: 0 }
  const base = h.session.client.get.bind(h.session.client)
  h.session.client.get = ((path: string, q?: never) => {
    if (path !== '/api/plugins/demo/tasks') return base(path, q)
    data.reads++
    return Promise.resolve(ok({ tasks: data.tasks }))
  }) as typeof base
  const w = await mountWith(h, PluginTasks, { name: 'demo' })
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
