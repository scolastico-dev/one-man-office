import AxeBuilder from '@axe-core/playwright'
import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import type { Page } from '@playwright/test'
import { openBoard } from './support/board'
import { expect, test, type Omo } from './support/omo'

const title = (page: Page) => page.locator('[data-settings-page] h1')
const row = (page: Page, name: string) => page.locator(`[data-prompt-row="${name}"]`)
const editor = (page: Page, name: string) => page.locator(`[data-prompt-editor="${name}"]`)
const saveBar = (page: Page) => page.getByRole('region', { name: 'Save changes' })

async function openPrompts(page: Page, omo: Omo) {
  await openBoard(page, omo)
  await page.goto(omo.url('/settings/prompts'))
  await expect(title(page)).toHaveText('Prompts')
  await expect(row(page, 'stalled')).toBeVisible()
}

async function openEditor(page: Page, name: string) {
  await row(page, name).locator('[data-prompt-toggle]').click()
  await expect(editor(page, name).locator('textarea')).toBeVisible()
  await expect(editor(page, name).locator('textarea')).toBeFocused()
}

async function violations(page: Page) {
  await page.evaluate(() => document.fonts.ready.then(() => undefined))
  const results = await new AxeBuilder({ page }).analyze()
  return results.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target.join(' ')) }))
}

const pageScrollsSideways = (page: Page) =>
  page.evaluate(() => {
    const root = document.documentElement
    return root.scrollWidth > root.clientWidth || document.body.scrollWidth > document.body.clientWidth
  })

/** The prompt files of the test's home: a hand edit writes one of them. */
function promptFile(omo: Omo, name: string): string {
  const dir = join(omo.home, 'omo', 'prompts')
  mkdirSync(dir, { recursive: true })
  return join(dir, `${name}.md`)
}

interface Stored {
  text: string
  customized: boolean
  revision: string
}

test('developer instructions saved show in the developer prompt preview', async ({ page, omo }) => {
  await openPrompts(page, omo)
  await expect(row(page, 'developer-instructions').locator('[data-prompt-state]')).toHaveText('Empty')
  await openEditor(page, 'developer-instructions')
  const ed = editor(page, 'developer-instructions')
  await ed.locator('textarea').fill('Run the tests of the package you changed.\nNever edit files under vendor/.')
  await ed.getByRole('button', { name: 'Preview' }).click()
  const text = ed.locator('[data-preview-text]')
  await expect(text).toContainText('Run the tests of the package you changed.')
  await expect(text).toContainText('You are Dana, the developer of task #12')

  await saveBar(page).getByRole('button', { name: 'Save' }).click()
  await expect(saveBar(page).locator('[data-save-state]')).toHaveText('Saved')
  await expect(row(page, 'developer-instructions').locator('[data-prompt-state]')).toHaveText('Set')
  const stored = await omo.api<Stored>('GET', '/api/prompts/developer-instructions')
  expect(stored.text).toBe('Run the tests of the package you changed.\nNever edit files under vendor/.')
  expect(stored.customized).toBe(true)

  // The preview of the developer's own prompt, as the server renders it with the saved instructions.
  const preview = await omo.api<{ text: string }>('POST', '/api/prompts/developer-instructions/preview', { text: stored.text })
  expect(preview.text).toContain('Never edit files under vendor/.')
  // The text is text: markup in it is not rendered.
  await ed.locator('textarea').fill('<b>bold</b> and **stars**')
  await expect(text).toContainText('<b>bold</b> and **stars**')
  await expect(text.locator('b')).toHaveCount(0)
})

