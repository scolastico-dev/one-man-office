// The OAuth consent page of omo's MCP endpoint against omo's own
// authorization server: a client registers itself (RFC 7591), the browser
// opens /oauth/authorize, the page shows the request as omo checked it, and
// Approve or Deny send the browser back to the client's address with a code
// or the refusal. The client's address is never reached: the test answers
// it in the browser. With OMO_SCREENSHOTS=<folder> the axe tests also write
// mcp-consent-<theme>-<width>.png there.
import AxeBuilder from '@axe-core/playwright'
import { createHash, randomBytes } from 'node:crypto'
import { mkdirSync } from 'node:fs'
import { join } from 'node:path'
import type { Page } from '@playwright/test'
import { openBoard } from './support/board'
import { expect, test, type Omo } from './support/omo'

const SHOTS = process.env.OMO_SCREENSHOTS ?? ''
/** The client's own return address: a loopback port nobody listens on; the browser's request to it is answered by the test. */
const REDIRECT = 'http://127.0.0.1:9/cb'

const b64url = (b: Buffer) => b.toString('base64').replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')

interface Request {
  clientId: string
  verifier: string
  state: string
  resource: string
  query: string
}

/** Registers a public client named `name` and builds its authorization request for the page's address. */
async function authorizationRequest(omo: Omo, name: string): Promise<Request> {
  const res = await fetch(`${omo.base}/oauth/register`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ client_name: name, redirect_uris: [REDIRECT], token_endpoint_auth_method: 'none', grant_types: ['authorization_code', 'refresh_token'], response_types: ['code'] }),
  })
  expect(res.status).toBe(201)
  const { client_id: clientId } = (await res.json()) as { client_id: string }
  const verifier = b64url(randomBytes(32))
  const state = b64url(randomBytes(12))
  const resource = `http://omo.localhost:${omo.port}/mcp`
  const q = new URLSearchParams({
    response_type: 'code',
    client_id: clientId,
    redirect_uri: REDIRECT,
    state,
    code_challenge: b64url(createHash('sha256').update(verifier).digest()),
    code_challenge_method: 'S256',
    resource,
    scope: 'mcp',
  })
  return { clientId, verifier, state, resource, query: q.toString() }
}

/** Answers the browser's arrival at the client's address and hands its query over. */
async function catchRedirect(page: Page): Promise<() => Promise<URLSearchParams>> {
  let resolve: (q: URLSearchParams) => void = () => {}
  const arrived = new Promise<URLSearchParams>((r) => (resolve = r))
  await page.route(`${REDIRECT}**`, async (route) => {
    resolve(new URL(route.request().url()).searchParams)
    await route.fulfill({ status: 200, contentType: 'text/plain', body: 'the client got its answer' })
  })
  return () => arrived
}

async function openConsent(page: Page, omo: Omo, req: Request) {
  await openBoard(page, omo)
  await page.goto(omo.url(`/oauth/authorize?${req.query}`))
  await expect(page.locator('[data-oauth-consent] h1')).toHaveText('Allow access to omo?')
}

async function violations(page: Page) {
  await page.evaluate(() => document.fonts.ready.then(() => undefined))
  const results = await new AxeBuilder({ page }).analyze()
  return results.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target.join(' ')) }))
}

test('the page shows the client and what it gets; Approve sends back a code that buys a token', async ({ page, omo }) => {
  await omo.api('PUT', '/api/mcp/servers/docs', { name: 'docs', enabled: true, transport: 'stdio', command: 'docs-mcp', global: true })
  await omo.api('PUT', '/api/mcp/servers/private', { name: 'private', enabled: true, transport: 'stdio', command: 'private-mcp', global: false })
  const req = await authorizationRequest(omo, 'E2E Client')
  const arrived = await catchRedirect(page)
  await openConsent(page, omo, req)

  await expect(page.locator('[data-oauth-client]')).toHaveText('E2E Client')
  await expect(page.locator('[data-oauth-redirect-host]')).toHaveText('127.0.0.1:9')
  // A registered client on a loopback address: both warnings.
  await expect(page.locator('[data-oauth-warning]')).toHaveCount(2)
  const grant = page.locator('[data-oauth-grant]')
  await expect(grant).toContainText('Also available without an omo session')
  await expect(page.locator('[data-oauth-servers]')).toHaveText('docs')
  await expect(grant).toContainText('No board or ticket actions')

  await page.locator('[data-oauth-approve]').click()
  const q = await arrived()
  expect(q.get('state')).toBe(req.state)
  expect(q.get('iss')).toBe(`http://omo.localhost:${omo.port}`)
  const code = q.get('code') ?? ''
  expect(code).not.toBe('')

  // The code buys a token for the resource the page approved.
  const token = await fetch(`${omo.base}/oauth/token`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    body: new URLSearchParams({ grant_type: 'authorization_code', code, redirect_uri: REDIRECT, client_id: req.clientId, code_verifier: req.verifier, resource: req.resource }).toString(),
  })
  expect(token.status).toBe(200)
  expect(((await token.json()) as { access_token: string }).access_token).toMatch(/^omo_at_/)
})

test('Deny sends the browser back with access_denied and grants nothing', async ({ page, omo }) => {
  const req = await authorizationRequest(omo, 'Unwanted')
  const arrived = await catchRedirect(page)
  await openConsent(page, omo, req)
  await page.locator('[data-oauth-deny]').click()
  const q = await arrived()
  expect(q.get('error')).toBe('access_denied')
  expect(q.get('state')).toBe(req.state)
  expect(q.get('code')).toBeNull()
})

test('a request omo refuses shows why and offers no Approve', async ({ page, omo }) => {
  const req = await authorizationRequest(omo, 'Tampered')
  const bad = new URLSearchParams(req.query)
  bad.set('redirect_uri', 'http://127.0.0.1:9/elsewhere')
  await openConsent(page, omo, { ...req, query: bad.toString() })
  await expect(page.locator('[data-oauth-error]')).toBeVisible()
  await expect(page.locator('[data-oauth-approve]')).toHaveCount(0)
})

for (const width of [1440, 390] as const) {
  for (const colorScheme of ['dark', 'light'] as const) {
    test.describe(`axe at ${width} px, ${colorScheme} theme`, () => {
      test.use({ viewport: { width, height: width === 390 ? 844 : 900 }, colorScheme, reducedMotion: 'reduce' })

      test('the consent page has no violations and does not scroll sideways', async ({ page, omo }) => {
        const req = await authorizationRequest(omo, `A client with a long name ${'x'.repeat(60)}`)
        await openConsent(page, omo, req)
        await expect(page.locator('[data-oauth-approve]')).toBeVisible()
        expect(await violations(page)).toEqual([])
        const sideways = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth || document.body.scrollWidth > document.body.clientWidth)
        expect(sideways).toBe(false)
        if (SHOTS !== '') {
          mkdirSync(SHOTS, { recursive: true })
          await page.screenshot({ path: join(SHOTS, `mcp-consent-${colorScheme}-${width}.png`), fullPage: true })
        }
      })
    })
  }
}
