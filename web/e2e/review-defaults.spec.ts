import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import AxeBuilder from '@axe-core/playwright'
import type { Page } from '@playwright/test'
import { card, expectBoardLive, openBoard } from './support/board'
import { expect, test, type Omo } from './support/omo'

const onDisk = (omo: Omo) => readFileSync(join(omo.home, 'omo', 'settings.yaml'), 'utf8')
const title = (page: Page) => page.locator('[data-settings-page] h1')
const bar = (page: Page) => page.getByRole('region', { name: 'Save changes' })
const saveButton = (page: Page) => bar(page).getByRole('button', { name: 'Save', exact: true })
const group = (page: Page, legend: string) => page.getByRole('group', { name: legend })
const panel = (page: Page) => page.locator('dialog[data-ticket-panel]')
const field = (page: Page, name: string) => panel(page).locator(`[data-field="${name}"]`)
const ceoTab = (page: Page) => page.locator('[data-sidebar]').getByRole('tab', { name: /^CEO/ })
const ceoSwitch = (page: Page) => page.getByRole('switch', { name: 'The CEO supervises the board' })

async function openSettings(page: Page, omo: Omo, section: 'review' | 'general') {
  await openBoard(page, omo)
  await page.goto(omo.url(`/settings/${section}`))
  await expect(title(page)).toHaveText(section === 'review' ? 'Review' : 'General')
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

test('task review Human: a new task shows "Default (Human)" in its panel and resolves to Human on the server', async ({ page, omo }) => {
  await openSettings(page, omo, 'review')
  await group(page, 'Tasks without a story').getByLabel('Review').selectOption('human')
  await group(page, 'Tasks without a story').getByLabel('When approved').selectOption('export')
  await group(page, 'Tasks in a story').getByLabel('Tasks are released by').selectOption('user')
  await saveButton(page).click()
  await expect(bar(page)).toContainText('Saved')
  const text = onDisk(omo)
  expect(text).toMatch(/task: human/)
  expect(text).toMatch(/task: export/)
  expect(text).toMatch(/require_user_release: true/)

  // Created after the save, from the board like any other ticket.
  const n = await omo.createTicket({ title: 'Uses the review defaults' })
  const ticket = await omo.api<{ effective_review: string; review: string | null }>('GET', `/api/tickets/${n}`)
  expect(ticket.review).toBeNull()
  expect(ticket.effective_review).toBe('human')

  await page.getByRole('link', { name: 'Back to board' }).click()
  await expectBoardLive(page)
  await card(page, n).locator('[data-card-link]').click()
  await expect(panel(page)).toBeVisible()
  await expect(field(page, 'review')).toContainText('Default (Human)')
  await expect(field(page, 'when_approved')).toContainText('Default (Export as a pull request)')

  // The release choice belongs to stories: a new story shows the default too.
  await page.keyboard.press('Escape')
  const story = await omo.createTicket({ title: 'A story', type: 'story' })
  await card(page, story).locator('[data-card-link]').click()
  await expect(field(page, 'task_release')).toContainText('Default (You release tasks)')
})

test('the story note shows for Skipped and Human and goes for Agent', async ({ page, omo }) => {
  await openSettings(page, omo, 'review')
  const stories = group(page, 'Stories')
  const note = stories.getByText('No agent runs the full test suite for stories.')
  await stories.getByLabel('Review').selectOption('agent')
  await expect(note).toHaveCount(0)
  await stories.getByLabel('Review').selectOption('skipped')
  await expect(note).toBeVisible()
  await stories.getByLabel('Review').selectOption('human')
  await expect(note).toBeVisible()
  await stories.getByLabel('Review').selectOption('agent_then_human')
  await expect(note).toHaveCount(0)
  const inStory = group(page, 'Tasks in a story')
  await expect(inStory.getByLabel('When approved')).toHaveValue('Merge locally')
  await expect(inStory.getByLabel('When approved')).toBeDisabled()
  await expect(inStory.getByText("A task in a story always merges into the story's branch")).toBeVisible()
})

test("a task in a story takes the review default of tasks in a story, and shows it in its panel", async ({ page, omo }) => {
  await openSettings(page, omo, 'review')
  await group(page, 'Tasks in a story').getByLabel('Review').selectOption('human')
  await saveButton(page).click()
  await expect(bar(page)).toContainText('Saved')
  expect(onDisk(omo)).toMatch(/story_task: human/)

  const story = await omo.createTicket({ title: 'A story', type: 'story' })
  const task = await omo.createTicket({ title: 'Its task', story })
  const standalone = await omo.createTicket({ title: 'On its own' })
  const read = (n: number) => omo.api<{ effective_review: string }>('GET', `/api/tickets/${n}`)
  expect((await read(task)).effective_review).toBe('human')
  expect((await read(standalone)).effective_review).toBe('agent_then_human')

  await page.getByRole('link', { name: 'Back to board' }).click()
  await expectBoardLive(page)
  await card(page, task).locator('[data-card-link]').click()
  await expect(field(page, 'review')).toContainText('Default (Human)')
})

test('switching the CEO off asks first, hides the CEO tab and survives a reload; on again needs no question', async ({ page, omo }) => {
  await openSettings(page, omo, 'general')
  await expect(ceoSwitch(page)).toHaveAttribute('aria-checked', 'true')

  await ceoSwitch(page).click()
  const ask = page.getByRole('dialog', { name: 'Switch the CEO off?' })
  await expect(ask).toBeVisible()
  await expect(ask).toContainText('a running CEO session ends at the next scheduling pass')
  await expect(ask).toContainText('Every ticket the CEO supervised, and every open question addressed to it, is handed to you.')
  await ask.getByRole('button', { name: 'Keep the CEO on' }).click()
  await expect(ask).toHaveCount(0)
  await expect(ceoSwitch(page)).toHaveAttribute('aria-checked', 'true')
  await expect(bar(page)).toContainText('No unsaved changes')

  await ceoSwitch(page).click()
  await ask.getByRole('button', { name: 'Switch the CEO off' }).click()
  await expect(ceoSwitch(page)).toHaveAttribute('aria-checked', 'false')
  await expect(bar(page)).toContainText('Unsaved changes')
  // The tab is still there: nothing happens before Save.
  await expect(ceoTab(page)).toBeVisible()
  await saveButton(page).click()
  await expect(bar(page)).toContainText('Saved')
  expect(onDisk(omo)).toMatch(/ceo:\s+enabled: false/)
  await expect(ceoTab(page)).toHaveCount(0)

  await page.reload()
  await expect(title(page)).toHaveText('General')
  await expect(ceoSwitch(page)).toHaveAttribute('aria-checked', 'false')
  await expect(ceoTab(page)).toHaveCount(0)

  await ceoSwitch(page).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await saveButton(page).click()
  await expect(bar(page)).toContainText('Saved')
  await expect(ceoTab(page)).toBeVisible()
})

test('the CEO may approve stories saves with its own key only; the server facts and the theme', async ({ page, omo }) => {
  await openSettings(page, omo, 'general')
  const patches: string[] = []
  page.on('request', (r) => {
    if (r.method() === 'PATCH') patches.push(r.postData() ?? '')
  })
  await page.getByRole('switch', { name: 'The CEO may approve stories' }).click()
  await saveButton(page).click()
  await expect(bar(page)).toContainText('Saved')
  expect(patches).toHaveLength(1)
  expect(Object.keys(JSON.parse(patches[0]!) as object)).toEqual(['ceo'])
  expect(onDisk(omo)).toMatch(/may_approve_stories: true/)

  const facts = page.locator('[data-server-facts]')
  await expect(facts).toContainText(join(omo.home, 'omo'))
  await expect(facts).toContainText(`omo.localhost:${omo.port}`)
  await expect(page.getByText('Host, port and sign-in are start options')).toBeVisible()

  // The theme is this browser's: it applies at once, needs no Save and is remembered.
  await page.getByLabel('Colours').selectOption('dark')
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
  await expect(bar(page)).not.toContainText('Unsaved changes')
  await page.reload()
  await expect(page.getByLabel('Colours')).toHaveValue('dark')
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
})

test.describe('at 390 px', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  for (const section of ['review', 'general'] as const) {
    test(`${section} does not scroll sideways and saves`, async ({ page, omo }) => {
      await openSettings(page, omo, section)
      expect(await pageScrollsSideways(page)).toBe(false)
      if (section === 'review') await group(page, 'Stories').getByLabel('Review').selectOption('skipped')
      else await page.getByRole('switch', { name: 'The CEO may approve stories' }).click()
      await saveButton(page).click()
      await expect(bar(page)).toContainText('Saved')
      await expect(saveButton(page)).toBeInViewport()
      expect(await pageScrollsSideways(page)).toBe(false)
    })
  }

  test('the CEO question is a dialog that fits', async ({ page, omo }) => {
    await openSettings(page, omo, 'general')
    await ceoSwitch(page).click()
    const ask = page.getByRole('dialog', { name: 'Switch the CEO off?' })
    await expect(ask).toBeVisible()
    await expect(ask.getByRole('button', { name: 'Switch the CEO off' })).toBeInViewport()
    expect(await pageScrollsSideways(page)).toBe(false)
  })
})

for (const scheme of ['dark', 'light'] as const) {
  test.describe(`${scheme} theme`, () => {
    test.use({ colorScheme: scheme })

    for (const width of [1440, 390]) {
      for (const section of ['review', 'general'] as const) {
        test(`${section} has no accessibility violations at ${width} px`, async ({ page, omo }) => {
          await page.setViewportSize({ width, height: 900 })
          await openSettings(page, omo, section)
          expect(await violations(page)).toEqual([])
          if (section === 'general') {
            await ceoSwitch(page).click()
            await expect(page.getByRole('dialog', { name: 'Switch the CEO off?' })).toBeVisible()
            expect(await violations(page)).toEqual([])
          }
        })
      }
    }
  })
}
