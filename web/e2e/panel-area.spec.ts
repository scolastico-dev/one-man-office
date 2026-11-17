// A ticket's panel on a wide window covers only the board's area: right of
// the sidebar and below the top bar. It is not modal: the sidebar (with live
// terminals) and the top bar stay usable, the focus can leave it and come
// back, and only the columns under it are inert. Escape closes it while the
// focus is inside. Dialogs opened from it are still modal over everything; the
// lock screen still covers everything. With OMO_SCREENSHOTS=<folder> it writes
// panel-overlay-<theme>-<width>.png there.
import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import AxeBuilder from '@axe-core/playwright'
import type { Locator, Page } from '@playwright/test'
import { expectNoAgentProcesses, script, waitForSession } from './support/agents'
import { card, openBoard } from './support/board'
import { expect, test, type Omo } from './support/omo'
import { expectTerminalText, terminalIn, typeInTerminal } from './support/terminal'

const SHOTS = process.env.OMO_SCREENSHOTS ?? ''

const sidebar = (page: Page) => page.locator('[data-sidebar]')
const panel = (page: Page) => page.locator('dialog[data-ticket-panel]')
const topbar = (page: Page) => page.locator('header').first()

async function box(l: Locator) {
  const b = await l.boundingBox()
  if (!b) throw new Error('not shown')
  return b
}

/** The panel lies in the board's area: right of the sidebar, below the top bar, inside the window. */
async function expectInBoardArea(page: Page, where: string) {
  const p = await box(panel(page))
  const main = await box(page.locator('main').first())
  const side = await box(sidebar(page))
  const top = await box(topbar(page))
  const vw = page.viewportSize()!
  expect(p.x, `${where}: right of the sidebar`).toBeGreaterThanOrEqual(side.x + side.width - 1)
  expect(p.y, `${where}: below the top bar`).toBeGreaterThanOrEqual(top.y + top.height - 1)
  expect(p.x, `${where}: inside the board's area`).toBeGreaterThanOrEqual(main.x - 1)
  expect(p.x + p.width, `${where}: inside the window`).toBeLessThanOrEqual(vw.width + 1)
  expect(p.y + p.height, `${where}: inside the window`).toBeLessThanOrEqual(vw.height + 1)
}

async function insidePanel(page: Page): Promise<boolean> {
  return page.evaluate(() => !!document.activeElement?.closest('dialog[data-ticket-panel]'))
}

async function documentStill(page: Page) {
  await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))))
  return page.evaluate(() => {
    const el = document.scrollingElement ?? document.documentElement
    return { extra: el.scrollHeight - el.clientHeight, top: el.scrollTop, sideways: el.scrollWidth - el.clientWidth }
  })
}

async function violations(page: Page) {
  await page.evaluate(() => document.fonts.ready.then(() => undefined))
  const results = await new AxeBuilder({ page }).analyze()
  return results.violations.map((v) => ({ id: v.id, nodes: v.nodes.map((n) => n.target.join(' ')) }))
}

async function openShell(omo: Omo): Promise<string> {
  return (await omo.api<{ id: string }>('POST', '/api/terminals', { project: omo.project })).id
}

const DEVELOPER = script('ready', 'print|hello from the developer', 'hang')

