// Package supervisor is omo's kernel loop: it owns every agent session,
// enforces the handshake, dispatches queued jobs and reacts to deaths.
package supervisor

import (
	"context"
	"database/sql"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"sync"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/bus"
	"github.com/scolastico-dev/one-man-office/internal/config"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/gitops"
	"github.com/scolastico-dev/one-man-office/internal/messages"
	"github.com/scolastico-dev/one-man-office/internal/queue"
	"github.com/scolastico-dev/one-man-office/internal/session"
)

var (
	ReadyTimeout     = 120 * time.Second
	StartPromptDelay = 2 * time.Second
	MaxSpawnRetries  = 2

	// The CEO is respawned on death because the office exists to serve it.
	// These bound that loop: a CEO that dies within CEORestartWindow of
	// starting counts as a failed start, and after MaxCEORestarts
	// consecutive failures omo stops respawning and tells the user instead
	// of spinning at full speed.
	MaxCEORestarts    = 3
	CEORestartWindow  = 30 * time.Second
	CEORestartBackoff = 500 * time.Millisecond
)

func (s *Supervisor) readyTimeout() time.Duration {
	if ReadyTimeout != 120*time.Second {
		return ReadyTimeout
	}
	if s.Cfg.Agents.ReadyTimeout == 0 {
		return 120 * time.Second
	}
	return time.Duration(s.Cfg.Agents.ReadyTimeout)
}

func (s *Supervisor) startPromptDelay() time.Duration {
	if StartPromptDelay != 2*time.Second {
		return StartPromptDelay
	}
	return time.Duration(s.Cfg.Agents.StartPromptDelay)
}

func (s *Supervisor) maxSpawnRetries() int {
	if MaxSpawnRetries != 2 || s.Cfg.Agents.MaxSpawnRetries == 0 {
		return MaxSpawnRetries
	}
	return s.Cfg.Agents.MaxSpawnRetries
}

func (s *Supervisor) maxJobRetries() int {
	if s.Cfg.Agents.MaxJobRetries == 0 {
		return 3
	}
	return s.Cfg.Agents.MaxJobRetries
}

func (s *Supervisor) maxCEORestarts() int {
	if MaxCEORestarts != 3 || s.Cfg.CEO.MaxRestarts == 0 {
		return MaxCEORestarts
	}
	return s.Cfg.CEO.MaxRestarts
}

func (s *Supervisor) ceoRestartWindow() time.Duration {
	if CEORestartWindow != 30*time.Second || s.Cfg.CEO.RestartWindow == 0 {
		return CEORestartWindow
	}
	return time.Duration(s.Cfg.CEO.RestartWindow)
}

func (s *Supervisor) ceoRestartBackoff() time.Duration {
	if CEORestartBackoff != 500*time.Millisecond {
		return CEORestartBackoff
	}
	return time.Duration(s.Cfg.CEO.RestartBackoff)
}

type Supervisor struct {
	Cfg           *config.Config
	DB            *sql.DB
	Jobs          *queue.Store
	Mail          *bus.Store
	Git           *gitops.Git
	Msgs          *messages.Set
	OfficeDir     string
	SocketPath    string
	SocketDisplay string

	// OnSpawnFailed is called (if set) after a spawn exhausts its retries.
	OnSpawnFailed func(role string, jobID int64)

	mu                sync.Mutex
	nameMu            sync.Mutex
	statisticsMu      sync.Mutex
	roleModelMu       sync.Mutex
	roleModelNext     map[string]int
	sessions          map[string]*session.Session
	waiters           map[string]chan struct{}
	firefighterPaused bool
	ceoSpawnHalted    bool
	kick              chan struct{} // wakes the dispatch loop (Task 14)

	// Smoke-alarm delta tracking: everything newer than these ids goes into
	// the next round's report.
	lastSmokeEventID int64
	lastSmokeMsgID   int64
	smokeHistory     map[string][]smokeSnapshot
	smokeRaised      map[string]bool

	// CEO restart accounting (see MaxCEORestarts).
	ceoFailures  int
	ceoSpawnedAt time.Time

	lastUserInput       map[string]time.Time
	pendingNudge        map[string]bool
	lastNudge           map[string]time.Time
	interactiveAgent    string
	interactiveWritable bool
	sessionStarted      time.Time
	sessionEventID      int64
	ceoActivityName     string
	ceoActivityLast     time.Time
	ceoActivityLog      logSignature
	ceoActivityActive   time.Duration
	ceoActivityIdle     time.Duration
	ceoStatsActive      time.Duration
	ceoStatsIdle        time.Duration
}

