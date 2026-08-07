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

// SmokeLoop spawns a fresh smoke alarm every interval (never two at once).
func (s *Supervisor) SmokeLoop(ctx context.Context) {
	t := time.NewTicker(time.Duration(s.Cfg.SmokeAlarm.Interval))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if n, _ := db.CountLivingByRole(s.DB, "smokealarm"); n > 0 {
			continue // previous round still running
		}
		goal := s.Msgs.SmokeAlarmGoal(s.smokeReport())
		s.Spawn("smokealarm", s.Cfg.Roles["smokealarm"], 0, s.OfficeDir, goal)
	}
}

// smokeReport assembles the round input: per-agent status + log tail, the
// event delta since the last round, and PM lateral-chatter volume.
func (s *Supervisor) smokeReport() string {
	var b strings.Builder
	agents, _ := db.LivingAgents(s.DB)
	for _, a := range agents {
		if a.Role == "smokealarm" || a.Role == "firefighter" {
			continue
		}
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
		if sess, ok := s.Session(a.Name); ok {
			lines, err := sess.TailLog(s.Cfg.SmokeAlarm.TailLines)
			if err == nil {
				fmt.Fprintf(&b, "RECENT OUTPUT (last %d lines):\n%s\n", len(lines), strings.Join(lines, "\n"))
			}
		}
		b.WriteString("\n")
	}
	s.mu.Lock()
	lastEvent, lastMsg := s.lastSmokeEventID, s.lastSmokeMsgID
	s.mu.Unlock()
	events, _ := db.EventsSince(s.DB, lastEvent)
	b.WriteString("== EVENTS SINCE LAST ROUND\n")
	maxEventID := lastEvent
	for _, e := range events {
		fmt.Fprintf(&b, "%s %s agent=%s job=%d %s\n", e.CreatedAt, e.Kind, e.Agent, e.JobID, e.Detail)
		if e.ID > maxEventID {
			maxEventID = e.ID
		}
	}
	var chatter int
	var maxMsgID int64
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
		res, err := s.DB.Exec(
			`INSERT INTO incidents (agent, class, detail) VALUES (?,?,?)`, a.Agent, a.Class, a.Detail)
		if err != nil {
			return nil, err
		}
		id, _ := res.LastInsertId()
		db.AppendEvent(s.DB, "incident", a.Agent, 0, fmt.Sprintf("#%d %s: %s", id, a.Class, a.Detail))
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
