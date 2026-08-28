package supervisor

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/scolastico-dev/one-man-office/internal/db"
)

// StatusLoop periodically derives current-step text from readable session
// transcripts when status.mode is "log". Reloaded configuration is observed
// before every round.
func (s *Supervisor) StatusLoop(ctx context.Context) {
	for {
		cfg := s.Config()
		if cfg.Status.Mode == "log" {
			s.deriveStatuses()
		}
		interval := time.Duration(cfg.Status.Interval)
		if interval <= 0 {
			interval = time.Minute
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (s *Supervisor) deriveStatuses() {
	agents, err := db.LivingAgents(s.DB)
	if err != nil {
		return
	}
	for _, agent := range agents {
		if agent.State != "working" {
			continue
		}
		sess, ok := s.Session(agent.Name)
		if !ok {
			continue
		}
		lines, err := sess.TailLog(20)
		if err != nil {
			continue
		}
		step := derivedStatus(lines)
		if step == "" {
			continue
		}
		if err := db.SetAgentStep(s.DB, agent.Name, step); err != nil {
			continue
		}
		if step != agent.Step {
			_ = db.AppendEvent(s.DB, "agent_step_derived", agent.Name, agent.JobID, step)
		}
	}
}

func derivedStatus(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.Join(strings.Fields(lines[i]), " ")
		if line == "" {
			continue
		}
		if utf8.RuneCountInString(line) <= 500 {
			return line
		}
		runes := []rune(line)
		return string(runes[:499]) + "…"
	}
	return ""
}
