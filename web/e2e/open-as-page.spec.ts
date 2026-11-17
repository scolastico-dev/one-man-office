// The setting "Open tickets on their own page" (Settings, General): off by
// default, so a click opens the panel over the board; on, every click that
// opens a ticket from outside a ticket's view goes to the page, the panel
// stays reachable with "Open in panel", and a change applies without a reload.
import AxeBuilder from '@axe-core/playwright'
import type { Page } from '@playwright/test'
import { script, waitForSession } from './support/agents'
import { card, openBoard } from './support/board'
import { expect, test, type Omo } from './support/omo'

const panel = (page: Page) => page.locator('dialog[data-ticket-panel]')
const ticketPage = (page: Page) => page.locator('[data-ticket-page]')
const sidebar = (page: Page) => page.locator('[data-sidebar]')
const bell = (page: Page) => page.locator('[data-bell]')
const bar = (page: Page) => page.getByRole('region', { name: 'Save changes' })
const asPageSwitch = (page: Page) => page.getByRole('switch', { name: 'Open tickets on their own page' })

const setting = (omo: Omo, on: boolean) => omo.api('PATCH', '/api/settings', { ui: { open_tickets_as_page: on } })

/** Whether a page has read the settings again (the board does so on every change of the general settings). */
const settingsRead = (page: Page) => page.waitForResponse((r) => r.request().method() === 'GET' && new URL(r.url()).pathname === '/api/settings')

/** Changes the setting from outside the page and returns once the page has read the change. */
async function change(page: Page, omo: Omo, on: boolean): Promise<void> {
  const read = settingsRead(page)
  await setting(omo, on)
  await read
}

const seeded = {
  seed: {
    projects: [{ path: '{{repo}}', name: 'demo' }],
    tickets: [
      { ref: 'first', type: 'task', title: 'First one', to: 'planning', projects: ['demo'] },
      { ref: 'second', type: 'task', title: 'Second one', to: 'planning', projects: ['demo'] },
      { ref: 'review', type: 'task', title: 'Waiting for you', to: 'review', review: 'human', projects: ['demo'], branch: 'omo/review' },
    ],
  },
}

test.describe('the default', () => {
  test.use({ omoOptions: seeded })

  test('is off: a card opens the panel, and the General page shows the switch off', async ({ page, omo }) => {
    const n = omo.refs.first!
    expect((await omo.api<{ settings: { ui: { open_tickets_as_page: boolean } } }>('GET', '/api/settings')).settings.ui.open_tickets_as_page).toBe(false)
    await openBoard(page, omo)
    await card(page, n).getByRole('link').first().click()
    await expect(page).toHaveURL(new RegExp(`/${n}$`))
    await expect(panel(page)).toBeVisible()
    await expect(ticketPage(page)).toHaveCount(0)
    await page.goto(omo.url('/settings/general'))
    await expect(asPageSwitch(page)).toHaveAttribute('aria-checked', 'false')
  })
})

