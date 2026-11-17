// The README's screenshots: .github/assets/screenshot-<view>-<theme>.png,
// taken from the worktree's own bin/omo on a temporary home (the browser-test
// harness), seeded with the demo board of demo.ts and with the scripted fake
// agent in place of every agent program. Run with `make readme-shots`.
//
// One more image is one more entry of VIEWS. OMO_README_SHOTS_ONLY=<text>
// takes only the views whose name contains it; OMO_README_SHOTS_DIR=<folder>
// writes somewhere else than .github/assets (to compare before replacing).
import { execFileSync } from 'node:child_process'
import { mkdirSync, mkdtempSync, realpathSync, rmSync, writeFileSync } from 'node:fs'
import { devNull, tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import type { Page } from '@playwright/test'
import { liveAgents, waitForSession } from '../support/agents'
import { openBoard } from '../support/board'
import { initRepo } from '../support/git'
import { REPO_DIR, expect, test as base, type Omo } from '../support/omo'
import { expectTerminalText, terminalIn } from '../support/terminal'
import { COMMENTS, DEVELOPER, LIMITS, NO_PROJECT_TASK, PM, PROJECTS, REVIEWER, TICKETS, TICKET_SHOT, ceo } from './demo'

const OUT = process.env.OMO_README_SHOTS_DIR ?? join(REPO_DIR, '.github', 'assets')

/** Commits `files` on a new branch of the repository `dir`, then returns to main. */
function commitOnBranch(dir: string, branch: string, message: string, files: Record<string, string>) {
  const env = { ...process.env, GIT_CONFIG_GLOBAL: devNull, GIT_CONFIG_SYSTEM: devNull }
  const git = (...args: string[]) => execFileSync('git', args, { cwd: dir, env, stdio: 'pipe' })
  git('checkout', '-q', '-b', branch)
  for (const [path, text] of Object.entries(files)) {
    mkdirSync(dirname(join(dir, path)), { recursive: true })
    writeFileSync(join(dir, path), text)
  }
  git('add', '--', ...Object.keys(files))
  git('-c', 'user.name=Agent', '-c', 'user.email=agent@example.invalid', '-c', 'commit.gpgsign=false', 'commit', '-q', '-m', message)
  git('checkout', '-q', 'main')
}

// `{{repo}}` is the folder that holds the demo repositories.
const test = base.extend({
  repo: async ({}, use) => {
    const root = mkdtempSync(join(realpathSync(tmpdir()), 'omo-shots-'))
    for (const name of PROJECTS) initRepo(join(root, name))
    for (const t of TICKETS) if (t.branch) commitOnBranch(join(root, t.projects[0]!), t.branch.name, t.branch.message, t.branch.files)
    try {
      await use(root)
    } finally {
      rmSync(root, { recursive: true, force: true })
    }
  },
})

/** The number of the CEO's first draft: after the seeded tickets and the task without a project. */
const firstCeoDraft = TICKETS.length + 2

test.use({
  omoOptions: {
    paused: true,
    seed: {
      projects: PROJECTS.map((name) => ({ path: `{{repo}}/${name}`, name })),
      tickets: TICKETS.map(({ branch, ...t }) => ({ ...t, ...(branch ? { branch: branch.name } : {}) })),
    },
    agents: { developer: DEVELOPER, pm: PM, reviewer: REVIEWER, ceo: ceo(firstCeoDraft) },
  },
})

const sidebar = (page: Page) => page.locator('[data-sidebar]')

interface Clip {
  x: number
  y: number
  width: number
  height: number
}

/**
 * One screenshot: its viewport (taken at 2x), how to bring the page into its
 * state, and optionally the area to keep (the whole viewport otherwise).
 */
interface View {
  name: string
  viewport: { width: number; height: number }
  show(page: Page, omo: Omo): Promise<void>
  clip?(page: Page): Promise<Clip>
}

const VIEWS: View[] = [
  {
    // Every column with work, the sidebar listing the running agents.
    name: 'board',
    viewport: { width: 1632, height: 850 },
    async show(page, omo) {
      await openBoard(page, omo)
      await openSidebarTab(page, 'Agents', 352)
      await expect(sidebar(page).locator('[data-agent-row]')).toHaveCount(5)
    },
  },
  {
    // The ticket as a page: the developer's terminal in the Agents tab, the details on the right.
    name: 'ticket',
    viewport: { width: 1440, height: 920 },
    async show(page, omo) {
      await page.goto(omo.url(`/ticket/${omo.refs[TICKET_SHOT]}`))
      await expect(page.locator('[data-connection]')).toHaveAttribute('data-state', 'live')
      if ((await sidebar(page).getAttribute('data-open')) === 'true') await sidebar(page).getByRole('button', { name: 'Collapse sidebar' }).click()
      await page.getByRole('tab', { name: /^Agents/ }).last().click()
      const agents = page.locator('[data-ticket-agents]')
      await agents.locator('[data-agent-row]').first().click()
      await expectTerminalText(terminalIn(agents), 'esc to interrupt')
    },
  },
  {
    // The CEO tab next to the first columns of the board, with the drafts on top of Planning.
    name: 'ceo',
    viewport: { width: 1600, height: 820 },
    async show(page, omo) {
      await openBoard(page, omo)
      await openSidebarTab(page, 'CEO', 480)
      await expectTerminalText(terminalIn(sidebar(page)), 'tell me what to change')
      const { tickets } = await omo.api<{ tickets: { number: number; type: string; column: string; created_by: { role: string } }[] }>('GET', '/api/tickets')
      const drafts = tickets.filter((t) => t.created_by.role === 'ceo' && t.type === 'task').map((t) => t.number)
      const firstTask = tickets.find((t) => t.column === 'planning' && t.type === 'task' && t.created_by.role !== 'ceo')
      for (const n of drafts.sort((a, b) => a - b)) await omo.moveTicket(n, 'planning', firstTask?.number)
      const planning = page.locator('section[data-drop-column="planning"] li[data-ticket]')
      await expect(planning.nth(2)).toHaveAttribute('data-ticket', String(drafts[0]))
    },
    async clip(page) {
      const ready = await page.locator('section[data-drop-column="ready"]').boundingBox()
      const size = page.viewportSize()!
      return { x: 0, y: 0, width: Math.round(ready!.x + ready!.width + 8), height: size.height }
    },
  },
]

/** Opens a tab of the sidebar at `width` pixels (320 to the window's 60 %, in steps of 16). */
async function openSidebarTab(page: Page, tab: string, width: number) {
  await sidebar(page).getByRole('tab', { name: new RegExp(`^${tab}`) }).click()
  const splitter = page.getByRole('separator', { name: 'Sidebar width' })
  await splitter.focus()
  await page.keyboard.press('Home')
  for (let w = 320; w < width; w += 16) await page.keyboard.press('ArrowRight')
  await expect(splitter).toHaveAttribute('aria-valuenow', String(width))
}

/** Writes the view in both themes, without focus ring, hover or text cursor. */
async function shoot(page: Page, view: View) {
  mkdirSync(OUT, { recursive: true })
  await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur())
  await page.mouse.move(0, 0)
  for (const theme of ['dark', 'light'] as const) {
    await page.emulateMedia({ colorScheme: theme })
    await page.evaluate(() => document.fonts.ready.then(() => undefined))
    // Two frames, so the theme's colours have reached the terminal's canvas.
    await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))))
    const clip = view.clip ? await view.clip(page) : undefined
    const file = join(OUT, `screenshot-${view.name}-${theme}.png`)
    await page.screenshot({ path: file, ...(clip ? { clip } : {}) })
    console.log(`wrote ${file}`)
  }
  await page.emulateMedia({ colorScheme: null })
}

