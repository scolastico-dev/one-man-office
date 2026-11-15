// A stand-in for omo in component tests: answers the client's reads from
// in-memory data, checks the login token the way the real client reports it,
// and replaces the event stream with handlers the test drives.
import { ApiError, type ApiClient, type ApiResult, type ClientOptions, type Query } from '../api/client'
import type { connectEventStream, StreamHandlers } from '../api/eventStream'
import type { ApiEvent, General, Project, ServerInfo, SettingsResponse, TicketList, TicketSummary } from '../api/types'
import { compareDone } from '../store/boardState'

export const general: General = {
  max_running_agents: 4,
  ceo: { enabled: false, may_approve_stories: false },
  tasks: { require_user_release: false },
  review_defaults: { story: 'human', task: 'agent' },
  when_approved_defaults: { story: 'merge', task: 'merge' },
  waiting: { timeouts_before_investigate: 3 },
  health: { silent_after: '15m', question_unanswered_after: '1h' },
  stop: { handoff_timeout: '1m' },
  workspace_roots: [],
  rescan_interval: '1h',
  agent_env: {},
}

export const project = (id: string, name = id): Project => ({ id, name, path: `/src/${id}`, enabled: true, missing: false, root: '', story_merge: 'merge' })

/** localStorage stand-in. */
export class MemoryStorage implements Storage {
  private items = new Map<string, string>()
  get length() {
    return this.items.size
  }
  clear() {
    this.items.clear()
  }
  getItem(k: string) {
    return this.items.get(k) ?? null
  }
  key(i: number) {
    return [...this.items.keys()][i] ?? null
  }
  removeItem(k: string) {
    this.items.delete(k)
  }
  setItem(k: string, v: string) {
    this.items.set(k, v)
  }
}

export class FakeOmo {
  tickets: TicketSummary[] = []
  projects: Project[] = []
  paused = false
  eventId = 1
  /** Done tickets per page. */
  donePage = 50
  /** The only token accepted; null accepts every request. */
  token: string | null = null
  /** While set, every read fails as if omo were not running. */
  down = false
  /** The paths read, in order. */
  reads: string[] = []
  stream: StreamHandlers | null = null
  connects = 0
  reconnects = 0
  closes = 0

  /** Builds the client a session uses; it shares this omo's data. */
  makeClient = (o: ClientOptions): ApiClient => ({
    get: async <T>(path: string, query?: Query): Promise<ApiResult<T>> => {
      this.reads.push(path)
      if (this.down) throw new ApiError(0, 'network', 'Cannot reach omo. Check that it is still running.')
      this.authorize(o)
      return { data: this.answer(path, query) as T, eventId: null }
    },
    send: async () => {
      throw new ApiError(501, 'not_implemented', 'The fake omo does not change anything.')
    },
    openStream: () => Promise.reject(new Error('the fake connector replaces the stream')),
  })

  connect: typeof connectEventStream = (opts) => {
    this.connects++
    this.stream = opts.handlers
    return {
      close: () => {
        this.closes++
      },
      reconnectNow: () => {
        this.reconnects++
      },
    }
  }

  /** Delivers an event as the stream would. */
  emit(kind: string, data: unknown, ticket: number | null = null): ApiEvent {
    const ev: ApiEvent = { id: ++this.eventId, kind, ticket, actor: { role: 'user' }, at: '2026-11-15T10:00:00.000Z', data }
    if (!this.stream) throw new Error('no stream connected')
    this.stream.onEvent(ev)
    return ev
  }

  /** Changes a ticket here and sends the event that says so. */
  change(kind: 'ticket.created' | 'ticket.updated' | 'ticket.moved', t: TicketSummary): ApiEvent {
    const i = this.tickets.findIndex((x) => x.number === t.number)
    const before = i >= 0 ? this.tickets[i] : undefined
    if (i >= 0) this.tickets[i] = t
    else this.tickets.push(t)
    const data = kind === 'ticket.moved' ? { ticket: t, from: before?.column ?? t.column, to: t.column, action: 'approve' } : { ticket: t }
    return this.emit(kind, data, t.number)
  }

  private authorize(o: ClientOptions): void {
    if (this.token === null) return
    const t = o.getToken()
    if (t === this.token) {
      o.tokenAccepted?.(t)
      return
    }
    if (t !== null) {
      const other = o.forgetToken(t)
      if (other === this.token) return
      o.onUnauthorized('rejected')
    } else {
      o.onUnauthorized('missing')
    }
    throw new ApiError(401, 'unauthorized', 'A valid login token is needed.')
  }

  private done(): TicketSummary[] {
    return this.tickets.filter((t) => t.column === 'done').sort(compareDone)
  }

  private page(from: TicketSummary[]): Pick<TicketList, 'tickets' | 'next_done_before'> {
    const page = from.slice(0, this.donePage)
    const last = page[page.length - 1]
    return from.length > this.donePage && last ? { tickets: page, next_done_before: last.number } : { tickets: page }
  }

  private answer(path: string, query?: Query): unknown {
    switch (path) {
      case '/api/tickets': {
        const done = this.done()
        if (query?.done_before !== undefined) {
          const i = done.findIndex((t) => t.number === Number(query.done_before))
          return { ...this.page(done.slice(i + 1)), event_id: this.eventId } satisfies TicketList
        }
        const p = this.page(done)
        return { ...p, tickets: [...this.tickets.filter((t) => t.column !== 'done'), ...p.tickets], event_id: this.eventId } satisfies TicketList
      }
      case '/api/projects':
        return this.projects
      case '/api/server':
        return { version: 'test', paused: this.paused, started_at: '2026-11-15T08:00:00Z', address: '' } satisfies ServerInfo
      case '/api/settings':
        return { settings: general, errors: [] } satisfies SettingsResponse
      default:
        throw new ApiError(404, 'not_found', `No route ${path}`)
    }
  }
}
