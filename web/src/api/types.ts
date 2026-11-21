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
export type ReviewStage = '' | 'agent' | 'human' | 'merging' | 'exporting' | 'awaiting_merge'
export type SessionState = 'starting' | 'running' | 'waiting' | 'ended'
export type WaitingReason = 'not_done' | 'export_not_merged' | 'cancelled'

/** The step of a ticket's work that failed, and why the engine has not started a ticket. */
export type FailedStep = '' | 'start' | 'agent' | 'merge'
/** `gate` is never stored: the board shows it while a plugin gate of the start is pending or holding. */
export type StartBlocked = '' | 'slot' | 'paused' | 'no_model' | 'usage' | 'gate'
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
  export_state: ExportState
  /** Why the export of this project failed; set with export_state "failed" only. */
  export_error: string
  /** The remote address and branch identities captured at export; the address is redacted for display. */
  export_remote: string
  export_branch: string
  export_base: string
  export_commit: string
  export_target_commit: string
  /** The most recent successful export, retained while follow-up work is reviewed. */
  last_export_commit?: string
  last_export_url?: string
  /** The commit handed in for the current review. */
  review_commit?: string
  /**
   * The merge base and reviewed commit recorded for the completed or exported diff, full ids.
   * Exported tickets may have them while waiting in Review. Empty before recording or after cancellation.
   */
  start_commit?: string
  end_commit?: string
}

/** Where the export of one project of a ticket stands; "" says nothing. */
export type ExportState = '' | 'exporting' | 'exported' | 'failed' | 'left_out'

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
  /** The working folder of a task without a project; empty before work started, after its removal and with a project. */
  workdir: string
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
  /** The move plugins hold or still have to answer for; null when no plugin gates the next move. */
  gate: TicketGates | null
}

/** A move of a ticket that plugins can gate: `release` is Planning to Ready For Dev, `start` is Ready For Dev to In Progress. */
export type GateMove = 'release' | 'start'
export type GateState = 'pending' | 'held' | 'passed'

/** A button a plugin offers on a ticket it holds; `action` names a manual hook of the same plugin. */
export interface GateChoice {
  label: string
  action: string
}

export interface TicketGate {
  plugin: string
  state: GateState
  reason: string
  choices: GateChoice[]
}

export interface TicketGates {
  move: GateMove
  from: Column
  to: Column
  gates: TicketGate[]
}

export interface GateReleaseRequest {
  plugin: string
}

export interface GateCancelRequest {
  plugin?: string
}

/** The data of ticket.held, ticket.released, ticket.refused, ticket.overridden and ticket.hold_cancelled. */
export interface TicketGateData {
  ticket: TicketSummary
  move: GateMove
  /** Empty when no single plugin is meant. */
  plugin: string
  reason: string
  choices: GateChoice[]
}

export interface Ticket extends TicketSummary {
  description: string
}

