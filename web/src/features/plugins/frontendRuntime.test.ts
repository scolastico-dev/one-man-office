import { flushPromises } from '@vue/test-utils'
import { afterEach, expect, test, vi } from 'vitest'
import { shallowRef, ref } from 'vue'
import type { ApiClient } from '../../api/client'
import type { PluginInfo, PluginsResponse } from '../../api/types'
import { createFrontendRuntime, type PluginFacade, type PluginContext } from './frontendRuntime'
import { ticketActionsOf } from '../ticket/pluginActions'
import { manualActions } from '../settings/plugins/pluginForms'
import { controlsOf } from './controls'

const info = (name: string, content_id = 'a'.repeat(64)): PluginInfo => ({ name, content_id, version: '1', description: '', state: 'ready', message: '', commit: '', last_event: '', last_run_at: null, hooks: [{ event: 'manual', name: 'run', description: '', scope: 'global', interval: '' }], buttons: [], frontend: { script: 'ui/entry.js', assets: 'public' }, config_fields: [] })
const response = (...plugins: PluginInfo[]): PluginsResponse => ({ plugins, settings: plugins.map((p) => ({ name: p.name, enabled: true, config: {}, source: '/repo', subpath: '', ref: '' })), errors: [] })
const disposers: (() => void)[] = []
afterEach(() => { disposers.splice(0).forEach((f) => f()); vi.restoreAllMocks() })
function setup(...plugins: PluginInfo[]) {
  // Unit tests drive load/error explicitly; the browser test executes real script bytes under CSP.
  const append = document.head.append.bind(document.head)
  vi.spyOn(document.head, 'append').mockImplementation((...nodes) => {
    for (const node of nodes) if (node instanceof HTMLScriptElement) node.type = 'text/plain'
    append(...nodes)
  })
  const list = shallowRef<PluginsResponse | null>(response(...plugins))
  const context = shallowRef<PluginContext>({ page: 'board' })
  const suspended = ref(false)
  const send = vi.fn().mockResolvedValue({ data: { message: 'ran' }, eventId: null })
  const runtime = createFrontendRuntime({ list, context, suspended, client: { send } as unknown as ApiClient })
  disposers.push(runtime.dispose)
  const script = (name: string) => document.querySelector<HTMLScriptElement & { omo: PluginFacade }>(`script[data-omo-plugin="${name}"]`)!
  return { runtime, list, context, suspended, send, script }
}

test('concurrent entries receive separate facades and only invoke their own manual hooks', async () => {
  const { script, send, runtime } = setup(info('one'), info('two'))
  const one = script('one').omo
  const two = script('two').omo
  expect(one).not.toBe(two)
  one.initialize((omo) => { omo.addButton({ id: 'a', place: 'sidebar', label: 'One', action: 'run' }) })
  two.initialize((omo) => { omo.addButton({ id: 'a', place: 'ticket', label: 'Two', action: 'run' }) })
  script('one').dispatchEvent(new Event('load'))
  script('two').dispatchEvent(new Event('load'))
  await flushPromises()
  expect(runtime.buttons.value.map((b) => [b.plugin, b.label])).toEqual([['one', 'One'], ['two', 'Two']])
  expect(one.assetUrl('images/logo.svg')).toBe('/plugin-assets/one/' + 'a'.repeat(64) + '/public/images/logo.svg')
  await one.runHook('run')
  await two.runHook('run')
  expect(send.mock.calls.map((c) => c[1])).toEqual(['/api/plugins/one/actions/run', '/api/plugins/two/actions/run'])
  await expect(one.runHook('not-manual')).rejects.toThrow(/manual/i)
  expect(() => one.assetUrl('../settings.yaml')).toThrow(/asset/i)
})

