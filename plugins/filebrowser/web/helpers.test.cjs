'use strict';

const test = require('node:test');
const assert = require('node:assert/strict');
const helpers = require('./helpers.js');

test('threshold decisions warn only above the warning boundary and reject above the maximum', () => {
  assert.equal(helpers.transferThreshold(50, 50, 100), 'ok');
  assert.equal(helpers.transferThreshold(51, 50, 100), 'warn');
  assert.equal(helpers.transferThreshold(100, 50, 100), 'warn');
  assert.equal(helpers.transferThreshold(101, 50, 100), 'reject');
});

test('file basenames and lexical destinations accept one safe component only', () => {
  assert.equal(helpers.validateFileComponent('report.txt'), '');
  assert.notEqual(helpers.validateFileComponent('../report.txt'), '');
  assert.notEqual(helpers.validateFileComponent('nested/report.txt'), '');
  assert.notEqual(helpers.validateFileComponent('report\n.txt'), '');
  assert.equal(helpers.joinPath('/var/tmp', 'report.txt'), '/var/tmp/report.txt');
  assert.equal(helpers.joinPath('/var/tmp', '../report.txt'), '');
});

test('normalizes POSIX absolute paths lexically and preserves root', () => {
  assert.equal(helpers.normalizePath('/home/demo/../work//./file'), '/home/work/file');
  assert.equal(helpers.normalizePath('/../../'), '/');
  assert.equal(helpers.normalizePath('/'), '/');
  assert.equal(helpers.normalizePath('/tmp/\0bad'), '');
});

test('normalizes Windows drive paths with slash variants and rejects UNC paths clearly', () => {
  assert.equal(helpers.normalizePath('C:\\'), 'C:\\');
  assert.equal(helpers.normalizePath('C:/Users/demo/../work'), 'C:\\Users\\work');
  assert.equal(helpers.normalizePath('C:\\Users\\demo\\file.txt'), 'C:\\Users\\demo\\file.txt');
  assert.equal(helpers.normalizePath('\\\\server\\share'), '');
  assert.match(helpers.pathError('\\\\server\\share'), /UNC paths are not supported/);
});

test('parent navigation and breadcrumbs include the filesystem root', () => {
  assert.equal(helpers.parentPath('/home/demo'), '/home');
  assert.equal(helpers.parentPath('/'), '/');
  assert.equal(helpers.parentPath('C:\\Users'), 'C:\\');
  assert.equal(helpers.parentPath('C:\\Users\\demo'), 'C:\\Users');
  assert.equal(helpers.parentPath('C:\\'), 'C:\\');
  assert.equal(helpers.parentPath('C:\\Users\\demo\\projects'), 'C:\\Users\\demo');
  assert.deepEqual(helpers.breadcrumbs('C:\\Users'), [
    {label: 'C:\\', path: 'C:\\'},
    {label: 'Users', path: 'C:\\Users'},
  ]);
  assert.deepEqual(helpers.breadcrumbs('C:\\Users\\demo'), [
    {label: 'C:\\', path: 'C:\\'},
    {label: 'Users', path: 'C:\\Users'},
    {label: 'demo', path: 'C:\\Users\\demo'},
  ]);
  assert.deepEqual(helpers.breadcrumbs('/home/demo'), [
    {label: '/', path: '/'},
    {label: 'home', path: '/home'},
    {label: 'demo', path: '/home/demo'},
  ]);
  assert.equal(helpers.parentPath('\\\\server\\share'), '/');
  assert.deepEqual(helpers.breadcrumbs('\\\\server\\share'), [{label: '/', path: '/'}]);
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
  assert.equal(helpers.joinPath('C:\\Temp', 'child'), 'C:\\Temp\\child');
});

test('extracts basenames from POSIX and Windows paths', () => {
  assert.equal(helpers.basename('/tmp/report.txt'), 'report.txt');
  assert.equal(helpers.basename('C:\\Temp\\report.txt'), 'report.txt');
});
