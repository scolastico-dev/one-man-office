import { DISCONNECTED_TEXT, TerminalInput } from './terminalInput'

export type ConnectionState = 'connecting' | 'live' | 'disconnected' | 'ended'

export interface SocketDeps {
  WebSocket?: typeof WebSocket
  location?: Pick<Location, 'protocol' | 'host'>
  fetch?: typeof fetch
}

/**
 * Opens the websocket at `path` on the page's own host. The login token
 * travels only as the subprotocol `omo-token.<token>` next to `omo`; the
 * address never carries it.
 */
export function openTerminalSocket(path: string, token: string | null, deps: SocketDeps = {}): WebSocket {
  const loc = deps.location ?? window.location
  const Socket = deps.WebSocket ?? WebSocket
  const url = `${loc.protocol === 'https:' ? 'wss:' : 'ws:'}//${loc.host}${path}`
  const protocols = token ? ['omo', `omo-token.${token}`] : ['omo']
  const socket = new Socket(url, protocols)
  socket.binaryType = 'arraybuffer'
  return socket
}

export interface ConnectionOptions {
  path: string
  /** The login token to offer, read again on every connection. */
  getToken(): string | null
  /**
   * Output of the terminal. `replay` is true for the history the server
   * replays on every new connection, up to its end-of-replay frame.
   */
  onData(data: Uint8Array, replay: boolean): void
  /**
   * A connection opened; its replay of the whole history follows. What the
   * screen shows from an earlier connection must go first, or the history
   * would be shown twice.
   */
  onReplayStart(): void
  /** `retryMs` is set while a reconnect is scheduled. */
  onState(state: ConnectionState, retryMs?: number): void
  /** A refusal of input, in words. */
  onError(text: string): void
  /** Back-off between automatic reconnects; when used up the connection stays disconnected. */
  delays?: readonly number[]
  deps?: SocketDeps
}

export interface TerminalConnection {
  /** Sends typed or pasted text to the process. */
  send(text: string): void
  /** Tells the process the new size; remembered and sent again on every new connection. */
  resize(rows: number, cols: number): void
  /** Connects again right now while `disconnected`, with the back-off from its start. */
  reconnect(): void
  /** Closes the socket and stops every timer. */
  dispose(): void
}

export const RECONNECT_DELAYS: readonly number[] = [1000, 2000, 4000, 8000, 16000]

/**
 * One terminal's connection: connect, replay, resize, input pacing, and
 * reconnect with back-off while the terminal still exists. A close with code
 * 1000, or a handshake the server answers with 404, means the process ended.
 * Input queued on a dead connection is never replayed into a new one.
 */
export function createTerminalConnection(o: ConnectionOptions): TerminalConnection {
  const delays = o.delays ?? RECONNECT_DELAYS
  let socket: WebSocket | null = null
  let input: TerminalInput | null = null
  let size: { rows: number; cols: number } | null = null
  let failures = 0
  let timer: ReturnType<typeof setTimeout> | undefined
  let disposed = false
  let finished = false

  function state(s: ConnectionState, retryMs?: number): void {
    if (!disposed) o.onState(s, retryMs)
  }

  function connect(): void {
    if (disposed || finished) return
    clearTimeout(timer)
    state('connecting')
    let opened = false
    let replaying = true
    let s: WebSocket
    try {
      s = openTerminalSocket(o.path, o.getToken(), o.deps)
    } catch {
      // The reason is left out on purpose: it may quote what was offered.
      failures = delays.length
      state('disconnected')
      return
    }
    socket = s
    const mine = new TerminalInput(s, o.onError)
    input = mine
    s.onopen = () => {
      if (disposed || socket !== s) return
      opened = true
      failures = 0
      o.onReplayStart()
      state('live')
      if (size) sendSize(s, size)
      mine.flush()
    }
    s.onmessage = (ev: MessageEvent) => {
      if (disposed || socket !== s) return
      if (typeof ev.data === 'string') {
        try {
          const type = (JSON.parse(ev.data) as { type?: unknown }).type
          if (type === 'input-ack') mine.acknowledge()
          else if (type === 'replay-end') replaying = false
        } catch {
          // Not a frame this client knows.
        }
        return
      }
      if (ev.data instanceof ArrayBuffer) o.onData(new Uint8Array(ev.data), replaying)
    }
    s.onclose = (ev: CloseEvent) => {
      mine.close()
      if (disposed || socket !== s) return
      socket = null
      if (ev.code === 1000) {
        finish()
        return
      }
      if (opened) lost()
      else void refused()
    }
  }

  function finish(): void {
    finished = true
    state('ended')
  }

  /** The connection dropped or could not be made: try again after the next back-off, or give up. */
  function lost(): void {
    if (disposed || finished || socket) return
    if (failures >= delays.length) {
      state('disconnected')
      return
    }
    const wait = delays[failures++] ?? 0
    state('disconnected', wait)
    timer = setTimeout(connect, wait)
  }

  /** A handshake that never opened: the terminal is gone when the server says 404. */
  async function refused(): Promise<void> {
    let status = 0
    try {
      const f = o.deps?.fetch ?? fetch
      const token = o.getToken()
      const res = await f(o.path, { headers: token ? { Authorization: `Bearer ${token}` } : {}, cache: 'no-store' })
      status = res.status
      void res.body?.cancel()
    } catch {
      // Offline or refused: treated like any other failed attempt.
    }
    if (disposed || finished) return
    if (status === 404) finish()
    else lost()
  }

  function sendSize(s: WebSocket, z: { rows: number; cols: number }): void {
    if (s.readyState === 1) s.send(JSON.stringify({ rows: z.rows, cols: z.cols }))
  }

  connect()

  return {
    send(text) {
      if (disposed) return
      if (!input || finished) {
        o.onError(DISCONNECTED_TEXT)
        return
      }
      input.send(text)
    },
    resize(rows, cols) {
      size = { rows, cols }
      if (socket) sendSize(socket, size)
    },
    reconnect() {
      if (disposed || finished || socket) return
      failures = 0
      connect()
    },
    dispose() {
      disposed = true
      clearTimeout(timer)
      input?.close()
      const s = socket
      socket = null
      if (s) {
        s.onopen = s.onmessage = s.onclose = null
        try {
          s.close()
        } catch {
          // Already closed.
        }
      }
    },
  }
}