test('a broken template is refused with the template message, and shown in the preview', async ({ page, omo }) => {
  await openPrompts(page, omo)
  await openEditor(page, 'stalled')
  const ed = editor(page, 'stalled')
  const broken = 'omo: {{.Silent}} {{nme .Ticket}}'
  await ed.locator('textarea').fill(broken)
  await ed.getByRole('button', { name: 'Preview' }).click()
  const serverSays = (await omo.api<{ error: string }>('POST', '/api/prompts/stalled/preview', { text: broken })).error
  expect(serverSays).toContain('"nme"')
  await expect(ed.locator('[data-preview-error]')).toContainText(serverSays)
  await expect(ed.locator('[data-preview-text]')).toHaveCount(0)

  await saveBar(page).getByRole('button', { name: 'Save' }).click()
  const field = ed.locator('.omo-field__error')
  await expect(field).toBeVisible()
  await expect(field).toHaveText(serverSays)
  await expect(ed.locator('textarea')).toHaveAttribute('aria-invalid', 'true')
  await expect(ed.locator('textarea')).toBeFocused()
  // Nothing was stored.
  expect((await omo.api<Stored>('GET', '/api/prompts/stalled')).customized).toBe(false)

  // A template the server accepts clears the error.
  await ed.locator('textarea').fill('omo: {{.Silent}} without output.')
  await expect(ed.locator('[data-preview-text]')).toContainText('omo: 15m without output.')
  await saveBar(page).getByRole('button', { name: 'Save' }).click()
  await expect(saveBar(page).locator('[data-save-state]')).toHaveText('Saved')
  await expect(field).toHaveCount(0)
})

