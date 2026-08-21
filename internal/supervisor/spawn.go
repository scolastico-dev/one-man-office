package supervisor

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/agentcli"
	"github.com/scolastico-dev/one-man-office/internal/bus"
	"github.com/scolastico-dev/one-man-office/internal/claudetrust"
	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/names"
	"github.com/scolastico-dev/one-man-office/internal/queue"
	"github.com/scolastico-dev/one-man-office/internal/session"
)

var ErrSpawningHalted = errors.New("new agent spawning is halted")

// Spawn starts one agent: name, DB row (spawning), PTY session, start
// prompt, handshake timer, exit watcher.
func (s *Supervisor) Spawn(role, profileKey string, jobID int64, dir, goal string) (string, error) {
	if !s.spawnAllowed(role) {
		return "", ErrSpawningHalted
	}
	return s.spawnAttempt(role, profileKey, jobID, dir, goal, 0, false)
}

func (s *Supervisor) spawnAllowed(role string) bool {
	s.mu.Lock()
	safeMode := s.safeMode
	s.mu.Unlock()
	if safeMode {
		return role == "ceo"
	}
	// Safety/continuity roles are deliberately independent from agent halts.
	if role == "smokealarm" || role == "firefighter" || role == "ceo" {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.firefighterPaused && !s.ceoSpawnHalted
}

func (s *Supervisor) spawnAttempt(role, profileKey string, jobID int64, dir, goal string, attempt int, configured bool) (string, error) {
	if !s.spawnAllowed(role) {
		return "", ErrSpawningHalted
	}
	cfg := s.Config()
	profile, ok := cfg.Models[profileKey]
	if !ok {
		return "", fmt.Errorf("unknown profile %q", profileKey)
	}
	s.nameMu.Lock()
	name, err := names.Pick(role, func(n string) bool {
		// Exact historical names stay reserved because transcript filenames
		// include them. First names are additionally unique across every role
		// for currently living agents (no ceo-luna + pm-luna ambiguity).
		if _, err := db.GetAgent(s.DB, n); err == nil {
			return true
		}
		living, _ := db.LivingAgents(s.DB)
		for _, a := range living {
			if names.FirstName(a.Name) == names.FirstName(n) {
				return true
			}
		}
		return false
	})
	if err != nil {
		s.nameMu.Unlock()
		return "", err
	}
	if err := db.InsertAgent(s.DB, db.Agent{Name: name, Role: role, Profile: profileKey, JobID: jobID, Goal: goal}); err != nil {
		s.nameMu.Unlock()
		return "", err
	}
	s.nameMu.Unlock()
	provider := agentcli.Resolve(profile.Provider, profile.Cmd)
	// Claude Code's trust dialog would block the agent forever: no human is
	// watching an agent session to answer it.
	if cfg.ShouldTrustWorkdirs() && provider == agentcli.Claude {
		if err := claudetrust.Ensure(dir); err != nil {
			db.AppendEvent(s.DB, "trust_warning", name, jobID, err.Error())
		}
	}
	startPrompt := s.Msgs.StartPrompt(name)
	launch := agentcli.Prepare(profile.Provider, profile.Cmd, profile.Args, profile.Env, dir, startPrompt, cfg.ShouldTrustWorkdirs(), profile.ShouldInjectPrompt())
	env := []string{"OMO_AGENT_ID=" + name, "OMO_SOCKET=" + s.SocketPath}
	for k, v := range launch.Env {
		env = append(env, k+"="+v)
	}
	sess, err := session.Start(session.Options{
		Cmd: profile.Cmd, Args: launch.Args, Env: env, Dir: dir,
		LogPath:      filepath.Join(s.OfficeDir, ".omo", "logs", LogName(name)),
		LogMaxSizeKB: cfg.Logs.MaxSizeKB,
		// Inactive-session retention is applied after an agent exits. Keep
		// all size-rotation segments while the session is alive.
		LogKeep: -1,
	})
	if err != nil {
		db.SetAgentState(s.DB, name, "dead")
		return "", err
	}
	s.mu.Lock()
	s.sessions[name] = sess
	if role == "ceo" {
		s.ceoSpawnedAt = time.Now()
	}
	s.mu.Unlock()
	db.AppendEvent(s.DB, "agent_spawned", name, jobID, fmt.Sprintf("role=%s profile=%s attempt=%d", role, profileKey, attempt))

	if profile.ShouldInjectPrompt() {
		go s.deliverInitialPrompt(name, jobID, sess, startPrompt, launch.PromptInjected, profile)
	}
	go s.watchHandshake(name, role, profileKey, jobID, dir, goal, attempt, configured)
	go s.watchExit(name)
	return name, nil
}

type promptSender interface {
	SendPrompt(string) error
	Done() <-chan struct{}
}

// deliverInitialPrompt performs automatic PTY delivery when the launch did
// not already carry the prompt, then retries while the agent remains in its
// pre-ready spawning state.
func (s *Supervisor) deliverInitialPrompt(name string, jobID int64, sender promptSender, prompt string, launchInjected bool, profile config.Profile) {
	if !profile.ShouldInjectPrompt() {
		return
	}
	if !launchInjected {
		if delay := profile.InitialPromptDelay(s.startPromptDelay()); delay > 0 {
			if !waitForPromptWindow(sender, delay) {
				return
			}
		}
		if !s.agentAwaitingReady(name) {
			return
		}
		if err := sender.SendPrompt(prompt); err != nil {
			db.AppendEvent(s.DB, "prompt_injection_error", name, jobID, err.Error())
		}
	}
	for retry := 1; retry <= profile.InitialPromptRetryCount(); retry++ {
		if !waitForPromptWindow(sender, profile.InitialPromptRetryWait()) {
			return
		}
		if !s.agentAwaitingReady(name) {
			return
		}
		if err := sender.SendPrompt(prompt); err != nil {
			db.AppendEvent(s.DB, "prompt_injection_error", name, jobID, fmt.Sprintf("retry=%d: %v", retry, err))
			continue
		}
		db.AppendEvent(s.DB, "prompt_retried", name, jobID, fmt.Sprintf("retry=%d", retry))
	}
}

func waitForPromptWindow(sender promptSender, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-sender.Done():
		return false
	}
}

