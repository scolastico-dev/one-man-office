import AxeBuilder from '@axe-core/playwright'
import type { Page } from '@playwright/test'
import { readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { expectBoardLive } from './support/board'
import { expect, test, type Omo } from './support/omo'

const VALUE = 'sk-example-7f3a91c2d4e85b60'

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

/** Every response body the page receives with its path and status, to look for a value in. */
function recordBodies(page: Page): { bodies: { path: string; status: number; body: string }[]; settled(): Promise<void> } {
  const bodies: { path: string; status: number; body: string }[] = []
  const pending: Promise<void>[] = []
  page.on('response', (r) => {
    // The event stream never ends, so its body cannot be read; the events carry no values by design.
    if ((r.headers()['content-type'] ?? '').includes('text/event-stream')) return
    pending.push(
      r
        .text()
        .then((t) => void bodies.push({ path: new URL(r.url()).pathname, status: r.status(), body: t }))
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

test('a saved value is shown again after a reload; only the settings answers carry it', async ({ page, omo }) => {
  test.setTimeout(90_000)
  const seen = recordBodies(page)
  await open(page, omo)
  await addVariable(page, 'ANTHROPIC_API_KEY', VALUE)
  await addVariable(page, 'PAGER', 'cat')
  await save(page).click()
  await expect(page.locator('[data-save-state]')).toContainText('Saved')
  await expect(valueOf(page, 'ANTHROPIC_API_KEY')).toHaveValue(VALUE)
  expect(stored(omo)).toContain(VALUE)

  // Another change keeps it.
  await valueOf(page, 'PAGER').fill('less')
  await save(page).click()
  await expect(page.locator('[data-save-state]')).toContainText('Saved')
  expect(stored(omo)).toContain(VALUE)

  // A reload shows it again, and the check answers states only.
  await page.reload()
  await expect(valueOf(page, 'ANTHROPIC_API_KEY')).toHaveValue(VALUE)
  await page.locator('[data-env-check-run]').click()
  await expect(page.locator('[data-check="ANTHROPIC_API_KEY"]')).toHaveText('ANTHROPIC_API_KEY: set')

  await seen.settled()
  expect(seen.bodies.some((b) => b.path === '/api/settings/agent-env/check')).toBe(true)
  for (const b of seen.bodies) if (b.path !== '/api/settings' || b.status >= 400) expect(b.body).not.toContain(VALUE)

  // A changed value replaces the stored one.
  await valueOf(page, 'ANTHROPIC_API_KEY').fill('sk-second-example-11')
  await save(page).click()
  await expect(page.locator('[data-save-state]')).toContainText('Saved')
  expect(stored(omo)).not.toContain(VALUE)
  expect(stored(omo)).toContain('sk-second-example-11')
  await page.reload()
  await expect(valueOf(page, 'ANTHROPIC_API_KEY')).toHaveValue('sk-second-example-11')
})

test('a command that works checks as set, one that fails silently as failed with its exit status, and an edit clears the answer', async ({ page, omo }) => {
  await open(page, omo)
  await addVariable(page, 'GOOD_CMD', '`echo ok`')
  await addVariable(page, 'BAD_CMD', '`false`')
  await addVariable(page, 'PLAIN_EMPTY', '')
  const run = page.locator('[data-env-check-run]')
  await expect(run).toContainText('runs your commands')
  await run.click()
  const result = page.locator('[data-env-check-result]')
  await expect(result.locator('[data-check="GOOD_CMD"]')).toHaveText('GOOD_CMD: set')
  await expect(result.locator('[data-check="BAD_CMD"]')).toHaveText('BAD_CMD: failed, its command exited with status 1 and printed nothing')
  await expect(row(page, 'BAD_CMD')).toContainText('its command exited with status 1 and printed nothing')
  await expect(result.locator('[data-check="PLAIN_EMPTY"]')).toHaveText('PLAIN_EMPTY: empty')
  await expect(result).toContainText('1 of 8 variables failed')
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
  writeFileSync(settingsFile(omo), 'limits:\n  tasks: 7\nagent_env:\n  KEEP: one\n  FROM_DISK: yes\n')
  await expect.poll(() => omo.api<{ settings: { limits: { tasks: number } } }>('GET', '/api/settings').then((s) => s.settings.limits.tasks), { timeout: 15_000 }).toBe(7)
  await save(page).click()
  await expect(page.locator('[data-save-state]')).toContainText('Saved')
  expect(stored(omo)).toMatch(/limits:\n\s+tasks: 7\b/)
  expect(stored(omo)).toMatch(/KEEP: two/)

  // Edits on the page, a hand edit of the variables: the draft stays and the new value is offered.
  await valueOf(page, 'KEEP').fill('three')
  writeFileSync(settingsFile(omo), 'limits:\n  tasks: 7\nagent_env:\n  KEEP: edited-by-hand\n')
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
        await addVariable(page, 'API_KEY', VALUE)
        await addVariable(page, 'BAD_CMD', '`false`')
        await addVariable(page, 'GOOD_CMD', '`echo ok`')
        await page.locator('[data-env-check-run]').click()
        await expect(page.locator('[data-env-check-result]')).toBeVisible()
        expect(await violations(page)).toEqual([])
        await save(page).click()
        await expect(page.locator('[data-save-state]')).toContainText('Saved')
        await expect(valueOf(page, 'API_KEY')).toHaveValue(VALUE)
        expect(await violations(page)).toEqual([])
      })
    }
  })
}
