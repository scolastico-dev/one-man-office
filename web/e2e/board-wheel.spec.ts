// The board scrolls sideways inside itself when the window is too narrow for
// its five columns. A horizontal wheel (a mouse's second wheel, a trackpad's
// sideways swipe, Shift with the wheel) scrolls the board wherever the
// pointer is over it, also over a column, whose own box scrolls only up and
// down; a vertical wheel over a column scrolls that column and never the
// board or the document.
import { execFileSync } from 'node:child_process'
import { existsSync, writeFileSync } from 'node:fs'
import { devNull } from 'node:os'
import { join } from 'node:path'
import type { Locator, Page } from '@playwright/test'
import { expect, test, type Omo } from './support/omo'
import { expectNoAgentProcesses, script, waitForSession } from './support/agents'
import { column, openBoard } from './support/board'

const board = (page: Page) => page.locator('[data-board]')
const body = (c: Locator) => c.locator('.column__body')

async function settled(page: Page): Promise<void> {
  await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))))
}

async function scrollState(page: Page) {
  return page.evaluate(() => {
    const b = document.querySelector<HTMLElement>('[data-board]')!
    const doc = document.scrollingElement ?? document.documentElement
    return { left: b.scrollLeft, top: b.scrollTop, room: b.scrollWidth - b.clientWidth, docTop: doc.scrollTop, docLeft: doc.scrollLeft }
  })
}

async function pointAt(page: Page, x: number, y: number): Promise<void> {
  await page.mouse.move(x, y)
}

/** The board back at its left edge, then a sideways wheel at a point; the board must have moved right. */
async function expectSidewaysAt(page: Page, x: number, y: number, where: string, wheel: () => Promise<void>): Promise<void> {
  await board(page).evaluate((b) => (b.scrollLeft = 0))
  await settled(page)
  await pointAt(page, x, y)
  await wheel()
  await expect.poll(async () => (await scrollState(page)).left, { message: `the board scrolls sideways with the pointer ${where}` }).toBeGreaterThan(0)
  const s = await scrollState(page)
  expect({ docTop: s.docTop, docLeft: s.docLeft }, where).toEqual({ docTop: 0, docLeft: 0 })
}

test.describe('the wheel over the board', () => {
  test.beforeEach(async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem('omo.sidebar', JSON.stringify({ open: true, width: 420, tab: 'agents' })))
    await page.setViewportSize({ width: 1100, height: 800 })
  })

  test('a horizontal wheel scrolls the board sideways over cards, an empty column, a header and between columns', async ({ page, omo }) => {
    for (let i = 0; i < 25; i++) await omo.createTicket({ title: `Ticket ${i + 1} that fills the Planning column` })
    await openBoard(page, omo)
    await expect(page.locator('[data-sidebar] [role="tablist"]')).toBeVisible()
    expect((await scrollState(page)).room, 'the window is too narrow for five columns').toBeGreaterThan(200)

    const planning = column(page, 'Planning')
    const ready = column(page, 'Ready For Dev')
    const cardBox = (await planning.locator('li[data-ticket]').nth(2).boundingBox())!
    const emptyBox = (await body(ready).boundingBox())!
    const readyBox = (await ready.boundingBox())!
    const planningBox = (await planning.boundingBox())!
    const sideways = () => page.mouse.wheel(400, 0)

    await expectSidewaysAt(page, cardBox.x + cardBox.width / 2, cardBox.y + cardBox.height / 2, 'over a card', sideways)
    await expectSidewaysAt(page, emptyBox.x + emptyBox.width / 2, emptyBox.y + emptyBox.height / 2, "over an empty column's middle", sideways)
    await expectSidewaysAt(page, readyBox.x + readyBox.width / 2, readyBox.y + 2, "over a column's header", sideways)
    await expectSidewaysAt(page, (planningBox.x + planningBox.width + readyBox.x) / 2, readyBox.y + readyBox.height / 2, 'between two columns', sideways)

    // Shift with the vertical wheel scrolls sideways too, as browsers do.
    await page.keyboard.down('Shift')
    await expectSidewaysAt(page, emptyBox.x + emptyBox.width / 2, emptyBox.y + emptyBox.height / 2, 'over a column with Shift held', () => page.mouse.wheel(0, 400))
    await page.keyboard.up('Shift')
  })

  test('a vertical wheel over a column scrolls that column, never the board or the document', async ({ page, omo }) => {
    for (let i = 0; i < 25; i++) await omo.createTicket({ title: `Ticket ${i + 1} that fills the Planning column` })
    await openBoard(page, omo)
    const planning = body(column(page, 'Planning'))
    const box = (await planning.boundingBox())!
    await pointAt(page, box.x + box.width / 2, box.y + box.height / 2)
    await page.mouse.wheel(0, 300)
    await expect.poll(() => planning.evaluate((el) => el.scrollTop)).toBeGreaterThan(0)
    // Past the column's end: nothing else moves.
    for (let i = 0; i < 10; i++) await page.mouse.wheel(0, 3000)
    await expect.poll(() => planning.evaluate((el) => el.scrollHeight - el.clientHeight - el.scrollTop)).toBeLessThan(2)
    await settled(page)
    const s = await scrollState(page)
    expect({ left: s.left, top: s.top, docTop: s.docTop, docLeft: s.docLeft }).toEqual({ left: 0, top: 0, docTop: 0, docLeft: 0 })

    // A column that cannot scroll at all keeps the board and the document still too.
    const ready = body(column(page, 'Ready For Dev'))
    const rb = (await ready.boundingBox())!
    await pointAt(page, rb.x + rb.width / 2, rb.y + rb.height / 2)
    for (let i = 0; i < 5; i++) await page.mouse.wheel(0, 2000)
    await settled(page)
    const t = await scrollState(page)
    expect({ left: t.left, top: t.top, docTop: t.docTop, docLeft: t.docLeft }).toEqual({ left: 0, top: 0, docTop: 0, docLeft: 0 })
  })
})

