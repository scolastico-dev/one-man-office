import AxeBuilder from '@axe-core/playwright'
import type { Locator, Page } from '@playwright/test'
import { accessSync, chmodSync, constants, mkdtempSync, readFileSync, realpathSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { delimiter, join } from 'node:path'
import { openBoard } from './support/board'
import { expect, FAKE_AGENT_BINARY, ServerError, test, type Omo } from './support/omo'

interface StoredProfile {
  name: string
  cli: string
  command: string
  args: string[] | null
  env: Record<string, string> | null
  models: Record<string, { args: string[] | null; env: Record<string, string> | null; description: string }> | null
}

const profileFile = (omo: Omo, name: string) => join(omo.home, 'omo', 'profiles', `${name}.yaml`)
const editor = (page: Page, name: string) => page.locator(`[data-profile-editor="${name}"]`)
const model = (page: Page, profile: string, name: string) => editor(page, profile).locator(`[data-model-editor="${name}"]`)
const saveBar = (page: Page) => page.getByRole('region', { name: 'Save changes' })

async function openAgents(page: Page, omo: Omo) {
  await openBoard(page, omo)
  await page.goto(omo.url('/settings/agents'))
  await expect(page.locator('[data-settings-page] h1')).toHaveText('Agents')
}

async function profiles(omo: Omo): Promise<StoredProfile[]> {
  return (await omo.api<{ profiles: StoredProfile[] }>('GET', '/api/profiles')).profiles
}

/** A profile written the way another client would. */
async function putProfile(omo: Omo, name: string, env: Record<string, string> = {}) {
  await omo.api('PUT', `/api/profiles/${name}`, {
    cli: 'custom',
    command: FAKE_AGENT_BINARY,
    args: [],
    env,
    account_dir: '',
    usage_script: '',
    prompt_delivery: {},
    models: { default: { args: [], env: {}, usage_windows: [], description: 'scripted fake agent' } },
  })
}

/** The server's refusal of a request, as the page should show it (without the harness's method and status prefix). */
async function refusal(p: Promise<unknown>): Promise<string> {
  try {
    await p
  } catch (e) {
    if (e instanceof ServerError) return e.message.replace(/^\S+ \S+: \d+ /, '')
    throw e
  }
  throw new Error('the request was not refused')
}

async function save(page: Page) {
  await saveBar(page).getByRole('button', { name: 'Save', exact: true }).click()
}

async function expectSaved(page: Page) {
  await expect(saveBar(page).locator('[data-save-state]')).toHaveText('Saved')
}

function listArgs(scope: Locator, label: string) {
  return scope.getByRole('group', { name: label }).getByRole('textbox')
}

async function violations(page: Page) {
  await page.evaluate(() => document.fonts.ready.then(() => undefined))
  const results = await new AxeBuilder({ page }).analyze()
  return results.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target.join(' ')) }))
}

async function pageScrollsSideways(page: Page): Promise<boolean> {
  return page.evaluate(() => {
    const root = document.documentElement
    return root.scrollWidth > root.clientWidth || document.body.scrollWidth > document.body.clientWidth
  })
}

