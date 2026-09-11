'use strict';
const {test} = require('node:test');
const assert = require('node:assert/strict');
const {once} = require('node:events');
const fs = require('node:fs');
const http = require('node:http');
const net = require('node:net');
const os = require('node:os');
const path = require('node:path');
const {spawn, spawnSync} = require('node:child_process');

const assetRoot = path.join(__dirname, 'assets');
const capability = 'dashboard-regression-token';
const runningShell = {
  id: 'shell-1', path: '/tmp/office', mode: 'shell', state: 'running',
  started: '2026-09-12T00:00:00.000Z', agents: [], actions: [],
};

function findBrowser() {
  const candidates = [
    process.env.OMO_CHROME,
    ...['chromium', 'chromium-browser', 'google-chrome', 'google-chrome-stable'].map(name => {
      const result = spawnSync('which', [name], {encoding: 'utf8'});
      return result.status === 0 ? result.stdout.trim() : '';
    }),
  ].filter(Boolean);
  const playwrightRoot = path.join(os.homedir(), '.cache', 'ms-playwright');
  try {
    for (const browserDirectory of fs.readdirSync(playwrightRoot)) {
      const browserPath = path.join(playwrightRoot, browserDirectory, 'chrome-linux64', 'chrome');
      if (fs.existsSync(browserPath)) candidates.push(browserPath);
    }
  } catch {}
  return candidates.find(candidate => fs.existsSync(candidate));
}

function contentType(file) {
  return file.endsWith('.html') ? 'text/html; charset=utf-8'
    : file.endsWith('.css') ? 'text/css; charset=utf-8'
      : file.endsWith('.js') ? 'text/javascript; charset=utf-8'
        : file.endsWith('.png') ? 'image/png' : 'application/octet-stream';
}

function dashboardServer() {
  return http.createServer((request, response) => {
    const requestURL = new URL(request.url, 'http://127.0.0.1');
    if (requestURL.pathname === '/api/state' || requestURL.pathname === '/api/extensions') {
      if (request.headers.authorization !== `Bearer ${capability}`) {
        response.writeHead(401, {'Content-Type': 'text/plain; charset=utf-8'});
        response.end('missing dashboard capability');
        return;
      }
      response.writeHead(200, {'Content-Type': 'application/json; charset=utf-8', 'Cache-Control': 'no-store'});
      response.end(requestURL.pathname === '/api/state'
        ? JSON.stringify({projects: [], instances: [runningShell], agents: 0, max_agents: 2})
        : '[]');
      return;
    }
    if (requestURL.pathname.startsWith('/api/')) {
      response.writeHead(404);
      response.end();
      return;
    }
    const relative = requestURL.pathname === '/' ? 'index.html' : requestURL.pathname.slice('/assets/'.length);
    if (requestURL.pathname !== '/' && !requestURL.pathname.startsWith('/assets/')) {
      response.writeHead(404);
      response.end();
      return;
    }
    const file = path.resolve(assetRoot, relative);
    if (!file.startsWith(assetRoot + path.sep)) {
      response.writeHead(404);
      response.end();
      return;
    }
    try {
      response.writeHead(200, {'Content-Type': contentType(file)});
      response.end(fs.readFileSync(file));
    } catch {
      response.writeHead(404);
      response.end();
    }
  });
}

async function freePort() {
  const server = net.createServer();
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const port = server.address().port;
  await new Promise(resolve => server.close(resolve));
  return port;
}

async function browserConnection(chrome) {
  const port = await freePort();
  const userData = fs.mkdtempSync(path.join(os.tmpdir(), 'omo-dashboard-reload-'));
  const browser = spawn(chrome, [
    '--headless=new', '--no-sandbox', '--disable-gpu', '--disable-dev-shm-usage',
    `--remote-debugging-port=${port}`, `--user-data-dir=${userData}`, 'about:blank',
  ], {stdio: ['ignore', 'ignore', 'ignore']});
  const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
  let version;
  for (let attempt = 0; attempt < 100; attempt++) {
    try {
      version = await fetch(`http://127.0.0.1:${port}/json/version`).then(response => response.json());
      break;
    } catch {
      await sleep(50);
    }
  }
  if (!version) {
    browser.kill('SIGTERM');
    throw new Error('Chromium remote debugging endpoint did not start');
  }
  const targets = await fetch(`http://127.0.0.1:${port}/json/list`).then(response => response.json());
  const page = targets.find(target => target.type === 'page');
  const socket = new WebSocket(page.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.onopen = resolve; socket.onerror = reject; });
  let nextID = 0;
  const pending = new Map();
  socket.onmessage = event => {
    const message = JSON.parse(event.data);
    const request = pending.get(message.id);
    if (!request) return;
    pending.delete(message.id);
    message.error ? request.reject(new Error(message.error.message)) : request.resolve(message.result);
  };
  const call = (method, params = {}) => new Promise((resolve, reject) => {
    const id = ++nextID;
    pending.set(id, {resolve, reject});
    socket.send(JSON.stringify({id, method, params}));
  });
  const evaluate = async expression => {
    const result = await call('Runtime.evaluate', {expression, awaitPromise: true, returnByValue: true});
    if (result.exceptionDetails) throw new Error(result.exceptionDetails.exception?.description || result.exceptionDetails.text);
    return result.result?.value;
  };
  const close = async () => {
    socket.close();
    browser.kill('SIGTERM');
    await Promise.race([once(browser, 'exit'), new Promise(resolve => setTimeout(resolve, 1000))]);
    fs.rmSync(userData, {recursive: true, force: true, maxRetries: 5, retryDelay: 100});
  };
  return {call, evaluate, close};
}

