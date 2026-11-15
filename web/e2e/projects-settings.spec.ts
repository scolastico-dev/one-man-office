import AxeBuilder from '@axe-core/playwright'
import type { Page } from '@playwright/test'
import { readFileSync, rmSync, writeFileSync } from 'node:fs'
import { basename, join } from 'node:path'
import { openBoard } from './support/board'
import { initRepo } from './support/git'
import { expect, test, type Omo } from './support/omo'
import { makeWorkspace, type Workspace, type WorkspaceEntry } from './support/workspace'

// a/one and b/two are repositories; node_modules/x is one too but sits in an
// ignored folder; c/wt is a linked worktree of a/one (its .git is a file);
// d is a link to a. Only a/one and b/two may become projects.
const LAYOUT = {
  'a/one': 'repo',
  'b/two': 'repo',
  'node_modules/x': 'repo',
  'c/wt': { worktree: 'a/one' },
  d: { link: 'a' },
} as const

const title = (page: Page) => page.locator('[data-settings-page] h1')
const rows = (page: Page) => page.locator('tr[data-project]')
const row = (page: Page, name: string) => page.locator('tr[data-project]', { has: page.getByRole('rowheader', { name, exact: true }) })
const rootRow = (page: Page, i: number) => page.locator(`[data-root="${i}"]`)
const picker = (page: Page) => page.getByRole('dialog').filter({ has: page.locator('[data-folder-picker]') })
const saveBar = (page: Page) => page.getByRole('region', { name: 'Save changes' })

let ws: Workspace | null = null
test.afterEach(() => {
  ws?.cleanup()
  ws = null
})

async function openProjects(page: Page, omo: Omo) {
  await openBoard(page, omo)
  await page.goto(omo.url('/settings/projects'))
  await expect(title(page)).toHaveText('Projects')
  await expect(page.locator('[data-workspace-roots]')).toBeVisible()
}

async function projectNames(omo: Omo): Promise<string[]> {
  const list = await omo.api<{ name: string }[]>('GET', '/api/projects?all=true')
  return list.map((p) => p.name).sort()
}

/** Makes the layout a workspace folder of omo through the API, and waits for its projects. */
async function withRoot(omo: Omo, layout: Record<string, WorkspaceEntry> = LAYOUT, names = ['a/one', 'b/two', 'demo']): Promise<Workspace> {
  ws = makeWorkspace(layout, omo.home)
  await omo.api('PATCH', '/api/settings', { workspace_roots: [{ path: ws.root, max_depth: 4, ignore: ['node_modules', '.*'] }] })
  await expect.poll(() => projectNames(omo)).toEqual(names)
  return ws
}

async function violations(page: Page) {
  await page.evaluate(() => document.fonts.ready.then(() => undefined))
  const results = await new AxeBuilder({ page }).analyze()
  return results.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target.join(' ')) }))
}

async function pageScrollsSideways(page: Page): Promise<boolean> {
  return page.evaluate(() => {
    const root = document.documentElement
    return root.scrollWidth > root.clientWidth || document.body.scrollWidth > document.body.clientWidth
  })
}

test('adding a workspace folder with the picker finds a/one and b/two at once, and not node_modules/x, the worktree or the link', async ({ page, omo }) => {
  ws = makeWorkspace(LAYOUT, omo.home)
  await openProjects(page, omo)
  await expect(page.locator('[data-scan-line]')).toHaveText('Add a workspace folder to look for repositories.')

  await page.getByRole('button', { name: 'Add workspace folder…' }).click()
  const dialog = picker(page)
  await expect(dialog.getByRole('heading', { name: 'Choose a workspace folder' })).toBeVisible()
  // The picker starts in the home folder (the test's home); open the workspace from there.
  await expect(dialog.locator('[data-picker-where]')).toContainText(omo.home)
  await dialog.locator(`[data-folder="${basename(ws.root)}"]`).click()
  await expect(dialog.locator('[data-picker-where]')).toContainText(ws.root)
  await expect(dialog.locator('[data-folder]')).toHaveText([/^a$/, /^b$/, /^c$/, /^d\s*link$/, /^node_modules$/])
  await dialog.getByRole('button', { name: 'Choose this folder' }).click()
  await expect(dialog).toBeHidden()

  await expect(rootRow(page, 0).getByLabel('Folder 1', { exact: true })).toHaveValue(ws.root)
  await expect(rootRow(page, 0).getByRole('textbox', { name: 'Pattern 1' })).toHaveValue('node_modules')
  await expect(rootRow(page, 0).getByRole('textbox', { name: 'Pattern 2' })).toHaveValue('.*')
  await saveBar(page).getByRole('button', { name: 'Save' }).click()
  await expect(saveBar(page).locator('[data-save-state]')).toContainText('Saved')

  await expect(row(page, 'a/one')).toBeVisible()
  await expect(row(page, 'b/two')).toBeVisible()
  await expect(row(page, 'a/one').locator('[data-project-origin]')).toHaveText(`found in ${ws.root}`)
  await expect(row(page, 'demo').locator('[data-project-origin]')).toHaveText('added by hand')
  await expect(rows(page)).toHaveCount(3)
  expect(await projectNames(omo)).toEqual(['a/one', 'b/two', 'demo'])
  await expect(page.locator('[data-scan-line]')).toContainText('2 added')
})

