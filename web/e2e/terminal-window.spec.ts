import { writeFileSync } from 'node:fs'
import { join } from 'node:path'
import AxeBuilder from '@axe-core/playwright'
import type { Locator, Page, WebSocket } from '@playwright/test'
import { expect, test, type Omo } from './support/omo'
import { expectNoAgentProcesses, script, waitForSession, type AgentSession } from './support/agents'
import { observe, openBoard } from './support/board'
import { expectNoTokenInUrls, expectTerminalText, terminalIn, typeInTerminal } from './support/terminal'

// A terminal popped out into a window of its own: the window shows it alone,
// the main window shows a placeholder and holds no connection to it, and the
// two find each other again after closing, reloading and locking.

test.use({ viewport: { width: 1440, height: 900 } })

const sidebar = (page: Page) => page.locator('[data-sidebar]')
const placeholder = (root: Locator) => root.locator('[data-terminal-out]')

/** Counts the open websockets of a page by a part of their address, across reloads. */
function sockets(page: Page) {
  const all: WebSocket[] = []
  page.on('websocket', (ws) => all.push(ws))
  return (part: string) => all.filter((ws) => !ws.isClosed() && ws.url().includes(part)).length
}

const agentSocket = (s: AgentSession) => `/api/agents/${encodeURIComponent(s.terminal_id!)}/terminal`

/** The test's home is empty; zsh would ask a setup question that swallows what is typed. */
test.beforeEach(({ omo }) => {
  writeFileSync(join(omo.home, '.zshrc'), '')
})

const shells = (omo: Omo) => omo.api<{ id: string }[]>('GET', '/api/terminals')
async function closeShells(omo: Omo): Promise<void> {
  for (const s of await shells(omo)) await omo.api('DELETE', `/api/terminals/${encodeURIComponent(s.id)}`)
}

/** Opens a shell in the home folder through the Terminals tab and waits for its prompt to take input. */
async function openHomeShell(page: Page): Promise<void> {
  await sidebar(page).getByRole('tab', { name: /^Terminals/ }).click()
  await sidebar(page).getByRole('button', { name: 'New terminal' }).click()
  await sidebar(page).getByRole('option', { name: /^Home folder/ }).click()
  const term = terminalIn(sidebar(page))
  await expect(term).toBeVisible()
  await typeInTerminal(page, term, 'echo ready=$((20 + 1))')
  await expectTerminalText(term, 'ready=21')
}

/** Opens the Agents tab on the agent and waits for its first output. */
async function showAgent(page: Page, s: AgentSession, text: string): Promise<void> {
  await sidebar(page).getByRole('tab', { name: /^Agents/ }).click()
  await sidebar(page).locator(`[data-session="${s.id}"]`).click()
  await expectTerminalText(terminalIn(sidebar(page)), text)
}

/** Presses the pop-out button named `name` and returns the window it opened, loaded. */
/**
 * Presses a button of a window that closes that very window. The window can be
 * gone before the click has finished, which is the click having worked.
 */
async function clickToClose(win: Page, name: string): Promise<void> {
  const closed = win.waitForEvent('close')
  await win
    .getByRole('button', { name })
    .click()
    .catch((err: unknown) => {
      if (!win.isClosed()) throw err
    })
  await closed
}

async function popOut(page: Page, name: string): Promise<Page> {
  const opened = page.waitForEvent('popup')
  await page.getByRole('button', { name }).click()
  const popup = await opened
  await popup.waitForLoadState()
  return popup
}

/** The terminal in the window fills the window: its rows take the screen's height, less than one row left over, and nothing scrolls. */
async function expectFitsWindow(popup: Page): Promise<void> {
  await expect
    .poll(async () =>
      popup.evaluate(() => {
        const screen = document.querySelector('.tv__screen')!.getBoundingClientRect()
        const rows = document.querySelector('.xterm-screen')!.getBoundingClientRect()
        const row = document.querySelector('.xterm-rows > div')!.getBoundingClientRect().height
        const main = document.querySelector('main')!.getBoundingClientRect()
        return {
          rowsFit: rows.height <= screen.height && screen.height - rows.height < row + 40,
          widthFit: rows.width <= screen.width,
          mainFits: main.bottom <= window.innerHeight + 1 && document.documentElement.scrollHeight <= window.innerHeight + 1,
        }
      }),
    )
    .toEqual({ rowsFit: true, widthFit: true, mainFits: true })
}

