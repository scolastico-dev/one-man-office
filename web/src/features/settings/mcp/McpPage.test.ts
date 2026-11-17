import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import { harness, mountWith, type Harness } from '../../create/dialogTesting'
import { FakeMcp, type FakeOmo } from '../../../testing/fakeOmo'
import McpPage from './McpPage.vue'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
})

async function open(setup: (omo: FakeOmo) => void): Promise<{ w: VueWrapper; h: Harness }> {
  const h = await harness(setup)
  h.respond.write = (s) => h.omo.mcp.write(s)
  const w = await mountWith(h, McpPage, {})
  mounted = w
  await flushPromises()
  await flushPromises()
  return { w, h }
}

const row = (w: VueWrapper, name: string) => w.get(`[data-mcp-server="${name}"]`)

test('the list shows each server with its live state, and mcp.changed updates it without a read', async () => {
  const { w, h } = await open((omo) => {
    omo.mcp.servers = [FakeMcp.server(), FakeMcp.server({ name: 'docs', transport: 'http', url: 'https://docs.example.invalid/mcp', command: '', args: [], global: true })]
    omo.mcp.states = [FakeMcp.state(), FakeMcp.state({ name: 'docs', state: 'needs_login', tools: [] })]
  })
  expect(row(w, 'files').get('[data-mcp-server-state]').text()).toBe('running')
  expect(row(w, 'files').get('[data-mcp-server-tools]').text()).toBe('1 tool')
  expect(row(w, 'docs').get('[data-mcp-server-state]').text()).toBe('needs login')
  expect(row(w, 'docs').find('[data-mcp-server-login]').exists()).toBe(true)
  expect(row(w, 'docs').find('[data-mcp-server-global]').exists()).toBe(true)
  expect(row(w, 'docs').get('[data-mcp-server-what]').text()).toBe('http · docs.example.invalid · for every role · also without an omo session')

  const reads = h.omo.reads.filter((p) => p === '/api/mcp/servers').length
  h.omo.emit('mcp.changed', FakeMcp.state({ state: 'failed', last_error: 'exit status 1' }))
  await flushPromises()
  expect(row(w, 'files').get('[data-mcp-server-state]').text()).toBe('failed')
  expect(row(w, 'files').get('[data-mcp-server-failure]').text()).toBe('exit status 1')
  expect(h.omo.reads.filter((p) => p === '/api/mcp/servers').length).toBe(reads)
  // Between two attempts the server is not running, and the list still says why.
  h.omo.emit('mcp.changed', FakeMcp.state({ state: 'stopped', instances: 0, last_error: 'exit status 1' }))
  await flushPromises()
  expect(row(w, 'files').get('[data-mcp-server-state]').text()).toBe('not running')
  expect(row(w, 'files').get('[data-mcp-server-failure]').text()).toBe('exit status 1')
  expect(w.find('[data-mcp-off]').exists()).toBe(false)
})

test('with MCP switched off the page says so and the editor offers no relay actions', async () => {
  const { w } = await open((omo) => {
    omo.settings.mcp.enabled = false
    omo.mcp.servers = [FakeMcp.server()]
  })
  expect(w.get('[data-mcp-off]').text()).toContain('MCP is switched off')
  expect(row(w, 'files').find('[data-mcp-server-state]').exists()).toBe(false)
  await row(w, 'files').get('[data-mcp-server-toggle]').trigger('click')
  await flushPromises()
  expect(w.find('[data-mcp-server-editor="files"]').exists()).toBe(true)
  expect(w.find('[data-mcp-test]').exists()).toBe(false)
  expect(w.find('[data-mcp-restart]').exists()).toBe(false)
})

test('the page has the four parts and the general switches', async () => {
  const { w } = await open(() => {})
  expect(w.find('[data-no-mcp-servers]').exists()).toBe(true)
  const legends = w.findAll('legend, h2').map((l) => l.text())
  for (const l of ['Servers', 'General switches', 'Agent programs', 'Take over', 'Access']) expect(legends.some((x) => x.includes(l)), l).toBe(true)
  expect(w.get('[data-mcp-enabled]').attributes('aria-checked')).toBe('true')
  expect(w.text()).not.toMatch(/\bwait\b/)
})

test('a server taken over appears in the list', async () => {
  const { w, h } = await open((omo) => {
    omo.mcp.takeover = {
      candidates: [{ key: 'k1', name: 'github', server: FakeMcp.server({ name: 'github' }), sources: [{ target: 'claude', entry_name: 'github' }], notes: [], needs_login: false, conflict: '' }],
      stays: [],
    }
  })
  await w.get('[data-mcp-takeover-apply]').trigger('click')
  await flushPromises()
  await flushPromises()
  expect(h.omo.mcp.servers.map((s) => s.name)).toEqual(['github'])
  expect(w.find('[data-mcp-server="github"]').exists()).toBe(true)
})