test('reset returns to the default after asking', async ({ page, omo }) => {
  await openPrompts(page, omo)
  await openEditor(page, 'stalled')
  const ed = editor(page, 'stalled')
  const original = (await omo.api<{ default: string }>('GET', '/api/prompts/stalled')).default
  await ed.locator('textarea').fill('my own stalled text')
  await saveBar(page).getByRole('button', { name: 'Save' }).click()
  await expect(row(page, 'stalled').locator('[data-prompt-state]')).toHaveText('Customized')

  await ed.getByRole('button', { name: /^Show default/ }).click()
  await expect(ed.locator('[data-prompt-default] pre')).toHaveText(original)

  await ed.getByRole('button', { name: /^Reset to omo's default/ }).click()
  const dialog = page.getByRole('dialog', { name: "Reset to omo's default?" })
  await expect(dialog).toBeVisible()
  await dialog.getByRole('button', { name: 'Keep mine' }).click()
  await expect(ed.locator('textarea')).toHaveValue('my own stalled text')
  expect((await omo.api<Stored>('GET', '/api/prompts/stalled')).text).toBe('my own stalled text')

  await ed.getByRole('button', { name: /^Reset to omo's default/ }).click()
  await page.getByRole('dialog', { name: "Reset to omo's default?" }).getByRole('button', { name: 'Reset' }).click()
  await expect(ed.locator('textarea')).toHaveValue(original)
  await expect(row(page, 'stalled').locator('[data-prompt-state]')).toHaveText("omo's default")
  await expect(ed.getByRole('button', { name: /^Reset to omo's default/ })).toHaveCount(0)
  expect((await omo.api<Stored>('GET', '/api/prompts/stalled')).customized).toBe(false)
})

test('a hand edit of the prompt file while the page is open', async ({ page, omo }) => {
  await openPrompts(page, omo)
  await openEditor(page, 'developer-instructions')
  const ed = editor(page, 'developer-instructions')
  const file = promptFile(omo, 'developer-instructions')

  // Not edited in the page: the new text is shown, with the note.
  writeFileSync(file, 'Written by hand.\n')
  await expect(ed.locator('textarea')).toHaveValue('Written by hand.\n', { timeout: 15_000 })
  await expect(ed.locator('[data-changed-on-disk]')).toContainText('Changed on disk at')
  await expect(ed.locator('[data-changed-on-disk]')).toContainText('not through omo')
  await expect(row(page, 'developer-instructions').locator('[data-prompt-state]')).toHaveText('Set')

  // Edited in the page: the draft stays, the new value is offered, and a save does not overwrite it unseen.
  await ed.locator('textarea').fill('My unsaved text.')
  writeFileSync(file, 'Written by hand again.\n')
  await expect(saveBar(page).locator('[data-save-state]')).toHaveText('Changed outside this page', { timeout: 15_000 })
  await expect(ed.locator('textarea')).toHaveValue('My unsaved text.')
  await saveBar(page).getByRole('button', { name: 'Save' }).click()
  await expect(saveBar(page).locator('[data-save-error]')).toContainText('Use the new value')
  expect((await omo.api<Stored>('GET', '/api/prompts/developer-instructions')).text).toBe('Written by hand again.\n')

  await saveBar(page).getByRole('button', { name: 'Use the new value' }).click()
  await expect(ed.locator('textarea')).toHaveValue('Written by hand again.\n')
  await expect(saveBar(page).locator('[data-save-state]')).toHaveText('No unsaved changes')
})

test('opening another prompt with unsaved changes asks first', async ({ page, omo }) => {
  await openPrompts(page, omo)
  await openEditor(page, 'developer-instructions')
  await editor(page, 'developer-instructions').locator('textarea').fill('unsaved')

  await row(page, 'stalled').locator('[data-prompt-toggle]').click()
  const dialog = page.getByRole('dialog', { name: 'Leave without saving?' })
  await expect(dialog).toBeVisible()
  await dialog.getByRole('button', { name: 'Stay' }).click()
  await expect(editor(page, 'developer-instructions').locator('textarea')).toHaveValue('unsaved')
  await expect(editor(page, 'stalled')).toHaveCount(0)

  await row(page, 'stalled').locator('[data-prompt-toggle]').click()
  await page.getByRole('dialog', { name: 'Leave without saving?' }).getByRole('button', { name: 'Leave' }).click()
  await expect(editor(page, 'stalled').locator('textarea')).toBeVisible()
  await expect(editor(page, 'developer-instructions')).toHaveCount(0)
  expect((await omo.api<Stored>('GET', '/api/prompts/developer-instructions')).customized).toBe(false)
})

test.describe('at 390 px', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('a prompt can be read, edited and saved without scrolling the page sideways', async ({ page, omo }) => {
    await openPrompts(page, omo)
    expect(await pageScrollsSideways(page)).toBe(false)
    await openEditor(page, 'developer')
    const ed = editor(page, 'developer')
    await ed.getByRole('button', { name: 'Preview' }).click()
    await expect(ed.locator('[data-preview-text]')).toContainText('You are Dana')
    await ed.getByRole('button', { name: /^Show default/ }).click()
    await expect(ed.locator('[data-prompt-default] pre')).toContainText('You are {{.Name}}')
    await ed.locator('details summary').click()
    await expect(ed.locator('[data-template-fields]')).toContainText('StoryTitle')
    expect(await pageScrollsSideways(page)).toBe(false)
    // The long texts scroll inside their own boxes, not the page.
    const box = await ed.locator('[data-preview-text]').boundingBox()
    expect(box!.width).toBeLessThanOrEqual(390)

    await ed.locator('textarea').fill(`${'long '.repeat(400)}`)
    await saveBar(page).getByRole('button', { name: 'Save' }).click()
    await expect(saveBar(page).locator('[data-save-state]')).toHaveText('Saved')
    expect(await pageScrollsSideways(page)).toBe(false)
  })
})

for (const scheme of ['dark', 'light'] as const) {
  for (const [width, height] of [
    [1440, 900],
    [390, 844],
  ] as const) {
    test.describe(`${scheme} theme at ${width} px`, () => {
      test.use({ colorScheme: scheme, viewport: { width, height } })

      test('the Prompts page has no accessibility violations', async ({ page, omo }) => {
        await openPrompts(page, omo)
        expect(await violations(page)).toEqual([])

        await openEditor(page, 'stalled')
        const ed = editor(page, 'stalled')
        await ed.getByRole('button', { name: 'Preview' }).click()
        await expect(ed.locator('[data-preview-text]')).toBeVisible()
        await ed.getByRole('button', { name: /^Show default/ }).click()
        await expect(ed.locator('[data-prompt-default]')).toBeVisible()
        await ed.locator('details summary').click()
        expect(await violations(page)).toEqual([])

        // With a refused template: the error under the field and in the preview.
        await ed.locator('textarea').fill('{{nme}}')
        await expect(ed.locator('[data-preview-error]')).toBeVisible()
        await saveBar(page).getByRole('button', { name: 'Save' }).click()
        await expect(ed.locator('.omo-field__error')).toBeVisible()
        expect(await violations(page)).toEqual([])
        expect(await pageScrollsSideways(page)).toBe(false)
      })
    })
  }
}
