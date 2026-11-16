// A ticket on a page of its own (/ticket/<n>): the ways in and out, the
// tabs at the top, the Diff tab's file browser, comments, a live agent
// terminal, editing, a missing ticket, phones and accessibility. With
// OMO_SCREENSHOTS=<folder> the layout tests also write the screenshots of
// the page (fix1-page-<tab>-<theme>-<width>.png) there.
import AxeBuilder from '@axe-core/playwright'
import { execFileSync } from 'node:child_process'
import { existsSync, mkdirSync, writeFileSync } from 'node:fs'
import { devNull } from 'node:os'
import { dirname, join } from 'node:path'
import type { Page } from '@playwright/test'
import { expect, test, type Omo } from './support/omo'
import { expectNoAgentProcesses, script, waitForSession } from './support/agents'
import { card, openBoard, observe } from './support/board'
import { initRepo } from './support/git'
import { expectNoTokenInUrls, expectTerminalText, terminalIn } from './support/terminal'

const ticketPage = (page: Page) => page.locator('[data-ticket-page]')
const panel = (page: Page) => page.locator('dialog[data-ticket-panel]')
const tree = (page: Page) => ticketPage(page).locator('[data-diff-tree]')
const treeFile = (page: Page, path: string) => tree(page).locator(`[data-tree-file="${path}"]`)
const filePane = (page: Page) => ticketPage(page).locator('[data-file-pane]')

const SHOTS = process.env.OMO_SCREENSHOTS ?? ''

/** 3 MiB of text in lines of 1 KiB: far over what one file may send in the whole diff. */
const BIG = ('b'.repeat(1023) + '\n').repeat(3 * 1024)

const gitEnv = () => ({ ...process.env, GIT_CONFIG_GLOBAL: devNull, GIT_CONFIG_SYSTEM: devNull })

/** Commits files into a worktree the way an agent would (folders made as needed), leaving every other change alone. */
function commitInto(dir: string, files: Record<string, string>, message: string): void {
  for (const [name, body] of Object.entries(files)) {
    mkdirSync(dirname(join(dir, name)), { recursive: true })
    writeFileSync(join(dir, name), body)
  }
  const git = (...args: string[]) => execFileSync('git', args, { cwd: dir, env: gitEnv(), stdio: 'pipe' })
  git('add', '--', ...Object.keys(files))
  git('-c', 'user.name=e2e', '-c', 'user.email=e2e@example.invalid', '-c', 'commit.gpgsign=false', 'commit', '-q', '--no-verify', '-m', message)
}

function writeInto(dir: string, files: Record<string, string>): void {
  for (const [name, body] of Object.entries(files)) {
    mkdirSync(dirname(join(dir, name)), { recursive: true })
    writeFileSync(join(dir, name), body)
  }
}

async function sideways(page: Page): Promise<number> {
  return page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
}

/** The worktree of each project of the ticket, once the developer has them. */
async function worktrees(omo: Omo, n: number): Promise<Record<string, string>> {
  let found: Record<string, string> = {}
  await expect
    .poll(
      async () => {
        const t = await omo.api<{ projects: { project: string; worktree: string }[] }>('GET', `/api/tickets/${n}`)
        found = Object.fromEntries(t.projects.map((p) => [p.project, p.worktree]))
        return t.projects.every((p) => p.worktree !== '' && existsSync(p.worktree))
      },
      { message: `waiting for the worktrees of #${n}`, timeout: 30_000 },
    )
    .toBe(true)
  return found
}

async function expectNoAxeViolations(page: Page, label: string): Promise<void> {
  for (const colorScheme of ['dark', 'light'] as const) {
    await page.emulateMedia({ colorScheme })
    await page.evaluate('document.fonts.ready')
    const results = await new AxeBuilder({ page }).analyze()
    expect(results.violations, `${label}, ${colorScheme}`).toEqual([])
  }
}

/** Writes a screenshot of the page in both themes when OMO_SCREENSHOTS names a folder. */
async function shoot(page: Page, name: string): Promise<void> {
  if (SHOTS === '') return
  mkdirSync(SHOTS, { recursive: true })
  const width = page.viewportSize()?.width ?? 0
  for (const theme of ['dark', 'light'] as const) {
    await page.emulateMedia({ colorScheme: theme })
    await page.evaluate('document.fonts.ready')
    await page.screenshot({ path: join(SHOTS, `fix1-page-${name}-${theme}-${width}.png`) })
  }
}

