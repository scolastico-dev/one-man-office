import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { createTokenStore } from '../../auth/token'
import { createSession, SESSION, type Session } from '../../session'
import { FakeOmo, general as FAKE_GENERAL } from '../../testing/fakeOmo'

const h = vi.hoisted(() => ({
  terms: [] as any[],
  conns: [] as any[],
  /** What the fit proposes; a test may change it. */
  propose: (): { rows: number; cols: number } | undefined => ({ rows: 30, cols: 100 }),
}))

vi.mock('@xterm/xterm', () => {
  class Terminal {
    options: Record<string, any>
    rows = 24
    cols = 80
    textarea = document.createElement('textarea')
    keyHandler: ((e: KeyboardEvent) => boolean) | null = null
    dataHandler: ((d: string) => void) | null = null
    osc: number[] = []
    oscHandlers = new Map<number, (data: string) => boolean>()
    selection = ''
    hasSelection() {
      return this.selection !== ''
    }
    getSelection() {
      return this.selection
    }
    clearSelection() {
      this.selection = ''
    }
    disposed = false
    written: Uint8Array[] = []
    lineText = ''
    buffer = {
      active: {
        getLine: () => ({ translateToString: (_trim: boolean, from = 0, to?: number) => this.lineText.slice(from, to) }),
      },
    }
    parser = {
      registerOscHandler: (code: number, cb: (data: string) => boolean) => {
        this.osc.push(code)
        this.oscHandlers.set(code, cb)
        expect(cb('')).toBe(true)
        return { dispose() {} }
      },
    }
    constructor(options: Record<string, any>) {
      this.options = options
      h.terms.push(this)
    }
    loadAddon() {}
    open(el: HTMLElement) {
      el.appendChild(this.textarea)
    }
    attachCustomKeyEventHandler(fn: (e: KeyboardEvent) => boolean) {
      this.keyHandler = fn
    }
    onData(fn: (d: string) => void) {
      this.dataHandler = fn
    }
    parsing: Array<() => void> = []
    write(d: Uint8Array, cb?: () => void) {
      this.written.push(d)
      if (cb) this.parsing.push(cb)
    }
    /** Lets the emulator finish parsing what was written. */
    parse() {
      for (const cb of this.parsing.splice(0)) cb()
    }
    pasted: string[] = []
    paste(d: string) {
      this.pasted.push(d)
    }
    resize(c: number, r: number) {
      this.cols = c
      this.rows = r
    }
    resets = 0
    /** A full reset: the screen and its history are empty afterwards. */
    reset() {
      this.resets++
      this.written = []
    }
    focus = vi.fn()
    dispose() {
      this.disposed = true
    }
  }
  return { Terminal }
})
vi.mock('@xterm/addon-fit', () => ({ FitAddon: class { proposeDimensions() { return h.propose() } } }))
vi.mock('@xterm/xterm/css/xterm.css', () => ({}))
vi.mock('./terminalSocket', () => ({
  createTerminalConnection: (o: any) => {
    const c = { o, send: vi.fn(), sendMessage: vi.fn(async () => {}), resize: vi.fn(), reconnect: vi.fn(), dispose: vi.fn() }
    h.conns.push(c)
    return c
  },
}))

import TerminalView from './TerminalView.vue'
import { resetPopouts } from './popout'
import { resetWritable } from './terminalWritable'

const live: Array<{ unmount(): void }> = []
function sessionWith(tokens = createTokenStore(() => window.localStorage)): Session {
  const omo = new FakeOmo()
  return createSession({ tokens, makeClient: omo.makeClient, connect: omo.connect })
}
function view(props: Record<string, unknown> = {}, session: Session = sessionWith()) {
  const w = mount(TerminalView, {
    props: { src: '/api/terminals/t/socket', label: 'Terminal of developer Ada on #12', ...props },
    attachTo: document.body,
    global: { provide: { [SESSION as symbol]: session } },
  })
  live.push(w)
  return w
}
const term = () => h.terms[h.terms.length - 1]
const conn = () => h.conns[h.conns.length - 1]

beforeEach(() => {
  h.propose = () => ({ rows: 30, cols: 100 })
  h.terms.length = 0
  h.conns.length = 0
  window.localStorage.clear()
  resetWritable()
})
afterEach(() => {
  live.splice(0).forEach((w) => w.unmount())
  vi.useRealTimers()
  vi.restoreAllMocks()
  resetPopouts()
})

