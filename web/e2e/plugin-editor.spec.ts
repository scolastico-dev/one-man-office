import type { Locator, Page } from '@playwright/test'
import { dirname, join } from 'node:path'
import { existsSync } from 'node:fs'
import { expect, type Omo } from './support/omo'
import { test, configure, tasks, forwards, expectClean, processTree, expectTreeGone, IDLE_SECONDS } from './support/editor'
import { openBoard, expectBoardLive } from './support/board'
import { script, waitForSession } from './support/agents'
import { initRepo } from './support/git'

// The runtime bridge in the archive is a POSIX fixture, not a native publisher
// distribution. Native platform behavior remains in the Lua/launcher fixtures.
test.skip(process.platform !== 'linux', 'local editor protocol fixture uses a POSIX runtime bridge')
test.setTimeout(90_000)
test.use({ launchOptions: { args: ['--host-resolver-rules=MAP board.example.test 127.0.0.1', '--no-proxy-server'] } })
const projectButton = (page: Page, name = 'demo') => page.locator(`.sidebar-plugins > section[aria-label="${name}"]`).getByRole('button', { name: 'Open in VS Code', exact: true })
async function openEditor(page: Page, button: Locator): Promise<Page> {
  const popup = page.context().waitForEvent('page')
  await button.click()
  const editor = await popup
  await expect(editor.getByRole('heading', { name: 'Editor stand-in' })).toBeVisible({ timeout: 30_000 })
  await expect(editor.locator('#asset')).toHaveText('Asset loaded')
  await expect(editor.locator('#socket')).toHaveText('connected')
  expect(await editor.evaluate(() => window.opener)).toBeNull()
  expect(editor.url()).not.toContain('#')
  return editor
}
async function details(page: Page) {
  await page.goto(new URL('/settings/plugins', page.url()).href)
  await page.locator('[data-plugin="vscode"] [data-plugin-toggle]').click()
  return page.locator('[data-plugin-detail="vscode"]')
}
async function runningTree(omo: Omo) {
  let tree: number[] = []
  await expect.poll(async () => {
    const task = (await tasks(omo)).find(t => t.state === 'running')
    tree = task ? processTree(task) : []
    return tree.length
  }).toBe(2)
  return tree
}

