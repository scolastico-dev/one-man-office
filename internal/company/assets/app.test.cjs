'use strict';
const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const {spawnSync} = require('node:child_process');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, 'app.js'), 'utf8');

function element(document, tagName = 'div') {
  const listeners = new Map();
  const node = {
    tagName: tagName.toUpperCase(),
    children: [],
    parentNode: null,
    ownerDocument: document,
    dataset: {},
    style: {},
    hidden: false,
    disabled: false,
    isConnected: true,
    tabIndex: 0,
    append(...nodes) {
      for (const child of nodes) {
        child.parentNode = this;
        this.children.push(child);
        if (child.tagName === 'SCRIPT' && typeof child.onload === 'function') child.onload();
      }
    },
    insertBefore(child, before) {
      const index = before ? this.children.indexOf(before) : -1;
      child.parentNode = this;
      if (index < 0) this.children.push(child);
      else this.children.splice(index, 0, child);
    },
    querySelectorAll(selector) {
      const descendants = [];
      const tags = selector.split(',').map(part => part.trim().toUpperCase());
      const visit = parent => {
        for (const child of parent.children) {
          if (selector === '*' || tags.includes(child.tagName)) descendants.push(child);
          visit(child);
        }
      };
      visit(this);
      return descendants;
    },
    addEventListener(type, listener) {
      const callbacks = listeners.get(type) || [];
      callbacks.push(listener);
      listeners.set(type, callbacks);
    },
    removeEventListener(type, listener) {
      listeners.set(type, (listeners.get(type) || []).filter(callback => callback !== listener));
    },
    dispatchEvent(event) {
      event.target ||= this;
      event.currentTarget = this;
      for (const listener of listeners.get(event.type) || []) listener(event);
      return !event.defaultPrevented;
    },
    click() {
      this.focus();
      if (typeof this.onclick === 'function') this.onclick({target: this, currentTarget: this});
      this.dispatchEvent({type: 'click', target: this});
    },
    focus() { if (!this.disabled) document.activeElement = this; },
    setAttribute(name, value) { this[name] = String(value); },
    getAttribute(name) { return this[name] === undefined ? null : this[name]; },
    removeAttribute(name) { delete this[name]; },
    remove() {
      this.isConnected = false;
      if (this.parentNode) this.parentNode.children = this.parentNode.children.filter(child => child !== this);
    },
    showModal() { this.open = true; },
    close() { this.open = false; this.dispatchEvent({type: 'close'}); },
    matches() { return true; },
  };
  Object.defineProperty(node, 'firstElementChild', {get: () => node.children[0] || null});
  Object.defineProperty(node, 'lastElementChild', {get: () => node.children[node.children.length - 1] || null});
  return node;
}

function loadAPI({fetchImpl, FormDataImpl, locationHash = ''} = {}) {
  const nodes = new Map();
  const document = {
    activeElement: null,
    head: null,
    createElement: tagName => element(document, tagName),
    getElementById(id) {
      if (!nodes.has(id)) nodes.set(id, element(document));
      return nodes.get(id);
    },
  };
  document.head = element(document, 'head');
  const register = (id, tagName = 'div') => {
    const node = element(document, tagName);
    nodes.set(id, node);
    return node;
  };
  const dialog = register('dialog', 'dialog');
  dialog.open = false;
  const dialogMessage = register('dialog-message');
  const dialogInput = register('dialog-input', 'input');
  const dialogCancel = register('dialog-cancel', 'button');
  const dialogConfirm = register('dialog-confirm', 'button');
  dialog.append(dialogMessage, dialogInput, dialogCancel, dialogConfirm);
  const listeners = new Map();
  const window = {
    addEventListener(type, listener) {
      const callbacks = listeners.get(type) || [];
      callbacks.push(listener);
      listeners.set(type, callbacks);
    },
    removeEventListener(type, listener) {
      listeners.set(type, (listeners.get(type) || []).filter(callback => callback !== listener));
    },
    dispatchEvent(event) {
      for (const listener of listeners.get(event.type) || []) listener(event);
    },
  };
  class CustomEvent {
    constructor(type, init = {}) {
      this.type = type;
      this.detail = init.detail;
    }
  }
  const calls = [];
  const context = {
    CustomEvent,
    document,
    fetch: async (...args) => {
      calls.push(args);
      return fetchImpl ? fetchImpl(...args) : {
        ok: true,
        status: 200,
        json: async () => args[0].endsWith('/api/extensions') ? [] : {projects: [], instances: [], agents: 0, max_agents: 0},
      };
    },
    FormData: FormDataImpl,
    Blob,
    Uint8Array,
    TextEncoder,
    TextDecoder,
    history: {replaceState() {}},
    location: {hash: locationHash, pathname: '/'},
    ResizeObserver: class { observe() {} },
    setInterval() {},
    window,
  };
  vm.runInNewContext(source, context);
  return {api: window.omo, CustomEvent, document, window, calls};
}

