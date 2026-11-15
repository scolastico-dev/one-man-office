import { flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, describe, expect, test, vi } from 'vitest'
import { defineComponent, nextTick } from 'vue'
import type { Comment, MentionNotice, Notifications, TicketSummary } from '../../api/types'
import { needsMe } from '../../board/filters'
import { announcements } from '../../components/ui/useAnnouncer'
import { MemoryStorage, project } from '../../testing/fakeOmo'
import { ticket } from '../board/testing'
import { mountPanel, ok, panelHarness, type PanelHarness } from '../ticket/panelTesting'
import { DESKTOP_KEY, type NotificationCtor } from './systemNotify'
import { createNotifications, NOTICE_LIMIT, NOTICE_WINDOW_MS, useNotifications, type NotificationsState } from './useNotifications'

let mounted: VueWrapper | null = null
afterEach(() => {
  mounted?.unmount()
  mounted = null
})

function comment(id: number, o: Partial<Comment> = {}): Comment {
  return {
    id,
    ticket: 4,
    author: { role: 'developer', name: 'calm-badger' },
    body: `@user c${id}`,
    reply_to: null,
    question_to: '' as Comment['question_to'],
    answered_by: null,
    mentions: [{ role: 'user' }],
    created_at: '2026-11-15T10:00:00.000Z',
    ...o,
  }
}
const notice = (id: number, unread: boolean, o: Partial<Comment> = {}): MentionNotice => ({ comment: comment(id, o), title: `Ticket of ${id}`, unread })

interface Fixture {
  hn: PanelHarness
  n: NotificationsState
  /** What `GET /api/notifications` answers next. */
  server: { value: Notifications }
  reads: () => number
  marks: () => number[]
}

async function setup(tickets: TicketSummary[], first: Notifications): Promise<Fixture> {
  const hn = await panelHarness((omo) => {
    omo.tickets = tickets
    omo.projects = [project('demo')]
  })
  const server = { value: first }
  let reads = 0
  const client = hn.session.client
  const get = client.get.bind(client)
  client.get = ((path: string, query?: Record<string, unknown>) => {
    if (path === '/api/notifications') {
      reads++
      return Promise.resolve(ok(server.value))
    }
    return get(path, query as never)
  }) as typeof client.get
  const marks: number[] = []
  hn.write.fn = (s) => {
    expect(s.method).toBe('PUT')
    expect(s.path).toBe('/api/notifications/seen')
    const through = (s.body as { through: number }).through
    marks.push(through)
    const seen = Math.max(server.value.seen_through, through)
    server.value = { ...server.value, seen_through: seen, mentions: server.value.mentions.map((m) => ({ ...m, unread: m.comment.id > seen })) }
    server.value.unread = server.value.mentions.filter((m) => m.unread).length
    return ok(server.value)
  }
  let n!: NotificationsState
  const Host = defineComponent({
    setup() {
      n = useNotifications()
      return () => null
    },
  })
  const { wrapper } = await mountPanel(hn, Host, {})
  mounted = wrapper
  await flushPromises()
  return { hn, n, server, reads: () => reads, marks: () => marks }
}

const empty: Notifications = { mentions: [], seen_through: 0, unread: 0 }
const unreadIds = (n: NotificationsState) => n.mentions.value.filter((m) => n.isUnread(m)).map((m) => m.comment.id)

test('waiting items equal the needs-me rule: question, attention on my tickets incl. failed step, human review', async () => {
  const tickets = [
    ticket({ number: 1, title: 'Asks', column: 'in_progress', questions_for_user: 2 }),
    ticket({ number: 2, title: 'Stuck', column: 'in_progress', attention: 'permission denied', failed_step: 'agent', supervisor: 'user' }),
    ticket({ number: 3, title: 'Stuck for the CEO', column: 'in_progress', attention: 'x', supervisor: 'ceo' }),
    ticket({ number: 4, title: 'Review me', column: 'review', review_stage: 'human' }),
    ticket({ number: 5, title: 'Agent review', column: 'review', review_stage: 'agent' }),
    ticket({ number: 6, title: 'Quiet', column: 'planning' }),
    ticket({ number: 7, title: 'Merge failed', column: 'review', review_stage: 'merging', attention: 'conflict', failed_step: 'merge', supervisor: 'user' }),
    ticket({ number: 8, title: 'Stuck without a step', column: 'in_progress', attention: 'look at it', supervisor: 'user' }),
  ]
  const f = await setup(tickets, empty)
  const got = f.n.waiting.value
  expect(got.map((w) => w.ticket).sort()).toEqual(tickets.filter(needsMe).map((t) => t.number).sort())
  expect(got.find((w) => w.ticket === 1)).toMatchObject({ kind: 'question', title: 'Asks', text: '2 questions for you' })
  // The failed step in the board card's words.
  expect(got.find((w) => w.ticket === 2)).toMatchObject({ kind: 'attention', text: 'needs attention · the agent keeps failing' })
  expect(got.find((w) => w.ticket === 8)).toMatchObject({ kind: 'attention', text: 'needs attention' })
  expect(got.find((w) => w.ticket === 4)).toMatchObject({ kind: 'review', text: 'your review' })
  expect(got.find((w) => w.ticket === 7)).toMatchObject({ kind: 'attention', text: 'needs attention · merge blocked' })

  // Resolved: it leaves the list, live.
  f.hn.omo.change('ticket.updated', { ...tickets[0]!, questions_for_user: 0 })
  expect(f.n.waiting.value.map((w) => w.ticket)).not.toContain(1)
  f.hn.omo.change('ticket.updated', { ...tickets[5]!, questions_for_user: 1 })
  expect(f.n.waiting.value.find((w) => w.ticket === 6)).toMatchObject({ kind: 'question', text: 'question for you' })
})

