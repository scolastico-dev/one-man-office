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
