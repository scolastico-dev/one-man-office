import type { Page } from '@playwright/test'
import { openBoard } from './support/board'
import { expect, test } from './support/omo'

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

test('the ticket number in branch names is off, switches on and is saved with the page', async ({ page, omo }) => {
  await openGeneral(page, omo)
  const sw = page.getByRole('switch', { name: 'Put the ticket number into branch names' })
  await expect(sw).toHaveAttribute('aria-checked', 'false')
  await sw.click()
  await page.getByRole('region', { name: 'Save changes' }).getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByRole('region', { name: 'Save changes' })).toContainText('Saved')
  const settings = await omo.api<{ settings: { branches: { ticket_number: boolean } } }>('GET', '/api/settings')
  expect(settings.settings.branches.ticket_number).toBe(true)
})
