package supervisor

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/bus"
	"github.com/scolastico-dev/one-man-office/internal/claudetrust"
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
	return s.spawnAttempt(role, profileKey, jobID, dir, goal, 0)
}

func (s *Supervisor) spawnAllowed(role string) bool {
	// Safety/continuity roles are deliberately independent from agent halts.
	if role == "smokealarm" || role == "firefighter" || role == "ceo" {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.firefighterPaused && !s.ceoSpawnHalted
}

func (s *Supervisor) spawnAttempt(role, profileKey string, jobID int64, dir, goal string, attempt int) (string, error) {
	if !s.spawnAllowed(role) {
		return "", ErrSpawningHalted
	}
	profile, ok := s.Cfg.Models[profileKey]
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
	// Claude Code's trust dialog would block the agent forever: no human is
	// watching an agent session to answer it.
	if s.Cfg.ShouldTrustWorkdirs() && strings.Contains(filepath.Base(profile.Cmd), "claude") {
		if err := claudetrust.Ensure(dir); err != nil {
			db.AppendEvent(s.DB, "trust_warning", name, jobID, err.Error())
		}
	}
	env := []string{"OMO_AGENT_ID=" + name, "OMO_SOCKET=" + s.SocketPath}
	for k, v := range profile.Env {
		env = append(env, k+"="+v)
	}
	sess, err := session.Start(session.Options{
		Cmd: profile.Cmd, Args: profile.Args, Env: env, Dir: dir,
		LogPath:      filepath.Join(s.OfficeDir, ".omo", "logs", LogName(name)),
		LogMaxSizeKB: s.Cfg.Logs.MaxSizeKB,
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

	go func() { // start prompt: give the CLI a moment to boot, then type.
		time.Sleep(s.startPromptDelay())
		sess.SendPrompt(s.Msgs.StartPrompt(name))
	}()
	go s.watchHandshake(name, role, profileKey, jobID, dir, goal, attempt)
	go s.watchExit(name)
	return name, nil
}

// watchHandshake kills and retries agents that never call `omo ready`.
func (s *Supervisor) watchHandshake(name, role, profileKey string, jobID int64, dir, goal string, attempt int) {
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
				if _, err := s.spawnAttempt(role, profileKey, jobID, dir, goal, attempt+1); errors.Is(err, ErrSpawningHalted) && jobID != 0 {
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
			s.Mail.Send("user", "user", "spawn failed", detail, bus.PrioUrgent)
			if ceo, ok := s.Mail.Dir.CEO(); ok {
				s.Mail.Send("user", ceo, "spawn failed", detail, bus.PrioUrgent)
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
			s.Mail.Send("user", "user", "job failed", detail, bus.PrioUrgent)
			if ceo, ok := s.Mail.Dir.CEO(); ok {
				s.Mail.Send("user", ceo, "job failed", detail, bus.PrioUrgent)
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
		s.Mail.Send("user", "user", "office stopped: CEO cannot start", detail, bus.PrioUrgent)
		return
	}
	time.Sleep(s.ceoRestartBackoff())
	goal := a.Goal + "\n\nNOTE: " + s.Msgs.RestartNote()
	s.Spawn("ceo", s.Cfg.Roles["ceo"], 0, s.OfficeDir, goal)
}
