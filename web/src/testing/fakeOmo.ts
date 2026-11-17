// A stand-in for omo in component tests: answers the client's reads from
// in-memory data, checks the login token the way the real client reports it,
// and replaces the event stream with handlers the test drives.
import { ApiError, type ApiClient, type ApiResult, type ClientOptions, type Query } from '../api/client'
import type { connectEventStream, StreamHandlers } from '../api/eventStream'
import type {
  ApiEvent,
  General,
  MCPClientPlan,
  MCPClientState,
  MCPServer,
  MCPServerState,
  MCPTakeover,
  MCPTakeoverApply,
  MCPTestResult,
  MCPToken,
  Project,
  ServerInfo,
  SettingsResponse,
  TicketList,
  TicketSummary,
  UsageState,
} from '../api/types'
import { compareDone } from '../store/boardState'

export const general: General = {
  limits: { tasks: 4, stories: 2 },
  ceo: { enabled: false, may_approve_stories: false, tickets_need_user_approval: true },
  tasks: { require_user_release: false },
  ui: { open_tickets_as_page: false, screen_reader_mode: false },
  branches: { template: 'omo/{slug}' },
  plugin_lists: [],
  mcp: { enabled: true, inject: { claude: true, codex: true } },
  review_defaults: { story: 'human', task: 'agent', story_task: 'agent' },
  when_approved_defaults: { story: 'merge', task: 'merge', story_task: 'merge' },
  merge_defaults: { story: 'merge', task: 'merge', story_task: 'merge' },
  waiting: { timeouts_before_investigate: 3 },
  health: { silent_after: '15m', question_unanswered_after: '1h' },
  images: { keep_days_after_done: 60 },
  stop: { handoff_timeout: '1m' },
  workspace_roots: [],
  rescan_interval: '1h',
  agent_env: {},
  usage: { soft_percent: 80, hard_percent: 95, refresh_interval: '5m', windows: {} },
}

export const project = (id: string, name = id): Project => ({ id, name, path: `/src/${id}`, enabled: true, missing: false, root: '', merge: { story: '', task: '', story_task: '' }, effective_merge: { story: 'merge', task: 'merge', story_task: 'merge' }, story_merge: 'merge' })

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

/** A write as the test harness records it. */
export interface FakeWrite {
  method: string
  path: string
  body: unknown
  ifMatch?: string
}

const answered = <T,>(data: T): ApiResult<T> => ({ data, eventId: null })
const unavailable = (what: string) => new ApiError(503, 'unavailable', `omo's MCP ${what} is not available in this version`)

/**
 * omo's MCP routes in memory: the relayed servers and their states, the
 * tokens, the agent programs' configurations and the take-over. Reads are
 * answered through FakeOmo's client; writes through `write`, which a test
 * hands to the harness (`h.respond.write = (s) => h.omo.mcp.write(s)`).
 */
export class FakeMcp {
  servers: MCPServer[] = []
  states: MCPServerState[] = []
  /** False answers the relay's routes with 503, as an omo without a relay. */
  relay = true
  /** False answers the token routes with 503. */
  tokensAvailable = true
  tokens: MCPToken[] = []
  /** The secret the next created token gets. */
  nextSecret = 'omo_pat_TESTSECRETabcdefghijklmnopqrstuvwxyz012345'
  testResult: MCPTestResult = { tools: ['read_file', 'write_file'], omitted: [], error: '', offers_resources: false, offers_prompts: false, millis: 42 }
  authorizeUrl = 'https://auth.example.invalid/authorize?client_id=omo&state=s1'
  clients: MCPClientState[] = []
  takeover: MCPTakeover = { candidates: [], stays: [] }
  /** What the last take-over apply was asked. */
  lastTakeover: MCPTakeoverApply | null = null
  private revisions = 0
  private ids = 0

  /** A server with every field, as the server answers it. */
  static server(o: Partial<MCPServer> = {}): MCPServer {
    return {
      name: 'files',
      enabled: true,
      description: '',
      transport: 'stdio',
      command: 'npx',
      args: ['-y', '@modelcontextprotocol/server-filesystem', '.'],
      env: {},
      cwd: '',
      instances: 'per_session',
      url: '',
      headers: {},
      headers_command: [],
      auth: '',
      oauth: { client_id: '', client_secret: '', scopes: [], resource: '' },
      roles: [],
      global: false,
      tools: { allow: [], deny: [] },
      timeouts: { startup: '0s', call: '0s' },
      source: '',
      revision: 'rev1',
      ...o,
    }
  }

