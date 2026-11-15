import AxeBuilder from '@axe-core/playwright'
import type { Page } from '@playwright/test'
import { expect, test } from './support/omo'
import { script, waitForSession, expectNoAgentProcesses } from './support/agents'
import { card, openBoard } from './support/board'

const panel = (page: Page) => page.locator('dialog[data-ticket-panel]')
const box = (page: Page) => panel(page).getByLabel('Write a comment')

async function send(page: Page, text: string) {
  await box(page).fill(text)
  await panel(page).locator('[data-send]').click()
}

async function sideways(page: Page): Promise<boolean> {
  return page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth)
}

test('a comment appears in a second tab without a reload', async ({ page, omo, context }) => {
  const n = await omo.createTicket({ title: 'Talk about it' })
  await openBoard(page, omo)
  await page.goto(omo.url(`/${n}`))
  await expect(panel(page)).toBeVisible()
  const other = await context.newPage()
  await other.goto(omo.url(`/${n}`))
  await expect(panel(other)).toBeVisible()
  await expect(panel(other).locator('[data-thread]')).toBeVisible()

  await send(page, 'First thought, see #' + n)
  await expect(panel(page).locator('[data-comment]')).toHaveCount(1)
  await expect(panel(other).locator('[data-comment-body]')).toContainText('First thought')
  // Shown once in both places, and the composer is empty again.
  await expect(panel(other).locator('[data-comment]')).toHaveCount(1)
  await expect(box(page)).toHaveValue('')
})

test('moving the ticket adds a greyed status line to the thread', async ({ page, omo }) => {
  const n = await omo.createTicket({ title: 'Move me' })
  await openBoard(page, omo)
  await page.goto(omo.url(`/${n}`))
  await expect(panel(page).locator('[data-thread]')).toBeVisible()
  await omo.moveTicket(n, 'ready')
  const line = panel(page).locator('[data-event]', { hasText: `moved #${n} from Planning to Ready For Dev` })
  await expect(line).toHaveCount(1)
  // Not a comment: no box.
  await expect(panel(page).locator('[data-comment]')).toHaveCount(0)
})

test('@ceo while the CEO is off shows the server message and keeps the text', async ({ page, omo }) => {
  const settings = await omo.api<{ settings: { ceo: { enabled: boolean } } }>('GET', '/api/settings')
  settings.settings.ceo.enabled = false
  await omo.api('PUT', '/api/settings', settings.settings)
  const n = await omo.createTicket({ title: 'No CEO' })
  await openBoard(page, omo)
  await page.goto(omo.url(`/${n}`))
  await expect(panel(page).locator('[data-thread]')).toBeVisible()
  await send(page, 'Hello @ceo, are you there?')
  await expect(panel(page).getByText(/@ceo cannot be mentioned on standalone task/)).toBeVisible()
  await expect(box(page)).toHaveValue('Hello @ceo, are you there?')
  await expect(panel(page).locator('[data-comment]')).toHaveCount(0)
})

test('characters that steer a terminal or reorder text are shown as escapes in a comment', async ({ page, omo }) => {
  const n = await omo.createTicket({ title: 'Hidden characters' })
  const body = 'Run `ok\u202E` then:\n\n```sh\nprintf "x\u001b[201~"\n\techo \u2066done\u2069\n```\n\n    rm\u200B -rf'
  await omo.api('POST', `/api/tickets/${n}/comments`, { body, question: false })
  await openBoard(page, omo)
  await page.goto(omo.url(`/${n}`))
  const shown = panel(page).locator('[data-comment-body]')
  await expect(shown).toHaveCount(1)
  await expect(shown.locator('pre code').first()).toHaveText('printf "x\\u{1B}[201~"\n\techo \\u{2066}done\\u{2069}\n')
  await expect(shown.locator('p code')).toHaveText('ok\\u{202E}')
  await expect(shown.locator('pre code').nth(1)).toHaveText('rm\\u{200B} -rf\n')
  const text = await shown.evaluate((el) => el.innerHTML)
  expect(text).not.toMatch(/[\u0000-\u0008\u000b-\u001f\u007f-\u009f\u200b\u202a-\u202e\u2066-\u2069]/u)
})

