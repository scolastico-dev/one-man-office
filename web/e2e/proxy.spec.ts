import AxeBuilder from '@axe-core/playwright'
import { createServer, type IncomingHttpHeaders } from 'node:http'
import type { AddressInfo } from 'node:net'
import type { Locator, Page } from '@playwright/test'
import type { ProxyForward } from '../src/api/types'
import { expect, test } from './support/omo'
import { openBoard } from './support/board'
import { rowMismatches } from './support/rows'

async function fixture() {
  const requests: { url: string; headers: IncomingHttpHeaders }[] = []
  let base = '/'
  const server = createServer((req, res) => {
    requests.push({ url: req.url ?? '', headers: req.headers })
    if (req.url?.endsWith('asset.js')) {
      res.setHeader('Content-Type', 'text/javascript')
      res.end("document.querySelector('#asset').textContent = 'Asset loaded'")
    } else {
      res.setHeader('Content-Type', 'text/html')
      res.end(`<!doctype html><html lang="en"><title>Forward fixture</title><h1>Real upstream</h1><p id="asset">Waiting</p><script src="${base}asset.js"></script></html>`)
    }
  })
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
  return {
    port: (server.address() as AddressInfo).port,
    requests,
    setBase: (path: string) => { base = path },
    stop: () => new Promise<void>((resolve, reject) => { server.closeAllConnections(); server.close((e) => e ? reject(e) : resolve()) }),
  }
}
async function create(list: Locator, port: number, name: string) {
  await list.locator('[data-forward-add]').click()
  await list.getByLabel('Port (required)', { exact: true }).fill(String(port))
  await list.getByLabel('Name (required)', { exact: true }).fill(name)
  await list.getByRole('button', { name: 'Add forward', exact: true }).click()
}
async function layout(page: Page) {
  await page.evaluate(() => document.fonts.ready)
  expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(1)
  expect(await rowMismatches(page)).toEqual([])
}

