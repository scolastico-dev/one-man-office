import AxeBuilder from '@axe-core/playwright'
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import type { Page } from '@playwright/test'
import { openBoard } from './support/board'
import { expect, test, type Omo } from './support/omo'
import { readZip, writeZip } from './support/zip'

const SHOTS = process.env.OMO_SCREENSHOTS ?? ''
const title = (page: Page) => page.locator('[data-settings-page] h1')
const dialog = (page: Page) => page.getByRole('dialog', { name: 'Import settings' })

async function openBackup(page: Page, omo: Omo) {
  await openBoard(page, omo)
  await page.goto(omo.url('/settings/backup'))
  await expect(title(page)).toHaveText('Backup')
}

/** Clicks Export and returns the saved download's name and entries. */
async function exportZip(page: Page, dir: string): Promise<{ name: string; file: string; entries: Map<string, Buffer> }> {
  const download = page.waitForEvent('download')
  await page.locator('[data-export]').click()
  const d = await download
  const file = join(dir, d.suggestedFilename())
  await d.saveAs(file)
  return { name: d.suggestedFilename(), file, entries: readZip(readFileSync(file)) }
}

async function violations(page: Page) {
  await page.evaluate(async () => {
    await document.fonts.ready
    await Promise.all(document.getAnimations().map((a) => a.finished.catch(() => undefined)))
  })
  const results = await new AxeBuilder({ page }).analyze()
  return results.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target.join(' ')) }))
}

test('export downloads a zip with the settings, and with the database when ticked', async ({ page, omo }) => {
  const dir = join(omo.home, 'downloads')
  mkdirSync(dir, { recursive: true })
  await openBackup(page, omo)
  await expect(page.getByText('The file contains your tokens and environment values; keep it private.')).toBeVisible()

  const plain = await exportZip(page, dir)
  expect(plain.name).toMatch(/^omo-settings-\d{4}-\d{2}-\d{2}\.zip$/)
  const names = [...plain.entries.keys()]
  expect(names).toContain('manifest.json')
  expect(names).toContain('settings.yaml')
  expect(names).toContain(`projects/${omo.project}.yaml`)
  expect(names).not.toContain('omo.db')
  expect(names.every((n) => n === 'manifest.json' || /^(settings|server)\.yaml$|^(projects|profiles|roles|prompts|usage|skillsets|plugins)\/[^/]+$/.test(n))).toBe(true)
  const manifest = JSON.parse(plain.entries.get('manifest.json')!.toString('utf8'))
  expect(manifest).toMatchObject({ format: 1, schema_version: expect.any(Number), database: false })

  await page.getByLabel('Include the database (tickets, comments, history)').check()
  const withDB = await exportZip(page, dir)
  expect([...withDB.entries.keys()]).toContain('omo.db')
  expect(withDB.entries.get('omo.db')!.subarray(0, 15).toString('latin1')).toBe('SQLite format 3')
  expect(JSON.parse(withDB.entries.get('manifest.json')!.toString('utf8')).database).toBe(true)
})

test('import of a changed zip shows what it replaces and, once confirmed, the setting changes', async ({ page, omo }) => {
  const dir = join(omo.home, 'downloads')
  mkdirSync(dir, { recursive: true })
  await openBackup(page, omo)
  const exported = await exportZip(page, dir)
  // The zip, changed by hand: another limit for tasks in work.
  exported.entries.set('settings.yaml', Buffer.from('limits:\n  tasks: 7\n'))
  const changed = join(dir, 'changed.zip')
  writeFileSync(changed, writeZip(exported.entries))

  await page.locator('[data-import-file]').setInputFiles(changed)
  await expect(dialog(page)).toBeVisible()
  await expect(dialog(page).locator('[data-import-replace]')).toContainText('settings.yaml')
  expect(await violations(page), 'dialog').toEqual([])
  // Nothing changed before the confirmation.
  expect(readFileSync(join(omo.home, 'omo', 'settings.yaml'), 'utf8')).not.toContain('tasks: 7')
  await dialog(page).getByRole('button', { name: 'Import', exact: true }).click()
  await expect(dialog(page)).toBeHidden()
  await expect(page.locator('[data-import-result]')).toContainText('Imported 1 file.')
  await expect(page.locator('[data-import-result]')).toContainText(join(omo.home, 'omo', 'backups'))

  await page.goto(omo.url('/settings/limits'))
  await expect(page.getByRole('spinbutton', { name: 'Tasks in work' })).toHaveValue('7')
})

