import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import AxeBuilder from '@axe-core/playwright'
import type { Page } from '@playwright/test'
import { card, openBoard } from './support/board'
import { initRepo } from './support/git'
import { expect, test, type Omo } from './support/omo'

interface ServerTicket {
  number: number
  title: string
  story: number | null
  supervisor: string
  model: string
  review: string | null
  when_approved: string | null
  depends_on: number[]
  projects: { project: string; base_branch: string; branch: string }[]
}

const demo = { path: '{{repo}}', name: 'demo' }
const PROFILE = 'cli: claude\ncommand: claude\nmodels:\n  sonnet:\n    args: ["--model", "sonnet"]\n  opus:\n    args: ["--model", "opus"]\n'

const panel = (page: Page) => page.locator('dialog[data-ticket-panel]')
const field = (page: Page, name: string) => panel(page).locator(`[data-field="${name}"]`)
const server = (omo: Omo, n: number) => omo.api<ServerTicket>('GET', `/api/tickets/${n}`)

async function openPanel(page: Page, omo: Omo, n: number) {
  await openBoard(page, omo)
  await card(page, n).locator('[data-card-link]').click()
  await expect(panel(page)).toBeVisible()
  await expect(field(page, 'review')).toBeVisible()
}

/** Writes the profile file by hand and waits until omo lists its models. */
async function addModels(omo: Omo) {
  const dir = join(omo.home, 'omo', 'profiles')
  mkdirSync(dir, { recursive: true })
  writeFileSync(join(dir, 'claude.yaml'), PROFILE)
  await expect.poll(async () => (await omo.api<{ id: string }[]>('GET', '/api/models')).map((m) => m.id).sort()).toEqual(['claude/opus', 'claude/sonnet'])
}

