'use strict';
const {test} = require('node:test');
const assert = require('node:assert/strict');
const {once} = require('node:events');
const fs = require('node:fs');
const net = require('node:net');
const os = require('node:os');
const path = require('node:path');
const {spawn, spawnSync} = require('node:child_process');

function findBrowser() {
  const candidates = [
    process.env.OMO_CHROME,
    ...[
      'chromium', 'chromium-browser', 'chromium_headless_shell', 'chromium-headless-shell',
      'google-chrome', 'google-chrome-stable',
    ].map(name => {
      const result = spawnSync('which', [name], {encoding: 'utf8'});
      return result.status === 0 ? result.stdout.trim() : '';
    }),
  ].filter(Boolean);
  const playwrightRoot = path.join(os.homedir(), '.cache', 'ms-playwright');
  try {
    for (const browserDirectory of fs.readdirSync(playwrightRoot)) {
      for (const relative of ['chrome-linux64/chrome', 'chrome-headless-shell-linux64/chrome-headless-shell']) {
        const browserPath = path.join(playwrightRoot, browserDirectory, relative);
        if (fs.existsSync(browserPath)) candidates.push(browserPath);
      }
    }
  } catch {}
  return candidates.find(candidate => fs.existsSync(candidate));
}

async function freePort() {
  const server = net.createServer();
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const port = server.address().port;
  await new Promise(resolve => server.close(resolve));
  return port;
}

async function browserConnection(chrome, auth) {
  const port = await freePort();
  const userData = fs.mkdtempSync(path.join(os.tmpdir(), 'omo-dashboard-reload-'));
  const args = [
    '--headless=new', '--no-sandbox', '--disable-gpu', '--disable-dev-shm-usage',
    `--remote-debugging-port=${port}`, `--user-data-dir=${userData}`, 'about:blank',
  ];
  let browser;
  let socket;
  const cleanup = async () => {
    socket?.close();
    if (browser && browser.exitCode === null) browser.kill('SIGTERM');
    if (browser && browser.exitCode === null) {
      await Promise.race([once(browser, 'exit'), new Promise(resolve => setTimeout(resolve, 1000))]);
    }
    fs.rmSync(userData, {recursive: true, force: true, maxRetries: 5, retryDelay: 100});
  };
  try {
    browser = spawn(chrome, args, {stdio: ['ignore', 'ignore', 'ignore']});
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
    if (!version) throw new Error('Chromium remote debugging endpoint did not start');
    console.log(`browser version: ${version.Browser}`);
    console.log(`browser launch: ${chrome} ${args.join(' ')}`);
    const targets = await fetch(`http://127.0.0.1:${port}/json/list`).then(response => response.json());
    const page = targets.find(target => target.type === 'page');
    if (!page) throw new Error('Chromium did not expose an initial page target');
    socket = new WebSocket(page.webSocketDebuggerUrl);
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
    await call('Network.enable');
    await call('Network.setExtraHTTPHeaders', {headers: {Authorization: `Basic ${Buffer.from(auth).toString('base64')}`}});
    await call('Page.enable');
    return {call, evaluate, close: cleanup};
  } catch (error) {
    await cleanup();
    throw error;
  }
}