describe('TerminalView', () => {
  test('offers the token the session holds, also when browser storage is blocked', () => {
    const blocked = createTokenStore(() => {
      throw new DOMException('blocked', 'SecurityError')
    })
    blocked.save('memory-token')
    view({}, sessionWith(blocked))
    expect(conn().o.getToken()).toBe('memory-token')
  })

  test('asks the session again on every connection, so a rotated token is used', () => {
    const tokens = createTokenStore(() => window.localStorage)
    tokens.save('a'.repeat(64))
    view({}, sessionWith(tokens))
    expect(conn().o.getToken()).toBe('a'.repeat(64))
    tokens.save('b'.repeat(64))
    expect(conn().o.getToken()).toBe('b'.repeat(64))
  })

  test('has an accessible name, a hint about F6 and hardened emulator options', () => {
    const w = view()
    expect(w.find('[role="group"]').attributes('aria-label')).toBe('Terminal of developer Ada on #12')
    expect(w.text()).toContain('F6 leaves the terminal')
    expect(term().textarea.getAttribute('aria-label')).toBe('Terminal of developer Ada on #12')
    const hint = w.find('.tv__hint').attributes('id')
    expect(term().textarea.getAttribute('aria-describedby')).toBe(hint)
    expect(term().options.allowProposedApi).toBe(false)
    expect(term().options.scrollback).toBe(5000)
    expect(term().options.linkHandler.allowNonHttpProtocols).toBe(false)
    expect(term().osc).toEqual(expect.arrayContaining([0, 2, 52]))
  })

  test('the toolbar says the state in words and offers Reconnect when disconnected', async () => {
    const w = view()
    const emitted = () => w.emitted('state')?.map((e) => e[0])
    expect(w.text()).toContain('Connecting')
    conn().o.onState('live')
    await nextTick()
    expect(w.text()).toContain('Connected')
    expect(w.findAll('button').some((b) => b.text() === 'Reconnect')).toBe(false)
    conn().o.onState('disconnected', 2000)
    await nextTick()
    expect(w.text()).toContain('Disconnected, trying again in 2 s')
    const reconnect = w.findAll('button').find((b) => b.text() === 'Reconnect')!
    await reconnect.trigger('click')
    expect(conn().reconnect).toHaveBeenCalled()
    conn().o.onState('ended')
    await nextTick()
    expect(w.text()).toContain('Ended')
    expect(w.text()).toContain('The process has ended.')
    expect(w.findAll('button').some((b) => b.text() === 'Reconnect')).toBe(false)
    expect(emitted()).toEqual(['live', 'disconnected', 'ended'])
    expect(term().options.disableStdin).toBe(true)
  })

  test('a new connection empties the screen before its replay, so a reconnect shows the history once', () => {
    view()
    const t = term()
    const bytes = (x: string) => new TextEncoder().encode(x)
    conn().o.onReplayStart()
    conn().o.onData(bytes('line 1\r\n'), true)
    conn().o.onData(bytes('line 2\r\n'), false)
    // The connection dropped and opened again: the server replays everything.
    conn().o.onReplayStart()
    conn().o.onData(bytes('line 1\r\nline 2\r\n'), true)
    expect(t.resets).toBe(2)
    expect(t.written.map((d: Uint8Array) => new TextDecoder().decode(d))).toEqual(['line 1\r\nline 2\r\n'])
  })

  test('a disconnected terminal connects again once the board is live again; a live one is left alone', async () => {
    const omo = new FakeOmo()
    const session = createSession({ tokens: createTokenStore(() => window.localStorage), makeClient: omo.makeClient, connect: omo.connect })
    session.start()
    await vi.waitFor(() => expect(omo.stream).not.toBeNull())
    omo.stream!.onState('live')
    view({}, session)
    conn().o.onState('live')
    omo.stream!.onState('retrying', 1000)
    omo.stream!.onState('live')
    await nextTick()
    expect(conn().reconnect).not.toHaveBeenCalled()
    // It tried its back-off and gave up while omo was away.
    conn().o.onState('disconnected')
    omo.stream!.onState('retrying', 1000)
    await nextTick()
    expect(conn().reconnect).not.toHaveBeenCalled()
    omo.stream!.onState('live')
    await nextTick()
    expect(conn().reconnect).toHaveBeenCalledTimes(1)
    session.store.stop()
  })

  test('fill makes the view take the height of its column; by default it keeps its own', () => {
    expect(view().classes()).not.toContain('tv--fill')
    expect(view({ fill: true }).classes()).toContain('tv--fill')
  })

  test('the ended text is the one given', async () => {
    const w = view({ endedText: 'The developer finished.' })
    conn().o.onState('ended')
    await nextTick()
    expect(w.text()).toContain('The developer finished.')
  })

  test('F6 leaves the terminal and Shift+F6 goes back, neither reaches the shell', () => {
    const before = document.createElement('button')
    const after = document.createElement('button')
    for (const b of [before, after]) b.getClientRects = () => [{}] as unknown as DOMRectList
    const w = view()
    document.body.prepend(before)
    document.body.append(after)
    const key = (init: KeyboardEventInit) => new KeyboardEvent('keydown', { cancelable: true, ...init })
    const f6 = key({ key: 'F6' })
    expect(term().keyHandler(f6)).toBe(false)
    expect(f6.defaultPrevented).toBe(true)
    expect(document.activeElement).toBe(after)
    const back = key({ key: 'F6', shiftKey: true })
    expect(term().keyHandler(back)).toBe(false)
    expect(document.activeElement).not.toBe(after)
    expect(term().keyHandler(key({ key: 'a' }))).toBe(true)
    w.unmount()
    before.remove()
    after.remove()
  })

  test('typing goes to the connection; a read-only terminal ignores it', () => {
    view()
    term().dataHandler('ls\r')
    expect(conn().send).toHaveBeenCalledWith('ls\r')
    const ro = view({ readonly: true })
    expect(ro.text()).toContain('Read only')
    expect(term().options.disableStdin).toBe(true)
    term().dataHandler('rm\r')
    expect(conn().send).not.toHaveBeenCalled()
  })

  test('a paste is cleaned of control characters before the emulator wraps it', () => {
    view()
    const ev = new Event('paste', { bubbles: true, cancelable: true })
    Object.defineProperty(ev, 'clipboardData', { value: { getData: () => 'echo a\x1b[201~\necho INJECTED' } })
    term().textarea.dispatchEvent(ev)
    expect(ev.defaultPrevented).toBe(true)
    expect(term().pasted).toEqual(['echo a[201~\necho INJECTED'])
  })

  test('a link whose text is not its address opens only after the address was shown and confirmed', async () => {
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    const w = view()
    const click = new MouseEvent('click', { ctrlKey: true })
    term().lineText = 'see https://docs.example now'
    term().options.linkHandler.activate(click, 'https://docs.example', { start: { x: 5, y: 1 }, end: { x: 24, y: 1 } })
    expect(open).toHaveBeenCalledTimes(1)
    term().lineText = 'https://bank.example'
    term().options.linkHandler.activate(click, 'https://evil.example/\u202elogin', { start: { x: 1, y: 1 }, end: { x: 20, y: 1 } })
    await nextTick()
    expect(open).toHaveBeenCalledTimes(1)
    const dialog = document.body.querySelector('[data-link-confirm]')!
    expect(dialog.textContent).toContain('https://bank.example')
    expect(dialog.querySelector('[data-link-target]')!.textContent).not.toContain('\u202e')
    expect(dialog.querySelector('[data-link-target]')!.textContent).toContain('https://evil.example/')
    ;(document.body.querySelector('[data-link-cancel]') as HTMLButtonElement).click()
    await nextTick()
    expect(document.body.querySelector('[data-link-confirm]')).toBeNull()
    expect(open).toHaveBeenCalledTimes(1)
    term().options.linkHandler.activate(click, 'https://evil.example/login', { start: { x: 1, y: 1 }, end: { x: 20, y: 1 } })
    await nextTick()
    ;(document.body.querySelector('[data-link-open]') as HTMLButtonElement).click()
    await nextTick()
    expect(open).toHaveBeenLastCalledWith('https://evil.example/login', '_blank', 'noopener,noreferrer')
    expect(document.body.querySelector('[data-link-confirm]')).toBeNull()
    w.unmount()
    open.mockRestore()
  })

  test('what the emulator answers while it parses replayed history never reaches the process', () => {
    view()
    const bytes = (t: string) => new TextEncoder().encode(t)
    conn().o.onData(bytes('old\x1b[c'), true)
    conn().o.onData(bytes('\x1b[6n'), true)
    // Answers to the replayed queries.
    term().dataHandler('\x1b[?1;2c')
    term().parse()
    expect(conn().send).not.toHaveBeenCalled()
    // Live output: its answers go to the process.
    conn().o.onData(bytes('\x1b[c'), false)
    term().dataHandler('\x1b[?1;2c')
    term().parse()
    term().dataHandler('ls\r')
    expect(conn().send.mock.calls).toEqual([['\x1b[?1;2c'], ['ls\r']])
  })

  test('what the user types or pastes while history replays reaches the process; the emulator\'s answers in a later turn still do not', async () => {
    const w = view()
    const bytes = (t: string) => new TextEncoder().encode(t)
    conn().o.onData(bytes('old\x1b[c'), true)
    // A key: the emulator asks the key handler and hands over the key's text in the same turn.
    for (const key of ['e', 'c']) {
      expect(term().keyHandler(new KeyboardEvent('keypress', { key }))).toBe(true)
      term().dataHandler(key)
    }
    expect(conn().send.mock.calls).toEqual([['e'], ['c']])
    // The turn of the key is over: an answer to a replayed query is not the user's.
    await Promise.resolve()
    term().dataHandler('\x1b[?1;2c')
    expect(conn().send.mock.calls).toEqual([['e'], ['c']])
    // A key without text (Shift alone) leaves nothing behind for a later answer either.
    expect(term().keyHandler(new KeyboardEvent('keydown', { key: 'Shift' }))).toBe(true)
    await Promise.resolve()
    term().dataHandler('\x1b[?1;2c')
    expect(conn().send.mock.calls).toEqual([['e'], ['c']])
    // A paste: the emulator hands the text over while it is being pasted.
    term().paste = (d: string) => term().dataHandler(d)
    const ev = new Event('paste', { bubbles: true, cancelable: true })
    Object.defineProperty(ev, 'clipboardData', { value: { getData: () => 'ho', files: [] } })
    w.find('.tv__screen').element.dispatchEvent(ev)
    expect(conn().send.mock.calls).toEqual([['e'], ['c'], ['ho']])
    term().dataHandler('\x1b[?1;2c')
    expect(conn().send.mock.calls).toEqual([['e'], ['c'], ['ho']])
    // Still replaying until the emulator has parsed the chunk.
    term().parse()
    term().dataHandler('\r')
    expect(conn().send.mock.calls).toEqual([['e'], ['c'], ['ho'], ['\r']])
  })

  test('output is written to the emulator only, never into the page', () => {
    const w = view()
    conn().o.onData(new TextEncoder().encode('<img src=x onerror=alert(1)>'))
    expect(term().written).toHaveLength(1)
    expect(w.find('img').exists()).toBe(false)
  })

  test('the global screen reader setting reaches the emulator and a change applies live; the toolbar has no toggle', async () => {
    const session = sessionWith()
    const general = (on: boolean) => ({ ...FAKE_GENERAL, ui: { ...FAKE_GENERAL.ui, screen_reader_mode: on } })
    ;(session.store.state as { general: unknown }).general = general(true)
    const w = view({}, session)
    expect(term().options.screenReaderMode).toBe(true)
    expect(w.find('[role="switch"]').exists()).toBe(false)
    ;(session.store.state as { general: unknown }).general = general(false)
    await nextTick()
    expect(term().options.screenReaderMode).toBe(false)
    expect(window.localStorage.getItem('omo.terminal.screenReader')).toBeNull()
  })

  test('a size is sent on mount and the box resizing is debounced', async () => {
    vi.useFakeTimers()
    const calls: Array<() => void> = []
    const disconnect = vi.fn()
    vi.stubGlobal('ResizeObserver', class { constructor(cb: () => void) { calls.push(cb) } observe() {} disconnect = disconnect })
    view()
    expect(conn().resize).toHaveBeenCalledWith(30, 100)
    conn().resize.mockClear()
    calls[0]!(); calls[0]!(); calls[0]!()
    expect(conn().resize).not.toHaveBeenCalled()
    vi.advanceTimersByTime(100)
    expect(conn().resize).toHaveBeenCalledTimes(1)
    vi.unstubAllGlobals()
  })

  test('the size is sent again when the terminal gets the focus or becomes visible, so another view of the same terminal cannot keep its size', () => {
    const seen: Array<(entries: Array<{ isIntersecting: boolean }>) => void> = []
    vi.stubGlobal('IntersectionObserver', class { constructor(cb: (e: Array<{ isIntersecting: boolean }>) => void) { seen.push(cb) } observe() {} disconnect() {} })
    view()
    conn().resize.mockClear()
    term().textarea.dispatchEvent(new FocusEvent('focus'))
    expect(conn().resize).toHaveBeenCalledWith(30, 100)
    conn().resize.mockClear()
    seen[0]!([{ isIntersecting: false }])
    expect(conn().resize).not.toHaveBeenCalled()
    seen[0]!([{ isIntersecting: true }])
    expect(conn().resize).toHaveBeenCalledWith(30, 100)
    vi.unstubAllGlobals()
  })

  test('unmounting closes the connection, stops observers and disposes the emulator', () => {
    const disconnect = vi.fn()
    vi.stubGlobal('ResizeObserver', class { observe() {} disconnect = disconnect })
    const w = view()
    const c = conn()
    const t = term()
    w.unmount()
    expect(c.dispose).toHaveBeenCalled()
    expect(t.disposed).toBe(true)
    expect(disconnect).toHaveBeenCalled()
    vi.unstubAllGlobals()
  })

  test('exposes focus()', () => {
    const w = view()
    ;(w.vm as unknown as { focus(): void }).focus()
    expect(term().focus).toHaveBeenCalled()
  })
})

