// The board shell never scrolls as a document: the top bar, the sidebar and
// the page's panes stay in the window and the panes inside scroll on their
// own. This walks the full-height views (the board, the ticket page on every
// tab, the settings pages, the sidebar with a terminal, the pop-out terminal
// window) at two window sizes, and drives the Diff tab's file browser, which
// once scrolled the whole document. With OMO_SCREENSHOTS=<folder> the diff
// test writes page-scroll-after-<width>.png there.
import { withoutShellConfig } from './support/env'
import { traceWheels, wheelTraceProblems } from './support/wheelTrace'
import { scrollBoxesTakingWheelsBadly } from './support/wheelAudit'
import { execFileSync } from 'node:child_process'
import { existsSync, mkdirSync, writeFileSync } from 'node:fs'
import { devNull } from 'node:os'
import { dirname, join } from 'node:path'
import type { Locator, Page } from '@playwright/test'
import { expect, test, type Omo } from './support/omo'
import { expectNoAgentProcesses, script, waitForSession } from './support/agents'
import { openBoard } from './support/board'
import { png, uploadImage } from './support/images'
import { terminalIn } from './support/terminal'
import { expectSettingsReady } from './support/settings'

const SHOTS = process.env.OMO_SCREENSHOTS ?? ''

const ticketPage = (page: Page) => page.locator('[data-ticket-page]')
const tree = (page: Page) => ticketPage(page).locator('[data-diff-tree]')
const filePane = (page: Page) => ticketPage(page).locator('[data-file-pane]')

const gitEnv = () => ({ ...withoutShellConfig(), GIT_CONFIG_GLOBAL: devNull, GIT_CONFIG_SYSTEM: devNull })

function commitInto(dir: string, files: Record<string, string>, message: string): void {
  for (const [name, body] of Object.entries(files)) {
    mkdirSync(dirname(join(dir, name)), { recursive: true })
    writeFileSync(join(dir, name), body)
  }
  const git = (...args: string[]) => execFileSync('git', args, { cwd: dir, env: gitEnv(), stdio: 'pipe' })
  git('add', '--', ...Object.keys(files))
  git('-c', 'user.name=e2e', '-c', 'user.email=e2e@example.invalid', '-c', 'commit.gpgsign=false', 'commit', '-q', '--no-verify', '-m', message)
}

async function worktreeOf(omo: Omo, n: number): Promise<string> {
  let found = ''
  await expect
    .poll(
      async () => {
        const t = await omo.api<{ projects: { worktree: string }[] }>('GET', `/api/tickets/${n}`)
        found = t.projects[0]?.worktree ?? ''
        return found !== '' && existsSync(found)
      },
      { message: `waiting for the worktree of #${n}`, timeout: 30_000 },
    )
    .toBe(true)
  return found
}

/** The document's scroll state: it must have nothing to scroll and stand at the top. */
async function documentScroll(page: Page) {
  return page.evaluate(() => {
    const el = document.scrollingElement ?? document.documentElement
    return { extra: el.scrollHeight - el.clientHeight, top: el.scrollTop, sideways: el.scrollWidth - el.clientWidth }
  })
}

async function expectStill(page: Page, where: string): Promise<void> {
  // Scrolling happens in the next frames (a tree row brought into view after a render).
  await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))))
  expect(await documentScroll(page), where).toEqual({ extra: 0, top: 0, sideways: 0 })
}

/** Turns the mouse wheel far down over an element, then waits until the scroll has settled. */
async function wheelOver(page: Page, target: Locator): Promise<void> {
  const box = await target.boundingBox()
  if (!box) throw new Error('nothing to scroll over')
  await page.mouse.move(box.x + box.width / 2, box.y + Math.min(box.height / 2, 200))
  // Nothing is awaited for a wheel that moves nothing, so the trace shows that every wheel reached a scroll
  // gesture the compositor handled: a dropped wheel would leave the check that the document stays still empty.
  const trace = await traceWheels(page, async () => {
    for (let i = 0; i < 10; i++) await page.mouse.wheel(0, 2000)
    await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))))
  })
  expect(wheelTraceProblems(trace, 10), 'the wheels over the element are handled by the compositor alone').toEqual([])
}

/** 3 MiB of text in lines of 1 KiB: more than one file may send in the whole diff. */
const BIG = ('b'.repeat(1023) + '\n').repeat(3 * 1024)

const SIZES = [
  { width: 2000, height: 1088 },
  { width: 1440, height: 900 },
] as const

/** A long file: 3,000 lines with a change near the end. */
const longFile = (changed: boolean) =>
  Array.from({ length: 3000 }, (_, i) => (changed && i === 2900 ? `line ${i + 1} changed` : `line ${i + 1}`)).join('\n') + '\n'

