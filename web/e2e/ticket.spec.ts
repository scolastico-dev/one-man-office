import AxeBuilder from '@axe-core/playwright'
import type { Page } from '@playwright/test'
import { expect, test } from './support/omo'
import { card, expectBoardLive, expectCardIn, expectCardTitle, openBoard } from './support/board'

interface ServerTicket {
  number: number
  title: string
  column: string
  resolution: string
  review_stage: string
  description: string
  description_version: number
  failed_step?: string
}

const demo = { path: '{{repo}}', name: 'demo' }

function panel(page: Page) {
  return page.locator('dialog[data-ticket-panel]')
}

async function server(omo: { api<T>(m: string, p: string): Promise<T> }, n: number): Promise<ServerTicket> {
  return omo.api<ServerTicket>('GET', `/api/tickets/${n}`)
}

async function openByClick(page: Page, n: number) {
  await card(page, n).locator('[data-card-link]').click()
  await expect(panel(page)).toBeVisible()
  await expect(page).toHaveURL(new RegExp(`/${n}$`))
}

/** Collects the bodies of the page's requests to a path. */
function bodiesOf(page: Page, path: RegExp): unknown[] {
  const out: unknown[] = []
  page.on('request', (r) => {
    if (r.method() !== 'GET' && path.test(new URL(r.url()).pathname)) out.push(r.postData() === null ? undefined : JSON.parse(r.postData()!))
  })
  return out
}

