'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const {createFilebrowser} = require('./main.js');

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
