import AxeBuilder from '@axe-core/playwright'
import { execFileSync } from 'node:child_process'
import { mkdirSync, writeFileSync } from 'node:fs'
import { devNull } from 'node:os'
import { dirname, join } from 'node:path'
import type { Page } from '@playwright/test'
import { openBoard } from './support/board'
import { initRepo } from './support/git'
import { expect, test, type Omo } from './support/omo'

// Searching skills.sh from the Skills page. omo's two search routes are
// stood in for here (the real ones ask skills.sh and GitHub, and a test
// never reaches the network; the Go tests run them against stand-ins): the
// search answers recorded-shape results, choosing one answers a local bare
// repository, so saving the set fetches only from the test's home.

const GIT_ENV = { ...process.env, GIT_CONFIG_GLOBAL: devNull, GIT_CONFIG_SYSTEM: devNull }
const git = (cwd: string, ...args: string[]) =>
  execFileSync('git', ['-c', 'user.name=e2e', '-c', 'user.email=e2e@example.invalid', '-c', 'commit.gpgsign=false', ...args], { cwd, env: GIT_ENV, stdio: 'pipe' })
    .toString()
    .trim()

/** A local bare repository with the grill-me skill in skills/productivity/grill-me. */
function grillRepo(omo: Omo): string {
  const work = join(omo.home, 'skill-src', 'work')
  const bare = join(omo.home, 'skill-src', 'bare.git')
  initRepo(work)
  const file = join(work, 'skills', 'productivity', 'grill-me', 'SKILL.md')
  mkdirSync(dirname(file), { recursive: true })
  writeFileSync(file, '---\nname: grill-me\ndescription: A relentless interview.\n---\n')
  git(work, 'add', '.')
  git(work, 'commit', '-q', '-m', 'grill-me')
  mkdirSync(bare, { recursive: true })
  git(bare, 'init', '--bare', '-q', '-b', 'main')
  git(work, 'push', '-q', bare, 'main')
  return bare
}

const RESULTS = [
  {
    id: 'mattpocock/skills/grill-me',
    name: 'grill-me',
    skill_id: 'grill-me',
    source: 'mattpocock/skills',
    installs: 1565244,
    repository: 'https://github.com/mattpocock/skills.git',
    url: 'https://skills.sh/mattpocock/skills/grill-me',
  },
  {
    id: 'evil/repo/x',
    name: '<img src=x onerror="window.__owned=1">',
    skill_id: 'x',
    source: 'evil/repo',
    installs: 12,
    repository: 'https://github.com/evil/repo.git',
    url: 'https://skills.sh/evil/repo/x',
  },
]

interface Fake {
  searches: string[]
  choices: unknown[]
}

/** Stands in for omo's search routes; `fail` makes the search answer what omo answers when skills.sh fails. */
async function fakeSearch(page: Page, bare: string, fail = false): Promise<Fake> {
  const fake: Fake = { searches: [], choices: [] }
  await page.route(/\/api\/skillsets\/search\?/, (route) => {
    const q = new URL(route.request().url()).searchParams.get('q') ?? ''
    fake.searches.push(q)
    if (fail)
      return route.fulfill({
        status: 503,
        contentType: 'application/json',
        body: JSON.stringify({ error: { code: 'unavailable', message: 'skills.sh could not be reached: it answered 500 Internal Server Error; try again later' } }),
      })
    return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ query: q, results: RESULTS }) })
  })
  await page.route(/\/api\/skillsets\/search\/resolve$/, (route) => {
    fake.choices.push(route.request().postDataJSON())
    return route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ repository: bare, ref: '', paths: ['skills/productivity/grill-me'], found: true, commit: 'abc' }),
    })
  })
  return fake
}

/** Fails the test when the page itself asks skills.sh or GitHub for anything. */
function noThirdParty(page: Page): string[] {
  const seen: string[] = []
  page.on('request', (r) => {
    const host = new URL(r.url()).hostname
    if (host.endsWith('skills.sh') || host.endsWith('github.com')) seen.push(r.url())
  })
  return seen
}

const stored = (omo: Omo) => omo.api<{ skill_sets: { name: string; repository: string; ref: string; paths: string[]; roles: string[] }[] }>('GET', '/api/skillsets')
const search = (page: Page) => page.locator('[data-skill-search]')
const editor = (page: Page, name: string) => page.locator(`[data-skillset-editor="${name}"]`)
const saveBar = (page: Page, name: string) => page.getByRole('region', { name: `Save changes to the skill set ${name}` })

async function openSkills(page: Page, omo: Omo) {
  await openBoard(page, omo)
  await page.goto(omo.url('/settings/skills'))
  await expect(page.locator('[data-new-skillset]')).toBeVisible()
}

