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
//
// Lines hidden between and around a file's changes can be shown (see
// expand.ts): the file's patch is read again with more context, and what
// the user revealed is kept per file while the tab is open and across reads
// that leave the file's patch as it was; a changed patch drops it. Reads of
// one file are numbered, so an older answer never replaces a newer one.
import { computed, onBeforeUnmount, reactive, ref, shallowRef, watch, type ComputedRef, type Ref } from 'vue'
import { ApiError } from '../../api/client'
import { getDiffFile, getTicketDiff } from '../../api/endpoints'
import type { DiffFile, ProjectDiff, RemoteBase, TicketDiff, TicketMovedData } from '../../api/types'
import { contextFor, covers, NOTHING_REVEALED, revealFor, sameChanges, type ExpandAction, type Reveal, type Wider } from './expand'
import { parsePatch } from './parsePatch'
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

/** What a file shows beyond its patch. */
export interface Expansion {
  /** The patch this applies to; another patch of the file starts afresh. */
  basePatch: string
  reveal: Reveal
  /** The file read with more context, once one was needed. */
  wider: Wider | null
  loading: boolean
  /** Why the last read was refused, shown where it was asked for. */
  error: string
  /** Where: the first hidden line of the expander row (its `from`), or 0 for "Show the whole file". */
  errorAt: number
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
  /** What the file (as shown) shows beyond its patch; null when nothing was asked. */
  expansionOf(key: string, f: DiffFile): Expansion | null
  /** Shows hidden lines of a file: reads it again with the context they need when the last read lacks them. */
  expand(project: string, f: DiffFile, uncommitted: boolean, action: ExpandAction): Promise<void>
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
  const expansions = reactive(new Map<string, Expansion>())
  /** The number of the newest read of each file's context. */
  const expandSeq = new Map<string, number>()
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
    // What was revealed stays for a file whose patch is unchanged.
    const patches = new Map<string, string | null>()
    for (const p of d.projects) {
      for (const f of p.files) patches.set(fileKey(p.project, false, f.path), f.patch)
      for (const f of p.uncommitted) patches.set(fileKey(p.project, true, f.path), f.patch)
    }
    for (const [key, x] of [...expansions]) if (patches.get(key) !== x.basePatch) expansions.delete(key)
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

  function expansionOf(key: string, f: DiffFile): Expansion | null {
    const x = expansions.get(key)
    return x !== undefined && x.basePatch === f.patch ? x : null
  }

  async function expand(p: string, f: DiffFile, uncommitted: boolean, action: ExpandAction): Promise<void> {
    if (f.patch === null) return
    const key = fileKey(p, uncommitted, f.path)
    const basePatch = f.patch
    const now = expansionOf(key, f) ?? { basePatch, reveal: NOTHING_REVEALED, wider: null, loading: false, error: '', errorAt: 0 }
    const base = parsePatch(basePatch).hunks
    const reveal = revealFor(now.reveal, action)
    const want = contextFor(base, reveal)
    const at = action.kind === 'all' ? 0 : action.gap.from
    if (covers(now.wider?.context ?? null, want)) {
      expansions.set(key, { ...now, reveal, error: '', errorAt: 0 })
      return
    }
    const seq = (expandSeq.get(key) ?? 0) + 1
    expandSeq.set(key, seq)
    expansions.set(key, { ...now, loading: true, error: '', errorAt: at })
    const current = () => !gone && expandSeq.get(key) === seq && expansions.get(key)?.basePatch === basePatch
    try {
      const remote = !uncommitted && diff.value?.projects.find((x) => x.project === p)?.compared_with === 'remote'
      const q = { project: p, path: f.path, uncommitted, context: want }
      const res = await getDiffFile(client, n, remote ? { ...q, base: 'remote' } : q)
      if (!current()) return
      const x = expansions.get(key)!
      const parsed = res.data.patch === null ? null : parsePatch(res.data.patch)
      if (parsed === null || parsed.cut || !sameChanges(base, parsed.hunks)) {
        expansions.set(key, { ...x, loading: false, error: 'The file changed since the diff was read; Refresh shows it as it is now.', errorAt: at })
        return
      }
      expansions.set(key, { ...x, reveal, wider: { parsed, context: want }, loading: false, error: '', errorAt: 0 })
    } catch (e) {
      if (!current()) return
      const x = expansions.get(key)!
      expansions.set(key, { ...x, loading: false, error: messageOf(e, 'The hidden lines could not be read.'), errorAt: at })
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
    expansionOf,
    expand,
  }
}
