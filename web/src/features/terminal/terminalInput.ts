/** The part of a WebSocket the input queue needs. */
export interface InputSocket {
  readyState: number
  send(data: Uint8Array<ArrayBuffer>): void
}

/** A refusal of input while no connection is open. */
export const DISCONNECTED_TEXT = 'Terminal disconnected. This input was not sent; press Reconnect.'

const OPEN = 1
const MAX_PENDING_BYTES = 4 * 1024 * 1024
const MAX_QUEUED = 1024
const CHUNK = 16 * 1024

/**
 * Keeps at most one frame in flight until the PTY has consumed it. Browser
 * WebSocket buffering alone cannot tell whether the terminal is reading.
 */
export class TerminalInput {
  queue: { data: Uint8Array<ArrayBuffer>; offset: number }[] = []
  pendingBytes = 0
  inFlight = 0
  closed = false

  constructor(
    private readonly socket: InputSocket,
    private readonly reportError: (text: string) => void,
  ) {}

  send(text: string): void {
    if (this.closed || this.socket.readyState > OPEN) {
      this.reportError(DISCONNECTED_TEXT)
      return
    }
    if (this.pendingBytes + text.length > MAX_PENDING_BYTES || this.queue.length >= MAX_QUEUED) {
      this.reject()
      return
    }
    const data = new TextEncoder().encode(text)
    if (!data.length) return
    if (this.pendingBytes + data.length > MAX_PENDING_BYTES) {
      this.reject()
      return
    }
    this.queue.push({ data, offset: 0 })
    this.pendingBytes += data.length
    this.flush()
  }

  private reject(): void {
    this.reportError(
      'Terminal input buffer is full (4 MiB maximum). This input was not sent; wait for the pending paste or paste a smaller selection.',
    )
  }

  flush(): void {
    const next = this.queue[0]
    if (this.closed || this.inFlight || !next || this.socket.readyState !== OPEN) return
    const chunk = next.data.subarray(next.offset, next.offset + CHUNK)
    this.socket.send(chunk)
    next.offset += chunk.length
    this.inFlight = chunk.length
  }

  acknowledge(): void {
    const head = this.queue[0]
    if (this.closed || !this.inFlight || !head) return
    this.pendingBytes -= this.inFlight
    this.inFlight = 0
    if (head.offset === head.data.length) this.queue.shift()
    this.flush()
  }

  close(): void {
    // Never replay possibly delivered input into a replacement connection.
    this.closed = true
    this.queue = []
    this.pendingBytes = 0
    this.inFlight = 0
  }
}