test('a comment mentioning @user from an agent adds an unread mention; my own does not', async () => {
  const f = await setup([ticket({ number: 4, title: 'Drag a card' })], { mentions: [notice(10, false)], seen_through: 10, unread: 0 })
  expect(f.n.unread.value).toBe(0)
  f.hn.omo.emit('comment.created', { comment: comment(11) }, 4)
  f.hn.omo.emit('comment.created', { comment: comment(12, { author: { role: 'user' } }) }, 4)
  f.hn.omo.emit('comment.created', { comment: comment(13, { mentions: [{ role: 'developer' }] }) }, 4)
  f.hn.omo.emit('comment.created', { comment: comment(14, { author: { role: 'system' } }) }, 4)
  // A replay of the same comment is one mention.
  f.hn.omo.emit('comment.created', { comment: comment(11) }, 4)
  expect(f.n.mentions.value.map((m) => m.comment.id)).toEqual([14, 11, 10])
  expect(f.n.mentions.value[1]!.title).toBe('Drag a card')
  expect(f.n.unread.value).toBe(2)
  expect(unreadIds(f.n)).toEqual([14, 11])
})

test('notifications.seen from another tab lowers the unread count; the marker never moves back', async () => {
  const f = await setup([ticket({ number: 4 })], { mentions: [notice(30, true), notice(20, true), notice(10, true)], seen_through: 0, unread: 3 })
  expect(f.n.unread.value).toBe(3)
  f.hn.omo.emit('notifications.seen', { through: 20 })
  expect(f.n.seenThrough.value).toBe(20)
  expect(f.n.unread.value).toBe(1)
  f.hn.omo.emit('notifications.seen', { through: 5 })
  expect(f.n.seenThrough.value).toBe(20)
  expect(f.n.unread.value).toBe(1)
  // A re-read that answers an older marker does not move it back either.
  f.server.value = { ...f.server.value, seen_through: 10 }
  await f.n.refresh()
  expect(f.n.seenThrough.value).toBe(20)
  expect(f.n.unread.value).toBe(1)
})

test('mentions beyond the first page count until the marker passes them', async () => {
  const f = await setup([ticket({ number: 4 })], { mentions: [notice(30, true), notice(20, true)], seen_through: 0, unread: 5 })
  expect(f.n.unread.value).toBe(5)
  f.hn.omo.emit('notifications.seen', { through: 25 })
  expect(f.n.unread.value).toBe(1)
})

test('a reconnect and a resync read the list again and keep live mentions', async () => {
  const f = await setup([ticket({ number: 4 })], { mentions: [notice(10, true)], seen_through: 0, unread: 1 })
  expect(f.reads()).toBe(1)
  // Missed while away: 11 arrived and another tab marked through 10.
  f.server.value = { mentions: [notice(11, true), notice(10, false)], seen_through: 10, unread: 1 }
  f.hn.omo.stream!.onState('retrying', 1000)
  f.hn.omo.stream!.onState('live')
  await flushPromises()
  expect(f.reads()).toBe(2)
  expect(f.n.mentions.value.map((m) => m.comment.id)).toEqual([11, 10])
  expect(f.n.seenThrough.value).toBe(10)
  expect(f.n.unread.value).toBe(1)

  f.server.value = { mentions: [notice(12, true), notice(11, true), notice(10, false)], seen_through: 10, unread: 2 }
  f.hn.omo.stream!.onResync(f.hn.omo.eventId)
  await flushPromises()
  expect(f.reads()).toBeGreaterThanOrEqual(3)
  expect(f.n.mentions.value.map((m) => m.comment.id)).toEqual([12, 11, 10])
  expect(f.n.unread.value).toBe(2)
})

test('marking read moves the marker only over a run of read mentions; the rest is read on this page', async () => {
  const f = await setup([ticket({ number: 4 })], { mentions: [notice(30, true), notice(20, true), notice(10, true)], seen_through: 5, unread: 3 })
  // 20 is read here, but 10 below it is not: the marker cannot pass 10.
  await f.n.markRead(20)
  expect(f.marks()).toEqual([])
  expect(unreadIds(f.n)).toEqual([30, 10])
  // 10 read: everything up to 20 is read, the marker moves to 20.
  await f.n.markRead(10)
  expect(f.marks()).toEqual([20])
  expect(f.n.seenThrough.value).toBe(20)
  expect(unreadIds(f.n)).toEqual([30])
  // Read already: nothing is sent.
  await f.n.markRead(10)
  expect(f.marks()).toEqual([20])
  await f.n.markAllRead()
  expect(f.marks()).toEqual([20, 30])
  expect(f.n.unread.value).toBe(0)
})

