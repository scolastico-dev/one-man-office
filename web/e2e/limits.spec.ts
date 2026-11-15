import { readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import AxeBuilder from '@axe-core/playwright'
import type { Page } from '@playwright/test'
import { expectBoardLive, openBoard } from './support/board'
import { expect, test, type Omo } from './support/omo'

const settingsFile = (omo: Omo) => join(omo.home, 'omo', 'settings.yaml')
const onDisk = (omo: Omo) => readFileSync(settingsFile(omo), 'utf8')
const title = (page: Page) => page.locator('[data-settings-page] h1')
const bar = (page: Page) => page.getByRole('region', { name: 'Save changes' })
const saveButton = (page: Page) => bar(page).getByRole('button', { name: 'Save', exact: true })
const maxAgents = (page: Page) => page.getByRole('spinbutton', { name: 'Max running agents' })
const silentAfter = (page: Page) => page.getByRole('textbox', { name: 'Silent after' })
const handoff = (page: Page) => page.getByRole('textbox', { name: 'Handoff time limit at stop' })
const question = (page: Page) => page.getByRole('dialog', { name: 'Leave without saving?' })

async function openLimits(page: Page, omo: Omo) {
  await openBoard(page, omo)
  await page.goto(omo.url('/settings/limits'))
  await expect(title(page)).toHaveText('Limits')
  await expect(maxAgents(page)).toBeVisible()
}

async function violations(page: Page) {
  // Colour transitions of a button that just became enabled would be measured half way.
  await page.evaluate(async () => {
    await document.fonts.ready
    await Promise.all(document.getAnimations().map((a) => a.finished.catch(() => undefined)))
  })
  const results = await new AxeBuilder({ page }).analyze()
  return results.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target.join(' ')) }))
}

async function pageScrollsSideways(page: Page): Promise<boolean> {
  return page.evaluate(() => {
    const root = document.documentElement
    return root.scrollWidth > root.clientWidth || document.body.scrollWidth > document.body.clientWidth
  })
}

test('max running agents 1 saves, survives a reload and the Agents tab shows "of 1"', async ({ page, omo }) => {
  await openLimits(page, omo)
  await expect(maxAgents(page)).toHaveValue('4')
  await expect(bar(page)).toContainText('No unsaved changes')
  await maxAgents(page).fill('1')
  await expect(bar(page)).toContainText('Unsaved changes')
  await saveButton(page).click()
  await expect(bar(page)).toContainText('Saved')
  expect(onDisk(omo)).toMatch(/max_running_agents: 1\b/)

  await page.reload()
  await expect(title(page)).toHaveText('Limits')
  await expect(maxAgents(page)).toHaveValue('1')

  await page.getByRole('link', { name: 'Back to board' }).click()
  await expectBoardLive(page)
  await page
    .locator('[data-sidebar]')
    .getByRole('tab', { name: /^Agents/ })
    .click()
  await expect(page.locator('[data-sidebar] [data-slots]')).toContainText('Slots: 0 of 1 in use')
})

test('a value out of bounds is refused under its field, with the server sentence; a fix saves', async ({ page, omo }) => {
  await openLimits(page, omo)
  await maxAgents(page).fill('0')
  await saveButton(page).click()
  const sentence = 'max_running_agents must be between 1 and 64'
  await expect(page.locator('.omo-field__error').filter({ hasText: sentence })).toBeVisible()
  await expect(maxAgents(page)).toHaveAttribute('aria-invalid', 'true')
  await expect(maxAgents(page)).toBeFocused()
  await expect(bar(page)).toContainText('Unsaved changes')
  // Linked to its field for screen readers.
  const described = await maxAgents(page).getAttribute('aria-describedby')
  expect(described).toBeTruthy()
  await expect(page.locator(`[id="${described!.split(' ').at(-1)}"]`)).toContainText(sentence)
  expect(onDisk(omo)).not.toMatch(/max_running_agents: 0/)

  await maxAgents(page).fill('64')
  await saveButton(page).click()
  await expect(bar(page)).toContainText('Saved')
  await expect(page.locator('.omo-field__error')).toHaveCount(0)
  expect(onDisk(omo)).toMatch(/max_running_agents: 64\b/)
})

