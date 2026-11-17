import { expect, test, BASIC } from './support/omo'
import { addressBar, expectBoardLive, historyUrls, observe, openBoard } from './support/board'

const lockedHeading = (page: import('@playwright/test').Page) => page.getByRole('heading', { level: 1, name: 'omo is locked' })

test('the printed address opens the board, leaves no fragment in the address bar and never puts the token in a request URL', async ({ page, omo, context }) => {
  const seen = observe(context)
  await page.goto(omo.accessUrl)
  // The fragment is removed before the app shows anything, so the load event is late enough.
  expect(await addressBar(page)).toBe(omo.url('/'))
  await expectBoardLive(page)

  expect(await addressBar(page)).not.toContain(omo.token)
  expect(await page.title()).not.toContain(omo.token)
  expect(seen.requestUrls.length).toBeGreaterThan(0)
  for (const u of seen.requestUrls) expect(u, 'request URL').not.toContain(omo.token)
  for (const m of seen.console) expect(m, 'console message').not.toContain(omo.token)
  expect(seen.pageErrors).toEqual([])

  // Going back never returns to an address that holds the token.
  for (const u of await historyUrls(page)) expect(u, 'history entry').not.toContain(omo.token)
  await page.goBack()
  expect(page.url()).not.toContain(omo.token)
  await page.goForward()
  expect(page.url()).not.toContain(omo.token)
  expect(await page.title()).not.toContain(omo.token)
})

// Opening the printed address in a tab that already shows the app (same path) only changes the
// fragment: the browser does not reload, the app never sees the token, and the token stays in the
// address bar and in history, unless the app reads the fragment when it changes.
test('opening the printed address in a tab that already shows the locked screen unlocks it and removes the fragment', async ({ page, omo }) => {
  const n = await omo.createTicket({ title: 'Open me' })
  await page.goto(omo.url('/'))
  await expect(lockedHeading(page)).toBeVisible()
  await page.goto(omo.accessUrl)
  await expectBoardLive(page)
  expect(await addressBar(page)).toBe(omo.url('/'))
  for (const u of await historyUrls(page)) expect(u).not.toContain(omo.token)
  // The router must not have recorded the address with the token either:
  // the next address it adds would bring it back into history.
  await page.locator(`li[data-ticket="${n}"] a`).click()
  await expect(page.locator('dialog[data-ticket-panel]')).toBeVisible()
  for (const u of await historyUrls(page)) expect(u, 'history entry').not.toContain(omo.token)
  expect(JSON.stringify(await page.evaluate(() => history.state))).not.toContain(omo.token)
  await page.goBack()
  expect(page.url()).not.toContain(omo.token)
  expect(JSON.stringify(await page.evaluate(() => history.state))).not.toContain(omo.token)
})

test('a reload works without the fragment', async ({ page, omo }) => {
  await openBoard(page, omo)
  await page.reload()
  await expectBoardLive(page)
  expect(await addressBar(page)).toBe(omo.url('/'))
})

test('a second tab opened on the plain address is logged in', async ({ page, omo, context }) => {
  await openBoard(page, omo)
  const second = await context.newPage()
  await second.goto(omo.url('/'))
  await expectBoardLive(second)
})

test('a wrong token shows the locked screen with the rotate hint', async ({ page, omo }) => {
  await page.goto(omo.url('/'))
  await expect(lockedHeading(page)).toBeVisible()
  await page.evaluate(() => localStorage.setItem('omo.token', '0'.repeat(64)))
  await page.reload()
  await expect(lockedHeading(page)).toBeVisible()
  await expect(page.locator('[data-rejected]')).toContainText('omo config rotate-token')
})

test('no token shows the locked screen; pasting the printed address unlocks', async ({ page, omo }) => {
  await page.goto(omo.url('/'))
  await expect(lockedHeading(page)).toBeVisible()
  await expect(page.locator('[data-board]')).toHaveCount(0)
  await page.getByLabel('Access address or token').fill(omo.accessUrl)
  await page.getByRole('button', { name: 'Unlock' }).click()
  await expectBoardLive(page)
  expect(await addressBar(page)).not.toContain(omo.token)
})

test('after omo config rotate-token and a restart, the old page is locked and the new address works', async ({ page, omo, context }) => {
  await openBoard(page, omo)
  const oldToken = omo.token
  await omo.stop()
  const rotated = await omo.cli(['config', 'rotate-token'])
  expect(rotated.code).toBe(0)
  await omo.start()
  expect(omo.token).not.toBe(oldToken)

  await page.reload()
  await expect(lockedHeading(page)).toBeVisible()
  await expect(page.locator('[data-rejected]')).toBeVisible()

  const fresh = await context.newPage()
  await openBoard(fresh, omo)
  expect(await addressBar(fresh)).toBe(omo.url('/'))
})

test.describe('basic auth', () => {
  test.use({ omoOptions: { auth: 'basic' }, httpCredentials: { username: BASIC.username, password: BASIC.password } })

  test("basic auth: the board loads with the browser's credentials", async ({ page, omo }) => {
    await openBoard(page, omo, 'basic')
  })
})
