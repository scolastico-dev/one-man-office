import type { Page } from '@playwright/test'
import { expect, test, type Omo } from './support/omo'
import { boardCards, card, expectBoardEqualsServer, expectBoardLive, expectCardIn, nudgeReconnect, openBoard } from './support/board'

/** Makes every request for the event stream fail, as if the network between page and omo were down. */
async function blockStream(page: Page): Promise<void> {
  await page.route('**/api/events*', (route) => route.abort('connectionrefused'))
}

async function unblockAndReconnect(page: Page): Promise<void> {
  await page.unroute('**/api/events*')
  await nudgeReconnect(page)
}

/** Opens the board in a new tab of the same browser (logged in) and returns what it shows after loading. */
async function freshCards(page: Page, omo: Omo) {
  const fresh = await page.context().newPage()
  try {
    await fresh.goto(omo.url('/'))
    await expectBoardLive(fresh)
    return await boardCards(fresh)
  } finally {
    await fresh.close()
  }
}

test('while the server is away the cards stay and the status shows reconnecting', async ({ page, omo }) => {
  const a = await omo.createTicket({ title: 'Stays visible' })
  await openBoard(page, omo)
  await expectCardIn(page, a, 'Planning')
  await omo.stop()
  await expect(page.locator('[data-connection]')).toHaveAttribute('data-state', /^(retrying|reconnecting)$/)
  await expect(page.locator('[data-connection]')).toContainText('reconnecting')
  await expectCardIn(page, a, 'Planning')
  await omo.start()
  await nudgeReconnect(page)
  await expect(page.locator('[data-connection]')).toHaveAttribute('data-state', 'live')
  await expectCardIn(page, a, 'Planning')
})

test('server restart: changes made while away appear once each', async ({ page, omo }) => {
  const keep = await omo.createTicket({ title: 'Kept' })
  const mover = await omo.createTicket({ title: 'Moves while away' })
  await openBoard(page, omo)
  await expectCardIn(page, mover, 'Planning')

  await blockStream(page)
  await omo.stop()
  await expect(page.locator('[data-connection]')).toHaveAttribute('data-state', /^(retrying|reconnecting)$/)
  await omo.start()

  // The page cannot reach the stream yet: everything below happens while it is disconnected.
  const created = await omo.ai(['ticket', 'create', '--type', 'task', '--title', 'Created while away', '--project', omo.project])
  expect(created.code, created.stdout + created.stderr).toBe(0)
  const fresh = Number(/^#(\d+) /.exec(created.stdout)?.[1])
  const moved = await omo.ai(['ticket', 'move', String(mover), 'ready'])
  expect(moved.code, moved.stdout + moved.stderr).toBe(0)
  const another = await omo.createTicket({ title: 'Also created while away' })
  await omo.moveTicket(another, 'ready')
  await expectCardIn(page, mover, 'Planning') // the page has not heard of the move

  await unblockAndReconnect(page)
  await expectCardIn(page, fresh, 'Planning')
  await expectCardIn(page, mover, 'Ready For Dev')
  await expectCardIn(page, another, 'Ready For Dev')
  await expectCardIn(page, keep, 'Planning')
  await expect(card(page, fresh)).toHaveCount(1)
  await expect(card(page, another)).toHaveCount(1)
  await expectBoardEqualsServer(page, omo)
  expect(await boardCards(page)).toEqual(await freshCards(page, omo))
})

test('more than 500 missed events: resync leaves the board equal to the server', async ({ page, omo }) => {
  test.setTimeout(120_000)
  const keep = await omo.createTicket({ title: 'Kept' })
  const mover = await omo.createTicket({ title: 'Moves while away' })
  await openBoard(page, omo)
  await expectCardIn(page, mover, 'Planning')

  await blockStream(page)
  await omo.stop()
  await omo.start()

  const created: number[] = []
  const queue = Array.from({ length: 510 }, (_, i) => i)
  const worker = async () => {
    for (let i = queue.shift(); i !== undefined; i = queue.shift()) created.push(await omo.createTicket({ title: `Bulk ${i}` }))
  }
  await Promise.all(Array.from({ length: 8 }, worker))
  expect(created).toHaveLength(510)
  await omo.moveTicket(mover, 'ready')
  await omo.moveTicket(created[0]!, 'ready')

  await unblockAndReconnect(page)
  await expectCardIn(page, mover, 'Ready For Dev')
  await expectCardIn(page, keep, 'Planning')
  await expectBoardEqualsServer(page, omo, 60_000)
  expect(await page.locator('li[data-ticket]').count()).toBe(512)
  expect(await boardCards(page)).toEqual(await freshCards(page, omo))
})
