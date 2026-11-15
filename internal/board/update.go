package board

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

// editRule names who may edit a ticket's title, description, projects,
// story and base branches.
const editRule = "the user edits every ticket, the CEO tickets it created (descriptions also of tickets it supervises), a product manager the tasks of its story"

// checkActor rejects a CEO actor while the CEO is turned off.
func checkActor(p Policy, a Actor) error {
	if a.Role == api.RoleCEO && !p.CEOEnabled {
		return api.Errorf(api.CodeForbidden, "the CEO is turned off")
	}
	return nil
}

// mayEdit reports whether a may change the title, projects, story and base
// branches of t.
func mayEdit(a Actor, t ticketRow) bool {
	switch a.Role {
	case api.RoleUser:
		return true
	case api.RoleCEO:
		return t.CreatedByRole == api.RoleCEO
	case api.RolePM:
		return t.inStory() && t.StoryID == a.Ticket
	}
	return false
}

// maySetModel reports whether a may change the model of t: the user always,
// the CEO and a pm on tickets they created (a pm within its story).
func maySetModel(a Actor, t ticketRow) bool {
	switch a.Role {
	case api.RoleUser:
		return true
	case api.RoleCEO:
		return t.CreatedByRole == api.RoleCEO
	case api.RolePM:
		return t.CreatedByRole == api.RolePM && t.inStory() && t.StoryID == a.Ticket
	}
	return false
}

// boundProject is one row of ticket_projects.
type boundProject struct {
	id, base, branch, worktree string
}

func loadProjects(q db.Queryer, n int64) ([]boundProject, error) {
	var out []boundProject
	err := each(q, `SELECT project_id, base_branch, branch, worktree FROM ticket_projects
		WHERE ticket_id = ? ORDER BY position`, n, func(rows *sql.Rows) error {
		var p boundProject
		if err := rows.Scan(&p.id, &p.base, &p.branch, &p.worktree); err != nil {
			return err
		}
		out = append(out, p)
		return nil
	})
	return out, err
}

func projectIDsOf(ps []boundProject) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.id
	}
	return out
}

// Update applies a PATCH to a ticket and emits ticket.updated with the names
// of the fields that changed. Fields sent with their current value change
// nothing; a request that changes nothing emits nothing.
func (b *Board) Update(ctx context.Context, a Actor, n int64, req api.UpdateTicket) (api.Ticket, error) {
	if req.Projects.Set || req.Model.Set {
		// From the model and project checks to the commit: a settings
		// removal cannot slip in between (see Options.RefsLock).
		b.refs.Lock()
		defer b.refs.Unlock()
	}
	p := b.env.Policy()
	if err := checkActor(p, a); err != nil {
		return api.Ticket{}, err
	}
	var out api.Ticket
	err := b.write(ctx, func(tx *sql.Tx) error {
		if err := checkRead(tx, a, n); err != nil {
			return err
		}
		old, err := loadTicket(tx, n)
		if err != nil {
			return err
		}
		if old.Col == api.ColumnDone {
			return api.Errorf(api.CodeInvalidTransition, "#%d is done; done tickets are read-only", n)
		}
		if err := checkUpdatePermissions(a, old, req); err != nil {
			return err
		}
		u := &update{b: b, tx: tx, p: p, a: a, old: old, new: old, req: req}
		if err := u.apply(); err != nil {
			return err
		}
		if len(u.fields) > 0 {
			if err := u.save(); err != nil {
				return err
			}
			if err := b.emitUpdated(tx, a, n, u.fields...); err != nil {
				return err
			}
			if old.StoryID != u.new.StoryID {
				// Both stories' progress changed.
				if err := b.touch(tx, a, old.StoryID, u.new.StoryID); err != nil {
					return err
				}
				if err := b.tellOldPM(tx, a, old, u.new); err != nil {
					return err
				}
			}
			if err := b.tellPM(tx, a, u.new, fmt.Sprintf("Task #%d edited by the user: %s.", n, strings.Join(u.fields, ", "))); err != nil {
				return err
			}
		}
		out, err = b.ticket(tx, n)
		return err
	})
	return forActor(a, out, err)
}

