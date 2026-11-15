import { expect, test } from './support/omo'
import { card, column, expectBoardEqualsServer, expectBoardLive, expectCardIn, expectCardTitle, markPage, expectNotReloaded, openBoard } from './support/board'

test('a ticket created through the API appears without a reload, once', async ({ page, omo }) => {
  await openBoard(page, omo)
  await markPage(page)
  const n = await omo.createTicket({ title: 'From the API' })
  await expectCardIn(page, n, 'Planning')
  await expectCardTitle(page, n, 'From the API')
  // Events arrive in order: once a later ticket is shown, the first has been applied for good.
  const later = await omo.createTicket({ title: 'Later' })
  await expectCardIn(page, later, 'Planning')
  await expect(card(page, n)).toHaveCount(1)
  await expectNotReloaded(page)
  await expectBoardEqualsServer(page, omo)
})

test('a ticket created in a second tab appears without a reload', async ({ page, omo, context }) => {
  await openBoard(page, omo)
  await markPage(page)
  const second = await context.newPage()
  await second.goto(omo.url('/'))
  await expectBoardLive(second)
  const n = await second.evaluate(async () => {
    const res = await fetch('/api/tickets', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${localStorage.getItem('omo.token')}` },
      body: JSON.stringify({ type: 'story', title: 'From the second tab' }),
    })
    return ((await res.json()) as { number: number }).number
  })
  await expectCardIn(page, n, 'Planning')
  await expectCardIn(second, n, 'Planning')
  await expectNotReloaded(page)
})

test('a move made through the API moves the card', async ({ page, omo }) => {
  const n = await omo.createTicket({ title: 'Mover' })
  const other = await omo.createTicket({ title: 'Stays' })
  await openBoard(page, omo)
  await expectCardIn(page, n, 'Planning')
  await markPage(page)
  await omo.moveTicket(n, 'ready')
  await expectCardIn(page, n, 'Ready For Dev')
  await expectCardIn(page, other, 'Planning')
  await expect(column(page, 'Planning').locator(`li[data-ticket="${n}"]`)).toHaveCount(0)
  await omo.moveTicket(n, 'planning')
  await expectCardIn(page, n, 'Planning')
  await expectNotReloaded(page)
  await expectBoardEqualsServer(page, omo)
})

test('omo ai ticket create and ticket move update the board live', async ({ page, omo }) => {
  await openBoard(page, omo)
  await markPage(page)
  const created = await omo.ai(['ticket', 'create', '--type', 'task', '--title', 'By the agent CLI', '--project', omo.project])
  expect(created.code, created.stdout + created.stderr).toBe(0)
  const n = Number(/^#(\d+) /.exec(created.stdout)?.[1])
  expect(n).toBeGreaterThan(0)
  await expectCardIn(page, n, 'Planning')
  await expectCardTitle(page, n, 'By the agent CLI')

  const moved = await omo.ai(['ticket', 'move', String(n), 'ready'])
  expect(moved.code, moved.stdout + moved.stderr).toBe(0)
  await expectCardIn(page, n, 'Ready For Dev')
  await expect(card(page, n)).toHaveCount(1)
  await expectNotReloaded(page)
  await expectBoardEqualsServer(page, omo)
})

test('two browser contexts show the same board after changes made in either', async ({ browser, omo }) => {
  const a = await browser.newContext({ reducedMotion: 'reduce' })
  const b = await browser.newContext({ reducedMotion: 'reduce' })
  try {
    const pa = await a.newPage()
    const pb = await b.newPage()
    await openBoard(pa, omo)
    await openBoard(pb, omo)

    const first = await omo.createTicket({ title: 'First' })
    await expectCardIn(pa, first, 'Planning')
    await expectCardIn(pb, first, 'Planning')

    // A change made from the browser of context A reaches context B.
    const second = await pa.evaluate(async () => {
      const res = await fetch('/api/tickets', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${localStorage.getItem('omo.token')}` },
        body: JSON.stringify({ type: 'story', title: 'Second' }),
      })
      return ((await res.json()) as { number: number }).number
    })
    await expectCardIn(pb, second, 'Planning')

    // And a change made elsewhere reaches both.
    await omo.moveTicket(first, 'ready')
    await expectCardIn(pa, first, 'Ready For Dev')
    await expectCardIn(pb, first, 'Ready For Dev')

    await expectBoardEqualsServer(pa, omo)
    await expectBoardEqualsServer(pb, omo)
  } finally {
    await a.close()
    await b.close()
  }
})
