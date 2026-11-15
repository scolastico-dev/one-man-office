const KEY = 'omo.token'
const TOKEN = /^[0-9a-fA-F]{64}$/

function isToken(s: string): boolean {
  return TOKEN.test(s)
}

/**
 * Moves a login token from the address bar into storage. A fragment that is
 * exactly 64 hex characters is stored and then removed from the address bar
 * (path and query stay), before anything else can read or record the URL. Any
 * other fragment is left alone.
 */
export function captureToken(loc: Location, hist: History, store: Storage): void {
  const fragment = loc.hash.startsWith('#') ? loc.hash.slice(1) : loc.hash
  if (!isToken(fragment)) return
  try {
    saveToken(store, fragment)
  } finally {
    hist.replaceState(hist.state, '', loc.pathname + loc.search)
  }
}

export function readToken(store: Storage): string | null {
  try {
    const v = store.getItem(KEY)
    return v !== null && isToken(v) ? v : null
  } catch {
    return null
  }
}

export function saveToken(store: Storage, token: string): void {
  try {
    store.setItem(KEY, token)
  } catch {
    // Storage may be blocked; the app then asks for the token again on reload.
  }
}

export function forgetToken(store: Storage): void {
  try {
    store.removeItem(KEY)
  } catch {
    // Nothing to remove.
  }
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
