package server

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/childenv"
	"github.com/scolastico-dev/one-man-office/internal/globalhome"
	"github.com/scolastico-dev/one-man-office/internal/homezip"
	"github.com/scolastico-dev/one-man-office/internal/mcpclients"
)

// The agent program routes (internal/mcpclients), all for the user only:
// they run the user's agent programs and read and change their
// configuration files, which hold the user's credentials.
//
//   - GET /api/mcp/clients: []api.MCPClientState, one per target (the
//     programs' default files and the files of the profiles' account
//     folders), values masked.
//   - POST /api/mcp/clients/{target}/plan: api.MCPClientPlanRequest;
//     answers api.MCPClientPlan. Nothing runs.
//   - POST /api/mcp/clients/{target}/apply: api.MCPClientApply with the
//     plan's hash; runs it after a copy of the file was saved in the
//     backups and answers api.MCPClientApplied. A changed file gives 409; a
//     failed command gives 409 whose details are the api.MCPClientApplied
//     after it.
//   - GET /api/mcp/takeover: api.MCPTakeover, values masked.
//   - POST /api/mcp/takeover/apply: api.MCPTakeoverApply; answers
//     api.MCPTakeoverReport. omo's copy of each server is stored before its
//     entries are removed from the programs.
func (s *Server) registerMCPClients(mux *http.ServeMux) {
	s.registerRoutes(mux, []apiRoute{
		{"GET /api/mcp/clients", s.listMCPClients, userOnly},
		{"POST /api/mcp/clients/{target}/plan", s.planMCPClient, userOnly},
		{"POST /api/mcp/clients/{target}/apply", s.applyMCPClient, userOnly},
		{"GET /api/mcp/takeover", s.previewMCPTakeover, userOnly},
		{"POST /api/mcp/takeover/apply", s.applyMCPTakeover, userOnly},
	})
}

// mcpClientsLock lets one change of the programs' files run at a time.
var mcpClientsLock sync.Mutex

// mcpClientsMachine is what the agent program routes take from the machine:
// the user's home, omo's executable, the programs' environment and the
// lookup of the programs. Tests replace it with a temporary home and fake
// programs.
var mcpClientsMachine = func(s *Server) (mcpclients.Options, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return mcpclients.Options{}, api.Errorf(api.CodeUnavailable, "the user's home folder is unknown")
	}
	exe, err := os.Executable()
	if err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
	} else {
		exe = ""
	}
	return mcpclients.Options{
		Home:     home,
		OmoPath:  exe,
		OmoHome:  s.opts.Layout.Root,
		PassHome: passHome(s.opts.Layout.Root, home),
		Env:      childenv.Inherited(),
		LookPath: exec.LookPath,
		Getenv:   os.LookupEnv,
	}, nil
}

// passHome reports whether omo's entries in the agent programs' files must
// name its home root: when it is not the home omo uses without OMO_HOME.
func passHome(root, home string) bool {
	def, err := globalhome.DefaultDir(home)
	return err != nil || filepath.Clean(root) != def
}

// mcpServerStore is omo's store of relayed servers, when the settings store
// has one; tests replace it.
var mcpServerStore = func(s *Server) (mcpclients.Store, bool) {
	st, ok := any(s.settings).(mcpclients.Store)
	return st, ok
}

// mcpClients builds the clients and the targets of the profiles.
func (s *Server) mcpClients() (*mcpclients.Clients, []mcpclients.Target, error) {
	o, err := mcpClientsMachine(s)
	if err != nil {
		return nil, nil, err
	}
	if o.Backups == "" {
		o.Backups = filepath.Join(s.opts.Layout.Root, homezip.BackupFolder)
	}
	return mcpclients.New(o), mcpclients.Targets(s.settings.Profiles(), o.Home), nil
}

// mcpTarget finds the target of the request's {target}.
func (s *Server) mcpTarget(r *http.Request) (*mcpclients.Clients, mcpclients.Target, error) {
	c, targets, err := s.mcpClients()
	if err != nil {
		return nil, mcpclients.Target{}, err
	}
	id := r.PathValue("target")
	for _, t := range targets {
		if t.ID == id {
			return c, t, nil
		}
	}
	return nil, mcpclients.Target{}, api.Errorf(api.CodeNotFound, "no agent program configuration %s", id)
}

