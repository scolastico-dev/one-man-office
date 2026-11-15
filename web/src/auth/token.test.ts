import { afterEach, describe, expect, it, vi } from 'vitest'
import { captureToken, forgetToken, parseAccess, readToken, saveToken } from './token'

const TOKEN = 'abababababababababababababababababababababababababababababababab'

function fakeLocation(hash: string, path = '/123', search = '?story=5'): Location {
  return { hash, pathname: path, search } as Location
}

function fakeStore(): Storage {
  const m = new Map<string, string>()
  return {
    getItem: (k: string) => m.get(k) ?? null,
    setItem: (k: string, v: string) => void m.set(k, v),
    removeItem: (k: string) => void m.delete(k),
  } as unknown as Storage
}

afterEach(() => vi.restoreAllMocks())

describe('captureToken', () => {
  it('stores a 64-hex fragment and removes the fragment with replaceState before returning', () => {
    const store = fakeStore()
    const replaceState = vi.fn()
    captureToken(fakeLocation('#' + TOKEN), { state: { a: 1 }, replaceState } as unknown as History, store)
    expect(readToken(store)).toBe(TOKEN)
    expect(replaceState).toHaveBeenCalledTimes(1)
    expect(replaceState).toHaveBeenCalledWith({ a: 1 }, '', '/123?story=5')
    expect(String(replaceState.mock.calls[0]?.[2])).not.toContain('#')
  })

  it('removes the fragment even when storage refuses the token', () => {
    const store = fakeStore()
    store.setItem = () => {
      throw new Error('blocked')
    }
    const replaceState = vi.fn()
    captureToken(fakeLocation('#' + TOKEN), { state: null, replaceState } as unknown as History, store)
    expect(replaceState).toHaveBeenCalledWith(null, '', '/123?story=5')
  })

  it('ignores a fragment that is not a token', () => {
    for (const hash of ['', '#', '#abc', '#' + TOKEN.slice(1), '#' + TOKEN + 'a', '#' + 'g'.repeat(64)]) {
      const store = fakeStore()
      const replaceState = vi.fn()
      captureToken(fakeLocation(hash), { state: null, replaceState } as unknown as History, store)
      expect(readToken(store)).toBeNull()
      expect(replaceState).not.toHaveBeenCalled()
    }
  })

  it('does not touch the document title or log anything', () => {
    document.title = 'omo'
    const spies = (['log', 'info', 'warn', 'error', 'debug'] as const).map((k) => vi.spyOn(console, k))
    captureToken(fakeLocation('#' + TOKEN), { state: null, replaceState: vi.fn() } as unknown as History, fakeStore())
    expect(document.title).toBe('omo')
    for (const s of spies) expect(s).not.toHaveBeenCalled()
  })
})

describe('stored token', () => {
  it('is saved, read and forgotten', () => {
    const store = fakeStore()
    expect(readToken(store)).toBeNull()
    saveToken(store, TOKEN)
    expect(readToken(store)).toBe(TOKEN)
    forgetToken(store)
    expect(readToken(store)).toBeNull()
  })

  it('treats a stored value that is not a token as absent', () => {
    const store = fakeStore()
    store.setItem('omo.token', 'nonsense')
    expect(readToken(store)).toBeNull()
  })
})

describe('parseAccess', () => {
  it('accepts the printed address and a bare token, rejects anything else', () => {
    expect(parseAccess(TOKEN)).toBe(TOKEN)
    expect(parseAccess('  ' + TOKEN + '\n')).toBe(TOKEN)
    expect(parseAccess('http://omo.localhost:8090/#' + TOKEN)).toBe(TOKEN)
    expect(parseAccess(' http://127.0.0.1:8090/#' + TOKEN + ' ')).toBe(TOKEN)
    expect(parseAccess('')).toBeNull()
    expect(parseAccess('hello')).toBeNull()
    expect(parseAccess(TOKEN.slice(1))).toBeNull()
    expect(parseAccess('http://omo.localhost:8090/')).toBeNull()
    expect(parseAccess('http://omo.localhost:8090/#abc')).toBeNull()
    expect(parseAccess('javascript:void(0)#' + TOKEN)).toBeNull()
  })
})
