import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, describe, expect, test } from 'vitest'
import { defineComponent } from 'vue'
import { ApiError } from '../../api/client'
import type { ApiEvent, DiffFile, ProjectDiff, TicketDiff } from '../../api/types'
import { project } from '../../testing/fakeOmo'
import { ticket } from '../board/testing'
import { mountPanel, ok, panelHarness, type PanelHarness } from '../ticket/panelTesting'
import { behindWords, changedCount, fileKey, forgetBaseChoices, remoteWords, startsOpen, useDiff, type DiffController } from './useDiff'
import type { Gap } from './expand'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
  forgetBaseChoices()
})

function file(path: string, o: Partial<DiffFile> = {}): DiffFile {
  return { path, old_path: '', status: 'modified', binary: false, additions: 1, deletions: 0, patch: '@@ -1 +1 @@\n+x\n', truncated: false, ...o }
}
function proj(id: string, o: Partial<ProjectDiff> = {}): ProjectDiff {
  return {
    project: id,
    base: 'main',
    branch: 'omo/4',
    base_commit: 'a',
    head_commit: 'b',
    state: 'ok',
    message: '',
    files: [file('a.txt')],
    files_truncated: false,
    uncommitted: [],
    uncommitted_truncated: false,
    compared_with: 'local',
    ...o,
  }
}
const answer = (projects: ProjectDiff[]): TicketDiff => ({ ticket: 4, projects, computed_at: '2026-11-15T10:00:00.000000000Z' })
const ev = (id: number, kind: string, t: number | null): ApiEvent => ({ id, kind, ticket: t, actor: { role: 'user' }, at: '2026-11-15T10:00:00.000Z', data: {} })

interface Fixture {
  hn: PanelHarness
  d: DiffController
  reads: number
  files: Array<Record<string, unknown>>
  next: { fn: (query?: Record<string, unknown>) => TicketDiff | Promise<TicketDiff> }
  queries: Array<Record<string, unknown> | undefined>
  fileAnswer: { fn: (query?: Record<string, unknown>) => DiffFile | Promise<DiffFile> }
  live(e: ApiEvent): void
}

async function setup(first: TicketDiff, tickets = [ticket({ number: 4 }), ticket({ number: 5 })]): Promise<Fixture> {
  const hn = await panelHarness((omo) => {
    omo.tickets = tickets
    omo.projects = [project('p1'), project('p2')]
  })
  const f = {
    hn,
    reads: 0,
    files: [] as Array<Record<string, unknown>>,
    next: { fn: (_q?: Record<string, unknown>): TicketDiff | Promise<TicketDiff> => first },
    queries: [] as Array<Record<string, unknown> | undefined>,
    fileAnswer: { fn: (_q?: Record<string, unknown>): DiffFile | Promise<DiffFile> => file('big.txt', { patch: '@@ -1 +1 @@\n+all of it\n' }) },
  } as unknown as Fixture
  const client = hn.session.client
  const get = client.get.bind(client)
  client.get = (async (path: string, query?: Record<string, unknown>) => {
    if (path === '/api/tickets/4/diff') {
      f.reads++
      f.queries.push(query)
      return ok(await f.next.fn(query))
    }
    if (path === '/api/tickets/4/diff/file') {
      f.files.push(query ?? {})
      return ok(await f.fileAnswer.fn(query))
    }
    return get(path, query as never)
  }) as typeof client.get
  const Host = defineComponent({
    setup() {
      f.d = useDiff(4)
      return () => null
    },
  })
  const { wrapper } = await mountPanel(hn, Host, {})
  mounted = wrapper
  await flushPromises()
  f.live = (e) => hn.omo.stream!.onEvent(e)
  return f
}

test('loads when mounted and selects the first project', async () => {
  const f = await setup(answer([proj('p1'), proj('p2')]))
  expect(f.reads).toBe(1)
  expect(f.d.state.value).toBe('ready')
  expect(f.d.project.value).toBe('p1')
  expect(f.d.current.value?.project).toBe('p1')
})