test('a refused duration shows the server message under its field', async ({ page, omo }) => {
  await openLimits(page, omo)
  await silentAfter(page).fill('1s')
  await saveButton(page).click()
  const field = page.locator('.omo-field').filter({ has: silentAfter(page) })
  await expect(field.locator('.omo-field__error')).toContainText('health.silent_after must be between 1m')
  await expect(silentAfter(page)).toBeFocused()
  // A text that is no duration at all is refused by the server too; it names the group, not the field, so the sentence shows above the form.
  await silentAfter(page).fill('15m')
  await handoff(page).fill('soon')
  await saveButton(page).click()
  await expect(page.locator('[data-form-errors]')).toContainText('write durations as Go duration text such as 15m or 1h30m')
  await handoff(page).fill('3m')
  await saveButton(page).click()
  await expect(bar(page)).toContainText('Saved')
  expect(onDisk(omo)).toMatch(/handoff_timeout: 3m/)
})

test('Save is blocked while a number field holds no number', async ({ page, omo }) => {
  const patches: string[] = []
  page.on('request', (r) => {
    if (r.method() === 'PATCH') patches.push(r.url())
  })
  await openLimits(page, omo)
  await maxAgents(page).fill('abc')
  await expect(page.locator('.omo-field__error').filter({ hasText: 'Enter a whole number' })).toBeVisible()
  await silentAfter(page).fill('20m')
  await saveButton(page).click()
  await expect(bar(page)).toContainText('Fix the fields marked above before saving.')
  await expect(maxAgents(page)).toBeFocused()
  expect(patches).toEqual([])
  await maxAgents(page).fill('5')
  await expect(page.locator('.omo-field__error')).toHaveCount(0)
  await saveButton(page).click()
  await expect(bar(page)).toContainText('Saved')
  expect(onDisk(omo)).toMatch(/max_running_agents: 5\b/)
  expect(patches).toHaveLength(1)
})

test.describe('a hand edit of settings.yaml while the page is open', () => {
  test('is shown at once when the page is clean', async ({ page, omo }) => {
    await openLimits(page, omo)
    writeFileSync(settingsFile(omo), 'max_running_agents: 7\n')
    await expect(maxAgents(page)).toHaveValue('7')
    await expect(page.locator('[data-changed-on-disk]')).toContainText('Changed on disk at')
    await expect(bar(page)).toContainText('No unsaved changes')
  })

  test('on the same key is kept as a draft; "Use the new value" takes it', async ({ page, omo }) => {
    await openLimits(page, omo)
    await maxAgents(page).fill('2')
    writeFileSync(settingsFile(omo), 'max_running_agents: 9\n')
    await expect(bar(page)).toContainText('Changed outside this page')
    await expect(maxAgents(page)).toHaveValue('2')
    // Save asks for a choice first and sends nothing.
    await saveButton(page).click()
    await expect(bar(page)).toContainText('Choose “Use the new value” or “Keep mine” first.')
    expect(onDisk(omo)).toMatch(/max_running_agents: 9\b/)

    await bar(page).getByRole('button', { name: 'Use the new value' }).click()
    await expect(maxAgents(page)).toHaveValue('9')
    await expect(bar(page)).toContainText('No unsaved changes')
  })

  test('on the same key: "Keep mine" and Save overwrite it', async ({ page, omo }) => {
    await openLimits(page, omo)
    await maxAgents(page).fill('2')
    writeFileSync(settingsFile(omo), 'max_running_agents: 11\n')
    await expect(bar(page)).toContainText('Changed outside this page')
    await bar(page).getByRole('button', { name: 'Keep mine' }).click()
    await expect(maxAgents(page)).toHaveValue('2')
    await expect(bar(page)).toContainText('Unsaved changes')
    await saveButton(page).click()
    await expect(bar(page)).toContainText('Saved')
    expect(onDisk(omo)).toMatch(/max_running_agents: 2\b/)
  })

  test('on another key is no conflict: Save keeps the hand edit', async ({ page, omo }) => {
    await openLimits(page, omo)
    await silentAfter(page).fill('20m')
    writeFileSync(settingsFile(omo), 'review_defaults:\n  task: human\n')
    // The hand edit is noticed (the disk note shows) but nothing asks for a choice.
    await expect(page.locator('[data-changed-on-disk]')).toBeVisible()
    await expect(bar(page)).toContainText('Unsaved changes')
    await expect(bar(page).getByRole('button', { name: 'Keep mine' })).toHaveCount(0)
    await saveButton(page).click()
    await expect(bar(page)).toContainText('Saved')
    const text = onDisk(omo)
    expect(text).toMatch(/silent_after: 20m/)
    expect(text).toMatch(/task: human/)
  })

  test('written between the poll and the Save is caught by the revision, not lost', async ({ page, omo }) => {
    await openLimits(page, omo)
    await silentAfter(page).fill('25m')
    // No wait for the poll: the page still holds the old revision when Save is pressed.
    writeFileSync(settingsFile(omo), 'review_defaults:\n  story: agent\n')
    await saveButton(page).click()
    await expect(bar(page)).toContainText(/Saved|Changed outside this page/)
    const text = onDisk(omo)
    expect(text).toMatch(/story: agent/)
    expect(text).toMatch(/silent_after: 25m/)
  })
})

