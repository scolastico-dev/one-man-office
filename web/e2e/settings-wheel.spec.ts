import type { Page } from '@playwright/test'
import { openBoard } from './support/board'
import { expect, FAKE_AGENT_BINARY, test, type Omo } from './support/omo'

// The mouse wheel scrolls the settings page from anywhere in the settings
// area: over the page, in the free space right of it, over the section list,
// in the empty space below the list and in the margin left of it. The test
// does not name the scroll box; it looks at what the user sees: the page's
// title moves up.

async function putProfile(omo: Omo, name: string) {
  await omo.api('PUT', `/api/profiles/${name}`, {
    cli: 'custom',
    command: FAKE_AGENT_BINARY,
    args: [],
    env: {},
    account_dir: '',
    usage_script: '',
    prompt_delivery: {},
    models: { default: { args: [], env: {}, usage_windows: [], description: 'scripted fake agent' } },
  })
}

const settle = (page: Page) => page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))))
const titleTop = (page: Page) => page.locator('[data-settings-page] h1').evaluate((h) => h.getBoundingClientRect().top)

/** Every scroll box of the settings area back at the top. */
async function toTop(page: Page) {
  await page.evaluate(() => {
    for (const el of document.querySelectorAll<HTMLElement>('[data-settings], [data-settings] *')) {
      if (el.scrollHeight > el.clientHeight && getComputedStyle(el).overflowY !== 'visible') el.scrollTop = 0
    }
  })
  await settle(page)
}

async function expectWheelScrollsPage(page: Page, x: number, y: number, where: string) {
  await toTop(page)
  const before = await titleTop(page)
  await page.mouse.move(x, y)
  for (let i = 0; i < 5; i++) await page.mouse.wheel(0, 400)
  await expect.poll(() => titleTop(page), { message: `the wheel at ${where} (${x}, ${y}) scrolls the page` }).toBeLessThan(before - 50)
  expect(await page.evaluate(() => (document.scrollingElement ?? document.documentElement).scrollTop), 'the document never scrolls').toBe(0)
}

for (const width of [1440, 2560]) {
  test(`at ${width} px the wheel scrolls the settings page from the page, the margins, the list and the space below it`, async ({ page, omo }) => {
    await putProfile(omo, 'fake')
    await page.setViewportSize({ width, height: 1400 })
    await openBoard(page, omo)
    await page.goto(omo.url('/settings/agents'))
    await page.locator('[data-profile="fake"]').click()
    await expect(page.locator('[data-profile-editor="fake"]')).toBeVisible()

    const m = await page.evaluate(() => {
      const r = (s: string) => document.querySelector(s)!.getBoundingClientRect()
      const nav = r('[data-settings-nav]')
      const col = r('[data-settings-page] > div')
      const link = r('[data-section="agents"]')
      return { nav: { l: nav.left, r: nav.right, t: nav.top, b: nav.bottom }, col: { l: col.left, r: col.right }, link: { x: link.left + link.width / 2, y: link.top + link.height / 2 }, h: innerHeight, w: innerWidth }
    })
    const scrollable = await page.locator('[data-settings]').evaluate((f) => {
      const page = f.querySelector<HTMLElement>('[data-settings-page]')!
      return Math.max(f.scrollHeight - f.clientHeight, page.scrollHeight - page.clientHeight)
    })
    expect(scrollable, 'the page is longer than the window').toBeGreaterThan(200)
    expect(m.nav.b, 'the list is shorter than the window').toBeLessThan(m.h - 100)

    await expectWheelScrollsPage(page, m.col.l + 200, 400, 'over the page')
    await expectWheelScrollsPage(page, (m.col.r + m.w) / 2 + 1, 400, 'right of the page')
    await expectWheelScrollsPage(page, m.link.x, m.link.y, 'over a list entry')
    await expectWheelScrollsPage(page, m.nav.l + 100, (m.nav.b + m.h) / 2, 'below the list')
    await expectWheelScrollsPage(page, m.nav.l / 2, 400, 'left of the list')
  })
}

test('a list taller than the window scrolls first, then hands the wheel to the page', async ({ page, omo }) => {
  await page.setViewportSize({ width: 1440, height: 420 })
  await openBoard(page, omo)
  await page.goto(omo.url('/settings/limits'))
  await expect(page.getByRole('spinbutton', { name: 'Tasks in work' })).toBeVisible()
  const nav = page.locator('[data-settings-nav]').locator('xpath=ancestor::div[contains(@class,"settings-view__side")]')
  const room = await nav.evaluate((e) => e.scrollHeight - e.clientHeight)
  expect(room, 'the list is taller than the window').toBeGreaterThan(50)
  const box = (await nav.boundingBox())!
  await page.mouse.move(box.x + 100, box.y + box.height / 2)
  await page.mouse.wheel(0, 100)
  await expect.poll(() => nav.evaluate((e) => e.scrollTop)).toBeGreaterThan(0)
  const pageTop = await titleTop(page)
  for (let i = 0; i < 10; i++) await page.mouse.wheel(0, 400)
  await expect.poll(() => nav.evaluate((e, r) => Math.round(e.scrollTop) >= Math.floor(r) - 1, room)).toBe(true)
  await expect.poll(() => titleTop(page), { message: 'after the list is at its end the wheel scrolls the page' }).toBeLessThan(pageTop - 20)
})
