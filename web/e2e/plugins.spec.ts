import AxeBuilder from '@axe-core/playwright'
import { existsSync, readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import type { Page } from '@playwright/test'
import { openBoard } from './support/board'
import { expect, test, type Omo } from './support/omo'
import { makePlugin, type PluginRepo } from './support/plugin'

// Plugins built by each test as local bare repositories in its temporary
// home: one that logs moved tickets with its configuration's mark, answers a
// ticket action and a global action. The bundled pushover is installed with
// its empty credentials, so its test action sends nothing anywhere.

const NAME = 'ticket-log'
const manifest = (version = '1.0.0') => ({
  name: NAME,
  version,
  description: 'Logs moved tickets',
  default_config: { mark: 'one' },
  hooks: [
    { event: 'ticket_moved', lua: 'moved.lua' },
    { event: 'manual', name: 'hello', description: 'Say hello', scope: 'ticket', lua: 'hello.lua' },
    { event: 'manual', name: 'ping', description: 'Ping the plugin', scope: 'global', lua: 'ping.lua' },
  ],
})
const FILES = {
  // Configuration values of six bytes or more are redacted in logs; the marks are shorter.
  'moved.lua': 'omo.log("moved #" .. tostring(event.data.ticket.number) .. " to " .. tostring(event.data.to) .. " mark " .. tostring(config.mark))\n',
  'hello.lua': 'return "hello #" .. tostring(event.data.ticket.number)\n',
  'ping.lua': 'return { message = "pong" }\n',
}

const source = (omo: Omo, name = NAME, m: object = manifest(), files: Record<string, string> = FILES): PluginRepo => makePlugin(join(omo.home, 'plugin-src', name), m, files)

interface Listed {
  plugins: { name: string; state: string; commit: string; version: string }[]
  settings: { name: string; enabled: boolean; config: Record<string, unknown>; revision: string }[]
}
const listed = (omo: Omo) => omo.api<Listed>('GET', '/api/plugins')
const logLines = async (omo: Omo, name = NAME) => (await omo.api<{ message: string }[]>('GET', `/api/plugins/${name}/log`)).map((l) => l.message)
const settingsFile = (omo: Omo, name = NAME) => join(omo.home, 'omo', 'plugins', `${name}.yaml`)
const treeOf = (omo: Omo, name = NAME) => join(omo.home, 'omo', 'plugins', name)

const row = (page: Page, name = NAME) => page.locator(`[data-plugin="${name}"]`)
const detail = (page: Page, name = NAME) => page.locator(`[data-plugin-detail="${name}"]`)
const saveBar = (page: Page, name = NAME) => page.getByRole('region', { name: `Save changes to the configuration of ${name}` })
const logBox = (page: Page, name = NAME) => page.locator(`[data-plugin-log="${name}"]`)
const short = (commit: string) => commit.slice(0, 7)

async function openPlugins(page: Page, omo: Omo) {
  await openBoard(page, omo)
  await page.goto(omo.url('/settings/plugins'))
  await expect(page.locator('[data-settings-page] h1')).toHaveText('Plugins')
  await expect(page.locator('[data-install-plugin]')).toBeVisible()
}

/** Installs through the API (the dialog is not what the test is about) and waits until the page shows it ready. */
async function install(page: Page, omo: Omo, repo: PluginRepo, name = NAME) {
  await omo.api('POST', '/api/plugins', { name, source: repo.source, subpath: '', ref: '' })
  await expect(row(page, name).locator('[data-plugin-state]')).toHaveText('ready')
}

async function openDetail(page: Page, name = NAME) {
  await row(page, name).locator('[data-plugin-toggle]').click()
  await expect(detail(page, name)).toBeVisible()
}

/** Refreshes the log on the page until it holds `line`. */
async function expectLogLine(page: Page, line: string | RegExp, name = NAME) {
  await expect(async () => {
    await logBox(page, name).locator('[data-plugin-log-refresh]').click()
    await expect(logBox(page, name).locator('[data-plugin-log-text]')).toContainText(line, { timeout: 1_000 })
  }).toPass({ timeout: 20_000 })
}

async function violations(page: Page) {
  await page.evaluate(() => document.fonts.ready.then(() => undefined))
  const results = await new AxeBuilder({ page }).analyze()
  return results.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target.join(' ')) }))
}

const pageScrollsSideways = (page: Page) =>
  page.evaluate(() => {
    const root = document.documentElement
    return root.scrollWidth > root.clientWidth || document.body.scrollWidth > document.body.clientWidth
  })