func New(cfg *config.Config, d *sql.DB, git *gitops.Git, officeDir string, msgs *messages.Set) *Supervisor {
	defaults := config.Defaults()
	if cfg.Agents == (config.Agents{}) {
		cfg.Agents = defaults.Agents
	}
	if cfg.CEO == (config.CEO{}) {
		cfg.CEO = defaults.CEO
	}
	if cfg.SmokeAlarm.Interval == 0 {
		cfg.SmokeAlarm = defaults.SmokeAlarm
	} else if cfg.SmokeAlarm.Timeout == 0 {
		cfg.SmokeAlarm.Timeout = defaults.SmokeAlarm.Timeout
	}
	if cfg.Reviews.EscalateAfter < 1 {
		cfg.Reviews.EscalateAfter = 2
	}
	if cfg.Notifications.RepeatInterval == 0 {
		cfg.Notifications.RepeatInterval = config.Duration(3 * time.Minute)
	}
	if cfg.Notifications.InputDebounce == 0 {
		cfg.Notifications.InputDebounce = config.Duration(30 * time.Second)
	}
	s := &Supervisor{
		Cfg:            cfg,
		DB:             d,
		Jobs:           &queue.Store{DB: d},
		Git:            git,
		Msgs:           msgs,
		OfficeDir:      officeDir,
		SocketPath:     filepath.Join(officeDir, ".omo", "omo.sock"),
		sessions:       map[string]*session.Session{},
		waiters:        map[string]chan struct{}{},
		roleModelNext:  map[string]int{},
		kick:           make(chan struct{}, 1),
		lastUserInput:  map[string]time.Time{},
		pendingNudge:   map[string]bool{},
		lastNudge:      map[string]time.Time{},
		smokeHistory:   map[string][]smokeSnapshot{},
		smokeRaised:    map[string]bool{},
		sessionStarted: time.Now(),
	}
	s.Mail = &bus.Store{DB: d, Dir: bus.DBDirectory{DB: d}, Notify: s.DeliverNudge}
	return s
}

// roleProfile selects the next configured default profile for a role.
// Explicit per-job model overrides bypass this function.
func (s *Supervisor) roleProfile(role string, retries int) (string, error) {
	configured, ok := s.Cfg.Roles[role]
	if !ok || len(configured.Models) == 0 {
		return "", fmt.Errorf("role %q has no configured profiles", role)
	}
	var index int
	switch configured.Assignment {
	case config.AssignmentRoundRobin:
		s.roleModelMu.Lock()
		index = s.roleModelNext[role] % len(configured.Models)
		s.roleModelNext[role]++
		s.roleModelMu.Unlock()
	case config.AssignmentRandom:
		index = rand.IntN(len(configured.Models))
	case config.AssignmentFailover:
		index = min(max(retries, 0), len(configured.Models)-1)
	default:
		return "", fmt.Errorf("role %q has unknown assignment %q", role, configured.Assignment)
	}
	return configured.Models[index], nil
}

func (s *Supervisor) spawnRole(role string, jobID int64, dir, goal string, retries int) (string, error) {
	profile, err := s.roleProfile(role, retries)
	if err != nil {
		return "", err
	}
	if !s.spawnAllowed(role) {
		return "", ErrSpawningHalted
	}
	return s.spawnAttempt(role, profile, jobID, dir, goal, 0, true)
}

// SpawnConfiguredRole selects a role's configured profile and starts it.
func (s *Supervisor) SpawnConfiguredRole(role string, jobID int64, dir, goal string, retries int) (string, error) {
	return s.spawnRole(role, jobID, dir, goal, retries)
}

// Auth is the socket AuthFunc: only living agents may speak; ready only
// while spawning.
func (s *Supervisor) Auth(agentID, verb string) error {
	a, err := db.GetAgent(s.DB, agentID)
	if err != nil {
		return fmt.Errorf("unknown OMO_AGENT_ID %q", agentID)
	}
	switch a.State {
	case "done", "dead":
		return fmt.Errorf("agent %q is no longer active", agentID)
	}
	if verb == "ready" && a.State != "spawning" {
		return fmt.Errorf("ready already called")
	}
	return nil
}

func (s *Supervisor) Session(name string) (*session.Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[name]
	return sess, ok
}

