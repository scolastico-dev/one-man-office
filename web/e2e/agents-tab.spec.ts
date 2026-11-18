import type { Page } from '@playwright/test'
import { expect, test } from './support/omo'
import { expectNoAgentProcesses, script, waitForSession } from './support/agents'
import { observe } from './support/board'
import { expectNoTokenInUrls, expectTerminalText, terminalIn, typeInTerminal } from './support/terminal'

test.use({ viewport: { width: 1440, height: 900 } })

const panel = (page: Page) => page.locator('dialog[data-ticket-panel]')

test.describe('a developer that waits for a line, answers it and exits', () => {
  // Keep the reply in a live terminal until the test explicitly lets the agent exit.
  test.use({
    omoOptions: { agents: { developer: script('ready', 'print|hello from fake', 'await-line|ping|10m', 'print|got it', 'await-line|finish|10m', 'exit|0') } },
  })

  test("the running developer's terminal shows its output and takes typed input", async ({ page, omo }) => {
    const seen = observe(page)
    const n = await omo.createTicket({ title: 'Talk to the developer' })
    await omo.moveTicket(n, 'ready')
    const session = await waitForSession(omo, n, 'developer')
    await page.goto(omo.accessUrl)
    await expect(page.locator('[data-board]')).toBeVisible()
    await page.goto(omo.url(`/${n}?tab=agents`))
    await expect(panel(page)).toBeVisible()
    const row = panel(page).locator(`[data-session="${session.id}"] [data-agent-row]`)
    await expect(row).toContainText('developer')
    await row.click()

    await expect(panel(page).getByText('Typing here types into the agent.')).toBeVisible()
    const term = terminalIn(panel(page))
    await expectTerminalText(term, 'hello from fake')
    await typeInTerminal(page, term, 'ping')
    await expectTerminalText(term, 'got it')
    await typeInTerminal(page, term, 'finish')
    await expect(panel(page).locator(`[data-session="${session.id}"] [data-ended]`)).toBeVisible()

    expectNoTokenInUrls(seen, omo.token)
    expect(seen.pageErrors).toEqual([])
    await expectNoAgentProcesses(omo)
  })

  test('after the agent exits its log is shown', async ({ page, omo }) => {
    const seen = observe(page)
    const n = await omo.createTicket({ title: 'Read the log' })
    await omo.moveTicket(n, 'ready')
    const session = await waitForSession(omo, n, 'developer')
    await page.goto(omo.accessUrl)
    await expect(page.locator('[data-board]')).toBeVisible()
    await page.goto(omo.url(`/${n}?tab=agents`))
    await expect(panel(page)).toBeVisible()
    await panel(page).locator(`[data-session="${session.id}"] [data-agent-row]`).click()
    const term = terminalIn(panel(page))
    await expectTerminalText(term, 'hello from fake')
    await typeInTerminal(page, term, 'ping')
    await typeInTerminal(page, term, 'finish')

    const item = panel(page).locator(`[data-session="${session.id}"]`)
    await expect(item.locator('[data-ended]')).toBeVisible({ timeout: 30_000 })
    // The open terminal gives way to the saved log of the session.
    await expect(item.locator('[data-log]')).toContainText('hello from fake')
    await expect(item.locator('[data-log]')).toContainText('got it')
    await expect(panel(page).locator('.xterm')).toHaveCount(0)
    await item.getByRole('button', { name: 'Hide log' }).click()
    await expect(item.locator('[data-log]')).toHaveCount(0)
    await item.getByRole('button', { name: 'Show log' }).click()
    await expect(item.locator('[data-log]')).toContainText('got it')

    expectNoTokenInUrls(seen, omo.token)
    await expectNoAgentProcesses(omo)
  })
})

test('a ticket without agents says so', async ({ page, omo }) => {
  const n = await omo.createTicket({ title: 'Nobody here' })
  await page.goto(omo.accessUrl)
  await page.goto(omo.url(`/${n}?tab=agents`))
  await expect(panel(page).locator('[data-agents-empty]')).toHaveText(`No agent has worked on #${n} yet.`)
})

test("a story's Tasks tab lists its tasks with their columns", async ({ page, omo }) => {
  const story = await omo.createTicket({ title: 'Big story', type: 'story' })
  const a = await omo.createTicket({ title: 'First task', story })
  const b = await omo.createTicket({ title: 'Second task', story })
  await omo.moveTicket(b, 'ready')
  await page.goto(omo.accessUrl)
  await page.goto(omo.url(`/${story}?tab=tasks`))
  await expect(panel(page)).toBeVisible()
  const rows = panel(page).locator('[data-task]')
  await expect(rows).toHaveCount(2)
  await expect(panel(page).locator(`[data-task="${a}"]`)).toContainText('First task')
  await expect(panel(page).locator(`[data-task="${a}"] [data-task-column]`)).toHaveText('Planning')
  await expect(panel(page).locator(`[data-task="${b}"] [data-task-column]`)).toHaveText('Ready For Dev')
  await expect(panel(page).locator(`[data-task="${a}"] a`)).toHaveAttribute('href', `/ticket/${a}`)
  await panel(page).locator('[data-add-task]').click()
  await expect(page.getByRole('dialog', { name: /create/i }).first()).toBeVisible()
})
