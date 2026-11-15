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

/** The step of a ticket's work that failed, and why the engine has not started a ticket. */
export type FailedStep = '' | 'start' | 'agent' | 'merge'
export type StartBlocked = '' | 'slot' | 'paused' | 'no_model' | 'usage'
/** What an agent session was started for. */
export type Purpose = 'work' | 'export'

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
  export_url: string
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
  purpose: Purpose
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
  export_url: string
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
  busy: boolean
  failed_step: FailedStep
  start_blocked: StartBlocked
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
  merge?: boolean
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
  /** Only used by export sessions. */
  url?: string
  /** Only used by export sessions. */
  error?: string
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
  purpose: Purpose
  review_round: number
  started_at: string
  ended_at: string | null
  /** Only for a running agent. */
  terminal_id?: string
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
  /** The branch checked out in the folder; absent when unknown (detached HEAD, missing folder). */
  current_branch?: string
  /** The repository's default branch; absent when none can be found. */
  default_branch?: string
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

/** The answer of `POST /api/ai/ready` (agent socket). */
export interface ReadyResponse {
  prompt: string
}

/** The body of `POST /api/ai/wait` (agent socket). */
export interface WaitRequest {
  timeout: Duration
}

/** The answer of `POST /api/ai/wait` (agent socket). */
export interface WaitResponse {
  reason: string
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

/** A secret's value in answers; sent back, it keeps the stored value. */
export const HIDDEN_VALUE = 'omo:hidden'

/** The revision of a settings item that has no file. */
export const NO_REVISION = '0'

/** Names the field a validation message of a settings write belongs to (a dotted key). */
export interface FieldDetail {
  field: string
}

export interface SettingsResponse {
  settings: General
  errors: FileError[]
  /** Identifies the content of settings.yaml, for If-Match. */
  revision: string
  /** The omo home folder. */
  home: string
}

type DeepPartial<T> = T extends readonly unknown[] ? T : T extends object ? { [K in keyof T]?: DeepPartial<T[K]> } : T

/** The body of PATCH /api/settings: top-level keys of settings.yaml. Objects merge key by key; lists and `agent_env` replace the whole value. */
export type SettingsPatch = { [K in keyof General]?: DeepPartial<General[K]> }

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
  /** Identifies the profile's file, for If-Match; ignored in PUT bodies. */
  revision?: string
}

export interface ProfilesResponse {
  profiles: Profile[]
  errors: FileError[]
}

export interface RoleSettings {
  name: string
  models: string[]
  assignment: string
  /** Identifies the role's file, for If-Match; ignored in PUT bodies. */
  revision?: string
}

export type PromptKind = 'role' | 'instructions' | 'message'

export interface PromptInfo {
  name: string
  customized: boolean
  kind: PromptKind
  revision: string
}

export interface Prompt {
  name: string
  text: string
  customized: boolean
  kind: PromptKind
  /** omo's own text. */
  default: string
  revision: string
}

export interface PromptUpdate {
  text: string
}

export interface RootScan {
  path: string
  state: string
  message: string
  visited: number
  found: number
  added: string[]
  skipped: string[]
  links: number
}

export interface ScanReport {
  trigger: string
  started_at: string
  finished_at: string
  roots: RootScan[]
  added: string[]
  missing: number
}

export interface ScanState {
  running: boolean
  last: ScanReport | null
}

export interface FolderEntry {
  name: string
  path: string
  repository: boolean
  hidden: boolean
  link: boolean
}

export interface FolderListing {
  path: string
  parent: string
  home: string
  folders: FolderEntry[]
  more: boolean
}

export interface AgentEnvCheck {
  env: Record<string, string>
}

export interface EnvVarCheck {
  name: string
  state: 'set' | 'empty' | 'failed'
  message: string
}

export interface AgentEnvReport {
  variables: EnvVarCheck[]
}

export interface PromptPreview {
  text: string
  error: string
}

export interface DetectedCLI {
  cli: string
  command: string
  path: string
}

export interface DetectResult {
  found: DetectedCLI[]
  created: string[]
  assigned: string[]
  skipped: string[]
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
  /** Set on cancel moves that discarded uncommitted work. */
  discard_uncommitted?: boolean
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

export type SettingsSource = 'app' | 'disk'

export interface SettingsChangedData {
  section: string
  name: string
  /** `app` for a change through the API, `disk` for a hand edit found by polling. */
  source: SettingsSource
}

export interface SettingsInvalidData {
  file: string
  message: string
}

export interface ServerPausedData {
  paused: boolean
}

/** The engine's current load. */
export interface SchedulerState {
  running: number
  limit: number
  paused: boolean
}

export interface SchedulerChangedData {
  state: SchedulerState
}

export interface TicketRetriedData {
  ticket: number
  step: FailedStep
}

export interface DiffFile {
  path: string
  /** Set for renamed and copied files. */
  old_path: string
  status: DiffFileStatus
  binary: boolean
  additions: number
  deletions: number
  /** Null for binary and untracked files and for files left out for size. */
  patch: string | null
  /** The patch was left out or cut for size. */
  truncated: boolean
}

export type DiffFileStatus = 'added' | 'modified' | 'deleted' | 'renamed' | 'copied' | 'type_changed' | 'untracked'
export type DiffState = 'ok' | 'not_started' | 'branch_gone' | 'project_unavailable' | 'failed'

export interface ProjectDiff {
  project: string
  /** The branch compared against. */
  base: string
  branch: string
  /** The merge base. */
  base_commit: string
  head_commit: string
  state: DiffState
  message: string
  files: DiffFile[]
  files_truncated: boolean
  uncommitted: DiffFile[]
  uncommitted_truncated: boolean
}

export interface TicketDiff {
  ticket: number
  projects: ProjectDiff[]
  computed_at: string
}

export interface TicketThread {
  /** Ascending ids. */
  events: ApiEvent[]
  /** Older events exist. */
  more: boolean
}

export interface MentionNotice {
  comment: Comment
  /** The ticket's title. */
  title: string
  unread: boolean
}

export interface Notifications {
  /** Newest first, at most 50. */
  mentions: MentionNotice[]
  seen_through: number
  unread: number
}

export interface NotificationsSeen {
  through: number
}

export type UserTerminalState = 'running' | 'exited'

export interface UserTerminal {
  id: string
  title: string
  dir: string
  project: string
  ticket: number | null
  state: UserTerminalState
  exit_error: string
  started_at: string
  ended_at: string | null
}

export interface OpenTerminalRequest {
  project: string
  ticket?: number
}

export interface TerminalData {
  terminal: UserTerminal
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
  'scheduler.changed': SchedulerChangedData
  'ticket.retried': TicketRetriedData
  'terminal.opened': TerminalData
  'terminal.exited': TerminalData
  'terminal.closed': TerminalData
  'notifications.seen': NotificationsSeen
  'projects.scanned': ScanReport
}

export type KnownEventKind = keyof EventDataByKind
