import AxeBuilder from '@axe-core/playwright'
import { execFileSync } from 'node:child_process'
import { existsSync, mkdirSync, readFileSync, renameSync, writeFileSync } from 'node:fs'
import { devNull } from 'node:os'
import { dirname, join } from 'node:path'
import type { Page } from '@playwright/test'
import { openBoard } from './support/board'
import { initRepo } from './support/git'
import { expect, test, type Omo } from './support/omo'

const GIT_ENV = { ...process.env, GIT_CONFIG_GLOBAL: devNull, GIT_CONFIG_SYSTEM: devNull }
const git = (cwd: string, ...args: string[]) =>
  execFileSync('git', ['-c', 'user.name=e2e', '-c', 'user.email=e2e@example.invalid', '-c', 'commit.gpgsign=false', ...args], { cwd, env: GIT_ENV, stdio: 'pipe' })
    .toString()
    .trim()

interface Source {
  /** The bare repository omo fetches from (a local folder in the test's home). */
  bare: string
  /** Commits a file and pushes it to the bare repository; answers the new commit. */
  commit(file: string, text: string): string
}

/** A local bare repository below the test's temporary home, with one commit. */
function makeSource(omo: Omo, name: string): Source {
  const root = join(omo.home, 'skill-src', name)
  const work = join(root, 'work')
  const bare = join(root, 'bare.git')
  initRepo(work)
  mkdirSync(bare, { recursive: true })
  git(bare, 'init', '--bare', '-q', '-b', 'main')
  const source: Source = {
    bare,
    commit(file, text) {
      mkdirSync(dirname(join(work, file)), { recursive: true })
      writeFileSync(join(work, file), text)
      git(work, 'add', file)
      git(work, 'commit', '-q', '-m', `write ${file}`)
      git(work, 'push', '-q', bare, 'main')
      return git(work, 'rev-parse', 'HEAD')
    },
  }
  source.commit('SKILL.md', '# A skill\n')
  return source
}

interface Stored {
  skill_sets: { name: string; revision: string; repository: string; paths: string[]; roles: string[]; instructions: Record<string, string> }[]
  states: { name: string; state: string; commit: string; path: string; message: string; missing_paths: string[] }[]
}

const SHOTS = process.env.OMO_SCREENSHOTS ?? ''
const stored = (omo: Omo) => omo.api<Stored>('GET', '/api/skillsets')

const title = (page: Page) => page.locator('[data-settings-page] h1')
const row = (page: Page, name: string) => page.locator(`[data-skillset="${name}"]`)
const editor = (page: Page, name: string) => page.locator(`[data-skillset-editor="${name}"]`)
const saveBar = (page: Page, name: string) => page.getByRole('region', { name: `Save changes to the skill set ${name}` })
const short = (commit: string) => commit.slice(0, 7)

async function openSkills(page: Page, omo: Omo) {
  await openBoard(page, omo)
  await page.goto(omo.url('/settings/skills'))
  await expect(title(page)).toHaveText('Skill sets')
  await expect(page.locator('[data-new-skillset]')).toBeVisible()
}

/** A set made through the API (the form is not what the test is about); waits until it is checked out. */
async function addSet(page: Page, omo: Omo, name: string, src: Source, extra: object = {}) {
  await omo.api('PUT', `/api/skillsets/${name}`, { name, repository: src.bare, ref: 'main', update: 'at_start', enabled: true, instructions: {}, ...extra })
  await expect(row(page, name).locator('[data-skillset-state]')).toHaveText('checked out')
}

