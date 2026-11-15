import AxeBuilder from '@axe-core/playwright'
import { rmSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import type { Page } from '@playwright/test'
import { liveAgents, script, waitForSession } from './support/agents'
import { card, openBoard } from './support/board'
import { expect, test, type Omo } from './support/omo'

// Two models, `meter/a` and `meter/b`, each metered by one window (`a` and
// `b`) that a usage script reads from a file the test writes in its home. The
// developer role picks the model with the most room left.
test.use({
  viewport: { width: 1440, height: 900 },
  omoOptions: { agents: { developer: script('ready', 'hang') } },
})

const READINGS = 'fake-usage.json'
const SCRIPT = `
function usage(account)
  local text, err = omo.read_file("~/${READINGS}")
  if not text then
    error("no readings: " .. err, 0)
  end
  local data = omo.json_decode(text)
  local out = {}
  for _, w in ipairs(data.windows) do
    table.insert(out, { name = w.name, used = w.used, resets_at = omo.now() + (w.reset_in or 7200) })
  end
  return out
end
`

type Reading = Record<'a' | 'b', number>
/** Writes the readings; `resetIn` gives a window's reset in seconds from the run (default two hours ahead). */
const writeReadings = (omo: Omo, r: Reading, resetIn: Partial<Reading> = {}) =>
  writeFileSync(join(omo.home, READINGS), JSON.stringify({ windows: [{ name: 'a', used: r.a, reset_in: resetIn.a }, { name: 'b', used: r.b, reset_in: resetIn.b }] }))

/** A write with a header the harness's `api` does not send. */
async function put(omo: Omo, path: string, body: unknown, ifMatch?: string) {
  const res = await fetch(omo.base + path, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${omo.token}`, ...(ifMatch === undefined ? {} : { 'If-Match': ifMatch }) },
    body: JSON.stringify(body),
  })
  if (res.status >= 400) throw new Error(`PUT ${path}: ${res.status} ${await res.text()}`)
}

/** The server's refusal of a start-anyway for a ticket that does not wait for usage: status and body as it sends them. */
async function refusalOf(omo: Omo, n: number): Promise<{ status: number; body: string; message: string }> {
  const res = await fetch(`${omo.base}/api/tickets/${n}/start-anyway`, { method: 'POST', headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${omo.token}` }, body: '{}' })
  const body = await res.text()
  expect(res.status).toBeGreaterThanOrEqual(400)
  return { status: res.status, body, message: (JSON.parse(body) as { error: { message: string } }).error.message }
}

interface UsageNow {
  readings: { windows: { name: string; used_percent: number }[]; error: string }[]
  models: { model: string; state: string }[]
}
const usageNow = (omo: Omo) => omo.api<UsageNow>('GET', '/api/usage')

/** Waits until the server shows the readings (or the failed run) the test wrote. */
async function waitForState(omo: Omo, states: Record<string, string>) {
  await expect
    .poll(async () => Object.fromEntries((await usageNow(omo)).models.filter((m) => m.model in states).map((m) => [m.model, m.state])), { message: 'waiting for the usage state', timeout: 30_000 })
    .toEqual(states)
}

/** Sets the readings, asks the server to read them and waits for the models' states. */
async function setReadings(omo: Omo, r: Reading, states: Record<string, string>) {
  writeReadings(omo, r)
  await omo.api('POST', '/api/usage/refresh', {})
  await waitForState(omo, states)
}

test.beforeEach(async ({ omo }) => {
  writeReadings(omo, { a: 10, b: 70 })
  await put(omo, '/api/usage/scripts/fake', { text: SCRIPT }, '"0"')
  const fake = (await omo.api<{ profiles: { name: string; command: string; env: Record<string, string> }[] }>('GET', '/api/profiles')).profiles.find((p) => p.name === 'fake')!
  const model = (window: string) => ({ args: [], env: {}, usage_windows: [window], selectable: true, description: `metered by ${window}` })
  await put(omo, '/api/profiles/meter', {
    cli: 'custom',
    command: fake.command,
    args: [],
    env: fake.env,
    account_dir: '',
    usage_script: 'fake',
    prompt_delivery: { inject: false },
    models: { a: model('a'), b: model('b') },
  })
  await put(omo, '/api/roles/developer', { models: ['meter/a', 'meter/b'], assignment: 'smart' })
  await waitForState(omo, { 'meter/a': 'ok', 'meter/b': 'ok' })
})

