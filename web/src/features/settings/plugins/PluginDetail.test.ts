import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import type { PluginHook, PluginInfo, PluginSettings, PluginsResponse } from '../../../api/types'
import { harness, mountWith, ok, refuse, type Harness } from '../../create/dialogTesting'
import PluginDetail from './PluginDetail.vue'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
})

const manual = (name: string, description: string, scope = 'global'): PluginHook => ({ event: 'manual', name, description, scope, interval: '' })
const settings = (o: Partial<PluginSettings> = {}): PluginSettings => ({ name: 'log', source: '/home/me/log', subpath: '', ref: '', enabled: true, config: {}, revision: 'rev1', ...o })
const info = (o: Partial<PluginInfo> = {}): PluginInfo => ({
  name: 'log',
  version: '0.3.1',
  description: 'Writes a log',
  state: 'ready',
  message: '',
  commit: '9c2e4f1aabb',
  last_event: '',
  last_run_at: null,
  hooks: [{ event: 'ticket_moved', name: '', description: '', scope: '', interval: '' }, manual('sum', 'Write a summary'), manual('mark', 'Mark it', 'ticket')],
  ...o,
})

async function open(props: Record<string, unknown>): Promise<{ w: VueWrapper; h: Harness }> {
  const h = await harness()
  const read = { plugins: [], settings: [settings()], bundled: [], errors: [] } as PluginsResponse
  const client = h.session.client
  const baseGet = client.get.bind(client)
  client.get = ((path: string, q?: never) => {
    if (path === '/api/plugins') return Promise.resolve(ok(read))
    if (path === '/api/plugins/log/log') return Promise.resolve(ok([]))
    return baseGet(path, q)
  }) as typeof client.get
  const w = await mountWith(h, PluginDetail, { name: 'log', info: info(), settings: settings(), ...props })
  mounted = w
  await flushPromises()
  return { w, h }
}

test('shows the state, the source, the installed commit and the hooks in words', async () => {
  const { w } = await open({})
  expect(w.get('[data-plugin-source]').text()).toBe('/home/me/log')
  expect(w.get('[data-plugin-version]').text()).toBe('0.3.1 at 9c2e4f1')
  expect(w.text()).toContain('When a ticket is moved')
  expect(w.text()).toContain('Button here: Write a summary')
  expect(w.text()).toContain('Button on a ticket: Mark it')
  expect(w.get('[data-plugin-update]').text()).toBe('Update')
})

test('a global action shows its result as text in a status line', async () => {
  const { w, h } = await open({})
  expect(w.find('[data-plugin-action="mark"]').exists()).toBe(false)
  h.respond.write = () => ok({ message: 'Wrote <b>14</b> tickets‮' })
  await w.get('[data-plugin-action="sum"]').trigger('click')
  await flushPromises()
  expect(h.sent).toEqual([{ method: 'POST', path: '/api/plugins/log/actions/sum', body: {} }])
  const line = w.get('[data-plugin-answer]')
  expect(line.attributes('role')).toBe('status')
  expect(line.text()).toBe('Wrote <b>14</b> tickets\\u{202E}')
  expect(line.find('b').exists()).toBe(false)

  h.respond.write = () => refuse('conflict', 'an action of plugin log is already running', undefined, 409)
  await w.get('[data-plugin-action="sum"]').trigger('click')
  await flushPromises()
  expect(w.get('[data-plugin-answer]').text()).toBe('an action of plugin log is already running')
})

test('the error of the last run is shown as text', async () => {
  const { w } = await open({ info: info({ state: 'error', message: 'hooks/moved.lua:14: <i>nil</i>' }) })
  expect(w.get('[data-plugin-failure]').text()).toContain('hooks/moved.lua:14: <i>nil</i>')
  expect(w.find('[data-plugin-failure] i').exists()).toBe(false)
})

test('a plugin whose files are missing offers Install, which shows busy and then the commit', async () => {
  const { w, h } = await open({ info: info({ state: 'not_installed', commit: '', hooks: [] }) })
  expect(w.find('[data-plugin-missing]').exists()).toBe(true)
  let release!: (i: PluginInfo) => void
  h.respond.write = () => new Promise((res) => (release = (i) => res(ok(i))))
  await w.get('[data-plugin-update]').trigger('click')
  expect(w.get('[data-plugin-update]').text()).toBe('Installing…')
  expect(w.get('[data-plugin-update]').attributes('disabled')).toBeDefined()
  release(info({ commit: 'ddddddd1234' }))
  await flushPromises()
  expect(w.get('[data-plugin-update-result]').text()).toBe('Installed; now at ddddddd.')
  expect(h.sent).toEqual([{ method: 'POST', path: '/api/plugins/log/update', body: {} }])
})

test('the switch saves at once with If-Match and the stored configuration', async () => {
  const { w, h } = await open({ settings: settings({ config: { mark: 'one' } }) })
  h.respond.write = () => ok(info({ state: 'disabled' }))
  await w.get('[data-plugin-enabled]').trigger('click')
  await flushPromises()
  expect(h.sent).toEqual([
    { method: 'PUT', path: '/api/plugins/log', body: { name: 'log', source: '/home/me/log', subpath: '', ref: '', enabled: false, config: { mark: 'one' } }, ifMatch: 'rev1' },
  ])
})

test('remove asks first and says the stored data is deleted', async () => {
  let removed = 0
  const { w, h } = await open({ onRemoved: () => removed++ })
  let asked = ''
  window.confirm = (q?: string) => {
    asked = q ?? ''
    return false
  }
  await w.get('[data-plugin-remove]').trigger('click')
  await flushPromises()
  expect(asked).toContain('Remove the plugin log?')
  expect(asked).toContain('everything it stored')
  expect(h.sent).toEqual([])
  window.confirm = () => true
  h.respond.write = () => ok(undefined)
  await w.get('[data-plugin-remove]').trigger('click')
  await flushPromises()
  expect(h.sent).toEqual([{ method: 'DELETE', path: '/api/plugins/log', body: undefined }])
  expect(removed).toBe(1)
})
