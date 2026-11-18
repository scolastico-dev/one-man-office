// Hidden lines of a diff: between the changes, above the first and below the
// last, and the whole file, read from the real server, on the ticket page and
// in the panel, for committed and uncommitted files, at 390 px, in both
// themes. With OMO_SCREENSHOTS=<folder> the page test writes
// diff-expand-<theme>-<width>.png there.
import { withoutShellConfig } from './support/env'
import AxeBuilder from '@axe-core/playwright'
import { execFileSync } from 'node:child_process'
import { existsSync, mkdirSync, writeFileSync } from 'node:fs'
import { devNull } from 'node:os'
import { dirname, join } from 'node:path'
import type { Locator, Page } from '@playwright/test'
import { expect, test, type Omo } from './support/omo'
import { expectNoAgentProcesses, script, waitForSession } from './support/agents'
import { openBoard } from './support/board'

const SHOTS = process.env.OMO_SCREENSHOTS ?? ''

const ticketPage = (page: Page) => page.locator('[data-ticket-page]')
const filePane = (page: Page) => ticketPage(page).locator('[data-file-pane]')
const tree = (page: Page) => ticketPage(page).locator('[data-diff-tree]')
const panel = (page: Page) => page.locator('dialog[data-ticket-panel]')

const gitEnv = () => ({ ...withoutShellConfig(), GIT_CONFIG_GLOBAL: devNull, GIT_CONFIG_SYSTEM: devNull })

function writeInto(dir: string, files: Record<string, string>): void {
  for (const [name, body] of Object.entries(files)) {
    mkdirSync(dirname(join(dir, name)), { recursive: true })
    writeFileSync(join(dir, name), body)
  }
}

function commitInto(dir: string, files: Record<string, string>, message: string): void {
  writeInto(dir, files)
  const git = (...args: string[]) => execFileSync('git', args, { cwd: dir, env: gitEnv(), stdio: 'pipe' })
  git('add', '--', ...Object.keys(files))
  git('-c', 'user.name=e2e', '-c', 'user.email=e2e@example.invalid', '-c', 'commit.gpgsign=false', 'commit', '-q', '--no-verify', '-m', message)
}

/** `n` lines "line i" of code, the lines in `changed` changed. */
function numbered(n: number, changed: number[] = []): string {
  return Array.from({ length: n }, (_, i) => `const line${i + 1} = ${changed.includes(i + 1) ? `'changed ${i + 1}'` : i + 1}`).join('\n') + '\n'
}

