// Package api defines the wire types of the HTTP API and the event stream.
package api

import (
	"encoding/json"
	"time"
)

// Column is a board column.
type Column string

// Board columns.
const (
	ColumnPlanning   Column = "planning"
	ColumnReady      Column = "ready"
	ColumnInProgress Column = "in_progress"
	ColumnReview     Column = "review"
	ColumnDone       Column = "done"
)

// TicketType is the kind of ticket.
type TicketType string

// Ticket types.
const (
	TypeStory TicketType = "story"
	TypeTask  TicketType = "task"
)

// Resolution records how a Done ticket ended.
type Resolution string

// Resolutions.
const (
	ResolutionCompleted Resolution = "completed"
	ResolutionCancelled Resolution = "cancelled"
	ResolutionExported  Resolution = "exported"
)

// Role is the role of an actor or an agent.
type Role string

// Roles.
const (
	RoleUser      Role = "user"
	RoleCEO       Role = "ceo"
	RolePM        Role = "pm"
	RoleDeveloper Role = "developer"
	RoleReviewer  Role = "reviewer"
	RoleSystem    Role = "system"
)

// Review selects who reviews a ticket.
type Review string

// Review modes.
const (
	ReviewSkipped        Review = "skipped"
	ReviewAgent          Review = "agent"
	ReviewHuman          Review = "human"
	ReviewAgentThenHuman Review = "agent_then_human"
)

// WhenApproved selects what happens after approval.
type WhenApproved string

// WhenApproved values.
const (
	WhenApprovedMerge  WhenApproved = "merge"
	WhenApprovedExport WhenApproved = "export"
)

// TaskRelease selects who releases a story's tasks.
type TaskRelease string

// TaskRelease values.
const (
	TaskReleaseAgents TaskRelease = "agents"
	TaskReleaseUser   TaskRelease = "user"
)

// ReviewStage is the current review stage of a ticket in Review.
type ReviewStage string

// Review stages.
const (
	ReviewStageNone      ReviewStage = ""
	ReviewStageAgent     ReviewStage = "agent"
	ReviewStageHuman     ReviewStage = "human"
	ReviewStageMerging   ReviewStage = "merging"
	ReviewStageExporting ReviewStage = "exporting"
)

// SessionState is the state of an agent session.
type SessionState string

// Session states.
const (
	SessionStarting SessionState = "starting"
	SessionRunning  SessionState = "running"
	SessionWaiting  SessionState = "waiting"
	SessionEnded    SessionState = "ended"
)

// WaitingReason says why a dependency still blocks a ticket.
type WaitingReason string

// Waiting reasons.
const (
	WaitingNotDone         WaitingReason = "not_done"
	WaitingExportNotMerged WaitingReason = "export_not_merged"
	WaitingCancelled       WaitingReason = "cancelled"
)

// Actor identifies who performed an action.
type Actor struct {
	Role    Role   `json:"role"`
	Session string `json:"session,omitempty"`
	Name    string `json:"name,omitempty"`
}

// TicketProject is a ticket's binding to a project.
type TicketProject struct {
	Project    string `json:"project"`
	BaseBranch string `json:"base_branch"`
	Branch     string `json:"branch"`
	Worktree   string `json:"worktree"`
	ExportURL  string `json:"export_url"`
	// ExportState says where the export of this project stands (see
	// ExportState); ExportError is why it failed, for ExportFailedState only.
	ExportState ExportState `json:"export_state"`
	ExportError string      `json:"export_error"`
	// StartCommit and EndCommit are the full ids of the commits the
	// project's diff runs between, recorded when the ticket went from
	// Review to Done: the merge base the reviewed work left its base at,
	// and the reviewed tip. Both are "" for a ticket that is not done, one
	// done without them (cancelled, or done before omo recorded them) and
	// a project without a branch.
	StartCommit string `json:"start_commit"`
	EndCommit   string `json:"end_commit"`
}

// ExportState is where the export of one project of a ticket stands.
type ExportState string

const (
	// ExportNone: nothing to say, the ticket is not exporting.
	ExportNone ExportState = ""
	// ExportExporting: the ticket is being exported and this project is not over yet.
	ExportExporting ExportState = "exporting"
	// ExportExported: the project has its pull request (ExportURL).
	ExportExported ExportState = "exported"
	// ExportFailedState: the export of this project failed (ExportError says why).
	ExportFailedState ExportState = "failed"
	// ExportLeftOut: the project has nothing to export.
	ExportLeftOut ExportState = "left_out"
)

// Waiting is one dependency that blocks a ticket.
type Waiting struct {
	Ticket int           `json:"ticket"`
	Reason WaitingReason `json:"reason"`
}