test('rename and switch off survive a rescan', async ({ page, omo }) => {
  await withRoot(omo)
  await openProjects(page, omo)

  await row(page, 'a/one').getByRole('button', { name: 'Edit a/one' }).click()
  const name = page.locator('input[data-project-rename]')
  await name.fill('First app')
  await name.press('Enter')
  await expect(row(page, 'First app')).toBeVisible()

  await row(page, 'b/two').getByRole('switch', { name: 'Use b/two' }).click()
  await expect(row(page, 'b/two').locator('.projects__state')).toHaveText('disabled')

  await page.getByRole('button', { name: 'Rescan' }).click()
  await expect(page.locator('[data-scan-line]')).toContainText('nothing new')
  await expect(rows(page)).toHaveCount(3)
  await expect(row(page, 'First app')).toBeVisible()
  await expect(row(page, 'b/two').getByRole('switch', { name: 'Use b/two' })).toHaveAttribute('aria-checked', 'false')
  expect(await projectNames(omo)).toEqual(['First app', 'b/two', 'demo'])
})

test('a removed repository folder shows as missing and Delete all missing removes it', async ({ page, omo }) => {
  const w = await withRoot(omo)
  await openProjects(page, omo)
  await expect(page.locator('[data-remove-missing]')).toHaveCount(0)

  rmSync(w.path('b/two'), { recursive: true, force: true })
  await page.getByRole('button', { name: 'Rescan' }).click()
  await expect(row(page, 'b/two').locator('[data-project-missing]')).toHaveText('The folder is gone. New tickets cannot use it.')
  await expect(row(page, 'b/two').getByRole('switch')).toHaveCount(0)
  await expect(page.locator('[data-scan-line]')).toContainText('1 missing')

  await page.getByRole('button', { name: 'Delete all missing projects…' }).click()
  const dialog = page.getByRole('dialog', { name: 'Delete all missing projects?' })
  await expect(dialog).toContainText('b/two')
  await expect(dialog).not.toContainText('a/one')
  await dialog.getByRole('button', { name: 'Delete 1 project' }).click()
  await expect(dialog).toBeHidden()
  await expect(row(page, 'b/two')).toHaveCount(0)
  expect(await projectNames(omo)).toEqual(['a/one', 'demo'])
})

test('removing a project found under a workspace folder is refused with the reason; an ignore pattern is the way out', async ({ page, omo }) => {
  await withRoot(omo)
  await openProjects(page, omo)

  const one = row(page, 'a/one')
  await one.getByRole('button', { name: 'Edit a/one' }).click()
  await page.locator('[data-project-remove]').click()
  await page.locator('[data-project-remove-confirm]').click()
  const message = page.locator('[data-project-message]')
  await expect(message.locator('[data-project-error]')).toHaveText(
    `"a/one" lies under the workspace root ${ws!.root} and would be found again at the next scan. Switch it off, or add an ignore pattern to the root.`,
  )
  await expect(message.getByRole('button', { name: 'Switch it off' })).toBeVisible()
  await message.getByRole('button', { name: 'Add an ignore pattern' }).click()
  await expect(rootRow(page, 0).getByRole('textbox', { name: 'Pattern 3' })).toHaveValue('a/one')
  await expect(rootRow(page, 0).getByRole('textbox', { name: 'Pattern 3' })).toBeFocused()
  await saveBar(page).getByRole('button', { name: 'Save' }).click()
  await expect(saveBar(page).locator('[data-save-state]')).toContainText('Saved')

  await page.locator('[data-project-remove]').click()
  await page.locator('[data-project-remove-confirm]').click()
  await expect(row(page, 'a/one')).toHaveCount(0)
  expect(await projectNames(omo)).toEqual(['b/two', 'demo'])
})