test.describe('without a seed', () => {
  test('/<n> deep link opens the panel over the board; Escape returns to the board', async ({ page, omo }) => {
    const n = await omo.createTicket({ title: 'Deep linked' })
    await openBoard(page, omo)

    // By click: Escape closes, the address is the board's again and focus is back on the card.
    await openByClick(page, n)
    await expect(panel(page).getByRole('heading', { level: 2, name: 'Deep linked' })).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(panel(page)).toHaveCount(0)
    await expect(page).toHaveURL(omo.url('/'))
    await expect(card(page, n).locator('[data-card-link]')).toBeFocused()

    // By address, and again after a reload: the board is behind the panel.
    await page.goto(omo.url(`/${n}`))
    await expectBoardLive(page)
    const dialog = page.getByRole('dialog', { name: 'Deep linked' })
    await expect(dialog).toBeVisible()
    await expect(dialog.locator('[data-ticket-column]')).toHaveText('Planning')
    await page.reload()
    await expect(page.getByRole('dialog', { name: 'Deep linked' })).toBeVisible()
    await page.getByRole('button', { name: 'Close' }).click()
    await expect(panel(page)).toHaveCount(0)
    await expect(page).toHaveURL(omo.url('/'))
    await expect(card(page, n)).toBeVisible()

    // An unknown number says so and leads back.
    await page.goto(omo.url('/9999'))
    await expect(panel(page)).toContainText('Ticket #9999 does not exist.')
    await panel(page).getByRole('button', { name: 'Back to the board' }).click()
    await expect(page).toHaveURL(omo.url('/'))
  })

  test('edit the title and description and see them rendered; a second page sees the change live', async ({ page, omo }) => {
    const n = await omo.createTicket({ title: 'Before' })
    const other = await omo.createTicket({ title: 'The other one' })
    await openBoard(page, omo)
    const second = await page.context().newPage()
    await second.goto(omo.url(`/${n}`))
    await expect(panel(second)).toBeVisible()

    await openByClick(page, n)
    await panel(page).getByRole('button', { name: 'Edit title' }).click()
    await panel(page).getByLabel('Title').fill('After')
    await panel(page).getByLabel('Title').press('Enter')
    await expect(panel(page).locator('[data-ticket-title]')).toHaveText('After')
    await expect(panel(second).locator('[data-ticket-title]')).toHaveText('After')
    await expectCardTitle(page, n, 'After')

    await panel(page).getByRole('button', { name: 'Edit', exact: true }).click()
    const area = panel(page).getByLabel('Description (Markdown)')
    await area.fill(`## Goal\n\nDepends on #${other}.\n\n- one\n- two`)
    await panel(page).getByRole('button', { name: 'Preview' }).click()
    await expect(panel(page).locator('[data-description-preview] h4')).toHaveText('Goal')
    await panel(page).getByRole('button', { name: 'Save', exact: true }).click()
    const view = panel(page).locator('[data-description]')
    await expect(view.locator('h4')).toHaveText('Goal')
    await expect(view.locator('li')).toHaveText(['one', 'two'])
    await expect(view.locator(`a[data-ticket-ref="${other}"]`)).toHaveAttribute('title', `#${other} The other one (Planning)`)
    await expect(panel(second).locator('[data-description] h4')).toHaveText('Goal')
    expect((await server(omo, n)).description).toContain('## Goal')

    // A reference opens that ticket in place.
    await view.locator(`a[data-ticket-ref="${other}"]`).click()
    await expect(page).toHaveURL(new RegExp(`/${other}$`))
    await expect(panel(page).locator('[data-ticket-title]')).toHaveText('The other one')
  })

  test('a concurrent description edit keeps my text and lets me compare and decide', async ({ page, omo }) => {
    const n = await omo.createTicket({ title: 'Shared' })
    await omo.api('PUT', `/api/tickets/${n}/description`, { text: 'version one' })
    await openBoard(page, omo)
    await openByClick(page, n)
    await expect(panel(page).locator('[data-description]')).toHaveText('version one')
    await panel(page).getByRole('button', { name: 'Edit', exact: true }).click()
    await panel(page).getByLabel('Description (Markdown)').fill('my version')

    // Someone else saves meanwhile.
    await omo.api('PUT', `/api/tickets/${n}/description`, { text: 'their version' })
    await expect(panel(page).locator('[data-stale]')).toContainText('Someone changed the description meanwhile.')
    await panel(page).getByRole('button', { name: 'Save', exact: true }).click()
    await expect(panel(page).getByLabel('Your description (Markdown)').or(panel(page).getByLabel('Description (Markdown)'))).toHaveValue('my version')
    expect((await server(omo, n)).description).toBe('their version')

    await panel(page).getByRole('button', { name: 'Show theirs' }).click()
    await expect(panel(page).locator('[data-theirs]')).toContainText('their version')
    await panel(page).getByRole('button', { name: 'Save mine anyway' }).click()
    await expect(panel(page).locator('[data-description]')).toHaveText('my version')
    await expect.poll(async () => (await server(omo, n)).description).toBe('my version')
  })

  test('approve from the panel moves the card to Ready For Dev', async ({ page, omo }) => {
    const n = await omo.createTicket({ title: 'Approve me' })
    await openBoard(page, omo)
    await openByClick(page, n)
    await panel(page).getByRole('button', { name: 'Approve', exact: true }).click()
    await expect(panel(page).locator('[data-ticket-column]')).toHaveText('Ready For Dev')
    await expectCardIn(page, n, 'Ready For Dev')
    expect((await server(omo, n)).column).toBe('ready')
  })

  test('cancel a story with tasks from the panel, keeping the branch', async ({ page, omo }) => {
    const story = await omo.createTicket({ title: 'Big story', type: 'story' })
    const a = await omo.createTicket({ title: 'First task', story })
    const b = await omo.createTicket({ title: 'Second task', story })
    await openBoard(page, omo)
    const sent = bodiesOf(page, /\/cancel$/)
    await openByClick(page, story)
    await panel(page).getByRole('button', { name: 'Cancel ticket…' }).click()
    const dialog = page.getByRole('dialog', { name: `Cancel #${story}?` })
    await expect(dialog.locator('[data-cancel-tasks] li')).toHaveText([`#${a} First task (Planning)`, `#${b} Second task (Planning)`])
    await expect(dialog.getByLabel('Keep the branch')).toBeChecked()
    await dialog.getByRole('button', { name: 'Cancel the ticket' }).click()
    await expect(dialog).toHaveCount(0)
    await expect(panel(page).locator('[data-ticket-column]')).toHaveText('Done')
    await expect(panel(page).locator('[data-ticket-resolution]')).toHaveText('cancelled')
    await expectCardIn(page, a, 'Done')
    await expectCardIn(page, b, 'Done')
    expect(sent).toEqual([{ delete_branch: false }])
    for (const x of [story, a, b]) expect((await server(omo, x)).resolution).toBe('cancelled')
  })

  test('cancel deleting the branch explains the uncommitted-work rule and never asks to discard', async ({ page, omo }) => {
    const n = await omo.createTicket({ title: 'Throw away' })
    await openBoard(page, omo)
    const sent = bodiesOf(page, /\/cancel$/)
    await openByClick(page, n)
    await panel(page).getByRole('button', { name: 'Cancel ticket…' }).click()
    const dialog = page.getByRole('dialog', { name: `Cancel #${n}?` })
    await dialog.getByLabel('Delete the branch').check()
    await expect(dialog.locator('[data-cancel-effect]')).toContainText('omo keeps the folder and the branch and lists the files in a comment on the ticket')
    await dialog.getByRole('button', { name: 'Cancel the ticket' }).click()
    await expect(panel(page).locator('[data-ticket-resolution]')).toHaveText('cancelled')
    expect(sent).toEqual([{ delete_branch: true }])
  })

  test('Markdown from a description cannot run script, load images or link to javascript:', async ({ page, omo }) => {
    const n = await omo.createTicket({ title: 'Hostile' })
    const hostile = [
      '<script>window.__owned = 1</script>',
      '<img src=x onerror="window.__owned = 2">',
      '[click me](javascript:window.__owned=3)',
      '[data](data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==)',
      '![tracker](https://tracker.example/pixel.png)',
      '<a href="javascript:window.__owned=4">raw</a>',
      'A safe link: https://example.com/docs',
    ].join('\n\n')
    await omo.api('PUT', `/api/tickets/${n}/description`, { text: hostile })
    const external: string[] = []
    page.on('request', (r) => {
      if (!r.url().startsWith(omo.url('/').replace(/\/$/, ''))) external.push(r.url())
    })
    await openBoard(page, omo)
    await openByClick(page, n)
    const view = panel(page).locator('[data-description]')
    await expect(view).toContainText('<script>window.__owned = 1</script>')
    await expect(view.locator('script, img, iframe')).toHaveCount(0)
    const hrefs = await view.locator('a').evaluateAll((as) => as.map((a) => a.getAttribute('href')))
    expect(hrefs).toEqual(['https://tracker.example/pixel.png', 'https://example.com/docs'])
    await expect(view.locator('a').first()).toHaveText('Image: tracker')
    await expect(view.locator('a').last()).toHaveAttribute('rel', 'noreferrer noopener')
    await view.getByText('[click me](javascript:window.__owned=3)').click()
    expect(await page.evaluate(() => (window as { __owned?: number }).__owned)).toBeUndefined()
    expect(external).toEqual([])
  })
})

