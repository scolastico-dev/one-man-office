import { existsSync, readdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test, type Omo } from './support/omo'
import { expectNoAgentProcesses, script, waitForNoLiveSession, waitForSession } from './support/agents'
import { expectCardIn, nudgeReconnect, observe, openBoard } from './support/board'
import { expectNoTokenInUrls, expectTerminalText, terminalIn, typeInTerminal } from './support/terminal'
import type { Locator, Page, WebSocketRoute } from '@playwright/test'
import AxeBuilder from '@axe-core/playwright'
import { makeWorkspace, type Workspace } from './support/workspace'

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

/** An option of New terminal's search is named by the project's name and then its folder. */
const optionName = (project: string) => new RegExp(`^${project.replace(/[.*+?^${}()|[\]\\/]/g, '\\$&')} `)

/** Opens a shell through New terminal's search: types the name, chooses the project. */
async function newTerminal(root: Locator, project = 'demo'): Promise<void> {
  await root.getByRole('button', { name: 'New terminal' }).click()
  await root.getByRole('combobox', { name: 'Search projects' }).fill(project)
  await root.getByRole('option', { name: optionName(project) }).click()
}

test('a shell opened in the project runs commands and survives a reload with its output; no address carries the token', async ({ page, omo, repo }) => {
  const seen = observe(page)
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

    expect(seen.websocketUrls.length).toBeGreaterThan(0)
    expectNoTokenInUrls(seen, omo.token)
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

/** How often `text` occurs in what the terminal's screen shows. */
async function occurrences(terminal: Locator, text: string): Promise<number> {
  const shown = (await terminal.locator('.xterm-rows').textContent()) ?? ''
  return shown.split(text).length - 1
}

test('the first keys typed into a terminal that just opened are not lost while its history is still being drawn', async ({ page, omo }) => {
  // The socket goes through the test like in the tests below, and the page is
  // slowed down: the keys then arrive while the terminal still parses its replay.
  await page.routeWebSocket(/\/api\/terminals\/[^/]+\/socket$/, (ws) => {
    const server = ws.connectToServer()
    ws.onClose(() => server.close())
  })
  try {
    await openBoard(page, omo)
    await openTerminalsTab(page)
    const cdp = await page.context().newCDPSession(page)
    await cdp.send('Emulation.setCPUThrottlingRate', { rate: 6 })
    await newTerminal(sidebar(page))
    const terminal = terminalIn(sidebar(page))
    await typeInTerminal(page, terminal, 'echo first=$((40 + 2))')
    await expectTerminalText(terminal, 'first=42')
    await cdp.send('Emulation.setCPUThrottlingRate', { rate: 1 })
  } finally {
    await closeAll(omo)
  }
})

test('a terminal whose connection drops connects again and shows its history once', async ({ page, omo }) => {
  // Every terminal socket goes through here; the ones open so far can be dropped.
  const open: WebSocketRoute[] = []
  await page.routeWebSocket(/\/api\/terminals\/[^/]+\/socket$/, (ws) => {
    const server = ws.connectToServer()
    open.push(ws)
    ws.onClose(() => server.close())
  })
  try {
    await openBoard(page, omo)
    await openTerminalsTab(page)
    await newTerminal(sidebar(page))
    const terminal = terminalIn(sidebar(page))
    await typeInTerminal(page, terminal, 'echo once=$((40 + 2))')
    await expectTerminalText(terminal, 'once=42')
    expect(await occurrences(terminal, 'once=42')).toBe(1)

    // A network blip: the socket closes without a word from omo.
    const before = open.length
    for (const ws of open.splice(0)) await ws.close({ code: 4000, reason: 'dropped' })
    await expect(sidebar(page).locator('.tv__state')).toHaveText(/^Disconnected/)
    await expect.poll(() => open.length).toBeGreaterThan(0)
    expect(before).toBeGreaterThan(0)
    await expect(sidebar(page).locator('.tv__state')).toHaveText('Connected')
    await expectTerminalText(terminal, 'once=42')
    expect(await occurrences(terminal, 'once=42')).toBe(1)
    // It is the same shell, and it takes input again.
    await typeInTerminal(page, terminal, 'echo after=$((6 * 7))')
    await expectTerminalText(terminal, 'after=42')
    expect(await occurrences(terminal, 'once=42')).toBe(1)
  } finally {
    await closeAll(omo)
  }
})

test('a terminal that stopped trying while the network was down connects again once the board is live again', async ({ page, omo }) => {
  await page.clock.install()
  let down = false
  const open: WebSocketRoute[] = []
  await page.routeWebSocket(/\/api\/terminals\/[^/]+\/socket$/, async (ws) => {
    if (down) {
      await ws.close({ code: 4000, reason: 'unreachable' })
      return
    }
    const server = ws.connectToServer()
    open.push(ws)
    ws.onClose(() => server.close())
  })
  try {
    await openBoard(page, omo)
    await openTerminalsTab(page)
    await newTerminal(sidebar(page))
    const terminal = terminalIn(sidebar(page))
    await typeInTerminal(page, terminal, 'echo here=$((1 + 1))')
    await expectTerminalText(terminal, 'here=2')

    // The network goes away: nothing reaches omo, the open terminal socket drops.
    down = true
    await page.route('**/api/events*', (route) => route.abort('connectionrefused'))
    for (const ws of open.splice(0)) await ws.close({ code: 4000, reason: 'dropped' })
    const status = sidebar(page).locator('.tv__state')
    // A long sleep: the terminal's back-off runs out and the board's stream goes silent.
    await expect
      .poll(
        async () => {
          await page.clock.fastForward(20_000)
          return status.textContent()
        },
        { timeout: 30_000 },
      )
      .toBe('Disconnected')
    await page.clock.fastForward(60_000)
    await expect(page.locator('[data-connection]')).toHaveAttribute('data-state', /^(retrying|reconnecting)$/)

    // The network is back: the board connects again, and the terminal with it.
    down = false
    await page.unroute('**/api/events*')
    await nudgeReconnect(page)
    await expect(page.locator('[data-connection]')).toHaveAttribute('data-state', 'live')
    await expect(status).toHaveText('Connected')
    await expectTerminalText(terminal, 'here=2')
    expect(await occurrences(terminal, 'here=2')).toBe(1)
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
      await expect(sidebar(page).getByRole('link', { name: `Open #${n}` })).toHaveAttribute('href', `/ticket/${n}`)
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

let ws: Workspace | null = null
test.afterEach(() => {
  ws?.cleanup()
  ws = null
})

/** `n` more projects below a workspace folder ("service-01" …), and waits for them. */
async function manyProjects(omo: Omo, n: number): Promise<Workspace> {
  const layout: Record<string, 'repo'> = {}
  for (let i = 1; i <= n; i++) layout[`clients/service-${String(i).padStart(2, '0')}`] = 'repo'
  ws = makeWorkspace(layout, omo.home)
  await omo.api('PATCH', '/api/settings', { workspace_roots: [{ path: ws.root, max_depth: 4, ignore: ['node_modules', '.*'] }] })
  await expect.poll(async () => (await omo.api<unknown[]>('GET', '/api/projects')).length, { timeout: 30_000 }).toBe(n + 1)
  return ws
}

const picker = (root: Locator) => root.getByRole('combobox', { name: 'Search projects' })

test('by keyboard alone: New terminal searches 40 projects, opens a project shell, and a Home shell in the home folder', async ({ page, omo }) => {
  const w = await manyProjects(omo, 40)
  try {
    await openBoard(page, omo)
    await openTerminalsTab(page)
    const root = sidebar(page)
    await root.getByRole('button', { name: 'New terminal' }).focus()
    await page.keyboard.press('Enter')
    await expect(picker(root)).toBeFocused()
    // Home folder first, then every project; the list scrolls inside the picker.
    await expect(root.getByRole('option').first()).toHaveAccessibleName('Home folder ~')
    await expect(root.getByRole('option')).toHaveCount(42)
    const list = root.getByRole('listbox')
    expect(await list.evaluate((el) => el.scrollHeight > el.clientHeight)).toBe(true)
    // Escape closes and gives the keyboard back to the button.
    await page.keyboard.press('Escape')
    await expect(picker(root)).toHaveCount(0)
    await expect(root.getByRole('button', { name: 'New terminal' })).toBeFocused()

    await page.keyboard.press('Enter')
    await page.keyboard.type('service-3')
    // Home folder stays first; service-30 … 39 match.
    await expect(root.getByRole('option')).toHaveCount(11)
    await page.keyboard.press('ArrowDown')
    await expect(root.getByRole('option', { selected: true })).toHaveAccessibleName(optionName('clients/service-30'))
    await page.keyboard.press('Enter')
    const terminal = terminalIn(root)
    await expect(terminal).toBeVisible()
    await typeInTerminal(page, terminal, `printf 'where=%s\\n' "$([ "$PWD" = '${w.path('clients/service-30')}' ] && echo project || echo elsewhere)"`)
    await expectTerminalText(terminal, 'where=project')
    await root.getByRole('button', { name: 'Back' }).click()

    await root.getByRole('button', { name: 'New terminal' }).focus()
    await page.keyboard.press('Enter')
    await page.keyboard.press('Enter')
    const home = terminalIn(root)
    await expect(home).toBeVisible()
    await typeInTerminal(page, home, `printf 'at=%s\\n' "$([ "$PWD" = '${omo.home}' ] && echo home || echo elsewhere)"`)
    await expectTerminalText(home, 'at=home')
    await root.getByRole('button', { name: 'Back' }).click()
    const rows = root.locator('[data-terminal-row]')
    await expect(rows).toHaveCount(2)
    await expect(rows.first().locator('.trow__title')).toHaveText('Home')
    await expect(rows.first().locator('.trow__dir')).toHaveText(omo.home.length > 44 ? /…/ : omo.home)
  } finally {
    await closeAll(omo)
  }
  await expectNoShellsLeft(omo)
})

for (const width of [1440, 390] as const) {
  for (const colorScheme of ['dark', 'light'] as const) {
    test.describe(`the New terminal search at ${width} px, ${colorScheme} theme`, () => {
      test.use({ viewport: { width, height: width === 390 ? 844 : 900 }, colorScheme, reducedMotion: 'reduce' })

      test('has no axe violations and nothing scrolls sideways', async ({ page, omo }) => {
        await manyProjects(omo, 30)
        await openBoard(page, omo)
        let root: Locator
        if (width === 390) {
          await page.getByRole('button', { name: 'Sidebar' }).click()
          root = page.getByRole('dialog', { name: 'Agents and terminals' })
          await root.getByRole('tab', { name: /^Terminals/ }).click()
        } else {
          await openTerminalsTab(page)
          root = sidebar(page)
        }
        await root.getByRole('button', { name: 'New terminal' }).click()
        await picker(root).fill('service-1')
        await expect(root.getByRole('option')).toHaveCount(11)
        await page.evaluate(() => document.fonts.ready.then(() => undefined))
        const results = await new AxeBuilder({ page }).analyze()
        expect(results.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target.join(' ')) }))).toEqual([])
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true)
        // The picker stays inside the screen.
        const box = (await root.locator('[data-pick]').boundingBox())!
        expect(box.x + box.width).toBeLessThanOrEqual(width)
        // Escape in the sheet closes the picker, not the sheet.
        await page.keyboard.press('Escape')
        await expect(picker(root)).toHaveCount(0)
        await expect(root).toBeVisible()
      })
    })
  }
}

