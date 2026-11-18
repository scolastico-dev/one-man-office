import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { createServer } from 'node:http'
import { test } from 'node:test'
import { chromium } from '@playwright/test'

// Standalone browser regression: no omo process, home, CLI, socket or agents.
// Exercise the exact embedded script in Chromium, including real session history.
test('proxy bootstrap clears fragment before exchange and replaces its history entry', async () => {
  const script = await readFile(new URL('../../internal/server/proxy_bootstrap.js', import.meta.url))
  const requests = []
  const grant = 'one-use-proxy-grant'
  let exchangeBody = ''
  let origin
  const server = createServer(async (request, response) => {
    requests.push({ url: request.url, referer: request.headers.referer ?? '' })
    response.setHeader('Referrer-Policy', 'no-referrer')
    response.setHeader('Content-Security-Policy', "default-src 'none'; script-src 'self'; connect-src 'self'")
    if (request.url === '/_omo/proxy/bootstrap.js') {
      response.setHeader('Content-Type', 'text/javascript')
      response.end(script)
    } else if (request.url === '/_omo/proxy/exchange') {
      for await (const chunk of request) exchangeBody += chunk
      response.setHeader('Content-Type', 'application/json')
      response.setHeader('Set-Cookie', 'omo_proxy_session=opaque; Path=/; HttpOnly; SameSite=Lax')
      response.end(JSON.stringify({ url: `${origin}/proxy/abc/start?q=1` }))
    } else if (request.url === '/_omo/proxy/bootstrap') {
      response.setHeader('Content-Type', 'text/html')
      response.end('<!doctype html><script src="/_omo/proxy/bootstrap.js" defer></script><p id="status">Opening</p><a id="board" href="/board">Back to board</a>')
    } else {
      response.setHeader('Content-Type', 'text/html')
      response.end('<!doctype html><p>Board or application</p>')
    }
  })
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  origin = `http://127.0.0.1:${server.address().port}`
  let browser
  try {
    browser = await chromium.launch({ headless: true })
    const page = await browser.newPage()
    await page.goto(`${origin}/board`)
    await page.route('**/_omo/proxy/exchange', async route => {
      assert.equal(new URL(page.url()).hash, '')
      assert.equal(await page.evaluate(() => location.hash), '')
      await route.continue()
    })
    await page.goto(`${origin}/_omo/proxy/bootstrap#${grant}`)
    await page.waitForURL(`${origin}/proxy/abc/start?q=1`)
    assert.deepEqual(JSON.parse(exchangeBody), { grant })
    assert.ok(requests.every(r => !r.url.includes(grant) && !r.referer.includes(grant)))
    assert.ok(requests.find(r => r.url === '/_omo/proxy/exchange').referer === '')
    assert.ok(!await page.evaluate(() => document.cookie.includes('omo_proxy_session')))
    await page.goBack()
    assert.equal(page.url(), `${origin}/board`)
    await page.goForward()
    assert.equal(page.url(), `${origin}/proxy/abc/start?q=1`)

    await page.goto(`${origin}/_omo/proxy/bootstrap`)
    await page.locator('#status').filter({ hasText: 'Open this Web UI again from the board.' }).waitFor()
    assert.equal(requests.filter(r => r.url === '/_omo/proxy/exchange').length, 1)
    await page.route('**/_omo/proxy/exchange', route => route.fulfill({ status: 401 }))
    await page.goto(`${origin}/board`)
    await page.goto(`${origin}/_omo/proxy/bootstrap#expired`)
    await page.locator('#status').filter({ hasText: 'This Web UI sign-in expired or is unavailable.' }).waitFor()
    assert.equal(new URL(page.url()).hash, '')
    assert.equal(await page.locator('#board').getAttribute('href'), '/board')
  } finally {
    await browser?.close()
    await new Promise(resolve => server.close(resolve))
  }
})