for (const width of [1440, 390]) {
  test(`ticket and overall forwards open a real upstream in one authenticated new tab at ${width}px`, async ({ page, context, omo }, testInfo) => {
    await page.setViewportSize({ width, height: 900 })
    const upstream = await fixture()
    let stopped = false
    try {
      const n = await omo.createTicket({ title: 'Preview this service' })
      await openBoard(page, omo)
      await page.goto(omo.url(`/${n}`))
      const panel = page.locator('[data-ticket-panel]')
      await expect(panel.locator('[data-forward-list]')).toHaveCount(0)
      await panel.getByRole('tab', { name: 'Forwards', exact: true }).click()
      const list = panel.locator('[data-forward-list]')
      await expect(list.locator('[data-forward-empty]')).toContainText('No forwards')
      const response = page.waitForResponse((r) => r.url().endsWith('/api/proxy/forwards') && r.request().method() === 'POST')
      await create(list, upstream.port, 'Working preview')
      const f = await (await response).json() as ProxyForward
      upstream.setBase(f.base_path)
      await expect(list.locator('[data-forward-created]')).toContainText(f.base_path)
      await expect(list.locator(`[data-forward="${f.id}"]`)).toContainText(`http://127.0.0.1:${upstream.port}/`)
      await layout(page)
      await page.screenshot({ path: testInfo.outputPath(`ticket-forwards-${width}.png`) })
      const before = context.pages().length
      const tab = context.waitForEvent('page')
      await list.getByRole('button', { name: 'Open Working preview in new tab', exact: true }).click()
      const opened = await tab
      await expect(opened.getByRole('heading', { name: 'Real upstream' })).toBeVisible()
      await expect(opened.locator('#asset')).toHaveText('Asset loaded')
      expect(context.pages()).toHaveLength(before + 1)
      expect(opened.url()).toBe(omo.url(f.base_path))
      expect(opened.url()).not.toContain('#')
      expect(await opened.evaluate(() => window.opener)).toBeNull()
      expect(await opened.evaluate(() => history.length)).toBe(1)
      expect(await context.cookies(omo.url('/'))).toEqual(expect.arrayContaining([expect.objectContaining({ name: 'omo_proxy_session', httpOnly: true })]))
      for (const r of upstream.requests) {
        expect(r.url).not.toContain(omo.token)
        expect(r.headers.authorization).toBeUndefined()
        expect(r.headers.cookie ?? '').not.toContain('omo_proxy_')
        expect(r.headers.referer ?? '').not.toContain('#')
      }
      await opened.close()
      await expect(list.locator(`[data-forward="${f.id}"]`)).toBeVisible()
      // The full ticket page shares the same list and form.
      await panel.locator('[data-open-page]').click()
      const ticketPage = page.locator('[data-ticket-page]')
      await expect(ticketPage.getByRole('tab', { name: 'Forwards', exact: true })).toHaveAttribute('aria-selected', 'true')
      await ticketPage.getByRole('tab', { name: 'Overview', exact: true }).click()
      await expect(ticketPage.locator('[data-forward-list]')).toHaveCount(0)
      await ticketPage.getByRole('tab', { name: 'Forwards', exact: true }).click()
      await expect(ticketPage.locator(`[data-forward="${f.id}"]`)).toBeVisible()
      await layout(page)
      // A dead service stays registered, and Open still gives a useful page.
      await upstream.stop(); stopped = true
      const deadTab = context.waitForEvent('page')
      await ticketPage.getByRole('button', { name: 'Open Working preview in new tab', exact: true }).click()
      const dead = await deadTab
      await expect(dead.getByRole('heading')).toContainText(/unreachable/i)
      const back = dead.waitForResponse((r) => r.url() === omo.url('/') && r.request().isNavigationRequest())
      await dead.getByRole('link', { name: 'Back to the board', exact: true }).click()
      const boardResponse = await back
      expect(boardResponse.status()).toBe(200)
      expect(boardResponse.request().headers().referer).toBeUndefined()
      await expect(dead).toHaveURL(omo.url('/'))
      await expect(dead.getByRole('button', { name: 'Forwards', exact: true })).toBeVisible()
      await dead.close()
      await expect(ticketPage.locator(`[data-forward="${f.id}"]`)).toBeVisible()
      await page.getByRole('button', { name: 'Forwards', exact: true }).click()
      const all = page.getByRole('dialog', { name: 'Forwards', exact: true })
      await expect(all.locator(`[data-forward="${f.id}"]`)).toContainText(`Ticket #${n}`)
      await layout(page)
      // Global registration and cleanup arrive live, without leaving the overall list.
      await create(all, upstream.port, 'Global preview')
      await expect(all.locator('[data-forward]').filter({ hasText: 'Global preview' })).toContainText('Global')
      await page.screenshot({ path: testInfo.outputPath(`overall-forwards-${width}.png`) })
      await omo.api('POST', `/api/tickets/${n}/cancel`, { delete_branch: false })
      await expect(all.locator(`[data-forward="${f.id}"]`)).toHaveCount(0)
      await expect(all.locator('[data-forward]')).toHaveCount(1)
      await all.getByRole('button', { name: 'Remove Global preview', exact: true }).click()
      await expect(all.locator('[data-forward-empty]')).toContainText('No forwards')
      expect((await new AxeBuilder({ page }).analyze()).violations).toEqual([])
      await all.getByRole('button', { name: 'Close', exact: true }).click()
      await expect(ticketPage.locator('[data-forward-add]')).toHaveCount(0)
    } finally {
      if (!stopped) await upstream.stop()
    }
  })
}

