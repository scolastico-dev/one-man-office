'use strict';

// Keep at most one frame in flight until the PTY has consumed it. Browser
// WebSocket buffering alone cannot tell us whether the terminal is reading.
class TerminalInput {
  constructor(socket, reportError) {
    this.socket = socket;
    this.reportError = reportError;
    this.queue = [];
    this.pendingBytes = 0;
    this.inFlight = 0;
    this.closed = false;
  }

  send(text) {
    if (this.closed || this.socket.readyState > WebSocket.OPEN) {
      this.reportError('Terminal disconnected. Select it again before pasting.');
      return;
    }
    if (this.pendingBytes + text.length > 4 * 1024 * 1024 || this.queue.length >= 1024) {
      this.reject();
      return;
    }
    const data = new TextEncoder().encode(text);
    if (!data.length) return;
    if (this.pendingBytes + data.length > 4 * 1024 * 1024) {
      this.reject();
      return;
    }
    this.queue.push({data, offset: 0});
    this.pendingBytes += data.length;
    this.flush();
  }

  reject() {
    this.reportError('Terminal input buffer is full (4 MiB maximum). This input was not sent; wait for the pending paste or paste a smaller selection.');
  }

  flush() {
    if (this.closed || this.inFlight || !this.queue.length || this.socket.readyState !== WebSocket.OPEN) return;
    const next = this.queue[0];
    const chunk = next.data.subarray(next.offset, next.offset + 16 * 1024);
    this.socket.send(chunk);
    next.offset += chunk.length;
    this.inFlight = chunk.length;
  }

  acknowledge() {
    if (this.closed || !this.inFlight) return;
    this.pendingBytes -= this.inFlight;
    this.inFlight = 0;
    if (this.queue[0].offset === this.queue[0].data.length) this.queue.shift();
    this.flush();
  }

  close() {
    // Never replay possibly delivered input into a replacement connection.
    this.closed = true;
    this.queue = [];
    this.pendingBytes = 0;
    this.inFlight = 0;
  }
}