test.describe('on a phone', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('at 390 px a shell can be opened, typed into and closed', async ({ page, omo }) => {
    try {
      await openBoard(page, omo)
      await page.getByRole('button', { name: 'Sidebar' }).click()
      const sheet = page.getByRole('dialog', { name: 'Agents and terminals' })
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

  test('a closed sheet keeps no terminal connected, and opening it again puts the focus on its tab', async ({ page, omo }) => {
    const open: string[] = []
    page.on('websocket', (ws) => {
      open.push(ws.url())
      ws.on('close', () => open.splice(open.indexOf(ws.url()), 1))
    })
    try {
      await openBoard(page, omo)
      await page.getByRole('button', { name: 'Sidebar' }).click()
      const sheet = page.getByRole('dialog', { name: 'Agents and terminals' })
      await sheet.getByRole('tab', { name: /^Terminals/ }).click()
      await newTerminal(sheet)
      const terminal = terminalIn(sheet)
      await typeInTerminal(page, terminal, 'echo sheet=$((40 + 2))')
      await expectTerminalText(terminal, 'sheet=42')
      expect(open).toHaveLength(1)

      await sheet.getByRole('button', { name: 'Close', exact: true }).click()
      await expect(sheet).toBeHidden()
      await expect(page.locator('.xterm')).toHaveCount(0)
      await expect.poll(() => open.length, { message: 'a terminal socket stays open behind the closed sheet' }).toBe(0)

      await page.getByRole('button', { name: 'Sidebar' }).click()
      await expect(sheet.getByRole('tab', { name: /^Terminals/ })).toBeFocused()
      await expectTerminalText(terminalIn(sheet), 'sheet=42')
    } finally {
      await closeAll(omo)
    }
    await expectNoShellsLeft(omo)
  })
})
