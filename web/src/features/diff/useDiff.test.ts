import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, expect, test } from 'vitest'
import { defineComponent } from 'vue'
import { ApiError } from '../../api/client'
import type { ApiEvent, DiffFile, ProjectDiff, TicketDiff } from '../../api/types'
import { project } from '../../testing/fakeOmo'
import { ticket } from '../board/testing'
import { mountPanel, ok, panelHarness, type PanelHarness } from '../ticket/panelTesting'
import { changedCount, fileKey, startsOpen, useDiff, type DiffController } from './useDiff'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
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
  next: { fn: () => TicketDiff | Promise<TicketDiff> }
  fileAnswer: { fn: () => DiffFile }
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
    next: { fn: (): TicketDiff | Promise<TicketDiff> => first },
    fileAnswer: { fn: (): DiffFile => file('big.txt', { patch: '@@ -1 +1 @@\n+all of it\n' }) },
  } as unknown as Fixture
  const client = hn.session.client
  const get = client.get.bind(client)
  client.get = (async (path: string, query?: Record<string, unknown>) => {
    if (path === '/api/tickets/4/diff') {
      f.reads++
      return ok(await f.next.fn())
    }
    if (path === '/api/tickets/4/diff/file') {
      f.files.push(query ?? {})
      return ok(f.fileAnswer.fn())
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
