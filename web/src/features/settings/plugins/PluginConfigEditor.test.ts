import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import type { PluginInfo, PluginSettings, PluginsResponse } from '../../../api/types'
import { harness, mountWith, ok, refuse, type Harness } from '../../create/dialogTesting'
import PluginConfigEditor from './PluginConfigEditor.vue'
import { CONFIG_PARSE_MESSAGE } from './pluginForms'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
})

const settings = (o: Partial<PluginSettings> = {}): PluginSettings => ({
  name: 'log',
  source: '/home/me/log',
  subpath: '',
  ref: '',
  enabled: true,
  config: { mark: 'one', lines: 5 },
  revision: 'rev1',
  ...o,
})
const info = (o: Partial<PluginInfo> = {}): PluginInfo => ({ name: 'log', version: '1.0.0', description: '', state: 'ready', message: '', commit: 'abc', last_event: '', last_run_at: null, hooks: [], buttons: [], frontend: {}, config_fields: [], content_id: '', ...o })

async function open(s: PluginSettings[]): Promise<{ w: VueWrapper; h: Harness; read: { value: PluginsResponse } }> {
  const h = await harness()
  const read = { value: { plugins: [], settings: s, errors: [] } as PluginsResponse }
  const client = h.session.client
  const baseGet = client.get.bind(client)
  client.get = ((path: string, q?: never) => (path === '/api/plugins' ? Promise.resolve(ok(read.value)) : baseGet(path, q))) as typeof client.get
  h.respond.write = ({ body }) => {
    read.value = { ...read.value, settings: [{ ...(body as PluginSettings), revision: 'rev2' }] }
    return ok(info())
  }
  const w = await mountWith(h, PluginConfigEditor, { name: 'log' })
  mounted = w
  await flushPromises()
  return { w, h, read }
}

const field = (w: VueWrapper) => w.get('[data-plugin-config-text]')
const bar = (w: VueWrapper) => w.get('[role="region"]')
const save = async (w: VueWrapper) => {
  await bar(w).findAll('button').find((b) => b.text() === 'Save')!.trigger('click')
  await flushPromises()
}

test('shows the configuration as indented JSON', async () => {
  const { w } = await open([settings()])
  expect((field(w).element as HTMLTextAreaElement).value).toBe('{\n  "mark": "one",\n  "lines": 5\n}')
  expect(bar(w).attributes('aria-label')).toBe('Save changes to the configuration of log')
})

test('save sends If-Match and the stored settings with the edited configuration', async () => {
  const { w, h } = await open([settings()])
  await field(w).setValue('{"mark": "two", "lines": 5}')
  await save(w)
  expect(h.sent).toEqual([
    {
      method: 'PUT',
      path: '/api/plugins/log',
      body: { name: 'log', source: '/home/me/log', subpath: '', ref: '', enabled: true, config: { mark: 'two', lines: 5 } },
      ifMatch: 'rev1',
    },
  ])
  expect(bar(w).text()).toContain('Saved')
})

test('invalid JSON blocks Save with the parse message; nothing is sent', async () => {
  const { w, h } = await open([settings()])
  await field(w).setValue('{"mark": ')
  expect(w.text()).toContain(CONFIG_PARSE_MESSAGE)
  expect(field(w).attributes('aria-invalid')).toBe('true')
  await save(w)
  expect(h.sent).toEqual([])
  expect(bar(w).text()).toContain('Fix the fields marked above before saving.')
  await field(w).setValue('["a list"]')
  expect(w.text()).toContain(CONFIG_PARSE_MESSAGE)
  await field(w).setValue('{"mark": "three"}')
  expect(w.text()).not.toContain(CONFIG_PARSE_MESSAGE)
  await save(w)
  expect((h.sent[0]?.body as PluginSettings).config).toEqual({ mark: 'three' })
})

test("the server's refusal of config is shown under the field, verbatim and as text", async () => {
  const { w, h } = await open([settings()])
  h.respond.write = () => refuse('validation', 'config must be a JSON object: <b>keys</b> are text', { field: 'config' })
  await field(w).setValue('{"when": 1}')
  await save(w)
  const box = field(w).element.closest('.omo-field')!
  expect(box.textContent).toContain('config must be a JSON object: <b>keys</b> are text')
  expect(box.querySelector('b')).toBeNull()
  expect(field(w).attributes('aria-invalid')).toBe('true')
})

test('discard brings back the stored configuration, also after text that did not parse', async () => {
  const { w } = await open([settings()])
  await field(w).setValue('{ nope')
  await bar(w).findAll('button').find((b) => b.text() === 'Discard')!.trigger('click')
  await flushPromises()
  expect((field(w).element as HTMLTextAreaElement).value).toBe('{\n  "mark": "one",\n  "lines": 5\n}')
  expect(w.text()).not.toContain(CONFIG_PARSE_MESSAGE)
})
