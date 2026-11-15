import { rmSync } from 'node:fs'
import AxeBuilder from '@axe-core/playwright'
import type { Page } from '@playwright/test'
import { expect, test } from './support/omo'
import { boardCards, card, column, expectBoardEqualsServer, expectBoardLive, expectCardIn, openBoard } from './support/board'
import { expectGreyed } from './support/drag'

const demo = { path: '{{repo}}', name: 'demo' }

/** The title link of a card: what has the focus when the card has it. */
const cardLink = (page: Page, n: number) => card(page, n).locator('[data-card-link]')
const polite = (page: Page) => page.locator('[data-announcer="polite"]')

async function server(omo: { api<T>(m: string, p: string): Promise<T> }, n: number): Promise<{ column: string }> {
  return omo.api<{ column: string }>('GET', `/api/tickets/${n}`)
}

/** Counts the move requests the page sends. */
function countMoves(page: Page): { readonly n: number } {
  const seen = { n: 0 }
  page.on('request', (r) => {
    if (r.method() === 'POST' && /\/api\/tickets\/\d+\/(move|reject)$/.test(new URL(r.url()).pathname)) seen.n++
  })
  return seen
}

/** Puts the keyboard focus on a card, as Tab would. */
async function focusCard(page: Page, n: number) {
  await cardLink(page, n).focus()
  await expect(cardLink(page, n)).toBeFocused()
}

test.describe('without a seed', () => {
  test('a card can be approved and reordered with the keyboard only', async ({ page, omo }) => {
    const a = await omo.createTicket({ title: 'First' })
    const b = await omo.createTicket({ title: 'Second' })
    const c = await omo.createTicket({ title: 'Third' })
    await openBoard(page, omo)

    // Reorder: #c to the top of Planning.
    await focusCard(page, c)
    await page.keyboard.press('Space')
    await expect(polite(page)).toContainText(`Picked up #${c}, Third. In Planning, position 3 of 3.`)
    await expect(card(page, c)).toHaveClass(/card--dragging/)
    await page.keyboard.press('ArrowUp')
    await expect(polite(page)).toHaveText('Position 2 of 3.')
    await page.keyboard.press('ArrowUp')
    await expect(polite(page)).toHaveText('Position 1 of 3.')
    await expect(card(page, a)).toHaveClass(/card--drop-before/)
    await page.keyboard.press('Space')
    await expect.poll(async () => (await boardCards(page)).planning).toEqual([c, a, b])
    await expect(polite(page)).toHaveText(`#${c} moved within Planning.`)
    await expect(cardLink(page, c)).toBeFocused()

    // Approve: #c across to Ready For Dev, with m this time; Review is skipped and then named with its reason.
    await page.keyboard.press('m')
    await page.keyboard.press('ArrowRight')
    await expect(polite(page)).toHaveText('Ready For Dev: approves. Position 1 of 1.')
    await expect(column(page, 'Ready For Dev')).toHaveClass(/column--over/)
    await page.keyboard.press('ArrowRight')
    await expect(polite(page)).toContainText(`No column to the right accepts #${c}.`)
    await expect(polite(page)).toContainText('Review: A ticket enters Review when its agent hands it in.')
    await page.keyboard.press('Enter')
    await expectCardIn(page, c, 'Ready For Dev')
    await expect(polite(page)).toHaveText(`#${c} moved to Ready For Dev.`)
    await expect(cardLink(page, c)).toBeFocused()
    await expect.poll(async () => (await server(omo, c)).column).toBe('ready')
    await expect(page.locator('.card--pending')).toHaveCount(0)
    await expect(cardLink(page, c)).toBeFocused()
    await expectBoardEqualsServer(page, omo)
  })

  test('Escape cancels, focus stays on the card and nothing is sent', async ({ page, omo }) => {
    const n = await omo.createTicket({ title: 'Stay' })
    await openBoard(page, omo)
    const moves = countMoves(page)
    await focusCard(page, n)
    await page.keyboard.press('Space')
    await page.keyboard.press('ArrowRight')
    await expect(column(page, 'Ready For Dev')).toHaveClass(/column--over/)
    await page.keyboard.press('Escape')
    await expect(polite(page)).toHaveText(`Move of #${n} cancelled.`)
    await expect(column(page, 'Ready For Dev')).not.toHaveClass(/column--over/)
    await expect(card(page, n)).not.toHaveClass(/card--dragging/)
    await expect(cardLink(page, n)).toBeFocused()
    // Enter still opens the card.
    await page.keyboard.press('Enter')
    await expect(page.locator('dialog[data-ticket-panel]')).toBeVisible()
    expect(moves.n).toBe(0)
    expect((await server(omo, n)).column).toBe('planning')
  })

  test('a refused move snaps back, is announced and keeps the focus on the card', async ({ page, omo }) => {
    const n = await omo.createTicket({ title: 'Refused' })
    await openBoard(page, omo)
    const projects = await omo.api<{ path: string }[]>('GET', '/api/projects')
    rmSync(projects[0]!.path, { recursive: true, force: true })
    await focusCard(page, n)
    await page.keyboard.press('Space')
    await page.keyboard.press('ArrowRight')
    await page.keyboard.press('Space')
    await expect(page.getByText('Move refused')).toBeVisible()
    await expectCardIn(page, n, 'Planning')
    const notice = (await card(page, n).locator('[data-move-notice]').textContent())!
    await expect(page.locator('[aria-live="assertive"]')).toContainText(`Move of #${n} refused: ${notice}`)
    await expect(cardLink(page, n)).toBeFocused()
    expect((await server(omo, n)).column).toBe('planning')
  })

  test('the focus stays on the card when the stream changes the board', async ({ page, omo }) => {
    const a = await omo.createTicket({ title: 'Mine' })
    const b = await omo.createTicket({ title: 'Other' })
    await openBoard(page, omo)
    await focusCard(page, a)
    const said = await polite(page).textContent()
    // A story arrives: Planning now shows groups and draws its cards anew.
    const story = await omo.createTicket({ title: 'A story', type: 'story' })
    await expect(card(page, story)).toBeVisible()
    await expect(column(page, 'Planning')).toContainText('Stories')
    await expect(cardLink(page, a)).toBeFocused()
    // Moves of others are shown, not announced.
    await omo.moveTicket(b, 'ready')
    await expectCardIn(page, b, 'Ready For Dev')
    await expect(cardLink(page, a)).toBeFocused()
    expect(await polite(page).textContent()).toBe(said)
    // Picked up, it stays picked up and focused while others change the board.
    await page.keyboard.press('Space')
    await omo.createTicket({ title: 'Another' })
    await expect(column(page, 'Planning').locator('li[data-ticket]')).toHaveCount(3)
    await expect(cardLink(page, a)).toBeFocused()
    await expect(card(page, a)).toHaveClass(/card--dragging/)
    await page.keyboard.press('Escape')
  })

  test('reduced motion: cards and dialogs have no transitions', async ({ page, omo }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' })
    const n = await omo.createTicket({ title: 'Still' })
    await openBoard(page, omo)
    const duration = (sel: string) => page.locator(sel).first().evaluate((el) => getComputedStyle(el).transitionDuration)
    expect(await duration(`li[data-ticket="${n}"]`)).toBe('0s')
    expect(await duration(`li[data-ticket="${n}"] [data-move-menu] button`)).toBe('0s')
    await page.keyboard.press('c')
    await expect(page.getByRole('dialog', { name: 'Create ticket' })).toBeVisible()
    expect(await duration('dialog[open]')).toBe('0s')
  })
})

