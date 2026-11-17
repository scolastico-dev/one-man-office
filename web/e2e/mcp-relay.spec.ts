// omo's relay of MCP servers, for real: the servers are the scripted fake
// MCP server (web/e2e/fakemcp, built by the global setup), which omo starts
// over stdio like any configured program. The page follows their states
// through mcp.changed; programs reach their tools on /mcp with a personal
// access token, and the scripted fake agent through omo's bridge. Signing
// in at an OAuth upstream (Connect) is not covered here: it needs an
// authorization server, which the Go tests of the relay fake. With
// OMO_SCREENSHOTS=<folder> the first test also writes
// mcp-relay-running.png and mcp-relay-tokens.png there.
import { mkdirSync, readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import type { Page } from '@playwright/test'
import { expectNoAgentProcesses, script, waitForSession } from './support/agents'
import { openBoard } from './support/board'
import { expect, FAKE_MCP_BINARY, test, type Omo } from './support/omo'
import { expectTerminalText, terminalIn } from './support/terminal'

const SHOTS = process.env.OMO_SCREENSHOTS ?? ''

/** Marks every fake MCP server process of one test (its value is the test's home); omo hands no OMO_ variable to the servers it starts. */
const MARK = 'FAKEMCP_E2E_MARK'

/** The tools of the fake server. */
const FAKE_TOOLS = ['add_tool', 'big', 'crash', 'echo', 'printenv', 'rpcerror', 'sleep', 'stderr', 'whoami']

const row = (page: Page, name: string) => page.locator(`[data-mcp-server="${name}"]`)
const editor = (page: Page, name: string) => page.locator(`[data-mcp-server-editor="${name}"]`)
const saveBar = (page: Page, name: string) => page.getByRole('region', { name: `Save changes to the MCP server ${name}` })
const state = (page: Page, name: string) => row(page, name).locator('[data-mcp-server-state]')

async function openMcp(page: Page, omo: Omo) {
  await openBoard(page, omo)
  await page.goto(omo.url('/settings/mcp'))
  await expect(page.locator('[data-new-mcp-server]')).toBeVisible()
}

async function save(page: Page, name: string) {
  await saveBar(page, name).getByRole('button', { name: 'Save', exact: true }).click()
  await expect(saveBar(page, name).locator('[data-save-state]')).toHaveText('Saved')
}

/** The environment of a fake server of this test. */
const fakeEnv = (omo: Omo, extra: Record<string, string> = {}) => ({ FAKEMCP_SERVER: '1', [MARK]: omo.home, ...extra })

/** Stores a fake stdio server through the API. */
async function addFake(omo: Omo, name: string, o: { roles?: string[]; global?: boolean; env?: Record<string, string> } = {}) {
  await omo.api('PUT', `/api/mcp/servers/${name}`, {
    name,
    enabled: true,
    transport: 'stdio',
    command: FAKE_MCP_BINARY,
    args: [],
    env: fakeEnv(omo, o.env),
    roles: o.roles ?? [],
    global: o.global ?? false,
    tools: { allow: [], deny: [] },
  })
}

/** Ids of the fake MCP server processes of this test that still run. Linux only (reads /proc). */
function fakeProcesses(omo: Omo): number[] {
  if (process.platform !== 'linux') return []
  const want = `${MARK}=${omo.home}\0`
  const out: number[] = []
  for (const name of readdirSync('/proc')) {
    if (!/^\d+$/.test(name)) continue
    try {
      if (!readFileSync(`/proc/${name}/environ`, 'latin1').includes(want)) continue
      const stat = readFileSync(`/proc/${name}/stat`, 'latin1')
      if (stat.slice(stat.lastIndexOf(')') + 2, stat.lastIndexOf(')') + 3) === 'Z') continue
      out.push(Number(name))
    } catch {
      // gone, or not ours to read
    }
  }
  return out
}

interface RpcAnswer {
  status: number
  result?: { tools?: { name: string }[]; content?: { type: string; text?: string }[]; isError?: boolean }
  error?: { code: number; message: string }
}

/**
 * One JSON-RPC request to omo's /mcp as an MCP client on another machine
 * sends it: POST, the bearer, both answer forms accepted. The endpoint is
 * stateless, so no session is set up first.
 */
async function mcp(omo: Omo, bearer: string, method: string, params: object = {}): Promise<RpcAnswer> {
  const res = await fetch(`${omo.base}/mcp`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Accept: 'application/json, text/event-stream', Authorization: `Bearer ${bearer}` },
    body: JSON.stringify({ jsonrpc: '2.0', id: 1, method, params }),
  })
  const text = await res.text()
  if (res.status !== 200) return { status: res.status }
  // An SSE answer: the message is the data of its last event.
  const body = (res.headers.get('content-type') ?? '').includes('text/event-stream')
    ? text
        .split('\n')
        .filter((l) => l.startsWith('data:'))
        .map((l) => l.slice(5).trim())
        .at(-1)!
    : text
  return { status: res.status, ...(JSON.parse(body) as Omit<RpcAnswer, 'status'>) }
}

