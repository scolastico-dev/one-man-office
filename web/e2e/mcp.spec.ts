// The MCP section of the settings against the real settings, token, agent
// program and take-over routes (the relayed servers at work are in
// mcp-relay.spec.ts). The agent programs are fakes: a `claude` script first on PATH that edits the
// `.claude.json` of the test's temporary home, as the real program's
// `mcp add-json` and `mcp remove` would. With OMO_SCREENSHOTS=<folder> the
// axe tests also write mcp-<section>-<theme>-<width>.png there.
import AxeBuilder from '@axe-core/playwright'
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { delimiter, join } from 'node:path'
import type { Page } from '@playwright/test'
import { openBoard } from './support/board'
import { expect, pathWithoutAgentPrograms, test, type Omo } from './support/omo'

const SHOTS = process.env.OMO_SCREENSHOTS ?? ''

interface Stored {
  servers: { name: string; revision: string; global: boolean; description: string; command: string; headers: Record<string, string>; source: string }[]
  states: unknown[]
}
const stored = (omo: Omo) => omo.api<Stored>('GET', '/api/mcp/servers')

const title = (page: Page) => page.locator('[data-settings-page] h1')
const section = (page: Page, id: string) => page.locator(`[data-mcp-section="${id}"]`)
const row = (page: Page, name: string) => page.locator(`[data-mcp-server="${name}"]`)
const editor = (page: Page, name: string) => page.locator(`[data-mcp-server-editor="${name}"]`)
const saveBar = (page: Page, name: string) => page.getByRole('region', { name: `Save changes to the MCP server ${name}` })
const serverFile = (omo: Omo, name: string) => join(omo.home, 'omo', 'mcpservers', `${name}.yaml`)

