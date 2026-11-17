import { expect, test } from 'vitest'
import type { MCPClientState } from '../../../api/types'
import { FakeMcp } from '../../../testing/fakeOmo'
import { expiresAt } from './accessForms'
import {
  actionLabel,
  blankServer,
  clientActions,
  commandLine,
  connectionCommands,
  isPlacedField,
  listErrors,
  mapErrors,
  reachWords,
  stateWords,
  toDraft,
  toServer,
  toggleRole,
  transportWords,
  WITHOUT_SESSION_GETS,
} from './mcpForms'
import { pickErrors, sourceWords, takeoverDetails } from './takeoverForms'

test('a stored stdio server round-trips through the form unchanged', () => {
  const s = FakeMcp.server({ env: { LOG: 'info' }, roles: ['developer', 'pm'], global: true, timeouts: { startup: '30s', call: '10m' } })
  const d = toDraft(s)
  expect(d.roles).toEqual(['pm', 'developer'])
  expect(d.startup).toBe('30s')
  const { revision: _r, ...stored } = s
  expect(toServer(d)).toEqual({ ...stored, roles: ['pm', 'developer'] })
})

test('only the fields of the chosen transport are sent', () => {
  const d = toDraft(FakeMcp.server({ env: { TOKEN: 'x' } }))
  d.transport = 'http'
  d.url = 'https://mcp.example.invalid/mcp'
  d.auth = 'headers'
  d.headers = { Authorization: 'Bearer ${T}' }
  const s = toServer(d)
  expect(s).toMatchObject({ command: '', args: [], env: {}, cwd: '', instances: '', url: 'https://mcp.example.invalid/mcp', auth: 'headers', headers: { Authorization: 'Bearer ${T}' } })
  expect(s.oauth).toEqual({ client_id: '', client_secret: '', scopes: [], resource: '' })

  d.auth = 'oauth'
  d.clientId = 'cid'
  d.clientSecret = 'csecret'
  d.scopes = ['read']
  const o = toServer(d)
  expect(o.headers).toEqual({})
  expect(o.oauth).toEqual({ client_id: 'cid', client_secret: 'csecret', scopes: ['read'], resource: '' })

  d.transport = 'stdio'
  const back = toServer(d)
  expect(back).toMatchObject({ url: '', auth: '', headers: {}, env: { TOKEN: 'x' }, command: 'npx' })
})

test("empty time limits are omo's default and sent as 0s", () => {
  const d = blankServer('x')
  expect([d.startup, d.call]).toEqual(['', ''])
  expect(toServer(d).timeouts).toEqual({ startup: '0s', call: '0s' })
})

test('a new server is off for connections without a session and runs one process per session', () => {
  const d = blankServer('x')
  expect(d.global).toBe(false)
  expect(d.instances).toBe('per_session')
  expect(d.roles).toEqual([])
})

test('an http server stored with headers but no auth is shown with header authorization', () => {
  expect(toDraft(FakeMcp.server({ transport: 'http', url: 'https://a.invalid', headers: { A: 'b' }, auth: '' })).auth).toBe('headers')
  expect(toDraft(FakeMcp.server({ transport: 'http', url: 'https://a.invalid', auth: '' })).auth).toBe('none')
})

test('roles toggle in the form order', () => {
  expect(toggleRole({ roles: ['reviewer'] }, 'ceo')).toEqual(['ceo', 'reviewer'])
  expect(toggleRole({ roles: ['ceo', 'reviewer'] }, 'ceo')).toEqual(['reviewer'])
})

test('reach, transport and state words', () => {
  expect(reachWords({ roles: [], global: false })).toBe("for every role · only omo's sessions")
  expect(reachWords({ roles: ['developer'], global: true })).toBe('for Developer · also without an omo session')
  expect(transportWords({ transport: 'http', command: '', url: 'https://mcp.example.invalid/mcp?key=secret' })).toBe('http · mcp.example.invalid')
  expect(transportWords({ transport: 'stdio', command: 'npx', url: '' })).toBe('stdio · npx')
  expect(stateWords('needs_login')).toEqual({ text: 'needs login', tone: 'warn' })
  expect(stateWords('weird').text).toBe('weird')
})

