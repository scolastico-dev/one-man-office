// What the MCP forms show and send, as plain functions. A relayed server is
// either a program omo starts (stdio) or an address omo connects to (http,
// sse); the form keeps the fields of both while it is open, and only the
// fields of the chosen transport are sent. Values (env, headers, the OAuth
// client secret) are stored, shown and sent as they are, like the
// environment of a profile; they never go into an address, a log or a toast.
import type {
  FileError,
  MCPAuth,
  MCPClientState,
  MCPClientTarget,
  MCPInstances,
  MCPServer,
  MCPServerState,
  MCPServerStateName,
  MCPTransport,
} from '../../../api/types'
import type { SelectOption } from '../../../components/ui/SelectField.vue'
import { SKILL_ROLES, type SkillRole } from '../skills/skillForms'

/** The roles whose agent sessions can be given a server, in the order the form shows them. */
export const MCP_ROLES: readonly { id: SkillRole; label: string }[] = SKILL_ROLES

/** The rule of a server's name (the server checks it too). */
export const MCP_NAME_PATTERN = /^[a-z][a-z0-9-]{0,23}$/
export const MCP_NAME_RULE = 'Lower-case letters, digits and "-", starting with a letter; at most 24 characters.'

/** What the toggle `global` means, in one line. */
export const GLOBAL_LABEL = 'Also available without an omo session'
export const GLOBAL_HINT =
  'Programs started outside omo, personal tokens and OAuth grants get this server too. Off: only the agent sessions omo starts, of the chosen roles.'

/** A server as the form edits it: lists and maps for every field, timeouts as text ("" is omo's default). */
export interface McpDraft {
  name: string
  enabled: boolean
  description: string
  transport: MCPTransport
  command: string
  args: string[]
  env: Record<string, string>
  cwd: string
  instances: MCPInstances
  url: string
  headers: Record<string, string>
  headersCommand: string[]
  auth: MCPAuth
  clientId: string
  clientSecret: string
  scopes: string[]
  resource: string
  roles: SkillRole[]
  global: boolean
  allow: string[]
  deny: string[]
  startup: string
  call: string
  source: string
}

/** A stored duration as the form shows it: "" for omo's default. */
const durationText = (d: string | undefined) => (d === undefined || d === '' || d === '0s' ? '' : d)

const isRole = (r: string): r is SkillRole => MCP_ROLES.some((x) => x.id === r)

/** The stored server as a draft; roles in the form's order (unknown ones kept, so the server names them). */
export function toDraft(s: MCPServer): McpDraft {
  const roles = MCP_ROLES.map((r) => r.id).filter((r) => (s.roles ?? []).includes(r))
  for (const r of s.roles ?? []) if (!isRole(r)) roles.push(r as SkillRole)
  const remote = s.transport !== 'stdio'
  let auth: MCPAuth = s.auth === '' || s.auth === undefined ? 'none' : s.auth
  if (remote && (s.auth === '' || s.auth === undefined) && (Object.keys(s.headers ?? {}).length > 0 || (s.headers_command ?? []).length > 0)) auth = 'headers'
  return {
    name: s.name,
    enabled: s.enabled,
    description: s.description ?? '',
    transport: s.transport,
    command: s.command ?? '',
    args: [...(s.args ?? [])],
    env: { ...(s.env ?? {}) },
    cwd: s.cwd ?? '',
    instances: s.instances === 'shared' ? 'shared' : 'per_session',
    url: s.url ?? '',
    headers: { ...(s.headers ?? {}) },
    headersCommand: [...(s.headers_command ?? [])],
    auth,
    clientId: s.oauth?.client_id ?? '',
    clientSecret: s.oauth?.client_secret ?? '',
    scopes: [...(s.oauth?.scopes ?? [])],
    resource: s.oauth?.resource ?? '',
    roles,
    global: s.global,
    allow: [...(s.tools?.allow ?? [])],
    deny: [...(s.tools?.deny ?? [])],
    startup: durationText(s.timeouts?.startup),
    call: durationText(s.timeouts?.call),
    source: s.source ?? '',
  }
}

/**
 * The draft as the server stores it: the fields of its transport only (a
 * stdio server sends no address, an http server no command), headers only
 * with header authorization, the OAuth client only with OAuth.
 */
export function toServer(d: McpDraft): MCPServer {
  const stdio = d.transport === 'stdio'
  const headers = !stdio && d.auth === 'headers'
  const oauth = !stdio && d.auth === 'oauth'
  return {
    name: d.name,
    enabled: d.enabled,
    description: d.description,
    transport: d.transport,
    command: stdio ? d.command : '',
    args: stdio ? [...d.args] : [],
    env: stdio ? { ...d.env } : {},
    cwd: stdio ? d.cwd : '',
    instances: stdio ? d.instances : '',
    url: stdio ? '' : d.url,
    headers: headers ? { ...d.headers } : {},
    headers_command: headers ? [...d.headersCommand] : [],
    auth: stdio ? '' : d.auth,
    oauth: oauth
      ? { client_id: d.clientId, client_secret: d.clientSecret, scopes: [...d.scopes], resource: d.resource }
      : { client_id: '', client_secret: '', scopes: [], resource: '' },
    roles: [...d.roles],
    global: d.global,
    tools: { allow: [...d.allow], deny: [...d.deny] },
    timeouts: { startup: d.startup === '' ? '0s' : d.startup, call: d.call === '' ? '0s' : d.call },
    source: d.source,
  }
}