const relayed = (a: RpcAnswer, server: string) => (a.result?.tools ?? []).map((t) => t.name).filter((n) => n.startsWith(`${server}__`))
const textOf = (a: RpcAnswer) => (a.result?.content ?? []).map((c) => c.text ?? '').join('')

/** Creates a personal access token on the Access part and returns its secret. */
async function createToken(page: Page, name: string): Promise<string> {
  const access = page.locator('[data-mcp-section="access"]')
  await access.getByLabel('Name', { exact: true }).fill(name)
  await access.getByRole('button', { name: 'Create token' }).click()
  const dialog = page.locator('[data-mcp-token-secret]')
  await expect(dialog.locator('pre')).toHaveText(/^omo_pat_/)
  const secret = (await dialog.locator('pre').innerText()).trim()
  await dialog.getByRole('button', { name: 'Done' }).click()
  return secret
}

test('a stdio server made through the form is tested, goes live for a token, logs, restarts, is switched off and deleted', async ({ page, omo }) => {
  await openMcp(page, omo)
  await page.getByRole('button', { name: 'Add MCP server…' }).click()
  await page.getByLabel('Name of the MCP server').fill('fake')
  await page.getByRole('button', { name: 'Add', exact: true }).click()
  const ed = editor(page, 'fake')
  await ed.getByLabel('Command').fill(FAKE_MCP_BINARY)
  for (const [k, v] of Object.entries(fakeEnv(omo))) {
    await ed.getByRole('button', { name: 'Add variable' }).click()
    await ed.getByRole('textbox', { name: /^Name of variable \d+$/ }).last().fill(k)
    await ed.getByRole('textbox', { name: `Value of ${k}`, exact: true }).fill(v)
  }
  await ed.locator('[data-mcp-global]').click()

  // Test starts the draft once, before it is saved, and stops it.
  await ed.locator('[data-mcp-test]').click()
  await expect(ed.locator('[data-mcp-test-result]')).toContainText(`${FAKE_TOOLS.length} tools in`)
  await expect(ed.locator('[data-mcp-test-result]')).toContainText('fake__echo, fake__printenv')
  await expect.poll(() => fakeProcesses(omo)).toEqual([])
  await expect(ed).toContainText('(not saved yet)')

  await save(page, 'fake')
  await expect(state(page, 'fake')).toHaveText('not running')
  await expect(row(page, 'fake')).toContainText('also without an omo session')

  // A program with a token lists and calls the server's tools; the page
  // follows without being read again.
  const navigations: string[] = []
  page.on('framenavigated', (f) => navigations.push(f.url()))
  const token = await createToken(page, 'laptop')
  const list = await mcp(omo, token, 'tools/list')
  expect(list.status).toBe(200)
  expect(relayed(list, 'fake').sort()).toEqual(FAKE_TOOLS.map((t) => `fake__${t}`))
  const echo = await mcp(omo, token, 'tools/call', { name: 'fake__echo', arguments: { text: 'from a token' } })
  expect(textOf(echo)).toBe('echo: from a token')
  await expect(state(page, 'fake')).toHaveText('running')
  await expect(row(page, 'fake').locator('[data-mcp-server-tools]')).toHaveText(`${FAKE_TOOLS.length} tools`)
  await expect(ed.locator('[data-mcp-tools]')).toContainText('fake__echo')
  expect(fakeProcesses(omo)).toHaveLength(1)
  expect(navigations).toEqual([])
  // omo mcp status in this home tells the same.
  const status = await omo.cli(['mcp', 'status'])
  expect(status.code).toBe(0)
  expect(status.stdout).toContain(`    fake: running, ${FAKE_TOOLS.length} tools; for every role, also without an omo session\n`)
  expect(status.stdout).toContain(`tools (${FAKE_TOOLS.length}):`)
  expect(status.stdout).toContain('    fake__echo\n')

  // The log has the start and the call.
  await ed.locator('[data-mcp-log-toggle]').click()
  const log = page.locator('[data-mcp-log="fake"] [data-mcp-log-text]')
  await expect(log).toContainText(`started`)
  await expect(log).toContainText(`with ${FAKE_TOOLS.length} tools`)
  await expect(log).not.toContainText(omo.home)
  if (SHOTS !== '') {
    mkdirSync(SHOTS, { recursive: true })
    await ed.locator('[data-mcp-server-status]').evaluate((el) => el.scrollIntoView({ block: 'start' }))
    await page.screenshot({ path: join(SHOTS, 'mcp-relay-running.png') })
    await page.locator('[data-mcp-tokens]').evaluate((el) => el.scrollIntoView({ block: 'center' }))
    await page.screenshot({ path: join(SHOTS, 'mcp-relay-tokens.png') })
  }

  // Restart stops the process; the next use starts it again.
  await ed.locator('[data-mcp-restart]').click()
  await expect(state(page, 'fake')).toHaveText('not running')
  await expect.poll(() => fakeProcesses(omo)).toEqual([])
  await page.locator('[data-mcp-log="fake"] [data-mcp-log-refresh]').click()
  await expect(log).toContainText('stopping')
  expect(textOf(await mcp(omo, token, 'tools/call', { name: 'fake__echo', arguments: { text: 'again' } }))).toBe('echo: again')
  await expect(state(page, 'fake')).toHaveText('running')

  // Only for omo's sessions: the token no longer sees it, and its process stops.
  await ed.locator('[data-mcp-global]').click()
  await save(page, 'fake')
  expect(relayed(await mcp(omo, token, 'tools/list'), 'fake')).toEqual([])
  expect((await mcp(omo, token, 'tools/call', { name: 'fake__echo', arguments: { text: 'x' } })).error?.message ?? '').toMatch(/unknown tool/i)
  await expect.poll(() => fakeProcesses(omo)).toEqual([])

  // Switched off: the page says so.
  await ed.locator('[data-mcp-enabled-server]').click()
  await save(page, 'fake')
  await expect(state(page, 'fake')).toHaveText('off')

  await ed.getByRole('button', { name: 'Delete fake…' }).click()
  await page.locator('[data-question-confirm]').click()
  await expect(row(page, 'fake')).toHaveCount(0)
  expect(fakeProcesses(omo)).toEqual([])
})