const only = process.env.OMO_README_SHOTS_ONLY ?? ''

test('README screenshots', async ({ page, omo }) => {
  await prepare(omo)
  for (const view of VIEWS.filter((v) => v.name.includes(only))) {
    await page.setViewportSize(view.viewport)
    await view.show(page, omo)
    await shoot(page, view)
  }
})

/** Real-looking agent programs, limits, the task without a project, comments; then the engine starts the agents. */
async function prepare(omo: Omo) {
  const { profiles } = await omo.api<{ profiles: { name: string; command: string; env: Record<string, string> }[] }>('GET', '/api/profiles')
  const fake = profiles.find((p) => p.name === 'fake')!
  const model = (description: string) => ({ args: [], env: {}, usage_windows: [], selectable: true, description })
  const profile = (models: Record<string, string>) => ({
    cli: 'custom',
    command: fake.command,
    args: [],
    env: fake.env,
    account_dir: '',
    usage_script: '',
    prompt_delivery: { inject: false },
    models: Object.fromEntries(Object.entries(models).map(([m, d]) => [m, model(d)])),
  })
  await omo.api('PUT', '/api/profiles/claude', profile({ opus: 'Strongest', sonnet: 'Everyday development' }))
  await omo.api('PUT', '/api/profiles/codex', profile({ 'gpt-5': 'Reviews' }))
  await omo.api('PUT', '/api/roles/ceo', { models: ['claude/opus'], assignment: 'round_robin' })
  await omo.api('PUT', '/api/roles/pm', { models: ['claude/opus'], assignment: 'round_robin' })
  await omo.api('PUT', '/api/roles/developer', { models: ['claude/sonnet', 'codex/gpt-5'], assignment: 'round_robin' })
  await omo.api('PUT', '/api/roles/reviewer', { models: ['codex/gpt-5'], assignment: 'round_robin' })
  await omo.api('DELETE', '/api/profiles/fake')
  await omo.api('PATCH', '/api/settings', { limits: LIMITS })
  await omo.api('POST', '/api/tickets', { type: 'task', title: NO_PROJECT_TASK.title, description: NO_PROJECT_TASK.description, projects: [] })
  for (const [ref, bodies] of Object.entries(COMMENTS)) {
    for (const body of bodies) await omo.api('POST', `/api/tickets/${omo.refs[ref]}/comments`, { body })
  }
  await omo.api('PUT', '/api/server/paused', { paused: false })
  await waitForSession(omo, omo.refs.recurring!, 'pm')
  await waitForSession(omo, omo.refs[TICKET_SHOT]!, 'developer')
  await waitForSession(omo, omo.refs.overdue!, 'reviewer')
  // Every agent that starts has started: the free slots are taken.
  await expect.poll(async () => (await liveAgents(omo)).filter((s) => s.state === 'running').length, { timeout: 30_000 }).toBe(5)
}