const sidebar = (page: Page) => page.locator('[data-sidebar]')
const usage = (page: Page) => page.locator('[data-usage]')
const panel = (page: Page) => page.locator('dialog[data-ticket-panel]')

/** Opens the Agents tab of the sidebar (at 390 px: of the sheet) and returns the usage block. */
async function openUsage(page: Page, omo: Omo, narrow = false) {
  await openBoard(page, omo)
  let root = sidebar(page)
  if (narrow) {
    await page.getByRole('button', { name: 'Sidebar' }).click()
    root = page.getByRole('dialog', { name: 'Sidebar' })
    await root.getByRole('tab', { name: /^Agents/ }).click()
  } else {
    await root.getByRole('tab', { name: /^Agents/ }).click()
    await expect(root).toHaveAttribute('data-open', 'true')
  }
  await expect(usage(page)).toBeVisible()
  return root
}

const noSideways = (page: Page) => page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth && document.body.scrollWidth <= document.body.clientWidth)

async function violations(page: Page) {
  await page.evaluate(() => document.fonts.ready.then(() => undefined))
  const results = await new AxeBuilder({ page }).analyze()
  return results.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target.join(' ')) }))
}

test('the block shows each window as a meter with the thresholds that apply', async ({ page, omo }) => {
  await openUsage(page, omo)
  const account = usage(page).locator('[data-usage-account]')
  await expect(account).toContainText('meter')
  await expect(account).toContainText('updated')
  const a = usage(page).locator('[data-usage-window="a"]')
  const b = usage(page).locator('[data-usage-window="b"]')
  await expect(a.getByRole('meter')).toBeVisible()
  await expect(a).toContainText('10 %')
  await expect(b).toContainText('70 %')
  await expect(a).toContainText('waits at 90 %, stops at 98 %')
  await expect(a).toContainText('resets in')
  await expect(usage(page).locator('[data-usage-limited]')).toHaveCount(0)
  await expect(usage(page).getByRole('link', { name: 'Usage settings' })).toHaveAttribute('href', /\/settings\/usage/)
})

test('a model near its limit makes new tickets wait; Start anyway starts one on its own model; lowering the reading and Refresh clears the wait', async ({ page, omo }) => {
  const seen: string[] = []
  page.on('pageerror', (e) => seen.push(e.message))
  await openUsage(page, omo)

  await setReadings(omo, { a: 96, b: 96 }, { 'meter/a': 'soft', 'meter/b': 'soft' })
  await expect(usage(page).locator('[data-usage-limited]')).toContainText('meter/a')
  await expect(usage(page).locator('[data-usage-limited]')).toContainText('near its limit')
  await expect(usage(page).locator('[data-usage-window="a"]')).toContainText('near its limit')

  const open = await omo.createTicket({ title: 'Waits for any model' })
  const own = await omo.api<{ number: number }>('POST', '/api/tickets', { type: 'task', title: 'Wants model a', projects: [omo.project], model: 'meter/a' })
  await omo.moveTicket(open, 'ready')
  await omo.moveTicket(own.number, 'ready')
  await expect(card(page, open).locator('[data-badge]')).toContainText('waiting for usage')
  await expect(card(page, own.number).locator('[data-badge]')).toContainText('waiting for usage')
  expect(await liveAgents(omo)).toEqual([])

  // The ticket without a model of its own cannot be started anyway: it says why.
  await card(page, open).click()
  await expect(panel(page)).toBeVisible()
  await expect(panel(page).locator('[data-action="start-anyway"]')).toBeDisabled()
  await expect(panel(page).locator('[data-action-why="start-anyway"]')).toContainText('has no model of its own')
  await page.keyboard.press('Escape')
  await expect(panel(page)).toHaveCount(0)

  // The other asks first, then starts on its model.
  await card(page, own.number).click()
  await expect(panel(page).locator('[data-action="start-anyway"]')).toBeEnabled()
  await panel(page).locator('[data-action="start-anyway"]').click()
  const dialog = page.locator('[data-start-anyway-dialog]')
  await expect(dialog).toContainText('Starts it on meter/a although the model is near its usage limit.')
  expect((await liveAgents(omo)).length).toBe(0)
  await dialog.locator('[data-start-anyway-confirm]').click()
  const session = await waitForSession(omo, own.number, 'developer')
  expect(session.model).toBe('meter/a')
  await expect(panel(page).locator('[data-action="start-anyway"]')).toHaveCount(0)
  await page.keyboard.press('Escape')
  await expect(card(page, own.number).locator('[data-badge]')).toHaveCount(0)
  await expect(card(page, open).locator('[data-badge]')).toContainText('waiting for usage')

  // Lower the reading and press Refresh in the block: the wait ends by itself.
  writeReadings(omo, { a: 10, b: 10 })
  await usage(page).locator('[data-usage-refresh]').click()
  await waitForSession(omo, open, 'developer')
  await expect(card(page, open).locator('[data-badge]')).toHaveCount(0)
  await expect(usage(page).locator('[data-usage-limited]')).toHaveCount(0)
  await expect(usage(page).locator('[data-usage-window="a"]')).toContainText('10 %')
  expect(seen).toEqual([])
})

