import type { ApiClient, ApiResult } from './client'
import type {
  AgentSession,
  ApproveRequest,
  Column,
  CommentRequest,
  CommentResult,
  CreateProject,
  CreateTicket,
  DependencyRequest,
  DescriptionUpdate,
  DescriptionVersion,
  DiffFile,
  Model,
  MoveRequest,
  Notifications,
  NotificationsSeen,
  OpenTerminalRequest,
  PausedRequest,
  ProfilesResponse,
  Project,
  RejectRequest,
  RoleSettings,
  SchedulerState,
  ServerInfo,
  SettingsResponse,
  Ticket,
  TicketDiff,
  TicketFilter,
  TicketList,
  TicketThread,
  UpdateTicket,
  UserTerminal,
} from './types'

type R<T> = Promise<ApiResult<T>>

const ticketPath = (n: number) => `/api/tickets/${n}`

export function listTickets(c: ApiClient, f: TicketFilter = {}): R<TicketList> {
  return c.get('/api/tickets', { ...f })
}

export function getTicket(c: ApiClient, n: number): R<Ticket> {
  return c.get(ticketPath(n))
}

export function createTicket(c: ApiClient, body: CreateTicket): R<Ticket> {
  return c.send('POST', '/api/tickets', body)
}

export function updateTicket(c: ApiClient, n: number, patch: UpdateTicket): R<Ticket> {
  return c.send('PATCH', ticketPath(n), patch)
}

export function setDescription(c: ApiClient, n: number, text: string, baseVersion: number): R<DescriptionVersion> {
  const body: DescriptionUpdate = { text, base_version: baseVersion }
  return c.send('PUT', `${ticketPath(n)}/description`, body)
}

export function moveTicket(c: ApiClient, n: number, column: Column, before: number | null): R<Ticket> {
  const body: MoveRequest = { column, before }
  return c.send('POST', `${ticketPath(n)}/move`, body)
}

/** Sends only the options that are true, so `{}` is a plain approval. */
export function approveTicket(c: ApiClient, n: number, opts: { export?: boolean; merge?: boolean } = {}): R<Ticket> {
  const body: ApproveRequest = {}
  if (opts.export === true) body.export = true
  if (opts.merge === true) body.merge = true
  return c.send('POST', `${ticketPath(n)}/approve`, body)
}

export function rejectTicket(c: ApiClient, n: number, comment: string): R<Ticket> {
  const body: RejectRequest = { comment }
  return c.send('POST', `${ticketPath(n)}/reject`, body)
}

/** Never asks the server to discard uncommitted work. */
export function cancelTicket(c: ApiClient, n: number, deleteBranch: boolean): R<Ticket> {
  return c.send('POST', `${ticketPath(n)}/cancel`, { delete_branch: deleteBranch })
}

export function retryTicket(c: ApiClient, n: number): R<Ticket> {
  return c.send('POST', `${ticketPath(n)}/retry`, {})
}

export function markMerged(c: ApiClient, n: number): R<Ticket> {
  return c.send('POST', `${ticketPath(n)}/mark-merged`)
}

export function addDependency(c: ApiClient, n: number, m: number): R<Ticket> {
  const body: DependencyRequest = { depends_on: m }
  return c.send('POST', `${ticketPath(n)}/dependencies`, body)
}

export function removeDependency(c: ApiClient, n: number, m: number): R<Ticket> {
  return c.send('DELETE', `${ticketPath(n)}/dependencies/${m}`)
}

export function listProjects(c: ApiClient, all?: boolean): R<Project[]> {
  return c.get('/api/projects', { all })
}

export function createProject(c: ApiClient, path: string, name?: string): R<Project> {
  const body: CreateProject = name === undefined ? { path } : { path, name }
  return c.send('POST', '/api/projects', body)
}

export function getServer(c: ApiClient): R<ServerInfo> {
  return c.get('/api/server')
}

