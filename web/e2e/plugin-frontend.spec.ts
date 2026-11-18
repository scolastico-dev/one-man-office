import { join } from 'node:path'
import { openBoard } from './support/board'
import { expect, test } from './support/omo'
import { makePlugin } from './support/plugin'

// Actual installed entry and image bytes, no intercepted script requests.
test('frontend executes under self-only CSP, loads its asset, invokes its own hook and follows lifecycle on every page', async ({ page, omo }) => {
  const repo = makePlugin(join(omo.home, 'frontend-source'), {
    name: 'frontend', version: '1.0.0', description: 'Frontend sample',
    frontend: { script: 'entry.js', assets: 'public' },
    buttons: [{ label: 'Declared ticket', place: 'ticket', action: 'ticket' }],
    hooks: [
      { event: 'manual', name: 'ping', scope: 'global', description: 'Ping', lua: 'ping.lua' },
      { event: 'manual', name: 'ticket', scope: 'ticket', description: 'Ticket', lua: 'ticket.lua' },
    ],
  }, {
    'entry.js': `(() => {
      const omo = document.currentScript.omo;
      omo.initialize(() => {
        omo.addButton({id:'ping',label:'Script ping',place:'sidebar',action:'ping'});
        omo.addButton({id:'ticket',label:'Script ticket',place:'ticket',action:'ticket'});
        omo.onRouteChange((route) => {
          omo.addPanel({id:'context',place:'sidebar',title:'Script context',text:route.page,image:omo.assetUrl('image.svg')});
          document.documentElement.dataset.pluginPage = route.page;
        });
      });
    })();`,
    'public/image.svg': '<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20"><rect width="20" height="20" fill="green"/></svg>',
    'ping.lua': 'return {message="Own hook answered"}',
    'ticket.lua': 'return {message="Ticket " .. tostring(event.data.ticket.number)}',
  })
  await omo.api('POST', '/api/plugins', { name: 'frontend', source: repo.source, acknowledge: 'community' })
  const number = await omo.createTicket({ title: 'Frontend test' })
  await openBoard(page, omo)
  const csp = await page.request.get(omo.base)
  expect(csp.headers()['content-security-policy']).toContain("script-src 'self'")
  expect(csp.headers()['content-security-policy']).not.toContain('unsafe-eval')
  await page.locator('[data-sidebar-toggle]').click()
  const panel = page.locator('[data-plugin-panel="frontend:context"]')
  await expect(panel).toContainText('board')
  await expect.poll(() => panel.locator('img').evaluate((image: HTMLImageElement) => image.complete && image.naturalWidth > 0)).toBe(true)
  const scriptButton = page.getByRole('button', { name: 'Script ping', exact: true })
  const bounds = await scriptButton.boundingBox()
  const sidebar = await page.locator('[data-sidebar]').boundingBox()
  expect(bounds!.y + bounds!.height).toBeLessThanOrEqual(sidebar!.y + sidebar!.height)
  await scriptButton.click()
  await expect(page.locator('[data-plugin-controls="sidebar"] [data-plugin-control-answer]')).toHaveText('Own hook answered')
  await page.setViewportSize({ width: 390, height: 720 })
  await page.locator('[data-sidebar-open]').click()
  const sheet = page.locator('dialog.side-sheet')
  const mobileButton = await sheet.getByRole('button', { name: 'Script ping', exact: true }).boundingBox()
  const sheetBounds = await sheet.boundingBox()
  expect(mobileButton!.y + mobileButton!.height).toBeLessThanOrEqual(sheetBounds!.y + sheetBounds!.height)
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await sheet.getByRole('button', { name: 'Close', exact: true }).click()
  await page.goto(omo.url(`/ticket/${number}`))
  await expect(page.getByRole('button', { name: 'Script ticket', exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Declared ticket', exact: true }).click()
  await expect(page.locator('[data-plugin-controls="ticket"] [data-plugin-control-answer]')).toHaveText(`Ticket ${number}`)
  await expect(page.getByRole('button', { name: 'frontend: Ticket', exact: true })).toHaveCount(0)
  await page.goto(omo.url('/settings/plugins'))
  await expect(page.locator('html')).toHaveAttribute('data-plugin-page', 'settings')
  await page.goto(omo.url('/terminal/user/absent'))
  await expect(page.locator('html')).toHaveAttribute('data-plugin-page', 'terminal')
  await page.goto(omo.url('/'))
  const list = await omo.api<{ settings: { name: string; enabled: boolean; source: string; config: object }[] }>('GET', '/api/plugins')
  await omo.api('PUT', '/api/plugins/frontend', { ...list.settings.find((s) => s.name === 'frontend'), revision: undefined, enabled: false })
  await expect(page.locator('script[data-omo-plugin="frontend"]')).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Script ping', exact: true })).toHaveCount(0)
})