export interface TicketList {
  tickets: TicketSummary[]
  event_id: number
  next_done_before?: number
  /** Matching, readable Done tickets before paging; absent on older servers. */
  done_total?: number
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

export interface FollowUpRequest {
  comment: string
  role: 'developer' | 'reviewer'
}

export interface CancelRequest {
  delete_branch: boolean
  /** Declared for the server's contract; the browser never sends it. */
  discard_uncommitted?: boolean
}

/** POST /api/tickets/{n}/remove-project: the project leaves the ticket with the worktree and branch omo made there. */
export interface RemoveProjectRequest {
  project: string
  /** Also delete the branch's commits its base lacks and the worktree's uncommitted changes. */
  discard?: boolean
}

/** The `details` of a refused project removal that would delete work (code `conflict`). */
export interface ProjectWork {
  project: string
  branch: string
  base: string
  /** "<short id> <subject>", newest first; the last line may be "and N more". */
  commits: string[]
  /** Uncommitted changes, as git status names them. */
  files: string[]
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

/** Answer of POST /api/ai/export-text: where the pull request's title and body come from. */
export interface ExportText {
  title: string
  title_from: 'agent' | 'omo'
  body_from: 'agent' | 'omo'
  body_bytes: number
  problems: string[]
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
  end_kind: 'stopped' | 'finished' | 'crashed' | 'usage' | 'shutdown' | 'user' | ''
  purpose: Purpose
  review_round: number
  started_at: string
  ended_at: string | null
  /** Only for a running agent. */
  terminal_id?: string
  /** 'connected' once the session used omo's MCP endpoint; only the user is shown it. */
  mcp?: 'connected'
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
  /** The project's own merge modes; '' means the general default (`merge_defaults`). */
  merge: ProjectMergeModes
  /** The merge modes in effect: the project's own, else the general default. */
  effective_merge: MergeModes
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
  /** Sets the project's own merge mode of each kind named; '' goes back to the general default. Kinds left out stay. */
  merge?: MergeModesUpdate
}

/**
 * How omo merges a ticket's branch into its target: `merge` makes a merge commit "Merge #N: <title>";
 * `squash` makes one commit "<title> (#N)" on top of the target (a story's lists its completed tasks).
 */
export type MergeMode = 'merge' | 'squash'

/** A merge mode per kind of merge: a story into its base branch, a task without a story into its base branch, a task into its story's branch. */
export interface MergeModes {
  story: MergeMode
  task: MergeMode
  story_task: MergeMode
}

/** A project's own merge modes: '' means "use the general default". */
export type ProjectMergeModes = { [K in keyof MergeModes]: MergeMode | '' }

export interface MergeModesUpdate {
  story?: MergeMode | ''
  task?: MergeMode | ''
  story_task?: MergeMode | ''
}

export type ProjectBatchAction = 'remove' | 'disable' | 'enable'

/** The body of POST /api/projects/batch. */
export interface ProjectBatch {
  ids: string[]
  action: ProjectBatchAction
}

/** The answer of POST /api/projects/batch: one item per id, in request order. */
export interface ProjectBatchResult {
  results: ProjectBatchItem[]
}

/** Done, or the refusal the single route would have answered (in_use, validation, not_found, ...). */
export interface ProjectBatchItem {
  id: string
  done: boolean
  error?: ErrorBody
}

export interface Model {
  id: string
  profile: string
  name: string
  cli: string
  description: string
  selectable: boolean
}

/** The answer of `GET /api/ceo` and `POST /api/ceo/stop`. */
export interface CEOState {
  /** True once the user stopped the CEO, until the user starts it again; until then nothing else starts it. */
  stopped: boolean
}

export interface ServerInfo {
  version: string
  paused: boolean
  started_at: string
  /** The address omo advertises: the `public_url` start option, else the address it listens on. */
  address: string
  /** True when `address` comes from the `public_url` option. */
  address_configured: boolean
  /** The host and port omo listens on, whatever `address` says. */
  listen: string
  /** The page of this build's commit or release on GitHub; "" for a version that names neither. */
  version_url: string
  /** The commit hash the version spells; "" for a release build and an unknown form. */
  version_commit: string
}

export interface RelayStatus {
  state: string
  enabled: boolean
  origin: string
  host_id: string
  host_fp: string
  retry_seconds: number
}

export interface RelayConnectRequest {
  origin: string
  client_id: string
  new_id: boolean
  password?: string | null
}

export interface RelayAcceptRequest {
  aid: string
  pin: string
  public_key: string
  name: string
}

export interface RelayPending {
  phase: string
  expires: string
  aid: string
  ui_id: string
}

export interface RelayDevice {
  ui_id: string
  name: string
  state: string
  accepted_at: string
  live_connections: number
}

export interface RelayDeviceList {
  devices: RelayDevice[]
}

export interface RelayAccepted {
  aid: string
  host_fp: string
  device: RelayDevice
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
  /** The CEO may approve stories; while tickets_need_user_approval is on, only the stories the user created. */
  may_approve_stories: boolean
  /** Stories and tasks the CEO creates wait in Planning until the user approves them. */
  tickets_need_user_approval: boolean
}

export interface UISettings {
  /** A click on a ticket opens its page instead of the panel over the board. */
  open_tickets_as_page: boolean
  /** Screen reader software gets a text copy of the output of every terminal. */
  screen_reader_mode: boolean
}

export interface BranchSettings {
  /**
   * The name of a new ticket branch, with the placeholders {slug}, {number}, {type}, {project}, {host} and {user};
   * omo/{slug} by default. Existing branches keep their names.
   */
  template: string
}

/** The body of POST /api/settings/branch-preview. */
export interface BranchPreviewRequest {
  template: string
}

/** The answer of POST /api/settings/branch-preview: the name the template gives for an example ticket, or why it cannot be used. */
export interface BranchPreview {
  /** Empty when `error` is set. */
  branch: string
  error: string
}

export interface TaskSettings {
  require_user_release: boolean
}

export interface ReviewDefaults {
  story: Review
  /** Tasks without a story. */
  task: Review
  /** Tasks inside a story. */
  story_task: Review
}

export interface WhenApprovedDefaults {
  story: WhenApproved
  /** Tasks without a story. */
  task: WhenApproved
  /** Tasks inside a story: always merge. */
  story_task: WhenApproved
}

/**
 * How many tickets of each kind may be in work at once. A ticket is in work
 * from its move to In Progress until it is Done, whatever its agents do.
 */
export interface LimitSettings {
  tasks: number
  stories: number
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
  limits: LimitSettings
  ceo: CEOSettings
  tasks: TaskSettings
  review_defaults: ReviewDefaults
  when_approved_defaults: WhenApprovedDefaults
  merge_defaults: MergeModes
  waiting: WaitingSettings
  health: HealthSettings
  images: ImageSettings
  stop: StopSettings
  workspace_roots: WorkspaceRoot[]
  rescan_interval: Duration
  export_check_interval: Duration
  agent_env: Record<string, string>
  usage: UsageSettings
  ui: UISettings
  branches: BranchSettings
  /** Addresses (http or https) of additional plugin list files; their plugins are always community plugins. */
  plugin_lists: string[]
  /** Switches of omo's MCP endpoint. */
  mcp: MCPSettings
  lsp: LSPSettings
}

/** Registered stdio language servers in managed worktrees. */
export interface LSPSettings {
  servers: LSPServer[]
  /** Quiet interval before reporting diagnostics: 0s–1m, default 500ms. */
  debounce: Duration
  /** Profile CLI values forcing chat delivery even with an active MCP listener. */
  force_chat: CLI[]
}

export interface LSPServer {
  name: string
  enabled: boolean
  command: string
  args: string[]
  /** Literal environment overrides. */
  env: Record<string, string>
  /** First matching selector supplies the language ID; no match excludes the file. */
  files: LSPFileSelector[]
  initialization_options: Record<string, unknown>
  configuration: Record<string, unknown>
}

export interface LSPFileSelector {
  /** Case-sensitive path.Match glob; slashless matches basename at any depth,
   * otherwise a slash-separated worktree-relative path. No ** or backslash. */
  pattern: string
  language_id: string
}

export interface LSPIgnoreRequest {
  id: string
}

export interface LSPIgnoreResult {
  id: string
  ignored: boolean
  worktree: string
  file: string
  line: number
  column: number
  message: string
}

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

export type CLI = 'claude' | 'codex' | 'agy' | 'gemini' | 'custom'
export type Assignment = 'round_robin' | 'random' | 'failover' | 'smart' | 'super_smart'

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

/**
 * The `details` of the `in_use` error of `DELETE /api/profiles/{name}` while
 * roles list models of the profile: the roles, in the order of the roles
 * page. Deleting with `?detach=true` takes the models out of them.
 */
export interface ProfileInUse {
  roles: string[]
}

/** The answer of `DELETE /api/profiles/{name}?detach=true`. */
export interface ProfileDeleted {
  /** The roles that lost the profile's models; empty when no role listed one. */
  detached: DetachedRole[]
}

/** One role a profile deletion changed. */
export interface DetachedRole {
  role: string
  /** The model ids taken out of the role, in its order. */
  models: string[]
}

export interface ProfilesResponse {
  profiles: Profile[]
  errors: FileError[]
}

export interface RoleSettings {
  name: string
  models: string[]
  assignment: string
  /** Reset urgency weight for super smart; omitted means 24h, 0s disables the bonus. */
  super_smart_reset_weight?: Duration
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

/** The answer of POST /api/profiles/{name}/catalog-models. */
export interface CatalogModelsAdded {
  /** The names of the models added, strongest first; empty when none was missing. */
  added: string[]
  /** The profile as stored afterwards, with its revision. */
  profile: Profile
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

export type SettingsSection = 'general' | 'profiles' | 'roles' | 'prompts' | 'usage' | 'skillsets' | 'plugins'

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
/** How many tickets of one kind are in work, against their limit (in_work can exceed it after the limit was lowered). */
export interface SlotLoad {
  in_work: number
  limit: number
}

export interface SchedulerState {
  tasks: SlotLoad
  stories: SlotLoad
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
export type DiffState = 'ok' | 'not_started' | 'branch_gone' | 'commits_gone' | 'project_unavailable' | 'failed'

export interface ProjectDiff {
  project: string
  /** The branch compared against (see `compared_with`). */
  base: string
  branch: string
  /** The merge base. */
  base_commit: string
  head_commit: string
  /** The diff runs between the commits recorded when the ticket was done (`base_commit`, `head_commit`), whatever the branch is now. */
  recorded?: boolean
  state: DiffState
  message: string
  files: DiffFile[]
  files_truncated: boolean
  uncommitted: DiffFile[]
  uncommitted_truncated: boolean
  /** local: `base` is the project's local base branch (the default); remote: its remote-tracking branch. */
  compared_with: DiffBase
  /** Set when the local base branch has a remote-tracking branch the repository knows (never fetched). */
  remote_base?: RemoteBase
}

/** The `base` parameter of the diff routes and `ProjectDiff.compared_with`. */
export type DiffBase = 'local' | 'remote'
/** Lines of context around each change of a single-file read (0 to MAX_DIFF_CONTEXT), or the whole file. */
export type DiffContext = number | 'all'
/** The most lines of context the server takes. */
export const MAX_DIFF_CONTEXT = 1048576

/** The remote-tracking branch of a project's local base branch, as of the repository's last fetch. */
export interface RemoteBase {
  /** The local base branch, e.g. main. */
  local: string
  /** Its remote-tracking branch, e.g. origin/main. */
  name: string
  /** Commits of `name` that `local` does not have. */
  behind: number
  /** Commits of `local` that `name` does not have. */
  ahead: number
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

export interface UICapabilities {
  features: string[]
}

export interface UINotification {
  id: string
  kind: string
  title: string
  body: string
  /** Board-relative path. */
  destination: string
}

export interface UINotificationFeed {
  notifications: UINotification[]
  /** Decimal string preserving the 64-bit event id. */
  next_cursor: string
  /** Unscanned events remain, even if this page contains no notifications. */
  has_more: boolean
}

export interface UINotificationCursorReset {
  /** Replay retained events from this decimal cursor. */
  reset_cursor: string
  /** Start after all history from this decimal cursor. */
  latest_cursor: string
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

/** The body of POST /api/terminals: a project (its folder), a ticket with or without a project (its worktree), or `home` alone (the user's home folder). */
export interface OpenTerminalRequest {
  project?: string
  ticket?: number
  home?: boolean
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
  'ticket.held': TicketGateData
  'ticket.released': TicketGateData
  'ticket.refused': TicketGateData
  'ticket.overridden': TicketGateData
  'ticket.hold_cancelled': TicketGateData
  'terminal.opened': TerminalData
  'terminal.exited': TerminalData
  'terminal.closed': TerminalData
  'notifications.seen': NotificationsSeen
  'projects.scanned': ScanReport
  'usage.changed': UsageState
  'skillset.changed': SkillSetState
  'plugin.changed': PluginInfo
  'plugin.tasks_changed': PluginTasksChangedData
  'image.added': ImageEventData
  'image.deleted': ImageEventData
  'image.expired': ImageEventData
  'proxy.forward_added': ProxyForward
  'proxy.forward_updated': ProxyForward
  'proxy.forward_removed': ProxyForward
  'mcp.changed': MCPServerState
  /** No data: the tools omo offers over MCP changed. */
  'mcp.tools_changed': Record<string, never>
}

export type KnownEventKind = keyof EventDataByKind

/** The usage part of the general settings; percentages are 0 to 100. */
export interface UsageSettings {
  soft_percent: number
  hard_percent: number
  refresh_interval: Duration
  /** Thresholds by window name; a PATCH replaces the whole map. */
  windows: Record<string, UsageThreshold>
}

/** Overrides the general thresholds for one window; a missing value means the general one. */
export interface UsageThreshold {
  soft_percent?: number
  hard_percent?: number
}

export interface UsageWindow {
  /** The technical name: what a model's usage_windows and the per-window limits refer to. */
  name: string
  /** "" or what the script calls the window for people: one line, at most 60 characters. */
  label: string
  used_percent: number
  /** ISO time, null when the script did not say. */
  resets_at: string | null
}

export interface UsageReading {
  /** 16 hex digits naming the account. */
  key: string
  script: string
  /** The profiles that share the account, sorted. */
  profiles: string[]
  windows: UsageWindow[]
  fetched_at: string | null
  next_at: string | null
  running: boolean
  /** "" or the redacted message of the last failed run. */
  error: string
  /**
   * When the first failed run after the last successful one ended: the
   * windows are then those of fetched_at and keep counting for 30 minutes
   * from this time. Null while the last run succeeded.
   */
  stale_since: string | null
}

export type ModelUsageState = 'unmetered' | 'unknown' | 'ok' | 'soft' | 'hard'

export interface ModelUsage {
  model: string
  state: ModelUsageState
  /** The fullest applicable window; "" when none. */
  window: string
  /** That window's label for people ("Weekly, Fable"); "" when the script gave none. */
  window_label: string
  used_percent: number | null
  resets_at: string | null
  /** "" or one sentence for a model that lists windows of which the script reported none (its usage is then unknown). */
  note: string
}

export interface UsageState {
  readings: UsageReading[]
  /** Every model of every profile, sorted by id. */
  models: ModelUsage[]
}

export interface UsageScriptInfo {
  name: string
  has_default: boolean
  customized: boolean
  /** The profiles that name the script. */
  used_by: string[]
  revision: string
}

export interface UsageScript {
  name: string
  text: string
  default: string
  has_default: boolean
  customized: boolean
  revision: string
}

export interface UsageScriptUpdate {
  text: string
}

export interface UsageTest {
  /** The draft to run. */
  text: string
  /** Run as this profile's account. */
  profile: string
}

export interface UsageTestResult {
  windows: UsageWindow[]
  error: string
  log: string[]
  millis: number
}

/** The body of POST /api/tickets/{n}/start-anyway. */
export type StartAnyway = Record<string, never>

export type SkillSetUpdate = 'at_start' | 'pinned'

export interface SkillSet {
  name: string
  repository: string
  ref: string
  update: SkillSetUpdate
  enabled: boolean
  /** Folders and files agents get, relative to the repository's root (`skills/grill-me`); empty: the whole repository. */
  paths: string[]
  /** The roles (ceo, pm, developer, reviewer, export) whose agents get the set; empty: every role. */
  roles: string[]
  /** By role that gets the set: a text that comes with its skills. */
  instructions: Record<string, string>
  revision?: string
}

export type SkillSetCheckout = 'missing' | 'updating' | 'ok' | 'failed'

export interface SkillSetState {
  name: string
  state: SkillSetCheckout
  /** Full hash; "" without a checkout. */
  commit: string
  path: string
  updated_at: string | null
  /** Why an update failed: git output, cut and without credentials. */
  message: string
  /** The set's paths its checkout does not have; empty without checkout. */
  missing_paths: string[]
}

export interface SkillSetsResponse {
  skill_sets: SkillSet[]
  states: SkillSetState[]
  errors: FileError[]
}

/** One skill skills.sh found. `name`, `skill_id` and `source` are someone else's text (checked and cut by omo); `repository` and `url` are made by omo. */
export interface SkillSearchResult {
  /** `source/skill_id`. */
  id: string
  name: string
  skill_id: string
  /** The GitHub repository as owner/repo. */
  source: string
  installs: number
  /** The git address of `source` on GitHub. */
  repository: string
  /** The skill's page on skills.sh. */
  url: string
}

export interface SkillSearchResponse {
  query: string
  results: SkillSearchResult[]
}

/** A search result the user chose (POST /api/skillsets/search/resolve). */
export interface SkillSearchChoice {
  source: string
  skill_id: string
  name: string
}

/** What a new skill set for a chosen result starts with. */
export interface SkillSearchResolved {
  repository: string
  /** "": the default branch. */
  ref: string
  /** The skill's folder when found; empty when it is the whole repository or was not found. */
  paths: string[]
  found: boolean
  commit: string
}

export interface PluginSettings {
  name: string
  source: string
  subpath: string
  ref: string
  enabled: boolean
  config: Record<string, unknown>
  revision?: string
}

export interface PluginHook {
  event: string
  /** Manual hooks only. */
  name: string
  description: string
  /** Manual hooks: `ticket` or `global`. */
  scope: string
  /** Manual hooks: suppress automatic controls; explicit controls and invocation remain available. */
  hidden?: boolean
  /** Timer hooks only. */
  interval: string
  /** Gate hooks: the move the hook gates, `release` or `start`. */
  move?: string
  /** Gate hooks: what a failing hook does, `go_ahead` or `hold`. */
  failure?: string
}

export interface PluginButton {
  label: string
  place: 'ticket' | 'sidebar' | 'project'
  action: string
  per_worktree?: boolean
}

export interface PluginFrontend {
  script?: string
  assets?: string
}

export interface PluginConfigChoice {
  value: string
  label: string
}

export interface PluginConfigField {
  key: string
  label: string
  type: 'select' | 'boolean' | 'string' | 'number'
  choices?: PluginConfigChoice[]
}

export type PluginState = 'ready' | 'running' | 'error' | 'disabled' | 'not_installed' | 'invalid'

export interface PluginInfo {
  name: string
  version: string
  description: string
  state: PluginState
  /** Why the plugin is invalid or what its last error was. */
  message: string
  /** The installed commit; "" when it is not known. */
  commit: string
  last_event: string
  last_run_at: string | null
  hooks: PluginHook[]
  buttons: PluginButton[]
  frontend: PluginFrontend
  config_fields: PluginConfigField[]
  /** Manifest and public file SHA-256, recomputed on load. */
  content_id: string
}

/** How far omo's maintainers vouch for a plugin of omo's list; a source the list does not name counts as `community`. */
export type PluginTag = 'official' | 'recommended' | 'community'

/** One plugin of the list omo provides, with the source it installs from. Its texts are data from the network: shown as plain text. */
export interface CatalogPlugin {
  name: string
  description: string
  tag: PluginTag
  source: string
  ref: string
  subpath: string
  /** The address of the additional list the plugin comes from; '' for omo's own list. */
  list: string
  /** That list's host; '' for omo's own list. */
  origin: string
}

/** How reading one additional plugin list went. Its message and notes are about data from the network: shown as plain text. */
export interface PluginListState {
  /** The address as the settings name it. */
  url: string
  origin: string
  /** False when the list could not be fetched or read; `message` says why. */
  available: boolean
  message: string
  /** The entries of the list that are not shown, and why. */
  notes: string[]
  fetched_at: string | null
}

/** GET /api/plugins/catalog: the list, or (`available` false) why it could not be read. */
export interface PluginCatalog {
  available: boolean
  message: string
  /** Where the list is read from. */
  source: string
  ref: string
  fetched_at: string | null
  /** omo's own plugins (when its list is available), then those of the additional lists. */
  plugins: CatalogPlugin[]
  /** The state of every additional list, in the order of the settings. */
  lists: PluginListState[]
}

/** POST /api/plugins, PUT /api/plugins/{name}: the settings and the warning the user confirmed for a source that is not official. */
export interface PluginInstall extends PluginSettings {
  acknowledge?: Exclude<PluginTag, 'official'>
}

/** POST /api/plugins/{name}/update. */
export interface PluginUpdate {
  acknowledge?: Exclude<PluginTag, 'official'>
}

/** Details of a refusal that needs the user's acknowledgement: the tag the server gave the source. */
export interface PluginConfirmation {
  field: 'acknowledge'
  tag: PluginTag
}

export interface PluginsResponse {
  plugins: PluginInfo[]
  settings: PluginSettings[]
  errors: FileError[]
}

export interface PluginLogLine {
  at: string
  message: string
}

export interface PluginRun {
  ticket?: number
  project?: string
}

export interface PluginRunResult {
  message: string
  url?: string
  forward_id?: string
}

/** How long uploaded images are kept once their ticket is done; 0 keeps them. */
export interface ImageSettings {
  keep_days_after_done: number
}

/** `stored`: the file is in omo's images folder; `expired`: deleted after its ticket was done long enough; `missing`: the file is gone. */
export type ImageState = 'stored' | 'expired' | 'missing'

/** An uploaded image. `name` is the name it was uploaded with; `path` the local file while stored; `markdown` the reference to write into a description or comment. */
export interface Image {
  id: string
  ticket: number
  name: string
  content_type: string
  size: number
  author: Actor
  created_at: string
  state: ImageState
  expired_at?: string
  path: string
  markdown: string
}

/** An image dropped or pasted into a terminal: the absolute path of the stored file (pasted into the terminal), its cleaned name, type and size. */
export interface TerminalImage {
  path: string
  name: string
  content_type: string
  size: number
}

/** Data of image.added, image.deleted and image.expired. */
export interface ImageEventData {
  ticket: number
  id: string
  name: string
  size: number
}

/** A registered web UI. Null ticket means global; base_path is the app's required path-mode base. */
export interface ProxyForward {
  plugin?: string
  task_id?: string
  id: string
  ticket: number | null
  host: string
  port: number
  scheme: string
  name: string
  start_path: string
  strip_prefix: boolean
  verify_tls: boolean
  base_path: string
  address: string
}

/** Registration only; the target process is started separately. */
export interface CreateProxyForward {
  ticket: number | null
  host: string
  port: number
  scheme: string
  name: string
  start_path: string
  strip_prefix: boolean
  verify_tls: boolean
}

export interface ProxyForwardList {
  forwards: ProxyForward[]
}

/** manifest.json of a settings zip: which omo wrote it, for which database schema, when, and whether the database is inside. */
export interface BackupManifest {
  format: number
  omo_version: string
  schema_version: number
  created_at: string
  database: boolean
}

/** What an import does to one file. */
export type ImportChange = 'add' | 'replace' | 'same'

/** One file of an import plan, by its path below the omo home. */
export interface ImportFile {
  path: string
  change: ImportChange
}

/** `POST /api/settings/import`: the zip passed every check and waits under `id`; nothing changed yet. */
export interface ImportPlan {
  id: string
  manifest: BackupManifest
  /** The settings files, server.yaml left out. */
  files: ImportFile[]
  /** What writing server.yaml would do; '' when the zip has none. */
  server: ImportChange | ''
  database: boolean
  agents_running: number
}

/** Body of `POST /api/settings/import/apply`. */
export interface ImportApply {
  id: string
  server: boolean
  database: boolean
}

/** Answer of `POST /api/settings/import/apply`. */
export interface ImportResult {
  written: string[]
  /** Absolute folder with the replaced files; '' when none was replaced. */
  backup: string
  database_pending: boolean
  restart_needed: boolean
}

export type MCPTransport = 'stdio' | 'http' | 'sse'
export type MCPInstances = 'per_session' | 'shared'
export type MCPAuth = 'none' | 'headers' | 'oauth'

/** An upstream MCP server omo relays: `mcpservers/<name>.yaml`; values are stored and shown as they are. */
export interface MCPServer {
  name: string
  enabled: boolean
  description: string
  transport: MCPTransport
  command: string
  args: string[]
  /** Plain values; `${VAR}` and `${VAR:-default}` expand from omo's environment. */
  env: Record<string, string>
  /** '' is the folder of the instance. */
  cwd: string
  instances: MCPInstances | ''
  url: string
  headers: Record<string, string>
  headers_command: string[]
  auth: MCPAuth | ''
  oauth: MCPOAuth
  /** Agent roles (ceo, pm, developer, reviewer, export) whose sessions see the tools; empty: every role. */
  roles: string[]
  /** Also offered to connections not tied to an omo agent session. */
  global: boolean
  tools: MCPToolFilter
  timeouts: MCPTimeouts
  source: string
  revision?: string
}

export interface MCPOAuth {
  client_id: string
  client_secret: string
  scopes: string[]
  resource: string
}

/** Upstream tool names; an empty allow list allows every tool, deny applies after allow. */
export interface MCPToolFilter {
  allow: string[]
  deny: string[]
}

/** Zero ("0s") is omo's default. */
export interface MCPTimeouts {
  startup: Duration
  call: Duration
}

export type MCPServerStateName = 'stopped' | 'starting' | 'running' | 'failed' | 'needs_login' | 'disabled'

export interface MCPServerState {
  name: string
  state: MCPServerStateName
  instances: number
  tools: string[]
  omitted: MCPOmittedTool[]
  last_error: string
  started_at: string | null
  auth: MCPAuthState
  offers_resources: boolean
  offers_prompts: boolean
}

export interface MCPOmittedTool {
  tool: string
  reason: string
}

export interface MCPAuthState {
  state: 'none' | 'connected' | 'needs_login' | 'error'
  expires_at: string | null
  scopes: string[]
}

/** Answer of `GET /api/mcp/servers`. */
export interface MCPServersResponse {
  servers: MCPServer[]
  states: MCPServerState[]
  errors: FileError[]
}

/** Answer of `POST /api/mcp/servers/test`. */
export interface MCPTestResult {
  tools: string[]
  omitted: MCPOmittedTool[]
  error: string
  offers_resources: boolean
  offers_prompts: boolean
  millis: number
}

export interface MCPLogLine {
  at: string
  message: string
}

/** Answer of `POST /api/mcp/servers/{name}/connect`. */
export interface MCPConnect {
  authorize_url: string
}

/** The general MCP switches. */
export interface MCPSettings {
  enabled: boolean
  inject: MCPInject
}

export interface MCPInject {
  claude: boolean
  codex: boolean
}

export interface MCPToken {
  id: string
  kind: 'pat' | 'oauth'
  name: string
  client_id: string
  redirect_host: string
  resource: string
  scope: string
  created_at: string
  expires_at: string | null
  refresh_expires_at: string | null
  last_used_at: string | null
}

/** Body of `POST /api/mcp/tokens`. */
export interface MCPTokenRequest {
  name: string
  expires_at?: string
}

/** Answer of `POST /api/mcp/tokens`; the secret is shown once. */
export interface MCPTokenCreated {
  token: MCPToken
  secret: string
}

export interface MCPClientTarget {
  id: string
  cli: 'claude' | 'codex' | 'agy'
  /** '' is the program's default file. */
  account: string
  config_path: string
}

export interface MCPClientState {
  target: MCPClientTarget
  state: 'absent' | 'installed' | 'outdated' | 'foreign'
  /** The omo entry; null when there is none. */
  entry: unknown
  entries: MCPClientEntry[]
  error: string
}

export interface MCPClientEntry {
  name: string
  server: MCPServer
  notes: string[]
  /** Why omo cannot take it over; '' when it can. */
  reason: string
}

/** Body of `POST /api/mcp/clients/{target}/plan`. */
export interface MCPClientPlanRequest {
  action: 'install' | 'remove'
}

export interface MCPClientPlan {
  target: MCPClientTarget
  action: 'install' | 'remove'
  file: string
  commands: string[][]
  before: unknown
  after: unknown
  message: string
  hash: string
}

/** Body of `POST /api/mcp/clients/{target}/apply`. */
export interface MCPClientApply {
  plan: string
}

export interface MCPClientApplied {
  state: MCPClientState
  backup: string
}

/** Answer of `GET /api/mcp/takeover`. */
export interface MCPTakeover {
  candidates: MCPTakeoverCandidate[]
  stays: MCPTakeoverStay[]
}

export interface MCPTakeoverCandidate {
  key: string
  name: string
  server: MCPServer
  sources: MCPTakeoverSource[]
  notes: string[]
  needs_login: boolean
  /** Why it is not chosen by default; '' when it is. */
  conflict: string
}

export interface MCPTakeoverSource {
  target: string
  entry_name: string
}

export interface MCPTakeoverStay {
  target: string
  entry_name: string
  reason: string
}

/** Body of `POST /api/mcp/takeover/apply`. */
export interface MCPTakeoverApply {
  take: MCPTakeoverPick[]
  remove_from_cli: boolean
}

export interface MCPTakeoverPick {
  key: string
  name: string
}

export interface MCPTakeoverReport {
  results: MCPTakeoverResult[]
  backups: string[]
}

export interface MCPTakeoverResult {
  name: string
  stored: boolean
  error: string
  removed: MCPTakeoverRemoval[]
  needs_login: boolean
}

export interface MCPTakeoverRemoval {
  target: string
  done: boolean
  error: string
}

/** Answer of `GET /api/oauth/consent`. */
export interface OAuthConsent {
  client_name: string
  client_id: string
  client_kind: 'cimd' | 'registered'
  redirect_host: string
  localhost_redirect: boolean
  resource: string
}

/** Body of `POST /api/oauth/consent`. */
export interface OAuthDecision {
  query: string
  approve: boolean
}

/** Answer of `POST /api/oauth/consent`. */
export interface OAuthRedirect {
  redirect: string
}

/** One supervised plugin process. IDs identify runs; names can be reused. */
export interface PluginTask {
  id: string
  name: string
  ticket?: number
  started_at: string
  ended_at?: string
  state: 'running' | 'exited'
  reason?: string
  error?: string
  output: string[]
}

export interface PluginTasks {
  tasks: PluginTask[]
}

/** Refetch a plugin's task snapshot on this event and after reconnect/resync. */
export interface PluginTasksChangedData {
  name: string
}
