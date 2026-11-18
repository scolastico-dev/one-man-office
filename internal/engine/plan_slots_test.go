package engine

import (
	"slices"
	"testing"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
)

// A slot belongs to the ticket, from Ready For Dev to In Progress until
// Done, whatever its agents do: one row per column and state the board has.
func TestPlanSlotTable(t *testing.T) {
	for _, row := range []struct {
		name  string
		holds bool
		setup func(w *world) // ticket 1, a task, and its sessions
	}{
		{"planning", false, func(w *world) { w.ticket(1, api.TypeTask, api.ColumnPlanning, 1) }},
		{"ready", false, func(w *world) { w.ticket(1, api.TypeTask, api.ColumnReady, 9) }},
		{"ready, waiting for a dependency", false, func(w *world) {
			w.ticket(1, api.TypeTask, api.ColumnReady, 9, waitingOn(99))
		}},
		{"ready, failed start", false, func(w *world) {
			w.ticket(1, api.TypeTask, api.ColumnReady, 9, func(t *board.EngineTicket) { t.FailedStep = api.FailedStart })
		}},
		{"ready, claimed by a gated start", true, func(w *world) {
			w.ticket(1, api.TypeTask, api.ColumnReady, 9, func(t *board.EngineTicket) { t.Busy = true })
		}},
		{"ready, gated start in flight", true, func(w *world) {
			w.ticket(1, api.TypeTask, api.ColumnReady, 9)
			w.in.inflight[1] = true
			w.in.starts = []pendingStart{{ticket: 1, role: api.RoleDeveloper}}
		}},
		{"in progress, agent running", true, func(w *world) {
			w.ticket(1, api.TypeTask, api.ColumnInProgress, 1)
			w.session(api.RoleDeveloper, 1, api.SessionRunning)
		}},
		{"in progress, agent in omo ai wait", true, func(w *world) {
			w.ticket(1, api.TypeTask, api.ColumnInProgress, 1)
			w.session(api.RoleDeveloper, 1, api.SessionWaiting)
		}},
		{"in progress, no agent and none can start (usage stop, restart)", true, func(w *world) {
			w.ticket(1, api.TypeTask, api.ColumnInProgress, 1)
			w.in.hasModel = func(r api.Role, _ api.Purpose, t board.EngineTicket) startCheck {
				if t.ID == 1 {
					return startUsage
				}
				return startOK
			}
		}},
		{"in progress, busy", true, func(w *world) {
			w.ticket(1, api.TypeTask, api.ColumnInProgress, 1, func(t *board.EngineTicket) { t.Busy = true })
		}},
		{"in progress, failed step", true, func(w *world) {
			w.ticket(1, api.TypeTask, api.ColumnInProgress, 1, func(t *board.EngineTicket) { t.FailedStep = api.FailedAgent })
		}},
		{"review, agent review", true, func(w *world) {
			w.ticket(1, api.TypeTask, api.ColumnReview, 1, stage(api.ReviewStageAgent))
			w.session(api.RoleDeveloper, 1, api.SessionWaiting)
			w.session(api.RoleReviewer, 1, api.SessionRunning, sessRound(1))
		}},
		{"review, waits for the user", true, func(w *world) {
			w.ticket(1, api.TypeTask, api.ColumnReview, 1, stage(api.ReviewStageHuman))
			w.session(api.RoleDeveloper, 1, api.SessionWaiting)
		}},
		{"review, merging", true, func(w *world) {
			w.ticket(1, api.TypeTask, api.ColumnReview, 1, stage(api.ReviewStageMerging),
				func(t *board.EngineTicket) { t.Busy = true })
		}},
		{"review, merge blocked", true, func(w *world) {
			w.ticket(1, api.TypeTask, api.ColumnReview, 1, stage(api.ReviewStageMerging),
				func(t *board.EngineTicket) { t.FailedStep = api.FailedMerge })
		}},
		{"review, exporting", true, func(w *world) {
			w.ticket(1, api.TypeTask, api.ColumnReview, 1, stage(api.ReviewStageExporting))
			w.session(api.RoleDeveloper, 1, api.SessionRunning, sessPurpose(api.PurposeExport), sessProject("p"))
		}},
		{"review, exported waiting for merge", true, func(w *world) {
			w.ticket(1, api.TypeTask, api.ColumnReview, 1, stage(api.ReviewStageAwaitingMerge))
		}},
		{"review, export failed", true, func(w *world) {
			w.ticket(1, api.TypeTask, api.ColumnReview, 1, stage(api.ReviewStageExporting))
			w.in.exportOver = map[int64]map[string]bool{1: {"p": true}}
		}},
		{"done", false, func(w *world) {
			w.ticket(1, api.TypeTask, api.ColumnDone, 1, func(t *board.EngineTicket) { t.Resolution = api.ResolutionCompleted })
		}},
		{"done, cancelled", false, func(w *world) {
			w.ticket(1, api.TypeTask, api.ColumnDone, 1, func(t *board.EngineTicket) { t.Resolution = api.ResolutionCancelled })
		}},
		{"done, exported", false, func(w *world) {
			w.ticket(1, api.TypeTask, api.ColumnDone, 1, func(t *board.EngineTicket) { t.Resolution = api.ResolutionExported })
		}},
	} {
		t.Run(row.name, func(t *testing.T) {
			w := newWorld(1)
			row.setup(w)
			w.ticket(2, api.TypeTask, api.ColumnReady, 5)
			w.session(api.RoleCEO, 0, api.SessionRunning) // the CEO never counts
			acts, st, blocked := w.plan(t)
			started := slices.ContainsFunc(starts(acts), func(a action) bool { return a.ticket == 2 && a.gated })
			if started == row.holds || st.Tasks.InWork != 1 || st.Stories.InWork != 0 {
				t.Fatalf("holds a slot = %v: starts = %v, state = %+v, blocked = %v", row.holds, starts(acts), st, blocked)
			}
			if row.holds && blocked[2] != api.StartBlockedSlot {
				t.Fatalf("blocked = %v", blocked)
			}
		})
	}
}