function keyboard(target, key, options = {}) {
  let prevented = false;
  target.dispatchEvent({
    type: 'keydown', key, shiftKey: options.shiftKey || false,
    preventDefault() { prevented = true; },
  });
  return prevented;
}

function commandResponse() {
  const lines = [
    JSON.stringify({type: 'output', stream: 'stdout', data: 'result'}),
    JSON.stringify({type: 'exit', code: 0}),
  ];
  let index = 0;
  return {
    ok: true,
    status: 200,
    body: {getReader: () => ({read: async () => index < lines.length ? {value: new TextEncoder().encode(lines[index++] + '\n'), done: false} : {value: undefined, done: true}})},
  };
}

class CapturedFormData {
  constructor() { this.fields = []; }
  append(name, value, filename) { this.fields.push({name, value, filename}); }
}

class TestFile extends Blob {
  constructor(parts, name, options) { super(parts, options); this.name = name; }
}

test('execute sends string stdin as ordered multipart fields without a content type header', async () => {
  const {api, calls} = loadAPI({locationHash: '#secret', FormDataImpl: CapturedFormData, fetchImpl: async url => url.endsWith('/api/commands') ? commandResponse() : {ok: true, status: 200, json: async () => []}});
  const signal = {aborted: false};
  const output = [];

  await api.execute('cat', ['--raw'], {cwd: 'office', stdin: 'hello', signal, onOutput: event => output.push(event)});

  const [url, options] = calls.find(([calledURL]) => calledURL.endsWith('/api/commands'));
  assert.equal(url, '/api/commands');
  assert.equal(options.headers['Content-Type'], undefined);
  assert.equal(options.headers.Authorization, 'Bearer secret');
  assert.equal(options.signal, signal);
  assert.equal(options.cache, 'no-store');
  assert.deepEqual(options.body.fields.map(field => field.name), ['request', 'stdin']);
  assert.equal(await options.body.fields[0].value.text(), JSON.stringify({cwd: 'office', command: 'cat', args: ['--raw']}));
  assert.equal(options.body.fields[0].value.type, 'application/json');
  assert.equal(options.body.fields[1].value, 'hello');
  assert.equal(output.length, 1);
  assert.equal(output[0].type, 'output');
  assert.equal(output[0].stream, 'stdout');
  assert.equal(output[0].data, 'result');
});

test('execute accepts Uint8Array, Blob, and File stdin', async () => {
  for (const stdin of [new Uint8Array([0, 1, 255]), new Blob(['blob input'], {type: 'text/plain'}), new TestFile(['file input'], 'input.txt')]) {
    const {api, calls} = loadAPI({FormDataImpl: CapturedFormData, fetchImpl: async url => url.endsWith('/api/commands') ? commandResponse() : {ok: true, status: 200, json: async () => []}});
    await api.execute('cat', [], {stdin});
    const [, options] = calls.find(([calledURL]) => calledURL.endsWith('/api/commands'));
    const value = options.body.fields[1].value;
    if (stdin instanceof Uint8Array) assert.deepEqual([...new Uint8Array(await value.arrayBuffer())], [...stdin]);
    else assert.equal(value, stdin);
  }
});

test('execute rejects unsupported stdin before making a command request', async () => {
  const {api, calls} = loadAPI({fetchImpl: async url => url.endsWith('/api/commands') ? commandResponse() : {ok: true, status: 200, json: async () => []}});

  await assert.rejects(api.execute('cat', [], {stdin: 42}), {name: 'TypeError'});
  assert.equal(calls.some(([url]) => url.endsWith('/api/commands')), false);
});

test('execute without stdin retains the JSON request and content type', async () => {
  const signal = {aborted: false};
  const {api, calls} = loadAPI({locationHash: '#secret', fetchImpl: async url => url.endsWith('/api/commands') ? commandResponse() : {ok: true, status: 200, json: async () => []}});

  await api.execute('pwd', [], {cwd: 'home', signal});

  const [url, options] = calls.find(([calledURL]) => calledURL.endsWith('/api/commands'));
  assert.equal(url, '/api/commands');
  assert.equal(options.headers['Content-Type'], 'application/json');
  assert.equal(options.headers.Authorization, 'Bearer secret');
  assert.equal(options.body, JSON.stringify({cwd: 'home', command: 'pwd', args: []}));
  assert.equal(options.signal, signal);
  assert.equal(options.cache, 'no-store');
});