func (s *Supervisor) agentAwaitingReady(name string) bool {
	agent, err := db.GetAgent(s.DB, name)
	return err == nil && agent.State == "spawning"
}

// watchHandshake kills and retries agents that never call `omo ready`.
func (s *Supervisor) watchHandshake(name, role, profileKey string, jobID int64, dir, goal string, attempt int, configured bool) {
	deadline := time.After(s.readyTimeout())
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			a, err := db.GetAgent(s.DB, name)
			if err != nil || a.State != "spawning" {
				return // handshake done (or agent gone) — nothing to do
			}
		case <-deadline:
			a, err := db.GetAgent(s.DB, name)
			if err != nil || a.State != "spawning" {
				return
			}
			db.AppendEvent(s.DB, "handshake_timeout", name, jobID, fmt.Sprintf("attempt=%d", attempt))
			s.KillAgent(name, true)
			if attempt < s.maxSpawnRetries() {
				nextProfile := profileKey
				roleModels := s.Config().Roles[role]
				if configured && roleModels.Assignment == config.AssignmentFailover {
					if i := slices.Index(roleModels.Models, profileKey); i >= 0 && i+1 < len(roleModels.Models) {
						nextProfile = roleModels.Models[i+1]
					}
				}
				if _, err := s.spawnAttempt(role, nextProfile, jobID, dir, goal, attempt+1, configured); errors.Is(err, ErrSpawningHalted) && jobID != 0 {
					if j, getErr := s.Jobs.Get(jobID); getErr == nil && (j.State == queue.StateAssigned || j.State == queue.StateWorking) {
						s.Jobs.SetAssignee(jobID, "")
						_ = s.Jobs.Transition(jobID, queue.StateQueued)
					}
				}
				return
			}
			// Retries exhausted: fail the job (if any) and tell CEO + user.
			detail := s.Msgs.SpawnFailed(name, role, attempt+1)
			if jobID != 0 {
				s.Jobs.Transition(jobID, queue.StateFailed)
				s.Jobs.SetNote(jobID, detail)
			}
			db.AppendEvent(s.DB, "spawn_failed", name, jobID, detail)
			s.Mail.Send(bus.SystemSender, "user", "spawn failed", detail, bus.PrioUrgent)
			if ceo, ok := s.Mail.Dir.CEO(); ok {
				s.Mail.Send(bus.SystemSender, ceo, "spawn failed", detail, bus.PrioUrgent)
			}
			if s.OnSpawnFailed != nil {
				s.OnSpawnFailed(role, jobID)
			}
			return
		}
	}
}

