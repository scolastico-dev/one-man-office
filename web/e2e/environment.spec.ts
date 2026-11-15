import AxeBuilder from '@axe-core/playwright'
import type { Page } from '@playwright/test'
import { readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { expectBoardLive } from './support/board'
import { expect, test, type Omo } from './support/omo'

const CANARY = 'sk-canary-7f3a91c2d4e85b60'

const row = (page: Page, name: string) => page.locator(`[data-env-row="${name}"]`)
const valueOf = (page: Page, name: string) => row(page, name).locator('[data-part="value"]')
const save = (page: Page) => page.getByRole('button', { name: 'Save', exact: true })
const settingsFile = (omo: Omo) => join(omo.home, 'omo', 'settings.yaml')
const stored = (omo: Omo) => readFileSync(settingsFile(omo), 'utf8')

async function open(page: Page, omo: Omo) {
  await page.goto(omo.accessUrl)
  await expectBoardLive(page)
  await page.goto(omo.url('/settings/environment'))
  await expect(page.locator('[data-settings-page] h1')).toHaveText('Environment')
  await expect(page.locator('[data-env-row]').first()).toBeVisible()
}

/** Adds a row at the end of the table and types its name and value. */
async function addVariable(page: Page, name: string, value: string) {
  const n = await page.locator('[data-env-row]').count()
  await page.locator('[data-part="add"]').click()
  await page.getByLabel(`Name of variable ${n + 1}`).fill(name)
  await valueOf(page, name).fill(value)
}

/** Every response body the page receives, to look for a secret in. */
function recordBodies(page: Page): { bodies: string[]; settled(): Promise<void> } {
  const bodies: string[] = []
  const pending: Promise<void>[] = []
  page.on('response', (r) => {
    // The event stream never ends, so its body cannot be read; the events carry no values by design.
    if ((r.headers()['content-type'] ?? '').includes('text/event-stream')) return
    pending.push(
      r
        .text()
        .then((t) => void bodies.push(t))
        .catch(() => undefined),
    )
  })
  return { bodies, settled: async () => void (await Promise.all(pending)) }
}

async function violations(page: Page) {
  await page.evaluate(() => document.fonts.ready.then(() => undefined))
  const results = await new AxeBuilder({ page }).analyze()
  return results.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target.join(' ')) }))
}

async function pageScrollsSideways(page: Page): Promise<boolean> {
  return page.evaluate(() => {
    const root = document.documentElement
    return root.scrollWidth > root.clientWidth || document.body.scrollWidth > document.body.clientWidth
  })
}

test('a variable can be added, changed and removed', async ({ page, omo }) => {
  await open(page, omo)
  await addVariable(page, 'PAGER', 'cat')
  await save(page).click()
  await expect(page.locator('[data-save-state]')).toContainText('Saved')
  expect(stored(omo)).toMatch(/PAGER: cat/)

  await valueOf(page, 'PAGER').fill('less -R')
  await save(page).click()
  await expect(page.locator('[data-save-state]')).toContainText('Saved')
  expect(stored(omo)).toMatch(/PAGER: less -R/)

  await page.getByRole('button', { name: 'Remove PAGER' }).click()
  await expect(row(page, 'PAGER')).toHaveCount(0)
  await save(page).click()
  await expect(page.locator('[data-save-state]')).toContainText('Saved')
  expect(stored(omo)).not.toContain('PAGER')
})

test('restore asks first and changes only the form', async ({ page, omo }) => {
  await open(page, omo)
  await addVariable(page, 'MINE', '1')
  await save(page).click()
  await expect(page.locator('[data-save-state]')).toContainText('Saved')
  await page.getByRole('button', { name: 'Restore shipped defaults…' }).click()
  const question = page.getByRole('dialog', { name: 'Restore the shipped defaults?' })
  await expect(question).toBeVisible()
  await question.getByRole('button', { name: 'Keep my variables' }).click()
  await expect(row(page, 'MINE')).toHaveCount(1)
  await page.getByRole('button', { name: 'Restore shipped defaults…' }).click()
  await question.getByRole('button', { name: 'Restore' }).click()
  await expect(row(page, 'MINE')).toHaveCount(0)
  await expect(row(page, 'GIT_AUTHOR_NAME')).toHaveCount(1)
  await expect(page.locator('[data-save-state]')).toContainText('Unsaved')
  expect(stored(omo)).toMatch(/MINE: "?1"?/)
})

