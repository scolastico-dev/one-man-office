import AxeBuilder from '@axe-core/playwright'
import type { Page } from '@playwright/test'
import { expect, test } from './support/omo'
import { expectCardIn, openBoard } from './support/board'
import { expectNoAgentProcesses, script, waitForSession } from './support/agents'
import { expectTerminalText, terminalIn } from './support/terminal'

async function violations(page: Page) {
  const results = await new AxeBuilder({ page }).analyze()
  return results.violations.map((v) => ({ id: v.id, impact: v.impact, help: v.help, nodes: v.nodes.map((n) => n.target.join(' ')) }))
}

for (const scheme of ['dark', 'light'] as const) {
  test.describe(`${scheme} theme`, () => {
    test.use({ colorScheme: scheme })

    test('the locked screen has no accessibility violations', async ({ page, omo }) => {
      await page.goto(omo.url('/'))
      await expect(page.getByRole('heading', { level: 1, name: 'omo is locked' })).toBeVisible()
      // The bundled font is part of what is checked: wait until it is in use.
      await page.evaluate(() => document.fonts.ready.then(() => undefined))
      expect(await violations(page)).toEqual([])
    })

    test('the board has no accessibility violations', async ({ page, omo }) => {
      const a = await omo.createTicket({ title: 'A task in planning' })
      const story = await omo.createTicket({ title: 'A story', type: 'story' })
      const b = await omo.createTicket({ title: 'A task that is ready' })
      await omo.moveTicket(b, 'ready')
      await openBoard(page, omo)
      await expectCardIn(page, a, 'Planning')
      await expectCardIn(page, story, 'Planning')
      await expectCardIn(page, b, 'Ready For Dev')
      expect(await violations(page)).toEqual([])
    })

    test('the first steps on an empty board have no accessibility violations', async ({ page, omo }) => {
      await openBoard(page, omo)
      expect(await violations(page)).toEqual([])
    })
  })
}

// The panel's Diff tab (files, both themes), the description history dialog and the
// open bell are checked in diff.spec.ts, history.spec.ts and notifications.spec.ts
// (the bell at 1440 and 390 px); they are not repeated here.

const sidebar = (page: Page) => page.locator('[data-sidebar]')
const panel = (page: Page) => page.locator('dialog[data-ticket-panel]')

async function expectNoSideways(page: Page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= document.documentElement.clientWidth)).toBe(true)
}

for (const scheme of ['dark', 'light'] as const) {
  test.describe(`${scheme} theme, with agents`, () => {
    test.use({
      colorScheme: scheme,
      omoOptions: {
        agents: {
          developer: script('ready', 'print|developer output', 'ask|Which colour should it be?', 'wait|10m', 'hang'),
          ceo: script('ready', 'print|CEO ready', 'hang'),
        },
      },
    })

    for (const width of [1440, 390]) {
      test(`${width} px: the ticket panel on Comments, Agents and Tasks has no violations`, async ({ page, omo }) => {
        await page.setViewportSize({ width, height: width === 390 ? 844 : 900 })
        const story = await omo.createTicket({ title: 'A story', type: 'story' })
        await omo.createTicket({ title: 'A task of the story', story })
        const n = await omo.createTicket({ title: 'Ask me something' })
        await omo.moveTicket(n, 'ready')
        const session = await waitForSession(omo, n, 'developer', 'waiting')
        await openBoard(page, omo)

        await page.goto(omo.url(`/${n}`))
        await expect(panel(page).locator('[data-comment][data-question="open"]')).toHaveCount(1)
        expect(await violations(page)).toEqual([])
        if (width === 390) await expectNoSideways(page)

        await page.goto(omo.url(`/${n}?tab=agents`))
        await panel(page).locator(`[data-session="${session.id}"] [data-agent-row]`).click()
        const term = terminalIn(panel(page))
        await expectTerminalText(term, 'developer output')
        expect(await violations(page)).toEqual([])
        if (width === 390) {
          await expectNoSideways(page)
          expect(await panel(page).evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true)
        }

        await page.goto(omo.url(`/${story}?tab=tasks`))
        await expect(panel(page).locator('[data-task]')).toHaveCount(1)
        expect(await violations(page)).toEqual([])
        if (width === 390) {
          await expectNoSideways(page)
          expect(await panel(page).evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true)
        }
        await expectNoAgentProcesses(omo)
      })
    }

    test('1440 px: the expanded sidebar on Agents, CEO and Terminals has no violations', async ({ page, omo }) => {
      await page.setViewportSize({ width: 1440, height: 900 })
      const n = await omo.createTicket({ title: 'Work in the sidebar' })
      await omo.moveTicket(n, 'ready')
      await waitForSession(omo, n, 'developer', 'waiting')
      await openBoard(page, omo)
      await sidebar(page).getByRole('tab', { name: /^Agents/ }).click()
      await expect(sidebar(page)).toHaveAttribute('data-open', 'true')
      await expect(sidebar(page).locator('[data-slots]')).toContainText('In work:')
      expect(await violations(page)).toEqual([])

      await sidebar(page).getByRole('tab', { name: /^CEO/ }).click()
      await expectTerminalText(terminalIn(sidebar(page)), 'CEO ready')
      expect(await violations(page)).toEqual([])

      await sidebar(page).getByRole('tab', { name: /^Terminals/ }).click()
      await expect(sidebar(page).getByText('No terminal is open.')).toBeVisible()
      expect(await violations(page)).toEqual([])
      await sidebar(page).getByRole('button', { name: 'New terminal' }).click()
      await sidebar(page).getByRole('option', { name: /^demo / }).click()
      await expect(terminalIn(sidebar(page))).toBeVisible()
      expect(await violations(page)).toEqual([])
      for (const s of await omo.api<{ id: string }[]>('GET', '/api/terminals')) await omo.api('DELETE', `/api/terminals/${encodeURIComponent(s.id)}`)
    })

    test('390 px: the sidebar sheet on Agents, CEO and Terminals has no violations', async ({ page, omo }) => {
      await page.setViewportSize({ width: 390, height: 844 })
      const n = await omo.createTicket({ title: 'Work on the phone' })
      await omo.moveTicket(n, 'ready')
      await waitForSession(omo, n, 'developer', 'waiting')
      await openBoard(page, omo)
      await page.getByRole('button', { name: 'Sidebar' }).click()
      const sheet = page.getByRole('dialog', { name: 'Agents and terminals' })
      await expect(sheet.locator('[data-slots]')).toContainText('In work:')
      expect(await violations(page)).toEqual([])
      await expectNoSideways(page)

      await sheet.getByRole('tab', { name: /^CEO/ }).click()
      await expectTerminalText(terminalIn(sheet), 'CEO ready')
      expect(await violations(page)).toEqual([])

      await sheet.getByRole('tab', { name: /^Terminals/ }).click()
      await expect(sheet.getByText('No terminal is open.')).toBeVisible()
      expect(await violations(page)).toEqual([])
      await expectNoSideways(page)
    })
  })
}