// checkUpdatePermissions applies the who-may-edit table to every field the
// request carries.
func checkUpdatePermissions(a Actor, t ticketRow, req api.UpdateTicket) error {
	if a.Role != api.RoleUser && (req.Supervisor.Set || req.Review.Set || req.WhenApproved.Set ||
		req.TaskRelease.Set || req.MaxParallel.Set) {
		return api.Errorf(api.CodeForbidden, "only the user changes supervisor, review, when_approved, task_release and max_parallel")
	}
	if (req.Title.Set || req.Projects.Set || req.Story.Set || req.BaseBranches.Set) && !mayEdit(a, t) {
		return api.Errorf(api.CodeForbidden, "a %s cannot edit #%d: %s", a.Role, t.ID, editRule)
	}
	if req.Story.Set {
		switch a.Role {
		case api.RolePM:
			if req.Story.Null || int64(req.Story.Value) != a.Ticket {
				return api.Errorf(api.CodeForbidden, "a product manager keeps its tasks in its own story #%d", a.Ticket)
			}
		case api.RoleCEO:
			if !req.Story.Null {
				return api.Errorf(api.CodeForbidden, "the CEO does not place tasks inside stories")
			}
		}
	}
	if req.Model.Set && !maySetModel(a, t) {
		return api.Errorf(api.CodeForbidden,
			"a %s cannot set the model of #%d: the user sets every model, the CEO and a product manager only on tickets they created", a.Role, t.ID)
	}
	return nil
}

// update computes the new state of one ticket.
type update struct {
	b      *Board
	tx     *sql.Tx
	p      Policy
	a      Actor
	req    api.UpdateTicket
	old    ticketRow
	new    ticketRow
	oldPs  []boundProject
	newPs  []boundProject
	fields []string
}

func (u *update) changed(field string) { u.fields = append(u.fields, field) }

func (u *update) apply() error {
	if err := u.title(); err != nil {
		return err
	}
	if err := u.placement(); err != nil {
		return err
	}
	if err := u.supervisor(); err != nil {
		return err
	}
	if err := u.model(); err != nil {
		return err
	}
	return u.settings()
}

func (u *update) title() error {
	if !u.req.Title.Set {
		return nil
	}
	if u.req.Title.Null {
		return api.Errorf(api.CodeValidation, "the title must have 1 to %d characters", maxTitle)
	}
	title, err := checkTitle(u.req.Title.Value)
	if err != nil {
		return err
	}
	if title != u.old.Title {
		u.new.Title = title
		u.changed("title")
	}
	return nil
}

// placement handles story, projects and base branches, which are locked
// while a branch of the ticket exists.
func (u *update) placement() error {
	var err error
	if u.oldPs, err = loadProjects(u.tx, u.old.ID); err != nil {
		return err
	}
	if err := u.story(); err != nil {
		return err
	}
	if err := u.projects(); err != nil {
		return err
	}
	if err := u.baseBranches(); err != nil {
		return err
	}

	storyChanged := u.new.StoryID != u.old.StoryID
	idsChanged := !slices.Equal(projectIDsOf(u.oldPs), projectIDsOf(u.newPs))
	basesChanged := false
	for _, np := range u.newPs {
		for _, op := range u.oldPs {
			if op.id == np.id && op.base != np.base {
				basesChanged = true
			}
		}
	}
	if storyChanged || idsChanged || basesChanged {
		// While omo holds a claim it may be cutting the ticket's branches
		// (or, for a story, the branch of a task it starts) from exactly
		// these values, before any branch is recorded.
		if err := checkNotBusy(u.tx, u.old); err != nil {
			return err
		}
		for _, op := range u.oldPs {
			if op.branch != "" {
				return api.Errorf(api.CodeFieldLocked,
					"project, story and base branch of #%d are locked while its branch %q exists in project %q",
					u.old.ID, op.branch, op.id)
			}
		}
	}

	if (storyChanged || idsChanged) && u.old.Col != api.ColumnPlanning && !u.new.inStory() {
		for _, np := range u.newPs {
			if np.base == "" {
				return api.Errorf(api.CodeValidation,
					"#%d is in %s and needs a base branch for project %q (the folder is on a detached HEAD); set one in base_branches",
					u.old.ID, u.old.Col, np.id)
			}
		}
	}
	if u.new.inStory() && (storyChanged || idsChanged) {
		if err := u.checkProjectInStory(); err != nil {
			return err
		}
	}
	if u.old.isStory() && idsChanged {
		if err := u.checkDroppedProjects(); err != nil {
			return err
		}
	}
	if storyChanged {
		if err := checkLinkScopes(u.tx, u.new); err != nil {
			return err
		}
		u.changed("story")
	}
	if idsChanged {
		u.changed("projects")
	}
	if basesChanged {
		u.changed("base_branches")
	}
	return nil
}

