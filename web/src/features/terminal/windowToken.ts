// The login token for a terminal window when the browser blocks storage.
//
// Normally a terminal window finds the token where every tab does, in
// `localStorage`. When storage is blocked the page that opened the window
// holds the token in memory only; the window then asks its opener for it
// with `postMessage`. Both sides name this origin as the target and check the
// origin and the sending window of what they receive, so the token never
// goes to another origin. Without an answer the window shows the lock screen.

import { parseAccess, type TokenStore } from '../../auth/token'

export const TOKEN_ASK_MS = 1500

interface TokenAsk {
  type: 'omo-token-ask'
}
interface TokenAnswer {
  type: 'omo-token'
  token: string
}

/** A window of this origin that can be written to. */
export interface PeerWindow {
  postMessage(m: unknown, targetOrigin: string): void
}

export interface MessageSource {
  addEventListener(type: 'message', fn: (ev: MessageEvent) => void): void
  removeEventListener(type: 'message', fn: (ev: MessageEvent) => void): void
}

/**
 * The opener's side: answers a window of this origin that asks for the token.
 * Returns a function that stops answering.
 */
export function answerTokenAsks(o: { target: MessageSource; origin: string; token(): string | null }): () => void {
  const fn = (ev: MessageEvent) => {
    if (ev.origin !== o.origin) return
    if ((ev.data as Partial<TokenAsk> | null)?.type !== 'omo-token-ask') return
    const source = ev.source as PeerWindow | null
    const token = o.token()
    if (!source || token === null) return
    const answer: TokenAnswer = { type: 'omo-token', token }
    source.postMessage(answer, o.origin)
  }
  o.target.addEventListener('message', fn)
  return () => o.target.removeEventListener('message', fn)
}

/**
 * The terminal window's side: when the store has no token, asks the opener
 * and keeps what it answers. Resolves once there is a token or after
 * `TOKEN_ASK_MS`; never rejects.
 */
export function askOpenerForToken(o: {
  tokens: TokenStore
  opener: PeerWindow | null
  target: MessageSource
  origin: string
  wait?: number
}): Promise<void> {
  if (o.tokens.read() !== null || o.opener === null) return Promise.resolve()
  const opener = o.opener
  return new Promise((resolve) => {
    const done = () => {
      clearTimeout(timer)
      o.target.removeEventListener('message', fn)
      resolve()
    }
    const fn = (ev: MessageEvent) => {
      if (ev.origin !== o.origin || ev.source !== opener) return
      const data = ev.data as Partial<TokenAnswer> | null
      if (data?.type !== 'omo-token' || typeof data.token !== 'string' || parseAccess(data.token) !== data.token) return
      o.tokens.save(data.token)
      done()
    }
    const timer = setTimeout(done, o.wait ?? TOKEN_ASK_MS)
    o.target.addEventListener('message', fn)
    try {
      const ask: TokenAsk = { type: 'omo-token-ask' }
      opener.postMessage(ask, o.origin)
    } catch {
      done()
    }
  })
}

/** `window.opener` when it is a window of this origin, else null. */
export function sameOriginOpener(): PeerWindow | null {
  try {
    const opener = window.opener as Window | null
    return opener && opener.location.origin === window.location.origin ? opener : null
  } catch {
    return null
  }
}
