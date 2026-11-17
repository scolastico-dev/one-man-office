import { execFileSync } from 'node:child_process'
import { existsSync, mkdirSync, readFileSync, rmSync } from 'node:fs'
import { devNull, tmpdir } from 'node:os'
import { join } from 'node:path'
import type { Page } from '@playwright/test'
import { script } from './support/agents'
import { card, openBoard } from './support/board'
import { expect, test, type Omo } from './support/omo'

// Export against the real server, real agents (the fake agent) and a remote
// the test makes in its own home. The project's origin is a forge address;
// git (omo's own and the agents', through the agent environment) sends it to
// a bare repository in the test's home, so nothing reaches the network. The
// developer works as the fake's built-in script does and hands in; the
// export job reports https://example.invalid/you/app/pull/<n>, which is on
// the pushed remote's host inside its repository path.

// The export job's prompt is longer than its screen, and the transcript of a
// session keeps what a snapshot of the screen saw, so the head of a prompt
// printed in one go may be missing from the log. The job saves its prompt
// instead, and the tests read the file.
const PROMPTS = join(tmpdir(), `omo-export-prompts-${process.pid}`)
test.beforeAll(() => mkdirSync(PROMPTS, { recursive: true }))
// Tests of one worker number their first ticket alike: an earlier test's file must not be read.
test.beforeEach(() => {
  rmSync(PROMPTS, { recursive: true, force: true })
  mkdirSync(PROMPTS, { recursive: true })
})
test.afterAll(() => rmSync(PROMPTS, { recursive: true, force: true }))
const promptFile = (n: number) => join(PROMPTS, `prompt-${n}.txt`)
/** The saved prompt of the export job of ticket `n`, once the job has saved it. */
async function promptOf(n: number): Promise<string> {
  await expect.poll(() => existsSync(promptFile(n)), { message: `waiting for the export job of #${n} to save its prompt` }).toBe(true)
  return readFileSync(promptFile(n), 'utf8')
}

test.use({
  viewport: { width: 1440, height: 900 },
  omoOptions: { agents: { export: script('ready', `save-prompt|${join(PROMPTS, 'prompt-$TICKET.txt')}`, 'done-url|https://example.invalid/you/app/pull/$TICKET', 'wait|10m') } },
})

const FORGE = 'https://example.invalid/you/app.git'
/** The same remote with a user and password in it, to check that no credentials are shown. */
const WITH_SECRET = 'https://exporter:s3cret-pass@example.invalid/you/app.git'
const SECRET = 's3cret-pass'

const gitEnv = { ...process.env, GIT_CONFIG_GLOBAL: devNull, GIT_CONFIG_SYSTEM: devNull }
const git = (cwd: string, ...args: string[]) => execFileSync('git', args, { cwd, env: gitEnv, stdio: 'pipe' }).toString().trim()

/** A bare repository in the test's home. */
function bare(omo: Omo, name = 'origin.git'): string {
  const dir = join(omo.home, name)
  git(omo.home, 'init', '-q', '--bare', dir)
  return dir
}

/** The commit `branch` points at in `repo`, or "" when it has no such branch. */
function head(repo: string, branch: string): string {
  try {
    return git(repo, 'rev-parse', '--verify', '-q', `refs/heads/${branch}`)
  } catch {
    return ''
  }
}