/** A new server: a stdio program, one process per session, every role, only for omo's sessions. */
export function blankServer(name: string): McpDraft {
  return toDraft({
    name,
    enabled: true,
    description: '',
    transport: 'stdio',
    command: '',
    args: [],
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
  })
}

/** The draft with `role` chosen or no longer chosen; the roles stay in the form's order. */
export function toggleRole(d: Pick<McpDraft, 'roles'>, role: SkillRole): SkillRole[] {
  const on = d.roles.includes(role)
  return MCP_ROLES.map((r) => r.id).filter((r) => (r === role ? !on : d.roles.includes(r)))
}

export const TRANSPORT_OPTIONS: SelectOption[] = [
  { value: 'stdio', label: 'A program omo starts (stdio)' },
  { value: 'http', label: 'An address (Streamable HTTP)' },
  { value: 'sse', label: 'An address (SSE, older servers)' },
]

export const INSTANCE_OPTIONS: SelectOption[] = [
  { value: 'per_session', label: 'One process per session, in its folder' },
  { value: 'shared', label: 'One process for every session' },
]

export const AUTH_OPTIONS: SelectOption[] = [
  { value: 'none', label: 'None' },
  { value: 'headers', label: 'Headers (a token or key)' },
  { value: 'oauth', label: 'OAuth (sign in with Connect)' },
]

/** A state in a word and the badge's tone. */
export interface Words {
  text: string
  tone: 'danger' | 'warn' | 'accent' | 'muted' | 'ok'
}

const STATE_WORDS: Record<MCPServerStateName, Words> = {
  stopped: { text: 'not running', tone: 'muted' },
  starting: { text: 'starting', tone: 'accent' },
  running: { text: 'running', tone: 'ok' },
  failed: { text: 'failed', tone: 'danger' },
  needs_login: { text: 'needs login', tone: 'warn' },
  disabled: { text: 'off', tone: 'muted' },
}

/** The state of a server in words; a state omo does not know is shown as it is. */
export function stateWords(state: string): Words {
  return STATE_WORDS[state as MCPServerStateName] ?? { text: state, tone: 'muted' }
}

/** The state of a server before the relay told one. */
export const unknownState = (name: string): MCPServerState => ({
  name,
  state: 'stopped',
  instances: 0,
  tools: [],
  omitted: [],
  last_error: '',
  started_at: null,
  auth: { state: 'none', expires_at: null, scopes: [] },
  offers_resources: false,
  offers_prompts: false,
})

/** Who gets a server, in words: "for every role · only omo's sessions". */
export function reachWords(s: Pick<MCPServer, 'roles' | 'global'>): string {
  const roles = s.roles.length === 0 ? 'every role' : s.roles.map((r) => MCP_ROLES.find((x) => x.id === r)?.label ?? r).join(', ')
  return `for ${roles} · ${s.global ? 'also without an omo session' : "only omo's sessions"}`
}

/** The host of an address, or "" when it is none. */
function hostOf(url: string): string {
  try {
    return new URL(url).host
  } catch {
    return ''
  }
}

/** What a server is, in a few words: "stdio · npx", "http · mcp.example.com". The full address stays in the form. */
export function transportWords(s: Pick<MCPServer, 'transport' | 'command' | 'url'>): string {
  if (s.transport === 'stdio') return `stdio · ${s.command}`
  const host = hostOf(s.url)
  return host === '' ? s.transport : `${s.transport} · ${host}`
}

/** The tools of a running server, in words. */
export function toolWords(st: MCPServerState): string {
  const n = st.tools.length
  const tools = n === 1 ? '1 tool' : `${n} tools`
  return st.instances > 1 ? `${tools} · ${st.instances} processes` : tools
}

/** The name of the server a settings file belongs to (`…/mcpservers/<name>.yaml`), or null. */
export function fileOfMCPServer(file: string): string | null {
  const m = /[\\/]mcpservers[\\/]([^\\/]+)\.ya?ml$/.exec(file)
  return m === null ? null : (m[1] ?? null)
}

/** The broken files that belong to the server `name`. */
export function errorsOfMCPServer(errors: readonly FileError[], name: string): FileError[] {
  return errors.filter((e) => fileOfMCPServer(e.file) === name)
}

/** The refusals (by field) a field of the form shows. */
export function isPlacedField(field: string): boolean {
  return /^(description|transport|command|args(\.\d+)?|env(\..+)?|cwd|instances|url|headers(\..+)?|headers_command(\.\d+)?|auth|oauth\.(client_id|client_secret|scopes(\.\d+)?|resource)|roles(\.\d+)?|tools\.(allow|deny)(\.\d+)?|timeouts\.(startup|call))$/.test(field)
}

