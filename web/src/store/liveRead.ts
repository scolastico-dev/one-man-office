// A list a page shows and keeps current: read at once, read again
// when an event says it may have changed, after a snapshot and when the
// event stream is live again. Reads are numbered, so an older answer (or an
// older failure) never replaces a newer one. Stops with the calling
// component or effect scope.
import { getCurrentScope, onScopeDispose, watch } from 'vue'
import type { ApiEvent } from '../api/types'
import type { BoardStore } from './board'

export interface LiveReadOptions<T> {
  store: Pick<BoardStore, 'onEvent' | 'onSnapshot' | 'connection'>
  read(): Promise<T>
  /** Takes the answer of the newest read so far. */
  apply(value: T): void
  /** The newest read failed. */
  failed?(e: unknown): void
  /** Whether an event may have changed what is read. */
  watches(ev: ApiEvent): boolean
}

export interface LiveRead {
  /** Reads again; resolves once the answer is applied or found old. */
  reload(): Promise<void>
  /** Something newer than every read under way was taken (a write's answer): their answers are dropped. */
  newer(): void
  stop(): void
}

export function liveRead<T>(o: LiveReadOptions<T>): LiveRead {
  let seq = 0
  let applied = 0
  async function reload() {
    const mine = ++seq
    try {
      const v = await o.read()
      if (mine <= applied) return
      applied = mine
      o.apply(v)
    } catch (e) {
      if (mine <= applied) return
      o.failed?.(e)
    }
  }
  const offEvent = o.store.onEvent('*', (ev) => {
    if (o.watches(ev)) void reload()
  })
  const offSnapshot = o.store.onSnapshot(() => void reload())
  const stopLive = watch(
    () => o.store.connection.value,
    (now, before) => {
      if (now === 'live' && before !== 'live') void reload()
    },
  )
  const stop = () => {
    offEvent()
    offSnapshot()
    stopLive()
  }
  if (getCurrentScope()) onScopeDispose(stop)
  void reload()
  return {
    reload,
    newer() {
      applied = ++seq
    },
    stop,
  }
}