test('create, edit, duplicate and delete a profile; add, reorder and remove models', async ({ page, omo }) => {
  await openAgents(page, omo)
  await expect(page.locator('[data-no-profiles]')).toBeVisible()

  // New profile: named in a dialog, saved from the form.
  await page.getByRole('button', { name: 'New profile…' }).click()
  const dialog = page.getByRole('dialog', { name: 'New profile' })
  await dialog.getByLabel('Name of the profile').fill('fake')
  await dialog.getByLabel('Agent program').selectOption('custom')
  await dialog.getByRole('button', { name: 'Create' }).click()
  const fake = editor(page, 'fake')
  await expect(fake.getByRole('heading', { name: 'Profile fake (not saved yet)' })).toBeVisible()
  await fake.getByLabel('Command').fill(FAKE_AGENT_BINARY)
  await model(page, 'fake', 'default').getByLabel('Description').fill('scripted fake agent')
  await fake.getByRole('button', { name: 'Add model' }).click()
  await fake.locator('[data-model-name]').last().fill('fast')
  const fast = model(page, 'fake', 'fast')
  await fast.getByRole('button', { name: 'Add argument' }).click()
  await listArgs(fast, 'Extra arguments').nth(0).fill('--fast')
  await save(page)
  await expectSaved(page)
  await expect(page.locator('[data-profile="fake"]')).toContainText('Custom · 2 models')
  const ids = (await omo.api<{ id: string }[]>('GET', '/api/models')).map((m) => m.id).sort()
  expect(ids).toEqual(['fake/default', 'fake/fast'])

  // Edit: base arguments added and reordered; the order is what is stored.
  await expect(fake.getByRole('heading', { name: 'Profile fake', exact: true })).toBeVisible()
  await fake.getByRole('group', { name: 'Base arguments' }).getByRole('button', { name: 'Add argument' }).click()
  await listArgs(fake, 'Base arguments').nth(0).fill('--one')
  await fake.getByRole('group', { name: 'Base arguments' }).getByRole('button', { name: 'Add argument' }).click()
  await listArgs(fake, 'Base arguments').nth(1).fill('--two')
  await fake.getByRole('button', { name: 'Move argument 2 up' }).click()
  await save(page)
  await expectSaved(page)
  expect((await profiles(omo)).find((p) => p.name === 'fake')?.args).toEqual(['--two', '--one'])

  // Remove a model.
  await fast.getByRole('button', { name: 'Remove model fast' }).click()
  await save(page)
  await expectSaved(page)
  expect(Object.keys((await profiles(omo)).find((p) => p.name === 'fake')?.models ?? {})).toEqual(['default'])
  await expect(page.locator('[data-profile="fake"]')).toContainText('1 model')

  // Duplicate: written at once under the new name, then opened.
  await fake.getByRole('button', { name: 'Duplicate…' }).click()
  const dup = page.getByRole('dialog', { name: 'Duplicate fake' })
  await dup.getByLabel('Name of the profile').fill('fake-two')
  await dup.getByRole('button', { name: 'Duplicate' }).click()
  await expect(editor(page, 'fake-two').getByRole('heading', { name: 'Profile fake-two', exact: true })).toBeVisible()
  const copy = (await profiles(omo)).find((p) => p.name === 'fake-two')
  expect(copy?.args).toEqual(['--two', '--one'])
  expect(copy?.command).toBe(FAKE_AGENT_BINARY)

  // Delete the copy, after the question.
  await editor(page, 'fake-two').getByRole('button', { name: 'Delete…' }).click()
  await page.getByRole('dialog', { name: 'Delete the profile fake-two?' }).getByRole('button', { name: 'Delete' }).click()
  await expect(page.locator('[data-profile="fake-two"]')).toHaveCount(0)
  await expect(editor(page, 'fake-two')).toHaveCount(0)
  expect((await profiles(omo)).map((p) => p.name)).toEqual(['fake'])
})

test('deleting a profile whose model an open ticket uses, or dropping that model, is refused with the server message and no offer', async ({ page, omo }) => {
  await putProfile(omo, 'fake')
  await omo.api('PUT', '/api/profiles/fake', {
    ...(await profiles(omo))[0],
    models: {
      default: { args: [], env: {}, usage_windows: [], description: 'scripted fake agent' },
      spare: { args: [], env: {}, usage_windows: [], description: '' },
    },
  })
  await omo.api('PUT', '/api/roles/developer', { models: ['fake/default'], assignment: 'round_robin' })
  const n = await omo.api<{ number: number }>('POST', '/api/tickets', { type: 'task', title: 'Uses spare', projects: [omo.project], model: 'fake/spare' })

  await openAgents(page, omo)
  await page.locator('[data-profile="fake"]').click()
  const fake = editor(page, 'fake')
  await expect(fake.getByRole('heading', { name: 'Profile fake', exact: true })).toBeVisible()

  // The model an open ticket uses: the sentence names the ticket.
  await model(page, 'fake', 'spare').getByRole('button', { name: 'Remove model spare' }).click()
  await save(page)
  const dropText = saveBar(page).locator('[data-save-error]')
  await expect(dropText).toContainText(`#${n.number}`)
  const expectedDrop = await refusal(
    omo.api('PUT', '/api/profiles/fake', {
      ...(await profiles(omo))[0],
      models: { default: { args: [], env: {}, usage_windows: [], description: 'scripted fake agent' } },
    }),
  )
  await expect(dropText).toHaveText(expectedDrop)
  await saveBar(page).getByRole('button', { name: 'Discard' }).click()

  // The profile whose model the open ticket uses: refused also while a role uses it, without the offer to take it out.
  const expectedDelete = await refusal(omo.api('DELETE', '/api/profiles/fake'))
  await fake.getByRole('button', { name: 'Delete…' }).click()
  await page.getByRole('dialog', { name: 'Delete the profile fake?' }).getByRole('button', { name: 'Delete' }).click()
  await expect(fake.locator('[data-delete-error] .callout__body')).toHaveText(expectedDelete)
  await expect(page.getByRole('dialog')).toHaveCount(0)
  expect((await profiles(omo)).map((p) => p.name)).toEqual(['fake'])
})

