import AxeBuilder from '@axe-core/playwright'
import type { Page } from '@playwright/test'
import { expectBoardLive, openBoard } from './support/board'
import { expect, test } from './support/omo'

const SECTIONS = [
  ['projects', 'Projects'],
  ['agents', 'Agents'],
  ['roles', 'Roles'],
  ['prompts', 'Prompts'],
  ['environment', 'Environment'],
  ['limits', 'Limits'],
  ['review', 'Review'],
  ['general', 'General'],
] as const

const nav = (page: Page) => page.getByRole('navigation', { name: 'Settings' })
const title = (page: Page) => page.locator('[data-settings-page] h1')

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

/** The board with a filter in its address, so the way back can be checked. */
async function boardWithFilter(page: Page, omo: Parameters<typeof openBoard>[1]) {
  await openBoard(page, omo)
  await page.goto(omo.url('/?q=hello&needs_me=1'))
  await expectBoardLive(page)
}

test('the gear opens the settings; every section is reachable by keyboard; Back to board keeps the filters', async ({ page, omo }) => {
  await boardWithFilter(page, omo)
  await page.getByRole('button', { name: 'Settings' }).click()
  await page.getByRole('menuitem', { name: 'Settings' }).click()
  await expect(page).toHaveURL(/\/settings\/projects\?q=hello&needs_me=1$/)
  await expect(title(page)).toHaveText('Projects')
  // The settings replace the board; the filters give way to the link back.
  await expect(page.locator('[data-board]')).toHaveCount(0)
  await expect(page.getByRole('search')).toHaveCount(0)
  await expect(nav(page).getByRole('link', { name: /^Projects/ })).toHaveAttribute('aria-current', 'page')

  // Tab from one entry to the next, Enter opens it.
  await nav(page).getByRole('link', { name: /^Projects/ }).focus()
  for (const [id, label] of SECTIONS.slice(1)) {
    await page.keyboard.press('Tab')
    await expect(page.locator(':focus')).toHaveAttribute('data-section', id)
    await page.keyboard.press('Enter')
    await expect(title(page)).toHaveText(label)
    await expect(page).toHaveURL(new RegExp(`/settings/${id}\\?q=hello&needs_me=1$`))
    await expect(nav(page).locator('[aria-current="page"]')).toHaveAttribute('data-section', id)
  }

  await page.getByRole('link', { name: 'Back to board' }).click()
  await expectBoardLive(page)
  await expect(page).toHaveURL(/\/\?q=hello&needs_me=1$/)
  await expect(page.getByRole('searchbox', { name: 'Search tickets' })).toHaveValue('hello')
})

test('the gear opens Projects and the agent setup; an address of a section opens it, an unknown one shows Projects with a notice', async ({ page, omo }) => {
  await openBoard(page, omo)
  await page.getByRole('button', { name: 'Settings' }).click()
  await page.getByRole('menuitem', { name: 'Projects' }).click()
  await expect(title(page)).toHaveText('Projects')
  await expect(page.locator('[data-project]')).toHaveCount(1)

  await page.getByRole('button', { name: 'Settings' }).click()
  await page.getByRole('menuitem', { name: 'Agent setup' }).click()
  await expect(page).toHaveURL(/\/settings\/roles$/)
  await expect(title(page)).toHaveText('Roles')
  await expect(page.locator('[data-setup-report]')).toBeVisible()

  await page.goto(omo.url('/settings/limits'))
  await expect(title(page)).toHaveText('Limits')
  await page.goto(omo.url('/settings/nothing'))
  await expect(title(page)).toHaveText('Projects')
  await expect(page.locator('[data-settings-notice]')).toContainText('There is no settings section “nothing”.')
  // Back and forward through the sections like any other address.
  await page.goBack()
  await expect(title(page)).toHaveText('Limits')
})

test.describe('at 390 px', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('the section list is a page and each section has Back', async ({ page, omo }) => {
    await boardWithFilter(page, omo)
    await page.getByRole('button', { name: 'Settings' }).click()
    await page.getByRole('menuitem', { name: 'Settings' }).click()
    await expect(title(page)).toHaveText('Projects')
    await expect(nav(page)).toHaveCount(0)
    await page.getByRole('link', { name: 'Settings', exact: true }).click()
    await expect(page).toHaveURL(/\/settings\?q=hello&needs_me=1$/)
    await expect(nav(page)).toBeVisible()
    await expect(page.locator('[data-settings-page]')).toHaveCount(0)
    expect(await pageScrollsSideways(page)).toBe(false)

    for (const [id, label] of SECTIONS) {
      await nav(page).locator(`[data-section="${id}"]`).click()
      await expect(title(page)).toHaveText(label)
      await expect(title(page)).toBeFocused()
      expect(await pageScrollsSideways(page)).toBe(false)
      await page.getByRole('link', { name: 'Settings', exact: true }).click()
      await expect(nav(page)).toBeVisible()
      await expect(nav(page).locator(`[data-section="${id}"]`)).toBeFocused()
    }
    await page.getByRole('link', { name: 'Back to board' }).click()
    await expectBoardLive(page)
    await expect(page).toHaveURL(/\/\?q=hello&needs_me=1$/)
  })
})

for (const scheme of ['dark', 'light'] as const) {
  test.describe(`${scheme} theme`, () => {
    test.use({ colorScheme: scheme })

    test('every settings section has no accessibility violations', async ({ page, omo }) => {
      await openBoard(page, omo)
      for (const [id, label] of SECTIONS) {
        await page.goto(omo.url(`/settings/${id}`))
        await expect(title(page)).toHaveText(label)
        if (id === 'roles') await expect(page.locator('[data-setup-report] [role="status"]')).toBeVisible()
        expect(await violations(page)).toEqual([])
      }
    })

    test('the section list at 390 px has no accessibility violations', async ({ page, omo }) => {
      await page.setViewportSize({ width: 390, height: 844 })
      await openBoard(page, omo)
      await page.goto(omo.url('/settings'))
      await expect(nav(page)).toBeVisible()
      expect(await violations(page)).toEqual([])
      await page.goto(omo.url('/settings/projects'))
      await expect(page.getByRole('link', { name: 'Settings', exact: true })).toBeVisible()
      expect(await violations(page)).toEqual([])
    })
  })
}
