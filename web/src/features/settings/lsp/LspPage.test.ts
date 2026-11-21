import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import type { LSPServer } from '../../../api/types'
import { general } from '../../../testing/fakeOmo'
import { harness, mountWith, ok, refuse } from '../../create/dialogTesting'
import LspPage from './LspPage.vue'

let w: VueWrapper | null = null
let active: Awaited<ReturnType<typeof harness>> | null = null
afterEach(() => {
  w?.unmount()
  w = null
  active?.session.store.stop()
  active = null
})

const server = (name = 'go'): LSPServer => ({
  name, enabled: true, command: 'gopls', args: ['serve'], env: { MODE: 'literal' },
  files: [{ pattern: '*.go', language_id: 'go' }],
  initialization_options: { nested: { keep: null } }, configuration: { gopls: { staticcheck: true } },
})

async function mountPage(servers: LSPServer[] = []) {
  const stored = structuredClone(general)
  stored.lsp.servers = servers
  const h = await harness((o) => { o.settings = stored })
  active = h
  h.respond.write = (s) => {
    Object.assign(stored, structuredClone(s.body))
    return ok({ settings: stored, errors: [], revision: 'r2', home: '/h' })
  }
  w = await mountWith(h, LspPage, {})
  return { h, stored }
}
const button = (text: string) => w!.findAll('button').find((b) => b.text() === text)!
const field = (name: string) => w!.get(`[data-field="${name}"]`)
async function save() {
  await button('Save').trigger('click')
  await flushPromises()
}

// Dropping any editable field from the payload breaks this round trip.
test('adds and saves a complete registration with literal arguments, environment and JSON objects', async () => {
  const { h, stored } = await mountPage()
  await button('Add language server').trigger('click')
  await field('lsp.servers.0.name').setValue('typescript')
  await field('lsp.servers.0.enabled').trigger('click')
  await field('lsp.servers.0.command').setValue('typescript-language-server')
  await w!.get('[data-lsp-args] [data-part="add"]').trigger('click')
  await w!.get('[data-lsp-args] input').setValue('--stdio')
  await w!.get('[data-lsp-env] [data-part="add"]').trigger('click')
  await w!.get('[data-lsp-env] [data-part="name"]').setValue('TOKEN')
  await w!.get('[data-lsp-env] [data-part="value"]').setValue('$TOKEN `literal`')
  await field('lsp.servers.0.files.0.pattern').setValue('*.ts')
  await field('lsp.servers.0.files.0.language_id').setValue('typescript')
  await field('lsp.servers.0.initialization_options').setValue('{"nested":{"keep":null}}')
  await field('lsp.servers.0.configuration').setValue('{"diagnostics":true}')
  await save()
  expect(h.sent).toEqual([{ method: 'PATCH', path: '/api/settings', ifMatch: '0', body: {
    lsp: { debounce: '500ms', force_chat: [], servers: [{
      name: 'typescript', enabled: true, command: 'typescript-language-server', args: ['--stdio'], env: { TOKEN: '$TOKEN `literal`' },
      files: [{ pattern: '*.ts', language_id: 'typescript' }], initialization_options: { nested: { keep: null } }, configuration: { diagnostics: true },
    }] },
  } }])
  expect(stored.lsp.servers[0]!.configuration).toEqual({ diagnostics: true })
  expect(w!.text()).toContain('Saved')
})

