package server

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/agentsetup"
	"github.com/scolastico-dev/one-man-office/internal/api"
	"github.com/scolastico-dev/one-man-office/internal/prompts"
	"github.com/scolastico-dev/one-man-office/internal/session"
	"github.com/scolastico-dev/one-man-office/internal/settings"
)

// envCheckLimit bounds one check of the agent environment as a whole.
const envCheckLimit = 30 * time.Second

// setupState is the state of the agent setup routes.
type setupState struct {
	// envCheck is held while an environment check runs: from the request
	// until its commands have ended, also when the answer was sent earlier
	// because the check ran past its limit.
	envCheck sync.Mutex
	// commandTimeout replaces session.DefaultCommandTimeout and checkLimit
	// envCheckLimit when set; tests only.
	commandTimeout time.Duration
	checkLimit     time.Duration
}

// registerSetup adds the routes that detect agent programs, check the agent
// environment and preview prompts. All of them are the user's only: they run
// the user's commands or write profiles that start programs.
func (s *Server) registerSetup(mux *http.ServeMux) {
	s.registerRoutes(mux, []apiRoute{
		{"POST /api/agent-setup/detect", s.postDetect, userOnly},
		{"POST /api/settings/agent-env/check", s.postEnvCheck, userOnly},
		{"POST /api/prompts/{name}/preview", s.postPromptPreview, userOnly},
	})
}

// ---- detection ----

// isFirstStart reports whether the settings in root have never been opened:
// settings.yaml, which the settings store writes when it first opens a home,
// does not exist, and the profiles folder holds nothing. (The folder itself
// is no sign: every omo command creates the home's folders.) Anything else,
// also a file that cannot be checked, is no first start.
func isFirstStart(root string) bool {
	if _, err := os.Lstat(filepath.Join(root, "settings.yaml")); !errors.Is(err, os.ErrNotExist) {
		return false
	}
	entries, err := os.ReadDir(filepath.Join(root, "profiles"))
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	return err == nil && len(entries) == 0
}

// detectAtFirstStart applies the detection plan once, at a first start (see
// isFirstStart) with Options.DetectAgents set. Each action is logged in one line; a write
// that fails is also shown as an invalid-settings event on the profiles
// folder. It never fails the start.
func (s *Server) detectAtFirstStart() {
	res, failures := s.detectAgents(s.opts.DetectAgents)
	for _, f := range res.Found {
		s.log.Printf("agent detection: found %s at %s", f.Command, f.Path)
	}
	for _, name := range res.Created {
		s.log.Printf("agent detection: created the profile %s", name)
	}
	for _, name := range res.Assigned {
		s.log.Printf("agent detection: assigned models to the role %s", name)
	}
	for _, line := range res.Skipped {
		s.log.Printf("agent detection: %s", line)
	}
	for _, line := range failures {
		s.emitSettingsInvalid(filepath.Join(s.opts.Layout.Root, "profiles"), "agent detection: "+line)
	}
}