// The Overview tab keeps its column at a readable width and stands it in the
// middle of the tab (as the settings stand in the middle of their room); the
// text inside stays aligned left. The other tabs use the tab's full width.
// With OMO_SCREENSHOTS=<folder>: fix5-overview-<theme>-<width>.png.
test.describe('the Overview column stands in the middle of its tab', () => {
  for (const width of [1440, 2560] as const) {
    test(`at ${width} px the free space left and right of the Overview column is equal`, async ({ page, omo }) => {
      await page.setViewportSize({ width, height: width === 2560 ? 1440 : 900 })
      const n = await omo.createTicket({ title: 'Centre the overview' })
      const { description_version } = await omo.api<{ description_version: number }>('GET', `/api/tickets/${n}`)
      await omo.api('PUT', `/api/tickets/${n}/description`, {
        text: '## Goal\n\n' + 'A long line of description text that wraps at the column width, so the reader never follows lines across the whole screen. '.repeat(6),
        base_version: description_version,
      })
      await omo.api('POST', `/api/tickets/${n}/comments`, { body: 'A comment that is long enough to show how wide the Comments tab is. '.repeat(8) })
      await openBoard(page, omo)
      await page.goto(omo.url(`/ticket/${n}`))
      await expect(ticketPage(page).locator('[data-description]')).toContainText('wraps at the column width')
      await page.evaluate('document.fonts.ready')

      const overview = await page.evaluate(() => {
        const tab = document.querySelector('[data-ticket-page] [data-tab-content="overview"]')!
        const column = tab.firstElementChild!
        const description = column.querySelector('[data-description]')!
        const t = tab.getBoundingClientRect()
        const c = column.getBoundingClientRect()
        return { left: c.left - t.left, right: t.right - c.right, width: c.width, tabWidth: t.width, align: getComputedStyle(description).textAlign }
      })
      expect(overview.width).toBeLessThanOrEqual(880)
      expect(overview.tabWidth, 'the tab is wider than the column').toBeGreaterThan(overview.width + 100)
      expect(Math.abs(overview.left - overview.right), `left ${overview.left}, right ${overview.right}`).toBeLessThanOrEqual(1)
      expect(['left', 'start']).toContain(overview.align)
      if (SHOTS !== '') {
        mkdirSync(SHOTS, { recursive: true })
        for (const theme of ['dark', 'light'] as const) {
          await page.emulateMedia({ colorScheme: theme })
          await page.evaluate('document.fonts.ready')
          await page.screenshot({ path: join(SHOTS, `fix5-overview-${theme}-${width}.png`) })
        }
      }

      // Comments keep the full width of the tab.
      await page.goto(omo.url(`/ticket/${n}?tab=comments`))
      await expect(ticketPage(page).locator('[data-thread]')).toContainText('long enough')
      const comments = await page.evaluate(() => {
        const tab = document.querySelector('[data-ticket-page] [data-tab-content="comments"]')!
        return { tab: tab.getBoundingClientRect().width, thread: document.querySelector('[data-ticket-page] [data-thread]')!.getBoundingClientRect().width }
      })
      expect(Math.abs(comments.tab - comments.thread)).toBeLessThanOrEqual(1)
    })
  }
})