for (const editorName of ['code-server', 'openvscode']) {
  for (const subdomain of [false, true]) {
    test(`${editorName}: first click, cache, assets, upgrades and idle through ${subdomain ? 'cross-site subdomain' : 'path'} proxy`, async ({ page, omo, editor, repo }) => {
      await editor.install(omo)
      await configure(omo, 'vscode', { config: { editor: editorName, idle_minutes: 1 } })
      await omo.stop()
      const boardOrigin = `http://board.example.test:${omo.port}`
      expect((await omo.cli(['config', 'set', 'public_url', boardOrigin])).code).toBe(0)
      if (subdomain) expect((await omo.cli(['config', 'set', 'proxy_base_host', 'apps.localhost'])).code).toBe(0)
      await omo.start()
      // Board host is deliberately outside apps.localhost. The actual plugin
      // click, not a direct page.goto(Open URL), must cross the bootstrap gate.
      await page.goto(boardOrigin + '/#' + omo.token)
      await expectBoardLive(page)
      const policy = (await page.request.get(omo.url('/'))).headers()['content-security-policy']
      expect(policy).toContain("script-src 'self'")
      await page.locator('[data-sidebar-toggle]').click()
      editor.hold()
      const popup = page.context().waitForEvent('page')
      await projectButton(page).click()
      const first = await popup
      await expect(page.locator('[data-plugin-panel^="vscode:opening-"]')).toContainText('download', { timeout: 15_000 })
      expect(first.url()).toBe('about:blank')
      expect((await tasks(omo)).filter(t => t.state === 'running')).toEqual([])
      await expect.poll(() => editor.activeDownloads).toBe(1)
      editor.release()
      await expect(first.getByRole('heading', { name: 'Editor stand-in' })).toBeVisible({ timeout: 30_000 })
      await expect(first.locator('#asset')).toHaveText('Asset loaded')
      await expect(first.locator('#socket')).toHaveText('connected')
      await expect(first.locator('#folder')).toHaveText(repo)
      expect(first.url()).not.toContain(omo.token)
      expect(first.url()).not.toContain('#')
      expect(await first.evaluate(() => window.opener)).toBeNull()
      expect(new URL(first.url()).hostname).toBe(subdomain ? new URL((await forwards(omo))[0]!.address).hostname : 'board.example.test')
      if (subdomain) expect(new URL(first.url()).hostname).toMatch(/\.apps\.localhost$/)
      else expect(first.url()).toContain(`/proxy/${(await forwards(omo))[0]!.id}/`)
      const initial = (await tasks(omo)).find(t => t.state === 'running')!
      const f = (await forwards(omo))[0]!
      expect(f.host).toBe('127.0.0.1')
      expect(f.strip_prefix).toBe(editorName === 'code-server')
      const tree = await runningTree(omo)
      const second = await openEditor(page, projectButton(page))
      expect((await tasks(omo)).filter(t => t.state === 'running').map(t => t.id)).toEqual([initial.id])
      expect((await forwards(omo)).map(f => f.id)).toEqual([f.id])
      expect(editor.requests).toHaveLength(1)
      expect((await page.request.get(omo.url('/'))).headers()['content-security-policy']).toBe(policy)
      expect(await page.getByRole('dialog').count()).toBe(0)
      // A proxy cookie authenticates the app, never the board API.
      expect((await page.context().cookies(boardOrigin)).some(c => c.name === 'omo_proxy_session')).toBe(true)
      expect(await page.evaluate(async () => (await fetch('/api/server', { credentials: 'include' })).status)).toBe(401)
      expect((await page.context().cookies(first.url())).some(c => c.name === 'omo_proxy_session' && c.httpOnly)).toBe(true)
      // Observe a protocol round-trip after an entire accelerated idle interval.
      await expect(async () => {
        expect((await tasks(omo)).find(t => t.id === initial.id)?.state).toBe('running')
        await expect(second.locator('#socket')).toHaveText('connected')
        expect(Date.now() - Date.parse(initial.started_at)).toBeGreaterThan((IDLE_SECONDS + 1) * 1000)
      }).toPass({ timeout: 12_000, intervals: [500] })
      expect(await second.evaluate(() => (window as unknown as { roundTrip(): Promise<string> }).roundTrip())).toBe('connected')
      await first.close()
      await second.close()
      // Reconnect before deadline; the stable task and forward must survive.
      const third = await openEditor(page, projectButton(page))
      expect((await tasks(omo)).find(t => t.id === initial.id)?.state).toBe('running')
      const runtime = JSON.parse(initial.output.find(line => line.startsWith('Runtime: '))!.slice(9)) as string
      expect(existsSync(runtime)).toBe(true)
      await expect(async () => {
        expect((await tasks(omo)).find(t => t.id === initial.id)?.state).toBe('running')
        expect((await forwards(omo)).map(f => f.id)).toEqual([f.id])
        expect(Date.now() - Date.parse(initial.started_at)).toBeGreaterThan((IDLE_SECONDS * 2 + 2) * 1000)
      }).toPass({ timeout: 12_000, intervals: [500] })
      expect(await third.evaluate(() => (window as unknown as { roundTrip(): Promise<string> }).roundTrip())).toBe('connected')
      const disconnected = Date.now()
      await third.close()
      await expectClean(omo)
      expect(Date.now() - disconnected).toBeGreaterThanOrEqual((IDLE_SECONDS - 0.5) * 1000)
      expect((await tasks(omo)).find(t => t.id === initial.id)?.reason).toBe('idle')
      await expectTreeGone(tree)
      expect(existsSync(runtime)).toBe(false)
      // A fresh instance checks disk cache, without another HTTPS request.
      const cached = await openEditor(page, projectButton(page))
      expect(editor.requests).toHaveLength(1)
      expect((await tasks(omo)).find(t => t.state === 'running')?.id).not.toBe(initial.id)
      const detail = await details(page)
      await detail.locator('[data-task-stop]').click()
      await expectClean(omo)
      await cached.close()
    })
  }
}

test('checksum mismatch never extracts or starts an editor and closes its reserved tab', async ({ page, omo, editor }) => {
  await editor.install(omo, { badChecksum: true })
  await openBoard(page, omo)
  await page.locator('[data-sidebar-toggle]').click()
  const popup = page.context().waitForEvent('page')
  await projectButton(page).click()
  const blank = await popup
  await expect(page.locator('[data-plugin-controls="project"] [data-plugin-control-answer]')).toContainText(/installation failed.*SHA-256.*digest/i, { timeout: 20_000 })
  await expect.poll(() => blank.isClosed()).toBe(true)
  await expectClean(omo)
  expect(editor.requests).toHaveLength(1)
})

for (const action of ['disable', 'shutdown']) {
  test(`first-use download cancels on ${action} and never auto-resumes`, async ({ page, omo, editor }) => {
    await editor.install(omo)
    await openBoard(page, omo)
    await page.locator('[data-sidebar-toggle]').click()
    editor.hold()
    const popup = page.context().waitForEvent('page')
    await projectButton(page).click()
    const blank = await popup
    await expect.poll(() => editor.activeDownloads).toBe(1)
    if (action === 'disable') {
      await configure(omo, 'vscode', { enabled: false })
      await expect.poll(() => blank.isClosed()).toBe(true)
    } else {
      await omo.stop()
    }
    await expect.poll(() => editor.activeDownloads).toBe(0)
    editor.release()
    if (action === 'disable') await configure(omo, 'vscode', { enabled: true })
    else await omo.start()
    await expectClean(omo)
    await page.reload()
    await expectBoardLive(page)
    await expect(page.getByRole('button', { name: 'Open in VS Code', exact: true })).toBeVisible()
    expect(editor.requests).toHaveLength(1)
    await blank.close()
  })
}

