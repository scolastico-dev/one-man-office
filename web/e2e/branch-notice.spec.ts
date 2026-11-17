import { execFileSync } from 'node:child_process'
import { realpathSync } from 'node:fs'
import { devNull, tmpdir } from 'node:os'
import { join } from 'node:path'
import AxeBuilder from '@axe-core/playwright'
import type { Page } from '@playwright/test'
import { openBoard } from './support/board'
import { initRepo } from './support/git'
import { expect, test } from './support/omo'

const createDialog = (page: Page) => page.getByRole('dialog', { name: 'Create ticket' })

/** A repository with `main` and `feature`, checked out on `feature`. */
function repoOnFeature(): string {
  const dir = join(realpathSync(tmpdir()), `omo-e2e-feature-${process.pid}-${Date.now()}`)
  initRepo(dir)
  const env = { ...process.env, GIT_CONFIG_GLOBAL: devNull, GIT_CONFIG_SYSTEM: devNull }
  execFileSync('git', ['checkout', '-q', '-b', 'feature'], { cwd: dir, env, stdio: 'pipe' })
  return dir
}

test('the create dialog warns about a project on a non-default branch and not about one on main', async ({ page, omo }) => {
  const dir = repoOnFeature()
  try {
    // The fixture's project `demo` is on main.
    await omo.api('POST', '/api/projects', { path: dir, name: 'onfeature' })
    await openBoard(page, omo)
    await page.getByRole('button', { name: 'Create ticket' }).click()
    const dialog = createDialog(page)
    await expect(dialog).toBeVisible()
    await dialog.getByRole('radio', { name: 'Task' }).check({ force: true })

    await dialog.getByRole('radio', { name: /demo/ }).check({ force: true })
    await expect(dialog.locator('[data-branch-notice]')).toHaveCount(0)

    await dialog.getByRole('radio', { name: /onfeature/ }).check({ force: true })
    const notice = dialog.locator('[data-branch-notice]')
    await expect(notice).toHaveCount(1)
    await expect(notice).toHaveText('onfeature is on feature, not its default branch main. The ticket will be based on feature.')
    const violations = (await new AxeBuilder({ page }).analyze()).violations.map((v) => v.id)
    expect(violations).toEqual([])

    // A notice never blocks creating.
    await dialog.getByLabel('Title').fill('Based on feature')
    await dialog.getByRole('button', { name: 'Create', exact: true }).click()
    await expect(dialog).toBeHidden()
    await page.keyboard.press('Escape')

    await page.getByRole('link', { name: 'Settings', exact: true }).click()
    const list = page.locator('section[data-settings-page]').filter({ has: page.getByRole('heading', { level: 1, name: 'Projects' }) })
    await expect(list.getByRole('row', { name: /onfeature/ })).toContainText('Not its default branch main.')
    await expect(list.getByRole('row', { name: /demo/ })).not.toContainText('Not its default branch')
  } finally {
    execFileSync('rm', ['-rf', dir])
  }
})