test.describe('ways in and out', () => {
  test('from the panel to the page and back, with the board filters kept; Escape stays on the page', async ({ page, omo }) => {
    const n = await omo.createTicket({ title: 'See me on a page' })
    await openBoard(page, omo)
    await page.goto(omo.url(`/?project=${omo.project}`))
    await card(page, n).locator('[data-card-link]').click()
    await expect(panel(page)).toBeVisible()
    await expect(page).toHaveURL(new RegExp(`/${n}\\?project=${omo.project}$`))

    await panel(page).getByRole('link', { name: 'Open as page' }).click()
    await expect(page).toHaveURL(new RegExp(`/ticket/${n}\\?project=${omo.project}&tab=comments$`))
    await expect(panel(page)).toHaveCount(0)
    // In the board's place: the top bar stays, the columns are gone; the heading has the focus.
    await expect(ticketPage(page)).toBeVisible()
    await expect(page.locator('section[data-drop-column]')).toHaveCount(0)
    await expect(page.locator('[data-back-to-board]')).toBeVisible()
    const heading = ticketPage(page).getByRole('heading', { level: 1, name: 'See me on a page' })
    await expect(heading).toBeFocused()
    await expect(ticketPage(page).getByRole('tab', { name: 'Comments' })).toHaveAttribute('aria-selected', 'true')

    // Escape does not leave the page.
    await page.keyboard.press('Escape')
    await expect(page).toHaveURL(new RegExp(`/ticket/${n}\\?`))

    await ticketPage(page).getByRole('link', { name: 'Open in panel' }).click()
    await expect(panel(page)).toBeVisible()
    await expect(page).toHaveURL(new RegExp(`/${n}\\?project=${omo.project}$`))
    // p in the panel opens the page, b on the page goes back to the board with the filter.
    await page.keyboard.press('p')
    await expect(ticketPage(page)).toBeVisible()
    await page.keyboard.press('b')
    await expect(page).toHaveURL(new RegExp(`/\\?project=${omo.project}$`))
    await expect(card(page, n)).toBeVisible()
    await expect(page.locator('[data-ticket-page]')).toHaveCount(0)

    // Back to the page through the address, then "Back to board".
    await page.goto(omo.url(`/ticket/${n}?project=${omo.project}`))
    await expect(ticketPage(page).getByRole('tab', { name: 'Overview' })).toHaveAttribute('aria-selected', 'true')
    await page.locator('[data-back-to-board]').click()
    await expect(page).toHaveURL(new RegExp(`/\\?project=${omo.project}$`))
    await expect(card(page, n)).toBeVisible()
  })

  test('ticket links carry the page address: a card and a dependency middle-clicked open the page in a new tab', async ({ page, omo, context }) => {
    const a = await omo.createTicket({ title: 'Waits for the other' })
    const b = await omo.createTicket({ title: 'The other' })
    await omo.api('POST', `/api/tickets/${a}/dependencies`, { depends_on: b })
    await openBoard(page, omo)
    const link = card(page, a).locator('[data-card-link]')
    await expect(link).toHaveAttribute('href', `/ticket/${a}`)
    const [tab] = await Promise.all([context.waitForEvent('page'), link.click({ button: 'middle' })])
    await tab.waitForLoadState()
    await expect(tab).toHaveURL(new RegExp(`/ticket/${a}$`))
    await expect(tab.locator('[data-ticket-page]').getByRole('heading', { level: 1 })).toHaveText('Waits for the other')
    await tab.close()
    // A plain click still opens the panel.
    await link.click()
    await expect(panel(page)).toBeVisible()
    const dep = panel(page).locator('[data-ticket-link]', { hasText: `#${b}` }).first()
    await expect(dep).toHaveAttribute('href', `/ticket/${b}`)
    // On the page, a ticket link leads to that ticket's page.
    await page.goto(omo.url(`/ticket/${a}`))
    await ticketPage(page).locator('[data-ticket-link]', { hasText: `#${b}` }).first().click()
    await expect(page).toHaveURL(new RegExp(`/ticket/${b}$`))
    await expect(ticketPage(page).getByRole('heading', { level: 1 })).toHaveText('The other')
  })

  test('an unknown ticket says so on the page, with the way back to the board', async ({ page, omo }) => {
    await openBoard(page, omo)
    await page.goto(omo.url('/ticket/4242?q=x'))
    await expect(ticketPage(page).locator('[data-ticket-missing]')).toContainText('Ticket #4242 does not exist.')
    await expect(ticketPage(page).getByRole('heading', { level: 1 })).toHaveText('Ticket #4242')
    await ticketPage(page).getByRole('link', { name: 'Back to the board' }).click()
    await expect(page).toHaveURL(/\/\?q=x$/)
  })
})