/** The rows and columns that fit the terminal's box in the window, from the emulator's cell size. */
function fittingSize(popup: Page): Promise<{ rows: number; maxCols: number }> {
  return popup.evaluate(() => {
    const host = document.querySelector('.tv__host')!.getBoundingClientRect()
    const cell = document.querySelector('.xterm-rows > div')!.getBoundingClientRect().height
    const measure = document.querySelector('.xterm-char-measure-element')!
    const cellWidth = measure.getBoundingClientRect().width / Math.max(1, (measure.textContent ?? '').length)
    return { rows: Math.floor(host.height / cell), maxCols: Math.floor(host.width / cellWidth) }
  })
}

let asked = 0
/** The size the shell's process sees (`stty size`). */
async function processSize(popup: Page, term: Locator): Promise<{ rows: number; cols: number }> {
  const mark = `sz${++asked}`
  await popup.keyboard.type(`echo ${mark}=$(stty size | tr ' ' x)`)
  await popup.keyboard.press('Enter')
  const re = new RegExp(`${mark}=(\\d+)x(\\d+)`)
  await expectTerminalText(term, re)
  const m = re.exec((await term.locator('.xterm-rows').textContent()) ?? '')!
  return { rows: Number(m[1]), cols: Number(m[2]) }
}

test.describe('a developer waiting for a line', () => {
  test.use({ omoOptions: { agents: { developer: script('ready', 'print|hello from fake', 'await-line|ping|10m', 'print|got it', 'hang') } } })

  test('pops out into its own window that types into the agent; the main window holds no view of it until the window closes', async ({ page, omo, context }) => {
    const seen = observe(context)
    const open = sockets(page)
    await openBoard(page, omo)
    const n = await omo.createTicket({ title: 'Pop me out' })
    await omo.moveTicket(n, 'ready')
    const s = await waitForSession(omo, n, 'developer')
    await showAgent(page, s, 'hello from fake')
    await expect.poll(() => open(agentSocket(s))).toBe(1)

    const popup = await popOut(page, `Open the terminal of developer ${s.name} on #${n} in its own window`)
    expect(new URL(popup.url()).pathname).toBe(`/terminal/agent/${encodeURIComponent(s.terminal_id!)}`)
    await expect(popup).toHaveTitle(`developer ${s.name} on #${n} · omo`)
    await expect(popup.getByRole('heading', { level: 1 })).toHaveText(`developer ${s.name} on #${n}`)
    await expect(popup.locator('.tv__state')).toHaveText('Connected')
    await expect(popup.getByRole('button', { name: 'Return to omo' })).toBeVisible()
    // No pop-out button in the window itself; the focus is in the terminal.
    await expect(popup.locator('[data-terminal-popout]')).toHaveCount(0)
    await expect(popup.locator('.xterm-helper-textarea')).toBeFocused()
    const term = terminalIn(popup.locator('main'))
    await expectTerminalText(term, 'hello from fake')
    await popup.keyboard.type('ping')
    await popup.keyboard.press('Enter')
    await expectTerminalText(term, 'got it')

    // The main window: a placeholder in the terminal's place, no terminal, no socket.
    await expect(placeholder(sidebar(page))).toContainText('This terminal is open in its own window.')
    await expect(sidebar(page).locator('.xterm')).toHaveCount(0)
    await expect.poll(() => open(agentSocket(s))).toBe(0)

    await expectFitsWindow(popup)

    // The window closed by the system (no word from it): the live view comes back.
    await popup.close()
    await expect(placeholder(sidebar(page))).toHaveCount(0)
    await expectTerminalText(terminalIn(sidebar(page)), 'got it')
    await expect.poll(() => open(agentSocket(s))).toBe(1)

    expectNoTokenInUrls(seen, omo.token)
    expect(seen.websocketUrls.some((u) => u.includes(agentSocket(s)))).toBe(true)
    await omo.api('POST', '/api/agents/stop-all', {})
    await expectNoAgentProcesses(omo)
  })

  test('two windows at once, found again after a reload; Show the window keeps it, Bring it back closes it', async ({ page, omo, context }) => {
    const seen = observe(context)
    const open = sockets(page)
    try {
      await openBoard(page, omo)
      const n = await omo.createTicket({ title: 'Two windows' })
      await omo.moveTicket(n, 'ready')
      const s = await waitForSession(omo, n, 'developer')
      await showAgent(page, s, 'hello from fake')
      const agentWindow = await popOut(page, `Open the terminal of developer ${s.name} on #${n} in its own window`)
      await expectTerminalText(terminalIn(agentWindow.locator('main')), 'hello from fake')

      await openHomeShell(page)
      const [shell] = await shells(omo)
      const shellSocket = `/api/terminals/${shell!.id}/socket`
      const shellWindow = await popOut(page, 'Open the terminal Home in its own window')
      await expect(shellWindow).toHaveTitle(/^Home — .+ · omo$/)
      await expect(shellWindow.locator('.xterm-helper-textarea')).toBeFocused()
      await shellWindow.keyboard.type('echo popped=$((3 * 3))')
      await shellWindow.keyboard.press('Enter')
      await expectTerminalText(terminalIn(shellWindow.locator('main')), 'popped=9')
      await expect(placeholder(sidebar(page))).toBeVisible()
      await expect.poll(() => open(shellSocket) + open(agentSocket(s))).toBe(0)

      // A reload of the main window finds both windows again: placeholders, no second view.
      await agentWindow.evaluate(() => ((window as unknown as { mark: number }).mark = 1))
      await page.reload()
      await expect(page.locator('[data-connection]')).toHaveAttribute('data-state', 'live')
      await sidebar(page).getByRole('tab', { name: /^Terminals/ }).click()
      await sidebar(page).locator('[data-terminal-row]').click()
      await expect(placeholder(sidebar(page))).toBeVisible()
      await sidebar(page).getByRole('tab', { name: /^Agents/ }).click()
      await sidebar(page).locator(`[data-session="${s.id}"]`).click()
      await expect(placeholder(sidebar(page))).toBeVisible()
      await expect(sidebar(page).locator('.xterm')).toHaveCount(0)
      await expect.poll(() => open(shellSocket) + open(agentSocket(s))).toBe(0)

      // Show the window brings the same window up: not reloaded, no new one.
      let more = 0
      page.on('popup', () => more++)
      await sidebar(page).getByRole('button', { name: 'Show the window' }).click()
      expect(await agentWindow.evaluate(() => (window as unknown as { mark?: number }).mark)).toBe(1)
      expect(more).toBe(0)

      // Bring it back closes the agent's window; the shell's stays out.
      const closed = agentWindow.waitForEvent('close')
      await sidebar(page).getByRole('button', { name: 'Bring it back' }).click()
      await closed
      await expectTerminalText(terminalIn(sidebar(page)), 'hello from fake')
      await expect.poll(() => open(agentSocket(s))).toBe(1)
      expect(shellWindow.isClosed()).toBe(false)
      await sidebar(page).getByRole('tab', { name: /^Terminals/ }).click()
      await sidebar(page).locator('[data-terminal-row]').click()
      await expect(placeholder(sidebar(page))).toBeVisible()

      // Return to omo in the shell's window closes it too.
      await clickToClose(shellWindow, 'Return to omo')
      await expectTerminalText(terminalIn(sidebar(page)), 'popped=9')
      expectNoTokenInUrls(seen, omo.token)
    } finally {
      await closeShells(omo)
    }
  })

  test("the ticket panel's Agents tab pops out too, and the sidebar's view of the same agent shows the placeholder", async ({ page, omo }) => {
    await openBoard(page, omo)
    const n = await omo.createTicket({ title: 'From the panel' })
    await omo.moveTicket(n, 'ready')
    const s = await waitForSession(omo, n, 'developer')
    await page.goto(omo.url(`/${n}?tab=agents`))
    const panel = page.locator('dialog[data-ticket-panel]')
    await panel.locator(`[data-session="${s.id}"] [data-agent-row]`).click()
    await expectTerminalText(terminalIn(panel), 'hello from fake')
    const popup = await popOut(page, `Open the terminal of developer ${s.name} on #${n} in its own window`)
    await expectTerminalText(terminalIn(popup.locator('main')), 'hello from fake')
    await expect(placeholder(panel)).toContainText('This terminal is open in its own window.')
    await expect(panel.locator('.xterm')).toHaveCount(0)
    // The panel is not modal: Escape closes it with the focus inside (the pop-out button that had it is gone).
    await panel.locator('[data-close-panel]').focus()
    await page.keyboard.press('Escape')
    await expect(panel).toHaveCount(0)
    await showAgentPlaceholder(page, s)
    await popup.close()
    await expectTerminalText(terminalIn(sidebar(page)), 'hello from fake')
  })
})

