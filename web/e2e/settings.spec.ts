import AxeBuilder from '@axe-core/playwright'
import type { Page } from '@playwright/test'
import { expectBoardLive, openBoard } from './support/board'
import { expect, test } from './support/omo'

const SECTIONS = [
  ['projects', 'Projects'],
  ['agents', 'Agents'],
  ['roles', 'Roles'],
  ['usage', 'Usage'],
  ['prompts', 'Prompts'],
  ['skills', 'Skill sets'],
  ['environment', 'Environment'],
  ['limits', 'Limits'],
  ['review', 'Review'],
  ['plugins', 'Plugins'],
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

/** The gear of the top bar: a link straight to the settings. */
const gear = (page: Page) => page.getByRole('banner').getByRole('link', { name: 'Settings', exact: true })
/** On a narrow screen, the settings page's own link back to the section list. */
const backToList = (page: Page) => page.getByRole('main').getByRole('link', { name: 'Settings', exact: true })

test('the gear opens the settings at once; every section is reachable by keyboard; Back to board keeps the filters', async ({ page, omo }) => {
  await boardWithFilter(page, omo)
  // No menu in between: the gear is a link to the settings.
  await expect(gear(page)).toHaveAttribute('href', '/settings?q=hello&needs_me=1')
  await gear(page).click()
  await expect(page.getByRole('menu')).toHaveCount(0)
  await expect(page).toHaveURL(/\/settings\?q=hello&needs_me=1$/)
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

test('the gear opens Projects, the theme is on General; an address of a section opens it, an unknown one shows Projects with a notice', async ({ page, omo }) => {
  await openBoard(page, omo)
  await gear(page).click()
  await expect(title(page)).toHaveText('Projects')
  await expect(page.locator('[data-project]')).toHaveCount(1)
  // What the gear's menu offered besides the pages: adding a project and the theme.
  await expect(page.getByRole('main').getByRole('button', { name: 'Add project' })).toBeVisible()
  await nav(page).getByRole('link', { name: /^General/ }).click()
  await expect(page.getByRole('group', { name: /^Theme/ })).toBeVisible()

  await nav(page).getByRole('link', { name: /^Roles/ }).click()
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

test('the gear works from a settings page and from an open ticket panel', async ({ page, omo }) => {
  const n = await omo.createTicket({ title: 'Open in the panel' })
  await openBoard(page, omo)
  await page.goto(omo.url('/settings/roles'))
  await expect(title(page)).toHaveText('Roles')
  await gear(page).click()
  await expect(page).toHaveURL(/\/settings$/)
  await expect(title(page)).toHaveText('Projects')

  await page.getByRole('link', { name: 'Back to board' }).click()
  await expectBoardLive(page)
  await page.goto(omo.url(`/${n}`))
  const panel = page.locator('dialog[data-ticket-panel]')
  await expect(panel).toBeVisible()
  // The panel is modal: the gear behind it points at the settings, not at the ticket, and works once the panel is closed.
  await expect(gear(page)).toHaveAttribute('href', '/settings')
  await page.keyboard.press('Escape')
  await expect(panel).toHaveCount(0)
  await gear(page).click()
  await expect(page).toHaveURL(/\/settings$/)
  await expect(title(page)).toHaveText('Projects')
})

test.describe('at 390 px', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('the gear works from a settings page and from an open ticket panel', async ({ page, omo }) => {
    const n = await omo.createTicket({ title: 'Open in the panel' })
    await openBoard(page, omo)
    await page.goto(omo.url('/settings/roles'))
    await expect(title(page)).toHaveText('Roles')
    await gear(page).click()
    await expect(page).toHaveURL(/\/settings$/)
    await expect(nav(page)).toBeVisible()

    await page.goto(omo.url(`/${n}`))
    const panel = page.locator('dialog[data-ticket-panel]')
    await expect(panel).toBeVisible()
    await expect(gear(page)).toHaveAttribute('href', '/settings')
    await page.keyboard.press('Escape')
    await expect(panel).toHaveCount(0)
    await gear(page).click()
    await expect(page).toHaveURL(/\/settings$/)
    await expect(nav(page)).toBeVisible()
    expect(await pageScrollsSideways(page)).toBe(false)
  })

  test('the section list is a page and each section has Back', async ({ page, omo }) => {
    await boardWithFilter(page, omo)
    // On a phone the gear opens the section list.
    await gear(page).click()
    await expect(page).toHaveURL(/\/settings\?q=hello&needs_me=1$/)
    await expect(nav(page)).toBeVisible()
    await nav(page).locator('[data-section="projects"]').click()
    await expect(title(page)).toHaveText('Projects')
    await expect(nav(page)).toHaveCount(0)
    await backToList(page).click()
    await expect(page).toHaveURL(/\/settings\?q=hello&needs_me=1$/)
    await expect(nav(page)).toBeVisible()
    await expect(page.locator('[data-settings-page]')).toHaveCount(0)
    expect(await pageScrollsSideways(page)).toBe(false)

    for (const [id, label] of SECTIONS) {
      await nav(page).locator(`[data-section="${id}"]`).click()
      await expect(title(page)).toHaveText(label)
      await expect(title(page)).toBeFocused()
      expect(await pageScrollsSideways(page)).toBe(false)
      await backToList(page).click()
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
      await expect(backToList(page)).toBeVisible()
      expect(await violations(page)).toEqual([])
    })
  })
}

test('when the code of the settings cannot be fetched (omo updated meanwhile), a message offers Reload', async ({ page, omo }) => {
  await openBoard(page, omo)
  // The old tab asks for a file the new omo no longer has.
  await page.route(/\/assets\/SettingsView-[^/]*\.js$/, (route) => route.fulfill({ status: 404, body: 'not found' }))
  await gear(page).click()
  const failed = page.locator('[data-route-failed]')
  await expect(failed).toContainText('Reload the page to get the current version.')
  await page.unroute(/\/assets\/SettingsView-[^/]*\.js$/)
  await failed.getByRole('button', { name: 'Reload' }).click()
  await expect(title(page)).toHaveText('Projects')
})

for (const width of [1440, 390]) {
  test(`at ${width} px a field the focus moves to, and the end of every page, come clear of the sticky Save bar`, async ({ page, omo }) => {
    await page.setViewportSize({ width, height: width === 390 ? 844 : 700 })
    await openBoard(page, omo)
    for (const [id, label] of SECTIONS) {
      await page.goto(omo.url(`/settings/${id}`))
      await expect(title(page)).toHaveText(label)
      await expect(page.locator('[data-settings-loading]')).toHaveCount(0)
      const bars = page.locator('[data-settings] .savebar')
      if ((await bars.count()) === 0) continue
      // The field just before the last Save bar: from the top of the page, the focus brings it into view above the bar.
      const covered = await page.locator('[data-settings]').evaluate((scroller) => {
        const bar = [...scroller.querySelectorAll('.savebar')].pop()!
        const fields = [...scroller.querySelectorAll<HTMLElement>('input:not([type="hidden"]), select, textarea, [role="spinbutton"]')].filter(
          (f) => f.compareDocumentPosition(bar) & Node.DOCUMENT_POSITION_FOLLOWING && f.getClientRects().length > 0,
        )
        const field = fields.pop()
        if (!field) return null
        scroller.scrollTop = 0
        field.focus()
        const f = field.getBoundingClientRect()
        const b = bar.getBoundingClientRect()
        return { field: field.outerHTML.slice(0, 80), under: f.bottom > b.top + 0.5 && f.top < b.bottom }
      })
      if (covered) expect(covered.under, `${id}: ${covered.field}`).toBe(false)
      // At the end of the page nothing lies under a Save bar.
      const overlaps = await page.locator('[data-settings]').evaluate((scroller) => {
        scroller.scrollTop = scroller.scrollHeight
        const found: string[] = []
        for (const bar of scroller.querySelectorAll('.savebar')) {
          const b = bar.getBoundingClientRect()
          for (const el of scroller.querySelectorAll<HTMLElement>('p, label, input, select, textarea, button, li, pre, [role="meter"]')) {
            if (bar.contains(el) || el.contains(bar) || el.getClientRects().length === 0) continue
            const r = el.getBoundingClientRect()
            if (r.bottom > b.top + 0.5 && r.top < b.bottom - 0.5 && r.right > b.left && r.left < b.right) found.push(el.outerHTML.slice(0, 60))
          }
        }
        return found
      })
      expect(overlaps, id).toEqual([])
    }
  })
}