test('onLoad delivers matching company-load events to the named plugin', () => {
  const {api, CustomEvent, window} = loadAPI();
  let received;
  api.onLoad('report-dashboard', event => { received = event; });
  const event = new CustomEvent('omo:company_load', {detail: {plugin: 'report-dashboard'}});

  window.dispatchEvent(event);

  assert.equal(received, event);
});

test('company-load dispatches each plugin config as a deeply frozen scoped snapshot', async () => {
  const alphaConfig = {nested: {mode: 'careful'}, list: [{value: 'alpha'}]};
  const {api} = loadAPI({fetchImpl: async url => ({
    ok: true,
    status: 200,
    json: async () => url.endsWith('/api/extensions') ? [
      {plugin: 'alpha', javascript: '/plugins/alpha/main.js', config: alphaConfig},
      {plugin: 'beta', javascript: '/plugins/beta/main.js', config: {}},
    ] : {projects: [], instances: [], agents: 0, max_agents: 0},
  })});
  let alphaEvent;
  let betaEvent;
  let wrongCalls = 0;
  api.onLoad('alpha', event => { alphaEvent = event; });
  api.onLoad('beta', event => { betaEvent = event; });
  api.onLoad('other', () => { wrongCalls++; });
  await new Promise(resolve => setImmediate(resolve));

  assert.equal(alphaEvent.detail.plugin, 'alpha');
  assert.deepEqual(JSON.parse(JSON.stringify(alphaEvent.detail.config)), alphaConfig);
  assert.equal(betaEvent.detail.plugin, 'beta');
  assert.deepEqual(JSON.parse(JSON.stringify(betaEvent.detail.config)), {});
  assert.equal(wrongCalls, 0);
  assert.equal(Object.isFrozen(alphaEvent.detail), true);
  assert.equal(Object.isFrozen(alphaEvent.detail.config), true);
  assert.equal(Object.isFrozen(alphaEvent.detail.config.nested), true);
  assert.equal(Object.isFrozen(alphaEvent.detail.config.list), true);
  assert.equal(Object.isFrozen(alphaEvent.detail.config.list[0]), true);
  assert.throws(() => { alphaEvent.detail.config.nested.mode = 'changed'; }, TypeError);
  assert.throws(() => { alphaEvent.detail.config.list.push({value: 'changed'}); }, TypeError);
  assert.throws(() => { alphaEvent.detail.config.list[0].value = 'changed'; }, TypeError);
});

test('onLoad does not deliver another plugin company-load event', () => {
  const {api, CustomEvent, window} = loadAPI();
  let calls = 0;
  api.onLoad('report-dashboard', () => { calls++; });

  window.dispatchEvent(new CustomEvent('omo:company_load', {detail: {plugin: 'other-plugin'}}));

  assert.equal(calls, 0);
});

test('onLoad remover stops later matching company-load delivery', () => {
  const {api, CustomEvent, window} = loadAPI();
  let calls = 0;
  const remove = api.onLoad('report-dashboard', () => { calls++; });
  window.dispatchEvent(new CustomEvent('omo:company_load', {detail: {plugin: 'report-dashboard'}}));
  remove();
  window.dispatchEvent(new CustomEvent('omo:company_load', {detail: {plugin: 'report-dashboard'}}));

  assert.equal(calls, 1);
});

test('onLoad rejects the unsupported single-argument form', () => {
  const {api} = loadAPI();

  assert.throws(() => api.onLoad(() => {}), {
    name: 'TypeError',
    message: 'onLoad requires a plugin name and function',
  });
});

test('dialog alert resolves when dismissed and restores the invoking focus', async () => {
  const {api, document} = loadAPI();
  const invokingButton = document.createElement('button');
  invokingButton.focus();

  const pending = api.dialog.alert('Office started.');
  assert.equal(api.$('dialog').open, true);
  assert.equal(api.$('dialog-message').textContent, 'Office started.');
  assert.equal(api.$('dialog-confirm').textContent, 'OK');

  api.$('dialog-confirm').click();
  await pending;
  assert.equal(api.$('dialog').open, false);
  assert.equal(document.activeElement, invokingButton);
});

test('dialog confirm resolves true and false through its buttons', async () => {
  const {api} = loadAPI();

  const accepted = api.dialog.confirm('Start this office?');
  api.$('dialog-confirm').click();
  assert.equal(await accepted, true);

  const rejected = api.dialog.confirm('Force kill this terminal?');
  api.$('dialog-cancel').click();
  assert.equal(await rejected, false);
});

test('dialog prompt resolves entered, default, and cancelled values', async () => {
  const {api} = loadAPI();

  const entered = api.dialog.prompt('Office path', '/tmp/office');
  assert.equal(api.$('dialog-input').value, '/tmp/office');
  api.$('dialog-input').value = '/srv/office';
  api.$('dialog-confirm').click();
  assert.equal(await entered, '/srv/office');

  const defaultValue = api.dialog.prompt('Office path', '');
  api.$('dialog-confirm').click();
  assert.equal(await defaultValue, '');

  const cancelled = api.dialog.prompt('Office path', '/tmp/office');
  api.$('dialog-cancel').click();
  assert.equal(await cancelled, null);
});

