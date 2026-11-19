import { join } from 'node:path'
import { card, expectCardIn, openBoard } from './support/board'
import { dragCard } from './support/drag'
import { expect, test } from './support/omo'
import { makePlugin } from './support/plugin'

// A real plugin whose gate hook (Lua, on the release move) answers by the ticket's title: a title with
// "hold" is held with two choices, one with "refuse" is refused. The manual hooks behind the choices
// call omo.gate_release and omo.gate_cancel, as a plugin does.
const GATE = `
local title = event.data.ticket.title
if title:find("hold") then
  return {action = "hold", reason = "the base is behind",
          choices = {{label = "Pull now", action = "pull"}, {label = "Stop it", action = "stop"}}}
end
if title:find("refuse") then return {action = "refuse", reason = "the branch is dirty"} end
return {action = "go_ahead"}
`

test.beforeEach(async ({ omo }) => {
  const repo = makePlugin(join(omo.home, 'gate-source'), {
    name: 'pull-base',
    version: '1.0.0',
    description: 'Pulls the base first',
    hooks: [
      { event: 'gate', move: 'release', lua: 'gate.lua' },
      { event: 'manual', name: 'pull', description: 'Pull the base', scope: 'ticket', hidden: true, lua: 'pull.lua' },
      { event: 'manual', name: 'stop', description: 'Stop the move', scope: 'ticket', hidden: true, lua: 'stop.lua' },
    ],
  }, {
    'gate.lua': GATE,
    'pull.lua': 'assert(omo.gate_release(event.data.ticket.number))\nreturn "pulled the base"',
    'stop.lua': 'assert(omo.gate_cancel(event.data.ticket.number))\nreturn "stopped"',
  })
  await omo.api('POST', '/api/plugins', { name: 'pull-base', source: repo.source, acknowledge: 'community' })
})

const panel = (page: import('@playwright/test').Page) => page.locator('[data-ticket-panel]')

/** Releases the ticket the way a drag does: the plugin's gate answers asynchronously. */
async function release(page: import('@playwright/test').Page, n: number) {
  await dragCard(page, n, 'Ready For Dev')
}

test('a held ticket is marked on its card and shows its hold in the panel; a choice releases it', async ({ page, omo }) => {
  const n = await omo.createTicket({ title: 'hold this one' })
  await openBoard(page, omo)
  await release(page, n)
  await expect(card(page, n).locator('[data-badge]')).toContainText('held by pull-base')
  await expectCardIn(page, n, 'Planning')
  await expect(card(page, n).locator('[data-move-notice]')).toContainText('held by pull-base: the base is behind')

  await card(page, n).click()
  await expect(panel(page).locator('[data-gate-head]')).toHaveText('Held by a plugin: Planning to Ready For Dev')
  await expect(panel(page).locator('[data-gate-reason]')).toHaveText('the base is behind')
  await panel(page).locator('[data-gate-choice="pull"]').click()
  await expect(page.locator('[data-plugin-answer]')).toHaveText('pulled the base')
  await page.locator('[data-plugin-answer-close]').click()
  await expectCardIn(page, n, 'Ready For Dev')
  await expect(card(page, n).locator('[data-badge]')).not.toContainText('held')
  await expect(panel(page).locator('[data-gate]')).toHaveCount(0)
})

test('Continue anyway releases a held ticket for good', async ({ page, omo }) => {
  const n = await omo.createTicket({ title: 'hold and override' })
  await openBoard(page, omo)
  await release(page, n)
  await expect(card(page, n).locator('[data-badge]')).toContainText('held by pull-base')
  await card(page, n).click()
  await panel(page).locator('[data-action="gate-override"]').click()
  await page.locator('[data-gate-confirm]').click()
  await expectCardIn(page, n, 'Ready For Dev')
  await expect(card(page, n).locator('[data-badge]')).not.toContainText('held')
})

test('Cancel move leaves the ticket in Planning without its hold', async ({ page, omo }) => {
  const n = await omo.createTicket({ title: 'hold and cancel' })
  await openBoard(page, omo)
  await release(page, n)
  await expect(card(page, n).locator('[data-badge]')).toContainText('held by pull-base')
  await card(page, n).click()
  await expect(panel(page).locator('[data-action-explain="gate-cancel"]')).toHaveText(`The move to Ready For Dev is not made; #${n} stays in Planning.`)
  await panel(page).locator('[data-action="gate-cancel"]').click()
  await page.locator('[data-gate-confirm]').click()
  await expect(panel(page).locator('[data-gate]')).toHaveCount(0)
  await expect(card(page, n).locator('[data-badge]')).toHaveCount(0)
  await expectCardIn(page, n, 'Planning')
  expect((await omo.api<{ gate: unknown; column: string }>('GET', `/api/tickets/${n}`)).gate).toBeNull()
})

test('a refused move shows the plugin and its reason on the card and in the panel', async ({ page, omo }) => {
  const n = await omo.createTicket({ title: 'refuse this one' })
  await openBoard(page, omo)
  await release(page, n)
  await expect(card(page, n).locator('[data-move-notice]')).toContainText('pull-base refused the move: the branch is dirty')
  await card(page, n).click()
  await expect(panel(page).locator('[data-gate-refusal-reason]')).toHaveText('the branch is dirty')
  await expectCardIn(page, n, 'Planning')
})

test('the hold is still there after a reload', async ({ page, omo }) => {
  const n = await omo.createTicket({ title: 'hold across a reload' })
  await openBoard(page, omo)
  await release(page, n)
  await expect(card(page, n).locator('[data-badge]')).toContainText('held by pull-base')
  await page.reload()
  await expect(card(page, n).locator('[data-badge]')).toContainText('held by pull-base')
})
