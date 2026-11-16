package engine

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/prompts"
	"github.com/scolastico-dev/one-man-office/internal/skillsets"
)

// Ready acknowledges the start of the agent session a and returns its role
// prompt, followed by the notifications that arrived while it started. The
// session is running from then on and its ready timeout no longer applies.
// A second call returns the prompt again; a notification is never returned
// twice.
func (e *Engine) Ready(ctx context.Context, a board.Actor) (string, error) {
	switch a.Role {
	case api.RoleCEO, api.RolePM, api.RoleDeveloper, api.RoleReviewer:
	default:
		return "", api.Errorf(api.CodeForbidden, "omo ai ready is for agent sessions")
	}
	if a.Session == "" {
		return "", api.Errorf(api.CodeForbidden, "omo ai ready is for agent sessions")
	}
	// Mark the session ready before taking its queue: a line delivered
	// from now on is typed, one delivered before is in the queue.
	e.markReady(a.Session)
	prompt, err := e.rolePrompt(ctx, a)
	if err != nil {
		return "", err
	}

	nt := e.nt
	nt.mu.Lock()
	mb := nt.box(a.Session)
	lines := mb.takeQueue()
	nt.mu.Unlock()
	e.syncState(ctx, a.Session, mb)
	if !e.hasEntry(a.Session) {
		nt.mu.Lock()
		nt.drop(a.Session)
		nt.mu.Unlock()
	}
	if len(lines) > 0 {
		prompt += "\n\nNotifications that arrived while you started:\n" + strings.Join(lines, "\n")
	}
	return prompt, nil
}

// rolePrompt renders the prompt of the agent session a: its role's prompt
// (an export job's own), filled from its ticket and the user's instructions
// for the role followed by the role's skill set blocks, with the restart
// note when the session continues work an earlier session of the same slot
// started.
func (e *Engine) rolePrompt(ctx context.Context, a board.Actor) (string, error) {
	g := e.st.General()
	name := string(a.Role)
	if a.Purpose == api.PurposeExport {
		name = "export"
	}
	d := prompts.Data{
		Name: a.Name, Role: string(a.Role),
		CEOEnabled: g.CEO.Enabled, CEOMayApprove: g.CEO.MayApproveStories,
		TaskRelease: string(api.TaskReleaseAgents),
	}
	if g.Tasks.RequireUserRelease {
		d.TaskRelease = string(api.TaskReleaseUser)
	}
	if text, _, ok := e.st.Prompt(name + "-instructions"); ok {
		d.Instructions = strings.TrimSpace(text)
	}
	// The skill sets' texts follow the user's own instructions as data, so
	// a customized role prompt gets them too.
	d.Instructions = skillsets.Join(d.Instructions, skillsets.Text(name, e.st.SkillSets(), skillsets.CheckoutIn(e.o.Layout.Skills)))
	if a.Role == api.RoleCEO {
		d.Home, d.ServerLog = e.o.Layout.Root, filepath.Join(e.o.Layout.Run, "omo.log")
		return e.renderPrompt(name, d), nil
	}

	t, err := e.b.Get(ctx, board.User, a.Ticket)
	if err != nil {
		return "", err
	}
	d.Ticket, d.Type, d.Title, d.ReviewRound = int(t.Number), string(t.Type), t.Title, t.ReviewRound
	if a.Role == api.RoleReviewer {
		d.ReviewRound = a.ReviewRound
	}
	supervisor := t.Supervisor
	if supervisor == api.RoleCEO && !g.CEO.Enabled {
		supervisor = api.RoleUser
	}
	d.Supervisor = supervisorWords(supervisor)
	if t.EffectiveTaskRelease != nil {
		d.TaskRelease = string(*t.EffectiveTaskRelease)
	}
	d.AgentReview = t.EffectiveReview == api.ReviewAgent || t.EffectiveReview == api.ReviewAgentThenHuman
	d.Verification = "full"
	var story *api.Ticket
	if t.Story != nil {
		d.Story = *t.Story
		s, err := e.b.Get(ctx, board.User, int64(*t.Story))
		if err != nil {
			return "", err
		}
		story = &s
		d.StoryTitle = s.Title
		if t.Type == api.TypeTask {
			d.Verification = "targeted"
		}
	}
	d.MultiProject = len(t.Projects) > 1
	if len(t.Projects) == 0 && t.Type == api.TypeTask {
		// The working folder, and the projects it may read but not
		// change.
		d.NoProject, d.Workdir = true, t.Workdir
		d.Repositories = []prompts.Project{}
		for _, p := range e.st.Projects(false) {
			if !p.Missing {
				d.Repositories = append(d.Repositories, prompts.Project{ID: p.ID, Name: p.Name, Folder: p.Path})
			}
		}
	}
	for _, p := range t.Projects {
		if (a.Role == api.RoleReviewer || a.Purpose == api.PurposeExport) && a.Project != "" && p.Project != a.Project {
			continue
		}
		pp := prompts.Project{ID: p.Project, Name: p.Project, Worktree: p.Worktree, Branch: p.Branch, Parent: p.BaseBranch}
		if proj, ok := e.st.Project(p.Project); ok {
			pp.Name, pp.Folder = proj.Name, proj.Path
		}
		if story != nil && t.Type == api.TypeTask && a.Purpose != api.PurposeExport {
			// A task of a story is cut from, and compared with, the
			// story's integration branch.
			for _, sp := range story.Projects {
				if sp.Project == p.Project && sp.Branch != "" {
					pp.Parent = sp.Branch
				}
			}
		}
		d.Projects = append(d.Projects, pp)
	}

	if a.Purpose == api.PurposeExport {
		d.Export = e.exportPromptData(ctx, a.Ticket, a.Project)
	}

	prev, found, err := e.b.PreviousSession(ctx, a.Ticket, a.Role, a.Project, a.Purpose)
	if err != nil {
		return "", err
	}
	if found && (a.Role != api.RoleReviewer || prev.ReviewRound == a.ReviewRound) {
		d.RestartNote = e.renderPrompt("restart", d)
	}
	return e.renderPrompt(name, d), nil
}

// supervisorWords names a supervising role in a prompt.
func supervisorWords(r api.Role) string {
	switch r {
	case api.RoleCEO:
		return "CEO"
	case api.RolePM:
		return "product manager"
	}
	return "user"
}
