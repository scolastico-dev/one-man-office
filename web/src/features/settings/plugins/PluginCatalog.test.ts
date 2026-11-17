import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import { defineComponent, h, provide } from 'vue'
import type { Query } from '../../../api/client'
import type { CatalogPlugin, PluginCatalog as Catalog, PluginInfo, PluginSettings } from '../../../api/types'
import { DialogOutlet, useDialogScope } from '../../../components/ui/mountDialog'
import { harness, mountWith, ok, refuse, type Harness } from '../../create/dialogTesting'
import PluginCatalog from './PluginCatalog.vue'
import { PLUGIN_DIALOGS, acknowledgementOf, confirmationOf, installedFrom, listWarning, trustTagOf, whereFrom } from './pluginTrust'
import { ApiError } from '../../../api/client'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
})

const REPO = 'https://github.com/scolastico-dev/one-man-office.git'
const entry = (o: Partial<CatalogPlugin> = {}): CatalogPlugin => ({
  name: 'pushover',
  description: 'Push notifications.',
  tag: 'official',
  source: REPO,
  ref: 'release',
  subpath: 'plugins/pushover',
  list: '',
  origin: '',
  ...o,
})
const LIST: CatalogPlugin[] = [
  entry(),
  entry({ name: 'ticket-log', tag: 'recommended', description: 'Logs <b>tickets</b>', source: 'https://example.org/me/ticket-log.git', ref: 'v1.0.0', subpath: '' }),
  entry({ name: 'notes', tag: 'community', description: 'Notes ‮ here', source: 'git@example.org:me/notes.git', ref: '', subpath: 'plugin' }),
]
const catalog = (o: Partial<Catalog> = {}): Catalog => ({ available: true, message: '', source: REPO, ref: 'release', fetched_at: '2026-11-16T09:00:00Z', plugins: LIST, lists: [], ...o })
const info = (name: string): PluginInfo => ({ name, version: '1.0.0', description: '', state: 'ready', message: '', commit: 'a'.repeat(40), last_event: '', last_run_at: null, hooks: [] })

/** Mounts the section with the page's dialog scope; `answers` answers each read of the list in turn (the last one repeats). */
async function open(answers: Catalog[], props: { settings?: PluginSettings[]; names?: string[] } = {}) {
  const hn: Harness = await harness()
  const reads: (Query | undefined)[] = []
  const client = hn.session.client
  const baseGet = client.get.bind(client)
  client.get = ((path: string, q?: Query) => {
    if (path === '/api/plugins/catalog') {
      reads.push(q)
      return Promise.resolve(ok(answers[Math.min(reads.length - 1, answers.length - 1)]))
    }
    return baseGet(path, q as never)
  }) as typeof client.get
  const installed: PluginInfo[] = []
  const Page = defineComponent({
    setup() {
      const dialogs = useDialogScope()
      provide(PLUGIN_DIALOGS, dialogs)
      return () => [h(PluginCatalog, { settings: props.settings ?? [], names: props.names ?? [], onInstalled: (i: PluginInfo) => installed.push(i) }), h(DialogOutlet, { scope: dialogs })]
    },
  })
  const w = await mountWith(hn, Page, {})
  mounted = w
  await flushPromises()
  return { w, hn, reads, installed }
}

const row = (w: VueWrapper, name: string) => w.get(`[data-catalog-plugin="${name}"]`)
const dialog = () => document.body.querySelector<HTMLElement>('[data-trust-dialog]')

test('the list shows each plugin with its tag, description and source as text; installed and taken names have no Install', async () => {
  const { w, reads } = await open([catalog()], { settings: [{ name: 'push', source: REPO, subpath: 'plugins/pushover', ref: 'release', enabled: true, config: {} }], names: ['push', 'notes'] })
  expect(reads).toEqual([undefined])
  expect(row(w, 'pushover').get('[data-catalog-tag]').text()).toBe('official')
  expect(row(w, 'ticket-log').get('[data-catalog-tag]').text()).toBe('recommended')
  expect(row(w, 'notes').get('[data-catalog-tag]').text()).toBe('community')
  expect(row(w, 'ticket-log').get('[data-catalog-description]').text()).toBe('Logs <b>tickets</b>')
  expect(row(w, 'ticket-log').find('b').exists()).toBe(false)
  expect(row(w, 'notes').get('[data-catalog-description]').text()).toBe('Notes \\u{202E} here')
  expect(row(w, 'ticket-log').get('[data-catalog-source]').text()).toBe('https://example.org/me/ticket-log.git, branch or tag v1.0.0')
  expect(row(w, 'notes').get('[data-catalog-source]').text()).toBe('git@example.org:me/notes.git, default branch, folder plugin')
  expect(row(w, 'pushover').get('[data-catalog-installed]').text()).toBe('Installed')
  expect(row(w, 'notes').find('[data-catalog-install]').exists()).toBe(false)
  expect(row(w, 'notes').find('[data-catalog-name-taken]').exists()).toBe(true)
  expect(row(w, 'ticket-log').get('[data-catalog-install]').text()).toBe('Install ticket-log')
})