test('route updates notify once with context data; removal clears controls, callbacks and async registration', async () => {
  const { script, runtime, list, context } = setup(info('one'))
  const omo = script('one').omo
  const routes = vi.fn()
  const cleanup = vi.fn()
  let finish!: () => void
  omo.initialize(async () => {
    omo.addPanel({ id: 'p', place: 'ticket', title: 'Panel', text: '<b>text</b>', image: omo.assetUrl('logo.svg') })
    omo.onRouteChange(routes)
    await new Promise<void>((r) => { finish = r })
    expect(() => omo.addButton({ id: 'late', place: 'sidebar', label: 'Late', action: 'run' })).toThrow(/active/i)
    return cleanup
  })
  await flushPromises()
  expect(runtime.panels.value).toHaveLength(1)
  context.value = { page: 'ticket', ticket: { number: 7, title: 'Ticket', projects: [{ id: 'repo', worktree: '/work' }], workdir: '' } }
  await flushPromises()
  expect(routes.mock.calls.map((c) => c[0].page)).toEqual(['board', 'ticket'])
  list.value = response()
  await flushPromises()
  expect(omo.signal.aborted).toBe(true)
  expect(runtime.panels.value).toEqual([])
  context.value = { page: 'settings' }
  finish()
  await flushPromises()
  expect(routes).toHaveBeenCalledTimes(2)
  expect(cleanup).toHaveBeenCalledTimes(1)
})

test('update replaces one lifetime, route changes keep it, and an old facade cannot remove new registrations', async () => {
  const { script, list, runtime, context } = setup(info('one'))
  const oldScript = script('one')
  const old = oldScript.omo
  old.initialize(() => { old.addButton({ id: 'a', place: 'sidebar', label: 'Old', action: 'run' }) })
  await flushPromises()
  context.value = { page: 'settings' }
  await flushPromises()
  expect(script('one')).toBe(oldScript)
  list.value = response(info('one', 'b'.repeat(64)))
  await flushPromises()
  expect(script('one')).not.toBe(oldScript)
  const current = script('one').omo
  current.initialize(() => { current.addButton({ id: 'a', place: 'sidebar', label: 'New', action: 'run' }) })
  await flushPromises()
  expect(() => old.removeButton('a')).toThrow(/active/i)
  expect(runtime.buttons.value.map((b) => b.label)).toEqual(['New'])
})

test('initialization/load failure removes partial controls, isolates other plugins and supports retry', async () => {
  const { script, runtime } = setup(info('bad'), info('good'))
  const bad = script('bad').omo
  bad.initialize(() => { bad.addButton({ id: 'a', place: 'sidebar', label: 'Partial', action: 'run' }); throw new Error('boom') })
  script('good').omo.initialize((omo) => { omo.addButton({ id: 'a', place: 'sidebar', label: 'Good', action: 'run' }) })
  await flushPromises()
  expect(runtime.buttons.value.map((b) => b.label)).toEqual(['Good'])
  expect(runtime.errors.value.map((e) => e.plugin)).toEqual(['bad'])
  runtime.retry('bad')
  const next = script('bad')
  expect(next.omo).not.toBe(bad)
  next.dispatchEvent(new Event('error'))
  expect(runtime.errors.value[0]?.message).toMatch(/load/i)
})

test('locking blocks hook calls and controls without losing registrations; disabled entries never load', async () => {
  const disabled = info('off'); disabled.state = 'disabled'
  const { script, runtime, suspended, send } = setup(info('one'), disabled)
  expect(script('off')).toBeNull()
  const omo = script('one').omo
  omo.initialize(() => { omo.addButton({ id: 'a', place: 'sidebar', label: 'One', action: 'run' }) })
  await flushPromises()
  suspended.value = true
  await expect(omo.runHook('run')).rejects.toThrow(/locked/i)
  expect(send).not.toHaveBeenCalled()
  expect(runtime.buttons.value).toHaveLength(1)
  suspended.value = false
  await omo.runHook('run')
  expect(send).toHaveBeenCalledTimes(1)
})

test('unlock delivers the latest route context once without rerunning initialization', async () => {
  const { script, context, suspended } = setup(info('one'))
  const routes = vi.fn()
  const init = vi.fn((omo: PluginFacade) => { omo.onRouteChange(routes) })
  script('one').omo.initialize(init)
  await flushPromises()
  suspended.value = true
  context.value = { page: 'settings' }
  await flushPromises()
  expect(routes.mock.calls.map((c) => c[0].page)).toEqual(['board'])
  suspended.value = false
  await flushPromises()
  expect(routes.mock.calls.map((c) => c[0].page)).toEqual(['board', 'settings'])
  expect(init).toHaveBeenCalledTimes(1)
})