test.describe('the panel in the board area', () => {
  test.use({ omoOptions: { agents: { developer: DEVELOPER } } })
  test.beforeEach(async ({ page, omo }) => {
    // zsh's new-user wizard would swallow the shell's first lines.
    writeFileSync(join(omo.home, '.zshrc'), '')
    await page.addInitScript(() => localStorage.setItem('omo.sidebar', JSON.stringify({ open: true, width: 480, tab: 'terminals' })))
    await page.setViewportSize({ width: 1440, height: 900 })
  })
  test.afterEach(async ({ omo }) => {
    for (const t of await omo.api<{ id: string }[]>('GET', '/api/terminals')) await omo.api('DELETE', `/api/terminals/${encodeURIComponent(t.id)}`)
    await omo.api('POST', '/api/agents/stop-all', {})
    await expectNoAgentProcesses(omo)
  })

  test('the sidebar and its live terminal stay usable beside an open panel; focus is free; Escape works only inside', async ({ page, omo }) => {
    const m = await omo.createTicket({ title: 'A plain ticket' })
    const shell = await openShell(omo)
    await openBoard(page, omo)
    await sidebar(page).locator(`[data-terminal="${shell}"]`).click()
    const term = terminalIn(sidebar(page))
    await typeInTerminal(page, term, "printf 'before=%s\\n' 1")
    await expectTerminalText(term, 'before=1')

    await card(page, m).locator('[data-card-link]').click()
    await expect(panel(page)).toBeVisible()
    await expect(panel(page).locator('[data-close-panel]')).toBeFocused()
    await expect(panel(page)).not.toHaveAttribute('aria-modal', 'true')
    await expectInBoardArea(page, 'sidebar open')
    // Only the columns under the panel are inert.
    await expect(sidebar(page)).not.toHaveAttribute('inert', /.*/)
    expect(await page.locator('[data-board]').evaluate((el) => el.closest('[inert]') !== null)).toBe(true)
    expect(await topbar(page).evaluate((el) => el.closest('[inert]') !== null)).toBe(false)

    // The terminal stays mounted and live; typing there (a "p" included) is the shell's, not a shortcut.
    await expect(term).toBeVisible()
    await typeInTerminal(page, term, "printf 'during=%s\\n' 2")
    await expectTerminalText(term, 'during=2')
    await expect(page).toHaveURL(new RegExp(`/${m}(\\?|$)`))
    // Escape in the terminal is the shell's: the panel stays.
    await page.keyboard.press('Escape')
    await expect(panel(page)).toBeVisible()

    // The focus is not trapped: Shift+Tab from the panel's first control leaves it.
    await panel(page).locator('[data-close-panel]').focus()
    let left = false
    for (let i = 0; i < 12 && !left; i++) {
      await page.keyboard.press('Shift+Tab')
      left = !(await insidePanel(page))
    }
    expect(left, 'Shift+Tab reaches the rest of the page').toBe(true)
    // Escape outside the panel does not close it.
    await sidebar(page).getByRole('tab', { name: /^Terminals/ }).focus()
    await page.keyboard.press('Escape')
    await expect(panel(page)).toBeVisible()

    // A filter of the top bar keeps the panel.
    await page.getByRole('combobox', { name: 'Supervisor' }).selectOption('user')
    await expect(page).toHaveURL(/supervisor=user/)
    await expect(panel(page)).toBeVisible()
    await expect(page).toHaveURL(new RegExp(`/${m}\\?`))

    // Back inside, Escape closes it, and the focus returns to the card.
    await panel(page).locator('[data-close-panel]').focus()
    await page.keyboard.press('Escape')
    await expect(panel(page)).toHaveCount(0)
    await expect(card(page, m).locator('[data-card-link]')).toBeFocused()
    await expect(page).toHaveURL(/\/\?supervisor=user$/)
  })

  test("collapsing or resizing the sidebar resizes the panel's area", async ({ page, omo }) => {
    const m = await omo.createTicket({ title: 'A plain ticket' })
    await openBoard(page, omo)
    await page.goto(omo.url(`/${m}`))
    await expect(panel(page)).toBeVisible()
    const area = page.locator('[data-ticket-area]')
    const wide = await box(area)
    const sep = sidebar(page).getByRole('separator', { name: 'Sidebar width' })
    await sep.focus()
    for (let i = 0; i < 5; i++) await page.keyboard.press('ArrowRight')
    await expect.poll(async () => (await box(area)).x).toBeGreaterThan(wide.x + 40)
    await expectInBoardArea(page, 'sidebar wider')
    expect(await documentStill(page)).toEqual({ extra: 0, top: 0, sideways: 0 })
    await sidebar(page).locator('[data-sidebar-toggle]').click()
    await expect.poll(async () => (await box(area)).x).toBeLessThan(wide.x - 200)
    await expectInBoardArea(page, 'sidebar collapsed')
    expect(await documentStill(page)).toEqual({ extra: 0, top: 0, sideways: 0 })
    await expect(panel(page)).toBeVisible()
  })

  test("the sidebar's Open #n switches the panel; one live view per terminal: the view opened last is live, the other says where", async ({ page, omo }) => {
    const m = await omo.createTicket({ title: 'Opened first' })
    const n = await omo.createTicket({ title: 'Worked on by an agent' })
    await omo.moveTicket(n, 'ready')
    const s = await waitForSession(omo, n, 'developer')
    await openBoard(page, omo)
    await page.goto(omo.url(`/${m}`))
    await expect(panel(page)).toBeVisible()

    await sidebar(page).getByRole('tab', { name: /^Agents/ }).click()
    await sidebar(page).locator(`[data-session="${s.id}"] [data-agent-row], [data-session="${s.id}"]`).first().click()
    const sideTerm = terminalIn(sidebar(page))
    await expectTerminalText(sideTerm, 'hello from the developer')
    await sidebar(page).getByRole('link', { name: `Open #${n}` }).click()
    await expect(page).toHaveURL(new RegExp(`/${n}(\\?|$)`))
    await expect(panel(page).locator('[data-ticket-title]')).toHaveText('Worked on by an agent')
    // The sidebar's terminal is still live.
    await expect(sidebar(page).locator('.xterm')).toHaveCount(1)

    // The same agent in the panel's Agents tab: the panel's view is live, the sidebar's stands aside.
    await panel(page).getByRole('tab', { name: /^Agents/ }).click()
    await panel(page).locator(`[data-session="${s.id}"] [data-agent-row]`).click()
    await expectTerminalText(terminalIn(panel(page)), 'hello from the developer')
    const aside = sidebar(page).locator('[data-terminal-elsewhere]')
    await expect(aside).toContainText("This terminal is shown in the ticket's panel.")
    await expect(sidebar(page).locator('.xterm')).toHaveCount(0)
    // "Show it here" brings it to the sidebar; the panel stands aside.
    await aside.getByRole('button', { name: 'Show it here' }).click()
    await expectTerminalText(terminalIn(sidebar(page)), 'hello from the developer')
    await expect(panel(page).locator('[data-terminal-elsewhere]')).toContainText('This terminal is shown in the sidebar.')
    await expect(panel(page).locator('.xterm')).toHaveCount(0)
    // Back in the panel, then the panel closes: the sidebar's view is live again.
    await panel(page).getByRole('button', { name: 'Show it here' }).click()
    await expect(sidebar(page).locator('[data-terminal-elsewhere]')).toBeVisible()
    await panel(page).locator('[data-close-panel]').click()
    await expect(panel(page)).toHaveCount(0)
    await expectTerminalText(terminalIn(sidebar(page)), 'hello from the developer')
    await expect(sidebar(page).locator('[data-terminal-elsewhere]')).toHaveCount(0)
  })

  test('dialogs opened from the panel are modal over the whole window; the gear leaves through the unsaved-text question', async ({ page, omo }) => {
    const m = await omo.createTicket({ title: 'A plain ticket' })
    await openBoard(page, omo)
    await page.goto(omo.url(`/${m}`))
    await expect(panel(page)).toBeVisible()
    await panel(page).locator('[data-action="cancel"]').click()
    const modal = () => page.evaluate(() => [...document.querySelectorAll('dialog')].filter((d) => d.matches(':modal')).map((d) => (d.hasAttribute('data-ticket-panel') ? 'panel' : 'other')))
    await expect.poll(modal).toEqual(['other'])
    await page.keyboard.press('Escape')
    await expect.poll(modal).toEqual([])
    await expect(panel(page)).toBeVisible()

    await panel(page).locator('[data-edit-description]').click()
    await panel(page).locator('[data-description-input]').fill('Some unsaved words')
    await page.locator('[data-settings-link]').click()
    await expect(panel(page).locator('[data-unsaved]')).toBeVisible()
    await expect(page).toHaveURL(new RegExp(`/${m}(\\?|$)`))
    await panel(page).locator('[data-discard]').click()
    await expect(page).toHaveURL(/\/settings/)
    await expect(panel(page)).toHaveCount(0)
  })

  test('the lock screen covers everything with a panel open; after the unlock the panel is back', async ({ page, omo }) => {
    const m = await omo.createTicket({ title: 'A plain ticket' })
    await openBoard(page, omo)
    await page.goto(omo.url(`/${m}`))
    await expect(panel(page)).toBeVisible()
    await omo.stop()
    expect((await omo.cli(['config', 'rotate-token'])).code).toBe(0)
    await omo.start()
    const locked = page.getByRole('heading', { level: 1, name: 'omo is locked' })
    await expect(locked).toBeVisible()
    await expect(panel(page)).toBeHidden()
    await expect(page.locator('[data-board-kept]')).toHaveAttribute('inert', '')
    await page.getByRole('textbox', { name: 'Access address or token' }).fill(omo.accessUrl)
    await page.getByRole('button', { name: 'Unlock' }).click()
    await expect(locked).toHaveCount(0)
    await expect(panel(page)).toBeVisible()
    await expectInBoardArea(page, 'after the unlock')
  })
})

