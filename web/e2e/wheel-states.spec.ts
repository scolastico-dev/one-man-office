// The scroll boxes that only exist in a populated state (rendered Markdown, a prompt's preview and default text, a
// script's default text, the MCP page's copy blocks, a plugin's log, an ended agent's log) take a trusted wheel on the
// compositor alone. Each state audits its own component, not the settings frame around it: text fields make Chromium
// ask the main thread about a frame that holds them (see support/wheelAudit), and the frame is audited where it holds none.
import { join } from 'node:path'
import { mkdirSync } from 'node:fs'
import { dirname } from 'node:path'
import { initRepo } from './support/git'
import type { Locator } from '@playwright/test'
import { expect, FAKE_AGENT_BINARY, test } from './support/omo'
import { openBoard } from './support/board'
import { script, waitForNoLiveSession } from './support/agents'
import { makePlugin } from './support/plugin'
import { auditWheels } from './support/wheelAudit'

test.use({ viewport: { width: 1440, height: 900 } })

const COLUMNS = 100
const table = `| ${Array.from({ length: COLUMNS }, (_, i) => `column ${i}`).join(' | ')} |\n| ${Array.from({ length: COLUMNS }, () => '---').join(' | ')} |\n| ${Array.from({ length: COLUMNS }, (_, i) => `value number column ${i}`).join(' | ')} |`
const markdown = `Intro\n\n\`\`\`\n${'a'.repeat(300)}\n${'a'.repeat(300)}\n\`\`\`\n\n${table}\n\n${'More text.\n\n'.repeat(60)}`

/** A scroll box that really scrolls sideways: the audit of one with no room to scroll proves little. */
async function expectSidewaysRoom(box: Locator, what: string): Promise<void> {
  expect(await box.first().evaluate((el) => el.scrollWidth - el.clientWidth), `${what} has horizontal scroll room`).toBeGreaterThan(200)
}

test('rendered Markdown with a code block and a wide table, in a description and in a comment, on the ticket page and in the panel', async ({ page, omo }) => {
  const n = await omo.createTicket({ title: 'Markdown with wide blocks' })
  const { description_version } = await omo.api<{ description_version: number }>('GET', `/api/tickets/${n}`)
  await omo.api('PUT', `/api/tickets/${n}/description`, { text: markdown, base_version: description_version })
  await omo.api('POST', `/api/tickets/${n}/comments`, { body: `A comment with a table:\n\n${table}\n`, question: false })
  await openBoard(page, omo)
  for (const [path, scope, where] of [
    [`/ticket/${n}`, '[data-ticket-page]', 'the description on the ticket page'],
    [`/${n}`, 'dialog[data-ticket-panel]', 'the description in the panel'],
    [`/ticket/${n}?tab=comments`, '[data-ticket-page]', 'the comment on the ticket page'],
    [`/${n}?tab=comments`, 'dialog[data-ticket-panel]', 'the comment in the panel'],
  ] as const) {
    await page.goto(omo.url(path))
    const root = page.locator(scope)
    await expect(root.locator('.md table, .cmt__body table').first()).toBeVisible()
    await expectSidewaysRoom(root.locator('.md table, .cmt__body table'), `the table of ${where}`)
    if (path.includes('comments') === false) {
      await expect(root.locator('.md pre').first()).toBeVisible()
      await expectSidewaysRoom(root.locator('.md pre'), `the code block of ${where}`)
    }
    const audit = await auditWheels(page, scope, where)
    expect(audit.visited.some((v) => v.startsWith('table x')), `${where}: the audit turned a wheel over the table`).toBe(true)
    expect(audit.bad, where).toEqual([])
  }
})

