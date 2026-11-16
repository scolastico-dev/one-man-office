// Copying from and pasting into a terminal.
// A program in the terminal hands its selection over with OSC 52 (as Claude
// Code, Codex and tmux do), which lands in the system clipboard; Ctrl+C with
// a selection of the terminal copies it and does not interrupt the program;
// Ctrl+V pastes. Chromium only: the clipboard is read back with the
// clipboard permissions granted.
import { writeFileSync } from 'node:fs'
import { join } from 'node:path'
import type { Locator, Page } from '@playwright/test'
import { expect, test, type Omo } from './support/omo'
import { openBoard } from './support/board'
import { expectTerminalText, terminalIn, typeInTerminal } from './support/terminal'

test.use({ viewport: { width: 1440, height: 900 }, permissions: ['clipboard-read', 'clipboard-write'] })

const sidebar = (page: Page) => page.locator('[data-sidebar]')
const clipboard = (page: Page) => page.evaluate(() => navigator.clipboard.readText())

async function closeShells(omo: Omo): Promise<void> {
  for (const s of await omo.api<{ id: string }[]>('GET', '/api/terminals')) await omo.api('DELETE', `/api/terminals/${encodeURIComponent(s.id)}`)
}

async function openShell(page: Page, omo: Omo): Promise<Locator> {
  // An empty startup file keeps zsh from asking its setup question.
  writeFileSync(join(omo.home, '.zshrc'), '')
  await openBoard(page, omo)
  await sidebar(page).getByRole('tab', { name: /^Terminals/ }).click()
  await sidebar(page).getByRole('button', { name: 'New terminal' }).click()
  await sidebar(page).getByRole('combobox', { name: 'Search projects' }).fill('demo')
  await sidebar(page).getByRole('option', { name: /^demo / }).click()
  const terminal = terminalIn(sidebar(page))
  await typeInTerminal(page, terminal, 'echo ready=$((20 + 1))')
  await expectTerminalText(terminal, 'ready=21')
  return terminal
}

test('a program that prints OSC 52 puts its text into the system clipboard', async ({ page, omo }) => {
  try {
    const terminal = await openShell(page, omo)
    await page.evaluate(() => navigator.clipboard.writeText('before'))
    await typeInTerminal(page, terminal, `printf '\\033]52;c;%s\\007' "$(printf 'from the program – ü' | base64)"`)
    await expect.poll(() => clipboard(page)).toBe('from the program – ü')
    await expect(sidebar(page).locator('[data-terminal-note]')).toHaveText('Copied')
  } finally {
    await closeShells(omo)
  }
})

test('Ctrl+C with a selection copies it and leaves the program running; the next Ctrl+C interrupts; Ctrl+V pastes', async ({ page, omo }) => {
  try {
    const terminal = await openShell(page, omo)
    await typeInTerminal(page, terminal, 'echo copyme-word')
    await typeInTerminal(page, terminal, 'cat')
    // The output line of echo: a double click selects the word.
    const row = terminal.locator('.xterm-rows > div', { hasText: /^copyme-word\s*$/ })
    const box = (await row.boundingBox())!
    // The emulator's screen above the rows takes the pointer.
    await page.mouse.dblclick(box.x + 20, box.y + box.height / 2)
    await page.keyboard.press('Control+C')
    await expect.poll(() => clipboard(page)).toBe('copyme-word')
    // cat still runs: what is typed comes back twice (the echo of the terminal, then cat).
    await typeInTerminal(page, terminal, 'alive')
    await expect(terminal.locator('.xterm-rows > div', { hasText: /^alive\s*$/ })).toHaveCount(2)
    // No selection now: Ctrl+C is the interrupt again.
    await page.keyboard.press('Control+C')
    await typeInTerminal(page, terminal, 'echo after=$((2 + 3))')
    await expectTerminalText(terminal, 'after=5')

    await page.evaluate(() => navigator.clipboard.writeText('echo pasted=$((40 + 2))'))
    await terminal.locator('.xterm-helper-textarea').focus()
    await page.keyboard.press('Control+V')
    await page.keyboard.press('Enter')
    await expectTerminalText(terminal, 'pasted=42')
  } finally {
    await closeShells(omo)
  }
})
