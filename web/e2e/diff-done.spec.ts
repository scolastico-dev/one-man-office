// The diff of a done ticket after its workspace is gone: the fake developer
// commits and hands in, omo squashes the work into main, removes the
// worktree and deletes the branch; git then drops every object no ref
// reaches. The Diff tab still shows the reviewed change from the commits
// omo recorded when the ticket went to Done, and the Branch field names
// them. With OMO_SCREENSHOTS=<folder> the test writes
// diff-range-{branch,diff}-<theme>-1440.png there.
import AxeBuilder from '@axe-core/playwright'
import { execFileSync } from 'node:child_process'
import { existsSync, mkdirSync } from 'node:fs'
import { devNull } from 'node:os'
import { join } from 'node:path'
import type { Page } from '@playwright/test'
import { expectCardIn, openBoard } from './support/board'
import { expectNoAgentProcesses, script, waitForNoLiveSession, waitForSession } from './support/agents'
import { expect, test } from './support/omo'

const SHOTS = process.env.OMO_SCREENSHOTS ?? ''

const panel = (page: Page) => page.locator('dialog[data-ticket-panel]')

interface TicketAnswer {
  column: string
  projects: { project: string; branch: string; worktree: string; start_commit?: string; end_commit?: string }[]
}

test.use({
  viewport: { width: 1440, height: 900 },
  omoOptions: {
    agents: { developer: script('ready', 'write|recorded.txt|kept after gc', 'commit|feat: add recorded', 'done|finished', 'hang') },
  },
})

test('a done ticket shows its recorded diff after its worktree and branch are gone and git collected garbage', async ({ page, omo, repo }) => {
  const env = { ...process.env, GIT_CONFIG_GLOBAL: devNull, GIT_CONFIG_SYSTEM: devNull }
  const git = (...args: string[]) => execFileSync('git', args, { cwd: repo, env, stdio: 'pipe' }).toString().trim()

  await omo.api('PATCH', '/api/settings', { merge_defaults: { task: 'squash' } })
  await openBoard(page, omo)
  const start = git('rev-parse', 'main')
  const n = await omo.createTicket({ title: 'Add the recorded file' })
  await omo.api('PATCH', `/api/tickets/${n}`, { review: 'skipped' })
  await omo.moveTicket(n, 'ready')
  const worktree = (await waitForSession(omo, n, 'developer')).workdir
  const branch = (await omo.api<TicketAnswer>('GET', `/api/tickets/${n}`)).projects[0]!.branch
  expect(branch).not.toBe('')

  await expectCardIn(page, n, 'Done')
  await waitForNoLiveSession(omo, n)
  await expect.poll(() => existsSync(worktree), { message: 'the worktree stayed', timeout: 30_000 }).toBe(false)
  const done = await omo.api<TicketAnswer>('GET', `/api/tickets/${n}`)
  const p = done.projects[0]!
  expect(p.start_commit).toBe(start)
  expect(p.end_commit).toMatch(/^[0-9a-f]{40}$/)
  await expect.poll(() => git('branch', '--list', branch), { message: 'the branch stayed', timeout: 30_000 }).toBe('')
  // Squashed: main does not contain the reviewed commit; only omo's ref keeps it.
  expect(() => git('merge-base', '--is-ancestor', p.end_commit!, 'main')).toThrow()
  git('reflog', 'expire', '--expire=now', '--all')
  git('gc', '-q', '--prune=now')
  expect(git('cat-file', '-t', p.end_commit!)).toBe('commit')

  await page.goto(omo.url(`/${n}?tab=diff`))
  const heading = panel(page).locator('[data-diff-heading]')
  await expect(heading).toHaveText(`Changes as recorded when the ticket was done: ${start.slice(0, 12)}..${p.end_commit!.slice(0, 12)}`)
  const file = panel(page).locator('[data-diff-file]', { has: page.locator('[data-path]', { hasText: 'recorded.txt' }) })
  await expect(file.locator('[data-status]')).toContainText('added')
  await expect(file.locator('[data-row="add"] [data-text]')).toHaveText('kept after gc')
  await expect(panel(page).locator('[data-diff-section="uncommitted"]')).toHaveCount(0)

  // The Branch field says the branch went and names the two commits.
  await expect(panel(page).locator('[data-field="branch"] [data-branch]')).toContainText('Deleted after the merge')
  const commits = panel(page).locator('[data-field="branch"] [data-commits]')
  await expect(commits).toHaveText(`Diff recorded when done: ${start.slice(0, 12)}..${p.end_commit!.slice(0, 12)}`)

  for (const colorScheme of ['dark', 'light'] as const) {
    await page.emulateMedia({ colorScheme })
    const results = await new AxeBuilder({ page }).include('dialog[data-ticket-panel]').analyze()
    expect(results.violations, colorScheme).toEqual([])
    if (SHOTS !== '') {
      mkdirSync(SHOTS, { recursive: true })
      await commits.scrollIntoViewIfNeeded()
      await page.screenshot({ path: join(SHOTS, `diff-range-branch-${colorScheme}-1440.png`) })
      await heading.scrollIntoViewIfNeeded()
      await page.screenshot({ path: join(SHOTS, `diff-range-diff-${colorScheme}-1440.png`) })
    }
  }
  await expectNoAgentProcesses(omo)
})
