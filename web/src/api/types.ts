// Wire types of the HTTP API and the event stream. Names and nullability
// follow the server's JSON exactly: a pointer on the server is `T | null`
// here, a list the server always sends is a plain array. Timestamps are
// ISO 8601 strings.

export type Column = 'planning' | 'ready' | 'in_progress' | 'review' | 'done'
export type TicketType = 'story' | 'task'
export type Resolution = '' | 'completed' | 'cancelled' | 'exported'
export type Role = 'user' | 'ceo' | 'pm' | 'developer' | 'reviewer' | 'system'
export type Review = 'skipped' | 'agent' | 'human' | 'agent_then_human'
export type WhenApproved = 'merge' | 'export'
export type TaskRelease = 'agents' | 'user'
export type ReviewStage = '' | 'agent' | 'human' | 'merging' | 'exporting'
export type SessionState = 'starting' | 'running' | 'waiting' | 'ended'
export type WaitingReason = 'not_done' | 'export_not_merged' | 'cancelled'

/** Fields of a ticket the engine adds; absent until it is merged. */
export type FailedStep = '' | 'start' | 'agent' | 'merge'
export type StartBlocked = '' | 'slot' | 'paused' | 'no_model' | 'usage'

export const COLUMNS: readonly Column[] = ['planning', 'ready', 'in_progress', 'review', 'done']

export interface Actor {
  role: Role
  session?: string
  name?: string
}

export interface TicketProject {
  project: string
  base_branch: string
  branch: string
  worktree: string
  export_url?: string
}

export interface Waiting {
  ticket: number
  reason: WaitingReason
}

export interface Progress {
  done: number
  total: number
}

export interface AgentRef {
  session: string
  role: Role
  name: string
  state: SessionState
  project?: string
  purpose?: string
}

export interface TicketSummary {
  number: number
  type: TicketType
  title: string
  column: Column
  resolution: Resolution
  rank: number
  story: number | null
  projects: TicketProject[]
  supervisor: Role
  created_by: Actor
  model: string
  review: Review | null
  effective_review: Review
  when_approved: WhenApproved | null
  effective_when_approved: WhenApproved
  task_release: TaskRelease | null
  effective_task_release: TaskRelease | null
  max_parallel: number | null
  review_stage: ReviewStage
  review_round?: number
  attention: string
  export_url?: string
  export_merged: boolean
  depends_on: number[]
  blocks: number[]
  waiting_for: Waiting[]
  open_questions: number
  questions_for_user: number
  progress: Progress | null
  agents: AgentRef[]
  description_version: number
  created_at: string
  updated_at: string
  closed_at: string | null
  busy?: boolean
  failed_step?: FailedStep
  start_blocked?: StartBlocked
}

export interface Ticket extends TicketSummary {
  description: string
}

export interface TicketList {
  tickets: TicketSummary[]
  event_id: number
  next_done_before?: number
}

export interface TicketFilter {
  column?: Column
  type?: TicketType
  project?: string
  story?: number
  supervisor?: Role
  q?: string
  needs_me?: boolean
  done_limit?: number
  done_before?: number
}

export interface CreateTicket {
  type: TicketType
  title: string
  description?: string
  projects: string[]
  story?: number
  supervisor?: Role
  model?: string
  review?: Review
  when_approved?: WhenApproved
  task_release?: TaskRelease
  max_parallel?: number
  depends_on?: number[]
}

/** A missing key leaves the field unchanged; `null` clears it. */
export type UpdateTicket = {
  [K in keyof UpdateTicketFields]?: UpdateTicketFields[K] | null
}

export interface UpdateTicketFields {
  title: string
  projects: string[]
  story: number
  base_branches: Record<string, string>
  supervisor: Role
  model: string
  review: Review
  when_approved: WhenApproved
  task_release: TaskRelease
  max_parallel: number
}

export interface DescriptionUpdate {
  text: string
  base_version?: number
}

export interface DescriptionVersion {
  number: number
  text: string
  author: Actor
  restored_from: number | null
  created_at: string
  current: boolean
}

export interface MoveRequest {
  column: Column
  before: number | null
}

export interface ApproveRequest {
  export?: boolean
}

export interface RejectRequest {
  comment: string
}

export interface CancelRequest {
  delete_branch: boolean
  /** Declared for the server's contract; the browser never sends it. */
  discard_uncommitted?: boolean
}

export interface DependencyRequest {
  depends_on: number
}

export interface CommentRequest {
  body: string
  reply_to?: number
  question: boolean
  to?: string
}

export interface Mention {
  role: Role
  session?: string
}

export interface Comment {
  id: number
  ticket: number
  author: Actor
  body: string
  reply_to: number | null
  question_to: Role
  answered_by: number | null
  mentions: Mention[]
  created_at: string
}

export interface CommentResult {
  comment: Comment
  unreachable: Role[]
}

export interface DoneRequest {
  verdict: string
  comment: string
}

export interface AgentSession {
  id: string
  role: Role
  ticket: number | null
  project: string
  name: string
  model: string
  state: SessionState
  workdir: string
  end_reason: string
  started_at: string
  ended_at: string | null
}

export interface ApiEvent {
  id: number
  kind: string
  ticket: number | null
  actor: Actor
  /** Commit time, UTC with a fixed width, so values sort as text. */
  at: string
  data: unknown
}

export interface Project {
  id: string
  name: string
  path: string
  enabled: boolean
  missing: boolean
  root: string
  story_merge: string
}

