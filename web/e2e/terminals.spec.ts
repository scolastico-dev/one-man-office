import { existsSync, readdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test, type Omo } from './support/omo'
import { expectNoAgentProcesses, script, waitForNoLiveSession, waitForSession } from './support/agents'
import { expectCardIn, observe, openBoard } from './support/board'
import { expectNoTokenInUrls, expectTerminalText, terminalIn, typeInTerminal } from './support/terminal'
import type { Locator, Page } from '@playwright/test'

test.use({ viewport: { width: 1440, height: 900 } })

interface Shell {
  id: string
  title: string
  dir: string
  state: 'running' | 'exited'
}

const shells = (omo: Omo) => omo.api<Shell[]>('GET', '/api/terminals')

/** Ends and forgets every shell the test opened. */
async function closeAll(omo: Omo): Promise<void> {
  for (const s of await shells(omo)) await omo.api('DELETE', `/api/terminals/${encodeURIComponent(s.id)}`)
}

/**
 * Ids of the user shells of this test that are still alive: they carry the
 * test's home and omo's socket but no agent id. Linux only; elsewhere none.
 */
function shellProcesses(omo: Omo): number[] {
  if (process.platform !== 'linux') return []
  const home = `HOME=${omo.home}\0`
  const left: number[] = []
  for (const name of readdirSync('/proc')) {
    if (!/^\d+$/.test(name)) continue
    try {
      const env = readFileSync(`/proc/${name}/environ`, 'latin1')
      if (!env.includes(home) || !env.includes('\0OMO_SOCKET=') || env.includes('OMO_AGENT_ID=')) continue
      const stat = readFileSync(`/proc/${name}/stat`, 'latin1')
      if (stat.slice(stat.lastIndexOf(')') + 2, stat.lastIndexOf(')') + 3) === 'Z') continue
      left.push(Number(name))
    } catch {
      // gone, or not ours to read
    }
  }
  return left
}

async function expectNoShellsLeft(omo: Omo): Promise<void> {
  expect(await shells(omo)).toEqual([])
  await expect.poll(() => shellProcesses(omo), { message: 'shell processes left behind', timeout: 15_000 }).toEqual([])
}

/**
 * The test's home is empty, and zsh then asks a setup question that swallows
 * what a test types. An empty startup file stops it; other shells ignore it.
 */
test.beforeEach(({ omo }) => {
  writeFileSync(join(omo.home, '.zshrc'), '')
})

const sidebar = (page: Page) => page.locator('[data-sidebar]')

/** Opens the Terminals tab on a wide screen. */
async function openTerminalsTab(page: Page): Promise<void> {
  await sidebar(page).getByRole('tab', { name: /^Terminals/ }).click()
  await expect(sidebar(page)).toHaveAttribute('data-open', 'true')
}

async function newTerminal(root: Locator, project = 'demo'): Promise<void> {
  await root.getByRole('button', { name: 'New terminal' }).click()
  await root.getByRole('menuitem', { name: project }).click()
}