test.describe('the Diff tab as a file browser', () => {
  test.use({ omoOptions: { agents: { developer: script('ready', 'hang'), pm: script('ready', 'hang') } } })

  test('two projects with nested folders: the tree, choosing files with mouse and keys, the filter, the uncommitted part, Show more', async ({ page, omo, repo }) => {
    const second = join(dirname(repo), 'second')
    initRepo(second)
    const p2 = await omo.api<{ id: string }>('POST', '/api/projects', { path: second, name: 'second' })
    // A story's product manager works in both projects' integration worktrees.
    const n = await omo.createTicket({ title: 'Touch two projects', type: 'story', projects: [omo.project, p2.id] })
    await omo.moveTicket(n, 'ready')
    await waitForSession(omo, n, 'pm')
    const dirs = await worktrees(omo, n)
    commitInto(
      dirs[omo.project]!,
      {
        'web/src/features/diff/a.ts': 'export const a = 1\n',
        'web/src/features/diff/b.ts': 'export const b = 2\n',
        'web/README.md': '# web\n',
        'top.txt': 'top\n',
        'big.txt': BIG,
      },
      'feat: change the web part',
    )
    commitInto(dirs[p2.id]!, { 'cmd/omo/main.go': 'package main\n' }, 'feat: a command')
    writeInto(dirs[omo.project]!, { 'notes/draft.md': 'not yet\n', 'top.txt': 'changed on top\n' })

    await openBoard(page, omo)
    await page.goto(omo.url(`/ticket/${n}?tab=diff`))
    await expect(ticketPage(page).getByRole('tab', { name: 'Diff' })).toHaveAttribute('aria-selected', 'true')
    await expect(tree(page)).toBeVisible()
    // One group per project; the folders nest, a chain of single folders is one entry.
    await expect(tree(page).locator('[data-tree-group="project"]')).toHaveCount(2)
    await expect(tree(page).locator('[data-tree-group="project"]').first()).toContainText('demo')
    await expect(tree(page).locator('[data-tree-folder="web"]')).toHaveAttribute('aria-level', '3')
    await expect(tree(page).locator('[data-tree-folder="src/features/diff"]')).toHaveAttribute('aria-level', '4')
    await expect(tree(page).locator('[data-tree-folder="cmd/omo"]')).toBeVisible()
    await expect(treeFile(page, 'web/src/features/diff/a.ts')).toContainText('+1 −0')

    // The first file shows at first; a click shows another, marked in the tree.
    await expect(treeFile(page, 'web/src/features/diff/a.ts')).toHaveAttribute('aria-selected', 'true')
    await expect(filePane(page).locator('[data-path]')).toHaveText('web/src/features/diff/a.ts')
    await treeFile(page, 'web/README.md').click()
    await expect(filePane(page).locator('[data-path]')).toHaveText('web/README.md')
    await expect(treeFile(page, 'web/README.md')).toHaveAttribute('aria-selected', 'true')
    await expect(filePane(page).locator('[data-row="add"] [data-text]')).toHaveText('# web')
    // Keys: Up goes to the file above, Enter shows it; Left on a folder closes it.
    await treeFile(page, 'web/README.md').focus()
    await page.keyboard.press('ArrowUp')
    await expect(treeFile(page, 'web/src/features/diff/b.ts')).toBeFocused()
    await page.keyboard.press('Enter')
    await expect(filePane(page).locator('[data-path]')).toHaveText('web/src/features/diff/b.ts')
    await page.keyboard.press('ArrowLeft')
    await expect(tree(page).locator('[data-tree-folder="src/features/diff"]')).toBeFocused()
    await page.keyboard.press('ArrowLeft')
    await expect(tree(page).locator('[data-tree-folder="src/features/diff"]')).toHaveAttribute('aria-expanded', 'false')
    await expect(treeFile(page, 'web/src/features/diff/a.ts')).toHaveCount(0)
    await page.keyboard.press('ArrowRight')
    await expect(treeFile(page, 'web/src/features/diff/a.ts')).toBeVisible()
    // Next file goes through the files in the tree's order, into the second project.
    await treeFile(page, 'top.txt').first().click()
    await filePane(page).getByRole('button', { name: 'Next file' }).click()
    await expect(filePane(page).locator('[data-path]')).toHaveText('notes/draft.md')
    await expect(filePane(page).locator('[data-diff-section="uncommitted"]')).toContainText('Uncommitted in the worktree')
    await expect(filePane(page).locator('[data-note]')).toContainText('not tracked')
    await expect(treeFile(page, 'notes/draft.md')).toHaveAttribute('aria-selected', 'true')

    // The uncommitted part has its own group; a file changed again since the commit is there too.
    const uncommitted = tree(page).locator('[data-tree-group="uncommitted"]')
    await expect(uncommitted).toHaveCount(1)
    await expect(tree(page).locator('[data-tree-file="top.txt"]')).toHaveCount(2)

    await treeFile(page, 'cmd/omo/main.go').click()
    await expect(filePane(page).locator('[data-diff-project]')).toHaveAttribute('data-diff-project', p2.id)
    await expect(filePane(page)).toContainText('second')

    // The filter.
    const filter = ticketPage(page).getByRole('searchbox', { name: 'Filter files' })
    await filter.fill('main')
    await expect(tree(page).locator('[data-tree-file]')).toHaveCount(1)
    await expect(ticketPage(page).getByText('1 of 8 files')).toBeVisible()
    await filter.fill('zzz')
    await expect(ticketPage(page).getByText('No file matches.')).toBeVisible()
    await filter.fill('')
    await expect(tree(page).locator('[data-tree-file]')).toHaveCount(8)

    // A huge file starts closed; opened, it shows in part and Show more reads more of it.
    await treeFile(page, 'big.txt').click()
    const toggle = filePane(page).locator('[data-file-toggle]')
    await expect(toggle).toHaveAttribute('aria-expanded', 'false')
    await toggle.click()
    await expect(filePane(page).locator('[data-note]')).toContainText('Shown in part')
    const rows = filePane(page).locator('[data-row="add"]')
    const before = await rows.count()
    expect(before).toBeGreaterThan(0)
    await filePane(page).locator('[data-more]').click()
    await expect.poll(() => rows.count()).toBeGreaterThan(before)

    // The file list hides and comes back at the width it had.
    await ticketPage(page).getByRole('button', { name: 'Hide files' }).click()
    await expect(tree(page)).toHaveCount(0)
    await ticketPage(page).getByRole('button', { name: /^Show files/ }).click()
    await expect(tree(page)).toBeVisible()
    const splitter = ticketPage(page).getByRole('separator', { name: 'Width of the file list' })
    const width = Number(await splitter.getAttribute('aria-valuenow'))
    await splitter.focus()
    await page.keyboard.press('ArrowRight')
    await expect(splitter).toHaveAttribute('aria-valuenow', String(width + 16))
    expect(await sideways(page)).toBe(0)
  })

  test('a base behind its remote-tracking branch is said on the page too, with the switch', async ({ page, omo, repo }) => {
    const origin = join(dirname(repo), 'origin.git')
    const git = (cwd: string, ...args: string[]) => execFileSync('git', args, { cwd, env: gitEnv(), stdio: 'pipe' })
    git(dirname(repo), 'init', '-q', '--bare', '-b', 'main', origin)
    git(repo, 'remote', 'add', 'origin', origin)
    git(repo, 'push', '-q', '-u', 'origin', 'main')
    const other = join(dirname(repo), 'other')
    git(dirname(repo), 'clone', '-q', origin, other)
    for (let i = 1; i <= 2; i++) commitInto(other, { [`upstream-${i}.txt`]: `upstream ${i}\n` }, `feat: upstream ${i}`)
    git(other, 'push', '-q', 'origin', 'main')
    git(repo, 'fetch', '-q', 'origin')

    const n = await omo.createTicket({ title: 'Behind' })
    await omo.moveTicket(n, 'ready')
    await waitForSession(omo, n, 'developer')
    const dirs = await worktrees(omo, n)
    commitInto(dirs[omo.project]!, { 'mine.txt': 'mine\n' }, 'feat: mine')
    await openBoard(page, omo)
    await page.goto(omo.url(`/ticket/${n}?tab=diff`))
    await expect(filePane(page).locator('[data-diff-behind]')).toHaveText(
      'local main is 2 commits behind origin/main, so changes from origin/main can show up here as changes of this ticket.',
    )
    const toggle = filePane(page).getByRole('switch', { name: 'Compare with origin/main' })
    await toggle.click()
    await expect(toggle).toHaveAttribute('aria-checked', 'true')
    await expect(filePane(page).locator('[data-diff-heading]')).toContainText(/since it left the remote-tracking branch origin\/main/)
    await expect(treeFile(page, 'mine.txt')).toBeVisible()
    await ticketPage(page).getByRole('button', { name: 'Refresh' }).click()
    await expect(toggle).toHaveAttribute('aria-checked', 'true')
  })
})