test('install a local plugin; its hook logs moved tickets with its configuration; disabled it logs nothing', async ({ page, omo }) => {
  const repo = source(omo)
  const first = await omo.createTicket({ title: 'First' })
  const second = await omo.createTicket({ title: 'Second' })
  const third = await omo.createTicket({ title: 'Third' })
  await openPlugins(page, omo)
  await expect(page.locator('[data-no-plugins]')).toBeVisible()

  await page.getByRole('button', { name: 'Install plugin…' }).click()
  const dialog = page.locator('[data-install-dialog]')
  await expect(dialog.locator('[data-install-warning]')).toContainText('runs on this computer as you')
  await dialog.getByLabel('Source').fill(repo.source)
  await expect(dialog.locator('[data-install-address]')).toHaveText(repo.source)
  await expect(dialog.getByLabel('Name')).toHaveValue('plugin')
  await dialog.getByLabel('Name').fill(NAME)
  await dialog.getByRole('button', { name: 'Install', exact: true }).click()
  await expect(dialog).toBeHidden()
  await expect(row(page).locator('[data-plugin-state]')).toHaveText('ready')
  const d = detail(page)
  await expect(d.locator('[data-plugin-version]')).toHaveText(`1.0.0 at ${short(repo.head())}`)
  await expect(d).toContainText('When a ticket is moved')
  await expect(d).toContainText('Button on a ticket: Say hello')

  await omo.moveTicket(first, 'ready')
  await expectLogLine(page, `moved #${first} to ready mark one`)
  await expect(row(page).locator('[data-plugin-last-run]')).toContainText('on a moved ticket')

  // The configuration: the hook uses the saved value.
  await d.getByLabel('Configuration (JSON)').fill('{"mark": "two"}')
  await saveBar(page).getByRole('button', { name: 'Save', exact: true }).click()
  await expect(saveBar(page).locator('[data-save-state]')).toHaveText('Saved')
  await expect.poll(async () => (await listed(omo)).settings[0]?.config).toEqual({ mark: 'two' })
  await omo.moveTicket(second, 'ready')
  await expectLogLine(page, `moved #${second} to ready mark two`)

  // Off: a move while it is off is never logged, also not once it is on again. A
  // second plugin that stays on shows when omo has handed the move to the plugins.
  await omo.api('POST', '/api/plugins', { name: 'witness', source: repo.source, subpath: '', ref: '' })
  await expect(row(page, 'witness').locator('[data-plugin-state]')).toHaveText('ready')
  const enabled = d.getByRole('switch', { name: 'Runs its hooks' })
  await enabled.click()
  await expect(row(page).locator('[data-plugin-state]')).toHaveText('off')
  await expect.poll(async () => (await listed(omo)).settings.find((x) => x.name === NAME)?.enabled).toBe(false)
  await omo.moveTicket(third, 'ready')
  await expect.poll(async () => (await logLines(omo, 'witness')).filter((l) => l.startsWith(`moved #${third} `))).toEqual([`moved #${third} to ready mark one`])
  expect((await logLines(omo)).filter((l) => l.startsWith(`moved #${third} `))).toEqual([])
  await enabled.click()
  await expect(row(page).locator('[data-plugin-state]')).toHaveText('ready')
  await omo.moveTicket(third, 'planning')
  await expectLogLine(page, `moved #${third} to planning mark two`)
  expect((await logLines(omo)).filter((l) => l.startsWith(`moved #${third} `))).toEqual([`moved #${third} to planning mark two`])
})

test('a ticket action answers in the panel; a global action answers on the page', async ({ page, omo }) => {
  const repo = source(omo)
  const n = await omo.createTicket({ title: 'Say hello to me' })
  await openPlugins(page, omo)
  await install(page, omo, repo)

  await openDetail(page)
  await detail(page).getByRole('button', { name: 'Ping the plugin' }).click()
  const answer = detail(page).locator('[data-plugin-answer]')
  await expect(answer).toHaveText('pong')
  // Said once, by the page's announcer; the line itself is no live region.
  await expect(answer).not.toHaveAttribute('role')
  await expect(page.locator('[data-announcer="polite"]')).toHaveText('pong')

  await page.goto(omo.url(`/${n}`))
  const panel = page.locator('dialog[data-ticket-panel]')
  await expect(panel).toBeVisible()
  await panel.getByRole('button', { name: `${NAME}: Say hello` }).click()
  const shown = page.locator('[data-plugin-answer-dialog]')
  await expect(shown.locator('[data-plugin-answer]')).toHaveText(`hello #${n}`)
  await shown.locator('[data-plugin-answer-close]').click()
  await expect(shown).toHaveCount(0)
  await expect(panel).toBeVisible()

  // Off, the action leaves the panel.
  const s = (await listed(omo)).settings[0]!
  await fetch(`${omo.base}/api/plugins/${NAME}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${omo.token}`, 'If-Match': `"${s.revision}"` },
    body: JSON.stringify({ ...s, enabled: false }),
  })
  await expect(panel.getByRole('button', { name: `${NAME}: Say hello` })).toHaveCount(0)
})