describe('fitting', () => {
  test('a resize that measures a font loaded meanwhile fits again with the new cell size, and the process gets that', () => {
    // The first proposal is made with the old cell size; after the resize the emulator has measured again.
    let calls = 0
    h.propose = () => (++calls === 1 ? { rows: 21, cols: 82 } : { rows: 18, cols: 80 })
    view()
    expect([term().rows, term().cols]).toEqual([18, 80])
    expect(conn().resize).toHaveBeenLastCalledWith(18, 80)
  })

  test('the emulator starts once the font is loaded, so it measures its cells with it', async () => {
    let loaded!: () => void
    const fonts = { check: vi.fn(() => false), load: vi.fn(() => new Promise<void>((r) => (loaded = r))) }
    const before = Object.getOwnPropertyDescriptor(document, 'fonts')
    Object.defineProperty(document, 'fonts', { value: fonts, configurable: true })
    try {
      const w = view()
      expect(h.terms).toHaveLength(0)
      expect(fonts.load).toHaveBeenCalledWith(expect.stringMatching(/^13px /))
      loaded()
      await new Promise((r) => setTimeout(r, 0))
      expect(h.terms).toHaveLength(1)
      expect(w.find('.tv__screen').attributes('data-size')).toBe('30x100')
    } finally {
      if (before) Object.defineProperty(document, 'fonts', before)
      else delete (document as { fonts?: unknown }).fonts
    }
  })

  test('a terminal unmounted while its font loads never starts', async () => {
    let loaded!: () => void
    const before = Object.getOwnPropertyDescriptor(document, 'fonts')
    Object.defineProperty(document, 'fonts', { value: { check: () => false, load: () => new Promise<void>((r) => (loaded = r)) }, configurable: true })
    try {
      view().unmount()
      loaded()
      await new Promise((r) => setTimeout(r, 0))
      expect(h.terms).toHaveLength(0)
      expect(h.conns).toHaveLength(0)
    } finally {
      if (before) Object.defineProperty(document, 'fonts', before)
      else delete (document as { fonts?: unknown }).fonts
    }
  })
})

