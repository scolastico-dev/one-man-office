package board

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

// ticketRow is one row of the tickets table plus the name of the creating
// session.
type ticketRow struct {
	ID                 int64
	Type               api.TicketType
	Title              string
	Col                api.Column
	Resolution         api.Resolution
	Rank               float64
	StoryID            int64 // 0 = no story
	Supervisor         api.Role
	CreatedByRole      api.Role
	CreatedBySession   string
	CreatedByName      string
	DescriptionVersion int
	Model              string
	Review             api.Review       // "" = inherit
	WhenApproved       api.WhenApproved // "" = inherit
	TaskRelease        api.TaskRelease  // "" = inherit
	MaxParallel        *int
	ReviewStage        api.ReviewStage
	ReviewRound        int
	Attention          string
	Busy               bool
	FailedStep         api.FailedStep
	StartBlocked       api.StartBlocked
	ExportURL          string
	ExportMerged       bool
	// Workdir is the working folder omo made for a task without a
	// project, "" before work started and after it was removed.
	Workdir   string
	CreatedAt string
	UpdatedAt string
	ClosedAt  sql.NullString
}

func (r ticketRow) isStory() bool { return r.Type == api.TypeStory }

// inStory reports whether the ticket is a task that belongs to a story.
func (r ticketRow) inStory() bool { return r.Type == api.TypeTask && r.StoryID != 0 }

// standalone reports whether the ticket is a task without a story.
func (r ticketRow) standalone() bool { return r.Type == api.TypeTask && r.StoryID == 0 }

const ticketSelect = `SELECT t.id, t.type, t.title, t.col, t.resolution, t.rank, COALESCE(t.story_id, 0),
	t.supervisor, t.created_by_role, COALESCE(t.created_by_session, ''), COALESCE(cs.name, ''),
	t.description_version, t.model, t.review, t.when_approved, t.task_release, t.max_parallel,
	t.review_stage, t.review_round, t.attention, t.busy, t.failed_step, t.start_blocked,
	t.export_url, t.export_merged, t.workdir, t.created_at, t.updated_at, t.closed_at
	FROM tickets t LEFT JOIN agent_sessions cs ON cs.id = t.created_by_session`

func scanTicket(rows interface{ Scan(...any) error }) (ticketRow, error) {
	var r ticketRow
	var maxParallel sql.NullInt64
	err := rows.Scan(&r.ID, &r.Type, &r.Title, &r.Col, &r.Resolution, &r.Rank, &r.StoryID,
		&r.Supervisor, &r.CreatedByRole, &r.CreatedBySession, &r.CreatedByName,
		&r.DescriptionVersion, &r.Model, &r.Review, &r.WhenApproved, &r.TaskRelease, &maxParallel,
		&r.ReviewStage, &r.ReviewRound, &r.Attention, &r.Busy, &r.FailedStep, &r.StartBlocked,
		&r.ExportURL, &r.ExportMerged, &r.Workdir,
		&r.CreatedAt, &r.UpdatedAt, &r.ClosedAt)
	if maxParallel.Valid {
		v := int(maxParallel.Int64)
		r.MaxParallel = &v
	}
	return r, err
}

// loadTicket reads one ticket; a missing ticket is api not_found.
func loadTicket(q db.Queryer, n int64) (ticketRow, error) {
	r, err := scanTicket(q.QueryRow(ticketSelect+` WHERE t.id = ?`, n))
	if errors.Is(err, sql.ErrNoRows) {
		return r, api.Errorf(api.CodeNotFound, "ticket #%d does not exist", n)
	}
	return r, err
}

// Effective values: the ticket's own setting, else the global default.

func effectiveReview(p Policy, r ticketRow) api.Review {
	if r.Review != "" {
		return r.Review
	}
	switch {
	case r.isStory():
		return p.ReviewStory
	case r.inStory():
		return p.ReviewStoryTask
	}
	return p.ReviewTask
}

