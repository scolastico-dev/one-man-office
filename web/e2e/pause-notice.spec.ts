import type { Page } from '@playwright/test'
import { expect, pluginListPath, test } from './support/omo'
import { expectNoAgentProcesses, script } from './support/agents'
import { openBoard } from './support/board'
import { makePluginList } from './support/plugin'
import { expectTerminalText, terminalIn } from './support/terminal'

// The official pause-notice plugin, installed from a stand-in of omo's plugin
// list (this checkout's plugins folder): switching the pause on types the halt
// text into the running CEO's terminal, switching it off the resume text.

test.use({ viewport: { width: 1440, height: 900 }, omoOptions: { agents: { ceo: script('ready', 'print|CEO ready', 'hang') } } })

const sidebar = (page: Page) => page.locator('[data-sidebar]')
const pauseSwitch = (page: Page) => page.getByRole('switch', { name: 'Paused' })

test('the pause-notice plugin tells the running CEO to halt and to continue', async ({ page, omo }) => {
  makePluginList(pluginListPath(omo.home))
  await openBoard(page, omo)
  await page.goto(omo.url('/settings/plugins'))
  const listed = page.locator('[data-catalog-plugin="pause-notice"]')
  await expect(listed.locator('[data-catalog-tag]')).toHaveText('official')
  await listed.getByRole('button', { name: 'Install pause-notice' }).click()
  await expect(page.locator('[data-plugin="pause-notice"] [data-plugin-state]')).toHaveText('ready')

  await page.goto(omo.url('/'))
  await sidebar(page).getByRole('tab', { name: /^CEO/ }).click()
  const terminal = terminalIn(sidebar(page))
  await expect(terminal).toBeVisible({ timeout: 30_000 })
  await expectTerminalText(terminal, 'CEO ready')

  await expect(pauseSwitch(page)).toHaveAttribute('aria-checked', 'false')
  await pauseSwitch(page).click()
  await expect(pauseSwitch(page)).toHaveAttribute('aria-checked', 'true')
  await expectTerminalText(terminal, 'omo: the user paused omo.')
  await expect(terminal.locator('.xterm-rows')).not.toContainText('omo: the pause is over')

  await pauseSwitch(page).click()
  await expect(pauseSwitch(page)).toHaveAttribute('aria-checked', 'false')
  await expectTerminalText(terminal, 'omo: the pause is over.')

  const log = async () => (await omo.api<{ message: string }[]>('GET', '/api/plugins/pause-notice/log')).map((l) => l.message)
  await expect.poll(log).toEqual(expect.arrayContaining(['pause-notice: told 1 agents to halt', 'pause-notice: told 1 agents to continue']))
  await expectNoAgentProcesses(omo)
})