async function showAgentPlaceholder(page: Page, s: AgentSession): Promise<void> {
  await sidebar(page).getByRole('tab', { name: /^Agents/ }).click()
  await sidebar(page).locator(`[data-session="${s.id}"]`).click()
  await expect(placeholder(sidebar(page))).toBeVisible()
}

test('the terminal fits its window and follows its size; the size is remembered for the next window', async ({ page, omo }) => {
  try {
    await openBoard(page, omo)
    await openHomeShell(page)
    const popup = await popOut(page, 'Open the terminal Home in its own window')
    const term = terminalIn(popup.locator('main'))
    await expect(popup.locator('.tv__state')).toHaveText('Connected')
    await expectFitsWindow(popup)
    for (const size of [
      { width: 700, height: 420 },
      { width: 1200, height: 820 },
    ]) {
      await popup.setViewportSize(size)
      await expect.poll(() => popup.evaluate(() => [window.innerWidth, window.innerHeight])).toEqual([size.width, size.height])
      // What the process sees is what fits the box the window gives the terminal.
      await expect
        .poll(async () => {
          const want = await fittingSize(popup)
          const got = await processSize(popup, term)
          return got.rows === want.rows && got.cols <= want.maxCols && got.cols > want.maxCols - 4 ? true : JSON.stringify({ want, got })
        })
        .toBe(true)
    }
    // The window's size is remembered for the next window of its kind.
    await expect.poll(() => page.evaluate(() => window.localStorage.getItem('omo.terminalWindow.size.user'))).toBe(JSON.stringify({ width: 1200, height: 820 }))
  } finally {
    await closeShells(omo)
  }
})

