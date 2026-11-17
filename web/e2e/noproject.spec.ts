// A task without a project: created with the explicit "No project" choice
// in the create dialog (checked with axe in both themes), shown with a quiet
// "no project" marker on its card, run by the fake agent from Ready to Done
// in its working folder, and shown without Diff, branch and base branch but
// with the folder's path. With OMO_SCREENSHOTS=<folder> the tests also write
// noproject-{create,card,ticket}-<theme>-1440.png there.
import { existsSync, mkdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import AxeBuilder from '@axe-core/playwright'
import type { Page } from '@playwright/test'
import { card, expectCardIn, openBoard } from './support/board'
import { expectNoAgentProcesses, script } from './support/agents'
import { expect, test } from './support/omo'

const SHOTS = process.env.OMO_SCREENSHOTS ?? ''

async function shot(page: Page, name: string) {
  if (SHOTS === '') return
  mkdirSync(SHOTS, { recursive: true })
  await page.screenshot({ path: join(SHOTS, name) })
}

const createDialog = (page: Page) => page.getByRole('dialog', { name: 'Create ticket' })

for (const theme of ['dark', 'light'] as const) {
  test.describe(`${theme} theme`, () => {
    test.use({ colorScheme: theme, viewport: { width: 1440, height: 900 } })

    test('No project is an explicit choice in the create dialog; the card says "no project"', async ({ page, omo }) => {
      await openBoard(page, omo)
      await page.getByRole('button', { name: 'Create ticket' }).click()
      const dialog = createDialog(page)
      await expect(dialog).toBeVisible()
      await dialog.getByRole('radio', { name: 'Task' }).check({ force: true })
      await dialog.getByLabel('Title').fill('Compare the caches')
      // The only project is preselected; No project has to be chosen.
      const none = dialog.getByRole('radio', { name: /^No project/ })
      await expect(none).not.toBeChecked()
      await dialog.getByText('More options').click()
      await expect(dialog.getByLabel('When approved')).toBeVisible()
      await none.check()
      await expect(dialog.getByLabel('When approved')).toHaveCount(0)
      await expect(dialog.locator('[data-field="projects"]')).toContainText('Approving it merges and exports nothing')
      await page.evaluate(() => document.fonts.ready.then(() => undefined))
      const results = await new AxeBuilder({ page }).include('dialog[open]').analyze()
      expect(results.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target.join(' ')) }))).toEqual([])
      await shot(page, `noproject-create-${theme}-1440.png`)

      await dialog.getByRole('button', { name: 'Create', exact: true }).click()
      await expect(dialog).toBeHidden()
      await expectCardIn(page, 1, 'Planning')
      await expect(card(page, 1).locator('[data-chip="no-project"]')).toHaveText('no project')
      const server = await omo.api<{ projects: unknown[]; when_approved: unknown }>('GET', '/api/tickets/1')
      expect(server.projects).toEqual([])
      expect(server.when_approved).toBeNull()
      await shot(page, `noproject-card-${theme}-1440.png`)
    })
  })
}

test.describe('with the fake agent', () => {
  test.use({
    viewport: { width: 1440, height: 900 },
    omoOptions: {
      agents: {
        developer: script('ready', 'write|findings-$TICKET.md|LRU wins', 'done|the findings are in findings-$TICKET.md', 'wait-loop|10m'),
        reviewer: script('ready', 'verdict|approve|the findings hold', 'wait-loop|10m'),
      },
    },
  })

  test('a task without a project runs to Done in its working folder; the ticket shows the folder, no Diff and no branch', async ({ page, omo, repo }) => {
    const headBefore = readFileSync(join(repo, '.git', 'HEAD'), 'utf8')
    const n = await omo.createTicket({ title: 'Research the caches', projects: [] })
    await openBoard(page, omo)
    await omo.moveTicket(n, 'ready')
    // The reviewer approves, then the user (a fresh home's default for a task).
    await expectCardIn(page, n, 'Review')
    await expect(card(page, n)).toContainText('your review')
    await page.goto(omo.url(`/${n}`))
    await page.locator('[data-ticket-panel] [data-action="approve"]').click()
    const approve = page.getByRole('dialog', { name: `Approve #${n}?` })
    await expect(approve).toContainText('nothing to merge or export')
    await approve.getByRole('button', { name: 'Approve', exact: true }).click()
    await expect(approve).toBeHidden()
    await expectCardIn(page, n, 'Done')
    const dir = join(omo.home, 'omo', 'workdirs', String(n))
    expect(readFileSync(join(dir, `findings-${n}.md`), 'utf8')).toBe('LRU wins\n')
    expect(readFileSync(join(repo, '.git', 'HEAD'), 'utf8')).toBe(headBefore)

    for (const path of [`/${n}`, `/ticket/${n}`]) {
      await page.goto(omo.url(path))
      const view = page.locator(path.startsWith('/ticket') ? '[data-ticket-page]' : '[data-ticket-panel]')
      await expect(view.locator('[data-workdir-path]').first()).toHaveText(dir)
      await expect(view.getByRole('tab', { name: 'Diff' })).toHaveCount(0)
      await expect(view.getByRole('tab', { name: 'Comments' })).toHaveCount(1)
      for (const f of ['branch', 'base_branch', 'when_approved']) await expect(view.locator(`[data-field="${f}"]`)).toHaveCount(0)
      await expect(view.locator('[data-field="projects"]').first()).toContainText('No project')
      if (path.startsWith('/ticket')) {
        await page.evaluate(() => document.fonts.ready.then(() => undefined))
        await view.locator('[data-field="workdir"]').first().scrollIntoViewIfNeeded()
        for (const theme of ['dark', 'light'] as const) {
          await page.emulateMedia({ colorScheme: theme })
          await shot(page, `noproject-ticket-${theme}-1440.png`)
        }
      }
    }

    // The folder stays after Done until it is removed from the ticket.
    await page.goto(omo.url(`/${n}`))
    const panel = page.locator('[data-ticket-panel]')
    await panel.locator('[data-remove-workdir]').click()
    const confirm = page.getByRole('dialog', { name: `Remove the working folder of #${n}?` })
    await confirm.getByRole('button', { name: 'Remove folder' }).click()
    await expect(confirm).toBeHidden()
    await expect(panel.locator('[data-workdir-none]')).toHaveText('Removed, or never made')
    expect(existsSync(dir)).toBe(false)
    await expectNoAgentProcesses(omo)
  })
})