test('the prompt preview and default text, the script default text and the MCP copy blocks', async ({ page, omo }) => {
  await openBoard(page, omo)
  await page.goto(omo.url('/settings/prompts'))
  await page.locator('[data-prompt-row="reviewer-instructions"] [data-prompt-toggle]').click()
  const ed = page.locator('[data-prompt-editor="reviewer-instructions"]')
  await ed.locator('textarea').fill(`Line one\n${'x'.repeat(400)}\n`)
  await ed.getByRole('button', { name: 'Preview' }).click()
  await expect(ed.locator('pre.prompt-preview__text')).toBeVisible()
  expect((await auditWheels(page, '.prompt-preview', 'the prompt preview')).bad).toEqual([])
  await page.goto(omo.url('/settings/prompts'))
  const other = page.locator('[data-prompt-row]:not([data-prompt-row$="instructions"]) [data-prompt-toggle]').first()
  await other.click()
  await page.locator('[data-prompt-default-toggle]').click()
  await expect(page.locator('pre.prompt-editor__default-text')).toBeVisible()
  expect((await auditWheels(page, '[data-prompt-default]', "a prompt's default text")).bad).toEqual([])

  await page.goto(omo.url('/settings/usage'))
  await page.locator('[data-script="claude"] [data-script-toggle]').click()
  await page.locator('[data-script-editor="claude"] [data-script-default-toggle]').click()
  await expect(page.locator('pre.script-editor__default-text')).toBeVisible()
  expect((await auditWheels(page, '[data-script-default]', "a script's default text")).bad).toEqual([])

  await page.goto(omo.url('/settings/mcp'))
  await expect(page.locator('[data-mcp-section="access"] .copy__text').first()).toBeVisible()
  expect((await auditWheels(page, '[data-mcp-section="access"]', "the MCP page's copy blocks")).bad).toEqual([])
})

test('a plugin with its log, and the log of an ended agent', async ({ page, omo }) => {
  const name = 'wheel-log'
  const repo = makePlugin(join(omo.home, 'plugin-src', name), { name, version: '1.0.0', description: 'Logs', default_config: { mark: 'one' }, hooks: [{ event: 'ticket_moved', lua: 'moved.lua' }] }, {
    'moved.lua': 'omo.log("moved #" .. tostring(event.data.ticket.number))\n',
  })
  await omo.api('POST', '/api/plugins', { name, source: repo.source, subpath: '', ref: '', acknowledge: 'community' })
  // More lines than the log box shows, so it scrolls.
  for (let i = 0; i < 30; i++) await omo.moveTicket(await omo.createTicket({ title: `Moved for the log ${i + 1}` }), 'ready')
  await openBoard(page, omo)
  await page.goto(omo.url('/settings/plugins'))
  const row = page.locator(`[data-plugin="${name}"]`)
  await expect(row.locator('[data-plugin-state]')).toHaveText('ready')
  await row.locator('[data-plugin-toggle]').click()
  const log = page.locator(`[data-plugin-log="${name}"]`)
  await expect(async () => {
    await log.locator('[data-plugin-log-refresh]').click()
    await expect(log.locator('[data-plugin-log-text]')).toContainText('moved #', { timeout: 1_000 })
    expect(await log.locator('[data-plugin-log-text]').evaluate((el) => el.scrollHeight > el.clientHeight)).toBe(true)
  }).toPass({ timeout: 20_000 })
  expect((await auditWheels(page, `[data-plugin-log="${name}"]`, "a plugin's log")).bad).toEqual([])
})