test.describe('without a seed', () => {
  test('adding a project from the project field selects it and saves nothing until Save', async ({ page, omo }) => {
    const second = join(omo.home, 'second-repo')
    initRepo(second)
    const n = await omo.createTicket({ title: 'A standalone task' })
    await openPanel(page, omo, n)
    await field(page, 'projects').getByRole('button', { name: /Project/ }).click()
    await field(page, 'projects').getByRole('button', { name: 'Add project…' }).click()
    const add = page.getByRole('dialog', { name: 'Add project' })
    await add.getByLabel('Folder').fill(second)
    await add.getByRole('button', { name: 'Add project' }).click()
    await expect(add).toBeHidden()
    // Still editing, the new project is chosen, and nothing was sent.
    await expect(field(page, 'projects').getByRole('radio', { checked: true })).toHaveCount(1)
    await expect(field(page, 'projects').getByRole('radio', { checked: true })).toHaveAccessibleName(/second-repo/)
    expect((await server(omo, n)).projects.map((p) => p.project)).toEqual([omo.project])
    await field(page, 'projects').getByRole('button', { name: 'Save' }).click()
    await expect(field(page, 'projects')).not.toHaveAttribute('data-editing', '')
    await expect(field(page, 'projects')).toContainText('second-repo')
    expect((await server(omo, n)).projects).toHaveLength(1)
    expect((await server(omo, n)).projects[0]?.project).not.toBe(omo.project)
  })

  test('edit supervisor, model, review and when approved; a second page sees each change', async ({ page, omo }) => {
    await addModels(omo)
    const story = await omo.createTicket({ type: 'story', title: 'The story' })
    const inStory = await omo.createTicket({ title: 'Task in the story', story })
    const plain = await omo.createTicket({ title: 'A standalone task' })
    await openBoard(page, omo)
    const second = await page.context().newPage()
    await second.goto(omo.url(`/${plain}`))
    await expect(panel(second)).toBeVisible()

    // Supervisor of a task in a story: the product manager by default, the user on request.
    await card(page, inStory).locator('[data-card-link]').click()
    await expect(field(page, 'supervisor')).toContainText('Product manager')
    await field(page, 'supervisor').getByRole('button', { name: /Supervisor/ }).click()
    await field(page, 'supervisor').getByRole('combobox', { name: 'Supervisor' }).selectOption('user')
    await field(page, 'supervisor').getByRole('button', { name: 'Save' }).click()
    await expect(field(page, 'supervisor')).not.toHaveAttribute('data-editing', '')
    await expect(field(page, 'supervisor')).toContainText('You')
    expect((await server(omo, inStory)).supervisor).toBe('user')
    await page.keyboard.press('Escape')
    await expect(panel(page)).toHaveCount(0)

    // The rest on a standalone task, watched on the second page.
    await card(page, plain).locator('[data-card-link]').click()
    await expect(field(page, 'model')).toContainText("Default (the role's model)")
    await expect(field(page, 'review')).toContainText('Default (Agent)')
    await expect(field(page, 'when_approved')).toContainText('Default (Merge locally)')

    await field(page, 'model').getByRole('button', { name: /Model/ }).click()
    await expect(field(page, 'model').getByRole('option')).toHaveText(["Default (the role's model)", 'claude/opus', 'claude/sonnet'])
    await field(page, 'model').getByRole('combobox', { name: 'Model' }).selectOption('claude/opus')
    await field(page, 'model').getByRole('button', { name: 'Save' }).click()
    await expect(field(page, 'model')).not.toHaveAttribute('data-editing', '')
    await expect(field(page, 'model')).toContainText('claude/opus')
    await expect(field(second, 'model')).toContainText('claude/opus')

    // By keyboard: Enter opens, the list takes a choice, Enter saves.
    const review = field(page, 'review')
    await review.getByRole('button', { name: /Review/ }).focus()
    await page.keyboard.press('Enter')
    const reviewList = review.getByRole('combobox', { name: 'Review' })
    await expect(reviewList).toBeFocused()
    await reviewList.selectOption('human')
    await page.keyboard.press('Enter')
    await expect(review).toContainText('Human')
    await expect(field(second, 'review')).toContainText('Human')

    await field(page, 'when_approved').getByRole('button', { name: /When approved/ }).click()
    await field(page, 'when_approved').getByRole('combobox').selectOption('export')
    await field(page, 'when_approved').getByRole('button', { name: 'Save' }).click()
    await expect(field(page, 'when_approved')).not.toHaveAttribute('data-editing', '')
    await expect(field(page, 'when_approved')).toContainText('Export as a pull request')
    await expect(field(second, 'when_approved')).toContainText('Export as a pull request')

    const t = await server(omo, plain)
    expect([t.model, t.review, t.when_approved]).toEqual(['claude/opus', 'human', 'export'])

    // Default again: the server stores nothing and the field says so.
    await field(page, 'review').getByRole('button', { name: /Review/ }).click()
    await field(page, 'review').getByRole('combobox').selectOption('')
    await field(page, 'review').getByRole('button', { name: 'Save' }).click()
    await expect(field(page, 'review')).not.toHaveAttribute('data-editing', '')
    await expect(field(page, 'review')).toContainText('Default (Agent)')
    expect((await server(omo, plain)).review).toBeNull()
  })

  test('Escape cancels an edit and leaves the panel open', async ({ page, omo }) => {
    const n = await omo.createTicket({ title: 'Keep as is' })
    await openPanel(page, omo, n)
    await field(page, 'review').getByRole('button', { name: /Review/ }).click()
    await field(page, 'review').getByRole('combobox').selectOption('human')
    await page.keyboard.press('Escape')
    await expect(field(page, 'review').getByRole('combobox')).toHaveCount(0)
    await expect(panel(page)).toBeVisible()
    await expect(field(page, 'review')).toContainText('Default (Agent)')
    expect((await server(omo, n)).review).toBeNull()
  })

  test('changing review to Human on a story shows the full-suite note', async ({ page, omo }) => {
    const n = await omo.createTicket({ type: 'story', title: 'A story' })
    await openPanel(page, omo, n)
    await expect(field(page, 'review').locator('[data-full-suite-note]')).toHaveCount(0)
    await field(page, 'review').getByRole('button', { name: /Review/ }).click()
    await field(page, 'review').getByRole('combobox').selectOption('human')
    await expect(field(page, 'review').locator('[data-full-suite-note]')).toHaveText('No agent runs the full test suite for this story.')
    await field(page, 'review').getByRole('button', { name: 'Save' }).click()
    await expect(field(page, 'review')).not.toHaveAttribute('data-editing', '')
    await expect(field(page, 'review')).toContainText('Human')
    await expect(field(page, 'review').locator('[data-full-suite-note]')).toBeVisible()
  })

  test('a change made elsewhere while a field is open is announced, not overwritten', async ({ page, omo }) => {
    const n = await omo.createTicket({ title: 'Edited twice' })
    await openPanel(page, omo, n)
    await field(page, 'review').getByRole('button', { name: /Review/ }).click()
    await field(page, 'review').getByRole('combobox').selectOption('human')
    await omo.api('PATCH', `/api/tickets/${n}`, { review: 'skipped' })
    await expect(field(page, 'review').locator('[data-changed]')).toContainText('It is now Skipped.')
    await expect(field(page, 'review').getByRole('combobox')).toHaveValue('human')
    await field(page, 'review').getByRole('button', { name: 'Use the new value' }).click()
    await expect(field(page, 'review').getByRole('combobox')).toHaveValue('skipped')
  })

  test('adding a dependency shows "waiting for #n" on the card and removing it clears it', async ({ page, omo }) => {
    const first = await omo.createTicket({ title: 'Needed first' })
    const later = await omo.createTicket({ title: 'Comes later' })
    await openPanel(page, omo, later)
    const deps = field(page, 'depends_on')
    await expect(deps).toContainText('Nothing')
    await deps.getByLabel('Add a dependency').fill('needed')
    await deps.locator(`[data-candidate="${first}"]`).click()
    const item = deps.locator(`[data-dependency="${first}"]`)
    await expect(item).toContainText(`#${first} Needed first`)
    await expect(item).toContainText('Planning')
    await expect(item.locator('[data-unsatisfied]')).toHaveText(`waiting for #${first}`)
    await expect(card(page, later)).toContainText(`waiting for #${first}`)
    // The other ticket lists what it blocks, as a link.
    await expect(field(page, 'blocks')).toContainText('Nothing waits for this ticket.')
    await page.goto(omo.url(`/${first}`))
    await expect(field(page, 'blocks').getByRole('link', { name: `#${later} Comes later` })).toBeVisible()
    await field(page, 'blocks').getByRole('link', { name: `#${later} Comes later` }).click()
    await expect(page).toHaveURL(new RegExp(`/${later}$`))

    await deps.getByRole('button', { name: `Remove the dependency on #${first} Needed first` }).click()
    await expect(deps).toContainText('Nothing')
    await expect(card(page, later)).not.toContainText(`waiting for #${first}`)
    expect((await server(omo, later)).depends_on).toEqual([])
  })

  test('a dependency across a story boundary and a cycle are refused with the server message', async ({ page, omo }) => {
    const story = await omo.createTicket({ type: 'story', title: 'A story' })
    const inside = await omo.createTicket({ title: 'Inside', story })
    const a = await omo.createTicket({ title: 'Alpha' })
    const b = await omo.createTicket({ title: 'Bravo' })
    const c = await omo.createTicket({ title: 'Charlie' })
    await openPanel(page, omo, inside)
    const deps = field(page, 'depends_on')
    // Not offered, but tried anyway: omo has the last word.
    await deps.getByLabel('Add a dependency').fill(`#${a}`)
    await expect(deps.locator('[data-candidate]')).toHaveCount(0)
    await deps.getByRole('button', { name: `Try #${a} anyway` }).click()
    await expect(deps.locator('[data-field-error]')).toHaveText(`#${inside} cannot depend on #${a}: a task in a story depends only on tasks of the same story`)
    await expect(deps).toContainText('Nothing')

    // A chain c -> b -> a; a depending on c closes it.
    await omo.api('POST', `/api/tickets/${b}/dependencies`, { depends_on: a })
    await omo.api('POST', `/api/tickets/${c}/dependencies`, { depends_on: b })
    await page.goto(omo.url(`/${a}`))
    await expect(field(page, 'review')).toBeVisible()
    const depsA = field(page, 'depends_on')
    await depsA.getByLabel('Add a dependency').fill('charlie')
    await depsA.locator(`[data-candidate="${c}"]`).click()
    const message = depsA.locator('[data-field-error]')
    await expect(message).toContainText(`#${a} cannot depend on #${c}: that would be a cycle`)
    await expect(message).toContainText(`#${a} → #${c} → #${b} → #${a}`)
    expect((await server(omo, a)).depends_on).toEqual([])
  })

  test('moving a task into a story says what changes, then the supervisor is the product manager', async ({ page, omo }) => {
    const story = await omo.createTicket({ type: 'story', title: 'Target story' })
    const n = await omo.createTicket({ title: 'Wanderer' })
    await openPanel(page, omo, n)
    await expect(field(page, 'story')).toContainText('None, a standalone task')
    await field(page, 'story').getByRole('button', { name: 'Change story' }).click()
    const note = field(page, 'story').locator('[data-move-note]')
    await expect(note).toContainText('changes its supervisor to the new default')
    await expect(note).toContainText("uses one of the story's projects")
    await field(page, 'story').getByRole('combobox', { name: 'Story' }).selectOption(String(story))
    await field(page, 'story').getByRole('button', { name: 'Save' }).click()
    await expect(field(page, 'story')).not.toHaveAttribute('data-editing', '')
    await expect(field(page, 'story').getByRole('link', { name: `#${story} Target story` })).toBeVisible()
    await expect(field(page, 'supervisor')).toContainText('Product manager')
    await expect(field(page, 'when_approved')).toHaveCount(0)
    const t = await server(omo, n)
    expect([t.story, t.supervisor]).toEqual([story, 'pm'])
  })

  test('on a 390 px screen the fields stack below the description and can be edited', async ({ page, omo }) => {
    await page.setViewportSize({ width: 390, height: 844 })
    const n = await omo.createTicket({ title: 'Small screen' })
    await omo.api('PUT', `/api/tickets/${n}/description`, { text: 'Short text.' })
    await page.goto(omo.accessUrl.replace(/\/#/, `/${n}#`))
    await expect(panel(page)).toBeVisible()
    await expect(field(page, 'review')).toBeVisible()
    const description = await panel(page).locator('[data-description]').boundingBox()
    const fields = await panel(page).locator('[data-ticket-fields]').boundingBox()
    expect(fields!.y).toBeGreaterThanOrEqual(description!.y + description!.height)
    expect(fields!.width).toBeLessThanOrEqual(390)
    await field(page, 'review').getByRole('button', { name: /Review/ }).click()
    await field(page, 'review').getByRole('combobox').selectOption('human')
    await field(page, 'review').getByRole('button', { name: 'Save' }).click()
    await expect(field(page, 'review')).not.toHaveAttribute('data-editing', '')
    await expect(field(page, 'review')).toContainText('Human')
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
    expect(await panel(page).evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true)
    const save = await field(page, 'when_approved').getByRole('button', { name: /When approved/ }).boundingBox()
    expect(save!.height).toBeGreaterThanOrEqual(44)
  })

  for (const scheme of ['dark', 'light'] as const) {
    test(`the panel with a field open has no accessibility violations (${scheme})`, async ({ page, omo }) => {
      await page.emulateMedia({ colorScheme: scheme })
      await addModels(omo)
      const other = await omo.createTicket({ title: 'Something else' })
      const n = await omo.createTicket({ title: 'Under test' })
      await omo.api('POST', `/api/tickets/${n}/dependencies`, { depends_on: other })
      await openPanel(page, omo, n)
      await field(page, 'model').getByRole('button', { name: /Model/ }).click()
      await field(page, 'depends_on').getByLabel('Add a dependency').fill('some')
      await expect(field(page, 'depends_on').locator('[data-candidate]')).toHaveCount(0)
      await field(page, 'depends_on').getByLabel('Add a dependency').fill('#999')
      await expect(field(page, 'depends_on').getByRole('button', { name: 'Try #999 anyway' })).toBeVisible()
      const results = await new AxeBuilder({ page }).analyze()
      expect(results.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((x) => x.target.join(' ')) }))).toEqual([])
    })
  }
})