test('dialog keyboard handling confirms, cancels, dismisses, and traps focus', async () => {
  const {api, document} = loadAPI();
  const dialog = api.$('dialog');
  const cancel = api.$('dialog-cancel');
  const confirm = api.$('dialog-confirm');

  const confirmed = api.dialog.confirm('Continue?');
  cancel.focus();
  assert.equal(keyboard(dialog, 'Tab'), true);
  assert.equal(document.activeElement, confirm);
  assert.equal(keyboard(dialog, 'Tab', {shiftKey: true}), true);
  assert.equal(document.activeElement, cancel);
  keyboard(dialog, 'Enter');
  assert.equal(await confirmed, true);

  const cancelled = api.dialog.confirm('Continue?');
  keyboard(dialog, 'Escape');
  assert.equal(await cancelled, false);

  const dismissed = api.dialog.alert('Done.');
  keyboard(dialog, 'Escape');
  await dismissed;
  assert.equal(dialog.open, false);
});

test('dialog queues concurrent requests and settles each request exactly once', async () => {
  const {api} = loadAPI();
  const first = api.dialog.confirm('First?');
  const second = api.dialog.confirm('Second?');
  assert.equal(api.$('dialog-message').textContent, 'First?');

  api.$('dialog-confirm').click();
  assert.equal(await first, true);
  assert.equal(api.$('dialog-message').textContent, 'Second?');
  api.$('dialog-cancel').click();
  assert.equal(await second, false);
  assert.equal(api.$('dialog').open, false);
});

test('dialog close event cancels an active request without double-closing', async () => {
  const {api, document} = loadAPI();
  const invokingButton = document.createElement('button');
  invokingButton.focus();
  const pending = api.dialog.confirm('Close me?');

  api.$('dialog').close();

  assert.equal(await pending, false);
  assert.equal(document.activeElement, invokingButton);
});

test('Chrome exercises dialog keyboard focus and restoration behavior', t => {
  const chrome = '/usr/bin/google-chrome';
  if (!fs.existsSync(chrome)) return t.skip('Google Chrome is not installed');
  const tempDir = fs.mkdtempSync(path.join(os.tmpdir(), 'omo-dialog-'));
  const fixture = path.join(tempDir, 'index.html');
  const assetRoot = new URL(`file://${path.join(__dirname, '/')}`).href;
  const html = fs.readFileSync(path.join(__dirname, 'index.html'), 'utf8')
    .replaceAll('"/assets/', `"${assetRoot}`)
    .replace('</head>', `<script>
      window.fetch = async () => ({ok: true, status: 200, json: async () => ({projects: [], instances: [], agents: 0, max_agents: 0}), text: async () => ''});
      window.ResizeObserver = class {observe() {}};
      window.setInterval = () => {};
    </script></head>`)
    .replace('</body>', `<script>
      addEventListener('load', async () => {
        try {
          const invokingButton = document.createElement('button');
          document.body.append(invokingButton);
          invokingButton.focus();
          const dialog = document.querySelector('#dialog');
          const cancel = document.querySelector('#dialog-cancel');
          const confirm = document.querySelector('#dialog-confirm');
          const pending = window.omo.dialog.confirm('Chrome confirm');
          cancel.focus();
          dialog.dispatchEvent(new KeyboardEvent('keydown', {key: 'Tab', bubbles: true}));
          const trappedForward = document.activeElement === confirm;
          dialog.dispatchEvent(new KeyboardEvent('keydown', {key: 'Enter', bubbles: true}));
          const confirmed = await pending;
          const restored = document.activeElement === invokingButton;
          const cancelledPending = window.omo.dialog.confirm('Chrome cancel');
          dialog.dispatchEvent(new KeyboardEvent('keydown', {key: 'Escape', bubbles: true}));
          const cancelled = await cancelledPending;
          document.body.dataset.dialogTest = trappedForward && confirmed && restored && !cancelled ? 'pass' : 'fail';
        } catch (error) {
          document.body.dataset.dialogTest = 'error:' + error.message;
        }
      });
    </script></body>`);
  fs.writeFileSync(fixture, html);
  const result = spawnSync(chrome, ['--headless', '--no-sandbox', '--disable-gpu', '--dump-dom', '--virtual-time-budget=3000', `file://${fixture}`], {encoding: 'utf8', timeout: 10000, maxBuffer: 2 * 1024 * 1024});
  assert.equal(result.error, undefined, result.error?.message);
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /data-dialog-test="pass"/);
});