test('deleting a profile that roles use asks again naming the roles, takes its models out of them and says so', async ({ page, omo }) => {
  await putProfile(omo, 'fake')
  await omo.api('PUT', '/api/roles/developer', { models: ['fake/default'], assignment: 'round_robin' })
  await omo.api('PUT', '/api/roles/reviewer', { models: ['fake/default'], assignment: 'round_robin' })

  await openAgents(page, omo)
  await page.locator('[data-profile="fake"]').click()
  const fake = editor(page, 'fake')
  await fake.getByRole('button', { name: 'Delete…' }).click()
  await page.getByRole('dialog', { name: 'Delete the profile fake?' }).getByRole('button', { name: 'Delete' }).click()
  const ask = page.getByRole('dialog', { name: 'Take fake out of its roles?' })
  await expect(ask).toContainText('fake is used by the roles developer and reviewer. Delete it and take its models out of these roles?')
  // Kept: nothing changes.
  await ask.getByRole('button', { name: 'Keep it' }).click()
  await expect(ask).toHaveCount(0)
  expect((await profiles(omo)).map((p) => p.name)).toEqual(['fake'])

  await fake.getByRole('button', { name: 'Delete…' }).click()
  await page.getByRole('dialog', { name: 'Delete the profile fake?' }).getByRole('button', { name: 'Delete' }).click()
  await ask.getByRole('button', { name: 'Delete and take out' }).click()
  await expect(page.locator('[data-profile-deleted]')).toContainText(
    'Profile fake deleted. Its models were taken out of the roles developer (fake/default) and reviewer (fake/default).',
  )
  await expect(fake).toHaveCount(0)
  expect(await profiles(omo)).toEqual([])
  const roles = await omo.api<{ name: string; models: string[] | null }[]>('GET', '/api/roles')
  expect(roles.find((r) => r.name === 'developer')?.models ?? []).toEqual([])

  // The Roles page reports the role left without a model, as for any such role.
  await page.goto(omo.url('/settings/roles'))
  await expect(page.locator('[data-setup-report] [data-blocking]')).toBeVisible()
})

test('an environment value is shown as saved, also after a reload, and a renamed variable keeps its value', async ({ page, omo }) => {
  const value = `sk-example-${Math.random().toString(36).slice(2)}`
  await putProfile(omo, 'fake')
  await openAgents(page, omo)
  await page.locator('[data-profile="fake"]').click()
  const fake = editor(page, 'fake')

  const env = fake.getByRole('group', { name: 'Environment', exact: true })
  await env.getByRole('button', { name: 'Add variable' }).click()
  await env.getByLabel('Name of variable 1').fill('ANTHROPIC_API_KEY')
  await env.getByLabel('Value of ANTHROPIC_API_KEY').fill(value)
  const menv = model(page, 'fake', 'default').getByRole('group', { name: 'Extra environment' })
  await menv.getByRole('button', { name: 'Add variable' }).click()
  await menv.getByLabel('Name of variable 1').fill('OTHER_TOKEN')
  await menv.getByLabel('Value of OTHER_TOKEN').fill(`${value}-model`)
  await save(page)
  await expectSaved(page)

  // Stored on disk and shown as it is.
  expect(readFileSync(profileFile(omo, 'fake'), 'utf8')).toContain(value)
  await expect(env.getByLabel('Value of ANTHROPIC_API_KEY')).toHaveValue(value)
  await expect(menv.getByLabel('Value of OTHER_TOKEN')).toHaveValue(`${value}-model`)

  // Read again from scratch: the same.
  await page.reload()
  await expect(page.locator('[data-settings-page] h1')).toHaveText('Agents')
  await page.locator('[data-profile="fake"]').click()
  await expect(env.getByLabel('Value of ANTHROPIC_API_KEY')).toHaveValue(value)
  await expect(menv.getByLabel('Value of OTHER_TOKEN')).toHaveValue(`${value}-model`)

  // A renamed variable is saved with the value in its row.
  await env.getByLabel('Name of variable 1').fill('ANTHROPIC_API_KEY_2')
  await save(page)
  await expectSaved(page)
  await expect(env.getByLabel('Value of ANTHROPIC_API_KEY_2')).toHaveValue(value)
  const p = (await profiles(omo)).find((x) => x.name === 'fake')!
  expect(p.env).toEqual({ ANTHROPIC_API_KEY_2: value })
})

