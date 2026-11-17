// What the bell shows, shared by the whole app:
// - what waits for the user, derived live from the board with the same rule
//   as the "Needs me" filter (and the server's `needs_me`); no read state,
//   an item leaves when it is resolved;
// - comments that mention the user, newest first, read from the server and
//   kept live through the stream, with a seen marker that only moves forward
//   and that other tabs follow through `notifications.seen`.
// New items that arrive after the page loaded are announced politely and,
// when the user switched them on, raise a desktop notification.
import { computed, effectScope, ref, shallowRef, watch, type ComputedRef, type Ref } from 'vue'
import { useRouter, type Router } from 'vue-router'
import type { ApiClient } from '../../api/client'
import { getNotifications, getTicketThread, markNotificationsSeen } from '../../api/endpoints'
import { logError } from '../../api/log'
import type { Actor, ApiEvent, Comment, CommentCreatedData, MentionNotice, Notifications, NotificationsSeen, QuestionAnsweredData, TicketSummary } from '../../api/types'
import { failedStepText } from '../../board/badges'
import { needsMe } from '../../board/filters'
import { useAnnouncer } from '../../components/ui/useAnnouncer'
import { useSession } from '../../session'
import type { TicketKeys } from '../../locations'
import { openTicketLocation } from '../ticket/openTicket'
import { useBoard, type BoardStore } from '../../store'
import { createSystemNotifier, plainText, type NotificationCtor, type SystemItem, type SystemNotifier } from './systemNotify'

/** The most mentions the list keeps, as the server sends. */
export const MAX_MENTIONS = 50
/** The longest text an item shows; the rest is cut (the list cuts further to its width). */
export const SNIPPET_MAX = 300
/**
 * At most this many announcements, and this many desktop notifications, in
 * NOTICE_WINDOW_MS: an agent that keeps mentioning the user must not flood
 * the screen reader or the system. The rest are summed up.
 */
export const NOTICE_LIMIT = 5
export const NOTICE_WINDOW_MS = 60_000

export type WaitingKind = 'question' | 'attention' | 'review'

export interface WaitingItem {
  kind: WaitingKind
  ticket: number
  /** The ticket's title, as visible single-line text. */
  title: string
  /** Why it waits, in words. */
  text: string
}

/** A ticket and what its address shows. */
export interface TicketTarget extends TicketKeys {
  ticket: number
}

/** A desktop notification's item and where a click on it leads; a summary leads nowhere. */
export interface NoticeItem extends SystemItem {
  to?: TicketTarget
}

export interface NotificationsState {
  waiting: ComputedRef<WaitingItem[]>
  /** Newest first. */
  mentions: Readonly<Ref<readonly MentionNotice[]>>
  /** Unread mentions, including those older than the list. */
  unread: ComputedRef<number>
  /** The highest comment id the user has seen; only moves forward. */
  seenThrough: Readonly<Ref<number>>
  /** Why the list could not be read or marked, in words; empty otherwise. */
  error: Readonly<Ref<string>>
  desktop: SystemNotifier<NoticeItem>
  isUnread(m: MentionNotice): boolean
  /** Marks one mention read: on the server when every older one is read too, else on this page. */
  markRead(id: number): Promise<void>
  markAllRead(): Promise<void>
  /** Reads the mention list again and merges it. */
  refresh(): Promise<void>
  /** Where a waiting item leads; for a question, the open question in the thread. */
  targetOf(item: WaitingItem): Promise<TicketTarget>
}

export interface NotificationsDeps {
  client: ApiClient
  store: BoardStore
  router: Router | null
  Notification?: NotificationCtor | undefined
  document?: { readonly visibilityState: DocumentVisibilityState }
  storage?: () => Storage | null
}

const KIND_ORDER: Record<WaitingKind, number> = { question: 0, review: 1, attention: 2 }

/** The kind of a ticket that needs the user, the first reason that applies. */
function kindOf(t: TicketSummary): WaitingKind {
  if (t.attention !== '' && t.supervisor === 'user') return 'attention'
  if (t.questions_for_user > 0) return 'question'
  return 'review'
}

function textOf(t: TicketSummary, kind: WaitingKind): string {
  if (kind === 'attention') return t.failed_step ? `needs attention · ${failedStepText(t.failed_step)}` : 'needs attention'
  if (kind === 'review') return 'your review'
  const n = t.questions_for_user
  return n === 1 ? 'question for you' : `${n} questions for you`
}

/** Who wrote a comment, as visible text: "developer calm-badger". */
export function authorOf(a: Actor): string {
  return plainText(a.name ? `${a.role} ${a.name}` : a.role, 80)
}