describe('its own window', () => {
  test('a named button opens the terminal in a window of its own, sized as remembered for its kind', async () => {
    const opened = { closed: false, focus: vi.fn(), close: vi.fn() }
    const open = vi.spyOn(window, 'open').mockReturnValue(opened as unknown as Window)
    window.localStorage.setItem('omo.terminalWindow.size.ceo', JSON.stringify({ width: 1100, height: 700 }))
    const w = view({ src: '/api/agents/t%201/terminal', label: 'Terminal of the CEO', windowKind: 'ceo' })
    const button = w.get('[data-terminal-popout]')
    expect(button.attributes('aria-label')).toBe('Open the terminal of the CEO in its own window')
    await button.trigger('click')
    expect(open).toHaveBeenCalledWith('/terminal/agent/t%201', 'omo-terminal-agent-t_1', expect.stringMatching(/^popup,width=\d+,height=\d+$/))
    expect(open.mock.calls[0]![2]).toContain('height=700')
    expect(opened.focus).toHaveBeenCalled()
  })

  test('a blocked window says so in place with a link to the same page in a new tab', async () => {
    vi.spyOn(window, 'open').mockReturnValue(null)
    const w = view({ src: '/api/terminals/s1/socket', label: 'Terminal Home' })
    await w.get('[data-terminal-popout]').trigger('click')
    const note = w.get('[data-terminal-blocked]')
    expect(note.attributes('role')).toBe('alert')
    expect(note.text()).toContain('The browser blocked the window. Allow pop-ups for this address, or open it here:')
    const link = w.get('[data-terminal-blocked-link]')
    expect(link.attributes('href')).toBe('/terminal/user/s1')
    expect(link.attributes('target')).toBe('_blank')
    // The live view stays.
    expect(w.find('.tv__screen').exists()).toBe(true)
  })

  test('no button in the window itself, for an ended process, or for a path that names no terminal', async () => {
    expect(view({ popout: false }).find('[data-terminal-popout]').exists()).toBe(false)
    expect(view({ src: '/somewhere' }).find('[data-terminal-popout]').exists()).toBe(false)
    const w = view()
    conn().o.onState('ended')
    await nextTick()
    expect(w.find('[data-terminal-popout]').exists()).toBe(false)
  })

  test('autofocus puts the focus into the terminal once it is shown', () => {
    view({ autofocus: true })
    expect(term().focus).toHaveBeenCalled()
  })
})

