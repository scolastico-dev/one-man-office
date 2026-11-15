package board

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/db"
)

// StoryTemplate is the description a story gets when it is created without
// one.
const StoryTemplate = "## Goal\n\n\n## Do\n\n\n## Don't\n\n\n## Done when\n\n"

// Create creates a story or task in Planning.
func (b *Board) Create(ctx context.Context, a Actor, req api.CreateTicket) (api.Ticket, error) {
	p := b.env.Policy()
	title, err := b.checkCreate(p, a, req)
	if err != nil {
		return api.Ticket{}, err
	}
	projects, err := b.resolveProjects(req)
	if err != nil {
		return api.Ticket{}, err
	}

	var out api.Ticket
	err = b.write(ctx, func(tx *sql.Tx) error {
		var story ticketRow
		if req.Story != nil {
			if story, err = b.checkCreateInStory(tx, a, req); err != nil {
				return err
			}
		}
		supervisor, err := createSupervisor(p, a, req)
		if err != nil {
			return err
		}
		deps, err := createDependencies(tx, req.DependsOn)
		if err != nil {
			return err
		}

		now := b.stamp()
		rank, err := endRank(tx, api.ColumnPlanning)
		if err != nil {
			return err
		}
		var storyID, maxParallel any
		if req.Story != nil {
			storyID = story.ID
		}
		if req.MaxParallel != nil {
			maxParallel = *req.MaxParallel
		}
		res, err := tx.Exec(`INSERT INTO tickets
			(type, title, col, rank, story_id, supervisor, created_by_role, created_by_session,
			 model, review, when_approved, task_release, max_parallel, created_at, updated_at)
			VALUES (?, ?, 'planning', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			req.Type, title, rank, storyID, supervisor, a.Role, a.sessionArg(),
			req.Model, req.Review, req.WhenApproved, req.TaskRelease, maxParallel, now, now)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		for i, pr := range projects {
			if _, err := tx.Exec(`INSERT INTO ticket_projects (ticket_id, project_id, position, base_branch)
				VALUES (?, ?, ?, ?)`, id, pr.id, i, pr.baseBranch); err != nil {
				return err
			}
		}

		description := req.Description
		if strings.TrimSpace(description) == "" {
			description = ""
			if req.Type == api.TypeStory {
				description = StoryTemplate
			}
		}
		if description != "" {
			if _, err := tx.Exec(`INSERT INTO description_versions
				(ticket_id, number, body, author_role, author_session, created_at) VALUES (?, 1, ?, ?, ?, ?)`,
				id, description, a.Role, a.sessionArg(), now); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE tickets SET description_version = 1 WHERE id = ?`, id); err != nil {
				return err
			}
		}

		if err := linkNewTicket(tx, id, deps, now); err != nil {
			return err
		}

		if out, err = b.ticket(tx, id); err != nil {
			return err
		}
		if err := emit(tx, api.KindTicketCreated, id, a, api.TicketCreatedData{Ticket: out.TicketSummary}); err != nil {
			return err
		}
		// The story's progress and the dependencies' blocks changed.
		if err := b.touch(tx, a, append([]int64{story.ID}, deps...)...); err != nil {
			return err
		}
		row, err := loadTicket(tx, id)
		if err != nil {
			return err
		}
		return b.tellPM(tx, a, row, fmt.Sprintf("Task #%d added to story #%d by the user.", id, story.ID))
	})
	return forActor(a, out, err)
}

