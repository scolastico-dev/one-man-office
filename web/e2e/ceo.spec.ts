// The CEO tab: starting, attaching, a new session, stopping and starting
// again, and choosing the CEO's model. With OMO_SCREENSHOTS=<folder> the
// model test writes ceo-menu-<state>-<theme>.png there.
import AxeBuilder from '@axe-core/playwright'
import { mkdirSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test, type Omo } from './support/omo'
import { expectNoAgentProcesses, liveAgents, script } from './support/agents'
import { observe, openBoard } from './support/board'
import { expectNoTokenInUrls, expectTerminalText, terminalIn } from './support/terminal'
import type { Page } from '@playwright/test'

test.use({ viewport: { width: 1440, height: 900 } })

const SHOTS = process.env.OMO_SCREENSHOTS ?? ''

async function violations(page: Page) {
  await page.evaluate(() => document.fonts.ready.then(() => undefined))
  const results = await new AxeBuilder({ page }).include('[data-sidebar]').analyze()
  return results.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target.join(' ')) }))
}

/** Screenshots of the sidebar's state in both themes, when asked for. */
async function shoot(page: Page, state: string) {
  if (SHOTS === '') return
  mkdirSync(SHOTS, { recursive: true })
  for (const theme of ['dark', 'light'] as const) {
    await page.emulateMedia({ colorScheme: theme })
    await page.evaluate('document.fonts.ready')
    await page.screenshot({ path: join(SHOTS, `ceo-menu-${state}-${theme}.png`) })
  }
  await page.emulateMedia({ colorScheme: null })
}

/** Gives the fake profile a second model, `fake/second`. */
async function addSecondModel(omo: Omo) {
  const { profiles } = await omo.api<{ profiles: { name: string; models: Record<string, unknown> }[] }>('GET', '/api/profiles')
  const fake = profiles.find((p) => p.name === 'fake')!
  const { revision: _revision, ...rest } = fake as typeof fake & { revision?: string }
  await omo.api('PUT', '/api/profiles/fake', {
    ...rest,
    models: { ...fake.models, second: { args: [], env: {}, usage_windows: [], selectable: false, description: 'a second fake model' } },
  })
}

const sidebar = (page: Page) => page.locator('[data-sidebar]')
const ceoTab = (page: Page) => sidebar(page).getByRole('tab', { name: /^CEO/ })
const liveCeos = async (omo: Omo) => (await liveAgents(omo)).filter((s) => s.role === 'ceo')

/** Records the path of every request that starts a CEO; call before the first navigation. */
function ceoStarts(page: Page): string[] {
  const starts: string[] = []
  page.on('request', (r) => {
    if (r.method() === 'POST' && /\/api\/ceo/.test(r.url())) starts.push(new URL(r.url()).pathname)
  })
  return starts
}

/** Waits for the next read of the live agents: the tab may start a CEO only after it. */
const agentsRead = (page: Page) => page.waitForResponse((r) => new URL(r.url()).pathname === '/api/agents' && r.request().method() === 'GET')