test.describe('working on the page', () => {
  test.use({ omoOptions: { agents: { developer: script('ready', 'print|hello from the page', 'write|src/app/main.ts|one', 'commit|feat: first', 'hang') } } })

  test('comments, a live agent terminal, fields and description on the page', async ({ page, omo }) => {
    const seen = observe(page)
    const n = await omo.createTicket({ title: 'Work on the page' })
    await omo.moveTicket(n, 'ready')
    const session = await waitForSession(omo, n, 'developer')
    await openBoard(page, omo)
    await page.goto(omo.url(`/ticket/${n}?tab=comments`))

    // A comment.
    const composer = ticketPage(page).locator('[data-composer]')
    await composer.getByRole('textbox').fill('Looks good from here')
    await composer.getByRole('button', { name: 'Comment' }).click()
    await expect(ticketPage(page).locator('[data-thread]')).toContainText('Looks good from here')

    // The agent's terminal, the whole width of the main column.
    await ticketPage(page).getByRole('tab', { name: /Agents/ }).click()
    await expect(page).toHaveURL(new RegExp(`/ticket/${n}\\?tab=agents$`))
    await ticketPage(page).locator(`[data-session="${session.id}"] [data-agent-row]`).click()
    const term = terminalIn(ticketPage(page))
    await expectTerminalText(term, 'hello from the page')
    // It fills the height the tab has left (a tall window): its screen ends just above the bottom of the window, and nothing scrolls.
    await page.setViewportSize({ width: 1440, height: 1100 })
    await expect
      .poll(async () =>
        page.evaluate(() => {
          const screen = document.querySelector('[data-ticket-page] .tv__screen')!.getBoundingClientRect()
          const panel = document.querySelector('[data-ticket-page] .tabs__panel')!
          return {
            reachesBottom: window.innerHeight - screen.bottom >= 0 && window.innerHeight - screen.bottom < 100,
            panelScrolls: panel.scrollHeight > panel.clientHeight + 1,
          }
        }),
      )
      .toEqual({ reachesBottom: true, panelScrolls: false })
    // Keys typed into the terminal are the agent's, not the page's.
    await term.locator('.xterm-helper-textarea').focus()
    await page.keyboard.press('b')
    await expect(page).toHaveURL(new RegExp(`/ticket/${n}\\?tab=agents$`))

    // The description, on Overview.
    await ticketPage(page).getByRole('tab', { name: 'Overview' }).click()
    await ticketPage(page).locator('[data-edit-description]').click()
    await ticketPage(page).locator('textarea[data-description-input]').fill('Written on the page.')
    await ticketPage(page).locator('[data-save-description]').click()
    await expect(ticketPage(page).locator('[data-description]')).toHaveText('Written on the page.')
    await expect.poll(async () => (await omo.api<{ description: string }>('GET', `/api/tickets/${n}`)).description).toBe('Written on the page.')

    // A field, in the details column.
    const supervisor = ticketPage(page).locator('[data-page-fields="side"] [data-field="supervisor"]')
    await supervisor.getByRole('button', { name: /Supervisor/ }).click()
    await supervisor.getByRole('combobox', { name: 'Supervisor' }).selectOption('user')
    await supervisor.getByRole('button', { name: 'Save' }).click()
    await expect.poll(async () => (await omo.api<{ supervisor: string | null }>('GET', `/api/tickets/${n}`)).supervisor).toBe('user')

    // The title, in the head.
    await ticketPage(page).getByRole('button', { name: 'Edit title' }).click()
    await ticketPage(page).locator('[data-title-input]').fill('Worked on the page')
    await page.keyboard.press('Enter')
    await expect(ticketPage(page).getByRole('heading', { level: 1 })).toHaveText('Worked on the page')

    expectNoTokenInUrls(seen, omo.token)
    expect(seen.pageErrors).toEqual([])
    await omo.api('POST', '/api/agents/stop-all', {})
    await expectNoAgentProcesses(omo)
  })

  test('p and b are plain letters in a terminal, in a search and in a field, on the page and in the panel', async ({ page, omo }) => {
    const n = await omo.createTicket({ title: 'Letters' })
    await omo.moveTicket(n, 'ready')
    const session = await waitForSession(omo, n, 'developer')
    await openBoard(page, omo)

    // The page: the sidebar's project search takes p and b as text.
    await page.goto(omo.url(`/ticket/${n}?tab=agents`))
    await ticketPage(page).locator(`[data-session="${session.id}"] [data-agent-row]`).click()
    await expectTerminalText(terminalIn(ticketPage(page)), 'hello from the page')
    const side = page.locator('[data-sidebar]')
    await side.getByRole('tab', { name: /^Terminals/ }).click()
    await side.getByRole('button', { name: 'New terminal' }).click()
    const search = side.getByRole('combobox', { name: /Search/ })
    await expect(search).toBeFocused()
    await page.keyboard.type('pbpb')
    await expect(search).toHaveValue('pbpb')
    await expect(page).toHaveURL(new RegExp(`/ticket/${n}\\?tab=agents$`))
    await page.keyboard.press('Escape')

    // The panel: a terminal and a field take them too.
    await page.goto(omo.url(`/${n}?tab=agents`))
    await expect(panel(page)).toBeVisible()
    await panel(page).locator(`[data-session="${session.id}"] [data-agent-row]`).click()
    const term = terminalIn(panel(page))
    await expectTerminalText(term, 'hello from the page')
    await term.locator('.xterm-helper-textarea').focus()
    await page.keyboard.type('pb')
    await expect(panel(page)).toBeVisible()
    await expect(page).toHaveURL(new RegExp(`/${n}\\?tab=agents$`))
    await omo.api('POST', '/api/agents/stop-all', {})
    await expectNoAgentProcesses(omo)
  })

  test('the agent terminal pops out from the page, leaves a placeholder that fills the tab, and comes back', async ({ page, omo }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    const n = await omo.createTicket({ title: 'Pop out from the page' })
    await omo.moveTicket(n, 'ready')
    const session = await waitForSession(omo, n, 'developer')
    await openBoard(page, omo)
    await page.goto(omo.url(`/ticket/${n}?tab=agents`))
    await ticketPage(page).locator(`[data-session="${session.id}"] [data-agent-row]`).click()
    await expectTerminalText(terminalIn(ticketPage(page)), 'hello from the page')

    const opened = page.waitForEvent('popup')
    await ticketPage(page).getByRole('button', { name: new RegExp(`^Open the terminal of developer .* on #${n} in its own window$`) }).click()
    const popup = await opened
    await popup.waitForLoadState()
    expect(new URL(popup.url()).pathname).toBe(`/terminal/agent/${encodeURIComponent(session.terminal_id!)}`)
    await expectTerminalText(terminalIn(popup.locator('main')), 'hello from the page')

    // The page keeps its place: a placeholder that takes the tab's height, no terminal.
    const out = ticketPage(page).locator('[data-terminal-out]')
    await expect(out).toContainText('This terminal is open in its own window.')
    await expect(ticketPage(page).locator('.xterm')).toHaveCount(0)
    const tall = await out.evaluate((el) => {
      const panel = document.querySelector('[data-ticket-page] .tabs__panel')!.getBoundingClientRect()
      return panel.bottom - el.getBoundingClientRect().bottom
    })
    expect(tall, 'the placeholder stops short of the bottom of the tab').toBeLessThan(80)

    await out.getByRole('button', { name: 'Bring it back' }).click()
    await expect(out).toHaveCount(0)
    await expectTerminalText(terminalIn(ticketPage(page)), 'hello from the page')
    await expect(page).toHaveURL(new RegExp(`/ticket/${n}\\?tab=agents$`))
    await omo.api('POST', '/api/agents/stop-all', {})
    await expectNoAgentProcesses(omo)
  })
})

