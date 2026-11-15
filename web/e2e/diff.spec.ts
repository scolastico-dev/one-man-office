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
  const env = { ...process.env, GIT_CONFIG_GLOBAL: devNull, GIT_CONFIG_SYSTEM: devNull }
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
  await expect(projects.getByRole('tab')).toHaveText(['demo 0', 'second 0'])
  await expect(panel(page).locator('[data-diff-project]')).toHaveAttribute('aria-label', 'demo')
  await expect(panel(page).locator('[data-diff-state="not_started"]')).toHaveText('Work on this ticket has not started in this project.')

  await projects.getByRole('tab', { name: /second/ }).click()
  await expect(panel(page).locator('[data-diff-project]')).toHaveAttribute('aria-label', 'second')
  await expect(panel(page).locator('[data-diff-state="not_started"]')).toBeVisible()
})