// checkCreate applies the create rules that need no database: who may create
// what, and the shape of the request. It returns the trimmed title.
func (b *Board) checkCreate(p Policy, a Actor, req api.CreateTicket) (string, error) {
	if req.Type != api.TypeStory && req.Type != api.TypeTask {
		return "", api.Errorf(api.CodeValidation, "type must be story or task")
	}
	inStory := req.Story != nil
	switch a.Role {
	case api.RoleUser:
	case api.RoleCEO:
		if !p.CEOEnabled {
			return "", api.Errorf(api.CodeForbidden, "the CEO is turned off")
		}
		if inStory {
			return "", api.Errorf(api.CodeForbidden, "the CEO creates stories and standalone tasks, not tasks inside a story")
		}
	case api.RolePM:
		if req.Type != api.TypeTask || !inStory {
			return "", api.Errorf(api.CodeForbidden, "a product manager creates only tasks inside its own story")
		}
	default:
		return "", api.Errorf(api.CodeForbidden, "a %s cannot create tickets", a.Role)
	}
	if a.isAgent() {
		if req.Supervisor != "" || req.Review != "" || req.WhenApproved != "" || req.TaskRelease != "" || req.MaxParallel != nil {
			return "", api.Errorf(api.CodeForbidden, "only the user sets supervisor, review, when_approved, task_release and max_parallel")
		}
	}

	title := strings.TrimSpace(req.Title)
	if n := utf8.RuneCountInString(title); n < 1 || n > 200 {
		return "", api.Errorf(api.CodeValidation, "the title must have 1 to 200 characters")
	}
	if req.Type == api.TypeStory {
		if inStory {
			return "", api.Errorf(api.CodeValidation, "a story cannot belong to another story")
		}
	} else {
		if len(req.Projects) != 1 {
			return "", api.Errorf(api.CodeValidation, "a task has exactly one project, got %d", len(req.Projects))
		}
		if req.TaskRelease != "" || req.MaxParallel != nil {
			return "", api.Errorf(api.CodeValidation, "task_release and max_parallel apply to stories only")
		}
	}
	if err := checkReview(req.Review); err != nil {
		return "", err
	}
	if err := checkWhenApproved(req.WhenApproved, req.Type == api.TypeTask && inStory); err != nil {
		return "", err
	}
	if err := checkTaskRelease(req.TaskRelease); err != nil {
		return "", err
	}
	if req.MaxParallel != nil && *req.MaxParallel < 1 {
		return "", api.Errorf(api.CodeValidation, "max_parallel must be at least 1")
	}
	if req.Model != "" {
		if err := b.checkModel(a, req.Model); err != nil {
			return "", err
		}
	}
	return title, nil
}

func checkReview(r api.Review) error {
	switch r {
	case "", api.ReviewSkipped, api.ReviewAgent, api.ReviewHuman, api.ReviewAgentThenHuman:
		return nil
	}
	return api.Errorf(api.CodeValidation, "review must be skipped, agent, human or agent_then_human, not %q", r)
}

// checkWhenApproved validates a when_approved value; export is not offered
// for tasks inside a story.
func checkWhenApproved(w api.WhenApproved, taskInStory bool) error {
	switch w {
	case "", api.WhenApprovedMerge:
		return nil
	case api.WhenApprovedExport:
		if taskInStory {
			return api.Errorf(api.CodeValidation, "a task inside a story is merged into the story, it cannot be exported")
		}
		return nil
	}
	return api.Errorf(api.CodeValidation, "when_approved must be merge or export, not %q", w)
}

func checkTaskRelease(r api.TaskRelease) error {
	switch r {
	case "", api.TaskReleaseAgents, api.TaskReleaseUser:
		return nil
	}
	return api.Errorf(api.CodeValidation, "task_release must be agents or user, not %q", r)
}

// checkModel requires an existing model; agents may only pick selectable
// ones.
func (b *Board) checkModel(a Actor, id string) error {
	m, ok := b.env.Model(id)
	if !ok {
		return api.Errorf(api.CodeValidation, "model %q does not exist", id)
	}
	if a.isAgent() && !m.Selectable {
		return api.Errorf(api.CodeForbidden, "model %q is not selectable by agents", id)
	}
	return nil
}

// newProject is a project binding of a ticket about to be created.
type newProject struct {
	id         string
	baseBranch string
}

// resolveProjects checks that every requested project exists and is
// available, and reads the base branch for stories and standalone tasks.
func (b *Board) resolveProjects(req api.CreateTicket) ([]newProject, error) {
	out := make([]newProject, 0, len(req.Projects))
	for i, id := range req.Projects {
		if slices.Contains(req.Projects[:i], id) {
			return nil, api.Errorf(api.CodeValidation, "project %q is listed twice", id)
		}
		info, err := b.availableProject(id)
		if err != nil {
			return nil, err
		}
		np := newProject{id: id}
		if req.Story == nil {
			branch, err := b.env.CurrentBranch(info.Path)
			if err != nil {
				return nil, api.Errorf(api.CodeProjectUnavailable, "cannot read the current branch of project %q: %v", id, err)
			}
			np.baseBranch = branch
		}
		out = append(out, np)
	}
	return out, nil
}