test('a broken manifest is refused with the server message and nothing is installed', async ({ page, omo }) => {
  const repo = source(omo, 'old', { name: 'old', version: '1.0.0', hooks: [{ event: 'prompt_render', lua: 'x.lua' }] }, { 'x.lua': 'return ""\n' })
  await openPlugins(page, omo)
  await page.getByRole('button', { name: 'Install plugin…' }).click()
  const dialog = page.locator('[data-install-dialog]')
  await dialog.getByLabel('Source').fill(repo.source)
  await dialog.getByLabel('Name').fill('old')
  await dialog.getByRole('button', { name: 'Install', exact: true }).click()
  const shown = dialog.locator('[data-install-error]')
  await expect(shown).toContainText('prompt_render')
  const said = await omo.api('POST', '/api/plugins', { name: 'old', source: repo.source, subpath: '', ref: '' }).then(
    () => '',
    (e: Error) => e.message,
  )
  expect(said).toContain(await shown.locator('p').last().innerText())
  await expect(dialog.getByRole('button', { name: 'Install', exact: true })).toBeEnabled()
  const now = await listed(omo)
  expect(now.plugins).toEqual([])
  expect(now.settings).toEqual([])
  expect(existsSync(treeOf(omo, 'old'))).toBe(false)
  expect(existsSync(settingsFile(omo, 'old'))).toBe(false)

  // A bad field is shown under it.
  await dialog.getByLabel('Source').fill('ext::sh -c touch')
  await dialog.getByRole('button', { name: 'Install', exact: true }).click()
  const field = dialog.locator('.omo-field').filter({ has: page.getByLabel('Source') })
  await expect(field.locator('.omo-field__error')).toContainText('source must be')
  await expect(dialog.getByLabel('Source')).toHaveAttribute('aria-invalid', 'true')
})

/** Holds the browser's requests to `url` until the returned function is called; then they go to the real server. */
async function holdRequests(page: Page, url: string, method: string) {
  let release!: () => void
  const gate = new Promise<void>((r) => (release = r))
  let held = 0
  await page.route(url, async (route) => {
    if (route.request().method() !== method) return route.fallback()
    held++
    await gate
    await route.continue()
  })
  return { release, held: () => held }
}

test('while a failing install runs the dialog stays open and says why; the refusal is then shown in it', async ({ page, omo }) => {
  const repo = source(omo, 'old', { name: 'old', version: '1.0.0', hooks: [{ event: 'prompt_render', lua: 'x.lua' }] }, { 'x.lua': 'return ""\n' })
  await openPlugins(page, omo)
  const hold = await holdRequests(page, '**/api/plugins', 'POST')
  await page.getByRole('button', { name: 'Install plugin…' }).click()
  const dialog = page.locator('[data-install-dialog]')
  await dialog.getByLabel('Source').fill(repo.source)
  await dialog.getByLabel('Name').fill('old')
  await dialog.getByRole('button', { name: 'Install', exact: true }).click()
  await expect(dialog.getByRole('button', { name: 'Installing…' })).toBeDisabled()
  await expect.poll(hold.held).toBe(1)

  // Cancel cannot be pressed; Escape and the Close button say why instead of closing.
  await expect(dialog.locator('[data-install-cancel]')).toBeDisabled()
  await page.keyboard.press('Escape')
  await dialog.getByRole('button', { name: 'Close' }).click()
  await expect(dialog).toBeVisible()
  await expect(dialog.locator('[data-install-busy]')).toContainText('omo is still installing the plugin; the dialog closes when omo has answered.')

  hold.release()
  await expect(dialog.locator('[data-install-error]')).toContainText('prompt_render')
  await expect(dialog.locator('[data-install-busy]')).toHaveCount(0)
  await expect(dialog.locator('[data-install-cancel]')).toBeEnabled()
  await dialog.locator('[data-install-cancel]').click()
  await expect(dialog).toBeHidden()
  expect((await listed(omo)).settings).toEqual([])
})

