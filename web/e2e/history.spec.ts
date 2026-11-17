import AxeBuilder from '@axe-core/playwright'
import type { Page } from '@playwright/test'
import { expect, test } from './support/omo'
import { card, openBoard } from './support/board'

const demo = { path: '{{repo}}', name: 'demo' }

function panel(page: Page) {
  return page.locator('dialog[data-ticket-panel]')
}

async function openByClick(page: Page, n: number) {
  await card(page, n).locator('[data-card-link]').click()
  await expect(panel(page)).toBeVisible()
}

test.describe('description history', () => {
  test('edit twice, restore version 1: the description shows it and version 3 says restored from v1; a second tab follows', async ({ page, omo }) => {
    const n = await omo.createTicket({ title: 'History' })
    await openBoard(page, omo)
    const second = await page.context().newPage()
    await second.goto(omo.url(`/${n}`))
    await expect(panel(second)).toBeVisible()
    await openByClick(page, n)

    // First edit in the panel, second one from elsewhere.
    await panel(page).getByRole('button', { name: 'Edit', exact: true }).click()
    await panel(page).getByLabel('Description (Markdown)').fill('## First\n\nthe first text')
    await panel(page).getByRole('button', { name: 'Save', exact: true }).click()
    await expect(panel(page).locator('[data-description] h4')).toHaveText('First')
    await omo.api('PUT', `/api/tickets/${n}/description`, { text: 'the second text' })
    await expect(panel(page).locator('[data-description]')).toHaveText('the second text')
    await expect(panel(second).locator('[data-description]')).toHaveText('the second text')

    await panel(page).getByRole('button', { name: 'History (2)' }).click()
    const dialog = page.getByRole('dialog', { name: `Description history of #${n}` })
    await expect(dialog).toBeVisible()
    await expect(dialog.locator('[data-version]')).toHaveText([/Version 2.*current/s, /Version 1/s])
    await expect(dialog.locator('[data-history-preview]')).toHaveText('the second text')
    await expect(dialog.getByRole('button', { name: 'Restore this version' })).toBeDisabled()
    for (const scheme of ['dark', 'light'] as const) {
      await page.emulateMedia({ colorScheme: scheme })
      const results = await new AxeBuilder({ page }).analyze()
      expect(results.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((x) => x.target.join(' ')) }))).toEqual([])
    }

    await dialog.locator('[data-version="1"]').click()
    await expect(dialog.locator('[data-history-preview] h4')).toHaveText('First')
    await dialog.getByRole('button', { name: 'Restore this version' }).click()
    await expect(dialog.locator('[data-version]')).toHaveText([/Version 3.*current.*restored from v1/s, /Version 2/s, /Version 1/s])
    await dialog.getByRole('button', { name: 'Done' }).click()
    await expect(dialog).toHaveCount(0)

    await expect(panel(page).locator('[data-description] h4')).toHaveText('First')
    await expect(panel(page).getByRole('button', { name: 'History (3)' })).toBeVisible()
    await expect(panel(second).locator('[data-description] h4')).toHaveText('First')
    await expect(panel(second).getByRole('button', { name: 'History (3)' })).toBeVisible()
    const versions = await omo.api<{ number: number; restored_from: number | null }[]>('GET', `/api/tickets/${n}/descriptions`)
    expect(versions.map((v) => [v.number, v.restored_from])).toEqual([[3, 1], [2, null], [1, null]])
  })

  test('restoring while editing asks first and replaces the text being edited', async ({ page, omo }) => {
    const n = await omo.createTicket({ title: 'Editing' })
    await omo.api('PUT', `/api/tickets/${n}/description`, { text: 'old text' })
    await omo.api('PUT', `/api/tickets/${n}/description`, { text: 'newer text' })
    await openBoard(page, omo)
    await openByClick(page, n)
    await panel(page).getByRole('button', { name: 'Edit', exact: true }).click()
    await panel(page).getByLabel('Description (Markdown)').fill('typed but not saved')
    await panel(page).getByRole('button', { name: 'History (2)' }).click()
    const dialog = page.getByRole('dialog', { name: `Description history of #${n}` })
    await dialog.locator('[data-version="1"]').click()
    await dialog.getByRole('button', { name: 'Restore this version' }).click()
    await expect(dialog.getByText('Restoring replaces the text you are editing. Restore anyway?')).toBeVisible()
    await dialog.getByRole('button', { name: 'Restore anyway' }).click()
    await expect(dialog.locator('[data-version]').first()).toContainText('restored from v1')
    await dialog.getByRole('button', { name: 'Done' }).click()
    await expect(panel(page).getByLabel('Description (Markdown)')).toHaveCount(0)
    await expect(panel(page).locator('[data-description]')).toHaveText('old text')
  })
})

test.describe('export', () => {
  test.use({
    omoOptions: {
      seed: {
        projects: [demo],
        tickets: [{ ref: 'review', type: 'task', title: 'Waiting for you', to: 'review', review: 'human', projects: ['demo'], branch: 'omo/review' }],
      },
    },
  })

  test('a seeded human review can be exported: the dialog explains it and a refusal shows inline', async ({ page, omo }) => {
    const n = omo.refs.review!
    await openBoard(page, omo)
    await openByClick(page, n)
    await panel(page).getByRole('button', { name: 'Export…' }).click()
    const dialog = page.getByRole('dialog', { name: `Export #${n}?` })
    await expect(dialog.locator('[data-approve-effect]')).toContainText('omo pushes the branch and opens a pull request')
    await expect(dialog).toContainText('The ticket is done once the pull request exists.')

    // Someone approves it meanwhile: omo refuses the second approval and the dialog shows why.
    await omo.api('POST', `/api/tickets/${n}/approve`, { merge: true })
    await dialog.getByRole('button', { name: 'Export', exact: true }).click()
    await expect(dialog.locator('[data-action-error]')).toContainText(`#${n} is already approved`)
    await expect(dialog).toBeVisible()
    await dialog.getByRole('button', { name: 'Not yet' }).click()
    await expect(dialog).toHaveCount(0)
  })
})
