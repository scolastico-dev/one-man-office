package api

// FailedStep names the engine step that is blocked on a person.
type FailedStep string

const (
	FailedNone  FailedStep = ""
	FailedStart FailedStep = "start"
	FailedAgent FailedStep = "agent"
	FailedMerge FailedStep = "merge"
)

// Purpose is what an agent session was started for.
type Purpose string

const (
	PurposeWork   Purpose = "work"
	PurposeExport Purpose = "export"
)

// StartBlocked says why a startable Ready ticket has not started.
type StartBlocked string

const (
	StartBlockedNone    StartBlocked = ""
	StartBlockedSlot    StartBlocked = "slot"
	StartBlockedPaused  StartBlocked = "paused"
	StartBlockedNoModel StartBlocked = "no_model"
	StartBlockedUsage   StartBlocked = "usage"
)

// ReadyResponse is the answer of POST /api/ai/ready.
type ReadyResponse struct {
	Prompt string `json:"prompt"`
}

// WaitRequest is the body of POST /api/ai/wait.
type WaitRequest struct {
	Timeout Duration `json:"timeout"`
}

// WaitResponse is the answer of POST /api/ai/wait.
type WaitResponse struct {
	Reason string `json:"reason"`
}

// SchedulerState is the engine's current load: how many tasks and stories
// are in work, against their limits.
type SchedulerState struct {
	Tasks   SlotLoad `json:"tasks"`
	Stories SlotLoad `json:"stories"`
	Paused  bool     `json:"paused"`
}

// SlotLoad is how many tickets of one kind are in work and how many may be.
// InWork can exceed Limit after the limit was lowered.
type SlotLoad struct {
	InWork int `json:"in_work"`
	Limit  int `json:"limit"`
}