test.describe('the document never scrolls', () => {
  test.use({ omoOptions: { agents: { developer: script('ready', 'hang') } } })

  // One scenario per test: each owns its ticket, files and time, so no test's pass depends on the whole walk finishing within one test's time.
  async function manyFiles(page: Page, omo: Omo, repo: string, size: { width: number; height: number }): Promise<void> {
    commitInto(repo, { 'long.txt': longFile(false) }, 'chore: a long file')
    const n = await omo.createTicket({ title: 'Touch many files' })
    await omo.moveTicket(n, 'ready')
    await waitForSession(omo, n, 'developer')
    const dir = await worktreeOf(omo, n)
    const files: Record<string, string> = { 'long.txt': longFile(true), 'big.txt': BIG }
    for (let i = 0; i < 80; i++) files[`src/part${Math.floor(i / 10)}/file${String(i).padStart(2, '0')}.ts`] = `export const v${i} = ${i}\n`
    commitInto(dir, files, 'feat: many files')

    await page.setViewportSize(size)
    await openBoard(page, omo)
    await page.goto(omo.url(`/ticket/${n}?tab=diff`))
    await expect(tree(page)).toBeVisible()
    await expect(filePane(page).locator('[data-path]')).toHaveText('src/part0/file00.ts')
    await expectStill(page, 'after opening the tab')
  }

  async function stopAgents(omo: Omo): Promise<void> {
    await omo.api('POST', '/api/agents/stop-all', {})
    await expectNoAgentProcesses(omo)
  }

  for (const size of SIZES) {
    const at = `at ${size.width}×${size.height}`

    test(`${at} the Diff tab with many files keeps the document still while the keyboard and the wheel walk the files`, async ({ page, omo, repo }) => {
      await manyFiles(page, omo, repo, size)
      // The last file through the keyboard: End in the tree, Enter.
      await tree(page).locator('[data-tree-file="src/part0/file00.ts"]').focus()
      await page.keyboard.press('End')
      await page.keyboard.press('Enter')
      await expect(filePane(page).locator('[data-path]')).toHaveText('long.txt')
      await expectStill(page, 'after choosing the last file with the keyboard')
      // The wheel over the tree past its end, and over the file: the rest of a scroll never moves the document.
      await wheelOver(page, tree(page))
      await expectStill(page, 'after the wheel over the tree')
      await wheelOver(page, filePane(page))
      await expectStill(page, 'after the wheel over the file')

      // Previous and Next walk back to the first file.
      for (let i = 0; i < 3; i++) await filePane(page).getByRole('button', { name: 'Previous file' }).click()
      await expectStill(page, 'after Previous file')
      await filePane(page).getByRole('button', { name: 'Next file' }).click()
      await expectStill(page, 'after Next file')
      await tree(page).locator('[data-tree-file="long.txt"]').click()
      await expect(filePane(page).locator('[data-path]')).toHaveText('long.txt')
      await expectStill(page, 'after choosing the long file')
      await stopAgents(omo)
    })

    test(`${at} the Diff tab with many files keeps the document still while hidden lines open above a change`, async ({ page, omo, repo }) => {
      await manyFiles(page, omo, repo, size)
      await tree(page).locator('[data-tree-file="long.txt"]').click()
      await expect(filePane(page).locator('[data-path]')).toHaveText('long.txt')
      await expectStill(page, 'after choosing the long file')
      const file = filePane(page).locator('[data-diff-file]')
      await file.getByRole('button', { name: /^Show \d+ hidden lines above line/ }).first().click()
      await expectStill(page, 'after expanding lines above a change')
      await stopAgents(omo)
    })

    // Showing the 3,000 lines of the whole file is the costly step (about a second of rendering, four times that under the browser tests' CPU throttle),
    // so each thing that is checked on the whole file is its own test with its own time.
    async function wholeFile(page: Page, omo: Omo, repo: string): Promise<Locator> {
      await manyFiles(page, omo, repo, size)
      await tree(page).locator('[data-tree-file="long.txt"]').click()
      await expect(filePane(page).locator('[data-path]')).toHaveText('long.txt')
      const file = filePane(page).locator('[data-diff-file]')
      await file.getByRole('button', { name: 'Show the whole file long.txt' }).click()
      await expect(file.locator('[data-gap]')).toHaveCount(0)
      await expectStill(page, 'after Show the whole file')
      return file
    }

    test(`${at} the Diff tab with many files keeps the document still after Show the whole file`, async ({ page, omo, repo }) => {
      await wholeFile(page, omo, repo)
      await stopAgents(omo)
    })

    test(`${at} the Diff tab with many files keeps the document still while the wheel turns over the whole file`, async ({ page, omo, repo }) => {
      await wholeFile(page, omo, repo)
      await wheelOver(page, filePane(page))
      await expectStill(page, 'after the wheel over the whole file')
      await stopAgents(omo)
    })

    test(`${at} the Diff tab with many files keeps the document still after Previous file from the whole file`, async ({ page, omo, repo }) => {
      await wholeFile(page, omo, repo)
      await filePane(page).getByRole('button', { name: 'Previous file' }).click()
      await expectStill(page, 'after Previous file from the whole file')
      await stopAgents(omo)
    })

    test(`${at} the Diff tab with many files keeps the document still while a huge file is read with Show more`, async ({ page, omo, repo }) => {
      await manyFiles(page, omo, repo, size)
      // A huge file read on its own with Show more.
      await tree(page).locator('[data-tree-file="big.txt"]').click()
      await filePane(page).locator('[data-file-toggle]').click()
      const rows = filePane(page).locator('[data-row="add"]')
      const before = await rows.count()
      await filePane(page).locator('[data-more]').click()
      await expect.poll(() => rows.count()).toBeGreaterThan(before)
      await wheelOver(page, filePane(page))
      await expectStill(page, 'after Show more')
      await tree(page).locator('[data-tree-file="long.txt"]').click()
      if (SHOTS !== '') {
        mkdirSync(SHOTS, { recursive: true })
        await page.emulateMedia({ colorScheme: 'dark' })
        await page.screenshot({ path: join(SHOTS, `page-scroll-after-${size.width}.png`) })
      }
      await stopAgents(omo)
    })
  }
})