test('a shell opened in the project runs commands and survives a reload with its output; no address carries the token', async ({ page, omo, repo }) => {
  const seen = observe(page)
  const sockets: string[] = []
  page.on('websocket', (ws) => sockets.push(ws.url()))
  try {
    await openBoard(page, omo)
    await openTerminalsTab(page)
    await expect(sidebar(page).getByText('No terminal is open.')).toBeVisible()
    await newTerminal(sidebar(page))

    // The shell is shown at once; its tab counts it.
    const terminal = terminalIn(sidebar(page))
    await expect(terminal).toBeVisible()
    await expect(sidebar(page).getByRole('tab', { name: /^Terminals/ })).toHaveText('Terminals 1')
    await typeInTerminal(page, terminal, 'echo answer=$((40 + 2))')
    await expectTerminalText(terminal, 'answer=42')
    // It runs in the project's folder.
    await typeInTerminal(page, terminal, `printf 'where=%s\\n' "$([ "$PWD" = '${repo}' ] && echo project || echo elsewhere)"`)
    await expectTerminalText(terminal, 'where=project')
    // Nothing of the agents' identity reaches it.
    await typeInTerminal(page, terminal, `printf 'agent=%s\\n' "\${OMO_AGENT_ID:-none}"`)
    await expectTerminalText(terminal, 'agent=none')

    // The process check sees a running shell, so an empty result later means something.
    if (process.platform === 'linux') expect(shellProcesses(omo).length).toBeGreaterThan(0)

    // A reload keeps the shell running: its list entry opens it again with the output so far.
    await page.reload()
    await expect(page.locator('[data-connection]')).toHaveAttribute('data-state', 'live')
    await expect(sidebar(page).getByRole('tab', { name: /^Terminals/ })).toHaveText('Terminals 1')
    const row = sidebar(page).locator('[data-terminal-row]')
    await expect(row).toHaveCount(1)
    await expect(row).toContainText('running')
    await row.click()
    const again = terminalIn(sidebar(page))
    await expectTerminalText(again, 'answer=42')
    await typeInTerminal(page, again, 'echo second=$((6 * 7))')
    await expectTerminalText(again, 'second=42')

    // Closing a running shell asks first.
    await sidebar(page).locator('[data-terminal-close]').click()
    await expect(sidebar(page).locator('[data-terminal-confirm]')).toContainText('Close this terminal? The shell and everything running in it stop.')
    await sidebar(page).getByRole('button', { name: 'Keep it open' }).click()
    await expect(sidebar(page).locator('[data-terminal-confirm]')).toHaveCount(0)
    expect(await shells(omo)).toHaveLength(1)
    await sidebar(page).locator('[data-terminal-close]').click()
    await sidebar(page).getByRole('button', { name: 'Close terminal' }).click()
    await expect(sidebar(page).getByText('No terminal is open.')).toBeVisible()
    await expectNoShellsLeft(omo)

    expectNoTokenInUrls(seen, omo.token)
    expect(sockets.length).toBeGreaterThan(0)
    for (const url of sockets) expect(url, 'a websocket address carries the token').not.toContain(omo.token)
    expect(seen.pageErrors).toEqual([])
  } finally {
    await closeAll(omo)
  }
})

test('pasted text cannot end bracketed paste early: nothing after ESC[201~ runs as typed input', async ({ page, omo }) => {
  try {
    await openBoard(page, omo)
    await openTerminalsTab(page)
    await newTerminal(sidebar(page))
    const terminal = terminalIn(sidebar(page))
    await typeInTerminal(page, terminal, 'echo ready=$((20 + 1))')
    await expectTerminalText(terminal, 'ready=21')

    // What a comment written by an agent could hold: the end of a paste, then a new line.
    const input = terminal.locator('.xterm-helper-textarea')
    await input.focus()
    // A DataTransfer made in the page loses control characters that the system
    // clipboard keeps, so the paste event carries the text itself.
    await input.evaluate((el, text) => {
      const ev = new ClipboardEvent('paste', { bubbles: true, cancelable: true })
      Object.defineProperty(ev, 'clipboardData', { value: { getData: (type: string) => (type === 'text/plain' ? text : '') } })
      el.dispatchEvent(ev)
    }, 'echo pasted=$((6 * 7))\x1b[201~\necho INJ""ECTED=$((1 + 1))')
    // The whole paste waits in the line editor; nothing of it ran.
    await expectTerminalText(terminal, 'INJ""ECTED')
    await page.keyboard.press('Control+C')
    await typeInTerminal(page, terminal, 'echo anchor=$((3 + 4))')
    await expectTerminalText(terminal, 'anchor=7')
    await expect(terminal.locator('.xterm-rows')).not.toContainText('pasted=42')
    await expect(terminal.locator('.xterm-rows')).not.toContainText('INJECTED=2')
  } finally {
    await closeAll(omo)
  }
})

