import { expect, test } from './support/omo'
import { card, column, expectBoardLive, openBoard } from './support/board'

test.use({ viewport: { width: 390, height: 844 } })

/** True when the page itself scrolls sideways. */
async function pageScrollsSideways(page: import('@playwright/test').Page): Promise<boolean> {
  return page.evaluate(() => {
    const root = document.documentElement
    return root.scrollWidth > root.clientWidth || document.body.scrollWidth > document.body.clientWidth
  })
}

test('at 390 px each column is reachable through the switcher and the page never scrolls sideways', async ({ page, omo }) => {
  const planning = await omo.createTicket({ title: 'Planned' })
  const ready = await omo.createTicket({ title: 'Ready one' })
  await omo.moveTicket(ready, 'ready')
  await openBoard(page, omo)
  expect(await pageScrollsSideways(page)).toBe(false)

  const tabs = page.getByRole('tablist', { name: 'Columns' })
  await expect(tabs).toBeVisible()
  await expect(tabs.getByRole('tab')).toHaveCount(5)

  // One column at a time: the first one shows, the others are hidden.
  await expect(card(page, planning)).toBeVisible()
  await expect(card(page, ready)).toBeHidden()
  await expect(column(page, 'Ready For Dev')).toBeHidden()

  await tabs.getByRole('tab', { name: /^Ready For Dev/ }).click()
  await expect(card(page, ready)).toBeVisible()
  await expect(card(page, planning)).toBeHidden()
  expect(await pageScrollsSideways(page)).toBe(false)

  for (const name of [/^In Progress/, /^Review/, /^Done/, /^Planning/]) {
    await tabs.getByRole('tab', { name }).click()
    await expect(tabs.getByRole('tab', { name })).toHaveAttribute('aria-selected', 'true')
    expect(await pageScrollsSideways(page)).toBe(false)
  }
  await expect(card(page, planning)).toBeVisible()
})

test('at 390 px the locked screen fits without sideways scrolling', async ({ page, omo }) => {
  await page.goto(omo.url('/'))
  await expect(page.getByRole('heading', { level: 1, name: 'omo is locked' })).toBeVisible()
  expect(await pageScrollsSideways(page)).toBe(false)
  await page.getByLabel('Access address or token').fill(omo.accessUrl)
  await page.getByRole('button', { name: 'Unlock' }).click()
  await expectBoardLive(page)
  expect(await pageScrollsSideways(page)).toBe(false)
})
