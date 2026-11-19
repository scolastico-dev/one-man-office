import AxeBuilder from '@axe-core/playwright'
import { execFileSync } from 'node:child_process'
import { existsSync, writeFileSync } from 'node:fs'
import { devNull } from 'node:os'
import { dirname, join } from 'node:path'
import type { Page } from '@playwright/test'
import { expect, test, type Omo } from './support/omo'
import { script, waitForSession } from './support/agents'
import { openBoard } from './support/board'
import { initRepo } from './support/git'
import { withoutShellConfig } from './support/env'

const panel = (page: Page) => page.locator('dialog[data-ticket-panel]')
const section = (page: Page, id: 'committed' | 'uncommitted') => panel(page).locator(`[data-diff-section="${id}"]`)
const fileIn = (page: Page, id: 'committed' | 'uncommitted', name: string) =>
  section(page, id).locator('[data-diff-file]', { has: page.locator('[data-path]', { hasText: name }) })

const RLO = '‮'
/** A file name with a right-to-left override, written by the agent. */
const BIDI_NAME = `x y${RLO}.txt`
const LONG_LINE = 'L'.repeat(600)

/** Commits files into a worktree the way an agent would, leaving every other change alone. */
function commitInto(dir: string, files: Record<string, string | Buffer>, message: string): void {
  for (const [name, body] of Object.entries(files)) writeFileSync(join(dir, name), body)
  const env = { ...withoutShellConfig(), GIT_CONFIG_GLOBAL: devNull, GIT_CONFIG_SYSTEM: devNull }
  const git = (...args: string[]) => execFileSync('git', args, { cwd: dir, env, stdio: 'pipe' })
  git('add', '--', ...Object.keys(files))
  git('-c', 'user.name=e2e', '-c', 'user.email=e2e@example.invalid', '-c', 'commit.gpgsign=false', 'commit', '-q', '--no-verify', '-m', message)
}

/** A 1×1 PNG: binary to git (it holds NUL bytes). */
const PNG = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==', 'base64')
/** 3 MiB of text in lines of 1 KiB: far over what one file may send in the whole diff. */
const BIG = ('b'.repeat(1023) + '\n').repeat(3 * 1024)

async function sideways(page: Page): Promise<boolean> {
  return page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth)
}

/** Starts the fake developer on a new task and waits until it has written its files. */
async function startWork(omo: Omo): Promise<{ n: number; dir: string }> {
  const n = await omo.createTicket({ title: 'Change some files' })
  await omo.moveTicket(n, 'ready')
  const s = await waitForSession(omo, n, 'developer')
  const dir = s.workdir
  await expect.poll(() => existsSync(join(dir, BIDI_NAME)), { message: 'waiting for the fake developer to write its files', timeout: 30_000 }).toBe(true)
  return { n, dir }
}