func (u *update) story() error {
	r := u.req.Story
	if !r.Set {
		return nil
	}
	if u.old.isStory() {
		if r.Null {
			return nil
		}
		return api.Errorf(api.CodeValidation, "a story cannot belong to another story")
	}
	if r.Null {
		u.new.StoryID = 0
		return nil
	}
	s, err := loadTicket(u.tx, int64(r.Value))
	if errCode(err) == api.CodeNotFound || (err == nil && !s.isStory()) {
		return api.Errorf(api.CodeValidation, "#%d is not a story", r.Value)
	}
	if err != nil {
		return err
	}
	if s.ID != u.old.StoryID {
		if err := checkStoryTakesTasks(s); err != nil {
			return err
		}
	}
	u.new.StoryID = s.ID
	return nil
}

// projects builds the new project list. Kept projects keep their rows;
// added ones must be available. Base branches are set for stories and
// standalone tasks (the current branch for a project that gets one) and
// empty for tasks in a story.
func (u *update) projects() error {
	ids := projectIDsOf(u.oldPs)
	if u.req.Projects.Set {
		ids = u.req.Projects.Value
		if ids == nil {
			ids = []string{}
		}
	}
	if u.old.isStory() {
		if len(ids) == 0 && u.old.Col != api.ColumnPlanning {
			return api.Errorf(api.CodeValidation, "a story outside Planning needs at least one project")
		}
	} else if len(ids) != 1 {
		return api.Errorf(api.CodeValidation, "a task has exactly one project, got %d", len(ids))
	}
	placementChanged := u.old.inStory() != u.new.inStory()
	u.newPs = make([]boundProject, 0, len(ids))
	for i, id := range ids {
		if slices.Contains(ids[:i], id) {
			return api.Errorf(api.CodeValidation, "project %q is listed twice", id)
		}
		np := boundProject{id: id}
		kept := false
		for _, op := range u.oldPs {
			if op.id == id {
				np, kept = op, true
			}
		}
		if !kept {
			if _, err := u.b.availableProject(id); err != nil {
				return err
			}
		}
		switch {
		case u.new.inStory():
			np.base = ""
		case !kept || placementChanged:
			info, err := u.b.availableProject(id)
			if err != nil {
				return err
			}
			branch, err := u.b.env.CurrentBranch(info.Path)
			if err != nil {
				return api.Errorf(api.CodeProjectUnavailable, "cannot read the current branch of project %q: %v", id, err)
			}
			np.base = branch
		}
		u.newPs = append(u.newPs, np)
	}
	return nil
}

func (u *update) baseBranches() error {
	r := u.req.BaseBranches
	if !r.Set {
		return nil
	}
	if r.Null {
		return api.Errorf(api.CodeValidation, "base_branches cannot be cleared; set a branch per project")
	}
	if u.new.inStory() {
		return api.Errorf(api.CodeValidation, "a task inside a story builds on the story's branch and has no base branch")
	}
	for _, id := range slices.Sorted(maps.Keys(r.Value)) {
		branch := strings.TrimSpace(r.Value[id])
		i := slices.IndexFunc(u.newPs, func(p boundProject) bool { return p.id == id })
		if i < 0 {
			return api.Errorf(api.CodeValidation, "project %q is not a project of #%d", id, u.old.ID)
		}
		if branch == "" {
			return api.Errorf(api.CodeValidation, "the base branch of project %q cannot be empty", id)
		}
		u.newPs[i].base = branch
	}
	return nil
}

