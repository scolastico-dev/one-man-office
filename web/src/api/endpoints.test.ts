import { describe, expect, it } from 'vitest'
import type { ApiClient } from './client'
import * as e from './endpoints'

interface Seen {
  method: string
  path: string
  query?: unknown
  hasBody: boolean
  body?: unknown
  opts?: unknown
}

let textHeaders: Record<string, string> = {}

function spy() {
  const seen: Seen[] = []
  const result = { data: undefined, eventId: null }
  const client = {
    async get(path: string, query?: unknown) {
      seen.push({ method: 'GET', path, query, hasBody: false })
      return result
    },
    async send(method: string, path: string, ...rest: unknown[]) {
      seen.push({ method, path, hasBody: rest.length > 0 && rest[0] !== undefined, body: rest[0], opts: rest[1] })
      return result
    },
    async getText(path: string, query?: unknown) {
      seen.push({ method: 'GET', path, query, hasBody: false })
      return { text: 'log', headers: new Headers(textHeaders) }
    },
    async openStream() {
      throw new Error('not used')
    },
  } as unknown as ApiClient
  return { client, seen }
}

describe('endpoints', () => {
  it('approve and cancel send {}; markMerged sends no body', async () => {
    const { client, seen } = spy()
    await e.approveTicket(client, 4)
    await e.retryTicket(client, 4)
    await e.markMerged(client, 4)
    await e.cancelTicket(client, 4, false)
    expect(seen[0]).toMatchObject({ method: 'POST', path: '/api/tickets/4/approve', body: {} })
    expect(seen[1]).toMatchObject({ method: 'POST', path: '/api/tickets/4/retry', body: {} })
    expect(seen[2]).toMatchObject({ method: 'POST', path: '/api/tickets/4/mark-merged', hasBody: false })
    expect(seen[3]).toMatchObject({ method: 'POST', path: '/api/tickets/4/cancel', body: { delete_branch: false } })
  })

  it('cancelTicket never sends discard_uncommitted', async () => {
    const { client, seen } = spy()
    await e.cancelTicket(client, 4, true)
    expect(seen[0]?.body).toEqual({ delete_branch: true })
    expect(Object.keys(seen[0]?.body as object)).toEqual(['delete_branch'])
  })

  it('updateTicket omits absent keys and sends null to clear', async () => {
    const { client, seen } = spy()
    await e.updateTicket(client, 7, { title: 'New', story: null, model: undefined })
    expect(seen[0]).toMatchObject({ method: 'PATCH', path: '/api/tickets/7' })
    expect(JSON.stringify(seen[0]?.body)).toBe('{"title":"New","story":null}')
  })

  it('moveTicket sends the column and a null before at the end', async () => {
    const { client, seen } = spy()
    await e.moveTicket(client, 2, 'review', null)
    await e.moveTicket(client, 2, 'ready', 9)
    expect(seen[0]?.body).toEqual({ column: 'review', before: null })
    expect(seen[1]?.body).toEqual({ column: 'ready', before: 9 })
    expect(seen[0]?.path).toBe('/api/tickets/2/move')
  })

  it('listTickets passes the filter as query and setDescription the base version', async () => {
    const { client, seen } = spy()
    await e.listTickets(client, { done_limit: 50, needs_me: true })
    await e.listTickets(client)
    await e.setDescription(client, 3, 'text', 5)
    expect(seen[0]?.query).toEqual({ done_limit: 50, needs_me: true })
    expect(seen[1]?.query).toEqual({})
    expect(seen[2]).toMatchObject({ method: 'PUT', path: '/api/tickets/3/description', body: { text: 'text', base_version: 5 } })
  })

  it('maps the remaining routes', async () => {
    const { client, seen } = spy()
    await e.getTicket(client, 1)
    await e.createTicket(client, { type: 'task', title: 't', projects: [] })
    await e.rejectTicket(client, 1, 'no')
    await e.addDependency(client, 1, 2)
    await e.removeDependency(client, 1, 2)
    await e.listProjects(client)
    await e.listProjects(client, true)
    await e.createProject(client, '/p')
    await e.createProject(client, '/p', 'n')
    await e.getServer(client)
    await e.setPaused(client, true)
    await e.stopAllAgents(client)
    await e.listModels(client)
    await e.getProfiles(client)
    await e.getRoles(client)
    await e.getSettings(client)
    expect(seen.map((s) => `${s.method} ${s.path}`)).toEqual([
      'GET /api/tickets/1',
      'POST /api/tickets',
      'POST /api/tickets/1/reject',
      'POST /api/tickets/1/dependencies',
      'DELETE /api/tickets/1/dependencies/2',
      'GET /api/projects',
      'GET /api/projects',
      'POST /api/projects',
      'POST /api/projects',
      'GET /api/server',
      'PUT /api/server/paused',
      'POST /api/agents/stop-all',
      'GET /api/models',
      'GET /api/profiles',
      'GET /api/roles',
      'GET /api/settings',
    ])
    expect(seen[2]?.body).toEqual({ comment: 'no' })
    expect(seen[3]?.body).toEqual({ depends_on: 2 })
    expect(seen[4]?.hasBody).toBe(false)
    expect(seen[6]?.query).toEqual({ all: true })
    expect(seen[7]?.body).toEqual({ path: '/p' })
    expect(seen[8]?.body).toEqual({ path: '/p', name: 'n' })
    expect(seen[10]?.body).toEqual({ paused: true })
    expect(seen[11]?.body).toEqual({})
  })

  it('each new endpoint sends the documented method, path, query and body', async () => {
    const { client, seen } = spy()
    await e.getTicketThread(client, 5)
    await e.getTicketThread(client, 5, { before: 40, limit: 20 })
    await e.postComment(client, 5, { body: 'hi', question: false })
    await e.getDescriptions(client, 5)
    await e.restoreDescription(client, 5, 3)
    await e.getTicketAgents(client, 5)
    await e.listAgents(client, true)
    await e.getScheduler(client)
    await e.openCEO(client)
    await e.newCEOSession(client)
    await e.getSessionLog(client, 's/1')
    await e.getTicketDiff(client, 5)
    await e.getDiffFile(client, 5, { project: 'api', path: 'a b/c.go' })
    await e.getDiffFile(client, 5, { project: 'api', path: 'x', uncommitted: true })
    await e.listTerminals(client)
    await e.openTerminal(client, { project: 'api', ticket: 5 })
    await e.closeTerminal(client, 't/1')
    await e.getNotifications(client)
    await e.markNotificationsSeen(client, 12)
    expect(seen.map((s) => `${s.method} ${s.path}`)).toEqual([
      'GET /api/tickets/5/thread',
      'GET /api/tickets/5/thread',
      'POST /api/tickets/5/comments',
      'GET /api/tickets/5/descriptions',
      'POST /api/tickets/5/descriptions/3/restore',
      'GET /api/tickets/5/agents',
      'GET /api/agents',
      'GET /api/scheduler',
      'POST /api/ceo',
      'POST /api/ceo/new-session',
      'GET /api/sessions/s%2F1/log',
      'GET /api/tickets/5/diff',
      'GET /api/tickets/5/diff/file',
      'GET /api/tickets/5/diff/file',
      'GET /api/terminals',
      'POST /api/terminals',
      'DELETE /api/terminals/t%2F1',
      'GET /api/notifications',
      'PUT /api/notifications/seen',
    ])
    expect(seen[0]?.query).toEqual({ before: undefined, limit: undefined })
    expect(seen[1]?.query).toEqual({ before: 40, limit: 20 })
    expect(seen[2]?.body).toEqual({ body: 'hi', question: false })
    expect(seen[4]?.hasBody).toBe(false)
    expect(seen[6]?.query).toEqual({ live: true })
    expect(seen[8]?.body).toEqual({})
    expect(seen[9]?.body).toEqual({})
    expect(seen[12]?.query).toEqual({ project: 'api', path: 'a b/c.go', uncommitted: undefined })
    expect(seen[13]?.query).toEqual({ project: 'api', path: 'x', uncommitted: true })
    expect(seen[15]?.body).toEqual({ project: 'api', ticket: 5 })
    expect(seen[16]?.hasBody).toBe(false)
    expect(seen[18]?.body).toEqual({ through: 12 })
  })

  it('approveTicket sends {} without options and only true keys with them', async () => {
    const { client, seen } = spy()
    await e.approveTicket(client, 4)
    await e.approveTicket(client, 4, { export: true })
    await e.approveTicket(client, 4, { export: false, merge: true })
    await e.approveTicket(client, 4, { export: true, merge: true })
    await e.approveTicket(client, 4, { export: false })
    expect(seen.map((s) => s.body)).toEqual([{}, { export: true }, { merge: true }, { export: true, merge: true }, {}])
  })

  it('getSessionLog reports truncation from Omo-Log-Truncated', async () => {
    const { client } = spy()
    textHeaders = { 'Omo-Log-Truncated': 'true' }
    expect(await e.getSessionLog(client, 's1')).toEqual({ text: 'log', truncated: true })
    textHeaders = {}
    expect(await e.getSessionLog(client, 's1')).toEqual({ text: 'log', truncated: false })
  })

  it('terminal paths encode ids', () => {
    expect(e.agentTerminalPath('a/b c')).toBe('/api/agents/a%2Fb%20c/terminal')
    expect(e.userTerminalPath('t-1?x')).toBe('/api/terminals/t-1%3Fx/socket')
  })

  it('each settings endpoint sends the documented method, path, body and If-Match', async () => {
    const { client, seen } = spy()
    const profile = { name: 'a/b c', cli: 'claude' } as never
    const role = { name: 'developer', models: [], assignment: 'random' } as never
    await e.patchSettings(client, { max_running_agents: 3 }, 'r1')
    await e.checkAgentEnv(client, { A: 'b' })
    await e.updateProject(client, 'p1', { enabled: false })
    await e.removeProject(client, 'p1')
    await e.removeMissingProjects(client)
    await e.getScanState(client)
    await e.rescanProjects(client)
    await e.listFolders(client)
    await e.listFolders(client, '/x y')
    await e.putProfile(client, profile, '0')
    await e.deleteProfile(client, 'main', 'r2')
    await e.deleteProfileDetached(client, 'main', 'r6')
    await e.detectAgents(client)
    await e.putRole(client, role, 'r3')
    await e.listPrompts(client)
    await e.getPrompt(client, 'developer')
    await e.putPrompt(client, 'developer', 'hi', 'r4')
    await e.resetPrompt(client, 'developer', 'r5')
    await e.previewPrompt(client, 'developer', 'hi')
    expect(seen).toMatchObject([
      { method: 'PATCH', path: '/api/settings', body: { max_running_agents: 3 }, opts: { ifMatch: 'r1' } },
      { method: 'POST', path: '/api/settings/agent-env/check', body: { env: { A: 'b' } } },
      { method: 'PATCH', path: '/api/projects/p1', body: { enabled: false } },
      { method: 'DELETE', path: '/api/projects/p1', hasBody: false },
      { method: 'DELETE', path: '/api/projects?missing=true', hasBody: false },
      { method: 'GET', path: '/api/projects/scan' },
      { method: 'POST', path: '/api/projects/rescan', body: {} },
      { method: 'GET', path: '/api/folders', query: { path: undefined } },
      { method: 'GET', path: '/api/folders', query: { path: '/x y' } },
      { method: 'PUT', path: '/api/profiles/a%2Fb%20c', body: profile, opts: { ifMatch: '0' } },
      { method: 'DELETE', path: '/api/profiles/main', hasBody: false, opts: { ifMatch: 'r2' } },
      { method: 'DELETE', path: '/api/profiles/main?detach=true', hasBody: false, opts: { ifMatch: 'r6' } },
      { method: 'POST', path: '/api/agent-setup/detect', body: {} },
      { method: 'PUT', path: '/api/roles/developer', body: role, opts: { ifMatch: 'r3' } },
      { method: 'GET', path: '/api/prompts' },
      { method: 'GET', path: '/api/prompts/developer' },
      { method: 'PUT', path: '/api/prompts/developer', body: { text: 'hi' }, opts: { ifMatch: 'r4' } },
      { method: 'DELETE', path: '/api/prompts/developer', hasBody: false, opts: { ifMatch: 'r5' } },
      { method: 'POST', path: '/api/prompts/developer/preview', body: { text: 'hi' } },
    ])
  })

  it('names are encoded', async () => {
    const { client, seen } = spy()
    await e.getPrompt(client, 'a/../b?')
    await e.updateProject(client, '../x', {})
    await e.deleteProfile(client, 'x y', '1')
    expect(seen.map((s) => s.path)).toEqual(['/api/prompts/a%2F..%2Fb%3F', '/api/projects/..%2Fx', '/api/profiles/x%20y'])
  })

  it('each extension endpoint sends the documented method, path, body and If-Match', async () => {
    const { client, seen } = spy()
    const set = { name: 'a/b', repository: 'r' } as never
    const plugin = { name: 'p q', source: 's' } as never
    await e.getUsage(client)
    await e.refreshUsage(client)
    await e.listUsageScripts(client)
    await e.getUsageScript(client, 'claude')
    await e.putUsageScript(client, 'claude', 'code', '0')
    await e.resetUsageScript(client, 'claude', 'r1')
    await e.testUsageScript(client, 'claude', { text: 'draft', profile: 'main' })
    await e.startAnyway(client, 7)
    await e.getSkillSets(client)
    await e.putSkillSet(client, set, 'r2')
    await e.deleteSkillSet(client, 'x', 'r3')
    await e.updateSkillSet(client, 'x')
    await e.getPlugins(client)
    await e.installPlugin(client, plugin)
    await e.putPlugin(client, plugin, 'r4')
    await e.updatePlugin(client, 'x')
    await e.deletePlugin(client, 'x')
    await e.getPluginLog(client, 'x')
    await e.runPluginAction(client, 'x', 'go')
    await e.runPluginAction(client, 'x', 'go', 5)
    expect(seen).toMatchObject([
      { method: 'GET', path: '/api/usage' },
      { method: 'POST', path: '/api/usage/refresh', body: {} },
      { method: 'GET', path: '/api/usage/scripts' },
      { method: 'GET', path: '/api/usage/scripts/claude' },
      { method: 'PUT', path: '/api/usage/scripts/claude', body: { text: 'code' }, opts: { ifMatch: '0' } },
      { method: 'DELETE', path: '/api/usage/scripts/claude', hasBody: false, opts: { ifMatch: 'r1' } },
      { method: 'POST', path: '/api/usage/scripts/claude/test', body: { text: 'draft', profile: 'main' } },
      { method: 'POST', path: '/api/tickets/7/start-anyway', body: {} },
      { method: 'GET', path: '/api/skillsets' },
      { method: 'PUT', path: '/api/skillsets/a%2Fb', body: set, opts: { ifMatch: 'r2' } },
      { method: 'DELETE', path: '/api/skillsets/x', hasBody: false, opts: { ifMatch: 'r3' } },
      { method: 'POST', path: '/api/skillsets/x/update', body: {} },
      { method: 'GET', path: '/api/plugins' },
      { method: 'POST', path: '/api/plugins', body: plugin },
      { method: 'PUT', path: '/api/plugins/p%20q', body: plugin, opts: { ifMatch: 'r4' } },
      { method: 'POST', path: '/api/plugins/x/update', body: {} },
      { method: 'DELETE', path: '/api/plugins/x', hasBody: false },
      { method: 'GET', path: '/api/plugins/x/log' },
      { method: 'POST', path: '/api/plugins/x/actions/go', body: {} },
      { method: 'POST', path: '/api/plugins/x/actions/go', body: { ticket: 5 } },
    ])
  })

  it('extension names are encoded', async () => {
    const { client, seen } = spy()
    await e.getUsageScript(client, '../x?')
    await e.updateSkillSet(client, 'a b')
    await e.getPluginLog(client, 'a/b')
    await e.runPluginAction(client, 'a/b', 'c d')
    expect(seen.map((s) => s.path)).toEqual([
      '/api/usage/scripts/..%2Fx%3F',
      '/api/skillsets/a%20b/update',
      '/api/plugins/a%2Fb/log',
      '/api/plugins/a%2Fb/actions/c%20d',
    ])
  })
})
