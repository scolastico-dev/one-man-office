'use strict';
const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, 'app.js'), 'utf8');

function element() {
  return {
    children: [],
    append(...nodes) { this.children.push(...nodes); },
    insertBefore(node, before) {
      const index = before ? this.children.indexOf(before) : -1;
      if (index < 0) this.children.push(node);
      else this.children.splice(index, 0, node);
    },
    querySelectorAll() { return []; },
    remove() {},
  };
}

function loadAPI() {
  const nodes = new Map();
  const document = {
    head: element(),
    createElement: element,
    getElementById(id) {
      if (!nodes.has(id)) nodes.set(id, element());
      return nodes.get(id);
    },
  };
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
  return {api: window.omo, window};
}

test('onLoad delivers matching company-load events to the named plugin', () => {
  const {api, window} = loadAPI();
  let received;
  api.onLoad('report-dashboard', event => { received = event; });
  const event = new CustomEvent('omo:company_load', {detail: {plugin: 'report-dashboard'}});

  window.dispatchEvent(event);

  assert.equal(received, event);
});

test('onLoad does not deliver another plugin company-load event', () => {
  const {api, window} = loadAPI();
  let calls = 0;
  api.onLoad('report-dashboard', () => { calls++; });

  window.dispatchEvent(new CustomEvent('omo:company_load', {detail: {plugin: 'other-plugin'}}));

  assert.equal(calls, 0);
});

test('onLoad remover stops later matching company-load delivery', () => {
  const {api, window} = loadAPI();
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
