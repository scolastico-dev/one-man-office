import { join } from 'node:path'
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

// A native launcher: a shell script where programs are found by their mode bit, a .cmd file where Windows finds them by extension.
const WINDOWS = process.platform === 'win32'
const CLAUDE_FILE = WINDOWS ? 'claude.cmd' : 'claude'
const AGENT_ENV = { OMO_FAKEAGENT: '1', GIT_AUTHOR_NAME: 'Agent', GIT_AUTHOR_EMAIL: 'agent@example.invalid', GIT_COMMITTER_NAME: 'omo', GIT_COMMITTER_EMAIL: 'omo@example.invalid' }
const CLAUDE = WINDOWS
  ? ['@echo off', ...Object.entries(AGENT_ENV).map(([k, v]) => `set ${k}=${v}`), `"${FAKE_AGENT_BINARY}" %*`, ''].join('\r\n')
  : ['#!/bin/sh', `export ${Object.entries(AGENT_ENV).map(([k, v]) => `${k}=${v}`).join(' ')}`, `exec '${FAKE_AGENT_BINARY}' "$@"`, ''].join('\n')

test.use({ omoOptions: { firstStart: true, programs: { files: { [CLAUDE_FILE]: CLAUDE }, git: true }, noProject: true, paused: false }, viewport: { width: 1440, height: 1200 } })

const steps = (page: Page) => page.getByRole('list', { name: 'First steps' })
const step = (page: Page, title: string) => steps(page).getByRole('listitem').filter({ has: page.getByRole('heading', { name: title }) })

async function shot(page: Page, name: string) {
  if (SHOTS !== '') await page.screenshot({ path: join(SHOTS, `first-run-${name}.png`), fullPage: true })
}

test('a new user goes from the printed address to a first task done by an agent', async ({ page, omo, repo }) => {
  test.setTimeout(120_000)
  // omo says what it found and prints the address with the login key.
  expect(omo.output()).toContain(`agent detection: found claude at ${join(omo.programs, CLAUDE_FILE)}`)
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
  await expect(panel.getByText('Gives the green light: it moves to Ready For Dev, where an agent starts on it as soon as nothing holds it back.')).toBeVisible()
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