describe('terminal imports', () => {
  test('no file outside features/terminal imports TerminalView.vue directly', () => {
    const files = import.meta.glob(['/src/**/*.{vue,ts}', '!/src/features/terminal/**'], { query: '?raw', import: 'default', eager: true }) as Record<string, string>
    const offenders = Object.entries(files)
      .filter(([, text]) => /TerminalView(\.vue)?['"]/.test(text))
      .map(([file]) => file)
    expect(Object.keys(files).length).toBeGreaterThan(50)
    expect(offenders).toEqual([])
  })

  test('the emulator is made in TerminalView.vue only, so every terminal view copies, pastes and takes images the same way', () => {
    const files = import.meta.glob(['/src/**/*.{vue,ts}', '!/src/**/*.test.ts'], { query: '?raw', import: 'default', eager: true }) as Record<string, string>
    const makers = Object.entries(files)
      .filter(([, text]) => /new Terminal\(/.test(text))
      .map(([file]) => file)
    expect(makers).toEqual(['/src/features/terminal/TerminalView.vue'])
  })

  test('OSC 52 has a handler of its own: the clipboard is neither swallowed nor answered', async () => {
    view()
    term().keyHandler(new KeyboardEvent('keydown', { key: 'a' }))
    const set = vi.fn(async () => {})
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: set } })
    try {
      term().oscHandlers.get(52)!('c;YQ==')
      await vi.waitFor(() => expect(set).toHaveBeenCalledWith('a'))
      expect(conn().send).not.toHaveBeenCalled()
    } finally {
      delete (navigator as { clipboard?: unknown }).clipboard
    }
  })
})