test.describe('a developer that asks', () => {
  test.use({ omoOptions: { agents: { developer: script('ready', 'ask|Which colour should it be?', 'wait|10m', 'hang') } } })

  test('a question from the fake developer can be answered from the thread', async ({ page, omo }) => {
    await openBoard(page, omo)
    const n = await omo.createTicket({ title: 'Pick a colour' })
    await omo.moveTicket(n, 'ready')
    await waitForSession(omo, n, 'developer', 'waiting')
    await expect(card(page, n).locator('[data-badge]')).toContainText('question for you')

    await page.goto(omo.url(`/${n}`))
    const question = panel(page).locator('[data-comment][data-question="open"]')
    await expect(question).toHaveCount(1)
    await expect(question).toContainText('Which colour should it be?')
    await expect(question).toContainText('Question for you')
    await expect(question).toContainText('Waiting for answer')

    await question.getByRole('button', { name: 'Answer' }).click()
    await expect(panel(page).locator('[data-answering]')).toContainText('Answering question')
    await expect(box(page)).toBeFocused()
    await box(page).fill('Blue.')
    await box(page).press('Control+Enter')

    await expect(panel(page).locator('[data-comment][data-question="answered"]')).toHaveCount(1)
    await expect(panel(page).locator('[data-comment][data-question="open"]')).toHaveCount(0)
    await expect(panel(page).locator('[data-answering]')).toHaveCount(0)
    await expect(panel(page).locator('[data-comment]')).toHaveCount(2)
    await page.keyboard.press('Escape')
    await expect(card(page, n)).not.toContainText('question for you')
    await expectNoAgentProcesses(omo)
  })
})

test('?tab=comments&comment=<id> opens the panel at that comment', async ({ page, omo }) => {
  const n = await omo.createTicket({ title: 'Linked' })
  const first = await omo.api<{ comment: { id: number } }>('POST', `/api/tickets/${n}/comments`, { body: 'Target comment', question: false })
  for (let i = 0; i < 15; i++) await omo.api('POST', `/api/tickets/${n}/comments`, { body: `Filler ${i}\n\n`.repeat(6), question: false })
  await openBoard(page, omo)
  await page.goto(omo.url(`/${n}?tab=comments&comment=${first.comment.id}`))
  const target = panel(page).locator(`#comment-${first.comment.id}`)
  await expect(target).toBeVisible()
  await expect(target).toBeFocused()
  await expect(target).toContainText('Target comment')
  await expect(target).toBeInViewport()
})

test('at 390 px the tabs and the composer are usable without horizontal scrolling', async ({ page, omo }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  const n = await omo.createTicket({ title: 'Narrow' })
  await omo.api('POST', `/api/tickets/${n}/comments`, { body: 'A long word: ' + 'x'.repeat(200) + '\n\n```\n' + 'y'.repeat(200) + '\n```', question: false })
  await openBoard(page, omo)
  await page.goto(omo.url(`/${n}`))
  await expect(panel(page).locator('[data-comment]')).toHaveCount(1)
  expect(await sideways(page)).toBe(false)
  const tabs = panel(page).getByRole('tablist', { name: `Activity on #${n}` })
  await expect(tabs.getByRole('tab')).toHaveCount(3)
  await tabs.getByRole('tab', { name: 'Diff' }).click()
  await expect(page).toHaveURL(/tab=diff/)
  await tabs.getByRole('tab', { name: 'Comments' }).click()
  await box(page).scrollIntoViewIfNeeded()
  await box(page).fill('@')
  await expect(panel(page).getByRole('listbox', { name: 'Roles to mention' })).toBeVisible()
  await box(page).press('Escape')
  await expect(panel(page)).toBeVisible()
  await expect(panel(page).locator('[data-send]')).toBeVisible()
  expect(await sideways(page)).toBe(false)
  const results = await new AxeBuilder({ page }).include('dialog[data-ticket-panel]').analyze()
  expect(results.violations).toEqual([])
})