// DeliverNudge is the bus Notify hook: wake waiting recipients, type the
// nudge into running ones. "user" is surfaced by the TUI, not nudged.
func (s *Supervisor) DeliverNudge(recipients []string) {
	for _, r := range recipients {
		if r == "user" {
			continue
		}
		s.mu.Lock()
		ch, waiting := s.waiters[r]
		_, hasSession := s.sessions[r]
		s.mu.Unlock()
		if waiting {
			select {
			case ch <- struct{}{}:
			default:
			}
			continue
		}
		if hasSession {
			s.mu.Lock()
			s.pendingNudge[r] = true
			s.mu.Unlock()
			go s.flushNudge(r)
		}
	}
}

// RecordUserInput prevents an automated mail prompt from being inserted into
// text the user is composing in an agent CLI.
func (s *Supervisor) RecordUserInput(agent string) {
	s.mu.Lock()
	s.lastUserInput[agent] = time.Now()
	s.mu.Unlock()
}

// SetInteraction tells the nudge scheduler which agent the user can currently
// type into. Moving to overview/read-only releases a pending notification.
func (s *Supervisor) SetInteraction(agent string, writable bool) {
	s.mu.Lock()
	previous := s.interactiveAgent
	s.interactiveAgent, s.interactiveWritable = agent, writable
	s.mu.Unlock()
	if previous != "" {
		go s.flushNudge(previous)
	}
	if agent != "" && !writable {
		go s.flushNudge(agent)
	}
}

func (s *Supervisor) NudgePending(agent string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pendingNudge[agent]
}

func (s *Supervisor) flushNudge(agent string) {
	s.mu.Lock()
	if !s.pendingNudge[agent] {
		s.mu.Unlock()
		return
	}
	if s.interactiveWritable && s.interactiveAgent == agent {
		debounce := time.Duration(s.Cfg.Notifications.InputDebounce)
		if debounce > 0 && time.Since(s.lastUserInput[agent]) < debounce {
			s.mu.Unlock()
			return
		}
	}
	sess := s.sessions[agent]
	if sess == nil {
		s.mu.Unlock()
		return
	}
	s.pendingNudge[agent] = false
	s.lastNudge[agent] = time.Now()
	s.mu.Unlock()
	_ = sess.SendPrompt(s.Msgs.MailNudge())
}

// NudgeLoop repeats reminders while mail remains unread and also flushes a
// prompt as soon as the typing debounce expires.
func (s *Supervisor) NudgeLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		agents, _ := db.LivingAgents(s.DB)
		for _, a := range agents {
			n, _ := s.Mail.UnreadCount(a.Name)
			s.mu.Lock()
			last := s.lastNudge[a.Name]
			if n == 0 {
				delete(s.pendingNudge, a.Name)
				s.mu.Unlock()
				continue
			}
			repeat := time.Duration(s.Cfg.Notifications.RepeatInterval)
			if last.IsZero() || (repeat > 0 && time.Since(last) >= repeat) {
				s.pendingNudge[a.Name] = true
			}
			s.mu.Unlock()
			s.flushNudge(a.Name)
		}
	}
}

// WakeAgent releases an agent parked in `omo wait`.
func (s *Supervisor) WakeAgent(name string) {
	s.mu.Lock()
	ch, ok := s.waiters[name]
	s.mu.Unlock()
	if ok {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// KillAgent terminates a session. markDead updates the agent row (pass
// false when the caller wants the death handler to requeue the job).
func (s *Supervisor) KillAgent(name string, markDead bool) error {
	if markDead {
		if err := db.SetAgentState(s.DB, name, "dead"); err != nil {
			return err
		}
	}
	s.mu.Lock()
	sess, ok := s.sessions[name]
	s.mu.Unlock()
	if ok {
		return sess.Kill()
	}
	return nil
}

// KillAll terminates every live session (office shutdown).
func (s *Supervisor) KillAll() {
	s.mu.Lock()
	sessions := make([]*session.Session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		sessions = append(sessions, sess)
	}
	s.mu.Unlock()
	_ = db.MarkAllAgentsDead(s.DB)
	for _, sess := range sessions {
		// Make every shutdown exit expected, so exit watchers do not requeue
		// jobs or respawn the CEO while the office is closing.
		_ = sess.Kill()
	}
	deadline := time.After(5 * time.Second)
	for _, sess := range sessions {
		select {
		case <-sess.Done():
		case <-deadline:
			return
		}
	}
}

func (s *Supervisor) kickDispatch() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}
