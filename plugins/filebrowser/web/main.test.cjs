'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const {createFilebrowser} = require('./main.js');

class FakeElement {
  constructor(document, tag) {
    this.ownerDocument = document; this.tagName = tag.toUpperCase(); this.children = []; this.parentNode = null;
    this.dataset = {}; this.style = {}; this.hidden = false; this.open = false; this.value = ''; this.textContent = '';
  }
  append(...children) { for (const child of children) { if (typeof child === 'string') continue; this.appendChild(child); } }
  appendChild(child) { if (!child) return child; child.parentNode = this; this.children.push(child); return child; }
  insertBefore(child, before) { const index = this.children.indexOf(before); child.parentNode = this; this.children.splice(index < 0 ? this.children.length : index, 0, child); }
  replaceChildren(...children) { this.children = []; this.append(...children); }
  setAttribute(name, value) { this[name] = String(value); }
  querySelector(selector) {
    if ((selector === 'details' && this.tagName === 'DETAILS') || (selector === 'h2' && this.tagName === 'H2') || (selector === '.filebrowser-picker-actions' && this.className === 'filebrowser-picker-actions')) return this;
    for (const child of this.children) { const found = child.querySelector?.(selector); if (found) return found; }
    return null;
  }
  querySelectorAll(selector) {
    const hasClassAncestor = (node, className) => {
      for (let parent = node.parentNode; parent; parent = parent.parentNode) if (parent.className === className) return true;
      return false;
    };
    const matches = node => selector.split(',').some(part => {
      part = part.trim();
      if (part === '#filebrowser-browse' || part === '#filebrowser-button') return node.id === part.slice(1);
      if (part === '.filebrowser-panel button') return node.tagName === 'BUTTON' && hasClassAncestor(node, 'panel filebrowser-panel');
      if (part === '.filebrowser-overlay button') return node.tagName === 'BUTTON' && hasClassAncestor(node, 'panel filebrowser-overlay');
      if (part === '.filebrowser-overlay input') return node.tagName === 'INPUT' && hasClassAncestor(node, 'panel filebrowser-overlay');
      if (part === '.filebrowser-overlay select') return node.tagName === 'SELECT' && hasClassAncestor(node, 'panel filebrowser-overlay');
      return false;
    });
    const result = [];
    const visit = node => { if (matches(node)) result.push(node); for (const child of node.children) visit(child); };
    visit(this);
    return result;
  }
  focus() { this.ownerDocument.activeElement = this; }
  showModal() { this.open = true; this.topLayer = true; }
  close() { this.open = false; }
  dispatchEvent() {}
}

class FakeDocument {
  constructor() { this.head = new FakeElement(this, 'head'); this.body = new FakeElement(this, 'body'); this.activeElement = null; }
  createElement(tag) { return new FakeElement(this, tag); }
  getElementById(id) {
    const visit = node => { if (node.id === id) return node; for (const child of node.children) { const found = visit(child); if (found) return found; } return null; };
    return visit(this.body) || visit(this.head);
  }
  querySelectorAll(selector) { return [...this.body.querySelectorAll(selector), ...this.head.querySelectorAll(selector)]; }
}

function projectDialogHarness() {
  const document = new FakeDocument();
  const ids = {};
  for (const [key, tag] of [['sidebar', 'aside'], ['main', 'main'], ['toolbar', 'nav']]) {
    const element = document.createElement(tag); element.id = key; document.body.append(element); ids[key] = key;
  }
  const projectDialog = document.createElement('dialog'); projectDialog.id = 'project-dialog'; projectDialog.open = true;
  const label = document.createElement('label'); const input = document.createElement('input'); input.id = 'project-path'; input.value = '/tmp/../work'; label.append(input); projectDialog.append(label); document.body.append(projectDialog);
  const calls = [];
  const window = {
    document, Event: class { constructor(type) { this.type = type; } },
    omo: {ids, token: 'secret-token', execute: async (command, args, options = {}) => {
      calls.push({command, args});
      if (command === 'pwd') options.onOutput?.({stream: 'stdout', data: '/home/user\n'});
      if (command === 'find') {
        const directory = args.includes('-type') && args[args.indexOf('-type') + 1] === 'd';
        options.onOutput?.({stream: 'stdout', data: directory ? '/work/dir\0' : '/work/z\0/work/a\0'});
      }
      if (command === 'wc') options.onOutput?.({stream: 'stdout', data: '2\n'});
      return {code: 0};
    }},
    fetch: async () => ({ok: true, json: async () => ({projects: [], instances: []})}),
  };
  return {document, projectDialog, input, window, calls};
}

async function waitFor(predicate) {
  for (let attempt = 0; attempt < 100; attempt++) {
    if (predicate()) return;
    await new Promise(resolve => setImmediate(resolve));
  }
  throw new Error('timed out waiting for controlled command');
}