test('a skill found on skills.sh fills a new set, warns, and is stored only on Save', async ({ page, omo }) => {
  const bare = grillRepo(omo)
  const fake = await fakeSearch(page, bare)
  const outside = noThirdParty(page)
  await openSkills(page, omo)

  // The note at the search.
  const note = search(page).locator('[data-skill-search-note]')
  await expect(note).toContainText('Powered by skills.sh')
  await expect(note).toContainText('omo is not affiliated with skills.sh or Vercel')
  await expect(note).toContainText('sends the search text, and nothing else, from omo to skills.sh')
  await expect(note.getByRole('link', { name: 'skills.sh' })).toHaveAttribute('href', 'https://skills.sh')

  // One character does not search; typing on searches once after a pause.
  const field = search(page).getByRole('searchbox', { name: 'Search' })
  await field.fill('g')
  await field.fill('grill')
  const first = page.locator('[data-skill-result="mattpocock/skills/grill-me"]')
  await expect(first).toBeVisible()
  expect(fake.searches).toEqual(['grill'])
  await expect(first.locator('[data-skill-result-name]')).toHaveText('grill-me')
  await expect(first.locator('[data-skill-result-source]')).toHaveText('mattpocock/skills')
  await expect(first.locator('[data-skill-result-installs]')).toHaveText('1.6M installs')
  await expect(first.locator('[data-skill-result-link]')).toHaveAttribute('href', 'https://skills.sh/mattpocock/skills/grill-me')
  // Someone else's text is text.
  const hostile = page.locator('[data-skill-result="evil/repo/x"] [data-skill-result-name]')
  await expect(hostile).toHaveText('<img src=x onerror="window.__owned=1">')
  await expect(page.locator('[data-skill-search] img')).toHaveCount(0)
  expect(await page.evaluate(() => (window as unknown as { __owned?: number }).__owned)).toBeUndefined()

  await first.getByRole('button', { name: 'Use grill-me' }).click()
  const ed = editor(page, 'grill-me')
  await expect(ed).toContainText('(not saved yet)')
  expect(fake.choices).toEqual([{ source: 'mattpocock/skills', skill_id: 'grill-me', name: 'grill-me' }])
  await expect(ed.getByLabel('Repository')).toHaveValue(bare)
  await expect(ed.getByLabel('Branch or tag')).toHaveValue('')
  await expect(ed.locator('[data-skillset-paths] input')).toHaveValue('skills/productivity/grill-me')
  await expect(ed.locator('[data-skillset-roles] [aria-pressed="true"]')).toHaveCount(0)
  const warning = ed.locator('[data-skillset-found]')
  await expect(warning).toContainText('Nobody reviewed this skill')
  await expect(warning).toContainText('A skill is instructions your agents follow.')
  await expect(warning.locator('[data-skillset-found-link]')).toHaveAttribute('href', 'https://skills.sh/mattpocock/skills/grill-me')
  expect((await stored(omo)).skill_sets).toEqual([])

  // Save asks first; "Not yet" stores nothing.
  await ed.locator('[data-skillset-role="ceo"]').click()
  await saveBar(page, 'grill-me').getByRole('button', { name: 'Save', exact: true }).click()
  const question = page.locator('[data-question]')
  await expect(question).toContainText('Nobody reviewed this skill')
  await question.locator('[data-question-cancel]').click()
  await expect(saveBar(page, 'grill-me').locator('[data-save-state]')).toHaveText('Unsaved changes')
  expect((await stored(omo)).skill_sets).toEqual([])

  await saveBar(page, 'grill-me').getByRole('button', { name: 'Save', exact: true }).click()
  await page.locator('[data-question-confirm]').click()
  await expect(saveBar(page, 'grill-me').locator('[data-save-state]')).toHaveText('Saved')
  await expect(ed.locator('[data-skillset-found]')).toHaveCount(0)
  expect((await stored(omo)).skill_sets).toMatchObject([{ name: 'grill-me', repository: bare, ref: '', paths: ['skills/productivity/grill-me'], roles: ['ceo'] }])
  await expect(page.locator('[data-skillset="grill-me"] [data-skillset-state]')).toHaveText('checked out')
  expect(outside).toEqual([])
})

test('when skills.sh cannot be reached the page says so and offers to try again', async ({ page, omo }) => {
  const fake = await fakeSearch(page, '/nonexistent', true)
  await openSkills(page, omo)
  await search(page).getByRole('searchbox', { name: 'Search' }).fill('grill')
  const error = search(page).locator('[data-skill-search-error]')
  await expect(error).toContainText('skills.sh could not be reached: it answered 500 Internal Server Error; try again later')
  await error.getByRole('button', { name: 'Try again' }).click()
  await expect.poll(() => fake.searches.length).toBe(2)
  await expect(error).toBeVisible()
})

const SHOTS = process.env.OMO_SCREENSHOTS ?? ''

for (const width of [1440, 390] as const) {
  for (const colorScheme of ['dark', 'light'] as const) {
    test.describe(`skill search at ${width} px, ${colorScheme} theme`, () => {
      test.use({ viewport: { width, height: width === 390 ? 844 : 900 }, colorScheme, reducedMotion: 'reduce' })

      test('results and a chosen skill have no violations', async ({ page, omo }) => {
        const bare = grillRepo(omo)
        await fakeSearch(page, bare)
        await openSkills(page, omo)
        await search(page).getByRole('searchbox', { name: 'Search' }).fill('grill')
        await expect(page.locator('[data-skill-result]')).toHaveCount(2)
        await page.evaluate(() => document.fonts.ready.then(() => undefined))
        expect((await new AxeBuilder({ page }).analyze()).violations.map((v) => v.id), 'results').toEqual([])
        expect(await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth)).toBe(false)
        if (SHOTS !== '') {
          mkdirSync(SHOTS, { recursive: true })
          await search(page).scrollIntoViewIfNeeded()
          await page.screenshot({ path: join(SHOTS, `skill-search-${colorScheme}-${width}-results.png`) })
        }
        await page.locator('[data-skill-result="mattpocock/skills/grill-me"] [data-skill-result-use]').click()
        await expect(editor(page, 'grill-me').locator('[data-skillset-found]')).toBeVisible()
        expect((await new AxeBuilder({ page }).analyze()).violations.map((v) => v.id), 'chosen').toEqual([])
        if (SHOTS !== '') {
          await editor(page, 'grill-me').locator('[data-skillset-found]').scrollIntoViewIfNeeded()
          await page.screenshot({ path: join(SHOTS, `skill-search-${colorScheme}-${width}-chosen.png`) })
        }
      })
    })
  }
}