/** A comment's body as one line of visible text. */
export function snippetOf(c: Comment): string {
  return plainText(c.body, SNIPPET_MAX)
}

function mentionsUser(c: Comment): boolean {
  return c.author.role !== 'user' && c.mentions.some((m) => m.role === 'user')
}

function isQuestionForUser(c: Comment): boolean {
  return c.author.role !== 'user' && c.question_to === 'user' && c.answered_by === null
}

/** A new comment the bell announces itself (a mention of the user or a question to the user); others need not announce it again. */
export function announcedByBell(c: Comment): boolean {
  return mentionsUser(c) || isQuestionForUser(c)
}

function messageOf(e: unknown, fallback: string): string {
  return e instanceof Error && e.message !== '' ? e.message : fallback
}

/** Counts uses in a sliding window. */
function rateLimit(max: number, windowMs: number) {
  const times: number[] = []
  const drop = () => {
    const now = Date.now()
    while (times.length > 0 && times[0]! <= now - windowMs) times.shift()
    return now
  }
  return {
    /** Uses one when fewer than `max` were used in the window. */
    take(): boolean {
      const now = drop()
      if (times.length >= max) return false
      times.push(now)
      return true
    },
    /** How long until one can be used again. */
    wait(): number {
      const now = drop()
      return times.length < max ? 0 : times[0]! + windowMs - now
    },
  }
}

function getStorage(): Storage | null {
  try {
    return typeof localStorage === 'undefined' ? null : localStorage
  } catch {
    return null
  }
}

/**
 * One app's notifications; `useNotifications` shares one per board. Exported for tests.
 * The instance outlives the component that asked first (the board unmounts
 * while locked and mounts again), so its watchers live in a scope of its own.
 */
export function createNotifications(deps: NotificationsDeps): NotificationsState {
  return effectScope(true).run(() => build(deps))!
}