// checkProjectInStory requires the task's project to be one of its story's
// projects.
func (u *update) checkProjectInStory() error {
	var count int
	if err := u.tx.QueryRow(`SELECT COUNT(*) FROM ticket_projects WHERE ticket_id = ? AND project_id = ?`,
		u.new.StoryID, u.newPs[0].id).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return api.Errorf(api.CodeValidation, "project %q is not a project of story #%d", u.newPs[0].id, u.new.StoryID)
	}
	return nil
}

// checkDroppedProjects rejects removing a project from a story while one of
// its tasks that is not cancelled uses it.
func (u *update) checkDroppedProjects() error {
	for _, op := range u.oldPs {
		if slices.ContainsFunc(u.newPs, func(p boundProject) bool { return p.id == op.id }) {
			continue
		}
		var task int64
		err := u.tx.QueryRow(`SELECT t.id FROM tickets t JOIN ticket_projects tp ON tp.ticket_id = t.id
			WHERE t.story_id = ? AND t.resolution <> 'cancelled' AND tp.project_id = ? ORDER BY t.id LIMIT 1`,
			u.old.ID, op.id).Scan(&task)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		return api.Errorf(api.CodeValidation, "story #%d cannot drop project %q: task #%d uses it", u.old.ID, op.id, task)
	}
	return nil
}

// supervisor resets the supervisor to the default when a task changes its
// story, then applies a requested value (null = the default).
func (u *update) supervisor() error {
	def := defaultSupervisor(u.p, u.new.inStory(), u.old.CreatedByRole)
	if u.new.StoryID != u.old.StoryID {
		u.new.Supervisor = def
	}
	if r := u.req.Supervisor; r.Set {
		if r.Null {
			u.new.Supervisor = def
		} else {
			if err := checkSupervisor(u.p, r.Value, u.new.inStory()); err != nil {
				return err
			}
			u.new.Supervisor = r.Value
		}
	}
	if u.new.Supervisor != u.old.Supervisor {
		u.changed("supervisor")
	}
	return nil
}

func (u *update) model() error {
	r := u.req.Model
	if !r.Set {
		return nil
	}
	model := r.Value
	if r.Null {
		model = ""
	}
	if model != "" {
		if err := u.b.checkModel(u.a, model); err != nil {
			return err
		}
	}
	if model != u.old.Model {
		u.new.Model = model
		u.changed("model")
	}
	return nil
}

// settings handles review, when_approved, task_release and max_parallel;
// null means inherit the global default.
func (u *update) settings() error {
	if r := u.req.Review; r.Set {
		u.new.Review = r.Value
		if r.Null {
			u.new.Review = ""
		}
		if err := checkReview(u.new.Review); err != nil {
			return err
		}
	}
	if r := u.req.WhenApproved; r.Set {
		u.new.WhenApproved = r.Value
		if r.Null {
			u.new.WhenApproved = ""
		}
	}
	// Also checked when only the story changed: an export setting does not
	// survive a move into a story.
	if err := checkWhenApproved(u.new.WhenApproved, u.new.inStory()); err != nil {
		return err
	}
	if r := u.req.TaskRelease; r.Set {
		u.new.TaskRelease = r.Value
		if r.Null {
			u.new.TaskRelease = ""
		}
		if err := checkTaskRelease(u.new.TaskRelease); err != nil {
			return err
		}
	}
	if r := u.req.MaxParallel; r.Set {
		u.new.MaxParallel = nil
		if !r.Null {
			if r.Value < 1 {
				return api.Errorf(api.CodeValidation, "max_parallel must be at least 1")
			}
			v := r.Value
			u.new.MaxParallel = &v
		}
	}
	if !u.old.isStory() && (u.new.TaskRelease != "" || u.new.MaxParallel != nil) {
		return api.Errorf(api.CodeValidation, "task_release and max_parallel apply to stories only")
	}
	if u.new.Review != u.old.Review {
		u.changed("review")
	}
	if u.new.WhenApproved != u.old.WhenApproved {
		u.changed("when_approved")
	}
	if u.new.TaskRelease != u.old.TaskRelease {
		u.changed("task_release")
	}
	if !equalIntPtr(u.new.MaxParallel, u.old.MaxParallel) {
		u.changed("max_parallel")
	}
	return nil
}