// detectAgents looks for the agent programs omo knows through lookPath (nil:
// the real PATH), writes a profile for each one whose CLI has no profile yet
// and assigns models to the roles that have none. It never changes an
// existing profile or a role that has models, and never runs a program it
// finds. A write refused because the file changed meanwhile, or that fails,
// is reported in Skipped; failures lists the failed writes again.
func (s *Server) detectAgents(lookPath func(string) (string, error)) (res api.DetectResult, failures []string) {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	// The revisions are taken before the roles are read: a role changed in
	// between is refused by its guarded write instead of overwritten.
	revs := map[string]string{}
	for _, name := range settings.RoleNames {
		revs[name] = s.settings.Revision(settings.SectionRoles, name)
	}
	plan := agentsetup.Detect(lookPath, s.settings.Profiles(), s.settings.Roles())
	res = api.DetectResult{Found: []api.DetectedCLI{}, Created: []string{}, Assigned: []string{}, Skipped: []string{}}
	res.Skipped = append(res.Skipped, plan.Skipped...)
	for _, f := range plan.Found {
		res.Found = append(res.Found, api.DetectedCLI{CLI: f.CLI, Command: f.Command, Path: f.Path})
	}
	for _, p := range plan.Profiles {
		err := s.settings.PutProfileIf(settings.NoRevision, p)
		switch {
		case err == nil:
			res.Created = append(res.Created, p.Name)
		case hasCode(err, api.CodeStaleSettings):
			res.Skipped = append(res.Skipped, fmt.Sprintf("Found %s, but a profile named %s was created meanwhile, so it was left as it is.", p.Command, p.Name))
		default:
			line := fmt.Sprintf("Found %s, but the profile %s could not be written: %s", p.Command, p.Name, errorMessage(err))
			res.Skipped = append(res.Skipped, line)
			failures = append(failures, line)
		}
	}
	for _, r := range plan.Roles {
		// Only models of profiles written now.
		r.Models = slices.DeleteFunc(slices.Clone(r.Models), func(id string) bool {
			profile, _, _ := strings.Cut(id, "/")
			return !slices.Contains(res.Created, profile)
		})
		if len(r.Models) == 0 {
			continue
		}
		err := s.settings.PutRoleIf(revs[r.Name], r)
		switch {
		case err == nil:
			res.Assigned = append(res.Assigned, r.Name)
		case hasCode(err, api.CodeStaleSettings):
			res.Skipped = append(res.Skipped, fmt.Sprintf("The role %s changed meanwhile, so no models were assigned to it.", r.Name))
		default:
			line := fmt.Sprintf("The role %s could not be written: %s", r.Name, errorMessage(err))
			res.Skipped = append(res.Skipped, line)
			failures = append(failures, line)
		}
	}
	return res, failures
}

func hasCode(err error, code string) bool {
	var ae *api.Error
	return errors.As(err, &ae) && ae.Code == code
}

// errorMessage is the message of an API error without its code.
func errorMessage(err error) string {
	var ae *api.Error
	if errors.As(err, &ae) {
		return ae.Message
	}
	return err.Error()
}

