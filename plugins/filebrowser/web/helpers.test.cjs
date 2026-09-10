'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const helpers = require('./helpers.js');

test('normalizes POSIX absolute paths lexically and preserves root', () => {
  assert.equal(helpers.normalizePath('/home/demo/../work//./file'), '/home/work/file');
  assert.equal(helpers.normalizePath('/../../'), '/');
  assert.equal(helpers.normalizePath('/'), '/');
  assert.equal(helpers.normalizePath('/tmp/\0bad'), '');
});

test('parent navigation and breadcrumbs include the filesystem root', () => {
  assert.equal(helpers.parentPath('/home/demo'), '/home');
  assert.equal(helpers.parentPath('/'), '/');
  assert.deepEqual(helpers.breadcrumbs('/home/demo'), [
    {label: '/', path: '/'},
    {label: 'home', path: '/home'},
    {label: 'demo', path: '/home/demo'},
  ]);
});

test('accumulates arbitrary output chunks before parsing ls lines', () => {
  const output = helpers.accumulateStdout([
    {stream: 'stdout', data: 'alpha\nbe'},
    {stream: 'stderr', data: 'ignored'},
    {stream: 'stdout', data: 'ta/\nplain'},
    {stream: 'stdout', data: '\n'},
  ]);
  assert.equal(output, 'alpha\nbeta/\nplain\n');
  assert.deepEqual(helpers.parseListing(output), [
    {name: 'alpha', type: 'file'},
    {name: 'beta', type: 'directory'},
    {name: 'plain', type: 'file'},
  ]);
});

test('skips newline names and exposes a visible note condition', () => {
  const result = helpers.parseNullListing('/tmp/ok\0/tmp/bad\nname\0/tmp/possible\0', 'directory');
  assert.deepEqual(result.entries, [
    {name: 'ok', type: 'directory'},
    {name: 'possible', type: 'directory'},
  ]);
  assert.equal(result.skippedNewlineNames, true);
  assert.equal(helpers.displayableName('bad\nname'), false);
});

test('parses arbitrary chunks of NUL-delimited absolute find output', () => {
  const chunks = ['/tmp/good\0/tmp/bad', '\nname\0/tmp/dir\0'];
  const parsed = helpers.parseNullListing(chunks.join(''), 'file');
  assert.deepEqual(parsed.entries, [
    {name: 'good', type: 'file'},
    {name: 'dir', type: 'file'},
  ]);
  assert.equal(parsed.skippedNewlineNames, true);
});

test('sorts entries stably by name, type, or byte size in both directions', () => {
  const entries = [
    {name: 'z', type: 'file', size: 2},
    {name: 'a', type: 'directory', size: null},
    {name: 'b', type: 'file', size: 2},
  ];
  assert.deepEqual(helpers.sortEntries(entries, 'name', 'asc').map(e => e.name), ['a', 'b', 'z']);
  assert.deepEqual(helpers.sortEntries(entries, 'type', 'desc').map(e => e.name), ['z', 'b', 'a']);
  assert.deepEqual(helpers.sortEntries(entries, 'size', 'asc').map(e => e.name), ['a', 'z', 'b']);
});

test('deduplicates absolute roots from projects and instances', () => {
  assert.deepEqual(helpers.buildRoots({
    projects: [{path: '/work'}, {path: '/work'}, {path: 'relative'}],
    instances: [{path: '/tmp'}, {path: '/work'}],
  }, '/home/demo'), [
    {label: 'Home', path: '/home/demo'},
    {label: '/work', path: '/work'},
    {label: '/tmp', path: '/tmp'},
  ]);
});

test('validates a single new-folder component', () => {
  assert.equal(helpers.validateFolderComponent('new-folder'), '');
  for (const value of ['', '.', '..', '/', 'a/b', 'a\0b', '  ']) {
    assert.notEqual(helpers.validateFolderComponent(value), '');
  }
});

test('joins only safe literal directory components', () => {
  assert.equal(helpers.joinPath('/tmp', 'child'), '/tmp/child');
  assert.equal(helpers.joinPath('/tmp', '../escape'), '');
  assert.equal(helpers.joinPath('/tmp', 'bad\nname'), '');
});
