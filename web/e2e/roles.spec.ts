import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import AxeBuilder from '@axe-core/playwright'
import type { Locator, Page } from '@playwright/test'
import { openBoard } from './support/board'
import { expect, test, type Omo } from './support/omo'

test.use({ omoOptions: { noProject: true } })

const MODEL = (description: string) => ({ args: [], env: {}, usage_windows: [], selectable: true, description })
const profile = (models: Record<string, string>) => ({
  cli: 'claude',
  command: 'claude',
  args: [],
  env: {},
  account_dir: '',
  usage_script: '',
  models: Object.fromEntries(Object.entries(models).map(([name, text]) => [name, MODEL(text)])),
})
const PROFILE = profile({ sonnet: 'Everyday development', opus: 'Strongest', haiku: 'Quick and cheap' })

const steps = (page: Page) => page.getByRole('list', { name: 'First steps' })
const step = (page: Page, title: string) => steps(page).getByRole('listitem').filter({ has: page.getByRole('heading', { name: title }) })
const editor = (page: Page, role: string) => page.locator(`[data-role-editor="${role}"]`)
const models = (e: Locator) => e.locator('[data-model]')
const saveBar = (e: Locator) => e.getByRole('region', { name: 'Save changes' })

async function openRoles(page: Page) {
  await step(page, 'Check the agent setup').getByRole('button', { name: 'Check the agent setup' }).click()
  await expect(page).toHaveURL(/\/settings\/roles$/)
  await expect(editor(page, 'reviewer')).toBeVisible()
}

async function addModel(e: Locator, id: string) {
  await e.getByLabel('Add model', { exact: true }).selectOption(id)
  await e.locator('[data-add-model]').click()
}

async function saveRole(e: Locator) {
  await saveBar(e).getByRole('button', { name: 'Save', exact: true }).click()
  await expect(saveBar(e).locator('[data-save-state]')).toHaveText('Saved')
}

function writeRole(omo: Omo, role: string, text: string) {
  const dir = join(omo.home, 'omo', 'roles')
  mkdirSync(dir, { recursive: true })
  writeFileSync(join(dir, `${role}.yaml`), text)
}

async function stored(omo: Omo, role: string): Promise<{ models: string[]; assignment: string }> {
  const all = await omo.api<{ name: string; models: string[]; assignment: string }[]>('GET', '/api/roles')
  const found = all.find((r) => r.name === role)
  if (!found) throw new Error(`no role ${role}`)
  return { models: found.models, assignment: found.assignment }
}

test('assigning a model to every role ticks the first-steps line; order, weights and removal are stored as shown', async ({ page, omo }) => {
  await omo.api('PUT', '/api/profiles/claude', PROFILE)
  await openBoard(page, omo)
  await expect(step(page, 'Check the agent setup')).not.toContainText('done')
  await openRoles(page)

  const dev = editor(page, 'developer')
  await expect(dev.getByText('No models yet. Nothing starts for this role until it has one.')).toBeVisible()
  await addModel(dev, 'claude/sonnet')
  await addModel(dev, 'claude/opus')
  await addModel(dev, 'claude/haiku')
  await addModel(dev, 'claude/sonnet')
  await expect(models(dev)).toHaveText([/claude\/sonnet\s*×2/, /claude\/opus/, /claude\/haiku/, /claude\/sonnet\s*×2/])
  await dev.getByRole('button', { name: 'Move claude/haiku up from place 3' }).click()
  await expect(models(dev)).toHaveCount(4)
  await dev.getByRole('button', { name: 'Remove claude/opus' }).click()
  await expect(models(dev)).toHaveText([/claude\/sonnet\s*×2/, /claude\/haiku/, /claude\/sonnet\s*×2/])
  await expect(saveBar(dev).locator('[data-save-state]')).toHaveText('Unsaved changes')
  await dev.getByLabel('Assignment').selectOption('random')
  await expect(dev).toContainText('A model listed twice is picked twice as often.')
  await saveRole(dev)
  expect(await stored(omo, 'developer')).toEqual({ models: ['claude/sonnet', 'claude/haiku', 'claude/sonnet'], assignment: 'random' })

  // Each role has its own form and its own Save: the developer's save left the others alone.
  await expect(saveBar(editor(page, 'reviewer')).locator('[data-save-state]')).toHaveText('No unsaved changes')
  await page.getByRole('link', { name: 'Back to board' }).click()
  await expect(step(page, 'Check the agent setup')).not.toContainText('done')
  await page.getByRole('link', { name: 'Settings', exact: true }).click()
  await page.getByRole('navigation', { name: 'Settings' }).getByRole('link', { name: 'Roles' }).click()
  for (const role of ['ceo', 'pm', 'reviewer']) {
    const e = editor(page, role)
    await addModel(e, 'claude/sonnet')
    await saveRole(e)
  }
  await page.getByRole('link', { name: 'Back to board' }).click()
  await expect(step(page, 'Check the agent setup')).toContainText('done')
  expect(await stored(omo, 'reviewer')).toEqual({ models: ['claude/sonnet'], assignment: 'round_robin' })
})