test('edits existing registrations and selector order, then removes a registration', async () => {
  const { stored } = await mountPage([server(), server('other')])
  expect((field('lsp.servers.0.configuration').element as HTMLTextAreaElement).value).toContain('staticcheck')
  await field('lsp.servers.0.name').setValue('golang')
  await button('Add file selector').trigger('click')
  await field('lsp.servers.0.files.1.pattern').setValue('go.mod')
  await field('lsp.servers.0.files.1.language_id').setValue('go.mod')
  await w!.get('[data-selector="1"] [data-move-up]').trigger('click')
  await w!.get('[data-lsp-server="1"] [data-remove-server]').trigger('click')
  await save()
  expect(stored.lsp.servers).toHaveLength(1)
  expect(stored.lsp.servers[0]!.name).toBe('golang')
  expect(stored.lsp.servers[0]!.files).toEqual([{ pattern: 'go.mod', language_id: 'go.mod' }, { pattern: '*.go', language_id: 'go' }])
  await w!.get('[data-remove-selector]').trigger('click')
  await save()
  expect(stored.lsp.servers[0]!.files).toEqual([{ pattern: '*.go', language_id: 'go' }])
  await w!.get('[data-remove-server]').trigger('click')
  await save()
  expect(stored.lsp.servers).toEqual([])
})

test('persists debounce and explicit chat overrides for every supported agent program', async () => {
  const { stored } = await mountPage()
  await field('lsp.debounce').setValue('0s')
  for (const cli of ['claude', 'codex', 'agy', 'gemini', 'custom']) await w!.get(`[data-force-chat="${cli}"]`).trigger('click')
  await save()
  expect(stored.lsp).toEqual({ servers: [], debounce: '0s', force_chat: ['claude', 'codex', 'agy', 'gemini', 'custom'] })
  await w!.get('[data-force-chat="codex"]').trigger('click')
  await save()
  expect(stored.lsp.force_chat).toEqual(['claude', 'agy', 'gemini', 'custom'])
  expect(w!.text()).toContain('even when the agent is listening')
  expect(w!.text()).toContain('managed worktrees')
})

test.each([
  ['lsp.debounce', 'bad debounce'], ['lsp.force_chat.0', 'bad program'],
  ['lsp.servers.0.name', 'bad name'], ['lsp.servers.0.command', 'bad command'],
  ['lsp.servers.0.args.0', 'bad argument'], ['lsp.servers.0.env.MODE', 'bad environment'],
  ['lsp.servers.0.files.0.pattern', 'bad pattern'], ['lsp.servers.0.files.0.language_id', 'bad language'],
  ['lsp.servers.0.initialization_options', 'bad options'], ['lsp.servers.0.configuration', 'bad configuration'],
])('places the server refusal for %s beside its field', async (key, message) => {
  const { h } = await mountPage([server()])
  await field('lsp.debounce').setValue('1s')
  h.respond.write = () => refuse('invalid_settings', message, { field: key })
  await save()
  const invalid = w!.get('[aria-invalid="true"]')
  const ids = invalid.attributes('aria-describedby')!.split(' ')
  expect(ids.some((id) => w!.get(`[id="${id}"]`).text().includes(message))).toBe(true)
})

test.each(['{', '[]', 'null', '1', '{"large":1e999}'])('invalid JSON %s blocks saving and Discard restores the stored value', async (text) => {
  const { h } = await mountPage([server()])
  await field('lsp.servers.0.configuration').setValue(text)
  await save()
  expect(h.sent).toEqual([])
  expect(field('lsp.servers.0.configuration').attributes('aria-invalid')).toBe('true')
  await button('Discard').trigger('click')
  expect(JSON.parse((field('lsp.servers.0.configuration').element as HTMLTextAreaElement).value)).toEqual({ gopls: { staticcheck: true } })
  expect(field('lsp.servers.0.configuration').attributes('aria-invalid')).toBeUndefined()
  expect(button('Save').attributes('disabled')).toBeDefined()
})

test('removing an invalid JSON registration clears its save blocker and preserves the next registration', async () => {
  const { stored } = await mountPage([server(), server('other')])
  await field('lsp.servers.0.configuration').setValue('{')
  await w!.get('[data-lsp-server="0"] [data-remove-server]').trigger('click')
  await save()
  expect(stored.lsp.servers).toEqual([server('other')])
})

