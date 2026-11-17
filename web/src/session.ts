// The app's connection to omo: the login token, the API client and the board
// store, and which screen the app shows because of them.

import { inject, ref, type InjectionKey, type Ref } from 'vue'
import { getServer } from './api/endpoints'
import { ApiError, createClient, type ApiClient, type ClientOptions } from './api/client'
import type { connectEventStream, StreamTimers } from './api/eventStream'
import type { TokenStore } from './auth/token'
import { createBoardStore, type BoardStore } from './store'

/**
 * `idle` before the first load; `loading` until the board's first snapshot is
 * in; `ready` shows the board; `locked` when omo refused the request for want
 * of a valid login token; `unreachable` when the board could not be loaded.
 */
export type SessionPhase = 'idle' | 'loading' | 'ready' | 'locked' | 'unreachable'
export type LockReason = 'missing' | 'rejected'

export interface Session {
  phase: Readonly<Ref<SessionPhase>>
  lockReason: Readonly<Ref<LockReason>>
  /** Why the board could not be loaded, in words, while `unreachable`. */
  problem: Readonly<Ref<string>>
  client: ApiClient
  store: BoardStore
  /**
   * The login token the session uses right now (stored, or kept in memory when
   * storage does not work), for the websockets, which cannot use the client.
   */
  token(): string | null
  /** Loads the board unless it is loading or loaded already. */
  start(): void
  /** Loads the board again after it could not be reached. */
  retry(): void
  /** Uses a token the user pasted and loads the board again. */
  unlock(token: string): void
  /**
   * The address changed without a reload (its fragment, through `hashchange`).
   * A login token in the fragment is taken like the one at start-up: stored,
   * removed from the address bar and history, and the board loads, or, when
   * it is shown already, the login is checked again. Any other fragment is
   * left alone.
   */
  addressChanged(loc: Location, hist: History): void
}

export interface SessionDeps {
  tokens: TokenStore
  /** Builds the API client; `createClient` by default. */
  makeClient?: (o: ClientOptions) => ApiClient
  connect?: typeof connectEventStream
  timers?: StreamTimers
}

export const SESSION: InjectionKey<Session> = Symbol('session')

export function useSession(): Session {
  const s = inject(SESSION, null)
  if (s === null) throw new Error('useSession() needs the session provided with the SESSION key')
  return s
}

export function createSession(deps: SessionDeps): Session {
  const phase = ref<SessionPhase>('idle')
  const lockReason = ref<LockReason>('missing')
  const problem = ref('')
  const { tokens } = deps
  const makeClient = deps.makeClient ?? createClient

  const client = makeClient({
    getToken: () => tokens.read(),
    forgetToken: (token) => tokens.refused(token),
    tokenAccepted: (token) => tokens.accepted(token),
    onUnauthorized: (reason) => lock(reason),
  })
  const store = createBoardStore({
    client,
    ...(deps.connect ? { connect: deps.connect } : {}),
    ...(deps.timers ? { timers: deps.timers } : {}),
  })

  /** Counts loads, so the answer of a load that was replaced is ignored. */
  let attempt = 0

  function lock(reason: LockReason): void {
    if (phase.value === 'locked') return
    lockReason.value = reason
    phase.value = 'locked'
    attempt++
    // Out of the client's call stack: the request that was refused finishes first.
    queueMicrotask(() => {
      if (phase.value === 'locked') store.stop()
    })
  }

  function load(): void {
    const my = ++attempt
    phase.value = 'loading'
    store.start().then(
      () => {
        if (my === attempt && phase.value === 'loading') phase.value = 'ready'
      },
      (e: unknown) => {
        if (my !== attempt || phase.value !== 'loading') return
        problem.value =
          e instanceof ApiError
            ? e.code === 'network'
              ? 'omo does not answer at this address. Check that it is still running.'
              : `omo answered: ${e.message}`
            : 'The board could not be loaded.'
        phase.value = 'unreachable'
        store.stop()
      },
    )
  }

  function addressChanged(loc: Location, hist: History): void {
    if (!tokens.capture(loc, hist)) return
    switch (phase.value) {
      case 'locked':
      case 'unreachable':
      case 'loading':
        store.stop()
        load()
        break
      case 'ready':
        // A refusal is handled by the client's rules and locks the screen.
        getServer(client).catch(() => {})
        break
      default:
        // Before the first load: the start reads the token itself.
        break
    }
  }

  return {
    phase,
    lockReason,
    problem,
    client,
    store,
    token: () => tokens.read(),
    start() {
      if (phase.value === 'idle') load()
    },
    retry() {
      if (phase.value !== 'unreachable') return
      store.stop()
      load()
    },
    addressChanged,
    unlock(token) {
      tokens.save(token)
      store.stop()
      load()
    },
  }
}
