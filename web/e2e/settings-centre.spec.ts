import { appendFileSync, mkdirSync } from 'node:fs'
import { join } from 'node:path'
import type { Page } from '@playwright/test'
import { openBoard } from './support/board'
import { expect, FAKE_AGENT_BINARY, test, type Omo } from './support/omo'

// The settings (the section list and the page beside it) stand in the middle
// of the room next to the sidebar: on a wide screen the free space left of
// the list equals the free space right of the page, and the page scrolls
// with its scrollbar at the window's edge. The list and the page keep their
// widths; where the room is short the block sits where it always did. With
// OMO_SCREENSHOTS=<folder> the test also writes the screenshots
// (fix3-settings-centre-<state>-<width>-<closed|open>.png), with
// MEASURE_REPORT=<file> every measurement is written there.

const SHOTS = process.env.OMO_SCREENSHOTS ?? ''
const REPORT = process.env.MEASURE_REPORT ?? ''
const STATE = process.env.SHOTS_STATE ?? 'after'

/** The padding the board's main area keeps around its content. */
const EDGE = 16

interface Measure {
  /** Free space between the main area's left edge and the section list. */
  left: number
  /** Free space between the page content's right edge and the main area's right edge. */
  right: number
  navWidth: number
  columnWidth: number
  /** Edges of the Save bar and the page column (the bar's are null without a bar). */
  bar: { l: number; r: number } | null
  column: { l: number; r: number }
  scrollerRight: number
  windowWidth: number
}

async function measure(page: Page): Promise<Measure> {
  await page.evaluate(() => document.fonts.ready.then(() => undefined))
  return page.evaluate(() => {
    const rect = (sel: string) => document.querySelector(sel)?.getBoundingClientRect()
    const main = rect('main')!
    const nav = rect('[data-settings-nav]')!
    const column = rect('[data-settings-page] > div')!
    const bar = rect('[aria-label="Save changes"]')
    const scroller = rect('[data-settings-page]')!
    return {
      left: nav.left - main.left,
      right: main.right - column.right,
      navWidth: nav.width,
      columnWidth: column.width,
      bar: bar ? { l: bar.left, r: bar.right } : null,
      column: { l: column.left, r: column.right },
      scrollerRight: scroller.right,
      windowWidth: window.innerWidth,
    }
  })
}

async function putProfile(omo: Omo, name: string) {
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

async function show(page: Page, omo: Omo, section: string, heading: string) {
  await page.goto(omo.url(`/settings/${section}`))
  await expect(title(page)).toHaveText(heading)
  if (section === 'agents') {
    await page.locator('[data-profile="fake"]').click()
    await expect(page.locator('[data-profile-editor="fake"]')).toBeVisible()
  }
  if (section === 'limits') await expect(page.getByRole('spinbutton', { name: 'Max running agents' })).toBeVisible()
  if (section === 'projects') await expect(page.locator('[data-settings-loading]')).toHaveCount(0)
}

const SECTIONS = [
  { id: 'projects', heading: 'Projects' },
  { id: 'agents', heading: 'Agents' },
  { id: 'limits', heading: 'Limits' },
]

async function arrange(page: Page, omo: Omo, width: number, height: number, sidebar: 'closed' | 'open') {
  await putProfile(omo, 'fake')
  await page.setViewportSize({ width, height })
  await openBoard(page, omo)
  const side = page.locator('[data-sidebar]')
  if (sidebar === 'open') {
    await page.locator('[data-sidebar-toggle]').click()
    await expect(side).toHaveAttribute('data-open', 'true')
  } else {
    await expect(side).toHaveAttribute('data-open', 'false')
  }
}

/** The measurement of every section; with `layout` it is judged by that rule. */
async function walk(page: Page, omo: Omo, label: string, judge: (m: Measure, where: string) => void) {
  for (const s of SECTIONS) {
    await show(page, omo, s.id, s.heading)
    const m = await measure(page)
    if (REPORT !== '') appendFileSync(REPORT, `${label}\t${s.id}\t${JSON.stringify(m)}\n`)
    judge(m, `${label} ${s.id}`)
  }
}

function expectCentred(m: Measure, where: string) {
  expect(m.left, `${where}: free space left of the list`).toBeGreaterThan(EDGE)
  expect(Math.abs(m.left - m.right), `${where}: left ${m.left}, right ${m.right}`).toBeLessThanOrEqual(1)
  expect(m.navWidth, `${where}: the list keeps its width`).toBe(256)
  expect(m.columnWidth, `${where}: the page keeps its reading width`).toBe(880)
  expect(m.scrollerRight, `${where}: the page scrolls at the window's edge`).toBe(m.windowWidth)
}

function expectSaveBarOnColumn(m: Measure, where: string) {
  if (m.bar === null) return
  expect(m.bar.l, `${where}: Save bar left`).toBeCloseTo(m.column.l, 0)
  expect(m.bar.r, `${where}: Save bar right`).toBeCloseTo(m.column.r, 0)
}

for (const width of [1920, 2560, 3440, 3840]) {
  for (const sidebar of ['closed', 'open'] as const) {
    test(`at ${width} px with the sidebar ${sidebar} the settings stand in the middle`, async ({ page, omo }) => {
      await arrange(page, omo, width, 1440, sidebar)
      if (SHOTS !== '' && width !== 1920) {
        mkdirSync(SHOTS, { recursive: true })
        await page.emulateMedia({ colorScheme: 'dark' })
        await show(page, omo, 'limits', 'Limits')
        await page.screenshot({ path: join(SHOTS, `fix3-settings-centre-${STATE}-${width}-${sidebar}.png`) })
      }
      let sawBar = false
      await walk(page, omo, `${width} ${sidebar}`, (m, where) => {
        expectCentred(m, where)
        expectSaveBarOnColumn(m, where)
        if (m.bar !== null) sawBar = true
      })
      expect(sawBar, 'a Save bar was measured').toBe(true)
    })
  }
}

for (const width of [1440, 1024]) {
  for (const sidebar of ['closed', 'open'] as const) {
    test(`at ${width} px with the sidebar ${sidebar} the settings sit as they did, or in the middle where there is room`, async ({ page, omo }) => {
      await arrange(page, omo, width, 900, sidebar)
      await walk(page, omo, `${width} ${sidebar}`, (m, where) => {
        if (m.left > EDGE + 1) {
          expect(Math.abs(m.left - m.right), `${where}: left ${m.left}, right ${m.right}`).toBeLessThanOrEqual(1)
        } else {
          expect(m.left, `${where}: no free space, the list at the edge`).toBeCloseTo(EDGE, 0)
          expect(m.right, `${where}: room kept right of the page`).toBeGreaterThanOrEqual(EDGE - 1)
        }
        expect(m.navWidth, `${where}: the list keeps its width`).toBe(256)
        expect(m.columnWidth, `${where}: the page is not wider than before`).toBeLessThanOrEqual(880)
        expect(m.scrollerRight, `${where}: the page scrolls at the window's edge`).toBe(m.windowWidth)
        expectSaveBarOnColumn(m, where)
      })
    })
  }
}