test('a secret is hidden after saving and never in a response body or the page', async ({ page, omo }) => {
  test.setTimeout(90_000)
  const seen = recordBodies(page)
  await open(page, omo)
  await addVariable(page, 'ANTHROPIC_API_KEY', CANARY)
  await addVariable(page, 'PAGER', 'cat')
  await save(page).click()
  await expect(page.locator('[data-save-state]')).toContainText('Saved')
  await expect(row(page, 'ANTHROPIC_API_KEY').locator('[data-hidden-value]')).toHaveText('hidden')
  expect(stored(omo)).toContain(CANARY)

  // Another change sends the placeholder back; the stored secret stays.
  await valueOf(page, 'PAGER').fill('less')
  await save(page).click()
  await expect(page.locator('[data-save-state]')).toContainText('Saved')
  expect(stored(omo)).toContain(CANARY)

  // A reload shows it hidden too, and the check answers states only.
  await page.reload()
  await expect(row(page, 'ANTHROPIC_API_KEY').locator('[data-hidden-value]')).toBeVisible()
  await page.locator('[data-env-check-run]').click()
  await expect(page.locator('[data-check="ANTHROPIC_API_KEY"]')).toHaveText('ANTHROPIC_API_KEY: set')

  await seen.settled()
  expect(seen.bodies.length).toBeGreaterThan(3)
  for (const body of seen.bodies) expect(body).not.toContain(CANARY)
  expect(await page.content()).not.toContain(CANARY)
  expect(await page.locator('body').innerText()).not.toContain(CANARY)
  expect(await page.locator('input').evaluateAll((els) => els.map((e) => (e as HTMLInputElement).value))).not.toContain(CANARY)

  // Replace it: the new literal is stored, the old one is gone.
  await row(page, 'ANTHROPIC_API_KEY').getByRole('button', { name: /Replace the value of/ }).click()
  await valueOf(page, 'ANTHROPIC_API_KEY').fill('sk-second-canary-11')
  await save(page).click()
  await expect(page.locator('[data-save-state]')).toContainText('Saved')
  expect(stored(omo)).not.toContain(CANARY)
  expect(stored(omo)).toContain('sk-second-canary-11')
  await page.reload()
  await seen.settled()
  for (const body of seen.bodies) expect(body).not.toContain('sk-second-canary-11')
})

test('a hidden value without a stored secret is refused under its row', async ({ page, omo }) => {
  await open(page, omo)
  await addVariable(page, 'NEW_SECRET_KEY', 'omo:hidden')
  await save(page).click()
  await expect(row(page, 'NEW_SECRET_KEY')).toContainText('Enter the value of NEW_SECRET_KEY again.')
  await page.locator('[data-env-check-run]').click()
  await expect(row(page, 'NEW_SECRET_KEY')).toContainText('Enter the value of NEW_SECRET_KEY again.')
})

test('a command that works checks as set, one that prints nothing as empty, and an edit clears the answer', async ({ page, omo }) => {
  await open(page, omo)
  await addVariable(page, 'GOOD_CMD', '`echo ok`')
  await addVariable(page, 'BAD_CMD', '`false`')
  await addVariable(page, 'PLAIN_EMPTY', '')
  const run = page.locator('[data-env-check-run]')
  await expect(run).toContainText('runs your commands')
  await run.click()
  const result = page.locator('[data-env-check-result]')
  await expect(result.locator('[data-check="GOOD_CMD"]')).toHaveText('GOOD_CMD: set')
  await expect(result.locator('[data-check="BAD_CMD"]')).toHaveText('BAD_CMD: empty')
  await expect(result.locator('[data-check="PLAIN_EMPTY"]')).toHaveText('PLAIN_EMPTY: empty')
  await expect(result).toContainText('2 of 8 variables empty')
  await valueOf(page, 'BAD_CMD').fill('`true`')
  await expect(result.locator('[data-check="BAD_CMD"]')).toHaveCount(0)
  await expect(result.locator('[data-check="GOOD_CMD"]')).toHaveCount(1)
  // Nothing was saved by checking.
  expect(stored(omo)).not.toContain('GOOD_CMD')
})

