import { readFileSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { expect, test, REPO_DIR, type Omo } from './support/omo'
import { installSample } from './support/samplePlugin'
import { configure, expectClean, expectTreeGone, forwards, processTree, tasks } from './support/editor'
import { openBoard, markPage, expectNotReloaded, nudgeReconnect } from './support/board'
import { git } from './support/git'

async function status(omo: Omo) {
  return (await omo.api<{ message: string }>('POST', '/api/plugins/sample/actions/status', {})).message
}
async function start(omo: Omo, ticket: number) {
  await omo.api('POST', '/api/plugins/sample/actions/ticket', { ticket })
  let tree: number[] = []
  await expect.poll(async () => {
    const task = (await tasks(omo, 'sample')).find(t => t.state === 'running' && t.ticket === ticket)
    tree = task ? processTree(task) : []
    return tree.length
  }).toBe(2)
  return tree
}

test('one sample uses declarations and the public facade across routes, configuration, tasks, update and enable lifetimes', async ({ page, omo, repo }) => {
  const sample = await installSample(omo)
  await expect.poll(() => status(omo)).toBe('starts=1 running=0 mode=one')
  const n = await omo.createTicket({ title: 'Sample task' })
  await openBoard(page, omo)
  const policy = (await page.request.get(omo.url('/'))).headers()['content-security-policy']
  expect(policy).toContain("script-src 'self'")
  await page.locator('[data-sidebar-toggle]').click()
  await expect(page.getByRole('button', { name: 'Sample status', exact: true })).toBeVisible()
  await page.getByRole('button', { name: 'Dynamic status', exact: true }).click()
  await expect(page.locator('[data-plugin-controls="sidebar"] [data-plugin-control-answer]')).toHaveText('starts=1 running=0 mode=one')
  const panel = page.locator('[data-plugin-panel="sample:sidebar"]')
  await expect(panel).toContainText('board route 1')
  await expect.poll(() => panel.locator('img').evaluate((img: HTMLImageElement) => img.complete && img.naturalWidth > 0)).toBe(true)
  const asset = await panel.locator('img').getAttribute('src')
  const entry = await page.locator('script[data-omo-plugin="sample"]').getAttribute('src')
  await page.getByRole('button', { name: 'Sample repository', exact: true }).click()
  await expect(page.locator('[data-plugin-controls="project"] [data-plugin-control-answer]')).toHaveText('repository: ' + repo)
  await markPage(page)
  await page.locator(`li[data-ticket="${n}"] a`).first().click()
  await expect(page.locator('[data-ticket-panel]')).toBeVisible()
  await expect(page.locator('[data-plugin-panel="sample:ticket"]')).toContainText('ticket route 2')
  await expectNotReloaded(page)
  await page.getByRole('button', { name: 'Start sample ticket', exact: true }).click()
  await expect(page.locator('[data-plugin-controls="ticket"] [data-plugin-control-answer]')).toHaveText(`Started worker-${n}`)
  let tree: number[] = []
  await expect.poll(async () => { tree = processTree((await tasks(omo, 'sample'))[0]!); return tree.length }).toBe(2)
  expect((await tasks(omo, 'sample'))[0]!.state).toBe('running') // the start hook has returned
  const dialogs = await page.getByRole('dialog').count()
  const popup = page.context().waitForEvent('page')
  await page.getByRole('button', { name: 'Open sample', exact: true }).click()
  const opened = await popup
  await expect(opened.getByRole('heading', { name: 'Editor stand-in' })).toBeVisible()
  await expect(opened.locator('#asset')).toHaveText('Asset loaded')
  await expect(opened.locator('#socket')).toHaveText('connected')
  await expect(page.getByRole('dialog')).toHaveCount(dialogs)
  await page.locator('[data-open-page]').click()
  await expect(page.locator('[data-ticket-page]')).toBeVisible()
  await page.getByRole('button', { name: 'Stop sample', exact: true }).click()
  await expectClean(omo, 'sample')
  await expectTreeGone(tree)
  await opened.close()
  tree = await start(omo, n)
  await page.goto(omo.url('/settings/plugins'))
  await page.locator('[data-plugin="sample"] [data-plugin-toggle]').click()
  const detail = page.locator('[data-plugin-detail="sample"]')
  const task = detail.getByRole('article', { name: `worker-${n}` }).filter({ has: page.locator('[data-task-stop]') })
  await expect(task).toContainText(`Ticket #${n}`)
  await expect(task.locator('[data-task-elapsed]')).toHaveText(/\d+s/)
  await expect(task.locator('pre')).toContainText('[redacted]')
  await expect(task.locator('pre')).toContainText('\\u{202E}')
  expect(await task.textContent()).not.toContain('fixture-secret-value')
  await detail.getByRole('combobox', { name: 'Sample mode' }).selectOption('two')
  // The save reloads the plugin and cancels the old runtime's context: an action started before the save has completed can be cancelled with it.
  const saved = page.waitForResponse((r) => r.request().method() === 'PUT' && r.url().endsWith('/api/plugins/sample') && r.ok())
  await detail.getByRole('button', { name: 'Save', exact: true }).click()
  await saved
  await expect.poll(() => status(omo)).toBe('starts=1 running=1 mode=two')
  await expect(task.locator('[data-task-stop]')).toBeEnabled()
  await task.locator('[data-task-stop]').click()
  await expectTreeGone(tree)
  await expectClean(omo, 'sample')
  tree = await start(omo, n)
  sample.repo.commit({ ...sample.manifest, version: '1.0.1' }, { 'frontend.js': sample.entry.replace('Sample route', 'Updated sample route') })
  await detail.locator('[data-plugin-update]').click()
  await expect(detail.locator('[data-plugin-update-result]')).toContainText('Updated')
  await expectClean(omo, 'sample')
  await expectTreeGone(tree)
  expect(await status(omo)).toBe('starts=1 running=0 mode=two')
  await expect(page.getByRole('heading', { name: 'Updated sample route' })).toBeVisible()
  expect((await page.request.get(omo.url(entry!))).status()).toBe(404)
  expect((await page.request.get(omo.url(asset!))).status()).toBe(404)
  await detail.locator('[data-plugin-enabled]').click()
  await expect(page.locator('script[data-omo-plugin="sample"]')).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Dynamic status', exact: true })).toHaveCount(0)
  // Enabling reloads the plugin: wait for the saved response before the status action.
  // It names the enabling request: the earlier disabling PUT may still be answered after this waiter exists.
  const enabled = page.waitForResponse((r) => r.request().method() === 'PUT' && r.url().endsWith('/api/plugins/sample') && r.ok() && r.request().postDataJSON()?.enabled === true)
  await detail.locator('[data-plugin-enabled]').click()
  await enabled
  await expect.poll(() => status(omo)).toBe('starts=2 running=0 mode=two')
  await expect(page.locator('script[data-omo-plugin="sample"]')).toHaveCount(1)
  tree = await start(omo, n)
  await page.route('**/api/events*', route => route.abort('connectionrefused'))
  await omo.stop()
  await expect(page.locator('[data-connection]')).toHaveAttribute('data-state', /^(retrying|reconnecting)$/)
  await expectTreeGone(tree)
  await omo.start()
  await expect.poll(() => status(omo)).toBe('starts=3 running=0 mode=two')
  await expectClean(omo, 'sample')
  // Changes made while disconnected must refresh the task list on reconnect.
  tree = await start(omo, n)
  await page.unroute('**/api/events*')
  await nudgeReconnect(page)
  await expect(page.locator('[data-connection]')).toHaveAttribute('data-state', 'live')
  await expect(task.locator('[data-task-stop]')).toBeEnabled()
  await task.locator('[data-task-stop]').click()
  await expectTreeGone(tree)
  await expectClean(omo, 'sample')
  await expect(page.getByRole('heading', { name: 'Updated sample route' })).toBeVisible()
  await omo.api('DELETE', '/api/plugins/sample')
  await expect(page.locator('script[data-omo-plugin="sample"]')).toHaveCount(0)
  await expect(page.locator('[data-plugin-panel^="sample:"]')).toHaveCount(0)
  expect((await page.request.get(omo.url('/'))).headers()['content-security-policy']).toBe(policy)
})

for (const boundary of ['cancel', 'cascade', 'disable', 'remove', 'shutdown', 'natural']) {
  test(`sample process tree and task-owned forward end together on ${boundary}`, async ({ omo }) => {
    const sample = await installSample(omo)
    const parent = await omo.createTicket({ type: 'story', title: 'Owner story' })
    const ticket = await omo.createTicket({ title: 'Owned task', story: parent })
    if (boundary === 'natural') {
      // The parent leaves an ordinary inherited-pipe child, then exits itself.
      // File gating is deterministic and confined to this test's temporary home.
      const gate = join(omo.home, 'exit-worker')
      const worker = readFileSync(join(REPO_DIR, 'web/e2e/fixtures/editor.cjs'), 'utf8')
      sample.repo.commit(null, { 'worker.cjs': worker + `\nsetInterval(()=>{if(require('node:fs').existsSync(${JSON.stringify(gate)}))process.exit(0)},10);` })
      await omo.api('POST', '/api/plugins/sample/update', {})
    }
    const tree = await start(omo, ticket)
    expect(await forwards(omo)).toHaveLength(1)
    if (boundary === 'cancel' || boundary === 'cascade') await omo.api('POST', `/api/tickets/${boundary === 'cascade' ? parent : ticket}/cancel`, { delete_branch: false })
    if (boundary === 'disable') await configure(omo, 'sample', { enabled: false })
    if (boundary === 'remove') await omo.api('DELETE', '/api/plugins/sample')
    if (boundary === 'shutdown') await omo.stop()
    if (boundary === 'natural') writeFileSync(join(omo.home, 'exit-worker'), '')
    await expectTreeGone(tree)
    if (boundary === 'shutdown') await omo.start()
    if (boundary === 'remove') {
      expect(await forwards(omo)).toEqual([])
      expect((await omo.api<{ plugins: { name: string }[] }>('GET', '/api/plugins')).plugins.some(p => p.name === 'sample')).toBe(false)
    } else await expectClean(omo, 'sample')
  })
}

test.describe('review completion', () => {
  test.use({ omoOptions: { seed: { projects: [{ path: '{{repo}}', name: 'demo' }], tickets: [{ ref: 'review', type: 'task', title: 'Reviewed task', projects: ['demo'], review: 'human', to: 'review', branch: 'test-reviewed' }] } } })
  test('normal Done transition reaps sample descendants and its owned forward', async ({ omo, repo }) => {
    git(repo, 'worktree', 'add', '-b', 'test-reviewed', join(omo.home, 'omo/worktrees/test-reviewed'), 'main')
    await installSample(omo)
    const n = omo.refs.review!
    const tree = await start(omo, n)
    await omo.api('POST', `/api/tickets/${n}/approve`, {})
    await expect.poll(async () => (await omo.api<{ column: string }>('GET', `/api/tickets/${n}`)).column).toBe('done')
    await expectClean(omo, 'sample')
    await expectTreeGone(tree)
    expect((await tasks(omo, 'sample'))[0]!.reason).toBe('ticket_done')
  })
})