test('overall form uses the keyboard, links to the owner and shows API validation failures', async ({ page, omo }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  const n = await omo.createTicket({ title: 'Owner ticket' })
  await openBoard(page, omo)
  await page.getByRole('button', { name: 'Forwards', exact: true }).click()
  const all = page.getByRole('dialog', { name: 'Forwards', exact: true })
  await all.locator('[data-forward-add]').click()
  await all.getByLabel('Belongs to', { exact: true }).selectOption('ticket')
  await all.getByLabel('Ticket number', { exact: true }).fill(String(n))
  await all.getByLabel('Port (required)', { exact: true }).fill('3000')
  await all.getByLabel('Name (required)', { exact: true }).fill('Keyboard preview')
  await all.getByLabel('Start path', { exact: true }).fill('invalid')
  await all.getByRole('button', { name: 'Add forward', exact: true }).click()
  await expect(all.getByRole('alert')).toBeVisible()
  await expect(all.getByRole('alert')).toContainText('start_path')
  await expect(all.getByLabel('Start path', { exact: true })).toHaveValue('invalid')
  await all.getByLabel('Start path', { exact: true }).fill('/')
  await all.getByLabel('Name (required)', { exact: true }).fill('Keyboard preview')
  await all.getByRole('switch', { name: 'Strip prefix', exact: true }).focus()
  await page.keyboard.press('Space')
  await expect(all.getByRole('switch', { name: 'Strip prefix', exact: true })).toHaveAttribute('aria-checked', 'true')
  await layout(page)
  expect((await new AxeBuilder({ page }).analyze()).violations).toEqual([])
  await all.getByRole('button', { name: 'Add forward', exact: true }).focus()
  await page.keyboard.press('Enter')
  await expect(all.locator('[data-forward]')).toHaveCount(1)
  await all.getByRole('link', { name: `Ticket #${n}`, exact: true }).click()
  await expect(all).toHaveCount(0)
  await page.locator('[data-ticket-panel]').getByRole('tab', { name: 'Forwards', exact: true }).click()
  await expect(page.locator('[data-ticket-panel] [data-forward]')).toHaveCount(1)
})

test('an app without a base path loads root assets through the Referer fallback', async ({ page, context, omo }) => {
  const upstream = await fixture()
  try {
    const n = await omo.createTicket({ title: 'Relative app preview' })
    const f = await omo.api('POST', '/api/proxy/forwards', {
      ticket: n, port: upstream.port, name: 'No base path', strip_prefix: true,
    }) as ProxyForward
    const before = await page.request.get(omo.url('/'))
    const securityPolicy = before.headers()['content-security-policy']
    expect(securityPolicy).toBeTruthy()
    await openBoard(page, omo)
    await page.goto(omo.url(`/${n}`))
    await page.locator('[data-ticket-panel]').getByRole('tab', { name: 'Forwards', exact: true }).click()
    const tab = context.waitForEvent('page')
    await page.getByRole('button', { name: 'Open No base path in new tab', exact: true }).click()
    const opened = await tab
    await expect(opened.getByRole('heading', { name: 'Real upstream' })).toBeVisible()
    await expect(opened.locator('#asset')).toHaveText('Asset loaded')
    expect(opened.url()).toBe(omo.url(f.base_path))
    expect(upstream.requests.some(r => r.url === '/asset.js')).toBe(true)
    expect(upstream.requests.every(r => !(r.headers.cookie ?? '').includes('omo_proxy_'))).toBe(true)
    // A proxy cookie alone never authenticates the board API, and the board's
    // own page retains its security policy while the app tab is open.
    const board = await page.request.get(omo.url('/'))
    expect(board.headers()['content-security-policy']).toBe(securityPolicy)
    expect(board.headers()['cache-control']).toBe('no-store')
    const unauthenticated = await opened.request.get(omo.url('/api/server'))
    expect(unauthenticated.status()).toBe(401)
    await opened.close()
    await page.getByRole('button', { name: 'Remove No base path', exact: true }).click()
    await expect(page.locator(`[data-forward="${f.id}"]`)).toHaveCount(0)
  } finally {
    await upstream.stop()
  }
})