test.describe('switched on', () => {
  test.use({ omoOptions: seeded })

  test('a card, a link in a comment and a notification open the page; Open in panel keeps links in the panel', async ({ page, omo }) => {
    const first = omo.refs.first!
    const second = omo.refs.second!
    const review = omo.refs.review!
    await omo.api('POST', `/api/tickets/${first}/comments`, { body: `See #${second}`, question: false })
    await setting(omo, true)
    await openBoard(page, omo)

    // The board's card.
    await card(page, first).getByRole('link').first().click()
    await expect(page).toHaveURL(new RegExp(`/ticket/${first}$`))
    await expect(ticketPage(page)).toBeVisible()
    await expect(panel(page)).toHaveCount(0)

    // A link in a comment, followed on the page, stays on the page.
    await ticketPage(page).getByRole('tab', { name: /^Comments/ }).click()
    await ticketPage(page).locator(`a[data-ticket-ref="${second}"]`).click()
    await expect(page).toHaveURL(new RegExp(`/ticket/${second}$`))

    // "Open in panel" shows the panel for this visit; a link followed from there stays in the panel.
    await ticketPage(page).getByRole('link', { name: 'Open in panel' }).click()
    await expect(page).toHaveURL(new RegExp(`/${second}(\\?.*)?$`))
    await expect(panel(page)).toBeVisible()
    await panel(page).getByRole('tab', { name: /^Comments/ }).click()
    await page.keyboard.press('Escape')
    await expect(panel(page)).toHaveCount(0)

    // From the board again: a link in a comment of the panel opened directly (/<n> means the panel) ...
    await page.goto(omo.url(`/${first}?tab=comments`))
    await expect(panel(page)).toBeVisible()
    await panel(page).locator(`a[data-ticket-ref="${second}"]`).click()
    await expect(page).toHaveURL(new RegExp(`/${second}(\\?.*)?$`))
    await expect(panel(page)).toBeVisible()
    await expect(ticketPage(page)).toHaveCount(0)
    await page.keyboard.press('Escape')
    await expect(panel(page)).toHaveCount(0)

    // A notification item.
    await expect(page.locator('[data-board]')).toBeVisible()
    await bell(page).click()
    await page.locator(`[data-bell-list] [data-waiting="${review}"]`).click()
    await expect(page).toHaveURL(new RegExp(`/ticket/${review}`))
    await expect(ticketPage(page)).toBeVisible()
  })

  test('the keyboard opens a focused card on the page', async ({ page, omo }) => {
    const n = omo.refs.first!
    await setting(omo, true)
    await openBoard(page, omo)
    await card(page, n).getByRole('link').first().focus()
    await page.keyboard.press('Enter')
    await expect(page).toHaveURL(new RegExp(`/ticket/${n}$`))
    await expect(ticketPage(page)).toBeVisible()
  })

  test('switching it off in another tab applies live, and on again too', async ({ page, omo }) => {
    const n = omo.refs.first!
    await setting(omo, true)
    await openBoard(page, omo)
    const other = await page.context().newPage()
    await openBoard(other, omo)
    await other.goto(omo.url('/settings/general'))
    await expect(asPageSwitch(other)).toHaveAttribute('aria-checked', 'true')
    await asPageSwitch(other).click()
    const read = settingsRead(page)
    await bar(other).getByRole('button', { name: 'Save', exact: true }).click()
    await expect(bar(other)).toContainText('Saved')
    await read

    // The first tab never reloaded: its next click opens the panel.
    await card(page, n).getByRole('link').first().click()
    await expect(page).toHaveURL(new RegExp(`/${n}$`))
    await expect(panel(page)).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(panel(page)).toHaveCount(0)

    await change(page, omo, true)
    await card(page, n).getByRole('link').first().click()
    await expect(page).toHaveURL(new RegExp(`/ticket/${n}$`))
    await expect(ticketPage(page)).toBeVisible()
  })

  test('the settings page saves it to settings.yaml and the switch reads it back', async ({ page, omo }) => {
    await openBoard(page, omo)
    await page.goto(omo.url('/settings/general'))
    await expect(asPageSwitch(page)).toHaveAttribute('aria-checked', 'false')
    await expect(page.getByText("A click on a ticket opens its page instead of the panel over the board. The panel stays reachable with 'Open in panel'.")).toBeVisible()
    await asPageSwitch(page).click()
    await bar(page).getByRole('button', { name: 'Save', exact: true }).click()
    await expect(bar(page)).toContainText('Saved')
    await page.reload()
    await expect(asPageSwitch(page)).toHaveAttribute('aria-checked', 'true')
  })

  test('the General page has no accessibility violations in both themes', async ({ page, omo }) => {
    await setting(omo, true)
    await openBoard(page, omo)
    await page.goto(omo.url('/settings/general'))
    await expect(asPageSwitch(page)).toBeVisible()
    for (const colorScheme of ['dark', 'light'] as const) {
      await page.emulateMedia({ colorScheme })
      await page.evaluate(async () => {
        await document.fonts.ready
        await Promise.all(document.getAnimations().map((a) => a.finished.catch(() => undefined)))
      })
      const results = await new AxeBuilder({ page }).analyze()
      expect(results.violations, colorScheme).toEqual([])
    }
  })
})

test.describe('a running developer', () => {
  test.use({ omoOptions: { agents: { developer: script('ready', 'print|working', 'hang') } } })

  test('the sidebar link Open #n opens the page once switched on, live', async ({ page, omo }) => {
    await openBoard(page, omo)
    await sidebar(page).getByRole('tab', { name: /^Agents/ }).click()
    const n = await omo.createTicket({ title: 'Say hello' })
    await omo.moveTicket(n, 'ready')
    const session = await waitForSession(omo, n, 'developer')
    await sidebar(page).locator(`[data-session="${session.id}"]`).click()
    const link = sidebar(page).getByRole('link', { name: `Open #${n}` })
    await expect(link).toHaveAttribute('href', `/ticket/${n}`)
    await change(page, omo, true)
    await link.click()
    await expect(page).toHaveURL(new RegExp(`/ticket/${n}$`))
    await expect(ticketPage(page)).toBeVisible()
    await ticketPage(page).getByRole('link', { name: 'Open in panel' }).click()
    await expect(panel(page)).toBeVisible()
  })
})