/** The refusals of the rows of a list field (`args.2` → {2: message}). */
export function listErrors(fieldErrors: Readonly<Record<string, string>>, key: string): Record<number, string> {
  const out: Record<number, string> = {}
  for (const [f, msg] of Object.entries(fieldErrors)) {
    const m = new RegExp(`^${key.replace('.', '\\.')}\\.(\\d+)$`).exec(f)
    if (m) out[Number(m[1])] = msg
  }
  return out
}

/** The refusals of the rows of a variable table (`env.TOKEN` → {TOKEN: message}). */
export function mapErrors(fieldErrors: Readonly<Record<string, string>>, key: string): Record<string, string> {
  const out: Record<string, string> = {}
  const prefix = `${key}.`
  for (const [f, msg] of Object.entries(fieldErrors)) if (f.startsWith(prefix)) out[f.slice(prefix.length)] = msg
  return out
}

/** The first refusal about the roles (the whole list or one entry). */
export function rolesError(fieldErrors: Readonly<Record<string, string>>): string | undefined {
  return Object.entries(fieldErrors).find(([f]) => f === 'roles' || /^roles\.\d+$/.test(f))?.[1]
}

// Agent programs

const CLI_LABELS: Record<string, string> = { claude: 'Claude Code', codex: 'Codex', agy: 'Antigravity (agy)' }

/** An agent program's name. */
export const cliLabel = (cli: string): string => CLI_LABELS[cli] ?? cli

/** Which configuration of a program a target is: its default file or the file of an account folder. */
export function targetWords(t: MCPClientTarget): string {
  return t.account === '' ? `${cliLabel(t.cli)}, your own configuration` : `${cliLabel(t.cli)}, account folder ${t.account}`
}

const CLIENT_WORDS: Record<MCPClientState['state'], Words> = {
  installed: { text: 'omo registered', tone: 'ok' },
  outdated: { text: 'omo registered, outdated', tone: 'warn' },
  absent: { text: 'omo not registered', tone: 'muted' },
  foreign: { text: 'another entry named omo', tone: 'danger' },
}

export function clientWords(st: MCPClientState): Words {
  if (st.error !== '') return { text: 'cannot be read', tone: 'danger' }
  return CLIENT_WORDS[st.state] ?? { text: st.state, tone: 'muted' }
}

/** The actions a target offers: install where omo is missing or not current, remove where an omo entry is. */
export function clientActions(st: MCPClientState): ('install' | 'remove')[] {
  if (st.error !== '') return []
  const out: ('install' | 'remove')[] = []
  if (st.state !== 'installed') out.push('install')
  if (st.state !== 'absent') out.push('remove')
  return out
}

/** The label of an action's button. */
export function actionLabel(st: MCPClientState, action: 'install' | 'remove'): string {
  if (action === 'remove') return 'Remove…'
  return st.state === 'absent' ? 'Apply…' : st.state === 'outdated' ? 'Update…' : 'Replace…'
}

/** One argument as a shell would need it written: quoted when it holds anything but plain characters. */
export function shellWord(arg: string): string {
  if (arg !== '' && /^[A-Za-z0-9_@%+=:,./-]+$/.test(arg)) return arg
  return `'${arg.replace(/'/g, `'\\''`)}'`
}

/** A command line to show. */
export const commandLine = (argv: readonly string[]): string => argv.map(shellWord).join(' ')

/** A JSON value (an entry of a file) for showing; "none" when there is none. */
export function entryText(v: unknown): string {
  if (v === null || v === undefined) return 'none'
  return JSON.stringify(v, null, 2)
}

// Access

export interface ConnectionCommand {
  id: string
  label: string
  /** The command lines, one per line; a token appears only as the variable OMO_MCP_TOKEN. */
  lines: string[]
}

/** The commands that connect an agent program on another machine to omo's MCP endpoint at `base`. */
export function connectionCommands(base: string): ConnectionCommand[] {
  const endpoint = `${base.replace(/\/+$/, '')}/mcp`
  return [
    {
      id: 'claude-oauth',
      label: 'Claude Code, signing in with OAuth',
      lines: [`claude mcp add --transport http -s user omo ${endpoint}`, '# then in Claude Code: /mcp, choose omo, Authenticate'],
    },
    { id: 'codex-oauth', label: 'Codex, signing in with OAuth', lines: [`codex mcp add omo --url ${endpoint}`, 'codex mcp login omo'] },
    {
      id: 'claude-token',
      label: 'Claude Code with a personal access token',
      lines: [`claude mcp add --transport http -s user omo ${endpoint} --header 'Authorization: Bearer \${OMO_MCP_TOKEN}'`],
    },
    { id: 'codex-token', label: 'Codex with a personal access token', lines: [`codex mcp add omo --url ${endpoint} --bearer-token-env-var OMO_MCP_TOKEN`] },
  ]
}

export { WITHOUT_SESSION_GETS } from '../../oauth/consentWords'