/** Every section of the settings, as the address names it. */
const SETTINGS = ['projects', 'agents', 'roles', 'usage', 'prompts', 'skills', 'mcp', 'environment', 'limits', 'review', 'plugins', 'general', 'backup'] as const

test.describe('every full-height view', () => {
  test.use({ omoOptions: { agents: { developer: script('ready', ...Array.from({ length: 200 }, (_, i) => `print|line ${i + 1} of the developer`), 'hang') } } })
  test.beforeEach(({ omo }) => {
    // zsh's new-user wizard would swallow the shell's first lines.
    writeFileSync(join(omo.home, '.zshrc'), '')
  })

  const views = [
    { width: 1440, height: 900 },
    { width: 390, height: 844 },
  ].flatMap((size) => (['still', 'tickets', 'settings'] as const).map((part) => ({ size, part })))
  // One test keeps the document still through every view; the wheel audit of the views is split in two, so that each test is a part of its time.
  for (const { size, part } of views) {
    const audit = part !== 'still'
    const what = { still: 'keep the document still', tickets: 'show every scroll box of the board and the ticket views a wheel over it is scrolled by the compositor alone', settings: 'show every scroll box of the settings a wheel over it is scrolled by the compositor alone' }[part]
    const board = part !== 'settings'
    const sections = part !== 'tickets'
    test(`at ${size.width}×${size.height} the board, the ticket page's tabs, the settings, the sidebar's terminal and a terminal's window ${what}`, async ({ page, omo, repo }) => {
      test.setTimeout(120_000)
      commitInto(repo, { 'long.txt': longFile(false) }, 'chore: a long file')
      const story = await omo.createTicket({ title: 'A story with many tasks', type: 'story' })
      for (let i = 0; i < 12; i++) await omo.createTicket({ title: `Task ${i + 1} of the story`, story })
      for (let i = 0; i < 20; i++) await omo.createTicket({ title: `Another ticket ${i + 1} to fill the columns` })
      const n = await omo.createTicket({ title: 'Long everything' })
      const { description_version } = await omo.api<{ description_version: number }>('GET', `/api/tickets/${n}`)
      await omo.api('PUT', `/api/tickets/${n}/description`, { text: Array.from({ length: 120 }, (_, i) => `Paragraph ${i + 1}.`).join('\n\n'), base_version: description_version })
      for (let i = 0; i < 30; i++) await omo.api('POST', `/api/tickets/${n}/comments`, { body: `Comment ${i + 1}\n\nwith a second paragraph.` })
      await omo.moveTicket(n, 'ready')
      const session = await waitForSession(omo, n, 'developer')
      const dir = await worktreeOf(omo, n)
      const files: Record<string, string> = { 'long.txt': longFile(true) }
      for (let i = 0; i < 60; i++) files[`src/part${Math.floor(i / 10)}/file${String(i).padStart(2, '0')}.ts`] = `export const v${i} = ${i}\n`
      commitInto(dir, files, 'feat: many files')
      const shell = await omo.api<{ id: string }>('POST', '/api/terminals', { project: omo.project })

      await page.setViewportSize(size)
      await openBoard(page, omo)
      await expect(page.locator('[data-card-link]').first()).toBeVisible()
      await expectStill(page, 'the board')
      // Every scroll box of the view takes a trusted wheel on the compositor alone (the board here, each view below).
      if (audit && board) expect(await scrollBoxesTakingWheelsBadly(page, 'body', `the board at ${size.width} px`), `the board at ${size.width} px`).toEqual([])

      const visit = async (path: string, where: string, ready: () => Promise<void>) => {
        await page.goto(omo.url(path))
        await ready()
        await expectStill(page, `${where} at ${size.width} px`)
        // Two views are left out, each for what the platform does, not for the app (the wheel on the first is checked in settings-wheel.spec.ts): ten or more text fields make Chromium ask the
        // main thread for every wheel on the page (no style changes that), and a terminal's wheel is xterm.js's own listener.
        if (audit && where !== 'settings environment' && where !== 'terminal window') expect(await scrollBoxesTakingWheelsBadly(page, /^\/\d+/.test(path) ? 'dialog[data-ticket-panel]' : 'body', `${where} at ${size.width} px`), `${where} at ${size.width} px`).toEqual([])
      }
      if (board) {
        await visit(`/ticket/${n}`, 'overview', () => expect(ticketPage(page).locator('[data-description]')).toContainText('Paragraph 120'))
        await visit(`/ticket/${n}?tab=comments`, 'comments', () => expect(ticketPage(page).locator('[data-thread]')).toContainText('Comment 30'))
        // An image in a comment, and its lightbox open and closed.
        const ref = await uploadImage(omo, n, 'wide.png', png(1600, 1200))
        await omo.api('POST', `/api/tickets/${n}/comments`, { body: `The picture:\n\n${ref}`, question: false })
        for (const [path, root, name] of [
          [`/ticket/${n}?tab=comments`, ticketPage(page), 'the page'],
          [`/${n}?tab=comments`, page.locator('dialog[data-ticket-panel]'), 'the panel'],
        ] as const) {
          await visit(path, `${name} with an image`, () => expect(root.locator('[data-comment-body]').getByRole('img', { name: 'wide.png' })).toBeVisible())
          await root.locator('[data-comment-body]').getByRole('button', { name: 'wide.png' }).click()
          await expect(page.locator('dialog[data-image-lightbox] img')).toBeVisible()
          await expectStill(page, `${name} with the lightbox open at ${size.width} px`)
          await wheelOver(page, page.locator('dialog[data-image-lightbox]'))
          await expectStill(page, `${name} with the lightbox after the wheel at ${size.width} px`)
          await page.keyboard.press('Escape')
          await expect(page.locator('dialog[data-image-lightbox]')).toHaveCount(0)
          await expectStill(page, `${name} after the lightbox closed at ${size.width} px`)
        }
        await visit(`/ticket/${n}?tab=diff`, 'diff', () => expect(filePane(page).locator('[data-path]')).toBeVisible())
        // The panel's stacked diff over the board.
        await visit(`/${n}?tab=diff`, 'the panel on its Diff tab', () => expect(page.locator('dialog[data-ticket-panel] [data-diff-file]').first()).toBeVisible())
        await wheelOver(page, page.locator('dialog[data-ticket-panel]'))
        await expectStill(page, `the panel's diff after the wheel at ${size.width} px`)
        await visit(`/ticket/${n}?tab=agents`, 'agents', async () => {
          await ticketPage(page).locator(`[data-session="${session.id}"] [data-agent-row]`).click()
          await expect(terminalIn(ticketPage(page))).toContainText('line 200 of the developer')
        })
        await visit(`/ticket/${story}?tab=tasks`, 'tasks', () => expect(ticketPage(page).locator('[data-task]')).toHaveCount(12))
      }
      if (sections) {
        for (const section of SETTINGS) {
          await visit(`/settings/${section}`, `settings ${section}`, () => expectSettingsReady(page, section))
        }
      }

      if (!audit) {
        // The sidebar with a shell in it (on a phone the sidebar is a sheet).
        await page.goto(omo.url('/'))
        await expect(page.locator('[data-card-link]').first()).toBeVisible()
        const side = size.width < 768 ? page.getByRole('dialog', { name: 'Agents and terminals' }) : page.locator('[data-sidebar]')
        if (size.width < 768) await page.getByRole('button', { name: 'Sidebar' }).click()
        await side.getByRole('tab', { name: /^Terminals/ }).click()
        await side.locator(`[data-terminal="${shell.id}"]`).click()
        await expect(terminalIn(side)).toBeVisible()
        await expectStill(page, `the sidebar with a terminal at ${size.width} px`)

        // A terminal alone in its own window.
        await visit(`/terminal/user/${encodeURIComponent(shell.id)}`, 'terminal window', () => expect(terminalIn(page.locator('body'))).toBeVisible())
      }

      await omo.api('POST', '/api/agents/stop-all', {})
      await omo.api('DELETE', `/api/terminals/${encodeURIComponent(shell.id)}`)
      await expectNoAgentProcesses(omo)
    })
  }
})