// watchExit reacts to a session ending. Expected ends (agent done/dead) are
// cleanup only; unexpected deaths requeue the job with the restart note.
func (s *Supervisor) watchExit(name string) {
	s.mu.Lock()
	sess := s.sessions[name]
	s.mu.Unlock()
	if sess == nil {
		return
	}
	<-sess.Done()
	s.mu.Lock()
	delete(s.sessions, name)
	delete(s.waiters, name)
	delete(s.smokeRaised, name)
	delete(s.smokeHistory, name)
	s.mu.Unlock()
	defer s.PruneInactiveLogs()
	a, err := db.GetAgent(s.DB, name)
	if err != nil {
		return
	}
	// A finished firefighter — however it ended — hands over to the next
	// open incident.
	if a.Role == "firefighter" {
		defer s.nextOpenIncident()
	}
	if a.State == "done" || a.State == "dead" {
		return // expected termination
	}
	db.SetAgentState(s.DB, name, "dead")
	db.AppendEvent(s.DB, "agent_died", name, a.JobID, "process exited unexpectedly")
	s.handleDeath(a)
}

// handleDeath requeues (or fails) the dead agent's job and respawns a dead
// CEO. Reviewer deaths are handled in review.go (Task 15).
func (s *Supervisor) handleDeath(a *db.Agent) {
	defer s.kickDispatch()
	if a.Role == "ceo" {
		s.respawnCEO(a)
		return
	}
	if a.Role == "reviewer" {
		s.handleReviewerDeath(a) // Task 15
		return
	}
	if a.JobID == 0 {
		return
	}
	j, err := s.Jobs.Get(a.JobID)
	if err != nil {
		return
	}
	switch j.State {
	case queue.StateAssigned, queue.StateWorking, queue.StateRework, queue.StateReview:
		n, err := s.Jobs.IncrementRetries(j.ID)
		if err != nil {
			return
		}
		if n > s.maxJobRetries() {
			s.Jobs.Transition(j.ID, queue.StateFailed)
			detail := s.Msgs.JobFailed(j.ID, j.Title, n)
			s.Mail.Send(bus.SystemSender, "user", "job failed", detail, bus.PrioUrgent)
			if ceo, ok := s.Mail.Dir.CEO(); ok {
				s.Mail.Send(bus.SystemSender, ceo, "job failed", detail, bus.PrioUrgent)
			}
			return
		}
		s.Jobs.SetNote(j.ID, s.Msgs.RestartNote())
		s.Jobs.SetAssignee(j.ID, "")
		s.Jobs.Transition(j.ID, queue.StateQueued)
	}
}

// respawnCEO brings the CEO back, but gives up after MaxCEORestarts deaths
// that all happened within CEORestartWindow of starting. Without that bound
// a CEO which crashes on startup is respawned in a tight loop, creating
// unbounded agents, PTYs and log files.
func (s *Supervisor) respawnCEO(a *db.Agent) {
	s.mu.Lock()
	window := s.ceoRestartWindow()
	if !s.ceoSpawnedAt.IsZero() && time.Since(s.ceoSpawnedAt) < window {
		s.ceoFailures++
	} else {
		s.ceoFailures = 1
	}
	failures := s.ceoFailures
	s.mu.Unlock()

	if failures > s.maxCEORestarts() {
		detail := s.Msgs.CEOGaveUp(failures, window.String())
		db.AppendEvent(s.DB, "ceo_gave_up", a.Name, 0, detail)
		s.Mail.Send(bus.SystemSender, "user", "office stopped: CEO cannot start", detail, bus.PrioUrgent)
		return
	}
	time.Sleep(s.ceoRestartBackoff())
	goal := a.Goal + "\n\nNOTE: " + s.Msgs.RestartNote()
	s.spawnRole("ceo", 0, s.OfficeDir, goal, failures-1)
}
