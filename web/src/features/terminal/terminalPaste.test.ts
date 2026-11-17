import { afterEach, describe, expect, test, vi } from 'vitest'
import { cleanPaste, guardPaste } from './terminalPaste'

const C0 = Array.from({ length: 0x20 }, (_, i) => String.fromCharCode(i))
const C1 = Array.from({ length: 0x20 }, (_, i) => String.fromCharCode(0x80 + i))

describe('cleanPaste', () => {
  test('removes ESC and CSI, so a paste cannot end bracketed paste early', () => {
    expect(cleanPaste('echo a\x1b[201~\necho INJECTED')).toBe('echo a[201~\necho INJECTED')
    expect(cleanPaste('a\x9b201~b')).toBe('a201~b')
  })

  test('keeps tab, newline and carriage return, removes every other C0, DEL and C1 character', () => {
    const kept = ['\t', '\n', '\r']
    for (const c of C0) expect(cleanPaste(`a${c}b`), c.charCodeAt(0).toString(16)).toBe(kept.includes(c) ? `a${c}b` : 'ab')
    for (const c of C1) expect(cleanPaste(`a${c}b`), c.charCodeAt(0).toString(16)).toBe('ab')
    expect(cleanPaste('a\x7fb')).toBe('ab')
  })

  test('leaves ordinary text, other scripts and emoji alone', () => {
    const text = 'ls -la ~/x | grep "ü" && echo 日本 🚀 ' + ' '
    expect(cleanPaste(text)).toBe(text)
  })
})

describe('guardPaste', () => {
  let el: HTMLElement
  let inner: HTMLTextAreaElement
  afterEach(() => {
    el?.remove()
  })

  function setup() {
    el = document.createElement('div')
    inner = document.createElement('textarea')
    el.appendChild(inner)
    document.body.appendChild(el)
    const pasted: string[] = []
    const reached = vi.fn()
    inner.addEventListener('paste', reached)
    inner.addEventListener('drop', reached)
    const files: File[][] = []
    const stop = guardPaste(el, (t) => pasted.push(t), (f) => files.push(f))
    return { pasted, reached, stop, files }
  }

  function withFiles(type: string, list: File[], text: string, prop: 'clipboardData' | 'dataTransfer'): Event {
    const ev = new Event(type, { bubbles: true, cancelable: true })
    Object.defineProperty(ev, prop, { value: { files: list, types: ['Files'], getData: (f: string) => (f === 'text/plain' ? text : '') } })
    return ev
  }

  function withText(type: string, text: string, prop: 'clipboardData' | 'dataTransfer'): Event {
    const ev = new Event(type, { bubbles: true, cancelable: true })
    Object.defineProperty(ev, prop, { value: { getData: (f: string) => (f === 'text/plain' ? text : '') } })
    return ev
  }

  test('a paste anywhere inside reaches the terminal cleaned and never the emulator raw', () => {
    const { pasted, reached } = setup()
    const ev = withText('paste', 'echo a\x1b[201~\necho INJECTED', 'clipboardData')
    inner.dispatchEvent(ev)
    expect(pasted).toEqual(['echo a[201~\necho INJECTED'])
    expect(ev.defaultPrevented).toBe(true)
    expect(reached).not.toHaveBeenCalled()
  })

  test('dropped text is pasted the same way', () => {
    const { pasted, reached } = setup()
    const over = new Event('dragover', { bubbles: true, cancelable: true })
    inner.dispatchEvent(over)
    expect(over.defaultPrevented).toBe(true)
    const ev = withText('drop', 'rm\x1b[201~x', 'dataTransfer')
    inner.dispatchEvent(ev)
    expect(pasted).toEqual(['rm[201~x'])
    expect(ev.defaultPrevented).toBe(true)
    expect(reached).not.toHaveBeenCalled()
  })

  test('an empty paste sends nothing; the returned function removes the guard', () => {
    const { pasted, stop } = setup()
    inner.dispatchEvent(withText('paste', '\x1b', 'clipboardData'))
    expect(pasted).toEqual([])
    stop()
    inner.dispatchEvent(withText('paste', 'x', 'clipboardData'))
    expect(pasted).toEqual([])
  })

  test('dropped files go to the file handler, whatever text comes with them', () => {
    const { pasted, files, reached } = setup()
    const png = new File(['x'], 'a.png', { type: 'image/png' })
    const ev = withFiles('drop', [png], 'file:///tmp/a.png', 'dataTransfer')
    inner.dispatchEvent(ev)
    expect(files).toEqual([[png]])
    expect(pasted).toEqual([])
    expect(ev.defaultPrevented).toBe(true)
    expect(reached).not.toHaveBeenCalled()
  })

  test('a paste of files without text goes to the file handler; with text the text is pasted', () => {
    const { pasted, files } = setup()
    const png = new File(['x'], 'image.png', { type: 'image/png' })
    inner.dispatchEvent(withFiles('paste', [png], '', 'clipboardData'))
    expect(files).toEqual([[png]])
    inner.dispatchEvent(withFiles('paste', [png], 'some text', 'clipboardData'))
    expect(pasted).toEqual(['some text'])
    expect(files).toHaveLength(1)
  })
})
