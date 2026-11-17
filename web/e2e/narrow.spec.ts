import { expect, test } from './support/omo'
import { card, column, expectBoardLive, openBoard } from './support/board'
import { expectNoAgentProcesses, script, waitForSession } from './support/agents'

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

const tabs = (page: import('@playwright/test').Page) => page.getByRole('tablist', { name: 'Columns' })

test.describe('on a touch screen', () => {
  test.use({ hasTouch: true })

  test('a card opens, and moves to another column through its menu', async ({ page, omo }) => {
    const n = await omo.createTicket({ title: 'Move me by menu' })
    await openBoard(page, omo)

    // Tapping the card opens it.
    await card(page, n).locator('[data-card-link]').tap()
    const panel = page.locator('dialog[data-ticket-panel]')
    await expect(panel).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(panel).toBeHidden()

    // The move menu shows without hover, with a finger-sized button.
    const trigger = card(page, n).getByRole('button', { name: `Move #${n}…` })
    await expect(trigger).toBeVisible()
    const box = (await trigger.boundingBox())!
    expect(box.width).toBeGreaterThanOrEqual(44)
    expect(box.height).toBeGreaterThanOrEqual(44)
    await trigger.tap()
    const menu = page.getByRole('menu')
    await expect(menu).toBeVisible()
    await expect(menu.getByRole('menuitem', { name: /^Review / })).toHaveAttribute('aria-disabled', 'true')
    expect(await pageScrollsSideways(page)).toBe(false)
    await menu.getByRole('menuitem', { name: 'Ready For Dev' }).tap()

    // The card left the shown column; a notice offers to show where it went.
    await expect(card(page, n)).toBeHidden()
    const show = page.getByRole('button', { name: 'Show Ready For Dev' })
    await expect(show).toBeVisible()
    await expect.poll(async () => (await omo.api<{ column: string }>('GET', `/api/tickets/${n}`)).column).toBe('ready')
    await show.tap()
    await expect(tabs(page).getByRole('tab', { name: /^Ready For Dev/ })).toHaveAttribute('aria-selected', 'true')
    await expect(card(page, n)).toBeVisible()
    await expect(card(page, n).locator('[data-card-link]')).toBeFocused()
    expect(await pageScrollsSideways(page)).toBe(false)
  })

  test('a ticket can be created', async ({ page, omo }) => {
    await openBoard(page, omo)
    await page.getByRole('button', { name: 'Create ticket' }).tap()
    const dialog = page.getByRole('dialog', { name: 'Create ticket' })
    await expect(dialog).toBeVisible()
    // Full screen.
    const box = (await dialog.boundingBox())!
    expect(box.width).toBeGreaterThanOrEqual(389)
    await dialog.getByLabel('Title').fill('From the phone')
    await dialog.getByRole('button', { name: 'Create', exact: true }).tap()
    await expect(dialog).toBeHidden()
    await expect(page.locator('li[data-ticket]').filter({ hasText: 'From the phone' })).toBeVisible()
    expect(await pageScrollsSideways(page)).toBe(false)
  })

  test('the top bar controls and the column tabs are within reach', async ({ page, omo }) => {
    await omo.createTicket({ title: 'Something' })
    await openBoard(page, omo)
    for (const name of ['Create ticket', 'Filters']) {
      const b = page.getByRole('button', { name, exact: true })
      await expect(b).toBeInViewport({ ratio: 1 })
    }
    for (const t of await tabs(page).getByRole('tab').all()) {
      const b = (await t.boundingBox())!
      expect(b.height).toBeGreaterThanOrEqual(44)
    }
    await page.getByRole('button', { name: 'Filters', exact: true }).tap()
    await expect(page.getByRole('dialog', { name: 'Filters' })).toBeVisible()
    expect(await pageScrollsSideways(page)).toBe(false)
  })
})

test.describe('seeded', () => {
  test.use({
    omoOptions: {
      seed: {
        projects: [{ path: '{{repo}}', name: 'demo' }],
        tickets: [{ ref: 'review', type: 'task', title: 'Waiting for you', to: 'review', review: 'human', projects: ['demo'], branch: 'omo/review' }],
      },
    },
  })

  test('the switcher shows counts and which column holds something waiting for the user', async ({ page, omo }) => {
    await openBoard(page, omo)
    const review = tabs(page).getByRole('tab', { name: /^Review/ })
    await expect(review).toContainText('1')
    await expect(review).toHaveAccessibleName(/something here waits for you/)
    await expect(review.locator('[data-waits]')).toBeVisible()
    await expect(tabs(page).locator('[data-waits]')).toHaveCount(1)
  })
})

