// Copying from and pasting into a terminal, and images dropped onto it.
// A program in the terminal hands its selection over with OSC 52 (as Claude
// Code, Codex and tmux do), which lands in the system clipboard; Ctrl+C with
// a selection of the terminal copies it and does not interrupt the program;
// Ctrl+V pastes. A dropped image is stored in omo's images folder and its
// path is pasted into the program; any other file is refused. Chromium only:
// the clipboard is read back with the clipboard permissions granted. With
// OMO_SCREENSHOTS=<folder>: terminal-drop-dark-1440.png.
import { mkdirSync, readdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import type { Locator, Page } from '@playwright/test'
import { expect, test, type Omo } from './support/omo'
import { expectNoAgentProcesses, script, waitForSession } from './support/agents'
import { observe, openBoard } from './support/board'
import { png } from './support/images'
import { expectTerminalText, makeWritable, terminalIn, typeInTerminal } from './support/terminal'

const SHOTS = process.env.OMO_SCREENSHOTS ?? ''

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

test.describe('images dropped onto an agent terminal', () => {
  test.use({
    omoOptions: { agents: { developer: script('ready', 'print|hello from fake', 'await-line|/images/terminal/|10m', 'print|got the image', 'hang') } },
  })

  /** Drops files built in the page onto the terminal, as a file manager would. */
  async function drop(terminal: Locator, files: { name: string; type: string; bytes: number[] }[], stop?: 'over') {
    await terminal.evaluate(
      (el, { files, stop }) => {
        const dt = new DataTransfer()
        for (const f of files) dt.items.add(new File([new Uint8Array(f.bytes)], f.name, { type: f.type }))
        const fire = (type: string) => el.dispatchEvent(new DragEvent(type, { bubbles: true, cancelable: true, dataTransfer: dt }))
        fire('dragenter')
        fire('dragover')
        if (stop !== 'over') fire('drop')
      },
      { files, stop },
    )
  }

  test('a dropped image is stored in the images folder and its path reaches the program; a text file is refused', async ({ page, omo }) => {
    const seen = observe(page)
    const n = await omo.createTicket({ title: 'Look at this screenshot' })
    await omo.moveTicket(n, 'ready')
    const session = await waitForSession(omo, n, 'developer')
    await openBoard(page, omo)
    await page.goto(omo.url(`/${n}?tab=agents`))
    const panel = page.locator('dialog[data-ticket-panel]')
    await panel.locator(`[data-session="${session.id}"] [data-agent-row]`).click()
    const terminal = terminalIn(panel)
    await expectTerminalText(terminal, 'hello from fake')
    // Dropped files go into an agent terminal only once it is writable (the draft box takes them before).
    await makeWritable(panel)
    const folder = join(omo.home, 'omo', 'images', 'terminal')
    // The stored images. omo writes a file under a temporary name (`.omo-write-…`)
    // and renames it when it is complete; a file still being written is not one.
    const stored = () => {
      try {
        return readdirSync(folder).filter((name) => !name.startsWith('.omo-write-'))
      } catch {
        return []
      }
    }

    const image = { name: 'screen shot.png', type: 'image/png', bytes: [...png()] }
    if (SHOTS !== '') {
      await terminal.scrollIntoViewIfNeeded()
      await drop(terminal, [image], 'over')
      await expect(panel.locator('[data-terminal-note]')).toHaveText('Drop images to paste their paths')
      mkdirSync(SHOTS, { recursive: true })
      await page.emulateMedia({ colorScheme: 'dark' })
      await page.evaluate('document.fonts.ready')
      await page.screenshot({ path: join(SHOTS, 'terminal-drop-dark-1440.png') })
      await terminal.evaluate((el) => el.dispatchEvent(new DragEvent('dragleave', { bubbles: true, dataTransfer: new DataTransfer() })))
    }

    await drop(terminal, [{ name: 'notes.txt', type: 'text/plain', bytes: [...Buffer.from('plain text')] }])
    await expect(panel.locator('[data-terminal-note]')).toHaveText('Only images can be dropped here.')
    expect(stored()).toEqual([])

    await drop(terminal, [image])
    await expect.poll(stored).toHaveLength(1)
    const file = join(folder, stored()[0]!)
    expect(file).toMatch(/\/images\/terminal\/[0-9a-f-]{36}\.png$/)
    // The path went to the program without a line break: only Enter sends the line it waits for.
    await terminal.locator('.xterm-helper-textarea').focus()
    await page.keyboard.press('Enter')
    await expectTerminalText(terminal, 'got the image')

    expect(seen.pageErrors).toEqual([])
    await omo.api('POST', '/api/agents/stop-all', {})
    await expectNoAgentProcesses(omo)
  })
})
