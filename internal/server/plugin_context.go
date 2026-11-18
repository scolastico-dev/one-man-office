package server

import (
	"context"
	"net/http"
	"slices"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
)

// Folder context follows the same trusted records and gitfile checks as shells.
// Unavailable paths are never exposed as usable launch destinations.
func (s *Server) pluginFolder(ctx context.Context, ticket int, project string) map[string]any {
	out := map[string]any{"project": project, "kind": "repository", "path": "", "available": false, "reason": ""}
	req := api.OpenTerminalRequest{Project: project}
	if ticket > 0 {
		req.Ticket = &ticket
		out["kind"] = "worktree"
		if project == "" {
			out["kind"] = "working_directory"
		}
	}
	if project != "" {
		p, ok := s.settings.Project(project)
		if !ok || !p.Enabled {
			out["reason"] = "project_unavailable"
			return out
		}
	}
	spec, err := s.shellSpec((&http.Request{}).WithContext(ctx), req)
	// An empty selection means a projectless directory here, not the shell's
	// convenient inference of a newly associated single project.
	if err != nil || spec.Project != project {
		out["reason"] = "folder_unavailable"
		return out
	}
	out["path"], out["available"] = spec.Dir, true
	return out
}

func (s *Server) pluginTicketPayload(ctx context.Context, t api.TicketSummary) map[string]any {
	out := plugins.TicketPayload(t, s.Address())
	folders := make([]any, 0, len(t.Projects))
	for _, p := range t.Projects {
		folders = append(folders, s.pluginFolder(ctx, t.Number, p.Project))
	}
	if len(t.Projects) == 0 && t.Type == api.TypeTask {
		folders = append(folders, s.pluginFolder(ctx, t.Number, ""))
	}
	out["folders"] = folders
	return out
}

func (s *Server) pluginProjectContext(ctx context.Context, ticket int, id string, projects []api.TicketProject) (map[string]any, error) {
	if p, ok := s.settings.Project(id); !ok || !p.Enabled {
		return nil, api.Errorf(api.CodeNotFound, "enabled project does not exist")
	}
	if ticket > 0 && !slices.ContainsFunc(projects, func(p api.TicketProject) bool { return p.Project == id }) {
		err := api.Errorf(api.CodeValidation, "project must belong to the selected ticket")
		err.Details = api.FieldDetail{Field: "project"}
		return nil, err
	}
	out := s.pluginFolder(ctx, ticket, id)
	if out["available"] != true {
		return nil, api.Errorf(api.CodeConflict, "the selected project folder is unavailable")
	}
	delete(out, "project")
	out["id"] = id
	return out, nil
}
