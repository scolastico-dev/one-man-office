// Features that were built apart and meet on one ticket: images in a comment
// with a ticket link on a ticket that opens as a page, the diff's hidden lines
// beside the remote-base switch, and the settings pages that share one
// settings document and must each save only their own keys.
import { execFileSync } from 'node:child_process'
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { devNull } from 'node:os'
import { dirname, join } from 'node:path'
import type { Page } from '@playwright/test'
import { expect, test, type Omo } from './support/omo'
import { expectNoAgentProcesses, script, waitForSession } from './support/agents'
import { openBoard } from './support/board'
import { uploadImage } from './support/images'

const ticketPage = (page: Page) => page.locator('[data-ticket-page]')
const panel = (page: Page) => page.locator('dialog[data-ticket-panel]')
const filePane = (page: Page) => ticketPage(page).locator('[data-file-pane]')
const tree = (page: Page) => ticketPage(page).locator('[data-diff-tree]')
const lightbox = (page: Page) => page.locator('dialog[data-image-lightbox]')
const bar = (page: Page) => page.getByRole('region', { name: 'Save changes' })
const saveButton = (page: Page) => bar(page).getByRole('button', { name: 'Save', exact: true })

const gitEnv = () => ({ ...process.env, GIT_CONFIG_GLOBAL: devNull, GIT_CONFIG_SYSTEM: devNull })
const git = (cwd: string, ...args: string[]) => execFileSync('git', args, { cwd, env: gitEnv(), stdio: 'pipe' })

function commitInto(dir: string, files: Record<string, string>, message: string): void {
  for (const [name, body] of Object.entries(files)) {
    mkdirSync(dirname(join(dir, name)), { recursive: true })
    writeFileSync(join(dir, name), body)
  }
  git(dir, 'add', '--', ...Object.keys(files))
  git(dir, '-c', 'user.name=e2e', '-c', 'user.email=e2e@example.invalid', '-c', 'commit.gpgsign=false', 'commit', '-q', '--no-verify', '-m', message)
}

const numbered = (n: number, changed: number[] = []) =>
  Array.from({ length: n }, (_, i) => `const line${i + 1} = ${changed.includes(i + 1) ? `'changed ${i + 1}'` : i + 1}`).join('\n') + '\n'

async function worktreeOf(omo: Omo, n: number): Promise<string> {
  let found = ''
  await expect
    .poll(
      async () => {
        const t = await omo.api<{ projects: { worktree: string }[] }>('GET', `/api/tickets/${n}`)
        found = t.projects[0]?.worktree ?? ''
        return found !== '' && existsSync(found)
      },
      { timeout: 30_000 },
    )
    .toBe(true)
  return found
}

/** A task with a committed change to lines 50 and 250 of a 300-line file. */
async function startWork(omo: Omo, repo: string, committed = false): Promise<number> {
  if (!committed) commitInto(repo, { 'src/long.ts': numbered(300) }, 'chore: a long file')
  const n = await omo.createTicket({ title: 'Change a long file' })
  await omo.moveTicket(n, 'ready')
  await waitForSession(omo, n, 'developer')
  commitInto(await worktreeOf(omo, n), { 'src/long.ts': numbered(300, [50, 250]) }, 'feat: change two lines')
  return n
}