test.describe('with a CEO', () => {
  test.use({ omoOptions: { agents: { ceo: script('ready', 'print|CEO ready', 'hang') } } })

  test('opening the CEO tab starts the CEO and shows its terminal', async ({ page, omo }) => {
    const seen = observe(page)
    await openBoard(page, omo)
    expect(await liveCeos(omo)).toHaveLength(0)
    await ceoTab(page).click()
    const terminal = terminalIn(sidebar(page))
    await expect(terminal).toBeVisible({ timeout: 30_000 })
    await expectTerminalText(terminal, 'CEO ready')
    await expect(sidebar(page).getByText('Write to the CEO in the box below the terminal.')).toBeVisible()
    expect(await liveCeos(omo)).toHaveLength(1)
    expectNoTokenInUrls(seen, omo.token)
    await expectNoAgentProcesses(omo)
  })

  test('a reload with the CEO tab open attaches to the running CEO and starts none', async ({ page, omo }) => {
    const starts = ceoStarts(page)
    await openBoard(page, omo)
    await ceoTab(page).click()
    await expect(terminalIn(sidebar(page))).toBeVisible({ timeout: 30_000 })
    const [before] = await liveCeos(omo)
    expect(starts).toEqual(['/api/ceo'])

    const read = agentsRead(page)
    await page.reload()
    await read
    await expect(page.locator('[data-connection]')).toHaveAttribute('data-state', 'live')
    await expect(ceoTab(page)).toHaveAttribute('aria-selected', 'true')
    await expect(terminalIn(sidebar(page))).toBeVisible({ timeout: 30_000 })
    expect(starts).toEqual(['/api/ceo'])
    const after = await liveCeos(omo)
    expect(after.map((s) => s.id)).toEqual([before!.id])
    await expectNoAgentProcesses(omo)
  })

  test('a restored CEO tab with no CEO running starts nothing until the button is used', async ({ page, omo }) => {
    const starts = ceoStarts(page)
    await page.addInitScript(() => localStorage.setItem('omo.sidebar', JSON.stringify({ open: true, width: 480, tab: 'ceo' })))
    let read = agentsRead(page)
    await openBoard(page, omo)
    await read
    await expect(ceoTab(page)).toHaveAttribute('aria-selected', 'true')
    await expect(sidebar(page).getByText('The CEO is not running.')).toBeVisible()
    read = agentsRead(page)
    await page.reload()
    await read
    await expect(sidebar(page).getByText('The CEO is not running.')).toBeVisible()
    expect(starts).toEqual([])

    await sidebar(page).getByRole('button', { name: 'Start the CEO' }).click()
    await expect(terminalIn(sidebar(page))).toBeVisible({ timeout: 30_000 })
    // The click is the only start.
    expect(starts).toEqual(['/api/ceo'])
    expect(await liveCeos(omo)).toHaveLength(1)
    await expectNoAgentProcesses(omo)
  })

  test('New session ends the old session and shows the new one', async ({ page, omo }) => {
    await openBoard(page, omo)
    await ceoTab(page).click()
    await expect(terminalIn(sidebar(page))).toBeVisible({ timeout: 30_000 })
    const [old] = await liveCeos(omo)

    await sidebar(page).getByRole('button', { name: 'New session…' }).click()
    const dialog = page.getByRole('dialog', { name: 'Start a new CEO session?' })
    await expect(dialog).toContainText("End the CEO's current session and start a fresh one?")
    await dialog.getByRole('button', { name: 'Start a new session' }).click()
    await expect(dialog).toBeHidden()

    await expect.poll(async () => (await liveCeos(omo)).map((s) => s.id), { timeout: 30_000 }).toHaveLength(1)
    const [fresh] = await liveCeos(omo)
    expect(fresh!.id).not.toBe(old!.id)
    await expect(sidebar(page).getByText('The CEO session ended.')).toHaveCount(0)
    await expectTerminalText(terminalIn(sidebar(page)), 'CEO ready')
    await expectNoAgentProcesses(omo)
  })

  test('Stop ends the CEO and keeps it stopped until its Start button is used', async ({ page, omo }) => {
    const starts = ceoStarts(page)
    await openBoard(page, omo)
    await ceoTab(page).click()
    await expect(terminalIn(sidebar(page))).toBeVisible({ timeout: 30_000 })
    const [running] = await liveCeos(omo)

    await sidebar(page).getByRole('button', { name: 'Stop…' }).click()
    const dialog = page.getByRole('dialog', { name: 'Stop the CEO?' })
    await expect(dialog).toContainText('It stays stopped until you start it again')
    await dialog.getByRole('button', { name: 'Stop the CEO' }).click()
    await expect(dialog).toBeHidden()
    await expect(sidebar(page).locator('[data-ceo-stopped]')).toHaveText(/^The CEO is stopped\./)
    await expect.poll(async () => (await liveCeos(omo)).length).toBe(0)
    const ended = (await omo.api<{ id: string; end_reason: string }[]>('GET', '/api/agents')).find((s) => s.id === running!.id)
    expect(ended?.end_reason).toBe('stopped by the user')
    expect(await omo.api('GET', '/api/ceo')).toEqual({ stopped: true })

    // Choosing the tab again, or a reload with it open, starts nothing.
    await sidebar(page).getByRole('tab', { name: /^Agents/ }).click()
    await ceoTab(page).click()
    await expect(sidebar(page).locator('[data-ceo-stopped]')).toBeVisible()
    const read = agentsRead(page)
    await page.reload()
    await read
    await expect(sidebar(page).locator('[data-ceo-stopped]')).toBeVisible()
    expect(starts).toEqual(['/api/ceo', '/api/ceo/stop'])
    expect(await liveCeos(omo)).toHaveLength(0)

    await sidebar(page).getByRole('button', { name: 'Start the CEO' }).click()
    await expectTerminalText(terminalIn(sidebar(page)), 'CEO ready')
    expect(starts).toEqual(['/api/ceo', '/api/ceo/stop', '/api/ceo'])
    expect(await liveCeos(omo)).toHaveLength(1)
    expect(await omo.api('GET', '/api/ceo')).toEqual({ stopped: false })
    await expectNoAgentProcesses(omo)
  })

  test('the tab chooses the CEO’s model: it is the CEO role’s setting and takes effect at the next start or Restart now', async ({ page, omo }) => {
    await addSecondModel(omo)
    await openBoard(page, omo)
    await ceoTab(page).click()
    await expect(terminalIn(sidebar(page))).toBeVisible({ timeout: 30_000 })
    const [first] = await liveCeos(omo)
    expect(first!.model).toBe('fake/default')
    const picker = sidebar(page).getByRole('button', { name: 'CEO model: fake/default' })
    await expect(picker).toBeVisible()
    await shoot(page, 'running')
    expect(await violations(page)).toEqual([])

    await picker.click()
    await page.getByRole('combobox', { name: 'Search models' }).fill('second')
    await shoot(page, 'picker')
    expect(await violations(page)).toEqual([])
    await page.getByRole('option', { name: /fake\/second/ }).click()
    const next = sidebar(page).locator('[data-ceo-model-next]')
    await expect(next).toContainText('The CEO runs fake/default; fake/second takes effect at its next start.')
    const roles = await omo.api<{ name: string; models: string[]; assignment: string }[]>('GET', '/api/roles')
    expect(roles.find((r) => r.name === 'ceo')).toMatchObject({ models: ['fake/second'], assignment: 'round_robin' })
    // The running CEO keeps its model until it starts again.
    expect((await liveCeos(omo)).map((s) => s.model)).toEqual(['fake/default'])
    await shoot(page, 'next-start')
    expect(await violations(page)).toEqual([])

    await next.getByRole('button', { name: 'Restart now' }).click()
    await expect.poll(async () => (await liveCeos(omo)).map((s) => s.id).filter((id) => id !== first!.id), { timeout: 30_000 }).toHaveLength(1)
    expect((await liveCeos(omo)).map((s) => s.model)).toEqual(['fake/second'])
    await expect(next).toHaveCount(0)
    await expectTerminalText(terminalIn(sidebar(page)), 'CEO ready')
    await expect(sidebar(page).getByRole('button', { name: 'CEO model: fake/second' })).toBeVisible()

    // Stopped, the tab still chooses the model, for the next start.
    await sidebar(page).getByRole('button', { name: 'Stop…' }).click()
    await page.getByRole('dialog', { name: 'Stop the CEO?' }).getByRole('button', { name: 'Stop the CEO' }).click()
    await expect(sidebar(page).locator('[data-ceo-stopped]')).toBeVisible()
    await sidebar(page).getByRole('button', { name: 'CEO model: fake/second' }).click()
    await page.getByRole('option', { name: /fake\/default/ }).click()
    await expect(sidebar(page).getByRole('button', { name: 'CEO model: fake/default' })).toBeVisible()
    await shoot(page, 'stopped')
    expect(await violations(page)).toEqual([])
    await sidebar(page).getByRole('button', { name: 'Start the CEO' }).click()
    await expectTerminalText(terminalIn(sidebar(page)), 'CEO ready')
    expect((await liveCeos(omo)).map((s) => s.model)).toEqual(['fake/default'])
    await expectNoAgentProcesses(omo)
  })

  test('with the CEO off the tab is hidden', async ({ page, omo }) => {
    await openBoard(page, omo)
    await expect(ceoTab(page)).toBeVisible()
    const { settings } = await omo.api<{ settings: { ceo: { enabled: boolean } } }>('GET', '/api/settings')
    await omo.api('PUT', '/api/settings', { ...settings, ceo: { ...settings.ceo, enabled: false } })
    await expect(ceoTab(page)).toHaveCount(0)
    await expect(sidebar(page).getByRole('tab')).toHaveText([/^Agents/, /^Terminals/])
    expect(await liveCeos(omo)).toHaveLength(0)
  })
})

