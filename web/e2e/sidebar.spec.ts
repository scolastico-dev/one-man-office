import { expect, test } from './support/omo'
import { card, column, expectCardIn, observe, openBoard } from './support/board'
import { expectNoAgentProcesses, script, waitForSession } from './support/agents'
import { expectNoTokenInUrls, expectTerminalText, terminalIn } from './support/terminal'

test.use({ viewport: { width: 1440, height: 900 } })

const sidebar = (page: import('@playwright/test').Page) => page.locator('[data-sidebar]')
const width = (page: import('@playwright/test').Page) => page.getByRole('separator', { name: 'Sidebar width' })

test('the sidebar collapses, expands, resizes by keyboard and remembers it after a reload', async ({ page, omo }) => {
  await openBoard(page, omo)
  await expect(sidebar(page)).toHaveAttribute('data-open', 'false')
  await expect(sidebar(page).getByRole('tablist', { name: 'Sidebar' })).toBeVisible()
  await expect(sidebar(page).getByRole('tab')).toHaveText([/^CEO/, /^Agents/, /^Terminals/])

  // The keyboard opens it and the focus goes into its tabs.
  await sidebar(page).getByRole('tab', { name: /^Agents/ }).focus()
  await page.keyboard.press('Enter')
  await expect(sidebar(page)).toHaveAttribute('data-open', 'true')
  await expect(sidebar(page).getByRole('tab', { name: /^Agents/ })).toBeFocused()
  await expect(sidebar(page).getByRole('tab', { name: /^Agents/ })).toHaveAttribute('aria-selected', 'true')
  await expect(sidebar(page).locator('[data-slots]')).toContainText('Slots: 0 of 4 in use')

  // The board keeps room next to it.
  const open = await sidebar(page).boundingBox()
  expect(open!.width).toBeCloseTo(480, -1)
  await expect(column(page, 'Planning')).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true)

  // Arrow keys resize in steps; the width stays within its limits.
  await width(page).focus()
  const before = Number(await width(page).getAttribute('aria-valuenow'))
  await page.keyboard.press('ArrowRight')
  await page.keyboard.press('ArrowRight')
  await expect(width(page)).toHaveAttribute('aria-valuenow', String(before + 32))
  await page.keyboard.press('Home')
  await expect(width(page)).toHaveAttribute('aria-valuenow', '320')
  await page.keyboard.press('End')
  await expect(width(page)).toHaveAttribute('aria-valuenow', String(Math.floor(1440 * 0.6)))
  await page.keyboard.press('Home')
  await page.keyboard.press('ArrowRight')
  await expect(width(page)).toHaveAttribute('aria-valuenow', '336')

  // Another tab and a reload: open, the width and the tab come back.
  await sidebar(page).getByRole('tab', { name: /^Terminals/ }).click()
  await page.reload()
  await expect(page.locator('[data-connection]')).toHaveAttribute('data-state', 'live')
  await expect(sidebar(page)).toHaveAttribute('data-open', 'true')
  await expect(width(page)).toHaveAttribute('aria-valuenow', '336')
  await expect(sidebar(page).getByRole('tab', { name: /^Terminals/ })).toHaveAttribute('aria-selected', 'true')

  // Collapsing brings the focus to the button, and that is remembered too.
  await sidebar(page).getByRole('button', { name: 'Collapse sidebar' }).focus()
  await page.keyboard.press('Enter')
  await expect(sidebar(page)).toHaveAttribute('data-open', 'false')
  await expect(sidebar(page).getByRole('button', { name: 'Expand sidebar' })).toBeFocused()
  await page.reload()
  await expect(page.locator('[data-connection]')).toHaveAttribute('data-state', 'live')
  await expect(sidebar(page)).toHaveAttribute('data-open', 'false')
})

test('the page renders and works when the browser refuses local storage', async ({ page, omo }) => {
  await page.addInitScript(() => {
    const refuse = () => {
      throw new DOMException('blocked', 'SecurityError')
    }
    Object.defineProperty(window, 'localStorage', { get: refuse })
  })
  await page.goto(omo.accessUrl)
  await expect(page.locator('[data-board]')).toBeVisible()
  await page.getByRole('tab', { name: /^Agents/ }).click()
  await expect(sidebar(page)).toHaveAttribute('data-open', 'true')
  await expect(sidebar(page).locator('[data-slots]')).toBeVisible()
})

