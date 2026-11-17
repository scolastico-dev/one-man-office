// A task the CEO creates waits in Planning for the user's approval: the
// CEO's own move to Ready For Dev is refused with the reason, no agent
// starts, and the user's approval through the card's Move menu starts the
// work. With OMO_SCREENSHOTS=<folder> it writes fix8-ceo-approval-<theme>.png
// there.
import { mkdirSync } from 'node:fs'
import { join } from 'node:path'
import type { Page } from '@playwright/test'
import { expectNoAgentProcesses, liveAgents, script, waitForSession } from './support/agents'
import { card, expectCardIn, openBoard } from './support/board'
import { expect, test } from './support/omo'
import { expectTerminalText, terminalIn } from './support/terminal'

test.use({ viewport: { width: 1440, height: 900 } })

const SHOTS = process.env.OMO_SCREENSHOTS ?? ''

async function shoot(page: Page) {
  if (SHOTS === '') return
  mkdirSync(SHOTS, { recursive: true })
  for (const theme of ['dark', 'light'] as const) {
    await page.emulateMedia({ colorScheme: theme })
    await page.evaluate('document.fonts.ready')
    await page.screenshot({ path: join(SHOTS, `fix8-ceo-approval-${theme}.png`) })
  }
  await page.emulateMedia({ colorScheme: null })
}

const sidebar = (page: Page) => page.locator('[data-sidebar]')

test.use({
  omoOptions: {
    agents: {
      // The project the fixture adds is "demo".
      ceo: script('ready', 'create-ticket|task|demo|Add a health check', 'release-created', 'hang'),
      developer: script('ready', 'print|WORKING', 'hang'),
    },
  },
})

test("a task the CEO creates waits for the user's approval, and the approval starts the work", async ({ page, omo }) => {
  expect(omo.project).toBe('demo')
  await openBoard(page, omo)
  await sidebar(page).getByRole('tab', { name: /^CEO/ }).click()
  const terminal = terminalIn(sidebar(page))
  await expect(terminal).toBeVisible({ timeout: 30_000 })
  await expectTerminalText(terminal, 'fake: created #1 Add a health check')
  // The CEO's own move to ready is refused, and the refusal says the user approves.
  await expectTerminalText(terminal, 'fake: #1 not released:')
  await expectTerminalText(terminal, 'the user approves the tickets the CEO creates')

  await expectCardIn(page, 1, 'Planning')
  const ticket = await omo.api<{ column: string }>('GET', '/api/tickets/1')
  expect(ticket.column).toBe('planning')
  expect((await liveAgents(omo)).filter((s) => s.ticket === 1)).toEqual([])
  await shoot(page)

  // The user approves it as any story: Move…, Ready For Dev.
  await card(page, 1).getByRole('button', { name: 'Move #1…' }).click()
  const menu = page.getByRole('menu')
  await expect(menu.getByRole('menuitem', { name: 'Ready For Dev' })).toHaveAccessibleDescription('Approves.')
  await menu.getByRole('menuitem', { name: 'Ready For Dev' }).click()

  await waitForSession(omo, 1, 'developer')
  await expectCardIn(page, 1, 'In Progress')
  await expectNoAgentProcesses(omo)
})