async function openEditor(page: Page, name: string) {
  await row(page, name).locator('[data-skillset-toggle]').click()
  await expect(editor(page, name)).toBeVisible()
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

const skillFile = (omo: Omo, name: string) => join(omo.home, 'omo', 'skillsets', `${name}.yaml`)

test('adding a skill set checks it out and shows its commit', async ({ page, omo }) => {
  const src = makeSource(omo, 'skills')
  await openSkills(page, omo)
  await expect(page.locator('[data-no-skillsets]')).toBeVisible()

  await page.getByRole('button', { name: 'Add skill set…' }).click()
  await page.getByLabel('Name of the skill set').fill('skills')
  await page.getByRole('button', { name: 'Add', exact: true }).click()
  const ed = editor(page, 'skills')
  await expect(ed).toContainText('(not saved yet)')
  await expect(saveBar(page, 'skills').locator('[data-save-state]')).toHaveText('Unsaved changes')
  await ed.getByLabel('Repository').fill(src.bare)
  await ed.getByLabel('Branch or tag').fill('main')
  await ed.getByLabel('Developer').fill('Read SKILL.md before you start.')
  await saveBar(page, 'skills').getByRole('button', { name: 'Save', exact: true }).click()
  await expect(saveBar(page, 'skills').locator('[data-save-state]')).toHaveText('Saved')

  const commit = git(src.bare, 'rev-parse', 'main')
  await expect(row(page, 'skills').locator('[data-skillset-state]')).toHaveText('checked out')
  await expect(row(page, 'skills')).toContainText(`at ${short(commit)}`)
  await expect(row(page, 'skills')).toContainText('updates at every start from main')
  await expect(ed.locator('[data-skillset-checkout]')).toContainText(`Checked out at ${short(commit)}`)
  const now = await stored(omo)
  const state = now.states.find((s) => s.name === 'skills')
  expect(state?.commit).toBe(commit)
  expect(existsSync(join(state?.path ?? '/nonexistent', 'SKILL.md'))).toBe(true)
  expect(now.skill_sets[0]?.instructions).toEqual({ developer: 'Read SKILL.md before you start.' })
})

test('what a role is told shows in the developer prompt preview', async ({ page, omo }) => {
  const src = makeSource(omo, 'skills')
  await openBoard(page, omo)
  await page.goto(omo.url('/settings/skills'))
  await expect(page.locator('[data-new-skillset]')).toBeVisible()
  await addSet(page, omo, 'skills', src)
  await openEditor(page, 'skills')
  await editor(page, 'skills').getByLabel('Developer').fill('Run the skill checks first.')
  await saveBar(page, 'skills').getByRole('button', { name: 'Save', exact: true }).click()
  await expect(saveBar(page, 'skills').locator('[data-save-state]')).toHaveText('Saved')

  await page.goto(omo.url('/settings/prompts'))
  await page.locator('[data-prompt-row="developer-instructions"] [data-prompt-toggle]').click()
  const ed = page.locator('[data-prompt-editor="developer-instructions"]')
  await ed.getByRole('button', { name: 'Preview' }).click()
  const text = ed.locator('[data-preview-text]')
  await expect(text).toContainText('Skill set skills (checked out at ')
  await expect(text).toContainText('Run the skill checks first.')
})

test('an ext:: address is refused with the server message under the field', async ({ page, omo }) => {
  await openSkills(page, omo)
  await page.getByRole('button', { name: 'Add skill set…' }).click()
  await page.getByLabel('Name of the skill set').fill('evil')
  await page.getByRole('button', { name: 'Add', exact: true }).click()
  const ed = editor(page, 'evil')
  await ed.getByLabel('Repository').fill('ext::sh -c "touch /tmp/omo-e2e-never"')
  await saveBar(page, 'evil').getByRole('button', { name: 'Save', exact: true }).click()
  const said = await omo
    .api('PUT', '/api/skillsets/evil', { name: 'evil', repository: 'ext::sh -c "touch /tmp/omo-e2e-never"', update: 'at_start', enabled: true, instructions: {} })
    .then(
      () => '',
      (e: Error) => e.message,
    )
  const field = ed.locator('.omo-field').filter({ has: page.getByLabel('Repository') })
  const shown = field.locator('.omo-field__error')
  await expect(shown).toContainText('repository must be an https:// or ssh:// address')
  expect(said).toContain(await shown.innerText())
  await expect(ed.getByLabel('Repository')).toHaveAttribute('aria-invalid', 'true')
  expect((await stored(omo)).skill_sets).toEqual([])
})

test('a failing source shows the failed state and keeps the last checkout; Update now fetches again', async ({ page, omo }) => {
  const src = makeSource(omo, 'skills')
  await openSkills(page, omo)
  await addSet(page, omo, 'skills', src)
  const first = git(src.bare, 'rev-parse', 'main')
  const path = (await stored(omo)).states[0]?.path ?? ''
  await openEditor(page, 'skills')
  const ed = editor(page, 'skills')

  renameSync(src.bare, `${src.bare}.away`)
  await ed.getByRole('button', { name: 'Update now' }).click()
  await expect(ed.locator('[data-skillset-result]')).toContainText('The update failed:')
  await expect(row(page, 'skills').locator('[data-skillset-state]')).toHaveText('failed')
  await expect(row(page, 'skills').locator('[data-skillset-failure]')).toBeVisible()
  await expect(ed.locator('[data-skillset-checkout]')).toContainText(`still at ${short(first)}`)
  expect(existsSync(join(path, 'SKILL.md'))).toBe(true)

  renameSync(`${src.bare}.away`, src.bare)
  const second = src.commit('MORE.md', 'more\n')
  await ed.getByRole('button', { name: 'Update now' }).click()
  await expect(ed.locator('[data-skillset-result]')).toHaveText(`Updated; now at ${short(second)}.`)
  await expect(row(page, 'skills').locator('[data-skillset-state]')).toHaveText('checked out')
  await expect(row(page, 'skills')).toContainText(`at ${short(second)}`)
  expect(existsSync(join(path, 'MORE.md'))).toBe(true)
})

test('delete asks first, removes the set and its checkout', async ({ page, omo }) => {
  const src = makeSource(omo, 'skills')
  await openSkills(page, omo)
  await addSet(page, omo, 'skills', src)
  const path = (await stored(omo)).states[0]?.path ?? ''
  expect(existsSync(path)).toBe(true)
  await openEditor(page, 'skills')
  await editor(page, 'skills').getByRole('button', { name: 'Delete skills…' }).click()
  const question = page.locator('[data-question]')
  await expect(question).toContainText('Delete the skill set skills?')
  await expect(question).toContainText('checkout')
  await question.locator('[data-question-cancel]').click()
  await expect(row(page, 'skills')).toBeVisible()
  await editor(page, 'skills').getByRole('button', { name: 'Delete skills…' }).click()
  await page.locator('[data-question-confirm]').click()
  await expect(row(page, 'skills')).toHaveCount(0)
  await expect(editor(page, 'skills')).toHaveCount(0)
  await expect(page.locator('[data-no-skillsets]')).toBeVisible()
  // Focus does not fall to the page: it goes where a new set is added.
  await expect(page.locator('[data-new-skillset]')).toBeFocused()
  await expect.poll(async () => (await stored(omo)).skill_sets.length).toBe(0)
  await expect.poll(() => existsSync(path)).toBe(false)
})

test('a hand edit of the set file is shown; with a draft it is a conflict', async ({ page, omo }) => {
  const src = makeSource(omo, 'skills')
  await openSkills(page, omo)
  await addSet(page, omo, 'skills', src)
  await openEditor(page, 'skills')
  const ed = editor(page, 'skills')
  const enabled = ed.getByRole('switch', { name: 'Agents are told about it' })
  await expect(enabled).toHaveAttribute('aria-checked', 'true')

  const file = skillFile(omo, 'skills')
  writeFileSync(file, readFileSync(file, 'utf8').replace(/^enabled: .*$/m, 'enabled: false'))
  await expect(enabled).toHaveAttribute('aria-checked', 'false')
  await expect(ed.locator('[data-changed-on-disk]')).toContainText('not through omo')
  await expect(row(page, 'skills')).toContainText('off')

  await ed.getByLabel('Reviewer').fill('A draft nobody saved.')
  writeFileSync(file, readFileSync(file, 'utf8').replace(/^update: .*$/m, 'update: pinned'))
  await expect(saveBar(page, 'skills').locator('[data-save-state]')).toHaveText('Changed outside this page')
  await expect(ed.getByLabel('Reviewer')).toHaveValue('A draft nobody saved.')
  await ed.locator('[data-use-theirs]').click()
  await expect(ed.getByLabel('Updates')).toHaveValue('pinned')
  await expect(ed.getByLabel('Reviewer')).toHaveValue('')
  await expect(row(page, 'skills')).toContainText('pinned to main')
})

test('Add Superpowers opens a filled-in form that is stored only when saved', async ({ page, omo }) => {
  await openSkills(page, omo)
  await page.locator('[data-add-recommended="superpowers"]').click()
  const ed = editor(page, 'superpowers')
  await expect(ed).toContainText('(not saved yet)')
  await expect(ed.getByLabel('Repository')).toHaveValue('https://github.com/obra/superpowers.git')
  await expect(ed.getByLabel('Branch or tag')).toHaveValue('main')
  await expect(ed.getByLabel('Developer')).toHaveValue(/test-driven-development/)
  await expect(ed.locator('[data-skillset-role][aria-pressed="true"]')).toHaveText(['Product manager', 'Developer', 'Reviewer'])
  await expect(ed.locator('[data-skillset-paths] [data-row]')).toHaveCount(0)
  await expect(saveBar(page, 'superpowers').locator('[data-save-state]')).toHaveText('Unsaved changes')
  expect((await stored(omo)).skill_sets).toEqual([])
  await ed.getByRole('button', { name: 'Close' }).click()
  await expect(page.locator('[data-question]')).toContainText('Leave without saving?')
  await page.locator('[data-question-confirm]').click()
  await expect(page.locator('[data-skillset-editor]')).toHaveCount(0)
})

/** A source with three skills in two groups, for the roles-and-paths tests. */
function grillSource(omo: Omo): Source {
  const src = makeSource(omo, 'talk')
  src.commit('skills/productivity/grill-me/SKILL.md', '---\nname: grill-me\ndescription: A relentless interview.\ndisable-model-invocation: true\n---\nUse grilling.\n')
  src.commit('skills/productivity/grilling/SKILL.md', '---\nname: grilling\ndescription: Grill the user.\n---\n')
  src.commit('skills/engineering/tdd/SKILL.md', '---\nname: tdd\ndescription: Test first.\n---\n')
  return src
}

async function previewOf(page: Page, omo: Omo, prompt: string) {
  await page.goto(omo.url('/settings/prompts'))
  await page.locator(`[data-prompt-row="${prompt}"] [data-prompt-toggle]`).click()
  const ed = page.locator(`[data-prompt-editor="${prompt}"]`)
  await ed.getByRole('button', { name: 'Preview' }).click()
  const text = ed.locator('[data-preview-text]')
  await expect(text).not.toBeEmpty()
  return text
}

test('roles and paths: a set for the CEO with one skill folder reaches only the CEO, with only that skill', async ({ page, omo }) => {
  const src = grillSource(omo)
  await openSkills(page, omo)
  await page.getByRole('button', { name: 'Add skill set…' }).click()
  await page.getByLabel('Name of the skill set').fill('talk')
  await page.getByRole('button', { name: 'Add', exact: true }).click()
  const ed = editor(page, 'talk')
  await ed.getByLabel('Repository').fill(src.bare)
  await expect(ed.locator('[data-skillset-roles-hint]')).toHaveText('None chosen: every role gets this set.')
  await ed.getByRole('button', { name: 'CEO' }).click()
  await expect(ed.getByRole('button', { name: 'CEO' })).toHaveAttribute('aria-pressed', 'true')
  await expect(ed.locator('[data-skillset-text]')).toHaveCount(1)
  await ed.getByRole('button', { name: 'Add path' }).click()
  await ed.getByRole('textbox', { name: 'Path 1', exact: true }).fill('skills/productivity/grill-me')
  await saveBar(page, 'talk').getByRole('button', { name: 'Save', exact: true }).click()
  await expect(saveBar(page, 'talk').locator('[data-save-state]')).toHaveText('Saved')
  await expect(row(page, 'talk').locator('[data-skillset-state]')).toHaveText('checked out')
  await expect(row(page, 'talk').locator('[data-skillset-reach]')).toHaveText('for CEO · skills/productivity/grill-me')
  const set = (await stored(omo)).skill_sets[0]
  expect(set?.roles).toEqual(['ceo'])
  expect(set?.paths).toEqual(['skills/productivity/grill-me'])

  // A path the checkout does not have is refused under its row; one that leaves the repository too.
  await ed.getByRole('button', { name: 'Add path' }).click()
  await ed.getByRole('textbox', { name: 'Path 2', exact: true }).fill('skills/productivity/nope')
  await saveBar(page, 'talk').getByRole('button', { name: 'Save', exact: true }).click()
  const row2 = ed.locator('[data-skillset-paths] [data-row="1"]')
  await expect(row2.locator('.omo-field__error')).toContainText('paths.1 is not a folder or file of the checkout at')
  await expect(ed.getByRole('textbox', { name: 'Path 2', exact: true })).toHaveAttribute('aria-invalid', 'true')
  await ed.getByRole('textbox', { name: 'Path 2', exact: true }).fill('../outside')
  await saveBar(page, 'talk').getByRole('button', { name: 'Save', exact: true }).click()
  await expect(row2.locator('.omo-field__error')).toContainText('paths.1 must stay inside the repository')
  await ed.getByRole('button', { name: 'Remove path 2' }).click()
  await expect(saveBar(page, 'talk').getByRole('button', { name: 'Discard' })).toBeDisabled()
  expect((await stored(omo)).skill_sets[0]?.paths).toEqual(['skills/productivity/grill-me'])

  const ceo = await previewOf(page, omo, 'ceo-instructions')
  await expect(ceo).toContainText('Skill set talk (checked out at ')
  await expect(ceo).toContainText('- grill-me (only when someone asks for it): A relentless interview.')
  await expect(ceo).not.toContainText('grilling')
  await expect(ceo).not.toContainText('tdd')
  const dev = await previewOf(page, omo, 'developer-instructions')
  await expect(dev).not.toContainText('Skill set')
})

test('a path that disappears from the source is named in the open set', async ({ page, omo }) => {
  const src = grillSource(omo)
  await openSkills(page, omo)
  await addSet(page, omo, 'talk', src, { paths: ['skills/productivity/grill-me', 'skills/engineering/tdd'] })
  await openEditor(page, 'talk')
  const ed = editor(page, 'talk')
  await expect(ed.locator('[data-skillset-missing]')).toHaveCount(0)
  git(join(src.bare, '..', 'work'), 'rm', '-q', '-r', 'skills/engineering')
  git(join(src.bare, '..', 'work'), 'commit', '-q', '-m', 'drop tdd')
  git(join(src.bare, '..', 'work'), 'push', '-q', src.bare, 'main')
  await ed.getByRole('button', { name: 'Update now' }).click()
  await expect(ed.locator('[data-skillset-missing]')).toContainText('Not in the checkout: skills/engineering/tdd.')
})

test('at 390 px the page does not scroll sideways', async ({ page, omo }) => {
  const src = makeSource(omo, 'skills')
  await page.setViewportSize({ width: 390, height: 844 })
  await openBoard(page, omo)
  await page.goto(omo.url('/settings/skills'))
  await expect(page.locator('[data-new-skillset]')).toBeVisible()
  await addSet(page, omo, 'skills', src, { instructions: { developer: 'x'.repeat(300) } })
  expect(await pageScrollsSideways(page)).toBe(false)
  await openEditor(page, 'skills')
  await editor(page, 'skills').getByLabel('Repository').fill(`${src.bare}/${'very-long-folder-name/'.repeat(8)}`)
  expect(await pageScrollsSideways(page)).toBe(false)
  renameSync(src.bare, `${src.bare}.away`)
  await editor(page, 'skills').getByRole('button', { name: 'Update now' }).click()
  await expect(editor(page, 'skills').locator('[data-skillset-result]')).toBeVisible()
  expect(await pageScrollsSideways(page)).toBe(false)
})

for (const width of [1440, 390] as const) {
  for (const colorScheme of ['dark', 'light'] as const) {
    test.describe(`axe at ${width} px, ${colorScheme} theme`, () => {
      test.use({ viewport: { width, height: width === 390 ? 844 : 900 }, colorScheme, reducedMotion: 'reduce' })

      test('the list and an open set have no violations', async ({ page, omo }) => {
        const src = makeSource(omo, 'skills')
        await openBoard(page, omo)
        await page.goto(omo.url('/settings/skills'))
        await expect(page.locator('[data-new-skillset]')).toBeVisible()
        expect(await violations(page), 'empty').toEqual([])
        await addSet(page, omo, 'skills', src, { instructions: { developer: 'Read it.' } })
        await omo.api('PUT', '/api/skillsets/gone', { name: 'gone', repository: join(omo.home, 'does-not-exist.git'), update: 'at_start', enabled: false, instructions: {} })
        await expect(row(page, 'gone').locator('[data-skillset-state]')).toHaveText('failed')
        expect(await violations(page), 'list').toEqual([])
        await openEditor(page, 'skills')
        await editor(page, 'skills').getByLabel('Reviewer').fill('Unsaved.')
        expect(await violations(page), 'open set').toEqual([])
        // Roles chosen, paths listed, one of them not in the checkout.
        const talk = grillSource(omo)
        await addSet(page, omo, 'talk', talk, {
          roles: ['ceo', 'pm'],
          paths: ['skills/productivity/grill-me', 'skills/productivity/gone'],
          instructions: { ceo: 'When the user asks you to grill them, follow the grill-me skill.' },
        })
        await row(page, 'talk').locator('[data-skillset-toggle]').click()
        await page.locator('[data-question-confirm]').click()
        const ed = editor(page, 'talk')
        await expect(ed.locator('[data-skillset-missing]')).toBeVisible()
        expect(await violations(page), 'roles and paths').toEqual([])
        if (SHOTS !== '') {
          mkdirSync(SHOTS, { recursive: true })
          await ed.locator('[data-skillset-roles]').scrollIntoViewIfNeeded()
          await page.screenshot({ path: join(SHOTS, `fix6-skills-${colorScheme}-${width}.png`) })
        }
      })
    })
  }
}
