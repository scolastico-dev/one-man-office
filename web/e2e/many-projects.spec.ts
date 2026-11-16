// With many projects the board's project filter and the project choice of a
// new ticket are searched, not scrolled.
import { dirname, join } from 'node:path'
import AxeBuilder from '@axe-core/playwright'
import type { Page } from '@playwright/test'
import { card, expectBoardLive, openBoard } from './support/board'
import { initRepo } from './support/git'
import { expect, test, type Omo } from './support/omo'

const NAMES = Array.from({ length: 14 }, (_, i) => `service-${String(i + 1).padStart(2, '0')}`)

/** Adds 14 more repositories as projects beside the test's own one; returns their ids by name. */
async function addProjects(omo: Omo, repo: string): Promise<Record<string, string>> {
  const ids: Record<string, string> = {}
  for (const name of NAMES) {
    const dir = join(dirname(repo), name)
    initRepo(dir)
    ids[name] = (await omo.api<{ id: string }>('POST', '/api/projects', { path: dir, name })).id
  }
  return ids
}

const picker = (page: Page) => page.locator('[data-filter-project]')

test('the project filter of the board is a search: type, pick with the keyboard, and the address keeps it', async ({ page, omo, repo }) => {
  const ids = await addProjects(omo, repo)
  const inEleven = await omo.createTicket({ title: 'In service 11', projects: [ids['service-11']!] })
  const elsewhere = await omo.createTicket({ title: 'In demo' })
  await openBoard(page, omo)

  const button = page.getByRole('button', { name: 'Project: All projects' })
  await button.click()
  const search = page.getByRole('combobox', { name: 'Search projects' })
  await expect(search).toBeFocused()
  await expect(picker(page).locator('[data-pick-count]')).toHaveText('15 projects')
  await search.fill('11')
  await expect(picker(page).locator('[data-pick-item]')).toHaveCount(2)
  await expect(picker(page).locator('[data-pick-item]').first()).toHaveText('All projects')
  await page.keyboard.press('ArrowDown')
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(new RegExp(`project=${encodeURIComponent(ids['service-11']!)}`))
  await expect(card(page, inEleven)).toBeVisible()
  await expect(card(page, elsewhere)).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Project: service-11' })).toBeFocused()

  const results = await new AxeBuilder({ page }).include('form[role=search]').analyze()
  expect(results.violations).toEqual([])

  await page.reload()
  await expectBoardLive(page)
  await expect(page.getByRole('button', { name: 'Project: service-11' })).toBeVisible()
  await page.getByRole('button', { name: 'Project: service-11' }).click()
  await picker(page).locator('[data-pick-item=""]').click()
  await expect(card(page, elsewhere)).toBeVisible()
  expect(page.url()).not.toContain('project=')
})

test('the project choice of a new ticket has a filter that keeps the chosen project in view', async ({ page, omo, repo }) => {
  await addProjects(omo, repo)
  await openBoard(page, omo)
  await page.getByRole('button', { name: 'Create ticket' }).click()
  const dialog = page.getByRole('dialog', { name: 'Create ticket' })
  await dialog.getByRole('radio', { name: 'Task' }).check({ force: true })
  await dialog.getByLabel('Title').fill('Picked from many')
  const filter = dialog.getByRole('searchbox', { name: 'Filter projects' })
  await filter.fill('service-0')
  await expect(dialog.locator('[data-project-count]')).toHaveText('9 of 15 projects match')
  await dialog.getByRole('radio', { name: /service-07/ }).check()
  await filter.fill('service-12')
  await expect(dialog.getByRole('radio', { name: /service-/ })).toHaveCount(2)
  await expect(dialog.getByRole('radio', { name: /service-07/ })).toBeChecked()
  await filter.press('Enter')
  await expect(dialog).toBeVisible()
  await dialog.getByRole('button', { name: 'Create', exact: true }).click()
  await expect(dialog).toBeHidden()
  const list = await omo.api<{ tickets: { title: string; projects: { project: string }[] }[] }>('GET', '/api/tickets')
  const t = list.tickets.find((x) => x.title === 'Picked from many')!
  const projects = await omo.api<{ id: string; name: string }[]>('GET', '/api/projects')
  expect(projects.find((p) => p.id === t.projects[0]!.project)?.name).toBe('service-07')
})