describe('clipboard', () => {
  const b64 = (text: string) => btoa(String.fromCharCode(...new TextEncoder().encode(text)))
  const key = (init: KeyboardEventInit) => new KeyboardEvent('keydown', { cancelable: true, ...init })
  let copiedTexts: string[]
  let allow: boolean
  beforeEach(() => {
    copiedTexts = []
    allow = true
    // happy-dom has no copy command: this one fires the copy event as a browser does, or refuses like one without a gesture.
    Object.defineProperty(document, 'execCommand', {
      configurable: true,
      writable: true,
      value: (cmd: string) => {
        if (cmd !== 'copy' || !allow) return false
        const ev = new Event('copy', { bubbles: true, cancelable: true })
        Object.defineProperty(ev, 'clipboardData', { value: { setData: (_t: string, v: string) => copiedTexts.push(v) } })
        ;(document.activeElement ?? document.body).dispatchEvent(ev)
        return true
      },
    })
  })
  afterEach(() => {
    delete (document as { execCommand?: unknown }).execCommand
    delete (navigator as { clipboard?: unknown }).clipboard
  })
  /** Outside a secure context there is no clipboard API, as over plain HTTP. */
  const noClipboardApi = () => Object.defineProperty(navigator, 'clipboard', { configurable: true, value: undefined })
  const osc52 = (data: string) => term().oscHandlers.get(52)!(data)

  test('a program sets the clipboard with OSC 52 shortly after the user pressed in the terminal', async () => {
    const w = view()
    term().keyHandler(key({ key: 'a' }))
    expect(osc52(`c;${b64('selected in the program – ü')}`)).toBe(true)
    await vi.waitFor(() => expect(copiedTexts).toEqual(['selected in the program – ü']))
    await nextTick()
    expect(w.find('[data-terminal-note]').text()).toBe('Copied')
    expect(conn().send).not.toHaveBeenCalled()
  })

  test('a read query is never answered and output without the user’s input never sets the clipboard', async () => {
    view()
    osc52(`c;${b64('unasked')}`)
    term().keyHandler(key({ key: 'a' }))
    osc52('c;?')
    osc52('c;!!!')
    await nextTick()
    expect(copiedTexts).toEqual([])
    expect(conn().send).not.toHaveBeenCalled()
    expect(term().pasted).toEqual([])
  })

  test('replayed history never sets the clipboard', async () => {
    view()
    term().keyHandler(key({ key: 'a' }))
    conn().o.onData(new Uint8Array([1]), true)
    osc52(`c;${b64('old')}`)
    term().parse()
    await nextTick()
    expect(copiedTexts).toEqual([])
  })

  test('refused without a gesture, the text waits for the Copy button or the next key in the terminal', async () => {
    noClipboardApi()
    const w = view()
    allow = false
    term().keyHandler(key({ key: 'a' }))
    osc52(`c;${b64('later')}`)
    await vi.waitFor(() => expect(w.find('[data-terminal-copy]').exists()).toBe(true))
    allow = true
    await w.find('[data-terminal-copy]').trigger('click')
    expect(copiedTexts).toEqual(['later'])
    await nextTick()
    expect(w.find('[data-terminal-copy]').exists()).toBe(false)

    allow = false
    term().keyHandler(key({ key: 'a' }))
    osc52(`c;${b64('next key')}`)
    await vi.waitFor(() => expect(w.find('[data-terminal-copy]').exists()).toBe(true))
    allow = true
    expect(term().keyHandler(key({ key: 'b' }))).toBe(true)
    expect(copiedTexts).toEqual(['later', 'next key'])
  })

  test('Shift+Enter and Ctrl+Enter send ESC CR, once and without the plain CR; Enter and Alt+Enter stay with the emulator', () => {
    view()
    const send = h.conns[0].send
    for (const mods of [{ shiftKey: true }, { ctrlKey: true }]) {
      send.mockClear()
      const down = key({ key: 'Enter', code: 'Enter', ...mods })
      expect(term().keyHandler(down)).toBe(false)
      expect(down.defaultPrevented).toBe(true)
      // The keypress that follows must not reach the emulator either.
      expect(term().keyHandler(new KeyboardEvent('keypress', { key: 'Enter', ...mods }))).toBe(false)
      expect(send.mock.calls).toEqual([['\x1b\r']])
    }
    send.mockClear()
    expect(term().keyHandler(key({ key: 'Enter', code: 'Enter' }))).toBe(true)
    expect(term().keyHandler(key({ key: 'Enter', code: 'Enter', altKey: true }))).toBe(true)
    expect(send).not.toHaveBeenCalled()
  })

  test('Ctrl+C with a selection copies it, clears it and does not interrupt; without one it is the interrupt', () => {
    view()
    term().selection = 'some output'
    const ctrlC = key({ key: 'c', code: 'KeyC', ctrlKey: true })
    expect(term().keyHandler(ctrlC)).toBe(false)
    expect(ctrlC.defaultPrevented).toBe(true)
    expect(copiedTexts).toEqual(['some output'])
    expect(term().hasSelection()).toBe(false)
    const again = key({ key: 'c', code: 'KeyC', ctrlKey: true })
    expect(term().keyHandler(again)).toBe(true)
    expect(again.defaultPrevented).toBe(false)
  })

  test('Ctrl+Shift+C and Ctrl+Insert copy the selection and never reach the program or the browser', () => {
    view()
    term().selection = 'abc'
    for (const init of [{ key: 'C', code: 'KeyC', ctrlKey: true, shiftKey: true }, { key: 'Insert', code: 'Insert', ctrlKey: true }]) {
      const ev = key(init)
      expect(term().keyHandler(ev)).toBe(false)
      expect(ev.defaultPrevented).toBe(true)
    }
    expect(copiedTexts).toEqual(['abc', 'abc'])
    expect(term().hasSelection()).toBe(true)
  })

  test('Ctrl+V, Ctrl+Shift+V and Shift+Insert are left to the browser’s paste', () => {
    view()
    for (const init of [{ key: 'v', ctrlKey: true }, { key: 'V', ctrlKey: true, shiftKey: true }, { key: 'Insert', shiftKey: true }]) {
      const ev = key(init)
      expect(term().keyHandler(ev)).toBe(false)
      expect(ev.defaultPrevented).toBe(false)
    }
  })
})

describe('dropped and pasted images', () => {
  const png = () => new File([new Uint8Array([0x89, 0x50, 0x4e, 0x47])], 'shot one.png', { type: 'image/png' })
  function drop(target: Element, files: File[]): Event {
    const ev = new Event('drop', { bubbles: true, cancelable: true })
    Object.defineProperty(ev, 'dataTransfer', { value: { files, types: ['Files'], getData: () => '' } })
    target.dispatchEvent(ev)
    return ev
  }

  test('a dropped image is stored by omo and its path pasted, quoted, without a line break', async () => {
    const session = sessionWith()
    const upload = vi.spyOn(session.client, 'upload').mockResolvedValue({
      data: { path: '/home/me/omo/images/terminal/a b.png', name: 'shot one.png', content_type: 'image/png', size: 4 },
    } as never)
    const w = view({}, session)
    drop(term().textarea, [png()])
    await vi.waitFor(() => expect(term().pasted).toEqual(["'/home/me/omo/images/terminal/a b.png'"]))
    expect(upload).toHaveBeenCalledWith('/api/terminal-images', expect.any(File), { 'Omo-Image-Name': 'shot%20one.png' })
    expect(w.find('[data-terminal-note]').exists()).toBe(false)
  })

  test('a file that is not an image is refused with a note and not uploaded', async () => {
    const session = sessionWith()
    const upload = vi.spyOn(session.client, 'upload')
    const w = view({}, session)
    drop(term().textarea, [new File(['x'], 'notes.txt', { type: 'text/plain' })])
    await vi.waitFor(() => expect(w.find('[data-terminal-note]').text()).toBe('Only images can be dropped here.'))
    expect(upload).not.toHaveBeenCalled()
    expect(term().pasted).toEqual([])
  })

  test('a refusal of the server is shown; a read-only or ended terminal takes nothing', async () => {
    const session = sessionWith()
    const { ApiError } = await import('../../api/client')
    const upload = vi.spyOn(session.client, 'upload').mockRejectedValue(new ApiError(422, 'validation', 'not an image omo takes'))
    const w = view({}, session)
    drop(term().textarea, [png()])
    await vi.waitFor(() => expect(w.find('[data-terminal-note]').text()).toBe('shot one.png: not an image omo takes'))
    expect(term().pasted).toEqual([])
    upload.mockClear()
    view({ readonly: true }, session)
    drop(term().textarea, [png()])
    const ended = view({}, session)
    conn().o.onState('ended')
    await nextTick()
    drop(term().textarea, [png()])
    await nextTick()
    expect(upload).not.toHaveBeenCalled()
    expect(ended.find('[data-terminal-note]').exists()).toBe(false)
  })
})

