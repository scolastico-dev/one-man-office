import AxeBuilder from '@axe-core/playwright'
import { readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import type { Page } from '@playwright/test'
import { openBoard } from './support/board'
import { expect, FAKE_AGENT_BINARY, test, type Omo } from './support/omo'

// The usage scripts here never reach a provider: they read a JSON file the
// test writes into its own temporary home (`~` of the server is that home).
const USAGE_FILE = 'fake-usage.json'
const SCRIPT = `function usage(account)
  local raw, err = omo.read_file("~/${USAGE_FILE}")
  if not raw then error("no usage file: " .. err) end
  local u = omo.json_decode(raw)
  omo.log("read the usage file for " .. account.profile)
  return {
    { name = "a", used = u.a },
    { name = "b", used = u.b },
  }
end
`
const BROKEN = 'function usage(account)\n  return {\nend\n'

interface StoredUsage {
  soft_percent: number
  hard_percent: number
  refresh_interval: string
  windows: Record<string, { soft_percent?: number; hard_percent?: number }>
}

const writeUsage = (omo: Omo, a: number, b: number) => writeFileSync(join(omo.home, USAGE_FILE), JSON.stringify({ a, b }))
const scriptFile = (omo: Omo, name: string) => join(omo.home, 'omo', 'usage', `${name}.lua`)
const storedUsage = async (omo: Omo) => (await omo.api<{ settings: { usage: StoredUsage } }>('GET', '/api/settings')).settings.usage

/** A script written the way another client would: created with If-Match "0". */
async function putScript(omo: Omo, name: string, text: string) {
  const res = await fetch(`${omo.base}/api/usage/scripts/${name}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${omo.token}`, 'If-Match': '"0"' },
    body: JSON.stringify({ text }),
  })
  expect(res.status, await res.text()).toBe(200)
}

/** A profile running the fake agent, without a usage script. */
async function putProfile(omo: Omo, name = 'fake') {
  await omo.api('PUT', `/api/profiles/${name}`, {
    cli: 'custom',
    command: FAKE_AGENT_BINARY,
    args: [],
    env: {},
    account_dir: '',
    usage_script: '',
    prompt_delivery: {},
    models: { default: { args: [], env: {}, usage_windows: [], description: 'scripted fake agent' } },
  })
}

const title = (page: Page) => page.locator('[data-settings-page] h1')
const limits = (page: Page) => page.locator('[data-usage-limits]')
const limitsBar = (page: Page) => page.getByRole('region', { name: 'Save changes to the limits' })
const row = (page: Page, name: string) => page.locator(`[data-script="${name}"]`)
const editor = (page: Page, name: string) => page.locator(`[data-script-editor="${name}"]`)
const scriptBar = (page: Page, name: string) => page.getByRole('region', { name: `Save changes to the script ${name}` })
const saveState = (bar: ReturnType<typeof limitsBar>) => bar.locator('[data-save-state]')

async function openUsage(page: Page, omo: Omo) {
  await openBoard(page, omo)
  await page.goto(omo.url('/settings/usage'))
  await expect(title(page)).toHaveText('Usage')
  await expect(limits(page).getByLabel('Soft limit, %')).toBeVisible()
  await expect(row(page, 'claude')).toBeVisible()
}

async function openScript(page: Page, name: string) {
  await row(page, name).locator('[data-script-toggle]').click()
  await expect(editor(page, name).locator('[data-script-text]')).toBeVisible()
}

async function newScript(page: Page, name: string) {
  await page.getByRole('button', { name: 'New script…' }).click()
  const dialog = page.getByRole('dialog', { name: 'New script' })
  await dialog.getByLabel('Name of the script').fill(name)
  await dialog.getByRole('button', { name: 'Create' }).click()
  await expect(editor(page, name)).toContainText(`Script ${name} (not saved yet)`)
}

async function testAs(page: Page, name: string, profile: string) {
  const ed = editor(page, name)
  await ed.getByLabel('Run as').selectOption(profile)
  await ed.locator('[data-test-run]').click()
  await expect(ed.locator('[data-test-result]')).toBeVisible()
}

