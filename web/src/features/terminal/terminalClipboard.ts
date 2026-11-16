// The clipboard of a terminal view. Programs that hold the mouse (Claude
// Code, Codex, Gemini, tmux, vim) select in their own screen and hand the
// selection to the terminal with OSC 52; the terminal's own selection (Shift
// and drag while a program holds the mouse) is copied with the keys below.
//
// Writing the clipboard: `document.execCommand('copy')` works on every
// address, also over plain HTTP where `navigator.clipboard` does not exist,
// but only while the browser counts a user gesture (inside a key or pointer
// handler; Chromium also for a few seconds after one). The clipboard API is
// the second way, in a secure context only. When both are refused the caller
// keeps the text and copies it on the next gesture.

/** The most text a program may put into the clipboard at once, in bytes. */
export const MAX_OSC52_TEXT = 1024 * 1024

export type Osc52 =
  | { kind: 'set'; text: string }
  /** `query`: a read of the clipboard, never answered; `clear`: an empty payload, the clipboard is left alone. */
  | { kind: 'ignored'; why: 'query' | 'clear' | 'malformed' | 'too-large' }

/** The longest base64 payload that can hold MAX_OSC52_TEXT bytes. */
const MAX_PAYLOAD = Math.ceil(MAX_OSC52_TEXT / 3) * 4

/**
 * Reads the data of an OSC 52 sequence (`Pc;Pd`, what follows `52;`): the
 * targets (c, p, q, s, 0 to 7 or none) and base64 text. Only a set is ever
 * taken; a query (`?`) would let any program in the terminal read the
 * user's clipboard and is never answered.
 */
export function parseOsc52(data: string): Osc52 {
  const semi = data.indexOf(';')
  if (semi < 0 || !/^[cpqs0-7]*$/.test(data.slice(0, semi))) return { kind: 'ignored', why: 'malformed' }
  const payload = data.slice(semi + 1)
  if (payload === '?') return { kind: 'ignored', why: 'query' }
  if (payload === '') return { kind: 'ignored', why: 'clear' }
  if (payload.length > MAX_PAYLOAD + 2) return { kind: 'ignored', why: 'too-large' }
  if (!/^[A-Za-z0-9+/]+={0,2}$/.test(payload) || payload.replace(/=+$/, '').length % 4 === 1) {
    return { kind: 'ignored', why: 'malformed' }
  }
  let binary: string
  try {
    binary = atob(payload)
  } catch {
    return { kind: 'ignored', why: 'malformed' }
  }
  if (binary.length > MAX_OSC52_TEXT) return { kind: 'ignored', why: 'too-large' }
  const bytes = Uint8Array.from(binary, (c) => c.charCodeAt(0))
  try {
    return { kind: 'set', text: new TextDecoder('utf-8', { fatal: true }).decode(bytes) }
  } catch {
    return { kind: 'ignored', why: 'malformed' }
  }
}

/**
 * Copies text with the browser's copy command: the copy event it fires gets
 * the text, and no other listener (the emulator's own copies its selection)
 * sees it. Nothing has to be selected and the focus stays where it is.
 * False when the browser refuses (no user gesture).
 */
export function execCopy(text: string): boolean {
  let wrote = false
  const onCopy = (ev: Event) => {
    const data = (ev as ClipboardEvent).clipboardData
    if (!data) return
    data.setData('text/plain', text)
    ev.preventDefault()
    ev.stopImmediatePropagation()
    wrote = true
  }
  window.addEventListener('copy', onCopy, true)
  try {
    return document.execCommand('copy') && wrote
  } catch {
    return false
  } finally {
    window.removeEventListener('copy', onCopy, true)
  }
}

export interface ClipboardEnv {
  execCopy(text: string): boolean
  /** `navigator.clipboard`; missing outside a secure context. */
  clipboard: Pick<Clipboard, 'writeText'> | undefined
}

const browserEnv = (): ClipboardEnv => ({
  execCopy,
  clipboard: typeof navigator !== 'undefined' && navigator.clipboard ? navigator.clipboard : undefined,
})

/**
 * Writes text to the system clipboard: the copy command first (it must run
 * before anything is awaited, while a gesture still counts), then the
 * clipboard API. False when the browser refused both.
 */
export async function writeClipboard(text: string, env: ClipboardEnv = browserEnv()): Promise<boolean> {
  if (env.execCopy(text)) return true
  if (!env.clipboard) return false
  try {
    await env.clipboard.writeText(text)
    return true
  } catch {
    return false
  }
}

/**
 * What a key does to the clipboard in a terminal, or null when it belongs to
 * the program. `copy`: Ctrl+Shift+C and Ctrl+Insert copy the terminal's
 * selection (else text a program handed over and the browser did not take);
 * `copy-clear`: Ctrl+C copies the selection and clears it, so the next
 * Ctrl+C is the interrupt again (without a selection it is the interrupt);
 * `paste`: Ctrl+V, Ctrl+Shift+V and Shift+Insert are the browser's paste.
 * On a Mac Cmd+C and Cmd+V are the browser's own and Ctrl stays the
 * program's.
 */
export function clipboardKey(ev: KeyboardEvent, hasSelection: boolean, mac = isMac()): 'copy' | 'copy-clear' | 'paste' | null {
  if (mac || ev.altKey || ev.metaKey) return null
  if (ev.key === 'Insert') {
    if (ev.ctrlKey && !ev.shiftKey) return 'copy'
    if (ev.shiftKey && !ev.ctrlKey) return 'paste'
    return null
  }
  if (!ev.ctrlKey) return null
  const letter = /^[a-z]$/i.test(ev.key) ? ev.key.toLowerCase() : ev.code === 'KeyC' ? 'c' : ev.code === 'KeyV' ? 'v' : ''
  if (letter === 'v') return 'paste'
  if (letter !== 'c') return null
  if (ev.shiftKey) return 'copy'
  return hasSelection ? 'copy-clear' : null
}

function isMac(): boolean {
  return typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent)
}