test('useDiff reloads on ticket.moved and agent.ended of this ticket only', async () => {
  const f = await setup(answer([proj('p1')]))
  expect(f.reads).toBe(1)
  f.live(ev(10, 'ticket.moved', 5))
  f.live(ev(11, 'agent.ended', 5))
  f.live(ev(12, 'agent.ended', null))
  f.live(ev(13, 'ticket.updated', 4))
  f.live(ev(14, 'agent.created', 4))
  await flushPromises()
  expect(f.reads).toBe(1)
  f.live(ev(15, 'ticket.moved', 4))
  await flushPromises()
  expect(f.reads).toBe(2)
  f.live(ev(16, 'agent.ended', 4))
  await flushPromises()
  expect(f.reads).toBe(3)
})

test("a story's diff reloads when one of its tasks moves or one of their agents ends", async () => {
  const f = await setup(answer([proj('p1')]), [
    ticket({ number: 4, type: 'story' }),
    ticket({ number: 7, type: 'task', story: 4 }),
    ticket({ number: 8, type: 'task', story: 4 }),
    ticket({ number: 5 }),
  ])
  expect(f.reads).toBe(1)
  f.live(ev(10, 'ticket.moved', 5))
  f.live(ev(11, 'agent.ended', 5))
  f.live(ev(12, 'ticket.updated', 7))
  await flushPromises()
  expect(f.reads).toBe(1)
  f.live(ev(13, 'ticket.moved', 7))
  await flushPromises()
  expect(f.reads).toBe(2)
  f.live(ev(14, 'agent.ended', 8))
  await flushPromises()
  expect(f.reads).toBe(3)
})

test('useDiff reads again after a resync and when the stream is live again', async () => {
  const f = await setup(answer([proj('p1')]))
  expect(f.reads).toBe(1)
  f.hn.omo.stream!.onResync(f.hn.omo.eventId)
  await flushPromises()
  expect(f.reads).toBe(2)
  f.hn.omo.stream!.onState('retrying', 3000)
  await flushPromises()
  expect(f.reads).toBe(2)
  f.hn.omo.stream!.onState('live')
  await flushPromises()
  expect(f.reads).toBe(3)
})

test('the selected project and the opened files stay across a reload', async () => {
  const f = await setup(answer([proj('p1'), proj('p2', { files: [file('b.txt')] })]))
  f.d.project.value = 'p2'
  const key = fileKey('p2', false, 'b.txt')
  f.d.setOpen(key, false)
  await f.d.refresh()
  expect(f.reads).toBe(2)
  expect(f.d.project.value).toBe('p2')
  expect(f.d.isOpen(key, true)).toBe(false)
  expect(f.d.isOpen(fileKey('p2', false, 'other'), true)).toBe(true)
})

test('a selected project that is gone falls back to the first', async () => {
  const f = await setup(answer([proj('p1'), proj('p2')]))
  f.d.project.value = 'p2'
  f.next.fn = () => answer([proj('p1')])
  await f.d.refresh()
  expect(f.d.project.value).toBe('p1')
})

test('reads that overlap end in one more read, not a pile', async () => {
  const f = await setup(answer([proj('p1')]))
  let release!: () => void
  f.next.fn = () => {
    f.next.fn = () => answer([proj('p1')])
    return new Promise((r) => (release = () => r(answer([proj('p1')]))))
  }
  const a = f.d.refresh()
  const b = f.d.refresh()
  const c = f.d.refresh()
  release()
  await Promise.all([a, b, c])
  await flushPromises()
  expect(f.reads).toBe(3)
})

test('a failed first read shows the server message; a failed reload keeps the diff', async () => {
  const f = await setup(answer([proj('p1')]))
  f.next.fn = () => {
    throw new ApiError(503, 'unavailable', 'git took too long')
  }
  await f.d.refresh()
  expect(f.d.state.value).toBe('ready')
  expect(f.d.current.value?.project).toBe('p1')
  expect(f.d.refreshError.value).toBe('git took too long')
  f.next.fn = () => answer([proj('p1')])
  await f.d.refresh()
  expect(f.d.refreshError.value).toBe('')
})

test('loadFile reads one file and shows it instead of the bounded one until the next reload', async () => {
  const big = file('big.txt', { patch: null, truncated: true, additions: 9000 })
  const f = await setup(answer([proj('p1', { files: [big] })]))
  const key = fileKey('p1', false, 'big.txt')
  expect(f.d.fileOf(key, big).patch).toBeNull()
  await f.d.loadFile('p1', 'big.txt', false)
  expect(f.files).toEqual([{ project: 'p1', path: 'big.txt', uncommitted: false }])
  expect(f.d.fileOf(key, big).patch).toContain('all of it')
  expect(f.d.loadState(key)).toEqual({ loading: false, loaded: true, error: '' })
  await f.d.refresh()
  expect(f.d.fileOf(key, big).patch).toBeNull()
})