test.describe('a waiting review in the bell', () => {
  test.use({
    omoOptions: {
      seed: {
        projects: [{ path: '{{repo}}', name: 'demo' }],
        tickets: [
          { ref: 'plain', type: 'task', title: 'Open first', projects: ['demo'] },
          { ref: 'review', type: 'task', title: 'Waiting for you', to: 'review', review: 'human', projects: ['demo'], branch: 'omo/review' },
        ],
      },
    },
  })

  test('opening another ticket from the bell switches the panel', async ({ page, omo }) => {
    await page.setViewportSize({ width: 1440, height: 900 })
    await openBoard(page, omo)
    await page.goto(omo.url(`/${omo.refs.plain!}`))
    await expect(panel(page).locator('[data-ticket-title]')).toHaveText('Open first')
    await page.locator('[data-bell]').click()
    await page.locator(`[data-bell-list] [data-waiting="${omo.refs.review!}"]`).click()
    await expect(page).toHaveURL(new RegExp(`/${omo.refs.review!}(\\?|$)`))
    await expect(panel(page)).toHaveCount(1)
    await expect(panel(page).locator('[data-ticket-title]')).toHaveText('Waiting for you')
  })
})

for (const theme of ['dark', 'light'] as const) {
  test.describe(`${theme} theme`, () => {
    test.use({ colorScheme: theme, omoOptions: { agents: { developer: DEVELOPER } } })
    test.beforeEach(({ omo }) => writeFileSync(join(omo.home, '.zshrc'), ''))
    test.afterEach(async ({ omo }) => {
      for (const t of await omo.api<{ id: string }[]>('GET', '/api/terminals')) await omo.api('DELETE', `/api/terminals/${encodeURIComponent(t.id)}`)
      await omo.api('POST', '/api/agents/stop-all', {})
      await expectNoAgentProcesses(omo)
    })

    for (const width of [1440, 390] as const) {
      test(`${width} px: no accessibility violations and a still document with the panel open beside a live terminal`, async ({ page, omo }) => {
        const n = await omo.createTicket({ title: 'Worked on by an agent' })
        await omo.moveTicket(n, 'ready')
        await waitForSession(omo, n, 'developer')
        const shell = await openShell(omo)
        await page.addInitScript(() => localStorage.setItem('omo.sidebar', JSON.stringify({ open: true, width: 480, tab: 'terminals' })))
        await page.setViewportSize({ width, height: width === 390 ? 844 : 900 })
        await openBoard(page, omo)
        if (width === 390) {
          await page.getByRole('button', { name: 'Sidebar' }).click()
          const sheet = page.getByRole('dialog', { name: 'Agents and terminals' })
          await sheet.locator(`[data-terminal="${shell}"]`).click()
          await expectTerminalText(terminalIn(sheet), /\S/)
          await sheet.locator('.dialog__close').click()
          await expect(sheet).toBeHidden()
        } else {
          await sidebar(page).locator(`[data-terminal="${shell}"]`).click()
          await typeInTerminal(page, terminalIn(sidebar(page)), "printf 'live=%s\\n' yes")
          await expectTerminalText(terminalIn(sidebar(page)), 'live=yes')
        }
        // On a phone one column shows at a time: In Progress first.
        if (width === 390) await page.getByRole('tablist', { name: 'Columns' }).getByRole('tab', { name: /In Progress/ }).click()
        await card(page, n).locator('[data-card-link]').click()
        await expect(panel(page)).toBeVisible()
        if (width === 390) {
          // A phone: the panel fills the screen as before, modal.
          await expect(panel(page)).toHaveAttribute('aria-modal', 'true')
          const p = await box(panel(page))
          expect(p).toEqual({ x: 0, y: 0, width: 390, height: 844 })
        } else await expectInBoardArea(page, 'with a live terminal')
        expect(await violations(page)).toEqual([])
        expect(await documentStill(page)).toEqual({ extra: 0, top: 0, sideways: 0 })
        if (width === 1440) {
          for (const w of [320, 640, 864]) {
            await page.evaluate((v) => localStorage.setItem('omo.sidebar', JSON.stringify({ open: true, width: v, tab: 'terminals' })), w)
            await page.reload()
            await expect(panel(page)).toBeVisible()
            await expectInBoardArea(page, `sidebar ${w} px`)
            expect(await documentStill(page), `sidebar ${w} px`).toEqual({ extra: 0, top: 0, sideways: 0 })
          }
          await page.evaluate(() => localStorage.setItem('omo.sidebar', JSON.stringify({ open: true, width: 480, tab: 'terminals' })))
          await page.reload()
          await expect(panel(page)).toBeVisible()
          await sidebar(page).locator(`[data-terminal="${shell}"]`).click()
          await expectTerminalText(terminalIn(sidebar(page)), 'live=yes')
        }
        if (SHOTS !== '') {
          mkdirSync(SHOTS, { recursive: true })
          await page.mouse.move(0, 0)
          await page.screenshot({ path: join(SHOTS, `panel-overlay-${theme}-${width}.png`) })
        }
      })
    }
  })
}
