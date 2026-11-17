import { afterEach, describe, expect, test, vi } from 'vitest'
import { readTerminalFont, readTerminalTheme, watchTheme } from './terminalTheme'

const NAMES = [
  'black', 'red', 'green', 'yellow', 'blue', 'magenta', 'cyan', 'white',
  'bright-black', 'bright-red', 'bright-green', 'bright-yellow', 'bright-blue', 'bright-magenta', 'bright-cyan', 'bright-white',
]

afterEach(() => {
  document.documentElement.removeAttribute('style')
  document.documentElement.removeAttribute('data-theme')
})

function setTokens(prefix: string) {
  const s = document.documentElement.style
  s.setProperty('--omo-term-bg', `${prefix}bg`)
  s.setProperty('--omo-term-fg', `${prefix}fg`)
  s.setProperty('--omo-term-cursor', `${prefix}cursor`)
  s.setProperty('--omo-term-selection', `${prefix}sel`)
  for (const n of NAMES) s.setProperty(`--omo-ansi-${n}`, `${prefix}${n}`)
  s.setProperty('--omo-font', 'Mono, monospace')
}

describe('terminal theme', () => {
  test('reads every terminal and ANSI token', () => {
    setTokens('a-')
    const t = readTerminalTheme(document.documentElement)
    expect(t.background).toBe('a-bg')
    expect(t.foreground).toBe('a-fg')
    expect(t.cursor).toBe('a-cursor')
    expect(t.selectionBackground).toBe('a-sel')
    expect(t.black).toBe('a-black')
    expect(t.brightWhite).toBe('a-bright-white')
    expect(t.brightMagenta).toBe('a-bright-magenta')
    for (const k of ['red', 'green', 'yellow', 'blue', 'magenta', 'cyan', 'white', 'brightBlack', 'brightRed', 'brightGreen', 'brightYellow', 'brightBlue', 'brightCyan']) {
      expect(t[k as keyof typeof t], k).toBeTruthy()
    }
    expect(readTerminalFont(document.documentElement)).toBe('Mono, monospace')
  })

  test('follows a theme change', async () => {
    const cb = vi.fn()
    const stop = watchTheme(cb)
    document.documentElement.setAttribute('data-theme', 'light')
    await new Promise((r) => setTimeout(r, 0))
    expect(cb).toHaveBeenCalled()
    cb.mockClear()
    stop()
    document.documentElement.setAttribute('data-theme', 'dark')
    await new Promise((r) => setTimeout(r, 0))
    expect(cb).not.toHaveBeenCalled()
  })
})