// effectiveWhenApproved is always merge for a task inside a story, which is
// merged into the story and never exported, whatever the defaults say (the
// settings allow nothing else for when_approved_defaults.story_task).
func effectiveWhenApproved(p Policy, r ticketRow) api.WhenApproved {
	if r.inStory() {
		return api.WhenApprovedMerge
	}
	if r.WhenApproved != "" {
		return r.WhenApproved
	}
	if r.isStory() {
		return p.WhenApprovedStory
	}
	return p.WhenApprovedTask
}

// effectiveTaskRelease is only meaningful for stories.
func effectiveTaskRelease(p Policy, r ticketRow) api.TaskRelease {
	if r.TaskRelease != "" {
		return r.TaskRelease
	}
	if p.TasksRequireUserRelease {
		return api.TaskReleaseUser
	}
	return api.TaskReleaseAgents
}

// summary returns the summary of one ticket; a missing ticket is api
// not_found.
func (b *Board) summary(q db.Queryer, n int64) (api.TicketSummary, error) {
	ss, err := b.summaries(q, `t.id = ?`, n)
	if err != nil {
		return api.TicketSummary{}, err
	}
	if len(ss) == 0 {
		return api.TicketSummary{}, api.Errorf(api.CodeNotFound, "ticket #%d does not exist", n)
	}
	return ss[0], nil
}

// ticket returns the full ticket including its current description.
func (b *Board) ticket(q db.Queryer, n int64) (api.Ticket, error) {
	s, err := b.summary(q, n)
	if err != nil {
		return api.Ticket{}, err
	}
	t := api.Ticket{TicketSummary: s}
	if s.DescriptionVersion > 0 {
		err := q.QueryRow(`SELECT body FROM description_versions WHERE ticket_id = ? AND number = ?`,
			n, s.DescriptionVersion).Scan(&t.Description)
		if err != nil {
			return api.Ticket{}, fmt.Errorf("read description of #%d: %w", n, err)
		}
	}
	return t, nil
}

// Get returns one ticket the actor may read.
func (b *Board) Get(ctx context.Context, a Actor, n int64) (api.Ticket, error) {
	if err := checkActor(b.env.Policy(), a); err != nil {
		return api.Ticket{}, err
	}
	var t api.Ticket
	err := b.read(ctx, func(tx *sql.Tx) error {
		if err := checkRead(tx, a, n); err != nil {
			return err
		}
		var err error
		t, err = b.ticket(tx, n)
		return err
	})
	return forActor(a, t, err)
}

// idsIn is the SQL fragment that matches a column against the JSON array of
// ids bound to its parameter.
const idsIn = ` IN (SELECT value FROM json_each(?))`