// omo ai wait changes no limit: a parked working agent, a parked reviewer
// and a parked export job leave the count where the board puts it.
func TestPlanWaitFreesNothing(t *testing.T) {
	w := newWorld(2)
	w.ticket(1, api.TypeTask, api.ColumnInProgress, 1)
	dev := w.session(api.RoleDeveloper, 1, api.SessionRunning)
	w.ticket(2, api.TypeTask, api.ColumnReview, 1, stage(api.ReviewStageAgent))
	rev := w.session(api.RoleReviewer, 2, api.SessionRunning, sessRound(1))
	w.ticket(3, api.TypeTask, api.ColumnReady, 1)
	_, running, _ := w.plan(t)
	for i := range w.snap.Sessions {
		w.snap.Sessions[i].State = api.SessionWaiting
	}
	acts, waiting, blocked := w.plan(t)
	if running != waiting || waiting.Tasks.InWork != 2 || len(starts(acts)) != 0 || blocked[3] != api.StartBlockedSlot {
		t.Fatalf("running %+v, waiting %+v, actions %v, blocked %v (sessions %s %s)", running, waiting, acts, blocked, dev, rev)
	}
}

// A story holds a story slot by the same rule; a task of a story counts
// against the task limit; the two limits do not hold each other back.
func TestPlanTaskAndStoryLimits(t *testing.T) {
	w := newWorld(0)
	w.in.limits = api.LimitSettings{Tasks: 2, Stories: 1}
	w.ticket(1, api.TypeStory, api.ColumnReview, 1, stage(api.ReviewStageHuman))
	w.session(api.RolePM, 1, api.SessionWaiting)
	w.ticket(2, api.TypeStory, api.ColumnReady, 1)
	w.ticket(3, api.TypeTask, api.ColumnReady, 1)
	w.ticket(4, api.TypeTask, api.ColumnReady, 2)
	w.ticket(5, api.TypeTask, api.ColumnReady, 3)
	acts, st, blocked := w.plan(t)
	if ss := starts(acts); len(ss) != 2 || ss[0].ticket != 3 || ss[1].ticket != 4 {
		t.Fatalf("starts = %v", ss)
	}
	if blocked[2] != api.StartBlockedSlot || blocked[5] != api.StartBlockedSlot {
		t.Fatalf("blocked = %v", blocked)
	}
	if st.Tasks != (api.SlotLoad{InWork: 2, Limit: 2}) || st.Stories != (api.SlotLoad{InWork: 1, Limit: 1}) {
		t.Fatalf("state = %+v", st)
	}

	// The story in review is done: the next story starts; the task limit
	// is still full.
	w.snap.Tickets[0].Column, w.snap.Tickets[0].Resolution = api.ColumnDone, api.ResolutionCompleted
	w.snap.Sessions = nil
	w.snap.Tickets[2].Column, w.snap.Tickets[3].Column = api.ColumnInProgress, api.ColumnInProgress
	w.session(api.RoleDeveloper, 3, api.SessionRunning)
	w.session(api.RoleDeveloper, 4, api.SessionWaiting)
	acts, st, blocked = w.plan(t)
	if ss := starts(acts); len(ss) != 1 || ss[0].ticket != 2 || ss[0].role != api.RolePM {
		t.Fatalf("starts = %v", ss)
	}
	if blocked[5] != api.StartBlockedSlot || st.Tasks.InWork != 2 || st.Stories.InWork != 1 {
		t.Fatalf("blocked = %v, state = %+v", blocked, st)
	}

	// Story 2 in progress: its tasks count against the task limit like any
	// other task, and go first among new tasks once a task is done.
	w.snap.Tickets[1].Column = api.ColumnInProgress
	w.session(api.RolePM, 2, api.SessionWaiting)
	w.ticket(6, api.TypeTask, api.ColumnReady, 9, inStory(2))
	w.snap.Tickets[3].Column, w.snap.Tickets[3].Resolution = api.ColumnDone, api.ResolutionCompleted
	w.snap.Sessions = slices.DeleteFunc(w.snap.Sessions, func(s board.EngineSession) bool { return s.Ticket == 4 })
	acts, st, blocked = w.plan(t)
	if ss := starts(acts); len(ss) != 1 || ss[0].ticket != 6 {
		t.Fatalf("starts = %v", ss)
	}
	if blocked[5] != api.StartBlockedSlot || st.Tasks.InWork != 2 || st.Stories.InWork != 1 {
		t.Fatalf("blocked = %v, state = %+v", blocked, st)
	}
}

