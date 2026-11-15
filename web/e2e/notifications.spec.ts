import AxeBuilder from '@axe-core/playwright'
import type { Page } from '@playwright/test'
import { expect, test } from './support/omo'
import { script, waitForSession } from './support/agents'
import { nudgeReconnect, openBoard } from './support/board'

const bell = (page: Page) => page.locator('[data-bell]')
const list = (page: Page) => page.locator('[data-bell-list]')
const panel = (page: Page) => page.locator('dialog[data-ticket-panel]')

async function sideways(page: Page): Promise<boolean> {
  return page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth)
}

const seededReview = {
  seed: {
    projects: [{ path: '{{repo}}', name: 'demo' }],
    tickets: [{ ref: 'review', type: 'task', title: 'Waiting for you', to: 'review', review: 'human', projects: ['demo'], branch: 'omo/review' }],
  },
}

test.describe('a seeded human review', () => {
  test.use({ omoOptions: seededReview })

  test('a seeded human review shows in the bell and opens the ticket', async ({ page, omo }) => {
    const n = omo.refs.review!
    await openBoard(page, omo)
    await expect(bell(page)).toHaveAttribute('aria-label', 'Notifications: 1 waiting for you, 0 unread mentions')
    await expect(page.locator('[data-bell-count]')).toHaveText('1')
    await bell(page).click()
    await expect(bell(page)).toHaveAttribute('aria-expanded', 'true')
    const item = list(page).locator(`[data-waiting="${n}"]`)
    await expect(item).toContainText(`#${n} Waiting for you`)
    await expect(item).toContainText('your review')
    // The same tickets as the server's "needs me".
    const needs = await omo.api<{ tickets: Array<{ number: number }> }>('GET', '/api/tickets?needs_me=true')
    expect(needs.tickets.map((t) => t.number)).toEqual([n])
    await item.click()
    await expect(page).toHaveURL(new RegExp(`/${n}$`))
    await expect(panel(page)).toBeVisible()
    await expect(list(page)).toHaveCount(0)
  })

  test('the bell works with the keyboard alone', async ({ page, omo }) => {
    const n = omo.refs.review!
    await openBoard(page, omo)
    await bell(page).focus()
    await page.keyboard.press('Enter')
    await expect(list(page)).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(list(page)).toHaveCount(0)
    await expect(bell(page)).toBeFocused()
    await page.keyboard.press('Enter')
    await page.keyboard.press('Tab')
    await expect(list(page).locator(`[data-waiting="${n}"]`)).toBeFocused()
    await page.keyboard.press('Enter')
    await expect(page).toHaveURL(new RegExp(`/${n}$`))
    await expect(panel(page)).toBeVisible()
  })

  test('the bell is usable at 390 px', async ({ page, omo }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    const n = omo.refs.review!
    await openBoard(page, omo)
    await expect(bell(page)).toBeVisible()
    await bell(page).click()
    await expect(list(page)).toBeVisible()
    const box = await list(page).boundingBox()
    expect(box).not.toBeNull()
    expect(box!.x).toBeGreaterThanOrEqual(0)
    expect(box!.x + box!.width).toBeLessThanOrEqual(390)
    expect(await sideways(page)).toBe(false)
    // A press outside closes it.
    await page.mouse.click(5, 800)
    await expect(list(page)).toHaveCount(0)
    await bell(page).click()
    await list(page).locator(`[data-waiting="${n}"]`).click()
    await expect(panel(page)).toBeVisible()
  })

  for (const scheme of ['dark', 'light'] as const) {
    for (const width of [1440, 390]) {
      test(`${scheme} theme, ${width} px: the open bell has no accessibility violations`, async ({ page, omo }) => {
        await page.setViewportSize({ width, height: width === 390 ? 844 : 900 })
        await page.emulateMedia({ colorScheme: scheme })
        await openBoard(page, omo)
        await bell(page).click()
        await expect(list(page).locator('[data-waiting]')).toHaveCount(1)
        const results = await new AxeBuilder({ page }).analyze()
        expect(results.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((x) => x.target.join(' ')) }))).toEqual([])
      })
    }
  }
})

