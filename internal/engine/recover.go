package engine

import "context"

// recoverAtStart prepares what a run needs before its first pass, after
// startup ended the processes, sessions and claims of the previous run: it
// gives omo's git client the agent environment before any git work, and
// records exports that were merged while omo was down.
//
// Resuming started work needs nothing here. The first pass is an ordinary
// one: every ticket in progress, and every ticket in review whose review
// needs an agent, has no live session, so the planner starts a fresh agent
// for each as a continuation of started work, in priority order and
// regardless of the limit, and nothing while omo is paused. Each fresh
// agent's omo ai ready prompt carries the restart warning, since an earlier
// session of its slot exists. What changed on disk while omo was down is
// found by that start: a worktree folder that is gone is recreated from
// its branch; a branch that is gone, or a folder that holds something else,
// becomes the failed step start; a project that is gone or disabled flags
// the ticket and starts nothing. None of these is retried until a person
// acts, and an agent that cannot start or keeps crashing is flagged after
// three attempts, so a restart costs at most three attempts per agent.
func (e *Engine) recoverAtStart(ctx context.Context) {
	// Before the first pass, so its merges carry the agents' author; ctx
	// ends a command that hangs when omo stops.
	e.syncGitEnvironment(ctx)
	e.checkExportsMerged(ctx)
}