function build(deps: NotificationsDeps): NotificationsState {
  const { client, store, router } = deps
  const announcer = useAnnouncer()

  const mentions = shallowRef<readonly MentionNotice[]>([])
  const seenThrough = ref(0)
  /** Unread mentions older than the list (the server counts them, the list leaves them out). */
  const beyond = ref(0)
  /** Mentions read on this page whose id the marker cannot reach yet. */
  const readHere = ref<ReadonlySet<number>>(new Set())
  const error = ref('')
  /** Open questions to the user seen on the stream, by ticket, oldest first. */
  const openQuestions = new Map<number, number[]>()
  let loaded = false

  const desktop = createSystemNotifier<NoticeItem>({
    Notification: deps.Notification,
    document: deps.document ?? document,
    storage: deps.storage ?? getStorage,
    onClick: (item) => {
      if (item.to !== undefined) void router?.push(openTicketLocation(store, item.to.ticket, router.currentRoute.value, item.to))
    },
  })

  const waiting = computed<WaitingItem[]>(() => {
    const out: WaitingItem[] = []
    for (const n of store.state.tickets.keys()) {
      const t = store.view(n)
      if (t === undefined || !needsMe(t)) continue
      const kind = kindOf(t)
      out.push({ kind, ticket: t.number, title: plainText(t.title, 200), text: textOf(t, kind) })
    }
    return out.sort((a, b) => KIND_ORDER[a.kind] - KIND_ORDER[b.kind] || a.ticket - b.ticket)
  })

  function isUnread(m: MentionNotice): boolean {
    return m.comment.id > seenThrough.value && !readHere.value.has(m.comment.id)
  }

  const unread = computed(() => mentions.value.filter(isUnread).length + beyond.value)

  /** Moves the marker forward (never back) and forgets what it now covers. */
  function advance(through: number): void {
    if (through <= seenThrough.value) return
    seenThrough.value = through
    const oldest = mentions.value[mentions.value.length - 1]
    if (oldest === undefined || through >= oldest.comment.id) beyond.value = 0
    if ([...readHere.value].some((id) => id <= through)) readHere.value = new Set([...readHere.value].filter((id) => id > through))
  }

  /** Joins mentions into the list by comment id, newest first, at most MAX_MENTIONS. */
  function join(incoming: readonly MentionNotice[]): MentionNotice[] {
    const byId = new Map<number, MentionNotice>()
    for (const m of mentions.value) byId.set(m.comment.id, m)
    const fresh: MentionNotice[] = []
    for (const m of incoming) {
      if (!byId.has(m.comment.id)) fresh.push(m)
      byId.set(m.comment.id, m)
    }
    mentions.value = [...byId.values()].sort((a, b) => b.comment.id - a.comment.id).slice(0, MAX_MENTIONS)
    return fresh
  }

  function install(data: Notifications): MentionNotice[] {
    const fresh = join(data.mentions)
    const listed = data.mentions.filter((m) => m.comment.id > data.seen_through).length
    const oldest = data.mentions[data.mentions.length - 1]
    beyond.value = Math.max(0, data.unread - listed)
    advance(data.seen_through)
    if (oldest !== undefined && seenThrough.value >= oldest.comment.id) beyond.value = 0
    return fresh
  }

  let inFlight: Promise<void> | null = null
  let again = false
  async function readOnce(): Promise<void> {
    const res = await getNotifications(client)
    const fresh = install(res.data)
    error.value = ''
    if (!loaded) {
      // Present when the page loaded: never announced, never on the desktop.
      loaded = true
      desktop.ignore(mentions.value.map((m) => `mention:${m.comment.id}`))
      return
    }
    for (const m of fresh) if (isUnread(m)) arrivedMention(m)
  }
  function refresh(): Promise<void> {
    if (inFlight !== null) {
      again = true
      return inFlight
    }
    inFlight = (async () => {
      try {
        do {
          again = false
          await readOnce()
        } while (again)
      } catch (e) {
        logError('notifications', e)
        error.value = messageOf(e, 'The notifications could not be read.')
      } finally {
        inFlight = null
      }
    })()
    return inFlight
  }

  const spoken = rateLimit(NOTICE_LIMIT, NOTICE_WINDOW_MS)
  let unspoken = 0
  let summary: ReturnType<typeof setTimeout> | undefined
  /** Announces politely, within the limit; what is left out is summed up once the window allows. */
  function say(text: string): void {
    if (spoken.take()) {
      announcer.polite(text)
      return
    }
    unspoken++
    summary ??= setTimeout(sayRest, spoken.wait())
  }
  function sayRest(): void {
    summary = undefined
    const n = unspoken
    unspoken = 0
    if (n === 0) return
    spoken.take()
    announcer.polite(`And ${n} more notification${n === 1 ? '' : 's'} in the bell.`)
  }

  const shownOnDesktop = rateLimit(NOTICE_LIMIT, NOTICE_WINDOW_MS)
  let notShown = 0
  let summaries = 0
  /** One notification per ticket (a newer replaces the older); past the limit, one summary that counts the rest. */
  function toDesktop(item: NoticeItem & { to: TicketTarget }): void {
    const tagged = { ...item, tag: `ticket:${item.to.ticket}` }
    if (!desktop.canShow() || shownOnDesktop.take()) {
      if (desktop.notify(tagged)) notShown = 0
      return
    }
    desktop.ignore([item.key])
    notShown++
    desktop.notify({ key: `more:${++summaries}`, tag: 'more', title: `${notShown} more notification${notShown === 1 ? '' : 's'}`, body: 'Open omo to see them in the bell.' })
  }

  function arrivedMention(m: MentionNotice): void {
    const c = m.comment
    const by = authorOf(c.author)
    say(`New mention on #${c.ticket} from ${by}`)
    toDesktop({ key: `mention:${c.id}`, title: `Mention on #${c.ticket}`, body: `${by}: ${snippetOf(c)}`, to: commentTarget(c.ticket, c.id) })
  }

  function arrivedQuestion(c: Comment): void {
    const by = authorOf(c.author)
    say(`New question for you on #${c.ticket} from ${by}`)
    toDesktop({ key: `question:${c.id}`, title: `Question for you on #${c.ticket}`, body: `${by}: ${snippetOf(c)}`, to: commentTarget(c.ticket, c.id) })
  }

  function arrivedWaiting(w: WaitingItem): void {
    const what = w.kind === 'review' ? 'waits for your review' : w.text
    say(`#${w.ticket} ${what}`)
    toDesktop({ key: `${w.kind}:${w.ticket}:${store.state.lastEventId}`, title: `#${w.ticket} ${what}`, body: w.title, to: { ticket: w.ticket } })
  }

  function onComment(ev: ApiEvent): void {
    const c = (ev.data as CommentCreatedData | null)?.comment
    if (c === undefined || c === null || typeof c.id !== 'number') return
    let notified = false
    if (isQuestionForUser(c)) {
      const list = openQuestions.get(c.ticket) ?? []
      if (!list.includes(c.id)) {
        openQuestions.set(c.ticket, [...list, c.id])
        arrivedQuestion(c)
        notified = true
      }
    }
    if (!mentionsUser(c)) return
    const fresh = join([{ comment: c, title: store.view(c.ticket)?.title ?? '', unread: true }])
    for (const m of fresh) if (!notified && isUnread(m)) arrivedMention(m)
  }

  function onAnswered(ev: ApiEvent): void {
    const d = ev.data as QuestionAnsweredData | null
    if (d === null || typeof d.ticket !== 'number') return
    const list = openQuestions.get(d.ticket)
    if (list !== undefined) openQuestions.set(d.ticket, list.filter((id) => id !== d.question))
  }

  store.onEvent('comment.created', onComment)
  store.onEvent('question.answered', onAnswered)
  store.onEvent('notifications.seen', (ev) => {
    const through = (ev.data as NotificationsSeen | null)?.through
    if (typeof through === 'number') advance(through)
  })
  // A snapshot or a stream that is live again after a break: mentions in between may have been missed.
  store.onSnapshot(() => void refresh())
  watch(
    () => store.connection.value,
    (now, before) => {
      if (now === 'live' && before !== 'live' && loaded) void refresh()
    },
  )

  // Waiting items that appear after the first look at the board are new.
  let shown: Map<number, WaitingKind> | null = null
  watch(
    waiting,
    (items) => {
      if (!store.state.loaded) return
      const now = new Map(items.map((w) => [w.ticket, w.kind]))
      if (shown !== null) {
        for (const w of items) {
          // A question is announced from its comment, which says more.
          if (w.kind !== 'question' && shown.get(w.ticket) !== w.kind) arrivedWaiting(w)
        }
      }
      shown = now
    },
    { immediate: true },
  )

  async function mark(through: number): Promise<void> {
    try {
      const res = await markNotificationsSeen(client, through)
      advance(res.data.seen_through)
      error.value = ''
    } catch (e) {
      error.value = messageOf(e, 'The mentions could not be marked as read.')
    }
  }

  async function markRead(id: number): Promise<void> {
    if (id <= seenThrough.value || readHere.value.has(id)) return
    readHere.value = new Set([...readHere.value, id])
    if (beyond.value > 0) return
    // The marker is a high-water mark: it can only pass a run of read mentions.
    let through = seenThrough.value
    for (const m of [...mentions.value].reverse()) {
      if (m.comment.id <= seenThrough.value) continue
      if (!readHere.value.has(m.comment.id)) break
      through = m.comment.id
    }
    if (through > seenThrough.value) await mark(through)
  }

  async function markAllRead(): Promise<void> {
    const newest = mentions.value[0]
    if (newest === undefined || newest.comment.id <= seenThrough.value) return
    readHere.value = new Set([...readHere.value, ...mentions.value.map((m) => m.comment.id)])
    beyond.value = 0
    await mark(newest.comment.id)
  }

  async function targetOf(item: WaitingItem): Promise<TicketTarget> {
    if (item.kind !== 'question') return { ticket: item.ticket }
    const known = openQuestions.get(item.ticket)
    const last = known?.[known.length - 1]
    if (last !== undefined) return commentTarget(item.ticket, last)
    try {
      const res = await getTicketThread(client, item.ticket)
      const answered = new Set<number>()
      for (const ev of res.data.events) if (ev.kind === 'question.answered') answered.add((ev.data as QuestionAnsweredData).question)
      let found: number | null = null
      for (const ev of res.data.events) {
        if (ev.kind !== 'comment.created') continue
        const c = (ev.data as CommentCreatedData).comment
        if (isQuestionForUser(c) && !answered.has(c.id)) found = c.id
      }
      if (found !== null) return commentTarget(item.ticket, found)
    } catch (e) {
      logError('notifications', e)
    }
    return { ticket: item.ticket, tab: 'comments' }
  }

  void refresh()

  return {
    waiting,
    mentions,
    unread,
    seenThrough,
    error,
    desktop,
    isUnread,
    markRead,
    markAllRead,
    refresh,
    targetOf,
  }
}

/** A comment in its ticket's thread. */
export function commentTarget(ticket: number, comment: number): TicketTarget {
  return { ticket, tab: 'comments', comment }
}

const shared = new WeakMap<BoardStore, NotificationsState>()

function browserNotification(): NotificationCtor | undefined {
  try {
    const n = (globalThis as { Notification?: unknown }).Notification
    return typeof n === 'function' ? (n as NotificationCtor) : undefined
  } catch {
    return undefined
  }
}

/** The app's notifications (one per board); call from a component's setup. */
export function useNotifications(): NotificationsState {
  const store = useBoard()
  let n = shared.get(store)
  if (n === undefined) {
    n = createNotifications({ client: useSession().client, store, router: useRouter() ?? null, Notification: browserNotification() })
    shared.set(store, n)
  }
  return n
}
