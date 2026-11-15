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

test('open, the sidebar tabs share its whole width with their labels in the middle, at every width', async ({ page, omo }) => {
  await page.setViewportSize({ width: 1920, height: 900 })
  await openBoard(page, omo)
  await sidebar(page).getByRole('button', { name: 'Expand sidebar' }).click()
  const splitter = page.getByRole('separator', { name: 'Sidebar width' })
  await splitter.focus()
  await page.keyboard.press('Home')
  const min = Number(await splitter.getAttribute('aria-valuemin'))
  const max = Number(await splitter.getAttribute('aria-valuemax'))
  for (;;) {
    const now = Number(await splitter.getAttribute('aria-valuenow'))
    const box = await sidebar(page).getByRole('tablist', { name: 'Sidebar' }).evaluate((list) => {
      const tabs = [...list.querySelectorAll('[role="tab"]')].map((t) => t.getBoundingClientRect())
      const labels = [...list.querySelectorAll('[role="tab"]')].map((t) => {
        const r = t.getBoundingClientRect()
        const kids = [...t.children].filter((c) => !c.classList.contains('sr-only')).map((c) => c.getBoundingClientRect())
        const left = Math.min(...kids.map((k) => k.left))
        const right = Math.max(...kids.map((k) => k.right))
        return Math.abs(left - r.left - (r.right - right))
      })
      const l = list.getBoundingClientRect()
      return { list: l.width, left: l.left, first: tabs[0]!.left, right: tabs[tabs.length - 1]!.right, lr: l.right, off: Math.max(...labels) }
    })
    expect(box.first, `width ${now}`).toBeCloseTo(box.left, 0)
    expect(box.right, `width ${now}`).toBeCloseTo(box.lr, 0)
    expect(box.off, `width ${now}`).toBeLessThanOrEqual(1.5)
    if (now >= max) break
    for (let i = 0; i < 5; i++) await page.keyboard.press('ArrowRight')
    await expect(splitter).not.toHaveAttribute('aria-valuenow', String(now))
  }
  expect(min).toBeLessThan(max)
})