/** About 1.9 MiB in 48-byte lines: too large to show whole. */
function huge(changed: boolean): string {
  return Array.from({ length: 40000 }, (_, i) => (changed && i === 100 ? 'changed\n' : `line ${String(i).padStart(7, '0')} of a file larger than the limit\n`)).join('')
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

/** A task whose branch changed lines 50 and 250 of a 300-line file, with an uncommitted change in another file. */
async function startWork(omo: Omo, repo: string): Promise<number> {
  commitInto(repo, { 'src/long.ts': numbered(300), 'notes.txt': numbered(100), 'huge.txt': huge(false) }, 'chore: files to change')
  const n = await omo.createTicket({ title: 'Change a long file' })
  await omo.moveTicket(n, 'ready')
  await waitForSession(omo, n, 'developer')
  const dir = await worktreeOf(omo, n)
  commitInto(dir, { 'src/long.ts': numbered(300, [50, 250]), 'huge.txt': huge(true) }, 'feat: change two lines')
  writeInto(dir, { 'notes.txt': numbered(100, [60]) })
  return n
}

/** The new-side numbers of the context lines shown in a file. */
const newNumbers = (where: Locator) => where.locator('[data-row="ctx"]').evaluateAll((rows) => rows.map((r) => Number(r.querySelectorAll('[data-no]')[1]!.textContent)))

async function expectNoAxeViolations(page: Page, include: string, label: string): Promise<void> {
  for (const colorScheme of ['dark', 'light'] as const) {
    await page.emulateMedia({ colorScheme })
    await page.evaluate('document.fonts.ready')
    const results = await new AxeBuilder({ page }).include(include).analyze()
    expect(results.violations, `${label}, ${colorScheme}`).toEqual([])
  }
}

test.describe('hidden lines of a diff', () => {
  test.use({ omoOptions: { agents: { developer: script('ready', 'hang') } } })

  test('on the page: between the changes, above and below, the rest of the file, by keyboard; kept on Refresh', async ({ page, omo, repo }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    const n = await startWork(omo, repo)
    await openBoard(page, omo)
    await page.goto(omo.url(`/ticket/${n}?tab=diff`))
    await tree(page).locator('[data-tree-file="src/long.ts"]').click()
    const file = filePane(page).locator('[data-diff-file]')
    await expect(file.locator('[data-path]')).toHaveText('src/long.ts')
    await expect(file.locator('[data-gap]')).toHaveCount(3)
    expect(await newNumbers(file)).toEqual([47, 48, 49, 51, 52, 53, 247, 248, 249, 251, 252, 253])

    // Twenty below the first change.
    await file.getByRole('button', { name: 'Show 20 hidden lines below line 53 of src/long.ts' }).click()
    await expect(file.locator('[data-row="ctx"]')).toHaveCount(32)
    const row73 = file.locator('[data-row="ctx"]').filter({ hasText: 'const line73 = 73' })
    await expect(row73.locator('[data-no]')).toHaveText(['73', '73'])
    // Twenty above the second, by keyboard; the row that stays there keeps the focus.
    const above = file.getByRole('button', { name: 'Show 20 hidden lines above line 247 of src/long.ts' })
    await above.focus()
    await page.keyboard.press('Enter')
    await expect(file.locator('[data-row="ctx"]')).toHaveCount(52)
    await expect(file.getByRole('button', { name: 'Show 20 hidden lines above line 227 of src/long.ts' })).toBeFocused()
    await page.keyboard.press('Shift+Tab')
    await expect(file.getByRole('button', { name: 'Show 20 hidden lines below line 73 of src/long.ts' })).toBeFocused()
    await page.keyboard.press('Tab')
    await page.keyboard.press('Tab')
    await expect(file.getByRole('button', { name: 'Show all 153 hidden lines between lines 73 and 227 of src/long.ts' })).toBeFocused()
    await page.keyboard.press('Enter')
    await expect(file.locator('[data-gap]')).toHaveCount(2)
    // Above the first change, and the rest of the file below the last.
    await file.getByRole('button', { name: 'Show all 46 hidden lines above line 47 of src/long.ts' }).click()
    // The wider read reached the file's end, so the lines below are counted now.
    await file.getByRole('button', { name: 'Show all 47 hidden lines below line 253 of src/long.ts' }).click()
    await expect(file.locator('[data-gap]')).toHaveCount(0)
    expect(await newNumbers(file)).toEqual(Array.from({ length: 300 }, (_, i) => i + 1).filter((i) => i !== 50 && i !== 250))
    await expect(file.locator('[data-whole-file]')).toHaveCount(0)

    // Refresh reads the diff again; the file is unchanged, so it stays open as it is.
    const reread = page.waitForResponse((r) => new URL(r.url()).pathname === `/api/tickets/${n}/diff`)
    await ticketPage(page).locator('[data-diff-refresh]').click()
    await reread
    await expect(ticketPage(page).locator('[data-diff-refresh]')).toHaveText('Refresh')
    await expect(file.locator('[data-gap]')).toHaveCount(0)
    await expect(file.locator('[data-row="ctx"]')).toHaveCount(298)
    // Previous and Next keep working, and coming back finds the file as it was.
    await filePane(page).getByRole('button', { name: 'Next file' }).click()
    await expect(file.locator('[data-path]')).not.toHaveText('src/long.ts')
    await tree(page).locator('[data-tree-file="src/long.ts"]').click()
    await expect(file.locator('[data-row="ctx"]')).toHaveCount(298)

    await omo.api('POST', '/api/agents/stop-all', {})
    await expectNoAgentProcesses(omo)
  })

  test('the whole of an uncommitted file; a file too large to show whole says so in place', async ({ page, omo, repo }) => {
    const n = await startWork(omo, repo)
    await openBoard(page, omo)
    await page.goto(omo.url(`/ticket/${n}?tab=diff`))
    const uncommitted = tree(page).locator('[data-tree-group="uncommitted"]')
    await expect(uncommitted).toBeVisible()
    await tree(page).locator('[data-tree-file="notes.txt"]').click()
    const file = filePane(page).locator('[data-diff-file]')
    await expect(filePane(page).locator('[data-diff-section="uncommitted"]')).toBeVisible()
    await file.getByRole('button', { name: 'Show the whole file notes.txt' }).click()
    await expect(file.locator('[data-gap]')).toHaveCount(0)
    await expect(file.locator('[data-row="ctx"]')).toHaveCount(99)

    await tree(page).locator('[data-tree-file="huge.txt"]').click()
    await expect(file.locator('[data-path]')).toHaveText('huge.txt')
    await file.getByRole('button', { name: 'Show 20 more lines below line 104 of huge.txt' }).click()
    await expect(file.locator('[data-row="ctx"]')).toHaveCount(26)
    await file.getByRole('button', { name: 'Show the whole file huge.txt' }).click()
    await expect(file.locator('[data-expand-tools] [role="alert"]')).toContainText('too large to show with that much context')
    // What was shown stays.
    await expect(file.locator('[data-row="ctx"]')).toHaveCount(26)
    await omo.api('POST', '/api/agents/stop-all', {})
    await expectNoAgentProcesses(omo)
  })

  test('in the panel: the stacked diff expands too, and reads well in both themes', async ({ page, omo, repo }) => {
    const n = await startWork(omo, repo)
    await openBoard(page, omo)
    await page.goto(omo.url(`/${n}?tab=diff`))
    const file = panel(page).locator('[data-diff-section="committed"] [data-diff-file]').filter({ has: page.locator('[data-path]', { hasText: /^src\/long\.ts$/ }) })
    await expect(file.locator('[data-gap]')).toHaveCount(3)
    await file.getByRole('button', { name: 'Show all 193 hidden lines between lines 53 and 247 of src/long.ts' }).click()
    await expect(file.locator('[data-gap]')).toHaveCount(2)
    await expect(file.locator('[data-row="ctx"]')).toHaveCount(205)
    await expectNoAxeViolations(page, 'dialog[data-ticket-panel]', 'panel')
    await omo.api('POST', '/api/agents/stop-all', {})
    await expectNoAgentProcesses(omo)
  })

  for (const width of [1440, 390] as const) {
    test(`at ${width} px the expanders read well: no sideways scroll, no accessibility violation in either theme`, async ({ page, omo, repo }) => {
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 })
      const n = await startWork(omo, repo)
      await openBoard(page, omo)
      await page.goto(omo.url(`/ticket/${n}?tab=diff`))
      if (width === 390) {
        await ticketPage(page).getByRole('button', { name: /^Files \(/ }).click()
        await page.getByRole('dialog', { name: /^Files \(/ }).locator('[data-tree-file="src/long.ts"]').click()
      } else await tree(page).locator('[data-tree-file="src/long.ts"]').click()
      const file = filePane(page).locator('[data-diff-file]')
      await file.getByRole('button', { name: 'Show 20 hidden lines below line 53 of src/long.ts' }).click()
      await expect(file.locator('[data-row="ctx"]')).toHaveCount(32)
      expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBe(0)
      await expectNoAxeViolations(page, '[data-ticket-page]', `page at ${width} px`)
      if (SHOTS !== '') {
        mkdirSync(SHOTS, { recursive: true })
        await file.locator('[data-gap="between"]').scrollIntoViewIfNeeded()
        for (const theme of ['dark', 'light'] as const) {
          await page.emulateMedia({ colorScheme: theme })
          await page.evaluate('document.fonts.ready')
          await page.screenshot({ path: join(SHOTS, `diff-expand-${theme}-${width}.png`) })
        }
      }
      await omo.api('POST', '/api/agents/stop-all', {})
      await expectNoAgentProcesses(omo)
    })
  }
})