export interface CreateProject {
  path: string
  name?: string
}

export interface UpdateProject {
  name?: string | null
  enabled?: boolean | null
  story_merge?: string | null
}

export interface Model {
  id: string
  profile: string
  name: string
  cli: string
  description: string
  selectable: boolean
}

export interface ServerInfo {
  version: string
  paused: boolean
  started_at: string
  address: string
}

export interface PausedRequest {
  paused: boolean
}

export interface Me {
  actor: Actor
  ticket: number | null
}

export interface FileError {
  file: string
  message: string
}

/** A Go duration as text, for example "15m". */
export type Duration = string

export interface CEOSettings {
  enabled: boolean
  may_approve_stories: boolean
}

export interface TaskSettings {
  require_user_release: boolean
}

export interface ReviewDefaults {
  story: Review
  task: Review
}

export interface WhenApprovedDefaults {
  story: WhenApproved
  task: WhenApproved
}

export interface WaitingSettings {
  timeouts_before_investigate: number
}

export interface HealthSettings {
  silent_after: Duration
  question_unanswered_after: Duration
}

export interface StopSettings {
  handoff_timeout: Duration
}

export interface WorkspaceRoot {
  path: string
  max_depth: number
  ignore: string[]
}

export interface General {
  max_running_agents: number
  ceo: CEOSettings
  tasks: TaskSettings
  review_defaults: ReviewDefaults
  when_approved_defaults: WhenApprovedDefaults
  waiting: WaitingSettings
  health: HealthSettings
  stop: StopSettings
  workspace_roots: WorkspaceRoot[]
  rescan_interval: Duration
  agent_env: Record<string, string>
}

export interface SettingsResponse {
  settings: General
  errors: FileError[]
}

export interface RemovedProjects {
  removed: string[]
}

export type CLI = 'claude' | 'codex' | 'gemini' | 'custom'
export type Assignment = 'round_robin' | 'random' | 'failover' | 'smart'

export interface PromptDelivery {
  inject?: boolean
  delay?: Duration
  retries?: number
  retry_wait?: Duration
}

export interface ProfileModel {
  args: string[] | null
  env: Record<string, string> | null
  usage_windows: string[] | null
  selectable?: boolean
  description: string
}

export interface Profile {
  name: string
  cli: string
  command: string
  args: string[] | null
  env: Record<string, string> | null
  account_dir: string
  usage_script: string
  prompt_delivery: PromptDelivery
  models: Record<string, ProfileModel> | null
}

export interface ProfilesResponse {
  profiles: Profile[]
  errors: FileError[]
}

export interface RoleSettings {
  name: string
  models: string[]
  assignment: string
}

export interface PromptInfo {
  name: string
  customized: boolean
}

export interface Prompt {
  name: string
  text: string
  customized: boolean
}

export interface PromptUpdate {
  text: string
}

/** The body of every error response. */
export interface ErrorBody {
  code: string
  message: string
  details?: unknown
}

export interface ErrorEnvelope {
  error: ErrorBody
}

// Event data

export interface TicketCreatedData {
  ticket: TicketSummary
}

export interface TicketUpdatedData {
  ticket: TicketSummary
  /** Contains "derived" when only waiting_for, progress, questions or agents changed. */
  fields: string[]
}

export interface TicketMovedData {
  ticket: TicketSummary
  from: Column
  to: Column
  action: string
  delete_branch?: boolean
}

export interface TicketReorderedData {
  ticket: number
  column: Column
  rank: number
}

export interface ColumnRenumberedData {
  column: Column
  /** Ticket number (as text) to its new rank. */
  ranks: Record<string, number>
}

export interface DependencyData {
  ticket: number
  depends_on: number
}

export interface DescriptionChangedData {
  ticket: number
  version: DescriptionVersion
}

export interface CommentCreatedData {
  comment: Comment
}

export interface QuestionAnsweredData {
  ticket: number
  question: number
  answer: number
}

export interface ReviewVerdictData {
  ticket: number
  round: number
  project: string
  verdict: string
}

export interface AgentData {
  agent: AgentSession
}

export interface ProjectChangedData {
  project: Project
}

export interface ProjectRemovedData {
  id: string
}

export type SettingsSection = 'general' | 'profiles' | 'roles' | 'prompts'

export interface SettingsChangedData {
  section: string
  name: string
}

export interface SettingsInvalidData {
  file: string
  message: string
}

export interface ServerPausedData {
  paused: boolean
}

export interface ResyncData {
  latest: number
}

/** Data type per event kind the server streams today. */
export interface EventDataByKind {
  'ticket.created': TicketCreatedData
  'ticket.updated': TicketUpdatedData
  'ticket.moved': TicketMovedData
  'ticket.reordered': TicketReorderedData
  'column.renumbered': ColumnRenumberedData
  'dependency.added': DependencyData
  'dependency.removed': DependencyData
  'description.changed': DescriptionChangedData
  'comment.created': CommentCreatedData
  'question.answered': QuestionAnsweredData
  'review.verdict': ReviewVerdictData
  'agent.created': AgentData
  'agent.updated': AgentData
  'agent.ended': AgentData
  'project.changed': ProjectChangedData
  'project.removed': ProjectRemovedData
  'settings.changed': SettingsChangedData
  'settings.invalid': SettingsInvalidData
  'server.paused': ServerPausedData
}

export type KnownEventKind = keyof EventDataByKind