test.describe('layout, phones and accessibility', () => {
  test.use({
    omoOptions: {
      agents: {
        developer: script(
          'ready',
          'print|Reading the ticket and the code…',
          'print|Changing the diff view and its tests.',
          'write|web/src/features/diff/DiffTree.vue|<template />',
          'write|web/src/features/diff/fileTree.ts|export {}',
          'write|web/src/features/diff/fileTree.test.ts|export {}',
          'write|web/src/features/ticket/page/TicketPage.vue|<template />',
          'write|README.md|# demo',
          'commit|feat: a file browser for the diff',
          'write|web/src/features/diff/DiffBrowser.vue|<template />',
          'hang',
        ),
      },
    },
  })

  for (const width of [1440, 390] as const) {
    test(`at ${width} px every tab of the page reads well: no sideways scroll, no accessibility violation in either theme`, async ({ page, omo }) => {
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 })
      const story = await omo.createTicket({ title: 'A dedicated ticket page', type: 'story' })
      await omo.createTicket({ title: 'Page layout with tabs at the top', story })
      await omo.createTicket({ title: 'Diff with a file tree', story })
      const n = await omo.createTicket({ title: 'Show the diff with a file browser on the left, like a pull request' })
      const { description_version } = await omo.api<{ description_version: number }>('GET', `/api/tickets/${n}`)
      await omo.api('PUT', `/api/tickets/${n}/description`, {
        text: '## Goal\n\nThe Diff tab on the ticket page lists the changed files as a folder tree on the left and shows the chosen file on the right.\n\n- keyboard navigable tree\n- filter field\n- remembered width',
        base_version: description_version,
      })
      await omo.moveTicket(n, 'ready')
      const session = await waitForSession(omo, n, 'developer')
      const committed = async () => (await omo.api<{ projects: { files: unknown[] }[] }>('GET', `/api/tickets/${n}/diff`)).projects[0]?.files.length ?? 0
      await expect.poll(committed, { message: 'waiting for the commit of the fake developer', timeout: 30_000 }).toBe(5)
      await omo.api('POST', `/api/tickets/${n}/comments`, { body: 'Please keep the tree keyboard navigable, like the board.' })
      await openBoard(page, omo)

      const visit = async (path: string, name: string, ready: (p: Page) => Promise<void>) => {
        await page.goto(omo.url(path))
        await ready(page)
        expect(await sideways(page), `${name} at ${width} px`).toBe(0)
        await expectNoAxeViolations(page, `${name} at ${width} px`)
        await shoot(page, name)
      }

      await visit(`/ticket/${n}`, 'overview', async (p) => {
        await expect(ticketPage(p).locator('[data-description]')).toContainText('folder tree')
        await expect(ticketPage(p).locator('[data-ticket-fields]')).toBeVisible()
      })
      await visit(`/ticket/${n}?tab=comments`, 'comments', async (p) => {
        await expect(ticketPage(p).locator('[data-thread]')).toContainText('keyboard navigable')
      })
      await visit(`/ticket/${n}?tab=diff`, 'diff', async (p) => {
        await expect(filePane(p).locator('[data-path]')).toBeVisible()
        if (width === 390) {
          await expect(ticketPage(p).getByRole('button', { name: 'Files (6)' })).toBeVisible()
          await expect(tree(p)).toHaveCount(0)
        } else await expect(tree(p)).toBeVisible()
      })
      if (width === 390) {
        // The tree as a sheet: choosing a file closes it and shows the file.
        await ticketPage(page).getByRole('button', { name: 'Files (6)' }).click()
        const sheet = page.getByRole('dialog', { name: 'Files (6)' })
        await expect(sheet.locator('[data-diff-tree]')).toBeVisible()
        expect(await sideways(page)).toBe(0)
        await expectNoAxeViolations(page, 'files sheet at 390 px')
        if (SHOTS !== '') await shoot(page, 'diff-files')
        await sheet.locator('[data-tree-file="README.md"]').click()
        await expect(sheet).toBeHidden()
        await expect(filePane(page).locator('[data-path]')).toHaveText('README.md')
      }
      await visit(`/ticket/${n}?tab=agents`, 'agents', async (p) => {
        await ticketPage(p).locator(`[data-session="${session.id}"] [data-agent-row]`).click()
        // The terminal takes the tab's height (a few rows at 390 px), so the early lines have scrolled off: the last ones are shown.
        await expectTerminalText(terminalIn(ticketPage(p)), 'committed: feat: a file browser for the diff')
      })
      await visit(`/ticket/${story}?tab=tasks`, 'tasks', async (p) => {
        await expect(ticketPage(p).locator('[data-task]')).toHaveCount(2)
      })
      // The tabs stay at the top of the page, right under the head.
      const tabs = await ticketPage(page).getByRole('tablist').first().boundingBox()
      expect(tabs?.y ?? 9999).toBeLessThan(width === 390 ? 500 : 320)

      await omo.api('POST', '/api/agents/stop-all', {})
      await expectNoAgentProcesses(omo)
    })
  }
})
