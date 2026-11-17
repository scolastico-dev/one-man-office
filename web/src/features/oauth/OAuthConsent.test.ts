import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import type { OAuthConsent as Consent } from '../../api/types'
import { harness, mountWith, ok, refuse, type Harness } from '../create/dialogTesting'
import { FakeMcp } from '../../testing/fakeOmo'
import OAuthConsent from './OAuthConsent.vue'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
})

const QUERY = 'response_type=code&client_id=omo_dcr_abc&redirect_uri=http%3A%2F%2F127.0.0.1%3A33418%2Fcb&state=st1&code_challenge=xyz&code_challenge_method=S256&resource=http%3A%2F%2Fomo.localhost%3A8090%2Fmcp&scope=mcp'

const consent = (o: Partial<Consent> = {}): Consent => ({
  client_name: 'Claude Code',
  client_id: 'omo_dcr_abc',
  client_kind: 'registered',
  redirect_host: '127.0.0.1:33418',
  localhost_redirect: true,
  resource: 'http://omo.localhost:8090/mcp',
  ...o,
})

async function open(answer: () => unknown): Promise<{ w: VueWrapper; h: Harness; went: string[]; asked: Record<string, unknown>[] }> {
  const h = await harness((omo) => {
    omo.mcp.servers = [FakeMcp.server({ name: 'docs', global: true }), FakeMcp.server({ name: 'private' })]
  })
  const asked: Record<string, unknown>[] = []
  const baseGet = h.session.client.get.bind(h.session.client)
  h.session.client.get = ((path: string, q?: Record<string, unknown>) => {
    if (path === '/api/oauth/consent') {
      asked.push(q ?? {})
      return Promise.resolve().then(() => ok(answer()))
    }
    return baseGet(path, q as never)
  }) as typeof h.session.client.get
  h.respond.write = (s) => ok({ redirect: `http://127.0.0.1:33418/cb?${(s.body as { approve: boolean }).approve ? 'code=c1' : 'error=access_denied'}&state=st1` })
  const went: string[] = []
  const w = await mountWith(h, OAuthConsent, { query: QUERY, go: (u: string) => went.push(u) })
  mounted = w
  await flushPromises()
  return { w, h, went, asked }
}

test('the page shows who asks, where the browser goes, the warnings and exactly what the grant gives', async () => {
  const { w, asked } = await open(() => consent())
  expect(asked[0]).toMatchObject({ client_id: 'omo_dcr_abc', code_challenge_method: 'S256', state: 'st1' })
  expect(w.get('[data-oauth-client]').text()).toBe('Claude Code')
  expect(w.get('[data-oauth-redirect-host]').text()).toBe('127.0.0.1:33418')
  expect(w.findAll('[data-oauth-warning]')).toHaveLength(2)
  const grant = w.get('[data-oauth-grant]').text()
  expect(grant).toContain('Also available without an omo session')
  expect(w.get('[data-oauth-servers]').text()).toBe('docs')
  expect(grant).toContain('No board or ticket actions')
  expect(grant).not.toMatch(/\bwait\b/)
})

test('a client with its own https document and an https return address gets no warning', async () => {
  const { w } = await open(() => consent({ client_kind: 'cimd', localhost_redirect: false, redirect_host: 'app.example.invalid' }))
  expect(w.findAll('[data-oauth-warning]')).toHaveLength(0)
  expect(w.get('[data-oauth-kind]').text()).toContain('https address')
})

test('Approve sends the request’s query and goes where omo says', async () => {
  const { w, h, went } = await open(() => consent())
  await w.get('[data-oauth-approve]').trigger('click')
  await flushPromises()
  expect(h.sent).toEqual([{ method: 'POST', path: '/api/oauth/consent', body: { query: QUERY, approve: true } }])
  expect(went).toEqual(['http://127.0.0.1:33418/cb?code=c1&state=st1'])
})

test('Deny sends the refusal and goes back to the program', async () => {
  const { w, h, went } = await open(() => consent())
  await w.get('[data-oauth-deny]').trigger('click')
  await flushPromises()
  expect((h.sent[0]?.body as { approve: boolean }).approve).toBe(false)
  expect(went[0]).toContain('error=access_denied')
})

test('a request omo refuses shows why and offers no buttons', async () => {
  const { w } = await open(() => refuse('bad_request', 'the redirect_uri is not registered for this client', undefined, 400))
  expect(w.get('[data-oauth-error]').text()).toContain('the redirect_uri is not registered for this client')
  expect(w.find('[data-oauth-approve]').exists()).toBe(false)
})
