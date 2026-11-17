import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createTokenStore } from '../../auth/token'
import { answerTokenAsks, askOpenerForToken, type MessageSource, type PeerWindow } from './windowToken'

const ORIGIN = 'http://omo.localhost:8090'
const TOKEN = 'ab'.repeat(32)

/** A window's message target, with what was posted to it. */
function target() {
  const fns = new Set<(ev: MessageEvent) => void>()
  const t: MessageSource & { deliver(data: unknown, origin: string, source: unknown): void } = {
    addEventListener: (_t, fn) => fns.add(fn),
    removeEventListener: (_t, fn) => fns.delete(fn),
    deliver(data, origin, source) {
      for (const fn of [...fns]) fn({ data, origin, source } as MessageEvent)
    },
  }
  return { t, count: () => fns.size }
}

const blocked = () => {
  throw new DOMException('blocked', 'SecurityError')
}

beforeEach(() => vi.useFakeTimers())
afterEach(() => vi.useRealTimers())

describe('the opener', () => {
  it('answers a window of this origin with the token, addressed to this origin only', () => {
    const { t } = target()
    const stop = answerTokenAsks({ target: t, origin: ORIGIN, token: () => TOKEN })
    const asker = { postMessage: vi.fn() }
    t.deliver({ type: 'omo-token-ask' }, 'http://evil.example', asker)
    t.deliver({ type: 'other' }, ORIGIN, asker)
    expect(asker.postMessage).not.toHaveBeenCalled()
    t.deliver({ type: 'omo-token-ask' }, ORIGIN, asker)
    expect(asker.postMessage).toHaveBeenCalledWith({ type: 'omo-token', token: TOKEN }, ORIGIN)
    stop()
    t.deliver({ type: 'omo-token-ask' }, ORIGIN, asker)
    expect(asker.postMessage).toHaveBeenCalledTimes(1)
  })

  it('says nothing without a token', () => {
    const { t } = target()
    answerTokenAsks({ target: t, origin: ORIGIN, token: () => null })
    const asker = { postMessage: vi.fn() }
    t.deliver({ type: 'omo-token-ask' }, ORIGIN, asker)
    expect(asker.postMessage).not.toHaveBeenCalled()
  })
})

describe('the terminal window', () => {
  it('with blocked storage asks its opener and keeps the answer in memory; answers of another origin or window are ignored', async () => {
    const tokens = createTokenStore(blocked)
    const { t, count } = target()
    const opener: PeerWindow = { postMessage: vi.fn() }
    const done = askOpenerForToken({ tokens, opener, target: t, origin: ORIGIN })
    expect(opener.postMessage).toHaveBeenCalledWith({ type: 'omo-token-ask' }, ORIGIN)
    t.deliver({ type: 'omo-token', token: TOKEN }, 'http://evil.example', opener)
    t.deliver({ type: 'omo-token', token: TOKEN }, ORIGIN, {})
    t.deliver({ type: 'omo-token', token: 'not a token' }, ORIGIN, opener)
    expect(tokens.read()).toBeNull()
    t.deliver({ type: 'omo-token', token: TOKEN }, ORIGIN, opener)
    await done
    expect(tokens.read()).toBe(TOKEN)
    expect(count()).toBe(0)
  })

  it('asks nobody when the store has a token or there is no opener, and gives up after a while', async () => {
    const stored = createTokenStore(blocked)
    stored.save(TOKEN)
    const opener: PeerWindow = { postMessage: vi.fn() }
    const { t, count } = target()
    await askOpenerForToken({ tokens: stored, opener, target: t, origin: ORIGIN })
    expect(opener.postMessage).not.toHaveBeenCalled()
    await askOpenerForToken({ tokens: createTokenStore(blocked), opener: null, target: t, origin: ORIGIN })

    const none = createTokenStore(blocked)
    const waiting = askOpenerForToken({ tokens: none, opener, target: t, origin: ORIGIN, wait: 1000 })
    vi.advanceTimersByTime(1000)
    await waiting
    expect(none.read()).toBeNull()
    expect(count()).toBe(0)
  })
})