test.describe('a running developer', () => {
  test.use({ omoOptions: { agents: { developer: script('ready', 'print|working', 'hang') } } })

  test('the Agents tab lists the running developer with its ticket; clicking shows its terminal; it leaves when it ends', async ({ page, omo }) => {
    const seen = observe(page)
    await openBoard(page, omo)
    await sidebar(page).getByRole('tab', { name: /^Agents/ }).click()
    await expect(sidebar(page)).toHaveAttribute('data-open', 'true')
    const n = await omo.createTicket({ title: 'Say hello <b>twice</b>' })
    await omo.moveTicket(n, 'ready')
    const session = await waitForSession(omo, n, 'developer')
    await expectCardIn(page, n, 'In Progress')

    const row = sidebar(page).locator(`[data-session="${session.id}"]`)
    await expect(row).toBeVisible()
    await expect(row).toContainText('developer')
    await expect(row).toContainText(session.name)
    await expect(row).toContainText(`#${n} Say hello <b>twice</b>`)
    await expect(row).toContainText('fake/default')
    await expect(sidebar(page).getByRole('tab', { name: /^Agents/ })).toHaveText('Agents 1')
    await expect(sidebar(page).locator('[data-slots]')).toContainText('1 of 4 in use')
    // The card of the board agrees on the state.
    await expect(card(page, n).locator('[data-agent]')).toContainText('developer')

    // The row is a button; it shows the agent with a way back and a link to the ticket.
    await row.click()
    await expect(sidebar(page).getByRole('button', { name: 'Back' })).toBeFocused()
    await expect(sidebar(page).getByText('Typing here types into the agent.')).toBeVisible()
    // The terminal is the agent's own: what it printed shows.
    await expectTerminalText(terminalIn(sidebar(page)), 'working')
    const link = sidebar(page).getByRole('link', { name: `Open #${n}` })
    await expect(link).toHaveAttribute('href', `/${n}`)
    // The ticket's panel covers the sidebar: its terminal goes, so the two
    // views never fight over the size; it comes back with the panel closed.
    await link.click()
    await expect(page.locator('dialog[data-ticket-panel]')).toBeVisible()
    await expect(sidebar(page).locator('.xterm')).toHaveCount(0)
    await expect(sidebar(page).locator('[data-terminal-covered]')).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(page.locator('dialog[data-ticket-panel]')).toHaveCount(0)
    await expectTerminalText(terminalIn(sidebar(page)), 'working')
    await sidebar(page).getByRole('button', { name: 'Back' }).click()
    await expect(row).toBeFocused()

    // Open it again: when the agent ends, the detail says so.
    await row.click()
    await omo.api('POST', '/api/agents/stop-all', {})
    await expect(sidebar(page).getByText('This agent ended.').first()).toBeVisible({ timeout: 30_000 })
    await sidebar(page).getByRole('button', { name: 'Back' }).click()
    await expect(sidebar(page).locator('[data-agent-row]')).toHaveCount(0)
    await expect(sidebar(page).getByRole('tab', { name: /^Agents/ })).toHaveText('Agents 0')
    await expect(sidebar(page).getByText('No agent is running.')).toBeVisible()
    expectNoTokenInUrls(seen, omo.token)
    await expectNoAgentProcesses(omo)
  })

  test('an agent that ends leaves the list without the list being open on it', async ({ page, omo }) => {
    await openBoard(page, omo)
    await sidebar(page).getByRole('tab', { name: /^Agents/ }).click()
    const n = await omo.createTicket({ title: 'Short job' })
    await omo.moveTicket(n, 'ready')
    await waitForSession(omo, n, 'developer')
    await expect(sidebar(page).locator('[data-agent-row]')).toHaveCount(1)
    await omo.api('POST', '/api/agents/stop-all', {})
    await expect(sidebar(page).locator('[data-agent-row]')).toHaveCount(0, { timeout: 30_000 })
    await expect(sidebar(page).getByRole('tab', { name: /^Agents/ })).toHaveText('Agents 0')
    await expectNoAgentProcesses(omo)
  })
})
