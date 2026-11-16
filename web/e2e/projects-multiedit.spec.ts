import AxeBuilder from '@axe-core/playwright'
import type { Page } from '@playwright/test'
import { join } from 'node:path'
import { openBoard } from './support/board'
import { initRepo } from './support/git'
import { expect, test, type Omo } from './support/omo'
import { makeWorkspace, type Workspace } from './support/workspace'

// Several projects at once on the Projects page: a filter over name and
// folder, Select… with a box per row, and Remove…, Switch off and Switch on
// for the selection, answered per project by the server.

interface ProjectAnswer {
  id: string
  name: string
  enabled: boolean
}

const title = (page: Page) => page.locator('[data-settings-page] h1')
const row = (page: Page, name: string) => page.locator('tr[data-project]', { has: page.getByRole('rowheader', { name, exact: true }) })
const box = (page: Page, name: string) => page.getByRole('checkbox', { name: `Select ${name}`, exact: true })
const filter = (page: Page) => page.getByRole('searchbox', { name: 'Filter projects' })
const bulk = (page: Page) => page.getByRole('group', { name: 'Selected projects' })
const count = (page: Page) => page.locator('[data-selection-count]')

let ws: Workspace | null = null
test.afterEach(() => {
  ws?.cleanup()
  ws = null
})

const list = (omo: Omo) => omo.api<ProjectAnswer[]>('GET', '/api/projects?all=true')
const names = async (omo: Omo) => (await list(omo)).map((p) => p.name).sort()

/** a/one and b/two found under a workspace folder (removing them is refused), x1…x3 added by hand below `extra`. */
async function setUp(omo: Omo) {
  ws = makeWorkspace({ 'a/one': 'repo', 'b/two': 'repo' }, omo.home)
  await omo.api('PATCH', '/api/settings', { workspace_roots: [{ path: ws.root, max_depth: 4, ignore: ['node_modules', '.*'] }] })
  for (const n of ['x1', 'x2', 'x3']) {
    const dir = join(omo.home, 'extra', n)
    initRepo(dir)
    await omo.api('POST', '/api/projects', { path: dir })
  }
  await expect.poll(() => names(omo)).toEqual(['a/one', 'b/two', 'demo', 'x1', 'x2', 'x3'])
}

/** `n` repositories below a workspace folder, with long names, and waits for them. */
async function many(omo: Omo, n: number) {
  const layout: Record<string, 'repo'> = {}
  for (let i = 1; i <= n; i++) layout[`clients/some-organisation-with-a-long-name/service-${String(i).padStart(2, '0')}`] = 'repo'
  ws = makeWorkspace(layout, omo.home)
  await omo.api('PATCH', '/api/settings', { workspace_roots: [{ path: ws.root, max_depth: 4, ignore: ['node_modules', '.*'] }] })
  await expect.poll(async () => (await list(omo)).length, { timeout: 30_000 }).toBe(n + 1)
}

async function openProjects(page: Page, omo: Omo) {
  await openBoard(page, omo)
  await page.goto(omo.url('/settings/projects'))
  await expect(title(page)).toHaveText('Projects')
}

async function violations(page: Page) {
  await page.evaluate(() => document.fonts.ready.then(() => undefined))
  const results = await new AxeBuilder({ page }).analyze()
  return results.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target.join(' ')) }))
}

const sideways = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth || document.body.scrollWidth > document.body.clientWidth)

test('select by filter, remove several: the one a workspace folder finds again is refused, named and stays selected', async ({ page, omo }) => {
  await setUp(omo)
  await openProjects(page, omo)
  await filter(page).fill('extra')
  await expect(page.locator('tr[data-project]')).toHaveCount(3)
  await page.getByRole('button', { name: 'Select…' }).click()
  await page.getByRole('button', { name: 'Select all 3 shown' }).click()
  await expect(count(page)).toHaveText('3 selected')
  await filter(page).fill('a/one')
  await page.getByRole('button', { name: 'Select all 1 shown' }).click()
  await filter(page).fill('')
  await expect(count(page)).toHaveText('4 selected')

  await bulk(page).getByRole('button', { name: 'Remove…' }).click()
  const dialog = page.getByRole('dialog', { name: 'Remove 4 projects?' })
  await expect(dialog.locator('[data-batch-remove-list] li .projects__gone-name')).toHaveText(['a/one', 'x1', 'x2', 'x3'])
  await dialog.getByRole('button', { name: 'Remove 4 projects' }).click()
  await expect(dialog).toBeHidden()

  const result = page.locator('[data-batch-result]')
  await expect(result.locator('[data-batch-summary]')).toHaveText('Removed 3; 1 could not be removed.')
  const refused = result.locator('[data-batch-refused]')
  await expect(refused).toHaveCount(1)
  await expect(refused).toContainText('a/one')
  await expect(refused).toContainText(`"a/one" lies under the workspace root ${ws!.root} and would be found again at the next scan.`)
  await expect(box(page, 'a/one')).toBeChecked()
  await expect(count(page)).toHaveText('1 selected')
  expect(await names(omo)).toEqual(['a/one', 'b/two', 'demo'])

  // The way out of the single row works here too.
  await refused.getByRole('button', { name: 'Switch it off' }).click()
  await expect(refused).toHaveCount(0)
  await expect.poll(async () => (await list(omo)).find((p) => p.name === 'a/one')!.enabled).toBe(false)
})