const captureScript = String.raw`(() => {
  const NativeWebSocket = window.WebSocket;
  const frames = [], sockets = [], terms = [];
  window.__omoWSFrames = frames;
  window.__omoSockets = sockets;
  window.__omoTerms = terms;
  window.__omoWSFrameBytes = 0;
  const hex = bytes => [...new Uint8Array(bytes)].slice(0, 512).map(value => value.toString(16).padStart(2, '0')).join('');
  class CapturedWebSocket {
    static CONNECTING = NativeWebSocket.CONNECTING;
    static OPEN = NativeWebSocket.OPEN;
    static CLOSING = NativeWebSocket.CLOSING;
    static CLOSED = NativeWebSocket.CLOSED;
    constructor(...args) {
      this._socket = new NativeWebSocket(...args);
      sockets.push(this);
      this._socket.addEventListener('message', event => {
        if (event.data instanceof ArrayBuffer) {
          window.__omoWSFrameBytes += event.data.byteLength;
          frames.push({length: event.data.byteLength, hex: hex(event.data), firstBytes: [...new Uint8Array(event.data).slice(0, 32)]});
        }
      });
    }
    get url() { return this._socket.url; }
    get readyState() { return this._socket.readyState; }
    get bufferedAmount() { return this._socket.bufferedAmount; }
    get extensions() { return this._socket.extensions; }
    get protocol() { return this._socket.protocol; }
    get binaryType() { return this._socket.binaryType; }
    set binaryType(value) { this._socket.binaryType = value; }
    set onopen(value) { this._socket.onopen = value; }
    get onopen() { return this._socket.onopen; }
    set onmessage(value) { this._socket.onmessage = value; }
    get onmessage() { return this._socket.onmessage; }
    set onerror(value) { this._socket.onerror = value; }
    get onerror() { return this._socket.onerror; }
    set onclose(value) { this._socket.onclose = value; }
    get onclose() { return this._socket.onclose; }
    send(...args) { return this._socket.send(...args); }
    close(...args) { return this._socket.close(...args); }
    addEventListener(...args) { return this._socket.addEventListener(...args); }
    removeEventListener(...args) { return this._socket.removeEventListener(...args); }
  }
  window.WebSocket = CapturedWebSocket;
  let terminalCtor;
  Object.defineProperty(window, 'Terminal', {configurable: true, get() { return terminalCtor; }, set(value) {
    if (!value || value.__omoWrapped) { terminalCtor = value; return; }
    class CapturedTerminal extends value {
      constructor(...args) { super(...args); terms.push(this); }
    }
    Object.defineProperty(CapturedTerminal, '__omoWrapped', {value: true});
    terminalCtor = CapturedTerminal;
  }});
})();`;

function snapshotExpression() {
  return `(() => {
    const rect = element => { if (!element) return null; const bounds = element.getBoundingClientRect(), style = getComputedStyle(element); return {tag: element.tagName, id: element.id, class: String(element.className || ''), hidden: element.hidden, open: element.open ?? null, display: style.display, pointerEvents: style.pointerEvents, position: style.position, zIndex: style.zIndex, cursor: style.cursor, rect: {x: bounds.x, y: bounds.y, width: bounds.width, height: bounds.height}}; };
    const point = (name, element) => { const bounds = element?.getBoundingClientRect(); if (!bounds) return {name, target: null, hit: null}; const x = bounds.left + bounds.width / 2, y = bounds.top + bounds.height / 2, hit = document.elementFromPoint(x, y); return {name, target: rect(element), point: {x, y}, inside: Boolean(hit && element.contains(hit)), hit: rect(hit)}; };
    const inertChain = []; for (let node = document.activeElement; node; node = node.parentElement) if (node.inert || node.hasAttribute('inert')) inertChain.push(node.id || node.tagName);
    const button = document.getElementById('edit-projects');
    const modeCodes = [1000, 1002, 1003, 1004, 1006, 1049, 2004];
    const frames = window.__omoWSFrames || [];
    const firstFrame = frames[0];
    const encode = value => [...new TextEncoder().encode(value)].map(byte => byte.toString(16).padStart(2, '0')).join('');
    const modeSequences = Object.fromEntries(modeCodes.map(code => [String(code), {on: firstFrame?.hex?.includes(encode('\\x1b[?' + code + 'h')) || false, off: firstFrame?.hex?.includes(encode('\\x1b[?' + code + 'l')) || false}]));
    let modalCount = 0; try { modalCount = document.querySelectorAll(':modal').length; } catch {}
    return {
      viewport: {width: innerWidth, height: innerHeight},
      points: [point('toolbar', document.getElementById('supervisor-toolbar')), point('offices', document.getElementById('supervisor-sidebar')), point('terminal', document.querySelector('.terminal-panel')), point('footer', document.querySelector('footer'))],
      dialogs: [...document.querySelectorAll('dialog')].map(rect), modalCount, inertChain, active: rect(document.activeElement),
      pluginNodes: [...document.querySelectorAll('#filebrowser-overlay, .filebrowser-panel, .filebrowser-hint, #filebrowser-button')].map(rect),
      terminalNodes: [...document.querySelectorAll('#terminals, #terminals > .terminal, #terminals .xterm')].map(rect),
      edit: {rect: rect(button), pressed: button?.getAttribute('aria-pressed')},
      terminalModes: (window.__omoTerms || []).map(term => ({modes: term.modes, privateModes: term._core?.coreService?.decPrivateModes, mouseProtocol: term._core?.coreMouseService?.activeProtocol})),
      frames, frameBytes: window.__omoWSFrameBytes || 0, modeSequences,
    };
  })()`;
}

