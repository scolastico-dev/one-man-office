const KEY = 'omo.token'
const TOKEN = /^[0-9a-fA-F]{64}$/

function isToken(s: string): boolean {
  return TOKEN.test(s)
}

/**
 * The login token of this page. It lives in `localStorage` under `omo.token`,
 * so every tab shares it, and in memory as a fallback for the life of the
 * page when storage is blocked: some privacy modes throw on merely reading
 * `window.localStorage`, on reading an item or on writing one.
 */
export interface TokenStore {
  /** The token to send: the stored one, or the one kept in memory when storage does not work. */
  read(): string | null
  /** Stores a token, for example one pasted by the user. Never throws. */
  save(token: string): void
  /** Forgets the token. Never throws. */
  forget(): void
  /**
   * Moves a login token from the address bar into the store. A fragment that
   * is exactly 64 hex characters is stored and then always removed from the
   * address bar (path and query stay), before anything else can read or
   * record the URL. Any other fragment is left alone.
   */
  capture(loc: Location, hist: History): void
}

function defaultStorage(): Storage {
  return window.localStorage
}

/** `getStorage` may throw; the store then keeps the token in memory only. */
export function createTokenStore(getStorage: () => Storage = defaultStorage): TokenStore {
  let memory: string | null = null
  /** True while the last write failed: storage then holds an older value, or none. */
  let detached = false

  /** Every storage access goes through here; it never throws. */
  function withStorage<T>(fallback: T, fn: (s: Storage) => T): T {
    try {
      return fn(getStorage())
    } catch {
      return fallback
    }
  }

  function read(): string | null {
    if (detached) return memory
    const stored = withStorage<string | null | undefined>(undefined, (s) => s.getItem(KEY))
    // Storage could not be read: the token this page saved is all there is.
    if (stored === undefined) return memory
    return stored !== null && isToken(stored) ? stored : null
  }

  function save(token: string): void {
    memory = token
    detached = !withStorage(false, (s) => {
      s.setItem(KEY, token)
      return true
    })
  }

  function forget(): void {
    memory = null
    // While the removal fails, storage may still hold the token: ignore it.
    detached = !withStorage(false, (s) => {
      s.removeItem(KEY)
      return true
    })
  }

  function capture(loc: Location, hist: History): void {
    const fragment = loc.hash.startsWith('#') ? loc.hash.slice(1) : loc.hash
    if (!isToken(fragment)) return
    try {
      save(fragment)
    } finally {
      hist.replaceState(hist.state, '', loc.pathname + loc.search)
    }
  }

  return { read, save, forget, capture }
}

/**
 * Extracts a token from what the user pasted: the printed address
 * (`http://host:port/#<token>`) or the bare token. Returns null for anything
 * else.
 */
export function parseAccess(input: string): string | null {
  const text = input.trim()
  if (isToken(text)) return text
  let url: URL
  try {
    url = new URL(text)
  } catch {
    return null
  }
  if (url.protocol !== 'http:' && url.protocol !== 'https:') return null
  const fragment = url.hash.slice(1)
  return isToken(fragment) ? fragment : null
}