test('a new window opens at the size remembered for its kind', async ({ page, omo }) => {
  try {
    await openBoard(page, omo)
    await page.evaluate(() => window.localStorage.setItem('omo.terminalWindow.size.user', JSON.stringify({ width: 1000, height: 700 })))
    await openHomeShell(page)
    const popup = await popOut(page, 'Open the terminal Home in its own window')
    await expect(popup.locator('.tv__state')).toHaveText('Connected')
    expect(await popup.evaluate(() => [window.innerWidth, window.innerHeight])).toEqual([1000, 700])
    await expectFitsWindow(popup)
  } finally {
    await closeShells(omo)
  }
})

test('a shell that exits in its window says so there and offers to close it; the main window shows its normal ended state', async ({ page, omo }) => {
  try {
    await openBoard(page, omo)
    await openHomeShell(page)
    const popup = await popOut(page, 'Open the terminal Home in its own window')
    await expect(popup.locator('.tv__state')).toHaveText('Connected')
    await expect(popup.locator('.xterm-helper-textarea')).toBeFocused()
    await popup.keyboard.type('exit')
    await popup.keyboard.press('Enter')
    await expect(popup.locator('.tv__ended')).toHaveText('The shell exited.')
    await expect(popup.locator('.tv__state')).toHaveText('Ended')
    await expect(placeholder(sidebar(page))).toHaveCount(0)
    await expect(sidebar(page).locator('.tv__ended')).toHaveText('The shell exited.')
    await expect(sidebar(page).locator('[data-terminal-popout]')).toHaveCount(0)
    await clickToClose(popup, 'Close this window')
  } finally {
    await closeShells(omo)
  }
})

