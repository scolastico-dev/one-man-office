// The user's shells, shared by the Terminals tab, the tab's count and the
// Open terminal action. The list is read when the first caller mounts and again
// after a resync or a restart of the stream; `terminal.opened`, `terminal.exited`
// and `terminal.closed` carry the whole shell, so they change the list without
// a request. Shared per board store; the work stays subscribed while at least
// one caller is mounted.
import { computed, getCurrentScope, onScopeDispose, shallowRef, type ComputedRef, type Ref } from 'vue'
import type { ApiClient } from '../../api/client'
import { closeTerminal, listTerminals, openTerminal } from '../../api/endpoints'
import { timeKey } from '../../api/time'
import type { ApiEvent, OpenTerminalRequest, TerminalData, UserTerminal } from '../../api/types'
import { useSession } from '../../session'
import type { BoardStore } from '../../store'

export interface Terminals {
  /** Every shell the server lists, newest first. */
  terminals: ComputedRef<UserTerminal[]>
  /** Why the list could not be read, in the server's words; empty when it could. */
  error: Readonly<Ref<string>>
  /** True once the first list is in. */
  loaded: Readonly<Ref<boolean>>
  /** The number of shells, running or exited. */
  count: ComputedRef<number>
  /** Opens a shell; refusals reject with the server's error. The new shell is in the list when this resolves. */
  open(req: OpenTerminalRequest): Promise<UserTerminal>
  /** Ends a shell if it runs and forgets it; refusals reject with the server's error. */
  close(id: string): Promise<void>
  /** Reads the list again now. */
  refresh(): void
}

export interface TerminalsDeps {
  client: ApiClient
  store: BoardStore
}

const newestFirst = (a: UserTerminal, b: UserTerminal) => {
  const ka = timeKey(a.started_at)
  const kb = timeKey(b.started_at)
  return ka < kb ? 1 : ka > kb ? -1 : a.id < b.id ? -1 : 1
}

function message(e: unknown): string {
  return e instanceof Error && e.message !== '' ? e.message : 'Could not read the terminals.'
}

interface Shared {
  terminals: Terminals
  acquire(): () => void
}

/** The list with its subscriptions; `useTerminals` shares one per board store. Exported for tests. */
export function createTerminals(deps: TerminalsDeps): Shared {
  const { client, store } = deps
  const byId = shallowRef(new Map<string, UserTerminal>())
  const error = shallowRef('')
  const loaded = shallowRef(false)

  /** What the stream said while a read was under way: the read's answer is older than that. */
  interface Meanwhile {
    gone: Set<string>
    seen: Map<string, UserTerminal>
  }
  const inflight = new Set<Meanwhile>()
  let users = 0
  let off: (() => void)[] = []

  function put(t: UserTerminal) {
    for (const m of inflight) {
      m.gone.delete(t.id)
      m.seen.set(t.id, t)
    }
    const next = new Map(byId.value)
    next.set(t.id, t)
    byId.value = next
  }

  function drop(id: string) {
    for (const m of inflight) {
      m.seen.delete(id)
      m.gone.add(id)
    }
    if (!byId.value.has(id)) return
    const next = new Map(byId.value)
    next.delete(id)
    byId.value = next
  }

  async function read() {
    const meanwhile: Meanwhile = { gone: new Set(), seen: new Map() }
    inflight.add(meanwhile)
    try {
      const { data } = await listTerminals(client)
      const next = new Map<string, UserTerminal>()
      for (const t of data) if (!meanwhile.gone.has(t.id)) next.set(t.id, t)
      for (const [id, t] of meanwhile.seen) next.set(id, t)
      byId.value = next
      loaded.value = true
      error.value = ''
    } catch (e) {
      error.value = message(e)
    } finally {
      inflight.delete(meanwhile)
    }
  }

  function onShell(ev: ApiEvent) {
    const t = (ev.data as TerminalData | undefined)?.terminal
    if (!t || typeof t.id !== 'string') return
    if (ev.kind === 'terminal.closed') drop(t.id)
    else put(t)
  }

  function start() {
    off = [store.onEvent('terminal.opened', onShell), store.onEvent('terminal.exited', onShell), store.onEvent('terminal.closed', onShell), store.onSnapshot(() => void read())]
    void read()
  }

  function stop() {
    for (const f of off) f()
    off = []
  }

  const terminals: Terminals = {
    terminals: computed(() => [...byId.value.values()].sort(newestFirst)),
    error,
    loaded,
    count: computed(() => byId.value.size),
    async open(req) {
      const { data } = await openTerminal(client, req)
      put(data)
      return data
    },
    async close(id) {
      await closeTerminal(client, id)
      drop(id)
    },
    refresh: () => void read(),
  }
  return {
    terminals,
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

/** The user's shells; stays live while the calling component is mounted. */
export function useTerminals(): Terminals {
  const session = useSession()
  let s = shared.get(session.store)
  if (!s) {
    s = createTerminals({ client: session.client, store: session.store })
    shared.set(session.store, s)
  }
  const release = s.acquire()
  if (getCurrentScope()) onScopeDispose(release)
  return s.terminals
}
