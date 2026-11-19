// A story with two projects has one reviewer per project. The comment box
// offers `@reviewer:<project id>` for each besides `@reviewer`, and a mention
// of one project's reviewer reaches that reviewer only.
import { withoutShellConfig } from './support/env'
import { execFileSync } from 'node:child_process'
import { writeFileSync } from 'node:fs'
import { devNull } from 'node:os'
import { dirname, join } from 'node:path'
import type { Page } from '@playwright/test'
import { card, openBoard } from './support/board'
import { script } from './support/agents'
import { initRepo } from './support/git'
import { expect, test } from './support/omo'

const panel = (page: Page) => page.locator('dialog[data-ticket-panel]')
const box = (page: Page) => panel(page).getByLabel('Write a comment')

test.use({
  omoOptions: {
    agents: {
      pm: script(
        'ready',
        'create-tasks|1',
        'release-all',
        'until-tasks-done|10m',
        'git|-C|../../second-place/$TICKET|rm|-q|keep.txt',
        'git|-C|../../second-place/$TICKET|commit|-q|-m|story change in the second project',
        'write|story-note.txt|a note',
        'commit|story change in the first project',
        'done|story complete',
        'wait-loop|10m',
      ),
      reviewer: script('ready', 'verdict|approve|fine', 'wait-loop|10m|answer from $NAME'),
    },
  },
})

test('on a story with two projects the composer offers each project\'s reviewer and the mention reaches that one', async ({ page, omo, repo }) => {
  const second = join(dirname(repo), 'second')
  initRepo(second)
  // A file the story's product manager removes, so the second project has a change to review.
  writeFileSync(join(second, 'keep.txt'), 'keep\n')
  const git = (...args: string[]) =>
    execFileSync('git', ['-c', 'user.name=e2e', '-c', 'user.email=e2e@example.invalid', '-c', 'commit.gpgsign=false', ...args], {
      cwd: second,
      env: { ...withoutShellConfig(), GIT_CONFIG_GLOBAL: devNull, GIT_CONFIG_SYSTEM: devNull },
    })
  git('add', 'keep.txt')
  git('commit', '-q', '-m', 'add keep.txt')
  const other = await omo.api<{ id: string }>('POST', '/api/projects', { path: second, name: 'Second Place' })
  const story = await omo.createTicket({ type: 'story', title: 'Two places', projects: [omo.project, other.id] })
  await omo.moveTicket(story, 'ready')

  // Each project has its own reviewer; both stay after their verdict.
  let reviewers: { id: string; name: string; state: string }[] = []
  await expect
    .poll(async () => {
      const all = await omo.api<{ ticket: number | null; role: string; id: string; name: string; state: string; project?: string }[]>('GET', '/api/agents?live=true')
      reviewers = all.filter((s) => s.ticket === story && s.role === 'reviewer')
      return reviewers.length === 2 && reviewers.every((r) => r.state === 'waiting')
    }, { timeout: 40_000 })
    .toBe(true)
  const live = await omo.api<{ tickets: { number: number; agents: { name: string; role: string; project?: string }[] }[] }>('GET', '/api/tickets')
  const agents = live.tickets.find((t) => t.number === story)!.agents.filter((a) => a.role === 'reviewer')
  const ofSecond = agents.find((a) => a.project === other.id)!
  const ofFirst = agents.find((a) => a.project === omo.project)!
  expect(ofSecond.name).not.toBe(ofFirst.name)

  await openBoard(page, omo)
  await page.goto(omo.url(`/${story}`))
  await expect(panel(page)).toBeVisible()
  await expect(card(page, story)).toHaveCount(1)

  // Typing @rev offers @reviewer and one entry per project, with its name.
  await box(page).fill('@rev')
  const options = panel(page).getByRole('listbox', { name: 'Roles to mention' }).getByRole('option')
  await expect(options).toHaveCount(3)
  await expect(options.nth(0)).toHaveText(/^@reviewer\s*$/)
  await expect(options.nth(1)).toContainText(`@reviewer:${omo.project}`)
  await expect(options.nth(1)).toContainText('demo')
  await expect(options.nth(2)).toContainText(`@reviewer:${other.id}`)
  await expect(options.nth(2)).toContainText('Second Place')
  await expect(options.nth(2)).toContainText(ofSecond.name)

  // Choose the second project's entry and ask.
  await options.nth(2).click()
  await expect(box(page)).toHaveValue(`@reviewer:${other.id} `)
  await box(page).pressSequentially('what did you check?')
  await panel(page).locator('[data-send]').click()

  // The comment shows as one mention of that project's reviewer, with its name.
  const mine = panel(page).locator('[data-comment]').filter({ hasText: 'what did you check?' })
  await expect(mine.locator('span.mention')).toHaveCount(1)
  await expect(mine.locator('span.mention')).toHaveText(`@reviewer:${other.id}`)
  await expect(mine.locator('span.mention')).toHaveAttribute('data-project', other.id)
  await expect(mine).toContainText(`@reviewer:${other.id} (${ofSecond.name})`)

  // Only that reviewer answered.
  const answer = panel(page).locator('[data-comment]').filter({ hasText: 'answer from' })
  await expect(answer).toHaveCount(1)
  await expect(answer).toContainText(`answer from ${ofSecond.name}`)
  await expect(panel(page).getByText(`answer from ${ofFirst.name}`)).toHaveCount(0)

  // @reviewer alone reaches both.
  await box(page).fill('@reviewer anything to add?')
  await panel(page).locator('[data-send]').click()
  await expect(panel(page).getByText(`answer from ${ofFirst.name}`)).toHaveCount(1)
  await expect(panel(page).locator('[data-comment]').filter({ hasText: 'answer from' })).toHaveCount(3)

  // A project that is not the ticket's is refused with the server's sentence.
  await box(page).fill('@reviewer:nowhere hello')
  await panel(page).locator('[data-send]').click()
  await expect(panel(page)).toContainText('names project "nowhere"')
})