func (s *Server) postDetect(w http.ResponseWriter, r *http.Request) {
	var req struct{}
	if err := decodeSettings(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	res, _ := s.detectAgents(s.opts.DetectAgents)
	s.waitSettingsEvents(r.Context())
	writeJSON(w, http.StatusOK, res)
}

// ---- environment check ----

// postEnvCheck expands a draft of the agent environment as the engine does
// and answers, per variable, whether it came out set, empty or failed. It
// never answers or logs a value. Hidden values are the stored ones. One
// check runs at a time.
func (s *Server) postEnvCheck(w http.ResponseWriter, r *http.Request) {
	var req api.AgentEnvCheck
	if err := decodeSettings(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	g := s.settings.General()
	env, err := settings.RestoreEnv(req.Env, g.AgentEnv, "agent_env")
	if err != nil {
		writeError(w, r, setupFieldError(err))
		return
	}
	g.AgentEnv = env
	if err := g.Validate(); err != nil {
		writeError(w, r, setupFieldError(err))
		return
	}
	if !s.setup.envCheck.TryLock() {
		writeError(w, r, api.Errorf(api.CodeConflict, "An environment check is already running. Try again when it has finished."))
		return
	}
	rep, ok := s.checkEnv(env)
	if !ok {
		writeError(w, r, api.Errorf(api.CodeUnavailable, "omo is shutting down"))
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

// setupFieldError maps a settings validation failure to a 422 naming its
// field.
func setupFieldError(err error) error {
	var fe *settings.FieldError
	if errors.As(err, &fe) {
		e := api.Errorf(api.CodeValidation, "%s", fe.Message)
		e.Details = api.FieldDetail{Field: fe.Field}
		return e
	}
	return api.Errorf(api.CodeValidation, "%s", errorMessage(err))
}

// envChecked is the progress of one environment check.
type envChecked struct {
	mu     sync.Mutex
	states map[string]api.EnvVarCheck
}

func (c *envChecked) set(v api.EnvVarCheck) {
	c.mu.Lock()
	c.states[v.Name] = v
	c.mu.Unlock()
}

// checkEnv runs the check of env; the caller holds s.setup.envCheck, which
// is released once the check's commands have ended. It returns at the latest
// after the check limit, with every variable not checked by then failed.
// The check runs as background work of the server: Close kills its command
// and waits for it. Once Close has begun, nothing runs and ok is false.
func (s *Server) checkEnv(env map[string]string) (_ api.AgentEnvReport, ok bool) {
	timeout := s.setup.commandTimeout
	if timeout <= 0 {
		timeout = session.DefaultCommandTimeout
	}
	limit := s.setup.checkLimit
	if limit <= 0 {
		limit = envCheckLimit
	}
	progress := &envChecked{states: map[string]api.EnvVarCheck{}}
	finished := make(chan struct{})
	deadline := time.Now().Add(limit)
	started := s.background(func(ctx context.Context) {
		defer s.setup.envCheck.Unlock()
		defer close(finished)
		expandForCheck(ctx, env, timeout, deadline, progress)
	})
	if !started {
		s.setup.envCheck.Unlock()
		return api.AgentEnvReport{}, false
	}
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case <-finished:
	case <-timer.C:
	}
	progress.mu.Lock()
	defer progress.mu.Unlock()
	out := api.AgentEnvReport{Variables: make([]api.EnvVarCheck, 0, len(env))}
	for _, name := range slices.Sorted(maps.Keys(env)) {
		v, ok := progress.states[name]
		if !ok {
			v = api.EnvVarCheck{Name: name, State: "failed", Message: fmt.Sprintf("the check stopped after %s before this variable was checked", limit)}
		}
		out.Variables = append(out.Variables, v)
	}
	return out, true
}

// expandForCheck expands env with session.ExpandEnvironment, the code the
// engine uses for the agent environment, and records each variable's state
// in progress. A variable whose command timed out is failed; the expansion
// is then repeated with that variable empty, so the others are still
// checked, until no command times out, the deadline passed or ctx ended.
// Values are only compared with "": none is kept, logged or returned.
func expandForCheck(ctx context.Context, env map[string]string, timeout time.Duration, deadline time.Time, progress *envChecked) {
	work := maps.Clone(env)
	failed := map[string]bool{}
	fail := func(name, message string) {
		failed[name] = true
		progress.set(api.EnvVarCheck{Name: name, State: "failed", Message: message})
	}
	failRest := func(message string) {
		for name := range env {
			if !failed[name] {
				fail(name, message)
			}
		}
	}
	for range len(env) + 1 {
		if time.Now().After(deadline) || ctx.Err() != nil {
			return
		}
		result, err := session.ExpandEnvironmentContext(ctx, timeout, work)
		var te *session.CommandTimeoutError
		switch {
		case errors.As(err, &te):
			if _, ok := work[te.Variable]; !ok || failed[te.Variable] {
				failRest("it could not be checked")
				return
			}
			fail(te.Variable, fmt.Sprintf("its command did not finish within %s", timeout))
			// Empty and without its command from now on: a reference
			// to it sees an empty value.
			work[te.Variable] = ""
			continue
		case err != nil:
			failRest("it could not be checked")
			return
		}
		set := map[string]bool{}
		for _, entry := range result {
			name, value, _ := strings.Cut(entry, "=")
			if _, ok := env[name]; ok {
				set[name] = value != ""
			}
		}
		for name := range env {
			if failed[name] {
				continue
			}
			state := "empty"
			if set[name] {
				state = "set"
			}
			progress.set(api.EnvVarCheck{Name: name, State: state})
		}
		return
	}
	failRest("it could not be checked")
}

// ---- prompt preview ----

// postPromptPreview renders a draft of a prompt with sample data. A
// template error is an answer, not a refusal: the draft is not saved.
func (s *Server) postPromptPreview(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, _, ok := s.settings.Prompt(name); !ok || prompts.Kind(name) == "" {
		writeError(w, r, api.Errorf(api.CodeNotFound, "no prompt %q", name))
		return
	}
	var req api.PromptUpdate
	if err := decodeSettings(w, r, &req); err != nil {
		writeError(w, r, err)
		return
	}
	roleText := ""
	if role := prompts.RoleOf(name); role != "" {
		roleText, _, _ = s.settings.Prompt(role)
	}
	text, err := prompts.Preview(name, req.Text, roleText)
	if err != nil {
		writeJSON(w, http.StatusOK, api.PromptPreview{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, api.PromptPreview{Text: text})
}