// summaries returns the summaries of the tickets matching where (a condition
// on the tickets table aliased t, optionally followed by ORDER BY and LIMIT),
// in the order the query returns them. It runs a fixed number of queries
// regardless of how many tickets match.
func (b *Board) summaries(q db.Queryer, where string, args ...any) ([]api.TicketSummary, error) {
	rows, err := q.Query(ticketSelect+` WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	var trs []ticketRow
	for rows.Next() {
		r, err := scanTicket(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		trs = append(trs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]api.TicketSummary, 0, len(trs))
	if len(trs) == 0 {
		return out, nil
	}

	p := b.env.Policy()
	byID := make(map[int64]*api.TicketSummary, len(trs))
	ids := make([]int64, len(trs))
	for i, r := range trs {
		s, err := baseSummary(p, r)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
		ids[i] = r.ID
	}
	for i := range out {
		byID[int64(out[i].Number)] = &out[i]
	}
	rawIDs, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	idJSON := string(rawIDs)

	// Projects in their stored order.
	if err := each(q, `SELECT ticket_id, project_id, base_branch, branch, worktree, export_url, export_state, export_error,
		start_commit, end_commit FROM ticket_projects
		WHERE ticket_id`+idsIn+` ORDER BY ticket_id, position`, idJSON, func(rows *sql.Rows) error {
		var id int64
		var tp api.TicketProject
		if err := rows.Scan(&id, &tp.Project, &tp.BaseBranch, &tp.Branch, &tp.Worktree, &tp.ExportURL, &tp.ExportState, &tp.ExportError,
			&tp.StartCommit, &tp.EndCommit); err != nil {
			return err
		}
		tp.ExportState = exportStateOf(byID[id], tp)
		if s := byID[id]; s.Column != api.ColumnDone || s.Resolution == api.ResolutionCancelled {
			// Recorded on the way to Done; before that, and for a
			// cancelled ticket, they describe no diff of the ticket.
			tp.StartCommit, tp.EndCommit = "", ""
		}
		byID[id].Projects = append(byID[id].Projects, tp)
		return nil
	}); err != nil {
		return nil, err
	}

	// Dependencies and what still blocks the ticket.
	if err := each(q, `SELECT d.ticket_id, d.depends_on_id, o.resolution, o.export_merged
		FROM dependencies d JOIN tickets o ON o.id = d.depends_on_id
		WHERE d.ticket_id`+idsIn+` ORDER BY d.ticket_id, d.depends_on_id`, idJSON, func(rows *sql.Rows) error {
		var id, dep int64
		var res api.Resolution
		var merged bool
		if err := rows.Scan(&id, &dep, &res, &merged); err != nil {
			return err
		}
		s := byID[id]
		s.DependsOn = append(s.DependsOn, int(dep))
		if reason, waiting := waitingReason(res, merged); waiting {
			s.WaitingFor = append(s.WaitingFor, api.Waiting{Ticket: int(dep), Reason: reason})
		}
		return nil
	}); err != nil {
		return nil, err
	}

	// Tickets that depend on this one.
	if err := each(q, `SELECT depends_on_id, ticket_id FROM dependencies
		WHERE depends_on_id`+idsIn+` ORDER BY depends_on_id, ticket_id`, idJSON, func(rows *sql.Rows) error {
		var id, dependent int64
		if err := rows.Scan(&id, &dependent); err != nil {
			return err
		}
		byID[id].Blocks = append(byID[id].Blocks, int(dependent))
		return nil
	}); err != nil {
		return nil, err
	}

	// Live agent sessions.
	if err := each(q, `SELECT ticket_id, id, role, name, state, project_id, purpose FROM agent_sessions
		WHERE state <> 'ended' AND ticket_id`+idsIn+` ORDER BY started_at, id`, idJSON, func(rows *sql.Rows) error {
		var id int64
		var ref api.AgentRef
		if err := rows.Scan(&id, &ref.Session, &ref.Role, &ref.Name, &ref.State, &ref.Project, &ref.Purpose); err != nil {
			return err
		}
		byID[id].Agents = append(byID[id].Agents, ref)
		return nil
	}); err != nil {
		return nil, err
	}

	// Open questions.
	if err := each(q, `SELECT ticket_id, COUNT(*), COALESCE(SUM(question_to = 'user'), 0) FROM comments
		WHERE question_to <> '' AND answered_by IS NULL AND ticket_id`+idsIn+` GROUP BY ticket_id`, idJSON, func(rows *sql.Rows) error {
		var id int64
		var open, forUser int
		if err := rows.Scan(&id, &open, &forUser); err != nil {
			return err
		}
		byID[id].OpenQuestions = open
		byID[id].QuestionsForUser = forUser
		return nil
	}); err != nil {
		return nil, err
	}

	// Story progress: total excludes cancelled tasks, done counts completed
	// or exported ones. A story also shows as busy while omo holds a claim
	// on one of its tasks, which refuses the same user actions on the story
	// as a claim on the story itself (see checkNotBusy). The engine reads the
	// story's own claim from its row, never from the summary.
	if err := each(q, `SELECT story_id, COALESCE(SUM(resolution <> 'cancelled'), 0),
		COALESCE(SUM(resolution IN ('completed', 'exported')), 0), COALESCE(MAX(busy), 0) FROM tickets
		WHERE story_id`+idsIn+` GROUP BY story_id`, idJSON, func(rows *sql.Rows) error {
		var id int64
		var total, done int
		var busyTask bool
		if err := rows.Scan(&id, &total, &done, &busyTask); err != nil {
			return err
		}
		if pr := byID[id].Progress; pr != nil {
			pr.Total, pr.Done = total, done
		}
		if busyTask {
			byID[id].Busy = true
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// waitingReason reports whether a dependency on a ticket with this
// resolution still blocks, and why. A dependency is satisfied when its target
// is completed, or exported with the export merged.
func waitingReason(res api.Resolution, exportMerged bool) (api.WaitingReason, bool) {
	switch res {
	case api.ResolutionCompleted:
		return "", false
	case api.ResolutionExported:
		if exportMerged {
			return "", false
		}
		return api.WaitingExportNotMerged, true
	case api.ResolutionCancelled:
		return api.WaitingCancelled, true
	default:
		return api.WaitingNotDone, true
	}
}

// baseSummary converts a row into a summary without the values that come
// from other tables. List fields are empty, never nil.
func baseSummary(p Policy, r ticketRow) (api.TicketSummary, error) {
	s := api.TicketSummary{
		Number:                int(r.ID),
		Type:                  r.Type,
		Title:                 r.Title,
		Column:                r.Col,
		Resolution:            r.Resolution,
		Rank:                  r.Rank,
		Projects:              []api.TicketProject{},
		Supervisor:            r.Supervisor,
		CreatedBy:             api.Actor{Role: r.CreatedByRole, Session: r.CreatedBySession, Name: r.CreatedByName},
		Model:                 r.Model,
		EffectiveReview:       effectiveReview(p, r),
		EffectiveWhenApproved: effectiveWhenApproved(p, r),
		MaxParallel:           r.MaxParallel,
		ReviewStage:           r.ReviewStage,
		ReviewRound:           r.ReviewRound,
		Attention:             r.Attention,
		Busy:                  r.Busy,
		FailedStep:            r.FailedStep,
		StartBlocked:          r.StartBlocked,
		ExportURL:             r.ExportURL,
		ExportMerged:          r.ExportMerged,
		Workdir:               r.Workdir,
		DependsOn:             []int{},
		Blocks:                []int{},
		WaitingFor:            []api.Waiting{},
		Agents:                []api.AgentRef{},
		DescriptionVersion:    r.DescriptionVersion,
	}
	if r.StoryID != 0 {
		v := int(r.StoryID)
		s.Story = &v
	}
	if r.Review != "" {
		v := r.Review
		s.Review = &v
	}
	if r.WhenApproved != "" {
		v := r.WhenApproved
		s.WhenApproved = &v
	}
	if r.isStory() {
		if r.TaskRelease != "" {
			v := r.TaskRelease
			s.TaskRelease = &v
		}
		eff := effectiveTaskRelease(p, r)
		s.EffectiveTaskRelease = &eff
		s.Progress = &api.Progress{}
	}
	var err error
	if s.CreatedAt, err = parseTime(r.CreatedAt); err != nil {
		return s, err
	}
	if s.UpdatedAt, err = parseTime(r.UpdatedAt); err != nil {
		return s, err
	}
	if s.ClosedAt, err = parseNullTime(r.ClosedAt); err != nil {
		return s, err
	}
	return s, nil
}

// each runs query with one argument and calls fn for every row.
func each(q db.Queryer, query string, arg any, fn func(rows *sql.Rows) error) error {
	rows, err := q.Query(query, arg)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// exportStateOf is where the export of project tp of ticket t stands: a
// pull request is exported; a failure or a left-out project is what the
// engine recorded (stored); a project of a ticket that is being exported
// and has neither is still exporting; anything else has nothing to say.
func exportStateOf(t *api.TicketSummary, tp api.TicketProject) api.ExportState {
	switch {
	case tp.ExportURL != "":
		return api.ExportExported
	case tp.ExportState != api.ExportNone:
		return tp.ExportState
	case t.Column == api.ColumnReview && t.ReviewStage == api.ReviewStageExporting:
		return api.ExportExporting
	}
	return api.ExportNone
}