test.describe('a developer at work', () => {
  test.use({
    omoOptions: {
      agents: {
        developer: script(
          'ready',
          'write|a.txt|first',
          `write|long.txt|${LONG_LINE}`,
          'commit|feat: add a and a long line',
          'write|a.txt|second',
          'write|b.txt|not yet',
          `write|${BIDI_NAME}|bidi`,
          'hang',
        ),
      },
    },
  })

  test('the Diff tab shows committed and uncommitted changes of a running task', async ({ page, omo }) => {
    await openBoard(page, omo)
    const { n } = await startWork(omo)
    await page.goto(omo.url(`/${n}?tab=diff`))

    const heading = panel(page).locator('[data-diff-heading]')
    await expect(heading).toContainText(/Changes on omo\/\S+ since it left main/)

    await expect(section(page, 'committed').locator('[data-diff-totals]')).toHaveText(/2 files, \+2 −0/)
    const a = fileIn(page, 'committed', 'a.txt')
    await expect(a.locator('[data-status]')).toContainText('added')
    await expect(a.locator('[data-row="add"] [data-text]')).toHaveText('first')
    await expect(a.locator('[data-row="add"] .sr-only')).toHaveText('added')

    const changed = fileIn(page, 'uncommitted', 'a.txt')
    await expect(changed.locator('[data-status]')).toContainText('modified')
    await expect(changed.locator('[data-row="del"] [data-text]')).toHaveText('first')
    await expect(changed.locator('[data-row="add"] [data-text]')).toHaveText('second')
    const untracked = fileIn(page, 'uncommitted', 'b.txt')
    await expect(untracked).toContainText('contents not shown')
    await expect(untracked.locator('[data-note]')).toContainText('not tracked')
    // The uncommitted file is not among the committed ones.
    await expect(fileIn(page, 'committed', 'b.txt')).toHaveCount(0)

    for (const colorScheme of ['dark', 'light'] as const) {
      await page.emulateMedia({ colorScheme })
      const results = await new AxeBuilder({ page }).include('dialog[data-ticket-panel]').analyze()
      expect(results.violations, colorScheme).toEqual([])
    }
  })

  test('a binary file and a too-large file are named with a note; Show more loads the part the server allows', async ({ page, omo }) => {
    await openBoard(page, omo)
    const { n, dir } = await startWork(omo)
    commitInto(dir, { 'big.txt': BIG, 'pic.png': PNG }, 'feat: add a picture and a big file')
    await page.goto(omo.url(`/${n}?tab=diff`))

    const pic = fileIn(page, 'committed', 'pic.png')
    await expect(pic.locator('[data-file-toggle]')).toContainText('binary')
    await expect(pic.locator('[data-note]')).toHaveText('Binary file, not shown.')

    const big = fileIn(page, 'committed', 'big.txt')
    const toggle = big.locator('[data-file-toggle]')
    // Too many lines: it starts closed.
    await expect(toggle).toHaveAttribute('aria-expanded', 'false')
    await toggle.click()
    await expect(toggle).toHaveAttribute('aria-expanded', 'true')
    await expect(big.locator('[data-note]')).toContainText('Shown in part')
    const rows = big.locator('[data-row="add"]')
    const before = await rows.count()
    expect(before).toBeGreaterThan(0)
    expect(before).toBeLessThan(3 * 1024)
    await big.locator('[data-more]').click()
    await expect.poll(() => rows.count()).toBeGreaterThan(before)
    // The single-file answer is bounded too: still in part, and nothing more to ask for.
    await expect(big.locator('[data-note]')).toContainText('Shown in part')
    await expect(big.locator('[data-more]')).toHaveCount(0)
    expect(await rows.count()).toBeLessThan(3 * 1024)
  })

  test('a file name with a bidi override is shown escaped', async ({ page, omo }) => {
    await openBoard(page, omo)
    const { n } = await startWork(omo)
    await page.goto(omo.url(`/${n}?tab=diff`))
    const files = section(page, 'uncommitted').locator('[data-path]')
    await expect(files.filter({ hasText: 'b.txt' })).toHaveCount(1)
    const names = await files.allTextContents()
    expect(names).toContain('x y\\u{202e}.txt')
    const text = (await panel(page).locator('[data-diff]').textContent()) ?? ''
    expect(text).not.toContain(RLO)
  })

  test('a base behind its remote-tracking branch is said, and the diff can compare with that branch', async ({ page, omo, repo }) => {
    // The project's main has a remote-tracking branch origin/main in a bare repository beside it, equal at first.
    const origin = join(dirname(repo), 'origin.git')
    const env = { ...withoutShellConfig(), GIT_CONFIG_GLOBAL: devNull, GIT_CONFIG_SYSTEM: devNull }
    const git = (cwd: string, ...args: string[]) => execFileSync('git', args, { cwd, env, stdio: 'pipe' })
    git(dirname(repo), 'init', '-q', '--bare', '-b', 'main', origin)
    git(repo, 'remote', 'add', 'origin', origin)
    git(repo, 'push', '-q', '-u', 'origin', 'main')

    await openBoard(page, omo)
    const { n } = await startWork(omo)
    await page.goto(omo.url(`/${n}?tab=diff`))
    const heading = panel(page).locator('[data-diff-heading]')
    await expect(heading).toContainText(/since it left main/)
    // Equal: nothing to say.
    await expect(panel(page).locator('[data-diff-remote]')).toHaveCount(0)

    // Someone else pushes four commits; the project's repository fetches them, its main stays.
    const other = join(dirname(repo), 'other')
    git(dirname(repo), 'clone', '-q', origin, other)
    for (let i = 1; i <= 4; i++) commitInto(other, { [`upstream-${i}.txt`]: `upstream ${i}\n` }, `feat: upstream ${i}`)
    git(other, 'push', '-q', 'origin', 'main')
    git(repo, 'fetch', '-q', 'origin')

    const requests: string[] = []
    page.on('request', (r) => {
      if (r.url().includes(`/api/tickets/${n}/diff`)) requests.push(r.url())
    })
    await panel(page).locator('[data-diff-refresh]').click()
    await expect(panel(page).locator('[data-diff-behind]')).toHaveText(
      'local main is 4 commits behind origin/main, so changes from origin/main can show up here as changes of this ticket.',
    )
    const toggle = panel(page).getByRole('switch', { name: 'Compare with origin/main' })
    await expect(toggle).toHaveAttribute('aria-checked', 'false')
    await toggle.click()
    await expect(toggle).toHaveAttribute('aria-checked', 'true')
    await expect(heading).toContainText(/since it left the remote-tracking branch origin\/main/)
    await expect(panel(page).locator('[data-diff-compared]')).toHaveText(
      "Compared with origin/main, the remote-tracking branch of main, which local main is 4 commits behind, as of the repository's last fetch.",
    )
    expect(requests.some((u) => new URL(u).searchParams.get('base') === 'remote')).toBe(true)

    // Kept when the tab reads again and when it is opened again; not saved anywhere.
    await panel(page).locator('[data-diff-refresh]').click()
    await expect(heading).toContainText(/since it left the remote-tracking branch origin\/main/)
    await panel(page).getByRole('tab', { name: /Comments/ }).click()
    await panel(page).getByRole('tab', { name: /Diff/ }).click()
    await expect(heading).toContainText(/since it left the remote-tracking branch origin\/main/)
    await expect(panel(page).getByRole('switch', { name: 'Compare with origin/main' })).toHaveAttribute('aria-checked', 'true')

    const results = await new AxeBuilder({ page }).include('dialog[data-ticket-panel]').analyze()
    expect(results.violations).toEqual([])

    // Back to the local base.
    await panel(page).getByRole('switch', { name: 'Compare with origin/main' }).click()
    await expect(heading).toContainText(/since it left main/)
    await expect(panel(page).locator('[data-diff-behind]')).toBeVisible()
  })

  test('at 390 px a long line scrolls inside its file, not the page', async ({ page, omo }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await openBoard(page, omo)
    const { n } = await startWork(omo)
    await page.goto(omo.url(`/${n}?tab=diff`))
    const body = fileIn(page, 'committed', 'long.txt').getByRole('region')
    await expect(body).toBeVisible()
    await expect(body.locator('[data-row="add"] [data-text]')).toHaveText(LONG_LINE)
    expect(await body.evaluate((el) => el.scrollWidth > el.clientWidth)).toBe(true)
    expect(await sideways(page)).toBe(false)
  })
})

test('a story lists each project; a project not started says so', async ({ page, omo, repo }) => {
  const second = join(dirname(repo), 'second')
  initRepo(second)
  const p = await omo.api<{ id: string }>('POST', '/api/projects', { path: second, name: 'second' })
  const story = await omo.createTicket({ type: 'story', title: 'Two places', projects: [omo.project, p.id] })
  await openBoard(page, omo)
  await page.goto(omo.url(`/${story}?tab=diff`))

  const projects = panel(page).getByRole('tablist', { name: `Projects of #${story}` })
  // Nothing changed yet: no count.
  await expect(projects.getByRole('tab')).toHaveText(['demo', 'second'])
  await expect(panel(page).locator('[data-diff-project]')).toHaveAttribute('aria-label', 'demo')
  await expect(panel(page).locator('[data-diff-state="not_started"]')).toHaveText('Work on this ticket has not started in this project.')

  await projects.getByRole('tab', { name: /second/ }).click()
  await expect(panel(page).locator('[data-diff-project]')).toHaveAttribute('aria-label', 'second')
  await expect(panel(page).locator('[data-diff-state="not_started"]')).toBeVisible()
})
