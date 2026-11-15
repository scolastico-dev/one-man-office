import { rmSync } from 'node:fs'
import { expect, test } from './support/omo'
import { boardCards, card, column, expectBoardEqualsServer, expectBoardLive, expectCardIn, openBoard } from './support/board'
import { dragCard, expectGreyed, moveOutside, moveOver, pickUp, release } from './support/drag'

interface ServerTicket {
  number: number
  column: string
  review_stage: string
  projects: { branch: string }[]
  agents: unknown[]
  failed_step?: string
}

const demo = { path: '{{repo}}', name: 'demo' }

async function server(omo: { api<T>(m: string, p: string): Promise<T> }, n: number): Promise<ServerTicket> {
  return omo.api<ServerTicket>('GET', `/api/tickets/${n}`)
}

/** Counts the move requests the page sends. */
function countMoves(page: import('@playwright/test').Page): { readonly n: number } {
  const seen = { n: 0 }
  page.on('request', (r) => {
    if (r.method() === 'POST' && /\/api\/tickets\/\d+\/(move|reject)$/.test(new URL(r.url()).pathname)) seen.n++
  })
  return seen
}

test.describe('without a seed', () => {
  test('within a column: changes priority and the order survives a reload', async ({ page, omo }) => {
    const a = await omo.createTicket({ title: 'First' })
    const b = await omo.createTicket({ title: 'Second' })
    const c = await omo.createTicket({ title: 'Third' })
    await openBoard(page, omo)
    expect((await boardCards(page)).planning).toEqual([a, b, c])

    await pickUp(page, c)
    await moveOver(page, 'Planning', { beforeCard: a })
    await expect(column(page, 'Planning').locator('[data-drop-action]')).toHaveText('Drop to change the priority.')
    await expect(card(page, a)).toHaveClass(/card--drop-before/)
    await release(page)
    await expect.poll(async () => (await boardCards(page)).planning).toEqual([c, a, b])
    await expect(page.locator('.card--pending')).toHaveCount(0)

    await page.reload()
    await expectBoardLive(page)
    expect((await boardCards(page)).planning).toEqual([c, a, b])
  })

  test('Planning → Ready For Dev approves', async ({ page, omo }) => {
    const n = await omo.createTicket({ title: 'Approve me' })
    await openBoard(page, omo)
    await pickUp(page, n)
    await moveOver(page, 'Ready For Dev')
    await expect(column(page, 'Ready For Dev')).toHaveClass(/column--over/)
    await expect(column(page, 'Ready For Dev').locator('[data-drop-action]')).toHaveText('Drop to approve.')
    await release(page)
    await expectCardIn(page, n, 'Ready For Dev')
    await expect(card(page, n)).not.toHaveClass(/card--pending/)
    expect((await server(omo, n)).column).toBe('ready')
  })

  test('Ready For Dev → Planning withdraws', async ({ page, omo }) => {
    const n = await omo.createTicket({ title: 'Withdraw me' })
    await omo.moveTicket(n, 'ready')
    await openBoard(page, omo)
    await dragCard(page, n, 'Planning')
    await expectCardIn(page, n, 'Planning')
    await expect.poll(async () => (await server(omo, n)).column).toBe('planning')
  })

  test('Escape, a drop on a greyed column and a drop outside the columns send nothing', async ({ page, omo }) => {
    const n = await omo.createTicket({ title: 'Stay' })
    await openBoard(page, omo)
    const moves = countMoves(page)

    await pickUp(page, n)
    await moveOver(page, 'Ready For Dev')
    await page.keyboard.press('Escape')
    await expect(page.locator('[data-drag-ghost]')).toHaveCount(0)
    await expect(column(page, 'Ready For Dev')).not.toHaveClass(/column--over/)
    await page.mouse.up()

    await pickUp(page, n)
    await moveOutside(page)
    await release(page)

    await pickUp(page, n)
    await moveOver(page, 'Review')
    await release(page)

    await expectCardIn(page, n, 'Planning')
    // A click on the card still opens it.
    await card(page, n).locator('a').click()
    await expect(page.locator('dialog[data-ticket-panel]')).toBeVisible()
    expect(moves.n).toBe(0)
    expect((await server(omo, n)).column).toBe('planning')
  })

  test('a refusal snaps back, shows the server message and survives a reload', async ({ page, omo }) => {
    const n = await omo.createTicket({ title: 'Refused' })
    await openBoard(page, omo)
    const projects = await omo.api<{ id: string; path: string }[]>('GET', '/api/projects')
    // The project folder disappears after the page has loaded: the browser still thinks it is there.
    rmSync(projects[0]!.path, { recursive: true, force: true })

    await pickUp(page, n)
    await moveOver(page, 'Ready For Dev')
    await expect(column(page, 'Ready For Dev')).not.toHaveAttribute('aria-disabled', 'true')
    await release(page)

    await expectCardIn(page, n, 'Planning')
    await expect(card(page, n).locator('[data-move-notice]')).not.toHaveText('')
    const notice = (await card(page, n).locator('[data-move-notice]').textContent())!
    await expect(page.getByText('Move refused')).toBeVisible()
    await expect(page.locator('[aria-live="assertive"]')).toContainText(`Move of #${n} refused: ${notice}`)
    expect((await server(omo, n)).column).toBe('planning')

    await page.reload()
    await expectBoardLive(page)
    await expectCardIn(page, n, 'Planning')
  })

  test('a card moved by someone else while it is dragged ends the drag with a notice', async ({ page, omo }) => {
    const n = await omo.createTicket({ title: 'Contested' })
    const other = await omo.createTicket({ title: 'Neighbour' })
    await openBoard(page, omo)
    const moves = countMoves(page)
    await pickUp(page, n)
    await moveOver(page, 'Planning', { beforeCard: other })

    await omo.moveTicket(n, 'ready')
    await expect(page.locator('[data-drag-ghost]')).toHaveCount(0)
    await expect(page.getByText(`#${n} was moved to Ready For Dev meanwhile. Nothing was moved.`).first()).toBeVisible()
    await moveOver(page, 'Planning')
    await page.mouse.up()
    await expectCardIn(page, n, 'Ready For Dev')
    expect(moves.n).toBe(0)
  })

  test('a card that arrives during a drag does not upset the drop place', async ({ page, omo }) => {
    const a = await omo.createTicket({ title: 'A' })
    const b = await omo.createTicket({ title: 'B' })
    await openBoard(page, omo)
    await pickUp(page, b)
    await moveOver(page, 'Planning', { beforeCard: a })
    const c = await omo.createTicket({ title: 'C' })
    await expect(card(page, c)).toBeVisible()
    await release(page)
    await expect.poll(async () => (await boardCards(page)).planning).toEqual([b, a, c])
    await expectBoardEqualsServer(page, omo)
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
          { ref: 'running2', type: 'task', title: 'Also being built', to: 'in_progress', projects: ['demo'], branch: 'omo/running2' },
          { ref: 'done', type: 'task', title: 'Finished', to: 'done', projects: ['demo'] },
        ],
      },
    },
  })

  test('Review → Done approves a review waiting for the user and shows merging…', async ({ page, omo }) => {
    const n = omo.refs.review!
    await openBoard(page, omo)
    await pickUp(page, n)
    await moveOver(page, 'Done')
    await expect(column(page, 'Done').locator('[data-drop-action]')).toHaveText('Drop to approve the review.')
    await release(page)
    // The card stays in Review while omo merges it.
    await expect(card(page, n)).toContainText('merging…')
    await expectCardIn(page, n, 'Review')
    // The seeding tool records the branch on the board only, so omo's merge
    // stops with a failed step and the card stays in Review, flagged.
    await expect.poll(async () => (await server(omo, n)).failed_step).toBe('merge')
    expect((await server(omo, n)).review_stage).toBe('merging')
    await expect(card(page, n).locator('[data-badge]')).toHaveText('needs attention')
    await expect(card(page, n)).toContainText('merging…')
    await expectCardIn(page, n, 'Review')
  })

  test('Review → In Progress asks for a comment and rejects', async ({ page, omo }) => {
    const n = omo.refs.reject!
    await openBoard(page, omo)
    const moves = countMoves(page)
    await dragCard(page, n, 'In Progress')
    const dialog = page.getByRole('dialog', { name: `Reject #${n}?` })
    await expect(dialog).toBeVisible()
    await expectCardIn(page, n, 'Review')
    // Cancelling sends nothing.
    await dialog.getByRole('button', { name: 'Cancel' }).click()
    await expect(dialog).toBeHidden()
    expect(moves.n).toBe(0)

    await dragCard(page, n, 'In Progress')
    await dialog.getByRole('button', { name: 'Reject' }).click()
    await expect(dialog.getByText('Write what needs to change.')).toBeVisible()
    expect(moves.n).toBe(0)
    await dialog.getByLabel('Comment').fill('The tests are missing.')
    await dialog.getByRole('button', { name: 'Reject' }).click()
    await expect(dialog).toBeHidden()
    await expectCardIn(page, n, 'In Progress')
    await expect.poll(async () => (await server(omo, n)).column).toBe('in_progress')
    expect(moves.n).toBe(1)
  })

  test('In Progress → Planning asks for confirmation and stops; the branch stays', async ({ page, omo }) => {
    const n = omo.refs.running!
    await openBoard(page, omo)
    const moves = countMoves(page)
    await dragCard(page, n, 'Planning')
    const dialog = page.getByRole('dialog', { name: `Stop #${n} and move it to Planning?` })
    await expect(dialog).toBeVisible()
    // omo ends every session left from before it started, the seeded one
    // included, and starts nothing while paused: no agent works on the ticket.
    expect((await server(omo, n)).agents).toEqual([])
    await expect(dialog).toContainText('The branch and worktree are kept.')
    await expect(dialog.locator('[data-stop-no-agents]')).toHaveText('No agent is working on it.')
    await expect(dialog.locator('[data-stop-agents]')).toHaveCount(0)
    await dialog.getByRole('button', { name: 'Cancel' }).click()
    await expect(dialog).toBeHidden()
    await expectCardIn(page, n, 'In Progress')
    expect(moves.n).toBe(0)

    await dragCard(page, n, 'Planning')
    await dialog.getByRole('button', { name: 'Stop and move' }).click()
    await expect(dialog).toBeHidden()
    await expectCardIn(page, n, 'Planning')
    await expect.poll(async () => (await server(omo, n)).column).toBe('planning')
    expect((await server(omo, n)).projects[0]!.branch).toBe('omo/running')
  })

  test('In Progress → Ready For Dev asks for confirmation and stops', async ({ page, omo }) => {
    const n = omo.refs.running2!
    await openBoard(page, omo)
    await dragCard(page, n, 'Ready For Dev')
    const dialog = page.getByRole('dialog', { name: `Stop #${n} and move it to Ready For Dev?` })
    await dialog.getByRole('button', { name: 'Stop and move' }).click()
    await expect(dialog).toBeHidden()
    await expectCardIn(page, n, 'Ready For Dev')
    await expect.poll(async () => (await server(omo, n)).column).toBe('ready')
  })

  test('In Progress and Review are greyed as targets; a Done card greys every column', async ({ page, omo }) => {
    await openBoard(page, omo)
    const moves = countMoves(page)
    await pickUp(page, omo.refs.running!)
    await moveOver(page, 'Review')
    await expectGreyed(page, ['Review', 'Done'])
    await release(page)

    await pickUp(page, omo.refs.done!)
    await moveOver(page, 'Planning')
    await expectGreyed(page, ['Planning', 'Ready For Dev', 'In Progress', 'Review', 'Done'])
    await expect(column(page, 'Done').locator('[data-reason]')).toHaveText('Done tickets stay where they are.')
    await release(page)
    expect(moves.n).toBe(0)
  })
})

test.describe('narrow screen', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('at 390 px a card is dragged onto a column tab and approved', async ({ page, omo }) => {
    const n = await omo.createTicket({ title: 'Small screen' })
    await openBoard(page, omo)
    await pickUp(page, n)
    await moveOver(page, 'Ready For Dev', { tab: true })
    await expect(page.locator('[role="tab"][data-drop-column="ready"]')).toHaveClass(/switcher__tab--over/)
    await expect(page.locator('[role="tab"][data-drop-column="review"]')).toHaveAttribute('aria-disabled', 'true')
    await release(page)
    await expect.poll(async () => (await server(omo, n)).column).toBe('ready')
    await page.getByRole('tab', { name: /^Ready For Dev/ }).click()
    await expect(card(page, n)).toBeVisible()
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true)
  })
})