test('an official plugin installs at once, without a question or an acknowledgement', async () => {
  const { w, hn, installed } = await open([catalog()])
  hn.respond.write = () => ok(info('pushover'))
  await row(w, 'pushover').get('[data-catalog-install]').trigger('click')
  await flushPromises()
  expect(dialog()).toBeNull()
  expect(hn.sent).toEqual([{ method: 'POST', path: '/api/plugins', body: { name: 'pushover', source: REPO, subpath: 'plugins/pushover', ref: 'release', enabled: true, config: {} } }])
  expect('acknowledge' in (hn.sent[0]!.body as object) && (hn.sent[0]!.body as { acknowledge?: string }).acknowledge !== undefined).toBe(false)
  expect(installed.map((i) => i.name)).toEqual(['pushover'])
})

test('a community plugin asks first with the strong warning and its source; Cancel sends nothing, Install sends the acknowledgement', async () => {
  const { w, hn, installed } = await open([catalog()])
  hn.respond.write = () => ok(info('notes'))
  await row(w, 'notes').get('[data-catalog-install]').trigger('click')
  await flushPromises()
  let d = dialog()!
  expect(d.getAttribute('data-trust-dialog')).toBe('community')
  const warning = d.querySelector('[data-trust-warning]')!
  expect(warning.getAttribute('data-tone')).toBe('danger')
  expect(warning.textContent).toContain('Nobody reviewed this plugin')
  expect(warning.textContent).toContain('run commands on this computer as you')
  expect(d.querySelector('[data-trust-source]')!.textContent).toBe('git@example.org:me/notes.git')
  expect(d.querySelector('[data-trust-ref]')!.textContent).toBe('the default branch')
  d.querySelector<HTMLElement>('[data-trust-cancel]')!.click()
  await flushPromises()
  expect(dialog()).toBeNull()
  expect(hn.sent).toEqual([])

  await row(w, 'notes').get('[data-catalog-install]').trigger('click')
  await flushPromises()
  d = dialog()!
  expect(d.querySelector('[data-trust-confirm]')!.textContent).toBe('Install notes')
  d.querySelector<HTMLElement>('[data-trust-confirm]')!.click()
  await flushPromises()
  expect(hn.sent.map((s) => (s.body as { acknowledge?: string }).acknowledge)).toEqual(['community'])
  expect(installed.map((i) => i.name)).toEqual(['notes'])
})

test('a recommended plugin asks with its own warning; when omo says it is community now, the page asks again with that warning', async () => {
  const { w, hn } = await open([catalog()])
  hn.respond.write = (s) =>
    (s.body as { acknowledge?: string }).acknowledge === 'community' ? ok(info('ticket-log')) : refuse('validation', 'not reviewed', { field: 'acknowledge', tag: 'community' })
  await row(w, 'ticket-log').get('[data-catalog-install]').trigger('click')
  await flushPromises()
  let d = dialog()!
  expect(d.getAttribute('data-trust-dialog')).toBe('recommended')
  expect(d.querySelector('[data-trust-warning]')!.getAttribute('data-tone')).toBe('warn')
  expect(d.querySelector('[data-trust-warning]')!.textContent).toContain('someone else')
  expect(d.querySelector('[data-trust-ref]')!.textContent).toBe('v1.0.0')
  d.querySelector<HTMLElement>('[data-trust-confirm]')!.click()
  await flushPromises()
  d = dialog()!
  expect(d.getAttribute('data-trust-dialog')).toBe('community')
  d.querySelector<HTMLElement>('[data-trust-confirm]')!.click()
  await flushPromises()
  expect(hn.sent.map((s) => (s.body as { acknowledge?: string }).acknowledge)).toEqual(['recommended', 'community'])
  expect(w.find('[data-catalog-install-error]').exists()).toBe(false)
})

test('a list omo could not read says why, keeps the page working and reads again on Retry', async () => {
  const { w, reads } = await open([catalog({ available: false, message: 'omo could not read its plugin list: the repository has no plugins/index.yaml', plugins: [] }), catalog()])
  const error = w.get('[data-catalog-error]')
  expect(error.text()).toContain('the repository has no plugins/index.yaml')
  expect(error.text()).toContain('Install plugin… installs from a source you type')
  expect(w.find('[data-catalog-plugin]').exists()).toBe(false)
  await w.get('[data-catalog-retry]').trigger('click')
  await flushPromises()
  expect(reads).toEqual([undefined, { refresh: 'true' }])
  expect(w.find('[data-catalog-error]').exists()).toBe(false)
  expect(w.findAll('[data-catalog-plugin]')).toHaveLength(3)
})

