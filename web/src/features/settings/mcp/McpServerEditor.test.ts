import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test, vi } from 'vitest'
import type { MCPServer } from '../../../api/types'
import { harness, mountWith, refuse, type Harness } from '../../create/dialogTesting'
import { FakeMcp } from '../../../testing/fakeOmo'
import McpServerEditor from './McpServerEditor.vue'
import { blankServer } from './mcpForms'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
  vi.restoreAllMocks()
})

async function open(servers: MCPServer[], props: Record<string, unknown> = {}): Promise<{ w: VueWrapper; h: Harness }> {
  const h = await harness((omo) => {
    omo.mcp.servers = servers
  })
  h.respond.write = (s) => h.omo.mcp.write(s)
  const w = await mountWith(h, McpServerEditor, { name: 'files', ...props })
  mounted = w
  await flushPromises()
  return { w, h }
}

const bar = (w: VueWrapper) => w.get('[role="region"]')
const save = async (w: VueWrapper) => {
  await bar(w).findAll('button').find((b) => b.text() === 'Save')!.trigger('click')
  await flushPromises()
}
const field = (w: VueWrapper, label: string) => w.findAll('.omo-field').find((f) => f.find('label').exists() && f.get('label').text() === label)!

test('a new server is created with If-Match "0", only for omo sessions, with its stdio fields', async () => {
  const { w, h } = await open([], { initial: blankServer('files') })
  expect(w.text()).toContain('(not saved yet)')
  await field(w, 'Command').get('input').setValue('npx')
  await save(w)
  expect(h.sent).toHaveLength(1)
  expect(h.sent[0]).toMatchObject({ method: 'PUT', path: '/api/mcp/servers/files', ifMatch: '0' })
  const body = h.sent[0]!.body as MCPServer
  expect(body).toMatchObject({ name: 'files', transport: 'stdio', command: 'npx', global: false, instances: 'per_session', url: '', auth: '' })
  expect(w.text()).toContain('Saved')
})

test('the global switch is saved with the stored revision', async () => {
  const { w, h } = await open([FakeMcp.server()])
  const toggle = w.get('[data-mcp-global]')
  expect(toggle.attributes('aria-checked')).toBe('false')
  await toggle.trigger('click')
  await save(w)
  expect(h.sent[0]).toMatchObject({ method: 'PUT', ifMatch: 'rev1' })
  expect((h.sent[0]!.body as MCPServer).global).toBe(true)
  expect(w.text()).toContain('Also available without an omo session')
})

test('a refusal naming a field shows under that field', async () => {
  const { w, h } = await open([FakeMcp.server()])
  h.respond.write = () => refuse('validation', 'command must name the program of a stdio server', { field: 'command' })
  await field(w, 'Command').get('input').setValue('')
  await save(w)
  expect(field(w, 'Command').get('.omo-field__error').text()).toBe('command must name the program of a stdio server')
})

test('choosing an address with OAuth shows its client fields and hides the program fields', async () => {
  const { w } = await open([FakeMcp.server()])
  await w.get('[data-mcp-transport] select, select[data-mcp-transport]').setValue('http')
  await flushPromises()
  expect(w.find('[data-mcp-command]').exists()).toBe(false)
  await w.get('select[data-mcp-auth]').setValue('oauth')
  await flushPromises()
  expect(field(w, 'Client id')).toBeTruthy()
  expect(field(w, 'Client secret')).toBeTruthy()
})

test('Test sends the draft unsaved and lists the tools', async () => {
  const { w, h } = await open([FakeMcp.server()])
  await w.get('[data-mcp-test]').trigger('click')
  await flushPromises()
  expect(h.sent[0]).toMatchObject({ method: 'POST', path: '/api/mcp/servers/test' })
  expect(w.get('[data-mcp-test-result]').text()).toContain('2 tools in 42 ms: files__read_file, files__write_file')
})

test('with MCP switched off the state, Restart, Log and Test are not offered', async () => {
  const { w } = await open([FakeMcp.server()], { off: true })
  expect(w.find('[data-mcp-server-status]').exists()).toBe(false)
  expect(w.find('[data-mcp-test]').exists()).toBe(false)
  expect(w.find('[data-mcp-server-delete]').exists()).toBe(true)
})

test('a server that needs login says so and Connect opens the sign-in in a new tab', async () => {
  const open1 = vi.spyOn(window, 'open').mockReturnValue(null)
  const srv = FakeMcp.server({ transport: 'http', url: 'https://mcp.example.invalid/mcp', auth: 'oauth', command: '', args: [] })
  const { w, h } = await open([srv], { state: FakeMcp.state({ state: 'needs_login', auth: { state: 'needs_login', expires_at: null, scopes: [] } }) })
  expect(w.get('[data-mcp-needs-login]').text()).toContain('Needs login')
  await w.get('[data-mcp-connect]').trigger('click')
  await flushPromises()
  expect(h.sent[0]).toMatchObject({ method: 'POST', path: '/api/mcp/servers/files/connect' })
  expect(open1).toHaveBeenCalledWith(h.omo.mcp.authorizeUrl, '_blank', 'noopener')
  // A blocked tab leaves a link to the sign-in.
  expect(w.get('[data-mcp-sign-in-link]').attributes('href')).toBe(h.omo.mcp.authorizeUrl)
})

test('the server state, its tools and what it leaves out are shown', async () => {
  const { w } = await open([FakeMcp.server()], {
    state: FakeMcp.state({ tools: ['files__read'], omitted: [{ tool: 'x'.repeat(5), reason: 'name too long' }], offers_prompts: true }),
  })
  const st = w.get('[data-mcp-server-status]').text()
  expect(st).toContain('State: running')
  expect(st).toContain('Tools: files__read')
  expect(st).toContain('Left out: xxxxx (name too long)')
  expect(st).toContain('also offers prompts')
})

test('delete asks first and sends the revision', async () => {
  const { w, h } = await open([FakeMcp.server()])
  const asked: string[] = []
  window.confirm = (text?: string) => (asked.push(text ?? ''), true)
  await w.get('[data-mcp-server-delete]').trigger('click')
  await flushPromises()
  expect(asked[0]).toContain('Delete the MCP server files?')
  expect(h.sent[0]).toMatchObject({ method: 'DELETE', path: '/api/mcp/servers/files', ifMatch: 'rev1' })
  expect(w.findComponent(McpServerEditor).emitted('deleted')).toHaveLength(1)
})
