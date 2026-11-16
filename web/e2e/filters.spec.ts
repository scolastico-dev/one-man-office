import { mkdirSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test } from './support/omo'
import { addressBar, boardCards, card, expectBoardLive, openBoard } from './support/board'

const demo = (path = '{{repo}}') => ({ path, name: 'demo' })

test.use({
  omoOptions: {
    seed: {
      projects: [demo()],
      tickets: [
        { ref: 'story', type: 'story', title: 'Billing overhaul', projects: ['demo'] },
        { ref: 'inside', type: 'task', title: 'Invoice layout', story: 'story', projects: ['demo'] },
        { ref: 'outside', type: 'task', title: 'Unrelated chore', projects: ['demo'] },
        { ref: 'review', type: 'task', title: 'Waiting for the user', to: 'review', review: 'human', projects: ['demo'] },
        { ref: 'second', type: 'task', title: 'Also waiting', to: 'review', review: 'human', projects: ['demo'] },
        { ref: 'plain', type: 'task', title: 'Plain one', projects: ['demo'] },
      ],
    },
  },
})

async function shown(page: import('@playwright/test').Page): Promise<number[]> {
  const all = await boardCards(page)
  return Object.values(all).flat().sort((a, b) => a - b)
}

test('needs me shows exactly the tickets GET /api/tickets?needs_me=true returns', async ({ page, omo }) => {
  const expected = (await omo.api<{ tickets: { number: number }[] }>('GET', '/api/tickets?needs_me=true')).tickets.map((t) => t.number).sort((a, b) => a - b)
  expect(expected).toContain(omo.refs.review)
  expect(expected).toContain(omo.refs.second)
  expect(expected.length).toBeLessThan(6)

  await openBoard(page, omo)
  await page.getByRole('button', { name: /^Needs me/ }).click()
  await expect.poll(() => shown(page)).toEqual(expected)
  await expect(page.getByRole('button', { name: /^Needs me/ })).toContainText(String(expected.length))
  await expect(page).toHaveURL(/needs_me=1/)
})

test('search finds a ticket by a word that is only in its description', async ({ page, omo }) => {
  const [project] = await omo.api<{ id: string }[]>('GET', '/api/projects')
  const n = await omo.api<{ number: number }>('POST', '/api/tickets', {
    type: 'task',
    title: 'Plain title',
    description: 'Remember the quokka migration before release.',
    projects: [project!.id],
  })
  await openBoard(page, omo)
  const search = page.getByRole('searchbox', { name: 'Search tickets' })
  await search.fill('quokka')
  await expect(card(page, n.number)).toBeVisible()
  expect(await shown(page)).toEqual([n.number])
  await expect(page.locator('[data-search-status]')).toHaveText('')
  // Titles match at once, without the server.
  await search.fill('invoice')
  await expect(card(page, omo.refs.inside!)).toBeVisible()
  await expect.poll(() => shown(page)).toEqual([omo.refs.inside!])
})

test('a story chip filters to the story and its tasks, and the chip removes it', async ({ page, omo }) => {
  await openBoard(page, omo)
  await card(page, omo.refs.inside!).locator('[data-chip="story"]').click()
  await expect.poll(() => shown(page)).toEqual([omo.refs.story!, omo.refs.inside!].sort((a, b) => a - b))
  await expect(page).toHaveURL(/story=/)
  await page.locator('[data-story-chip]').click()
  await expect.poll(async () => (await shown(page)).length).toBe(6)
  expect(await addressBar(page)).not.toContain('story=')
})

test('a ticket opened from the bell keeps the filters, and closing it returns to the filtered board', async ({ page, omo }) => {
  const n = omo.refs.review!
  await openBoard(page, omo)
  await page.getByRole('button', { name: /^Needs me/ }).click()
  await expect(page).toHaveURL(/needs_me=1/)
  await page.locator('[data-bell]').click()
  await page.locator(`[data-bell-list] [data-waiting="${n}"]`).click()
  await expect(page.locator('dialog[data-ticket-panel]')).toBeVisible()
  await expect(page).toHaveURL(new RegExp(`/${n}\\?needs_me=1$`))
  await page.keyboard.press('Escape')
  await expect(page.locator('dialog[data-ticket-panel]')).toHaveCount(0)
  await expect(page).toHaveURL(/\/\?needs_me=1$/)
  await expect(page.getByRole('button', { name: /^Needs me/ })).toHaveAttribute('aria-pressed', 'true')
})

test('filters survive a reload', async ({ page, omo }) => {
  await openBoard(page, omo)
  await page.getByRole('combobox', { name: 'Supervisor' }).selectOption('user')
  await page.getByRole('searchbox', { name: 'Search tickets' }).fill('plain')
  await expect.poll(() => shown(page)).toEqual([omo.refs.plain!])
  const url = await addressBar(page)
  expect(url).toContain('supervisor=user')
  expect(url).toContain('q=plain')
  expect(url).not.toContain(omo.token)
  await page.reload()
  await expectBoardLive(page)
  await expect.poll(() => shown(page)).toEqual([omo.refs.plain!])
  await expect(page.getByRole('searchbox', { name: 'Search tickets' })).toHaveValue('plain')
  await expect(page.getByRole('combobox', { name: 'Supervisor' })).toHaveValue('user')
})

