'use strict';
const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const source = fs.readFileSync(path.join(__dirname, 'assets/terminal-input.js'), 'utf8');
const TerminalInput = vm.runInNewContext(source + '\nTerminalInput;', {TextEncoder, WebSocket: {OPEN: 1}});
function fixture(readyState = 1) {
  const frames = [], errors = [];
  const socket = {readyState, send(data) {frames.push(Buffer.from(data));}};
  return {frames, errors, socket, input: new TerminalInput(socket, text => errors.push(text))};
}

test('large Unicode paste is paced by PTY acknowledgments and followed by Enter', () => {
  const {input, frames, errors} = fixture();
  const paste = '\x1b[200~' + 'ä🎉\rtext'.repeat(20000) + '\x1b[201~';
  input.send(paste);
  input.send('\r');
  assert.equal(frames.length, 1, 'a stalled PTY must not receive a burst of frames');
  assert.equal(frames[0].length, 16 * 1024);
  while (input.pendingBytes) input.acknowledge();
  assert.ok(frames.every(frame => frame.length <= 16 * 1024));
  assert.deepEqual(Buffer.concat(frames), Buffer.from(paste + '\r'));
  assert.deepEqual(errors, []);
  assert.equal(input.queue.length, 0);
});

test('paste before socket open waits instead of disappearing', () => {
  const {input, socket, frames} = fixture(0);
  input.send('pending');
  assert.equal(frames.length, 0);
  socket.readyState = 1;
  input.flush();
  assert.equal(frames[0].toString(), 'pending');
  input.acknowledge();
  assert.equal(input.pendingBytes, 0);
});

test('oversized and excessive queued inputs are rejected whole without disconnecting', () => {
  const {input, socket, frames, errors} = fixture();
  input.send('🎉'.repeat(1024 * 1024 + 1));
  assert.equal(frames.length, 0);
  assert.equal(errors.length, 1);
  input.send('valid');
  assert.equal(frames[0].toString(), 'valid');
  for (let i = 0; i < 1024; i++) input.send('x');
  assert.equal(input.queue.length, 1024);
  assert.equal(errors.length, 2);
  assert.equal(socket.readyState, 1);
});

test('disconnect discards pending input and late acknowledgments cannot replay it', () => {
  const {input, frames, errors} = fixture();
  input.send('x'.repeat(70000));
  input.close();
  input.acknowledge();
  input.flush();
  input.send('more');
  assert.equal(input.pendingBytes, 0);
  assert.equal(input.queue.length, 0);
  assert.equal(frames.length, 1);
  assert.match(errors[0], /disconnected/);
});