test('loadFile shows the server message when the file is gone', async () => {
  const big = file('big.txt', { patch: null, truncated: true })
  const f = await setup(answer([proj('p1', { uncommitted: [big] })]))
  f.fileAnswer.fn = () => {
    throw new ApiError(404, 'not_found', 'The file is not part of this diff.')
  }
  await f.d.loadFile('p1', 'big.txt', true)
  expect(f.d.loadState(fileKey('p1', true, 'big.txt'))).toEqual({ loading: false, loaded: false, error: 'The file is not part of this diff.' })
})

test('startsOpen: many files or a long file start collapsed', () => {
  expect(startsOpen(file('a'), 20)).toBe(true)
  expect(startsOpen(file('a'), 21)).toBe(false)
  expect(startsOpen(file('a', { additions: 300, deletions: 100 }), 1)).toBe(true)
  expect(startsOpen(file('a', { additions: 300, deletions: 101 }), 1)).toBe(false)
})

test('a project tab counts the changed files, and shows no count when nothing changed', () => {
  expect(changedCount(proj('demo', { files: [file('a.txt'), file('b.txt')], uncommitted: [file('c.txt')] }))).toBe(3)
  expect(changedCount(proj('demo', { files: [], uncommitted: [] }))).toBeUndefined()
  expect(changedCount(proj('demo', { state: 'not_started', files: [], uncommitted: [] }))).toBeUndefined()
})

const behind = { local: 'main', name: 'origin/main', behind: 4, ahead: 0 }
/** The server's answer: with base=remote the projects that have a remote-tracking branch compare with it. */
function byBase(q?: Record<string, unknown>): TicketDiff {
  const remote = q?.base === 'remote'
  return answer([
    proj('p1', { remote_base: behind, ...(remote ? { base: 'origin/main', compared_with: 'remote' as const, files: [file('upstream.txt'), file('a.txt')] } : {}) }),
    proj('p2', { remote_base: { ...behind, behind: 0 }, ...(remote ? { base: 'origin/main', compared_with: 'remote' as const } : {}) }),
  ])
}

test('comparing one project with its remote-tracking branch reads it with base=remote; the others stay local', async () => {
  const f = await setup(answer([]))
  f.next.fn = byBase
  await f.d.refresh()
  expect(f.queries.at(-1)).toBeUndefined()
  expect(f.d.remoteOf('p1')).toBe(false)

  await f.d.setRemote('p1', true)
  expect(f.d.remoteOf('p1')).toBe(true)
  expect(f.queries.slice(-2)).toEqual([undefined, { base: 'remote' }])
  const [p1, p2] = f.d.diff.value!.projects
  expect(p1).toMatchObject({ project: 'p1', compared_with: 'remote', base: 'origin/main' })
  expect(p2).toMatchObject({ project: 'p2', compared_with: 'local', base: 'main' })

  // Show more uses the same base for committed files; uncommitted ones have no base.
  await f.d.loadFile('p1', 'upstream.txt', false)
  await f.d.loadFile('p1', 'w.txt', true)
  await f.d.loadFile('p2', 'a.txt', false)
  expect(f.files).toEqual([
    { project: 'p1', path: 'upstream.txt', uncommitted: false, base: 'remote' },
    { project: 'p1', path: 'w.txt', uncommitted: true },
    { project: 'p2', path: 'a.txt', uncommitted: false },
  ])

  // Back to local: one read again.
  await f.d.setRemote('p1', false)
  expect(f.queries.at(-1)).toBeUndefined()
  expect(f.d.diff.value!.projects[0]).toMatchObject({ compared_with: 'local' })
})

