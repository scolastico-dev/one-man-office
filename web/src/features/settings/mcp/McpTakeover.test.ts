import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import type { MCPTakeover } from '../../../api/types'
import { harness, mountWith, type Harness } from '../../create/dialogTesting'
import { FakeMcp } from '../../../testing/fakeOmo'
import McpTakeover from './McpTakeover.vue'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
})

const PREVIEW: MCPTakeover = {
  candidates: [
    {
      key: 'k-gh',
      name: 'github',
      server: FakeMcp.server({ name: 'github', transport: 'http', url: 'https://api.example.invalid/mcp', command: '', args: [], auth: 'oauth' }),
      sources: [{ target: 'claude', entry_name: 'github' }],
      notes: ['the OAuth sign-in of Claude Code is not moved'],
      needs_login: true,
      conflict: '',
    },
    {
      key: 'k-fs',
      name: 'files-codex',
      server: FakeMcp.server({ name: 'files', env: { TOKEN: '***' } }),
      sources: [{ target: 'codex', entry_name: 'files' }],
      notes: [],
      needs_login: false,
      conflict: 'omo has a server named files with another configuration',
    },
  ],
  stays: [{ target: 'claude', entry_name: 'local-db', reason: 'it belongs to the project /src/x (Claude’s local scope) and stays there' }],
}

async function open(taken: string[] = []): Promise<{ w: VueWrapper; h: Harness }> {
  const h = await harness((omo) => {
    omo.mcp.takeover = structuredClone(PREVIEW)
  })
  h.respond.write = (s) => h.omo.mcp.write(s)
  const w = await mountWith(h, McpTakeover, { taken })
  mounted = w
  await flushPromises()
  return { w, h }
}

const cand = (w: VueWrapper, key: string) => w.get(`[data-mcp-candidate="${key}"]`)

test('the preview picks every candidate without a conflict, shows masked values, login needs and what stays', async () => {
  const { w } = await open()
  expect(cand(w, 'k-gh').get('[role="switch"]').attributes('aria-checked')).toBe('true')
  expect(cand(w, 'k-fs').get('[role="switch"]').attributes('aria-checked')).toBe('false')
  expect(cand(w, 'k-gh').text()).toContain('needs login after')
  expect(cand(w, 'k-gh').text()).toContain('also without session')
  expect(cand(w, 'k-fs').text()).toContain('Environment: TOKEN: ***')
  expect(cand(w, 'k-fs').get('[data-mcp-candidate-conflict]').text()).toContain('another configuration')
  expect(w.get('[data-mcp-stays]').text()).toContain('local-db')
  expect(w.get('[data-mcp-stays]').text()).toContain('stays there')
  expect(w.get('[data-mcp-takeover-apply]').text()).toBe('Take over 1 server')
})

test('the apply sends the picks under their names and shows the report', async () => {
  const { w, h } = await open()
  await cand(w, 'k-gh').get('[data-mcp-candidate-name] input, input[data-mcp-candidate-name]').setValue('gh')
  await w.get('[data-mcp-takeover-apply]').trigger('click')
  await flushPromises()
  expect(h.sent[0]).toEqual({ method: 'POST', path: '/api/mcp/takeover/apply', body: { take: [{ key: 'k-gh', name: 'gh' }], remove_from_cli: true } })
  const report = w.get('[data-mcp-takeover-report]').text()
  expect(report).toContain('gh: stored in omo; needs login: open it under Servers and Connect.')
  expect(report).toContain('Removed from Claude Code.')
  expect(report).toContain('Copies of the files before:')
  expect(h.omo.mcp.servers[0]?.global).toBe(true)
})

test('a name omo has already blocks the apply with the reason under the name', async () => {
  const { w, h } = await open(['github'])
  expect(cand(w, 'k-gh').text()).toContain('omo has a server named github already')
  expect(w.get('[data-mcp-takeover-apply]').attributes('disabled')).toBeDefined()
  expect(h.sent).toEqual([])
})

test('keeping the programs’ entries is sent as such', async () => {
  const { w, h } = await open()
  await w.get('[data-mcp-takeover-remove]').trigger('click')
  await w.get('[data-mcp-takeover-apply]').trigger('click')
  await flushPromises()
  expect((h.sent[0]?.body as { remove_from_cli: boolean }).remove_from_cli).toBe(false)
})

/** The browser tab is hidden and shown again, as after a look at another tab. */
function shownAgain(): void {
  for (const state of ['hidden', 'visible']) {
    Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => state })
    document.dispatchEvent(new Event('visibilitychange'))
  }
  delete (document as { visibilityState?: unknown }).visibilityState
}

test('reading the files again keeps the picks and names the user set for servers still there', async () => {
  const { w, h } = await open()
  const nameOf = () => cand(w, 'k-gh').get('input[data-mcp-candidate-name], [data-mcp-candidate-name] input')
  await nameOf().setValue('gh')
  await cand(w, 'k-fs').get('[role="switch"]').trigger('click', { detail: 1 })
  expect(cand(w, 'k-fs').get('[role="switch"]').attributes('aria-checked')).toBe('true')
  h.omo.mcp.takeover = structuredClone(PREVIEW)
  shownAgain()
  await flushPromises()
  expect(cand(w, 'k-fs').get('[role="switch"]').attributes('aria-checked')).toBe('true')
  expect((nameOf().element as HTMLInputElement).value).toBe('gh')
  // A server gone from the files leaves the list.
  h.omo.mcp.takeover = { ...structuredClone(PREVIEW), candidates: [structuredClone(PREVIEW.candidates[0]!)] }
  shownAgain()
  await flushPromises()
  expect(w.findAll('[data-mcp-candidate]')).toHaveLength(1)
})