/** Types a window name for row n under "Other…" (no script has reported it). */
async function typeWindow(page: Page, n: number, name: string) {
  await limits(page).getByLabel(`Window ${n}`, { exact: true }).selectOption({ label: 'Other…' })
  await limits(page).getByLabel(`Name of window ${n}`).fill(name)
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

test('the limits survive a reload; values out of bounds are refused under their field', async ({ page, omo }) => {
  await openUsage(page, omo)
  const form = limits(page)
  await form.getByLabel('Soft limit, %').fill('80')
  await form.getByLabel('Hard limit, %').fill('95')
  await form.getByLabel('Refresh every').fill('10m')
  await form.getByRole('button', { name: 'Add window' }).click()
  await typeWindow(page, 1, 'weekly')
  await form.getByLabel('Soft limit of weekly of every script').fill('70')
  await limitsBar(page).getByRole('button', { name: 'Save', exact: true }).click()
  await expect(saveState(limitsBar(page))).toHaveText('Saved')
  expect(await storedUsage(omo)).toEqual({ soft_percent: 80, hard_percent: 95, refresh_interval: '10m', windows: { weekly: { soft_percent: 70 } } })

  await page.reload()
  await expect(form.getByLabel('Soft limit, %')).toHaveValue('80')
  await expect(form.getByLabel('Hard limit, %')).toHaveValue('95')
  await expect(form.getByLabel('Refresh every')).toHaveValue('10m')
  await expect(form.getByLabel('Window 1', { exact: true })).toHaveValue('weekly')
  await expect(form.getByLabel('Window 1', { exact: true }).locator('option:checked')).toHaveText('weekly (not reported now)')
  await expect(form.getByLabel('Soft limit of weekly of every script')).toHaveValue('70')
  await expect(form.getByLabel('Hard limit of weekly of every script')).toHaveValue('')

  // Text that is no number blocks Save; nothing is sent.
  await form.getByLabel('Hard limit of weekly of every script').fill('9x')
  await limitsBar(page).getByRole('button', { name: 'Save', exact: true }).click()
  await expect(limitsBar(page)).toContainText('Fix the fields marked above before saving.')
  await expect(form.getByLabel('Hard limit of weekly of every script')).toHaveAttribute('aria-invalid', 'true')
  await form.getByLabel('Hard limit of weekly of every script').fill('')

  // The hard limit below the soft one: the server's words under the field.
  await form.getByLabel('Hard limit, %').fill('70')
  await limitsBar(page).getByRole('button', { name: 'Save', exact: true }).click()
  const hard = form.locator('[data-field="usage.hard_percent"]')
  await expect(hard).toHaveAttribute('aria-invalid', 'true')
  await expect(hard).toBeFocused()
  await expect(form.locator('.omo-field', { has: page.locator('[data-field="usage.hard_percent"]') })).toContainText('usage.hard_percent must be between usage.soft_percent and 100')

  // A window's own limit out of bounds is said before anything is sent, with what is wrong.
  await form.getByLabel('Hard limit, %').fill('95')
  await form.getByLabel('Soft limit of weekly of every script').fill('150')
  await limitsBar(page).getByRole('button', { name: 'Save', exact: true }).click()
  await expect(limitsBar(page)).toContainText('Fix the fields marked above before saving.')
  await expect(form.getByLabel('Soft limit of weekly of every script')).toHaveAttribute('aria-invalid', 'true')
  await expect(form.locator('[data-window-row="weekly"]')).toContainText('Enter a number from 1 to 100, or leave it empty.')

  // A window's limit the server refuses: its words under that window's field.
  await form.getByLabel('Soft limit of weekly of every script').fill('97')
  await limitsBar(page).getByRole('button', { name: 'Save', exact: true }).click()
  await expect(form.getByLabel('Soft limit of weekly of every script')).toHaveAttribute('aria-invalid', 'true')
  await expect(form.locator('[data-window-row="weekly"]')).toContainText("usage.windows.weekly.soft_percent must not be above usage.hard_percent, the window's hard threshold")
  await expect(form.locator('[data-field="usage.hard_percent"]')).not.toHaveAttribute('aria-invalid', 'true')

  // Removing the row sends the windows whole: the override is gone on disk.
  await form.getByRole('button', { name: 'Remove the limits of weekly' }).click()
  await limitsBar(page).getByRole('button', { name: 'Save', exact: true }).click()
  await expect(saveState(limitsBar(page))).toHaveText('Saved')
  expect((await storedUsage(omo)).windows).toEqual({})
})

test('a new script reading a file in the home shows its windows in Test', async ({ page, omo }) => {
  await putProfile(omo)
  writeUsage(omo, 42, 97)
  await openUsage(page, omo)
  await expect(page.locator('[data-settings-page]')).toContainText('It is code that runs as you, with the credentials of the profiles that use it')
  await newScript(page, 'fake-usage')
  const ed = editor(page, 'fake-usage')
  await expect(ed.locator('[data-script-text]')).toHaveValue(/function usage\(account\)/)
  await ed.locator('[data-script-text]').fill(SCRIPT)
  await expect(ed).toContainText('with its real credentials, as you')

  // The draft runs before it is saved.
  await testAs(page, 'fake-usage', 'fake')
  const result = ed.locator('[data-test-result]')
  await expect(result).toContainText(/Ran as fake · took \d+ ms/)
  const a = result.getByRole('meter', { name: 'a' })
  const b = result.getByRole('meter', { name: 'b' })
  await expect(a).toHaveAttribute('aria-valuenow', '42')
  await expect(b).toHaveAttribute('aria-valuenow', '97')
  await expect(result.locator('[data-test-window="b"]')).toContainText('near its limit')
  await expect(result.locator('[data-test-window="b"]')).toContainText('waits at 90 %, stops at 98 %')
  await expect(result.locator('[data-test-log]')).toHaveText('read the usage file for fake')

  await scriptBar(page, 'fake-usage').getByRole('button', { name: 'Save', exact: true }).click()
  await expect(saveState(scriptBar(page, 'fake-usage'))).toHaveText('Saved')
  await expect(ed).not.toContainText('(not saved yet)')
  await expect(row(page, 'fake-usage').locator('[data-script-kind]')).toHaveText('Yours')
  await expect(row(page, 'fake-usage').locator('[data-script-used-by]')).toHaveText('No profile uses it.')
  expect(readFileSync(scriptFile(omo, 'fake-usage'), 'utf8')).toBe(SCRIPT)

  // The file changes: the next test says so.
  writeUsage(omo, 99, 10)
  await ed.locator('[data-test-run]').click()
  await expect(result.getByRole('meter', { name: 'a' })).toHaveAttribute('aria-valuenow', '99')
  await expect(result.locator('[data-test-window="a"]')).toContainText('at its limit')
})

test('the labels a script gives its windows show in Test, under the limits, in the Agents tab and in the model editor', async ({ page, omo }) => {
  writeUsage(omo, 42, 97)
  const labelled = SCRIPT.replace('{ name = "a", used = u.a }', '{ name = "a", label = "Window A", used = u.a }')
  expect(labelled).not.toBe(SCRIPT)
  await putScript(omo, 'fake-usage', labelled)
  await putProfile(omo)
  const fake = (await omo.api<{ profiles: Record<string, unknown>[] }>('GET', '/api/profiles')).profiles.find((p) => p.name === 'fake')!
  await omo.api('PUT', '/api/profiles/fake', { ...fake, usage_script: 'fake-usage' })
  await expect
    .poll(async () => (await omo.api<{ readings: { windows: { name: string; label: string }[] }[] }>('GET', '/api/usage')).readings.flatMap((r) => r.windows))
    .toEqual([expect.objectContaining({ name: 'a', label: 'Window A' }), expect.objectContaining({ name: 'b', label: '' })])

  // Test: "label (name)".
  await openUsage(page, omo)
  await openScript(page, 'fake-usage')
  await testAs(page, 'fake-usage', 'fake')
  const result = editor(page, 'fake-usage').locator('[data-test-result]')
  await expect(result.getByRole('meter', { name: 'Window A (a)' })).toHaveAttribute('aria-valuenow', '42')
  await expect(result.getByRole('meter', { name: 'b', exact: true })).toHaveAttribute('aria-valuenow', '97')

  // The limits table offers the reported windows by label and script; the names of its fields say the same.
  await limits(page).getByRole('button', { name: 'Add window' }).click()
  const choice = limits(page).getByLabel('Window 1', { exact: true })
  await expect(choice.locator('option')).toHaveText(['Choose a window', 'Fake-usage: b', 'Fake-usage: Window A (a)', 'Other…'])
  await choice.selectOption({ label: 'Fake-usage: Window A (a)' })
  await expect(choice).toHaveValue('fake-usage/a')
  await expect(limits(page).getByRole('textbox', { name: 'Soft limit of Fake-usage: Window A (a)' })).toBeVisible()
  await limitsBar(page).getByRole('button', { name: 'Discard' }).click()

  // The Agents tab: the label on the meter, the name as its tooltip.
  await page.goto(omo.url('/'))
  const side = page.locator('[data-sidebar]')
  await side.getByRole('tab', { name: /^Agents/ }).click()
  const meterA = page.locator('[data-usage] [data-usage-window="a"]')
  await expect(meterA.getByRole('meter', { name: 'Window A' })).toBeVisible()
  await expect(meterA.locator('.umeter__label')).toHaveAttribute('title', 'a')
  await expect(page.locator('[data-usage] [data-usage-window="b"]').getByRole('meter', { name: 'b', exact: true })).toBeVisible()

  // The model editor names the windows the script reports.
  await page.goto(omo.url('/settings/agents'))
  await page.locator('[data-profile="fake"]').click()
  await expect(page.locator('[data-profile-editor="fake"] [data-model-editor="default"]')).toContainText('Its script reports now: Window A (a); b.')
})

test('a broken script is refused with the line number and fails on Test', async ({ page, omo }) => {
  await putProfile(omo)
  await openUsage(page, omo)
  await newScript(page, 'broken')
  const ed = editor(page, 'broken')
  await ed.locator('[data-script-text]').fill(BROKEN)

  await testAs(page, 'broken', 'fake')
  await expect(ed.locator('[data-test-error]')).toContainText('The script failed')
  await expect(ed.locator('[data-test-error]')).toContainText(/line \d+/)
  await expect(ed.getByRole('meter')).toHaveCount(0)

  await scriptBar(page, 'broken').getByRole('button', { name: 'Save', exact: true }).click()
  const text = ed.locator('[data-script-text]')
  await expect(text).toHaveAttribute('aria-invalid', 'true')
  await expect(text).toBeFocused()
  await expect(ed.locator('.omo-field', { has: page.locator('[data-script-text]') }).locator('.omo-field__error')).toContainText(/line \d+/)
  await expect(ed).toContainText('(not saved yet)')
  const scripts = await omo.api<{ name: string }[]>('GET', '/api/usage/scripts')
  expect(scripts.map((s) => s.name)).not.toContain('broken')

  // A script without a usage function is refused too.
  await text.fill('local x = 1\n')
  await scriptBar(page, 'broken').getByRole('button', { name: 'Save', exact: true }).click()
  await expect(ed.locator('.omo-field', { has: page.locator('[data-script-text]') }).locator('.omo-field__error')).toContainText('usage')
})

test("a customized default script is reset to omo's default", async ({ page, omo }) => {
  await openUsage(page, omo)
  await expect(row(page, 'claude').locator('[data-script-kind]')).toHaveText("omo's default")
  await openScript(page, 'claude')
  const ed = editor(page, 'claude')
  const original = await ed.locator('[data-script-text]').inputValue()
  await expect(ed.locator('[data-script-reset]')).toHaveCount(0)
  await ed.getByRole('button', { name: 'Show default' }).click()
  await expect(ed.locator('[data-script-default]')).toContainText('function usage')

  await ed.locator('[data-script-text]').fill(`-- mine\n${original}`)
  await scriptBar(page, 'claude').getByRole('button', { name: 'Save', exact: true }).click()
  await expect(saveState(scriptBar(page, 'claude'))).toHaveText('Saved')
  await expect(row(page, 'claude').locator('[data-script-kind]')).toHaveText('Customized')

  await ed.getByRole('button', { name: "Reset to omo's default…" }).click()
  const dialog = page.getByRole('dialog', { name: "Reset to omo's default?" })
  await dialog.getByRole('button', { name: 'Keep mine' }).click()
  await expect(row(page, 'claude').locator('[data-script-kind]')).toHaveText('Customized')
  await ed.getByRole('button', { name: "Reset to omo's default…" }).click()
  await page.getByRole('dialog', { name: "Reset to omo's default?" }).getByRole('button', { name: 'Reset' }).click()
  await expect(row(page, 'claude').locator('[data-script-kind]')).toHaveText("omo's default")
  await expect(ed.locator('[data-script-text]')).toHaveValue(original)
  await expect(ed.locator('[data-script-reset]')).toHaveCount(0)
})

test('a script a profile uses cannot be deleted; the profile and its model count its windows', async ({ page, omo }) => {
  await putProfile(omo)
  writeUsage(omo, 42, 97)
  await putScript(omo, 'fake-usage', SCRIPT)

  // The profile chooses the script, its model the window a.
  await openBoard(page, omo)
  await page.goto(omo.url('/settings/agents'))
  await page.locator('[data-profile="fake"]').click()
  const profile = page.locator('[data-profile-editor="fake"]')
  const select = profile.getByLabel('Usage script')
  await expect(select.locator('option')).toHaveText(['None: no usage readings', 'claude', 'codex', 'fake-usage'])
  await expect(profile).toContainText('Without a script')
  await select.selectOption('fake-usage')
  await expect(profile).toContainText('omo runs it every 5m.')
  const windows = profile.locator('[data-model-editor="default"]').getByRole('group', { name: 'Usage windows that count' })
  await expect(profile.locator('[data-model-editor="default"]')).toContainText('Empty: every window of the script counts except extra_usage (paid usage beyond the plan).')
  await windows.getByRole('button', { name: 'Add window' }).click()
  await windows.getByRole('textbox').nth(0).fill('Not A Window')
  const bar = page.getByRole('region', { name: 'Save changes' })
  await bar.getByRole('button', { name: 'Save', exact: true }).click()
  // The server names the row it refuses.
  await expect(windows.getByRole('textbox').nth(0)).toHaveAttribute('aria-invalid', 'true')
  await expect(profile.locator('[data-model-editor="default"]')).toContainText('models.default.usage_windows.0 must be a window name')
  await windows.getByRole('textbox').nth(0).fill('a')
  await bar.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(bar.locator('[data-save-state]')).toHaveText('Saved')
  const stored = (await omo.api<{ profiles: { name: string; usage_script: string; models: Record<string, { usage_windows: string[] }> }[] }>('GET', '/api/profiles')).profiles
  expect(stored[0]).toMatchObject({ usage_script: 'fake-usage', models: { default: { usage_windows: ['a'] } } })

  // omo runs the script for the profile and judges the model by window a only.
  await expect
    .poll(async () => (await omo.api<{ models: { model: string; state: string; window: string; used_percent: number | null }[] }>('GET', '/api/usage')).models.find((m) => m.model === 'fake/default'))
    .toMatchObject({ state: 'ok', window: 'a', used_percent: 42 })

  // The Usage page says who uses the script, and refuses to delete it with the server's words.
  await page.goto(omo.url('/settings/usage'))
  await expect(row(page, 'fake-usage').locator('[data-script-used-by]')).toHaveText('Used by fake')
  await openScript(page, 'fake-usage')
  const ed = editor(page, 'fake-usage')
  // The profiles that use the script come first.
  await expect(ed.getByLabel('Run as').locator('option').first()).toHaveText('fake (uses this script)')
  await ed.getByRole('button', { name: 'Delete fake-usage…' }).click()
  await page.getByRole('dialog', { name: 'Delete the script fake-usage?' }).getByRole('button', { name: 'Delete' }).click()
  await expect(ed.locator('[data-reset-error]')).toContainText('fake')
  await expect(row(page, 'fake-usage')).toBeVisible()
})

test('focus goes to "New script…" when a new script is closed and when a script is deleted, and stays on the row when it is closed there', async ({ page, omo }) => {
  await putScript(omo, 'mine', SCRIPT)
  await openUsage(page, omo)
  await newScript(page, 'draft')
  await editor(page, 'draft').locator('[data-script-close]').click()
  // A script not saved yet is an unsaved change: closing asks first.
  await page.locator('[data-question-confirm]').click()
  await expect(editor(page, 'draft')).toHaveCount(0)
  await expect(page.locator('[data-new-script]')).toBeFocused()

  await openScript(page, 'mine')
  await row(page, 'mine').locator('[data-script-toggle]').click()
  await expect(editor(page, 'mine')).toHaveCount(0)
  await expect(row(page, 'mine').locator('[data-script-toggle]')).toBeFocused()

  await openScript(page, 'mine')
  await editor(page, 'mine').getByRole('button', { name: 'Delete mine…' }).click()
  await page.getByRole('dialog', { name: 'Delete the script mine?' }).getByRole('button', { name: 'Delete' }).click()
  await expect(row(page, 'mine')).toHaveCount(0)
  await expect(page.locator('[data-new-script]')).toBeFocused()
})

test('a hand edit of the script file while it is open is shown; with a draft it is a conflict', async ({ page, omo }) => {
  await putScript(omo, 'fake-usage', SCRIPT)
  await openUsage(page, omo)
  await openScript(page, 'fake-usage')
  const ed = editor(page, 'fake-usage')
  const text = ed.locator('[data-script-text]')
  await expect(text).toHaveValue(SCRIPT)

  writeFileSync(scriptFile(omo, 'fake-usage'), `-- by hand\n${SCRIPT}`)
  await expect(text).toHaveValue(`-- by hand\n${SCRIPT}`)
  await expect(ed.locator('[data-changed-on-disk]')).toContainText('not through omo')

  await text.fill(`-- a draft\n${SCRIPT}`)
  writeFileSync(scriptFile(omo, 'fake-usage'), `-- by hand again\n${SCRIPT}`)
  await expect(saveState(scriptBar(page, 'fake-usage'))).toHaveText('Changed outside this page')
  await expect(text).toHaveValue(`-- a draft\n${SCRIPT}`)
  await ed.locator('[data-use-theirs]').click()
  await expect(text).toHaveValue(`-- by hand again\n${SCRIPT}`)
})

test('switching to another script with unsaved changes asks first', async ({ page, omo }) => {
  await openUsage(page, omo)
  await openScript(page, 'claude')
  await editor(page, 'claude').locator('[data-script-text]').fill('-- unsaved\n')
  await row(page, 'codex').locator('[data-script-toggle]').click()
  const dialog = page.getByRole('dialog', { name: 'Leave without saving?' })
  await dialog.getByRole('button', { name: 'Stay' }).click()
  await expect(editor(page, 'claude')).toBeVisible()
  await row(page, 'codex').locator('[data-script-toggle]').click()
  await page.getByRole('dialog', { name: 'Leave without saving?' }).getByRole('button', { name: 'Leave' }).click()
  await expect(editor(page, 'codex')).toBeVisible()
  await expect(editor(page, 'claude')).toHaveCount(0)
})

test('at 390 px the page does not scroll sideways; long script lines scroll in their box', async ({ page, omo }) => {
  await putProfile(omo)
  writeUsage(omo, 42, 97)
  await page.setViewportSize({ width: 390, height: 844 })
  await openUsage(page, omo)
  await limits(page).getByRole('button', { name: 'Add window' }).click()
  await typeWindow(page, 1, 'a_rather_long_window_name_for_a_phone')
  expect(await pageScrollsSideways(page)).toBe(false)

  await newScript(page, 'fake-usage')
  const ed = editor(page, 'fake-usage')
  await ed.locator('[data-script-text]').fill(`-- ${'a very long comment line '.repeat(20)}\n${SCRIPT}`)
  const area = ed.locator('[data-script-text]')
  expect(await area.evaluate((el) => el.scrollWidth > el.clientWidth)).toBe(true)
  await testAs(page, 'fake-usage', 'fake')
  await expect(ed.getByRole('meter', { name: 'b' })).toBeVisible()
  expect(await pageScrollsSideways(page)).toBe(false)
})

for (const width of [1440, 390] as const) {
  for (const colorScheme of ['dark', 'light'] as const) {
    test.describe(`axe at ${width} px, ${colorScheme} theme`, () => {
      test.use({ viewport: { width, height: width === 390 ? 844 : 900 }, colorScheme, reducedMotion: 'reduce' })

      test('the limits, an open script and test results have no violations', async ({ page, omo }) => {
        await putProfile(omo)
        writeUsage(omo, 42, 97)
        await openUsage(page, omo)
        await limits(page).getByRole('button', { name: 'Add window' }).click()
        await typeWindow(page, 1, 'weekly')
        expect(await violations(page), 'limits').toEqual([])

        await newScript(page, 'fake-usage')
        const ed = editor(page, 'fake-usage')
        await ed.locator('[data-script-text]').fill(SCRIPT)
        await testAs(page, 'fake-usage', 'fake')
        await expect(ed.getByRole('meter', { name: 'b' })).toBeVisible()
        await ed.locator('details summary').click()
        expect(await violations(page), 'test with meters').toEqual([])

        await ed.locator('[data-script-text]').fill(BROKEN)
        await ed.locator('[data-test-run]').click()
        await expect(ed.locator('[data-test-error]')).toBeVisible()
        await scriptBar(page, 'fake-usage').getByRole('button', { name: 'Save', exact: true }).click()
        await expect(ed.locator('[data-script-text]')).toHaveAttribute('aria-invalid', 'true')
        expect(await violations(page), 'error').toEqual([])
        expect(await pageScrollsSideways(page)).toBe(false)
      })
    })
  }
}