test('dashboard reload preserves hit-testing while exposing token loss explicitly', async t => {
  const chrome = findBrowser();
  if (!chrome || typeof WebSocket !== 'function') {
    t.skip('Chromium and Node WebSocket runtime are required');
    return;
  }
  const server = dashboardServer();
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const address = server.address();
  const browser = await browserConnection(chrome);
  t.after(async () => {
    await browser.close();
    await new Promise(resolve => server.close(resolve));
  });
  const baseURL = `http://127.0.0.1:${address.port}/`;
  const waitFor = async expression => {
    for (let attempt = 0; attempt < 100; attempt++) {
      if (await browser.evaluate(expression)) return;
      await new Promise(resolve => setTimeout(resolve, 50));
    }
    throw new Error(`timed out waiting for ${expression}`);
  };

  await browser.call('Page.enable');
  await browser.call('Page.navigate', {url: baseURL + '#' + capability});
  await waitFor("document.querySelector('#instances .instance-entry')?.dataset.state === 'running'");
  const initial = await browser.evaluate(`(() => ({
    href: location.href,
    instanceState: document.querySelector('#instances .instance-entry')?.dataset.state,
    instanceCount: document.querySelectorAll('#instances .instance-entry').length,
  }))()`);
  assert.equal(initial.href, baseURL);
  assert.equal(initial.instanceState, 'running');
  assert.equal(initial.instanceCount, 1);

  await browser.call('Page.reload', {ignoreCache: true});
  await waitFor("document.querySelector('#notice')?.textContent.includes('Open the access URL printed by omo company.')");
  const state = await browser.evaluate(`(() => {
    const button = document.querySelector('#edit-projects');
    const rect = button.getBoundingClientRect();
    const hit = document.elementFromPoint(rect.left + rect.width / 2, rect.top + rect.height / 2);
    const activeAncestors = [];
    for (let node = document.activeElement; node; node = node.parentElement) if (node.inert || node.hasAttribute('inert')) activeAncestors.push(node.id || node.tagName);
    return {
      href: location.href,
      instanceCount: document.querySelectorAll('#instances .instance-entry').length,
      notice: document.querySelector('#notice').textContent,
      hit: {tag: hit?.tagName, id: hit?.id, pointerEvents: hit ? getComputedStyle(hit).pointerEvents : null},
      dialogs: [...document.querySelectorAll('dialog')].map(dialog => ({id: dialog.id, open: dialog.open, rect: dialog.getBoundingClientRect().toJSON()})),
      modalCount: (() => { try { return document.querySelectorAll(':modal').length; } catch { return 0; } })(),
      activeAncestors,
      beforePressed: button.getAttribute('aria-pressed'),
      point: {x: rect.left + rect.width / 2, y: rect.top + rect.height / 2},
    };
  })()`);
  assert.equal(state.href, baseURL);
  assert.equal(state.instanceCount, 0);
  assert.match(state.notice, /access key stays in this page/);
  assert.deepEqual(state.hit, {tag: 'BUTTON', id: 'edit-projects', pointerEvents: 'auto'});
  assert.ok(state.dialogs.every(dialog => !dialog.open));
  assert.equal(state.modalCount, 0);
  assert.deepEqual(state.activeAncestors, []);

  await browser.call('Input.dispatchMouseEvent', {type: 'mouseMoved', ...state.point});
  await browser.call('Input.dispatchMouseEvent', {type: 'mousePressed', ...state.point, button: 'left', clickCount: 1});
  await browser.call('Input.dispatchMouseEvent', {type: 'mouseReleased', ...state.point, button: 'left', clickCount: 1});
  assert.equal(await browser.evaluate("document.querySelector('#edit-projects').getAttribute('aria-pressed')"), 'true');
});
