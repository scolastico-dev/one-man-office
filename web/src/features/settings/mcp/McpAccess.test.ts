import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import type { MCPToken } from '../../../api/types'
import { announcements } from '../../../components/ui/useAnnouncer'
import { activeToasts } from '../../../components/ui/useToast'
import { harness, mountWith, type Harness } from '../../create/dialogTesting'
import McpAccess from './McpAccess.vue'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
})

const grant: MCPToken = {
  id: 'ab12',
  kind: 'oauth',
  name: 'Claude Code',
  client_id: 'omo_dcr_xyz',
  redirect_host: 'localhost:33418',
  resource: 'http://localhost:8090/mcp',
  scope: 'mcp',
  created_at: '2026-11-16T10:00:00Z',
  expires_at: null,
  refresh_expires_at: '2026-12-16T10:00:00Z',
  last_used_at: '2026-11-17T09:00:00Z',
}

async function open(setup: Parameters<typeof harness>[0] = () => {}): Promise<{ w: VueWrapper; h: Harness }> {
  const h = await harness(setup)
  h.respond.write = (s) => h.omo.mcp.write(s)
  const w = await mountWith(h, McpAccess, {})
  mounted = w
  await flushPromises()
  return { w, h }
}

test('the scope says that tokens and grants get no board actions; the commands name the endpoint of this page', async () => {
  const { w } = await open()
  expect(w.get('[data-mcp-access-scope]').text()).toContain('no board or ticket actions')
  expect(w.get('[data-mcp-command="codex-token"]').text()).toContain(`codex mcp add omo --url ${window.location.origin}/mcp --bearer-token-env-var OMO_MCP_TOKEN`)
  expect(w.text()).not.toMatch(/\bwait\b/)
})

test('a new token’s secret is shown once in a dialog, never in a toast or an announcement, and is gone after Done', async () => {
  const { w, h } = await open()
  await w.get('[data-mcp-token-name] input, input[data-mcp-token-name]').setValue('laptop')
  await w.get('[data-mcp-token-create]').trigger('submit')
  await flushPromises()
  expect(h.sent[0]).toEqual({ method: 'POST', path: '/api/mcp/tokens', body: { name: 'laptop' } })
  const secret = h.omo.mcp.nextSecret
  expect(w.get('[data-mcp-token-secret]').text()).toContain(secret)
  expect(JSON.stringify(activeToasts)).not.toContain(secret)
  expect(announcements.polite.value + announcements.assertive.value).not.toContain(secret)
  expect(w.get('[data-mcp-token="laptop"]').text()).toContain('never used')

  const done = w.get('[data-mcp-token-secret]').findAll('button').find((b) => b.text() === 'Done')!
  await done.trigger('click')
  await flushPromises()
  expect(document.body.textContent).not.toContain(secret)
})

test('a token with an end is created with it', async () => {
  const { w, h } = await open()
  await w.get('[data-mcp-token-name] input, input[data-mcp-token-name]').setValue('ci')
  await w.get('select').setValue('30')
  await w.get('[data-mcp-token-create]').trigger('submit')
  await flushPromises()
  expect((h.sent[0]?.body as { expires_at?: string }).expires_at).toMatch(/^\d{4}-\d\d-\d\dT/)
})

test('an empty name is refused in place without asking omo', async () => {
  const { w, h } = await open()
  await w.get('[data-mcp-token-create]').trigger('submit')
  await flushPromises()
  expect(h.sent).toEqual([])
  expect(w.text()).toContain('Enter a name')
})

test('grants are listed apart from tokens and can be revoked after asking', async () => {
  const { w, h } = await open((omo) => {
    omo.mcp.tokens = [grant]
  })
  expect(w.find('[data-no-mcp-tokens]').exists()).toBe(true)
  const row = w.get('[data-mcp-grant="Claude Code"]')
  expect(row.text()).toContain('answers went to localhost:33418')
  const asked: string[] = []
  window.confirm = (text?: string) => (asked.push(text ?? ''), true)
  await row.get('[data-mcp-grant-revoke]').trigger('click')
  await flushPromises()
  expect(asked[0]).toContain('Revoke the access of Claude Code?')
  expect(h.sent[0]).toEqual({ method: 'DELETE', path: '/api/mcp/tokens/ab12', body: undefined })
  expect(w.find('[data-mcp-grant="Claude Code"]').exists()).toBe(false)
  expect(w.find('[data-no-mcp-grants]').exists()).toBe(true)
})

test('an omo without the token store says so instead of the lists', async () => {
  const { w } = await open((omo) => {
    omo.mcp.tokensAvailable = false
  })
  expect(w.get('[data-mcp-tokens-unavailable]').text()).toContain('Tokens not available')
  expect(w.find('[data-mcp-token-create]').exists()).toBe(false)
  expect(w.find('[data-mcp-tokens-error]').exists()).toBe(false)
})