// availableProject returns a project that exists, is enabled and is present
// on disk.
func (b *Board) availableProject(id string) (ProjectInfo, error) {
	info, ok := b.env.Project(id)
	switch {
	case !ok:
		return info, api.Errorf(api.CodeValidation, "project %q does not exist", id)
	case !info.Enabled:
		return info, api.Errorf(api.CodeProjectUnavailable, "project %q is disabled", id)
	case info.Missing:
		return info, api.Errorf(api.CodeProjectUnavailable, "project %q is missing on disk", id)
	}
	return info, nil
}

// checkCreateInStory applies the rules for a new task inside a story: the
// story exists and is open, a pm only adds to its own story, and the task's
// project is one of the story's projects.
func (b *Board) checkCreateInStory(q db.Queryer, a Actor, req api.CreateTicket) (ticketRow, error) {
	n := int64(*req.Story)
	story, err := loadTicket(q, n)
	if errCode(err) == api.CodeNotFound || (err == nil && !story.isStory()) {
		return story, api.Errorf(api.CodeValidation, "#%d is not a story", n)
	}
	if err != nil {
		return story, err
	}
	if a.Role == api.RolePM && a.Ticket != story.ID {
		return story, api.Errorf(api.CodeForbidden, "a product manager creates tasks only in its own story #%d", a.Ticket)
	}
	if err := checkStoryTakesTasks(story); err != nil {
		return story, err
	}
	var count int
	if err := q.QueryRow(`SELECT COUNT(*) FROM ticket_projects WHERE ticket_id = ? AND project_id = ?`,
		n, req.Projects[0]).Scan(&count); err != nil {
		return story, err
	}
	if count == 0 {
		return story, api.Errorf(api.CodeValidation, "project %q is not a project of story #%d", req.Projects[0], n)
	}
	return story, nil
}

// checkStoryTakesTasks accepts new or moved-in tasks only while the story
// is in Planning, Ready For Dev or In Progress. Later the story is handed in
// or closed, and a task added then could never be worked on.
func checkStoryTakesTasks(story ticketRow) error {
	switch story.Col {
	case api.ColumnPlanning, api.ColumnReady, api.ColumnInProgress:
		return nil
	}
	return api.Errorf(api.CodeInvalidTransition,
		"story #%d is in %s and takes no new tasks; tasks join a story in planning, ready or in_progress", story.ID, story.Col)
}

// createSupervisor returns the requested supervisor (user only) or the
// default: pm for a task in a story, otherwise the creator.
func createSupervisor(p Policy, a Actor, req api.CreateTicket) (api.Role, error) {
	taskInStory := req.Type == api.TypeTask && req.Story != nil
	if req.Supervisor == "" {
		return defaultSupervisor(p, taskInStory, a.Role), nil
	}
	return req.Supervisor, checkSupervisor(p, req.Supervisor, taskInStory)
}

// defaultSupervisor returns the supervisor a ticket gets by default: the pm
// for a task in a story, otherwise its creator. A creator that cannot
// supervise there (a pm outside a story, the CEO while it is off) falls back
// to the user.
func defaultSupervisor(p Policy, taskInStory bool, creator api.Role) api.Role {
	switch {
	case taskInStory:
		return api.RolePM
	case creator == api.RoleCEO && p.CEOEnabled:
		return api.RoleCEO
	}
	return api.RoleUser
}

// checkSupervisor validates a supervisor value for a ticket: pm only for a
// task in a story, ceo only while the CEO is enabled.
func checkSupervisor(p Policy, s api.Role, taskInStory bool) error {
	switch s {
	case api.RoleUser:
		return nil
	case api.RolePM:
		if !taskInStory {
			return api.Errorf(api.CodeValidation, "the product manager supervises only tasks inside a story")
		}
		return nil
	case api.RoleCEO:
		if !p.CEOEnabled {
			return api.Errorf(api.CodeValidation, "the CEO is turned off and cannot supervise")
		}
		return nil
	}
	return api.Errorf(api.CodeValidation, "supervisor must be user, ceo or pm, not %q", s)
}

// createDependencies checks that every requested dependency exists and
// returns the distinct ticket numbers.
func createDependencies(q db.Queryer, req []int) ([]int64, error) {
	var out []int64
	for _, n := range req {
		dep := int64(n)
		if slices.Contains(out, dep) {
			continue
		}
		if _, err := loadTicket(q, dep); err != nil {
			if errCode(err) == api.CodeNotFound {
				return nil, api.Errorf(api.CodeValidation, "dependency #%d does not exist", n)
			}
			return nil, err
		}
		out = append(out, dep)
	}
	return out, nil
}
