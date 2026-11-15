import type { Page } from '@playwright/test'
import { expect, test } from './support/omo'
import { openBoard } from './support/board'

// How the frame of the board page lines up: the sidebar's head and the top
// bar, the sidebar's tabs open and collapsed.

const sidebar = (page: Page) => page.locator('[data-sidebar]')

/** The bottom edge of an element, in CSS pixels from the top of the page. */
const bottomOf = (page: Page, selector: string) => page.locator(selector).first().evaluate((el) => el.getBoundingClientRect().bottom)

for (const scheme of ['dark', 'light'] as const) {
  test.describe(`${scheme} theme`, () => {
    test.use({ colorScheme: scheme })

    for (const width of [1440, 1920]) {
      test(`at ${width} px the sidebar's head ends on the top bar's line, collapsed and open`, async ({ page, omo }) => {
        await page.setViewportSize({ width, height: 900 })
        await openBoard(page, omo)
        for (const open of [false, true]) {
          if (open) await sidebar(page).getByRole('button', { name: 'Expand sidebar' }).click()
          await expect(sidebar(page)).toHaveAttribute('data-open', String(open))
          const top = await bottomOf(page, '.topbar-frame')
          expect(await bottomOf(page, '[data-sidebar] .side__head')).toBe(top)
          // Every control of the top bar is as tall as the others, the sidebar's button too.
          const heights = await page.locator('.topbar__actions > *, [data-sidebar-toggle]').evaluateAll((els) => els.map((e) => e.getBoundingClientRect().height))
          expect(new Set(heights.filter((h) => h > 0))).toEqual(new Set([32]))
        }
      })
    }
  })
}