test('a hand edit of the profile file while it is open shows the change notice and keeps the draft', async ({ page, omo }) => {
  await putProfile(omo, 'fake')
  await openAgents(page, omo)
  await page.locator('[data-profile="fake"]').click()
  const fake = editor(page, 'fake')
  const command = fake.getByLabel('Command')
  await expect(command).toHaveValue(FAKE_AGENT_BINARY)

  // No draft: the page follows the file.
  const file = profileFile(omo, 'fake')
  writeFileSync(file, readFileSync(file, 'utf8').replace(`command: ${FAKE_AGENT_BINARY}`, 'command: /bin/edited-by-hand'))
  await expect(command).toHaveValue('/bin/edited-by-hand')
  await expect(fake.locator('[data-changed-on-disk]')).toContainText('Changed on disk at')

  // A draft: kept, and the new value is offered.
  await command.fill('/bin/my-draft')
  writeFileSync(file, readFileSync(file, 'utf8').replace('command: /bin/edited-by-hand', 'command: /bin/second-hand-edit'))
  await expect(saveBar(page).getByRole('button', { name: 'Use the new value' })).toBeVisible()
  await expect(command).toHaveValue('/bin/my-draft')
  await saveBar(page).getByRole('button', { name: 'Use the new value' }).click()
  await expect(command).toHaveValue('/bin/second-hand-edit')
})

test('a broken profile file shows the callout and can be saved over after "Save anyway?"', async ({ page, omo }) => {
  await putProfile(omo, 'fake')
  await openAgents(page, omo)
  await page.locator('[data-profile="fake"]').click()
  const fake = editor(page, 'fake')
  await expect(fake.getByLabel('Command')).toHaveValue(FAKE_AGENT_BINARY)

  writeFileSync(profileFile(omo, 'fake'), 'cli: custom\ncommand: [unclosed\n')
  await expect(fake.locator('[data-file-error]')).toContainText('Cannot be read')
  await expect(fake.locator('[data-file-error]')).toContainText('fake.yaml')
  // The list does not show it twice.
  await expect(page.locator('[data-file-error]')).toHaveCount(1)

  await fake.getByLabel('Command').fill('/bin/fixed')
  await save(page)
  const question = page.getByRole('dialog', { name: 'Save anyway?' })
  await expect(question).toContainText('Saving replaces the file on disk')
  await question.getByRole('button', { name: 'Save anyway' }).click()
  await expectSaved(page)
  await expect(fake.locator('[data-file-error]')).toHaveCount(0)
  expect(readFileSync(profileFile(omo, 'fake'), 'utf8')).toContain('/bin/fixed')
  expect((await omo.api<{ errors: unknown[] }>('GET', '/api/profiles')).errors).toEqual([])
})

// Detection looks at PATH only. The server here gets a PATH with nothing but
// git (omo needs it to start) and this test's own fake `claude` (an empty
// file with the execute bit; detection never runs it), so the test neither
// depends on nor shows which agent programs are installed on this machine.
const fakePrograms = mkdtempSync(join(realpathSync(tmpdir()), 'omo-e2e-path-'))
writeFileSync(join(fakePrograms, 'claude'), '')
chmodSync(join(fakePrograms, 'claude'), 0o755)
symlinkSync(findGit(), join(fakePrograms, 'git'))

function findGit(): string {
  for (const dir of (process.env.PATH ?? '').split(delimiter)) {
    const git = join(dir, 'git')
    try {
      accessSync(git, constants.X_OK)
      return git
    } catch {
      // not here
    }
  }
  throw new Error('git is not on the PATH of the test runner')
}
test.afterAll(() => rmSync(fakePrograms, { recursive: true, force: true }))

