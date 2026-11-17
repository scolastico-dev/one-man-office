package server

import (
	"errors"
	"fmt"
	"mime"
	"net/http"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/board"
	"github.com/scolastico-dev/one-man-office/internal/homezip"
)

// Settings export and import (internal/homezip), all for the user only: the
// zip holds the agents' environment values, tokens and prompts, and an
// import rewrites code that runs as the user.
//
//   - GET /api/settings/export[?database=true]: the settings zip as a
//     download; with database=true a consistent snapshot of the database is
//     inside.
//   - POST /api/settings/import: the zip as raw bytes
//     (application/octet-stream); answers api.ImportPlan once every check
//     passed. Nothing changes; the zip waits under the plan's id.
//   - POST /api/settings/import/apply: api.ImportApply; writes the waiting
//     zip and answers api.ImportResult. With database the zip's database
//     replaces the current one at the next start; refused (409) while an
//     agent runs, and omo is paused until then.
func (s *Server) registerBackup(mux *http.ServeMux) {
	s.registerRoutes(mux, []apiRoute{
		{"GET /api/settings/export", s.exportSettings, userOnly},
		{"POST /api/settings/import", s.stageImport, userOnly},
		{"POST /api/settings/import/apply", s.applyImport, userOnly},
	})
}

// backupLock lets one import (stage or apply) run at a time.
var backupLock sync.Mutex

func (s *Server) home() homezip.Home {
	return homezip.Home{Layout: s.opts.Layout, Settings: s.settings, Version: s.opts.Version}
}

func (s *Server) exportSettings(w http.ResponseWriter, r *http.Request) {
	database := false
	switch v := r.URL.Query().Get("database"); v {
	case "", "false":
	case "true":
		database = true
	default:
		writeError(w, r, api.Errorf(api.CodeBadRequest, "database must be true or false"))
		return
	}
	now := time.Now()
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": homezip.FileName(now)}))
	w.Header().Set("Cache-Control", "no-store")
	out := &startedWriter{w: w}
	if err := s.home().Export(r.Context(), out, database, now); err != nil {
		if out.started {
			// The answer is under way; the zip ends broken and the
			// browser reports a failed download.
			logf(r, "export settings: %v", err)
			return
		}
		w.Header().Del("Content-Disposition")
		writeError(w, r, importError(err))
	}
}

// startedWriter records whether anything was written.
type startedWriter struct {
	w       http.ResponseWriter
	started bool
}

func (sw *startedWriter) Write(p []byte) (int, error) {
	sw.started = true
	return sw.w.Write(p)
}

func (s *Server) stageImport(w http.ResponseWriter, r *http.Request) {
	backupLock.Lock()
	defer backupLock.Unlock()
	plan, err := s.home().Stage(r.Context(), r.Body)
	if err != nil {
		writeError(w, r, importError(err))
		return
	}
	n, err := s.runningAgents(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	plan.AgentsRunning = n
	writeJSON(w, http.StatusOK, plan)
}

// beforeImportCheck, when set by a test, runs right before a database
// import checks that no agent runs.
var beforeImportCheck func()

func (s *Server) applyImport(w http.ResponseWriter, r *http.Request) {
	var req api.ImportApply
	if err := decode(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	backupLock.Lock()
	defer backupLock.Unlock()
	ctx, cancel := s.detached(r, 10*time.Minute)
	defer cancel()
	o := homezip.ApplyOptions{Server: req.Server, Database: req.Database, Now: time.Now()}
	// pausedHere is set when the import paused the board; a failed import
	// takes that pause back (a pause of the user's stays).
	pausedHere := false
	unpause := func() {
		if pausedHere {
			if err := s.board.SetPaused(ctx, false); err != nil {
				logf(r, "resume after a failed database import: %v", err)
			}
		}
	}
	if req.Database {
		o.Ready = func() error {
			// Nothing may start from the check until the restart: the
			// database that would hold such work is replaced. The pause
			// comes first, so no agent starts between check and import.
			paused, err := s.board.Paused(ctx)
			if err != nil {
				return err
			}
			if !paused {
				if err := s.board.SetPaused(ctx, true); err != nil {
					return fmt.Errorf("pause the board for the database import: %w", err)
				}
				pausedHere = true
			}
			if beforeImportCheck != nil {
				beforeImportCheck()
			}
			n, err := s.runningAgents(r)
			if err == nil && n > 0 {
				err = api.Errorf(api.CodeConflict, "%s. The database can only be imported while no agent runs: stop them first (Stop all on the Agents tab, and the CEO on its tab).", agentsRun(n))
			}
			return err
		}
	}
	res, err := s.home().Apply(ctx, req.ID, o)
	if err != nil {
		unpause()
		writeError(w, r, importError(err))
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// runningAgents counts the live agent sessions, the CEO's included.
func (s *Server) runningAgents(r *http.Request) (int, error) {
	live, err := s.board.AgentSessions(r.Context(), board.User, true)
	if err != nil {
		return 0, err
	}
	return len(live), nil
}

func agentsRun(n int) string {
	if n == 1 {
		return "1 agent runs"
	}
	return fmt.Sprintf("%d agents run", n)
}

// importError maps the refusals of homezip to API errors.
func importError(err error) error {
	var p *homezip.Problem
	switch {
	case errors.As(err, &p):
		return api.Errorf(api.CodeValidation, "%s", p.Message)
	case errors.Is(err, homezip.ErrNoUpload):
		return api.Errorf(api.CodeNotFound, "%s", err.Error())
	}
	return err
}