// Every settings section, the folder picker and the section list are checked in
// the spec of each page (both themes, 1440 and 390 px); the dialogs and messages
// that no page spec runs axe on are checked here.
async function settled(page: Page) {
  await page.evaluate(async () => {
    await document.fonts.ready
    await Promise.all(document.getAnimations().map((a) => a.finished.catch(() => undefined)))
  })
  return violations(page)
}

for (const scheme of ['dark', 'light'] as const) {
  for (const width of [1440, 390] as const) {
    test.describe(`settings dialogs, ${scheme} theme at ${width} px`, () => {
      test.use({ colorScheme: scheme, viewport: { width, height: width === 390 ? 844 : 900 }, reducedMotion: 'reduce' })

      test('the leave-without-saving dialog has no violations', async ({ page, omo }) => {
        await openBoard(page, omo)
        await page.goto(omo.url('/settings/limits'))
        const taskLimit = page.getByRole('spinbutton', { name: 'Tasks in work' })
        await expect(taskLimit).toBeVisible()
        await taskLimit.fill('3')
        await page.getByRole('link', { name: 'Back to board' }).click()
        const question = page.getByRole('dialog', { name: 'Leave without saving?' })
        await expect(question).toBeVisible()
        expect(await settled(page)).toEqual([])
        if (width === 390) await expectNoSideways(page)
      })

      test('the message that the settings could not be loaded has no violations', async ({ page, omo }) => {
        await openBoard(page, omo)
        await page.route(/\/assets\/SettingsView-[^/]*\.js$/, (route) => route.fulfill({ status: 404, body: 'not found' }))
        await page.goto(omo.url('/'))
        await page.getByRole('link', { name: 'Settings', exact: true }).click()
        const failed = page.locator('[data-route-failed]')
        await expect(failed).toContainText('Reload the page to get the current version.')
        expect(await settled(page)).toEqual([])
        if (width === 390) await expectNoSideways(page)
      })

      test('the message that a settings page could not be loaded has no violations', async ({ page, omo }) => {
        await openBoard(page, omo)
        await page.route(/\/assets\/LimitsPage-[^/]*\.js$/, (route) => route.fulfill({ status: 404, body: 'not found' }))
        await page.goto(omo.url('/settings/limits'))
        const failed = page.locator('[data-settings-page-failed]')
        await expect(failed).toContainText('Reload the page to get the current version.')
        expect(await settled(page)).toEqual([])
        if (width === 390) await expectNoSideways(page)
      })

      test('the lock screen over a settings page with unsaved changes has no violations', async ({ page, omo }) => {
        await openBoard(page, omo)
        await page.goto(omo.url('/settings/limits'))
        const taskLimit = page.getByRole('spinbutton', { name: 'Tasks in work' })
        await expect(taskLimit).toBeVisible()
        await taskLimit.fill('6')
        await omo.stop()
        expect((await omo.cli(['config', 'rotate-token'])).code).toBe(0)
        await omo.start()
        await expect(page.getByRole('heading', { level: 1, name: 'omo is locked' })).toBeVisible()
        await expect(page.locator('[data-board-kept]')).toHaveAttribute('inert', '')
        expect(await settled(page)).toEqual([])
        if (width === 390) await expectNoSideways(page)
      })
    })
  }
}