test.describe('seeded', () => {
  test.use({
    omoOptions: {
      seed: {
        projects: [demo],
        tickets: [
          { ref: 'running', type: 'task', title: 'Being built', to: 'in_progress', projects: ['demo'], branch: 'omo/running' },
        ],
      },
    },
  })

  test('a ticket in progress shows what is locked and why, and what stays editable', async ({ page, omo }) => {
    const n = omo.refs.running!
    await openPanel(page, omo, n)
    await expect(field(page, 'projects').locator('[data-locked]')).toHaveText('Locked while the branch omo/running exists.')
    await expect(field(page, 'base_branch').locator('[data-locked]')).toHaveText('Locked while the branch omo/running exists.')
    await expect(field(page, 'projects').getByRole('button')).toHaveCount(0)
    await expect(field(page, 'branch')).toContainText('omo/running')
    await expect(field(page, 'base_branch')).toContainText('main')
    // Everything else can still be changed.
    await expect(field(page, 'review').getByRole('button', { name: /Review/ })).toBeEnabled()
    await expect(field(page, 'supervisor').getByRole('button', { name: /Supervisor/ })).toBeEnabled()
    await field(page, 'review').getByRole('button', { name: /Review/ }).click()
    await field(page, 'review').getByRole('combobox').selectOption('skipped')
    await field(page, 'review').getByRole('button', { name: 'Save' }).click()
    await expect(field(page, 'review')).not.toHaveAttribute('data-editing', '')
    await expect(field(page, 'review')).toContainText('Skipped')
  })
})