test('a command that does not finish checks as failed, in words, under its row', async ({ page, omo }) => {
  test.setTimeout(60_000)
  await open(page, omo)
  await addVariable(page, 'SLOW_CMD', '`sleep 30`')
  await page.locator('[data-env-check-run]').click()
  await expect(page.locator('[data-env-check-run]')).toContainText('Checking')
  const result = page.locator('[data-env-check-result]')
  await expect(result.locator('[data-check="SLOW_CMD"]')).toContainText('SLOW_CMD: failed, its command did not finish within 10s', { timeout: 25_000 })
  await expect(result).toContainText('1 of 6 variables failed')
  await expect(row(page, 'SLOW_CMD')).toContainText('its command did not finish within 10s')
  await expect(valueOf(page, 'SLOW_CMD')).toHaveAttribute('aria-invalid', 'true')
})

test('a hand edit of settings.yaml while the page is open is shown', async ({ page, omo }) => {
  await open(page, omo)
  await addVariable(page, 'KEEP', 'one')
  await save(page).click()
  await expect(page.locator('[data-save-state]')).toContainText('Saved')

  // No edits on the page: a hand edit of the variables shows at once, marked as made on disk.
  writeFileSync(settingsFile(omo), 'agent_env:\n  KEEP: one\n  FROM_DISK: yes\n')
  await expect(row(page, 'FROM_DISK')).toHaveCount(1, { timeout: 15_000 })
  await expect(page.locator('[data-changed-on-disk]')).toContainText('Changed on disk')

  // Edits on the page, a hand edit of another key only: Save goes through and keeps it.
  await valueOf(page, 'KEEP').fill('two')
  writeFileSync(settingsFile(omo), 'max_running_agents: 7\nagent_env:\n  KEEP: one\n  FROM_DISK: yes\n')
  await expect.poll(() => omo.api<{ settings: { max_running_agents: number } }>('GET', '/api/settings').then((s) => s.settings.max_running_agents), { timeout: 15_000 }).toBe(7)
  await save(page).click()
  await expect(page.locator('[data-save-state]')).toContainText('Saved')
  expect(stored(omo)).toMatch(/max_running_agents: 7/)
  expect(stored(omo)).toMatch(/KEEP: two/)

  // Edits on the page, a hand edit of the variables: the draft stays and the new value is offered.
  await valueOf(page, 'KEEP').fill('three')
  writeFileSync(settingsFile(omo), 'max_running_agents: 7\nagent_env:\n  KEEP: edited-by-hand\n')
  await expect(page.locator('[data-use-theirs]')).toBeVisible({ timeout: 15_000 })
  await expect(valueOf(page, 'KEEP')).toHaveValue('three')
  await page.locator('[data-use-theirs]').click()
  await expect(valueOf(page, 'KEEP')).toHaveValue('edited-by-hand')
  await expect(row(page, 'FROM_DISK')).toHaveCount(0)
})

test.describe('at 390 px', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('the page is usable without sideways scrolling', async ({ page, omo }) => {
    await open(page, omo)
    await addVariable(page, 'SOME_VERY_LONG_VARIABLE_NAME_FOR_A_NARROW_SCREEN', '`pass show a/very/long/path/that/goes/on/and/on/and/on`')
    await page.locator('[data-env-check-run]').click()
    await expect(page.locator('[data-env-check-result]')).toBeVisible()
    expect(await pageScrollsSideways(page)).toBe(false)
    await save(page).click()
    await expect(page.locator('[data-save-state]')).toContainText('Saved')
    expect(await pageScrollsSideways(page)).toBe(false)
  })
})

for (const scheme of ['dark', 'light'] as const) {
  test.describe(`${scheme} theme`, () => {
    test.use({ colorScheme: scheme })

    for (const width of [1440, 390]) {
      test(`has no accessibility violations at ${width} px`, async ({ page, omo }) => {
        await page.setViewportSize({ width, height: 900 })
        await open(page, omo)
        expect(await violations(page)).toEqual([])
        await addVariable(page, 'API_KEY', CANARY)
        await addVariable(page, 'BAD_CMD', '`false`')
        await addVariable(page, 'GOOD_CMD', '`echo ok`')
        await page.locator('[data-env-check-run]').click()
        await expect(page.locator('[data-env-check-result]')).toBeVisible()
        expect(await violations(page)).toEqual([])
        await save(page).click()
        await expect(page.locator('[data-save-state]')).toContainText('Saved')
        await expect(row(page, 'API_KEY').locator('[data-hidden-value]')).toBeVisible()
        expect(await violations(page)).toEqual([])
      })
    }
  })
}