test('Add project with Browse… adds a folder outside every workspace folder', async ({ page, omo }) => {
  const outside = join(omo.home, 'elsewhere', 'tool')
  initRepo(outside)
  await openProjects(page, omo)
  await page.getByRole('button', { name: 'Add project' }).click()
  const add = page.getByRole('dialog', { name: 'Add project' })
  await add.getByRole('button', { name: 'Browse…' }).click()
  const dialog = picker(page)
  await dialog.getByLabel('Folder', { exact: true }).fill(join(omo.home, 'elsewhere'))
  await dialog.getByLabel('Folder', { exact: true }).press('Enter')
  await expect(dialog.locator('[data-folder="tool"]')).toContainText('git repository')
  await dialog.locator('[data-folder="tool"]').click()
  await dialog.getByRole('button', { name: 'Choose this folder' }).click()
  await expect(dialog).toBeHidden()
  await expect(add.getByLabel('Folder', { exact: true })).toHaveValue(outside)
  await add.getByRole('button', { name: 'Add project' }).click()
  await expect(add).toBeHidden()
  await expect(row(page, 'tool').locator('[data-project-origin]')).toHaveText('added by hand')
})

test('the picker says so when a typed folder does not exist or is not absolute', async ({ page, omo }) => {
  await openProjects(page, omo)
  await page.getByRole('button', { name: 'Add workspace folder…' }).click()
  const dialog = picker(page)
  const field = dialog.getByLabel('Folder', { exact: true })
  await expect(dialog.locator('[data-picker-where]')).toContainText(omo.home)
  await field.fill(join(omo.home, 'nothing-here'))
  await field.press('Enter')
  await expect(dialog.locator('.omo-field__error')).toHaveText(`There is no folder ${join(omo.home, 'nothing-here')}.`)
  await expect(field).toHaveAttribute('aria-invalid', 'true')
  await field.fill('relative/path')
  await field.press('Enter')
  await expect(dialog.locator('.omo-field__error')).toHaveText('the path must be absolute')
  await dialog.getByRole('button', { name: 'Cancel' }).click()
  await expect(dialog).toBeHidden()
  await expect(page.locator('[data-root]')).toHaveCount(0)
})

test('a hand edit of settings.yaml while the page is open: shown at once, or kept as a conflict with a draft', async ({ page, omo }) => {
  ws = makeWorkspace(LAYOUT, omo.home)
  const file = join(omo.home, 'omo', 'settings.yaml')
  const original = readFileSync(file, 'utf8')
  const write = (depth: number) =>
    writeFileSync(file, `${original}workspace_roots:\n  - path: ${JSON.stringify(ws!.root)}\n    max_depth: ${depth}\n    ignore: [node_modules, ".*"]\n`)
  await openProjects(page, omo)

  // No draft: the page shows the new folder, says it changed on disk, and the scan finds its projects.
  write(4)
  await expect(rootRow(page, 0).getByLabel('Folder 1', { exact: true })).toHaveValue(ws.root, { timeout: 15_000 })
  await expect(page.locator('[data-changed-on-disk]')).toContainText('Changed on disk at')
  await expect(row(page, 'a/one')).toBeVisible()

  // A draft: it stays, and the new value is offered.
  await rootRow(page, 0).getByLabel('Search depth', { exact: true }).fill('6')
  write(2)
  await expect(saveBar(page).getByRole('button', { name: 'Use the new value' })).toBeVisible({ timeout: 15_000 })
  await expect(rootRow(page, 0).getByLabel('Search depth', { exact: true })).toHaveValue('6')
  // Saving over the hand edit is refused until the user chooses.
  await saveBar(page).getByRole('button', { name: 'Save' }).click()
  await expect(saveBar(page).locator('[data-save-error]')).toContainText('Choose “Use the new value” or “Keep mine” first.')
  expect(readFileSync(file, 'utf8')).toContain('max_depth: 2')
  await saveBar(page).getByRole('button', { name: 'Use the new value' }).click()
  await expect(rootRow(page, 0).getByLabel('Search depth', { exact: true })).toHaveValue('2')
  await expect(saveBar(page).getByRole('button', { name: 'Save' })).toBeDisabled()
})

