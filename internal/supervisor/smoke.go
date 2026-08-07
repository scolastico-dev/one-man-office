package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/messages"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/sockd"
)

type smokeSnapshot struct {
	At    time.Time
	Lines []string
}

// SmokeLoop schedules fresh smoke alarms. A firefighter or unresolved
// incident suspends inspections, while CEO/firefighter work-spawn halts do not.
func (s *Supervisor) SmokeLoop(ctx context.Context) {
	if !s.Cfg.SmokeAlarm.Enabled {
		return
	}
	t := time.NewTicker(time.Duration(s.Cfg.SmokeAlarm.Interval))
	defer t.Stop()
	if s.Cfg.SmokeAlarm.RunOnStart {
		s.runSmokeRound()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.runSmokeRound()
	}
}

func (s *Supervisor) runSmokeRound() {
	if n, _ := db.CountLivingByRole(s.DB, "smokealarm"); n > 0 {
		return
	}
	if n, _ := db.CountLivingByRole(s.DB, "firefighter"); n > 0 || s.hasOpenIncident() {
		return
	}
	reports := s.smokeReports()
	for _, report := range reports {
		goal := s.Msgs.SmokeAlarmGoal(report)
		_, _ = s.Spawn("smokealarm", s.Cfg.Roles["smokealarm"], 0, s.OfficeDir, goal)
	}
}

func (s *Supervisor) hasOpenIncident() bool {
	var n int
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM incidents WHERE state = 'open'`).Scan(&n)
	return n > 0
}

// smokeReport remains the combined-report entry point used by tests and
// diagnostics. The scheduler uses smokeReports to honor the configured mode.
func (s *Supervisor) smokeReport() string {
	reports := s.smokeReportsForMode("all")
	if len(reports) == 0 {
		return "== NO LIVING WORK AGENTS\n"
	}
	return reports[0]
}

func (s *Supervisor) smokeReports() []string {
	return s.smokeReportsForMode(s.Cfg.SmokeAlarm.Mode)
}

func (s *Supervisor) smokeReportsForMode(mode string) []string {
	agents, _ := db.LivingAgents(s.DB)
	var blocks []string
	for _, a := range agents {
		if a.Role == "smokealarm" || a.Role == "firefighter" {
			continue
		}
		blocks = append(blocks, s.smokeAgentBlock(&a))
	}
	if len(blocks) == 0 {
		return nil
	}
	shared := s.smokeSharedContext()
	if mode == "per_agent" {
		reports := make([]string, 0, len(blocks))
		for _, block := range blocks {
			reports = append(reports, block+shared)
		}
		return reports
	}
	return []string{strings.Join(blocks, "") + shared}
}

func (s *Supervisor) smokeAgentBlock(a *db.Agent) string {
	var b strings.Builder
	goal := a.Goal
	if a.JobID != 0 {
		if j, err := s.Jobs.Get(a.JobID); err == nil {
			goal = fmt.Sprintf("job #%d %q: %s", j.ID, j.Title, j.Goal)
		}
	}
	if len(goal) > 200 {
		goal = goal[:200] + "…"
	}
	fmt.Fprintf(&b, "== AGENT %s (role=%s, state=%s)\nGOAL: %s\n", a.Name, a.Role, a.State, goal)
	var current []string
	if sess, ok := s.Session(a.Name); ok {
		lines, err := sess.TailLog(s.Cfg.SmokeAlarm.TailLines)
		if err == nil {
			current = append([]string(nil), lines...)
		}
	}
	fmt.Fprintf(&b, "CURRENT OUTPUT (last %d lines):\n%s\n", len(current), strings.Join(current, "\n"))
	s.mu.Lock()
	history := append([]smokeSnapshot(nil), s.smokeHistory[a.Name]...)
	keep := s.Cfg.SmokeAlarm.HistoryRuns
	if keep > 0 {
		s.smokeHistory[a.Name] = append(history, smokeSnapshot{At: time.Now(), Lines: current})
		if len(s.smokeHistory[a.Name]) > keep {
			s.smokeHistory[a.Name] = s.smokeHistory[a.Name][len(s.smokeHistory[a.Name])-keep:]
		}
	} else {
		delete(s.smokeHistory, a.Name)
	}
	s.mu.Unlock()
	if keep > len(history) {
		keep = len(history)
	}
	for ago := 1; ago <= keep; ago++ {
		snapshot := history[len(history)-ago]
		fmt.Fprintf(&b, "OUTPUT FROM %d SMOKE RUN(S) AGO (%s):\n%s\n", ago, snapshot.At.Format(time.RFC3339), strings.Join(snapshot.Lines, "\n"))
	}
	b.WriteString("\n")
	return b.String()
}

func (s *Supervisor) smokeSharedContext() string {
	var b strings.Builder
	s.mu.Lock()
	lastEvent, lastMsg := s.lastSmokeEventID, s.lastSmokeMsgID
	s.mu.Unlock()
	maxEventID := lastEvent
	if s.Cfg.SmokeAlarm.IncludeEvents {
		events, _ := db.EventsSince(s.DB, lastEvent)
		b.WriteString("== EVENTS SINCE LAST ROUND\n")
		for _, e := range events {
			fmt.Fprintf(&b, "%s %s agent=%s job=%d %s\n", e.CreatedAt, e.Kind, e.Agent, e.JobID, e.Detail)
			if e.ID > maxEventID {
				maxEventID = e.ID
			}
		}
	}
	var chatter int
	maxMsgID := lastMsg
	if s.Cfg.SmokeAlarm.IncludePMChatter {
		s.DB.QueryRow(
			`SELECT COUNT(*), COALESCE(MAX(m.id), ?) FROM messages m
		 JOIN agents a1 ON a1.name = m.from_agent
		 JOIN agents a2 ON a2.name = m.to_target
		 WHERE a1.role = 'product_manager' AND a2.role = 'product_manager' AND m.id > ?`,
			lastMsg, lastMsg).Scan(&chatter, &maxMsgID)
		fmt.Fprintf(&b, "\n== PM lateral messages since last round: %d\n", chatter)
		if chatter > 0 {
			b.WriteString("Unusual lateral chatter — inspect the involved PMs closely.\n")
		}
	}
	s.mu.Lock()
	s.lastSmokeEventID, s.lastSmokeMsgID = maxEventID, maxMsgID
	s.mu.Unlock()
	return b.String()
}

func (s *Supervisor) registerIncidentCreate(srv *sockd.Server) {
	srv.Handle("incident.create", func(agentID string, args json.RawMessage) (any, error) {
		caller, err := db.GetAgent(s.DB, agentID)
		if err != nil {
			return nil, err
		}
		if caller.Role != "smokealarm" && caller.Role != "firefighter" {
			return nil, fmt.Errorf("role %q may not file incidents", caller.Role)
		}
		var a proto.IncidentCreateArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, err
		}
		if a.Agent == "" || a.Class == "" {
			return nil, fmt.Errorf("agent and class are required")
		}
		if caller.Role == "smokealarm" {
			s.mu.Lock()
			alreadyRaised := s.smokeRaised[caller.Name]
			s.mu.Unlock()
			if alreadyRaised {
				return nil, fmt.Errorf("a smoke alarm may raise only one incident per run")
			}
		}
		tx, err := s.DB.Begin()
		if err != nil {
			return nil, err
		}
		defer tx.Rollback()
		if caller.Role == "smokealarm" {
			var open int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM incidents WHERE state = 'open'`).Scan(&open); err != nil {
				return nil, err
			}
			if open > 0 {
				return nil, fmt.Errorf("another smoke incident is already open; wait for its firefighter")
			}
		}
		res, err := tx.Exec(
			`INSERT INTO incidents (agent, class, detail) VALUES (?,?,?)`, a.Agent, a.Class, a.Detail)
		if err != nil {
			return nil, err
		}
		id, _ := res.LastInsertId()
		if err := db.AppendEvent(tx, "incident", a.Agent, 0, fmt.Sprintf("#%d %s: %s", id, a.Class, a.Detail)); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		if caller.Role == "smokealarm" {
			s.mu.Lock()
			s.smokeRaised[caller.Name] = true
			s.mu.Unlock()
		}
		s.onIncident(id)
		return proto.IncidentCreateResponse{ID: id}, nil
	})
}