describe('agent terminals', () => {
  const AGENT = '/api/agents/a1/terminal'
  const agent = (props: Record<string, unknown> = {}, session: Session = sessionWith()) => view({ src: AGENT, ...props }, session)
  const png = () => new File([new Uint8Array([0x89, 0x50, 0x4e, 0x47])], 'shot.png', { type: 'image/png' })
  const keyEv = (init: KeyboardEventInit) => new KeyboardEvent('keydown', { cancelable: true, ...init })

  test('starts read-only: keys and text input do nothing, and it says so', async () => {
    const w = agent()
    const toggle = w.find('[data-terminal-mode]')
    expect(toggle.text()).toBe('Read only')
    expect(toggle.attributes('data-writable')).toBeUndefined()
    expect(term().keyHandler!(keyEv({ key: 'a' }))).toBe(false)
    expect(term().keyHandler!(keyEv({ key: 'Enter' }))).toBe(false)
    expect(term().keyHandler!(keyEv({ key: 'Enter', shiftKey: true }))).toBe(false)
    expect(conn().send).not.toHaveBeenCalled()
    for (const name of ['keypress', 'input', 'beforeinput', 'compositionend']) {
      const reached = vi.fn()
      term().textarea.addEventListener(name, reached)
      term().textarea.dispatchEvent(new Event(name, { bubbles: true, cancelable: true }))
      expect(reached).not.toHaveBeenCalled()
    }
    expect(w.find('.tv__screen').attributes('data-readonly')).toBeDefined()
  })

  test('the mouse keeps working: reports the emulator makes for the mouse reach the program', () => {
    agent()
    term().dataHandler!('\x1b[<0;10;5M')
    term().dataHandler!('\x1b[<64;10;5M')
    expect(conn().send).toHaveBeenCalledWith('\x1b[<0;10;5M')
    expect(conn().send).toHaveBeenCalledWith('\x1b[<64;10;5M')
    expect(term().options.disableStdin).toBeFalsy()
  })

  test('copying and F6 still work while read-only', () => {
    agent()
    term().selection = 'abc'
    const copy = keyEv({ key: 'C', code: 'KeyC', ctrlKey: true, shiftKey: true })
    expect(term().keyHandler!(copy)).toBe(false)
    expect(copy.defaultPrevented).toBe(true)
  })

  test('a paste and dropped files are refused while read-only', async () => {
    const session = sessionWith()
    const upload = vi.spyOn(session.client, 'upload')
    agent({}, session)
    const paste = new Event('paste', { bubbles: true, cancelable: true })
    Object.defineProperty(paste, 'clipboardData', { value: { getData: () => 'hello' } })
    term().textarea.dispatchEvent(paste)
    const drop = new Event('drop', { bubbles: true, cancelable: true })
    Object.defineProperty(drop, 'dataTransfer', { value: { files: [png()], types: ['Files'], getData: () => '' } })
    term().textarea.dispatchEvent(drop)
    await nextTick()
    expect(term().pasted).toEqual([])
    expect(upload).not.toHaveBeenCalled()
  })

  test('the toggle makes it writable, with the state in words, and back', async () => {
    const w = agent()
    await w.find('[data-terminal-mode]').trigger('click')
    expect(w.find('[data-terminal-mode]').text()).toBe('Writable')
    expect(w.find('[data-terminal-mode]').attributes('data-writable')).toBeDefined()
    expect(term().keyHandler!(keyEv({ key: 'a' }))).toBe(true)
    term().dataHandler!('a')
    expect(conn().send).toHaveBeenCalledWith('a')
    const paste = new Event('paste', { bubbles: true, cancelable: true })
    Object.defineProperty(paste, 'clipboardData', { value: { getData: () => 'hello' } })
    term().textarea.dispatchEvent(paste)
    expect(term().pasted).toEqual(['hello'])
    await w.find('[data-terminal-mode]').trigger('click')
    expect(w.find('[data-terminal-mode]').text()).toBe('Read only')
    expect(term().keyHandler!(keyEv({ key: 'a' }))).toBe(false)
  })

  test('the choice is remembered for that terminal in the browser session; a new terminal starts read-only', async () => {
    const first = agent()
    await first.find('[data-terminal-mode]').trigger('click')
    first.unmount()
    const again = agent()
    expect(again.find('[data-terminal-mode]').text()).toBe('Writable')
    const other = agent({ src: '/api/agents/a2/terminal' })
    expect(other.find('[data-terminal-mode]').text()).toBe('Read only')
  })

  test('a user shell has no toggle, no draft box, and stays writable', () => {
    const w = view({ src: '/api/terminals/t/socket' })
    expect(w.find('[data-terminal-mode]').exists()).toBe(false)
    expect(w.find('[data-terminal-draft]').exists()).toBe(false)
    expect(term().keyHandler!(keyEv({ key: 'a' }))).toBe(true)
  })

  test('the draft box sends whatever the toggle says, as one message, and empties on success', async () => {
    const w = agent()
    conn().o.onState('live')
    await w.find('[data-draft-field]').setValue('first line\nsecond line')
    await w.find('[data-draft-send]').trigger('click')
    await vi.waitFor(() => expect(conn().sendMessage).toHaveBeenCalledWith(['first line\nsecond line']))
    await vi.waitFor(() => expect((w.find('[data-draft-field]').element as HTMLTextAreaElement).value).toBe(''))
    expect(conn().send).not.toHaveBeenCalled()
  })

  test('a failed send keeps the draft and shows the reason', async () => {
    const w = agent()
    conn().sendMessage.mockRejectedValueOnce(new Error('Terminal disconnected.'))
    await w.find('[data-draft-field]').setValue('keep me')
    await w.find('[data-draft-send]').trigger('click')
    await vi.waitFor(() => expect(w.find('[data-draft-error]').text()).toBe('Terminal disconnected.'))
    expect((w.find('[data-draft-field]').element as HTMLTextAreaElement).value).toBe('keep me')
  })

  test('output arriving above does not touch the draft or its focus', async () => {
    const w = agent()
    const field = w.find('[data-draft-field]').element as HTMLTextAreaElement
    field.focus()
    await w.find('[data-draft-field]').setValue('half written')
    conn().o.onReplayStart()
    conn().o.onData(new TextEncoder().encode('output\r\n'), true)
    conn().o.onData(new TextEncoder().encode('more output\r\n'), false)
    conn().o.onState('live')
    await nextTick()
    expect(field.value).toBe('half written')
    expect(document.activeElement).toBe(field)
  })

  test('the draft box is gone once the process has ended', async () => {
    const w = agent()
    expect(w.find('[data-draft-field]').exists()).toBe(true)
    conn().o.onState('ended')
    await nextTick()
    expect(w.find('[data-draft-field]').exists()).toBe(false)
    expect(w.find('[data-terminal-mode]').exists()).toBe(false)
  })

  test('an output-only terminal (the readonly prop) has no draft box', () => {
    expect(agent({ readonly: true }).find('[data-draft-field]').exists()).toBe(false)
  })
})