test('a server whose program exits shows why, in the test and in the list', async ({ page, omo }) => {
  await addFake(omo, 'broken', { global: true, env: { FAKEMCP_MODE: 'exit' } })
  const { secret } = await omo.api<{ secret: string }>('POST', '/api/mcp/tokens', { name: 'probe' })
  await openMcp(page, omo)
  await row(page, 'broken').locator('[data-mcp-server-toggle]').click()
  const ed = editor(page, 'broken')
  await ed.locator('[data-mcp-test]').click()
  await expect(ed.locator('[data-mcp-test-result]')).toContainText('The test failed: server broken is not reachable')

  // A program that uses it: the tool list leaves it out, and the page tells why.
  expect(relayed(await mcp(omo, secret, 'tools/list'), 'broken')).toEqual([])
  await expect(row(page, 'broken').locator('[data-mcp-server-failure]')).not.toBeEmpty()
  const failure = await row(page, 'broken').locator('[data-mcp-server-failure]').innerText()
  await expect(ed.locator('[data-mcp-last-error]')).toHaveText(failure)
  const status = await omo.cli(['mcp', 'status'])
  expect(status.stdout).toContain(`    broken: not running (${failure}); for every role, also without an omo session\n`)
  await expect(page.locator('[data-mcp-off]')).toHaveCount(0)
})

