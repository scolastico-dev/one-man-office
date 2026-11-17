import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import type { MCPClientState } from '../../../api/types'
import { harness, mountWith, type Harness } from '../../create/dialogTesting'
import McpClients from './McpClients.vue'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
})

const client = (o: Partial<MCPClientState> = {}): MCPClientState => ({
  target: { id: 'claude', cli: 'claude', account: '', config_path: '/home/u/.claude.json' },
  state: 'absent',
  entry: null,
  entries: [],
  error: '',
  ...o,
})

async function open(clients: MCPClientState[]): Promise<{ w: VueWrapper; h: Harness }> {
  const h = await harness((omo) => {
    omo.mcp.clients = clients
  })
  h.respond.write = (s) => h.omo.mcp.write(s)
  const w = await mountWith(h, McpClients, {})
  mounted = w
  await flushPromises()
  return { w, h }
}

const rowOf = (w: VueWrapper, id: string) => w.get(`[data-mcp-client="${id}"]`)

test('each configuration shows whether omo is registered', async () => {
  const { w } = await open([
    client(),
    client({ target: { id: 'codex-1a2b3c4d', cli: 'codex', account: '/home/u/.codex-work', config_path: '/home/u/.codex-work/config.toml' }, state: 'installed' }),
    client({ target: { id: 'agy', cli: 'agy', account: '', config_path: '/home/u/.gemini/config/mcp_config.json' }, error: 'line 3: not JSON' }),
  ])
  expect(rowOf(w, 'claude').text()).toContain('Claude Code, your own configuration')
  expect(rowOf(w, 'claude').get('[data-mcp-client-state]').text()).toBe('omo not registered')
  expect(rowOf(w, 'codex-1a2b3c4d').text()).toContain('Codex, account folder /home/u/.codex-work')
  expect(rowOf(w, 'codex-1a2b3c4d').get('[data-mcp-client-state]').text()).toBe('omo registered')
  expect(rowOf(w, 'agy').text()).toContain('line 3: not JSON')
  expect(rowOf(w, 'agy').findAll('[data-mcp-client-action]')).toHaveLength(0)
})

test('Apply shows the plan first and runs it with the plan hash only when confirmed', async () => {
  const { w, h } = await open([client()])
  await rowOf(w, 'claude').get('[data-mcp-client-action="install"]').trigger('click')
  await flushPromises()
  expect(h.sent).toEqual([{ method: 'POST', path: '/api/mcp/clients/claude/plan', body: { action: 'install' } }])
  const dialog = w.get('[data-mcp-plan]')
  expect(dialog.text()).toContain('File: /home/u/.claude.json')
  expect(dialog.get('[data-mcp-plan-commands]').text()).toBe('claude mcp add omo -- /usr/local/bin/omo mcp stdio')
  expect(dialog.get('[data-mcp-plan-after]').text()).toContain('"mcp"')
  await dialog.get('[data-mcp-plan-apply]').trigger('click')
  await flushPromises()
  expect(h.sent[1]).toEqual({ method: 'POST', path: '/api/mcp/clients/claude/apply', body: { plan: 'claude-install-absent' } })
  expect(rowOf(w, 'claude').get('[data-mcp-client-state]').text()).toBe('omo registered')
  expect(rowOf(w, 'claude').get('[data-mcp-client-done]').text()).toContain('A copy of the file before is at /home/sample/.local/omo/backups/')
})

test('replacing an entry named omo that is not omo’s needs a second click', async () => {
  const { w, h } = await open([client({ state: 'foreign', entry: { type: 'http', url: 'https://other.invalid/mcp', headers: { Authorization: 'Bearer ***' } } })])
  expect(rowOf(w, 'claude').text()).toContain('not omo')
  await rowOf(w, 'claude').get('[data-mcp-client-action="install"]').trigger('click')
  await flushPromises()
  const dialog = w.get('[data-mcp-plan]')
  expect(dialog.get('[data-mcp-plan-foreign]').text()).toContain('not omo')
  expect(dialog.text()).toContain('Bearer ***')
  await dialog.get('[data-mcp-plan-apply]').trigger('click')
  await flushPromises()
  expect(h.sent.filter((s) => s.path.endsWith('/apply'))).toHaveLength(0)
  expect(dialog.get('[data-mcp-plan-apply]').text()).toBe('Replace the other entry')
  await dialog.get('[data-mcp-plan-apply]').trigger('click')
  await flushPromises()
  expect(h.sent.filter((s) => s.path.endsWith('/apply'))).toHaveLength(1)
})

test('a changed file refuses the apply and the dialog says so', async () => {
  const { w, h } = await open([client()])
  await rowOf(w, 'claude').get('[data-mcp-client-action="install"]').trigger('click')
  await flushPromises()
  h.omo.mcp.clients[0] = client({ state: 'outdated' })
  await w.get('[data-mcp-plan-apply]').trigger('click')
  await flushPromises()
  expect(w.get('[data-mcp-apply-error]').text()).toContain('the file changed; review again')
})

test('a plan with nothing to do says so and offers no apply', async () => {
  const { w } = await open([client({ state: 'outdated' })])
  await rowOf(w, 'claude').get('[data-mcp-client-action="remove"]').trigger('click')
  await flushPromises()
  expect(w.get('[data-mcp-plan-commands]').text()).toBe('claude mcp remove omo')
})