async function worktreeOf(omo: Omo, n: number): Promise<string> {
  let found = ''
  await expect
    .poll(async () => {
      const t = await omo.api<{ projects: { worktree: string }[] }>('GET', `/api/tickets/${n}`)
      found = t.projects[0]?.worktree ?? ''
      return found !== '' && existsSync(found)
    })
    .toBe(true)
  return found
}

test.describe('the wheel over the ticket page', () => {
  test.use({ omoOptions: { agents: { developer: script('ready', 'hang') } } })

  test("a sideways wheel over a diff's long lines scrolls the file view sideways", async ({ page, omo }) => {
    const n = await omo.createTicket({ title: 'One very long line' })
    await omo.moveTicket(n, 'ready')
    await waitForSession(omo, n, 'developer')
    const dir = await worktreeOf(omo, n)
    const lines = Array.from({ length: 200 }, (_, i) => (i === 3 ? `const wide${i} = '${'x'.repeat(600)}'` : `const line${i} = ${i}`)).join('\n') + '\n'
    writeFileSync(join(dir, 'wide.ts'), lines)
    const git = (...args: string[]) => execFileSync('git', args, { cwd: dir, env: { ...process.env, GIT_CONFIG_GLOBAL: devNull, GIT_CONFIG_SYSTEM: devNull }, stdio: 'pipe' })
    git('add', '--', 'wide.ts')
    git('-c', 'user.name=e2e', '-c', 'user.email=e2e@example.invalid', '-c', 'commit.gpgsign=false', 'commit', '-q', '--no-verify', '-m', 'feat: wide')

    await page.setViewportSize({ width: 1100, height: 800 })
    await openBoard(page, omo)
    await page.goto(omo.url(`/ticket/${n}?tab=diff`))
    const code = page.locator('[data-ticket-page] [data-file-pane] .dfile__body')
    await expect(code).toBeVisible()
    expect(await code.evaluate((el) => el.scrollWidth - el.clientWidth)).toBeGreaterThan(100)
    const box = (await code.boundingBox())!
    await page.mouse.move(box.x + box.width / 2, box.y + Math.min(box.height / 2, 120))
    await page.mouse.wheel(300, 0)
    await expect.poll(() => code.evaluate((el) => el.scrollLeft)).toBeGreaterThan(0)
    const doc = await page.evaluate(() => ({ top: document.documentElement.scrollTop, left: document.documentElement.scrollLeft }))
    expect(doc).toEqual({ top: 0, left: 0 })

    await omo.api('POST', '/api/agents/stop-all', {})
    await expectNoAgentProcesses(omo)
  })
})