// onIncident spawns a firefighter unless one is already active (then the
// open incident is picked up when the current firefighter finishes).
func (s *Supervisor) onIncident(id int64) {
	if n, _ := db.CountLivingByRole(s.DB, "firefighter"); n > 0 {
		return
	}
	s.spawnFirefighter(id)
}

func (s *Supervisor) spawnFirefighter(id int64) {
	var agent, class, detail string
	if err := s.DB.QueryRow(
		`SELECT agent, class, detail FROM incidents WHERE id = ? AND state = 'open'`, id).
		Scan(&agent, &class, &detail); err != nil {
		return
	}
	var b strings.Builder
	agents, _ := db.LivingAgents(s.DB)
	for _, a := range agents {
		fmt.Fprintf(&b, "agent %s role=%s state=%s job=%d\n", a.Name, a.Role, a.State, a.JobID)
	}
	jobs, _ := s.Jobs.List()
	for _, j := range jobs {
		fmt.Fprintf(&b, "job #%d [%s] %s assignee=%s\n", j.ID, j.State, j.Title, j.Assignee)
	}
	goal := s.Msgs.FirefighterGoal(messages.IncidentData{
		ID: id, Agent: agent, Class: class, Detail: detail, Snapshot: b.String(),
	})
	s.Spawn("firefighter", s.Cfg.Roles["firefighter"], 0, s.OfficeDir, goal)
}
