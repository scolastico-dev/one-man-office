import type { ApiClient, ApiResult, Query } from './client'
import type {
  AgentEnvCheck,
  AgentEnvReport,
  AgentSession,
  ApproveRequest,
  CatalogModelsAdded,
  Column,
  CommentRequest,
  CommentResult,
  CreateProject,
  CreateTicket,
  DependencyRequest,
  DescriptionUpdate,
  DescriptionVersion,
  DetectResult,
  DiffBase,
  DiffContext,
  DiffFile,
  FolderListing,
  Image,
  Model,
  MoveRequest,
  Notifications,
  NotificationsSeen,
  OpenTerminalRequest,
  PausedRequest,
  Profile,
  ProfileDeleted,
  ProfilesResponse,
  Project,
  ProjectBatch,
  ProjectBatchAction,
  ProjectBatchResult,
  Prompt,
  PromptInfo,
  PromptPreview,
  BranchPreview,
  BranchPreviewRequest,
  PromptUpdate,
  RejectRequest,
  RemovedProjects,
  RoleSettings,
  ScanReport,
  ScanState,
  SchedulerState,
  ServerInfo,
  SettingsPatch,
  SettingsResponse,
  Ticket,
  TicketDiff,
  TicketFilter,
  TicketList,
  TicketThread,
  UpdateProject,
  UpdateTicket,
  UserTerminal,
  PluginInfo,
  PluginLogLine,
  PluginRun,
  PluginRunResult,
  PluginSettings,
  PluginsResponse,
  SkillSet,
  SkillSetState,
  SkillSetsResponse,
  UsageScript,
  UsageScriptInfo,
  UsageScriptUpdate,
  UsageState,
  UsageTest,
  UsageTestResult,
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

/**
 * The diff of a ticket. `base: 'remote'` compares each project whose base
 * branch has a remote-tracking branch with that branch (`compared_with`
 * says which was used); refused (validation) when no project has one.
 */
export function getTicketDiff(c: ApiClient, n: number, opts: { base?: DiffBase } = {}): R<TicketDiff> {
  return opts.base ? c.get(`${ticketPath(n)}/diff`, { base: opts.base }) : c.get(`${ticketPath(n)}/diff`)
}

/**
 * One file of a ticket's diff. `base: 'remote'` compares with the base
 * branch's remote-tracking branch; refused (validation) when it has none.
 * `context` reads the file's patch again with that many lines of context
 * around each change (`'all'`: the whole file); a patch that would be over
 * the single-file limit (1 MiB) is refused (validation) instead of cut.
 */
export function getDiffFile(
  c: ApiClient,
  n: number,
  q: { project: string; path: string; uncommitted?: boolean; base?: DiffBase; context?: DiffContext },
): R<DiffFile> {
  const query: Query = { project: q.project, path: q.path, uncommitted: q.uncommitted }
  if (q.base) query.base = q.base
  if (q.context !== undefined) query.context = String(q.context)
  return c.get(`${ticketPath(n)}/diff/file`, query)
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

const seg = encodeURIComponent

export function patchSettings(c: ApiClient, patch: SettingsPatch, revision: string): R<SettingsResponse> {
  return c.send('PATCH', '/api/settings', patch, { ifMatch: revision })
}

export function checkAgentEnv(c: ApiClient, env: Record<string, string>): R<AgentEnvReport> {
  const body: AgentEnvCheck = { env }
  return c.send('POST', '/api/settings/agent-env/check', body)
}

export function updateProject(c: ApiClient, id: string, patch: UpdateProject): R<Project> {
  return c.send('PATCH', `/api/projects/${seg(id)}`, patch)
}

export function removeProject(c: ApiClient, id: string): R<unknown> {
  return c.send('DELETE', `/api/projects/${seg(id)}`)
}

export function removeMissingProjects(c: ApiClient): R<RemovedProjects> {
  return c.send('DELETE', '/api/projects?missing=true')
}

/** Removes, switches off or switches on several projects; each is handled on its own and answered per id. */
export function batchProjects(c: ApiClient, ids: string[], action: ProjectBatchAction): R<ProjectBatchResult> {
  const body: ProjectBatch = { ids, action }
  return c.send('POST', '/api/projects/batch', body)
}

export function getScanState(c: ApiClient): R<ScanState> {
  return c.get('/api/projects/scan')
}

export function rescanProjects(c: ApiClient): R<ScanReport> {
  return c.send('POST', '/api/projects/rescan', {})
}

/** Lists the sub-folders of `path`; without a path, those of the user's home folder. */
export function listFolders(c: ApiClient, path?: string): R<FolderListing> {
  return c.get('/api/folders', { path })
}

/** A revision of "0" creates the profile only when it does not exist yet. */
export function putProfile(c: ApiClient, p: Profile, revision: string): R<Profile> {
  return c.send('PUT', `/api/profiles/${seg(p.name)}`, p, { ifMatch: revision })
}

export function deleteProfile(c: ApiClient, name: string, revision: string): R<unknown> {
  return c.send('DELETE', `/api/profiles/${seg(name)}`, undefined, { ifMatch: revision })
}

/**
 * Deletes a profile and first takes its models out of every role that lists
 * one (a role may be left with none); the answer names those roles. Open
 * tickets of one of its models still refuse the delete.
 */
export function deleteProfileDetached(c: ApiClient, name: string, revision: string): R<ProfileDeleted> {
  return c.send('DELETE', `/api/profiles/${seg(name)}?detach=true`, undefined, { ifMatch: revision })
}

/**
 * Adds to a profile the catalog's models for its CLI that it does not have
 * (by name). Its own models, command, arguments and environment stay as
 * they are; roles are not touched. A CLI without a catalog is refused.
 */
export function addCatalogModels(c: ApiClient, name: string, revision: string): R<CatalogModelsAdded> {
  return c.send('POST', `/api/profiles/${seg(name)}/catalog-models`, {}, { ifMatch: revision })
}

export function detectAgents(c: ApiClient): R<DetectResult> {
  return c.send('POST', '/api/agent-setup/detect', {})
}

export function putRole(c: ApiClient, r: RoleSettings, revision: string): R<RoleSettings> {
  return c.send('PUT', `/api/roles/${seg(r.name)}`, r, { ifMatch: revision })
}

export function listPrompts(c: ApiClient): R<PromptInfo[]> {
  return c.get('/api/prompts')
}

export function getPrompt(c: ApiClient, name: string): R<Prompt> {
  return c.get(`/api/prompts/${seg(name)}`)
}

export function putPrompt(c: ApiClient, name: string, text: string, revision: string): R<Prompt> {
  const body: PromptUpdate = { text }
  return c.send('PUT', `/api/prompts/${seg(name)}`, body, { ifMatch: revision })
}

export function resetPrompt(c: ApiClient, name: string, revision: string): R<unknown> {
  return c.send('DELETE', `/api/prompts/${seg(name)}`, undefined, { ifMatch: revision })
}

/** The branch name `template` gives for an example ticket, by the server's rules; nothing is saved. */
export function previewBranch(c: ApiClient, template: string): R<BranchPreview> {
  const body: BranchPreviewRequest = { template }
  return c.send('POST', '/api/settings/branch-preview', body)
}

export function previewPrompt(c: ApiClient, name: string, text: string): R<PromptPreview> {
  const body: PromptUpdate = { text }
  return c.send('POST', `/api/prompts/${seg(name)}/preview`, body)
}

export function getUsage(c: ApiClient): R<UsageState> {
  return c.get('/api/usage')
}

/** Runs the usage scripts now; the answer comes when they are done (up to about 70 seconds). */
export function refreshUsage(c: ApiClient): R<UsageState> {
  return c.send('POST', '/api/usage/refresh', {})
}

export function listUsageScripts(c: ApiClient): R<UsageScriptInfo[]> {
  return c.get('/api/usage/scripts')
}

export function getUsageScript(c: ApiClient, name: string): R<UsageScript> {
  return c.get(`/api/usage/scripts/${seg(name)}`)
}

/** A revision of "0" creates the script only when it does not exist yet. */
export function putUsageScript(c: ApiClient, name: string, text: string, revision: string): R<UsageScript> {
  const body: UsageScriptUpdate = { text }
  return c.send('PUT', `/api/usage/scripts/${seg(name)}`, body, { ifMatch: revision })
}

/** Resets a script to its default, or deletes one that has none. */
export function resetUsageScript(c: ApiClient, name: string, revision: string): R<unknown> {
  return c.send('DELETE', `/api/usage/scripts/${seg(name)}`, undefined, { ifMatch: revision })
}

export function testUsageScript(c: ApiClient, name: string, t: UsageTest): R<UsageTestResult> {
  return c.send('POST', `/api/usage/scripts/${seg(name)}/test`, t)
}

export function startAnyway(c: ApiClient, n: number): R<Ticket> {
  return c.send('POST', `${ticketPath(n)}/start-anyway`, {})
}

export function getSkillSets(c: ApiClient): R<SkillSetsResponse> {
  return c.get('/api/skillsets')
}

/** A revision of "0" creates the skill set only when it does not exist yet. */
export function putSkillSet(c: ApiClient, s: SkillSet, revision: string): R<SkillSet> {
  return c.send('PUT', `/api/skillsets/${seg(s.name)}`, s, { ifMatch: revision })
}

export function deleteSkillSet(c: ApiClient, name: string, revision: string): R<unknown> {
  return c.send('DELETE', `/api/skillsets/${seg(name)}`, undefined, { ifMatch: revision })
}

export function updateSkillSet(c: ApiClient, name: string): R<SkillSetState> {
  return c.send('POST', `/api/skillsets/${seg(name)}/update`, {})
}

export function getPlugins(c: ApiClient): R<PluginsResponse> {
  return c.get('/api/plugins')
}

/** Installs a plugin; only name, source, subpath and ref are read. */
export function installPlugin(c: ApiClient, p: PluginSettings): R<PluginInfo> {
  return c.send('POST', '/api/plugins', p)
}

export function putPlugin(c: ApiClient, p: PluginSettings, revision: string): R<PluginInfo> {
  return c.send('PUT', `/api/plugins/${seg(p.name)}`, p, { ifMatch: revision })
}

export function updatePlugin(c: ApiClient, name: string): R<PluginInfo> {
  return c.send('POST', `/api/plugins/${seg(name)}/update`, {})
}

export function deletePlugin(c: ApiClient, name: string): R<unknown> {
  return c.send('DELETE', `/api/plugins/${seg(name)}`)
}

export function getPluginLog(c: ApiClient, name: string): R<PluginLogLine[]> {
  return c.get(`/api/plugins/${seg(name)}/log`)
}

export function runPluginAction(c: ApiClient, name: string, action: string, ticket?: number): R<PluginRunResult> {
  const body: PluginRun = ticket === undefined ? {} : { ticket }
  return c.send('POST', `/api/plugins/${seg(name)}/actions/${seg(action)}`, body)
}

/** The header that carries an upload's original file name, percent-encoded. */
export const IMAGE_NAME_HEADER = 'Omo-Image-Name'

/** Uploads an image file to ticket `n`; the server decides from its bytes what it is. */
export function uploadImage(c: ApiClient, n: number, file: Blob, name: string): R<Image> {
  return c.upload(`${ticketPath(n)}/images`, file, { [IMAGE_NAME_HEADER]: encodeURIComponent(name) })
}

/** Every image of ticket `n`, with its state. */
export function listTicketImages(c: ApiClient, n: number): R<Image[]> {
  return c.get(`${ticketPath(n)}/images`)
}

/** The record of one image (any ticket the caller may read). */
export function getImage(c: ApiClient, id: string): R<Image> {
  return c.get(`/api/images/${encodeURIComponent(id)}`)
}

/** The bytes of a stored image, fetched with the login token (never in an address). */
export function getImageBytes(c: ApiClient, id: string): Promise<Blob> {
  return c.getBlob(`/api/images/${encodeURIComponent(id)}/content`)
}

export function deleteImage(c: ApiClient, id: string): R<unknown> {
  return c.send('DELETE', `/api/images/${encodeURIComponent(id)}`)
}
