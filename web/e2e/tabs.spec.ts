// Every tab strip follows one rule: the first tab has no edge before it, the
// last none after it, and one separator line stands between two tabs. This
// measures the drawn borders of the strips in the ticket panel, on the
// ticket page, in the sidebar (open, collapsed and the phone's sheet) and the
// diff's project switcher, in both themes. With OMO_SCREENSHOTS=<folder> it
// writes fix4-tabs-<theme>-<width>.png there.
import { mkdirSync, realpathSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import type { Locator, Page } from '@playwright/test'
import { openBoard } from './support/board'
import { initRepo } from './support/git'
import { expect, test } from './support/omo'

const SHOTS = process.env.OMO_SCREENSHOTS ?? ''

interface Edges {
  before: number
  after: number
}

/** The border widths of each tab of a strip, before and after it along the strip. */
async function edgesOf(list: Locator): Promise<Edges[]> {
  return list.evaluate((el) => {
    const vertical = el.getAttribute('aria-orientation') === 'vertical'
    return [...el.querySelectorAll<HTMLElement>('[role="tab"]')].map((tab) => {
      const s = getComputedStyle(tab)
      const w = (side: string) => (s.getPropertyValue(`border-${side}-style`) === 'none' ? 0 : parseFloat(s.getPropertyValue(`border-${side}-width`)))
      return vertical ? { before: w('top'), after: w('bottom') } : { before: w('left'), after: w('right') }
    })
  })
}

async function expectOneRule(list: Locator, where: string): Promise<void> {
  await expect(list).toBeVisible()
  const edges = await edgesOf(list)
  expect(edges.length, `${where}: tabs`).toBeGreaterThan(1)
  expect(edges[0]!.before, `${where}: the first tab has no edge before it`).toBe(0)
  expect(edges[edges.length - 1]!.after, `${where}: the last tab has no edge after it`).toBe(0)
  for (let i = 0; i + 1 < edges.length; i++) {
    expect(edges[i]!.after + edges[i + 1]!.before, `${where}: one line between tab ${i + 1} and tab ${i + 2}`).toBeGreaterThan(0)
    expect(edges[i]!.after === 0 || edges[i + 1]!.before === 0, `${where}: not two lines between tab ${i + 1} and tab ${i + 2}`).toBe(true)
  }
}

const tabsIn = (root: Locator, name: RegExp | string) => root.getByRole('tablist', { name })

async function secondProject(page: Page, omo: import('./support/omo').Omo): Promise<string> {
  void page
  const dir = join(realpathSync(tmpdir()), `omo-e2e-tabs-${process.pid}-${Date.now()}`)
  initRepo(dir)
  return (await omo.api<{ id: string }>('POST', '/api/projects', { path: dir, name: 'second' })).id
}

for (const theme of ['dark', 'light'] as const) {
  test(`every tab strip has no edge before its first tab and none after its last (${theme}, 1440 px)`, async ({ page, omo }) => {
    await page.emulateMedia({ colorScheme: theme })
    await page.addInitScript(() => localStorage.setItem('omo.sidebar', JSON.stringify({ open: true, width: 420, tab: 'agents' })))
    await page.setViewportSize({ width: 1440, height: 900 })
    const second = await secondProject(page, omo)
    const story = await omo.createTicket({ title: 'A story with a task', type: 'story', projects: [omo.project, second] })
    await omo.createTicket({ title: 'The task', story })

    await openBoard(page, omo)
    await expectOneRule(tabsIn(page.locator('[data-sidebar]'), 'Sidebar'), 'the open sidebar')

    await page.goto(omo.url(`/${story}`))
    const panel = page.locator('[data-ticket-panel]')
    await expectOneRule(tabsIn(panel, `Activity on #${story}`), 'the panel')
    if (SHOTS !== '') {
      mkdirSync(SHOTS, { recursive: true })
      await tabsIn(panel, `Activity on #${story}`).evaluate((el) => el.scrollIntoView({ block: 'center' }))
      await page.screenshot({ path: join(SHOTS, `fix4-tabs-${theme}-1440.png`) })
    }
    await panel.getByRole('tab', { name: 'Diff' }).click()
    await expectOneRule(tabsIn(panel, `Projects of #${story}`), "the panel diff's project switcher")

    await page.goto(omo.url(`/ticket/${story}`))
    await expectOneRule(tabsIn(page.locator('[data-ticket-page]'), `Activity on #${story}`), 'the ticket page')

    await page.locator('[data-sidebar-toggle]').click()
    const strip = page.locator('[data-sidebar]').getByRole('tablist', { name: 'Sidebar' })
    await expect(strip).toHaveAttribute('aria-orientation', 'vertical')
    await expectOneRule(strip, 'the collapsed sidebar')
  })

  test(`on a phone the tab strips follow the same rule (${theme}, 390 px)`, async ({ page, omo }) => {
    await page.emulateMedia({ colorScheme: theme })
    await page.setViewportSize({ width: 390, height: 844 })
    const story = await omo.createTicket({ title: 'A story with a task', type: 'story' })
    await omo.createTicket({ title: 'The task', story })
    await openBoard(page, omo)
    await page.goto(omo.url(`/${story}`))
    const panel = page.locator('[data-ticket-panel]')
    await expectOneRule(tabsIn(panel, `Activity on #${story}`), 'the panel')
    if (SHOTS !== '') {
      mkdirSync(SHOTS, { recursive: true })
      await tabsIn(panel, `Activity on #${story}`).evaluate((el) => el.scrollIntoView({ block: 'center' }))
      await page.screenshot({ path: join(SHOTS, `fix4-tabs-${theme}-390.png`) })
    }
    await page.goto(omo.url(`/ticket/${story}`))
    await expectOneRule(tabsIn(page.locator('[data-ticket-page]'), `Activity on #${story}`), 'the ticket page')
    await page.goto(omo.url('/'))
    await page.getByRole('button', { name: 'Sidebar' }).click()
    await expectOneRule(tabsIn(page.getByRole('dialog', { name: 'Agents and terminals' }), 'Sidebar'), "the sidebar's sheet")
  })
}
