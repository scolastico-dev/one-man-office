// The diff of one ticket while its Diff tab is open. Commits produce no
// event, so the diff is read when the tab opens, on Refresh, when the ticket
// (or, for a story, one of its tasks) moves or one of their agents ends, after
// a new snapshot and when the stream is live again after a break (events in
// between may have been missed). The selected project and the files
// the user opened or closed stay across reads; a file read in full with
// "Show anyway" is shown until the next read.
//
// A project whose base branch has a remote-tracking branch can be compared
// with that instead (when the local base is behind it). The choice is kept
// per ticket and project for this browser session, not saved: the diff is
// then read twice, once as usual and once with base=remote, and the chosen
// projects are taken from the second read.
import { computed, onBeforeUnmount, reactive, ref, shallowRef, watch, type ComputedRef, type Ref } from 'vue'
import { ApiError } from '../../api/client'
import { getDiffFile, getTicketDiff } from '../../api/endpoints'
import type { DiffFile, ProjectDiff, RemoteBase, TicketDiff, TicketMovedData } from '../../api/types'
import { useSession } from '../../session'
import { useBoard } from '../../store'

/** More files than this in a project and every file starts closed. */
export const OPEN_FILES_MAX = 20
/** A file with more changed lines than this starts closed. */
export const OPEN_LINES_MAX = 400

/** Whether a file starts open, in a project with `files` files. */
export function startsOpen(f: DiffFile, files: number): boolean {
  return files <= OPEN_FILES_MAX && f.additions + f.deletions <= OPEN_LINES_MAX
}

/** The number of changed files a project's tab shows; none when nothing changed. */
export function changedCount(p: ProjectDiff): number | undefined {
  const n = p.files.length + p.uncommitted.length
  return n > 0 ? n : undefined
}

/** The key of a file in the open and loaded lists. */
export function fileKey(project: string, uncommitted: boolean, path: string): string {
  return `${project}\u0000${uncommitted ? 'u' : 'c'}\u0000${path}`
}

/** Ticket and project pairs the user compares with the remote-tracking branch, for this session. */
const remoteChoices = reactive(new Set<string>())
const choiceKey = (n: number, project: string) => `${n}\u0000${project}`

/** "4 commits" */
const commits = (k: number) => (k === 1 ? '1 commit' : `${k.toLocaleString('en')} commits`)

/**
 * Why a base behind its remote-tracking branch matters, in words; empty when
 * it is not behind (ahead only or equal).
 */
export function behindWords(rb: RemoteBase): string {
  if (rb.behind <= 0) return ''
  return `local ${rb.local} is ${commits(rb.behind)} behind ${rb.name}, so changes from ${rb.name} can show up here as changes of this ticket.`
}

/** What a project compared with its remote-tracking branch is compared with, in words. */
export function remoteWords(rb: RemoteBase): string {
  const how = rb.behind > 0 ? `, which local ${rb.local} is ${commits(rb.behind)} behind` : ''
  return `Compared with ${rb.name}, the remote-tracking branch of ${rb.local}${how}, as of the repository's last fetch.`
}

/** Forgets every choice of base (for tests). */
export function forgetBaseChoices(): void {
  remoteChoices.clear()
}

export interface FileLoad {
  loading: boolean
  /** The file was read on its own and shows all the server allows. */
  loaded: boolean
  error: string
}

export interface DiffController {
  diff: Readonly<Ref<TicketDiff | null>>
  state: Readonly<Ref<'loading' | 'ready' | 'failed'>>
  /** Why the first read failed. */
  error: Readonly<Ref<string>>
  refreshing: Readonly<Ref<boolean>>
  /** Why the last read after the first failed (the diff shown is the one before). */
  refreshError: Readonly<Ref<string>>
  /** The selected project's id. */
  project: Ref<string>
  current: ComputedRef<ProjectDiff | null>
  /** Reads the diff again; a failed first read is retried with it. */
  refresh(): Promise<void>
  isOpen(key: string, byDefault: boolean): boolean
  setOpen(key: string, open: boolean): void
  /** The file as read on its own, or else as the diff has it. */
  fileOf(key: string, f: DiffFile): DiffFile
  loadState(key: string): FileLoad
  /** Reads one file with the larger single-file allowance ("Show anyway"). */
  loadFile(project: string, path: string, uncommitted: boolean): Promise<void>
  /** Whether the user chose to compare the project with its remote-tracking branch. */
  remoteOf(project: string): boolean
  /** Compares the project with its remote-tracking branch, or with the local base again, and reads the diff. */
  setRemote(project: string, on: boolean): Promise<void>
  /** Why comparing with the remote-tracking branch was refused; the local base is shown instead. */
  baseError: Readonly<Ref<string>>
}

function messageOf(e: unknown, fallback: string): string {
  return e instanceof Error && e.message !== '' ? e.message : fallback
}