func equalIntPtr(x, y *int) bool {
	if x == nil || y == nil {
		return x == y
	}
	return *x == *y
}

// save writes the new state.
func (u *update) save() error {
	t := u.new
	var story, maxParallel any
	if t.StoryID != 0 {
		story = t.StoryID
	}
	if t.MaxParallel != nil {
		maxParallel = *t.MaxParallel
	}
	if _, err := u.tx.Exec(`UPDATE tickets SET title = ?, story_id = ?, supervisor = ?, model = ?, review = ?,
		when_approved = ?, task_release = ?, max_parallel = ?, updated_at = ? WHERE id = ?`,
		t.Title, story, t.Supervisor, t.Model, t.Review, t.WhenApproved, t.TaskRelease, maxParallel,
		u.b.stamp(), t.ID); err != nil {
		return err
	}
	if !slices.Equal(u.oldPs, u.newPs) {
		if _, err := u.tx.Exec(`DELETE FROM ticket_projects WHERE ticket_id = ?`, t.ID); err != nil {
			return err
		}
		for i, p := range u.newPs {
			if _, err := u.tx.Exec(`INSERT INTO ticket_projects (ticket_id, project_id, position, base_branch, branch, worktree)
				VALUES (?, ?, ?, ?, ?, ?)`, t.ID, p.id, i, p.base, p.branch, p.worktree); err != nil {
				return err
			}
		}
	}
	return nil
}

// ReassignCEO hands everything the CEO held to the user when the CEO is
// turned off: every ticket it supervises and every open question addressed
// to it, including the question's mention. Answered questions and other
// comments keep their mentions. Each affected ticket gets one
// ticket.updated.
func (b *Board) ReassignCEO(ctx context.Context) error {
	return b.write(ctx, func(tx *sql.Tx) error {
		supervised := map[int64]bool{}
		var ids []int64
		rows, err := tx.Query(`SELECT id, supervisor = 'ceo' FROM tickets WHERE supervisor = 'ceo' OR id IN
			(SELECT ticket_id FROM comments WHERE question_to = 'ceo' AND answered_by IS NULL) ORDER BY id`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id int64
			var sup bool
			if err := rows.Scan(&id, &sup); err != nil {
				rows.Close()
				return err
			}
			supervised[id] = sup
			ids = append(ids, id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(ids) == 0 {
			return nil
		}
		if _, err := tx.Exec(`UPDATE tickets SET supervisor = 'user', updated_at = ? WHERE supervisor = 'ceo'`, b.stamp()); err != nil {
			return err
		}
		// The open questions' mention of the CEO becomes one of the user, so
		// the mention rows agree with the addressee. A question that already
		// mentions the user keeps that row and drops the CEO's.
		const openToCEO = `comment_id IN (SELECT id FROM comments WHERE question_to = 'ceo' AND answered_by IS NULL)`
		if _, err := tx.Exec(`UPDATE OR IGNORE comment_mentions SET role = 'user', session_id = NULL
			WHERE role = 'ceo' AND ` + openToCEO); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM comment_mentions WHERE role = 'ceo' AND ` + openToCEO); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE comments SET question_to = 'user' WHERE question_to = 'ceo' AND answered_by IS NULL`); err != nil {
			return err
		}
		byID, err := b.summariesOf(tx, ids)
		if err != nil {
			return err
		}
		for _, id := range ids {
			fields := []string{fieldDerived}
			if supervised[id] {
				fields = []string{"supervisor"}
			}
			if err := emit(tx, api.KindTicketUpdated, id, System, api.TicketUpdatedData{Ticket: byID[id], Fields: fields}); err != nil {
				return err
			}
		}
		return nil
	})
}