test.describe('a developer that asks', () => {
  test.use({ omoOptions: { agents: { developer: script('ready', 'ask|Which colour should it be?', 'wait|10m', 'hang') } } })

  test('a question from the fake developer appears live and opens the thread at the question', async ({ page, omo }) => {
    await openBoard(page, omo)
    await expect(bell(page)).toHaveAttribute('aria-label', /: 0 waiting for you/)
    const n = await omo.createTicket({ title: 'Pick a colour' })
    await omo.moveTicket(n, 'ready')
    await waitForSession(omo, n, 'developer', 'waiting')
    await expect(bell(page)).toHaveAttribute('aria-label', /: 1 waiting for you/)
    await expect(page.locator('[data-announcer="polite"]')).toContainText(`New question for you on #${n}`)
    await bell(page).click()
    const item = list(page).locator(`[data-waiting="${n}"]`)
    await expect(item).toContainText('question for you')
    await item.click()
    await expect(page).toHaveURL(new RegExp(`/${n}\\?tab=comments&comment=\\d+$`))
    const question = panel(page).locator('[data-comment][data-question="open"]')
    await expect(question).toContainText('Which colour should it be?')
    await expect(question).toBeFocused()
  })

  test('with desktop notifications enabled and the page hidden, a new question raises one notification', async ({ page, omo, context }) => {
    await context.grantPermissions(['notifications'])
    await page.addInitScript(() => {
      const w = window as unknown as { __notes: Array<{ title: string; body: string; tag: string }>; __hidden: boolean }
      w.__notes = []
      w.__hidden = false
      class Recorder {
        static permission = 'granted'
        static requestPermission() {
          return Promise.resolve('granted')
        }
        onclick: (() => void) | null = null
        constructor(title: string, opts: { body?: string; tag?: string } = {}) {
          w.__notes.push({ title, body: opts.body ?? '', tag: opts.tag ?? '' })
        }
        close() {}
      }
      Object.defineProperty(window, 'Notification', { value: Recorder, configurable: true, writable: true })
      Object.defineProperty(document, 'visibilityState', { get: () => (w.__hidden ? 'hidden' : 'visible'), configurable: true })
    })
    const notes = () => page.evaluate(() => (window as unknown as { __notes: Array<{ title: string; body: string; tag: string }> }).__notes)

    // Present before the page loaded: never a desktop notification.
    const before = await omo.createTicket({ title: 'Asked before' })
    await omo.moveTicket(before, 'ready')
    await waitForSession(omo, before, 'developer', 'waiting')

    await openBoard(page, omo)
    await expect(bell(page)).toHaveAttribute('aria-label', /: 1 waiting for you/)
    await bell(page).click()
    const toggle = list(page).getByRole('switch', { name: 'Desktop notifications' })
    await expect(toggle).toHaveAttribute('aria-checked', 'false')
    await toggle.click()
    await expect(toggle).toHaveAttribute('aria-checked', 'true')
    await page.keyboard.press('Escape')
    await page.evaluate(() => ((window as unknown as { __hidden: boolean }).__hidden = true))

    const n = await omo.createTicket({ title: 'Pick a colour' })
    await omo.moveTicket(n, 'ready')
    await waitForSession(omo, n, 'developer', 'waiting')
    await expect.poll(async () => (await notes()).length).toBe(1)
    const [note] = await notes()
    expect(note!.title).toBe(`Question for you on #${n}`)
    expect(note!.body).toContain('Which colour should it be?')
    expect(note!.tag).toMatch(/^question:\d+$/)
    // The choice is kept per browser.
    expect(await page.evaluate(() => localStorage.getItem('omo.desktopNotifications'))).toBe('on')
    // Nothing more comes for the same question.
    await page.waitForTimeout(500)
    expect(await notes()).toHaveLength(1)
  })
})

test.describe('a developer that mentions the user', () => {
  test.use({ omoOptions: { agents: { developer: script('ready', 'comment|$TICKET|@user please look at <b>this</b>', 'hang') } } })

  test('an agent comment mentioning @user is unread until marked; a second tab follows', async ({ page, omo, context }) => {
    await openBoard(page, omo)
    const other = await context.newPage()
    await openBoard(other, omo)
    const n = await omo.createTicket({ title: 'Look at this' })
    await omo.moveTicket(n, 'ready')
    await waitForSession(omo, n, 'developer')

    for (const p of [page, other]) await expect(bell(p)).toHaveAttribute('aria-label', /, 1 unread mention$/)
    // Unread on the server too: a reload still shows it.
    await other.reload()
    await expect(bell(other)).toHaveAttribute('aria-label', /, 1 unread mention$/)

    await bell(page).click()
    const mention = list(page).locator('[data-mention]')
    await expect(mention).toHaveCount(1)
    await expect(mention).toHaveAttribute('data-unread', 'true')
    // Agent text is text: the tag shows, no element is made.
    await expect(mention).toContainText('@user please look at <b>this</b>')
    await expect(mention.locator('b')).toHaveCount(0)
    await mention.click()
    await expect(page).toHaveURL(new RegExp(`/${n}\\?tab=comments&comment=\\d+$`))
    await expect(bell(page)).toHaveAttribute('aria-label', /, 0 unread mentions$/)
    // The other tab follows without a reload, and the server agrees.
    await expect(bell(other)).toHaveAttribute('aria-label', /, 0 unread mentions$/)
    const server = await omo.api<{ unread: number; seen_through: number }>('GET', '/api/notifications')
    expect(server.unread).toBe(0)
    expect(server.seen_through).toBeGreaterThan(0)
  })

  test('a mention made while the page was away shows after the reconnect', async ({ page, omo }) => {
    await openBoard(page, omo)
    await page.route('**/api/events*', (route) => route.abort('connectionrefused'))
    await omo.stop()
    await expect(page.locator('[data-connection]')).toHaveAttribute('data-state', /^(retrying|reconnecting)$/)
    await omo.start()
    const n = await omo.createTicket({ title: 'While away' })
    await omo.moveTicket(n, 'ready')
    await expect.poll(async () => (await omo.api<{ unread: number }>('GET', '/api/notifications')).unread, { timeout: 30_000 }).toBe(1)
    await expect(bell(page)).toHaveAttribute('aria-label', /, 0 unread mentions$/)
    await page.unroute('**/api/events*')
    await nudgeReconnect(page)
    await expect(page.locator('[data-connection]')).toHaveAttribute('data-state', 'live', { timeout: 30_000 })
    await expect(bell(page)).toHaveAttribute('aria-label', /, 1 unread mention$/)
  })
})
