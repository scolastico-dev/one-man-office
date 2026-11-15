import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import { reactive } from 'vue'
import type { PluginHook, PluginInfo, PluginSettings, PluginsResponse } from '../../../api/types'
import { harness, mountWith, ok, refuse, type Harness } from '../../create/dialogTesting'
import { announcements } from '../../../components/ui/useAnnouncer'
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

/** The element is no live region of its own: what it shows is said once, by the announcer. */
const silent = (el: { attributes(n: string): string | undefined }) => {
  expect(['status', 'alert']).not.toContain(el.attributes('role'))
  expect(el.attributes('aria-live')).toBeUndefined()
}

test('a global action shows its result as text, said once by the announcer', async () => {
  const { w, h } = await open({})
  expect(w.find('[data-plugin-action="mark"]').exists()).toBe(false)
  h.respond.write = () => ok({ message: 'Wrote <b>14</b> tickets‮' })
  await w.get('[data-plugin-action="sum"]').trigger('click')
  await flushPromises()
  expect(h.sent).toEqual([{ method: 'POST', path: '/api/plugins/log/actions/sum', body: {} }])
  const line = w.get('[data-plugin-answer]')
  silent(line)
  expect(announcements.polite.value).toBe('Wrote <b>14</b> tickets\\u{202E}')
  expect(line.text()).toBe('Wrote <b>14</b> tickets\\u{202E}')
  expect(line.find('b').exists()).toBe(false)

  h.respond.write = () => refuse('conflict', 'an action of plugin log is already running', undefined, 409)
  await w.get('[data-plugin-action="sum"]').trigger('click')
  await flushPromises()
  expect(w.get('[data-plugin-action-error] .callout__body').text()).toBe('an action of plugin log is already running')
  expect(w.get('[data-plugin-answer]').text()).toBe('')
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
  silent(w.get('[data-plugin-update-result]'))
  expect(announcements.polite.value).toBe('Installed; now at ddddddd.')
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

test('the box says it is busy while Update runs, so the page keeps it open', async () => {
  const busy: boolean[] = []
  const { w, h } = await open({ onBusy: (b: boolean) => busy.push(b) })
  let release!: (i: PluginInfo) => void
  h.respond.write = () => new Promise((res) => (release = (i) => res(ok(i))))
  await w.get('[data-plugin-update]').trigger('click')
  await flushPromises()
  expect(busy).toEqual([true])
  release(info())
  await flushPromises()
  expect(busy).toEqual([true, false])
})

test('a plugin removed elsewhere while its box is open says so, drops unsaved edits and offers nothing that would fail', async () => {
  const h = await harness()
  const client = h.session.client
  const baseGet = client.get.bind(client)
  client.get = ((path: string, q?: never) => {
    if (path === '/api/plugins') return Promise.resolve(ok({ plugins: [], settings: [settings()], bundled: [], errors: [] }))
    if (path === '/api/plugins/log/log') return Promise.resolve(ok([]))
    return baseGet(path, q)
  }) as typeof client.get
  const dirty: boolean[] = []
  const props = reactive<Record<string, unknown>>({ name: 'log', info: info(), settings: settings(), onDirty: (d: boolean) => dirty.push(d) })
  const w = await mountWith(h, PluginDetail, props)
  mounted = w
  await flushPromises()
  await w.get('[data-plugin-config] textarea').setValue('{"mark": "two"}')
  await flushPromises()
  expect(dirty.at(-1)).toBe(true)

  props.info = null
  props.settings = null
  await flushPromises()
  expect(w.get('[data-plugin-gone] .callout__body').text()).toBe('The plugin log no longer exists.')
  expect(dirty.at(-1)).toBe(false)
  for (const sel of ['[data-plugin-remove]', '[data-plugin-update]', '[data-plugin-enabled]', '[data-plugin-action]', '[data-plugin-config]', '[data-plugin-log]', '[data-plugin-missing]']) {
    expect(w.find(sel).exists(), sel).toBe(false)
  }
})

test('a refused Update and a refused switch are callouts in place', async () => {
  const { w, h } = await open({})
  h.respond.write = () => refuse('conflict', 'plugin log is being installed, updated, changed or removed; try again when that is done', undefined, 409)
  await w.get('[data-plugin-update]').trigger('click')
  await flushPromises()
  expect(w.get('[data-plugin-update-error] .callout__body').text()).toContain('try again when that is done')
  expect(w.find('[data-plugin-update-result]').exists()).toBe(false)
  await w.get('[data-plugin-enabled]').trigger('click')
  await flushPromises()
  expect(w.get('[data-plugin-switch-error] .callout__body').text()).toContain('try again when that is done')
})

/** A box whose list read answers `fresh`, as the server holds it now. */
async function withFresh(fresh: PluginSettings) {
  const h = await harness()
  const client = h.session.client
  const baseGet = client.get.bind(client)
  client.get = ((path: string, q?: never) => {
    if (path === '/api/plugins') return Promise.resolve(ok({ plugins: [], settings: [fresh], bundled: [], errors: [] }))
    if (path === '/api/plugins/log/log') return Promise.resolve(ok([]))
    return baseGet(path, q)
  }) as typeof client.get
  const w = await mountWith(h, PluginDetail, { name: 'log', info: info(), settings: settings() })
  mounted = w
  await flushPromises()
  return { w, h }
}

test('the switch right after another write: when only the revision was stale it reads again and sends once more', async () => {
  const { w, h } = await withFresh(settings({ revision: 'rev2' }))
  let calls = 0
  h.respond.write = () => (++calls === 1 ? refuse('stale_settings', 'the plugin settings changed since they were read', undefined, 409) : ok(info({ state: 'disabled' })))
  await w.get('[data-plugin-enabled]').trigger('click')
  await flushPromises()
  expect(h.sent.map((s) => s.ifMatch)).toEqual(['rev1', 'rev2'])
  expect(w.find('[data-plugin-switch-error]').exists()).toBe(false)
  expect(w.get('[data-plugin-enabled]').attributes('aria-checked')).toBe('false')
})

test('the switch after a real change elsewhere is not sent again: it says so', async () => {
  const { w, h } = await withFresh(settings({ revision: 'rev2', config: { mark: 'changed elsewhere' } }))
  h.respond.write = () => refuse('stale_settings', 'the plugin settings changed since they were read', undefined, 409)
  await w.get('[data-plugin-enabled]').trigger('click')
  await flushPromises()
  expect(h.sent).toHaveLength(1)
  expect(w.get('[data-plugin-switch-error]').text()).toContain('switch again if you still want to')
})
