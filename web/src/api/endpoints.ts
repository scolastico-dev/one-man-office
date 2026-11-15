import type { ApiClient, ApiResult } from './client'
import type {
  Column,
  CreateProject,
  CreateTicket,
  DependencyRequest,
  DescriptionUpdate,
  DescriptionVersion,
  Model,
  MoveRequest,
  PausedRequest,
  ProfilesResponse,
  Project,
  RejectRequest,
  RoleSettings,
  ServerInfo,
  SettingsResponse,
  Ticket,
  TicketFilter,
  TicketList,
  UpdateTicket,
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

export function approveTicket(c: ApiClient, n: number): R<Ticket> {
  return c.send('POST', `${ticketPath(n)}/approve`, {})
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
