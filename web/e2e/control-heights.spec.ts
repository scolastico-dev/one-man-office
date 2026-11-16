import { appendFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import type { Locator, Page } from '@playwright/test'
import { expect, test, type Omo } from './support/omo'
import { openBoard } from './support/board'
import { script, waitForSession } from './support/agents'
import { makePlugin } from './support/plugin'
import { rowMismatches } from './support/rows'

// Controls that share a row are equally tall: a button beside a field, a
// select beside its Add button, a switch beside an icon button. This walks
// the main views with their editors and dialogs open, wide and narrow, and
// lists every row whose controls differ in height (see support/rows.ts for
// what a row is and for the opt-out attribute).

/** With ROWS_REPORT=<file>, every row compared is written there (view, row, controls, heights). */
const REPORT = process.env.ROWS_REPORT

interface Walk {
  page: Page
  omo: Omo
  width: number
  found: string[]
  /** Rows already listed (the top bar is on every view; it is listed once). */
  seen: Set<string>
}

async function check(w: Walk, view: string) {
  await w.page.evaluate(() => document.fonts.ready.then(() => undefined))
  for (const m of await rowMismatches(w.page)) {
    const key = `${m.row}: ${m.a} ${m.ha}px, ${m.b} ${m.hb}px`
    if (w.seen.has(key)) continue
    w.seen.add(key)
    w.found.push(`${view} @${w.width}: ${key}`)
  }
  if (REPORT) for (const m of await rowMismatches(w.page, true)) appendFileSync(REPORT, `${w.width}\t${view}\t${m.row}\t${m.a}\t${m.ha}\t${m.b}\t${m.hb}\n`)
}

const settingsTitle = (page: Page) => page.locator('[data-settings-page] h1')

async function settings(w: Walk, section: string, title: string) {
  await w.page.goto(w.omo.url(`/settings/${section}`))
  await expect(settingsTitle(w.page)).toHaveText(title)
}

/** Opens a dialog with `open`, checks it, closes it with Escape. */
async function inDialog(w: Walk, view: string, open: () => Promise<unknown>, name: string | RegExp) {
  await open()
  const dialog = w.page.getByRole('dialog', { name })
  await expect(dialog).toBeVisible()
  await check(w, view)
  await w.page.keyboard.press('Escape')
  await expect(dialog).toHaveCount(0)
}

/** Opens each field editor of a ticket in turn (one at a time). */
async function fieldEditors(w: Walk, view: string, root: Locator) {
  const fields = await root.locator('[data-field]').evaluateAll((els) => els.filter((e) => e.querySelector('[data-edit-open]')).map((e) => e.getAttribute('data-field')!))
  expect(fields.length).toBeGreaterThan(3)
  for (const f of fields) {
    const field = root.locator(`[data-field="${f}"]`)
    await field.locator('[data-edit-open]').first().click()
    await expect(field).toHaveAttribute('data-editing', '')
    await check(w, `${view}, editing ${f}`)
    const cancel = field.locator('[data-cancel]')
    if (await cancel.count()) await cancel.click()
    else await w.page.keyboard.press('Escape')
    await expect(field).not.toHaveAttribute('data-editing', '')
  }
}

for (const width of [1440, 390]) {
  test(`controls on one row are equally tall at ${width} px`, async ({ page, omo }) => {
    test.setTimeout(240_000)
    await page.setViewportSize({ width, height: 900 })
    const narrow = width < 768
    const w: Walk = { page, omo, width, found: [], seen: new Set() }
    const n = await omo.createTicket({ title: 'A task to look at' })
    const story = await omo.createTicket({ title: 'A story to look at', type: 'story' })
    // A profile with arguments, variables and two models, so its editor shows every kind of row.
    await omo.api('PUT', '/api/profiles/fake', {
      cli: 'custom',
      command: '/bin/true',
      args: ['--one'],
      env: { PAGER: 'cat' },
      account_dir: '',
      usage_script: '',
      prompt_delivery: {},
      models: {
        default: { args: ['--fast'], env: { MODE: 'a' }, usage_windows: [], description: 'first' },
        second: { args: [], env: {}, usage_windows: [], description: 'second' },
      },
    })

    // The board with its top bar: filters, search, Create ticket, Stop all, the switch, bell, gear.
    await openBoard(page, omo)
    await page.goto(omo.url('/?q=look'))
    await check(w, 'board with a filter')
    if (narrow) await inDialog(w, 'filter sheet', () => page.getByRole('button', { name: /^Filters/ }).click(), 'Filters')
    await inDialog(w, 'create ticket', () => page.getByRole('button', { name: 'Create ticket' }).click(), 'Create ticket')
    await page.getByRole('button', { name: /^Notifications/ }).click()
    await expect(page.locator('[data-desktop]')).toBeVisible()
    await check(w, 'notifications')
    await page.keyboard.press('Escape')
    await inDialog(w, 'stop all', () => page.getByRole('button', { name: 'Stop all agents' }).click(), 'Stop every running agent?')

    // The sidebar's tabs.
    if (narrow) await page.getByRole('button', { name: 'Sidebar', exact: true }).click()
    else await page.getByRole('button', { name: 'Expand sidebar' }).click()
    const side = narrow ? page.getByRole('dialog', { name: 'Sidebar' }) : page.locator('[data-sidebar]')
    for (const tab of [/^CEO/, /^Agents/, /^Terminals/]) {
      await side.getByRole('tab', { name: tab }).click()
      await check(w, `sidebar ${tab.source.slice(1)}`)
    }
    await side.getByRole('button', { name: 'New terminal' }).click()
    await check(w, 'sidebar, new terminal picker')
    await page.keyboard.press('Escape')

    // A ticket in the panel and on its page, every field editor, the comment composer.
    await page.goto(omo.url(`/${n}`))
    const panel = page.locator('[data-ticket-panel]')
    await expect(panel).toBeVisible()
    await check(w, 'ticket panel')
    await fieldEditors(w, 'ticket panel', panel)
    await page.goto(omo.url(`/${story}`))
    await expect(panel).toBeVisible()
    await fieldEditors(w, 'story panel', panel)
    await page.goto(omo.url(`/ticket/${n}`))
    await expect(page.locator('[data-ticket-page]')).toBeVisible()
    await check(w, 'ticket page')
    for (const tab of ['comments', 'diff', 'agents']) {
      await page.goto(omo.url(`/ticket/${n}?tab=${tab}`))
      await expect(page.locator('[data-ticket-page]')).toBeVisible()
      await check(w, `ticket page, ${tab}`)
    }

    // Every settings section, with its editors open.
    await settings(w, 'projects', 'Projects')
    await check(w, 'projects')
    await page.locator('[data-select-mode]').click()
    await check(w, 'projects, selecting')
    await page.locator('[data-select-mode]').click()
    await page.getByRole('button', { name: /^Edit demo/ }).click()
    await check(w, 'projects, editing demo')
    await inDialog(w, 'folder picker', () => page.getByRole('button', { name: 'Add workspace folder…' }).click(), /folder/i)
    // A workspace folder: its path with Browse…, its depth, its ignore patterns with their move and remove buttons.
    await page.getByRole('button', { name: 'Add workspace folder…' }).click()
    await page.getByRole('dialog').getByRole('button', { name: 'Choose this folder' }).click()
    await expect(page.getByRole('dialog')).toHaveCount(0)
    const addPattern = page.getByRole('button', { name: 'Add pattern' }).first()
    await addPattern.click()
    await addPattern.click()
    await check(w, 'projects, a workspace folder')

    await settings(w, 'agents', 'Agents')
    await check(w, 'agents')
    await inDialog(w, 'new profile', () => page.getByRole('button', { name: 'New profile…' }).click(), 'New profile')
    const profile = page.locator('[data-profile-editor="fake"]')
    if (!(await profile.isVisible())) await page.locator('button[data-profile="fake"]').click()
    await expect(profile).toBeVisible()
    await check(w, 'agents, a profile open')
    await profile.getByRole('button', { name: 'Add model' }).click()
    await check(w, 'agents, a model added')

    await settings(w, 'roles', 'Roles')
    await expect(page.getByRole('button', { name: /^Add model/ }).first()).toBeVisible()
    await check(w, 'roles')

    await settings(w, 'usage', 'Usage')
    await expect(page.locator('[data-script]').first()).toBeVisible()
    await check(w, 'usage')
    await page.locator('[data-script-toggle]').first().click()
    await expect(page.locator('[data-script-text]').first()).toBeVisible()
    await check(w, 'usage, a script open')
    await page.getByRole('button', { name: 'Add window' }).click()
    await check(w, 'usage, a window row')

    await settings(w, 'prompts', 'Prompts')
    await page.locator('[data-prompt-toggle]').first().click()
    await check(w, 'prompts, a prompt open')

    await settings(w, 'skills', 'Skill sets')
    await check(w, 'skill sets')
    await inDialog(w, 'add skill set', () => page.getByRole('button', { name: 'Add skill set…' }).click(), /skill set/i)

    await settings(w, 'environment', 'Environment')
    await check(w, 'environment')

    await settings(w, 'limits', 'Limits')
    await check(w, 'limits')

    await settings(w, 'review', 'Review')
    await check(w, 'review')

    await settings(w, 'plugins', 'Plugins')
    await check(w, 'plugins')
    await inDialog(w, 'install plugin', () => page.getByRole('button', { name: 'Install plugin…' }).click(), /plugin/i)
    const repo = makePlugin(join(omo.home, 'plugin-src', 'look'), { name: 'look', version: '1.0.0', description: 'A plugin to look at', default_config: { mark: 'one', on: true } }, {})
    await omo.api('POST', '/api/plugins', { name: 'look', source: repo.source, subpath: '', ref: '' })
    await expect(page.locator('[data-plugin="look"] [data-plugin-state]')).toHaveText('ready')
    await page.locator('[data-plugin="look"] [data-plugin-toggle]').click()
    await expect(page.locator('[data-plugin-detail="look"]')).toBeVisible()
    await check(w, 'plugins, a plugin open')

    await settings(w, 'general', 'General')
    await check(w, 'general')

    // A terminal in a window of its own: its title line.
    writeFileSync(join(omo.home, '.zshrc'), '')
    const shell = await omo.api<{ id: string }>('POST', '/api/terminals', { home: true })
    await page.goto(omo.url(`/terminal/user/${shell.id}`))
    await expect(page.locator('[data-terminal-window], .xterm').first()).toBeVisible()
    await check(w, 'terminal window')
    await omo.api('DELETE', `/api/terminals/${encodeURIComponent(shell.id)}`)

    expect(w.found).toEqual([])
  })
}

test.describe('a diff with files', () => {
  test.use({ omoOptions: { agents: { developer: script('ready', 'write|a.txt|first', 'commit|feat: add a', 'write|a.txt|second', 'write|b.txt|not yet', 'hang') } } })

  for (const width of [1440, 390]) {
    test(`the diff's controls on one row are equally tall at ${width} px`, async ({ page, omo }) => {
      await page.setViewportSize({ width, height: 900 })
      const w: Walk = { page, omo, width, found: [], seen: new Set() }
      await openBoard(page, omo)
      const n = await omo.createTicket({ title: 'Change some files' })
      await omo.moveTicket(n, 'ready')
      await waitForSession(omo, n, 'developer')
      for (const at of [`/${n}?tab=diff`, `/ticket/${n}?tab=diff`]) {
        await page.goto(omo.url(at))
        await expect(page.locator('[data-diff-heading]').first()).toBeVisible()
        await expect(page.locator('[data-diff-file], [data-file-pane]').first()).toBeVisible()
        await check(w, `diff at ${at}`)
      }
      expect(w.found).toEqual([])
    })
  }
})