async function openMcp(page: Page, omo: Omo) {
  await openBoard(page, omo)
  await page.goto(omo.url('/settings/mcp'))
  await expect(title(page)).toHaveText('MCP')
  await expect(page.locator('[data-new-mcp-server]')).toBeVisible()
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

/** A server stored through the API (the form is not what the test is about). */
async function addServer(omo: Omo, name: string, extra: object = {}) {
  await omo.api('PUT', `/api/mcp/servers/${name}`, {
    name,
    enabled: true,
    transport: 'stdio',
    command: 'npx',
    args: ['-y', '@modelcontextprotocol/server-filesystem', '.'],
    env: { LOG_LEVEL: 'info' },
    roles: [],
    global: false,
    tools: { allow: [], deny: [] },
    timeouts: { startup: '0s', call: '0s' },
    oauth: { client_id: '', client_secret: '', scopes: [], resource: '' },
    ...extra,
  })
}

// The fake agent program

const CLAUDE_FIXTURE = {
  numStartups: 3,
  mcpServers: {
    github: { type: 'http', url: 'https://api.example.invalid/mcp', headers: { Authorization: 'Bearer ghp_e2eSECRETvalue' } },
  },
  projects: { '/src/demo': { mcpServers: { localdb: { command: 'db-mcp', args: [] } } } },
}

/**
 * A folder with a fake `claude` that edits $HOME/.claude.json (the test's
 * temporary home, which omo passes on) and logs its arguments there, and the
 * PATH that finds it first. Made once per worker, removed after the tests.
 */
function fakeClaude(): { dir: string; path: string } {
  const dir = mkdtempSync(join(realpathSync(tmpdir()), 'omo-e2e-fakeclaude-'))
  const script = `#!${process.execPath}
const fs = require('node:fs')
const path = require('node:path')
const home = process.env.CLAUDE_CONFIG_DIR || process.env.HOME
const file = path.join(home, '.claude.json')
const args = process.argv.slice(2)
fs.appendFileSync(path.join(process.env.HOME, 'claude-calls.log'), JSON.stringify(args) + '\\n')
const data = JSON.parse(fs.readFileSync(file, 'utf8'))
data.mcpServers = data.mcpServers || {}
if (args[0] === 'mcp' && args[1] === 'add-json') data.mcpServers[args[4]] = JSON.parse(args[5])
else if (args[0] === 'mcp' && args[1] === 'remove') delete data.mcpServers[args[args.length - 1]]
else { console.error('fake claude: unknown command'); process.exit(2) }
fs.writeFileSync(file, JSON.stringify(data, null, 2))
`
  writeFileSync(join(dir, 'claude'), script)
  chmodSync(join(dir, 'claude'), 0o755)
  return { dir, path: [dir, pathWithoutAgentPrograms()].join(delimiter) }
}
const FAKE = fakeClaude()
test.afterAll(() => rmSync(FAKE.dir, { recursive: true, force: true }))

const claudeJson = (omo: Omo) => JSON.parse(readFileSync(join(omo.home, '.claude.json'), 'utf8')) as typeof CLAUDE_FIXTURE & { mcpServers: Record<string, unknown> }

test('create a server through the form, edit it, make it global and delete it', async ({ page, omo }) => {
  await openMcp(page, omo)
  await expect(page.locator('[data-no-mcp-servers]')).toBeVisible()

  await page.getByRole('button', { name: 'Add MCP server…' }).click()
  await page.getByLabel('Name of the MCP server').fill('files')
  await page.getByRole('button', { name: 'Add', exact: true }).click()
  const ed = editor(page, 'files')
  await expect(ed).toContainText('(not saved yet)')
  await ed.getByLabel('Command').fill('npx')
  await ed.getByRole('button', { name: 'Add argument' }).click()
  await ed.getByRole('textbox', { name: 'Argument 1', exact: true }).fill('-y')
  await saveBar(page, 'files').getByRole('button', { name: 'Save', exact: true }).click()
  await expect(saveBar(page, 'files').locator('[data-save-state]')).toHaveText('Saved')
  expect(existsSync(serverFile(omo, 'files'))).toBe(true)

  // The relay tells its state at once; nothing started it yet.
  await expect(row(page, 'files').locator('[data-mcp-server-state]')).toHaveText('not running')
  await expect(ed.locator('[data-mcp-server-status]')).toContainText('State: not running')
  await expect(ed.locator('[data-mcp-test]')).toBeVisible()
  await expect(page.locator('[data-mcp-off]')).toHaveCount(0)
  await expect(row(page, 'files')).toContainText("stdio · npx · for every role · only omo's sessions")

  // Edit: a description, a role and "also available without an omo session".
  await ed.getByLabel('Description').fill('Files of the session')
  await ed.locator('[data-mcp-role="developer"]').click()
  await ed.locator('[data-mcp-global]').click()
  await saveBar(page, 'files').getByRole('button', { name: 'Save', exact: true }).click()
  await expect(saveBar(page, 'files').locator('[data-save-state]')).toHaveText('Saved')
  await expect(row(page, 'files').locator('[data-mcp-server-global]')).toBeVisible()
  await expect(row(page, 'files')).toContainText('for Developer · also without an omo session')
  const after = (await stored(omo)).servers.find((s) => s.name === 'files')
  expect(after?.global).toBe(true)
  expect(after?.description).toBe('Files of the session')

  // A refusal of the server lands under its field.
  await ed.getByLabel('Command').fill('')
  await saveBar(page, 'files').getByRole('button', { name: 'Save', exact: true }).click()
  const command = ed.locator('.omo-field').filter({ has: page.getByLabel('Command') })
  await expect(command.locator('.omo-field__error')).toContainText('command must name the program of a stdio server')
  await saveBar(page, 'files').getByRole('button', { name: 'Discard' }).click()

  // Delete asks first.
  await ed.getByRole('button', { name: 'Delete files…' }).click()
  await expect(page.locator('[data-question]')).toContainText('Delete the MCP server files?')
  await page.locator('[data-question-confirm]').click()
  await expect(row(page, 'files')).toHaveCount(0)
  await expect(page.locator('[data-no-mcp-servers]')).toBeVisible()
  expect((await stored(omo)).servers).toEqual([])
  expect(existsSync(serverFile(omo, 'files'))).toBe(false)
})

test('the general switches are saved in settings.yaml', async ({ page, omo }) => {
  await openMcp(page, omo)
  const sw = page.locator('[data-mcp-switches]')
  await sw.locator('[data-mcp-inject="codex"]').click()
  await sw.getByRole('region', { name: 'Save changes to the general MCP switches' }).getByRole('button', { name: 'Save', exact: true }).click()
  await expect(sw.locator('[data-save-state]')).toHaveText('Saved')
  const s = await omo.api<{ settings: { mcp: { enabled: boolean; inject: { claude: boolean; codex: boolean } } } }>('GET', '/api/settings')
  expect(s.settings.mcp).toEqual({ enabled: true, inject: { claude: true, codex: false } })
})

test('a personal access token is created with its secret shown once, and revoked', async ({ page, omo }) => {
  await openMcp(page, omo)
  const access = section(page, 'access')
  await access.getByLabel('Name', { exact: true }).fill('laptop')
  await access.getByRole('button', { name: 'Create token' }).click()
  const dialog = page.locator('[data-mcp-token-secret]')
  await expect(dialog).toBeVisible()
  const secret = (await dialog.locator('pre').innerText()).trim()
  expect(secret).toMatch(/^omo_pat_/)
  await dialog.getByRole('button', { name: 'Done' }).click()
  await expect(dialog).toBeHidden()
  await expect(page.locator('body')).not.toContainText(secret)
  expect(page.url()).not.toContain(secret)
  const tokens = await omo.api<{ id: string; name: string; kind: string }[]>('GET', '/api/mcp/tokens')
  expect(tokens.map((t) => [t.name, t.kind])).toEqual([['laptop', 'pat']])
  expect(omo.output()).not.toContain(secret)

  await access.locator('[data-mcp-token="laptop"] [data-mcp-token-revoke]').click()
  await expect(page.locator('[data-question]')).toContainText('Revoke the token laptop?')
  await page.locator('[data-question-confirm]').click()
  await expect(access.locator('[data-mcp-token="laptop"]')).toHaveCount(0)
  expect(await omo.api('GET', '/api/mcp/tokens')).toEqual([])
})

test.describe('agent programs and take-over with a fake claude', () => {
  test.use({ omoOptions: { path: FAKE.path } })

  test('omo is registered after its plan was shown, and removed again', async ({ page, omo }) => {
    writeFileSync(join(omo.home, '.claude.json'), JSON.stringify(CLAUDE_FIXTURE, null, 2))
    await openMcp(page, omo)
    const programs = section(page, 'programs')
    const claude = programs.locator('[data-mcp-client="claude"]')
    await expect(claude).toContainText('Claude Code, your own configuration')
    await expect(claude.locator('[data-mcp-client-state]')).toHaveText('omo not registered')

    await claude.locator('[data-mcp-client-action="install"]').click()
    const plan = page.locator('[data-mcp-plan]')
    await expect(plan).toContainText(`File: ${join(omo.home, '.claude.json')}`)
    await expect(plan.locator('[data-mcp-plan-commands]')).toContainText('claude mcp add-json -s user omo')
    await expect(plan.locator('[data-mcp-plan-after]')).toContainText('"stdio"')
    // Nothing ran before the confirmation.
    expect(claudeJson(omo).mcpServers.omo).toBeUndefined()
    await plan.locator('[data-mcp-plan-apply]').click()
    await expect(claude.locator('[data-mcp-client-state]')).toHaveText('omo registered')
    await expect(claude.locator('[data-mcp-client-done]')).toContainText('A copy of the file before is at')
    const entry = claudeJson(omo).mcpServers.omo as { type: string; args: string[] }
    expect(entry.type).toBe('stdio')
    expect(entry.args).toEqual(['mcp', 'stdio'])
    // The other entries of the file are untouched.
    expect(claudeJson(omo).numStartups).toBe(3)

    await claude.locator('[data-mcp-client-action="remove"]').click()
    await expect(plan.locator('[data-mcp-plan-commands]')).toHaveText('claude mcp remove -s user omo')
    await plan.locator('[data-mcp-plan-apply]').click()
    await expect(claude.locator('[data-mcp-client-state]')).toHaveText('omo not registered')
    expect(claudeJson(omo).mcpServers.omo).toBeUndefined()
  })

  test('a server is taken over: secrets masked in the preview, stored global with its value, removed from the program', async ({ page, omo }) => {
    writeFileSync(join(omo.home, '.claude.json'), JSON.stringify(CLAUDE_FIXTURE, null, 2))
    await openMcp(page, omo)
    const takeover = section(page, 'takeover')
    const cand = takeover.locator('[data-mcp-candidate]').filter({ hasText: 'github' })
    await expect(cand).toContainText('Headers: Authorization: Bearer ***')
    await expect(cand).toContainText('also without session')
    await expect(page.locator('body')).not.toContainText('ghp_e2eSECRETvalue')
    await expect(takeover.locator('[data-mcp-stays]')).toContainText('localdb')

    await takeover.locator('[data-mcp-takeover-apply]').click()
    const report = takeover.locator('[data-mcp-takeover-report]')
    await expect(report).toContainText('github: stored in omo')
    await expect(report).toContainText('Removed from Claude Code.')
    await expect(row(page, 'github')).toBeVisible()
    await expect(row(page, 'github').locator('[data-mcp-server-global]')).toBeVisible()
    await expect(page.locator('body')).not.toContainText('ghp_e2eSECRETvalue')
    const s = (await stored(omo)).servers.find((x) => x.name === 'github')
    expect(s?.global).toBe(true)
    expect(s?.headers).toEqual({ Authorization: 'Bearer ghp_e2eSECRETvalue' })
    expect(claudeJson(omo).mcpServers.github).toBeUndefined()
    expect(claudeJson(omo).projects['/src/demo']!.mcpServers.localdb).toBeDefined()
  })
})

test('at 390 px the page does not scroll sideways', async ({ page, omo }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await addServer(omo, 'files', { description: 'x'.repeat(180), args: ['--root', `/very/long/${'folder-name/'.repeat(12)}`] })
  await openMcp(page, omo)
  expect(await pageScrollsSideways(page)).toBe(false)
  await row(page, 'files').locator('[data-mcp-server-toggle]').click()
  await expect(editor(page, 'files')).toBeVisible()
  expect(await pageScrollsSideways(page)).toBe(false)
})

for (const width of [1440, 390] as const) {
  for (const colorScheme of ['dark', 'light'] as const) {
    test.describe(`axe at ${width} px, ${colorScheme} theme`, () => {
      test.use({ viewport: { width, height: width === 390 ? 844 : 900 }, colorScheme, reducedMotion: 'reduce', omoOptions: { path: FAKE.path } })

      test('every section has no violations and the page does not scroll sideways', async ({ page, omo }) => {
        writeFileSync(join(omo.home, '.claude.json'), JSON.stringify(CLAUDE_FIXTURE, null, 2))
        await addServer(omo, 'files', { global: true, roles: ['developer', 'reviewer'], description: 'Files of the session' })
        await addServer(omo, 'docs', { transport: 'http', command: '', args: [], env: {}, url: 'https://docs.example.invalid/mcp', auth: 'oauth' })
        await openMcp(page, omo)
        await expect(row(page, 'files').locator('[data-mcp-server-state]')).toHaveText('not running')
        await expect(row(page, 'docs').locator('[data-mcp-server-state]')).toHaveText('needs login')
        await expect(section(page, 'programs').locator('[data-mcp-client="claude"]')).toBeVisible()
        await expect(section(page, 'takeover').locator('[data-mcp-candidate]').first()).toBeVisible()
        expect(await violations(page), 'page').toEqual([])
        expect(await pageScrollsSideways(page)).toBe(false)

        await row(page, 'docs').locator('[data-mcp-server-toggle]').click()
        await expect(editor(page, 'docs')).toBeVisible()
        await editor(page, 'docs').getByLabel('Client id').fill('unsaved')
        expect(await violations(page), 'open server').toEqual([])
        expect(await pageScrollsSideways(page)).toBe(false)
        if (SHOTS !== '') {
          mkdirSync(SHOTS, { recursive: true })
          for (const id of ['servers', 'programs', 'takeover', 'access']) {
            // The settings frame scrolls, not the page: show the section's top in the window.
            await section(page, id).evaluate((el) => el.scrollIntoView({ block: 'start' }))
            await page.screenshot({ path: join(SHOTS, `mcp-${id}-${colorScheme}-${width}.png`) })
          }
        }

        await section(page, 'programs').locator('[data-mcp-client-action="install"]').click()
        await expect(page.locator('[data-mcp-plan]')).toBeVisible()
        expect(await violations(page), 'plan').toEqual([])
        if (SHOTS !== '') await page.screenshot({ path: join(SHOTS, `mcp-plan-${colorScheme}-${width}.png`) })
      })
    })
  }
}
