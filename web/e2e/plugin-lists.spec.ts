import AxeBuilder from '@axe-core/playwright'
import { mkdirSync } from 'node:fs'
import { createServer, type Server } from 'node:http'
import type { AddressInfo } from 'node:net'
import { join } from 'node:path'
import type { Page } from '@playwright/test'
import { openBoard } from './support/board'
import { expect, pluginListPath, test, type Omo } from './support/omo'
import { makePlugin, makePluginList } from './support/plugin'

// Additional plugin lists: the user adds list addresses on the Plugins page;
// the lists are served over http by a server of this test on 127.0.0.1 (no
// network), their plugins show under their list as community plugins
// (whatever the list says) and install only after the warning; a list that
// cannot be read shows its error while omo's own list still renders.
//
// OMO_SCREENSHOTS=<folder> the theme tests also write the screenshots
// plugin-lists-<theme>-<width>.png there.

const SHOTS = process.env.OMO_SCREENSHOTS ?? ''

const NAME = 'claimer'
const manifest = { name: NAME, version: '1.0.0', description: 'A third-party plugin', hooks: [{ event: 'manual', name: 'ping', description: 'Ping', scope: 'global', lua: 'ping.lua' }] }

/** Serves `files` by path on 127.0.0.1; closed when the test ends. */
async function serveLists(files: Record<string, string>): Promise<{ server: Server; url: (path: string) => string; origin: string }> {
  const server = createServer((req, res) => {
    const text = files[req.url ?? '']
    if (text === undefined) {
      res.writeHead(404).end('not here')
      return
    }
    res.writeHead(200, { 'Content-Type': 'text/plain' }).end(text)
  })
  await new Promise<void>((done) => server.listen(0, '127.0.0.1', done))
  const origin = `127.0.0.1:${(server.address() as AddressInfo).port}`
  return { server, url: (p) => `http://${origin}${p}`, origin }
}

async function setup(omo: Omo) {
  makePluginList(pluginListPath(omo.home))
  const repo = makePlugin(join(omo.home, 'plugin-src', NAME), manifest, { 'ping.lua': 'return { message = "pong" }\n' })
  const lists = await serveLists({
    '/good.yaml': [
      'plugins:',
      `  - name: ${NAME}`,
      '    tag: official',
      '    description: "Claims to be <b>official</b>"',
      `    source: ${JSON.stringify(repo.source)}`,
      '  - name: pushover',
      '    tag: official',
      '    description: Takes the name of an official plugin',
      `    source: ${JSON.stringify(repo.source)}`,
      '    path: other',
      '',
    ].join('\n'),
    '/broken.yaml': 'plugins: [\n',
  })
  return { repo, lists }
}

async function openPlugins(page: Page, omo: Omo) {
  await openBoard(page, omo)
  await page.goto(omo.url('/settings/plugins'))
  await expect(page.locator('[data-settings-page] h1')).toHaveText('Plugins')
  await expect(page.locator('[data-catalog-plugin="pushover"]')).toBeVisible()
}

const listsForm = (page: Page) => page.locator('[data-plugin-lists]')
const listsSaveBar = (page: Page) => page.getByRole('region', { name: 'Save changes to the plugin lists' })
const listBlock = (page: Page, url: string) => page.locator(`[data-plugin-list="${url}"]`)
const trustDialog = (page: Page) => page.locator('dialog[data-trust-dialog]')

async function addLists(page: Page, urls: string[]) {
  for (const [i, url] of urls.entries()) {
    await listsForm(page).getByRole('button', { name: 'Add list' }).click()
    await listsForm(page).getByLabel(`Address ${i + 1}`, { exact: true }).fill(url)
  }
  await listsSaveBar(page).getByRole('button', { name: 'Save', exact: true }).click()
  await expect(listsSaveBar(page)).toContainText('Saved')
}

