// The live thread of one ticket: the newest page of its event log, older
// pages on request, the stream's events as they come, and the user's own
// comments until the log has them. Every event is kept once, by id.
import { computed, onBeforeUnmount, ref, shallowRef, watch, type ComputedRef, type Ref } from 'vue'
import { ApiError } from '../../api/client'
import { getTicketThread, postComment } from '../../api/endpoints'
import { logError } from '../../api/log'
import type { ApiEvent, Comment, CommentRequest, Role } from '../../api/types'
import { useSession } from '../../session'
import { useBoard } from '../../store'
import { buildThread, isThreadKind, mergeEvents, type ThreadItem } from './thread'

/** The thread's page size; the server's default and largest page. */
export const THREAD_PAGE = 200

export type ThreadEntry = ThreadItem & { sending?: boolean }

export interface Thread {
  items: ComputedRef<ThreadEntry[]>
  state: Readonly<Ref<'loading' | 'ready' | 'failed'>>
  /** Why the first page could not be read. */
  error: Readonly<Ref<string>>
  /** Older events exist. */
  more: Readonly<Ref<boolean>>
  loadingOlder: Readonly<Ref<boolean>>
  olderError: Readonly<Ref<string>>
  /** Reads the first page; a failed first load is retried with it. */
  load(): Promise<void>
  loadOlder(): Promise<void>
  /** Reads the newest page again and merges it (after a reconnect or a lost answer). */
  refresh(): Promise<void>
  /** Sends a comment; resolves with the roles nobody works as; rejects with the server's error. */
  post(req: CommentRequest): Promise<Role[]>
}

function messageOf(e: unknown, fallback: string): string {
  return e instanceof Error && e.message !== '' ? e.message : fallback
}

export function useThread(n: number): Thread {
  const { client } = useSession()
  const store = useBoard()

  const events = shallowRef<ApiEvent[]>([])
  const pending = shallowRef<Comment[]>([])
  const more = ref(false)
  const state = ref<'loading' | 'ready' | 'failed'>('loading')
  const error = ref('')
  const loadingOlder = ref(false)
  const olderError = ref('')
  let gone = false
  /** The first page has been read (live events may have come before it). */
  let hasPage = false
  /**
   * Counts the times the log started again from the newest page. An older
   * page asked for before that belongs to the old log and is dropped.
   */
  let restarts = 0

  const items = computed<ThreadEntry[]>(() => {
    const built: ThreadEntry[] = buildThread(events.value)
    const have = new Set<number>()
    for (const it of built) if (it.kind === 'comment') have.add(it.comment.id)
    for (const c of pending.value) {
      if (have.has(c.id)) continue
      built.push({ kind: 'comment', comment: c, eventId: Number.MAX_SAFE_INTEGER, answered: false, answeredBy: null, sending: true })
    }
    return built
  })

  /** Takes events into the log; a comment that arrived leaves the sending list. */
  function take(incoming: readonly ApiEvent[]): void {
    events.value = mergeEvents(events.value, incoming)
    if (pending.value.length === 0) return
    const arrived = new Set<number>()
    for (const ev of incoming) {
      if (ev.kind === 'comment.created') arrived.add((ev.data as { comment: Comment }).comment.id)
    }
    if (arrived.size > 0) pending.value = pending.value.filter((c) => !arrived.has(c.id))
  }

  async function readNewest(): Promise<void> {
    const res = await getTicketThread(client, n, { limit: THREAD_PAGE })
    if (gone) return
    const page = res.data.events
    const known = events.value
    const newest = known[known.length - 1]
    if (!hasPage) {
      hasPage = true
      more.value = res.data.more
      take(page)
    } else if (page.length > 0 && newest !== undefined && page[0]!.id > newest.id) {
      // Everything known is older than the page, and events between were missed: start from the page.
      restarts++
      events.value = []
      more.value = res.data.more
      take(page)
    } else take(page)
  }

  let inFlight: Promise<void> | null = null
  let again = false
  function read(): Promise<void> {
    if (inFlight !== null) {
      again = true
      return inFlight
    }
    inFlight = (async () => {
      try {
        do {
          again = false
          await readNewest()
        } while (again && !gone)
      } finally {
        inFlight = null
      }
    })()
    return inFlight
  }

  async function load(): Promise<void> {
    state.value = 'loading'
    error.value = ''
    try {
      await read()
      state.value = 'ready'
    } catch (e) {
      if (gone) return
      state.value = 'failed'
      error.value = messageOf(e, 'The comments could not be loaded.')
    }
  }

  async function refresh(): Promise<void> {
    if (state.value !== 'ready') return load()
    try {
      await read()
    } catch (e) {
      logError('thread', e)
    }
  }

  async function loadOlder(): Promise<void> {
    if (loadingOlder.value || !more.value) return
    const first = events.value[0]
    if (first === undefined) return
    loadingOlder.value = true
    olderError.value = ''
    const since = restarts
    try {
      const res = await getTicketThread(client, n, { before: first.id, limit: THREAD_PAGE })
      if (gone || since !== restarts) return
      more.value = res.data.more
      take(res.data.events)
    } catch (e) {
      if (!gone && since === restarts) olderError.value = messageOf(e, 'Older comments could not be loaded.')
    } finally {
      loadingOlder.value = false
    }
  }

  async function post(req: CommentRequest): Promise<Role[]> {
    let result
    try {
      result = await postComment(client, n, req)
    } catch (e) {
      // A lost answer: the comment may be in the log already.
      if (e instanceof ApiError && e.code === 'network') void refresh()
      throw e
    }
    const c = result.data.comment
    const known = events.value.some((ev) => ev.kind === 'comment.created' && (ev.data as { comment: Comment }).comment.id === c.id)
    if (!known && !pending.value.some((p) => p.id === c.id)) pending.value = [...pending.value, c]
    // A live stream brings the comment (and a stream that comes back reads the
    // log again); only while it is not live is the log read for it.
    if (store.connection.value !== 'live') void refresh()
    return result.data.unreachable
  }

  // Live events are merged by id, also while the first page is still loading.
  const offEvent = store.onEvent('*', (ev) => {
    if (ev.ticket === n && isThreadKind(ev.kind)) take([ev])
  })
  const offSnapshot = store.onSnapshot(() => void refresh())
  // Live again after a break: events in between may have been missed.
  const stopWatch = watch(
    () => store.connection.value,
    (now, before) => {
      if (now === 'live' && before !== 'live') void refresh()
    },
  )

  onBeforeUnmount(() => {
    gone = true
    offEvent()
    offSnapshot()
    stopWatch()
  })

  void load()
  return { items, state, error, more, loadingOlder, olderError, load, loadOlder, refresh, post }
}