test('a failed mark keeps the item read on the page and reports the reason', async () => {
  const f = await setup([ticket({ number: 4 })], { mentions: [notice(10, true)], seen_through: 0, unread: 1 })
  f.hn.write.fn = () => {
    throw new Error('Cannot reach omo.')
  }
  await f.n.markAllRead()
  expect(f.n.error.value).toBe('Cannot reach omo.')
  expect(f.n.unread.value).toBe(0)
})

test('a bell that unmounts and mounts again on the same board still announces and re-reads', async () => {
  const t = ticket({ number: 4, title: 'Review me', column: 'in_progress' })
  const f = await setup([t], empty)
  mounted!.unmount()
  let again!: NotificationsState
  const Host = defineComponent({
    setup() {
      again = useNotifications()
      return () => null
    },
  })
  mounted = (await mountPanel(f.hn, Host, {})).wrapper
  expect(again).toBe(f.n)

  f.hn.omo.change('ticket.updated', { ...t, column: 'review', review_stage: 'human' })
  await flushPromises()
  expect(announcements.polite.value).toBe('#4 waits for your review')

  const before = f.reads()
  f.hn.omo.stream!.onState('retrying', 1000)
  f.hn.omo.stream!.onState('live')
  await flushPromises()
  expect(f.reads()).toBe(before + 1)
})

describe('a flood of mentions', () => {
  interface Note {
    title: string
    body: string | undefined
    tag: string | undefined
  }

  async function flood() {
    const hn = await panelHarness((omo) => {
      omo.tickets = [ticket({ number: 4 }), ticket({ number: 5 })]
      omo.projects = [project('demo')]
    })
    const client = hn.session.client
    const get = client.get.bind(client)
    client.get = ((path: string, query?: Record<string, unknown>) =>
      path === '/api/notifications' ? Promise.resolve(ok(empty)) : get(path, query as never)) as typeof client.get
    const notes: Note[] = []
    class Recorder {
      static permission: NotificationPermission = 'granted'
      static requestPermission = () => Promise.resolve('granted' as NotificationPermission)
      onclick = null
      constructor(title: string, o?: NotificationOptions) {
        notes.push({ title, body: o?.body, tag: o?.tag })
      }
      close() {}
    }
    const storage = new MemoryStorage()
    storage.setItem(DESKTOP_KEY, 'on')
    const n = createNotifications({
      client,
      store: hn.session.store,
      router: null,
      Notification: Recorder as unknown as NotificationCtor,
      document: { visibilityState: 'hidden' },
      storage: () => storage,
    })
    await flushPromises()
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'Date'] })
    const heard: string[] = []
    let id = 100
    async function mention(t: number) {
      id++
      hn.omo.emit('comment.created', { comment: comment(id, { ticket: t }) }, t)
      // The region is cleared at once when something is said, and filled in the next tick.
      const said = announcements.polite.value === ''
      await nextTick()
      if (said) heard.push(announcements.polite.value)
    }
    return { n, notes, heard, mention }
  }

  afterEach(() => {
    vi.useRealTimers()
  })

  test('at most NOTICE_LIMIT announcements a minute, then one summary of the rest', async () => {
    const f = await flood()
    for (let i = 0; i < NOTICE_LIMIT + 3; i++) await f.mention(4)
    expect(f.heard).toHaveLength(NOTICE_LIMIT)
    expect(f.heard.every((h) => h === 'New mention on #4 from developer calm-badger')).toBe(true)
    vi.advanceTimersByTime(NOTICE_WINDOW_MS)
    await nextTick()
    expect(announcements.polite.value).toBe('And 3 more notifications in the bell.')
    // A new minute: announced again.
    await f.mention(5)
    expect(f.heard.at(-1)).toBe('New mention on #5 from developer calm-badger')
  })

  test('one desktop notification per ticket replaces the older; past the limit one summary is updated', async () => {
    const f = await flood()
    await f.mention(4)
    await f.mention(4)
    await f.mention(5)
    expect(f.notes.map((x) => x.tag)).toEqual(['ticket:4', 'ticket:4', 'ticket:5'])
    for (let i = 0; i < NOTICE_LIMIT; i++) await f.mention(4)
    expect(f.notes.slice(0, NOTICE_LIMIT).every((x) => x.tag?.startsWith('ticket:'))).toBe(true)
    const more = f.notes.slice(NOTICE_LIMIT)
    expect(more.map((x) => x.tag)).toEqual(['more', 'more', 'more'])
    expect(more.map((x) => x.title)).toEqual(['1 more notification', '2 more notifications', '3 more notifications'])
    vi.advanceTimersByTime(NOTICE_WINDOW_MS)
    await f.mention(5)
    expect(f.notes.at(-1)).toMatchObject({ tag: 'ticket:5', title: 'Mention on #5' })
    expect(f.n.unread.value).toBe(NOTICE_LIMIT + 4)
  })
})