test('a refused save shows the server message under the list, with focus on it; a model that vanished is marked and removable', async ({ page, omo }) => {
  await omo.api('PUT', '/api/profiles/claude', PROFILE)
  await openBoard(page, omo)
  await openRoles(page)
  const rev = editor(page, 'reviewer')
  await addModel(rev, 'claude/haiku')
  // The model leaves its profile while the page holds an unsaved list that names it.
  await omo.api('PUT', '/api/profiles/claude', profile({ sonnet: 'Everyday development', opus: 'Strongest' }))
  const row = rev.locator('[data-model="claude/haiku"]')
  await expect(row).toHaveAttribute('data-mark', 'missing')
  await expect(row).toContainText('does not exist')
  await expect(rev.getByLabel('Add model', { exact: true }).locator('option')).toHaveCount(2)

  await saveBar(rev).getByRole('button', { name: 'Save', exact: true }).click()
  const refusal = rev.locator('[data-field-error="models"]')
  await expect(refusal).toContainText('unknown model')
  await expect(refusal).toContainText('claude/haiku')
  await expect(row).toContainText('refused')
  await expect(rev.locator('[aria-invalid="true"]')).toBeFocused()
  // The other roles are untouched by the refusal.
  await expect(editor(page, 'developer').locator('[data-field-error]')).toHaveCount(0)

  await rev.getByRole('button', { name: 'Remove claude/haiku' }).click()
  await addModel(rev, 'claude/opus')
  await saveRole(rev)
  await expect(rev.locator('[data-field-error]')).toHaveCount(0)
  expect((await stored(omo, 'reviewer')).models).toEqual(['claude/opus'])
})

test('a hand edit of a role file while the page is open: shown at once, or kept as a draft conflict', async ({ page, omo }) => {
  await omo.api('PUT', '/api/profiles/claude', PROFILE)
  writeRole(omo, 'developer', 'models:\n  - claude/sonnet\nassignment: round_robin\n')
  await omo.stop()
  await omo.start()
  await openBoard(page, omo)
  await openRoles(page)
  const dev = editor(page, 'developer')
  await expect(models(dev)).toHaveText([/claude\/sonnet/])

  // No unsaved edits: the page shows the new value and says it came from the disk.
  writeRole(omo, 'developer', 'models:\n  - claude/opus\n  - claude/haiku\nassignment: failover\n')
  await expect(models(dev)).toHaveText([/claude\/opus/, /claude\/haiku/])
  await expect(dev.locator('[data-changed-on-disk]')).toContainText('Changed on disk at')
  await expect(dev.getByLabel('Assignment')).toHaveValue('failover')
  await expect(editor(page, 'reviewer').locator('[data-changed-on-disk]')).toHaveCount(0)

  // Unsaved edits: the draft stays, the new value is offered.
  await addModel(dev, 'claude/sonnet')
  writeRole(omo, 'developer', 'models:\n  - claude/haiku\nassignment: failover\n')
  await expect(saveBar(dev).locator('[data-save-state]')).toHaveText('Changed outside this page')
  await expect(models(dev)).toHaveText([/claude\/opus/, /claude\/haiku/, /claude\/sonnet/])
  await saveBar(dev).getByRole('button', { name: 'Save', exact: true }).click()
  await expect(dev.getByText('Choose “Use the new value” or “Keep mine” first.')).toBeVisible()
  await dev.locator('[data-use-theirs]').click()
  await expect(models(dev)).toHaveText([/claude\/haiku/])
  await addModel(dev, 'claude/opus')
  writeRole(omo, 'developer', 'models:\n  - claude/sonnet\nassignment: failover\n')
  await expect(dev.locator('[data-keep-mine]')).toBeVisible()
  await dev.locator('[data-keep-mine]').click()
  await saveRole(dev)
  expect((await stored(omo, 'developer')).models).toEqual(['claude/haiku', 'claude/opus'])
})