test("a refusal by the server is shown in the panel and announced, in the server's words", async ({ page, omo }) => {
  await openUsage(page, omo)
  await setReadings(omo, { a: 96, b: 96 }, { 'meter/a': 'soft', 'meter/b': 'soft' })
  const own = await omo.api<{ number: number }>('POST', '/api/tickets', { type: 'task', title: 'Wants model a', projects: [omo.project], model: 'meter/a' })
  await omo.moveTicket(own.number, 'ready')
  await expect(card(page, own.number).locator('[data-badge]')).toContainText('waiting for usage')
  // The page cannot make the real server refuse a ticket that still waits for
  // usage, so the request is answered with the server's own refusal of a
  // ticket that does not wait (a planning ticket), word for word.
  const planning = await omo.createTicket({ title: 'Still in planning' })
  const refusal = await refusalOf(omo, planning)
  await page.route(`**/api/tickets/${own.number}/start-anyway`, (route) => route.fulfill({ status: refusal.status, contentType: 'application/json', body: refusal.body }))
  await card(page, own.number).click()
  await panel(page).locator('[data-action="start-anyway"]').click()
  await page.locator('[data-start-anyway-confirm]').click()
  await expect(panel(page).locator('[data-action-error]')).toHaveText(refusal.message)
  await expect(page.locator('[role="alert"]', { hasText: refusal.message }).first()).toBeAttached()
  expect(await liveAgents(omo)).toEqual([])
  // The button stays: the ticket still waits.
  await expect(panel(page).locator('[data-action="start-anyway"]')).toBeEnabled()
})

/** A ticket on model meter/a that waits for usage, with its panel open. */
async function waitingOwnTicket(page: Page, omo: Omo): Promise<number> {
  await openUsage(page, omo)
  await setReadings(omo, { a: 96, b: 96 }, { 'meter/a': 'soft', 'meter/b': 'soft' })
  const own = await omo.api<{ number: number }>('POST', '/api/tickets', { type: 'task', title: 'Wants model a', projects: [omo.project], model: 'meter/a' })
  await omo.moveTicket(own.number, 'ready')
  await expect(card(page, own.number).locator('[data-badge]')).toContainText('waiting for usage')
  await card(page, own.number).click()
  await expect(panel(page)).toBeVisible()
  return own.number
}

test('a lock while Start anyway asks: the dialog steps aside, the lock screen works, and the question comes back after', async ({ page, omo }) => {
  await waitingOwnTicket(page, omo)
  await panel(page).locator('[data-action="start-anyway"]').click()
  const dialog = page.locator('[data-start-anyway-dialog]')
  await expect(dialog).toBeVisible()

  await omo.stop()
  expect((await omo.cli(['config', 'rotate-token'])).code).toBe(0)
  await omo.start()
  const locked = page.getByRole('heading', { name: 'omo is locked' })
  await expect(locked).toBeVisible()
  // Nothing of the board is in the way: the dialog is not shown and does not make the lock screen inert.
  await expect(dialog).toBeHidden()
  await page.getByRole('textbox', { name: 'Access address or token' }).fill(omo.accessUrl)
  await page.getByRole('button', { name: 'Unlock' }).click()
  await expect(locked).toHaveCount(0)

  // The question is still open, and still sends nothing until it is answered.
  await expect(dialog).toBeVisible()
  await dialog.locator('[data-start-anyway-cancel]').click()
  await expect(dialog).toHaveCount(0)
  expect(await liveAgents(omo)).toEqual([])
})

test('closing the panel while Start anyway asks takes the question with it', async ({ page, omo }) => {
  const n = await waitingOwnTicket(page, omo)
  await panel(page).locator('[data-action="start-anyway"]').click()
  const dialog = page.locator('[data-start-anyway-dialog]')
  await expect(dialog).toBeVisible()
  // The panel goes away under the dialog (Back leaves the ticket's address).
  await page.goBack()
  await expect(panel(page)).toHaveCount(0)
  await expect(dialog).toHaveCount(0)
  expect(await liveAgents(omo)).toEqual([])
  // The board is usable again: the ticket opens and still waits.
  await card(page, n).click()
  await expect(panel(page).locator('[data-action="start-anyway"]')).toBeEnabled()
})

