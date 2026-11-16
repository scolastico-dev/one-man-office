import type { Page } from '@playwright/test'
import { openBoard } from './support/board'
import { expect, test } from './support/omo'

// On the ticket page the wheel scrolls the Overview from anywhere beside it
// that has nothing to scroll for itself: over the centred text, in the free
// space around it, over the details column and over the tabs. The head (title and actions) stays
// put like the top bar.

const settle = (page: Page) => page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))))
const descTop = (page: Page) => page.locator('[data-ticket-page] [data-ticket-description], [data-ticket-page] .tpage__overview').first().evaluate((e) => e.getBoundingClientRect().top)

async function expectMoves(page: Page, x: number, y: number, where: string) {
  await page.evaluate(() => {
    for (const el of document.querySelectorAll<HTMLElement>('[data-ticket-page], [data-ticket-page] *')) if (el.scrollTop > 0) el.scrollTop = 0
  })
  await settle(page)
  const before = await descTop(page)
  await page.mouse.move(x, y)
  for (let i = 0; i < 5; i++) await page.mouse.wheel(0, 400)
  await expect.poll(() => descTop(page), { message: `the wheel ${where} (${x}, ${y}) scrolls the overview` }).toBeLessThan(before - 50)
  expect(await page.evaluate(() => (document.scrollingElement ?? document.documentElement).scrollTop)).toBe(0)
}

for (const width of [1440, 2560]) {
  test(`at ${width} px the wheel scrolls the ticket's overview from the text, the free space, the details and the tabs`, async ({ page, omo }) => {
    const n = await omo.createTicket({ title: 'A ticket with a long text' })
    await omo.api('PUT', `/api/tickets/${n}/description`, {
      text: Array.from({ length: 80 }, (_, i) => `Paragraph ${i + 1} of a long description.`).join('\n\n'),
    })
    await page.setViewportSize({ width, height: 800 })
    await openBoard(page, omo)
    await page.goto(omo.url(`/ticket/${n}`))
    await expect(page.locator('[data-ticket-page] h1')).toBeVisible()
    const m = await page.evaluate(() => {
      const r = (s: string) => document.querySelector(s)?.getBoundingClientRect()
      const o = r('.tpage__overview')!
      const side = r('.tpage__side')
      const tabs = r('[data-ticket-page] [role="tablist"]')!
      return { o: { l: o.left, r: o.right }, side: side ? { x: side.left + side.width / 2, y: side.top + 40 } : null, tabs: { x: tabs.left + 20, y: tabs.top + tabs.height / 2 }, w: innerWidth }
    })
    await expectMoves(page, (m.o.l + m.o.r) / 2, 500, 'over the text')
    if (m.o.l > 100) await expectMoves(page, m.o.l - 30, 500, 'left of the text')
    if (m.side) await expectMoves(page, m.side.x, m.side.y, 'over the details column')
    await expectMoves(page, m.tabs.x, m.tabs.y, 'over the tabs')
  })
}
