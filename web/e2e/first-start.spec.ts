import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import AxeBuilder from '@axe-core/playwright'
import type { Page } from '@playwright/test'
import { expectCardIn, openBoard } from './support/board'
import { expect, test, type Omo } from './support/omo'

test.use({ omoOptions: { noProject: true } })

const steps = (page: Page) => page.getByRole('list', { name: 'First steps' })
const step = (page: Page, title: string) => steps(page).getByRole('listitem').filter({ has: page.getByRole('heading', { name: title }) })
/** The agent setup: on the Roles page of the settings. */
const setupPage = (page: Page) => page.locator('section[data-settings-page]').filter({ has: page.getByRole('heading', { level: 1, name: 'Roles' }) })

function writeSetting(omo: Omo, file: string, text: string) {
  const dir = join(omo.home, 'omo', file.split('/')[0]!)
  mkdirSync(dir, { recursive: true })
  writeFileSync(join(omo.home, 'omo', file), text)
}

const PROFILE = 'cli: claude\ncommand: claude\nmodels:\n  sonnet:\n    args: ["--model", "sonnet"]\n'
const roleFile = (model: string) => `models:\n  - ${model}\nassignment: round_robin\n`

async function openSetup(page: Page) {
  await step(page, 'Check the agent setup').getByRole('button', { name: 'Check the agent setup' }).click()
  await expect(page).toHaveURL(/\/settings\/roles$/)
  await expect(setupPage(page)).toBeVisible()
}

async function backToBoard(page: Page) {
  await page.getByRole('link', { name: 'Back to board' }).click()
  await expect(page.locator('[data-board]')).toBeVisible()
}

test('a fresh home shows four steps, none done, Superpowers first and recommended', async ({ page, omo }) => {
  await openBoard(page, omo)
  await expect(steps(page).getByRole('listitem')).toHaveCount(4)
  await expect(steps(page).getByRole('listitem').first()).toContainText('Add the Superpowers skill set (recommended)')
  for (const title of ['Add the Superpowers skill set (recommended)', 'Add a project', 'Check the agent setup', 'Create a first ticket']) {
    await expect(step(page, title)).toBeVisible()
    await expect(step(page, title)).not.toContainText('done')
  }
  await expect(page.getByText('a folder with a git repository on this machine')).toBeVisible()
  await expect(page.getByText('This list disappears once the board has a ticket.')).toBeVisible()
})

const SUPERPOWERS = 'Add the Superpowers skill set (recommended)'

/**
 * Stands in for omo's skill set routes, so no test fetches the real
 * repository from GitHub: a write is answered with `answer`, a read with the
 * sets written so far.
 */
async function fakeSkillSets(page: Page, answer: (body: Record<string, unknown>) => { status: number; body: unknown }) {
  const sets: unknown[] = []
  const writes: { url: string; ifMatch: string | undefined; body: Record<string, unknown> }[] = []
  await page.route(/\/api\/skillsets(\/[^/]+)?$/, async (route) => {
    const req = route.request()
    if (req.method() === 'GET') return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ skill_sets: sets, states: [], errors: [] }) })
    const body = req.postDataJSON() as Record<string, unknown>
    writes.push({ url: new URL(req.url()).pathname, ifMatch: req.headers()['if-match'], body })
    const a = answer(body)
    if (a.status < 300) sets.push({ ...body, revision: '1' })
    return route.fulfill({ status: a.status, contentType: 'application/json', body: JSON.stringify(a.body) })
  })
  return writes
}

test('the Superpowers step adds the set in one click and ticks; the other steps stay as they are', async ({ page, omo }) => {
  const writes = await fakeSkillSets(page, (body) => ({ status: 200, body: { ...body, revision: '1' } }))
  await openBoard(page, omo)
  // Recommended, not needed: the main button stays on the first needed step.
  await expect(step(page, 'Add a project').getByRole('button', { name: 'Add project' })).toHaveClass(/omo-btn--primary/)
  await step(page, SUPERPOWERS).getByRole('button', { name: 'Add Superpowers' }).click()
  await expect(step(page, SUPERPOWERS)).toContainText('done')
  expect(writes).toEqual([
    {
      url: '/api/skillsets/superpowers',
      ifMatch: '"0"',
      body: expect.objectContaining({ name: 'superpowers', repository: 'https://github.com/obra/superpowers.git', ref: 'main', update: 'at_start', enabled: true }),
    },
  ])
  await expect(step(page, 'Add a project')).not.toContainText('done')
  // The link opens the Skill sets page.
  await step(page, SUPERPOWERS).getByRole('button', { name: 'Skill sets page' }).click()
  await expect(page).toHaveURL(/\/settings\/skills$/)
})

test("the server's refusal of the Superpowers step shows in the step; skipping it holds nothing up", async ({ page, omo, repo }) => {
  await fakeSkillSets(page, () => ({ status: 409, body: { error: { code: 'stale_settings', message: 'A skill set of this name exists already.' } } }))
  await openBoard(page, omo)
  await step(page, SUPERPOWERS).getByRole('button', { name: 'Add Superpowers' }).click()
  await expect(step(page, SUPERPOWERS).getByRole('alert')).toHaveText('A skill set of this name exists already.')
  await expect(step(page, SUPERPOWERS)).not.toContainText('done')
  // The other steps go on without it.
  await step(page, 'Add a project').getByRole('button', { name: 'Add project' }).click()
  const add = page.getByRole('dialog', { name: 'Add project' })
  await add.getByLabel('Folder').fill(repo)
  await add.getByRole('button', { name: 'Add project' }).click()
  await expect(step(page, 'Add a project')).toContainText('done')
})