test('a filter that hides everything says so and clearing brings the cards back', async ({ page, omo }) => {
  await openBoard(page, omo)
  await page.getByRole('searchbox', { name: 'Search tickets' }).fill('zzzzzz')
  await expect(page.locator('[data-nothing-matches]')).toBeVisible()
  await expect(page.getByText('first steps', { exact: false })).toHaveCount(0)
  await expect(page.locator('[data-drop-column="planning"] h2')).toContainText('0 of')
  await page.locator('[data-nothing-matches]').getByRole('button', { name: 'Clear filters' }).click()
  await expect.poll(async () => (await shown(page)).length).toBe(6)
})

test('the search shortcut focuses the field', async ({ page, omo }) => {
  await openBoard(page, omo)
  await page.keyboard.press('/')
  await expect(page.getByRole('searchbox', { name: 'Search tickets' })).toBeFocused()
})

test('the story filter is a search: by number or title words, keyboard to choose, the address keeps it', async ({ page, omo }) => {
  const [project] = await omo.api<{ id: string }[]>('GET', '/api/projects')
  const more: number[] = []
  for (const title of ['Search rework', 'Payments export', 'Onboarding tour']) {
    more.push((await omo.api<{ number: number }>('POST', '/api/tickets', { type: 'story', title, projects: [project!.id] })).number)
  }
  await openBoard(page, omo)
  const button = page.getByRole('button', { name: 'Story: All stories' })
  await button.click()
  const search = page.getByRole('combobox', { name: 'Search stories' })
  await expect(search).toBeFocused()
  const items = page.locator('[data-filter-story] [data-pick-item]')
  await expect(items.first()).toContainText('All stories')
  await expect(items).toHaveCount(5)
  await expect(page.locator('[data-filter-story] [data-pick-item]').nth(1)).toContainText(`#${omo.refs.story!}`)
  await expect(page.locator('[data-filter-story] [data-pick-item]').nth(1)).toContainText('Billing overhaul')
  await expect(page.locator('[data-filter-story] [data-pick-item]').nth(1)).toContainText('Planning')

  // Title words, in any order; "All stories" stays first.
  await search.fill('export pay')
  await expect(items).toHaveCount(2)
  await expect(items.nth(1)).toContainText('Payments export')
  // By number.
  await search.fill(`#${omo.refs.story!}`)
  await expect(items.nth(1)).toContainText('Billing overhaul')
  await page.keyboard.press('ArrowDown')
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(new RegExp(`story=${omo.refs.story!}`))
  await expect.poll(() => shown(page)).toEqual([omo.refs.story!, omo.refs.inside!].sort((a, b) => a - b))
  const chosen = page.getByRole('button', { name: `Story: #${omo.refs.story!} Billing overhaul` })
  await expect(chosen).toBeFocused()

  // Back to every story through the first entry.
  await chosen.click()
  await page.keyboard.press('Enter')
  await expect(page.getByRole('button', { name: 'Story: All stories' })).toBeVisible()
  expect(await addressBar(page)).not.toContain('story=')
  expect(more).toHaveLength(3)
})

test.describe('on a phone', () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true })

  test('the story search works inside the filter sheet', async ({ page, omo }) => {
    await openBoard(page, omo)
    await page.getByRole('button', { name: 'Filters', exact: true }).tap()
    const sheet = page.getByRole('dialog', { name: 'Filters' })
    await sheet.getByRole('button', { name: 'Story: All stories' }).tap()
    const search = sheet.getByRole('combobox', { name: 'Search stories' })
    await expect(search).toBeFocused()
    await search.fill('billing')
    await page.keyboard.press('ArrowDown')
    await page.keyboard.press('Enter')
    await expect(page).toHaveURL(new RegExp(`story=${omo.refs.story!}`))
    await expect(sheet.getByRole('button', { name: `Story: #${omo.refs.story!} Billing overhaul` })).toBeFocused()
    expect(await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth)).toBe(false)
    await sheet.getByRole('button', { name: 'Done' }).tap()
    await expect.poll(() => shown(page)).toEqual([omo.refs.story!, omo.refs.inside!].sort((a, b) => a - b))
  })
})

// With OMO_SCREENSHOTS=<folder>: the open story search, fix4-storypicker-<theme>-<width>.png.
for (const theme of ['dark', 'light'] as const) {
  for (const width of [1440, 390] as const) {
    test(`the story search looks right (${theme}, ${width} px)`, async ({ page, omo }) => {
      const shots = process.env.OMO_SCREENSHOTS ?? ''
      test.skip(shots === '', 'screenshots only')
      await page.emulateMedia({ colorScheme: theme })
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 })
      const [project] = await omo.api<{ id: string }[]>('GET', '/api/projects')
      for (const title of ['Search rework', 'Payments export', 'Onboarding tour']) await omo.api('POST', '/api/tickets', { type: 'story', title, projects: [project!.id] })
      await openBoard(page, omo)
      let root = page.locator('body')
      if (width === 390) {
        await page.getByRole('button', { name: 'Filters', exact: true }).click()
        root = page.getByRole('dialog', { name: 'Filters' })
      }
      await root.getByRole('button', { name: 'Story: All stories' }).click()
      await root.getByRole('combobox', { name: 'Search stories' }).fill('o')
      mkdirSync(shots, { recursive: true })
      await page.screenshot({ path: join(shots, `fix4-storypicker-${theme}-${width}.png`) })
    })
  }
}