test.describe('detection', () => {
  test.use({ omoOptions: { path: fakePrograms, noProject: true } })

  test('Detect says what it does first and then lists exactly what the server answered', async ({ page, omo }) => {
    await openAgents(page, omo)
    const explained = page.locator('[data-detect-explained]')
    await expect(explained).toContainText('PATH')
    await expect(explained).toContainText("skips the program's permission prompts")

    await page.getByRole('button', { name: 'Detect agent programs' }).click()
    const result = page.locator('[data-detect-result]')
    await expect(result.locator('[data-detect-found]')).toHaveText([`claude: ${join(fakePrograms, 'claude')}`])
    await expect(result.locator('[data-detect-created]')).toHaveText('Profiles written: claude.')
    await expect(result.locator('[data-detect-assigned]')).toContainText('Roles that got models:')
    await expect(page.locator('[data-profile="claude"]')).toContainText('Claude Code')

    // The arguments that skip the permission prompts are in the profile, visible.
    await page.locator('[data-profile="claude"]').click()
    const args = await listArgs(model(page, 'claude', 'sonnet'), 'Extra arguments').evaluateAll((els) => els.map((e) => (e as HTMLInputElement).value))
    expect(args.join(' ')).toContain('--dangerously-skip-permissions')

    // The CEO got the strongest model, which agents may not choose for tickets; the role can still use it.
    await page.goto(omo.url('/settings/roles'))
    await expect(page.locator('[data-settings-page] h1')).toHaveText('Roles')
    await expect(page.locator('[data-model="claude/fable"]').first()).toBeVisible()
    await expect(page.locator('[data-setup-report]')).not.toContainText('Nothing starts')
    await page.goto(omo.url('/settings/agents'))
    await expect(page.locator('[data-settings-page] h1')).toHaveText('Agents')

    // A second run finds it again and skips it with the server's sentence.
    await page.getByRole('button', { name: 'Detect agent programs' }).click()
    await expect(result.locator('[data-detect-created-none]')).toBeVisible()
    await expect(result.locator('[data-detect-skipped]')).toHaveCount(1)
  })
})

for (const scheme of ['dark', 'light'] as const) {
  test.describe(`${scheme} theme`, () => {
    test.use({ colorScheme: scheme })

    test('the agents page with an open profile has no accessibility violations at 1440 and 390 px', async ({ page, omo }) => {
      await putProfile(omo, 'fake', { ANTHROPIC_API_KEY: 'sk-axe-secret', HTTPS_PROXY: '$OMO_PROXY' })
      await page.setViewportSize({ width: 1440, height: 900 })
      await openAgents(page, omo)
      await page.locator('[data-profile="fake"]').click()
      await expect(editor(page, 'fake').getByLabel('Value of ANTHROPIC_API_KEY')).toHaveValue('sk-axe-secret')
      await editor(page, 'fake').locator('summary').click()
      expect(await violations(page)).toEqual([])

      await page.setViewportSize({ width: 390, height: 844 })
      await expect(editor(page, 'fake').getByLabel('Command')).toBeVisible()
      expect(await violations(page)).toEqual([])
    })
  })
}

test.describe('at 390 px', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('the profile form is usable without horizontal scrolling', async ({ page, omo }) => {
    await putProfile(omo, 'a-profile-with-a-rather-long-name-for-a-narrow-screen', { ANTHROPIC_API_KEY: 'sk-narrow' })
    await openAgents(page, omo)
    await page.locator('[data-profile]').first().click()
    const ed = editor(page, 'a-profile-with-a-rather-long-name-for-a-narrow-screen')
    await expect(ed.getByLabel('Value of ANTHROPIC_API_KEY')).toHaveValue('sk-narrow')
    expect(await pageScrollsSideways(page)).toBe(false)

    // Change and save at this width.
    await ed.getByLabel('Command').fill('/bin/narrow')
    await ed.getByRole('button', { name: 'Add model' }).click()
    await ed.locator('[data-model-name]').last().fill('second')
    await save(page)
    await expectSaved(page)
    expect(await pageScrollsSideways(page)).toBe(false)
    const stored = (await profiles(omo))[0]!
    expect(stored.command).toBe('/bin/narrow')
    expect(Object.keys(stored.models ?? {}).sort()).toEqual(['default', 'second'])
  })
})
