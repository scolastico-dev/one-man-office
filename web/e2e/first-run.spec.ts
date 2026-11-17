import { accessSync, chmodSync, constants, mkdtempSync, realpathSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { delimiter, join } from 'node:path'
import type { Page } from '@playwright/test'
import { expectCardIn } from './support/board'
import { expect, FAKE_AGENT_BINARY, test } from './support/omo'

// A new user's first start, end to end: omo on a fresh home finds an agent
// program on PATH, prints its address, the board shows the first steps, the
// user adds a project and creates a task, and an agent works it to Done.
//
// The agent program is this test's own `claude`: a script that runs the
// scripted fake agent (its built-in scripts), so detection writes the real
// Claude Code profile and the roles use it, and no real program runs.
//
// OMO_SCREENSHOTS=<folder> also writes first-run-*.png there.

const SHOTS = process.env.OMO_SCREENSHOTS ?? ''

const programs = mkdtempSync(join(realpathSync(tmpdir()), 'omo-e2e-first-run-'))
writeFileSync(
  join(programs, 'claude'),
  [
    '#!/bin/sh',
    'export OMO_FAKEAGENT=1 GIT_AUTHOR_NAME=Agent GIT_AUTHOR_EMAIL=agent@example.invalid GIT_COMMITTER_NAME=omo GIT_COMMITTER_EMAIL=omo@example.invalid',
    `exec '${FAKE_AGENT_BINARY}' "$@"`,
    '',
  ].join('\n'),
)
chmodSync(join(programs, 'claude'), 0o755)
symlinkSync(findGit(), join(programs, 'git'))
test.afterAll(() => rmSync(programs, { recursive: true, force: true }))

function findGit(): string {
  for (const dir of (process.env.PATH ?? '').split(delimiter)) {
    try {
      accessSync(join(dir, 'git'), constants.X_OK)
      return join(dir, 'git')
    } catch {
      // not here
    }
  }
  throw new Error('git is not on the PATH of the test runner')
}

test.use({ omoOptions: { firstStart: true, path: programs, noProject: true, paused: false }, viewport: { width: 1440, height: 1200 } })
test.skip(process.platform === 'win32', 'the fake agent program is a shell script')

const steps = (page: Page) => page.getByRole('list', { name: 'First steps' })
const step = (page: Page, title: string) => steps(page).getByRole('listitem').filter({ has: page.getByRole('heading', { name: title }) })

async function shot(page: Page, name: string) {
  if (SHOTS !== '') await page.screenshot({ path: join(SHOTS, `first-run-${name}.png`), fullPage: true })
}

test('a new user goes from the printed address to a first task done by an agent', async ({ page, omo, repo }) => {
  test.setTimeout(120_000)
  // omo says what it found and prints the address with the login key.
  expect(omo.output()).toContain(`agent detection: found claude at ${join(programs, 'claude')}`)
  expect(omo.output()).toMatch(/^omo: http:\/\/omo\.localhost:\d+\/#[0-9a-f]{64}$/m)
  expect(omo.output()).toContain('Open the address in your browser. The part after # is your login key: keep it to yourself.')

  // The first page: the steps, the agent setup already done by the detection.
  await page.goto(omo.accessUrl)
  await expect(steps(page).getByRole('listitem')).toHaveCount(4)
  await expect(step(page, 'Check the agent setup')).toContainText('done')
  await expect(step(page, 'Add a project')).not.toContainText('done')
  await expect(step(page, 'Create a first ticket')).toContainText('It waits in Planning until you approve it')
  // The CEO is on and its tickets wait for the user's approval by default.
  await expect(step(page, 'Create a first ticket')).toContainText('So do tickets the CEO drafts.')
  await shot(page, '1-board')

  // A project.
  await step(page, 'Add a project').getByRole('button', { name: 'Add project' }).click()
  const add = page.getByRole('dialog', { name: 'Add project' })
  await add.getByLabel('Folder').fill(repo)
  await add.getByRole('button', { name: 'Add project' }).click()
  await expect(add).toBeHidden()
  await expect(step(page, 'Add a project')).toContainText('done')
  await shot(page, '2-project')

  // A task: it starts in Planning and the steps are gone.
  await step(page, 'Create a first ticket').getByRole('button', { name: 'Create first ticket' }).click()
  const dialog = page.getByRole('dialog', { name: 'Create ticket' })
  await dialog.getByRole('radio', { name: 'Task' }).check({ force: true })
  await dialog.getByLabel('Title').fill('Add a hello file')
  await dialog.getByRole('button', { name: 'Create', exact: true }).click()
  await expectCardIn(page, 1, 'Planning')
  await expect(steps(page)).toBeHidden()
  await shot(page, '3-planning')

  // Approve gives the green light; a developer and then a reviewer work it.
  await page.locator('li[data-ticket="1"]').click()
  const panel = page.locator('[data-ticket-panel]')
  await expect(panel.getByText('Gives the green light: it moves to Ready For Dev, where an agent picks it up.')).toBeVisible()
  await panel.getByRole('button', { name: 'Approve', exact: true }).click()
  await expectCardIn(page, 1, 'Review')
  await expect(panel.locator('[data-ticket-badge]')).toHaveText('your review', { timeout: 30_000 })
  await shot(page, '4-review')

  // The user's review: approve and merge.
  await panel.getByRole('button', { name: 'Approve and merge…' }).click()
  const approve = page.getByRole('dialog', { name: 'Approve #1?' })
  await approve.getByRole('button', { name: 'Approve and merge' }).click()
  await expectCardIn(page, 1, 'Done')
  await shot(page, '5-done')
  // The agent's work is on the project's branch.
  const ticket = await omo.api<{ column: string }>('GET', '/api/tickets/1')
  expect(ticket.column).toBe('done')
})