test('Search depth that is no number or empty blocks Save; Discard drops the text', async ({ page, omo }) => {
  const sent: string[] = []
  page.on('request', (r) => {
    if (r.method() === 'PATCH') sent.push(r.postData() ?? '')
  })
  const w = await withRoot(omo)
  await openProjects(page, omo)
  const depth = rootRow(page, 0).getByRole('spinbutton', { name: 'Search depth' })
  await expect(depth).toHaveValue('4')
  const save = saveBar(page).getByRole('button', { name: 'Save', exact: true })

  // Text that is no number, with the number itself unchanged: Discard is offered and drops it.
  await depth.fill('4x')
  await expect(rootRow(page, 0)).toContainText('Enter a whole number')
  await expect(saveBar(page)).toContainText('Unsaved changes')
  await save.click()
  await expect(saveBar(page)).toContainText('Fix the fields marked above before saving.')
  await expect(depth).toBeFocused()
  await saveBar(page).getByRole('button', { name: 'Discard' }).click()
  await expect(depth).toHaveValue('4')
  await expect(rootRow(page, 0)).not.toContainText('Enter a whole number')
  await expect(saveBar(page)).toContainText('No unsaved changes')

  // Emptied: blocked as well, never sent as null.
  await depth.fill('')
  await expect(rootRow(page, 0)).toContainText('Enter a whole number')
  await save.click()
  await expect(saveBar(page)).toContainText('Fix the fields marked above before saving.')
  expect(sent).toEqual([])

  await depth.fill('2')
  await expect(saveBar(page).getByText('Fix the fields marked above before saving.')).toHaveCount(0)
  await save.click()
  await expect(saveBar(page)).toContainText('Saved')
  expect(sent).toHaveLength(1)
  expect(JSON.parse(sent[0]!)).toEqual({ workspace_roots: [{ path: w.root, max_depth: 2, ignore: ['node_modules', '.*'] }] })
})

test.describe('at 390 px', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('roots and projects are usable without horizontal scrolling', async ({ page, omo }) => {
    const long = 'github.com/some-organisation-with-a-long-name/a-repository-with-a-rather-long-name'
    await withRoot(omo, { ...LAYOUT, [long]: 'repo' }, ['a/one', 'b/two', 'demo', long].sort())
    await openProjects(page, omo)
    expect(await pageScrollsSideways(page)).toBe(false)
    await row(page, 'b/two').getByRole('switch', { name: 'Use b/two' }).click()
    await expect(row(page, 'b/two').locator('.projects__state')).toHaveText('disabled')
    await row(page, 'a/one').getByRole('button', { name: 'Edit a/one' }).click()
    await expect(page.locator('input[data-project-rename]')).toBeVisible()
    expect(await pageScrollsSideways(page)).toBe(false)
    await page.getByRole('button', { name: 'Add workspace folder…' }).click()
    await expect(picker(page).locator('[data-folder]').first()).toBeVisible()
    expect(await pageScrollsSideways(page)).toBe(false)
  })
})

// Each theme on a page of its own, without transitions: axe must not see a
// colour halfway between two states (a button that just became enabled).
for (const width of [1440, 390] as const) {
  for (const colorScheme of ['dark', 'light'] as const) {
    test.describe(`axe at ${width} px, ${colorScheme} theme`, () => {
      test.use({ viewport: { width, height: width === 390 ? 844 : 900 }, colorScheme, reducedMotion: 'reduce' })

      test('the page, an open project and the folder picker have no violations', async ({ page, omo }) => {
        await withRoot(omo)
        await openProjects(page, omo)
        await row(page, 'a/one').getByRole('button', { name: 'Edit a/one' }).click()
        await expect(page.locator('input[data-project-rename]')).toBeVisible()
        await page.locator('[data-scan-details] summary').click()
        expect(await violations(page), 'page').toEqual([])
        await page.getByRole('button', { name: 'Add workspace folder…' }).click()
        await expect(picker(page).locator('[data-folder]').first()).toBeVisible()
        expect(await violations(page), 'picker').toEqual([])
      })
    })
  }
}