test('Microsoft is visible and unavailable despite old consent; unsupported platform refuses before download', async ({ page, omo, editor }) => {
  await editor.install(omo, { unsupported: true })
  await openBoard(page, omo)
  const detail = await details(page)
  const choices = detail.getByRole('combobox', { name: 'Editor (pinned publisher builds)' })
  await expect(choices.locator('option[value="microsoft"]')).toContainText('terms not confirmed')
  await expect(detail.getByRole('checkbox', { name: /licen|terms/i })).toHaveCount(0)
  await configure(omo, 'vscode', { config: { editor: 'microsoft', accept_microsoft_license: true } })
  const refused = await omo.api<{ message: string }>('POST', '/api/plugins/vscode/actions/open-project', { project: omo.project })
  expect(refused.message).toContain('terms have not been confirmed')
  await page.goto(omo.url('/'))
  await page.locator('[data-sidebar-toggle]').click()
  await projectButton(page).click()
  await expect(page.locator('[data-plugin-controls="project"] [data-plugin-control-answer]')).toContainText('terms have not been confirmed')
  await configure(omo, 'vscode', { config: { editor: 'openvscode' } })
  await projectButton(page).click()
  await expect(page.locator('[data-plugin-controls="project"] [data-plugin-control-answer]')).toContainText('unavailable on windows-arm64')
  expect(editor.requests).toEqual([])
  await expectClean(omo)
})

test.describe('worktree context', () => {
  test.use({ omoOptions: async ({ editor }, use) => { await use({ tlsCA: editor.ca, agents: { pm: script('ready', 'hang'), ceo: 'hang' } }) } })
  test('per-project worktrees use authoritative folders; Stop permits switching without duplicate instances', async ({ page, omo, editor, repo }) => {
    const secondRepo = join(dirname(repo), 'second')
    initRepo(secondRepo)
    const project = await omo.api<{ id: string }>('POST', '/api/projects', { path: secondRepo, name: 'second' })
    const n = await omo.createTicket({ type: 'story', title: 'Two worktrees', projects: [omo.project, project.id] })
    await omo.moveTicket(n, 'ready')
    await waitForSession(omo, n, 'pm')
    type Ticket = { projects: { project: string; worktree: string }[] }
    await expect.poll(async () => (await omo.api<Ticket>('GET', `/api/tickets/${n}`)).projects.every(p => p.worktree !== '')).toBe(true)
    const ticket = await omo.api<Ticket>('GET', `/api/tickets/${n}`)
    await editor.install(omo)
    await openBoard(page, omo)
    await page.goto(omo.url(`/${n}`))
    const buttons = page.locator('[data-plugin-controls="ticket"]').getByRole('button', { name: /Open in VS Code/ })
    await expect(buttons).toHaveCount(2)
    const first = await openEditor(page, buttons.nth(0))
    const firstFolder = await first.locator('#folder').textContent()
    expect(ticket.projects.map(p => p.worktree)).toContain(firstFolder)
    expect([repo, secondRepo]).not.toContain(firstFolder)
    await page.locator('[data-open-page]').click()
    const other = await openEditor(page, page.locator('[data-plugin-controls="ticket"]').getByRole('button', { name: /Open in VS Code/ }).nth(1))
    const otherFolder = await other.locator('#folder').textContent()
    expect(ticket.projects.map(p => p.worktree)).toContain(otherFolder)
    expect(otherFolder).not.toBe(firstFolder)
    expect((await tasks(omo)).filter(t => t.state === 'running')).toHaveLength(2)
    expect((await tasks(omo)).filter(t => t.state === 'running').every(t => t.ticket === n)).toBe(true)
    await configure(omo, 'vscode', { config: { editor: 'openvscode', idle_minutes: 1 } })
    await page.locator('[data-plugin-controls="ticket"]').getByRole('button', { name: /Open in VS Code/ }).nth(0).click()
    await expect(page.locator('[data-plugin-controls="ticket"] [data-plugin-control-answer]')).toContainText(/Stop.*plugin settings/i, { timeout: 20_000 })
    expect((await tasks(omo)).filter(t => t.state === 'running')).toHaveLength(2)
    expect(await forwards(omo)).toHaveLength(2)
    const selected = (await tasks(omo)).find(t => t.state === 'running' && t.output.some(line => line.includes(JSON.stringify(firstFolder))))!
    const stoppedTree = processTree(selected)
    await omo.api('POST', `/api/plugins/vscode/tasks/${selected.name}/stop`, {})
    await expectTreeGone(stoppedTree)
    const changed = await openEditor(page, page.locator('[data-plugin-controls="ticket"]').getByRole('button', { name: /Open in VS Code/ }).nth(0))
    await expect(changed.locator('#folder')).toHaveText(firstFolder!)
    expect((await tasks(omo)).filter(t => t.state === 'running')).toHaveLength(2)
    const trees = (await tasks(omo)).filter(t => t.state === 'running').map(processTree)
    await omo.api('POST', `/api/tickets/${n}/cancel`, { delete_branch: false })
    await expectClean(omo)
    for (const tree of trees) await expectTreeGone(tree)
    await first.close(); await other.close(); await changed.close()
  })
})