test('rotating or resizing across 768 px keeps the column and the focused card', async ({ page, omo }) => {
  const a = await omo.createTicket({ title: 'Planned' })
  const b = await omo.createTicket({ title: 'Ready one' })
  await omo.moveTicket(b, 'ready')
  await openBoard(page, omo)
  await tabs(page).getByRole('tab', { name: /^Ready For Dev/ }).click()
  const link = card(page, b).locator('[data-card-link]')
  await link.focus()

  // Landscape on a large phone, or a wider window: every column, and the card keeps the focus.
  await page.setViewportSize({ width: 1024, height: 700 })
  await expect(card(page, a)).toBeVisible()
  await expect(link).toBeFocused()
  await expect(link).toBeInViewport()

  // Back to narrow: the column of the focused card is the one shown.
  await page.setViewportSize({ width: 390, height: 844 })
  await expect(tabs(page).getByRole('tab', { name: /^Ready For Dev/ })).toHaveAttribute('aria-selected', 'true')
  await expect(card(page, b)).toBeVisible()
  await expect(link).toBeFocused()
  expect(await pageScrollsSideways(page)).toBe(false)
})

test.describe('with a running agent', () => {
  test.use({ omoOptions: { agents: { developer: script('ready', 'print|working', 'hang') } } })

  test('at 390 px the sidebar opens as a sheet with the Agents tab and no horizontal scrolling', async ({ page, omo }) => {
    const n = await omo.createTicket({ title: 'Work on the phone' })
    await omo.moveTicket(n, 'ready')
    const session = await waitForSession(omo, n, 'developer')
    await openBoard(page, omo)
    // There is no column at this width, only the button.
    await expect(page.locator('[data-sidebar]')).toHaveCount(0)
    const open = page.getByRole('button', { name: 'Sidebar' })
    await open.click()
    const sheet = page.getByRole('dialog', { name: 'Agents and terminals' })
    await expect(sheet).toBeVisible()
    await expect(sheet.getByRole('tab', { name: /^Agents/ })).toHaveAttribute('aria-selected', 'true')
    await expect(sheet.locator('[data-slots]')).toContainText('1 of 4 tasks')
    const row = sheet.locator(`[data-session="${session.id}"]`)
    await expect(row).toContainText(`#${n} Work on the phone`)
    expect(await pageScrollsSideways(page)).toBe(false)
    const box = await sheet.boundingBox()
    expect(box!.width).toBeLessThanOrEqual(390)
    expect(await sheet.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true)

    // The row opens the agent inside the sheet; the sheet still fits.
    await row.click()
    await expect(sheet.getByRole('link', { name: `Open #${n}` })).toBeVisible()
    expect(await sheet.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true)
    await sheet.getByRole('button', { name: 'Back' }).click()

    // Escape closes the sheet and the focus goes back to the button.
    await page.keyboard.press('Escape')
    await expect(sheet).toBeHidden()
    await expect(open).toBeFocused()
    await expectNoAgentProcesses(omo)
  })
})

test('at 390 px a ticket opens with the focus on its Close button, never on its title, and the title wraps in full, also while edited', async ({ page, omo }) => {
  const title = 'A rather long ticket title that needs several lines on a phone to be read in full'
  const n = await omo.createTicket({ title })
  for (const how of ['keyboard', 'address'] as const) {
    if (how === 'keyboard') {
      await openBoard(page, omo)
      await card(page, n).locator('[data-card-link]').focus()
      await page.keyboard.press('Enter')
    } else {
      await page.goto(omo.url(`/${n}`))
    }
    const dialog = page.locator('dialog[data-ticket-panel]')
    await expect(dialog.getByRole('heading', { level: 2, name: title })).toBeVisible()
    // As other dialogs without a field to fill: the focus is on Close, not on the (editable) title.
    await expect(dialog.getByRole('button', { name: 'Close' })).toBeFocused()
    // The whole title is shown: it wraps, nothing is cut off at the side.
    const cut = await dialog.locator('[data-ticket-title]').evaluate((h) => {
      const r = h.getBoundingClientRect()
      const d = h.closest('dialog')!.getBoundingClientRect()
      return { overflow: h.scrollWidth - h.clientWidth, right: r.right - d.right, left: d.left - r.left }
    })
    expect(cut.overflow).toBeLessThanOrEqual(0)
    expect(cut.right).toBeLessThanOrEqual(0)
    expect(cut.left).toBeLessThanOrEqual(0)
    expect(await pageScrollsSideways(page)).toBe(false)
    if (how === 'address') {
      // Editing keeps the whole title in view: the field wraps and grows.
      await dialog.getByRole('button', { name: 'Edit title' }).click()
      const field = dialog.locator('[data-title-input]')
      await expect(field).toBeFocused()
      await expect(field).toHaveValue(title)
      const box = await field.evaluate((f) => ({ wide: f.scrollWidth - f.clientWidth, tall: f.scrollHeight - f.clientHeight, lines: f.clientHeight / parseFloat(getComputedStyle(f).lineHeight) }))
      expect(box.wide).toBeLessThanOrEqual(0)
      expect(box.tall).toBeLessThanOrEqual(1)
      expect(box.lines).toBeGreaterThan(2)
      expect(await pageScrollsSideways(page)).toBe(false)
      // Enter still saves; the title stays one line.
      await field.fill(`${title} again`)
      await page.keyboard.press('Enter')
      await expect(dialog.locator('[data-ticket-title]')).toHaveText(`${title} again`)
    }
    await page.keyboard.press('Escape')
    await expect(dialog).toHaveCount(0)
  }
})