test.describe('tickets open as pages, with an image, a ticket link and a diff', () => {
  test.use({ omoOptions: { agents: { developer: script('ready', 'hang') } } })

  test('the comment keeps its link, its image and its lightbox on the page; the diff expands there', async ({ page, omo, repo }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    const n = await startWork(omo, repo)
    const other = await omo.createTicket({ title: 'The other one' })
    const ref = await uploadImage(omo, n, 'seen.png')
    await omo.api('POST', `/api/tickets/${n}/comments`, { body: `See #${other} and\n\n${ref}`, question: false })
    await omo.api('PATCH', '/api/settings', { ui: { open_tickets_as_page: true } })

    await openBoard(page, omo)
    await page.locator(`[data-card-link][href$="/${n}"]`).first().click()
    await expect(page).toHaveURL(new RegExp(`/ticket/${n}$`))
    await ticketPage(page).getByRole('tab', { name: /^Comments/ }).click()
    const body = ticketPage(page).locator('[data-comment-body]', { hasText: 'See #' })
    const img = body.getByRole('img', { name: 'seen.png' })
    await expect(img).toBeVisible()
    await expect(img).toHaveAttribute('src', /^blob:/)

    // The image opens its lightbox on the page and Escape brings the page back.
    await body.getByRole('button', { name: 'seen.png' }).click()
    await expect(lightbox(page)).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(lightbox(page)).toHaveCount(0)
    await expect(page).toHaveURL(new RegExp(`/ticket/${n}\\?tab=comments$`))
    await expect(panel(page)).toHaveCount(0)

    // The ticket link beside the image follows to the other ticket's page, not to a panel.
    await body.locator(`a[data-ticket-ref="${other}"]`).click()
    await expect(page).toHaveURL(new RegExp(`/ticket/${other}$`))
    await expect(panel(page)).toHaveCount(0)
    await page.goBack()
    await expect(body.getByRole('img', { name: 'seen.png' })).toBeVisible()

    // The diff on the page expands its hidden lines.
    await page.goto(omo.url(`/ticket/${n}?tab=diff`))
    await tree(page).locator('[data-tree-file="src/long.ts"]').click()
    const file = filePane(page).locator('[data-diff-file]')
    await expect(file.locator('[data-gap]')).toHaveCount(3)
    await file.getByRole('button', { name: 'Show all 193 hidden lines between lines 53 and 247 of src/long.ts' }).click()
    await expect(file.locator('[data-gap]')).toHaveCount(2)
    await file.getByRole('button', { name: 'Show the whole file src/long.ts' }).click()
    await expect(file.locator('[data-gap]')).toHaveCount(0)
    await expect(file.locator('[data-row="ctx"]')).toHaveCount(298)
    await omo.api('POST', '/api/agents/stop-all', {})
    await expectNoAgentProcesses(omo)
  })

  test('hidden lines are read from the remote-tracking branch when the switch is on', async ({ page, omo, repo }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    const origin = join(dirname(repo), 'origin.git')
    git(dirname(repo), 'init', '-q', '--bare', '-b', 'main', origin)
    git(repo, 'remote', 'add', 'origin', origin)
    commitInto(repo, { 'src/long.ts': numbered(300) }, 'chore: a long file')
    git(repo, 'push', '-q', '-u', 'origin', 'main')
    const n = await startWork(omo, repo, true)
    // Someone else pushes two commits; the repository fetches them, its main stays behind.
    const clone = join(dirname(repo), 'clone')
    git(dirname(repo), 'clone', '-q', origin, clone)
    for (const i of [1, 2]) commitInto(clone, { [`upstream-${i}.txt`]: `${i}\n` }, `feat: upstream ${i}`)
    git(clone, 'push', '-q', 'origin', 'main')
    git(repo, 'fetch', '-q', 'origin')

    await openBoard(page, omo)
    await page.goto(omo.url(`/ticket/${n}?tab=diff`))
    await tree(page).locator('[data-tree-file="src/long.ts"]').click()
    const file = filePane(page).locator('[data-diff-file]')
    // Hidden lines opened with the local base, then the switch: the file stays readable and expandable.
    await file.getByRole('button', { name: 'Show 20 hidden lines below line 53 of src/long.ts' }).click()
    await expect(file.locator('[data-row="ctx"]')).toHaveCount(32)
    const requests: string[] = []
    page.on('request', (r) => {
      if (new URL(r.url()).pathname === `/api/tickets/${n}/diff/file`) requests.push(r.url())
    })
    await ticketPage(page).getByRole('switch', { name: 'Compare with origin/main' }).click()
    await expect(ticketPage(page).locator('[data-diff-compared]')).toBeVisible()
    await expect(file.locator('[data-path]')).toHaveText('src/long.ts')
    await expect(file.locator('[data-gap]')).not.toHaveCount(0)
    await file.getByRole('button', { name: 'Show the whole file src/long.ts' }).click()
    await expect(file.locator('[data-gap]')).toHaveCount(0)
    await expect(file.locator('[data-row="ctx"]')).toHaveCount(298)
    expect(requests.length).toBeGreaterThan(0)
    for (const u of requests) expect(new URL(u).searchParams.get('base'), u).toBe('remote')
    await expect(ticketPage(page).getByRole('alert')).toHaveCount(0)
    await omo.api('POST', '/api/agents/stop-all', {})
    await expectNoAgentProcesses(omo)
  })
})

test.describe('settings pages share one document', () => {
  const settingsFile = (omo: Omo) => join(omo.home, 'omo', 'settings.yaml')
  const patched = (page: Page) => {
    const bodies: Record<string, unknown>[] = []
    page.on('request', (r) => {
      if (r.method() === 'PATCH' && new URL(r.url()).pathname === '/api/settings') bodies.push(JSON.parse(r.postData() ?? '{}') as Record<string, unknown>)
    })
    return bodies
  }

  test('General saves branches, ceo, merge_defaults and ui only; the image keep days and the limits stay as they were', async ({ page, omo }) => {
    await omo.api('PATCH', '/api/settings', { images: { keep_days_after_done: 9 }, max_running_agents: 3 })
    await openBoard(page, omo)
    await page.goto(omo.url('/settings/general'))
    const bodies = patched(page)
    await page.getByRole('switch', { name: 'Open tickets on their own page' }).click()
    await saveButton(page).click()
    await expect(bar(page)).toContainText('Saved')
    expect(bodies).toHaveLength(1)
    expect(Object.keys(bodies[0]!).sort()).toEqual(['branches', 'ceo', 'merge_defaults', 'ui'])
    expect(bodies[0]!.branches).toEqual({ template: 'omo/{slug}' })
    const text = readFileSync(settingsFile(omo), 'utf8')
    expect(text).toMatch(/open_tickets_as_page: true/)
    expect(text).toMatch(/keep_days_after_done: 9\b/)
    expect(text).toMatch(/max_running_agents: 3\b/)
  })

  test('Limits saves its own keys only; the Tickets setting and the merge defaults stay as they were', async ({ page, omo }) => {
    await omo.api('PATCH', '/api/settings', { ui: { open_tickets_as_page: true }, merge_defaults: { task: 'squash' } })
    await openBoard(page, omo)
    await page.goto(omo.url('/settings/limits'))
    const bodies = patched(page)
    const keep = page.getByRole('spinbutton', { name: 'Keep images after Done (days)' })
    await keep.fill('12')
    await saveButton(page).click()
    await expect(bar(page)).toContainText('Saved')
    expect(bodies).toHaveLength(1)
    expect(Object.keys(bodies[0]!).sort()).toEqual(['health', 'images', 'max_running_agents', 'rescan_interval', 'stop', 'waiting'])
    const text = readFileSync(settingsFile(omo), 'utf8')
    expect(text).toMatch(/keep_days_after_done: 12\b/)
    expect(text).toMatch(/open_tickets_as_page: true/)
    expect(text).toMatch(/task: squash/)
  })
})