export function setPaused(c: ApiClient, paused: boolean): R<ServerInfo> {
  const body: PausedRequest = { paused }
  return c.send('PUT', '/api/server/paused', body)
}

export function stopAllAgents(c: ApiClient): R<unknown> {
  return c.send('POST', '/api/agents/stop-all', {})
}

export function listModels(c: ApiClient): R<Model[]> {
  return c.get('/api/models')
}

export function getProfiles(c: ApiClient): R<ProfilesResponse> {
  return c.get('/api/profiles')
}

export function getRoles(c: ApiClient): R<RoleSettings[]> {
  return c.get('/api/roles')
}

export function getSettings(c: ApiClient): R<SettingsResponse> {
  return c.get('/api/settings')
}

export function getTicketThread(c: ApiClient, n: number, o: { before?: number; limit?: number } = {}): R<TicketThread> {
  return c.get(`${ticketPath(n)}/thread`, { before: o.before, limit: o.limit })
}

export function postComment(c: ApiClient, n: number, body: CommentRequest): R<CommentResult> {
  return c.send('POST', `${ticketPath(n)}/comments`, body)
}

export function getDescriptions(c: ApiClient, n: number): R<DescriptionVersion[]> {
  return c.get(`${ticketPath(n)}/descriptions`)
}

export function restoreDescription(c: ApiClient, n: number, version: number): R<DescriptionVersion> {
  return c.send('POST', `${ticketPath(n)}/descriptions/${version}/restore`)
}

export function getTicketAgents(c: ApiClient, n: number): R<AgentSession[]> {
  return c.get(`${ticketPath(n)}/agents`)
}

export function listAgents(c: ApiClient, live: boolean): R<AgentSession[]> {
  return c.get('/api/agents', { live })
}

export function getScheduler(c: ApiClient): R<SchedulerState> {
  return c.get('/api/scheduler')
}

export function openCEO(c: ApiClient): R<AgentSession> {
  return c.send('POST', '/api/ceo', {})
}

export function newCEOSession(c: ApiClient): R<AgentSession> {
  return c.send('POST', '/api/ceo/new-session', {})
}

/** The readable transcript of a finished session; `truncated` when the server cut it to its newest part. */
export async function getSessionLog(c: ApiClient, id: string): Promise<{ text: string; truncated: boolean }> {
  const { text, headers } = await c.getText(`/api/sessions/${encodeURIComponent(id)}/log`)
  return { text, truncated: headers.get('Omo-Log-Truncated') === 'true' }
}

export function getTicketDiff(c: ApiClient, n: number): R<TicketDiff> {
  return c.get(`${ticketPath(n)}/diff`)
}

export function getDiffFile(
  c: ApiClient,
  n: number,
  q: { project: string; path: string; uncommitted?: boolean },
): R<DiffFile> {
  return c.get(`${ticketPath(n)}/diff/file`, { project: q.project, path: q.path, uncommitted: q.uncommitted })
}

export function listTerminals(c: ApiClient): R<UserTerminal[]> {
  return c.get('/api/terminals')
}

export function openTerminal(c: ApiClient, req: OpenTerminalRequest): R<UserTerminal> {
  return c.send('POST', '/api/terminals', req)
}

export function closeTerminal(c: ApiClient, id: string): R<unknown> {
  return c.send('DELETE', `/api/terminals/${encodeURIComponent(id)}`)
}

export function getNotifications(c: ApiClient): R<Notifications> {
  return c.get('/api/notifications')
}

export function markNotificationsSeen(c: ApiClient, through: number): R<Notifications> {
  const body: NotificationsSeen = { through }
  return c.send('PUT', '/api/notifications/seen', body)
}

/** The websocket path of a running agent's terminal. */
export function agentTerminalPath(terminalId: string): string {
  return `/api/agents/${encodeURIComponent(terminalId)}/terminal`
}

/** The websocket path of a user shell. */
export function userTerminalPath(id: string): string {
  return `/api/terminals/${encodeURIComponent(id)}/socket`
}
