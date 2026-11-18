import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test, vi } from 'vitest'
import type { PluginInfo, PluginRunResult, PluginsResponse, TicketSummary } from '../../api/types'
import { ticket } from '../board/testing'
import { harness, mountWith, ok } from '../create/dialogTesting'
import PluginControls from './PluginControls.vue'
import { disposePluginExtensions } from './usePluginExtensions'

const cleanups: (() => void)[] = []
afterEach(() => { cleanups.splice(0).forEach((f) => f()); vi.restoreAllMocks() })
const plugin: PluginInfo = { name: 'editor', state: 'ready', version: '1.0.0', content_id: 'identity', commit: '', description: '', message: '', last_event: '', last_run_at: null, hooks: [], frontend: {}, config_fields: [], buttons: [
  { label: 'Edit', place: 'ticket', action: 'edit', per_worktree: true },
  { label: 'Project editor', place: 'project', action: 'project' },
  { label: 'Global', place: 'sidebar', action: 'global' },
] }
async function setup(props: { place: 'ticket' | 'project' | 'sidebar'; ticket?: TicketSummary; project?: string }) {
  const h = await harness()
  const response: PluginsResponse = { plugins: [plugin], settings: [{ name: 'editor', source: '/repo', subpath: '', ref: '', enabled: true, config: {} }], errors: [] }
  const base = h.session.client.get.bind(h.session.client)
  h.session.client.get = ((path: string, q?: never) => path === '/api/plugins' ? Promise.resolve(ok(response)) : base(path, q)) as typeof base
  h.respond.write = () => ok({ message: 'Done' })
  const w: VueWrapper = await mountWith(h, PluginControls, props)
  cleanups.push(() => { w.unmount(); disposePluginExtensions(h.session.store) })
  return { w, h }
}

test('rendered worktree choices issue distinct ticket/project payloads; sidebar project controls send project alone', async () => {
  const t = ticket({ number: 7, projects: [{ project: 'one', worktree: '/work/one' }, { project: 'two', worktree: '/work/two' }] as never })
  const { w, h } = await setup({ place: 'ticket', ticket: t })
  expect(w.findAll('button').map((b) => b.text())).toEqual(['Edit — one', 'Edit — two'])
  await w.findAll('button')[1]!.trigger('click')
  await flushPromises()
  expect(h.sent).toEqual([{ method: 'POST', path: '/api/plugins/editor/actions/edit', body: { ticket: 7, project: 'two' } }])
  const project = await setup({ place: 'project', project: 'one' })
  await project.w.get('button').trigger('click')
  await flushPromises()
  expect(project.h.sent[0]?.body).toEqual({ project: 'one' })
})

test('missing worktrees show an explanation and cannot send an action', async () => {
  const { w, h } = await setup({ place: 'ticket', ticket: ticket({ projects: [], workdir: '' }) })
  expect(w.get('button').attributes('disabled')).toBeDefined()
  expect(w.text()).toContain('no available working folder')
  await w.get('button').trigger('click')
  expect(h.sent).toEqual([])
})

test('a pending button reserves one detached tab, blocks double activation and opens final forward only after the hook', async () => {
  const tab = { opener: window, closed: false, location: { href: 'about:blank', replace: vi.fn() }, close: vi.fn() }
  const reserve = vi.spyOn(window, 'open').mockReturnValue(tab as unknown as Window)
  const { w, h } = await setup({ place: 'sidebar' })
  let finish!: (result: PluginRunResult) => void
  h.respond.write = ({ path }) => path.endsWith('/open') ? ok({ url: 'https://apps.example/_omo/proxy/bootstrap#grant' }) : new Promise((resolve) => { finish = (result) => resolve(ok(result)) })
  await w.get('button').trigger('click')
  expect(reserve).toHaveBeenCalledTimes(1)
  expect(tab.opener).toBeNull()
  expect(w.get('button').attributes('aria-busy')).toBe('true')
  await w.get('button').trigger('click')
  expect(h.sent).toHaveLength(1)
  finish({ message: 'Ready', forward_id: '0123456789abcdef0123456789abcdef' })
  await flushPromises()
  expect(h.sent.map((s) => s.path)).toEqual(['/api/plugins/editor/actions/global', '/api/proxy/forwards/0123456789abcdef0123456789abcdef/open'])
  expect(tab.location.replace).toHaveBeenCalledWith('https://apps.example/_omo/proxy/bootstrap#grant')
  expect(w.text()).toContain('Ready')
})