test('an added list shows its plugins attributed and as community; installing asks first; a broken list shows its error beside omo\'s list', async ({ page, omo }) => {
  const { lists } = await setup(omo)
  try {
    await openPlugins(page, omo)
    await expect(listsForm(page)).toContainText('run commands on this computer as you')

    // A non-http address is refused at save, under its row.
    await listsForm(page).getByRole('button', { name: 'Add list' }).click()
    await listsForm(page).getByLabel('Address 1', { exact: true }).fill('file:///etc/passwd')
    await listsSaveBar(page).getByRole('button', { name: 'Save', exact: true }).click()
    await expect(listsForm(page).locator('[data-row="0"]')).toContainText('http:// or https://')
    await listsForm(page).getByRole('button', { name: 'Remove address 1' }).click()

    await addLists(page, [lists.url('/broken.yaml'), lists.url('/good.yaml')])
    expect((await omo.api<{ settings: { plugin_lists: string[] } }>('GET', '/api/settings')).settings.plugin_lists).toEqual([lists.url('/broken.yaml'), lists.url('/good.yaml')])

    // omo's own list still renders; the broken list says why.
    await expect(page.locator('[data-catalog-plugin="pushover"]')).toHaveCount(1)
    await expect(page.locator('[data-catalog-plugin="pushover"] [data-catalog-tag]')).toHaveText('official')
    await expect(listBlock(page, lists.url('/broken.yaml')).locator('[data-plugin-list-error]')).toContainText('could not read this plugin list')
    const good = listBlock(page, lists.url('/good.yaml'))
    await expect(good.locator('[data-plugin-list-notes]')).toContainText('"pushover" is not shown')

    const row = good.locator(`[data-catalog-plugin="${NAME}"]`)
    await expect(row.locator('[data-catalog-tag]')).toHaveText('community')
    await expect(row.locator('[data-catalog-origin]')).toHaveText(`Listed by ${lists.origin}, not by omo`)
    await expect(row.locator('[data-catalog-description]')).toHaveText('Claims to be <b>official</b>')

    // Installing asks first; Cancel installs nothing.
    await row.getByRole('button', { name: `Install ${NAME}` }).click()
    await expect(trustDialog(page)).toHaveAttribute('data-trust-dialog', 'community')
    await trustDialog(page).getByRole('button', { name: 'Cancel' }).click()
    await expect(trustDialog(page)).toHaveCount(0)
    await row.getByRole('button', { name: `Install ${NAME}` }).click()
    await trustDialog(page).getByRole('button', { name: `Install ${NAME}` }).click()
    await expect(page.locator(`[data-plugin="${NAME}"] [data-plugin-state]`)).toHaveText('ready')
    await expect(row.locator('[data-catalog-installed]')).toHaveText('Installed')
  } finally {
    lists.server.close()
  }
})

test("the server refuses a list's plugin without the acknowledgement, whatever the list claims", async ({ omo }) => {
  const { repo, lists } = await setup(omo)
  try {
    await omo.api('PATCH', '/api/settings', { plugin_lists: [lists.url('/good.yaml')] })
    const cat = await omo.api<{ plugins: { name: string; tag: string; list: string }[] }>('GET', '/api/plugins/catalog')
    expect(cat.plugins.find((p) => p.name === NAME)).toMatchObject({ tag: 'community', list: lists.url('/good.yaml') })
    await expect(omo.api('POST', '/api/plugins', { name: NAME, source: repo.source })).rejects.toThrow(/acknowledge/)
  } finally {
    lists.server.close()
  }
})

for (const colorScheme of ['dark', 'light'] as const) {
  test.describe(`${colorScheme} theme`, () => {
    test.use({ viewport: { width: 1440, height: 900 }, colorScheme, reducedMotion: 'reduce' })

    test('the lists and their plugins have no axe violations', async ({ page, omo }) => {
      const { lists } = await setup(omo)
      try {
        await omo.api('PATCH', '/api/settings', { plugin_lists: [lists.url('/broken.yaml'), lists.url('/good.yaml')] })
        await openPlugins(page, omo)
        await expect(listBlock(page, lists.url('/good.yaml')).locator(`[data-catalog-plugin="${NAME}"]`)).toBeVisible()
        await expect(listBlock(page, lists.url('/broken.yaml')).locator('[data-plugin-list-error]')).toBeVisible()
        await page.evaluate(() => document.fonts.ready.then(() => undefined))
        const results = await new AxeBuilder({ page }).analyze()
        expect(results.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target.join(' ')) }))).toEqual([])
        if (SHOTS !== '') {
          mkdirSync(SHOTS, { recursive: true })
          await listBlock(page, lists.url('/broken.yaml')).scrollIntoViewIfNeeded()
          await page.screenshot({ path: join(SHOTS, `plugin-lists-${colorScheme}-1440.png`), fullPage: false })
          await listsForm(page).scrollIntoViewIfNeeded()
          await page.screenshot({ path: join(SHOTS, `plugin-lists-${colorScheme}-1440-form.png`), fullPage: false })
        }
      } finally {
        lists.server.close()
      }
    })
  })
}
