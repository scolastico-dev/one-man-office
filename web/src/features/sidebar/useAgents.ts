// The running agents and the scheduler's load (tasks and stories in work), for the sidebar (and any other
// place that lists live agents). Shared per app: every caller sees the same
// lists, and the work stays subscribed while at least one caller is mounted.
//
// The event stream carries the whole session in `agent.created`,
// `agent.updated` and `agent.ended`, and the scheduler's state in
// `scheduler.changed`, so nothing is polled. Two things come from a read: the
// first list, and the web terminal's id, which the stream does not carry (a
// new agent is read again once, shortly after it appears). A resync or a
// restart of the stream reads both again.
import { computed, getCurrentScope, onScopeDispose, shallowRef, type ComputedRef, type Ref } from 'vue'
import type { ApiClient } from '../../api/client'
import { getScheduler, listAgents } from '../../api/endpoints'
import type { AgentData, AgentSession, ApiEvent, SchedulerChangedData, SchedulerState } from '../../api/types'
import { timeKey } from '../../api/time'
import { useSession } from '../../session'
import type { BoardStore } from '../../store'

/** How long a read waits after the last event that asked for it. */
export const REREAD_DELAY_MS = 200

export interface Agents {
  /** Live sessions (starting, running, parked), newest first; an ended session never shows. */
  sessions: ComputedRef<AgentSession[]>
  scheduler: Readonly<Ref<SchedulerState | null>>
  /** Why the lists could not be read, in the server's words; empty when they could. */
  error: Readonly<Ref<string>>
  /** True once the first list is in. */
  loaded: Readonly<Ref<boolean>>
  /** Reads both again now. */
  refresh(): void
}

export interface AgentsDeps {
  client: ApiClient
  store: BoardStore
  timers?: { setTimeout(fn: () => void, ms: number): unknown; clearTimeout(h: unknown): void }
}

/** The word a card and a row show for a live state; a running agent needs none. */
export function stateWord(state: AgentSession['state']): 'parked' | 'starting' | '' {
  return state === 'waiting' ? 'parked' : state === 'starting' ? 'starting' : ''
}

const isLive = (s: AgentSession) => s.state !== 'ended'

const newestFirst = (a: AgentSession, b: AgentSession) => {
  const ka = timeKey(a.started_at)
  const kb = timeKey(b.started_at)
  return ka < kb ? 1 : ka > kb ? -1 : a.id < b.id ? -1 : 1
}

function message(e: unknown): string {
  return e instanceof Error ? e.message : 'Could not read the agents.'
}

interface Shared {
  agents: Agents
  acquire(): () => void
}

/** The lists with their subscriptions; `useAgents` shares one per board store. Exported for tests. */
export function createAgents(deps: AgentsDeps): Shared {
  const { client, store } = deps
  const timers = deps.timers ?? {
    setTimeout: (fn, ms) => setTimeout(fn, ms),
    clearTimeout: (h) => clearTimeout(h as ReturnType<typeof setTimeout>),
  }
  const byId = shallowRef(new Map<string, AgentSession>())
  const scheduler = shallowRef<SchedulerState | null>(null)
  const error = shallowRef('')
  const loaded = shallowRef(false)

  /**
   * Per read under way, what the stream said meanwhile: its answer is older
   * than that. `ended` ids must not come back, `seen` sessions are kept as
   * the stream gave them.
   */
  interface Meanwhile {
    ended: Set<string>
    seen: Map<string, AgentSession>
  }
  const inflight = new Set<Meanwhile>()
  /**
   * Answers can arrive in any order. Each read takes a number when it starts;
   * an answer is applied only when nothing newer was: a newer read's answer
   * for the list, a newer read's answer or a scheduler event for the load.
   */
  let agentReads = 0
  let agentsApplied = 0
  let schedulerSeq = 0
  let schedulerApplied = 0
  let timer: unknown = null
  let users = 0
  let off: (() => void)[] = []

  function put(s: AgentSession) {
    for (const m of inflight) {
      if (isLive(s)) {
        m.ended.delete(s.id)
        m.seen.set(s.id, s)
      } else {
        m.seen.delete(s.id)
        m.ended.add(s.id)
      }
    }
    const next = new Map(byId.value)
    if (isLive(s)) {
      const old = next.get(s.id)
      // The stream does not carry the terminal's id: keep the one a read gave.
      next.set(s.id, s.terminal_id === undefined && old?.terminal_id !== undefined ? { ...s, terminal_id: old.terminal_id } : s)
    } else next.delete(s.id)
    byId.value = next
  }

  async function readAgents() {
    const mine = ++agentReads
    const meanwhile: Meanwhile = { ended: new Set(), seen: new Map() }
    inflight.add(meanwhile)
    try {
      const { data } = await listAgents(client, true)
      if (mine < agentsApplied) return
      agentsApplied = mine
      const next = new Map<string, AgentSession>()
      for (const s of data) if (isLive(s) && !meanwhile.ended.has(s.id)) next.set(s.id, s)
      for (const [id, s] of meanwhile.seen) {
        const terminal = s.terminal_id ?? next.get(id)?.terminal_id
        next.set(id, terminal === undefined ? s : { ...s, terminal_id: terminal })
      }
      byId.value = next
      loaded.value = true
      error.value = ''
    } catch (e) {
      if (mine >= agentsApplied) error.value = message(e)
    } finally {
      inflight.delete(meanwhile)
    }
  }

  async function readScheduler() {
    const mine = ++schedulerSeq
    try {
      const { data } = await getScheduler(client)
      if (mine < schedulerApplied) return
      schedulerApplied = mine
      scheduler.value = data
    } catch (e) {
      if (mine >= schedulerApplied) error.value = message(e)
    }
  }

  function refresh() {
    void readAgents()
    void readScheduler()
  }

  /** A live agent with no terminal yet is read again once the events have settled. */
  function later() {
    if (timer !== null) return
    timer = timers.setTimeout(() => {
      timer = null
      void readAgents()
    }, REREAD_DELAY_MS)
  }

  function onAgent(ev: ApiEvent) {
    const s = (ev.data as AgentData | undefined)?.agent
    if (!s || typeof s.id !== 'string') return
    if (ev.kind === 'agent.ended' || !isLive(s)) {
      put({ ...s, state: 'ended' })
      return
    }
    put(s)
    if (byId.value.get(s.id)?.terminal_id === undefined) later()
  }

  function onScheduler(ev: ApiEvent) {
    const state = (ev.data as SchedulerChangedData | undefined)?.state
    if (!state) return
    // Newer than every read under way.
    schedulerApplied = ++schedulerSeq
    scheduler.value = state
  }

  function start() {
    off = [store.onEvent('agent.created', onAgent), store.onEvent('agent.updated', onAgent), store.onEvent('agent.ended', onAgent), store.onEvent('scheduler.changed', onScheduler), store.onSnapshot(refresh)]
    refresh()
  }

  function stop() {
    for (const f of off) f()
    off = []
    if (timer !== null) timers.clearTimeout(timer)
    timer = null
  }

  const agents: Agents = {
    sessions: computed(() => [...byId.value.values()].sort(newestFirst)),
    scheduler,
    error,
    loaded,
    refresh,
  }
  return {
    agents,
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

/** The agents of the app's board; stays live while the calling component is mounted. */
export function useAgents(): Agents {
  const session = useSession()
  let s = shared.get(session.store)
  if (!s) {
    s = createAgents({ client: session.client, store: session.store })
    shared.set(session.store, s)
  }
  const release = s.acquire()
  if (getCurrentScope()) onScopeDispose(release)
  return s.agents
}
