import AxeBuilder from '@axe-core/playwright'
import type { Page } from '@playwright/test'
import { openBoard } from './support/board'
import { expect, test, type Omo } from './support/omo'

// The merge mode of each kind of merge: the general defaults on the General
// page and a project's own modes on the Projects page, where "Use global
// default (…)" names the general value and follows its changes.

interface ProjectAnswer {
  id: string
  name: string
  merge: Record<string, string>
  effective_merge: Record<string, string>
}

const title = (page: Page) => page.locator('[data-settings-page] h1')
const saveBar = (page: Page) => page.getByRole('region', { name: 'Save changes' })
const demoRow = (page: Page) => page.locator('tr[data-project]', { has: page.getByRole('rowheader', { name: 'demo', exact: true }) })
const select = (page: Page, label: string) => page.getByRole('combobox', { name: label, exact: true })
const STORY = 'When a story is merged'
const TASK = 'When a task without a story is merged'
const STORY_TASK = 'When a task is merged into its story'

async function demo(omo: Omo): Promise<ProjectAnswer> {
  const list = await omo.api<ProjectAnswer[]>('GET', '/api/projects?all=true')
  return list.find((p) => p.name === 'demo')!
}

async function violations(page: Page) {
  await page.evaluate(() => document.fonts.ready.then(() => undefined))
  const results = await new AxeBuilder({ page }).analyze()
  return results.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target.join(' ')) }))
}

async function openDemo(page: Page, omo: Omo) {
  await page.goto(omo.url('/settings/projects'))
  await expect(title(page)).toHaveText('Projects')
  await demoRow(page).getByRole('button', { name: 'Edit demo' }).click()
}

const firstOption = (page: Page, label: string) => select(page, label).locator('option').first()

test('each merge mode of a project is saved on its own; "Use global default" follows the general defaults', async ({ page, omo }) => {
  await openBoard(page, omo)
  await openDemo(page, omo)
  for (const label of [STORY, TASK, STORY_TASK]) {
    await expect(select(page, label)).toHaveValue('')
    await expect(select(page, label).locator('option')).toHaveText(['Use global default (Merge commit)', 'Merge commit', 'Squash into one commit'])
  }
  await expect(demoRow(page).locator('[data-project-merge-override]')).toHaveCount(0)

  await select(page, TASK).selectOption('squash')
  await expect.poll(async () => (await demo(omo)).merge).toEqual({ story: '', task: 'squash', story_task: '' })
  await expect(demoRow(page).locator('[data-project-merge-override]')).toHaveText('merge: overridden')
  await select(page, STORY_TASK).selectOption('merge')
  await expect.poll(async () => (await demo(omo)).merge).toEqual({ story: '', task: 'squash', story_task: 'merge' })
  await select(page, STORY_TASK).selectOption('')
  await select(page, TASK).selectOption('')
  await expect.poll(async () => (await demo(omo)).merge).toEqual({ story: '', task: '', story_task: '' })
  await expect(demoRow(page).locator('[data-project-merge-override]')).toHaveCount(0)

  // The general default changes on the General page; the project follows it.
  await page.goto(omo.url('/settings/general'))
  await expect(title(page)).toHaveText('General')
  const defaults = page.locator('[data-merge-defaults]')
  await expect(defaults.getByRole('combobox', { name: STORY })).toHaveValue('merge')
  await defaults.getByRole('combobox', { name: STORY }).selectOption('squash')
  await saveBar(page).getByRole('button', { name: 'Save' }).click()
  await expect(saveBar(page).locator('[data-save-state]')).toContainText('Saved')
  await expect.poll(async () => (await demo(omo)).effective_merge.story).toBe('squash')

  await openDemo(page, omo)
  await expect(firstOption(page, STORY)).toHaveText('Use global default (Squash into one commit)')
  await expect(firstOption(page, TASK)).toHaveText('Use global default (Merge commit)')

  // A change made elsewhere while the page is open: the words follow without a reload.
  await omo.api('PATCH', '/api/settings', { merge_defaults: { task: 'squash' } })
  await expect(firstOption(page, TASK)).toHaveText('Use global default (Squash into one commit)')
  // A project's own mode stays its own; the default it would go back to is named.
  await select(page, STORY_TASK).selectOption('squash')
  await expect(demoRow(page).locator('[data-project-merge-override]')).toHaveText('merge: overridden')
  await omo.api('PATCH', '/api/settings', { merge_defaults: { story_task: 'squash' } })
  await expect(firstOption(page, STORY_TASK)).toHaveText('Use global default (Squash into one commit)')
  await expect(select(page, STORY_TASK)).toHaveValue('squash')
})

for (const width of [1440, 390] as const) {
  for (const colorScheme of ['dark', 'light'] as const) {
    test.describe(`axe at ${width} px, ${colorScheme} theme`, () => {
      test.use({ viewport: { width, height: width === 390 ? 844 : 900 }, colorScheme, reducedMotion: 'reduce' })

      test('the merge defaults and a project with its own merge mode have no violations and no sideways scroll', async ({ page, omo }) => {
        await openBoard(page, omo)
        await page.goto(omo.url('/settings/general'))
        await expect(page.locator('[data-merge-defaults]')).toBeVisible()
        expect(await violations(page), 'general').toEqual([])
        await openDemo(page, omo)
        await select(page, STORY).selectOption('squash')
        await expect(demoRow(page).locator('[data-project-merge-override]')).toBeVisible()
        expect(await violations(page), 'project').toEqual([])
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true)
      })
    })
  }
}