test('a stale LSP revision keeps the draft until the user chooses a conflict action', async () => {
  const { h, stored } = await mountPage()
  await field('lsp.debounce').setValue('1s')
  stored.lsp.debounce = '2s'
  h.respond.write = () => refuse('stale_settings', 'changed', undefined, 409)
  await save()
  expect(w!.find('[data-use-theirs]').exists()).toBe(true)
  expect((field('lsp.debounce').element as HTMLInputElement).value).toBe('1s')
  await w!.get('[data-use-theirs]').trigger('click')
  expect((field('lsp.debounce').element as HTMLInputElement).value).toBe('2s')
  expect(button('Save').attributes('disabled')).toBeDefined()
})

test('Keep mine explicitly saves the edited LSP group over a stale value', async () => {
  const { h, stored } = await mountPage([server()])
  await field('lsp.servers.0.command').setValue('new-gopls')
  stored.lsp.servers[0]!.command = 'disk-gopls'
  h.respond.write = () => refuse('stale_settings', 'changed', undefined, 409)
  await save()
  await w!.get('[data-keep-mine]').trigger('click')
  h.respond.write = (s) => {
    Object.assign(stored, structuredClone(s.body))
    return ok({ settings: stored, errors: [], revision: 'r3', home: '/h' })
  }
  await save()
  expect(stored.lsp.servers[0]!.command).toBe('new-gopls')
  expect(w!.find('[data-use-theirs]').exists()).toBe(false)
})

test('a stale change to another group is preserved while saving LSP', async () => {
  const { h, stored } = await mountPage()
  await field('lsp.debounce').setValue('1s')
  stored.limits.tasks = 9
  const write = h.respond.write
  let stale = true
  h.respond.write = (s) => {
    if (stale) {
      stale = false
      return refuse('stale_settings', 'changed', undefined, 409)
    }
    return write(s)
  }
  await save()
  expect(stored.lsp.debounce).toBe('1s')
  expect(stored.limits.tasks).toBe(9)
  expect(w!.text()).toContain('Saved')
})

test('hand edits reload the LSP form with a notice, and invalid files keep the last valid form', async () => {
  const { h, stored } = await mountPage()
  stored.lsp.debounce = '3s'
  h.omo.emit('settings.changed', { section: 'general', name: '', source: 'disk' })
  await flushPromises()
  expect((field('lsp.debounce').element as HTMLInputElement).value).toBe('3s')
  expect(w!.get('[data-changed-on-disk]').text()).toContain('Changed on disk')
  h.omo.emit('settings.invalid', { file: '/h/settings.yaml', message: 'Invalid YAML' })
  await flushPromises()
  expect(w!.text()).toContain('Invalid YAML')
  expect((field('lsp.debounce').element as HTMLInputElement).value).toBe('3s')
})

test.each(['lsp', 'lsp.servers', 'lsp.servers.0', 'lsp.servers.0.files.0', 'unknown'])('a group refusal for %s remains visible', async (key) => {
  const { h } = await mountPage([server()])
  await field('lsp.debounce').setValue('1s')
  h.respond.write = () => refuse('invalid_settings', 'Group refused', { field: key })
  await save()
  expect(w!.text()).toContain('Group refused')
})

test('invalid JSON stays with the surviving registration when an earlier one is removed', async () => {
  const { h, stored } = await mountPage([server(), server('other')])
  await field('lsp.servers.1.configuration').setValue('{')
  await w!.get('[data-lsp-server="0"] [data-remove-server]').trigger('click')
  expect((field('lsp.servers.0.configuration').element as HTMLTextAreaElement).value).toBe('{')
  await save()
  expect(h.sent).toEqual([])
  await field('lsp.servers.0.configuration').setValue('{"fixed":true}')
  await save()
  expect(stored.lsp.servers[0]!.name).toBe('other')
  expect(stored.lsp.servers[0]!.configuration).toEqual({ fixed: true })
})
