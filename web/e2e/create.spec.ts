import { mkdirSync, realpathSync } from 'node:fs'
import { join } from 'node:path'
import AxeBuilder from '@axe-core/playwright'
import type { Page } from '@playwright/test'
import { card, column, expectCardIn, openBoard } from './support/board'
import { expect, test } from './support/omo'

const createDialog = (page: Page) => page.getByRole('dialog', { name: 'Create ticket' })
/** A settings page by its title. */
const settingsPage = (page: Page, title: string) => page.locator('section[data-settings-page]').filter({ has: page.getByRole('heading', { level: 1, name: title }) })
const projectsPage = (page: Page) => settingsPage(page, 'Projects')

async function openCreate(page: Page) {
  await page.getByRole('button', { name: 'Create ticket' }).click()
  await expect(createDialog(page)).toBeVisible()
}

test.describe('with a project', () => {
  test('a story, a task in it and a standalone task are created and appear once in Planning', async ({ page, omo }) => {
    await openBoard(page, omo)

    // A story with the template, the project preselected.
    await openCreate(page)
    const dialog = createDialog(page)
    await expect(dialog.getByLabel('Title')).toBeFocused()
    await expect(dialog.getByLabel('Description')).toHaveValue(/## Goal[\s\S]*## Done when/)
    await dialog.getByLabel('Title').fill('Ship the login')
    await dialog.getByRole('button', { name: 'Create', exact: true }).click()
    await expect(dialog).toBeHidden()
    await expect(page.getByText('#1 created')).toBeVisible()
    await expectCardIn(page, 1, 'Planning')
    await expect(card(page, 1)).toContainText('Ship the login')

    // A task in the story.
    await page.keyboard.press('c')
    await expect(dialog).toBeVisible()
    await dialog.getByRole('radio', { name: 'Task' }).check({ force: true })
    await dialog.getByLabel('Title').fill('Build the form')
    await dialog.getByLabel('Story (optional)').selectOption('1')
    await dialog.getByRole('button', { name: 'Create', exact: true }).click()
    await expect(dialog).toBeHidden()
    await expectCardIn(page, 2, 'Planning')
    await expect(card(page, 2)).toContainText('#1')
    await expect(card(page, 2)).toContainText('Build the form')

    // A standalone task from the add button of the column.
    await column(page, 'Planning').getByRole('button', { name: 'Add to Planning' }).click()
    await expect(dialog).toBeVisible()
    await dialog.getByRole('radio', { name: 'Task' }).check({ force: true })
    await dialog.getByLabel('Title').fill('Fix the typo')
    await dialog.getByRole('button', { name: 'Create', exact: true }).click()
    await expect(dialog).toBeHidden()
    await expectCardIn(page, 3, 'Planning')
    await expect(card(page, 3)).not.toContainText('Ship the login')
    for (const n of [1, 2, 3]) await expect(card(page, n)).toHaveCount(1)

    const server = await omo.api<{ story: number | null; projects: { project: string }[] }>('GET', '/api/tickets/2')
    expect(server.story).toBe(1)
    expect(server.projects.map((p) => p.project)).toEqual([omo.project])
  })

  test('a title with a hidden character is refused under Title before any request, and what was typed is kept', async ({ page, omo }) => {
    await openBoard(page, omo)
    await openCreate(page)
    const dialog = createDialog(page)
    // The dialog checks the same characters as the server (internal/textsafe), so a
    // right-to-left override is refused here and no request is sent.
    const posts: string[] = []
    page.on('request', (r) => {
      if (r.method() === 'POST' && new URL(r.url()).pathname === '/api/tickets') posts.push(r.url())
    })
    await dialog.getByLabel('Title').fill('Looks fine ‮ but is not')
    await dialog.getByLabel('Description').fill('Typed text stays')
    await dialog.getByRole('button', { name: 'Create', exact: true }).click()
    await expect(dialog.locator('[data-field="title"] .omo-field__error')).toHaveText('The title must be one line without control characters.')
    expect(posts).toEqual([])
    await expect(dialog.getByLabel('Title')).toBeFocused()
    await expect(dialog.getByLabel('Description')).toHaveValue('Typed text stays')
    await expect(dialog.getByRole('button', { name: 'Create', exact: true })).toBeEnabled()
    expect(await omo.api<{ tickets: unknown[] }>('GET', '/api/tickets').then((r) => r.tickets)).toHaveLength(0)

    await dialog.getByLabel('Title').fill('Fixed title')
    await dialog.getByRole('button', { name: 'Create', exact: true }).click()
    await expect(dialog).toBeHidden()
    await expectCardIn(page, 1, 'Planning')
  })

  test('c opens the dialog; Escape closes it at once when empty and asks before discarding typed text', async ({ page, omo }) => {
    await openBoard(page, omo)
    await page.keyboard.press('c')
    const dialog = createDialog(page)
    await expect(dialog).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(dialog).toBeHidden()

    await page.keyboard.press('c')
    await dialog.getByLabel('Title').fill('Half a thought')
    await page.keyboard.press('Escape')
    await expect(dialog).toBeVisible()
    await expect(dialog.getByText('Discard what you typed?')).toBeVisible()
    await dialog.getByRole('button', { name: 'Keep editing' }).click()
    await expect(dialog.getByLabel('Title')).toHaveValue('Half a thought')
    await page.keyboard.press('Escape')
    await dialog.getByRole('button', { name: 'Discard' }).click()
    await expect(dialog).toBeHidden()
    // Focus is back where it was before the dialog opened.
    await expect(page.locator('body')).toBeFocused()
  })
})

test.describe('without a project', () => {
  test.use({ omoOptions: { noProject: true } })

  test('a project is added by its path from the create dialog and chosen for the new ticket', async ({ page, omo, repo }) => {
    await openBoard(page, omo)
    await openCreate(page)
    const dialog = createDialog(page)
    await expect(dialog.getByText('No project yet.')).toBeVisible()
    await dialog.getByRole('button', { name: 'Add project…' }).click()
    const add = page.getByRole('dialog', { name: 'Add project' })
    await expect(add.getByLabel('Folder')).toBeFocused()
    await add.getByLabel('Folder').fill(repo)
    await add.getByRole('button', { name: 'Add project' }).click()
    await expect(add).toBeHidden()
    await expect(dialog.getByRole('checkbox', { checked: true })).toHaveCount(1)
    await dialog.getByLabel('Title').fill('Uses the new project')
    await dialog.getByRole('button', { name: 'Create', exact: true }).click()
    await expect(dialog).toBeHidden()
    await expectCardIn(page, 1, 'Planning')
    const projects = await omo.api<{ path: string }[]>('GET', '/api/projects')
    expect(projects.map((p) => p.path)).toEqual([realpathSync(repo)])
  })

  test('a project added after a title was typed does not submit the create dialog', async ({ page, omo, repo }) => {
    await openBoard(page, omo)
    await openCreate(page)
    const dialog = createDialog(page)
    await dialog.getByLabel('Title').fill('Typed before the project')
    await dialog.getByRole('button', { name: 'Add project…' }).click()
    const add = page.getByRole('dialog', { name: 'Add project' })
    await add.getByLabel('Folder').fill(repo)
    await add.getByRole('button', { name: 'Add project' }).click()
    await expect(add).toBeHidden()
    await expect(dialog).toBeVisible()
    await expect(dialog.getByLabel('Title')).toHaveValue('Typed before the project')
    await expect(dialog.getByRole('checkbox', { checked: true })).toHaveCount(1)
    expect((await omo.api<{ tickets: unknown[] }>('GET', '/api/tickets')).tickets).toHaveLength(0)
    // Enter inside the nested dialog adds the project and nothing else.
    await dialog.getByRole('button', { name: 'Create', exact: true }).click()
    await expect(dialog).toBeHidden()
    const made = (await omo.api<{ tickets: { projects: unknown[] }[] }>('GET', '/api/tickets')).tickets
    expect(made).toHaveLength(1)
    expect(made[0]?.projects).toHaveLength(1)
  })

  test('a folder that is not a repository, or does not exist, shows the server message', async ({ page, omo }) => {
    await openBoard(page, omo)
    await page.getByRole('button', { name: 'Settings' }).click()
    await page.getByRole('menuitem', { name: 'Projects' }).click()
    const list = projectsPage(page)
    await expect(list.getByText('No project yet.')).toBeVisible()
    await list.getByRole('button', { name: 'Add project' }).click()
    const add = page.getByRole('dialog', { name: 'Add project' })

    // Inside the test's home, so it goes with it.
    const plain = join(omo.home, 'plain')
    mkdirSync(plain, { recursive: true })
    await add.getByLabel('Folder').fill(plain)
    await add.getByRole('button', { name: 'Add project' }).click()
    await expect(add.locator('.omo-field__error')).toContainText('is not a git repository')
    await expect(add.getByLabel('Folder')).toHaveValue(plain)

    await add.getByLabel('Folder').fill(join(plain, 'nope'))
    await add.getByRole('button', { name: 'Add project' }).click()
    await expect(add.locator('.omo-field__error')).toContainText('cannot be resolved')

    await add.getByLabel('Folder').fill('relative/path')
    await add.getByRole('button', { name: 'Add project' }).click()
    await expect(add.locator('.omo-field__error')).toContainText('must be absolute')
    expect(await omo.api<unknown[]>('GET', '/api/projects')).toHaveLength(0)
  })

  test('the project list shows an added project as enabled', async ({ page, omo, repo }) => {
    await openBoard(page, omo)
    await omo.api('POST', '/api/projects', { path: repo, name: 'listed' })
    await page.getByRole('button', { name: 'Settings' }).click()
    await page.getByRole('menuitem', { name: 'Projects' }).click()
    const row = projectsPage(page).getByRole('row', { name: /listed/ })
    await expect(row).toContainText('enabled')
  })
})

test.describe('theme', () => {
  const background = (page: Page) => page.evaluate(() => getComputedStyle(document.body).backgroundColor)

  async function choose(page: Page, label: 'System' | 'Dark' | 'Light') {
    await page.getByRole('button', { name: 'Settings' }).click()
    await page.getByRole('menuitem', { name: 'Theme' }).click()
    const dialog = page.getByRole('dialog', { name: 'Theme' })
    await dialog.getByRole('radio', { name: label }).check({ force: true })
    await dialog.getByRole('button', { name: 'Close' }).first().click()
    await expect(dialog).toBeHidden()
  }

  test('the choice switches to light, survives a reload, and System follows the browser\'s color scheme', async ({ page, omo }) => {
    await page.emulateMedia({ colorScheme: 'dark' })
    await openBoard(page, omo)
    const dark = await background(page)

    await choose(page, 'Light')
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
    const light = await background(page)
    expect(light).not.toBe(dark)

    await page.reload()
    await expect(page.locator('html')).toHaveAttribute('data-theme', 'light')
    expect(await background(page)).toBe(light)

    // System follows the browser again.
    await choose(page, 'System')
    await expect(page.locator('html')).not.toHaveAttribute('data-theme', /.+/)
    expect(await background(page)).toBe(dark)
    await page.emulateMedia({ colorScheme: 'light' })
    expect(await background(page)).toBe(light)
    await page.reload()
    await expect(page.locator('html')).not.toHaveAttribute('data-theme', /.+/)
    expect(await background(page)).toBe(light)
  })
})

for (const scheme of ['dark', 'light'] as const) {
  test.describe(`${scheme} dialogs`, () => {
    test.use({ colorScheme: scheme })

    test('the create dialog, the projects page and the agent setup have no accessibility violations', async ({ page, omo }) => {
      await omo.createTicket({ title: 'A story', type: 'story' })
      await openBoard(page, omo)
      const check = async () => (await new AxeBuilder({ page }).analyze()).violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target.join(' ')) }))

      await openCreate(page)
      await createDialog(page).getByText('More options').click()
      expect(await check()).toEqual([])
      await page.keyboard.press('Escape')

      await page.getByRole('button', { name: 'Settings' }).click()
      await page.getByRole('menuitem', { name: 'Projects' }).click()
      await expect(projectsPage(page).getByRole('row', { name: /demo/ })).toBeVisible()
      expect(await check()).toEqual([])

      await page.getByRole('button', { name: 'Settings' }).click()
      await page.getByRole('menuitem', { name: 'Agent setup' }).click()
      await expect(settingsPage(page, 'Roles').getByRole('heading', { name: 'Roles', level: 2 })).toBeVisible()
      expect(await check()).toEqual([])
    })
  })
}
