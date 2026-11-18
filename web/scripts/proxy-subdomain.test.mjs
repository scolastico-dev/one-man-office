import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { test } from 'node:test'
import { chromium } from '@playwright/test'

// Exercise real BrowserHandler/registry/grants/cookie flags with Chromium.
// The opt-in Go fixture uses httptest only and never starts omo or a socket.
test('clicked subdomain Open crosses sites, clears grants and supports app WebSockets in Chromium', { timeout: 90000 }, async () => {
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
      const bootstrapRequests = []
      const exchangeHashes = []
      context.on('request', r => {
        requests.push(r.url())
        if (new URL(r.url()).pathname === '/_omo/proxy/bootstrap') bootstrapRequests.push(r)
        if (new URL(r.url()).pathname === '/_omo/proxy/exchange') exchangeHashes.push(new URL(r.frame().page().url()).hash)
      })
      await page.goto(f.board)
      // Match the board's actual user gesture: reserve an about:blank tab,
      // authenticate Open asynchronously, then navigate the reserved tab.
      // page.goto(open.url) would suppress the initiator's cross-site metadata.
      await page.evaluate(({ id, token }) => {
        const button = document.createElement('button')
        button.textContent = 'Open generic app'
        button.onclick = async () => {
          const tab = window.open('about:blank', '_blank')
          tab.opener = null
          const response = await fetch(`/api/proxy/forwards/${id}/open`, { method: 'POST', headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' }, body: '{}' })
          if (!response.ok) throw new Error(`Open ${response.status}`)
          const open = await response.json()
          window.fixtureOpenURL = open.url
          tab.location.replace(open.url)
        }
        document.body.append(button)
      }, f)
      const popupPromise = context.waitForEvent('page')
      await page.getByRole('button', { name: 'Open generic app' }).click()
      const app = await popupPromise
      await app.waitForURL(`${f.forward}/start?q=hello%20world`, { timeout: 10000 })
      const open = { url: await page.evaluate(() => window.fixtureOpenURL) }
      assert.equal(new URL(open.url).origin, f.forward)
      const grant = new URL(open.url).hash.slice(1)
      assert.equal(bootstrapRequests.length, 1)
      const navigationHeaders = await bootstrapRequests[0].allHeaders()
      assert.equal(navigationHeaders['sec-fetch-mode'], 'navigate')
      assert.equal(navigationHeaders['sec-fetch-dest'], 'document')
      assert.equal(navigationHeaders.origin, undefined)
      if (f.variant === 'http' || f.variant === 'https') assert.equal(navigationHeaders['sec-fetch-site'], 'cross-site')
      assert.deepEqual(exchangeHashes, [''])
      assert.equal(new URL(app.url()).hash, '')
      assert.equal(await app.locator('h1').innerText(), 'Application')
      assert.ok(requests.every(url => !url.includes(grant) && !url.includes(f.token)))
      assert.ok(!await app.evaluate(() => document.cookie.includes('omo_proxy_')))
      assert.equal(await app.evaluate(() => new Promise((resolve, reject) => {
        const url = new URL('/socket', location.href)
        url.protocol = location.protocol === 'https:' ? 'wss:' : 'ws:'
        const socket = new WebSocket(url)
        socket.onopen = () => socket.send('generic app echo')
        socket.onmessage = event => { socket.close(); resolve(event.data) }
        socket.onerror = () => reject(new Error('generic app WebSocket failed'))
      })), 'generic app echo')
      const replay = await app.evaluate(async grant => (await fetch('/_omo/proxy/exchange', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ grant }) })).status, grant)
      assert.equal(replay, 401)
      // Both replace calls remove the bootstrap/grant from popup history.
      assert.equal(await app.goBack(), null)
      assert.equal(new URL(app.url()).hash, '')
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