test('a shell that exits stays listed as exited until it is removed; a second tab sees the new shell', async ({ page, context, omo }) => {
  try {
    await openBoard(page, omo)
    const second = await context.newPage()
    await openBoard(second, omo)
    await openTerminalsTab(page)
    await openTerminalsTab(second)

    await newTerminal(sidebar(page))
    await expect(sidebar(second).locator('[data-terminal-row]')).toHaveCount(1)
    await expect(sidebar(second).getByRole('tab', { name: /^Terminals/ })).toHaveText('Terminals 1')

    // The shell ends itself: both pages say exited, and removing needs no question.
    const terminal = terminalIn(sidebar(page))
    await typeInTerminal(page, terminal, 'exit')
    await expect(sidebar(page).getByText('The shell exited.').first()).toBeVisible({ timeout: 15_000 })
    await sidebar(page).getByRole('button', { name: 'Back' }).click()
    await expect(sidebar(page).locator('[data-terminal-row]')).toContainText('exited')
    await expect(sidebar(second).locator('[data-terminal-row]')).toContainText('exited')

    await sidebar(page).locator('[data-terminal-row]').click()
    await sidebar(page).locator('[data-terminal-close]').click()
    await expect(sidebar(page).locator('[data-terminal-confirm]')).toHaveCount(0)
    await expect(sidebar(page).getByText('No terminal is open.')).toBeVisible()
    await expect(sidebar(second).getByText('No terminal is open.')).toBeVisible()
    await expectNoShellsLeft(omo)
  } finally {
    await closeAll(omo)
  }
})

test('a refused open shows the server message in the tab', async ({ page, omo }) => {
  try {
    await openBoard(page, omo)
    await openTerminalsTab(page)
    // Make the server refuse: sixteen shells are the most it keeps running.
    for (let i = 0; i < 16; i++) await omo.api('POST', '/api/terminals', { project: omo.project })
    await newTerminal(sidebar(page))
    await expect(sidebar(page).locator('[data-terminal-error]')).toHaveText('16 terminals are open; close one first.')
  } finally {
    await closeAll(omo)
  }
  await expectNoShellsLeft(omo)
})

test.describe('with a running fake developer', () => {
  test.use({ omoOptions: { agents: { developer: script('ready', 'print|working', 'hang') } } })

  test('Open terminal on the ticket opens a shell in its worktree', async ({ page, omo }) => {
    try {
      await openBoard(page, omo)
      const n = await omo.createTicket({ title: 'Work with a shell' })
      await omo.moveTicket(n, 'ready')
      await waitForSession(omo, n, 'developer')
      await expectCardIn(page, n, 'In Progress')
      const worktree = (await omo.api<{ projects: { worktree: string }[] }>('GET', `/api/tickets/${n}`)).projects[0]!.worktree
      expect(worktree).not.toBe('')

      await page.goto(omo.url(`/${n}`))
      const panel = page.getByRole('dialog', { name: /Work with a shell/ })
      await panel.getByRole('button', { name: 'Open terminal' }).click()
      // The panel gives way to the sidebar, which shows the shell, focused.
      await expect(panel).toBeHidden()
      await expect(sidebar(page).getByRole('tab', { name: /^Terminals/ })).toHaveAttribute('aria-selected', 'true')
      const terminal = terminalIn(sidebar(page))
      await expect(terminal).toBeVisible()
      await expect(sidebar(page).locator('[data-terminal-hold]')).toHaveText(`Keeps the worktree of #${n} until it is closed.`)
      await expect(sidebar(page).getByRole('link', { name: `Open #${n}` })).toHaveAttribute('href', `/${n}`)
      await typeInTerminal(page, terminal, `printf 'dir=%s\\n' "$([ "$PWD" = '${worktree}' ] && echo worktree || echo elsewhere)"`)
      await expectTerminalText(terminal, 'dir=worktree')
      await sidebar(page).locator('[data-terminal-close]').click()
      await sidebar(page).getByRole('button', { name: 'Close terminal' }).click()
      await expect(sidebar(page).getByText('No terminal is open.')).toBeVisible()
    } finally {
      await closeAll(omo)
    }
    await expectNoShellsLeft(omo)
    await expectNoAgentProcesses(omo)
  })

  test('Open terminal says why it cannot open before the work starts', async ({ page, omo }) => {
    await openBoard(page, omo)
    const n = await omo.createTicket({ title: 'Not started' })
    await page.goto(omo.url(`/${n}`))
    const panel = page.getByRole('dialog', { name: /Not started/ })
    await expect(panel.getByRole('button', { name: 'Open terminal' })).toBeDisabled()
    await expect(panel).toContainText(`No worktree yet: work on #${n} has not started.`)
    await expectNoShellsLeft(omo)
  })
})