test('the words and rules of tags', () => {
  expect(acknowledgementOf('official')).toBeUndefined()
  expect(acknowledgementOf('recommended')).toBe('recommended')
  expect(confirmationOf(new ApiError(422, 'validation', 'x', { field: 'acknowledge', tag: 'recommended' }))).toBe('recommended')
  expect(confirmationOf(new ApiError(422, 'validation', 'x', { field: 'acknowledge', tag: 'gold' }))).toBe('community')
  expect(confirmationOf(new ApiError(422, 'validation', 'x', { field: 'source' }))).toBeNull()
  expect(confirmationOf(new Error('x'))).toBeNull()
  expect(installedFrom(entry(), [{ source: REPO, subpath: 'plugins/pushover', ref: 'release' }])).toBe(true)
  expect(installedFrom(entry(), [{ source: REPO, subpath: 'plugins/pushover', ref: 'main' }])).toBe(false)
  expect(whereFrom(entry())).toBe(`${REPO}, branch or tag release, folder plugins/pushover`)
})

const LIST_URL = 'https://lists.example/omo/index.yaml'
const BROKEN_URL = 'http://broken.example/list.yaml'
const fromList = (o: Partial<CatalogPlugin> = {}): CatalogPlugin =>
  entry({ name: 'claimer', tag: 'official', description: 'Says <i>official</i>', source: 'https://example.org/claimer.git', ref: '', subpath: '', list: LIST_URL, origin: 'lists.example', ...o })
const withLists = (o: Partial<Catalog> = {}): Catalog =>
  catalog({
    plugins: [...LIST, fromList()],
    lists: [
      { url: BROKEN_URL, origin: 'broken.example', available: false, message: 'omo could not read this plugin list: the address answered 404 Not Found', notes: [], fetched_at: null },
      { url: LIST_URL, origin: 'lists.example', available: true, message: '', notes: ['"pushover" is not shown: omo\'s own list has a plugin of this name'], fetched_at: null },
    ],
    ...o,
  })

test('a plugin of an additional list is shown as community under its list, whatever the list says, and asks before it installs', async () => {
  const { w, hn, installed } = await open([withLists()])
  // omo's own list still renders, next to the broken list's error.
  expect(w.findAll('.catalog > [data-catalog-plugin]').map((r) => r.attributes('data-catalog-plugin'))).toEqual(['pushover', 'ticket-log', 'notes', 'claimer'])
  const broken = w.get(`[data-plugin-list="${BROKEN_URL}"]`)
  expect(broken.get('[data-plugin-list-error]').text()).toContain('answered 404 Not Found')
  expect(broken.find('[data-catalog-plugin]').exists()).toBe(false)
  const good = w.get(`[data-plugin-list="${LIST_URL}"]`)
  expect(good.find('[data-plugin-list-error]').exists()).toBe(false)
  expect(good.get('[data-plugin-list-notes]').text()).toContain('"pushover" is not shown')
  const r = good.get('[data-catalog-plugin="claimer"]')
  expect(r.attributes('data-tag')).toBe('community')
  expect(r.get('[data-catalog-tag]').text()).toBe('community')
  expect(r.get('[data-catalog-origin]').text()).toBe('Listed by lists.example, not by omo')
  expect(r.get('[data-catalog-description]').text()).toBe('Says <i>official</i>')
  expect(r.find('i').exists()).toBe(false)
  expect(row(w, 'pushover').find('[data-catalog-origin]').exists()).toBe(false)

  hn.respond.write = () => ok(info('claimer'))
  await r.get('[data-catalog-install]').trigger('click')
  await flushPromises()
  const d = dialog()!
  expect(d.getAttribute('data-trust-dialog')).toBe('community')
  d.querySelector<HTMLElement>('[data-trust-confirm]')!.click()
  await flushPromises()
  expect(hn.sent.map((s) => (s.body as { acknowledge?: string }).acknowledge)).toEqual(['community'])
  expect(installed.map((i) => i.name)).toEqual(['claimer'])
})

test("an additional list's Retry reads every list again", async () => {
  const { w, reads } = await open([withLists()])
  await w.get('[data-plugin-list-retry]').trigger('click')
  await flushPromises()
  expect(reads).toEqual([undefined, { refresh: 'true' }])
})

test('omo itself unreadable: the additional lists still show', async () => {
  const { w } = await open([withLists({ available: false, message: 'omo could not read its plugin list: gone', plugins: [fromList()] })])
  expect(w.find('[data-catalog-error]').exists()).toBe(true)
  expect(w.get(`[data-plugin-list="${LIST_URL}"] [data-catalog-plugin="claimer"]`).attributes('data-tag')).toBe('community')
})

test('trustTagOf: only omo\'s own list grants a tag', () => {
  expect(trustTagOf(entry())).toBe('official')
  expect(trustTagOf(entry({ tag: 'recommended' }))).toBe('recommended')
  for (const tag of ['official', 'recommended', 'community'] as const) expect(trustTagOf(fromList({ tag }))).toBe('community')
  expect(listWarning()).toContain('run commands on this computer as you')
  expect(listWarning()).toContain('community')
})