/** Sends git's pushes to `remote` (an address as the project names it) to `target` instead, for omo and its agents. */
async function sendGitTo(omo: Omo, remote: string, target: string) {
  const res = await fetch(`${omo.base}/api/settings`, { headers: { Authorization: `Bearer ${omo.token}` } })
  const now = (await res.json()) as { settings: { agent_env: Record<string, string> }; revision: string }
  const env = { ...now.settings.agent_env, GIT_CONFIG_COUNT: '1', GIT_CONFIG_KEY_0: `url.${target}.insteadOf`, GIT_CONFIG_VALUE_0: remote }
  const put = await fetch(`${omo.base}/api/settings`, {
    method: 'PATCH',
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${omo.token}`, 'If-Match': `"${now.revision}"` },
    body: JSON.stringify({ agent_env: env }),
  })
  if (put.status >= 400) throw new Error(`PATCH /api/settings: ${put.status} ${await put.text()}`)
}

interface Ticket {
  number: number
  column: string
  review_stage: string
  attention: string
  resolution: string
  export_url: string | null
  projects: { project: string; branch: string; export_url?: string }[]
}
const ticketOf = (omo: Omo, n: number) => omo.api<Ticket>('GET', `/api/tickets/${n}`)

/** A task the developer works and hands in; resolves once it waits for the user's review. */
async function reviewed(omo: Omo, title: string): Promise<Ticket> {
  const t = await omo.api<{ number: number }>('POST', '/api/tickets', { type: 'task', title, projects: [omo.project], review: 'human' })
  await omo.moveTicket(t.number, 'ready')
  await expect
    .poll(async () => {
      const now = await ticketOf(omo, t.number)
      return `${now.column}/${now.review_stage}`
    }, { message: `waiting for #${t.number} to wait for the review`, timeout: 30_000 })
    .toBe('review/human')
  return ticketOf(omo, t.number)
}

interface ThreadEvent {
  kind: string
  data: { comment?: { body: string } } | null
}
/** The bodies of the ticket's comments, oldest first. */
async function comments(omo: Omo, n: number): Promise<string[]> {
  const th = await omo.api<{ events: ThreadEvent[] }>('GET', `/api/tickets/${n}/thread?limit=200`)
  return th.events.filter((e) => e.kind === 'comment.created' && e.data?.comment).map((e) => e.data!.comment!.body)
}

const panel = (page: Page) => page.locator('dialog[data-ticket-panel]')

/** Opens the ticket's panel, presses Export… and confirms. */
async function exportFromPanel(page: Page, n: number) {
  await card(page, n).click()
  await expect(panel(page)).toBeVisible()
  await panel(page).locator('[data-action="export"]').click()
  const dialog = page.getByRole('dialog', { name: `Export #${n}?` })
  await expect(dialog).toBeVisible()
  await expect(dialog.locator('[data-export-text-note]')).toHaveText('The pull request text is written per repository by the export agent.')
  await dialog.getByRole('button', { name: 'Export', exact: true }).click()
  await expect(dialog).toBeHidden()
}

test('Export… pushes the reviewed commit, says so, and the export job ends the ticket in Done with its pull request', async ({ page, omo, repo }) => {
  const origin = bare(omo)
  git(repo, 'remote', 'add', 'origin', FORGE)
  await sendGitTo(omo, FORGE, origin)
  const t = await reviewed(omo, 'Export me')
  const n = t.number
  const branch = t.projects[0]!.branch
  const commit = head(repo, branch)
  expect(commit).toMatch(/^[0-9a-f]{40}$/)
  expect(head(origin, branch)).toBe('')

  await openBoard(page, omo)
  await exportFromPanel(page, n)

  // The push: one comment, and the remote has the branch at the reviewed commit.
  const pushed = `Pushed \`${commit.slice(0, 12)}\` to \`origin\` (\`${FORGE}\`) as \`${branch}\`.`
  await expect(panel(page).locator('[data-thread]')).toContainText(`Pushed ${commit.slice(0, 12)} to origin (${FORGE}) as ${branch}.`)
  expect((await comments(omo, n)).filter((c) => c.startsWith('Pushed '))).toEqual([pushed])
  expect(head(origin, branch)).toBe(commit)
  expect(branch).toMatch(/^omo\//)

  // The export job reports its pull request: Done, with the link.
  await expect.poll(async () => (await ticketOf(omo, n)).column, { timeout: 30_000 }).toBe('done')
  const done = await ticketOf(omo, n)
  expect(done.resolution).toBe('exported')
  const link = panel(page).locator('[data-pull-requests] a')
  await expect(link).toHaveText(`demo: https://example.invalid/you/app/pull/${n}`)
  await expect(link).toHaveAttribute('href', `https://example.invalid/you/app/pull/${n}`)

  // The export job worked in its own folder and was told so.
  const sessions = await omo.api<{ id: number; purpose: string }[]>('GET', `/api/tickets/${n}/agents`)
  expect(sessions.find((s) => s.purpose === 'export')).toBeDefined()
  expect(await promptOf(n)).toContain(`Work in your current folder, ${join(omo.home, 'omo', 'run', 'export', `${n}-${t.projects[0]!.project}`)}.`)
  // main did not move.
  expect(head(repo, 'main')).toBe(git(repo, 'rev-parse', 'main'))
})

test('a remote that cannot be reached leaves the ticket in Review with the reason and no credentials; approving again after the fix exports it', async ({ page, omo, repo }) => {
  const origin = bare(omo)
  git(repo, 'remote', 'add', 'origin', WITH_SECRET)
  // Pushes go to a folder that does not exist: the remote cannot be reached.
  await sendGitTo(omo, WITH_SECRET, join(omo.home, 'missing.git'))
  const t = await reviewed(omo, 'Export to nowhere')
  const n = t.number
  const branch = t.projects[0]!.branch

  await openBoard(page, omo)
  await exportFromPanel(page, n)

  const attention = panel(page).locator('[data-attention]')
  await expect(attention).toContainText(`Export of ${t.projects[0]!.project} failed`)
  const now = await ticketOf(omo, n)
  expect(`${now.column}/${now.review_stage}`).toBe('review/human')
  expect(now.attention).toContain('Export of')
  // No credentials anywhere the user reads.
  expect(now.attention).not.toContain(SECRET)
  await expect(panel(page)).not.toContainText(SECRET)
  for (const c of await comments(omo, n)) expect(c).not.toContain(SECRET)
  expect((await omo.api<{ purpose: string }[]>('GET', `/api/tickets/${n}/agents`)).some((s) => s.purpose === 'export')).toBe(false)
  expect(head(origin, branch)).toBe('')

  // The remote can be reached again: approving with export again retries and exports it.
  await sendGitTo(omo, WITH_SECRET, origin)
  await panel(page).locator('[data-action="export"]').click()
  const dialog = page.getByRole('dialog', { name: `Export #${n}?` })
  await dialog.getByRole('button', { name: 'Export', exact: true }).click()
  await expect.poll(async () => (await ticketOf(omo, n)).column, { timeout: 30_000 }).toBe('done')
  expect(head(origin, branch)).toBe(head(repo, branch))
  await expect(panel(page).locator('[data-pull-requests] a')).toHaveText(`demo: https://example.invalid/you/app/pull/${n}`)
  await expect(panel(page)).not.toContainText(SECRET)
})

test.describe('with a remote that is a folder on this machine', () => {
  // A local remote takes no pull request address: the prompt says so, and the
  // export job reports the failure as it is told to.
  const LOCAL = 'the remote is a folder on this machine, not a forge'
  test.use({ omoOptions: { agents: { export: script('ready', `save-prompt|${join(PROMPTS, 'prompt-$TICKET.txt')}`, `done-failed|${LOCAL}`, 'wait|10m') } } })

  test('the push works, the job is told there is no pull request to open, and its failure leaves the ticket in Review', async ({ page, omo, repo }) => {
    const origin = bare(omo)
    git(repo, 'remote', 'add', 'origin', origin)
    const t = await reviewed(omo, 'Export to a folder')
    const n = t.number
    const branch = t.projects[0]!.branch

    await openBoard(page, omo)
    await exportFromPanel(page, n)
    await expect.poll(() => head(origin, branch), { timeout: 30_000 }).toBe(head(repo, branch))
    expect(await comments(omo, n)).toContain(`Pushed \`${head(repo, branch).slice(0, 12)}\` to \`origin\` (\`${origin}\`) as \`${branch}\`.`)

    const attention = panel(page).locator('[data-attention]')
    await expect(attention).toContainText(LOCAL)
    const now = await ticketOf(omo, n)
    expect(`${now.column}/${now.review_stage}`).toBe('review/human')
    expect(now.resolution).toBe('')
    await expect(panel(page).locator('[data-pull-requests]')).toHaveCount(0)

    // The job's prompt said there is no command for a folder.
    expect(await promptOf(n)).toContain('omo has no command to open the pull request here')
  })
})

test('a project without a remote cannot be exported: the ticket stays in Review and the project says why', async ({ page, omo }) => {
  const t = await reviewed(omo, 'Export without a remote')
  const n = t.number
  const id = t.projects[0]!.project

  await openBoard(page, omo)
  await card(page, n).click()
  await expect(panel(page)).toBeVisible()
  await panel(page).locator('[data-action="export"]').click()
  const dialog = page.getByRole('dialog', { name: `Export #${n}?` })
  await expect(dialog.locator('[data-approve-effect]')).toHaveText(
    'omo pushes the branch and opens a pull request; a branch without commits of its own has nothing to export. The ticket is done once the pull request exists.',
  )
  await dialog.getByRole('button', { name: 'Export', exact: true }).click()

  await expect(panel(page).locator('[data-attention]')).toContainText(`Export of ${id} failed`)
  const now = await ticketOf(omo, n)
  expect(`${now.column}/${now.review_stage}`).toBe('review/human')
  const reason = now.attention.replace(`Export of ${id} failed: `, '')
  await expect(panel(page).locator(`[data-branch="${id}"] [data-export-state="failed"]`)).toHaveText(`export failed: ${reason}`)
  await expect(panel(page).locator('[data-pull-requests]')).toHaveCount(0)
})

test.describe('a developer who hands in without a commit', () => {
  test.use({ omoOptions: { agents: { developer: script('ready', 'done|nothing to change') } } })

  test('the export leaves the project out and the ticket is done with nothing to export', async ({ page, omo, repo }) => {
    const origin = bare(omo)
    git(repo, 'remote', 'add', 'origin', FORGE)
    await sendGitTo(omo, FORGE, origin)
    const t = await reviewed(omo, 'Nothing to export')
    const n = t.number

    await openBoard(page, omo)
    await exportFromPanel(page, n)
    await expect.poll(async () => (await ticketOf(omo, n)).column, { timeout: 30_000 }).toBe('done')
    const done = await ticketOf(omo, n)
    expect(done.resolution).toBe('completed')
    await expect(panel(page).locator('[data-thread]')).toContainText(`Nothing to export: no project of #${n} has commits of its own. It is done.`)
    await expect(panel(page).locator('[data-pull-requests]')).toHaveCount(0)
    // Nothing was pushed.
    expect(head(origin, t.projects[0]!.branch)).toBe('')
  })
})