test.describe('leaving a dirty page', () => {
  test('asks before the Back to board link, before another section, and stays when declined', async ({ page, omo }) => {
    await openLimits(page, omo)
    // A clean page leaves without a question.
    await page
      .getByRole('navigation', { name: 'Settings' })
      .getByRole('link', { name: /^Review/ })
      .click()
    await expect(title(page)).toHaveText('Review')
    await page
      .getByRole('navigation', { name: 'Settings' })
      .getByRole('link', { name: /^Limits/ })
      .click()
    await expect(maxAgents(page)).toBeVisible()

    await maxAgents(page).fill('3')
    await page.getByRole('link', { name: 'Back to board' }).click()
    await expect(question(page)).toBeVisible()
    await expect(question(page)).toContainText('Your changes are lost.')
    await question(page).getByRole('button', { name: 'Stay' }).click()
    await expect(question(page)).toHaveCount(0)
    await expect(page).toHaveURL(/\/settings\/limits/)
    await expect(maxAgents(page)).toHaveValue('3')

    // Another section asks too; Escape declines.
    await page
      .getByRole('navigation', { name: 'Settings' })
      .getByRole('link', { name: /^General/ })
      .click()
    await expect(question(page)).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(question(page)).toHaveCount(0)
    await expect(page).toHaveURL(/\/settings\/limits/)

    // The browser's own history is asked as well.
    await page.goBack()
    await expect(question(page)).toBeVisible()
    await question(page).getByRole('button', { name: 'Leave' }).click()
    // Back goes to the section visited before this one.
    await expect(title(page)).toHaveText('Review')
    expect(onDisk(omo)).not.toMatch(/max_running_agents: 3/)
  })

  test('Leave goes to the board and drops the edit; a saved page leaves freely', async ({ page, omo }) => {
    await openLimits(page, omo)
    await maxAgents(page).fill('3')
    await page.getByRole('link', { name: 'Back to board' }).click()
    await question(page).getByRole('button', { name: 'Leave' }).click()
    await expectBoardLive(page)
    expect(onDisk(omo)).not.toMatch(/max_running_agents: 3/)

    await page.goto(omo.url('/settings/limits'))
    await maxAgents(page).fill('3')
    await saveButton(page).click()
    await expect(bar(page)).toContainText('Saved')
    await page.getByRole('link', { name: 'Back to board' }).click()
    await expectBoardLive(page)
  })
})

test('a file that cannot be read is shown; saving over it asks "Save anyway?"', async ({ page, omo }) => {
  await openLimits(page, omo)
  const broken = 'max_running_agents: [\n'
  writeFileSync(settingsFile(omo), broken)
  await expect(page.locator('[data-file-error]')).toContainText('settings.yaml')
  await expect(page.locator('[data-file-error]')).toContainText('Cannot be read')
  await expect(page.getByText('omo uses the last version it could read.')).toBeVisible()
  // The page keeps showing the last version omo could read.
  await expect(maxAgents(page)).toHaveValue('4')

  await maxAgents(page).fill('6')
  await saveButton(page).click()
  const ask = page.getByRole('dialog', { name: 'Save anyway?' })
  await expect(ask).toBeVisible()
  await expect(ask).toContainText('Saving replaces the file on disk, including the edit that cannot be read.')
  await ask.getByRole('button', { name: 'Cancel' }).click()
  await expect(ask).toHaveCount(0)
  expect(onDisk(omo)).toBe(broken)
  await expect(bar(page)).toContainText('Unsaved changes')

  await saveButton(page).click()
  await page.getByRole('dialog', { name: 'Save anyway?' }).getByRole('button', { name: 'Save anyway' }).click()
  await expect(bar(page)).toContainText('Saved')
  expect(onDisk(omo)).toMatch(/max_running_agents: 6\b/)
  expect(onDisk(omo)).not.toContain('max_running_agents: [')
  await expect(page.locator('[data-file-error]')).toHaveCount(0)
})

