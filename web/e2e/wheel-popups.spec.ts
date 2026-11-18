// Every scroll box that opens over the board (a list in a popup, a dialog's body, a picker) takes a trusted wheel
// on the compositor alone: a box that needs the main thread to find its target can drop the wheel (see support/wheelTrace).
// Text fields are the exception, see support/wheelAudit.
import { mkdirSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { expect, test, type Omo } from './support/omo'
import { openBoard } from './support/board'
import { initRepo } from './support/git'
import { scrollBoxesTakingWheelsBadly } from './support/wheelAudit'

const seeded = {
  seed: {
    projects: [{ path: '{{repo}}', name: 'demo' }],
    tickets: [{ ref: 'review', type: 'task', title: 'Waiting for you', to: 'review', review: 'human', projects: ['demo'], branch: 'omo/review' }],
  },
}

/** Twelve more repositories as projects beside the test's own one, so the project searches have a list to scroll. */
async function addProjects(omo: Omo, repo: string): Promise<void> {
  for (let i = 1; i <= 12; i++) {
    const dir = join(dirname(repo), `service-${String(i).padStart(2, '0')}`)
    initRepo(dir)
    await omo.api('POST', '/api/projects', { path: dir, name: `service-${String(i).padStart(2, '0')}` })
  }
}

test.describe('the wheel over popups and lists', () => {
  test.use({ omoOptions: seeded })

  test('the bell list, the project filter, New terminal and the folder picker take the wheel on the compositor', async ({ page, omo, repo }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await addProjects(omo, repo)
    await openBoard(page, omo)

    await page.locator('[data-bell]').click()
    await expect(page.locator('[data-bell-list]')).toBeVisible()
    expect(await scrollBoxesTakingWheelsBadly(page, '[data-bell-list]'), 'the bell list').toEqual([])
    await page.keyboard.press('Escape')

    const filter = page.getByRole('button', { name: 'Project: All projects' })
    await filter.click()
    await expect(page.getByRole('listbox').first()).toBeVisible()
    expect(await scrollBoxesTakingWheelsBadly(page, '[role="listbox"]'), 'the project filter').toEqual([])
    await page.keyboard.press('Escape')

    await page.locator('[data-sidebar]').getByRole('tab', { name: /^Terminals/ }).click()
    await page.locator('[data-sidebar]').getByRole('button', { name: 'New terminal' }).click()
    await expect(page.getByRole('listbox').first()).toBeVisible()
    expect(await scrollBoxesTakingWheelsBadly(page, '[role="listbox"]'), "New terminal's search").toEqual([])
    await page.keyboard.press('Escape')

    for (const tab of [/^CEO/, /^Agents/]) {
      await page.locator('[data-sidebar]').getByRole('tab', { name: tab }).click()
      expect(await scrollBoxesTakingWheelsBadly(page, '[data-sidebar]'), `the sidebar tab ${tab}`).toEqual([])
    }

    const outside = join(omo.home, 'elsewhere')
    for (let i = 0; i < 40; i++) mkdirSync(join(outside, `folder-${String(i).padStart(2, '0')}`), { recursive: true })
    initRepo(join(outside, 'tool'))
    await page.goto(omo.url('/settings/projects'))
    await page.getByRole('button', { name: 'Add project' }).click()
    const add = page.getByRole('dialog', { name: 'Add project' })
    await add.getByRole('button', { name: 'Browse…' }).click()
    const picker = page.getByRole('dialog', { name: /folder/i }).last()
    await picker.getByLabel('Folder', { exact: true }).fill(outside)
    await picker.getByLabel('Folder', { exact: true }).press('Enter')
    await expect(picker.locator('[data-folder="tool"]')).toBeVisible()
    expect(await scrollBoxesTakingWheelsBadly(page, 'dialog[open]'), 'the folder picker').toEqual([])
  })

  test("a ticket's description history dialog and its create dialog take the wheel on the compositor", async ({ page, omo }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    const n = omo.refs.review!
    for (let i = 0; i < 25; i++) {
      const { description_version } = await omo.api<{ description_version: number }>('GET', `/api/tickets/${n}`)
      await omo.api('PUT', `/api/tickets/${n}/description`, { text: `Version ${i + 1}\n\n${'A line of the description.\n\n'.repeat(i + 3)}`, base_version: description_version })
    }
    await openBoard(page, omo)
    await page.goto(omo.url(`/ticket/${n}`))
    await page.locator('[data-open-history]').click()
    await expect(page.locator('dialog[open]').last()).toBeVisible()
    expect(await scrollBoxesTakingWheelsBadly(page, 'dialog[open]'), 'the description history').toEqual([])
  })
})