export function useDiff(n: number): DiffController {
  const { client } = useSession()
  const store = useBoard()

  const diff = shallowRef<TicketDiff | null>(null)
  const state = ref<'loading' | 'ready' | 'failed'>('loading')
  const error = ref('')
  const refreshing = ref(false)
  const refreshError = ref('')
  const project = ref('')
  const opened = reactive(new Map<string, boolean>())
  const loaded = reactive(new Map<string, DiffFile>())
  const loads = reactive(new Map<string, FileLoad>())
  const baseError = ref('')
  let gone = false

  const remoteOf = (p: string) => remoteChoices.has(choiceKey(n, p))
  /** A refusal of base=remote: the chosen projects go back to the local base, with the server's words. */
  function refusedRemote(e: unknown): boolean {
    if (!(e instanceof ApiError) || e.code !== 'validation') return false
    for (const k of [...remoteChoices]) if (k.startsWith(choiceKey(n, ''))) remoteChoices.delete(k)
    baseError.value = messageOf(e, 'The diff cannot be compared with the remote-tracking branch.')
    return true
  }

  /** The diff as usual, with the projects compared with their remote-tracking branch where the user chose so. */
  async function readBoth(): Promise<TicketDiff> {
    const local = (await getTicketDiff(client, n)).data
    const wanted = local.projects.filter((p) => p.remote_base !== undefined && remoteOf(p.project)).map((p) => p.project)
    if (wanted.length === 0) return local
    try {
      const remote = (await getTicketDiff(client, n, { base: 'remote' })).data
      const byId = new Map(remote.projects.map((p) => [p.project, p]))
      return { ...local, projects: local.projects.map((p) => (wanted.includes(p.project) ? (byId.get(p.project) ?? p) : p)) }
    } catch (e) {
      if (refusedRemote(e)) return local
      throw e
    }
  }

  const current = computed(() => {
    const all = diff.value?.projects ?? []
    return all.find((p) => p.project === project.value) ?? all[0] ?? null
  })

  function take(d: TicketDiff): void {
    diff.value = d
    loaded.clear()
    loads.clear()
    if (!d.projects.some((p) => p.project === project.value)) project.value = d.projects[0]?.project ?? ''
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
          const res = await readBoth()
          if (gone) return
          take(res)
        } while (again && !gone)
      } finally {
        inFlight = null
      }
    })()
    return inFlight
  }

  async function refresh(): Promise<void> {
    const first = state.value !== 'ready'
    if (first) {
      state.value = 'loading'
      error.value = ''
    }
    refreshing.value = true
    try {
      await read()
      if (gone) return
      state.value = 'ready'
      refreshError.value = ''
    } catch (e) {
      if (gone) return
      const msg = messageOf(e, 'The diff could not be read.')
      if (first) {
        state.value = 'failed'
        error.value = msg
      } else refreshError.value = msg
    } finally {
      if (!gone) refreshing.value = false
    }
  }

  async function loadFile(p: string, path: string, uncommitted: boolean): Promise<void> {
    const key = fileKey(p, uncommitted, path)
    if (loads.get(key)?.loading) return
    const at = diff.value
    loads.set(key, { loading: true, loaded: false, error: '' })
    try {
      const remote = !uncommitted && at?.projects.find((x) => x.project === p)?.compared_with === 'remote'
      const res = await getDiffFile(client, n, remote ? { project: p, path, uncommitted, base: 'remote' } : { project: p, path, uncommitted })
      // A newer read of the whole diff replaces what this one would show.
      if (gone || diff.value !== at) return
      loaded.set(key, res.data)
      loads.set(key, { loading: false, loaded: true, error: '' })
    } catch (e) {
      if (gone || diff.value !== at) return
      loads.set(key, { loading: false, loaded: false, error: messageOf(e, 'The file could not be read.') })
      // The remote-tracking branch is gone: compare with the local base again.
      if (!uncommitted && at?.projects.find((x) => x.project === p)?.compared_with === 'remote' && refusedRemote(e)) void refresh()
    }
  }

  async function setRemote(p: string, on: boolean): Promise<void> {
    if (on) remoteChoices.add(choiceKey(n, p))
    else remoteChoices.delete(choiceKey(n, p))
    baseError.value = ''
    await refresh()
  }

  const reload = () => void refresh()
  /** The ticket itself or, for a story, one of its tasks: a story's branch takes its tasks' merges. */
  const concerns = (t: number | null) => t !== null && (t === n || store.tasksOf(n).some((task) => task.number === t))
  const offMoved = store.onEvent('ticket.moved', (ev) => {
    if (concerns(ev.ticket) || (ev.data as Partial<TicketMovedData> | null)?.ticket?.story === n) reload()
  })
  const offEnded = store.onEvent('agent.ended', (ev) => {
    if (concerns(ev.ticket)) reload()
  })
  const offSnapshot = store.onSnapshot(reload)
  const stopWatch = watch(
    () => store.connection.value,
    (now, before) => {
      if (now === 'live' && before !== 'live') reload()
    },
  )
  onBeforeUnmount(() => {
    gone = true
    offMoved()
    offEnded()
    offSnapshot()
    stopWatch()
  })

  void refresh()
  return {
    diff,
    state,
    error,
    refreshing,
    refreshError,
    project,
    current,
    refresh,
    isOpen: (key, byDefault) => opened.get(key) ?? byDefault,
    setOpen: (key, open) => void opened.set(key, open),
    fileOf: (key, f) => loaded.get(key) ?? f,
    loadState: (key) => loads.get(key) ?? { loading: false, loaded: false, error: '' },
    loadFile,
    remoteOf,
    setRemote,
    baseError,
  }
}
