package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/agentcli"
	"github.com/scolastico-dev/one-man-office/internal/bus"
	"github.com/scolastico-dev/one-man-office/internal/claudetrust"
	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/names"
	"github.com/scolastico-dev/one-man-office/internal/plugins"
	"github.com/scolastico-dev/one-man-office/internal/queue"
	"github.com/scolastico-dev/one-man-office/internal/session"
	"github.com/scolastico-dev/one-man-office/internal/websupervisor/controlplane"
)

var ErrSpawningHalted = errors.New("new agent spawning is halted")

// Spawn starts one agent: name, DB row (spawning), PTY session, start
// prompt, handshake timer, exit watcher.
func (s *Supervisor) Spawn(role, profileKey string, jobID int64, dir, goal string) (string, error) {
	if !s.spawnAllowed(role) {
		return "", ErrSpawningHalted
	}
	return s.spawnAttempt(role, profileKey, jobID, dir, goal, 0, false, false, false)
}

func (s *Supervisor) spawnAllowed(role string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopping {
		return false
	}
	if s.safeMode {
		return role == "ceo"
	}
	// Safety/continuity roles are deliberately independent from agent halts.
	if role == "smokealarm" || role == "firefighter" || role == "ceo" {
		return true
	}
	return !s.firefighterPaused && !s.ceoSpawnHalted
}

func (s *Supervisor) spawnAttempt(role, profileKey string, jobID int64, dir, goal string, attempt int, configured, forceUsage, managementRestart bool) (string, error) {
	if !managementRestart && !s.spawnAllowed(role) {
		return "", ErrSpawningHalted
	}
	if jobID != 0 {
		if pending, ok := s.takeDeferredJobSpawn(role, jobID); ok {
			validated, err := s.revalidateDeferredSpawn(pending, jobID)
			if err != nil {
				s.rememberDeferredJobSpawn(role, jobID, pending)
				return "", fmt.Errorf("%w: %v", errDeferredProfile, err)
			}
			pending = validated
			profileKey, dir, goal, attempt = pending.profile, pending.dir, pending.goal, pending.attempt
			configured, forceUsage, managementRestart = pending.configured, pending.forceUsage, pending.managementRestart
		}
	}
	if !configured {
		if err := s.checkExplicitProfile(profileKey, forceUsage); err != nil {
			return "", err
		}
	}
	cfg := s.Config()
	profile, ok := cfg.Models[profileKey]
	if !ok {
		return "", fmt.Errorf("unknown profile %q", profileKey)
	}
	release, err := s.acquireSpawnLease()
	if err != nil {
		if errors.Is(err, controlplane.ErrLimit) {
			request := capacitySpawn{role: role, profile: profileKey, dir: dir, goal: goal, attempt: attempt, configured: configured, forceUsage: forceUsage, managementRestart: managementRestart}
			if jobID == 0 {
				s.deferManagementSpawn(request)
			} else {
				s.rememberDeferredJobSpawn(role, jobID, request)
			}
		}
		return "", err
	}
	leaseTransferred := false
	defer func() {
		if !leaseTransferred {
			release()
		}
	}()
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
	if err := db.InsertAgent(s.DB, db.Agent{Name: name, Role: role, Profile: profileKey, JobID: jobID, Goal: goal, WorkDir: dir}); err != nil {
		s.nameMu.Unlock()
		return "", err
	}
	s.nameMu.Unlock()
	dir, err = s.roleWorkDir(role, dir)
	if err != nil {
		db.SetAgentState(s.DB, name, "dead")
		return "", err
	}
	provider := agentcli.Resolve(profile.Provider, profile.Cmd)
	// Claude Code's trust dialog would block the agent forever: no human is
	// watching an agent session to answer it.
	if cfg.ShouldTrustWorkdirs() && provider == agentcli.Claude {
		if err := claudetrust.EnsureForEnv(profile.Env, dir); err != nil {
			db.AppendEvent(s.DB, "trust_warning", name, jobID, err.Error())
		}
	}
	startPrompt := s.Msgs.StartPrompt(name)
	launch := agentcli.Prepare(profile.Provider, profile.Cmd, profile.Args, profile.Env, dir, startPrompt, cfg.ShouldTrustWorkdirs(), profile.ShouldInjectPrompt())
	env := session.MergeEnvironment(cfg.Agents.Env, launch.Env, map[string]string{
		"OMO_AGENT_ID": name,
		"OMO_SOCKET":   s.SocketPath,
	})
	sess, err := session.Start(session.Options{
		Cmd: profile.Cmd, Args: launch.Args, Env: env, Dir: dir,
		LowerPriority: cfg.Agents.LowerPriority,
		NiceIncrement: cfg.Agents.NiceIncrement,
		LogPath:       filepath.Join(s.OfficeDir, ".omo", "logs", LogName(name)),
		LogMaxSizeKB:  cfg.Logs.MaxSizeKB,
		// Inactive-session retention is applied after an agent exits. Keep
		// all size-rotation segments while the session is alive.
		LogKeep: -1,
		OnLogLine: func(line string) {
			if s.Plugins != nil {
				s.Plugins.EmitAsync(plugins.Event{Name: plugins.EventAgentLogLine, Data: map[string]any{
					"agent": name, "role": role, "profile": profileKey, "job_id": jobID, "line": line,
				}})
			}
		},
	})
	if err != nil {
		db.SetAgentState(s.DB, name, "dead")
		return "", err
	}
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		_ = sess.Kill()
		<-sess.Done()
		_ = db.SetAgentState(s.DB, name, "dead")
		return "", ErrSpawningHalted
	}
	s.sessionWatchers.Add(1)
	s.sessions[name] = sess
	if role == "ceo" {
		s.ceoSpawnedAt = time.Now()
	}
	s.mu.Unlock()
	leaseTransferred = true
	go func() {
		defer s.sessionWatchers.Done()
		// Release capacity before exit handling can respawn a management
		// agent. Done closes only after the process has been reaped.
		<-sess.Done()
		release()
		s.kickDispatch()
		s.watchExit(name)
	}()
	db.AppendEvent(s.DB, "agent_spawned", name, jobID, fmt.Sprintf("role=%s profile=%s attempt=%d", role, profileKey, attempt))
	if s.Plugins != nil {
		s.Plugins.EmitAsync(plugins.Event{Name: plugins.EventAgentStart, Data: map[string]any{
			"agent": name, "role": role, "profile": profileKey, "job_id": jobID, "workdir": dir,
		}})
	}

	if profile.ShouldInjectPrompt() {
		go s.deliverInitialPrompt(name, jobID, sess, startPrompt, launch.PromptInjected, profile)
	}
	go s.watchHandshake(name, role, profileKey, jobID, dir, goal, attempt, configured, forceUsage, managementRestart)
	return name, nil
}