test('the lock screen shows in the window with no terminal under it; after the unlock the terminal is back', async ({ page, omo, context }) => {
  const seen = observe(context)
  try {
    await openBoard(page, omo)
    await openHomeShell(page)
    const popup = await popOut(page, 'Open the terminal Home in its own window')
    await expect(popup.locator('.tv__state')).toHaveText('Connected')

    const oldToken = omo.token
    await omo.stop()
    expect((await omo.cli(['config', 'rotate-token'])).code).toBe(0)
    await omo.start()
    expect(omo.token).not.toBe(oldToken)

    const locked = popup.getByRole('heading', { name: 'omo is locked' })
    await expect(locked).toBeVisible({ timeout: 30_000 })
    await expect(popup.locator('.xterm')).toHaveCount(0)
    await expect(popup.locator('[data-board-kept]')).toHaveAttribute('inert', '')
    await popup.getByRole('textbox', { name: 'Access address or token' }).fill(omo.accessUrl)
    await popup.getByRole('button', { name: 'Unlock' }).click()
    await expect(locked).toHaveCount(0)
    // The shell died with the old omo: the window connects again and says so.
    await expect(popup.locator('[data-terminal-window] .tv__state')).toHaveText(/Connected|Ended/)
    await expect(popup.locator('[data-terminal-window] .xterm')).toBeVisible()
    expectNoTokenInUrls(seen, oldToken)
    expectNoTokenInUrls(seen, omo.token)
    // The window's own address never carried a token either.
    expect(popup.url()).not.toContain(oldToken)
  } finally {
    await closeShells(omo)
  }
})

test('with storage blocked the window gets the token from the page that opened it', async ({ page, omo, context }) => {
  const seen = observe(context)
  await context.addInitScript(() => {
    Object.defineProperty(window, 'localStorage', {
      get() {
        throw new DOMException('blocked', 'SecurityError')
      },
    })
  })
  try {
    await page.goto(omo.accessUrl)
    await expect(page.locator('[data-board]')).toBeVisible()
    await openHomeShell(page)
    const popup = await popOut(page, 'Open the terminal Home in its own window')
    await expect(popup.getByRole('heading', { name: 'omo is locked' })).toHaveCount(0)
    await expect(popup.locator('.tv__state')).toHaveText('Connected')
    await expectTerminalText(terminalIn(popup.locator('main')), 'ready=21')
    expectNoTokenInUrls(seen, omo.token)
  } finally {
    await closeShells(omo)
  }
})

test('a blocked window says so in place and links to the same page in a new tab', async ({ page, omo }) => {
  try {
    await page.addInitScript(() => {
      window.open = () => null
    })
    await openBoard(page, omo)
    await openHomeShell(page)
    await page.getByRole('button', { name: 'Open the terminal Home in its own window' }).click()
    const note = sidebar(page).locator('[data-terminal-blocked]')
    await expect(note).toContainText('The browser blocked the window. Allow pop-ups for this address, or open it here:')
    const [shell] = await shells(omo)
    await expect(note.getByRole('link')).toHaveAttribute('href', `/terminal/user/${shell!.id}`)
    await expect(terminalIn(sidebar(page))).toBeVisible()
  } finally {
    await closeShells(omo)
  }
})

test.describe('at 390 px', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('there is no pop-out button: phones have no windows to speak of', async ({ page, omo }) => {
    try {
      await openBoard(page, omo)
      await page.getByRole('button', { name: 'Sidebar' }).click()
      const sheet = page.getByRole('dialog', { name: 'Sidebar' })
      await sheet.getByRole('tab', { name: /^Terminals/ }).click()
      await sheet.getByRole('button', { name: 'New terminal' }).click()
      await sheet.getByRole('option', { name: /^Home folder/ }).click()
      await expect(terminalIn(sheet)).toBeVisible()
      await expect(sheet.getByRole('button', { name: 'Open the terminal Home in its own window' })).toBeHidden()
    } finally {
      await closeShells(omo)
    }
  })
})

for (const scheme of ['dark', 'light'] as const) {
  test.describe(`${scheme} theme`, () => {
    test.use({ colorScheme: scheme })

    test('the window and the placeholder have no accessibility violations', async ({ page, omo }) => {
      try {
        await openBoard(page, omo)
        await openHomeShell(page)
        const popup = await popOut(page, 'Open the terminal Home in its own window')
        await expect(popup.locator('.tv__state')).toHaveText('Connected')
        await expect(placeholder(sidebar(page))).toBeVisible()
        expect((await new AxeBuilder({ page: popup }).analyze()).violations).toEqual([])
        expect((await new AxeBuilder({ page }).include('[data-sidebar]').analyze()).violations).toEqual([])
        await popup.close()
      } finally {
        await closeShells(omo)
      }
    })
  })
}
