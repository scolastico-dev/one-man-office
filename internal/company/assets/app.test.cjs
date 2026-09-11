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
        if (child.tagName === 'SCRIPT') {
          if (typeof this.ownerDocument.scriptAppend === 'function') this.ownerDocument.scriptAppend(child);
          else if (typeof child.onload === 'function') child.onload();
        }
      }
    },
    insertBefore(child, before) {
      const current = this.children.indexOf(child);
      if (current >= 0) this.children.splice(current, 1);
      const index = before ? this.children.indexOf(before) : -1;
      child.parentNode = this;
      if (index < 0) this.children.push(child);
      else this.children.splice(index, 0, child);
    },
    querySelectorAll(selector) {
      const descendants = [];
      const selectors = selector.split(',').map(part => part.trim());
      const matches = child => selectors.some(part => part === '*' || (part.startsWith('.') ? (child.className || '').split(/\s+/).includes(part.slice(1)) : child.tagName === part.toUpperCase()));
      const visit = parent => {
        for (const child of parent.children) {
          if (matches(child)) descendants.push(child);
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

function loadAPI({fetchImpl, FormDataImpl, locationHash = '', scriptAppend} = {}) {
  const nodes = new Map();
  const document = {
    activeElement: null,
    head: null,
    scriptAppend,
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
  document.defaultView = window;
  class CustomEvent {
    constructor(type, init = {}) {
      this.type = type;
      this.detail = init.detail;
    }
  }
  const calls = [];
  const intervals = [];
  const terminalOptions = [];
  class FakeTerminal {
    constructor(options) { terminalOptions.push(options); this.rows = 30; this.cols = 100; }
    loadAddon() {}
    open() {}
    onData() {}
    onResize() {}
    write() {}
    focus() {}
    dispose() {}
  }
  class FakeWebSocket {
    static OPEN = 1;
    constructor() { this.readyState = FakeWebSocket.OPEN; }
    send() {}
    close() {}
  }
  class FakeTerminalInput {
    constructor() {}
    flush() {}
    close() {}
  }
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
    Terminal: FakeTerminal,
    FitAddon: {FitAddon: class { fit() {} }},
    WebSocket: FakeWebSocket,
    TerminalInput: FakeTerminalInput,
    history: {replaceState() {}},
    location: {hash: locationHash, pathname: '/'},
    ResizeObserver: class { observe() {} },
    setInterval(callback) { intervals.push(callback); },
    window,
  };
  vm.runInNewContext(source, context);
  return {api: window.omo, CustomEvent, document, window, calls, intervals, terminalOptions};
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

test('trigger targets the scoped global and instance routes with bearer auth', async () => {
  const responses = [
    {ok: true, status: 200, json: async () => ({request_id: 7, result: {url: '/filebrowser/7/name'}})},
    {ok: true, status: 200, json: async () => ({request_id: 8})},
  ];
  let scoped;
  const {calls} = loadAPI({locationHash: '#secret', fetchImpl: async url => url.endsWith('/api/extensions')
    ? {ok: true, status: 200, json: async () => [{plugin: 'company', javascript: '/plugins/company/main.js', config: {}}]}
    : url.endsWith('/api/state') ? {ok: true, status: 200, json: async () => ({projects: [], instances: [], agents: 0, max_agents: 0})}
    : responses.shift(), scriptAppend: script => { scoped = script.ownerDocument.defaultView.omo; script.onload(); }});
  await new Promise(resolve => setImmediate(resolve));

  assert.deepEqual(await scoped.trigger(null, 'download', ['/tmp/a b.txt']), {request_id: 7, result: {url: '/filebrowser/7/name'}});
  assert.deepEqual(await scoped.trigger('office-1', 'download', ['/tmp/a b.txt']), {request_id: 8});
  const [globalURL, globalOptions] = calls.find(([url]) => url.endsWith('/plugins/company/trigger'));
  assert.equal(globalURL, '/api/plugins/company/trigger');
  assert.equal(globalOptions.headers.Authorization, 'Bearer secret');
  assert.equal(globalOptions.cache, 'no-store');
  assert.equal(globalOptions.body, JSON.stringify({action: 'download', args: ['/tmp/a b.txt']}));
  const [instanceURL, instanceOptions] = calls.find(([url]) => url.endsWith('/instances/office-1/trigger'));
  assert.equal(instanceURL, '/api/instances/office-1/trigger');
  assert.equal(instanceOptions.body, JSON.stringify({plugin: 'company', action: 'download', args: ['/tmp/a b.txt']}));
});

test('trigger validates arguments and throws response text', async () => {
  let calls = 0;
  let scoped;
  loadAPI({fetchImpl: async url => url.endsWith('/api/extensions')
    ? {ok: true, status: 200, json: async () => [{plugin: 'company', javascript: '/plugins/company/main.js', config: {}}]}
    : url.endsWith('/api/state') ? {ok: true, status: 200, json: async () => ({projects: [], instances: [], agents: 0, max_agents: 0})}
    : (() => {
    calls++;
    return {ok: false, status: 400, text: async () => 'bad trigger'};
  })(), scriptAppend: script => { scoped = script.ownerDocument.defaultView.omo; script.onload(); }});
  await new Promise(resolve => setImmediate(resolve));
  await assert.rejects(scoped.trigger(null, 'run', []), /bad trigger/);
  await assert.rejects(scoped.trigger(null, '', []), {name: 'TypeError'});
  await assert.rejects(scoped.trigger(undefined, 'run', []), {name: 'TypeError'});
  await assert.rejects(scoped.trigger(null, 'run', [42]), {name: 'TypeError'});
  assert.equal(calls, 1);
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

test('company-load scripts and events execute sequentially in API order', async () => {
  const trace = [];
  const {api} = loadAPI({
    fetchImpl: async url => ({
      ok: true,
      status: 200,
      json: async () => url.endsWith('/api/extensions') ? [
        {plugin: 'base', javascript: '/plugins/base/main.js', config: {}},
        {plugin: 'dependent', javascript: '/plugins/dependent/main.js', config: {}},
      ] : {projects: [], instances: [], agents: 0, max_agents: 0},
    }),
    scriptAppend: script => {
      const plugin = script.src.split('/')[2];
      trace.push(`${plugin}:script`);
      Promise.resolve().then(() => script.onload());
    },
  });
  api.onLoad('base', () => trace.push('base:event'));
  api.onLoad('dependent', () => trace.push('dependent:event'));

  await new Promise(resolve => setImmediate(resolve));

  assert.deepEqual(trace, ['base:script', 'base:event', 'dependent:script', 'dependent:event']);
});

test('scoped trigger identity remains isolated after delayed company-load handlers', async () => {
  const scopedTriggers = [];
  const triggerCalls = [];
  loadAPI({
    fetchImpl: async (url, options) => url.endsWith('/api/extensions')
      ? {ok: true, status: 200, json: async () => [
        {plugin: 'alpha', javascript: '/plugins/alpha/main.js', config: {}},
        {plugin: 'beta', javascript: '/plugins/beta/main.js', config: {}},
      ]}
      : url.endsWith('/api/state')
        ? {ok: true, status: 200, json: async () => ({projects: [], instances: [], agents: 0, max_agents: 0})}
        : (triggerCalls.push({url, options}), {ok: true, status: 200, json: async () => ({request_id: triggerCalls.length})}),
    scriptAppend: script => {
      const scoped = script.ownerDocument.defaultView.omo;
      scopedTriggers.push(scoped.trigger);
      setTimeout(() => script.onload(), 0);
    },
  });
  await new Promise(resolve => setTimeout(resolve, 20));

  await scopedTriggers[0](null, 'run', []);
  await scopedTriggers[1](null, 'run', []);
  assert.equal(scopedTriggers.length, 2);
  assert.match(triggerCalls[0].url, /plugins\/alpha\/trigger$/);
  assert.match(triggerCalls[1].url, /plugins\/beta\/trigger$/);
});

test('delayed company-load handler keeps alpha identity while beta loads', async () => {
  let alphaStarted = false;
  const triggerCalls = [];
  const {window} = loadAPI({
    fetchImpl: async url => url.endsWith('/api/extensions')
      ? {ok: true, status: 200, json: async () => [
        {plugin: 'alpha', javascript: '/plugins/alpha/main.js', config: {}},
        {plugin: 'beta', javascript: '/plugins/beta/main.js', config: {}},
      ]}
      : url.endsWith('/api/state')
        ? {ok: true, status: 200, json: async () => ({projects: [], instances: [], agents: 0, max_agents: 0})}
        : (triggerCalls.push({url}), {ok: true, status: 200, json: async () => ({request_id: triggerCalls.length})}),
    scriptAppend: script => {
      const plugin = script.src.split('/')[2];
      if (plugin === 'alpha') {
        window.omo.onLoad('alpha', async () => {
          alphaStarted = true;
          await new Promise(resolve => setTimeout(resolve, 5));
          await window.omo.trigger(null, 'run', []);
        });
      }
      script.onload();
    },
  });
  await new Promise(resolve => setTimeout(resolve, 20));
  assert.equal(alphaStarted, true);
  assert.deepEqual(triggerCalls.map(call => call.url), ['/api/plugins/alpha/trigger']);
});

test('failed company-load script stops dependent scripts and events', async () => {
  const trace = [];
  const {api} = loadAPI({
    fetchImpl: async url => ({
      ok: true,
      status: 200,
      json: async () => url.endsWith('/api/extensions') ? [
        {plugin: 'base', javascript: '/plugins/base/main.js', config: {}},
        {plugin: 'dependent', javascript: '/plugins/dependent/main.js', config: {}},
      ] : {projects: [], instances: [], agents: 0, max_agents: 0},
    }),
    scriptAppend: script => {
      const plugin = script.src.split('/')[2];
      trace.push(`${plugin}:script`);
      Promise.resolve().then(() => {
        if (plugin === 'base') script.onerror();
        else script.onload();
      });
    },
  });
  api.onLoad('base', () => trace.push('base:event'));
  api.onLoad('dependent', () => trace.push('dependent:event'));

  await new Promise(resolve => setImmediate(resolve));

  assert.deepEqual(trace, ['base:script']);
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

async function settleDashboard() {
  await new Promise(resolve => setImmediate(() => setImmediate(resolve)));
}

function projectState(projects) {
  return {projects, instances: [], agents: 0, max_agents: 2};
}

test('project dialog uses the exact trust, create, and clone labels', () => {
  const html = fs.readFileSync(path.join(__dirname, 'index.html'), 'utf8');
  assert.match(html, /<option value="trust">Trust and load<\/option>/);
  assert.match(html, /<option value="create">Create<\/option>/);
  assert.match(html, /<option value="clone">Clone<\/option>/);
  assert.match(source, /'Trust and load'/);
  assert.match(source, /'Create'/);
  assert.match(source, /'Clone'/);
});

test('offices use an accessible Edit toggle and hide edit controls outside edit mode', async () => {
  const projects = [
    {path: '/tmp/one', name: 'one', available: true},
    {path: '/tmp/two', name: 'two', available: false},
  ];
  const html = fs.readFileSync(path.join(__dirname, 'index.html'), 'utf8');
  assert.match(html, /<button[^>]*id="edit-projects"[^>]*aria-pressed="false"[^>]*>Edit<\/button>/);
  const {document} = loadAPI({fetchImpl: async url => ({
    ok: true, status: 200,
    json: async () => url.endsWith('/api/extensions') ? [] : projectState(projects),
  })});
  await settleDashboard();

  const edit = document.getElementById('edit-projects');
  assert.equal(edit.textContent, 'Edit');
  assert.equal(edit.getAttribute('aria-pressed'), 'false');
  assert.equal(document.getElementById('projects').children[0].children.length, 1);

  edit.click();
  assert.equal(edit.textContent, 'Done');
  assert.equal(edit.getAttribute('aria-pressed'), 'true');
  for (const row of document.getElementById('projects').children) {
    assert.equal(row.children[0].firstElementChild.textContent.length > 0, true);
    assert.deepEqual([...row.children].slice(1).map(button => button.textContent), ['↑', '↓', 'Remove']);
  }
});

test('edit mode disables move controls at the stored-order edges', async () => {
  const projects = [
    {path: '/tmp/one', name: 'one', available: true},
    {path: '/tmp/two', name: 'two', available: true},
    {path: '/tmp/three', name: 'three', available: true},
  ];
  const {document} = loadAPI({fetchImpl: async url => ({
    ok: true, status: 200,
    json: async () => url.endsWith('/api/extensions') ? [] : projectState(projects),
  })});
  await settleDashboard();
  document.getElementById('edit-projects').click();

  const rows = document.getElementById('projects').children;
  assert.equal(rows[0].children[1].disabled, true);
  assert.equal(rows[0].children[2].disabled, false);
  assert.equal(rows[1].children[1].disabled, false);
  assert.equal(rows[1].children[2].disabled, false);
  assert.equal(rows[2].children[1].disabled, false);
  assert.equal(rows[2].children[2].disabled, true);
});

test('moving an office posts the full order and renders the returned order', async () => {
  const one = {path: '/tmp/one', name: 'one', available: true};
  const two = {path: '/tmp/two', name: 'two', available: true};
  const calls = [];
  const {document} = loadAPI({fetchImpl: async (url, options) => {
    calls.push([url, options]);
    if (url.endsWith('/api/projects')) {
      assert.equal(options.body, JSON.stringify({action: 'reorder', paths: [two.path, one.path]}));
      return {ok: true, status: 200, json: async () => ({projects: [two, one]})};
    }
    return {ok: true, status: 200, json: async () => url.endsWith('/api/extensions') ? [] : projectState([one, two])};
  }});
  await settleDashboard();
  document.getElementById('edit-projects').click();
  document.getElementById('projects').children[0].children[2].click();
  await settleDashboard();

  assert.equal(calls.filter(([url, options]) => url.endsWith('/api/projects') && options.method === 'POST').length, 1);
  assert.deepEqual([...document.getElementById('projects').children].map(row => row.dataset.key), [two.path, one.path]);
  assert.equal(document.getElementById('edit-projects').textContent, 'Done');
});

test('failed reorder keeps the current order and shows the API error', async () => {
  const projects = [
    {path: '/tmp/one', name: 'one', available: true},
    {path: '/tmp/two', name: 'two', available: true},
  ];
  const {document} = loadAPI({fetchImpl: async (url, options) => {
    if (url.endsWith('/api/projects')) return {ok: false, status: 400, text: async () => 'reorder rejected'};
    return {ok: true, status: 200, json: async () => url.endsWith('/api/extensions') ? [] : projectState(projects)};
  }});
  await settleDashboard();
  document.getElementById('edit-projects').click();
  document.getElementById('projects').children[0].children[2].click();
  await settleDashboard();

  assert.deepEqual([...document.getElementById('projects').children].map(row => row.dataset.key), projects.map(project => project.path));
  assert.equal(document.getElementById('notice').textContent, 'reorder rejected');
});

test('edit mode survives polling and reuses focused Edit and move controls', async () => {
  const projects = [
    {path: '/tmp/one', name: 'one', available: true},
    {path: '/tmp/two', name: 'two', available: true},
  ];
  const {document, intervals} = loadAPI({fetchImpl: async url => ({
    ok: true, status: 200,
    json: async () => url.endsWith('/api/extensions') ? [] : projectState(projects),
  })});
  await settleDashboard();
  const edit = document.getElementById('edit-projects');
  edit.click();
  const move = document.getElementById('projects').children[0].children[2];
  move.focus();
  await intervals[0]();

  assert.equal(document.getElementById('edit-projects').textContent, 'Done');
  assert.equal(document.getElementById('edit-projects').getAttribute('aria-pressed'), 'true');
  assert.equal(document.getElementById('projects').children[0].children[2], move);
  assert.equal(document.activeElement, move);

  edit.focus();
  await intervals[0]();
  assert.equal(document.getElementById('edit-projects'), edit);
  assert.equal(document.activeElement, edit);
});

test('Remove stays confirmation-protected in edit mode and reuses its focused node', async () => {
  const project = {path: '/tmp/trusted-office', name: 'trusted-office', available: true};
  const {document, intervals} = loadAPI({fetchImpl: async url => ({
    ok: true, status: 200,
    json: async () => url.endsWith('/api/extensions') ? [] : projectState([project]),
  })});
  await settleDashboard();
  document.getElementById('edit-projects').click();
  const remove = document.getElementById('projects').children[0].children[3];
  remove.focus();
  await intervals[0]();
  assert.equal(document.getElementById('projects').children[0].children[3], remove);
  assert.equal(document.activeElement, remove);
  remove.click();
  assert.match(document.getElementById('dialog-message').textContent, /No files or directories will be deleted/);
});

test('successful create auto-selects the returned setup terminal', async () => {
  const setup = {id: 'setup-1', path: '/tmp/new-office', mode: 'setup', state: 'running', started: '2026-01-01T00:00:00Z'};
  let stateCalls = 0;
  const {document} = loadAPI({fetchImpl: async (url, options) => {
    if (url.endsWith('/api/projects')) {
      assert.equal(options.body, JSON.stringify({action: 'create', path: setup.path}));
      return {ok: true, status: 201, json: async () => setup};
    }
    if (url.endsWith('/api/state')) {
      stateCalls++;
      return {ok: true, status: 200, json: async () => ({projects: [], instances: stateCalls > 1 ? [setup] : [], agents: 0, max_agents: 2})};
    }
    return {ok: true, status: 200, json: async () => []};
  }});
  await settleDashboard();
  document.getElementById('action').value = 'create';
  document.getElementById('project-path').value = setup.path;
  await document.getElementById('project-form').onsubmit({preventDefault() {}});
  await settleDashboard();
  assert.equal(document.getElementById('project-dialog').open, false);
  assert.equal(document.getElementById('selected').textContent, `Setup · ${setup.path}`);
});

test('office terminals disable xterm scrollback while shell terminals retain it', async () => {
  const office = {id: 'office-1', path: '/tmp/office', mode: 'omo', state: 'running', started: '2026-01-01T00:00:00Z'};
  const shell = {id: 'shell-1', path: '/tmp/office', mode: 'shell', state: 'running', started: '2026-01-01T00:00:01Z'};
  const {document, terminalOptions} = loadAPI({fetchImpl: async url => ({
    ok: true,
    status: 200,
    json: async () => url.endsWith('/api/extensions') ? [] : {projects: [], instances: [office, shell], agents: 0, max_agents: 2},
  })});
  await settleDashboard();

  document.getElementById('instances').children[0].click();
  document.getElementById('instances').children[1].click();

  assert.equal(terminalOptions[0].scrollback, 0);
  assert.equal(terminalOptions[1].scrollback, 2000);
});

test('stale project keeps only a disabled launch control outside edit mode', async () => {
  const project = {path: '/tmp/stale-office', name: 'stale-office', available: false};
  const {document} = loadAPI({fetchImpl: async url => ({
    ok: true,
    status: 200,
    json: async () => url.endsWith('/api/extensions') ? [] : projectState([project]),
  })});
  await settleDashboard();

  const row = document.getElementById('projects').children[0];
  assert.equal(row.className, 'project-row');
  assert.equal(row.children.length, 1);
  assert.equal(row.children[0].tagName, 'BUTTON');
  assert.equal(row.children[0].disabled, true);
});

test('cancelling Remove confirmation sends no untrust request', async () => {
  const project = {path: '/tmp/trusted-office', name: 'trusted-office', available: true};
  const calls = [];
  const {api, document} = loadAPI({fetchImpl: async (url, options) => {
    calls.push([url, options]);
    return {ok: true, status: 200, json: async () => url.endsWith('/api/extensions') ? [] : projectState([project])};
  }});
  await settleDashboard();

  document.getElementById('edit-projects').click();
  const remove = document.getElementById('projects').children[0].children[3];
  remove.click();
  assert.match(document.getElementById('dialog-message').textContent, /No files or directories will be deleted/);
  document.getElementById('dialog-cancel').click();
  await settleDashboard();
  assert.equal(calls.some(([url, options]) => url.endsWith('/api/projects') && options.method === 'POST'), false);
  assert.equal(api.$('notice').textContent || '', '');
});

test('confirming Remove posts the exact untrust action and refreshes state', async () => {
  const project = {path: '/tmp/trusted-office', name: 'trusted-office', available: true};
  let stateCalls = 0;
  const calls = [];
  const {document} = loadAPI({fetchImpl: async (url, options) => {
    calls.push([url, options]);
    if (url.endsWith('/api/projects')) return {ok: true, status: 204};
    if (url.endsWith('/api/state')) return {ok: true, status: 200, json: async () => projectState(stateCalls++ === 0 ? [project] : [])};
    return {ok: true, status: 200, json: async () => []};
  }});
  await settleDashboard();

  document.getElementById('edit-projects').click();
  document.getElementById('projects').children[0].children[3].click();
  document.getElementById('dialog-confirm').click();
  await settleDashboard();
  const post = calls.find(([url, options]) => url.endsWith('/api/projects'));
  assert.equal(post[1].method, 'POST');
  assert.equal(post[1].body, JSON.stringify({action: 'untrust', path: project.path}));
  assert.equal(document.getElementById('projects').children[0].textContent, 'No offices to launch. Add a project to get started.');
  assert.match(document.getElementById('notice').textContent, /No files were deleted/);
});

test('Remove API failures flow to the dashboard notice', async () => {
  const project = {path: '/tmp/trusted-office', name: 'trusted-office', available: true};
  const {document} = loadAPI({fetchImpl: async url => {
    if (url.endsWith('/api/projects')) return {ok: false, status: 409, text: async () => 'stop the running instance first'};
    return {ok: true, status: 200, json: async () => url.endsWith('/api/extensions') ? [] : projectState([project])};
  }});
  await settleDashboard();

  document.getElementById('edit-projects').click();
  document.getElementById('projects').children[0].children[3].click();
  document.getElementById('dialog-confirm').click();
  await settleDashboard();
  assert.equal(document.getElementById('notice').textContent, 'stop the running instance first');
});

test('project polling reuses the Remove node and preserves its focus', async () => {
  const project = {path: '/tmp/trusted-office', name: 'trusted-office', available: true};
  const {document, intervals} = loadAPI({fetchImpl: async url => ({
    ok: true,
    status: 200,
    json: async () => url.endsWith('/api/extensions') ? [] : projectState([project]),
  })});
  await settleDashboard();
  document.getElementById('edit-projects').click();
  const remove = document.getElementById('projects').children[0].children[3];
  remove.focus();
  await intervals[0]();
  assert.equal(document.getElementById('projects').children[0].children[3], remove);
  assert.equal(document.activeElement, remove);
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

test('Chrome preserves focused move control after successful reorder', t => {
  const chrome = '/usr/bin/google-chrome';
  if (!fs.existsSync(chrome)) return t.skip('Google Chrome is not installed');
  const tempDir = fs.mkdtempSync(path.join(os.tmpdir(), 'omo-reorder-'));
  const fixture = path.join(tempDir, 'index.html');
  const assetRoot = new URL(`file://${path.join(__dirname, '/')}`).href;
  const html = fs.readFileSync(path.join(__dirname, 'index.html'), 'utf8')
    .replaceAll('"/assets/', `"${assetRoot}`)
    .replace('</head>', `<script>
      const first = {path: '/tmp/one', name: 'one', available: true};
      const second = {path: '/tmp/two', name: 'two', available: true};
      const third = {path: '/tmp/three', name: 'three', available: true};
      window.fetch = async (url, options = {}) => {
        if (url.endsWith('/api/projects')) {
          window.reorderPayload = JSON.parse(options.body);
          return {ok: true, status: 200, json: async () => ({projects: [second, first, third]})};
        }
        if (url.endsWith('/api/extensions')) return {ok: true, status: 200, json: async () => []};
        return {ok: true, status: 200, json: async () => ({projects: [first, second, third], instances: [], agents: 0, max_agents: 2})};
      };
      window.ResizeObserver = class {observe() {}};
      window.setInterval = () => {};
    </script></head>`)
    .replace('</body>', `<script>
      addEventListener('load', () => setTimeout(() => {
        try {
          document.querySelector('#edit-projects').click();
          const move = document.querySelector('#projects').children[0].children[2];
          move.focus();
          move.click();
          setTimeout(() => {
            document.body.dataset.reorderFocus = document.activeElement === move ? 'preserved' : 'lost';
            document.body.dataset.reorderPayload = JSON.stringify(window.reorderPayload || {});
          }, 100);
        } catch (error) {
          document.body.dataset.reorderFocus = 'error:' + error.message;
        }
      }, 150));
    </script></body>`);
  fs.writeFileSync(fixture, html);
  const result = spawnSync(chrome, ['--headless', '--no-sandbox', '--disable-gpu', '--dump-dom', '--virtual-time-budget=3000', `file://${fixture}`], {encoding: 'utf8', timeout: 10000, maxBuffer: 2 * 1024 * 1024});
  assert.equal(result.error, undefined, result.error?.message);
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /data-reorder-focus="preserved"/);
  assert.match(result.stdout, /data-reorder-payload="\{&quot;action&quot;:&quot;reorder&quot;,&quot;paths&quot;:\[&quot;\/tmp\/two&quot;,&quot;\/tmp\/one&quot;,&quot;\/tmp\/three&quot;\]\}"/);
});