  /** A state as the relay tells it. */
  static state(o: Partial<MCPServerState> = {}): MCPServerState {
    return {
      name: 'files',
      state: 'running',
      instances: 1,
      tools: ['files__read_file'],
      omitted: [],
      last_error: '',
      started_at: '2026-11-17T10:00:00Z',
      auth: { state: 'none', expires_at: null, scopes: [] },
      offers_resources: false,
      offers_prompts: false,
      ...o,
    }
  }

  /** The plan omo would answer for a target and action. */
  planOf(st: MCPClientState, action: 'install' | 'remove'): MCPClientPlan {
    const entry = { type: 'stdio', command: '/usr/local/bin/omo', args: ['mcp', 'stdio'] }
    const prog = st.target.cli
    let commands: string[][] = []
    let message = ''
    if (action === 'install') {
      if (st.state === 'installed') message = `omo is already installed in ${st.target.config_path}.`
      else if (st.state === 'absent') commands = [[prog, 'mcp', 'add', 'omo', '--', '/usr/local/bin/omo', 'mcp', 'stdio']]
      else {
        message = st.state === 'foreign' ? "The file has an entry named omo that is not omo's bridge; it is replaced." : "The file has omo's bridge with another path or environment; it is replaced."
        commands = [[prog, 'mcp', 'remove', 'omo'], [prog, 'mcp', 'add', 'omo', '--', '/usr/local/bin/omo', 'mcp', 'stdio']]
      }
    } else if (st.state === 'absent') message = `omo is not installed in ${st.target.config_path}.`
    else commands = [[prog, 'mcp', 'remove', 'omo']]
    return {
      target: st.target,
      action,
      file: st.target.config_path,
      commands,
      before: st.entry,
      after: commands.length === 0 ? st.entry : action === 'install' ? entry : null,
      message,
      hash: `${st.target.id}-${action}-${st.state}`,
    }
  }

  read(path: string): { found: boolean; value?: unknown } {
    const log = /^\/api\/mcp\/servers\/([^/]+)\/log$/.exec(path)
    if (log) {
      if (!this.relay) throw unavailable('relay')
      return { found: true, value: [{ at: '2026-11-17T10:00:00Z', message: 'started' }] }
    }
    switch (path) {
      case '/api/mcp/servers':
        return { found: true, value: { servers: this.servers, states: this.relay ? this.states : [], errors: [] } }
      case '/api/mcp/tokens':
        if (!this.tokensAvailable) throw unavailable('tokens')
        return { found: true, value: this.tokens }
      case '/api/mcp/clients':
        return { found: true, value: this.clients }
      case '/api/mcp/takeover':
        return { found: true, value: this.takeover }
      default:
        return { found: false }
    }
  }

