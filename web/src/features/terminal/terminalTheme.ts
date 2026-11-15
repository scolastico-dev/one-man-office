import type { ITheme } from '@xterm/xterm'

const ANSI = [
  'black', 'red', 'green', 'yellow', 'blue', 'magenta', 'cyan', 'white',
  'bright-black', 'bright-red', 'bright-green', 'bright-yellow', 'bright-blue', 'bright-magenta', 'bright-cyan', 'bright-white',
] as const

const KEYS = [
  'black', 'red', 'green', 'yellow', 'blue', 'magenta', 'cyan', 'white',
  'brightBlack', 'brightRed', 'brightGreen', 'brightYellow', 'brightBlue', 'brightMagenta', 'brightCyan', 'brightWhite',
] as const

/** The terminal's colours, read from the `--omo-term-*` and `--omo-ansi-*` tokens as they apply to `root`. */
export function readTerminalTheme(root: HTMLElement): ITheme {
  const style = getComputedStyle(root)
  const token = (name: string) => style.getPropertyValue(`--omo-${name}`).trim()
  const theme: ITheme = {
    background: token('term-bg'),
    foreground: token('term-fg'),
    cursor: token('term-cursor'),
    cursorAccent: token('term-bg'),
    selectionBackground: token('term-selection'),
  }
  ANSI.forEach((name, i) => {
    theme[KEYS[i]!] = token(`ansi-${name}`)
  })
  return theme
}

/** The font family of the design system. */
export function readTerminalFont(root: HTMLElement): string {
  return getComputedStyle(root).getPropertyValue('--omo-font').trim()
}

/** Calls `cb` when the theme changes (the `data-theme` attribute or the system colour scheme). Returns the stop function. */
export function watchTheme(cb: () => void): () => void {
  const observer = new MutationObserver(cb)
  observer.observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })
  const media = typeof window.matchMedia === 'function' ? window.matchMedia('(prefers-color-scheme: dark)') : null
  media?.addEventListener('change', cb)
  return () => {
    observer.disconnect()
    media?.removeEventListener('change', cb)
  }
}