test('while Update runs the plugin stays open: closing it says why, and the result is shown in it', async ({ page, omo }) => {
  const repo = source(omo)
  await openPlugins(page, omo)
  await install(page, omo, repo)
  await openDetail(page)
  const next = repo.commit(manifest('1.1.0'))
  const hold = await holdRequests(page, `**/api/plugins/${NAME}/update`, 'POST')
  await detail(page).getByRole('button', { name: 'Update' }).click()
  await expect.poll(hold.held).toBe(1)
  await row(page).locator('[data-plugin-toggle]').click()
  await expect(detail(page)).toBeVisible()
  await expect(page.locator('[data-editor-busy]')).toContainText(`omo is still working on the plugin ${NAME}; it stays open until omo has answered.`)
  hold.release()
  await expect(detail(page).locator('[data-plugin-update-result]')).toHaveText(`Updated; now at ${short(next)}.`)
  await expect(page.locator('[data-editor-busy]')).toHaveCount(0)
  await row(page).locator('[data-plugin-toggle]').click()
  await expect(detail(page)).toHaveCount(0)
})

test('Update installs the new commit; Remove asks first and deletes the plugin', async ({ page, omo }) => {
  const repo = source(omo)
  await openPlugins(page, omo)
  await install(page, omo, repo)
  await openDetail(page)
  const next = repo.commit(manifest('1.1.0'))
  await detail(page).getByRole('button', { name: 'Update' }).click()
  await expect(detail(page).locator('[data-plugin-update-result]')).toHaveText(`Updated; now at ${short(next)}.`)
  await expect(detail(page).locator('[data-plugin-version]')).toHaveText(`1.1.0 at ${short(next)}`)
  await expect(row(page)).toContainText('1.1.0')

  await detail(page).getByRole('button', { name: `Remove ${NAME}…` }).click()
  const question = page.locator('[data-question]')
  await expect(question).toContainText(`Remove the plugin ${NAME}?`)
  await expect(question).toContainText('everything it stored')
  await question.locator('[data-question-cancel]').click()
  await expect(row(page)).toBeVisible()
  await detail(page).getByRole('button', { name: `Remove ${NAME}…` }).click()
  await page.locator('[data-question-confirm]').click()
  await expect(row(page)).toHaveCount(0)
  await expect(page.locator('[data-no-plugins]')).toBeVisible()
  // Focus does not fall to the page: it goes where a plugin is installed.
  await expect(page.locator('[data-install-plugin]')).toBeFocused()
  await expect.poll(() => existsSync(treeOf(omo))).toBe(false)
  expect(existsSync(settingsFile(omo))).toBe(false)
})

test('a settings file whose plugin is missing offers Install', async ({ page, omo }) => {
  const repo = source(omo)
  writeFileSync(settingsFile(omo), `source: ${repo.source}\nsubpath: ""\nref: ""\nenabled: true\nconfig:\n  mark: one\n`)
  await openPlugins(page, omo)
  await expect(row(page).locator('[data-plugin-state]')).toHaveText('not installed')
  await openDetail(page)
  await expect(detail(page).locator('[data-plugin-missing]')).toBeVisible()
  await detail(page).getByRole('button', { name: 'Install', exact: true }).click()
  await expect(detail(page).locator('[data-plugin-update-result]')).toHaveText(`Installed; now at ${short(repo.head())}.`)
  await expect(row(page).locator('[data-plugin-state]')).toHaveText('ready')
})

test('a hand edit of the settings file is shown; with a draft it is a conflict', async ({ page, omo }) => {
  const repo = source(omo)
  await openPlugins(page, omo)
  await install(page, omo, repo)
  await openDetail(page)
  const config = detail(page).getByLabel('Configuration (JSON)')
  await expect(config).toHaveValue('{\n  "mark": "one"\n}')

  const file = settingsFile(omo)
  writeFileSync(file, readFileSync(file, 'utf8').replace(/mark: .*$/m, 'mark: six'))
  await expect(config).toHaveValue('{\n  "mark": "six"\n}')
  await expect(detail(page).locator('[data-changed-on-disk]')).toContainText('not through omo')

  await config.fill('{"mark": "draft"}')
  writeFileSync(file, readFileSync(file, 'utf8').replace(/mark: .*$/m, 'mark: ten'))
  await expect(saveBar(page).locator('[data-save-state]')).toHaveText('Changed outside this page')
  await expect(config).toHaveValue('{"mark": "draft"}')
  await detail(page).locator('[data-use-theirs]').click()
  await expect(config).toHaveValue('{\n  "mark": "ten"\n}')

  // Text that is no JSON object blocks Save.
  await config.fill('{"mark": ')
  await expect(detail(page)).toContainText('Enter a JSON object')
  await saveBar(page).getByRole('button', { name: 'Save', exact: true }).click()
  await expect(saveBar(page)).toContainText('Fix the fields marked above before saving.')
  await expect(config).toBeFocused()
})

