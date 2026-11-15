import { expect, test, type Omo } from './support/omo'
import { expectNoAgentProcesses, liveAgents, script } from './support/agents'
import { observe, openBoard } from './support/board'
import { expectNoTokenInUrls, expectTerminalText, terminalIn } from './support/terminal'
import type { Page } from '@playwright/test'

test.use({ viewport: { width: 1440, height: 900 } })

const sidebar = (page: Page) => page.locator('[data-sidebar]')
const ceoTab = (page: Page) => sidebar(page).getByRole('tab', { name: /^CEO/ })
const liveCeos = async (omo: Omo) => (await liveAgents(omo)).filter((s) => s.role === 'ceo')

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
    await expect(sidebar(page).getByText('Typing here types into the CEO.')).toBeVisible()
    expect(await liveCeos(omo)).toHaveLength(1)
    expectNoTokenInUrls(seen, omo.token)
    await expectNoAgentProcesses(omo)
  })

  test('a reload with the CEO tab open attaches to the running CEO and starts none', async ({ page, omo }) => {
    await openBoard(page, omo)
    await ceoTab(page).click()
    await expect(terminalIn(sidebar(page))).toBeVisible({ timeout: 30_000 })
    const [before] = await liveCeos(omo)
    const starts: string[] = []
    page.on('request', (r) => {
      if (r.method() === 'POST' && /\/api\/ceo/.test(r.url())) starts.push(r.url())
    })

    await page.reload()
    await expect(page.locator('[data-connection]')).toHaveAttribute('data-state', 'live')
    await expect(ceoTab(page)).toHaveAttribute('aria-selected', 'true')
    await expect(terminalIn(sidebar(page))).toBeVisible({ timeout: 30_000 })
    expect(starts).toEqual([])
    const after = await liveCeos(omo)
    expect(after.map((s) => s.id)).toEqual([before!.id])
    await expectNoAgentProcesses(omo)
  })

  test('a restored CEO tab with no CEO running starts nothing until the button is used', async ({ page, omo }) => {
    await page.addInitScript(() => localStorage.setItem('omo.sidebar', JSON.stringify({ open: true, width: 480, tab: 'ceo' })))
    await openBoard(page, omo)
    await expect(ceoTab(page)).toHaveAttribute('aria-selected', 'true')
    await expect(sidebar(page).getByText('The CEO is not running.')).toBeVisible()
    await page.waitForTimeout(1000)
    expect(await liveCeos(omo)).toHaveLength(0)
    await page.reload()
    await expect(sidebar(page).getByText('The CEO is not running.')).toBeVisible()
    await page.waitForTimeout(500)
    expect(await liveCeos(omo)).toHaveLength(0)

    await sidebar(page).getByRole('button', { name: 'Start the CEO' }).click()
    await expect(terminalIn(sidebar(page))).toBeVisible({ timeout: 30_000 })
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