test('a stale save is refused by the server and shows the conflict', async ({ page, omo }) => {
  await omo.api('PUT', '/api/profiles/claude', PROFILE)
  await openBoard(page, omo)
  await openRoles(page)
  const pm = editor(page, 'pm')
  await addModel(pm, 'claude/sonnet')
  // Written by another client right before the save, inside the store's poll interval.
  await omo.api('PUT', '/api/roles/pm', { name: 'pm', models: ['claude/opus'], assignment: 'failover' })
  await saveBar(pm).getByRole('button', { name: 'Save', exact: true }).click()
  await expect(saveBar(pm).locator('[data-save-state]')).toHaveText('Changed outside this page')
  expect((await stored(omo, 'pm')).models).toEqual(['claude/opus'])
})

test.describe('at 390 px', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('every role can be edited without sideways scrolling', async ({ page, omo }) => {
    await omo.api('PUT', '/api/profiles/claude', PROFILE)
    writeRole(omo, 'reviewer', 'models:\n  - claude/gone-with-a-very-long-model-name-that-has-to-wrap-somewhere\n  - claude/sonnet\nassignment: random\n')
    await omo.stop()
    await omo.start()
    await openBoard(page, omo)
    await page.goto(omo.url('/settings/roles'))
    const rev = editor(page, 'reviewer')
    await expect(rev).toBeVisible()
    await expect(rev.locator('[data-mark="missing"]')).toBeVisible()
    const sideways = () =>
      page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth || document.body.scrollWidth > document.body.clientWidth)
    expect(await sideways()).toBe(false)
    await addModel(rev, 'claude/opus')
    await rev.getByRole('button', { name: 'Remove claude/gone-with-a-very-long-model-name-that-has-to-wrap-somewhere' }).click()
    await saveRole(rev)
    expect((await stored(omo, 'reviewer')).models).toEqual(['claude/sonnet', 'claude/opus'])
    expect(await sideways()).toBe(false)
  })
})

for (const scheme of ['dark', 'light'] as const) {
  for (const width of [1440, 390]) {
    test.describe(`${scheme} theme at ${width} px`, () => {
      test.use({ colorScheme: scheme, viewport: { width, height: 900 } })

      test('the Roles page has no accessibility violations, also with a missing model and a refusal', async ({ page, omo }) => {
        await omo.api('PUT', '/api/profiles/claude', PROFILE)
        writeRole(omo, 'developer', 'models:\n  - claude/sonnet\n  - claude/sonnet\n  - claude/gone\nassignment: random\n')
        await omo.stop()
        await omo.start()
        await openBoard(page, omo)
        await page.goto(omo.url('/settings/roles'))
        await expect(editor(page, 'developer').locator('[data-mark="missing"]')).toBeVisible()
        const check = async () => {
          await page.evaluate(() => document.fonts.ready.then(() => undefined))
          const results = await new AxeBuilder({ page }).analyze()
          return results.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target.join(' ')) }))
        }
        expect(await check()).toEqual([])
        // The server refuses the missing model: the refused state must be clean too.
        await editor(page, 'developer').getByLabel('Assignment').selectOption('failover')
        await saveBar(editor(page, 'developer')).getByRole('button', { name: 'Save', exact: true }).click()
        await expect(editor(page, 'developer').locator('[data-field-error="models"]')).toBeVisible()
        expect(await check()).toEqual([])
      })
    })
  }
}