test('a failing script shows its error and the models stay available', async ({ page, omo }) => {
  await openUsage(page, omo)
  await setReadings(omo, { a: 96, b: 96 }, { 'meter/a': 'soft', 'meter/b': 'soft' })
  const n = await omo.createTicket({ title: 'Waits, then the script breaks' })
  await omo.moveTicket(n, 'ready')
  await expect(card(page, n).locator('[data-badge]')).toContainText('waiting for usage')

  rmSync(join(omo.home, READINGS))
  await usage(page).locator('[data-usage-refresh]').click()
  const failed = usage(page).locator('[data-usage-script-error]')
  await expect(failed).toContainText('Script failed:')
  await expect(failed).toContainText('no readings: file not found')
  await expect(failed).toContainText('Its models count as available.')
  await expect(usage(page).locator('[data-usage-limited]')).toHaveCount(0)
  // The meters agree with the server: the last numbers no longer count.
  for (const w of ['a', 'b']) {
    const meter = usage(page).locator(`[data-usage-window="${w}"]`)
    await expect(meter.locator('[data-state]')).toHaveAttribute('data-state', 'unknown')
    await expect(meter.locator('[data-usage-value]')).toHaveText('?')
    await expect(meter).not.toContainText('near its limit')
  }
  // Unknown is available: the waiting ticket starts.
  await waitForSession(omo, n, 'developer')
  await expect(card(page, n).locator('[data-badge]')).toHaveCount(0)
})

test('a window whose reset lies in the past counts 0 %, as the server counts it', async ({ page, omo }) => {
  await openUsage(page, omo)
  writeReadings(omo, { a: 99, b: 96 }, { a: -60 })
  await omo.api('POST', '/api/usage/refresh', {})
  await waitForState(omo, { 'meter/a': 'ok', 'meter/b': 'soft' })
  const a = usage(page).locator('[data-usage-window="a"]')
  await expect(a.locator('[data-state]')).toHaveAttribute('data-state', 'ok')
  await expect(a.locator('[data-usage-value]')).toHaveText('0 %')
  await expect(a.locator('[data-usage-reset]')).toHaveCount(0)
  await expect(a.getByRole('meter')).toHaveAttribute('aria-valuenow', '0')
  const b = usage(page).locator('[data-usage-window="b"]')
  await expect(b.locator('[data-state]')).toHaveAttribute('data-state', 'soft')
  await expect(b).toContainText('96 %')
  await expect(usage(page).locator('[data-usage-limited]')).not.toContainText('meter/a')
})

test('at 390 px the usage block is usable without sideways scrolling and passes axe in both themes', async ({ page, omo }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  const root = await openUsage(page, omo, true)
  await setReadings(omo, { a: 96, b: 99 }, { 'meter/a': 'soft', 'meter/b': 'hard' })
  await expect(root.locator('[data-usage-limited]')).toContainText('at its limit')
  expect(await noSideways(page)).toBe(true)
  for (const colorScheme of ['dark', 'light'] as const) {
    await page.emulateMedia({ colorScheme })
    expect(await violations(page), colorScheme).toEqual([])
  }
  const refresh = root.locator('[data-usage-refresh]')
  await refresh.click()
  await expect(refresh).toHaveText('Refresh')
  expect(await noSideways(page)).toBe(true)
})

test('at 1440 px the block passes axe in both themes with meters, limits and an error', async ({ page, omo }) => {
  await openUsage(page, omo)
  await setReadings(omo, { a: 96, b: 99 }, { 'meter/a': 'soft', 'meter/b': 'hard' })
  await expect(usage(page).locator('[data-usage-limited]')).toContainText('at its limit')
  for (const colorScheme of ['dark', 'light'] as const) {
    await page.emulateMedia({ colorScheme })
    expect(await violations(page), colorScheme).toEqual([])
  }
  rmSync(join(omo.home, READINGS))
  await usage(page).locator('[data-usage-refresh]').click()
  await expect(usage(page).locator('[data-usage-script-error]')).toBeVisible()
  for (const colorScheme of ['dark', 'light'] as const) {
    await page.emulateMedia({ colorScheme })
    expect(await violations(page), colorScheme).toEqual([])
  }
})