test('a single left click on an agent screen focuses the draft, while dragging and scrolling preserve selection and focus', async () => {
  const w = view({ src: '/api/agents/a1/terminal' })
  const screen = w.find('.tv__screen')
  const field = w.find('[data-draft-field]').element as HTMLTextAreaElement
  await screen.trigger('pointerdown', { button: 0, clientX: 10, clientY: 10 })
  await screen.trigger('pointerup', { button: 0, clientX: 10, clientY: 10 })
  await screen.trigger('click', { button: 0, detail: 1 })
  expect(document.activeElement).toBe(field)
  field.blur()
  await screen.trigger('pointerdown', { button: 0, clientX: 10, clientY: 10 })
  await screen.trigger('pointermove', { clientX: 30, clientY: 10 })
  term().selection = 'selected output'
  await screen.trigger('pointerup', { button: 0, clientX: 30, clientY: 10 })
  await screen.trigger('click', { button: 0, detail: 1 })
  expect(document.activeElement).not.toBe(field)
  expect(term().getSelection()).toBe('selected output')
  const wheel = new Event('wheel', { bubbles: true, cancelable: true })
  screen.element.dispatchEvent(wheel)
  expect(wheel.defaultPrevented).toBe(false)
  expect(document.activeElement).not.toBe(field)
  await screen.trigger('pointerdown', { button: 2 })
  await screen.trigger('click', { button: 2, detail: 1 })
  expect(document.activeElement).not.toBe(field)
})

test('draft sending blocks direct typing and pasting until the message finishes', async () => {
  const w = view({ src: '/api/agents/a1/terminal' })
  await w.find('[data-terminal-mode]').trigger('click')
  let release!: () => void
  conn().sendMessage.mockImplementation(() => new Promise<void>((resolve) => { release = resolve }))
  await w.find('[data-draft-field]').setValue('hello')
  await w.find('[data-draft-send]').trigger('click')
  const key = new KeyboardEvent('keydown', { key: 'a' })
  expect(term().keyHandler!(key)).toBe(false)
  const ev = new Event('paste', { bubbles: true, cancelable: true })
  Object.defineProperty(ev, 'clipboardData', { value: { getData: () => 'during' } })
  term().textarea.dispatchEvent(ev)
  expect(term().pasted).toEqual([])
  release()
  await vi.waitFor(() => expect(term().keyHandler!(key)).toBe(true))
})

test('clicking a Writable agent screen leaves focus with the terminal', async () => {
  const w = view({ src: '/api/agents/a1/terminal' })
  await w.find('[data-terminal-mode]').trigger('click')
  const field = w.find('[data-draft-field]').element as HTMLTextAreaElement
  field.focus()
  const screen = w.find('.tv__screen')
  await screen.trigger('pointerdown', { button: 0, clientX: 10, clientY: 10 })
  // The xterm test double does not handle pointers; simulate its normal focus.
  term().textarea.focus()
  await screen.trigger('pointerup', { button: 0, clientX: 10, clientY: 10 })
  await screen.trigger('click', { button: 0, detail: 1 })
  expect(document.activeElement).toBe(term().textarea)
})