// The story's own max_parallel narrows the task limit; whichever is reached
// first holds a task of the story back, and a task held back by its story
// does not hold back a standalone task.
func TestPlanPerStoryLimitWithTaskLimit(t *testing.T) {
	w := newWorld(0)
	w.in.limits = api.LimitSettings{Tasks: 3, Stories: 1}
	w.ticket(1, api.TypeStory, api.ColumnInProgress, 1, func(t *board.EngineTicket) { t.MaxParallel = 1 })
	w.session(api.RolePM, 1, api.SessionWaiting)
	w.ticket(2, api.TypeTask, api.ColumnReview, 1, inStory(1), stage(api.ReviewStageHuman))
	w.session(api.RoleDeveloper, 2, api.SessionWaiting)
	w.ticket(3, api.TypeTask, api.ColumnReady, 1, inStory(1))
	w.ticket(4, api.TypeTask, api.ColumnReady, 2)
	w.ticket(5, api.TypeTask, api.ColumnReady, 3)
	w.ticket(6, api.TypeTask, api.ColumnReady, 4)
	acts, st, blocked := w.plan(t)
	if got := gatedTickets(acts); !slices.Equal(got, []int64{4, 5}) || blocked[3] != api.StartBlockedSlot || blocked[6] != api.StartBlockedSlot {
		t.Fatalf("gated = %v, blocked = %v", got, blocked)
	}
	if st.Tasks.InWork != 3 {
		t.Fatalf("state = %+v", st)
	}
	// Without the story's own limit the global one decides, tasks of the
	// story first.
	w.snap.Tickets[0].MaxParallel = 0
	acts, _, blocked = w.plan(t)
	if got := gatedTickets(acts); !slices.Equal(got, []int64{3, 4}) || blocked[5] != api.StartBlockedSlot {
		t.Fatalf("gated = %v, blocked = %v", got, blocked)
	}
}

func gatedTickets(acts []action) []int64 {
	var out []int64
	for _, a := range starts(acts) {
		if a.gated {
			out = append(out, a.ticket)
		}
	}
	return out
}

// A ticket in work keeps its agents going above a lowered limit; only new
// work waits until the count is below the limit again.
func TestPlanLoweredLimitEndsNothing(t *testing.T) {
	w := newWorld(1)
	for n := int64(1); n <= 3; n++ {
		w.ticket(n, api.TypeTask, api.ColumnInProgress, float64(n))
	}
	w.session(api.RoleDeveloper, 1, api.SessionRunning)
	w.session(api.RoleDeveloper, 2, api.SessionWaiting)
	w.ticket(4, api.TypeTask, api.ColumnReady, 1)
	acts, st, blocked := w.plan(t)
	ss := starts(acts)
	if len(ofKind(acts, actEnd)) != 0 || len(ss) != 1 || ss[0].ticket != 3 || ss[0].gated {
		t.Fatalf("actions = %v", acts)
	}
	if st.Tasks != (api.SlotLoad{InWork: 3, Limit: 1}) || blocked[4] != api.StartBlockedSlot {
		t.Fatalf("state = %+v, blocked = %v", st, blocked)
	}
}

// A move back to Ready For Dev or Planning gives the slot back; a story's
// stop moves its tasks back with it, freeing theirs too.
func TestPlanMoveBackFreesTheSlot(t *testing.T) {
	w := newWorld(1)
	w.ticket(1, api.TypeTask, api.ColumnReady, 9) // stopped: back from In Progress
	w.ticket(2, api.TypeStory, api.ColumnPlanning, 9)
	w.ticket(5, api.TypeTask, api.ColumnReady, 9, inStory(2))
	w.ticket(3, api.TypeTask, api.ColumnReady, 1)
	w.ticket(4, api.TypeStory, api.ColumnReady, 1)
	acts, st, _ := w.plan(t)
	if got := gatedTickets(acts); !slices.Equal(got, []int64{3, 4}) {
		t.Fatalf("gated = %v", got)
	}
	if st.Tasks.InWork != 1 || st.Stories.InWork != 1 {
		t.Fatalf("state = %+v", st)
	}
}
