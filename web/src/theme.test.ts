import { afterEach, describe, expect, test } from 'vitest'
import bootSource from './theme-boot.js?raw'
import { THEME_KEY, applyTheme, readTheme, type ThemeChoice } from './theme'

function memoryStorage(initial: Record<string, string> = {}): Storage {
  const data = new Map(Object.entries(initial))
  return {
    get length() {
      return data.size
    },
    clear: () => data.clear(),
    getItem: (k) => data.get(k) ?? null,
    key: (i) => [...data.keys()][i] ?? null,
    removeItem: (k) => void data.delete(k),
    setItem: (k, v) => void data.set(k, String(v)),
  }
}

function brokenStorage(): Storage {
  const fail = () => {
    throw new Error('blocked')
  }
  return { length: 0, clear: fail, getItem: fail, key: fail, removeItem: fail, setItem: fail }
}

describe('theme', () => {
  afterEach(() => {
    document.documentElement.removeAttribute('data-theme')
    localStorage.clear()
  })

  test('applyTheme sets data-theme for dark and light, removes it for system, and remembers the choice', () => {
    const root = document.createElement('html')
    const store = memoryStorage()

    applyTheme('dark', root, store)
    expect(root.getAttribute('data-theme')).toBe('dark')
    expect(readTheme(store)).toBe('dark')

    applyTheme('light', root, store)
    expect(root.getAttribute('data-theme')).toBe('light')
    expect(store.getItem(THEME_KEY)).toBe('light')

    applyTheme('system', root, store)
    expect(root.hasAttribute('data-theme')).toBe(false)
    expect(readTheme(store)).toBe('system')
  })

  test('readTheme falls back to system for unknown values and unusable storage', () => {
    expect(readTheme(memoryStorage())).toBe('system')
    expect(readTheme(memoryStorage({ [THEME_KEY]: 'sepia' }))).toBe('system')
    expect(readTheme(brokenStorage())).toBe('system')
  })

  test('applyTheme ignores storage errors', () => {
    const root = document.createElement('html')
    expect(() => applyTheme('light', root, brokenStorage())).not.toThrow()
    expect(root.getAttribute('data-theme')).toBe('light')
  })

  test.each<[string | null, ThemeChoice]>([
    ['dark', 'dark'],
    ['light', 'light'],
    ['system', 'system'],
    ['sepia', 'system'],
    [null, 'system'],
  ])('the boot script applies a stored %s the same way applyTheme does', (stored, expected) => {
    if (stored !== null) localStorage.setItem(THEME_KEY, stored)
    new Function(bootSource)()
    const expectedRoot = document.createElement('html')
    applyTheme(expected, expectedRoot)
    expect(document.documentElement.getAttribute('data-theme')).toBe(expectedRoot.getAttribute('data-theme'))
  })
})