test('the choice is kept per ticket and project across reloads and a new tab, not for other tickets', async () => {
  const f = await setup(answer([]))
  f.next.fn = byBase
  await f.d.setRemote('p1', true)
  f.live(ev(20, 'ticket.moved', 4))
  await flushPromises()
  expect(f.d.diff.value!.projects[0]).toMatchObject({ compared_with: 'remote' })

  mounted!.unmount()
  mounted = null
  const g = await setup(answer([]))
  g.next.fn = byBase
  await g.d.refresh()
  expect(g.d.remoteOf('p1')).toBe(true)
  expect(g.d.diff.value!.projects[0]).toMatchObject({ compared_with: 'remote' })
  expect(g.queries.at(-1)).toEqual({ base: 'remote' })
})

test('a refusal of base=remote falls back to the local base and shows the message', async () => {
  const f = await setup(answer([]))
  f.next.fn = (q) => {
    if (q?.base === 'remote') throw new ApiError(422, 'validation', 'The base branch main has no remote-tracking branch in this repository, so the diff cannot be compared with one.')
    return byBase(q)
  }
  await f.d.setRemote('p1', true)
  expect(f.d.remoteOf('p1')).toBe(false)
  expect(f.d.baseError.value).toBe('The base branch main has no remote-tracking branch in this repository, so the diff cannot be compared with one.')
  expect(f.d.state.value).toBe('ready')
  expect(f.d.diff.value!.projects[0]).toMatchObject({ compared_with: 'local' })
})

test('a base behind its remote-tracking branch is said in words; ahead only or equal says nothing', () => {
  expect(behindWords(behind)).toBe('local main is 4 commits behind origin/main, so changes from origin/main can show up here as changes of this ticket.')
  expect(behindWords({ ...behind, behind: 1 })).toBe('local main is 1 commit behind origin/main, so changes from origin/main can show up here as changes of this ticket.')
  expect(behindWords({ ...behind, behind: 0, ahead: 3 })).toBe('')
  expect(behindWords({ ...behind, behind: 0 })).toBe('')
  expect(remoteWords(behind)).toBe("Compared with origin/main, the remote-tracking branch of main, which local main is 4 commits behind, as of the repository's last fetch.")
})

/** A 100-line file "line i" with lines 10 and 90 changed, as git writes it with `c` lines of context. */
function longPatch(c: number | 'all', changed = [10, 90]): string {
  const n = 100
  const k = c === 'all' ? n : c
  const groups: number[][] = []
  for (const x of changed) {
    const last = groups[groups.length - 1]
    if (last && x - last[last.length - 1]! - 1 <= 2 * k) last.push(x)
    else groups.push([x])
  }
  let out = ''
  for (const g of groups) {
    const from = Math.max(1, g[0]! - k)
    const to = Math.min(n, g[g.length - 1]! + k)
    out += `@@ -${from},${to - from + 1} +${from},${to - from + 1} @@\n`
    for (let i = from; i <= to; i++) out += g.includes(i) ? `-line ${i}\n+line ${i} changed\n` : ` line ${i}\n`
  }
  return out
}

const between: Gap = { place: 'between', from: 14, count: 73, before: 13, after: 87 }