test('a broken zip is refused with the reason and nothing changes', async ({ page, omo }) => {
  const bad = join(omo.home, 'bad.zip')
  writeFileSync(bad, writeZip(new Map([['manifest.json', Buffer.from('{"format":1,"omo_version":"dev","schema_version":6,"created_at":"2026-11-17T10:00:00Z","database":false}')], ['../settings.yaml', Buffer.from('x')]])))
  await openBackup(page, omo)
  await page.locator('[data-import-file]').setInputFiles(bad)
  await expect(page.locator('[data-import-error]')).toContainText("outside omo's home")
  await expect(dialog(page)).toBeHidden()
})

test('the database goes in only when ticked, with a warning, and waits for a restart', async ({ page, omo }) => {
  const dir = join(omo.home, 'downloads')
  mkdirSync(dir, { recursive: true })
  await openBackup(page, omo)
  await page.getByLabel('Include the database (tickets, comments, history)').check()
  const zipped = await exportZip(page, dir)
  await page.locator('[data-import-file]').setInputFiles(zipped.file)
  await expect(dialog(page)).toBeVisible()
  // The same settings and no choice made: nothing to import.
  await expect(dialog(page).getByRole('button', { name: 'Import', exact: true })).toBeDisabled()
  await dialog(page).getByLabel('Also replace the database').check()
  await expect(dialog(page).locator('[data-import-database-warning]')).toContainText('replaces all tickets')
  await dialog(page).getByRole('button', { name: 'Import', exact: true }).click()
  await expect(page.locator('[data-import-result]')).toContainText('omo is paused. Restart omo')
  expect(existsSync(join(omo.home, 'omo', 'omo.db.import'))).toBe(true)
})

for (const width of [1440, 390] as const) {
  for (const colorScheme of ['dark', 'light'] as const) {
    test(`the page and the import dialog at ${width} px, ${colorScheme} theme, have no violations`, async ({ page, omo }) => {
      await page.setViewportSize({ width, height: width === 390 ? 844 : 900 })
      await page.emulateMedia({ colorScheme, reducedMotion: 'reduce' })
      const dir = join(omo.home, 'downloads')
      mkdirSync(dir, { recursive: true })
      await openBackup(page, omo)
      expect(await violations(page), 'page').toEqual([])
      if (SHOTS !== '') {
        mkdirSync(SHOTS, { recursive: true })
        await page.screenshot({ path: join(SHOTS, `backup-${colorScheme}-${width}-page.png`) })
      }
      await page.getByLabel('Include the database (tickets, comments, history)').check()
      const zipped = await exportZip(page, dir)
      zipped.entries.set('settings.yaml', Buffer.from('limits:\n  tasks: 5\n'))
      zipped.entries.set('prompts/developer-instructions.md', Buffer.from('Write tests first.\n'))
      const changed = join(dir, 'changed.zip')
      writeFileSync(changed, writeZip(zipped.entries))
      await page.locator('[data-import-file]').setInputFiles(changed)
      await expect(dialog(page)).toBeVisible()
      await dialog(page).getByLabel('Also replace the database').check()
      await expect(dialog(page).locator('[data-import-database-warning]')).toBeVisible()
      expect(await violations(page), 'dialog').toEqual([])
      expect(await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth)).toBe(false)
      if (SHOTS !== '') await page.screenshot({ path: join(SHOTS, `backup-${colorScheme}-${width}-dialog.png`) })
    })
  }
}
