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
      for (const child of nodes) { child.parentNode = this; this.children.push(child); }
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

function loadAPI() {
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
  const context = {
    CustomEvent,
    document,
    fetch: async url => ({
      ok: true,
      status: 200,
      json: async () => url.endsWith('/api/extensions') ? [] : {projects: [], instances: [], agents: 0, max_agents: 0},
    }),
    history: {replaceState() {}},
    location: {hash: '', pathname: '/'},
    ResizeObserver: class { observe() {} },
    setInterval() {},
    window,
  };
  vm.runInNewContext(source, context);
  return {api: window.omo, CustomEvent, document, window};
}

function keyboard(target, key, options = {}) {
  let prevented = false;
  target.dispatchEvent({
    type: 'keydown', key, shiftKey: options.shiftKey || false,
    preventDefault() { prevented = true; },
  });
  return prevented;
}

test('onLoad delivers matching company-load events to the named plugin', () => {
  const {api, CustomEvent, window} = loadAPI();
  let received;
  api.onLoad('report-dashboard', event => { received = event; });
  const event = new CustomEvent('omo:company_load', {detail: {plugin: 'report-dashboard'}});

  window.dispatchEvent(event);

  assert.equal(received, event);
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