test('"also available without an omo session" decides what a personal token sees', async ({ page, omo }) => {
  await addFake(omo, 'shared', { global: true })
  await addFake(omo, 'private', { global: false })
  await openMcp(page, omo)
  const token = await createToken(page, 'desktop')
  const list = await mcp(omo, token, 'tools/list')
  expect(relayed(list, 'shared')).toHaveLength(FAKE_TOOLS.length)
  expect(relayed(list, 'private')).toEqual([])
  expect((await mcp(omo, token, 'tools/call', { name: 'private__echo', arguments: { text: 'x' } })).error?.message ?? '').toMatch(/unknown tool/i)
  // A connection without an omo session gets no board actions.
  const whoami = await mcp(omo, token, 'tools/call', { name: 'whoami', arguments: {} })
  expect(whoami.result?.isError).toBe(true)
  expect(textOf(whoami)).toContain('omo ai')
  // Revoked, the token is refused.
  await page.locator('[data-mcp-token="desktop"] [data-mcp-token-revoke]').click()
  await page.locator('[data-question-confirm]').click()
  await expect(page.locator('[data-mcp-token="desktop"]')).toHaveCount(0)
  expect((await mcp(omo, token, 'tools/list')).status).toBe(401)
})

test.describe('an agent session', () => {
  test.use({
    viewport: { width: 1440, height: 900 },
    omoOptions: {
      agents: {
        developer: script('ready', 'mcp-tools', 'mcp|dev__echo|{"text":"from the developer"}', 'mcp|dev__whoami', 'print|AGENT-MCP-DONE', 'hang'),
      },
    },
  })

  test('calls a relayed tool of a server for its role, in its own folder, and is not offered a server of another role', async ({ page, omo }) => {
    await addFake(omo, 'dev', { roles: ['developer'] })
    await addFake(omo, 'rev', { roles: ['reviewer'], global: true })
    const n = await omo.createTicket({ title: 'Use a relayed tool' })
    await omo.moveTicket(n, 'ready')
    const session = await waitForSession(omo, n, 'developer')

    await openBoard(page, omo)
    await page.goto(omo.url(`/${n}?tab=agents`))
    const panel = page.locator('dialog[data-ticket-panel]')
    const agent = panel.locator(`[data-session="${session.id}"] [data-agent-row]`)
    await agent.click()
    const term = terminalIn(panel)
    await expectTerminalText(term, 'AGENT-MCP-DONE')
    await expectTerminalText(term, 'fake: mcp dev__echo: echo: from the developer')
    await expectTerminalText(term, `cwd=${session.workdir}`)
    const tools = ((await term.locator('.xterm-rows').innerText()).replace(/\s+/g, '').match(/fake:mcptools:([^\n]*?)fake:mcpdev__echo/) ?? [])[1] ?? ''
    expect(tools).toContain('dev__echo')
    expect(tools).not.toContain('rev__')
    // The agents tab marks the session as one that uses omo's MCP tools.
    await expect(agent.locator('[data-agent-mcp]')).toBeVisible()

    // The page shows the session's process of the server.
    await page.goto(omo.url('/settings/mcp'))
    await expect(state(page, 'dev')).toHaveText('running')
    await expect(state(page, 'rev')).toHaveText('not running')
    expect(fakeProcesses(omo)).toHaveLength(1)

    await expectNoAgentProcesses(omo)
    await expect.poll(() => fakeProcesses(omo), { message: 'fake MCP servers left behind' }).toEqual([])
  })
})