test("a name taken meanwhile is refused by the server, and the step shows the server's words", async ({ page, omo }) => {
  // The page still believes no set exists (its read is held back); another client creates one named superpowers.
  await page.route(/\/api\/skillsets$/, (route) =>
    route.request().method() === 'GET'
      ? route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ skill_sets: [], states: [], errors: [] }) })
      : route.continue(),
  )
  await openBoard(page, omo)
  await omo.api('PUT', '/api/skillsets/superpowers', { repository: 'https://example.invalid/other.git', ref: '', update: 'pinned', enabled: false, instructions: {} })
  const put = page.waitForResponse((r) => r.url().endsWith('/api/skillsets/superpowers') && r.request().method() === 'PUT')
  await step(page, SUPERPOWERS).getByRole('button', { name: 'Add Superpowers' }).click()
  const answer = await put
  expect(answer.status()).toBe(409)
  const message = ((await answer.json()) as { error: { message: string } }).error.message
  expect(message).not.toBe('')
  await expect(step(page, SUPERPOWERS).getByRole('alert')).toHaveText(message)
  await expect(step(page, SUPERPOWERS)).not.toContainText('done')
})

test('the first step offers the workspace folders on the Projects page', async ({ page, omo }) => {
  await openBoard(page, omo)
  await step(page, 'Add a project').getByRole('button', { name: 'Add a workspace folder' }).click()
  await expect(page).toHaveURL(/\/settings\/projects$/)
  await expect(page.getByRole('heading', { level: 1, name: 'Projects' })).toBeVisible()
  await backToBoard(page)
  await expect(step(page, 'Add a project')).toBeVisible()
})

test('adding a project ticks the first step', async ({ page, omo, repo }) => {
  await openBoard(page, omo)
  await step(page, 'Add a project').getByRole('button', { name: 'Add project' }).click()
  const add = page.getByRole('dialog', { name: 'Add project' })
  await add.getByLabel('Folder').fill(repo)
  await add.getByRole('button', { name: 'Add project' }).click()
  await expect(add).toBeHidden()
  await expect(step(page, 'Add a project')).toContainText('done')
  await expect(step(page, 'Check the agent setup')).not.toContainText('done')
})

test('a role without a model keeps the second step open and the Roles page names it; files written by hand tick it without a reload', async ({ page, omo }) => {
  await openBoard(page, omo)
  await openSetup(page)
  const dialog = setupPage(page)
  await expect(dialog.getByText('Nothing starts for the CEO, product manager, developer and reviewer: they have no usable model.')).toBeVisible()
  await expect(dialog.getByText('No model assigned. Nothing starts for the developer until it has one.')).toBeVisible()
  await expect(dialog.getByText('No profile is defined, so there are no models.')).toBeVisible()
  await expect(dialog.getByRole('heading', { name: 'How to change it' })).toBeVisible()

  // A profile and three of the four roles: the CEO is still without a model.
  writeSetting(omo, 'profiles/claude.yaml', PROFILE)
  for (const r of ['pm', 'developer', 'reviewer']) writeSetting(omo, `roles/${r}.yaml`, roleFile('claude/sonnet'))
  await expect(dialog.getByText('Nothing starts for the CEO: it has no usable model.')).toBeVisible()
  await expect(dialog.locator('[data-role="developer"]')).toContainText('claude/sonnet runs claude with the profile claude')
  await backToBoard(page)
  await expect(step(page, 'Check the agent setup')).not.toContainText('done')

  writeSetting(omo, 'roles/ceo.yaml', roleFile('claude/sonnet'))
  await expect(step(page, 'Check the agent setup')).toContainText('done')
  await openSetup(page)
  await expect(dialog.getByText('Every role that is turned on has a usable model.')).toBeVisible()
})

test('a role naming a model that does not exist and a broken profile file are shown', async ({ page, omo }) => {
  writeSetting(omo, 'profiles/claude.yaml', PROFILE)
  writeSetting(omo, 'profiles/broken.yaml', 'cli: nonsense\n')
  writeSetting(omo, 'roles/developer.yaml', roleFile('claude/gone'))
  await omo.stop()
  await omo.start()
  await openBoard(page, omo)
  await openSetup(page)
  const dialog = setupPage(page)
  await expect(dialog.locator('[data-role="developer"]')).toContainText('claude/gone does not exist')
  await expect(dialog.getByText(/The profile file .*broken\.yaml cannot be used/)).toBeVisible()
})

test('creating a ticket removes the list', async ({ page, omo, repo }) => {
  await openBoard(page, omo)
  await omo.api('POST', '/api/projects', { path: repo })
  await expect(step(page, 'Add a project')).toContainText('done')
  await step(page, 'Create a first ticket').getByRole('button', { name: 'Create first ticket' }).click()
  const dialog = page.getByRole('dialog', { name: 'Create ticket' })
  await dialog.getByRole('radio', { name: 'Task' }).check({ force: true })
  await dialog.getByLabel('Title').fill('First one')
  await dialog.getByRole('button', { name: 'Create', exact: true }).click()
  await expectCardIn(page, 1, 'Planning')
  await expect(steps(page)).toBeHidden()
})

for (const scheme of ['dark', 'light'] as const) {
  test.describe(`${scheme} theme`, () => {
    test.use({ colorScheme: scheme })

    test('the first steps and the Roles page with the agent setup have no accessibility violations', async ({ page, omo }) => {
      await openBoard(page, omo)
      await expect(steps(page)).toBeVisible()
      expect((await new AxeBuilder({ page }).analyze()).violations.map((v) => v.id)).toEqual([])
      await openSetup(page)
      expect((await new AxeBuilder({ page }).analyze()).violations.map((v) => ({ id: v.id, n: v.nodes.map((n) => n.target.join(' ')) }))).toEqual([])
    })
  })
}