func (s *Supervisor) acquireSpawnLease() (func(), error) {
	if s.Control == nil {
		return func() {}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	lease, err := s.Control.Acquire(ctx)
	cancel()
	if err != nil {
		if !errors.Is(err, controlplane.ErrLimit) {
			s.controlFailed(err)
		}
		return nil, err
	}
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.Control.Release(ctx, lease); err != nil {
			s.controlFailed(err)
		}
	}, nil
}

func (s *Supervisor) roleWorkDir(role, requested string) (string, error) {
	switch role {
	case "ceo", "product_manager", "smokealarm", "firefighter", "branch_namer":
		dir := filepath.Join(s.OfficeDir, ".omo", "storage")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("create office storage: %w", err)
		}
		return dir, nil
	default:
		return requested, nil
	}
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
func (s *Supervisor) watchHandshake(name, role, profileKey string, jobID int64, dir, goal string, attempt int, configured, forceUsage, managementRestart bool) {
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
				if configured {
					selectionRole := role
					if selectionRole == "branch_namer" {
						selectionRole = "smokealarm"
					}
					var selectErr error
					nextProfile, selectErr = s.roleProfile(selectionRole, attempt+1)
					if selectErr != nil {
						db.AppendEvent(s.DB, "spawn_retry_selection_failed", name, jobID, selectErr.Error())
						if role == "branch_namer" {
							s.failBranchNaming(jobID, selectErr)
						}
						return
					}
				}
				if _, err := s.spawnAttempt(role, nextProfile, jobID, dir, goal, attempt+1, configured, forceUsage, managementRestart); err != nil {
					if spawnBackpressure(err) {
						if jobID == 0 && managementRestart && errors.Is(err, controlplane.ErrLimit) && role != "ceo" && role != "firefighter" && role != "smokealarm" {
							s.queueExplicitRestart(name, capacitySpawn{role: role, profile: nextProfile, dir: dir, goal: goal, attempt: attempt + 1, configured: configured, forceUsage: forceUsage, managementRestart: managementRestart})
						}
						s.deferJobSpawn(role, jobID, err)
					} else if role == "branch_namer" {
						s.failBranchNaming(jobID, err)
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
			if role == "branch_namer" {
				s.failBranchNaming(jobID, errors.New(detail))
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
	pendingInput := s.pendingAgentInput[name]
	if s.agentInputFlushing[name] && len(pendingInput) > 0 {
		pendingInput = pendingInput[1:]
	}
	if timer := s.agentInputTimers[name]; timer != nil {
		timer.Stop()
	}
	delete(s.sessions, name)
	delete(s.waiters, name)
	delete(s.lastUserInput, name)
	delete(s.pendingMailNotification, name)
	delete(s.pendingAgentInput, name)
	delete(s.agentInputTimers, name)
	delete(s.agentInputFlushing, name)
	delete(s.smokeRaised, name)
	delete(s.smokeHistory, name)
	s.mu.Unlock()
	for _, input := range pendingInput {
		input.done <- fmt.Errorf("send input to %s: session ended", name)
	}
	defer s.PruneInactiveLogs()
	a, err := db.GetAgent(s.DB, name)
	if err != nil {
		return
	}
	if a.JobID != 0 {
		defer func() {
			if err := s.cleanupTerminalWorktree(a.JobID); err != nil {
				db.AppendEvent(s.DB, "cleanup_error", "", a.JobID, err.Error())
			}
		}()
	}
	// A finished firefighter — however it ended — hands over to the next
	// open incident.
	if a.Role == "firefighter" {
		defer s.nextOpenIncident()
	}
	if a.State == "done" || a.State == "dead" {
		if a.Role == "branch_namer" && a.State == "dead" {
			if job, err := s.Jobs.Get(a.JobID); err == nil && job.State == queue.StateCancelled {
				s.failBranchNaming(a.JobID, fmt.Errorf("branch naming agent was killed before returning a name"))
			}
		}
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
	if a.Role == "branch_namer" {
		s.failBranchNaming(a.JobID, fmt.Errorf("branch naming agent exited before returning a name"))
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
