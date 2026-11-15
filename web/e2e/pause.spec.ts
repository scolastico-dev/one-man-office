import { expect, test } from './support/omo'
import { openBoard } from './support/board'

const switchOf = (page: import('@playwright/test').Page) => page.getByRole('switch', { name: 'Paused' })

test.use({ omoOptions: { paused: false } })

test('the pause switch pauses omo and a second tab follows', async ({ page, omo, context }) => {
  await openBoard(page, omo)
  const second = await context.newPage()
  await second.goto(omo.url('/'))
  await expect(switchOf(second)).toBeVisible()
  await expect(switchOf(page)).toHaveAttribute('aria-checked', 'false')
  // The state shows in words too, while the name stays "Paused".
  await expect(switchOf(page)).toHaveText('Running')

  await switchOf(page).click()
  await expect(switchOf(page)).toHaveAttribute('aria-checked', 'true')
  await expect(switchOf(page)).toHaveText('Paused')
  await expect(page.locator('[data-paused-banner]')).toHaveText('Paused: omo starts no new agents.')
  await expect(switchOf(second)).toHaveAttribute('aria-checked', 'true')
  expect((await omo.api<{ paused: boolean }>('GET', '/api/server')).paused).toBe(true)

  await switchOf(second).click()
  await expect(switchOf(second)).toHaveAttribute('aria-checked', 'false')
  await expect(switchOf(page)).toHaveAttribute('aria-checked', 'false')
  await expect(switchOf(page)).toHaveText('Running')
  await expect(page.locator('[data-paused-banner]')).toHaveCount(0)
  expect((await omo.api<{ paused: boolean }>('GET', '/api/server')).paused).toBe(false)
})

test('stop all agents asks first and reports the server answer in words', async ({ page, omo }) => {
  await openBoard(page, omo)
  await page.getByRole('button', { name: 'Stop all agents' }).click()
  const dialog = page.getByRole('dialog', { name: 'Stop every running agent?' })
  await expect(dialog).toContainText('The CEO keeps running')
  await dialog.getByRole('button', { name: 'Stop all agents' }).click()
  await expect(page.getByText('All agents stopped')).toBeVisible()
  await expect(dialog).toBeHidden()
  // Stopping every agent also pauses omo.
  await expect(page.getByRole('switch', { name: 'Paused' })).toHaveAttribute('aria-checked', 'true')
  expect((await omo.api<{ paused: boolean }>('GET', '/api/server')).paused).toBe(true)
})