test.describe('an agent that exits', () => {
  test.use({ omoOptions: { agents: { developer: script('ready', ...Array.from({ length: 100 }, (_, i) => `print|line ${i + 1} of the log ${'y'.repeat(120)}`), 'exit|0') } } })

  test("the saved log of an ended agent in the ticket's Agents tab", async ({ page, omo }) => {
    const n = await omo.createTicket({ title: 'Ends at once' })
    await omo.moveTicket(n, 'ready')
    // The agent has finished before anything asks for it: the session is read from what was saved, never observed running.
    await expect
      .poll(async () => (await omo.api<{ ticket: number | null; role: string; state: string }[]>('GET', '/api/agents')).some((s) => s.ticket === n && s.role === 'developer' && s.state === 'ended'), {
        message: `waiting for the saved, ended developer session of #${n}`,
        timeout: 30_000,
      })
      .toBe(true)
    await waitForNoLiveSession(omo, n)
    await openBoard(page, omo)
    await page.goto(omo.url(`/ticket/${n}?tab=agents`))
    const item = page.locator('[data-ticket-page] li[data-session]').first()
    await expect(item.locator('[data-ended]')).toBeVisible()
    await item.getByRole('button', { name: 'Show log' }).click()
    await expect(item.locator('pre')).toContainText('line 100 of the log')
    expect(await item.locator('pre').evaluate((el) => el.scrollHeight > el.clientHeight)).toBe(true)
    expect((await auditWheels(page, '[data-ticket-page] li[data-session]', "an ended agent's log")).bad).toEqual([])
  })
})

test("a usage script's test log and the import's file list", async ({ page, omo }) => {
  await omo.api('PUT', '/api/profiles/fake', {
    cli: 'custom',
    command: FAKE_AGENT_BINARY,
    args: [],
    env: {},
    account_dir: '',
    usage_script: '',
    prompt_delivery: {},
    models: { default: { args: [], env: {}, usage_windows: [], description: 'scripted fake agent' } },
  })
  const text = `function usage(account)\n${Array.from({ length: 60 }, (_, i) => `  omo.log("line ${i + 1} of the log ${'z'.repeat(150)}")`).join('\n')}\n  return { { name = "a", used = 1 } }\nend\n`
  const res = await fetch(`${omo.base}/api/usage/scripts/wheel-log`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${omo.token}`, 'If-Match': '"0"' },
    body: JSON.stringify({ text }),
  })
  expect(res.status, await res.text()).toBe(200)
  await openBoard(page, omo)
  await page.goto(omo.url('/settings/usage'))
  const ed = page.locator('[data-script-editor="wheel-log"]')
  await page.locator('[data-script="wheel-log"] [data-script-toggle]').click()
  await ed.getByLabel('Run as').selectOption('fake')
  await ed.locator('[data-test-run]').click()
  await expect(ed.locator('[data-test-log]')).toContainText('line 1 of the log')
  expect((await auditWheels(page, '[data-script-test]', "a script's test log")).bad).toEqual([])

  const dir = join(omo.home, 'downloads')
  mkdirSync(dir, { recursive: true })
  await page.goto(omo.url('/settings/backup'))
  const download = page.waitForEvent('download')
  await page.locator('[data-export]').click()
  const file = join(dir, 'export.zip')
  await (await download).saveAs(file)
  await page.locator('[data-import-file]').setInputFiles(file)
  const dialog = page.getByRole('dialog', { name: 'Import settings' })
  await expect(dialog).toBeVisible()
  expect((await auditWheels(page, 'dialog[open]', 'the import dialog')).bad).toEqual([])
})

test('the list of projects a batch removal names', async ({ page, omo, repo }) => {
  for (let i = 1; i <= 14; i++) {
    const dir = join(dirname(repo), `service-${String(i).padStart(2, '0')}`)
    initRepo(dir)
    await omo.api('POST', '/api/projects', { path: dir, name: `service-${String(i).padStart(2, '0')}` })
  }
  await openBoard(page, omo)
  await page.goto(omo.url('/settings/projects'))
  await page.getByRole('button', { name: 'Select…' }).click()
  await page.locator('[data-select-all]').click()
  await page.getByRole('group', { name: 'Selected projects' }).getByRole('button', { name: 'Remove…' }).click()
  const dialog = page.getByRole('dialog', { name: /^Remove \d+ projects\?$/ })
  await expect(dialog.locator('[data-batch-remove-list] li').first()).toBeVisible()
  expect(await dialog.locator('[data-batch-remove-list]').evaluate((el) => el.scrollHeight > el.clientHeight)).toBe(true)
  expect((await auditWheels(page, '[data-batch-remove-dialog]', 'the batch removal list')).bad).toEqual([])
})