test('a failing route listener cancels its remaining listeners and leaves another plugin usable', async () => {
  const { script, context } = setup(info('one'), info('two'))
  let first = true
  const after = vi.fn()
  const other = vi.fn()
  script('one').omo.initialize((omo) => {
    omo.onRouteChange(() => { if (!first) throw new Error('failed'); first = false })
    omo.onRouteChange(after)
  })
  script('two').omo.initialize((omo) => { omo.onRouteChange(other) })
  await flushPromises()
  context.value = { page: 'settings' }
  await flushPromises()
  expect(after).toHaveBeenCalledTimes(1)
  expect(other).toHaveBeenCalledTimes(2)
})

test('a disabled plugin cannot navigate an in-flight forward Open', async () => {
  const { script, list, send } = setup(info('one'))
  const tab = { opener: window, closed: false, location: { href: 'about:blank', replace: vi.fn() }, close: vi.fn() }
  vi.spyOn(window, 'open').mockReturnValue(tab as unknown as Window)
  let finish!: (value: { data: { url: string } }) => void
  send.mockReturnValue(new Promise((r) => { finish = r }))
  const pending = script('one').omo.openForward('0123456789abcdef0123456789abcdef')
  list.value = response()
  finish({ data: { url: 'https://forward.example/_omo/proxy/bootstrap#grant' } })
  await expect(pending).rejects.toThrow(/active/i)
  expect(tab.location.replace).not.toHaveBeenCalled()
  expect(tab.close).toHaveBeenCalledTimes(1)
})

test('async route listener rejection removes partial controls and obsolete listener failures cannot affect a replacement', async () => {
  const { script, context, runtime, list } = setup(info('one'))
  let reject!: (error: Error) => void
  script('one').omo.initialize((omo) => {
    omo.addPanel({ id: 'p', place: 'sidebar', title: 'Panel', text: 'Waiting' })
    omo.onRouteChange(async () => { await new Promise<void>((_resolve, fail) => { reject = fail }) })
  })
  await flushPromises()
  reject(new Error('listener failed'))
  await flushPromises()
  expect(runtime.panels.value).toEqual([])
  expect(runtime.errors.value.map((e) => e.plugin)).toEqual(['one'])
  runtime.retry('one')
  script('one').omo.initialize((omo) => {
    omo.onRouteChange(async () => { await new Promise<void>((_resolve, fail) => { reject = fail }) })
  })
  await flushPromises()
  list.value = response(info('one', 'b'.repeat(64)))
  script('one').omo.initialize((omo) => { omo.addPanel({ id: 'new', place: 'sidebar', title: 'New', text: 'Ready' }) })
  reject(new Error('obsolete failure'))
  context.value = { page: 'settings' }
  await flushPromises()
  expect(runtime.errors.value).toEqual([])
  expect(runtime.panels.value.map((p) => p.title)).toEqual(['New'])
})

test('top-level entry errors after initialize remove that plugin and unrelated script errors do not', async () => {
  const { script, runtime } = setup(info('one'))
  const entry = script('one')
  entry.omo.initialize((omo) => { omo.addButton({ id: 'a', place: 'sidebar', label: 'One', action: 'run' }) })
  await flushPromises()
  window.dispatchEvent(new ErrorEvent('error', { filename: 'https://other.example/script.js', message: 'other script' }))
  expect(runtime.buttons.value).toHaveLength(1)
  window.dispatchEvent(new ErrorEvent('error', { filename: entry.src, message: 'private error text' }))
  expect(runtime.buttons.value).toEqual([])
  expect(runtime.errors.value.map((e) => e.plugin)).toEqual(['one'])
  expect(runtime.errors.value[0]?.message).not.toContain('private error text')
})

test.each(['CON.txt', 'COM¹.png', 'safe/LPT1.svg', 'conout$.txt', 'C:/file', '\\\\server\\file', 'folder/.data/file', 'folder/settings.json', 'safe/file.'])('assetUrl refuses the portable public-path violation %s', (path) => {
  const { script } = setup(info('one'))
  expect(() => script('one').omo.assetUrl(path)).toThrow(/asset/i)
})

test('assetUrl encodes literal percent characters once instead of treating them as traversal', () => {
  const { script } = setup(info('one'))
  expect(script('one').omo.assetUrl('logo%20.png')).toBe('/plugin-assets/one/' + 'a'.repeat(64) + '/public/logo%2520.png')
})

