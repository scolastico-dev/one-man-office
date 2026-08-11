package supervisor

import (
	"encoding/json"
	"fmt"

	"github.com/scolastico-dev/one-man-office/internal/bus"
	"github.com/scolastico-dev/one-man-office/internal/db"
	"github.com/scolastico-dev/one-man-office/internal/proto"
	"github.com/scolastico-dev/one-man-office/internal/queue"
	"github.com/scolastico-dev/one-man-office/internal/sockd"
)

func (s *Supervisor) gateFirefighter(agentID string) (*db.Agent, error) {
	a, err := db.GetAgent(s.DB, agentID)
	if err != nil {
		return nil, err
	}
	if a.Role != "firefighter" {
		return nil, fmt.Errorf("only the firefighter may do this")
	}
	return a, nil
}

func (s *Supervisor) registerFireVerbs(srv *sockd.Server) {
	srv.Handle("office.pause", func(agentID string, _ json.RawMessage) (any, error) {
		if _, err := s.gateFirefighter(agentID); err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.firefighterPaused = true
		s.mu.Unlock()
		db.AppendEvent(s.DB, "office_paused", agentID, 0, "")
		return nil, nil
	})
	srv.Handle("office.resume", func(agentID string, _ json.RawMessage) (any, error) {
		if _, err := s.gateFirefighter(agentID); err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.firefighterPaused = false
		s.mu.Unlock()
		db.AppendEvent(s.DB, "office_resumed", agentID, 0, "")
		s.kickDispatch()
		go s.resumePendingReviews()
		return nil, nil
	})
	srv.Handle("office.halt-spawns", func(agentID string, _ json.RawMessage) (any, error) {
		caller, err := db.GetAgent(s.DB, agentID)
		if err != nil {
			return nil, err
		}
		if caller.Role != "ceo" {
			return nil, fmt.Errorf("only the CEO may halt new agent spawning")
		}
		s.mu.Lock()
		s.ceoSpawnHalted = true
		s.mu.Unlock()
		db.AppendEvent(s.DB, "spawning_halted", agentID, 0, "by CEO")
		return nil, nil
	})
	srv.Handle("office.resume-spawns", func(agentID string, _ json.RawMessage) (any, error) {
		caller, err := db.GetAgent(s.DB, agentID)
		if err != nil {
			return nil, err
		}
		if caller.Role != "ceo" {
			return nil, fmt.Errorf("only the CEO may resume agent spawning")
		}
		s.mu.Lock()
		s.ceoSpawnHalted = false
		s.mu.Unlock()
		db.AppendEvent(s.DB, "spawning_resumed", agentID, 0, "by CEO")
		s.kickDispatch()
		go s.resumePendingReviews()
		return nil, nil
	})

	kill := func(agentID string, args json.RawMessage) (any, error) {
		caller, err := s.gateFirefighter(agentID)
		if err != nil {
			return nil, err
		}
		var a proto.AgentNameArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, err
		}
		var victims []string
		if agents, _ := db.LivingByRole(s.DB, a.Name); len(agents) > 0 {
			for _, v := range agents {
				victims = append(victims, v.Name)
			}
		} else if _, err := db.GetAgent(s.DB, a.Name); err == nil {
			victims = []string{a.Name}
		} else {
			return nil, fmt.Errorf("no agent or role %q", a.Name)
		}
		killed := 0
		for _, v := range victims {
			if v == caller.Name {
				continue // never self-terminate via kill
			}
			db.AppendEvent(s.DB, "agent_killed", v, 0, "by "+caller.Name)
			// markDead=false → watchExit's death path requeues the job.
			s.KillAgent(v, false)
			s.WakeAgent(v) // release a parked wait so the process can die
			killed++
		}
		return map[string]int{"killed": killed}, nil
	}
	srv.Handle("agent.kill", kill)
	srv.Handle("agent.restart", kill) // requeue + dispatch IS the restart

	srv.Handle("job.cancel", func(agentID string, args json.RawMessage) (any, error) {
		if _, err := s.gateFirefighter(agentID); err != nil {
			return nil, err
		}
		var a proto.JobIDArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, err
		}
		j, err := s.Jobs.Get(a.ID)
		if err != nil {
			return nil, err
		}
		if err := s.Jobs.Transition(a.ID, queue.StateCancelled); err != nil {
			return nil, err
		}
		if j.Assignee != "" {
			s.KillAgent(j.Assignee, true) // marked dead: no requeue
		}
		return nil, nil
	})
	srv.Handle("job.requeue", func(agentID string, args json.RawMessage) (any, error) {
		if _, err := s.gateFirefighter(agentID); err != nil {
			return nil, err
		}
		var a proto.JobIDArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, err
		}
		if err := s.Jobs.Retry(a.ID); err != nil {
			return nil, err
		}
		s.kickDispatch()
		return nil, nil
	})
	srv.Handle("incident.resolve", func(agentID string, args json.RawMessage) (any, error) {
		caller, err := s.gateFirefighter(agentID)
		if err != nil {
			return nil, err
		}
		var a proto.IncidentResolveArgs
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, err
		}
		if a.Report == "" {
			return nil, fmt.Errorf("--report is required")
		}
		res, err := s.DB.Exec(
			`UPDATE incidents SET state = 'resolved', resolved_at = datetime('now') WHERE id = ? AND state = 'open'`, a.ID)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil, fmt.Errorf("incident %d not open", a.ID)
		}
		db.AppendEvent(s.DB, "incident_resolved", caller.Name, 0, fmt.Sprintf("#%d", a.ID))
		if _, err := s.Mail.Send(caller.Name, "user",
			fmt.Sprintf("incident %d resolved", a.ID), a.Report, bus.PrioHigh); err != nil {
			return nil, err
		}
		return nil, nil
	})
}

// nextOpenIncident spawns a firefighter for the oldest open incident, if any.
func (s *Supervisor) nextOpenIncident() {
	var id int64
	if err := s.DB.QueryRow(
		`SELECT id FROM incidents WHERE state = 'open' ORDER BY id LIMIT 1`).Scan(&id); err != nil {
		return
	}
	if n, _ := db.CountLivingByRole(s.DB, "firefighter"); n > 0 {
		return
	}
	s.spawnFirefighter(id)
}
