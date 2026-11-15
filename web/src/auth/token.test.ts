import { afterEach, describe, expect, it, vi } from 'vitest'
import { createTokenStore, parseAccess } from './token'

const TOKEN = 'abababababababababababababababababababababababababababababababab'
const OTHER = 'cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd'

function fakeLocation(hash: string, path = '/123', search = '?story=5'): Location {
  return { hash, pathname: path, search } as Location
}

function fakeHistory(state: unknown = null) {
  const replaceState = vi.fn()
  return { hist: { state, replaceState } as unknown as History, replaceState }
}

/** A storage shared like localStorage; `fail` makes chosen operations throw. */
function fakeStorage(fail: { read?: boolean; write?: boolean; remove?: boolean } = {}) {
  const m = new Map<string, string>()
  const storage = {
    getItem: (k: string) => {
      if (fail.read) throw new Error('blocked')
      return m.get(k) ?? null
    },
    setItem: (k: string, v: string) => {
      if (fail.write) throw new Error('blocked')
      m.set(k, v)
    },
    removeItem: (k: string) => {
      if (fail.remove) throw new Error('blocked')
      m.delete(k)
    },
  } as unknown as Storage
  return { storage, map: m, fail }
}

const throwingAccess = (): Storage => {
  throw new DOMException('The operation is insecure.', 'SecurityError')
}

afterEach(() => vi.restoreAllMocks())

describe('capture', () => {
  it('stores a 64-hex fragment and removes the fragment with replaceState before returning', () => {
    const { storage } = fakeStorage()
    const tokens = createTokenStore(() => storage)
    const { hist, replaceState } = fakeHistory({ a: 1 })
    tokens.capture(fakeLocation('#' + TOKEN), hist)
    expect(tokens.read()).toBe(TOKEN)
    expect(createTokenStore(() => storage).read()).toBe(TOKEN)
    expect(replaceState).toHaveBeenCalledTimes(1)
    expect(replaceState).toHaveBeenCalledWith({ a: 1 }, '', '/123?story=5')
    expect(String(replaceState.mock.calls[0]?.[2])).not.toContain('#')
  })

  it('ignores a fragment that is not a token', () => {
    for (const hash of ['', '#', '#abc', '#' + TOKEN.slice(1), '#' + TOKEN + 'a', '#' + 'g'.repeat(64)]) {
      const tokens = createTokenStore(() => fakeStorage().storage)
      const { hist, replaceState } = fakeHistory()
      tokens.capture(fakeLocation(hash), hist)
      expect(tokens.read()).toBeNull()
      expect(replaceState).not.toHaveBeenCalled()
    }
  })

  it('does not touch the document title or log anything', () => {
    document.title = 'omo'
    const spies = (['log', 'info', 'warn', 'error', 'debug'] as const).map((k) => vi.spyOn(console, k))
    createTokenStore(() => fakeStorage().storage).capture(fakeLocation('#' + TOKEN), fakeHistory().hist)
    expect(document.title).toBe('omo')
    for (const s of spies) expect(s).not.toHaveBeenCalled()
  })
})

describe('blocked storage', () => {
  it('access that throws: the token is kept in memory and the fragment is removed', () => {
    const tokens = createTokenStore(throwingAccess)
    const { hist, replaceState } = fakeHistory()
    expect(() => tokens.capture(fakeLocation('#' + TOKEN), hist)).not.toThrow()
    expect(replaceState).toHaveBeenCalledWith(null, '', '/123?story=5')
    expect(tokens.read()).toBe(TOKEN)
    tokens.forget()
    expect(tokens.read()).toBeNull()
  })

  it('reads that throw: the saved token is used from memory', () => {
    const { storage } = fakeStorage({ read: true })
    const tokens = createTokenStore(() => storage)
    expect(tokens.read()).toBeNull()
    const { hist, replaceState } = fakeHistory()
    tokens.capture(fakeLocation('#' + TOKEN), hist)
    expect(replaceState).toHaveBeenCalled()
    expect(tokens.read()).toBe(TOKEN)
  })

  it('writes that throw: the token is used from memory, not the older stored one', () => {
    const { storage, map, fail } = fakeStorage()
    map.set('omo.token', OTHER)
    fail.write = true
    fail.remove = true
    const tokens = createTokenStore(() => storage)
    const { hist, replaceState } = fakeHistory()
    tokens.capture(fakeLocation('#' + TOKEN), hist)
    expect(replaceState).toHaveBeenCalled()
    expect(tokens.read()).toBe(TOKEN)
    // Forgetting it does not bring back the token storage still holds.
    tokens.forget()
    expect(tokens.read()).toBeNull()
  })

  it('a pasted address works without storage too', () => {
    const tokens = createTokenStore(throwingAccess)
    const pasted = parseAccess('http://omo.localhost:8090/#' + TOKEN)
    expect(pasted).toBe(TOKEN)
    tokens.save(pasted!)
    expect(tokens.read()).toBe(TOKEN)
  })
})

describe('stored token', () => {
  it('is saved, read and forgotten, and shared with other tabs through storage', () => {
    const { storage } = fakeStorage()
    const a = createTokenStore(() => storage)
    const b = createTokenStore(() => storage)
    expect(a.read()).toBeNull()
    a.save(TOKEN)
    expect(b.read()).toBe(TOKEN)
    b.save(OTHER)
    expect(a.read()).toBe(OTHER)
    a.forget()
    expect(b.read()).toBeNull()
  })

  it('treats a stored value that is not a token as absent', () => {
    const { storage, map } = fakeStorage()
    map.set('omo.token', 'nonsense')
    expect(createTokenStore(() => storage).read()).toBeNull()
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
