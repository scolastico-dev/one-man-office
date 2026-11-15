import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import { createTokenStore } from '../../auth/token'
import { createSession, SESSION, type Session } from '../../session'
import { FakeOmo } from '../../testing/fakeOmo'

const h = vi.hoisted(() => ({
  terms: [] as any[],
  conns: [] as any[],
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
    disposed = false
    written: Uint8Array[] = []
    lineText = ''
    buffer = {
      active: {
        getLine: () => ({ translateToString: (_trim: boolean, from = 0, to?: number) => this.lineText.slice(from, to) }),
      },
    }
    parser = {
      registerOscHandler: (code: number, cb: () => boolean) => {
        this.osc.push(code)
        expect(cb()).toBe(true)
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
    focus = vi.fn()
    dispose() {
      this.disposed = true
    }
  }
  return { Terminal }
})
vi.mock('@xterm/addon-fit', () => ({ FitAddon: class { proposeDimensions() { return { rows: 30, cols: 100 } } } }))
vi.mock('@xterm/xterm/css/xterm.css', () => ({}))
vi.mock('./terminalSocket', () => ({
  createTerminalConnection: (o: any) => {
    const c = { o, send: vi.fn(), resize: vi.fn(), reconnect: vi.fn(), dispose: vi.fn() }
    h.conns.push(c)
    return c
  },
}))

import TerminalView from './TerminalView.vue'

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
  h.terms.length = 0
  h.conns.length = 0
  window.localStorage.clear()
})
afterEach(() => {
  live.splice(0).forEach((w) => w.unmount())
  vi.useRealTimers()
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
    expect(w.find('button.omo-btn').exists()).toBe(false)
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

  test('output is written to the emulator only, never into the page', () => {
    const w = view()
    conn().o.onData(new TextEncoder().encode('<img src=x onerror=alert(1)>'))
    expect(term().written).toHaveLength(1)
    expect(w.find('img').exists()).toBe(false)
  })

  test('the screen reader toggle is remembered and reaches the emulator', async () => {
    const w = view()
    expect(term().options.screenReaderMode).toBe(false)
    await w.find('[role="switch"]').trigger('click')
    expect(term().options.screenReaderMode).toBe(true)
    expect(window.localStorage.getItem('omo.terminal.screenReader')).toBe('1')
    expect(view().vm).toBeTruthy()
    expect(term().options.screenReaderMode).toBe(true)
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

describe('terminal imports', () => {
  test('no file outside features/terminal imports TerminalView.vue directly', () => {
    const files = import.meta.glob(['/src/**/*.{vue,ts}', '!/src/features/terminal/**'], { query: '?raw', import: 'default', eager: true }) as Record<string, string>
    const offenders = Object.entries(files)
      .filter(([, text]) => /TerminalView(\.vue)?['"]/.test(text))
      .map(([file]) => file)
    expect(Object.keys(files).length).toBeGreaterThan(50)
    expect(offenders).toEqual([])
  })
})