  /** Answers a write to an MCP route as omo would; throws omo's refusal. */
  write(w: FakeWrite): ApiResult<unknown> {
    const m = /^\/api\/mcp\/servers\/([^/]+)(?:\/([a-z]+))?$/.exec(w.path)
    if (w.path === '/api/mcp/servers/test') {
      if (!this.relay) throw unavailable('relay')
      return answered(this.testResult)
    }
    if (m) {
      const name = decodeURIComponent(m[1]!)
      const action = m[2]
      const i = this.servers.findIndex((s) => s.name === name)
      if (action === undefined && w.method === 'PUT') {
        if (w.ifMatch === '0' ? i >= 0 : i < 0 || (w.ifMatch !== undefined && this.servers[i]!.revision !== w.ifMatch)) throw new ApiError(409, 'stale_settings', 'The settings changed meanwhile.')
        const stored = { ...(w.body as MCPServer), name, revision: `rev${++this.revisions + 1}` }
        if (i >= 0) this.servers[i] = stored
        else this.servers.push(stored)
        return answered(stored)
      }
      if (action === undefined && w.method === 'DELETE') {
        if (i < 0) throw new ApiError(404, 'not_found', `no MCP server "${name}"`)
        this.servers.splice(i, 1)
        return answered(null)
      }
      if (i < 0) throw new ApiError(404, 'not_found', `no MCP server "${name}"`)
      if (!this.relay) throw unavailable('relay')
      if (action === 'connect') return answered({ authorize_url: this.authorizeUrl })
      return answered(null)
    }
    if (w.path === '/api/mcp/tokens' && w.method === 'POST') {
      if (!this.tokensAvailable) throw unavailable('tokens')
      const req = w.body as { name: string; expires_at?: string }
      const token: MCPToken = {
        id: (++this.ids).toString(16).padStart(16, '0'),
        kind: 'pat',
        name: req.name,
        client_id: '',
        redirect_host: '',
        resource: '',
        scope: 'mcp',
        created_at: '2026-11-17T10:00:00Z',
        expires_at: req.expires_at ?? null,
        refresh_expires_at: null,
        last_used_at: null,
      }
      this.tokens.push(token)
      return answered({ token, secret: this.nextSecret })
    }
    const tok = /^\/api\/mcp\/tokens\/([^/]+)$/.exec(w.path)
    if (tok && w.method === 'DELETE') {
      if (!this.tokensAvailable) throw unavailable('tokens')
      const i = this.tokens.findIndex((t) => t.id === tok[1])
      if (i < 0) throw new ApiError(404, 'not_found', 'no MCP token')
      this.tokens.splice(i, 1)
      return answered(null)
    }
    const client = /^\/api\/mcp\/clients\/([^/]+)\/(plan|apply)$/.exec(w.path)
    if (client) {
      const i = this.clients.findIndex((c) => c.target.id === client[1])
      if (i < 0) throw new ApiError(404, 'not_found', `no agent program configuration ${client[1]}`)
      const st = this.clients[i]!
      if (client[2] === 'plan') return answered(this.planOf(st, (w.body as { action: 'install' | 'remove' }).action))
      const hash = (w.body as { plan: string }).plan
      const action = (['install', 'remove'] as const).find((a) => this.planOf(st, a).hash === hash)
      if (action === undefined) throw new ApiError(409, 'conflict', 'the file changed; review again')
      const after: MCPClientState =
        action === 'install'
          ? { ...st, state: 'installed', entry: { type: 'stdio', command: '/usr/local/bin/omo', args: ['mcp', 'stdio'] } }
          : { ...st, state: 'absent', entry: null }
      this.clients[i] = after
      return answered({ state: after, backup: `/home/sample/.local/omo/backups/20261117-mcp-${st.target.cli}/${st.target.config_path.split('/').pop()}` })
    }
    if (w.path === '/api/mcp/takeover/apply') {
      const req = w.body as MCPTakeoverApply
      this.lastTakeover = req
      const results = req.take.map((p) => {
        const c = this.takeover.candidates.find((x) => x.key === p.key)
        if (!c) return { name: p.name, stored: false, error: 'it is no longer in the files', removed: [], needs_login: false }
        if (this.servers.some((s) => s.name === p.name)) return { name: p.name, stored: false, error: `omo has a server named ${p.name} already`, removed: [], needs_login: false }
        this.servers.push({ ...c.server, name: p.name, global: true, revision: `rev${++this.revisions + 1}` })
        return { name: p.name, stored: true, error: '', removed: req.remove_from_cli ? c.sources.map((s) => ({ target: s.target, done: true, error: '' })) : [], needs_login: c.needs_login }
      })
      this.takeover = { ...this.takeover, candidates: this.takeover.candidates.filter((c) => !req.take.some((p) => p.key === c.key)) }
      return answered({ results, backups: ['/home/sample/.local/omo/backups/20261117-mcp-takeover'] })
    }
    throw new ApiError(404, 'not_found', `No route ${w.method} ${w.path}`)
  }
}

export class FakeOmo {
  /** omo's MCP routes. */
  mcp = new FakeMcp()
  tickets: TicketSummary[] = []
  projects: Project[] = []
  paused = false
  /** Fields of `GET /api/server` a test overrides. */
  server: Partial<ServerInfo> = {}
  /** What GET /api/usage answers. */
  usage: UsageState = { readings: [], models: [] }
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
    getText: () => Promise.reject(new ApiError(501, 'not_implemented', 'The fake omo has no text answers.')),
    getBlob: () => Promise.reject(new ApiError(501, 'not_implemented', 'The fake omo has no images.')),
    upload: () => Promise.reject(new ApiError(501, 'not_implemented', 'The fake omo does not change anything.')),
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
        return { version: 'test', paused: this.paused, started_at: '2026-11-15T08:00:00Z', address: '', address_configured: false, listen: '', version_url: '', version_commit: '', ...this.server } satisfies ServerInfo
      case '/api/usage':
        return this.usage
      case '/api/settings':
        return { settings: general, errors: [], revision: '0', home: '/home/sample/.local/omo' } satisfies SettingsResponse
      default: {
        const mcp = this.mcp.read(path)
        if (mcp.found) return mcp.value
        throw new ApiError(404, 'not_found', `No route ${path}`)
      }
    }
  }
}