test('initialization is idempotent and probes only once', async () => {
  const elements = new Map();
  const document = {
    getElementById(id) { return elements.get(id) || null; },
    createElement() { return {dataset: {}, classList: {add() {}, contains() { return false; }}, append() {}, addEventListener() {}, setAttribute() {}, style: {}, hidden: false}; },
  };
  const window = {
    document,
    omo: {
      ids: {sidebar: 'sidebar', main: 'main', toolbar: 'toolbar', status: 'status'},
      execute: async () => ({code: 0}),
    },
  };
  for (const id of ['sidebar', 'main', 'toolbar', 'status']) elements.set(id, {id, children: [], append() {}, appendChild() {}, querySelector() { return null; }});
  const app = createFilebrowser(window, document);
  const first = app.init();
  const second = app.init();
  await Promise.all([first, second]);
  assert.equal(app.initializationCount(), 1);
  assert.equal(app.probeCount(), 1);
});

test('picker selection writes the normalized path and emits input and change', () => {
  const events = [];
  const input = {value: '', dispatchEvent: event => events.push(event.type), focus() {}};
  const app = createFilebrowser({omo: {}}, {getElementById: id => id === 'project-path' ? input : null});
  app.selectPickerPath('/tmp/../work');
  assert.equal(input.value, '/work');
  assert.deepEqual(events, ['input', 'change']);
});

test('Browse from an open project dialog opens a top-layer picker and restores focus', async () => {
  const harness = projectDialogHarness();
  const app = createFilebrowser(harness.window, harness.document);
  await app.init({detail: {config: {}}});
  const browse = harness.document.getElementById('filebrowser-browse');
  assert.ok(browse, 'Browse button should be injected');
  await browse.onclick();
  const picker = harness.document.getElementById('filebrowser-overlay');
  assert.equal(harness.projectDialog.open, true);
  assert.equal(picker.tagName, 'DIALOG');
  assert.equal(picker.open, true);
  assert.equal(picker.hidden, false);
  assert.equal(picker.topLayer, true);
  assert.deepEqual(harness.calls.filter(call => call.command === 'find').map(call => call.args), [
    ['/work', '-mindepth', '1', '-maxdepth', '1', '-type', 'd', '-print0'],
  ]);
  harness.document.getElementById('filebrowser-select').onclick();
  assert.equal(picker.open, false);
  assert.equal(picker.hidden, true);
  assert.equal(harness.input.value, '/work');
  assert.equal(harness.document.activeElement, harness.input);
});

test('sorting each header in both directions retains every listed row', async () => {
  const harness = projectDialogHarness();
  const app = createFilebrowser(harness.window, harness.document);
  await app.init({detail: {config: {}}});
  await app.openBrowser(false);
  const body = harness.document.getElementById('filebrowser-rows');
  const rowCount = body.children.length;
  for (const field of ['name', 'type', 'size']) {
    const header = harness.document.getElementById(`filebrowser-sort-${field}`);
    header.onclick();
    assert.equal(body.children.length, rowCount, `${field} ascending should retain rows`);
    header.onclick();
    assert.equal(body.children.length, rowCount, `${field} descending should retain rows`);
  }
});

test('probe failure disables every filebrowser action including Files toolbar and Browse', async () => {
  const harness = projectDialogHarness();
  harness.window.omo.execute = async () => { throw new Error('uname unavailable'); };
  const app = createFilebrowser(harness.window, harness.document);
  await app.init({detail: {config: {}}});
  assert.equal(harness.document.getElementById('filebrowser-button').disabled, true);
  assert.equal(harness.document.getElementById('filebrowser-browse').disabled, true);
  assert.equal(harness.document.getElementById('filebrowser-warning').textContent, 'The file manager is not supported on Windows');
});

test('a stale listing failure cannot clear a newer successful listing', async () => {
  const harness = projectDialogHarness();
  const pendingFinds = [];
  harness.window.omo.execute = async (command, args, options = {}) => {
    harness.calls.push({command, args});
    if (command === 'uname') return {code: 0};
    if (command === 'pwd') { options.onOutput?.({stream: 'stdout', data: '/home/user\n'}); return {code: 0}; }
    if (command === 'wc') { options.onOutput?.({stream: 'stdout', data: '1\n'}); return {code: 0}; }
    if (command === 'find') return new Promise((resolve, reject) => pendingFinds.push({options, resolve, reject}));
    return {code: 0};
  };
  const app = createFilebrowser(harness.window, harness.document);
  await app.init({detail: {config: {}}});
  const first = app.openBrowser(false);
  await waitFor(() => pendingFinds.length === 1);
  const second = app.openBrowser(false);
  await waitFor(() => pendingFinds.length === 2);
  pendingFinds[1].options.onOutput({stream: 'stdout', data: '/work/new-dir\0'});
  pendingFinds[1].resolve({code: 0});
  await waitFor(() => pendingFinds.length === 3);
  pendingFinds[2].options.onOutput({stream: 'stdout', data: '/work/new-file\0'});
  pendingFinds[2].resolve({code: 0});
  await second;
  const body = harness.document.getElementById('filebrowser-rows');
  const newerRowCount = body.children.length;
  pendingFinds[0].reject(new Error('old request failed'));
  await first;
  assert.equal(body.children.length, newerRowCount);
});