test('actual company reload/reconnect keeps controls clickable for current and stale plugins', async t => {
  const pageURL = process.env.OMO_BROWSER_URL;
  const auth = process.env.OMO_BROWSER_AUTH;
  const variant = process.env.OMO_BROWSER_VARIANT || 'direct';
  const chrome = findBrowser();
  if (!pageURL || !auth) {
    t.skip('integration URL and BasicAuth credentials are supplied by the Go company test');
    return;
  }
  if (!chrome || typeof WebSocket !== 'function') {
    t.skip('Chromium and Node WebSocket runtime are required');
    return;
  }
  const browser = await browserConnection(chrome, auth);
  t.after(() => browser.close());
  await browser.call('Page.addScriptToEvaluateOnNewDocument', {source: captureScript});
  const waitFor = async expression => {
    for (let attempt = 0; attempt < 200; attempt++) {
      if (await browser.evaluate(expression)) return;
      await new Promise(resolve => setTimeout(resolve, 50));
    }
    throw new Error(`timed out waiting for ${expression}`);
  };
  const selectRunningInstance = async () => {
    await waitFor("document.querySelector('#instances .instance-entry[data-state=running]')");
    await browser.evaluate("document.querySelector('#instances .instance-entry[data-state=running]').click()");
    await waitFor("document.querySelector('#terminals .xterm') && window.__omoTerms?.length && window.__omoSockets?.length");
    await waitFor("document.querySelector('#filebrowser-button') && !document.querySelector('#filebrowser-button').disabled");
    await new Promise(resolve => setTimeout(resolve, 250));
  };
  const clickEdit = async snapshot => {
    assert.ok(snapshot.edit.rect?.rect.width > 0 && snapshot.edit.rect.rect.height > 0, `${variant}: Edit has no geometry`);
    assert.equal(snapshot.edit.pressed, 'false');
    const point = snapshot.edit.rect.rect;
    const x = point.x + point.width / 2, y = point.y + point.height / 2;
    await browser.call('Input.dispatchMouseEvent', {type: 'mouseMoved', x, y});
    await browser.call('Input.dispatchMouseEvent', {type: 'mousePressed', x, y, button: 'left', clickCount: 1});
    await browser.call('Input.dispatchMouseEvent', {type: 'mouseReleased', x, y, button: 'left', clickCount: 1});
    assert.equal(await browser.evaluate("document.querySelector('#edit-projects').getAttribute('aria-pressed')"), 'true');
  };
  const assertSnapshot = snapshot => {
    for (const point of snapshot.points) {
      assert.ok(point.target?.rect.width > 0 && point.target?.rect.height > 0, `${variant}: ${point.name} has no geometry`);
      assert.equal(point.inside, true, `${variant}: ${point.name} was intercepted by ${point.hit?.id || point.hit?.class || point.hit?.tag}`);
      assert.equal(point.hit?.pointerEvents, 'auto', `${variant}: ${point.name} hit has pointer-events ${point.hit?.pointerEvents}`);
      assert.ok(point.hit?.rect.width > 0 && point.hit?.rect.height > 0, `${variant}: ${point.name} hit has no geometry`);
      for (const property of ['tag', 'id', 'cursor', 'pointerEvents', 'position', 'zIndex', 'rect']) assert.ok(point.hit?.[property] !== undefined, `${variant}: ${point.name} hit omitted ${property}`);
    }
    assert.equal(snapshot.points.find(point => point.name === 'toolbar').hit.tag, 'BUTTON');
    assert.match(snapshot.points.find(point => point.name === 'terminal').hit.class, /xterm/);
    assert.ok(snapshot.dialogs.every(dialog => !dialog.open), `${variant}: unexpected open dialog`);
    assert.equal(snapshot.modalCount, 0, `${variant}: unexpected modal dialog`);
    assert.deepEqual(snapshot.inertChain, [], `${variant}: active element is inside inert content`);
    assert.ok(snapshot.active, `${variant}: active element was not recorded`);
    assert.ok(snapshot.terminalNodes.some(node => node.class.includes('xterm') && node.rect.width > 0 && node.rect.height > 0), `${variant}: xterm geometry missing`);
    assert.ok(snapshot.pluginNodes.some(node => node.id === 'filebrowser-button'), `${variant}: filebrowser did not load`);
    const overlay = snapshot.pluginNodes.find(node => node.id === 'filebrowser-overlay');
    if (overlay) assert.equal(overlay.open, false, `${variant}: filebrowser overlay remained open`);
    if (variant === 'stale') {
      const panel = snapshot.pluginNodes.find(node => node.id === 'filebrowser-panel');
      assert.ok(panel && panel.rect.width > 0 && panel.rect.width < snapshot.viewport.width, 'stale filebrowser panel geometry was not observed');
    } else {
      assert.equal(snapshot.pluginNodes.some(node => node.id === 'filebrowser-panel'), false, 'current filebrowser unexpectedly rendered the stale panel');
    }
  };

  await browser.call('Page.navigate', {url: pageURL + '#capability-fragment'});
  await selectRunningInstance();
  const initial = await browser.evaluate(snapshotExpression());
  assertSnapshot(initial);
  assert.equal(initial.terminalModes[0].modes.bracketedPasteMode, true, `${variant}: startup replay did not enable bracketed paste`);
  assert.equal(String(initial.terminalModes[0].mouseProtocol).toUpperCase(), 'DRAG', `${variant}: startup replay did not enable drag mouse mode`);
  assert.ok(initial.modeSequences['1002'].on && initial.modeSequences['1006'].on && initial.modeSequences['1004'].on && initial.modeSequences['1049'].on && initial.modeSequences['2004'].on, `${variant}: startup frame omitted expected terminal mode bytes`);
  await browser.evaluate("window.__omoSockets[0].send(new TextEncoder().encode('OMO_BROWSER_REPLAY'))");
  await waitFor('window.__omoWSFrameBytes > 262144');
  await clickEdit(initial);

  const snapshots = [];
  for (let iteration = 1; iteration <= 3; iteration++) {
    await browser.call('Page.reload', {ignoreCache: true});
    await selectRunningInstance();
    const snapshot = await browser.evaluate(snapshotExpression());
    assertSnapshot(snapshot);
    snapshots.push(snapshot);
    await clickEdit(snapshot);
    assert.equal(snapshot.terminalModes[0].modes.bracketedPasteMode, false, `${variant}: reconnect unexpectedly enabled bracketed paste`);
    assert.equal(String(snapshot.terminalModes[0].mouseProtocol).toUpperCase(), 'NONE', `${variant}: reconnect unexpectedly enabled mouse tracking`);
    assert.equal(snapshot.terminalModes[0].modes.sendFocusMode, false, `${variant}: reconnect unexpectedly enabled focus reporting`);
    assert.ok(snapshot.frames[0]?.length >= 256 * 1024, `${variant}: reconnect did not receive the retained replay tail`);
    for (const code of ['1000', '1002', '1003', '1004', '1006', '1049', '2004']) assert.equal(snapshot.modeSequences[code].on, false, `${variant}: retained tail unexpectedly contained ?${code}h`);
  }
  console.log(JSON.stringify({variant, initial: {frame: initial.frames[0], modes: initial.terminalModes, modeSequences: initial.modeSequences}, reconnects: snapshots.map(snapshot => ({frame: snapshot.frames[0], modes: snapshot.terminalModes, modeSequences: snapshot.modeSequences, points: snapshot.points, dialogs: snapshot.dialogs, modalCount: snapshot.modalCount, inertChain: snapshot.inertChain, active: snapshot.active, pluginNodes: snapshot.pluginNodes, terminalNodes: snapshot.terminalNodes}))}, null, 2));
});
