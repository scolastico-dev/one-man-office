import assert from 'node:assert/strict'
import { test } from 'node:test'
import { chromium } from '@playwright/test'
import { fixtureEnv, startFixture } from './fixtureProcess.mjs'

// Exercise real BrowserHandler/registry/grants/cookie flags with Chromium.
// The opt-in Go fixture uses httptest only and never starts omo or a socket.
test('subdomain Open, domain cookies, HTTPS and path mode coexist in Chromium', { timeout: 90000 }, async () => {
  // The helper's environment is an allowlist that keeps the configured temporary roots (see fixtureProcess.mjs).
  const run = await startFixture({
    cwd: new URL('../../', import.meta.url),
    env: fixtureEnv(process.env, { PROXY_BROWSER_FIXTURE: '1' }),
  })
  const { fixtures } = run
  let exitCode
  let browser
  try {
    browser = await chromium.launch({ headless: true, args: ['--no-proxy-server'] })
    for (const f of fixtures) {
      const context = await browser.newContext({ ignoreHTTPSErrors: true })
      const page = await context.newPage()
      const requests = []
      page.on('request', r => requests.push(r.url()))
      await page.goto(f.board)
      const open = await page.evaluate(async ({ id, token }) => {
        const response = await fetch(`/api/proxy/forwards/${id}/open`, { method: 'POST', headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' }, body: '{}' })
        if (!response.ok) throw new Error(`Open ${response.status}`)
        return response.json()
      }, f)
      assert.equal(new URL(open.url).origin, f.forward)
      const grant = new URL(open.url).hash.slice(1)
      await page.goto(open.url)
      await page.waitForURL(`${f.forward}/start?q=hello%20world`)
      assert.equal(new URL(page.url()).hash, '')
      assert.ok(requests.every(url => !url.includes(grant) && !url.includes(f.token)))
      assert.ok(!await page.evaluate(() => document.cookie.includes('omo_proxy_')))
      const cookies = await context.cookies(f.forward2)
      assert.equal(cookies.find(c => c.name === 'omo_proxy_session')?.domain, '.apps.localhost')
      for (const name of ['omo_proxy_session', 'omo_proxy_forward']) {
        const cookie = cookies.find(c => c.name === name)
        assert.ok(cookie?.httpOnly)
        assert.equal(cookie.secure, f.variant === 'https')
      }
      await page.goto(`${f.forward2}/inspect?q=two`)
      const inspection = JSON.parse(await page.locator('body').innerText())
      assert.equal(inspection.path, '/inspect?q=two')
      assert.equal(inspection.cookie, '')
      assert.equal(inspection.authorization, '')
      assert.equal(inspection.host, new URL(f.forward2).host)
      await page.goto(`${f.forward}/redirect`)
      await page.waitForURL(`${f.forward}/final?q=1`)
      assert.equal((await context.cookies(f.forward)).find(c => c.name === 'app')?.value, 'yes')
      await page.goto(`${f.board}${f.base_path}inspect?path=works`)
      assert.equal(JSON.parse(await page.locator('body').innerText()).path, `${f.base_path}inspect?path=works`)
      // The same base host serves the board; mismatching labels are refused.
      const board = await page.goto(f.base)
      assert.equal(board.status(), 200)
      const wrong = new URL(f.forward)
      wrong.hostname = `wrong.${wrong.hostname}`
      const rejected = await page.goto(wrong.href)
      assert.equal(rejected.status(), 403)
      await context.close()
    }
  } finally {
    await browser?.close()
    // The stop address belongs to the httptest wrapper, never the omo API. Whatever failed above, the helper ends here.
    exitCode = await run.stop(fixtures[0]?.stop)
  }
  assert.equal(exitCode, 0, run.output())
})
