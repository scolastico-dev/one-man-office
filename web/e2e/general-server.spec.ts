import { execFileSync } from 'node:child_process'
import { devNull } from 'node:os'
import type { Page } from '@playwright/test'
import { expectNoAgentProcesses, script, waitForSession } from './support/agents'
import { openBoard } from './support/board'
import { expect, test } from './support/omo'

function git(repo: string, ...args: string[]): string {
  return execFileSync('git', ['-C', repo, ...args], {
    encoding: 'utf8',
    env: { ...process.env, GIT_CONFIG_GLOBAL: devNull, GIT_CONFIG_SYSTEM: devNull },
  })
}

const facts = (page: Page) => page.locator('[data-server-facts]')

async function openGeneral(page: Page, omo: Parameters<typeof openBoard>[1]) {
  await openBoard(page, omo)
  await page.goto(omo.url('/settings/general'))
  await expect(facts(page)).toContainText('Home folder')
}

test('the version links to its commit or release on GitHub, or is plain text', async ({ page, omo }) => {
  const info = await omo.api<{ version: string; version_url: string; version_commit: string }>('GET', '/api/server')
  await openGeneral(page, omo)
  await expect(facts(page)).toContainText(info.version)
  const link = facts(page).getByRole('link')
  if (info.version_url === '') {
    await expect(link).toHaveCount(0)
    return
  }
  expect(info.version_url).toMatch(/^https:\/\/github\.com\/scolastico-dev\/one-man-office\/(commit|releases\/tag)\//)
  await expect(link).toHaveAttribute('href', info.version_url)
  await expect(link).toHaveAttribute('target', '_blank')
  await expect(link).toHaveAttribute('rel', 'noopener noreferrer')
  await expect(link).toHaveText(info.version)
  const what = info.version_commit === '' ? 'release' : `commit ${info.version_commit}`
  await expect(link).toHaveAccessibleName(`omo ${info.version}, ${what} on GitHub`)
})

test('the address names the option that sets it and the address omo listens on', async ({ page, omo }) => {
  await openGeneral(page, omo)
  const help = page.locator('[data-address-help]')
  await expect(help).toHaveText(`Set with omo config set public_url <address>; omo keeps listening on 127.0.0.1:${omo.port}.`)

  // An option equal to the printed address starts like the default and is reported as set.
  const advertised = `http://omo.localhost:${omo.port}/`
  const set = await omo.cli(['config', 'set', 'public_url', advertised])
  expect(set.code, set.stderr).toBe(0)
  await omo.stop()
  await omo.start()
  expect(omo.output()).toContain(`omo: ${advertised}#${omo.token}`)
  await openGeneral(page, omo)
  await expect(facts(page)).toContainText(advertised)
  await expect(help).toHaveText(`From the public_url option; omo keeps listening on 127.0.0.1:${omo.port}.`)
  const info = await omo.api<{ address: string; address_configured: boolean }>('GET', '/api/server')
  expect(info).toMatchObject({ address: advertised, address_configured: true })
})

const saveBar = (page: Page) => page.getByRole('region', { name: 'Save changes' })

test('the branch template is a text field with an example from the server, refused when unusable, saved with the page', async ({ page, omo }) => {
  await openGeneral(page, omo)
  const field = page.getByRole('textbox', { name: 'Branch name template' })
  const preview = page.locator('[data-branch-preview]')
  await expect(field).toHaveValue('omo/{slug}')
  await expect(preview).toHaveText('Example: omo/fix-the-login for #42 "Fix the login", a task')

  await field.fill('main')
  await expect(page.locator('[data-branch-preview-error]')).toContainText('must contain {slug} or {number}')
  await saveBar(page).getByRole('button', { name: 'Save', exact: true }).click()
  await expect(field).toHaveAttribute('aria-invalid', 'true')
  await expect(page.locator('[data-branches-section]')).toContainText('must contain {slug} or {number}')

  await field.fill('feature/{number}-{slug}')
  await expect(preview).toHaveText('Example: feature/42-fix-the-login for #42 "Fix the login", a task')
  await saveBar(page).getByRole('button', { name: 'Save', exact: true }).click()
  await expect(saveBar(page)).toContainText('Saved')
  const settings = await omo.api<{ settings: { branches: { template: string } } }>('GET', '/api/settings')
  expect(settings.settings.branches).toEqual({ template: 'feature/{number}-{slug}' })
})

test.describe('a developer on a branch named by the template', () => {
  test.use({ omoOptions: { agents: { developer: script('ready', 'write|hello.txt|hi', 'commit|feat: hello', 'hang') } } })

  test('a ticket started after the template was saved gets a branch of that name', async ({ page, omo, repo }) => {
    await openGeneral(page, omo)
    await page.getByRole('textbox', { name: 'Branch name template' }).fill('{type}/{number}-{slug}')
    await expect(page.locator('[data-branch-preview]')).toHaveText('Example: task/42-fix-the-login for #42 "Fix the login", a task')
    await saveBar(page).getByRole('button', { name: 'Save', exact: true }).click()
    await expect(saveBar(page)).toContainText('Saved')

    const n = await omo.createTicket({ title: 'Say hello' })
    await omo.moveTicket(n, 'ready')
    await waitForSession(omo, n, 'developer')
    const ticket = await omo.api<{ projects: { branch: string }[] }>('GET', `/api/tickets/${n}`)
    expect(ticket.projects[0]!.branch).toBe(`task/${n}-say-hello`)
    await expect.poll(() => git(repo, 'log', '--format=%s', `task/${n}-say-hello`)).toContain('feat: hello')
    await expectNoAgentProcesses(omo)
  })
})