test.describe('seeded', () => {
  test.use({
    omoOptions: {
      seed: {
        projects: [demo],
        tickets: [
          { ref: 'reject', type: 'task', title: 'Needs work', to: 'review', review: 'human', projects: ['demo'], branch: 'omo/reject' },
          { ref: 'done', type: 'task', title: 'Finished', to: 'done', projects: ['demo'] },
        ],
      },
    },
  })

  test('a move that rejects asks for the comment, then keeps the focus on the card', async ({ page, omo }) => {
    const n = omo.refs.reject!
    await openBoard(page, omo)
    await focusCard(page, n)
    await page.keyboard.press('Space')
    await page.keyboard.press('ArrowLeft')
    await expect(polite(page)).toHaveText('In Progress: rejects.')
    await page.keyboard.press('Space')
    const dialog = page.getByRole('dialog', { name: `Reject #${n}?` })
    await expect(dialog).toBeVisible()
    await expect(dialog.getByLabel('Comment')).toBeFocused()
    await page.keyboard.type('The tests are missing.')
    await dialog.getByRole('button', { name: 'Reject' }).press('Enter')
    await expect(dialog).toBeHidden()
    await expectCardIn(page, n, 'In Progress')
    await expect(cardLink(page, n)).toBeFocused()
    await expect.poll(async () => (await server(omo, n)).column).toBe('in_progress')
  })

  test('every column is unavailable for a Done ticket, and the keyboard says why', async ({ page, omo }) => {
    const n = omo.refs.done!
    await openBoard(page, omo)
    const moves = countMoves(page)
    await focusCard(page, n)
    await page.keyboard.press('Space')
    await expect(polite(page)).toContainText('No column accepts it. Done: Done tickets stay where they are.')
    await expectGreyed(page, ['Planning', 'Ready For Dev', 'In Progress', 'Review', 'Done'])
    await page.keyboard.press('ArrowLeft')
    await expect(polite(page)).toHaveText(`No other column accepts #${n}. Review, In Progress, Ready For Dev, Planning: Done tickets stay where they are.`)
    await page.keyboard.press('Space')
    await expect(polite(page)).toHaveText(`#${n} was not moved: Done does not accept it. Done tickets stay where they are.`)
    await expect(cardLink(page, n)).toBeFocused()
    expect(moves.n).toBe(0)
  })
})

for (const scheme of ['dark', 'light'] as const) {
  test(`${scheme} theme: the board with a move menu open has no accessibility violations`, async ({ page, omo }) => {
    await page.emulateMedia({ colorScheme: scheme })
    const n = await omo.createTicket({ title: 'Menu' })
    await omo.createTicket({ title: 'Below' })
    await openBoard(page, omo)
    await expectBoardLive(page)
    await focusCard(page, n)
    await page.keyboard.press('Tab')
    const trigger = card(page, n).getByRole('button', { name: `Move #${n}…` })
    await expect(trigger).toBeFocused()
    await page.keyboard.press('Enter')
    const menu = page.getByRole('menu')
    await expect(menu).toBeVisible()
    await expect(menu.getByRole('menuitem', { name: 'Ready For Dev' })).toBeVisible()
    const results = await new AxeBuilder({ page }).analyze()
    expect(results.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((x) => x.target.join(' ')) }))).toEqual([])
    // The disabled columns are reachable and say why.
    await page.keyboard.press('ArrowDown')
    await page.keyboard.press('ArrowDown')
    await expect(menu.getByRole('menuitem', { name: 'In Progress' })).toBeFocused()
    await expect(menu.getByRole('menuitem', { name: 'In Progress' })).toHaveAccessibleDescription(
      'A ticket enters In Progress when its agent starts or its review is rejected.',
    )
    await page.keyboard.press('Escape')
    await expect(trigger).toBeFocused()
  })
}