test('the commands on the Access part reach the endpoint at the address of the page, with a token in the header they show', async ({ page, omo }) => {
  await addFake(omo, 'shared', { global: true })
  await openMcp(page, omo)
  const token = await createToken(page, 'cli')
  // The resource omo announces at this address is the endpoint the commands name.
  const resource = await page.evaluate(async () => ((await (await fetch('/.well-known/oauth-protected-resource/mcp')).json()) as { resource: string }).resource)
  expect(resource).toBe(omo.url('/mcp'))
  const command = async (id: string) => (await page.locator(`[data-mcp-command="${id}"] pre`).innerText()).trim()
  for (const id of ['claude-oauth', 'codex-oauth', 'claude-token', 'codex-token']) {
    expect((await command(id)).split(/\s+/), id).toContain(resource)
  }
  expect(await command('codex-token')).toContain('--bearer-token-env-var OMO_MCP_TOKEN')
  // The header of the Claude command, with the token where the program puts in its variable.
  const header = /--header '([^:']+): ([^']*)'/.exec(await command('claude-token'))
  expect(header?.[2]).toBe('Bearer ${OMO_MCP_TOKEN}')
  const value = header![2]!.replace('${OMO_MCP_TOKEN}', token)
  const names = await page.evaluate(
    async ([url, name, value]) => {
      const res = await fetch(url!, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Accept: 'application/json, text/event-stream', [name!]: value! },
        body: JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'tools/list', params: {} }),
      })
      const text = await res.text()
      const data = text.includes('data:') ? text.split('\n').filter((l) => l.startsWith('data:')).at(-1)!.slice(5) : text
      return ((JSON.parse(data) as { result: { tools: { name: string }[] } }).result.tools ?? []).map((t) => t.name)
    },
    [resource, header![1], value],
  )
  expect(names).toContain('shared__echo')
})

test('switched off, the page says so and /mcp does not exist; switched on again, it works', async ({ page, omo }) => {
  await addFake(omo, 'shared', { global: true })
  const { secret } = await omo.api<{ secret: string }>('POST', '/api/mcp/tokens', { name: 'probe' })
  await openMcp(page, omo)
  await expect(state(page, 'shared')).toHaveText('not running')
  await expect(page.locator('[data-mcp-off]')).toHaveCount(0)

  const sw = page.locator('[data-mcp-switches]')
  const bar = sw.getByRole('region', { name: 'Save changes to the general MCP switches' })
  await sw.locator('[data-mcp-enabled]').click()
  await bar.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(sw.locator('[data-save-state]')).toHaveText('Saved')
  await expect(page.locator('[data-mcp-off]')).toContainText('MCP is switched off')
  expect((await mcp(omo, secret, 'tools/list')).status).toBe(404)
  await row(page, 'shared').locator('[data-mcp-server-toggle]').click()
  await expect(editor(page, 'shared')).toBeVisible()
  await expect(editor(page, 'shared').locator('[data-mcp-test]')).toHaveCount(0)
  await expect(editor(page, 'shared').locator('[data-mcp-restart]')).toHaveCount(0)
  const status = await omo.cli(['mcp', 'status'])
  expect(status.stdout + status.stderr).toContain('switched off')

  await sw.locator('[data-mcp-enabled]').click()
  await bar.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.locator('[data-mcp-off]')).toHaveCount(0)
  expect(relayed(await mcp(omo, secret, 'tools/list'), 'shared')).toHaveLength(FAKE_TOOLS.length)
  await expect(state(page, 'shared')).toHaveText('running')
})
