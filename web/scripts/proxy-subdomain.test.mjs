import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { test } from 'node:test'
import { chromium } from '@playwright/test'

// Exercise real BrowserHandler/registry/grants/cookie flags with Chromium.
// The opt-in Go fixture uses httptest only and never starts omo or a socket.
test('subdomain Open, domain cookies, HTTPS and path mode coexist in Chromium', { timeout: 90000 }, async () => {
  const fixture = spawn('go', ['test', './internal/server', '-run', '^TestProxySubdomainBrowserFixture$', '-count=1', '-v', '-timeout', '80s'], {
    cwd: new URL('../../', import.meta.url),
    env: {
      PATH: process.env.PATH, HOME: process.env.HOME,
      USERPROFILE: process.env.USERPROFILE, LOCALAPPDATA: process.env.LOCALAPPDATA,
      SystemRoot: process.env.SystemRoot, TEMP: process.env.TEMP,
      CGO_ENABLED: '0', PROXY_BROWSER_FIXTURE: '1',
    },
  })
  let output = ''
  fixture.stderr.on('data', data => { output += data })
  const fixtures = await new Promise((resolve, reject) => {
    fixture.stdout.on('data', data => {
      output += data
      const line = output.split('\n').find(line => line.startsWith('PROXY_BROWSER_FIXTURES '))
      if (line) resolve(JSON.parse(line.slice('PROXY_BROWSER_FIXTURES '.length)))
    })
    fixture.on('error', reject)
    fixture.on('exit', code => reject(new Error(`fixture exited ${code}: ${output}`)))
  })
  const exited = new Promise(resolve => fixture.once('exit', resolve))
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
    // This endpoint belongs to the httptest wrapper, never the omo API.
    await fetch(fixtures[0].stop)
  }
  assert.equal(await exited, 0, output)
})