test('the bundled pushover is offered, installs, and its test sends nothing without credentials', async ({ page, omo }) => {
  await openPlugins(page, omo)
  const offered = page.locator('[data-bundled="pushover"]')
  await expect(offered).toBeVisible()
  await offered.getByRole('button', { name: 'Install pushover' }).click()
  await expect(row(page, 'pushover').locator('[data-plugin-state]')).toHaveText('ready')
  await expect(offered).toHaveCount(0)
  await expect(page.locator('[data-no-bundled]')).toBeVisible()
  const d = detail(page, 'pushover')
  await expect(d.locator('[data-plugin-version]')).toContainText('bundled with omo')
  await expect(d.getByLabel('Configuration (JSON)')).toHaveValue(/"user_key": ""/)
  await d.getByRole('button', { name: 'Send a test notification' }).click()
  await expect(d.locator('[data-plugin-answer]')).toHaveText("Not sent: set user_key and app_token in the plugin's configuration.")
})

test('at 390 px the page does not scroll sideways', async ({ page, omo }) => {
  const repo = source(omo)
  await page.setViewportSize({ width: 390, height: 844 })
  await openPlugins(page, omo)
  await install(page, omo, repo)
  const n = await omo.createTicket({ title: 'Narrow' })
  await omo.moveTicket(n, 'ready')
  await openDetail(page)
  await expectLogLine(page, `moved #${n} to ready`)
  await detail(page).getByLabel('Configuration (JSON)').fill(`{"mark": "one", "long": "${'x'.repeat(300)}"}`)
  expect(await pageScrollsSideways(page)).toBe(false)
  await page.getByRole('button', { name: 'Install plugin…' }).click()
  await page.locator('[data-install-dialog]').getByLabel('Source').fill(`/${'very-long-folder-name/'.repeat(12)}`)
  expect(await pageScrollsSideways(page)).toBe(false)
})

for (const width of [1440, 390] as const) {
  for (const colorScheme of ['dark', 'light'] as const) {
    test.describe(`axe at ${width} px, ${colorScheme} theme`, () => {
      test.use({ viewport: { width, height: width === 390 ? 844 : 900 }, colorScheme, reducedMotion: 'reduce' })

      test('the page, an open plugin, the install dialog and the ticket action have no violations', async ({ page, omo }) => {
        const repo = source(omo)
        await openPlugins(page, omo)
        expect(await violations(page), 'empty').toEqual([])
        await install(page, omo, repo)
        const n = await omo.createTicket({ title: 'Axe' })
        await omo.moveTicket(n, 'ready')
        await openDetail(page)
        await expectLogLine(page, `moved #${n} to ready`)
        await detail(page).getByRole('button', { name: 'Ping the plugin' }).click()
        await expect(detail(page).locator('[data-plugin-answer]')).toHaveText('pong')
        await detail(page).getByLabel('Configuration (JSON)').fill('{"mark": ')
        expect(await violations(page), 'open plugin').toEqual([])
        await page.getByRole('button', { name: 'Install plugin…' }).click()
        await page.locator('[data-install-dialog]').getByLabel('Source').fill(repo.source)
        expect(await violations(page), 'install dialog').toEqual([])
        await page.locator('[data-install-dialog]').getByRole('button', { name: 'Cancel' }).click()
        await saveBar(page).getByRole('button', { name: 'Discard' }).click()

        await page.goto(omo.url(`/${n}`))
        const panel = page.locator('dialog[data-ticket-panel]')
        await panel.getByRole('button', { name: `${NAME}: Say hello` }).click()
        await expect(page.locator('[data-plugin-answer-dialog] [data-plugin-answer]')).toHaveText(`hello #${n}`)
        expect(await violations(page), 'ticket action').toEqual([])
      })
    })
  }
}
