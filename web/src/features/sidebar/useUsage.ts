// How much of each account's usage is used, and which models agents are kept
// off, for the Agents tab. Shared per app: every caller sees the same state,
// and the work stays subscribed while at least one caller is mounted (the
// same acquire/release as `useAgents`; no watcher belongs to a caller).
//
// The first state is read, a `usage.changed` event carries the whole state,
// and a resync or a restart of the stream reads it again. Refresh asks the
// server to run the scripts now and takes its answer as a read.
import { getCurrentScope, onScopeDispose, shallowRef, type Ref } from 'vue'
import type { ApiClient } from '../../api/client'
import { getUsage, refreshUsage } from '../../api/endpoints'
import type { ApiEvent, UsageState } from '../../api/types'
import { useSession } from '../../session'
import type { BoardStore } from '../../store'

export interface Usage {
  /** The readings and the models' states; null until the first read. */
  state: Readonly<Ref<UsageState | null>>
  /** Why the state could not be read or refreshed, in the server's words; empty when it could. */
  error: Readonly<Ref<string>>
  /** True once the first state is in. */
  loaded: Readonly<Ref<boolean>>
  /** True while a refresh is under way. */
  refreshing: Readonly<Ref<boolean>>
  /** Runs the scripts now; resolves once the server answered (the failure is in `error`). */
  refresh(): Promise<void>
}

export interface UsageDeps {
  client: ApiClient
  store: BoardStore
}

function message(e: unknown, fallback: string): string {
  return e instanceof Error && e.message !== '' ? e.message : fallback
}

function isState(v: unknown): v is UsageState {
  return typeof v === 'object' && v !== null && Array.isArray((v as UsageState).readings) && Array.isArray((v as UsageState).models)
}

interface Shared {
  usage: Usage
  acquire(): () => void
}

/** The state with its subscriptions; `useUsage` shares one per board store. Exported for tests. */
export function createUsage(deps: UsageDeps): Shared {
  const { client, store } = deps
  const state = shallowRef<UsageState | null>(null)
  const error = shallowRef('')
  const loaded = shallowRef(false)
  const refreshing = shallowRef(false)

  /**
   * Answers can arrive in any order. Each read takes a number when it
   * starts; an answer is applied only when no newer one was: a newer read's
   * answer or an event (which is newer than every read under way).
   */
  let seq = 0
  let applied = 0
  let users = 0
  let off: (() => void)[] = []

  function apply(next: UsageState, mine: number) {
    if (mine < applied) return
    applied = mine
    state.value = next
    loaded.value = true
    error.value = ''
  }

  async function read() {
    const mine = ++seq
    try {
      apply((await getUsage(client)).data, mine)
    } catch (e) {
      if (mine >= applied) error.value = message(e, 'Could not read the usage.')
    }
  }

  async function refresh() {
    if (refreshing.value) return
    refreshing.value = true
    const mine = ++seq
    try {
      apply((await refreshUsage(client)).data, mine)
    } catch (e) {
      error.value = message(e, 'Usage could not be refreshed.')
    } finally {
      refreshing.value = false
    }
  }

  function onChanged(ev: ApiEvent) {
    if (!isState(ev.data)) return
    apply(ev.data, ++seq)
  }

  function start() {
    off = [store.onEvent('usage.changed', onChanged), store.onSnapshot(() => void read())]
    void read()
  }

  function stop() {
    for (const f of off) f()
    off = []
  }

  return {
    usage: { state, error, loaded, refreshing, refresh },
    acquire() {
      if (users++ === 0) start()
      let released = false
      return () => {
        if (released) return
        released = true
        if (--users === 0) stop()
      }
    },
  }
}

const shared = new WeakMap<BoardStore, Shared>()

/** The usage of the app's accounts; stays live while the calling component is mounted. */
export function useUsage(): Usage {
  const session = useSession()
  let s = shared.get(session.store)
  if (!s) {
    s = createUsage({ client: session.client, store: session.store })
    shared.set(session.store, s)
  }
  const release = s.acquire()
  if (getCurrentScope()) onScopeDispose(release)
  return s.usage
}
