package api

import "strings"

// Event kinds. Only kinds that contain a dot are streamed to clients.
const (
	KindTicketCreated       = "ticket.created"
	KindTicketUpdated       = "ticket.updated"
	KindTicketMoved         = "ticket.moved"
	KindTicketReordered     = "ticket.reordered"
	KindColumnRenumbered    = "column.renumbered"
	KindDependencyAdded     = "dependency.added"
	KindDependencyRemoved   = "dependency.removed"
	KindDescriptionChanged  = "description.changed"
	KindCommentCreated      = "comment.created"
	KindQuestionAnswered    = "question.answered"
	KindReviewVerdict       = "review.verdict"
	KindAgentCreated        = "agent.created"
	KindAgentUpdated        = "agent.updated"
	KindAgentEnded          = "agent.ended"
	KindProjectChanged      = "project.changed"
	KindProjectRemoved      = "project.removed"
	KindSettingsChanged     = "settings.changed"
	KindSettingsInvalid     = "settings.invalid"
	KindServerPaused        = "server.paused"
	KindSchedulerChanged    = "scheduler.changed"
	KindTicketRetried       = "ticket.retried"
	KindTerminalOpened      = "terminal.opened"
	KindTerminalExited      = "terminal.exited"
	KindTerminalClosed      = "terminal.closed"
	KindNotificationsSeen   = "notifications.seen"
	KindProjectsScanned     = "projects.scanned"
	KindUsageChanged        = "usage.changed"         // data UsageState
	KindSkillSetChanged     = "skillset.changed"      // data SkillSetState
	KindPluginTasksChanged  = "plugin.tasks_changed"  // data PluginTasksChangedData
	KindPluginChanged       = "plugin.changed"        // data PluginInfo
	KindImageAdded          = "image.added"           // data ImageEventData
	KindImageDeleted        = "image.deleted"         // data ImageEventData
	KindImageExpired        = "image.expired"         // data ImageEventData
	KindProxyForwardAdded   = "proxy.forward_added"   // data ProxyForward (relative address)
	KindProxyForwardRemoved = "proxy.forward_removed" // data ProxyForward (relative address)
	KindMCPChanged          = "mcp.changed"           // data MCPServerState
	// KindMCPToolsChanged has no data: the tools omo offers over MCP
	// changed.
	KindMCPToolsChanged = "mcp.tools_changed"
)

// Streamed reports whether events of this kind are delivered on the event stream.
func Streamed(kind string) bool { return strings.Contains(kind, ".") }

// Data payloads of the streamed event kinds.

// TicketCreatedData is the data of ticket.created.
type TicketCreatedData struct {
	Ticket TicketSummary `json:"ticket"`
}

// TicketUpdatedData is the data of ticket.updated. Fields holds "derived" when
// only waiting_for, progress, questions or agents changed.
type TicketUpdatedData struct {
	Ticket TicketSummary `json:"ticket"`
	Fields []string      `json:"fields"`
}

// TicketMovedData is the data of ticket.moved.
type TicketMovedData struct {
	Ticket       TicketSummary `json:"ticket"`
	From         Column        `json:"from"`
	To           Column        `json:"to"`
	Action       string        `json:"action"`
	DeleteBranch *bool         `json:"delete_branch,omitempty"`
	// DiscardUncommitted is set on cancel moves that discard uncommitted work.
	DiscardUncommitted *bool `json:"discard_uncommitted,omitempty"`
}

// SchedulerChangedData is the data of scheduler.changed.
type SchedulerChangedData struct {
	State SchedulerState `json:"state"`
}

// TicketRetriedData is the data of ticket.retried.
type TicketRetriedData struct {
	Ticket int        `json:"ticket"`
	Step   FailedStep `json:"step"`
}

// TicketReorderedData is the data of ticket.reordered.
type TicketReorderedData struct {
	Ticket int     `json:"ticket"`
	Column Column  `json:"column"`
	Rank   float64 `json:"rank"`
}

// ColumnRenumberedData is the data of column.renumbered; Ranks maps ticket
// numbers (as strings) to their new rank.
type ColumnRenumberedData struct {
	Column Column             `json:"column"`
	Ranks  map[string]float64 `json:"ranks"`
}

// DependencyData is the data of dependency.added and dependency.removed.
type DependencyData struct {
	Ticket    int `json:"ticket"`
	DependsOn int `json:"depends_on"`
}

// DescriptionChangedData is the data of description.changed.
type DescriptionChangedData struct {
	Ticket  int                `json:"ticket"`
	Version DescriptionVersion `json:"version"`
}

// CommentCreatedData is the data of comment.created.
type CommentCreatedData struct {
	Comment Comment `json:"comment"`
}

// QuestionAnsweredData is the data of question.answered.
type QuestionAnsweredData struct {
	Ticket   int `json:"ticket"`
	Question int `json:"question"`
	Answer   int `json:"answer"`
}

// ReviewVerdictData is the data of review.verdict.
type ReviewVerdictData struct {
	Ticket  int    `json:"ticket"`
	Round   int    `json:"round"`
	Project string `json:"project"`
	Verdict string `json:"verdict"`
}

// AgentData is the data of agent.created, agent.updated and agent.ended.
type AgentData struct {
	Agent AgentSession `json:"agent"`
}

// ProjectChangedData is the data of project.changed.
type ProjectChangedData struct {
	Project Project `json:"project"`
}

// ProjectRemovedData is the data of project.removed.
type ProjectRemovedData struct {
	ID string `json:"id"`
}

// Settings sections named by settings.changed.
const (
	SectionGeneral  = "general"
	SectionProfiles = "profiles"
	SectionRoles    = "roles"
	SectionPrompts  = "prompts"
	// SectionUsage, SectionSkillSets and SectionPlugins name the usage
	// settings, the skill set files and the plugin settings files.
	SectionUsage     = "usage"
	SectionSkillSets = "skillsets"
	SectionPlugins   = "plugins"
	// SectionMCPServers names the files of the MCP servers omo relays.
	SectionMCPServers = "mcpservers"
)

// Values of SettingsChangedData.Source.
const (
	SourceApp  = "app"
	SourceDisk = "disk"
)

// SettingsChangedData is the data of settings.changed. Source is "app" for a
// change made through the API and "disk" for a hand edit found by polling.
type SettingsChangedData struct {
	Section string `json:"section"`
	Name    string `json:"name"`
	Source  string `json:"source"`
}

// SettingsInvalidData is the data of settings.invalid.
type SettingsInvalidData struct {
	File    string `json:"file"`
	Message string `json:"message"`
}

// ServerPausedData is the data of server.paused.
type ServerPausedData struct {
	Paused bool `json:"paused"`
}

// ResyncData is the data of the resync stream event.
type ResyncData struct {
	Latest int64 `json:"latest"`
}