test('switch several off and on again', async ({ page, omo }) => {
  await setUp(omo)
  await openProjects(page, omo)
  await page.getByRole('button', { name: 'Select…' }).click()
  await box(page, 'x1').click()
  await box(page, 'x3').click({ modifiers: ['Shift'] })
  await expect(count(page)).toHaveText('3 selected')
  await bulk(page).getByRole('button', { name: 'Switch off' }).click()
  await expect(page.locator('[data-batch-summary]')).toHaveText('Switched off 3.')
  await expect.poll(async () => (await list(omo)).filter((p) => !p.enabled).map((p) => p.name).sort()).toEqual(['x1', 'x2', 'x3'])
  await expect(row(page, 'x2').locator('.projects__state')).toHaveText('disabled')

  await bulk(page).getByRole('button', { name: 'Switch on' }).click()
  await expect(page.locator('[data-batch-summary]')).toHaveText('Switched on 3.')
  await expect.poll(async () => (await list(omo)).filter((p) => !p.enabled)).toEqual([])
})

test('the keyboard alone: space toggles, shift with an arrow extends, the count is announced, Escape leaves', async ({ page, omo }) => {
  await setUp(omo)
  await openProjects(page, omo)
  await filter(page).focus()
  await page.keyboard.type('extra')
  await page.keyboard.press('Tab')
  await expect(page.getByRole('button', { name: 'Select…' })).toBeFocused()
  await page.keyboard.press('Enter')
  // To the first box: through Select all, Clear selection (disabled, skipped), the actions, then the rows.
  await expect(page.getByRole('button', { name: 'Done' })).toBeFocused()
  for (let i = 0; i < 6 && !(await box(page, 'x1').evaluate((e) => e === document.activeElement)); i++) await page.keyboard.press('Tab')
  await expect(box(page, 'x1')).toBeFocused()
  await page.keyboard.press('Space')
  await expect(box(page, 'x1')).toBeChecked()
  await page.keyboard.press('Shift+ArrowDown')
  await page.keyboard.press('Shift+ArrowDown')
  await expect(box(page, 'x3')).toBeFocused()
  await expect(count(page)).toHaveText('3 selected')
  await expect(page.locator('[data-announcer="polite"]')).toHaveText('3 selected')
  await page.keyboard.press('Space')
  await expect(count(page)).toHaveText('2 selected')
  await page.keyboard.press('Escape')
  await expect(page.locator('[data-selection-count]')).toHaveCount(0)
  await expect(page.getByRole('checkbox')).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Select…' })).toBeFocused()
})

test.describe('at 390 px', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('80 projects: the actions are a bar at the bottom above Save, nothing scrolls sideways', async ({ page, omo }) => {
    await many(omo, 80)
    await openProjects(page, omo)
    await page.getByRole('button', { name: 'Select…' }).click()
    await filter(page).fill('service-1')
    await page.getByRole('button', { name: 'Select all 10 shown' }).click()
    await expect(count(page)).toHaveText('10 selected')
    await box(page, 'clients/some-organisation-with-a-long-name/service-15').scrollIntoViewIfNeeded()
    expect(await sideways(page)).toBe(false)
    const bar = (await bulk(page).boundingBox())!
    const save = (await page.getByRole('region', { name: 'Save changes' }).boundingBox())!
    expect(bar.y + bar.height).toBeLessThanOrEqual(save.y + 1)
    expect(save.y + save.height).toBeLessThanOrEqual(844)
    await bulk(page).getByRole('button', { name: 'Remove…' }).click()
    await expect(page.getByRole('dialog', { name: 'Remove 10 projects?' })).toBeVisible()
    expect(await sideways(page)).toBe(false)
  })
})

for (const width of [1440, 390] as const) {
  for (const colorScheme of ['dark', 'light'] as const) {
    test.describe(`axe at ${width} px, ${colorScheme} theme`, () => {
      test.use({ viewport: { width, height: width === 390 ? 844 : 900 }, colorScheme, reducedMotion: 'reduce' })

      test('the select mode, a batch answer with a refusal and the remove dialog have no violations', async ({ page, omo }) => {
        await setUp(omo)
        await openProjects(page, omo)
        await page.getByRole('button', { name: 'Select…' }).click()
        await box(page, 'a/one').click()
        await box(page, 'x1').click()
        expect(await violations(page), 'select mode').toEqual([])
        await bulk(page).getByRole('button', { name: 'Remove…' }).click()
        const dialog = page.getByRole('dialog', { name: 'Remove 2 projects?' })
        expect(await violations(page), 'dialog').toEqual([])
        await dialog.getByRole('button', { name: 'Remove 2 projects' }).click()
        await expect(page.locator('[data-batch-refused]')).toHaveCount(1)
        expect(await violations(page), 'answer').toEqual([])
        expect(await sideways(page)).toBe(false)
      })
    })
  }
}