test('a file broken by hand right before Save is not written over unasked', async ({ page, omo }) => {
  await openLimits(page, omo)
  await maxAgents(page).fill('6')
  // No wait for the poll: the save is refused as stale and the page reads the broken file then.
  const broken = 'max_running_agents: [\n'
  writeFileSync(settingsFile(omo), broken)
  await saveButton(page).click()
  const ask = page.getByRole('dialog', { name: 'Save anyway?' })
  await expect(ask).toBeVisible()
  await ask.getByRole('button', { name: 'Cancel' }).click()
  await expect(ask).toHaveCount(0)
  expect(onDisk(omo)).toBe(broken)
  await expect(page.locator('[data-file-error]')).toContainText('settings.yaml')
  await expect(bar(page)).toContainText('Unsaved changes')
  await expect(maxAgents(page)).toHaveValue('6')

  await saveButton(page).click()
  await page.getByRole('dialog', { name: 'Save anyway?' }).getByRole('button', { name: 'Save anyway' }).click()
  await expect(bar(page)).toContainText('Saved')
  expect(onDisk(omo)).toMatch(/max_running_agents: 6\b/)
})

test('a lock while the page has unsaved changes keeps them; after the unlock Save works with the new token', async ({ page, omo }) => {
  await openLimits(page, omo)
  await maxAgents(page).fill('6')
  await expect(bar(page)).toContainText('Unsaved changes')
  const oldToken = omo.token
  await omo.stop()
  expect((await omo.cli(['config', 'rotate-token'])).code).toBe(0)
  await omo.start()
  expect(omo.token).not.toBe(oldToken)

  // The event stream comes back with the old token, is refused, and the app locks.
  const locked = page.getByRole('heading', { name: 'omo is locked' })
  await expect(locked).toBeVisible()
  // The page waits hidden under the lock screen: nothing of it is shown or reachable.
  await expect(maxAgents(page)).toBeHidden()
  await expect(page.locator('[data-board-kept]')).toHaveAttribute('aria-hidden', 'true')
  await expect(page.locator('[data-board-kept]')).toHaveAttribute('inert', '')

  await page.getByRole('textbox', { name: 'Access address or token' }).fill(omo.accessUrl)
  await page.getByRole('button', { name: 'Unlock' }).click()
  await expect(locked).toHaveCount(0)
  await expect(maxAgents(page)).toHaveValue('6')
  await expect(bar(page)).toContainText('Unsaved changes')
  await saveButton(page).click()
  await expect(bar(page)).toContainText('Saved')
  expect(onDisk(omo)).toMatch(/max_running_agents: 6\b/)
})

test.describe('at 390 px', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('the page is usable, saves and does not scroll sideways', async ({ page, omo }) => {
    await openBoard(page, omo)
    await page.goto(omo.url('/settings/limits'))
    await expect(title(page)).toHaveText('Limits')
    await expect(page.getByRole('link', { name: 'Settings', exact: true })).toBeVisible()
    expect(await pageScrollsSideways(page)).toBe(false)
    await maxAgents(page).fill('2')
    await silentAfter(page).fill('1s')
    await saveButton(page).click()
    await expect(page.locator('.omo-field__error').filter({ hasText: 'health.silent_after must be between' })).toBeVisible()
    expect(await pageScrollsSideways(page)).toBe(false)
    await silentAfter(page).fill('30m')
    await saveButton(page).click()
    await expect(bar(page)).toContainText('Saved')
    await expect(saveButton(page)).toBeInViewport()
    expect(await pageScrollsSideways(page)).toBe(false)
  })
})

for (const scheme of ['dark', 'light'] as const) {
  test.describe(`${scheme} theme`, () => {
    test.use({ colorScheme: scheme })

    for (const width of [1440, 390]) {
      test(`the limits page has no accessibility violations at ${width} px`, async ({ page, omo }) => {
        await page.setViewportSize({ width, height: 900 })
        await openLimits(page, omo)
        expect(await violations(page)).toEqual([])
        // With a refusal under a field and the Save bar in its dirty state.
        await silentAfter(page).fill('1s')
        await saveButton(page).click()
        await expect(page.locator('.omo-field__error').filter({ hasText: 'health.silent_after' })).toBeVisible()
        expect(await violations(page)).toEqual([])
      })
    }
  })
}