// Progress counts finished tasks of a story.
type Progress struct {
	Done  int `json:"done"`
	Total int `json:"total"`
}

// AgentRef references a live agent session on a ticket.
type AgentRef struct {
	Session string       `json:"session"`
	Role    Role         `json:"role"`
	Name    string       `json:"name"`
	State   SessionState `json:"state"`
	Project string       `json:"project,omitempty"`
	Purpose Purpose      `json:"purpose"`
}

// TicketSummary is a ticket without its description.
type TicketSummary struct {
	Number                int             `json:"number"`
	Type                  TicketType      `json:"type"`
	Title                 string          `json:"title"`
	Column                Column          `json:"column"`
	Resolution            Resolution      `json:"resolution"`
	Rank                  float64         `json:"rank"`
	Story                 *int            `json:"story"`
	Projects              []TicketProject `json:"projects"`
	Supervisor            Role            `json:"supervisor"`
	CreatedBy             Actor           `json:"created_by"`
	Model                 string          `json:"model"`
	Review                *Review         `json:"review"`
	EffectiveReview       Review          `json:"effective_review"`
	WhenApproved          *WhenApproved   `json:"when_approved"`
	EffectiveWhenApproved WhenApproved    `json:"effective_when_approved"`
	TaskRelease           *TaskRelease    `json:"task_release"`
	EffectiveTaskRelease  *TaskRelease    `json:"effective_task_release"`
	MaxParallel           *int            `json:"max_parallel"`
	ReviewStage           ReviewStage     `json:"review_stage"`
	ReviewRound           int             `json:"review_round"`
	Attention             string          `json:"attention"`
	Busy                  bool            `json:"busy"`
	FailedStep            FailedStep      `json:"failed_step"`
	StartBlocked          StartBlocked    `json:"start_blocked"`
	ExportURL             string          `json:"export_url"`
	ExportMerged          bool            `json:"export_merged"`
	// Workdir is the working folder of a task without a project: omo makes
	// it when work starts and keeps it after the ticket is done, until the
	// user removes it. "" before work started, after the removal, and for
	// every ticket with a project.
	Workdir            string     `json:"workdir"`
	DependsOn          []int      `json:"depends_on"`
	Blocks             []int      `json:"blocks"`
	WaitingFor         []Waiting  `json:"waiting_for"`
	OpenQuestions      int        `json:"open_questions"`
	QuestionsForUser   int        `json:"questions_for_user"`
	Progress           *Progress  `json:"progress"`
	Agents             []AgentRef `json:"agents"`
	DescriptionVersion int        `json:"description_version"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	ClosedAt           *time.Time `json:"closed_at"`
}

// Ticket is a TicketSummary plus the current description.
type Ticket struct {
	TicketSummary
	Description string `json:"description"`
}

// TicketList is the snapshot answer of GET /api/tickets.
type TicketList struct {
	Tickets        []TicketSummary `json:"tickets"`
	EventID        int64           `json:"event_id"`
	NextDoneBefore int             `json:"next_done_before,omitempty"`
}

// TicketFilter holds the query parameters of GET /api/tickets.
type TicketFilter struct {
	Column     Column
	Type       TicketType
	Project    string
	Story      *int
	Supervisor Role
	Search     string
	NeedsMe    bool
	DoneLimit  int
	DoneBefore int
}

// CreateTicket is the body of POST /api/tickets.
type CreateTicket struct {
	Type         TicketType   `json:"type"`
	Title        string       `json:"title"`
	Description  string       `json:"description,omitempty"`
	Projects     []string     `json:"projects"`
	Story        *int         `json:"story,omitempty"`
	Supervisor   Role         `json:"supervisor,omitempty"`
	Model        string       `json:"model,omitempty"`
	Review       Review       `json:"review,omitempty"`
	WhenApproved WhenApproved `json:"when_approved,omitempty"`
	TaskRelease  TaskRelease  `json:"task_release,omitempty"`
	MaxParallel  *int         `json:"max_parallel,omitempty"`
	DependsOn    []int        `json:"depends_on,omitempty"`
}

// UpdateTicket is the body of PATCH /api/tickets/{n}; absent keys stay unchanged.
type UpdateTicket struct {
	Title        Optional[string]            `json:"title,omitzero"`
	Projects     Optional[[]string]          `json:"projects,omitzero"`
	Story        Optional[int]               `json:"story,omitzero"`
	BaseBranches Optional[map[string]string] `json:"base_branches,omitzero"`
	Supervisor   Optional[Role]              `json:"supervisor,omitzero"`
	Model        Optional[string]            `json:"model,omitzero"`
	Review       Optional[Review]            `json:"review,omitzero"`
	WhenApproved Optional[WhenApproved]      `json:"when_approved,omitzero"`
	TaskRelease  Optional[TaskRelease]       `json:"task_release,omitzero"`
	MaxParallel  Optional[int]               `json:"max_parallel,omitzero"`
}

// DescriptionUpdate is the body of PUT /api/tickets/{n}/description.
type DescriptionUpdate struct {
	Text        string `json:"text"`
	BaseVersion int    `json:"base_version,omitempty"`
}

// DescriptionVersion is one stored version of a description.
type DescriptionVersion struct {
	Number       int       `json:"number"`
	Text         string    `json:"text"`
	Author       Actor     `json:"author"`
	RestoredFrom *int      `json:"restored_from"`
	CreatedAt    time.Time `json:"created_at"`
	Current      bool      `json:"current"`
}

// MoveRequest is the body of POST /api/tickets/{n}/move.
type MoveRequest struct {
	Column Column `json:"column"`
	Before *int   `json:"before"`
}

// ApproveRequest is the body of POST /api/tickets/{n}/approve.
type ApproveRequest struct {
	Export bool `json:"export,omitempty"`
	Merge  bool `json:"merge,omitempty"`
}

// RejectRequest is the body of POST /api/tickets/{n}/reject.
type RejectRequest struct {
	Comment string `json:"comment"`
}

// CancelRequest is the body of POST /api/tickets/{n}/cancel.
type CancelRequest struct {
	DeleteBranch bool `json:"delete_branch"`
	// DiscardUncommitted also discards uncommitted changes in the worktree
	// when the branch is deleted.
	DiscardUncommitted bool `json:"discard_uncommitted,omitempty"`
}

// RemoveProjectRequest is the body of POST /api/tickets/{n}/remove-project:
// take Project off the ticket together with the worktree and branch omo
// made for it there.
type RemoveProjectRequest struct {
	Project string `json:"project"`
	// Discard allows the removal when it deletes work: commits of the
	// branch that its base branch does not contain, or uncommitted changes
	// in the worktree. Without it such a removal is refused with
	// CodeConflict and ProjectWork as its details.
	Discard bool `json:"discard,omitempty"`
}

// ProjectWork is the details of a project removal refused because it would
// delete work: the commits of Branch that Base does not contain ("<short
// id> <subject>", newest first, the last line "and N more" when there are
// more) and the uncommitted changes of the worktree (paths as git status
// names them). Both lists are never nil.
type ProjectWork struct {
	Project string   `json:"project"`
	Branch  string   `json:"branch"`
	Base    string   `json:"base"`
	Commits []string `json:"commits"`
	Files   []string `json:"files"`
}

// DependencyRequest is the body of POST /api/tickets/{n}/dependencies.
type DependencyRequest struct {
	DependsOn int `json:"depends_on"`
}

// CommentRequest is the body of POST /api/tickets/{n}/comments. To is one of
// supervisor, pm, ceo or user.
type CommentRequest struct {
	Body     string `json:"body"`
	ReplyTo  *int   `json:"reply_to,omitempty"`
	Question bool   `json:"question"`
	To       string `json:"to,omitempty"`
}

// Mention is a resolved @mention in a comment.
type Mention struct {
	Role    Role   `json:"role"`
	Session string `json:"session,omitempty"`
}

// Comment is a ticket comment.
type Comment struct {
	ID         int       `json:"id"`
	Ticket     int       `json:"ticket"`
	Author     Actor     `json:"author"`
	Body       string    `json:"body"`
	ReplyTo    *int      `json:"reply_to"`
	QuestionTo Role      `json:"question_to"`
	AnsweredBy *int      `json:"answered_by"`
	Mentions   []Mention `json:"mentions"`
	CreatedAt  time.Time `json:"created_at"`
}

// CommentResult is the answer of POST /api/tickets/{n}/comments.
type CommentResult struct {
	Comment     Comment `json:"comment"`
	Unreachable []Role  `json:"unreachable"`
}

// DoneRequest is the body of POST /api/ai/done. Verdict is approve or reject
// and is only accepted from reviewers.
type DoneRequest struct {
	Verdict string `json:"verdict"`
	Comment string `json:"comment"`
	// URL and Error are only used by export sessions.
	URL   string `json:"url,omitempty"`
	Error string `json:"error,omitempty"`
}

// Where the title or the body of an exported pull request comes from.
const (
	// TextFromAgent: the export job wrote it.
	TextFromAgent = "agent"
	// TextFromOmo: omo's own text, because the job's was missing or unusable.
	TextFromOmo = "omo"
)

// ExportText is the answer of POST /api/ai/export-text: the title and body
// the export job's pull request gets, each from the job's own file or omo's
// (TextFromAgent, TextFromOmo), and why omo used its own.
type ExportText struct {
	Title     string   `json:"title"`
	TitleFrom string   `json:"title_from"`
	BodyFrom  string   `json:"body_from"`
	BodyBytes int      `json:"body_bytes"`
	Problems  []string `json:"problems"`
}

// AgentSession is one agent session.
type AgentSession struct {
	ID          string       `json:"id"`
	Role        Role         `json:"role"`
	Ticket      *int         `json:"ticket"`
	Project     string       `json:"project"`
	Name        string       `json:"name"`
	Model       string       `json:"model"`
	State       SessionState `json:"state"`
	Workdir     string       `json:"workdir"`
	EndReason   string       `json:"end_reason"`
	Purpose     Purpose      `json:"purpose"`
	ReviewRound int          `json:"review_round"`
	StartedAt   time.Time    `json:"started_at"`
	EndedAt     *time.Time   `json:"ended_at"`
	// TerminalID names the web terminal of the session's running agent
	// (GET /api/agents/{terminal_id}/terminal). Only the user is shown it,
	// and only while the agent runs; it is unrelated to the session id.
	TerminalID string `json:"terminal_id,omitempty"`
	// MCP is MCPAgentConnected once the session used omo's MCP endpoint;
	// only the user is shown it.
	MCP string `json:"mcp,omitempty"`
}

// MCPAgentConnected marks an agent session that used omo's MCP endpoint.
const MCPAgentConnected = "connected"

// CEOState is the answer of GET /api/ceo and POST /api/ceo/stop.
type CEOState struct {
	// Stopped is true once the user stopped the CEO, until the user starts
	// it again: until then nothing else starts it.
	Stopped bool `json:"stopped"`
}

// Event is one entry of the event log and of the event stream.
type Event struct {
	ID     int64  `json:"id"`
	Kind   string `json:"kind"`
	Ticket *int   `json:"ticket"`
	Actor  Actor  `json:"actor"`
	// At is the commit time as stored in the event log: UTC with a fixed
	// width, so values sort as text.
	At   string          `json:"at"`
	Data json.RawMessage `json:"data"`
}

// Project is a registered project.
type Project struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Path    string `json:"path"`
	Enabled bool   `json:"enabled"`
	Missing bool   `json:"missing"`
	Root    string `json:"root"`
	// Merge holds the project's own merge modes; an empty value means the
	// general default (General.MergeDefaults). EffectiveMerge holds the
	// modes in effect: the project's own, else the general default.
	Merge          MergeModes `json:"merge"`
	EffectiveMerge MergeModes `json:"effective_merge"`
	// CurrentBranch is the branch checked out in the project folder and
	// DefaultBranch the repository's default branch, both read from git when
	// the project is listed; each is omitted when unknown (a detached HEAD,
	// a missing folder, no default branch to be found).
	CurrentBranch string `json:"current_branch,omitempty"`
	DefaultBranch string `json:"default_branch,omitempty"`
}

// CreateProject is the body of POST /api/projects.
type CreateProject struct {
	Path string `json:"path"`
	Name string `json:"name,omitempty"`
}

// UpdateProject is the body of PATCH /api/projects/{id}.
type UpdateProject struct {
	Name    Optional[string]           `json:"name,omitzero"`
	Enabled Optional[bool]             `json:"enabled,omitzero"`
	Merge   Optional[MergeModesUpdate] `json:"merge,omitzero"`
}

// Model is a selectable agent model.
type Model struct {
	ID          string `json:"id"`
	Profile     string `json:"profile"`
	Name        string `json:"name"`
	CLI         string `json:"cli"`
	Description string `json:"description"`
	Selectable  bool   `json:"selectable"`
}

// ServerInfo is the answer of GET /api/server.
type ServerInfo struct {
	Version   string    `json:"version"`
	Paused    bool      `json:"paused"`
	StartedAt time.Time `json:"started_at"`
	// Address is the address omo advertises: the public_url start option, or
	// the address it listens on.
	Address string `json:"address"`
	// AddressConfigured is true when Address comes from the public_url start
	// option.
	AddressConfigured bool `json:"address_configured"`
	// Listen is the host and port omo listens on, whatever Address says.
	Listen string `json:"listen"`
	// VersionURL is the page of this build's commit or release on GitHub; ""
	// when the version names neither (for example "dev").
	VersionURL string `json:"version_url"`
	// VersionCommit is the commit hash the version spells, "" for a release
	// build and an unknown form.
	VersionCommit string `json:"version_commit"`
}

// Me is the answer of GET /api/me.
type Me struct {
	Actor  Actor `json:"actor"`
	Ticket *int  `json:"ticket"`
}

// FileError reports a settings file that could not be loaded.
type FileError struct {
	File    string `json:"file"`
	Message string `json:"message"`
}
