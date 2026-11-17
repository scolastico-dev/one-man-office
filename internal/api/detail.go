package api

import "time"

// DiffFile is one changed file of a diff.
type DiffFile struct {
	Path string `json:"path"`
	// OldPath is set for renamed and copied files.
	OldPath string `json:"old_path"`
	// Status is added, modified, deleted, renamed, copied, type_changed or untracked.
	Status    string `json:"status"`
	Binary    bool   `json:"binary"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	// Patch is nil for binary and untracked files and for files left out for size.
	Patch *string `json:"patch"`
	// Truncated reports that the patch was left out or cut for size.
	Truncated bool `json:"truncated"`
}

// ProjectDiff is the diff of a ticket in one project.
type ProjectDiff struct {
	Project string `json:"project"`
	// Base is the branch compared against (see ComparedWith).
	Base   string `json:"base"`
	Branch string `json:"branch"`
	// BaseCommit is the merge base of Base and Branch.
	BaseCommit string `json:"base_commit"`
	HeadCommit string `json:"head_commit"`
	// Recorded says the diff runs between the commits recorded when the
	// ticket was done (BaseCommit and HeadCommit are those); it does not
	// depend on the branch, the worktree or the base as they are now, and
	// has no uncommitted changes.
	Recorded bool `json:"recorded"`
	// State is ok, not_started, branch_gone, commits_gone (the recorded
	// commits are no longer in the repository), project_unavailable or
	// failed.
	State                string     `json:"state"`
	Message              string     `json:"message"`
	Files                []DiffFile `json:"files"`
	FilesTruncated       bool       `json:"files_truncated"`
	Uncommitted          []DiffFile `json:"uncommitted"`
	UncommittedTruncated bool       `json:"uncommitted_truncated"`
	// ComparedWith is local (Base is the project's local base branch, the
	// default) or remote (Base is its remote-tracking branch, asked for
	// with base=remote).
	ComparedWith string `json:"compared_with"`
	// RemoteBase is set when the local base branch has a remote-tracking
	// branch the repository knows; absent otherwise.
	RemoteBase *RemoteBase `json:"remote_base,omitempty"`
}

// RemoteBase is the remote-tracking branch of a project's local base branch
// as the repository knows it: omo never fetches, so the counts are as of the
// repository's last fetch.
type RemoteBase struct {
	// Local is the local base branch (main), Name its remote-tracking
	// branch (origin/main).
	Local string `json:"local"`
	Name  string `json:"name"`
	// Behind counts the commits of Name that Local does not have, Ahead
	// the commits of Local that Name does not have.
	Behind int `json:"behind"`
	Ahead  int `json:"ahead"`
}

// TicketDiff is the answer of GET /api/tickets/{n}/diff.
type TicketDiff struct {
	Ticket     int           `json:"ticket"`
	Projects   []ProjectDiff `json:"projects"`
	ComputedAt time.Time     `json:"computed_at"`
}

// TicketThread is the answer of GET /api/tickets/{n}/thread.
type TicketThread struct {
	// Events are the thread's events in ascending id order.
	Events []Event `json:"events"`
	// More reports that older events exist.
	More bool `json:"more"`
}

// MentionNotice is one comment that mentions the user.
type MentionNotice struct {
	Comment Comment `json:"comment"`
	// Title is the title of the comment's ticket.
	Title  string `json:"title"`
	Unread bool   `json:"unread"`
}

// Notifications is the answer of GET /api/notifications.
type Notifications struct {
	// Mentions are the newest first, at most 50.
	Mentions    []MentionNotice `json:"mentions"`
	SeenThrough int             `json:"seen_through"`
	Unread      int             `json:"unread"`
}

// NotificationsSeen is the body of PUT /api/notifications/seen and the data
// of notifications.seen.
type NotificationsSeen struct {
	Through int `json:"through"`
}

// UserTerminal is a shell the user opened.
type UserTerminal struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Dir     string `json:"dir"`
	Project string `json:"project"`
	Ticket  *int   `json:"ticket"`
	// State is running or exited.
	State     string     `json:"state"`
	ExitError string     `json:"exit_error"`
	StartedAt time.Time  `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at"`
}

// OpenTerminalRequest is the body of POST /api/terminals: a project (its
// folder), a ticket with or without a project (the ticket's worktree), or
// Home alone (the user's home folder).
type OpenTerminalRequest struct {
	Project string `json:"project"`
	Ticket  *int   `json:"ticket,omitempty"`
	Home    bool   `json:"home,omitempty"`
}

// TerminalData is the data of terminal.opened, terminal.exited and
// terminal.closed.
type TerminalData struct {
	Terminal UserTerminal `json:"terminal"`
}
