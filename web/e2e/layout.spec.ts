import { writeFileSync } from 'node:fs'
import { join } from 'node:path'
import type { Page } from '@playwright/test'
import { expect, test } from './support/omo'
import { openBoard } from './support/board'
import { script, waitForSession } from './support/agents'
import { terminalIn } from './support/terminal'

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

test('collapsed, the strip labels read from bottom to top, are larger, and every tab is as wide as the strip', async ({ page, omo }) => {
  await openBoard(page, omo)
  await expect(sidebar(page)).toHaveAttribute('data-open', 'false')
  const strip = sidebar(page).getByRole('tablist', { name: 'Sidebar' })
  const tabs = await strip.evaluate((list) => {
    const side = list.closest('.side')!.getBoundingClientRect()
    const border = parseFloat(getComputedStyle(list.closest('.side')!).borderRightWidth)
    return [...list.querySelectorAll<HTMLElement>('[role="tab"]')].map((t) => {
      const label = t.querySelector('.tabs__label')!
      const text = label.firstChild!
      const range = document.createRange()
      range.setStart(text, 0)
      range.setEnd(text, 1)
      const first = range.getBoundingClientRect()
      range.setStart(text, text.textContent!.length - 1)
      range.setEnd(text, text.textContent!.length)
      const last = range.getBoundingClientRect()
      const r = t.getBoundingClientRect()
      const l = label.getBoundingClientRect()
      return {
        name: t.textContent!.trim(),
        width: r.width,
        strip: side.width - border,
        firstBelowLast: first.top > last.top,
        size: parseFloat(getComputedStyle(label).fontSize),
        centre: Math.abs(l.left + l.width / 2 - (r.left + r.width / 2)),
      }
    })
  })
  expect(tabs.length).toBe(3)
  for (const t of tabs) {
    expect(t.firstBelowLast, t.name).toBe(true)
    expect(t.width, t.name).toBeCloseTo(t.strip, 0)
    expect(t.size, t.name).toBeGreaterThanOrEqual(14)
    expect(t.centre, t.name).toBeLessThanOrEqual(1)
  }
  // The counts still show.
  await expect(strip.getByRole('tab', { name: /^Agents \d+/ })).toBeVisible()
})

/** How the open terminal of the sidebar sits in its tab: the room left below it, whether the tab scrolls, and how many rows it has. */
function terminalFit(page: Page) {
  return sidebar(page)
    .locator('[role="tabpanel"]')
    .evaluate((panel) => {
      const tv = panel.querySelector('.tv')
      if (!tv) return null
      const p = panel.getBoundingClientRect()
      const padding = parseFloat(getComputedStyle(panel.firstElementChild!).paddingBottom)
      return {
        below: Math.round(p.bottom - padding - tv.getBoundingClientRect().bottom),
        scrolls: panel.scrollHeight - panel.clientHeight > 1,
        rows: panel.querySelectorAll('.xterm-rows > div').length,
      }
    })
}

/** The terminal ends at the bottom of the tab at both window heights, and its rows follow the height. */
async function expectTerminalFills(page: Page) {
  await expect(terminalIn(sidebar(page))).toBeVisible({ timeout: 30_000 })
  const rows: number[] = []
  for (const height of [900, 600, 1100]) {
    await page.setViewportSize({ width: 1440, height })
    await expect.poll(async () => (await terminalFit(page))?.below, { message: `room below the terminal at ${height} px` }).toBe(0)
    expect((await terminalFit(page))!.scrolls, `the tab scrolls at ${height} px`).toBe(false)
    // The emulator fits itself to the new height (debounced).
    await expect.poll(async () => (await terminalFit(page))!.rows).not.toBe(rows[rows.length - 1] ?? -1)
    rows.push((await terminalFit(page))!.rows)
  }
  expect(rows[1]).toBeLessThan(rows[0]!)
  expect(rows[2]).toBeGreaterThan(rows[0]!)
}

test.describe('the open terminal of the sidebar fills its height', () => {
  test.use({ omoOptions: { agents: { ceo: script('ready', 'print|CEO ready', 'hang'), developer: script('ready', 'print|working', 'hang') } } })

  test('in the CEO tab', async ({ page, omo }) => {
    await openBoard(page, omo)
    await sidebar(page).getByRole('tab', { name: /^CEO/ }).click()
    await expectTerminalFills(page)
  })

  test("in the Agents tab, an agent's terminal", async ({ page, omo }) => {
    await openBoard(page, omo)
    await sidebar(page).getByRole('tab', { name: /^Agents/ }).click()
    const n = await omo.createTicket({ title: 'Fill me' })
    await omo.moveTicket(n, 'ready')
    const session = await waitForSession(omo, n, 'developer')
    await sidebar(page).locator(`[data-session="${session.id}"]`).click()
    await expectTerminalFills(page)
  })

  test("on a phone, in the sheet's CEO tab", async ({ page, omo }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    await openBoard(page, omo)
    await page.locator('[data-sidebar-open]').click()
    const sheet = page.locator('dialog.side-sheet')
    await sheet.getByRole('tab', { name: /^CEO/ }).click()
    await expect(terminalIn(sheet)).toBeVisible({ timeout: 30_000 })
    const fit = () =>
      sheet.evaluate((d) => {
        const body = d.querySelector('.dialog__body')!
        const tv = d.querySelector('.tv')!
        return {
          below: Math.round(body.getBoundingClientRect().bottom - parseFloat(getComputedStyle(body).paddingBottom) - parseFloat(getComputedStyle(d.querySelector('.ceo')!).paddingBottom) - tv.getBoundingClientRect().bottom),
          scrolls: d.scrollHeight - d.clientHeight > 1,
        }
      })
    for (const height of [844, 640]) {
      await page.setViewportSize({ width: 390, height })
      await expect.poll(async () => (await fit()).below, { message: `room below the terminal at ${height} px` }).toBe(0)
      expect((await fit()).scrolls).toBe(false)
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true)
    }
  })

  test('in the Terminals tab, a shell', async ({ page, omo }) => {
    writeFileSync(join(omo.home, '.zshrc'), '')
    await openBoard(page, omo)
    await sidebar(page).getByRole('tab', { name: /^Terminals/ }).click()
    await sidebar(page).getByRole('button', { name: 'New terminal' }).click()
    await sidebar(page).getByRole('menuitem', { name: 'demo' }).click()
    await expectTerminalFills(page)
  })
})