test.describe('a ticket that finishes while a shell is open', () => {
  test.use({
    omoOptions: { agents: { developer: script('ready', 'sleep|8s', 'write|held.txt|held', 'commit|feat: held', 'done|held', 'hang') } },
  })

  test('the worktree stays while a shell is open in it and goes when it closes', async ({ page, omo }) => {
    try {
      await openBoard(page, omo)
      const n = await omo.createTicket({ title: 'Finish under a shell' })
      await omo.api('PATCH', `/api/tickets/${n}`, { review: 'skipped' })
      await omo.moveTicket(n, 'ready')
      await waitForSession(omo, n, 'developer')
      const worktree = (await omo.api<{ projects: { worktree: string }[] }>('GET', `/api/tickets/${n}`)).projects[0]!.worktree
      expect(existsSync(worktree)).toBe(true)

      // The shell is opened through the panel while the developer is still busy.
      await page.goto(omo.url(`/${n}`))
      await page.getByRole('dialog', { name: /Finish under a shell/ }).getByRole('button', { name: 'Open terminal' }).click()
      await expect(terminalIn(sidebar(page))).toBeVisible()

      // The ticket is merged and done; the folder stays for the shell.
      await expectCardIn(page, n, 'Done')
      await waitForNoLiveSession(omo, n)
      expect(existsSync(worktree), 'the worktree went while a shell was open in it').toBe(true)
      await typeInTerminal(page, terminalIn(sidebar(page)), 'echo still=$(ls | wc -l)')
      await expectTerminalText(terminalIn(sidebar(page)), /still=\d/)

      // Closing the shell lets the folder go.
      await sidebar(page).locator('[data-terminal-close]').click()
      await sidebar(page).getByRole('button', { name: 'Close terminal' }).click()
      await expect(sidebar(page).getByText('No terminal is open.')).toBeVisible()
      await expect.poll(() => existsSync(worktree), { message: 'the worktree stayed after the shell closed', timeout: 30_000 }).toBe(false)
    } finally {
      await closeAll(omo)
    }
    await expectNoShellsLeft(omo)
    await expectNoAgentProcesses(omo)
  })
})

test.describe('on a phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('at 390 px a shell can be opened, typed into and closed', async ({ page, omo }) => {
    try {
      await openBoard(page, omo)
      await page.getByRole('button', { name: 'Sidebar' }).click()
      const sheet = page.getByRole('dialog', { name: 'Sidebar' })
      await sheet.getByRole('tab', { name: /^Terminals/ }).click()
      await newTerminal(sheet)
      const terminal = terminalIn(sheet)
      await expect(terminal).toBeVisible()
      await typeInTerminal(page, terminal, 'echo phone=$((20 + 22))')
      await expectTerminalText(terminal, 'phone=42')
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true)
      expect(await sheet.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true)
      await sheet.locator('[data-terminal-close]').click()
      await sheet.getByRole('button', { name: 'Close terminal' }).click()
      await expect(sheet.getByText('No terminal is open.')).toBeVisible()
    } finally {
      await closeAll(omo)
    }
    await expectNoShellsLeft(omo)
  })
})