test.describe('with a CEO that ends by itself', () => {
  test.use({ omoOptions: { agents: { ceo: script('ready', 'print|CEO ready', 'exit|0') } } })

  test('collapsing and expanding the sidebar, or a window that gets narrow and wide again, does not start an ended CEO again', async ({ page, omo }) => {
    const starts = ceoStarts(page)
    await openBoard(page, omo)
    await ceoTab(page).click()
    await expect(sidebar(page).getByText('The CEO session ended.')).toBeVisible({ timeout: 30_000 })
    expect(starts).toEqual(['/api/ceo'])

    await sidebar(page).locator('[data-sidebar-toggle]').click()
    await expect(sidebar(page)).toHaveAttribute('data-open', 'false')
    await sidebar(page).locator('[data-sidebar-toggle]').click()
    await expect(sidebar(page)).toHaveAttribute('data-open', 'true')
    await expect(sidebar(page).getByText('The CEO is not running.')).toBeVisible()
    expect(starts).toEqual(['/api/ceo'])

    await page.setViewportSize({ width: 390, height: 844 })
    await expect(sidebar(page)).toHaveCount(0)
    await page.setViewportSize({ width: 1440, height: 900 })
    await expect(sidebar(page).getByText('The CEO is not running.')).toBeVisible()
    expect(starts).toEqual(['/api/ceo'])
    expect(await liveCeos(omo)).toHaveLength(0)
    await expectNoAgentProcesses(omo)
  })
})