describe('hidden lines', () => {
  test('revealing lines reads the file once with the context they need; lines already read need no read', async () => {
    const long = file('long.txt', { patch: longPatch(3), additions: 2, deletions: 2 })
    const f = await setup(answer([proj('p1', { files: [long] })]))
    f.fileAnswer.fn = (q) => file('long.txt', { patch: longPatch(q!.context as number) })
    const key = fileKey('p1', false, 'long.txt')
    expect(f.d.expansionOf(key, long)).toBeNull()
    await f.d.expand('p1', long, false, { kind: 'below', gap: between })
    expect(f.files).toEqual([{ project: 'p1', path: 'long.txt', uncommitted: false, context: '23' }])
    const x = f.d.expansionOf(key, long)!
    expect(x.reveal.ranges).toEqual([{ from: 14, to: 33 }])
    expect(x.wider?.context).toBe(23)
    expect(x.loading).toBe(false)
    // Twenty more right below: still within what was read? No: 53 lines need more context.
    await f.d.expand('p1', long, false, { kind: 'below', gap: { ...between, from: 34, count: 53, before: 33 } })
    expect(f.files).toHaveLength(2)
    // Lines inside what was read come without a read.
    await f.d.expand('p1', long, false, { kind: 'above', gap: { place: 'top', from: 1, count: 6, before: 0, after: 7 } })
    expect(f.files).toHaveLength(2)
    expect(f.d.expansionOf(key, long)!.reveal.ranges).toHaveLength(3)
  })

  test('the whole file asks for all; a remote comparison and an uncommitted file are asked for as such', async () => {
    const long = file('long.txt', { patch: longPatch(3) })
    const f = await setup(answer([proj('p1', { files: [long], uncommitted: [long], compared_with: 'remote' })]))
    f.fileAnswer.fn = () => file('long.txt', { patch: longPatch('all') })
    await f.d.expand('p1', long, false, { kind: 'all' })
    await f.d.expand('p1', long, true, { kind: 'all' })
    expect(f.files).toEqual([
      { project: 'p1', path: 'long.txt', uncommitted: false, context: 'all', base: 'remote' },
      { project: 'p1', path: 'long.txt', uncommitted: true, context: 'all' },
    ])
    expect(f.d.expansionOf(fileKey('p1', false, 'long.txt'), long)!.reveal.all).toBe(true)
    expect(f.d.expansionOf(fileKey('p1', true, 'long.txt'), long)!.reveal.all).toBe(true)
  })

  test('kept across a read that leaves the file as it was, dropped when its patch changed', async () => {
    const long = file('long.txt', { patch: longPatch(3) })
    const other = file('other.txt', { patch: longPatch(3) })
    const f = await setup(answer([proj('p1', { files: [long, other] })]))
    f.fileAnswer.fn = () => file('x', { patch: longPatch('all') })
    await f.d.expand('p1', long, false, { kind: 'all' })
    await f.d.expand('p1', other, false, { kind: 'all' })
    const changed = file('other.txt', { patch: longPatch(3, [10, 91]) })
    f.next.fn = () => answer([proj('p1', { files: [long, changed] })])
    await f.d.refresh()
    expect(f.d.expansionOf(fileKey('p1', false, 'long.txt'), long)?.reveal.all).toBe(true)
    expect(f.d.expansionOf(fileKey('p1', false, 'other.txt'), changed)).toBeNull()
    // And a file gone from the diff is forgotten.
    f.next.fn = () => answer([proj('p1', { files: [] })])
    await f.d.refresh()
    expect(f.d.expansionOf(fileKey('p1', false, 'long.txt'), long)).toBeNull()
  })

  test('an older answer never replaces a newer one', async () => {
    const long = file('long.txt', { patch: longPatch(3) })
    const f = await setup(answer([proj('p1', { files: [long] })]))
    const releases: Array<() => void> = []
    f.fileAnswer.fn = (q) => new Promise((r) => releases.push(() => r(file('long.txt', { patch: longPatch(q!.context as number | 'all') }))))
    const first = f.d.expand('p1', long, false, { kind: 'below', gap: between })
    await flushPromises()
    const second = f.d.expand('p1', long, false, { kind: 'all' })
    await flushPromises()
    releases[1]!()
    await second
    releases[0]!()
    await first
    const x = f.d.expansionOf(fileKey('p1', false, 'long.txt'), long)!
    expect(x.wider?.context).toBe('all')
    expect(x.reveal.all).toBe(true)
  })

  test('a refusal shows where it was asked for, and a file that changed meanwhile is not mixed in', async () => {
    const long = file('long.txt', { patch: longPatch(3) })
    const f = await setup(answer([proj('p1', { files: [long] })]))
    f.fileAnswer.fn = () => {
      throw new ApiError(422, 'validation', 'This file is too large to show with that much context: its diff would be over 1 MiB.')
    }
    await f.d.expand('p1', long, false, { kind: 'all' })
    let x = f.d.expansionOf(fileKey('p1', false, 'long.txt'), long)!
    expect(x).toMatchObject({ loading: false, errorAt: 0, error: 'This file is too large to show with that much context: its diff would be over 1 MiB.' })
    expect(x.reveal.all).toBe(false)
    f.fileAnswer.fn = () => file('long.txt', { patch: longPatch('all', [10, 50, 90]) })
    await f.d.expand('p1', long, false, { kind: 'below', gap: between })
    x = f.d.expansionOf(fileKey('p1', false, 'long.txt'), long)!
    expect(x).toMatchObject({ errorAt: 14, wider: null })
    expect(x.error).toContain('changed since the diff was read')
  })
})