test('refusals are placed under the fields the form shows', () => {
  for (const f of ['command', 'args.2', 'env.TOKEN', 'headers.Authorization', 'oauth.client_secret', 'tools.deny.0', 'timeouts.call', 'roles.1']) expect(isPlacedField(f), f).toBe(true)
  for (const f of ['name', 'source', 'global']) expect(isPlacedField(f), f).toBe(false)
  expect(listErrors({ 'args.2': 'bad', args: 'x', 'tools.allow.1': 'y' }, 'args')).toEqual({ 2: 'bad' })
  expect(listErrors({ 'oauth.scopes.0': 'bad' }, 'oauth.scopes')).toEqual({ 0: 'bad' })
  expect(mapErrors({ 'env.A': 'bad', env: 'x' }, 'env')).toEqual({ A: 'bad' })
})

const client = (o: Partial<MCPClientState>): MCPClientState => ({
  target: { id: 'claude', cli: 'claude', account: '', config_path: '/home/u/.claude.json' },
  state: 'absent',
  entry: null,
  entries: [],
  error: '',
  ...o,
})

test('agent programs offer install where omo is missing or not current, remove where an omo entry is', () => {
  expect(clientActions(client({ state: 'absent' }))).toEqual(['install'])
  expect(clientActions(client({ state: 'installed' }))).toEqual(['remove'])
  expect(clientActions(client({ state: 'outdated' }))).toEqual(['install', 'remove'])
  expect(clientActions(client({ state: 'foreign' }))).toEqual(['install', 'remove'])
  expect(clientActions(client({ error: 'unreadable' }))).toEqual([])
  expect(actionLabel(client({ state: 'foreign' }), 'install')).toBe('Replace…')
  expect(actionLabel(client({ state: 'outdated' }), 'install')).toBe('Update…')
})

test('commands are shown with shell quoting', () => {
  expect(commandLine(['claude', 'mcp', 'add-json', '-s', 'user', 'omo', '{"type":"stdio"}'])).toBe(`claude mcp add-json -s user omo '{"type":"stdio"}'`)
  expect(commandLine(["it's"])).toBe(`'it'\\''s'`)
})

test('connection commands carry no token and promise no board tools', () => {
  const cmds = connectionCommands('http://omo.localhost:8123/')
  const all = cmds.flatMap((c) => c.lines).join('\n')
  expect(all).toContain('http://omo.localhost:8123/mcp')
  expect(all).not.toContain('omo_pat_')
  expect(all).toContain('${OMO_MCP_TOKEN}')
  expect(all).not.toMatch(/\bwait\b/)
  expect(WITHOUT_SESSION_GETS).toContain('no board or ticket actions')
})

test('take-over names follow the rule and are unique', () => {
  expect(pickErrors([{ key: 'a', name: 'GitHub' }], [])).toEqual({ a: expect.stringContaining('Lower-case') })
  expect(pickErrors([{ key: 'a', name: 'files' }], ['files'])).toEqual({ a: expect.stringContaining('already') })
  expect(Object.keys(pickErrors([{ key: 'a', name: 'x' }, { key: 'b', name: 'x' }], []))).toEqual(['a', 'b'])
  expect(pickErrors([{ key: 'a', name: 'github' }], ['files'])).toEqual({})
})

test('a take-over candidate is described with the values omo masked', () => {
  const lines = takeoverDetails(FakeMcp.server({ transport: 'http', url: 'https://api.example.invalid/mcp', headers: { Authorization: 'Bearer ***' }, auth: 'headers' }))
  expect(lines).toEqual(['Address (http): https://api.example.invalid/mcp', 'Headers: Authorization: Bearer ***'])
  expect(takeoverDetails(FakeMcp.server({ env: { KEY: '***' } }))).toEqual(["Runs: npx -y @modelcontextprotocol/server-filesystem .", 'Environment: KEY: ***'])
  expect(sourceWords([{ target: 'claude', entry_name: 'gh' }, { target: 'codex-1a2b', entry_name: 'github' }])).toBe('From Claude Code: entry gh; Codex (codex-1a2b): entry github')
})

test('token validity ends the chosen days from now', () => {
  const now = new Date('2026-11-17T10:00:00Z')
  expect(expiresAt('never', now)).toBeNull()
  expect(expiresAt('30', now)).toBe('2026-12-17T10:00:00.000Z')
})