func (s *Server) listMCPClients(w http.ResponseWriter, r *http.Request) {
	c, targets, err := s.mcpClients()
	if err != nil {
		writeError(w, r, err)
		return
	}
	out := make([]api.MCPClientState, 0, len(targets))
	for _, t := range targets {
		out = append(out, mcpclients.MaskState(c.Inspect(t)))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) planMCPClient(w http.ResponseWriter, r *http.Request) {
	var req api.MCPClientPlanRequest
	if err := decode(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	c, t, err := s.mcpTarget(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	p, err := c.Plan(t, req.Action)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) applyMCPClient(w http.ResponseWriter, r *http.Request) {
	var req api.MCPClientApply
	if err := decode(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	c, t, err := s.mcpTarget(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	mcpClientsLock.Lock()
	defer mcpClientsLock.Unlock()
	action, err := planAction(c, t, req.Plan)
	if err != nil {
		writeError(w, r, err)
		return
	}
	applied, err := c.Apply(r.Context(), t, action, req.Plan)
	if err != nil {
		s.log.Printf("mcp: %s of omo's entry in %s failed; omo's entry is %s", action, t.ID, applied.State.State)
		writeError(w, r, err)
		return
	}
	s.log.Printf("mcp: %s of omo's entry in %s done; omo's entry is %s", action, t.ID, applied.State.State)
	writeJSON(w, http.StatusOK, mcpclients.MaskApplied(applied))
}

// planAction is the action (install or remove) whose current plan has the
// hash; the hash covers the action. A hash of no current plan is a changed
// file.
func planAction(c *mcpclients.Clients, t mcpclients.Target, hash string) (string, error) {
	for _, a := range []string{api.MCPClientInstall, api.MCPClientRemove} {
		if p, err := c.Plan(t, a); err == nil && p.Hash == hash {
			return a, nil
		}
	}
	return "", api.Errorf(api.CodeConflict, "the file changed; review again")
}

// mcpProjectFolders are the folders of omo's projects, whose project-level
// entries the take-over lists as staying.
func (s *Server) mcpProjectFolders() []string {
	var out []string
	for _, p := range s.settings.Projects(true) {
		if p.Path != "" && !p.Missing {
			out = append(out, p.Path)
		}
	}
	return out
}

func (s *Server) previewMCPTakeover(w http.ResponseWriter, r *http.Request) {
	c, targets, err := s.mcpClients()
	if err != nil {
		writeError(w, r, err)
		return
	}
	var existing []api.MCPServer
	if st, ok := mcpServerStore(s); ok {
		existing = st.MCPServers()
	}
	tk := mcpclients.MaskTakeover(c.Preview(targets, existing))
	tk.Stays = append(tk.Stays, mcpclients.ProjectStays(s.mcpProjectFolders())...)
	writeJSON(w, http.StatusOK, tk)
}

func (s *Server) applyMCPTakeover(w http.ResponseWriter, r *http.Request) {
	var req api.MCPTakeoverApply
	if err := decode(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	st, ok := mcpServerStore(s)
	if !ok {
		writeError(w, r, api.Errorf(api.CodeUnavailable, "this omo cannot store MCP servers yet"))
		return
	}
	c, targets, err := s.mcpClients()
	if err != nil {
		writeError(w, r, err)
		return
	}
	mcpClientsLock.Lock()
	defer mcpClientsLock.Unlock()
	report := c.ApplyTakeover(r.Context(), targets, req, st)
	s.waitSettingsEvents(r.Context())
	for _, res := range report.Results {
		removed, kept := 0, 0
		for _, rm := range res.Removed {
			if rm.Done {
				removed++
			} else {
				kept++
			}
		}
		s.log.Printf("mcp: take-over of %s: stored %v, removed from %d programs, still in %d", res.Name, res.Stored, removed, kept)
	}
	writeJSON(w, http.StatusOK, report)
}
