import { afterEach, describe, expect, test, vi } from 'vitest'
import { MAX_OSC52_TEXT, clipboardKey, execCopy, parseOsc52, writeClipboard } from './terminalClipboard'

const b64 = (text: string): string => {
  const bytes = new TextEncoder().encode(text)
  let bin = ''
  for (const b of bytes) bin += String.fromCharCode(b)
  return btoa(bin)
}

describe('parseOsc52', () => {
  test('a set of the clipboard gives the decoded text', () => {
    expect(parseOsc52(`c;${b64('hello world')}`)).toEqual({ kind: 'set', text: 'hello world' })
    // No target named is the default target; p, s and the cut buffers are taken too.
    expect(parseOsc52(`;${b64('x')}`)).toEqual({ kind: 'set', text: 'x' })
    expect(parseOsc52(`ps0;${b64('y')}`)).toEqual({ kind: 'set', text: 'y' })
  })

  test('UTF-8 text survives the round trip', () => {
    const text = 'Grüße – 日本語 🎉\nline two'
    expect(parseOsc52(`c;${b64(text)}`)).toEqual({ kind: 'set', text })
  })

  test('unpadded base64 is read', () => {
    expect(parseOsc52('c;YQ')).toEqual({ kind: 'set', text: 'a' })
  })

  test('a read query is never answered', () => {
    expect(parseOsc52('c;?')).toEqual({ kind: 'ignored', why: 'query' })
    expect(parseOsc52(';?')).toEqual({ kind: 'ignored', why: 'query' })
  })

  test('an empty payload (clear) leaves the clipboard alone', () => {
    expect(parseOsc52('c;')).toEqual({ kind: 'ignored', why: 'clear' })
  })

  test('malformed payloads are ignored', () => {
    for (const data of ['', 'no-semicolon', 'c;not base64!', 'c;Y', 'xz;YQ==', 'c;YQ==?', `c;${btoa('\xff\xfe')}`]) {
      expect(parseOsc52(data), data).toEqual({ kind: 'ignored', why: 'malformed' })
    }
  })

  test('more than the limit is refused before it is decoded', () => {
    const atLimit = 'a'.repeat(MAX_OSC52_TEXT)
    expect(parseOsc52(`c;${b64(atLimit)}`)).toEqual({ kind: 'set', text: atLimit })
    expect(parseOsc52(`c;${b64(atLimit + 'abc')}`)).toEqual({ kind: 'ignored', why: 'too-large' })
  })
})

describe('execCopy', () => {
  // happy-dom has no copy command: each test gives the document one.
  const setCommand = (fn: (cmd: string) => boolean) =>
    Object.defineProperty(document, 'execCommand', { value: fn, configurable: true, writable: true })
  afterEach(() => {
    delete (document as { execCommand?: unknown }).execCommand
  })

  test('puts the text into the copy event the command fires and keeps it from the page', () => {
    const seen: string[] = []
    const later = vi.fn()
    document.addEventListener('copy', later)
    setCommand((cmd) => {
      expect(cmd).toBe('copy')
      const data = new Map<string, string>()
      const ev = new Event('copy', { bubbles: true, cancelable: true }) as ClipboardEvent
      Object.defineProperty(ev, 'clipboardData', { value: { setData: (t: string, v: string) => data.set(t, v) } })
      document.body.dispatchEvent(ev)
      seen.push(data.get('text/plain') ?? '')
      return ev.defaultPrevented
    })
    expect(execCopy('copied text')).toBe(true)
    expect(seen).toEqual(['copied text'])
    expect(later).not.toHaveBeenCalled()
    document.removeEventListener('copy', later)
  })

  test('false when the browser refuses the command', () => {
    setCommand(() => false)
    expect(execCopy('x')).toBe(false)
    setCommand(() => {
      throw new Error('denied')
    })
    expect(execCopy('x')).toBe(false)
  })
})

describe('writeClipboard', () => {
  afterEach(() => vi.restoreAllMocks())

  test('the copy command first, the clipboard API when it is refused', async () => {
    const writeText = vi.fn(async () => {})
    expect(await writeClipboard('a', { execCopy: () => true, clipboard: { writeText } })).toBe(true)
    expect(writeText).not.toHaveBeenCalled()
    expect(await writeClipboard('b', { execCopy: () => false, clipboard: { writeText } })).toBe(true)
    expect(writeText).toHaveBeenCalledWith('b')
  })

  test('false when neither works (no clipboard API outside a secure context)', async () => {
    expect(await writeClipboard('a', { execCopy: () => false, clipboard: undefined })).toBe(false)
    const writeText = vi.fn(async () => {
      throw new DOMException('not allowed', 'NotAllowedError')
    })
    expect(await writeClipboard('a', { execCopy: () => false, clipboard: { writeText } })).toBe(false)
  })
})

describe('clipboardKey', () => {
  const key = (init: KeyboardEventInit & { type?: string }) => new KeyboardEvent(init.type ?? 'keydown', init)

  test('Ctrl+Shift+C and Ctrl+Insert copy', () => {
    expect(clipboardKey(key({ key: 'C', code: 'KeyC', ctrlKey: true, shiftKey: true }), false)).toBe('copy')
    expect(clipboardKey(key({ key: 'Insert', code: 'Insert', ctrlKey: true }), false)).toBe('copy')
  })

  test('Ctrl+C copies only with a selection and then clears it; without one it is the interrupt', () => {
    expect(clipboardKey(key({ key: 'c', code: 'KeyC', ctrlKey: true }), true)).toBe('copy-clear')
    expect(clipboardKey(key({ key: 'c', code: 'KeyC', ctrlKey: true }), false)).toBe(null)
    // Another layout: the key named c counts, wherever it sits.
    expect(clipboardKey(key({ key: 'c', code: 'KeyI', ctrlKey: true }), true)).toBe('copy-clear')
  })

  test('Ctrl+V, Ctrl+Shift+V and Shift+Insert paste', () => {
    expect(clipboardKey(key({ key: 'v', code: 'KeyV', ctrlKey: true }), false)).toBe('paste')
    expect(clipboardKey(key({ key: 'V', code: 'KeyV', ctrlKey: true, shiftKey: true }), false)).toBe('paste')
    expect(clipboardKey(key({ key: 'Insert', code: 'Insert', shiftKey: true }), false)).toBe('paste')
  })

  test('other keys and other modifiers are the program’s', () => {
    expect(clipboardKey(key({ key: 'c' }), true)).toBe(null)
    expect(clipboardKey(key({ key: 'c', ctrlKey: true, altKey: true }), true)).toBe(null)
    expect(clipboardKey(key({ key: 'v', ctrlKey: true, altKey: true }), false)).toBe(null)
    expect(clipboardKey(key({ key: 'Insert' }), false)).toBe(null)
    expect(clipboardKey(key({ key: 'x', ctrlKey: true }), true)).toBe(null)
  })

  test('on a Mac the system keys (Cmd) copy and paste; Ctrl stays the program’s', () => {
    expect(clipboardKey(key({ key: 'c', ctrlKey: true }), true, true)).toBe(null)
    expect(clipboardKey(key({ key: 'v', ctrlKey: true }), false, true)).toBe(null)
  })
})
