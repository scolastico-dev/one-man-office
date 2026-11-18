// Keys of the terminal that a CLI reads differently from what xterm.js sends,
// and the global screen reader setting (Settings, General). With
// OMO_SCREENSHOTS=<folder>: sidebar-title-*.png, screen-reader-setting-*.png.
import { writeFileSync } from 'node:fs'
import { join } from 'node:path'
import type { Locator, Page } from '@playwright/test'
import { expect, test, type Omo } from './support/omo'
import { openBoard } from './support/board'
import { expectTerminalText, terminalIn, typeInTerminal } from './support/terminal'

const SHOTS = process.env.OMO_SCREENSHOTS ?? ''
const seed = { seed: { projects: [{ path: '{{repo}}', name: 'demo' }] } }
const sidebar = (page: Page) => page.locator('[data-sidebar]')

async function closeShells(omo: Omo): Promise<void> {
  for (const s of await omo.api<{ id: string }[]>('GET', '/api/terminals')) await omo.api('DELETE', `/api/terminals/${encodeURIComponent(s.id)}`)
}

async function openShell(page: Page, root: Locator): Promise<Locator> {
  await root.getByRole('tab', { name: /^Terminals/ }).click()
  await root.getByRole('button', { name: 'New terminal' }).click()
  await root.getByRole('combobox', { name: 'Search projects' }).fill('demo')
  await root.getByRole('option', { name: /^demo / }).click()
  const terminal = terminalIn(root)
  await typeInTerminal(page, terminal, 'echo ready=$((20 + 1))')
  await expectTerminalText(terminal, 'ready=21')
  return terminal
}

test.describe('keys', () => {
  test.use({ viewport: { width: 1440, height: 900 }, omoOptions: seed })

  test('Shift+Enter and Ctrl+Enter reach the pty as ESC CR, Enter as CR, Alt+Enter as ESC CR', async ({ page, omo }) => {
    writeFileSync(join(omo.home, '.zshrc'), '')
    try {
      await openBoard(page, omo)
      const terminal = await openShell(page, sidebar(page))
      // Raw input without echo; cat -v shows what arrives: ^M is CR, ^[ is ESC.
      await typeInTerminal(page, terminal, 'stty -icrnl -icanon -echo; echo mode=$((1 + 1)); cat -v')
      await expectTerminalText(terminal, 'mode=2')
      const press = async (key: string, shown: string) => {
        await page.keyboard.press(key)
        await expect(terminal.locator('.xterm-rows')).toContainText(shown)
      }
      await press('Enter', '^M')
      await press('Shift+Enter', '^M^[^M')
      await press('Control+Enter', '^M^[^M^[^M')
      await press('Alt+Enter', '^M^[^M^[^M^[^M')
      // No plain CR follows any of the line breaks: three ESC CR and one CR in all.
      const text = (await terminal.locator('.xterm-rows').innerText()).replace(/\s+/g, '')
      expect(text.match(/\^M/g)).toHaveLength(4)
      expect(text.match(/\^\[/g)).toHaveLength(3)
    } finally {
      await closeShells(omo)
    }
  })
})

test.describe('screen reader setting', () => {
  test.use({ viewport: { width: 1440, height: 900 }, omoOptions: seed })

  test('the toolbar has no toggle; the setting switches xterm accessibility tree on in an open terminal', async ({ page, omo }) => {
    writeFileSync(join(omo.home, '.zshrc'), '')
    try {
      await openBoard(page, omo)
      const terminal = await openShell(page, sidebar(page))
      await expect(sidebar(page).getByRole('switch', { name: 'Screen reader mode' })).toHaveCount(0)
      await expect(terminal.locator('.xterm-accessibility')).toHaveCount(0)
      const read = page.waitForResponse((r) => r.request().method() === 'GET' && new URL(r.url()).pathname === '/api/settings')
      await omo.api('PATCH', '/api/settings', { ui: { screen_reader_mode: true } })
      await read
      await expect(terminal.locator('.xterm-accessibility')).toHaveCount(1)
      await omo.api('PATCH', '/api/settings', { ui: { screen_reader_mode: false } })
      await expect(terminal.locator('.xterm-accessibility')).toHaveCount(0)
    } finally {
      await closeShells(omo)
    }
  })

  for (const scheme of ['dark', 'light'] as const) {
    test(`the General page switches it (${scheme})`, async ({ page, omo }) => {
      await page.emulateMedia({ colorScheme: scheme })
      await openBoard(page, omo)
      await page.goto(omo.url('/settings/general'))
      const sw = page.getByRole('switch', { name: 'Screen reader mode for terminals' })
      await expect(sw).toHaveAttribute('aria-checked', 'false')
      await sw.click()
      // Hold the write before the server handles it: the edited switch
      // already reads true, even though no new value has been stored yet.
      let save: (() => Promise<void>) | undefined
      await page.route('**/api/settings', (route) => {
        if (route.request().method() === 'PATCH') save = () => route.continue()
        else return route.continue()
      })
      await page.getByRole('button', { name: /^Save/ }).click()
      await expect.poll(() => save !== undefined).toBe(true)
      await expect(page.locator('[data-save-state]')).toHaveText('Saving…')
      expect((await omo.api<{ settings: { ui: { screen_reader_mode: boolean } } }>('GET', '/api/settings')).settings.ui.screen_reader_mode).toBe(false)
      await save!()
      await expect(page.locator('[data-save-state]')).toHaveText('Saved')
      await expect(sw).toHaveAttribute('aria-checked', 'true')
      expect((await omo.api<{ settings: { ui: { screen_reader_mode: boolean } } }>('GET', '/api/settings')).settings.ui.screen_reader_mode).toBe(true)
      if (SHOTS) await page.getByText('Accessibility').first().scrollIntoViewIfNeeded().then(() => page.screenshot({ path: join(SHOTS, `screen-reader-setting-${scheme}.png`) }))
    })
  }
})

test.describe('mobile sidebar title', () => {
  test.use({ viewport: { width: 390, height: 800 }, omoOptions: seed })

  for (const scheme of ['dark', 'light'] as const) {
    test(`the sheet is titled ${scheme}`, async ({ page, omo }) => {
      await page.emulateMedia({ colorScheme: scheme })
      await openBoard(page, omo)
      await page.getByRole('button', { name: 'Sidebar' }).click()
      const sheet = page.getByRole('dialog', { name: 'Agents and terminals' })
      await expect(sheet.getByRole('heading', { name: 'Agents and terminals' })).toBeVisible()
      if (SHOTS) await page.screenshot({ path: join(SHOTS, `sidebar-title-${scheme}.png`) })
    })
  }
})