test('an installed commit update renews frontend lifetime even when its public bytes are identical', async () => {
  const { script, list, runtime } = setup({ ...info('one'), commit: 'old' })
  const entry = script('one')
  const facade = entry.omo
  facade.initialize((omo) => { omo.addButton({ id: 'a', place: 'sidebar', label: 'Old', action: 'run' }) })
  await flushPromises()
  list.value = response({ ...info('one'), commit: 'new' })
  await flushPromises()
  expect(script('one')).not.toBe(entry)
  expect(facade.signal.aborted).toBe(true)
  expect(runtime.buttons.value).toEqual([])
})

test('another entry cannot retrieve a plugin facade from its script element after load or failure', async () => {
  const { script } = setup(info('one'), info('two'))
  const first = script('one')
  const facade = first.omo
  facade.initialize(() => {})
  first.dispatchEvent(new Event('load'))
  expect((document.querySelector('script[data-omo-plugin="one"]') as HTMLScriptElement & { omo?: PluginFacade }).omo).toBeUndefined()
  const second = script('two')
  second.dispatchEvent(new Event('error'))
  expect(second.omo).toBeUndefined()
  await flushPromises()
  expect(facade.signal.aborted).toBe(false)
})

test('route subscriptions ignore identical context replacements and unchanged unlocks', async () => {
  const { script, context, suspended } = setup(info('one'))
  const routes = vi.fn()
  script('one').omo.initialize((omo) => { omo.onRouteChange(routes) })
  await flushPromises()
  context.value = { page: 'board' }
  await flushPromises()
  suspended.value = true
  await flushPromises()
  suspended.value = false
  await flushPromises()
  expect(routes).toHaveBeenCalledTimes(1)
})

test('a scripted action button uses its own hook scope when a ticket is shown', async () => {
  const { script, runtime, send } = setup(info('one'))
  script('one').omo.initialize((omo) => { omo.addButton({ id: 'a', label: 'Global', place: 'ticket', action: 'run' }) })
  await flushPromises()
  await runtime.buttons.value[0]!.invoke({ page: 'ticket', ticket: { number: 7, title: 'T', workdir: '', projects: [] }, project: 'repo' })
  expect(send).toHaveBeenCalledExactlyOnceWith('POST', '/api/plugins/one/actions/run', { project: 'repo' })
})

// Visibility is presentation metadata: a facade must still invoke its own hidden hook.
test('a plugin facade and explicit declarations can invoke hidden manual hooks', async () => {
  const plugin = info('one')
  plugin.hooks = [{ ...plugin.hooks[0]!, hidden: true }]
  plugin.buttons = [{ label: 'Declared', place: 'sidebar', action: 'run' }]
  const { script, send, list } = setup(plugin)
  const facade = script('one').omo
  facade.initialize(() => {})
  await flushPromises()
  await expect(facade.runHook('run')).resolves.toEqual({ message: 'ran' })
  expect(send).toHaveBeenCalledExactlyOnceWith('POST', '/api/plugins/one/actions/run', {})
  expect(controlsOf(list.value, [], 'sidebar').map((b) => [b.label, b.action])).toEqual([['Declared', 'run']])
  expect(manualActions(plugin, 'global')).toEqual([])
})

// Failing the frontend must not resurrect either generic ticket or settings buttons.
test.each(['load', 'initialize'])('hidden hooks do not create duplicate controls after frontend %s failure', async (failure) => {
  const plugin = info('one')
  plugin.hooks = [
    { ...plugin.hooks[0]!, hidden: true },
    { ...plugin.hooks[0]!, name: 'ticket', scope: 'ticket', hidden: true },
    { ...plugin.hooks[0]!, name: 'unlisted-ticket', scope: 'ticket', hidden: true },
  ]
  plugin.buttons = [{ label: 'Declared', place: 'ticket', action: 'ticket' }]
  const { script, runtime, list } = setup(plugin)
  const entry = script('one')
  if (failure === 'load') entry.dispatchEvent(new Event('error'))
  else entry.omo.initialize((omo) => {
    omo.addButton({ id: 'partial', place: 'ticket', label: 'Partial', action: 'ticket' })
    throw new Error('Initialization failed')
  })
  await flushPromises()
  expect(runtime.errors.value.map((e) => e.plugin)).toEqual(['one'])
  expect(runtime.buttons.value).toEqual([])
  expect(ticketActionsOf(list.value)).toEqual([])
  expect(manualActions(list.value!.plugins[0]!, 'global')).toEqual([])
  expect(controlsOf(list.value, runtime.buttons.value, 'ticket').map((b) => b.label)).toEqual(['Declared'])
})