test.describe('seeded', () => {
  test.use({
    omoOptions: {
      seed: {
        projects: [demo],
        tickets: [
          { ref: 'review', type: 'task', title: 'Waiting for you', to: 'review', review: 'human', projects: ['demo'], branch: 'omo/review' },
          { ref: 'reject', type: 'task', title: 'Needs work', to: 'review', review: 'human', projects: ['demo'], branch: 'omo/reject' },
          { ref: 'running', type: 'task', title: 'Being built', to: 'in_progress', projects: ['demo'], branch: 'omo/running' },
        ],
      },
    },
  })

  test('approve at the human stage says it merges locally and leaves the card merging…', async ({ page, omo }) => {
    const n = omo.refs.review!
    await openBoard(page, omo)
    await openByClick(page, n)
    await expect(panel(page).locator('[data-ticket-badge]')).toHaveText('your review')
    await panel(page).getByRole('button', { name: 'Approve and merge…' }).click()
    const dialog = page.getByRole('dialog', { name: `Approve #${n}?` })
    await expect(dialog.locator('[data-approve-effect]')).toHaveText('omo merges omo/review into main locally.')
    await dialog.getByRole('button', { name: 'Approve and merge' }).click()
    await expect(dialog).toHaveCount(0)
    // The seeding tool records the branch on the board only, so omo's merge
    // stops with a failed step and the ticket stays in Review at stage merging.
    await expect.poll(async () => (await server(omo, n)).failed_step).toBe('merge')
    expect((await server(omo, n)).review_stage).toBe('merging')
    // The panel follows.
    await expect(panel(page).locator('[data-ticket-caption]')).toHaveText('merging…')
    await expect(panel(page).locator('[data-ticket-badge]')).toHaveText('needs attention')
    await expect(panel(page).getByRole('button', { name: 'Reject…' })).toHaveCount(0)
  })

  test('reject from the panel requires a comment and moves the card back to In Progress', async ({ page, omo }) => {
    const n = omo.refs.reject!
    await openBoard(page, omo)
    await openByClick(page, n)
    await panel(page).getByRole('button', { name: 'Reject…' }).click()
    const dialog = page.getByRole('dialog', { name: `Reject #${n}?` })
    await dialog.getByRole('button', { name: 'Reject' }).click()
    await expect(dialog.getByText('Write what needs to change.')).toBeVisible()
    await dialog.getByLabel('Comment').fill('Please add tests.')
    await dialog.getByRole('button', { name: 'Reject' }).click()
    await expect(dialog).toHaveCount(0)
    await expect(panel(page).locator('[data-ticket-column]')).toHaveText('In Progress')
    await expectCardIn(page, n, 'In Progress')
    expect((await server(omo, n)).column).toBe('in_progress')
  })

  test('on a 390 px screen the panel fills the screen and its actions are in view', async ({ page, omo }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    const n = omo.refs.running!
    await omo.api('PUT', `/api/tickets/${n}/description`, { text: Array.from({ length: 80 }, (_, i) => `Line ${i} of a long description.`).join('\n\n') })
    await page.goto(omo.accessUrl.replace(/\/#/, `/${n}#`))
    await expect(panel(page)).toBeVisible()
    const box = await panel(page).boundingBox()
    expect(box).toEqual({ x: 0, y: 0, width: 390, height: 844 })
    const cancel = panel(page).getByRole('button', { name: 'Cancel ticket…' })
    await expect(cancel).toBeInViewport()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    expect(await panel(page).evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true)
  })

  for (const scheme of ['dark', 'light'] as const) {
    test(`the open panel has no accessibility violations (${scheme})`, async ({ page, omo }) => {
      await page.emulateMedia({ colorScheme: scheme })
      const n = omo.refs.review!
      await omo.api('PUT', `/api/tickets/${n}/description`, { text: '## Goal\n\nShip it. See #1.\n\n```\ncode\n```' })
      await openBoard(page, omo)
      await openByClick(page, n)
      await expect(panel(page).locator('[data-description] h4')).toHaveText('Goal')
      const results = await new AxeBuilder({ page }).analyze()
      expect(results.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((x) => x.target.join(' ')) }))).toEqual([])
    })
  }
})
